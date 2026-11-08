package config

import (
	"os"
	"regexp"
	"strings"
)

// Sanitize нормализует конфиг (порт _cfgSanityCheck): legacy-имена методов,
// IdentityFile → publickey, screenshot → screenshots[], мусорные UUID,
// defaults.version, сброс gui-пароля у конфигов до 4.5.0.1. Мутирует cfg.
// seedDefaults — дефолты defaults (легаси _cfgSanityCheck, PACUtils:2049-2079,
// 2120-2121). Отклонение: явный 0 у FlexInt-полей не отличим от отсутствия —
// перетирается сидом (документировано).
func seedDefaults(c *Config) {
	d := &c.Defaults
	seed := func(k string) bool { return !d.seen[k] } // легаси //=: только неявнное
	if seed("timeout connect") && d.TimeOutConnect == 0 {
		d.TimeOutConnect = 40
	}
	if seed("timeout command") && d.TimeOutCommand == 0 {
		d.TimeOutCommand = 60
	}
	if seed("auto accept key") && d.AutoAcceptKey == 0 {
		d.AutoAcceptKey = 1 // легаси-дефолт: ключ принимается автоматически
	}
	if seed("sudo password") && d.SudoPassword == "" {
		d.SudoPassword = "<<ASK_PASS>>"
	}
	if seed("command prompt") && d.CommandPrompt == "" {
		d.CommandPrompt = DefaultCommandPrompt
	}
	if seed("username prompt") && d.UsernamePrompt == "" {
		d.UsernamePrompt = DefaultUsernamePrompt
	}
	if seed("password prompt") && d.PasswordPrompt == "" {
		d.PasswordPrompt = DefaultPasswordPrompt
	}
	if seed("hostkey changed prompt") && d.HostkeyChangedPrompt == "" {
		d.HostkeyChangedPrompt = DefaultHostkeyChangedPrompt
	}
	if seed("press any key prompt") && d.PressAnyKeyPrompt == "" {
		d.PressAnyKeyPrompt = DefaultPressAnyKeyPrompt
	}
	if seed("remote host changed prompt") && d.RemoteHostChangedPrompt == "" {
		d.RemoteHostChangedPrompt = DefaultRemoteHostPrompt
	}
	if seed("sudo prompt") && d.SudoPrompt == "" {
		d.SudoPrompt = DefaultSudoPrompt
	}
	// M21: дефолтные привязки (легаси PACKeyBindings _getDefaultConfig
	// 476-530); регистрируются только действия, реализованные в порте.
	// Живой тест 2026-10-14: «голые» Ctrl+<буква> съедались оконными
	// QShortcut до шелла (Ctrl+R не доходил до bash reverse-search и т.д.) —
	// дефолты приведены к konsole-паритету (Ctrl+Shift+*, дерево Alt+Shift+*),
	// старые легаси-записи переезжает migrateKeybindings.
	if seed("keybindings") && len(d.KeyBindings) == 0 {
		d.KeyBindings = map[string]map[string][]string{
			"pacmain": {
				"Alt+n": {"Main Window", "showconnections", "Show/Hide connections list"},
			},
			// Batch B (терминал): база легаси PACKeyBindings.pm 500-575,
			// клавиши — konsole-паритет.
			"terminal": {
				"Alt+e":        {"Terminal", "edit_node", "Edit connection"},
				"Ctrl+Shift+C": {"Terminal", "copy", "Copy selection to clipboard"},
				"Ctrl+Shift+V": {"Terminal", "paste", "Paste clipboard into terminal"},
				"Ctrl+Shift+W": {"Terminal", "close", "Close Terminal"},
				"Ctrl+Alt+W":   {"Terminal", "disconnect", "Disconnect Terminal"},
				"Ctrl+Shift+F": {"Terminal", "find", "Find in connection tree"},
				"CtrlShift+F4": {"Terminal", "closealltabs", "Close all tabs"},
				"Ctrl+Shift+D": {"Terminal", "duplicate", "Duplicate connection"},
				"Ctrl+Shift+R": {"Terminal", "restart", "Restart connection (close and start)"},
				"Ctrl+Shift+G": {"Terminal", "hostname", "Guess hostname"},
				"AltCtrl+s":    {"Terminal", "sftp", "Open SFTP session"},
			},
			// Batch A (дерево): база легаси PACKeyBindings.pm 476-530.
			"treeConnections": {
				"Alt+c":       {"Connections Tree", "connect_node", "Connect selected node"},
				"Alt+Shift+E": {"Connections Tree", "expand_all", "Expand tree completely"},
				"Alt+Shift+C": {"Connections Tree", "collaps_all", "Collapse tree completely"},
				"Alt+Shift+D": {"Connections Tree", "clone", "Clone connection"},
				"Alt+r":       {"Connections Tree", "protection", "Toggle protection"},
				"F2":          {"Connections Tree", "rename", "Rename node"},
			},
		}
	}
}

