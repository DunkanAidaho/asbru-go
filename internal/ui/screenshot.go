package ui

// M32: PACScreenshots — хранение скриншотов соединения. Современное тестируемое
// ядро (имена/список); фактический захват изображения терминала — UI-часть.
// Легаси: $CFG_DIR/screenshots/asbru_screenshot_<rn>.png, список в cfg.

import (
	"fmt"
	"time"

	"github.com/DunkanAidaho/asbru-go/internal/config"
)

// screenshotName — имя файла скриншота (unix-подобное, с инкрементом для
// уникальности): asbru_screenshot_<YYYYMMDD_HHMMSS>_<n>.png.
func screenshotName(now time.Time, n int) string {
	return fmt.Sprintf("asbru_screenshot_%s_%d.png", now.Format("20060102_150405"), n)
}

// addScreenshot — добавляет путь скриншота в соединение (дедуп, без пустых).
func addScreenshot(env *config.Environment, path string) {
	if env == nil || path == "" {
		return
	}
	for _, s := range env.Screenshots {
		if v, ok := s.(string); ok && v == path {
			return
		}
	}
	env.Screenshots = append(env.Screenshots, path)
}
