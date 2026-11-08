package session

// M30/M32 backlog: SOCKS/jump-туннель для встроенных RDP/VNC (легаси asbru_conn:
// 883-928). Pе-дизайн (re-review f769f1f7): НЕ shell/FIFO-конвейер (он был
// неработоспособен: F1 sh -c sh, F2 -F на несуществующем conf, F3 течь
// фонового процесса, F4 потеря -p/-i). Вместо этого:
//   - SOCKS: Go-нативный SOCKS5-клиент-форвардер в подпроцессном горутин:
//     локальный net.Listener → SOCKS5-рукопожатие с прокси → relay к target.
//     Пароль НЕ в argv (остаётся в памяти — даже безопаснее M35-файла).
//     Teardown: закрыть Listener + отменить session-контекст (чисто, без течи).
//   - Jump: ssh -L с контроль-сокетом (-S/-O exit), БЕЗ -F (нет проблемного
//     конфига), с -p/-i для порта/ключа прыжкового хоста.

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// allocLocalPort — свободный локальный TCP-порт (bind :0, вернуть, закрыть).
func allocLocalPort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// AllocLocalPort — экспортируемая обёртка allocLocalPort (для UI-слоя, туннель).
func AllocLocalPort() (int, error) { return allocLocalPort() }

// repointTarget — заменяет целевой хост/порт в argv RDP/VNC на 127.0.0.1:<port>
// (клиент идёт через локальный туннель). xfreerdp — /v:IP:PORT; rdesktop/vnc —
// последний аргумент IP:PORT.
func repointTarget(argv []string, method string, port int) []string {
	target := "127.0.0.1:" + strconv.Itoa(port)
	switch method {
	case "RDP (xfreerdp)", "xfreerdp":
		out := make([]string, len(argv))
		for i, a := range argv {
			if strings.HasPrefix(a, "/v:") {
				out[i] = "/v:" + target
			} else {
				out[i] = a
			}
		}
		return out
	default:
		if len(argv) == 0 {
			return argv
		}
		out := append([]string(nil), argv[:len(argv)-1]...)
		return append(out, target)
	}
}

// Tunnel — живой туннель. LocalPort — локальный порт, на который переадресуется
// клиент. cancel — остановить туннель (закрыть Listener/процесс). Потокобезоп.
type Tunnel struct {
	LocalPort int
	mu        sync.Mutex
	cancel    func() error
}

// Kill — остановить туннель (идемпотентно, потокобезопасно).
func (t *Tunnel) Kill() error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cancel == nil {
		return nil
	}
	err := t.cancel()
	t.cancel = nil
	return err
}

// StartTunnelParams — параметры запуска туннеля для встроенного RDP/VNC.
type StartTunnelParams struct {
	LocalPort  int
	TargetIP   string
	TargetPort string
	UseProxy   int // 1=SOCKS, 3=jump (легаси asbru_conn)
	UUID       string
	TmpDir     string
	// SOCKS:
	ProxyIP   string
	ProxyPort string
	ProxyUser string
	ProxyPass string
	// Jump:
	JumpIP   string
	JumpUser string
	JumpPort string
	JumpKey  string
	CtlPath  string // контрол-сокет ssh (uuid.ctl); "" → из TmpDir+UUID
}

// StartTunnel выбирает ветку по UseProxy и запускает туннель.
// Возвращает *Tunnel (nil,nil для use_proxy=0 — туннель не нужен).
func StartTunnel(p StartTunnelParams) (*Tunnel, error) {
	switch p.UseProxy {
	case 0:
		return nil, nil
	case 3:
		return startJump(p)
	case 1:
		return startSocks(p)
	default:
		return nil, errTunnelProxy
	}
}

// startJump — Jump-туннель: ssh -L с контроль-сокетом, БЕЗ -F (не создаём
// несуществующий conf — re-review F2), с -p/-i (F4). Остановка: ssh -S ctl -O exit.
func startJump(p StartTunnelParams) (*Tunnel, error) {
	if p.TargetIP == "" || p.TargetPort == "" || p.JumpIP == "" {
		return nil, errTunnelTarget
	}
	// R1 (re-review SECURITY): jump host — значение из конфига (attacker-controlled
	// при импорте недоверенного YAML). Если начинается с "-", ssh getopt распарсит
	// его как ОПЦИЮ (напр. -oProxyCommand=...) вместо хоста → injection. Хосты/IP
	// никогда не начинаются с "-", поэтому отклоняем. `--` после опций закрывает
	// остаток (доп. защита в tunnelCmdJump + cancel).
	if strings.HasPrefix(p.JumpIP, "-") {
		return nil, errTunnelTarget
	}
	ctl := p.CtlPath
	if ctl == "" {
		ctl = tunnelCtlPath(p.TmpDir, p.UUID, p.LocalPort)
	}
	cmd := tunnelCmdJump(strconv.Itoa(p.LocalPort), p.TargetIP, p.TargetPort,
		p.JumpUser, p.JumpIP, p.JumpPort, p.JumpKey, ctl)
	tn, err := tunnelExec(cmd, ctl)
	if tn != nil {
		tn.LocalPort = p.LocalPort
	}
	return tn, err
}

