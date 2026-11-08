package ui

// M31: панель Info — строки "метка: значение" о соединении + Qt-виджет.
// Чистая функция connInfoLines (тестируемая), показ — QFormLayout.

import (
	"github.com/DunkanAidaho/asbru-go/internal/config"
	"github.com/DunkanAidaho/asbru-go/internal/i18n"
	qt "github.com/mappu/miqt/qt6"
)

// infoLine — "метка: значение" в панели Info.
type infoLine struct {
	Label string
	Value string
}

// connInfoLines — непустые поля соединения как строки метка/значение.
func connInfoLines(env *config.Environment) []infoLine {
	if env == nil {
		return nil
	}
	var out []infoLine
	add := func(label, value string) {
		if value != "" {
			out = append(out, infoLine{label, value})
		}
	}
	add(i18n.Tr("Info", "Имя"), env.Name)
	add(i18n.Tr("Info", "Описание"), env.Description)
	add(i18n.Tr("Info", "Заголовок"), env.Title)
	add(i18n.Tr("Info", "IP"), env.IP)
	add(i18n.Tr("Info", "Порт"), env.Port)
	add(i18n.Tr("Info", "Пользователь"), env.User)
	add(i18n.Tr("Info", "Метод"), env.Method)
	add(i18n.Tr("Info", "Тип аутентификации"), env.AuthType)
	add(i18n.Tr("Info", "Общедоступный ключ"), env.PublicKey)
	return out
}

// showInfo — панель Info: метка/значение в QFormLayout.
func showInfo(env *config.Environment, parent *qt.QWidget) {
	d := qt.NewQDialog2()
	d.SetWindowTitle(i18n.Tr("Info", "Информация о соединении"))
	d.SetMinimumWidth(380)

	form := qt.NewQFormLayout2()
	for _, l := range connInfoLines(env) {
		val := qt.NewQLabel3(l.Value)
		val.SetWordWrap(true)
		form.AddRow3(l.Label, val.QWidget)
	}

	close := qt.NewQPushButton3(i18n.Tr("Info", "Закрыть"))
	close.OnClicked(d.Reject)
	buttons := qt.NewQHBoxLayout2()
	buttons.AddStretch()
	buttons.AddWidget(close.QWidget)

	box := qt.NewQVBoxLayout2()
	box.AddLayout(form.QLayout)
	box.AddLayout(buttons.QLayout)
	d.SetLayout(box.QLayout)
	d.Exec()
}
