package ui

import (
	"reflect"
	"testing"

	"github.com/DunkanAidaho/asbru-go/internal/config"
)

// M29: commandRows сортирует по имени; applyCommandRows перестраивает список.
func TestCommandRowsRoundTrip(t *testing.T) {
	cfg := &config.Config{Defaults: config.Defaults{Raw: map[string]any{
		"local commands": []any{
			map[string]any{"description": "b", "command": "ls"},
			map[string]any{"description": "a", "command": "pwd"},
			map[string]any{"description": "x", "txt": "uptime"}, // fallback txt
		},
	}}}
	rows := commandRows(cfg, "local commands")
	if len(rows) != 3 {
		t.Fatalf("rows: %+v", rows)
	}
	if rows[0].Name != "a" || rows[0].Cmd != "pwd" {
		t.Fatalf("сорт: %+v", rows[0])
	}
	if rows[2].Name != "x" || rows[2].Cmd != "uptime" {
		t.Fatalf("txt fallback: %+v", rows[2])
	}
	// rebuild: изменение + пустая команда отбрасывается.
	rows[0].Cmd = "pwd2"
	out := &config.Config{}
	applyCommandRows(out, "remote commands", append(rows, commandRow{Name: "empty", Cmd: ""}))
	raw := out.Defaults.Raw["remote commands"].([]any)
	if len(raw) != 3 {
		t.Fatalf("apply: %v", raw)
	}
	m0 := raw[0].(map[string]any)
	if m0["command"] != "pwd2" || m0["description"] != "a" {
		t.Fatalf("первая: %v", m0)
	}
	if commandRows(nil, "local commands") != nil {
		t.Fatal("nil cfg → nil")
	}
	if len(out.Defaults.Raw) == 0 {
		t.Fatal("Raw не создан?")
	}
	if !reflect.DeepEqual(out.Defaults.Raw["remote commands"], raw) {
		t.Fatalf("raw mismatch")
	}
}

// M29-ревью: незатронутые ключи мапы (confirm/intro) СОХРАНЯЮТСЯ при apply.
func TestCommandRowsPreservesExtraKeys(t *testing.T) {
	cfg := &config.Config{Defaults: config.Defaults{Raw: map[string]any{
		"remote commands": []any{
			map[string]any{"description": "Reboot", "command": "sudo reboot", "confirm": 1, "intro": 1},
		},
	}}}
	rows := commandRows(cfg, "remote commands")
	if len(rows) != 1 {
		t.Fatalf("rows: %+v", rows)
	}
	out := &config.Config{}
	applyCommandRows(out, "remote commands", rows)
	raw := out.Defaults.Raw["remote commands"].([]any)
	m := raw[0].(map[string]any)
	if m["confirm"] != 1 || m["intro"] != 1 {
		t.Fatalf("потеряны confirm/intro: %v", m)
	}
	if m["command"] != "sudo reboot" || m["description"] != "Reboot" {
		t.Fatalf("editable: %v", m)
	}
	// пустая команда → отбрасывается, orig не должен просочиться.
	applyCommandRows(out, "remote commands", []commandRow{{Name: "x", Cmd: ""}})
	if got := out.Defaults.Raw["remote commands"].([]any); len(got) != 0 {
		t.Fatalf("пустая команда не отброшена: %v", got)
	}
}
