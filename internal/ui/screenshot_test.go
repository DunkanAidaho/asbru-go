package ui

// M32: PACScreenshots — хранение скриншотов соединения (имена/список).

import (
	"testing"
	"time"

	"github.com/DunkanAidaho/asbru-go/internal/config"
)

func TestScreenshotName(t *testing.T) {
	now := time.Date(2026, 8, 27, 15, 4, 5, 0, time.UTC)
	if got := screenshotName(now, 0); got != "asbru_screenshot_20260827_150405_0.png" {
		t.Fatalf("name=%q", got)
	}
	if got := screenshotName(now, 3); got != "asbru_screenshot_20260827_150405_3.png" {
		t.Fatalf("name=%q", got)
	}
}

func TestAddScreenshot(t *testing.T) {
	env := &config.Environment{Screenshots: []any{}}
	addScreenshot(env, "/s/a.png")
	addScreenshot(env, "/s/b.png")
	addScreenshot(env, "/s/a.png") // дедуп
	if len(env.Screenshots) != 2 {
		t.Fatalf("len=%d want 2: %v", len(env.Screenshots), env.Screenshots)
	}
	if env.Screenshots[0] != "/s/a.png" || env.Screenshots[1] != "/s/b.png" {
		t.Fatalf("screenshots=%v", env.Screenshots)
	}
	addScreenshot(nil, "/x.png")
	addScreenshot(env, "")
	if len(env.Screenshots) != 2 {
		t.Fatalf("пустой путь добавлен: %v", env.Screenshots)
	}
}
