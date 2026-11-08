// Пакет session: goroutine-сессия соединения (PTY, expect, событийные каналы).
package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DunkanAidaho/asbru-go/internal/config"
	"github.com/DunkanAidaho/asbru-go/internal/method"
)

// BuildEnv — внеконфиговый контекст сборки командной строки.
type BuildEnv struct {
	UUID         string
	Name         string
	TmpDir       string
	ShellBinary  string           // Local Shell: defaults.shell binary
	ShellOptions string           // Local Shell: defaults.shell options
	RandomSocks  string           // порт случайного SOCKS-туннеля ("-D localhost:PORT")
	SudoPrompt   string           // 'use sudo' — клиентский sudo -p (literal; defaults 'sudo prompt')
	Defaults     *config.Defaults // M35 P0: defaults-proxy (case 0 'use global configuration')
}

// BuildCmd собирает argv для запуска подключения — современный аналог
// диспетчера asbru_conn:1100-1308 без X11/embed-логики и без sh-обёртки.
func BuildCmd(env BuildEnv, e *config.Environment, vncBackend method.VNCBackend) ([]string, error) {
	opts := method.SplitArgs(e.Options)
	manual := e.AuthType == "manual"

	argv := buildByMethod(e, opts, vncBackend, env, manual)
	if argv == nil {
		return nil, fmt.Errorf("unsupported connection method %q", e.Method)
	}
	// H2-ревью: nil-метод отклоняется ДО applyProxy (иначе proxy подменил
	// бы пустую команду на ["-o", ...] и exec упал непонятной ошибкой)
	var perr error
	if argv, perr = applyProxy(argv, e, env); perr != nil {
		return nil, perr
	}
	// M23: prepend/postpend (легаси asbru_conn:1311-1338) — слова команды
	// в начало/в конец. Quote-флаги у легаси оборачивали строку для sh
	// двойными кавычками; у нас argv без sh — кавычки не переносим.
	// M30: sudo — обёртка снаружи prepend/postpend (легаси asbru_conn:1337-1340).
	return applySudo(applyPrePost(argv, e), e, env), nil
}

// applySudo — 'use sudo': команда оборачивается sudo -p <prompt> (легаси
// asbru_conn:1337-1340). Prompt — клиентский, чтобы expect-движок мог его
// сматчить и послать пароль; пустой -> дефолт defaults 'sudo prompt'.
func applySudo(argv []string, e *config.Environment, env BuildEnv) []string {
	if e.UseSudo != 1 || len(argv) == 0 {
		return argv
	}
	prompt := env.SudoPrompt
	if prompt == "" {
		prompt = config.DefaultSudoPrompt
	}
	pre := []string{"sudo", "-p", prompt}
	return append(pre, argv...)
}

// applyPrePost — prepend-слова в начало, postpend — в конец.
func applyPrePost(argv []string, e *config.Environment) []string {
	// легаси: quote-флаги только модифицируют ОБОРОТ строки в sh
	// (QUOTE = quote && USE_...), включателем сами не являются.
	pre := strings.Fields(e.PrependCommand)
	post := strings.Fields(e.PostpendCommand)
	if e.UsePrependCommand == 1 && len(pre) > 0 {
		argv = append(append([]string(nil), pre...), argv...)
	}
	if e.UsePostpendCommand == 1 && len(post) > 0 {
		argv = append(argv, post...)
	}
	return argv
}

func buildByMethod(e *config.Environment, opts []string,
	vncBackend method.VNCBackend, env BuildEnv, manual bool) []string {
	switch e.Method {
	case "SSH", "ssh":
		return sshCmd(e, opts, env)
	case "MOSH", "mosh":
		return moshCmd(e, opts)
	case "SFTP", "sftp":
		return sftpCmd(e, opts)
	case "Telnet", "telnet":
		argv := append([]string{"telnet"}, opts...)
		argv = append(argv, e.IP)
		if e.Port != "" && e.Port != "23" {
			argv = append(argv, e.Port)
		}
		return argv
	case "FTP", "ftp":
		argv := append([]string{"ftp"}, opts...)
		argv = append(argv, e.IP)
		if e.Port != "" && e.Port != "21" {
			argv = append(argv, e.Port)
		}
		return argv
	case "WebDAV", "cadaver":
		return append([]string{"cadaver"}, append(opts, e.IP)...)
	case "Serial (cu)", "cu":
		return append([]string{"cu"}, append(opts, e.IP)...)
	case "Serial (remote-tty)", "remote-tty":
		argv := append([]string{"remote-tty"}, opts...)
		if e.User != "" {
			argv = append(argv, "-l", e.User)
		}
		return append(argv, e.IP)
	case "IBM 3270/5250", "c3270":
		return c3270Cmd(e, opts)
	case "RDP (rdesktop)", "rdesktop":
		return rdesktopCmd(e, opts, manual)
	case "RDP (xfreerdp)", "xfreerdp":
		return xfreerdpCmd(e, opts, manual)
	case "VNC", "vncviewer":
		return vncCmd(e, opts, vncBackend, env)
	case "Generic Command", "generic":
		return method.SplitArgs(e.IP)
	case "Local Shell", "PACShell":
		shell := env.ShellBinary
		if shell == "" {
			shell = os.Getenv("SHELL") // L2-3-ревью M17
		}
		if shell == "" {
			shell = "/bin/bash"
		}
		argv := strings.Fields(shell)
		return append(argv, method.SplitArgs(env.ShellOptions)...)
	}
	return nil
}

