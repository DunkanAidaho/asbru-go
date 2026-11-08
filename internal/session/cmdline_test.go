package session

import (
	"testing"

	"github.com/DunkanAidaho/asbru-go/internal/config"
	"github.com/DunkanAidaho/asbru-go/internal/method"
	"strings"
)

func conn(method, user, pass string) *config.Environment {
	return &config.Environment{Name: "test", User: user, Pass: pass, Method: method, IP: "10.0.0.1", Port: "22"}
}

func be() BuildEnv {
	return BuildEnv{UUID: "u1", Name: "test", TmpDir: "/tmp/asbru-test"}
}

func containsArg(t *testing.T, argv []string, want string) {
	t.Helper()
	for _, a := range argv {
		if a == want {
			return
		}
	}
	t.Fatalf("argv %#v не содержит %q", argv, want)
}

// SSH: порт, publickey (фallback), принудительная аутентификация.
func TestBuildCMDSSH(t *testing.T) {
	base := conn("SSH", "root", "p")
	got, err := BuildCmd(be(), base, method.VNCGeneric)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"ssh", "-p", "22", "-l", "root", "10.0.0.1"}
	if len(got) != len(want) {
		t.Fatalf("ssh base: %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("arg %d = %q, want %q", i, got[i], want[i])
		}
	}

	pub := conn("SSH", "root", "p")
	pub.AuthType = "publickey"
	pub.PublicKey = "/id"
	pub.AuthFallback = "1"
	pub.PassphraseUser = "ru"
	got, err = BuildCmd(be(), pub, method.VNCGeneric)
	if err != nil {
		t.Fatal(err)
	}
	// точный порядок: -i, -l из passphrase user (ревью: containsArg не фиксировал порядок)
	wantPub := []string{"ssh", "-p", "22", "-i", "/id", "-l", "ru", "10.0.0.1"}
	if len(got) != len(wantPub) {
		t.Fatalf("pub: %#v, want %#v", got, wantPub)
	}
	for i := range wantPub {
		if got[i] != wantPub[i] {
			t.Errorf("pub arg %d = %q, want %q", i, got[i], wantPub[i])
		}
	}

	// Очный дебаг issue 3: publickey с ПУСТЫМ passphrase user → ssh логинит под
	// системным пользователем (нет -l). Должен фолбэкнуть на User соединения.
	pub2 := conn("SSH", "rock", "p")
	pub2.AuthType = "publickey"
	pub2.PublicKey = "/id"
	pub2.PassphraseUser = ""
	got, err = BuildCmd(be(), pub2, method.VNCGeneric)
	if err != nil {
		t.Fatal(err)
	}
	wantPub2 := []string{"ssh", "-p", "22", "-i", "/id", "-l", "rock", "10.0.0.1"}
	if len(got) != len(wantPub2) {
		t.Fatalf("pub empty passuser: %#v, want %#v", got, wantPub2)
	}
	foundL := false
	for i := range wantPub2 {
		if got[i] != wantPub2[i] {
			t.Errorf("pub-empty arg %d = %q, want %q", i, got[i], wantPub2[i])
		}
		if got[i] == "-l" {
			foundL = true
		}
	}
	if !foundL {
		t.Errorf("нет -l: юзер соединения не передан (системный логин)")
	}

	noFall := conn("SSH", "root", "p")
	noFall.AuthType = "userpass"
	noFall.AuthFallback = "0"
	got, err = BuildCmd(be(), noFall, method.VNCGeneric)
	if err != nil {
		t.Fatal(err)
	}
	containsArg(t, got, "PreferredAuthentications=password,keyboard-interactive")
}

