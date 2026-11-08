package ui

import (
	"testing"

	"github.com/DunkanAidaho/asbru-go/internal/config"
)

// #KeyBindings-редактор: addKeyBinding — добавление новой привязки.
func TestAddKeyBinding(t *testing.T) {
	cfg := &config.Config{Defaults: config.Defaults{KeyBindings: map[string]map[string][]string{
		"terminal": {"Ctrl+c": {"Terminal", "copy", "Copy"}},
	}}}

	if err := addKeyBinding(cfg, "terminal", "find", "Ctrl+f"); err != nil {
		t.Fatalf("add: %v", err)
	}
	got := cfg.Defaults.KeyBindings["terminal"]["Ctrl+f"]
	if len(got) != 3 || got[0] != "Terminal" || got[1] != "find" || got[2] != "Find in connection tree" {
		t.Fatalf("added = %#v, want [Terminal find desc]", got)
	}
	// новая appwin-секция создаётся
	if err := addKeyBinding(cfg, "pacmain", "quit", "Ctrl+q"); err != nil {
		t.Fatalf("add pacmain: %v", err)
	}
	if _, ok := cfg.Defaults.KeyBindings["pacmain"]["Ctrl+q"]; !ok {
		t.Fatal("pacmain Ctrl+q не добавлен")
	}
}

// коллизия (EqualFold) — ошибка, не перезапись.
func TestAddKeyBindingCollision(t *testing.T) {
	cfg := &config.Config{Defaults: config.Defaults{KeyBindings: map[string]map[string][]string{
		"terminal": {"Ctrl+f": {"Terminal", "find", "Find"}},
	}}}
	if err := addKeyBinding(cfg, "terminal", "copy", "ctrl+f"); err == nil {
		t.Fatal("ожидали коллизию для ctrl+f (EqualFold)")
	}
	// ключ не перезаписан
	got := cfg.Defaults.KeyBindings["terminal"]["Ctrl+f"]
	if got[1] != "find" {
		t.Fatalf("перезаписан: %#v", got)
	}
}

// Коллизия проверяется по ВСЕМ appwin-секциям: runtime-регистрация
// (bindingActions) дедуплицирует seq между секциями, поэтому привязка в другой
// секции молча никогда бы не сработала.
func TestAddKeyBindingCrossSectionCollision(t *testing.T) {
	cfg := &config.Config{Defaults: config.Defaults{KeyBindings: map[string]map[string][]string{
		"terminal": {"Ctrl+f": {"Terminal", "find", "Find"}},
	}}}
	if err := addKeyBinding(cfg, "pacmain", "quit", "ctrl+f"); err == nil {
		t.Fatal("ожидали коллизию Ctrl+f из другой секции (EqualFold)")
	}
	if _, ok := cfg.Defaults.KeyBindings["pacmain"]["ctrl+f"]; ok {
		t.Fatal("коллизионная привязка добавлена")
	}
}

// пустой ключ, неподдерживаемое действие, неизвестный appwin.
func TestAddKeyBindingErrors(t *testing.T) {
	cfg := &config.Config{Defaults: config.Defaults{KeyBindings: map[string]map[string][]string{}}}
	if err := addKeyBinding(cfg, "terminal", "copy", ""); err != errKeyBindKeyRequired {
		t.Fatalf("пустой ключ: %v", err)
	}
	if err := addKeyBinding(cfg, "terminal", "no_such_action", "Ctrl+x"); err != errKeyBindUnsupported {
		t.Fatalf("неизвестное действие: %v", err)
	}
	if err := addKeyBinding(cfg, "no_app", "copy", "Ctrl+x"); err != errKeyBindAppUnknown {
		t.Fatalf("неизвестный appwin: %v", err)
	}
	if err := addKeyBinding(nil, "terminal", "copy", "Ctrl+x"); err != errKeyBindNoCfg {
		t.Fatalf("nil cfg: %v", err)
	}
}

// removeKeyBinding — удаление; no-op для отсутствующего.
func TestRemoveKeyBinding(t *testing.T) {
	cfg := &config.Config{Defaults: config.Defaults{KeyBindings: map[string]map[string][]string{
		"terminal": {"Ctrl+c": {"Terminal", "copy", "Copy"}, "Ctrl+f": {"Terminal", "find", "Find"}},
	}}}
	removeKeyBinding(cfg, "terminal", "Ctrl+c")
	if _, ok := cfg.Defaults.KeyBindings["terminal"]["Ctrl+c"]; ok {
		t.Fatal("Ctrl+c не удалён")
	}
	if _, ok := cfg.Defaults.KeyBindings["terminal"]["Ctrl+f"]; !ok {
		t.Fatal("Ctrl+f не должен удалиться")
	}
	// нет аппвина — no-op
	removeKeyBinding(cfg, "no_app", "Ctrl+c")
	removeKeyBinding(nil, "terminal", "Ctrl+c") // не падает
}

// keyBindApps — известные + существующие.
func TestKeyBindApps(t *testing.T) {
	cfg := &config.Config{Defaults: config.Defaults{KeyBindings: map[string]map[string][]string{
		"custom_app": {"Ctrl+q": {"X", "quit", "Quit"}},
	}}}
	apps := keyBindApps(cfg)
	seen := map[string]bool{}
	for _, a := range apps {
		seen[a] = true
	}
	for _, want := range []string{"pacmain", "terminal", "treeConnections", "custom_app"} {
		if !seen[want] {
			t.Errorf("apps missing %q: %v", want, apps)
		}
	}
}