// applyProxy — SOCKS/jump (легаси asbru_conn:792-869): use proxy=1 —
// ProxyCommand через nc -x (или ncat с auth); =3 — jump (ssh -J, sftp -o
// Proxyjump); =0 — глобальные defaults-proxy (defaults.proxy 'Jump'/'Proxy').
// Туннели для RDP/VNC — бэклог (only ssh-like). Опции вставляются ДО хоста:
// sftp парсит опции только до хоста (ревью M16 H1-2), ssh — с обоих мест.
//
// M35-ревью MAJOR (безопасность): SSH исполняет ProxyCommand через
// /bin/sh -c, поэтому каждое значение из конфига (ip/port/user/pass) должно
// попадать в команду в shellQuote — иначе метасимволы ломали бы её (инъекция),
// а пароль читался бы из argv ssh (виден через /proc/PID/cmdline). Пароль при
// авторизации нcat передаётся через 0600-файл, а не в argv.
func applyProxy(argv []string, e *config.Environment, env BuildEnv) ([]string, error) {
	if len(argv) == 0 {
		return argv, nil
	}
	var extra []string
	var err error
	switch e.UseProxy {
	case 1:
		extra, err = proxyExtra(env, e.Method, e.ProxyIP, e.ProxyPort, e.ProxyUser, e.ProxyPass)
	case 3:
		extra = jumpExtra(e.Method, e.JumpIP, int(e.JumpPort), e.JumpUser, e.JumpKey)
	case 0:
		// M35 P0: 'Use global configuration settings' (легаси asbru_conn:825).
		if env.Defaults == nil {
			return argv, nil
		}
		switch env.Defaults.Proxy {
		case "Jump":
			extra = jumpExtra(e.Method, env.Defaults.JumpIP, int(env.Defaults.JumpPort), env.Defaults.JumpUser, env.Defaults.JumpKey)
		case "Proxy":
			extra, err = proxyExtra(env, e.Method, env.Defaults.ProxyIP, env.Defaults.ProxyPort, env.Defaults.ProxyUser, env.Defaults.ProxyPass)
		}
	}
	if err != nil {
		return nil, err
	}
	if extra == nil {
		return argv, nil
	}
	// до хоста: хост — последний элемент (ssh/sftp-ветки, легаси-порядок).
	// ФРЕШ-СРЕЗ: append в argv[:len-1] при достаточном cap пишет в тот же
	// back-массив и затирает хост (ревью M16 — видно на Port+AuthFallback).
	host := argv[len(argv)-1]
	head := append([]string(nil), argv[:len(argv)-1]...)
	res := make([]string, 0, len(head)+len(extra)+1)
	res = append(res, head...)
	res = append(res, extra...)
	return append(res, host), nil
}

