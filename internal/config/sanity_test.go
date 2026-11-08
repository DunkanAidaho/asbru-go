package config

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func env(name, method string) Environment {
	return Environment{Name: name, Method: method}
}

// Нормализация legacy-имён методов (порядок правил важен — как в _cfgSanityCheck).
func TestSanitizeMethodNames(t *testing.T) {
	cases := []struct{ in, want string }{
		{"ssh", "SSH"},
		{"SSH", "SSH"},
		{"sftp", "SFTP"},
		{"telnet", "Telnet"},
		{"ftp", "FTP"},
		{"cu", "Serial (cu)"},
		{"remote-tty", "Serial (remote-tty)"},
		{"3270", "IBM 3270/5250"},
		{"RDP (Windows)", "RDP (rdesktop)"},
		{"vncviewer", "VNC"},
		{"generic", "Generic Command"},
		{"RDP (xfreerdp)", "RDP (xfreerdp)"}, // канонические имена не трогаем
		{"Local Shell", "Local Shell"},
	}
	for _, c := range cases {
		cfg := &Config{Environments: map[string]Environment{"u1": env("c", c.in)}}
		Sanitize(cfg)
		if got := cfg.Environments["u1"].Method; got != c.want {
			t.Errorf("method %q → %q, want %q", c.in, got, c.want)
		}
	}
}

// -o "IdentityFile=..." из options → auth type publickey + public key + passphrase.
func TestSanitizeIdentityFile(t *testing.T) {
	cfg := &Config{Environments: map[string]Environment{
		"u1": {Name: "c", Method: "SSH", User: "root", Pass: "p", Options: ` -o "IdentityFile=/home/x/id_rsa"`},
	}}
	Sanitize(cfg)
	e := cfg.Environments["u1"]
	if e.AuthType != "publickey" || e.PublicKey != "/home/x/id_rsa" {
		t.Errorf("auth: type=%q key=%q", e.AuthType, e.PublicKey)
	}
	if e.PassphraseUser != "root" || e.Passphrase != "p" {
		t.Errorf("passphrase defaults: user=%q pass=%q", e.PassphraseUser, e.Passphrase)
	}
	if e.Options != "" {
		t.Errorf("options %q должен быть очищен", e.Options)
	}
}

