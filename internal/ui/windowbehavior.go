package ui

// M29-остаток: привязка поведения зарегистрированных окно/трей полей defaults к
// приложению (close to tray, minimize to tray, confirm exit, save on exit,
// auto save on exit, start iconified, hide on connect). Чистые геттеры — TDD;
// Qt-привязка (OnCloseEvent/startup/launch) — build-only.

import (
	"sync/atomic"

	"github.com/DunkanAidaho/asbru-go/internal/config"
	"github.com/DunkanAidaho/asbru-go/internal/i18n"
	qt "github.com/mappu/miqt/qt6"
)

// quitting — явный выход (горячая «quit»/трей «Выход»): закрываем окно, минуя
// close-to-tray (иначе «Выход» спрятал бы окно в трей вместо выхода).
var quitting atomic.Bool

// wireWindowClose — поведение закрытия главного окна (M29): close-to-tray →
// confirm-exit → save-on-exit → super (настоящий выход). Qt-нить. При close-to-tray
// и отказе подтверждения — event.Ignore() (иначе окно закрылось бы по умолчанию).
func wireWindowClose(w *qt.QMainWindow, cfg *config.Config, cfgPath string, trayOn bool) {
	w.OnCloseEvent(func(super func(event *qt.QCloseEvent), event *qt.QCloseEvent) {
		explicit := quitting.Load()
		if cfg == nil {
			super(event)
			return
		}
		// close-to-tray: только неявное закрытие (кнопка X), не явный «Выход».
		// ОБЯЗАТЕЛЬНО event.Ignore() — QCloseEvent принят по умолчанию; без
		// Ignore окно закрылось бы (quitOnLastWindowClosed → выход), а не спряталось.
		if !explicit && closeToTray(&cfg.Defaults) && trayOn {
			quitting.Store(false)
			w.Hide()
			event.Ignore()
			return
		}
		// подтверждение выхода (и для X-закрытия без close-to-tray, и для явного
		// «Выход»). Отклонили → event.Ignore() + сброс явного-fлага.
		if confirmExit(&cfg.Defaults) {
			if !showConfirm(w.QWidget, i18n.Tr("Window", "Выйти из Ásbrú?")) {
				quitting.Store(false)
				event.Ignore()
				return
			}
		}
		// сохранить конфиг, затем реальный выход.
		// Геометрия окна пишется ВСЕГДА (свой файл ui-state.json, не зависит от
		// «save on exit») — иначе состояние окна не запоминалось (очный дебаг).
		// M39-fullscreen: полноэкранный режим ВКЛАДКИ — режим сессии, а не окна:
		// выходим из него, чтобы ui-state.json не запомнил фуллскрин и следующий
		// запуск открылся с обычным окном.
		if embedFullID.Load() != 0 {
			exitEmbedFullscreen()
		}
		saveWindowState()
		if saveOnExit(&cfg.Defaults) && cfgPath != "" {
			persistWindowSize(cfg)
			_ = saveCfg(cfg, cfgPath)
		}
		super(event) // accept → close → quitOnLastWindowClosed → выход
	})
}

// closeToTray — «Сворачивать в трей вместо закрытия» (close to tray) или
// «Сворачивать в трей» (minimize to tray): закрытие окна → в трей вместо выхода.
func closeToTray(d *config.Defaults) bool {
	if d == nil {
		return false
	}
	return rawInt(d, "close to tray") == 1 || rawInt(d, "minimize to tray") == 1
}

// confirmExit — «Подтверждать выход»: диалог перед закрытием окна.
func confirmExit(d *config.Defaults) bool {
	if d == nil {
		return false
	}
	return rawInt(d, "confirm exit") == 1
}

// saveOnExit — «Сохранять конфиг при выходе» (save on exit) или
// «Автосохранение при выходе» (auto save on exit): персист конфига на выходе.
func saveOnExit(d *config.Defaults) bool {
	if d == nil {
		return false
	}
	return rawInt(d, "save on exit") == 1 || rawInt(d, "auto save on exit") == 1
}

// startIconifiedDefault — «Запускать свёрнутым в трей» (defaults-флаг, без --iconified).
func startIconifiedDefault(d *config.Defaults) bool {
	if d == nil {
		return false
	}
	return rawInt(d, "start iconified") == 1
}
