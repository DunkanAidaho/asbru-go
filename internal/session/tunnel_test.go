package session

// M30/M32 backlog: SOCKS/jump туннель для RDP/VNC. Ядро: порт, re-point, команда
// Jump (ssh -L с контрол-сокетом, -p/-i), Go-нативный SOCKS5-форвардер,
// инжектируемый запуск jump-процесса. Жизненный цикл — здесь.

import (
	"context"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Finding #5: jump-туннель должен дождаться готовности (локальный порт или
// control-сокет), а не сообщать успех сразу после Start (легаси/ревью).
func TestWaitTunnelReadyPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := waitTunnelReady("", port, time.Time{}, nil, time.Second); err != nil {
		t.Fatalf("готовый порт: %v", err)
	}
	_ = ln.Close()
	if err := waitTunnelReady("", port, time.Time{}, nil, 120*time.Millisecond); err == nil {
		t.Fatal("закрытый порт: ожидалась ошибка готовности")
	}
}

func TestWaitTunnelReadyControlSocket(t *testing.T) {
	ctl := filepath.Join(t.TempDir(), "x.ctl")
	ln, err := net.Listen("unix", ctl)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if err := waitTunnelReady(ctl, 0, time.Time{}, nil, time.Second); err != nil {
		t.Fatalf("control-сокет: %v", err)
	}
}

// ag-a20: готовность по control-сокету — только если сокет появился/обновлён
// после старта ssh. Сокет от предыдущей попытки не даёт ложной готовности.
func TestTunnelReadyRequiresFreshControlSocket(t *testing.T) {
	ctl := filepath.Join(t.TempDir(), "old.ctl")
	ln, err := net.Listen("unix", ctl)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if tunnelReady(ctl, 0, time.Now().Add(time.Second)) {
		t.Fatal("старый control-сокет принят за готовность")
	}
	if !tunnelReady(ctl, 0, time.Now().Add(-time.Minute)) {
		t.Fatal("свежий control-сокет не признан готовым")
	}
	if !tunnelReady(ctl, 0, time.Time{}) {
		t.Fatal("нулевой notBefore должен отключать проверку свежести")
	}
}

// ag-a20: на пути ошибки готовности обязан вызываться teardown master
// (ssh -S ctl -O exit) — Process.Kill при -f no-op, демон иначе переживает.
func TestTunnelExecTearsDownMasterOnTimeout(t *testing.T) {
	oldExec, oldTimeout, oldProbe, oldExit := execCommand, tunnelReadyTimeout, tunnelProbeInterval, exitCtlMaster
	execCommand = func(string, ...string) *exec.Cmd { return exec.Command("sh", "-c", "exit 0") }
	tunnelReadyTimeout = 150 * time.Millisecond
	tunnelProbeInterval = 5 * time.Millisecond
	calls := make(chan string, 4)
	exitCtlMaster = func(ctl, host string) { calls <- ctl + "|" + host }
	defer func() {
		execCommand, tunnelReadyTimeout, tunnelProbeInterval, exitCtlMaster = oldExec, oldTimeout, oldProbe, oldExit
	}()

	ctl := filepath.Join(t.TempDir(), "t.ctl")
	if _, err := tunnelExec([]string{"ssh", "-L", "1:127.0.0.1:1", "user@jump"}, ctl); err == nil {
		t.Fatal("ожидался таймаут готовности")
	}
	select {
	case got := <-calls:
		if got != ctl+"|user@jump" {
			t.Fatalf("teardown = %q, want %q", got, ctl+"|user@jump")
		}
	case <-time.After(time.Second):
		t.Fatal("на ошибке готовности не выполнен ssh -O exit")
	}
}