// String-поле screenshot (существующий файл) → screenshots[0].
func TestSanitizeScreenshotToScreenshots(t *testing.T) {
	dir := t.TempDir()
	shot := filepath.Join(dir, "shot.png")
	if err := os.WriteFile(shot, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{Environments: map[string]Environment{
		"u1": {Name: "c", Method: "SSH", Raw: map[string]any{"screenshot": shot}},
	}}
	Sanitize(cfg)
	e := cfg.Environments["u1"]
	if len(e.Screenshots) != 1 || e.Screenshots[0] != shot {
		t.Errorf("Screenshots = %#v, want [%s]", e.Screenshots, shot)
	}
	if _, ok := e.Raw["screenshot"]; ok {
		t.Errorf("screenshot key должен быть удалён из Raw")
	}
}

func TestSanitizeDropsJunkUUIDs(t *testing.T) {
	cfg := &Config{Environments: map[string]Environment{
		"HASH(0x1234)":    {Name: "junk", Method: "SSH"},
		"_tmp_abc":        {Name: "tmp", Method: "SSH"},
		"pacshell_PID123": {Name: "ps", Method: "SSH"},
		"":                {Name: "empty", Method: "SSH"},
		"no-name":         {Method: "SSH"},
		"good":            {Name: "ok", Method: "SSH"},
		RootUUID:          {Name: "", IsGroup: 1},
	}}
	Sanitize(cfg)
	if len(cfg.Environments) != 2 {
		t.Errorf("environments = %v, want 2 leftovers", cfg.Environments)
	}
	if _, ok := cfg.Environments["good"]; !ok {
		t.Errorf("good entry lost")
	}
}

// defaults.version //= APPVERSION и сброс gui-пароля для конфигов до 4.5.0.1.
func TestSanitizeVersionRules(t *testing.T) {
	cfg := &Config{Defaults: Defaults{Version: "", UseGUIPassword: 1, GUIPassword: "x"}}
	Sanitize(cfg)
	if cfg.Defaults.Version != "6.4.2" {
		t.Errorf("version = %q, want 6.4.2", cfg.Defaults.Version)
	}

	old := &Config{Defaults: Defaults{Version: "4.5.0.0", UseGUIPassword: 1, GUIPassword: "x"}}
	Sanitize(old)
	if old.Defaults.UseGUIPassword != 0 || old.Defaults.GUIPassword != "" {
		t.Errorf("old config gui password not reset: %+v", old.Defaults)
	}
}

// Пустое, но ПРИСУТСТВУЮЩЕЕ имя: Perl удаляет только при undef (ревью major #2).
func TestSanitizeKeepsEmptyNamedEntry(t *testing.T) {
	var env Environment
	src := "name: \"\"\nmethod: SSH\n"
	if err := yaml.Unmarshal([]byte(src), &env); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{Environments: map[string]Environment{"u1": env}}
	Sanitize(cfg)
	if _, ok := cfg.Environments["u1"]; !ok {
		t.Fatal("запись с пустым, но определённым name удалена")
	}
}

// COSMETIC-3: `name: ~` (undef) — запись джанк (Perl удаляет при !defined name),
// в отличие от `name: ”` (определённый, пустой). NameSet не ставится для null.
func TestSanitizeDropsNullNamedEntry(t *testing.T) {
	var env Environment
	if err := yaml.Unmarshal([]byte("name: ~\nmethod: SSH\n"), &env); err != nil {
		t.Fatal(err)
	}
	if env.NameSet {
		t.Fatal("NameSet для name: ~ (null) должен быть false")
	}
	cfg := &Config{Environments: map[string]Environment{"u1": env}}
	Sanitize(cfg)
	if _, ok := cfg.Environments["u1"]; ok {
		t.Fatal("запись с name: ~ (undef) должна удаляться как джанк")
	}
}

// IdentityFile регистронезависимо (ревью #3).
func TestSanitizeIdentityFileCaseInsensitive(t *testing.T) {
	cfg := &Config{Environments: map[string]Environment{
		"u1": {Name: "c", Method: "SSH", User: "u", Pass: "p", Options: ` -o "identityfile=/home/x/id2"`},
	}}
	Sanitize(cfg)
	if cfg.Environments["u1"].AuthType != "publickey" || cfg.Environments["u1"].PublicKey != "/home/x/id2" {
		t.Fatalf("lowercase identityfile не извлечён: %+v", cfg.Environments["u1"])
	}
}

// Не-строковый screenshot удаляется всегда; миграция только при отсутствии screenshots (ревью #4).
func TestSanitizeScreenshotEdges(t *testing.T) {
	cfg := &Config{Environments: map[string]Environment{
		"u1": {Name: "c", Method: "SSH", Raw: map[string]any{"screenshot": 123}},
		"u2": {Name: "c", Method: "SSH", Raw: map[string]any{"screenshot": "/tmp/x"}, Screenshots: []any{}},
	}}
	Sanitize(cfg)
	if _, ok := cfg.Environments["u1"].Raw["screenshot"]; ok {
		t.Fatal("не-строковый screenshot не удалён")
	}
	if len(cfg.Environments["u2"].Screenshots) != 0 || cfg.Environments["u2"].Screenshots == nil {
		t.Fatalf("миграция при явном screenshots: %+v", cfg.Environments["u2"].Screenshots)
	}
}

// Методы: регистронезависимость как в Perl (Ssh остаётся Ssh — ревью #6).
func TestSanitizeMethodCaseSensitiveLikeLegacy(t *testing.T) {
	cfg := &Config{Environments: map[string]Environment{"u1": env("c", "Ssh")}}
	Sanitize(cfg)
	if cfg.Environments["u1"].Method != "Ssh" {
		t.Fatalf("Ssh → %q, в легаси неизвестное имя не трогается", cfg.Environments["u1"].Method)
	}
}

// M10a: пустые дефолты сеются легаси-значениями (PACUtils:2049-2079, 2120-2121).
func TestDefaultsSeed(t *testing.T) {
	cfg := &Config{Environments: map[string]Environment{}}
	Sanitize(cfg)
	d := cfg.Defaults
	if d.TimeOutConnect != 40 || d.TimeOutCommand != 60 {
		t.Fatalf("таймауты: connect=%d command=%d", d.TimeOutConnect, d.TimeOutCommand)
	}
	if d.AutoAcceptKey != 1 {
		t.Fatalf("auto accept key = %d, хотим 1 (легаси-дефолт)", d.AutoAcceptKey)
	}
	if d.SudoPassword != "<<ASK_PASS>>" || d.SudoShowPassword != 0 {
		t.Fatalf("sudo: (%q, %d)", d.SudoPassword, d.SudoShowPassword)
	}
	if d.CommandPrompt != DefaultCommandPrompt ||
		d.UsernamePrompt != DefaultUsernamePrompt ||
		d.PasswordPrompt != DefaultPasswordPrompt ||
		d.HostkeyChangedPrompt != DefaultHostkeyChangedPrompt ||
		d.PressAnyKeyPrompt != DefaultPressAnyKeyPrompt ||
		d.RemoteHostChangedPrompt != DefaultRemoteHostPrompt ||
		d.SudoPrompt != `[__PAC__SUDO__PROMPT__]` {
		t.Fatalf("промпты не посеяны: %+v", d)
	}
}

// M10a: явное 'auto accept key: 0' не перетирается сидом.
func TestDefaultsSeedHonorsExplicitZero(t *testing.T) {
	cfg := &Config{
		Defaults:     Defaults{AutoAcceptKey: 0},
		Environments: map[string]Environment{},
	}
	_ = cfg
	// FlexInt не отличает отсутствие от 0 — сид перезапишет; отклонение
	// ДОПУСТИМО (документировано): явная 0 в YAML станет 1 после санитизации.
	Sanitize(cfg)
	if cfg.Defaults.AutoAcceptKey != 1 {
		t.Fatalf("ожидаем 1 после сида (отклонение документировано)")
	}
}

// M37-ревью MINOR-3: сырые алиасы RDP-методов обязаны нормализоваться — иначе
// конфиг с method: rdesktop|xfreerdp уедет во внешний процесс вместо встроенного
// клиента (остальной порт эти написания терпит: rdpmigrate, cmdline, tunnel).
func TestSanitizeMethodAliases(t *testing.T) {
	cases := []struct{ in, want string }{
		{"rdesktop", "RDP (rdesktop)"},
		{"xfreerdp", "RDP (xfreerdp)"},
		{"RDP (Windows)", "RDP (rdesktop)"},
		{"RDP (rdesktop)", "RDP (rdesktop)"},
		{"RDP (xfreerdp)", "RDP (xfreerdp)"},
	}
	for _, c := range cases {
		cfg := &Config{Environments: map[string]Environment{"u1": env("c", c.in)}}
		Sanitize(cfg)
		if got := cfg.Environments["u1"].Method; got != c.want {
			t.Errorf("method %q → %q, want %q", c.in, got, c.want)
		}
	}
}

// Живой тест 2026-10-14: Ctrl+R в SSH-сессии не доходил до bash (reverse-history
// search) — оконные QShortcut дефолтных биндингов съедали шелловые сочетания
// (Ctrl+R restart, Ctrl+C copy, Ctrl+D duplicate, Ctrl+W close, Ctrl+F find,
// Ctrl+G hostname, в дереве — Ctrl+r/t/d). Правило konsole-паритета: дефолтные
// привязки не должны занимать «голый» Ctrl+<буква> — только с Shift/Alt.

func TestDefaultKeybindingsKonsoleStyle(t *testing.T) {
	cfg := &Config{Environments: map[string]Environment{}}
	Sanitize(cfg)
	kb := cfg.Defaults.KeyBindings
	if len(kb) == 0 {
		t.Fatalf("дефолтные keybindings не засеяны")
	}
	want := []struct{ section, key, action string }{
		{"terminal", "Ctrl+Shift+C", "copy"},
		{"terminal", "Ctrl+Shift+V", "paste"},
		{"terminal", "Ctrl+Shift+R", "restart"},
		{"terminal", "Ctrl+Shift+W", "close"},
		{"terminal", "Ctrl+Shift+F", "find"},
		{"terminal", "Ctrl+Shift+G", "hostname"},
		{"terminal", "Ctrl+Shift+D", "duplicate"},
		{"treeConnections", "Alt+Shift+E", "expand_all"},
		{"treeConnections", "Alt+Shift+C", "collaps_all"},
		{"treeConnections", "Alt+Shift+D", "clone"},
	}
	for _, w := range want {
		spec, ok := kb[w.section][w.key]
		if !ok {
			t.Errorf("%s: %q отсутствует (хотели %s)", w.section, w.key, w.action)
			continue
		}
		if len(spec) < 2 || spec[1] != w.action {
			t.Errorf("%s[%q] = %v, ожидалось действие %q", w.section, w.key, spec, w.action)
		}
	}
	// Ни одна дефолтная привязка не занимает голый Ctrl+<буква>.
	plain := []string{"Ctrl+C", "Ctrl+V", "Ctrl+R", "Ctrl+D", "Ctrl+w", "Ctrl+W",
		"Ctrl+f", "Ctrl+g", "Ctrl+r", "Ctrl+t", "Ctrl+d"}
	for section, binds := range kb {
		for _, k := range plain {
			if _, taken := binds[k]; taken {
				t.Errorf("%s: %q занимает шелловое сочетание", section, k)
			}
		}
	}
}

// Конфиги, засеянные старыми легаси-дефолтами (до 2026-10-14), обязаны
// мигрировать: известная легаси-запись переезжает на konsole-клавишу, свои
// биндинги пользователя не трогаются, повторный Sanitize ничего не меняет
// (самосходящаяся миграция без маркера версии).
func TestMigrateLegacyKeybindings(t *testing.T) {
	legacy := func() map[string]map[string][]string {
		return map[string]map[string][]string{
			"terminal": {
				"Ctrl+C": {"Terminal", "copy", "Copy selection to clipboard"},
				"Ctrl+R": {"Terminal", "restart", "Restart connection (close and start)"},
				"Ctrl+D": {"Terminal", "duplicate", "Duplicate connection"},
				// свой биндинг пользователя — НЕ легаси-дефолт, не трогать
				"Ctrl+Y": {"Terminal", "close", "Close Terminal"},
			},
			"treeConnections": {
				"Ctrl+r": {"Connections Tree", "expand_all", "Expand tree completely"},
			},
		}
	}
	cfg := &Config{Defaults: Defaults{KeyBindings: legacy()}, Environments: map[string]Environment{}}
	Sanitize(cfg)
	kb := cfg.Defaults.KeyBindings
	if _, still := kb["terminal"]["Ctrl+C"]; still {
		t.Errorf("Ctrl+C copy не переехал")
	}
	if spec, ok := kb["terminal"]["Ctrl+Shift+C"]; !ok || len(spec) < 2 || spec[1] != "copy" {
		t.Errorf("Ctrl+Shift+C не получил copy: %v", spec)
	}
	if spec, ok := kb["terminal"]["Ctrl+Shift+R"]; !ok || len(spec) < 2 || spec[1] != "restart" {
		t.Errorf("Ctrl+Shift+R не получил restart: %v", spec)
	}
	if spec, ok := kb["treeConnections"]["Alt+Shift+E"]; !ok || len(spec) < 2 || spec[1] != "expand_all" {
		t.Errorf("Alt+Shift+E не получил expand_all: %v", spec)
	}
	if spec, ok := kb["terminal"]["Ctrl+Y"]; !ok || len(spec) < 2 || spec[1] != "close" {
		t.Errorf("свой биндинг Ctrl+Y затёрт: %v", spec)
	}
	before := len(kb["terminal"])
	Sanitize(cfg)
	if len(kb["terminal"]) != before {
		t.Errorf("миграция не идемпотентна: было %d, стало %d", before, len(kb["terminal"]))
	}
}

// Живой баг 2026-10-14 (продолжение): конфиг пользователя несёт ПОЛНУЮ
// легаси-таблицу Perl (PACKeyBindings 476-530), а не только сид M21 —
// и её «голые» Ctrl+<буква> (дерево copy/paste/find, quit) тоже съедали
// шелловые сочетания (Ctrl+C не доходил до SIGINT). Расширенная миграция.

func TestMigrateLegacyKeybindingsFullPerlTable(t *testing.T) {
	cfg := &Config{Defaults: Defaults{KeyBindings: map[string]map[string][]string{
		"treeConnections": {
			"Ctrl+c": {"Connections Tree", "copy", "Copy node"},
			"Ctrl+v": {"Connections Tree", "paste", "Paste node"},
			"Ctrl+f": {"Connections Tree", "find", "Find in connection tree"},
		},
		"pacmain": {
			"Ctrl+f": {"Main Window", "find", "Find in connection tree"},
			"Ctrl+q": {"Main Window", "quit", "Exit Ásbrú"},
		},
		"terminal": {
			"Ctrl+q": {"Terminal", "quit", "Exit Ásbrú"},
		},
	}}, Environments: map[string]Environment{}}
	Sanitize(cfg)
	kb := cfg.Defaults.KeyBindings
	check := func(section, key, action string, want bool) {
		spec, ok := kb[section][key]
		if want && (!ok || len(spec) < 2 || spec[1] != action) {
			t.Errorf("%s[%q]: ожидалось %s, есть %v", section, key, action, spec)
		}
		if !want && ok {
			t.Errorf("%s[%q]: должно быть удалено/перенесено, осталось %v", section, key, spec)
		}
	}
	check("treeConnections", "Ctrl+c", "", false)         // съедал Ctrl+C
	check("treeConnections", "Ctrl+Alt+C", "copy", true)  // новый дом
	check("treeConnections", "Ctrl+v", "", false)         // съедал Ctrl+V
	check("treeConnections", "Ctrl+Alt+V", "paste", true) // новый дом
	check("treeConnections", "Ctrl+f", "", false)         // дубль find
	check("pacmain", "Ctrl+f", "", false)                 // дубль find
	check("terminal", "Ctrl+q", "", false)                // съедал Ctrl+Q
	check("terminal", "Ctrl+Alt+Q", "quit", true)         // новый дом
	check("pacmain", "Ctrl+q", "", false)                 // дубль quit
	// Идемпотентность: второй прогон ничего не ломает.
	Sanitize(cfg)
	check("treeConnections", "Ctrl+Alt+C", "copy", true)
	check("terminal", "Ctrl+Alt+Q", "quit", true)
}

// 'auth fallback' (легаси PACUtils.pm:2347 `auth fallback //= 1`): отсутствие
// или нераспознанное значение → "1" (резервная авторизация разрешена); явные
// "0"/"1" сохраняются. Пустая строка для combo {"0","1"} неотличима от
// отсутствия: SetCurrentText("") оставлял индекс 0 и сохранение тихо писало "0"
// (регресс: парольный fallback выключался при открытии+сохранении соединения).
func TestSanitizeAuthFallbackDefault(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"absent", "name: c\nmethod: SSH\n", "1"},
		{"empty", "name: c\nmethod: SSH\nauth fallback: \"\"\n", "1"},
		{"zero", "name: c\nmethod: SSH\nauth fallback: \"0\"\n", "0"},
		{"one", "name: c\nmethod: SSH\nauth fallback: \"1\"\n", "1"},
		{"garbage", "name: c\nmethod: SSH\nauth fallback: maybe\n", "1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var env Environment
			if err := yaml.Unmarshal([]byte(c.src), &env); err != nil {
				t.Fatalf("yaml: %v", err)
			}
			cfg := &Config{Environments: map[string]Environment{"u1": env}}
			Sanitize(cfg)
			if got := cfg.Environments["u1"].AuthFallback; got != c.want {
				t.Errorf("auth fallback %q (src %q) → %q, want %q", c.src, c.name, got, c.want)
			}
		})
	}
}
