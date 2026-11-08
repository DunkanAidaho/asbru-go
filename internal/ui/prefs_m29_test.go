package ui

// M29: Preferences-расширение — покрытие documented-опций (doc/Manual/Preferences/*,
// res/asbru.yml defaults). Новые поля регистрируются в prefFields (Raw-backed,
// как 'max retry on disconnect' в M25). Существующие тесты round-trip/coverage
// не ломаются.

import (
	"testing"

	"github.com/DunkanAidaho/asbru-go/internal/config"
)

// M29: каталог новых полей предпочтений присутствует в реестре.
func TestPrefFieldsM29Coverage(t *testing.T) {
	want := []string{
		"close to tray", "hide on connect", "start iconified",
		"confirm exit", "auto save on exit", "open connections in tabs",
		"tabs in main window", "save on exit",
		"terminal font", "text color", "back color", "cursor shape",
		"terminal scrollback lines", "terminal transparency",
		"terminal windows hsize", "terminal windows vsize",
		"shell binary", "shell options",
		"save session logs", "session logs amount", "session logs folder",
		// backlog №2 (M29-остаток):
		"close terminal on disconnect", "tabs position", "use bw icon",
		"auto hide connections list", "minimize to tray", "force split tabs to 50%",
		"ping port before connect", "ping port timeout",
		"screenshots use external viewer", "screenshots external viewer",
		// M29-остаток (регистрация seed-ключей defaults, ещё не заведённых в
		// реестр): цвета состояния, окно-старт, сессии, сеть.
		// (seed-ключи 'debug'/'only one instance' НЕ регистрируются — не
		// потребляются в Go-порте; см. prefs.go.)
		"connected color", "disconnected color", "new data color",
		"intro reconnects", "record command history", "show screenshots",
		"start main maximized", "start maximized", "use system proxy",
		// #4: панель макросов терминала.
		"show commands box", "show global commands box",
	}
	got := map[string]bool{}
	for _, f := range prefFields() {
		got[f.Key] = true
	}
	for _, k := range want {
		if !got[k] {
			t.Errorf("M29: нет поля предпочтений %q", k)
		}
	}
}

// M29: новые поля корректно round-trip (Get/Set) — проверка Raw-backed маппинга.
func TestPrefFieldsM29RoundTrip(t *testing.T) {
	for _, f := range prefFields() {
		switch f.Key {
		case "close to tray", "hide on connect", "start iconified",
			"confirm exit", "auto save on exit", "open connections in tabs",
			"tabs in main window", "save on exit", "save session logs",
			"terminal scrollback lines", "terminal transparency",
			"terminal windows hsize", "terminal windows vsize",
			"session logs amount",
			// M29-остаток: новые check/num-поля (Raw-backed).
			"intro reconnects", "record command history", "show screenshots",
			"start maximized", "start main maximized", "use system proxy":
			d := &config.Defaults{Raw: map[string]any{}}
			f.Set(d, "1")
			if got := f.Get(d); got != "1" {
				t.Errorf("%s: round-trip = %q, want 1", f.Key, got)
			}
		}
	}
}

// #4-ревью (MINOR 2): 'show commands box' — Get должен маппить int-конфиг
// (легаси) в метку, иначе save затирает макробар в "Never".
func TestPrefShowCommandsBoxMapsInt(t *testing.T) {
	var field prefField
	for _, f := range prefFields() {
		if f.Key == "show commands box" {
			field = f
			break
		}
	}
	if field.Key == "" {
		t.Fatal("нет поля show commands box")
	}
	if field.Kind != prefCombo {
		t.Fatalf("kind = %v, want prefCombo", field.Kind)
	}
	for raw, label := range map[any]string{1: "Combobox", 2: "Buttons", 0: "Never"} {
		d := &config.Defaults{Raw: map[string]any{"show commands box": raw}}
		if got := field.Get(d); got != label {
			t.Errorf("raw=%v: Get=%q, want %q", raw, got, label)
		}
	}
	d := &config.Defaults{Raw: map[string]any{"show commands box": "Buttons"}}
	if got := field.Get(d); got != "Buttons" {
		t.Fatalf("string Get=%q, want Buttons", got)
	}
	// Save (Set) записывает метку, макробар-режим не ломается.
	field.Set(d, "Combobox")
	if macroBarMode(d) != 1 {
		t.Fatalf("Set→mode=%d, want 1", macroBarMode(d))
	}
}

// M29-остаток: цветовые поля (prefText, hex-строки) round-trip без искажения.
func TestPrefFieldsM29ColorRoundTrip(t *testing.T) {
	for _, key := range []string{"connected color", "disconnected color", "new data color"} {
		var field prefField
		for _, f := range prefFields() {
			if f.Key == key {
				field = f
				break
			}
		}
		if field.Key == "" {
			t.Fatalf("нет поля %q", key)
		}
		if field.Kind != prefText {
			t.Fatalf("%s: kind = %v, want prefText", key, field.Kind)
		}
		d := &config.Defaults{Raw: map[string]any{}}
		const hex = "#0c0cbaba0000"
		field.Set(d, hex)
		if got := field.Get(d); got != hex {
			t.Fatalf("%s: round-trip = %q, want %q", key, got, hex)
		}
	}
}

// M29-ревью I1: 'terminal transparency' — доля (0-1), internal — проценты.
// Ранее rawInt делал int(0.3)=0, диалог показывал 0 и при сохранении затирал
// значение. Теперь Get отдаёт проценты (30), Set кладёт долю (0.5 → 50%).
func TestPrefFieldsM29TransparencyRoundTrip(t *testing.T) {
	var field prefField
	for _, f := range prefFields() {
		if f.Key == "terminal transparency" {
			field = f
			break
		}
	}
	if field.Key == "" {
		t.Fatal("нет поля terminal transparency")
	}
	d := &config.Defaults{Raw: map[string]any{"terminal transparency": 0.3}}
	if got := field.Get(d); got != "30" {
		t.Fatalf("Get = %q, want 30 (0.3 → 30%%)", got)
	}
	field.Set(d, "50")
	if got := field.Get(d); got != "50" {
		t.Fatalf("Get после Set = %q, want 50", got)
	}
	if v, ok := d.Raw["terminal transparency"].(float64); !ok || v != 0.5 {
		t.Fatalf("Raw = %v (%T), want 0.5", d.Raw["terminal transparency"], d.Raw["terminal transparency"])
	}
	// мусор → не перетираем предыдущее значение (0.5 → 50%).
	field.Set(d, "abc")
	if got := field.Get(d); got != "50" {
		t.Fatalf("Get после мусора = %q, want 50", got)
	}
}