// shellQuote — POSIX-оборачивание в одиночные кавычки: значение из конфига
// не может разорвать команду, которую SSH исполняет через /bin/sh -c
// (M35-ревью MAJOR: инъекция через ProxyCommand).
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// proxyExtra — SOCKS ProxyCommand. Легаси `_getProxyCmd`: c user/pass — ncat
// (--proxy-type socks5, --proxy-auth user:"pass"); иначе nc -x. proxy-type
// захардкожен 'socks5' (asbru_conn:75). Только ssh-like; не-ssh — no-op.
func proxyExtra(env BuildEnv, method, ip, port, user, pass string) ([]string, error) {
	if !isSSHLike(method) || ip == "" || port == "" {
		return nil, nil // L2: пустой порт — мусор, не собираем
	}
	if user != "" {
		// M35-ревью MAJOR: пароль НЕ в argv (виден через /proc/PID/cmdline) —
		// пишем в 0600-файл в tmp и читаем в ProxyCommand через $(cat ...).
		// Строка внутри двойных кавычек: вывод cat не сплитится/не глобится —
		// значение пароля безопасно, а команда с уже равноцена 'pw'. Все
		// значения из конфига — shellQuote (инъекция закрыта).
		passFile, err := writeProxyPass(env.TmpDir, env.UUID, pass)
		if err != nil {
			return nil, err
		}
		pc := fmt.Sprintf(
			"ProxyCommand=ncat --proxy %s:%s --proxy-type socks5 --proxy-auth %s:\"$(cat %s)\" %%h %%p",
			shellQuote(ip), shellQuote(port), shellQuote(user), shellQuote(passFile))
		return []string{"-o", pc}, nil
	}
	return []string{"-o", fmt.Sprintf(
		"ProxyCommand=nc -x %s:%s %%h %%p", shellQuote(ip), shellQuote(port))}, nil
}

