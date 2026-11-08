package ui

// M27: Terminal Options — разрешённые параметры терминала:
// глобальные defaults + per-connection override (env.terminal options,
// активируется 'use personal settings'; легаси PACTerminal.pm:3974-3999).

import (
	"testing"

	"github.com/DunkanAidaho/asbru-go/internal/config"
)

// пустые defaults + env → все дефолтные значения (Global запуска работает).
// M44: история прокрутки — 5000, а не 0: в qtermwidget setHistorySize(0) это
// HistoryTypeNone, то есть истории НЕТ ВООЩЕ, и колесо/Shift+PageUp мертвы
// (отсутствие ключа не означает «без истории» — так же считают сид, редактор
// соединения и диалог настроек).
func TestResolveTermOptsDefaults(t *testing.T) {
	o := resolveTermOpts(&config.Defaults{}, nil)
	if o.Font != "" || o.Transparency != 0 {
		t.Fatalf("ожидали пустые дефолты, got %+v", o)
	}
	if o.Scrollback != 5000 {
		t.Fatalf("Scrollback = %d, want 5000 (дефолт истории)", o.Scrollback)
	}
}

// Явный 0 в конфиге — осознанный выбор «без истории»: уважаем.
func TestResolveTermOptsScrollbackExplicitZero(t *testing.T) {
	d := &config.Defaults{Raw: map[string]any{"terminal scrollback lines": 0}}
	if o := resolveTermOpts(d, nil); o.Scrollback != 0 {
		t.Fatalf("Scrollback = %d, want 0 (явный ноль в конфиге)", o.Scrollback)
	}
}

// Нечисловое значение ключа не должно превращаться в «без истории».
func TestResolveTermOptsScrollbackNonNumeric(t *testing.T) {
	d := &config.Defaults{Raw: map[string]any{"terminal scrollback lines": "много"}}
	if o := resolveTermOpts(d, nil); o.Scrollback != 5000 {
		t.Fatalf("Scrollback = %d, want 5000 (нечисловой ключ → дефолт)", o.Scrollback)
	}
}

// глобальные defaults из Raw подставляются, когда нет личных настроек
func TestResolveTermOptsGlobal(t *testing.T) {
	d := &config.Defaults{Raw: map[string]any{
		"terminal font":             "Monospace 9",
		"text color":                "#cc62cc62cc62",
		"back color":                "#000000000000",
		"cursor shape":              "block",
		"terminal scrollback lines": 5000,
		"terminal transparency":     0.3,
	}}
	o := resolveTermOpts(d, nil)
	if o.Font != "Monospace 9" {
		t.Fatalf("Font = %q", o.Font)
	}
	if o.TextColor != "#cc62cc62cc62" {
		t.Fatalf("TextColor = %q", o.TextColor)
	}
	if o.CursorShape != "block" {
		t.Fatalf("CursorShape = %q, want block", o.CursorShape)
	}
	if o.Scrollback != 5000 || o.Transparency != 30 {
		t.Fatalf("scrollback/transparency = %d/%d, want 5000/30", o.Scrollback, o.Transparency)
	}
}

// use personal settings → override берётся из env.terminal options
func TestResolveTermOptsPersonal(t *testing.T) {
	d := &config.Defaults{Raw: map[string]any{
		"terminal font": "Monospace 9",
		"cursor shape":  "block",
	}}
	env := &config.Environment{TerminalOptions: map[string]any{
		"use personal settings": true,
		"terminal font":         "Courier 12",
		"cursor shape":          "ibeam",
		"terminal transparency": 0.15,
	}}
	o := resolveTermOpts(d, env)
	if o.Font != "Courier 12" {
		t.Fatalf("Font = %q, want Courier 12", o.Font)
	}
	if o.CursorShape != "ibeam" {
		t.Fatalf("CursorShape = %q, want ibeam", o.CursorShape)
	}
	if o.Transparency != 15 {
		t.Fatalf("Transparency = %v, want 15", o.Transparency)
	}
}