func TestBuildCMDLocalShellConfig(t *testing.T) {
	// M29-ревью I2: Local Shell уважает настроенные shell binary/options,
	// а не только дефолт $SHELL//bin/bash.
	e := &config.Environment{Name: "ls", Method: "Local Shell"}
	got, err := BuildCmd(BuildEnv{Name: "ls", ShellBinary: "/usr/bin/fish", ShellOptions: "-l"},
		e, method.VNCGeneric)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/usr/bin/fish", "-l"}
	if len(got) != len(want) {
		t.Fatalf("Local Shell: %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("arg %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// M30: 'use sudo' — команда оборачивается sudo -p <prompt> (легаси
// asbru_conn:1337-1340, обёртка снаружи prepend/postpend).
func TestBuildCMDSudoWrapper(t *testing.T) {
	e := conn("SSH", "root", "p")
	e.UseSudo = 1
	got, err := BuildCmd(BuildEnv{Name: "s", SudoPrompt: "[__PAC__SUDO__PROMPT__]"}, e, method.VNCGeneric)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"sudo", "-p", "[__PAC__SUDO__PROMPT__]", "ssh", "-p", "22", "-l", "root", "10.0.0.1"}
	if len(got) != len(want) {
		t.Fatalf("sudo: %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("arg %d = %q, want %q", i, got[i], want[i])
		}
	}
	// use sudo=0 — обёртки нет.
	e.UseSudo = 0
	got, err = BuildCmd(BuildEnv{Name: "s", SudoPrompt: "[P]"}, e, method.VNCGeneric)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) > 0 && got[0] == "sudo" {
		t.Fatalf("use sudo выключен, но команда обёрнута: %#v", got)
	}
}

func TestBuildCMDAutossh(t *testing.T) {
	e := conn("SSH", "root", "p")
	e.Autossh = 1
	got, err := BuildCmd(be(), e, method.VNCGeneric)
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != "autossh" {
		t.Fatalf("argv[0] = %q, want autossh (%#v)", got[0], got)
	}
	// мониторинг-порт/прочие ssh-аргументы передаются как обычно.
	for _, want := range []string{"-p", "22", "-l", "root", "10.0.0.1"} {
		containsArg(t, got, want)
	}
	// autossh=0 — обычный ssh.
	e.Autossh = 0
	got, err = BuildCmd(be(), e, method.VNCGeneric)
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != "ssh" {
		t.Fatalf("argv[0] = %q, want ssh", got[0])
	}
}

func TestBuildCMDRandomSocks(t *testing.T) {
	e := conn("SSH", "root", "p")
	got, err := BuildCmd(BuildEnv{Name: "s", RandomSocks: "41234"}, e, method.VNCGeneric)
	if err != nil {
		t.Fatal(err)
	}
	containsArg(t, got, "-D")
	containsArg(t, got, "localhost:41234")
	if got[len(got)-1] != "10.0.0.1" {
		t.Fatalf("host = %q, want 10.0.0.1", got[len(got)-1])
	}
	// пустой RandomSocks — -D не добавляется.
	got, err = BuildCmd(be(), e, method.VNCGeneric)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range got {
		if a == "-D" {
			t.Fatalf("нет RandomSocks, но есть -D: %#v", got)
		}
	}
}

// M30: -L/-R/-D форвардинг передаётся через опции подключения (свободная строка
// options в редакторе; легаси PACMethod_ssh.pm / asbru_conn:1107-1109). Все три
// типа порт-форвардинга идут ДО хоста.
func TestBuildCMDForwarding(t *testing.T) {
	e := conn("SSH", "root", "p")
	e.Options = "-L 8080:localhost:80 -R 9000:localhost:90 -D 1080"
	got, err := BuildCmd(be(), e, method.VNCGeneric)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"-L", "8080:localhost:80", "-R", "9000:localhost:90", "-D", "1080"} {
		containsArg(t, got, want)
	}
	if got[len(got)-1] != "10.0.0.1" {
		t.Fatalf("host = %q, want 10.0.0.1 (форвардинг ДО хоста): %#v", got[len(got)-1], got)
	}
}

