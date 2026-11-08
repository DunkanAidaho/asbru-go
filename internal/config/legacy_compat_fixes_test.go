package config

// Регрессионные тесты на находки ревью: legacy-скаляры в коллекциях,
// MergeConfig на нулевом dst, YAML-целые в FlexInt, дроп всех токенов при
// миграции RDP, каталог вместо файла в migrateScreenshot.

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// MAJOR: легаси допускал скаляры в variables/expect/local*/macros и
// переписывал их в map (PACUtils.pm:2401,2436,2455,2484,2501,2520). yaml.v3
// отвергал такой конфиг целиком — он не загружался.
func TestLoadLegacyScalarCollections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "asbru.yml")
	yamlSrc := `
environments:
  u1:
    name: legacy
    method: SSH
    variables:
      - foo
      - bar
    expect:
      - "login:"
    local before:
      - echo before
    local after:
      - echo after
    local connected:
      - echo connected
    macros:
      - echo macro
`
	if err := os.WriteFile(path, []byte(yamlSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path, nil)
	if err != nil {
		t.Fatalf("legacy-скаляры должны загружаться: %v", err)
	}
	e := cfg.Environments["u1"]

	// variables: {hide: 0, txt: <скаляр>}
	if len(e.Variables) != 2 {
		t.Fatalf("variables = %d, want 2", len(e.Variables))
	}
	if e.Variables[0]["txt"] != "foo" || e.Variables[0]["hide"] != 0 {
		t.Fatalf("variables[0] = %#v", e.Variables[0])
	}
	if e.Variables[1]["txt"] != "bar" {
		t.Fatalf("variables[1] = %#v", e.Variables[1])
	}

	// expect: скаляр значения не несёт, только дефолты (PACUtils.pm:2484).
	if len(e.Expect) != 1 || e.Expect[0]["active"] != 1 || e.Expect[0]["send"] != "" {
		t.Fatalf("expect[0] = %#v", e.Expect)
	}

	// local before/after: {default: 1, ask: 1, command: <скаляр>}
	for name, list := range map[string][]map[string]any{
		"local before": e.LocalBefore,
		"local after":  e.LocalAfter,
	} {
		if len(list) != 1 || list[0]["command"] == "" || list[0]["ask"] != 1 || list[0]["default"] != 1 {
			t.Fatalf("%s = %#v", name, list)
		}
	}
	if e.LocalBefore[0]["command"] != "echo before" || e.LocalAfter[0]["command"] != "echo after" {
		t.Fatalf("local: %#v / %#v", e.LocalBefore, e.LocalAfter)
	}

	// local connected: {confirm: 0, txt: <скаляр>}
	if len(e.LocalConnected) != 1 || e.LocalConnected[0]["txt"] != "echo connected" || e.LocalConnected[0]["confirm"] != 0 {
		t.Fatalf("local connected = %#v", e.LocalConnected)
	}

	// macros: {confirm: 0, intro: 1, txt: <скаляр>}
	if len(e.Macros) != 1 || e.Macros[0]["txt"] != "echo macro" ||
		e.Macros[0]["intro"] != 1 || e.Macros[0]["confirm"] != 0 {
		t.Fatalf("macros = %#v", e.Macros)
	}
}