// M29-ревью I1: 'terminal transparency' хранится как 0-1 доля (seed 0.3),
// internal — проценты (0-100). rawInt ранее делал int(0.3)=0, теряя значение.
func TestResolveTermOptsTransparencyFraction(t *testing.T) {
	d := &config.Defaults{Raw: map[string]any{"terminal transparency": 0.3}}
	if o := resolveTermOpts(d, nil); o.Transparency != 30 {
		t.Fatalf("Transparency = %d, want 30 (доля 0.3 → 30%%)", o.Transparency)
	}
	// кламп: доля >1 (мусор) → 100%.
	d = &config.Defaults{Raw: map[string]any{"terminal transparency": 1.7}}
	if o := resolveTermOpts(d, nil); o.Transparency != 100 {
		t.Fatalf("Transparency = %d, want 100 (кламп)", o.Transparency)
	}
}

// use personal settings = 0 → личные настройки игнорируются (берутся defaults)
func TestResolveTermOptsPersonalOff(t *testing.T) {
	d := &config.Defaults{Raw: map[string]any{"terminal font": "Monospace 9"}}
	env := &config.Environment{TerminalOptions: map[string]any{
		"use personal settings": 0,
		"terminal font":         "Courier 12",
	}}
	if o := resolveTermOpts(d, env); o.Font != "Monospace 9" {
		t.Fatalf("Font = %q, want глобальный Monospace 9", o.Font)
	}
}

// M44-ревью MAJOR-1: диалог настроек читал отсутствующий ключ как 0 и записывал
// его же обратно при «Сохранить» — то есть открыть настройки по любому поводу и
// сохранить означало молча убить историю терминала (при явном 0 resolveTermOpts
// честно ставит HistoryTypeNone). Значения диалога, редактора соединения и
// рантайма обязаны совпадать.
func TestPrefsScrollbackMatchesTerminal(t *testing.T) {
	var field *prefField
	for i := range prefFields() {
		if prefFields()[i].Key == "terminal scrollback lines" {
			f := prefFields()[i]
			field = &f
			break
		}
	}
	if field == nil {
		t.Fatal("в реестре настроек нет поля 'terminal scrollback lines'")
	}
	d := &config.Defaults{}
	if got := field.Get(d); got != "5000" {
		t.Fatalf("настройки показывают %q, рантайм/редактор — 5000", got)
	}
	// «Сохранить» без правки поля не должно превращать отсутствие ключа в явный 0.
	field.Set(d, field.Get(d))
	if got := rawInt(d, "terminal scrollback lines"); got != 5000 {
		t.Fatalf("после сохранения настроек история = %d, want 5000", got)
	}
	if got := resolveTermOpts(d, nil).Scrollback; got != 5000 {
		t.Fatalf("после сохранения настроек resolveTermOpts = %d, want 5000", got)
	}
}

// M44-ревью MINOR-4: тот же класс дефекта у 'bold color like text' — редактор
// соединения показывает включённую галочку (дефолт 1), а рантайм считал 0.
func TestBoldLikeTextDefaultMatchesEditor(t *testing.T) {
	if got := resolveTermOpts(&config.Defaults{}, nil).BoldLikeText; !got {
		t.Error("рантайм считает 'bold color like text' выключенным, а редактор соединения — включённым (дефолт 1)")
	}
}

// Числовая строка в ключе ('1000' — YAML отдал значение строкой) понимается как
// число: раньше рантайм брал дефолт, а настройки и редактор показывали 1000
// (ревью M44, остаток №2).
func TestResolveTermOptsScrollbackNumericString(t *testing.T) {
	d := &config.Defaults{Raw: map[string]any{"terminal scrollback lines": "1000"}}
	if o := resolveTermOpts(d, nil); o.Scrollback != 1000 {
		t.Fatalf("Scrollback = %d, want 1000 (числовая строка из конфига)", o.Scrollback)
	}
	// Пробелы вокруг числа не должны отправлять значение в дефолт.
	d2 := &config.Defaults{Raw: map[string]any{"terminal scrollback lines": " 700 "}}
	if o := resolveTermOpts(d2, nil); o.Scrollback != 700 {
		t.Fatalf("Scrollback = %d, want 700 (пробелы вокруг числа)", o.Scrollback)
	}
}