// M30-ревью #5: sudo — ВНЕШНЯЯ обёртка (легаси asbru_conn:1337-1340): снаружи
// prepend/postpend; пустой SudoPrompt → дефолт config.DefaultSudoPrompt.
func TestBuildCMDSudoOutermost(t *testing.T) {
	e := conn("SSH", "root", "p")
	e.UseSudo = 1
	e.UsePrependCommand = 1
	e.PrependCommand = "env A=1"
	e.UsePostpendCommand = 1
	e.PostpendCommand = "sh -l"
	got, err := BuildCmd(BuildEnv{Name: "s"}, e, method.VNCGeneric)
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != "sudo" || got[1] != "-p" {
		t.Fatalf("argv[:2] = %q, want [sudo -p]", got[:2])
	}
	if got[2] != config.DefaultSudoPrompt {
		t.Fatalf("prompt = %q, want %q", got[2], config.DefaultSudoPrompt)
	}
	// prepend сразу после sudo -p <prompt>, postpend в самом конце.
	if got[3] != "env" || got[4] != "A=1" {
		t.Fatalf("prepend не сразу после sudo: %#v", got[:5])
	}
	if got[len(got)-1] != "-l" || got[len(got)-2] != "sh" {
		t.Fatalf("postpend не в конце: %#v", got)
	}
}

func TestBuildCMDOtherMethods(t *testing.T) {
	t.Setenv("SHELL", "") // детерминизм: дефолт /bin/bash (иначе $SHELL)
	cases := []struct {
		name string
		cfg  *config.Environment
		want []string
	}{
		{"telnet-default", &config.Environment{Name: "t", Method: "Telnet", IP: "10.0.0.1", Port: "23"}, []string{"telnet", "10.0.0.1"}},
		{"telnet-22", conn("Telnet", "", ""), []string{"telnet", "10.0.0.1", "22"}},
		{"ftp-default", &config.Environment{Name: "f", Method: "FTP", IP: "10.0.0.1", Port: "21"}, []string{"ftp", "10.0.0.1"}},
		{"ftp-22", conn("FTP", "", ""), []string{"ftp", "10.0.0.1", "22"}},
		{"cadaver", conn("WebDAV", "", ""), []string{"cadaver", "10.0.0.1"}},
		{"cu", conn("Serial (cu)", "", ""), []string{"cu", "10.0.0.1"}},
		{"remote-tty", conn("Serial (remote-tty)", "root", ""), []string{"remote-tty", "-l", "root", "10.0.0.1"}},
		{"sftp", conn("SFTP", "root", ""), []string{"sftp", "-P", "22", "root@10.0.0.1"}},
		{"mosh", conn("MOSH", "root", ""), []string{"mosh", "root@10.0.0.1"}},
		{"c3270", conn("IBM 3270/5250", "", ""), []string{"c3270", "[10.0.0.1]:22"}},
		{"c3270-prepend", &config.Environment{Name: "c", Method: "IBM 3270/5250", IP: "10.0.0.1", Port: "23", Options: " -prepend_P x"}, []string{"c3270", "x", "P:[10.0.0.1]:23"}},
		{"generic", &config.Environment{Name: "g", Method: "Generic Command", IP: "ls -la"}, []string{"ls", "-la"}},
		{"shell", &config.Environment{Name: "s", Method: "Local Shell"}, []string{"/bin/bash"}}, // SHELL="" в Setenv ниже
	}
	for _, c := range cases {
		c.cfg.Name = c.name
		got, err := BuildCmd(be(), c.cfg, method.VNCGeneric)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if len(got) != len(c.want) {
			t.Errorf("%s: %#v, want %#v", c.name, got, c.want)
			continue
		}
		for i := range c.want {
			if got[i] != c.want[i] {
				t.Errorf("%s: arg %d = %q, want %q", c.name, i, got[i], c.want[i])
			}
		}
	}
}

func TestBuildCMDRDP(t *testing.T) {
	rd := conn("RDP (rdesktop)", "admin", "pw")
	rd.Port = "3389"
	got, err := BuildCmd(be(), rd, method.VNCGeneric)
	if err != nil {
		t.Fatal(err)
	}
	wantRd := []string{"rdesktop", "-u", "admin", "-p", "-", "-T", "test", "10.0.0.1:3389"}
	if len(got) != len(wantRd) {
		t.Fatalf("rdesktop: %#v, want %#v", got, wantRd)
	}

	xf := conn("RDP (xfreerdp)", "admin", "pw")
	xf.Port = "3389"
	got, err = BuildCmd(be(), xf, method.VNCGeneric)
	if err != nil {
		t.Fatal(err)
	}
	wantXf := []string{"xfreerdp", "/u:admin", "/p:pw", "/t:test", "/v:10.0.0.1:3389"}
	if len(got) != len(wantXf) {
		t.Fatalf("xfreerdp: %#v, want %#v", got, wantXf)
	}
}

