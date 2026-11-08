package ui

import (
	"reflect"
	"testing"

	"github.com/DunkanAidaho/asbru-go/internal/config"
)

// #4: режим панели макросов из defaults 'show commands box'.
func TestMacroBarMode(t *testing.T) {
	cases := []struct {
		raw  any
		want int
	}{
		{nil, 0}, {0, 0}, {1, 1}, {2, 2}, {3, 2}, {-1, 0},
		{int64(1), 1}, {float64(2), 2},
		{"Never", 0}, {"Combobox", 1}, {"Buttons", 2}, {"never", 0},
		{"combobox", 1}, {"buttons", 2}, {"bogus", 0}, {"", 0},
	}
	for _, c := range cases {
		var d *config.Defaults
		if c.raw != nil {
			d = &config.Defaults{Raw: map[string]any{"show commands box": c.raw}}
		} else {
			d = &config.Defaults{Raw: map[string]any{}}
		}
		if got := macroBarMode(d); got != c.want {
			t.Errorf("raw=%v: got %d, want %d", c.raw, got, c.want)
		}
	}
	if got := macroBarMode(nil); got != 0 {
		t.Fatalf("nil defaults: got %d", got)
	}
}

// #4: 'show global commands box' — bool.
func TestShowGlobalCommands(t *testing.T) {
	if showGlobalCommands(&config.Defaults{Raw: map[string]any{"show global commands box": 1}}) != true {
		t.Fatal("1 → true")
	}
	if showGlobalCommands(&config.Defaults{Raw: map[string]any{"show global commands box": 0}}) != false {
		t.Fatal("0 → false")
	}
	if showGlobalCommands(&config.Defaults{Raw: map[string]any{}}) != false {
		t.Fatal("отсутствие → false")
	}
}

// #4: список панели — per-conn макросы + глобальные remote commands (при вкл),
// пустые отбрасываются, сортировка по desc.
func TestMacroBarList(t *testing.T) {
	env := &config.Environment{Macros: []map[string]any{
		{"description": "restart", "txt": "sudo restart", "confirm": 1},
		{"description": "z last", "txt": "tail -f /var/log"},
		{"description": "", "txt": "  "}, // пустая cmd → отбрасывается
	}}
	cfg := &config.Config{Defaults: config.Defaults{Raw: map[string]any{
		"show global commands box": 1,
		"remote commands": []any{
			map[string]any{"description": "alpha", "txt": "clear"},
			map[string]any{"description": "beta", "txt": "uptime", "intro": "int"},
		},
	}}}
	got := macroBarList(cfg, env)
	// ожидаем: alpha, beta(с intro), restart, z last — сортировка по desc.
	want := []macroBarItem{
		{Desc: "alpha", Cmd: "clear"},
		{Desc: "beta", Cmd: "uptime", Intro: "int"},
		{Desc: "restart", Cmd: "sudo restart", Confirm: true},
		{Desc: "z last", Cmd: "tail -f /var/log"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("list:\n got %#v\nwant %#v", got, want)
	}
}

// #4: при выключенном 'show global commands box' глобальные не включаются.
func TestMacroBarListNoGlobal(t *testing.T) {
	env := &config.Environment{Macros: []map[string]any{
		{"description": "restart", "txt": "sudo restart"},
	}}
	cfg := &config.Config{Defaults: config.Defaults{Raw: map[string]any{
		"show global commands box": 0,
		"remote commands": []any{
			map[string]any{"description": "alpha", "txt": "clear"},
		},
	}}}
	got := macroBarList(cfg, env)
	if len(got) != 1 || got[0].Cmd != "sudo restart" {
		t.Fatalf("global просочился: %#v", got)
	}
}