// migrateKeybindings — перевод существующих конфигов с легаси-дефолтов
// (PACKeyBindings.pm 476-575, «голый» Ctrl+<буква>) на konsole-паритет.
// Переезжает ТОЧНО известноя легаси-запись (секция+клавиша+действие); свои
// биндинги пользователя не трогаются. Самосходящаяся: после переноса старой
// записи больше нет, повторный Sanitize — no-op, маркер версии не нужен.
// Новая клавиша не перезатирает занятую (там, где пользователь уже повесил
// что-то своё, старая запись просто удаляется — действие доступно из меню).
//
// Живой баг 2026-10-14 (продолжение): у реальных конфигов, унаследованных от
// Perl, таблица ШИРЕ сида M21 — и её «голые» Ctrl тоже ели шелл
// (treeConnections Ctrl+c «Copy node» съедал Ctrl+C — SIGINT не доходил до
// bash). Пустая новая клавиша = «удалить дубль» (действие остаётся на другом
// биндинге). NB Qt (проверено equality-пробником 6.12): «Ctrl+X» ЛЮБОГО
// регистра матчит ГОЛЫЙ Ctrl+X — Shift заглавной буквой не добавляется,
// поэтому регистр в таблице переносов не важен, важен сам факт «Ctrl+буква».
func migrateKeybindings(d *Defaults) {
	// старый ключ → [новая секция, новая клавиша, действие]; пустая клавиша — удалить.
	moves := map[string][3]string{
		"terminal|Ctrl+C":        {"terminal", "Ctrl+Shift+C", "copy"},
		"terminal|Ctrl+V":        {"terminal", "Ctrl+Shift+V", "paste"},
		"terminal|Ctrl+w":        {"terminal", "Ctrl+Shift+W", "close"},
		"terminal|Ctrl+W":        {"terminal", "Ctrl+Alt+W", "disconnect"},
		"terminal|Ctrl+f":        {"terminal", "Ctrl+Shift+F", "find"},
		"terminal|Ctrl+D":        {"terminal", "Ctrl+Shift+D", "duplicate"},
		"terminal|Ctrl+R":        {"terminal", "Ctrl+Shift+R", "restart"},
		"terminal|Ctrl+g":        {"terminal", "Ctrl+Shift+G", "hostname"},
		"terminal|Ctrl+q":        {"terminal", "Ctrl+Alt+Q", "quit"},
		"treeConnections|Ctrl+r": {"treeConnections", "Alt+Shift+E", "expand_all"},
		"treeConnections|Ctrl+t": {"treeConnections", "Alt+Shift+C", "collaps_all"},
		"treeConnections|Ctrl+d": {"treeConnections", "Alt+Shift+D", "clone"},
		"treeConnections|Ctrl+c": {"treeConnections", "Ctrl+Alt+C", "copy"},
		"treeConnections|Ctrl+v": {"treeConnections", "Ctrl+Alt+V", "paste"},
		"treeConnections|Ctrl+f": {"treeConnections", "", "find"},
		"pacmain|Ctrl+f":         {"pacmain", "", "find"},
		"pacmain|Ctrl+q":         {"pacmain", "", "quit"},
		// Ревью B (Critical): на Perl-конфигах жили ещё supported-записи на
		// голом Ctrl — localshell ел Ctrl+T (transpose-char), infotab ел
		// Ctrl+I (=Tab, автодополнение).
		"pacmain|Ctrl+T":  {"pacmain", "Ctrl+Alt+T", "localshell"},
		"terminal|Ctrl+I": {"terminal", "Ctrl+Alt+I", "infotab"},
	}
	for section, binds := range d.KeyBindings {
		for key, spec := range binds {
			if len(spec) < 2 {
				continue
			}
			m, ok := moves[section+"|"+key]
			if !ok || spec[1] != m[2] {
				continue // не легаси-дефолт (свой биндинг) — не трогаем
			}
			if m[1] != "" {
				if d.KeyBindings[m[0]] == nil {
					d.KeyBindings[m[0]] = map[string][]string{}
				}
				if _, taken := d.KeyBindings[m[0]][m[1]]; !taken {
					d.KeyBindings[m[0]][m[1]] = spec
				}
			}
			delete(binds, key)
		}
	}
}