func TestBuildCMDVNC(t *testing.T) {
	c := conn("VNC", "", "secret")
	c.Port = "5901"

	got, err := BuildCmd(be(), c, method.VNCTigerVNC)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != "vncviewer" || got[1] != "-PasswordFile=/tmp/asbru-test/asbru_conn_u1.pass" {
		t.Fatalf("tigervnc: %#v", got)
	}
	containsArg(t, got, "10.0.0.1:5901")

	got, err = BuildCmd(be(), c, method.VNCRealVNC)
	if err != nil {
		t.Fatal(err)
	}
	containsArg(t, got, "10.0.0.1:5901")

	// generic: argv без echo-пайпа; пароль сессия передаёт в stdin
	got, err = BuildCmd(be(), c, method.VNCGeneric)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "vncviewer" {
		t.Fatalf("generic vnc: %#v", got)
	}
	containsArg(t, got, "10.0.0.1:5901")
}

func TestBuildCMDUnknown(t *testing.T) {
	_, err := BuildCmd(be(), conn("Bogus", "", ""), method.VNCGeneric)
	if err == nil {
		t.Fatal("ожидали ошибку неизвестного метода")
	}
}

// M16: use proxy=1 (SOCKS) — ProxyCommand через nc (-x host:port).
func TestBuildCmdSocksProxy(t *testing.T) {
	env := &config.Environment{IP: "10.0.0.5", User: "root", Method: "SSH",
		UseProxy: 1, ProxyIP: "10.0.0.1", ProxyPort: "1080"}
	argv, err := BuildCmd(BuildEnv{Name: "s"}, env, "")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "ProxyCommand=nc -x '10.0.0.1':'1080' %h %p") {
		t.Fatalf("нет ProxyCommand: %v", argv)
	}
}

// M16: use proxy=3 (jump) для SSH — -J user@ip:port.
func TestBuildCmdJumpSSH(t *testing.T) {
	env := &config.Environment{IP: "10.0.0.5", User: "root", Method: "SSH",
		UseProxy: 3, JumpIP: "10.0.0.2", JumpPort: 22, JumpUser: "ju"}
	argv, err := BuildCmd(BuildEnv{Name: "s"}, env, "")
	if err != nil {
		t.Fatal(err)
	}
	if !hasPair(argv, "-J", "ju@10.0.0.2:22") {
		t.Fatalf("нет -J: %v", argv)
	}
}

// M16: jump для SFTP — -o Proxyjump=...
func TestBuildCmdJumpSFTP(t *testing.T) {
	env := &config.Environment{IP: "10.0.0.5", User: "root", Method: "SFTP",
		UseProxy: 3, JumpIP: "10.0.0.2", JumpPort: 2222, JumpUser: "ju"}
	argv, err := BuildCmd(BuildEnv{Name: "s"}, env, "")
	if err != nil {
		t.Fatal(err)
	}
	if !hasPair(argv, "-o", "Proxyjump=ju@10.0.0.2:2222") {
		t.Fatalf("нет Proxyjump: %v", argv)
	}
}

// M16: без proxy/jump — команда не меняется.
func TestBuildCmdNoProxy(t *testing.T) {
	env := &config.Environment{IP: "10.0.0.5", User: "root", Method: "SSH"}
	argv, err := BuildCmd(BuildEnv{Name: "s"}, env, "")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(argv, " ")
	if strings.Contains(joined, "ProxyCommand") || strings.Contains(joined, "-J") {
		t.Fatalf("лишний proxy: %v", argv)
	}
}

func hasPair(argv []string, want string, value string) bool {
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == want && argv[i+1] == value {
			return true
		}
	}
	return false
}

