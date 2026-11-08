package config

import (
	"gopkg.in/yaml.v3"
	"path/filepath"
	"reflect"
	"testing"
)

const (
	connUUID  = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	groupUUID = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
)

func loadFixture(t *testing.T) *Config {
	t.Helper()
	cfg, err := LoadConfig(filepath.Join("fixtures", "legacy.yml"), NewCBC())
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	return cfg
}

// Parse фикстуры и расшифровка известных полей.
func TestLoadLegacyFixture(t *testing.T) {
	cfg := loadFixture(t)

	if cfg.Defaults.Version != "999.0.0-test" {
		t.Errorf("Defaults.Version = %q", cfg.Defaults.Version)
	}
	if cfg.ConfigVersion != 2 {
		t.Errorf("ConfigVersion = %d", cfg.ConfigVersion)
	}
	if cfg.Defaults.GUIPassword != "" || cfg.Defaults.SudoPassword != "" || cfg.Defaults.Keepass.Password != "" {
		t.Errorf("encrypted defaults did not decrypt to empty: %q %q %q",
			cfg.Defaults.GUIPassword, cfg.Defaults.SudoPassword, cfg.Defaults.Keepass.Password)
	}
	if got := cfg.Defaults.UnknownDefaultKey(); got != "keep-me" {
		t.Errorf("unknown defaults key lost: %v", got)
	}

	env, ok := cfg.Environments[connUUID]
	if !ok {
		t.Fatalf("connection %s not found", connUUID)
	}
	if env.Name != "Test SSH" || env.IP != "10.0.0.1" || env.User != "root" || env.Method != "SSH" {
		t.Errorf("bad identity: %+v", env)
	}
	if env.Pass != "hunter2" || env.Passphrase != "" {
		t.Errorf("env secrets not decrypted: pass=%q passphrase=%q", env.Pass, env.Passphrase)
	}
	if env.Port != "22" {
		t.Errorf("Port = %v (must stay string)", env.Port)
	}
	if env.Options != " -o ServerAliveInterval=5 -g 1920x1080" {
		t.Errorf("Options = %q", env.Options)
	}
	if env.TerminalOptions["change background color on idle"] != 0 {
		t.Errorf("TerminalOptions = %#v", env.TerminalOptions)
	}
	if env.PublicKey != "/home/test/.ssh/id_rsa" || env.AuthType != "publickey" {
		t.Errorf("auth fields: %q %q", env.PublicKey, env.AuthType)
	}
	if got := env.Raw["unknown-conn-key"]; got != "also-kept" {
		t.Errorf("unknown conn key lost: %v", got)
	}

	grp, ok := cfg.Environments[groupUUID]
	if !ok {
		t.Fatalf("group %s not found", groupUUID)
	}
	if grp.IsGroup != 1 {
		t.Errorf("IsGroup = %d", grp.IsGroup)
	}
	if grp.Children[connUUID] != 1 {
		t.Errorf("Children = %v", grp.Children)
	}

	if _, ok := cfg.Environments[shellUUID]; !ok {
		t.Fatalf("%s entry must parse (dropped only on save)", shellUUID)
	}
}