// NormalizeAuthFallback приводит 'auth fallback' к канону «0»/«1».
// Легаси PACUtils.pm:2347 (`auth fallback //= 1`): отсутствие → 1 (резервная
// авторизация разрешена). Go-строка не отличает отсутствие от пустой, а combo
// редактора знает только {"0","1"}: SetCurrentText("") оставлял индекс 0, и
// сохранение тихо писало "0" (парольный fallback выключался). Поэтому пустое и
// любое нераспознанное значение нормализуем в "1"; явные "0"/"1" не трогаем.
func NormalizeAuthFallback(v string) string {
	if v == "0" || v == "1" {
		return v
	}
	return "1"
}

func Sanitize(c *Config) {
	seedDefaults(c)
	migrateKeybindings(&c.Defaults)
	if c.Defaults.Version == "" {
		c.Defaults.Version = "6.4.2" // APPVERSION последнего Perl-релиза
	}
	if c.Defaults.Version < "4.5.0.1" { // в легаси здесь strcmp: same as Perl lt
		c.Defaults.UseGUIPassword = 0
		c.Defaults.GUIPassword = ""
	}

	clean := make(map[string]Environment, len(c.Environments))
	for uuid, env := range c.Environments {
		if isJunkUUID(uuid, env) {
			continue
		}
		env.Method = normalizeMethod(env.Method)
		env.AuthFallback = NormalizeAuthFallback(env.AuthFallback)
		if env.Method == "SSH" {
			extractIdentityFile(&env)
		}
		migrateRdpOptions(&env) // user-тест #3: rdesktop↔xfreerdp автоконвертация
		migrateScreenshot(&env)
		clean[uuid] = env
	}
	c.Environments = clean
}

func isJunkUUID(uuid string, env Environment) bool {
	return strings.HasPrefix(uuid, "HASH") || strings.HasPrefix(uuid, "_tmp_") ||
		strings.HasPrefix(uuid, "pacshell_PID") || uuid == "" ||
		(env.Name == "" && !env.NameSet && uuid != RootUUID)
}

// normalizeMethod — цепочка правил из _cfgSanityCheck. Сравнения (как в Perl)
// регистрозависимы: известные lowercase-имена приводятся к канону, незнакомые
// вариации регистра не трогаются.
func normalizeMethod(m string) string {
	switch {
	case strings.Contains(m, "ssh"):
		return "SSH"
	case strings.Contains(m, "sftp"):
		return "SFTP"
	case strings.Contains(m, "telnet"):
		return "Telnet"
	case strings.Contains(m, "ftp"):
		return "FTP"
	case strings.HasSuffix(m, "cu"):
		return "Serial (cu)"
	case strings.Contains(m, "remote-tty"):
		return "Serial (remote-tty)"
	case strings.Contains(m, "3270"):
		return "IBM 3270/5250"
	case strings.Contains(m, "rdesktop"), m == "RDP (Windows)":
		// M37-ревью MINOR-3: сырые «rdesktop»/«xfreerdp» приводим к канону —
		// иначе ui.embeddedKind не узнаёт метод и соединение уедет во внешний
		// процесс в PTY вместо встроенного клиента.
		return "RDP (rdesktop)"
	case strings.Contains(m, "xfreerdp"):
		return "RDP (xfreerdp)"
	case strings.Contains(m, "vncviewer"):
		return "VNC"
	case strings.Contains(m, "generic"):
		return "Generic Command"
	}
	return m
}

var identityFileRe = regexp.MustCompile(`(?i)\s+\-o\s+"IdentityFile(\s+|\s*=\s*)(.+)"`)

func extractIdentityFile(env *Environment) {
	m := identityFileRe.FindStringSubmatch(env.Options)
	if m == nil {
		return
	}
	env.AuthType = "publickey"
	env.PublicKey = m[2]
	if env.PassphraseUser == "" {
		env.PassphraseUser = env.User
	}
	if env.Passphrase == "" {
		env.Passphrase = env.Pass
	}
	env.Options = identityFileRe.ReplaceAllString(env.Options, "")
}

// migrateScreenshot (как Perl): ключ `screenshot` удаляется всегда; миграция
// в screenshots[] — только для строкового пути существующего файла и только
// при полном ОТСУТСТВИИ screenshots (nil, не пустой список).
func migrateScreenshot(env *Environment) {
	v, ok := env.Raw["screenshot"]
	if !ok {
		return
	}
	delete(env.Raw, "screenshot")
	if env.Screenshots != nil {
		return
	}
	path, ok := v.(string)
	if !ok {
		return
	}
	// Легаси требует обычный файл (-f): каталоги os.Stat тоже пропускает,
	// но в screenshots они не годятся. os.Stat идёт по симлинку → файл ок.
	if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
		env.Screenshots = []any{path}
	}
}
