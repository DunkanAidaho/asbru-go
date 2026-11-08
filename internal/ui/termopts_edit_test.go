package ui

import (
	"testing"

	"github.com/DunkanAidaho/asbru-go/internal/config"
)

// M29-остаток (#2): вкладка Terminal Options — при 'use personal settings'=off
// показываем глобальные defaults (из d.Raw), а не перекрытия.
func TestTermOptsEditPersonalOffUsesGlobal(t *testing.T) {
	d := &config.Defaults{Raw: map[string]any{
		"terminal font":             "Monospace 12",
		"text color":                "#000000000000",
		"terminal scrollback lines": 8000,
		"terminal transparency":     0.3, // доля
	}}
	env := &config.Environment{}
	e := termOptsEditFromEnv(d, env)
	if e.UsePersonal {
		t.Fatal("без 'use personal settings' флаг должен быть off")
	}
	if e.Font != "Monospace 12" || e.TextColor != "#000000000000" {
		t.Fatalf("global: %+v", e)
	}
	if e.Scrollback != "8000" {
		t.Fatalf("scrollback = %q, want 8000", e.Scrollback)
	}
	if e.Transparency != "30" { // 0.3 → 30%
		t.Fatalf("transparency = %q, want 30", e.Transparency)
	}
	if e.CursorShape != "block" {
		t.Fatalf("cursor default = %q", e.CursorShape)
	}
}

// M29-остаток (#2): per-conn перекрытия применяются при 'use personal settings'=on.
func TestTermOptsEditPersonalOnOverride(t *testing.T) {
	d := &config.Defaults{Raw: map[string]any{"terminal font": "Monospace 12"}}
	env := &config.Environment{TerminalOptions: map[string]any{
		"use personal settings":     1,
		"terminal font":             "Fira Code 10",
		"terminal scrollback lines": 1000,
		"terminal transparency":     0.5,
		"bold color like text":      0,
		"bold color":                "#ff0000000000",
	}}
	e := termOptsEditFromEnv(d, env)
	if !e.UsePersonal {
		t.Fatal("флаг on не считан")
	}
	if e.Font != "Fira Code 10" {
		t.Fatalf("font = %q", e.Font)
	}
	if e.Scrollback != "1000" || e.Transparency != "50" || e.BoldLikeText || e.BoldColor != "#ff0000000000" {
		t.Fatalf("overrides: %+v", e)
	}
}

// M29-остаток (#2): apply → resolveTermOpts уважает флаг (перс. перекрывает).
func TestTermOptsApplyThenResolve(t *testing.T) {
	d := &config.Defaults{Raw: map[string]any{
		"terminal font":             "Monospace 12",
		"terminal scrollback lines": 5000,
	}}
	env := &config.Environment{}

	// on: персональный шрифт применяется.
	applyTermOptsEdit(env, termOptsEdit{UsePersonal: true, Font: "Fira 10",
		Scrollback: "1000", Transparency: "50"})
	o := resolveTermOpts(d, env)
	if o.Font != "Fira 10" {
		t.Fatalf("on: font = %q", o.Font)
	}
	if o.Scrollback != 1000 {
		t.Fatalf("on: scrollback = %d", o.Scrollback)
	}
	if o.Transparency != 50 {
		t.Fatalf("on: transparency = %d", o.Transparency)
	}

	// off: перекрытия игнорируются → глобальный шрифт.
	applyTermOptsEdit(env, termOptsEdit{UsePersonal: false, Font: "Fira 10",
		Scrollback: "1000", Transparency: "50"})
	o = resolveTermOpts(d, env)
	if o.Font != "Monospace 12" || o.Scrollback != 5000 {
		t.Fatalf("off: font=%q scrollback=%d", o.Font, o.Scrollback)
	}
}

// M29-остаток (#2, ревью MINOR B): personal=OFF без личной карты → блок не
// создаётся (иначе каждый сохранённый конфиг получил бы пустой terminal options).
func TestTermOptsApplyOffNoBloat(t *testing.T) {
	env := &config.Environment{}
	applyTermOptsEdit(env, termOptsEdit{UsePersonal: false})
	if env.TerminalOptions != nil {
		t.Fatalf("off без карты: создан блок %v", env.TerminalOptions)
	}
}

// M29-остаток (#2, ревью MINOR B): personal=OFF при существующей карте —
// флаг false, ключи НЕ теряются (инертны, сохранены для повторного включения).
func TestTermOptsApplyOffKeepsConfig(t *testing.T) {
	env := &config.Environment{TerminalOptions: map[string]any{"terminal font": "Fira 10"}}
	applyTermOptsEdit(env, termOptsEdit{UsePersonal: false})
	if env.TerminalOptions["use personal settings"] != false {
		t.Fatalf("флаг не false: %v", env.TerminalOptions)
	}
	if env.TerminalOptions["terminal font"] != "Fira 10" {
		t.Fatalf("ключи потеряны: %v", env.TerminalOptions)
	}
}