// Finding #5: дочерний ssh должен репиться (Wait), а провал родителя (-f) —
// возвращаться как ошибка, а не тихий «успех». Раньше Start без Wait оставлял
// зомби и рапортовал успех без готовности.
func TestTunnelExecReapsAndReportsChildFailure(t *testing.T) {
	oldExec, oldTimeout, oldProbe := execCommand, tunnelReadyTimeout, tunnelProbeInterval
	execCommand = func(string, ...string) *exec.Cmd { return exec.Command("sh", "-c", "exit 3") }
	tunnelReadyTimeout = 200 * time.Millisecond
	tunnelProbeInterval = 5 * time.Millisecond
	defer func() { execCommand, tunnelReadyTimeout, tunnelProbeInterval = oldExec, oldTimeout, oldProbe }()

	ctl := filepath.Join(t.TempDir(), "absent.ctl") // готовности не будет
	_, err := tunnelExec([]string{"ssh", "-L", "1:127.0.0.1:1", "host"}, ctl)
	if err == nil {
		t.Fatal("провал ssh не вернул ошибку (нет Wait/readiness)")
	}
	if !strings.Contains(err.Error(), "exit status 3") {
		t.Fatalf("ошибка = %v, хотим статус потомка (Wait не вызван?)", err)
	}
}

// ag-a20: control-сокет, оставшийся от прошлой неудачной попытки, НЕ является
// признаком готовности нового туннеля (ssh ещё не аутентифицировался), а на
// пути ошибки он вместе с ssh-демоном должен быть убран (иначе следующая
// попытка видит чужой master и проброс порта утекает).
func TestTunnelExecStaleCtlNotReadyAndCleaned(t *testing.T) {
	oldExec, oldTimeout, oldProbe, oldExit := execCommand, tunnelReadyTimeout, tunnelProbeInterval, exitCtlMaster
	// ssh -f: прямой ребёнок уже вышел, демон остался бы — Kill на нём no-op.
	execCommand = func(string, ...string) *exec.Cmd { return exec.Command("sh", "-c", "exit 0") }
	tunnelReadyTimeout = 150 * time.Millisecond
	tunnelProbeInterval = 5 * time.Millisecond
	// Стаб master'а: сокет в тесте — Go-listener, реальный ssh -O exit на нём
	// повис бы в ожидании mux-ответа.
	exitCtlMaster = func(string, string) {}
	defer func() {
		execCommand, tunnelReadyTimeout, tunnelProbeInterval, exitCtlMaster = oldExec, oldTimeout, oldProbe, oldExit
	}()

	ctl := filepath.Join(t.TempDir(), "stale.ctl")
	// «живой» сокет прошлой попытки: dial в removeStaleCtl проходит (backlog),
	// поэтому как мёртвый он не удаляется — именно этот случай и был ложной
	// готовностью.
	ln, err := net.Listen("unix", ctl)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	if _, err := tunnelExec([]string{"ssh", "-L", "1:127.0.0.1:1", "user@jump"}, ctl); err == nil {
		t.Fatal("старый control-сокет дал ложную готовность (ожидался таймаут)")
	}
	if _, err := os.Stat(ctl); !os.IsNotExist(err) {
		t.Fatalf("control-сокет не удалён после таймаута: %v", err)
	}
}

// Finding #6: Kill должен закрывать не только принятые клиентские, но и
// установленные UPSTREAM-соединения (иначе relay/соединения текут). Проверяем
// по стороне прокси: после Kill её conn получает EOF.
func TestSocksKillClosesUpstream(t *testing.T) {
	proxy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	established := make(chan struct{})
	closed := make(chan struct{})
	go func() {
		c, err := proxy.Accept()
		if err != nil {
			return
		}
		if !mockHandshake(c) {
			c.Close()
			return
		}
		close(established)
		buf := make([]byte, 1)
		_, _ = c.Read(buf) // блокируется, пока upstream не закроют
		close(closed)
		c.Close()
	}()

	tn, err := StartTunnel(StartTunnelParams{
		UseProxy: 1, LocalPort: 0,
		TargetIP: "10.0.0.1", TargetPort: "5900",
		ProxyIP: "127.0.0.1", ProxyPort: strconv.Itoa(proxy.Addr().(*net.TCPAddr).Port),
	})
	if err != nil {
		t.Fatalf("StartTunnel socks: %v", err)
	}
	client, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(tn.LocalPort)))
	if err != nil {
		t.Fatalf("dial forwarder: %v", err)
	}
	defer client.Close()
	select {
	case <-established:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream не установлен")
	}
	if err := tn.Kill(); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream-соединение не закрыто после Kill (утечка)")
	}
}