// ag-9tj: скаляр коллекции может быть не строкой (YAML !!int/!!bool). Легаси
// (PACUtils.pm:2401 и далее) сохраняет его как есть, а в Perl число/булево
// стрингифицируется при подстановке. Go-потребитель (session/variables.go)
// делает m["txt"].(string) — не-string молча терялся, и <V:0> оставался
// литералом. Значение обязано всегда декодироваться в string.
func TestLoadLegacyScalarCollectionsNonStringScalars(t *testing.T) {
	path := filepath.Join(t.TempDir(), "asbru.yml")
	yamlSrc := `
environments:
  u1:
    name: legacy
    method: SSH
    variables:
      - 42
      - true
      - false
    local before:
      - 7
    macros:
      - 13
`
	if err := os.WriteFile(path, []byte(yamlSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path, nil)
	if err != nil {
		t.Fatalf("legacy-скаляры должны загружаться: %v", err)
	}
	e := cfg.Environments["u1"]

	wantVars := []string{"42", "true", "false"}
	if len(e.Variables) != len(wantVars) {
		t.Fatalf("variables = %d, want %d", len(e.Variables), len(wantVars))
	}
	for i, want := range wantVars {
		got, ok := e.Variables[i]["txt"].(string)
		if !ok || got != want {
			t.Fatalf("variables[%d][txt] = %#v, want string %q", i, e.Variables[i]["txt"], want)
		}
	}

	if got, ok := e.LocalBefore[0]["command"].(string); !ok || got != "7" {
		t.Fatalf("local before command = %#v, want string %q", e.LocalBefore[0]["command"], "7")
	}
	if got, ok := e.Macros[0]["txt"].(string); !ok || got != "13" {
		t.Fatalf("macros txt = %#v, want string %q", e.Macros[0]["txt"], "13")
	}
}

// ag-j1r: легаси-элемент коллекции может быть YAML-alias (`*a`) на якорный
// скаляр (`&a 42`). Perl (YAML.pm) раскрывает alias до нормализации, поэтому
// скалярное значение попадает в valueField как обычный скаляр
// (PACUtils.pm:2401,2501). Go-нормализатор пропускал AliasNode, и node.Decode
// падал с «cannot unmarshal !!int 42 into map[string]interface {}» — конфиг
// не загружался целиком.
func TestLoadLegacyScalarCollectionAliases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "asbru.yml")
	yamlSrc := `
environments:
  u1:
    name: legacy
    method: SSH
    variables:
      - &a 42
      - *a
    macros:
      - &m echo anchor
      - *m
    local before:
      - &b "echo before"
      - *b
`
	if err := os.WriteFile(path, []byte(yamlSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path, nil)
	if err != nil {
		t.Fatalf("alias-элементы легаси-коллекций должны загружаться: %v", err)
	}
	e := cfg.Environments["u1"]

	if len(e.Variables) != 2 {
		t.Fatalf("variables = %d, want 2", len(e.Variables))
	}
	for i, v := range e.Variables {
		if got, ok := v["txt"].(string); !ok || got != "42" {
			t.Fatalf("variables[%d][txt] = %#v, want string %q", i, v["txt"], "42")
		}
		if v["hide"] != 0 {
			t.Fatalf("variables[%d][hide] = %#v, want 0", i, v["hide"])
		}
	}

	if len(e.Macros) != 2 {
		t.Fatalf("macros = %d, want 2", len(e.Macros))
	}
	for i, m := range e.Macros {
		if got, ok := m["txt"].(string); !ok || got != "echo anchor" {
			t.Fatalf("macros[%d][txt] = %#v, want string %q", i, m["txt"], "echo anchor")
		}
		if m["intro"] != 1 || m["confirm"] != 0 {
			t.Fatalf("macros[%d] = %#v", i, m)
		}
	}

	if len(e.LocalBefore) != 2 {
		t.Fatalf("local before = %d, want 2", len(e.LocalBefore))
	}
	for i, lb := range e.LocalBefore {
		if got, ok := lb["command"].(string); !ok || got != "echo before" {
			t.Fatalf("local before[%d][command] = %#v, want string %q", i, lb["command"], "echo before")
		}
	}
}

// alias на map-элемент (`*m` после `&m {txt: ...}`) — не скаляр: нормализация
// не должна его трогать, декодирование в map обязано работать без изменений.
func TestLoadLegacyScalarCollectionAliasOfMap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "asbru.yml")
	yamlSrc := `
environments:
  u1:
    name: legacy
    method: SSH
    variables:
      - &v {txt: custom, hide: 1}
      - *v
`
	if err := os.WriteFile(path, []byte(yamlSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path, nil)
	if err != nil {
		t.Fatalf("alias на map должен загружаться: %v", err)
	}
	vars := cfg.Environments["u1"].Variables
	if len(vars) != 2 {
		t.Fatalf("variables = %d, want 2", len(vars))
	}
	for i, v := range vars {
		if v["txt"] != "custom" || v["hide"] != 1 {
			t.Fatalf("variables[%d] = %#v", i, v)
		}
	}
}