// M16-ревью H1-2: прокси-опции идут ДО хоста (sftp игнорирует после).
func TestBuildCmdSocksProxyBeforeHost(t *testing.T) {
	env := &config.Environment{IP: "10.0.0.5", User: "root", Method: "SFTP",
		UseProxy: 1, ProxyIP: "10.0.0.1", ProxyPort: "1080"}
	argv, err := BuildCmd(BuildEnv{Name: "s"}, env, "")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(argv, " ")
	host := strings.Index(joined, "root@10.0.0.5")
	if host == -1 || !strings.Contains(joined[:host], "ProxyCommand") {
		t.Fatalf("ProxyCommand после хоста: %v", argv)
	}
}

// M16-ревью H1-1: sftp + JumpKey — опция целая, ключ отдельным аргументом.
func TestBuildCmdJumpSFTPWithKey(t *testing.T) {
	env := &config.Environment{IP: "10.0.0.5", User: "root", Method: "SFTP",
		UseProxy: 3, JumpIP: "10.0.0.2", JumpPort: 2222, JumpUser: "ju",
		JumpKey: "~/.ssh/jump"}
	argv, err := BuildCmd(BuildEnv{Name: "s"}, env, "")
	if err != nil {
		t.Fatal(err)
	}
	if !hasPair(argv, "-o", "Proxyjump=ju@10.0.0.2:2222") {
		t.Fatalf("proxyjump сломан: %v", argv)
	}
	if !hasPair(argv, "-i", "~/.ssh/jump") {
		t.Fatalf("ключ не отдельным аргументом: %v", argv)
	}
}

// M16-ревью H1-3: proxy=1 на не-ssh — no-op.
func TestBuildCmdProxyNonSSHNoop(t *testing.T) {
	env := &config.Environment{IP: "10.0.0.5", Method: "Telnet",
		UseProxy: 1, ProxyIP: "10.0.0.1", ProxyPort: "1080"}
	argv, err := BuildCmd(BuildEnv{Name: "s"}, env, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(argv, " "), "ProxyCommand") {
		t.Fatalf("telnet получил ProxyCommand: %v", argv)
	}
}

// M16-ревью H2: неизвестный метод + proxy — ошибка не потеряна.
func TestBuildCmdUnknownWithProxy(t *testing.T) {
	env := &config.Environment{IP: "10.0.0.5", Method: "Bogus",
		UseProxy: 1, ProxyIP: "10.0.0.1", ProxyPort: "1080"}
	if _, err := BuildCmd(BuildEnv{Name: "s"}, env, ""); err == nil {
		t.Fatal("неизвестный метод не отклонён")
	}
}

// M16-ревью L2: пустой ProxyPort — no-op.
func TestBuildCmdEmptyProxyPort(t *testing.T) {
	env := &config.Environment{IP: "10.0.0.5", User: "root", Method: "SSH",
		UseProxy: 1, ProxyIP: "10.0.0.1"}
	argv, err := BuildCmd(BuildEnv{Name: "s"}, env, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(argv, " "), "ProxyCommand") {
		t.Fatalf("пустой порт собрал ProxyCommand: %v", argv)
	}
}

// M16-ре-ревью: aliasing — толстый argv (Port+AuthFallback) не теряет
// хост при вставке прокси-опций; хост — последний элемент.
func TestBuildCmdProxyKeepsHost(t *testing.T) {
	env := &config.Environment{IP: "10.0.0.5", User: "root", Method: "SSH",
		Port: "22", AuthFallback: "0",
		UseProxy: 1, ProxyIP: "10.0.0.1", ProxyPort: "1080"}
	argv, err := BuildCmd(BuildEnv{Name: "s"}, env, "")
	if err != nil {
		t.Fatal(err)
	}
	if argv[len(argv)-1] != "10.0.0.5" {
		t.Fatalf("хост затиранием: %v", argv)
	}
	env.UseProxy = 3
	env.JumpIP, env.JumpPort, env.JumpUser = "10.0.0.2", 22, "ju"
	argv, err = BuildCmd(BuildEnv{Name: "s"}, env, "")
	if err != nil {
		t.Fatal(err)
	}
	if argv[len(argv)-1] != "10.0.0.5" {
		t.Fatalf("jump затиранием: %v", argv)
	}
}

