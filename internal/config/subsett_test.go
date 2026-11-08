package config

import "testing"

// subEnv — окружение-заглушка с заданными родителем/детьми (для SubsetConfig).
func subEnv(parent string, group bool, children ...string) Environment {
	e := Environment{Name: "e", Parent: parent}
	if group {
		e.IsGroup = 1
		if len(children) > 0 {
			e.Children = map[string]FlexInt{}
			for _, c := range children {
				e.Children[c] = 1
			}
		}
	}
	return e
}

// SubsetConfig — выделенная группа захватывает потомков; дети остаются под ней.
func TestSubsetConfigGroupWithChildren(t *testing.T) {
	src := &Config{Environments: map[string]Environment{
		"g":   subEnv(RootUUID, true, "c1", "c2"),
		"c1":  subEnv("g", false),
		"c2":  subEnv("g", false),
		"out": subEnv(RootUUID, false),
	}}
	dst, n := SubsetConfig(src, []string{"g"})
	if n != 3 {
		t.Fatalf("SubsetConfig: %d узлов, want 3 (g+c1+c2)", n)
	}
	for _, u := range []string{"g", "c1", "c2"} {
		if _, ok := dst.Environments[u]; !ok {
			t.Fatalf("узел %q не включён", u)
		}
	}
	if _, ok := dst.Environments["out"]; ok {
		t.Fatalf("невыбранный узел out попал в экспорт")
	}
	if dst.Environments["c1"].Parent != "g" || dst.Environments["c2"].Parent != "g" {
		t.Fatalf("иерархия детей нарушена: %+v %+v", dst.Environments["c1"], dst.Environments["c2"])
	}
	if len(dst.Environments["g"].Children) != 2 {
		t.Fatalf("children у g: %v, want {c1,c2}", dst.Environments["g"].Children)
	}
}

// SubsetConfig — выбранный узел с невыбранным родителем пересаживается в корень.
func TestSubsetConfigOrphanToRoot(t *testing.T) {
	src := &Config{Environments: map[string]Environment{
		"parent": subEnv(RootUUID, true, "c1"),
		"c1":     subEnv("parent", false),
	}}
	dst, n := SubsetConfig(src, []string{"c1"})
	if n != 1 {
		t.Fatalf("SubsetConfig: %d узлов, want 1 (только c1)", n)
	}
	if dst.Environments["c1"].Parent != RootUUID {
		t.Fatalf("сирота не пересажен в корень: %+v", dst.Environments["c1"])
	}
}

// SubsetConfig — выбранное соединение без детей.
func TestSubsetConfigLeafOnly(t *testing.T) {
	src := &Config{Environments: map[string]Environment{
		"leaf": subEnv(RootUUID, false),
	}}
	dst, n := SubsetConfig(src, []string{"leaf"})
	if n != 1 || dst.Environments["leaf"].Parent != RootUUID {
		t.Fatalf("SubsetConfig(лист) = %d/%+v, want 1 в корне", n, dst.Environments["leaf"])
	}
}

// SubsetConfig — Defaults копируются (не мутируются).
func TestSubsetConfigDefaultsCopied(t *testing.T) {
	src := &Config{Environments: map[string]Environment{
		"l": subEnv(RootUUID, false),
	}, Defaults: Defaults{Proxy: "Jump"}}
	dst, _ := SubsetConfig(src, []string{"l"})
	if dst.Defaults.Proxy != "Jump" {
		t.Fatalf("Defaults не перенесены: %+v", dst.Defaults)
	}
	dst.Defaults.Proxy = "Proxy"
	if src.Defaults.Proxy != "Jump" {
		t.Fatalf("Defaults мутированы: %+v", src.Defaults)
	}
}

// SubsetConfig — M36-ревью MINOR: map-поля Defaults НЕ алиасят источник
// (правка части экспорта не должна портить живой конфиг).
func TestSubsetConfigDefaultsMapsNotAliased(t *testing.T) {
	src := &Config{Environments: map[string]Environment{
		"l": subEnv(RootUUID, false),
	}, Defaults: Defaults{GlobalVariables: map[string]VarEntry{
		"k": {Type: "string", Value: "v"},
	}}}
	dst, _ := SubsetConfig(src, []string{"l"})
	// мутируем копию через экспорт — источник должен остаться нетронутым.
	dst.Defaults.GlobalVariables["k"] = VarEntry{Type: "string", Value: "changed"}
	if src.Defaults.GlobalVariables["k"].Value != "v" {
		t.Fatalf("GlobalVariables алиасят: src=%+v", src.Defaults.GlobalVariables["k"])
	}
	// и новый ключ в копии не появляется в источнике.
	dst.Defaults.GlobalVariables["new"] = VarEntry{Type: "string", Value: "x"}
	if _, ok := src.Defaults.GlobalVariables["new"]; ok {
		t.Fatalf("новый ключ утёк в источник")
	}
}

// SubsetConfig — M36-ревью MINOR: неизвестный uuid в sel не создаёт фантома.
func TestSubsetConfigUnknownUUIDDropped(t *testing.T) {
	src := &Config{Environments: map[string]Environment{
		"g":  subEnv(RootUUID, true, "c1"),
		"c1": subEnv("g", false),
	}}
	dst, n := SubsetConfig(src, []string{"nosuch", "g"})
	if n != 2 {
		t.Fatalf("SubsetConfig: %d узлов, want 2 (без фантома)", n)
	}
	if _, ok := dst.Environments["nosuch"]; ok {
		t.Fatalf("фантомный узел попал в экспорт")
	}
}

// SubsetConfig — коллекционные поля Environment не алиасят источник.
func TestSubsetConfigEnvCollectionsNotAliased(t *testing.T) {
	src := &Config{Environments: map[string]Environment{
		"l": {Name: "l", Expect: []map[string]any{{"expect": "x"}},
			Variables: []map[string]any{{"name": "v"}}},
	}}
	dst, _ := SubsetConfig(src, []string{"l"})
	// мутируем копию — источник не должен измениться.
	dst.Environments["l"].Expect[0]["expect"] = "mutated"
	dst.Environments["l"].Variables[0]["name"] = "mutated"
	if src.Environments["l"].Expect[0]["expect"] == "mutated" || src.Environments["l"].Variables[0]["name"] == "mutated" {
		t.Fatalf("коллекции Environment алиасят: %+v", src.Environments["l"])
	}
}

// M36: _protected — поле защищённого узла round-trip через YAML (и в known-keys).
func TestEnvironmentProtectedRoundTrip(t *testing.T) {
	cfg := &Config{Environments: map[string]Environment{
		"u1": {Name: "c1", Method: "SSH", Protected: 1},
	}}
	out := t.TempDir() + "/cfg.yml"
	if err := cfg.Save(out, NewCBC()); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConfig(out, NewCBC())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Environments["u1"].Protected != 1 {
		t.Fatalf("_protected не сохранился: %+v", loaded.Environments["u1"])
	}
	// и не попал в Raw (known-keys содержит _protected).
	if _, ok := loaded.Environments["u1"].Raw["_protected"]; ok {
		t.Fatalf("_protected утёк в Raw: %v", loaded.Environments["u1"].Raw)
	}
}