// MAJOR (латентный): MergeConfig писал в nil-map dst.Environments и паниковал
// на нулевом Config.
func TestMergeConfigZeroValueDst(t *testing.T) {
	src := &Config{
		Environments: map[string]Environment{
			"g": {Name: "grp", IsGroup: 1},
			"c": {Name: "child", Parent: "g"},
		},
		Defaults: Defaults{GlobalVariables: map[string]VarEntry{
			"v": {Type: "string", Value: "x"},
		}},
	}
	dst := &Config{}
	if added := MergeConfig(dst, src); added != 2 {
		t.Fatalf("added = %d, want 2", added)
	}
	if dst.Environments["c"].Parent != "g" {
		t.Fatalf("родитель потерян: %#v", dst.Environments["c"])
	}
	if dst.Environments["g"].Children["c"] != 1 {
		t.Fatalf("children не прокинуты: %#v", dst.Environments["g"].Children)
	}
	if dst.Defaults.GlobalVariables["v"].Value != "x" {
		t.Fatalf("global variables не добавлены: %#v", dst.Defaults.GlobalVariables)
	}
}

// MINOR: FlexInt должен понимать YAML-формы целых (hex/octal/binary/подчёркивания
// и легаси-октал с ведущим нулём), а не сырой Atoi.
func TestFlexIntYAMLIntegerForms(t *testing.T) {
	cases := []struct {
		in   string
		want FlexInt
	}{
		{"0x30", 48},
		{"1_000", 1000},
		{"0o17", 15},
		{"0022", 18}, // YAML 1.1: ведущий 0 — октал
		{"0b101", 5},
	}
	for _, c := range cases {
		var f FlexInt
		if err := yaml.Unmarshal([]byte(c.in), &f); err != nil {
			t.Errorf("FlexInt(%s): %v", c.in, err)
			continue
		}
		if f != c.want {
			t.Errorf("FlexInt(%s) = %d, want %d", c.in, f, c.want)
		}
	}
}

// MINOR: конверсия, которая намеренно выкидывает ВСЕ токены (xfreerdp-метод и
// "-4"), должна очистить строку, а не оставить старый токен.
func TestMigrateRdpOptionsDropsAllTokens(t *testing.T) {
	env := &Environment{Method: "RDP (xfreerdp)", Options: "-4"}
	migrateRdpOptions(env)
	if env.Options != "" {
		t.Fatalf("xfreerdp + -4 → %q, want пусто (токен дропается)", env.Options)
	}
}

// MINOR: migrateScreenshot принимал каталоги (os.Stat успешен) — легаси
// требует regular file (-f).
func TestSanitizeScreenshotRejectsDirectory(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{Environments: map[string]Environment{
		"u1": {Name: "c", Method: "SSH", Raw: map[string]any{"screenshot": dir}},
	}}
	Sanitize(cfg)
	if got := cfg.Environments["u1"].Screenshots; len(got) != 0 {
		t.Fatalf("каталог принят как screenshot: %#v", got)
	}
}

// Симлинк на обычный файл — это файл (os.Stat идёт по ссылке), миграция ок.
func TestSanitizeScreenshotFollowsSymlinkToFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "shot.png")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.png")
	if err := os.Symlink(file, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	cfg := &Config{Environments: map[string]Environment{
		"u1": {Name: "c", Method: "SSH", Raw: map[string]any{"screenshot": link}},
	}}
	Sanitize(cfg)
	if got := cfg.Environments["u1"].Screenshots; len(got) != 1 || got[0] != link {
		t.Fatalf("симлинк на файл не принят: %#v", got)
	}
}