// startSocks — SOCKS-туннель: Go-нативный форвардер (локальный Listener →
// SOCKS5 → target). Закрытие — Listener.Close + ctx cancel.
func startSocks(p StartTunnelParams) (*Tunnel, error) {
	if p.TargetIP == "" || p.TargetPort == "" || p.ProxyIP == "" || p.ProxyPort == "" {
		return nil, errTunnelTarget
	}
	ctx, cancel := context.WithCancel(context.Background())
	// привязка к 127.0.0.1:LocalPort (может быть 0 → эпhemeral, узнаём из Addr).
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(p.LocalPort))
	if err != nil {
		cancel()
		return nil, err
	}
	localPort := ln.Addr().(*net.TCPAddr).Port
	fwd := &socksForwarder{
		ln:      ln,
		ctx:     ctx,
		pc:      p,
		conns:   map[net.Conn]struct{}{},
		connsMu: sync.Mutex{},
	}
	go fwd.serve()
	return &Tunnel{
		LocalPort: localPort,
		cancel: func() error {
			cancel()         // stop accept loop
			_ = ln.Close()   // ускорить accept
			fwd.closeConns() // закрыть активные relay
			return nil
		},
	}, nil
}

// tunnelCtlPath — контрол-сокет ssh (легаси: $TMP_DIR/$uuid.ctl).
// ag-a20: имя должно быть уникально НА ТУННЕЛЬ (uuid + локальный порт): иначе
// параллельные экземпляры одной сессии («Запустить N…») делят один сокет, второй
// ssh -M не создаёт свой master, а гашение «своего» master задевает чужой.
func tunnelCtlPath(tmpDir, uuid string, localPort int) string {
	if tmpDir == "" {
		return ""
	}
	return joinTmp(tmpDir, fmt.Sprintf("%s_%d", uuid, localPort)) + ".ctl"
}

// joinTmp — безопасный компонент имени в tmp (из uuid, санитизированный).
func joinTmp(tmpDir, uuid string) string {
	return filepathJoin(tmpDir, "asbru_tunnel_"+sanitizeFileToken(uuid))
}

var filepathJoin = func(dir, name string) string {
	if dir == "" {
		return name
	}
	if dir[len(dir)-1] == '/' {
		return dir + name
	}
	return dir + "/" + name
}

var (
	errTunnelTarget = errTunnel{"туннель: неполный целевой адрес (ip/порт)"}
	errTunnelProxy  = errTunnel{"туннель: неизв. use proxy (ожидалось 1/3)"}
	errTunnelReady  = errTunnel{"туннель: не дождались готовности (порт/control-сокет)"}
)

// Finding #5: инжектируемые границы запуска/ожидания готовности jump-туннеля.
var (
	execCommand         = exec.Command
	tunnelReadyTimeout  = 10 * time.Second
	tunnelProbeInterval = 50 * time.Millisecond
	// exitCtlMaster — гашение ssh-master по control-сокету (инжектируется в
	// тестах, чтобы не звать реальный ssh).
	exitCtlMaster = func(ctl, host string) {
		args := []string{"-S", ctl, "-O", "exit"}
		if host != "" {
			// R1: `--` до хоста (иначе host с "-" распарсится как опция).
			args = append(args, "--", host)
		}
		_ = exec.Command("ssh", args...).Run()
	}
)

type errTunnel struct{ msg string }

func (e errTunnel) Error() string { return e.msg }

