package ui

import (
	"embed"
	"log/slog"
	"strings"

	qt "github.com/mappu/miqt/qt6"
)

// iconPNGs — встроенный набор PNG-размеров иконки приложения (16–512 px).
// Мастер assets/asbru-icon.svg лежит рядом для правок; в бинарник попадают
// только PNG, чтобы не зависеть от Qt-плагина SVG и от установленной темы
// иконок. Иконка должна работать у «голого» бинарника без пакета иконок.
//
//go:embed assets/asbru-icon-*.png
var iconPNGs embed.FS

// applyAppIcon ставит иконку приложения из встроенных PNG. Вызывать сразу
// после qt.NewQApplication: QGuiApplication_SetWindowIcon задаёт иконку по
// умолчанию для всех окон, созданных позже (включая главное окно).
func applyAppIcon() {
	entries, err := iconPNGs.ReadDir("assets")
	if err != nil {
		slog.Warn("app icon: чтение встроенных файлов", "err", err)
		return
	}
	icon := qt.NewQIcon()
	loaded := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, "asbru-icon-") || !strings.HasSuffix(name, ".png") {
			continue
		}
		data, err := iconPNGs.ReadFile("assets/" + name)
		if err != nil {
			slog.Warn("app icon: файл не прочитан", "file", name, "err", err)
			continue
		}
		pm := qt.NewQPixmap()
		if !pm.LoadFromDataWithData(data) {
			slog.Warn("app icon: PNG не декодирован", "file", name)
			continue
		}
		icon.AddPixmap(pm)
		loaded++
	}
	if loaded == 0 {
		slog.Warn("app icon: ни один размер не загружен")
		return
	}
	qt.QGuiApplication_SetWindowIcon(icon)
}
