package ui

import (
	"testing"

	"github.com/DunkanAidaho/asbru-go/internal/config"
)

// Добавление соединения/группы: новый uuid, привязка к родителю через
// children{} и к Parent.
func TestAddEnvLinksParent(t *testing.T) {
	cfg := &config.Config{Environments: map[string]config.Environment{
		"root": {Name: "root", IsGroup: 1, Children: map[string]config.FlexInt{"g1": 1}},
		"g1":   {Name: "Группа", IsGroup: 1, Parent: "root"},
	}}
	uuid, err := addEnv(cfg, "g1", &config.Environment{Name: "Новое", Method: "SSH"})
	if err != nil {
		t.Fatal(err)
	}
	if uuid == "" || cfg.Environments[uuid].Name != "Новое" {
		t.Fatalf("addEnv: %+v", cfg.Environments[uuid])
	}
	if cfg.Environments[uuid].Parent != "g1" {
		t.Fatalf("parent = %q", cfg.Environments[uuid].Parent)
	}
	if cfg.Environments["g1"].Children[uuid] != 1 {
		t.Fatalf("children не связаны: %v", cfg.Environments["g1"].Children)
	}
}

// Несуществующий родитель → привязка к корню.
func TestAddEnvFallbackRoot(t *testing.T) {
	cfg := &config.Config{Environments: map[string]config.Environment{
		"root": {Name: "root", IsGroup: 1, Children: map[string]config.FlexInt{}},
	}}
	uuid, err := addEnv(cfg, "ghost", &config.Environment{Name: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Environments[uuid].Parent != config.RootUUID {
		t.Fatalf("parent = %q, want root", cfg.Environments[uuid].Parent)
	}
	if cfg.Environments[config.RootUUID].Children[uuid] != 1 {
		t.Fatal("root children не связаны")
	}
}

// Удаление: снимается из environments и из children{} родителя.
func TestRemoveEnvUnlinks(t *testing.T) {
	cfg := &config.Config{Environments: map[string]config.Environment{
		"root": {Name: "root", IsGroup: 1, Children: map[string]config.FlexInt{"g1": 1}},
		"g1":   {Name: "Группа", IsGroup: 1, Parent: "root"},
		"c1":   {Name: "С", Method: "SSH", Parent: "g1"},
		"g2":   {Name: "Точка", IsGroup: 1, Parent: "g1", Children: map[string]config.FlexInt{"c1": 1}},
	}}
	if err := removeEnv(cfg, "c1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Environments["c1"]; ok {
		t.Fatal("запись не удалена")
	}
	if _, ok := cfg.Environments["g2"].Children["c1"]; ok {
		t.Fatal("children не вычищен: c1 остался в g2")
	}
}

// Пустой конфиг: addEnv синтезирует корень как группу (ревью F5).
func TestAddEnvEmptyConfigSynthesizesRoot(t *testing.T) {
	cfg := &config.Config{Environments: map[string]config.Environment{}}
	uuid, err := addEnv(cfg, "", &config.Environment{Name: "A", Method: "SSH"})
	if err != nil {
		t.Fatal(err)
	}
	root := cfg.Environments[config.RootUUID]
	if root.IsGroup != 1 || root.Children[uuid] != 1 {
		t.Fatalf("root = %+v", root)
	}
	if cfg.Environments[uuid].Parent != config.RootUUID {
		t.Fatalf("parent = %q", cfg.Environments[uuid].Parent)
	}
}

// Корень удалить нельзя; удаление группы убирает поддерево (ревью F6).
func TestRemoveEnvGuardsRootAndDeletesSubtree(t *testing.T) {
	cfg := &config.Config{Environments: map[string]config.Environment{
		"root": {Name: "root", IsGroup: 1, Children: map[string]config.FlexInt{"g1": 1}},
		"g1":   {Name: "Г", IsGroup: 1, Parent: "root", Children: map[string]config.FlexInt{"c1": 1}},
		"c1":   {Name: "С", Method: "SSH", Parent: "g1"},
	}}
	if err := removeEnv(cfg, config.RootUUID); err == nil {
		t.Fatal("удаление корня должно быть запрещено")
	}
	if err := removeEnv(cfg, "g1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Environments["g1"]; ok {
		t.Fatal("группа осталась")
	}
	if _, ok := cfg.Environments["c1"]; ok {
		t.Fatal("поддерево не удалено: c1 осталась")
	}
	if len(cfg.Environments[config.RootUUID].Children) != 0 {
		t.Fatal("root children не вычищены")
	}
}

// Циклические children{}: удаление не должно уходить в бесконечную
// рекурсию (ревью R2 — visited в removeEnv).
func TestRemoveEnvCycleSafe(t *testing.T) {
	cfg := &config.Config{Environments: map[string]config.Environment{
		"root": {Name: "root", IsGroup: 1, Children: map[string]config.FlexInt{"g1": 1}},
		"g1":   {Name: "Г1", IsGroup: 1, Parent: "root", Children: map[string]config.FlexInt{"g2": 1}},
		"g2":   {Name: "Г2", IsGroup: 1, Parent: "g1", Children: map[string]config.FlexInt{"g1": 1}},
	}}
	if err := removeEnv(cfg, "g1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Environments["g1"]; ok {
		t.Fatal("g1 осталась")
	}
	if _, ok := cfg.Environments["g2"]; ok {
		t.Fatal("g2 осталась")
	}
}

// M36: защищённый узел удалить нельзя (removeEnv отказывает).
func TestRemoveEnvRefusesProtected(t *testing.T) {
	cfg := &config.Config{Environments: map[string]config.Environment{
		"root": {Name: "root", IsGroup: 1, Children: map[string]config.FlexInt{"p": 1}},
		"p":    {Name: "protected", Method: "SSH", Parent: "root", Protected: 1},
	}}
	if err := removeEnv(cfg, "p"); err == nil {
		t.Fatal("защищённый узел удалён без ошибки")
	}
	if _, ok := cfg.Environments["p"]; !ok {
		t.Fatal("защищённый узел исчез из конфига")
	}
}

// M36-ревью MAJOR: удаление группы с защищённым потомком — АТОМАРНЫЙ отказ
// (не частичное удаление незащищённых соседей = потеря данных).
func TestRemoveEnvProtectedDescendantAtomic(t *testing.T) {
	cfg := &config.Config{Environments: map[string]config.Environment{
		"root": {Name: "root", IsGroup: 1, Children: map[string]config.FlexInt{"g": 1}},
		"g":    {Name: "g", IsGroup: 1, Parent: "root", Children: map[string]config.FlexInt{"a": 1, "p": 1}},
		"a":    {Name: "a", Method: "SSH", Parent: "g"},
		"p":    {Name: "p", Method: "SSH", Parent: "g", Protected: 1},
	}}
	if err := removeEnv(cfg, "g"); err == nil {
		t.Fatal("ожидали отказ (в поддереве защищённый)")
	}
	for _, u := range []string{"g", "a", "p"} {
		if _, ok := cfg.Environments[u]; !ok {
			t.Fatalf("узел %q удалён при частичном отказе", u)
		}
	}
}

// M36 Protect: клонирование под защищённым родителем запрещено (легаси 2954).
func TestCloneEnvRefusesProtectedParent(t *testing.T) {
	cfg := &config.Config{Environments: map[string]config.Environment{
		"root": {Name: "root", IsGroup: 1, Children: map[string]config.FlexInt{"g": 1}},
		"g":    {Name: "g", IsGroup: 1, Parent: "root", Protected: 1, Children: map[string]config.FlexInt{"c": 1}},
		"c":    {Name: "c", Method: "SSH", Parent: "g"},
	}}
	if _, err := cloneEnv(cfg, "c"); err == nil {
		t.Fatal("клон под защищённым родителем не отклонён")
	}
}