// M17-ревью L2-4: mosh с пустым user — не "@host".
func TestBuildCmdMoshEmptyUser(t *testing.T) {
	env := &config.Environment{IP: "10.0.0.1", Method: "MOSH"}
	argv, err := BuildCmd(BuildEnv{Name: "s"}, env, "")
	if err != nil {
		t.Fatal(err)
	}
	if argv[len(argv)-1] != "10.0.0.1" {
		t.Fatalf("mosh: %v", argv)
	}
}

// M23: prepend/postpend — слова вокруг команды (легаси asbru_conn:1311-1338
// склеивает строку для sh; у нас argv без sh — слова prepend-команды
// встают ПЕРЕД строкой, postpend — В КОНЕЦ).
func TestBuildCmdPrependPostpend(t *testing.T) {
	e := &config.Environment{Name: "x", Method: "SSH", User: "u", IP: "h",
		UsePrependCommand: 1, PrependCommand: "env",
		UsePostpendCommand: 1, PostpendCommand: "-v"}
	argv, err := BuildCmd(BuildEnv{}, e, method.VNCGeneric)
	if err != nil {
		t.Fatal(err)
	}
	if argv[0] != "env" || argv[1] != "ssh" {
		t.Fatalf("prepend: %v", argv)
	}
	if argv[len(argv)-1] != "-v" {
		t.Fatalf("postpend: %v", argv)
	}
	// выключенные — без изменений
	e.UsePrependCommand, e.UsePostpendCommand = 0, 0
	argv, _ = BuildCmd(BuildEnv{}, e, method.VNCGeneric)
	if argv[0] == "env" || argv[len(argv)-1] == "-v" {
		t.Fatalf("выключено: %v", argv)
	}
}

// M23: quote-флаги легаси оборачивали строку для sh; в argv-модели кавычки
// не переносим (документировано) — просто не падаем.
func TestBuildCmdQuotesNoop(t *testing.T) {
	e := &config.Environment{Name: "x", Method: "SSH", User: "u", IP: "h",
		UsePrependCommand: 1, PrependCommand: "time", QuoteCommand: 1}
	argv, err := BuildCmd(BuildEnv{}, e, method.VNCGeneric)
	if err != nil || len(argv) < 2 || argv[0] != "time" {
		t.Fatalf("quote: %v (%v)", argv, err)
	}
}

// M35: passwordChoice — реальный пароль шлём один раз автоматически; после
// отклонения (повторный промпт) — вопрос пользователю (легаси <<ASK_PASS>>).
func TestPasswordChoice(t *testing.T) {
	cases := []struct {
		name    string
		pass    string
		att     int
		sent    bool
		wantV   string
		wantAsk bool
		wantRej bool
	}{
		{"first-real", "secret", 1, false, "secret", false, false},
		{"second-after-reject", "secret", 2, true, "", true, false},
		{"six-attempts-ok", "secret", 6, true, "", true, false},
		{"empty-pass", "", 1, false, "", true, false},
		{"ask-pass-token", "<<ASK_PASS>>", 1, false, "", true, false},
		{"seven-attempts", "secret", 7, true, "", false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pe := &config.Environment{Pass: c.pass}
			v, ask, rej := passwordChoice(pe, c.att, c.sent)
			if v != c.wantV || ask != c.wantAsk || rej != c.wantRej {
				t.Errorf("passwordChoice(%q,%d,%v) = %q,%v,%v; want %q,%v,%v",
					c.pass, c.att, c.sent, v, ask, rej, c.wantV, c.wantAsk, c.wantRej)
			}
		})
	}
}

// M36/sec-ревью MINOR: sanitizeFileToken — безопасный компонент имени (не даёт
// path traversal из attacker-controlled uuid).
func TestSanitizeFileToken(t *testing.T) {
	cases := map[string]string{
		"10.0.0.1-abc": "10001-abc", // '.' не допускается (traversal)
		"../../etc/x":  "etcx",      // '/' и '.' отброшены
		"../..":        "",          // всё отброшено
		"u;rm -rf /":   "urm-rf",    // ';'/' ' отброшены
		"abc_def-1":    "abc_def-1",
	}
	for in, want := range cases {
		if got := sanitizeFileToken(in); got != want {
			t.Errorf("sanitizeFileToken(%q)=%q, want %q", in, got, want)
		}
	}
}
