package ui

import (
	"testing"

	"github.com/DunkanAidaho/asbru-go/internal/config"
)

// M29-остаток: геттеры поведения окно/трей полей.
func TestWindowBehaviorGetters(t *testing.T) {
	d := &config.Defaults{Raw: map[string]any{"close to tray": 1}}
	if !closeToTray(d) {
		t.Fatal("close to tray=1 → true")
	}
	if closeToTray(&config.Defaults{Raw: map[string]any{"close to tray": 0}}) {
		t.Fatal("close to tray=0 → false")
	}
	if !closeToTray(&config.Defaults{Raw: map[string]any{"minimize to tray": 1}}) {
		t.Fatal("minimize to tray=1 → true (OR)")
	}
	if closeToTray(nil) {
		t.Fatal("nil → false")
	}

	if !confirmExit(&config.Defaults{Raw: map[string]any{"confirm exit": 1}}) {
		t.Fatal("confirm exit=1 → true")
	}
	if confirmExit(&config.Defaults{Raw: map[string]any{}}) {
		t.Fatal("отсутствие → false")
	}

	if !saveOnExit(&config.Defaults{Raw: map[string]any{"save on exit": 1}}) {
		t.Fatal("save on exit=1 → true")
	}
	if !saveOnExit(&config.Defaults{Raw: map[string]any{"auto save on exit": 1}}) {
		t.Fatal("auto save on exit=1 → true (OR)")
	}
	if saveOnExit(&config.Defaults{Raw: map[string]any{}}) {
		t.Fatal("отсутствие → false")
	}

	if !startIconifiedDefault(&config.Defaults{Raw: map[string]any{"start iconified": 1}}) {
		t.Fatal("start iconified=1 → true")
	}
	if startIconifiedDefault(&config.Defaults{Raw: map[string]any{}}) {
		t.Fatal("отсутствие → false")
	}
}