// Сохранение атомарно пишет и удаляет __PAC_SHELL__.
func TestSaveDropsPACShell(t *testing.T) {
	cfg := loadFixture(t)
	out := filepath.Join(t.TempDir(), "asbru.yml")
	if err := cfg.Save(out, NewCBC()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	reloaded, err := LoadConfig(out, NewCBC())
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if _, ok := reloaded.Environments[shellUUID]; ok {
		t.Errorf("%s survived save", shellUUID)
	}
	if reloaded.Environments[connUUID].Pass != "hunter2" {
		t.Errorf("pass lost after save/reload")
	}
}

// emptyToNil приводит nil-контейнеры к пустым: запись материализует их
// (`terminal options: {}`, `variables: []`), семантически это одно и то же.
func emptyToNil(c *Config) *Config {
	n := *c
	n.Defaults.Raw = nilOr(c.Defaults.Raw)
	n.Raw = nilOr(c.Raw)
	n.Tmp = nilOr(c.Tmp)
	n.Defaults.GlobalVariables = nilOrVars(c.Defaults.GlobalVariables)
	envs := make(map[string]Environment, len(c.Environments))
	for k, v := range c.Environments {
		v.Children = nilOrInt(v.Children)
		v.TerminalOptions = nilOr(v.TerminalOptions)
		v.Raw = nilOr(v.Raw)
		for i := range v.Variables {
			v.Variables[i] = nilOr(v.Variables[i])
		}
		for i := range v.Expect {
			v.Expect[i] = nilOr(v.Expect[i])
		}
		if len(v.Variables) == 0 {
			v.Variables = nil
		}
		if len(v.Expect) == 0 {
			v.Expect = nil
		}
		if len(v.Screenshots) == 0 {
			v.Screenshots = nil
		}
		if len(v.LocalBefore) == 0 {
			v.LocalBefore = nil
		}
		if len(v.LocalConnected) == 0 {
			v.LocalConnected = nil
		}
		if len(v.Macros) == 0 {
			v.Macros = nil
		}
		if len(v.LocalAfter) == 0 {
			v.LocalAfter = nil
		}
		envs[k] = v
	}
	n.Environments = envs
	return &n
}

func nilOr[T ~map[string]any](m T) T {
	if len(m) == 0 {
		return nil
	}
	return m
}

func nilOrInt(m map[string]FlexInt) map[string]FlexInt {
	if len(m) == 0 {
		return nil
	}
	return m
}

func nilOrVars(m map[string]VarEntry) map[string]VarEntry {
	if len(m) == 0 {
		return nil
	}
	return m
}

// Сохранение и повторная загрузка не меняют содержимое (кроме __PAC_SHELL__).
func TestRoundTripStable(t *testing.T) {
	cfg := loadFixture(t)
	out := filepath.Join(t.TempDir(), "asbru.yml")
	if err := cfg.Save(out, NewCBC()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	reloaded, err := LoadConfig(out, NewCBC())
	if err != nil {
		t.Fatalf("reload: %v", err)
	}

	want := emptyToNil(cfg.WithoutShell())
	got := emptyToNil(reloaded)
	// seen — внутренняя бухгалтерия присутствия ключей, не состояние YAML
	clear(want.Defaults.seen)
	clear(got.Defaults.seen)
	if !reflect.DeepEqual(want, got) {
		t.Errorf("round-trip mismatch:\nwant %#v\ngot  %#v", want, got)
	}
}

// Строковые поля, «похожие на числа», не превращаются в числа.
func TestStringNumbersStayStrings(t *testing.T) {
	cfg := loadFixture(t)
	out := filepath.Join(t.TempDir(), "asbru.yml")
	if err := cfg.Save(out, NewCBC()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	reloaded, err := LoadConfig(out, NewCBC())
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	env := reloaded.Environments[connUUID]
	if env.Port != "22" || env.Options != " -o ServerAliveInterval=5 -g 1920x1080" {
		t.Errorf("numeric-looking strings changed type/content: %q %q", env.Port, env.Options)
	}
}

// M20: cluster — массив имён (легаси PACUtils.pm:2393); скаляр/0/пусто
// не роняют загрузку (толерантный разбор), круг — последовательность.
func TestClusterNamesYAML(t *testing.T) {
	var e Environment
	if err := yaml.Unmarshal([]byte("cluster: [prod, staging]"), &e); err != nil {
		t.Fatal(err)
	}
	if len(e.Cluster) != 2 || e.Cluster[0] != "prod" || e.Cluster[1] != "staging" {
		t.Fatalf("list: %v", e.Cluster)
	}
	for _, in := range []string{"cluster: 0", "cluster: ''", "cluster: ~"} {
		var e2 Environment
		if err := yaml.Unmarshal([]byte(in), &e2); err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if len(e2.Cluster) != 0 {
			t.Fatalf("%s: → %v", in, e2.Cluster)
		}
	}
	var e3 Environment
	if err := yaml.Unmarshal([]byte("cluster: prod"), &e3); err != nil {
		t.Fatal(err)
	}
	if len(e3.Cluster) != 1 || e3.Cluster[0] != "prod" {
		t.Fatalf("scalar: %v", e3.Cluster)
	}
}

// M24: defaults 'max retry on disconnect' (легаси PACUtils.pm:2053, дефолт 50).
func TestMaxRetryOnDisconnect(t *testing.T) {
	var d Defaults // пустой → дефолт
	if got := d.MaxRetryOnDisconnect(); got != 50 {
		t.Fatalf("пустой defaults: %d, хотим 50", got)
	}
	d.Raw = map[string]any{"max retry on disconnect": 7}
	if got := d.MaxRetryOnDisconnect(); got != 7 {
		t.Fatalf("число 7: %d", got)
	}
	d.Raw = map[string]any{"max retry on disconnect": "12"}
	if got := d.MaxRetryOnDisconnect(); got != 12 {
		t.Fatalf(`строка "12": %d`, got)
	}
	d.Raw = map[string]any{"max retry on disconnect": -3}
	if got := d.MaxRetryOnDisconnect(); got != 50 {
		t.Fatalf("отрицательное: %d, хотим 50", got)
	}
}

// бэклог: GroupConnections — сбор потомков-подключений группы (для «двойного клика
// по группе» и «Start Group»).
func TestGroupConnections(t *testing.T) {
	cfg := &Config{Environments: map[string]Environment{
		"g1": {Name: "Group", IsGroup: 1, Children: map[string]FlexInt{"c1": 1, "g2": 1, "g3": 1}},
		"g2": {Name: "Sub", IsGroup: 1, Children: map[string]FlexInt{"c2": 1}},
		"g3": {Name: "Empty", IsGroup: 1, Children: map[string]FlexInt{}},
		"c1": {Name: "Conn1", IP: "h1"},
		"c2": {Name: "Conn2", IP: "h2"},
	}}
	// вложенные группы + сортировка.
	if got, want := cfg.GroupConnections("g1"), []string{"c1", "c2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("GroupConnections(g1) = %v, хотим %v", got, want)
	}
	// root — сам подключение → сам.
	if got, want := cfg.GroupConnections("c1"), []string{"c1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("GroupConnections(c1) = %v, хотим %v", got, want)
	}
	// пустая группа → пусто.
	if got := cfg.GroupConnections("g3"); len(got) != 0 {
		t.Fatalf("GroupConnections(g3) = %v, хотим пусто", got)
	}
	// неизвестный root → пусто.
	if got := cfg.GroupConnections("nope"); len(got) != 0 {
		t.Fatalf("GroupConnections(nope) = %v, хотим пусто", got)
	}
}

// бэклог: защита от цикла A→B→A / самоссылки в GroupConnections (не должна виснуть).
func TestGroupConnectionsCycle(t *testing.T) {
	cfg := &Config{Environments: map[string]Environment{
		"g1": {Name: "A", IsGroup: 1, Children: map[string]FlexInt{"g1": 1, "g2": 1, "c1": 1}},
		"g2": {Name: "B", IsGroup: 1, Children: map[string]FlexInt{"g1": 1}},
		"c1": {Name: "Conn1", IP: "h1"},
	}}
	got := cfg.GroupConnections("g1")
	if !reflect.DeepEqual(got, []string{"c1"}) {
		t.Fatalf("GroupConnections(g1) при цикле = %v, хотим [c1]", got)
	}
}

// бэклог: defaults 'terminal show status bar' (строка состояния терминала).
func TestShowStatusBar(t *testing.T) {
	var d Defaults
	if !d.ShowStatusBar() {
		t.Fatal("пустой defaults → true (легаси-дефолт //=1)")
	}
	d.Raw = map[string]any{"terminal show status bar": 1}
	if !d.ShowStatusBar() {
		t.Fatalf("число 1 → true")
	}
	d.Raw = map[string]any{"terminal show status bar": "1"}
	if !d.ShowStatusBar() {
		t.Fatalf(`строка "1" → true`)
	}
	d.Raw = map[string]any{"terminal show status bar": 0}
	if d.ShowStatusBar() {
		t.Fatalf("число 0 → false")
	}
	d.Raw = map[string]any{"terminal show status bar": true}
	if !d.ShowStatusBar() {
		t.Fatalf("bool true → true")
	}
}

// бэклог: defaults 'close terminal on disconnect' (легаси PACUtils.pm:2052, дефолт ”=выкл).
func TestCloseTerminalOnDisconnect(t *testing.T) {
	var d Defaults
	if d.CloseTerminalOnDisconnect() {
		t.Fatal("пустой defaults → false (выкл)")
	}
	d.Raw = map[string]any{"close terminal on disconnect": 1}
	if !d.CloseTerminalOnDisconnect() {
		t.Fatal("число 1 → true")
	}
	d.Raw = map[string]any{"close terminal on disconnect": "1"}
	if !d.CloseTerminalOnDisconnect() {
		t.Fatal(`строка "1" → true`)
	}
	d.Raw = map[string]any{"close terminal on disconnect": 0}
	if d.CloseTerminalOnDisconnect() {
		t.Fatal("число 0 → false")
	}
	d.Raw = map[string]any{"close terminal on disconnect": ""}
	if d.CloseTerminalOnDisconnect() {
		t.Fatal(`пустая строка → false`)
	}
}

// бэклог Incognito: мост легаси 'connection options.randomSocksTunnel' → EnableRandomSocks.
func TestEnvironmentRandomSocksLegacyBridge(t *testing.T) {
	var e Environment
	if err := yaml.Unmarshal([]byte("name: t\nconnection options:\n  randomSocksTunnel: true\n"), &e); err != nil {
		t.Fatal(err)
	}
	if e.EnableRandomSocks != 1 {
		t.Fatalf("EnableRandomSocks (легаси-мост) = %d, хотим 1", e.EnableRandomSocks)
	}
	// плоский (Go-канон) ключ тоже работает.
	var e2 Environment
	if err := yaml.Unmarshal([]byte("name: t\ncreate random socks tunnel: 1\n"), &e2); err != nil {
		t.Fatal(err)
	}
	if e2.EnableRandomSocks != 1 {
		t.Fatalf("EnableRandomSocks (плоский) = %d, хотим 1", e2.EnableRandomSocks)
	}
}

// бэклог Incognito: плоский Go-ключ приоритетнее легаси-моста — «снятая галка»
// (EnableRandomSocks=0) держится после marshap→unmarshal, несмотря на legacy
// connection options.randomSocksTunnel=true.
func TestEnvironmentRandomSocksFlatBeatsLegacy(t *testing.T) {
	// легаси-мост срабатывает, когда плоского ключа нет.
	var e Environment
	if err := yaml.Unmarshal([]byte("name: t\nconnection options:\n  randomSocksTunnel: true\n"), &e); err != nil {
		t.Fatal(err)
	}
	if e.EnableRandomSocks != 1 {
		t.Fatalf("мост: %d, хотим 1", e.EnableRandomSocks)
	}
	// юзер снял галку → плоский ключ 0; перезагрузка: 0 остаётся.
	e.EnableRandomSocks = 0
	data, err := yaml.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var e2 Environment
	if err := yaml.Unmarshal(data, &e2); err != nil {
		t.Fatal(err)
	}
	if e2.EnableRandomSocks != 0 {
		t.Fatalf("после round-trip снятой галки: %d, хотим 0 (yaml: %s)", e2.EnableRandomSocks, data)
	}
}

// Batch A hotkeys: сид-дефолт keybindings включает дерево-действия.
// (2026-10-14: клавиши переведены на konsole-паритет — «голый» Ctrl+<буква>
// не занимает шелловые сочетания; см. TestMigrateLegacyKeybindings.)
func TestSeedKeybindingsTreeBatchA(t *testing.T) {
	var cfg Config
	Sanitize(&cfg)
	k := cfg.Defaults.KeyBindings["treeConnections"]
	if k == nil {
		t.Fatal("нет treeConnections keybindings")
	}
	for seq := range map[string]bool{"Alt+Shift+E": true, "Alt+Shift+C": true, "Alt+Shift+D": true, "Alt+c": true, "Alt+r": true, "F2": true} {
		if _, ok := k[seq]; !ok {
			t.Errorf("нет сид-привязки %s", seq)
		}
	}
}

// Batch B hotkeys: сид-дефолт keybindings терминала включает копи/вставку/закрыть/...
// (2026-10-14: konsole-паритет — Ctrl+Shift+*.)
func TestSeedKeybindingsTerminalBatchB(t *testing.T) {
	var cfg Config
	Sanitize(&cfg)
	k := cfg.Defaults.KeyBindings["terminal"]
	if k == nil {
		t.Fatal("нет terminal keybindings")
	}
	for seq := range map[string]bool{"Ctrl+Shift+C": true, "Ctrl+Shift+V": true, "Ctrl+Shift+W": true,
		"Ctrl+Alt+W": true, "Ctrl+Shift+F": true, "CtrlShift+F4": true, "Ctrl+Shift+D": true,
		"Ctrl+Shift+R": true, "Ctrl+Shift+G": true, "Alt+e": true, "AltCtrl+s": true} {
		if _, ok := k[seq]; !ok {
			t.Errorf("нет сид-привязки terminal %s", seq)
		}
	}
}

// VNC security: встроенный VNC — плоский ключ 'vnc security' (VeNCrypt-режим).
// Parse, round-trip и отсутствие в Raw (известный ключ).
func TestEnvironmentVNCEncryption(t *testing.T) {
	var e Environment
	if err := yaml.Unmarshal([]byte("name: t\nmethod: VNC\nvnc security: vencrypt\n"), &e); err != nil {
		t.Fatal(err)
	}
	if e.VNCEncryption != "vencrypt" {
		t.Fatalf("VNCEncryption = %q, хотим vencrypt", e.VNCEncryption)
	}
	if _, ok := e.Raw["vnc security"]; ok {
		t.Fatalf("'vnc security' попал в Raw (известный ключ): %#v", e.Raw)
	}
	// round-trip
	data, err := yaml.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var e2 Environment
	if err := yaml.Unmarshal(data, &e2); err != nil {
		t.Fatal(err)
	}
	if e2.VNCEncryption != "vencrypt" {
		t.Fatalf("после round-trip: %q, хотим vencrypt (yaml: %s)", e2.VNCEncryption, data)
	}
}

// VNC security отсутствует → пустое (auto — поведение по умолчанию, NULL authSchemes).
func TestEnvironmentVNCEncryptionDefaultEmpty(t *testing.T) {
	var e Environment
	if err := yaml.Unmarshal([]byte("name: t\nmethod: VNC\n"), &e); err != nil {
		t.Fatal(err)
	}
	if e.VNCEncryption != "" {
		t.Fatalf("дефолт: %q, хотим пустое (auto)", e.VNCEncryption)
	}
}

// Бэклог #7 SecretService: плоский ключ 'use secret service' → UseSecretService.
// Parse, round-trip и отсутствие в Raw (известный ключ).
func TestEnvironmentUseSecretService(t *testing.T) {
	var e Environment
	if err := yaml.Unmarshal([]byte("name: t\nmethod: SSH\nuse secret service: 1\n"), &e); err != nil {
		t.Fatal(err)
	}
	if e.UseSecretService != 1 {
		t.Fatalf("UseSecretService = %d, хотим 1", e.UseSecretService)
	}
	if _, ok := e.Raw["use secret service"]; ok {
		t.Fatalf("'use secret service' попал в Raw (известный ключ): %#v", e.Raw)
	}
	data, err := yaml.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var e2 Environment
	if err := yaml.Unmarshal(data, &e2); err != nil {
		t.Fatal(err)
	}
	if e2.UseSecretService != 1 {
		t.Fatalf("после round-trip: %d, хотим 1 (yaml: %s)", e2.UseSecretService, data)
	}
}

// Use secret service отсутствует → 0 (по умолчанию выключен).
func TestEnvironmentUseSecretServiceDefaultOff(t *testing.T) {
	var e Environment
	if err := yaml.Unmarshal([]byte("name: t\nmethod: SSH\n"), &e); err != nil {
		t.Fatal(err)
	}
	if e.UseSecretService != 0 {
		t.Fatalf("дефолт: %d, хотим 0", e.UseSecretService)
	}
}

// KeePass-ссылка по Uuid: 'KPX uuid' → KPXUUID. Parse/round-trip/известный ключ.
func TestEnvironmentKPXUUID(t *testing.T) {
	var e Environment
	if err := yaml.Unmarshal([]byte("name: t\nmethod: SSH\nKPX uuid: bc4121d3e3fa994ab5002a3f80fd2149\n"), &e); err != nil {
		t.Fatal(err)
	}
	if e.KPXUUID != "bc4121d3e3fa994ab5002a3f80fd2149" {
		t.Fatalf("KPXUUID = %q", e.KPXUUID)
	}
	if _, ok := e.Raw["KPX uuid"]; ok {
		t.Fatalf("'KPX uuid' попал в Raw (известный ключ): %#v", e.Raw)
	}
	data, err := yaml.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var e2 Environment
	if err := yaml.Unmarshal(data, &e2); err != nil {
		t.Fatal(err)
	}
	if e2.KPXUUID != "bc4121d3e3fa994ab5002a3f80fd2149" {
		t.Fatalf("после round-trip: %q (yaml: %s)", e2.KPXUUID, data)
	}
}

// KPX uuid отсутствует → пусто (старый путь — title-regexp).
func TestEnvironmentKPXUUIDDefaultEmpty(t *testing.T) {
	var e Environment
	if err := yaml.Unmarshal([]byte("name: t\nmethod: SSH\n"), &e); err != nil {
		t.Fatal(err)
	}
	if e.KPXUUID != "" {
		t.Fatalf("дефолт: %q, хотим пустое", e.KPXUUID)
	}
}

// Аудит #8: defaults 'show notifications' (Raw) → ShowNotifications().
func TestDefaultsShowNotifications(t *testing.T) {
	d := &Defaults{Raw: map[string]any{"show notifications": 1}}
	if !d.ShowNotifications() {
		t.Fatal("show notifications=1 → true")
	}
	d2 := &Defaults{}
	if d2.ShowNotifications() {
		t.Fatal("по умолчанию → false")
	}
	d3 := &Defaults{Raw: map[string]any{"show notifications": false}}
	if d3.ShowNotifications() {
		t.Fatal("false → false")
	}
	if (*Defaults)(nil).ShowNotifications() {
		t.Fatal("nil → false")
	}
}
