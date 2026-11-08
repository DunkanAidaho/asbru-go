package ui

import (
	"testing"

	"github.com/DunkanAidaho/asbru-go/internal/config"
	"github.com/DunkanAidaho/asbru-go/internal/method"
)

// Смена метода в редакторе: конверсия опций происходит мгновенно (баг-фикс M4
// применяется на уровне редактора, а не после сохранения).
func TestEditorMethodChangePerformsConversion(t *testing.T) {
	env := &config.Environment{
		Name:    "Win",
		Method:  "RDP (rdesktop)",
		Options: " -5 -a 24 -k en-us -d DOMAIN -r clipboard:PRIMARYCLIPBOARD",
	}
	wantOpts := "/bpp:24 /kbd:en-us /d:DOMAIN +clipboard"
	converted, unknown := onMethodChange(env, "RDP (xfreerdp)")
	if env.Method != "RDP (xfreerdp)" {
		t.Fatalf("метод не сохранён: %q", env.Method)
	}
	if converted != wantOpts {
		t.Fatalf("опции = %q, want %q (unknown=%v)", converted, wantOpts, unknown)
	}
	if env.Options != converted {
		t.Fatalf("env.Options не обновлён: %q", env.Options)
	}
}

// Неизвестные токены не теряются (passthrough) и попадают в список подсказки.
func TestEditorMethodChangeKeepsUnknown(t *testing.T) {
	env := &config.Environment{
		Name:    "W",
		Method:  "RDP (rdesktop)",
		Options: " -m 4x4 -a 24",
	}
	converted, unknown := onMethodChange(env, "RDP (xfreerdp)")
	if converted != "-m 4x4 /bpp:24" { // порядок исходника сохраняется
		t.Fatalf("опции = %q", converted)
	}
	if len(unknown) != 2 {
		t.Fatalf("unknown = %v", unknown)
	}
}

// Смена на тот же метод — без изменений.
func TestEditorMethodChangeSameMethod(t *testing.T) {
	env := &config.Environment{Name: "W", Method: "RDP (rdesktop)", Options: " -a 24"}
	converted, _ := onMethodChange(env, "RDP (rdesktop)")
	if converted != " -a 24" {
		t.Fatalf("same method изменил опции: %q", converted)
	}
}

// Заголовок ячейки: соединения получают имя, группы — имя и пометку.
func TestEditorTreeLabel(t *testing.T) {
	if got := treeLabel(&config.Environment{Name: "Srv", Method: "SSH"}, false); got != "Srv" {
		t.Fatalf("метка соединения = %q", got)
	}
	if got := treeLabel(&config.Environment{Name: "Grp"}, true); got != "Grp (группа)" {
		t.Fatalf("метка группы = %q", got)
	}
}

// method.ConvertOptions применяется через обёртку редактора.
var _ = method.RDesktop

// Канонизация метода для комбо: легаси-значения rdesktop/xfreerdp/mosh
// должны находить свою строку (ревью F4 — иначе сохранится "SSH").
func TestEditorMethodCanonical(t *testing.T) {
	cases := map[string]string{
		"rdesktop":       "RDP (rdesktop)",
		"xfreerdp":       "RDP (xfreerdp)",
		"mosh":           "MOSH",
		"RDP (rdesktop)": "RDP (rdesktop)",
		"Unknown":        "Unknown",
	}
	for in, want := range cases {
		if got := canonicalMethod(in); got != want {
			t.Errorf("canonicalMethod(%q) = %q, want %q", in, got, want)
		}
	}
}

// Ревью R4: незнакомый метод не должен исчезнуть из combo и стать "SSH"
// при сохранении — наоборот, добавляется своим именем.
func TestComboTargetUnknown(t *testing.T) {
	// незнакомый метод: значение сохраняется, флаг known=false (добавляется
	// отдельным пунктом combo)
	if tr, ok := comboTarget("custom-foo"); ok || tr != "custom-foo" {
		t.Fatalf("unknown: (%q, %v)", tr, ok)
	}
	if tr, ok := comboTarget("rdesktop"); !ok || tr != method.DisplayName(method.RDesktop) {
		t.Fatalf("rdesktop: (%q, %v)", tr, ok)
	}
}

// L-остаток ревью M11: OSC-санитизация названия (нет экшейта из конфига).
func TestEscapeOSC(t *testing.T) {
	got := escapeOSC("we\x07b\x1b\\1\x7f")
	if got != "web\\1" { // управляющие вырезаны, байты \1 остаются
		t.Fatalf("escapeOSC: %q", got)
	}
	if got := escapeOSC("сервер Сп"); got != "сервер Сп" {
		t.Fatalf("UTF-8: %q", got)
	}
}