// writeProxyPass — пароль прокси в 0600-файл (как VNC pass, sessionio.go): не
// попадает в argv ssh (M35-ревью MAJOR). Возвращает путь для ProxyCommand.
func writeProxyPass(dir, uuid, pass string) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	// Security (sec-ревью MINOR): uuid — ключ конфига (attacker-controlled при
	// импорте недоверенного YAML); filepath.Join дал бы путь ВНЕ tmp при '../..'.
	// Санитизируем к безопасному компоненту имени (только [0-9a-zA-Z._-]).
	name := sanitizeFileToken(uuid)
	path := filepath.Join(dir, "asbru_conn_"+name+".proxy")
	if err := os.WriteFile(path, []byte(pass), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// sanitizeFileToken — безопасный компонент имени файла: только [0-9a-zA-Z_-];
// точки НЕ допускаются (иначе '..' — возможный traversal при filepath.Join).
func sanitizeFileToken(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '_' || r == '-' ||
			(r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// jumpExtra — jump-аргументы (ssh -J / sftp -o Proxyjump). Только SSH/SFTP;
// не-ssh (VNC/RDP) — no-op (туннель — бэклог).
func jumpExtra(method, ip string, port int, user, key string) []string {
	if ip == "" {
		return nil
	}
	addr := ip
	if user != "" {
		addr = user + "@" + ip
	}
	if port != 0 { // легаси всегда пишет Port (jump)
		addr += fmt.Sprintf(":%d", port)
	}
	switch method {
	case "SSH", "ssh":
		extra := []string{"-J", addr}
		if key != "" {
			// упрощение: ключ идёт обоим хопам (легаси — IdentityFile
			// для jumpserver; осознанный девиант — больше попыток, не краш)
			extra = append(extra, "-i", key)
		}
		return extra
	case "SFTP", "sftp":
		extra := []string{"-o", "Proxyjump=" + addr}
		if key != "" {
			extra = append(extra, "-i", key)
		}
		return extra
	}
	return nil
}

func isSSHLike(m string) bool {
	switch m {
	case "SSH", "ssh", "SFTP", "sftp":
		return true
	}
	return false
}

// authArgs — общий publickey/auth-fallback-блок для ssh/sftp (вынесен из sshCmd/
// sftpCmd; бэклог-#Cleanup — дедупликация). Возвращает дополненный argv и user.
func authArgs(argv []string, e *config.Environment) ([]string, string) {
	user := e.User
	if e.AuthType == "publickey" {
		if e.PublicKey != "" {
			argv = append(argv, "-i", e.PublicKey)
		}
		if e.AuthFallback == "0" {
			argv = append(argv, "-o", "PreferredAuthentications=publickey")
		}
		// Очный дебаг issue 3: пустой 'passphrase user' → ssh логинился под
		// СИСТЕМНЫМ пользователем (нет -l). Фолбэк на User соединения.
		user = e.PassphraseUser
		if user == "" {
			user = e.User
		}
	} else if e.AuthFallback == "0" {
		argv = append(argv, "-o", "PreferredAuthentications=password,keyboard-interactive")
	}
	return argv, user
}

func sshCmd(e *config.Environment, opts []string, env BuildEnv) []string {
	// M30: 'autossh' — бинарник autossh вместо ssh (легаси asbru_conn:152-153);
	// мониторинг-порт/туннели задаются через опции подключения как обычно.
	bin := "ssh"
	if e.Autossh == 1 {
		bin = "autossh"
	}
	argv := []string{bin}
	if e.Port != "" && e.Port != "0" {
		argv = append(argv, "-p", e.Port)
	}
	argv, user := authArgs(argv, e)
	argv = append(argv, opts...)
	// M30: Incognito random-SOCKS-туннель (легаси asbru_conn:1107-1109):
	// -D localhost:<port> — до хоста. Собственно аллокация порта — в UI-слое.
	if env.RandomSocks != "" {
		argv = append(argv, "-D", "localhost:"+env.RandomSocks)
	}
	if user != "" {
		argv = append(argv, "-l", user)
	}
	return append(argv, e.IP)
}

func moshCmd(e *config.Environment, opts []string) []string {
	argv := []string{"mosh"}
	user := e.User
	if e.AuthType == "publickey" {
		user = e.PassphraseUser
		if user == "" { // issue 3: пустой passphrase user → User соединения
			user = e.User
		}
		key := ""
		if e.PublicKey != "" {
			key = fmt.Sprintf(" -i '%s'", e.PublicKey)
		}
		// M35 P0: пустой порт не даёт мусорное "--ssh=ssh -p " (без значения).
		// легаси-дефолт порта 22; не указываем -p для дефолта (как в userpass).
		ssh := "ssh"
		if e.Port != "" && e.Port != "22" {
			ssh += fmt.Sprintf(" -p %s", e.Port)
		}
		argv = append(argv, "--ssh="+ssh+key)
	} else if e.Port != "" && e.Port != "22" {
		argv = append(argv, fmt.Sprintf("--ssh=ssh -p %s", e.Port))
	}
	argv = append(argv, opts...)
	if user == "" { // L2-4-ревью M17: не даём "mosh @host"
		return append(argv, e.IP)
	}
	return append(argv, user+"@"+e.IP)
}

func sftpCmd(e *config.Environment, opts []string) []string {
	argv := []string{"sftp"}
	argv, user := authArgs(argv, e)
	if e.Port != "" && e.Port != "0" {
		argv = append(argv, "-P", e.Port)
	}
	argv = append(argv, opts...)
	if user == "" { // defense: пустой user не даёт "@host" (L3-ревью M16)
		return append(argv, e.IP)
	}
	return append(argv, user+"@"+e.IP)
}

func c3270Cmd(e *config.Environment, opts []string) []string {
	cleaned := make([]string, 0, len(opts))
	prefix := ""
	for _, o := range opts {
		if len(o) > len("-prepend_") && strings.HasPrefix(o, "-prepend_") && strings.ContainsAny(o[len("-prepend_"):], "PSNL") {
			prefix = o[len("-prepend_"):]
			continue
		}
		cleaned = append(cleaned, o)
	}
	argv := append([]string{"c3270"}, cleaned...)
	target := "[" + e.IP + "]:" + e.Port
	if prefix != "" {
		target = prefix + ":" + target
	}
	return append(argv, target)
}

func rdesktopCmd(e *config.Environment, opts []string, manual bool) []string {
	argv := append([]string{"rdesktop"}, opts...)
	if !manual && e.User != "" {
		argv = append(argv, "-u", e.User, "-p", "-")
	} else if !manual {
		argv = append(argv, "-p", "-")
	}
	argv = append(argv, "-T", e.TitleOrName())
	return append(argv, e.IP+":"+e.Port)
}

func xfreerdpCmd(e *config.Environment, opts []string, manual bool) []string {
	argv := append([]string{"xfreerdp"}, opts...)
	if !manual {
		argv = append(argv, "/u:"+e.User, "/p:"+e.Pass)
	}
	argv = append(argv, "/t:"+e.TitleOrName())
	return append(argv, "/v:"+e.IP+":"+e.Port)
}

func vncCmd(e *config.Environment, opts []string, backend method.VNCBackend, env BuildEnv) []string {
	argv := append([]string{"vncviewer"}, opts...)
	switch backend {
	case method.VNCTigerVNC:
		// Файл-пароль создаёт session перед запуском; путь САНИТИЗИРОВАН как в
		// writeVNCPassFile (иначе path traversal из uuid в imported YAML).
		pfile := filepath.Join(env.TmpDir, "asbru_conn_"+sanitizeFileToken(env.UUID)+".pass")
		argv = append(argv, "-PasswordFile="+pfile)
	case method.VNCRealVNC:
		if e.User != "" {
			argv = append(argv, "UserName="+e.User)
		}
	}
	// VNCGeneric: пароль передаётся в stdin из expect-движка.
	return append(argv, e.IP+":"+e.Port)
}