// Finding #6: ctx должен прерывать и SOCKS-рукопожатие (раньше отмена ждала
// 15-секундный deadline). Прокси принимает TCP и молчит.
func TestSocksConnectHonorsContextCancel(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan struct{})
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		close(accepted)
		time.Sleep(5 * time.Second) // не отвечаем на greeting
	}()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := socks5Connect(ctx, "127.0.0.1", strconv.Itoa(ln.Addr().(*net.TCPAddr).Port),
			"", "", "10.0.0.1", "5900")
		done <- err
	}()
	select {
	case <-accepted:
	case <-time.After(2 * time.Second):
		t.Fatal("прокси не принял соединение")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("socks5Connect вернул успех при отмене")
		}
	case <-time.After(700 * time.Millisecond):
		t.Fatal("socks5Connect не реагирует на отмену ctx (ждёт 15с deadline)")
	}
}

func TestAllocLocalPort(t *testing.T) {
	p, err := allocLocalPort()
	if err != nil {
		t.Fatal(err)
	}
	if p <= 0 || p > 65535 {
		t.Fatalf("port=%d вне диапазона", p)
	}
}

func TestRepointTarget(t *testing.T) {
	rdesk := []string{"rdesktop", "-u", "user", "10.0.0.1:3389"}
	got := repointTarget(rdesk, "RDP (rdesktop)", 5555)
	if got[len(got)-1] != "127.0.0.1:5555" {
		t.Fatalf("rdesktop last=%q want 127.0.0.1:5555", got[len(got)-1])
	}
	xf := []string{"xfreerdp", "/u:user", "/v:10.0.0.1:3389"}
	got = repointTarget(xf, "RDP (xfreerdp)", 5555)
	found := false
	for _, a := range got {
		if a == "/v:127.0.0.1:5555" {
			found = true
		}
	}
	if !found {
		t.Fatalf("xfreerdp: /v не переадресован: %v", got)
	}
	if r := repointTarget(nil, "RDP (rdesktop)", 1); len(r) != 0 {
		t.Fatalf("nil argv: %v", r)
	}
}

func TestTunnelCmdJump(t *testing.T) {
	// Jump (ssh -L) с контрол-сокетом, -p/-i (F4): ssh -S ctl -M -f -N -T
	// -L LPORT:IP:PORT -p p -i key user@jumpserver.
	cmd := tunnelCmdJump("12345", "10.0.0.1", "5900", "user", "jumpserver", "2222", "/k", "ctl")
	s := strings.Join(cmd, " ")
	for _, want := range []string{"-S", "ctl", "-M", "-L", "12345:10.0.0.1:5900", "-p", "2222", "-i", "/k", "user@jumpserver"} {
		if !contains(s, want) {
			t.Fatalf("jump cmd %q не содержит %q", s, want)
		}
	}
	if cmd[0] != "ssh" {
		t.Fatalf("jump[0]=%q want ssh", cmd[0])
	}
}

func TestTunnelCmdJumpNoCtl(t *testing.T) {
	// без ctl — базовая форма (тесты): ssh -f -N -T -L ... user@host.
	cmd := tunnelCmdJump("12345", "10.0.0.1", "5900", "user", "jmp", "", "", "")
	s := strings.Join(cmd, " ")
	if contains(s, "-S") || contains(s, "-M") {
		t.Fatalf("без ctl не должно быть -S/-M: %q", s)
	}
	if !contains(s, "-L 12345:10.0.0.1:5900") {
		t.Fatalf("нет -L: %q", s)
	}
	if !contains(s, "user@jmp") {
		t.Fatalf("нет target: %q", s)
	}
}