// tunnelExec — инжектируемая точка запуска ДЖАМП-подпроцесса (юнит-тесты
// подменяют). production: ssh -L с контроль-сокетом; cancel — ssh -S ctl -O exit.
//
// Finding #5: ssh запускается с -f (родитель форкается и выходит), поэтому
// раньше Start без Wait оставлял зомби, а успех возвращался ДО готовности.
// Теперь потомок репится фоновым Wait, а возврат происходит только после
// появления control-сокета (ssh -M) либо приёма на локальном порту -L.
var tunnelExec = func(cmd []string, ctl string, passFile ...string) (*Tunnel, error) {
	// Finding #5: осиротевший control-сокет от прошлого запуска сломал бы
	// ssh -M и дал бы ложную «готовность» — удаляем, если master не отвечает.
	removeStaleCtl(ctl)
	host := jumpHost(cmd)
	// ag-a20: отметка старта ДО запуска — readiness принимает только сокет,
	// появившийся после неё; старый (осиротевший/чужой) master не должен давать
	// ложную «готовность» ещё до аутентификации нового ssh.
	started := time.Now()
	c := execCommand(cmd[0], cmd[1:]...)
	if err := c.Start(); err != nil {
		return nil, err
	}
	localPort := 0
	for i, a := range cmd {
		if a == "-L" && i+1 < len(cmd) {
			localPort = parsePort(cmd[i+1])
			break
		}
	}
	// Wait в фоне: даже после возврата из tunnelExec (и при ошибке готовности)
	// потомок репится, зомби не остаётся.
	waitErr := make(chan error, 1)
	go func() { waitErr <- c.Wait() }()
	if err := waitTunnelReady(ctl, localPort, started, waitErr, tunnelReadyTimeout); err != nil {
		// ag-a20: ssh запущен с -f — прямой ребёнок уже вышел, Process.Kill на
		// нём no-op. Без гашения демона и удаления control-сокета следующая
		// попытка увидит «живой» master и ложно сочтёт туннель готовым.
		cleanupJump(ctl, host, c)
		return nil, err
	}
	cancel := func() error {
		cleanupJump(ctl, host, c)
		return nil
	}
	return &Tunnel{LocalPort: localPort, cancel: cancel}, nil
}

// jumpHost — хост jump-подключения (последний аргумент команды: host или
// user@host).
func jumpHost(cmd []string) string {
	if len(cmd) == 0 {
		return ""
	}
	return cmd[len(cmd)-1]
}

// cleanupJump — остановка jump-туннеля: гасим master по control-сокету
// (ssh -S ctl -O exit), удаляем сокет и best-effort убиваем прямой процесс
// (при -f он уже вышел). Используется и в cancel, и на пути ошибки готовности
// (ag-a20), иначе демон/сокет переживают неудачную попытку.
func cleanupJump(ctl, host string, c *exec.Cmd) {
	if ctl != "" {
		exitCtlMaster(ctl, host)
		_ = os.Remove(ctl)
	}
	if c != nil && c.Process != nil {
		_ = c.Process.Kill() // -f: родитель уже вышел; best-effort.
	}
}

// removeStaleCtl — удаляет control-сокет, если по нему никто не отвечает
// (иначе ssh -M упадёт «already exists», а readiness ложно сработает).
func removeStaleCtl(ctl string) {
	if ctl == "" {
		return
	}
	if _, err := os.Stat(ctl); err != nil {
		return
	}
	if c, err := net.DialTimeout("unix", ctl, 100*time.Millisecond); err == nil {
		_ = c.Close() // живой master — не трогаем
		return
	}
	_ = os.Remove(ctl)
}

// waitTunnelReady — ждёт готовности туннеля: control-сокет (если задан) либо
// приём на локальном порту. notBefore — время старта ssh: control-сокет должен
// появиться не раньше (иначе готовность по чужому/осиротевшему master);
// нулевое время отключает проверку (вызовы без старта). childErr (может быть
// nil) — результат Wait: ненулевой выход ssh прерывает ожидание с исходной
// ошибкой.
func waitTunnelReady(ctl string, port int, notBefore time.Time, childErr <-chan error, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if childErr != nil {
			select {
			case err := <-childErr:
				childErr = nil // одно значение; успешный выход -f не мешает ждать демона
				if err != nil {
					return fmt.Errorf("ssh jump: %w", err)
				}
			default:
			}
		}
		if tunnelReady(ctl, port, notBefore) {
			return nil
		}
		if time.Now().After(deadline) {
			return errTunnelReady
		}
		time.Sleep(tunnelProbeInterval)
	}
}

// tunnelReady — control-сокет существует, является unix-сокетом и появился не
// раньше notBefore (ag-a20: старый сокет от неудачной попытки не означает
// готовность), либо локальный порт принимает соединения (закрываем пробное
// соединение). Нулевой notBefore — без проверки свежести.
func tunnelReady(ctl string, port int, notBefore time.Time) bool {
	if ctl != "" {
		fi, err := os.Stat(ctl)
		if err != nil || fi.Mode()&os.ModeSocket == 0 {
			return false
		}
		return notBefore.IsZero() || !fi.ModTime().Before(notBefore)
	}
	if port > 0 {
		c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 100*time.Millisecond)
		if err == nil {
			_ = c.Close()
			return true
		}
	}
	return false
}

