package ui

import (
	"github.com/DunkanAidaho/asbru-go/internal/i18n"
	qt "github.com/mappu/miqt/qt6"
)

// M37 (п.3): «глаз» у поля пароля — показать/скрыть введённое значение.
// Иконка отражает ДЕЙСТВИЕ кнопки (скрыт → «показать»), как принято в KDE.

const (
	passEchoNormal   = int(qt.QLineEdit__Normal)
	passEchoPassword = int(qt.QLineEdit__Password)
)

// passEyeIcon — иконка кнопки по текущей видимости пароля.
func passEyeIcon(shown bool) string {
	if shown {
		return "view-hidden"
	}
	return "view-visible"
}

// passToggleState — состояние эха после нажатия глаза (иконка — вместе с ним).
func passToggleState(shown bool) (nextShown bool, echo int, icon string) {
	if !shown {
		return true, passEchoNormal, passEyeIcon(true)
	}
	return false, passEchoPassword, passEyeIcon(false)
}

// passEyeRow — поле пароля с кнопкой-глазом справа. Возвращает контейнер
// (в форму) и саму кнопку (для тестов/подсказок).
func passEyeRow(edit *qt.QLineEdit) (*qt.QWidget, *qt.QPushButton) {
	btn := qt.NewQPushButton3("")
	btn.SetIcon(qt.QIcon_FromTheme(passEyeIcon(false)))
	btn.SetIconSize(qt.NewQSize2(16, 16))
	btn.SetFixedWidth(32)
	btn.SetToolTip(i18n.Tr("Editor", "Показать пароль"))
	shown := false
	btn.OnClicked(func() {
		shown, echo, icon := passToggleState(shown)
		edit.SetEchoMode(qt.QLineEdit__EchoMode(echo))
		btn.SetIcon(qt.QIcon_FromTheme(icon))
		if shown {
			btn.SetToolTip(i18n.Tr("Editor", "Скрыть пароль"))
		} else {
			btn.SetToolTip(i18n.Tr("Editor", "Показать пароль"))
		}
	})
	row := qt.NewQWidget2()
	lay := qt.NewQHBoxLayout2()
	lay.SetContentsMargins(0, 0, 0, 0)
	lay.AddWidget(edit.QWidget)
	lay.AddWidget(btn.QWidget)
	row.SetLayout(lay.QLayout)
	return row, btn
}
