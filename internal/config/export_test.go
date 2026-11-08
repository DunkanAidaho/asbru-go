package config

import (
	"path/filepath"
	"testing"
)

// M22: full-маркер экспорта — __PAC__EXPORTED__FULL__ проходит round-trip
// (легаси PACConfig.pm:635; по нему импорт замещает конфиг целиком).
func TestExportMarkerRoundTrip(t *testing.T) {
	cfg := &Config{Environments: map[string]Environment{"u1": env("c1", "SSH")}}
	WithExportMarker(cfg)
	out := filepath.Join(t.TempDir(), "export.yml")
	if err := cfg.Save(out, NewCBC()); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConfig(out, NewCBC())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ExportedFull != 1 {
		t.Fatalf("маркер ExportedFull = %v", loaded.ExportedFull)
	}
}

// M22: merge-импорт — новые uuid добавляются, существующие НЕ перетираются,
// дети с отсутствующим родителем пересаживаются в корень.
func TestMergeConfig(t *testing.T) {
	dst := &Config{Environments: map[string]Environment{
		"keep": {Name: "existing", Method: "SSH", Parent: RootUUID},
	}}
	src := &Config{Environments: map[string]Environment{
		"keep":  {Name: "overwrite? no", Method: "Telnet"},
		"fresh": {Name: "new", Method: "SSH", Parent: "orphan-parent"},
	}}
	if added := MergeConfig(dst, src); added != 1 {
		t.Fatalf("added = %d, хотим 1", added)
	}
	if dst.Environments["keep"].Name != "existing" {
		t.Fatalf("существующее перетёрто: %q", dst.Environments["keep"].Name)
	}
	if dst.Environments["fresh"].Parent != RootUUID {
		t.Fatalf("родитель неправильного причём: %q", dst.Environments["fresh"].Parent)
	}
	// группы-родители из src тоже переносятся своим ходом; повторный merge — no-op
	if again := MergeConfig(dst, src); again != 0 {
		t.Fatalf("повторный merge: %d", again)
	}
}

// M22: глобальные переменные — новые ключи добавляются, существующие целы.
func TestMergeConfigGlobals(t *testing.T) {
	dst := &Config{Defaults: Defaults{GlobalVariables: map[string]VarEntry{
		"mine": {Type: "string", Value: "1"},
	}}}
	src := &Config{Defaults: Defaults{GlobalVariables: map[string]VarEntry{
		"mine":  {Type: "string", Value: "x"}, // не перетирать
		"their": {Type: "string", Value: "y"},
	}}}
	MergeConfig(dst, src)
	if dst.Defaults.GlobalVariables["mine"].Value != "1" {
		t.Fatal("перетёрта своя переменная")
	}
	if dst.Defaults.GlobalVariables["their"].Value != "y" {
		t.Fatal("чужая переменная не добавлена")
	}
}

// M22-ревью P1 (блокер): дерево строится ТОЛЬКО из children{}-карт групп
// (tree/model.go), merge обязан прокинуть их — иначе импортированное
// невидимо в UI.
func TestMergeConfigWiresChildren(t *testing.T) {
	dst := &Config{Environments: map[string]Environment{
		RootUUID: {Name: "__PAC__ROOT__", Parent: "", IsGroup: 1,
			Children: map[string]FlexInt{"keep": 1}},
		"keep": {Name: "existing", Parent: RootUUID, IsGroup: 1, Children: map[string]FlexInt{}},
	}}
	src := &Config{Environments: map[string]Environment{
		"g":      {Name: "New group", Parent: RootUUID, IsGroup: 1, Children: map[string]FlexInt{"c1": 1}},
		"c1":     {Name: "child", Parent: "g"},
		"orphan": {Name: "orphan", Parent: "missing-parent"},
	}}
	if added := MergeConfig(dst, src); added != 3 {
		t.Fatalf("added = %d", added)
	}
	if dst.Environments[RootUUID].Children["g"] != 1 || dst.Environments[RootUUID].Children["orphan"] != 1 {
		t.Fatalf("root.Children: %v", dst.Environments[RootUUID].Children)
	}
	if dst.Environments["g"].Children["c1"] != 1 {
		t.Fatalf("g.Children: %v", dst.Environments["g"].Children)
	}
	if dst.Environments["c1"].Parent != "g" {
		t.Fatalf("c1.Parent = %q", dst.Environments["c1"].Parent)
	}
}