func TestStartTunnelErrors(t *testing.T) {
	if tn, err := StartTunnel(StartTunnelParams{UseProxy: 0}); err != nil || tn != nil {
		t.Fatalf("proxy=0: tn=%v err=%v (хотим nil,nil)", tn, err)
	}
	if _, err := StartTunnel(StartTunnelParams{UseProxy: 9}); err == nil {
		t.Fatal("use_proxy=9 должен дать ошибку")
	}
	if _, err := StartTunnel(StartTunnelParams{UseProxy: 3, TargetIP: "", TargetPort: "", JumpIP: "jmp"}); err == nil {
		t.Fatal("пустой target должен дать ошибку")
	}
	if _, err := StartTunnel(StartTunnelParams{UseProxy: 1, TargetIP: "", TargetPort: "", ProxyIP: "pr", ProxyPort: "1080"}); err == nil {
		t.Fatal("пустой target (socks) должен дать ошибку")
	}
}

func TestStartTunnelJumpDerivesPaths(t *testing.T) {
	old := tunnelExec
	defer func() { tunnelExec = old }()
	var got string
	tunnelExec = func(cmd []string, ctl string, passFile ...string) (*Tunnel, error) {
		got = strings.Join(cmd, " ")
		return &Tunnel{cancel: func() error { return nil }}, nil
	}
	// НЕ передаём CtlPath — он выводится из TmpDir+UUID.
	_, err := StartTunnel(StartTunnelParams{
		UseProxy: 3, LocalPort: 5555, TargetIP: "10.0.0.1", TargetPort: "5900",
		JumpIP: "jmp", JumpUser: "ju", TmpDir: "/tmp", UUID: "u1",
	})
	if err != nil {
		t.Fatalf("StartTunnel: %v", err)
	}
	// ag-a20: имя ctl уникально на туннель (uuid + локальный порт).
	if !strings.Contains(got, "-S /tmp/asbru_tunnel_u1_5555.ctl") {
		t.Fatalf("jump cmd не содержит ctl: %q", got)
	}
	if tunnelCtlPath("/tmp", "u1", 5555) == tunnelCtlPath("/tmp", "u1", 5556) {
		t.Fatal("ctl-путь не различает параллельные туннели одной сессии")
	}
}

// socksTestServer — минимальный SOCKS5-сервер для теста: принимает CONNECT и
// эхо-чitает/пишет. Проверяет собственную работу RFC 1928 без внешних бинарник.
type socksTestServer struct {
	ln    net.Listener
	proxy net.Conn // коннект к test-прокси (установлен на accept)
}

func (s *socksTestServer) serve() net.Conn {
	// accept клиента, прочитать рукопожатие, CONNECT, вернуть соединение.
	c, err := s.ln.Accept()
	if err != nil {
		return nil
	}
	if !mockHandshake(c) {
		c.Close()
		return nil
	}
	return c
}

func mockHandshake(c net.Conn) bool {
	// клиент шлёт greeting; ответ choose method 0x00.
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(c, hdr); err != nil {
		return false
	}
	n := int(hdr[1])
	meth := make([]byte, n)
	if _, err := io.ReadFull(c, meth); err != nil {
		return false
	}
	// выбираем 0x00 (no-auth), если есть.
	chosen := byte(0)
	for _, m := range meth {
		if m == 0 {
			chosen = 0
			break
		}
	}
	if _, err := c.Write([]byte{0x05, chosen}); err != nil {
		return false
	}
	// CONNECT request: VER CMD RSV ATYP ... → ответ REP=0.
	req := make([]byte, 4)
	if _, err := io.ReadFull(c, req); err != nil {
		return false
	}
	// прочитать ATYP-зависимую часть.
	switch req[3] {
	case 1:
		io.ReadFull(c, make([]byte, 4+2))
	case 4:
		io.ReadFull(c, make([]byte, 16+2))
	case 3:
		l := make([]byte, 1)
		io.ReadFull(c, l)
		io.ReadFull(c, make([]byte, int(l[0])+2))
	}
	// ответ REP=0 (success), BND=0.0.0.0:0
	_, _ = c.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
	return true
}