func parsePort(s string) int {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) > 0 {
		if n, err := strconv.Atoi(parts[0]); err == nil {
			return n
		}
	}
	return 0
}

// tunnelCmdJump — команда Jump-туннеля (ssh -L): контроль-сокет (-S/-O exit),
// БЕЗ -F (re-review F2), с -p/-i (F4). Базовый туннель для тестов — без ctl.
func tunnelCmdJump(localPort, targetIP, targetPort, jumpUser, jumpIP, jumpPort, jumpKey, ctl string) []string {
	cmd := []string{"ssh"}
	if ctl != "" {
		cmd = append(cmd, "-S", ctl, "-M")
	}
	// IPv6-цель в -L должна быть в скобках [addr] (иначе ":" — разделитель).
	cmd = append(cmd, "-f", "-N", "-T", "-L", localPort+":"+bracketIP(targetIP)+":"+targetPort)
	if jumpPort != "" {
		cmd = append(cmd, "-p", jumpPort)
	}
	if jumpKey != "" {
		cmd = append(cmd, "-i", jumpKey)
	}
	target := jumpIP
	if jumpUser != "" {
		target = jumpUser + "@" + jumpIP
	}
	// R1: `--` до хоста — остаток не парсится как опции (ssh трактует как host).
	return append(cmd, "--", target)
}

// bracketIP — оборачивает IPv6-адрес в [ ] (используется в -L / -J / ProxyCommand);
// IPv4 и имена — как есть.
func bracketIP(s string) string {
	if strings.Count(s, ":") > 1 { // содержит больше одного ":" → IPv6
		return "[" + s + "]"
	}
	return s
}

// socksForwarder — Go-нативный SOCKS5-клиент форвардер: accept на локальном
// порту, для каждого клиента — SOCKS5-рукопожатие с прокси (auth user/pass при
// заданном), CONNECT к target, затем двунаправленный relay.
type socksForwarder struct {
	ln      net.Listener
	ctx     context.Context
	pc      StartTunnelParams
	conns   map[net.Conn]struct{}
	connsMu sync.Mutex
}

func (f *socksForwarder) serve() {
	for {
		c, err := f.ln.Accept()
		if err != nil {
			return // listener закрыт (cancel) или фатальна ошибка
		}
		f.track(c, true)
		go func() {
			defer f.track(c, false)
			defer c.Close()
			f.handle(c)
		}()
	}
}

func (f *socksForwarder) track(c net.Conn, add bool) {
	f.connsMu.Lock()
	defer f.connsMu.Unlock()
	if add {
		f.conns[c] = struct{}{}
	} else {
		delete(f.conns, c)
	}
}

func (f *socksForwarder) closeConns() {
	f.connsMu.Lock()
	defer f.connsMu.Unlock()
	for c := range f.conns {
		_ = c.Close()
	}
}

func (f *socksForwarder) handle(client net.Conn) {
	up, err := socks5Connect(f.ctx, f.pc.ProxyIP, f.pc.ProxyPort,
		f.pc.ProxyUser, f.pc.ProxyPass, f.pc.TargetIP, f.pc.TargetPort)
	if err != nil {
		return
	}
	// Finding #6: upstream тоже отслеживаем — Kill закрывает и его, иначе relay
	// (io.Copy) и установленное соединение к прокси живут после остановки.
	f.track(up, true)
	defer f.track(up, false)
	defer up.Close()
	go func() { _, _ = io.Copy(up, client) }()
	_, _ = io.Copy(client, up)
}

