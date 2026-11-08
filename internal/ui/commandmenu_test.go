package ui

import (
	"testing"

	"github.com/DunkanAidaho/asbru-go/internal/config"
)

// commandsOf — читает команды из записей, отсеивает пустые, desc → cmd fallback.
func TestCommandsOf(t *testing.T) {
	entries := []map[string]any{
		{"command": "ls -la", "description": "Список файлов"},
		{"txt": "pwd", "description": "Текущий каталог"},
		{"command": "   "},   // пустая — отбросить
		{"command": "uname"}, // без desc → desc = cmd
		{},
	}
	got := commandsOf(entries)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	if got[0].txt != "ls -la" || got[0].desc != "Список файлов" {
		t.Fatalf("[0] = %+v", got[0])
	}
	if got[1].txt != "pwd" || got[1].desc != "Текущий каталог" {
		t.Fatalf("[1] = %+v", got[1])
	}
	if got[2].txt != "uname" || got[2].desc != "uname" {
		t.Fatalf("[2] = %+v (desc должен упасть на cmd)", got[2])
	}
}

// defaultsCommandList — глобальные команды из defaults.Raw ([]map и []any).
func TestDefaultsCommandList(t *testing.T) {
	// []map[string]any
	cfg := &config.Config{Defaults: config.Defaults{Raw: map[string]any{
		"remote commands": []map[string]any{{"command": "ls", "description": "List"}},
	}}}
	got := defaultsCommandList(cfg, "remote commands")
	if len(got) != 1 || got[0]["command"] != "ls" {
		t.Fatalf("[]map: %+v", got)
	}
	// []any (yaml list-of-maps)
	cfg2 := &config.Config{Defaults: config.Defaults{Raw: map[string]any{
		"local commands": []any{map[string]any{"command": "pwd", "description": "Pwd"}},
	}}}
	got2 := defaultsCommandList(cfg2, "local commands")
	if len(got2) != 1 || got2[0]["command"] != "pwd" {
		t.Fatalf("[]any: %+v", got2)
	}
	// nil/нет ключа
	if defaultsCommandList(cfg2, "remote commands") != nil {
		t.Fatal("нет ключа → должен быть nil")
	}
}