func TestSocksForwarderEndToEnd(t *testing.T) {
	// test-прокси (no-auth) + test-target (echo). Форвардер соединяет клиента
	// с прокси, прокси CONNECT-ит target, relay. Проверяем round-trip.
	target, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	targetAddr := target.Addr().(*net.TCPAddr)
	go func() {
		// эхо-сервер: читать байт, вернуть обратно.
		c, err := target.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		buf := make([]byte, 16)
		n, _ := c.Read(buf)
		_, _ = c.Write(buf[:n])
	}()

	// test-прокси (no-auth).
	proxy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	proxyAddr := proxy.Addr().(*net.TCPAddr)
	go func() {
		for {
			c, err := proxy.Accept()
			if err != nil {
				return
			}
			go func() {
				if !mockHandshake(c) {
					c.Close()
					return
				}
				// target-host: прочитали из CONNECT; здесь просто соединяемся
				// к targetAddr (захардкожено для теста).
				up, err := net.Dial("tcp", targetAddr.String())
				if err != nil {
					c.Close()
					return
				}
				defer up.Close()
				go func() { _, _ = io.Copy(up, c) }()
				_, _ = io.Copy(c, up)
			}()
		}
	}()

	// StartTunnel SOCKS (no-auth): LocalPort=0 → эфемерный.
	tn, err := StartTunnel(StartTunnelParams{
		UseProxy: 1, LocalPort: 0,
		TargetIP: "127.0.0.1", TargetPort: strconv.Itoa(targetAddr.Port),
		ProxyIP: "127.0.0.1", ProxyPort: strconv.Itoa(proxyAddr.Port),
	})
	if err != nil {
		t.Fatalf("StartTunnel socks: %v", err)
	}
	if tn == nil {
		t.Fatal("tn == nil")
	}
	defer tn.Kill()

	// клиент к локальному порту форвардера.
	client, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(tn.LocalPort)))
	if err != nil {
		t.Fatalf("dial forwarder: %v", err)
	}
	defer client.Close()
	if _, err := client.Write([]byte("hello")); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, 5)
	if _, err := io.ReadFull(client, buf); err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if string(buf) != "hello" {
		t.Fatalf("echo = %q want hello", buf)
	}
}

// R1 (security, re-review): jump-хост с ведущим "-" парсится как ОПЦИЯ ssh —
// отклоняем. Проверяем и guard в startJump, и "--" в команде.
func TestJumpRejectsLeadingDashHost(t *testing.T) {
	// guard в startJump: JumpIP начинается с "-" → ошибка (не запускаем ssh).
	_, err := StartTunnel(StartTunnelParams{
		UseProxy: 3, LocalPort: 5555, TargetIP: "10.0.0.1", TargetPort: "5900",
		JumpIP: "-oProxyCommand=evil", CtlPath: "/tmp/x.ctl",
	})
	if err == nil {
		t.Fatal("leading-dash jump host должен быть отклонён")
	}
	// "--" в команде до хоста (cmd-уровень).
	cmd := tunnelCmdJump("5555", "10.0.0.1", "5900", "", "host", "2222", "/k", "ctl")
	if cmd[len(cmd)-2] != "--" {
		t.Fatalf("нет '--' перед хостом: %v", cmd)
	}
}

// R2 (re-review): целевой хост — имя (не IP) → ATYP 0x03. Полный цикл через
// socks5Connect (no-auth прокси) с доменным target; mockHandshake принимает
// ATYP 0x03 и отвечает REP=0 — значит hostname не отвергнут.
func TestSocksConnectCmdHostnameAtTyp3(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		mockHandshake(c)
	}()
	up, err := socks5Connect(context.Background(), "127.0.0.1", strconv.Itoa(ln.Addr().(*net.TCPAddr).Port),
		"", "", "win10vm.local", "5900")
	if err != nil {
		t.Fatalf("socks5Connect с hostname (ATYP 0x03): %v", err)
	}
	up.Close()
}