// socks5Connect — SOCKS5-рукопожатие (RFC 1928) + CONNECT к target.
// auth: user/pass → методы 0x00/0x02 (выбрать 0x02), под-ауth RFC 1929.
func socks5Connect(ctx context.Context, proxyIP, proxyPort, user, pass, targetIP, targetPort string) (net.Conn, error) {
	rd := net.Dialer{Timeout: 10 * time.Second}
	conn, err := rd.DialContext(ctx, "tcp", net.JoinHostPort(proxyIP, proxyPort))
	if err != nil {
		return nil, err
	}
	// Finding #6: отмена ctx должна прерывать и рукопожатие (ReadFull ниже),
	// а не ждать 15-секундный deadline: закрываем conn по Done.
	stopCtx := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopCtx()
	// Deadline на рукопожатие: прокси, принявший TCP, но не отвечающий, не должен
	// вешать горутину/коннект (re-review MINOR). Сбрасываем перед relay.
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	clearDeadline := func() { _ = conn.SetDeadline(time.Time{}) }
	// greeting: VER(5) NMETHODS METHODS. Если user задан — ТОЛЬКО 0x02 (не
	// даём серверу тихо даунгрейдить в no-auth, min-замечание ревьюера).
	if user != "" {
		if _, err := conn.Write([]byte{0x05, 0x01, 0x02}); err != nil {
			conn.Close()
			return nil, err
		}
	} else {
		if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
			conn.Close()
			return nil, err
		}
	}
	var meth [2]byte
	if _, err := io.ReadFull(conn, meth[:]); err != nil {
		conn.Close()
		return nil, err
	}
	if meth[0] != 0x05 {
		conn.Close()
		return nil, errors.New("socks: bad version")
	}
	switch {
	case user != "" && meth[1] == 0x02:
		if err := socksAuth(conn, user, pass); err != nil {
			conn.Close()
			return nil, err
		}
	case user != "":
		// сервер не выбрал 0x02, но мы просили только его — недопустимо.
		conn.Close()
		return nil, fmt.Errorf("socks: auth required, got method %d", meth[1])
	case meth[1] == 0x00:
		// no-auth ok
	case meth[1] == 0xFF:
		conn.Close()
		return nil, errors.New("socks: no acceptable auth method")
	default:
		conn.Close()
		return nil, fmt.Errorf("socks: server chose method %d, not supported", meth[1])
	}
	if err := socksConnectCmd(conn, targetIP, targetPort); err != nil {
		conn.Close()
		return nil, err
	}
	clearDeadline() // рукопожатие завершено — relay без дедлайна
	return conn, nil
}

// socksAuth — RFC 1929 username/password auth.
func socksAuth(conn net.Conn, user, pass string) error {
	if len(user) > 255 || len(pass) > 255 {
		return errors.New("socks: credential too long")
	}
	b := make([]byte, 0, 3+len(user)+len(pass))
	b = append(b, 0x01, byte(len(user)))
	b = append(b, user...)
	b = append(b, byte(len(pass)))
	b = append(b, pass...)
	if _, err := conn.Write(b); err != nil {
		return err
	}
	var r [2]byte
	if _, err := io.ReadFull(conn, r[:]); err != nil {
		return err
	}
	if r[1] != 0 {
		return errors.New("socks: auth failed")
	}
	return nil
}

// socksConnectCmd — CONNECT: VER(5) CMD(1) RSV(0) ATYP(1) DST.ADDR DST.PORT.
// Target — IP (v4/v6) или имя-хост (ATYP 0x03); RDP/VNC-серверы часто по имени
// (R2: легаси nc -x разрешал hostname, наш предыдущий код требовал IP — регресс).
func socksConnectCmd(conn net.Conn, targetIP, targetPort string) error {
	port, err := strconv.Atoi(targetPort)
	if err != nil || port < 1 || port > 65535 {
		return errors.New("socks: bad target port")
	}
	ip := net.ParseIP(targetIP)
	b := []byte{0x05, 0x01, 0x00}
	if ip == nil {
		// имя-хост: ATYP 0x03, длина (1 байт) + байты имени.
		if len(targetIP) > 255 {
			return errors.New("socks: target hostname too long")
		}
		b = append(b, 0x03, byte(len(targetIP)))
		b = append(b, targetIP...)
	} else if v4 := ip.To4(); v4 != nil {
		b = append(b, 0x01)
		b = append(b, v4...)
	} else {
		b = append(b, 0x04)
		b = append(b, ip.To16()...)
	}
	var p [2]byte
	binary.BigEndian.PutUint16(p[:], uint16(port))
	b = append(b, p[:]...)
	if _, err := conn.Write(b); err != nil {
		return err
	}
	// reply: VER REP RSV ATYP BND.ADDR BND.PORT (переменной длины)
	hdr := make([]byte, 4)
	if _, err := io.ReadFull(conn, hdr); err != nil {
		return err
	}
	if hdr[1] != 0 {
		return fmt.Errorf("socks: connect failed, code %d", hdr[1])
	}
	switch hdr[3] {
	case 0x01:
		if _, err := io.ReadFull(conn, make([]byte, 4+2)); err != nil {
			return err
		}
	case 0x04:
		if _, err := io.ReadFull(conn, make([]byte, 16+2)); err != nil {
			return err
		}
	case 0x03:
		l := make([]byte, 1)
		if _, err := io.ReadFull(conn, l); err != nil {
			return err
		}
		if _, err := io.ReadFull(conn, make([]byte, int(l[0])+2)); err != nil {
			return err
		}
	default:
		return errors.New("socks: bad reply atyp")
	}
	return nil
}