// Auth-path (re-review MINOR): прокси требует 0x02 user/pass — socksAuth шлёт
// корректные креды. Здесь проверяем только выбор авторизованного метода.
func TestSocksConnectAuthMethod(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	// прокси просит 0x02; принимает креды.
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		authHandshake(c, "user", "secret")
	}()
	up, err := socks5Connect(context.Background(), "127.0.0.1", strconv.Itoa(ln.Addr().(*net.TCPAddr).Port),
		"user", "secret", "10.0.0.1", "5900")
	if err != nil {
		t.Fatalf("socks5Connect auth: %v", err)
	}
	up.Close()
}

// authHandshake — прокси: просит 0x02, валидирует креды, CONNECT → REP=0.
func authHandshake(c net.Conn, wantUser, wantPass string) bool {
	// greeting: VER + NMETHODS + methods[].
	g := make([]byte, 2)
	if _, err := io.ReadFull(c, g); err != nil {
		return false
	}
	meth := make([]byte, int(g[1]))
	if _, err := io.ReadFull(c, meth); err != nil {
		return false
	}
	// просим ТОЛЬКО 0x02.
	if _, err := c.Write([]byte{0x05, 0x02}); err != nil {
		return false
	}
	// RFC 1929 auth: VER(0x01) ULEN UNAME PLEN PASSWD.
	av := make([]byte, 1)
	if _, err := io.ReadFull(c, av); err != nil {
		return false
	}
	if av[0] != 0x01 {
		return false
	}
	ul := make([]byte, 1)
	if _, err := io.ReadFull(c, ul); err != nil {
		return false
	}
	uname := make([]byte, int(ul[0]))
	if _, err := io.ReadFull(c, uname); err != nil {
		return false
	}
	pl := make([]byte, 1)
	if _, err := io.ReadFull(c, pl); err != nil {
		return false
	}
	pw := make([]byte, int(pl[0]))
	if _, err := io.ReadFull(c, pw); err != nil {
		return false
	}
	ok := string(uname) == wantUser && string(pw) == wantPass
	if !ok {
		_, _ = c.Write([]byte{0x01, 0x01})
		return false
	}
	_, _ = c.Write([]byte{0x01, 0x00})
	// CONNECT request.
	req := make([]byte, 4)
	if _, err := io.ReadFull(c, req); err != nil {
		return false
	}
	switch req[3] {
	case 1:
		io.ReadFull(c, make([]byte, 4+2))
	case 4:
		io.ReadFull(c, make([]byte, 16+2))
	case 3:
		l := make([]byte, 1)
		io.ReadFull(c, l)
		io.ReadFull(c, make([]byte, int(l[0])+2))
	}
	_, _ = c.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
	return true
}

// IPv6-цель в -L оборачивается в [ ] (bracketIP), IPv4 — без скобок.
func TestTunnelCmdJumpIPv6(t *testing.T) {
	// IPv6: -L 5555:[2001:db8::1]:5900
	cmd := tunnelCmdJump("5555", "2001:db8::1", "5900", "", "jmp", "", "", "")
	s := strings.Join(cmd, " ")
	if !contains(s, "-L 5555:[2001:db8::1]:5900") {
		t.Fatalf("IPv6 -L не огорожен: %q", s)
	}
	// IPv4: без скобок.
	cmd4 := tunnelCmdJump("5555", "10.0.0.1", "5900", "", "jmp", "", "", "")
	if strings.Contains(strings.Join(cmd4, " "), "[10.0.0.1]") {
		t.Fatalf("IPv4 лишний [ ]: %q", strings.Join(cmd4, " "))
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
