package ui

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/DunkanAidaho/asbru-go/internal/config"
	"github.com/DunkanAidaho/asbru-go/internal/i18n"
	qt "github.com/mappu/miqt/qt6"
)

// M19: диалог настроек (тонкая Qt-плёнка поверх реестра prefFields).
// Вся семантика полей — в prefs.go; здесь только виджеты и сохранение.

// tabLabel — заголовок вкладки настроек. Литеральные вызовы i18n.Tr в функции
// (а не в пакетном map): экстрактор видит их и перевод читается в рантайме,
// после SetTranslate (ревью E6 — иначе tabLabels[tab] из-за IndexExpr не попал
// бы в каталог и строки бы не переводились).
func tabLabel(tab string) string {
	switch tab {
	case tabGeneral:
		return i18n.Tr("Prefs", "Общие")
	case tabPrompts:
		return i18n.Tr("Prefs", "Промпты")
	case tabKeepass:
		return i18n.Tr("Prefs", "KeePass")
	case tabProxy:
		return i18n.Tr("Prefs", "Прокси")
	case tabJump:
		return i18n.Tr("Prefs", "Jump")
	case tabMain:
		return i18n.Tr("Prefs", "Окно")
	case tabTerm:
		return i18n.Tr("Prefs", "Терминал")
	case tabShell:
		return i18n.Tr("Prefs", "Shell")
	case tabLogs:
		return i18n.Tr("Prefs", "Логи")
	case tabNet:
		return i18n.Tr("Prefs", "Сеть")
	case tabScreen:
		return i18n.Tr("Prefs", "Скриншоты")
	}
	// Известные вкладки покрыты выше; на будущий неизвестный ключ — не пустой
	// заголовок и предупреждение (ревью E9 / defensивный default).
	slog.Warn("prefs: заголовок неизвестной вкладки", "tab", tab)
	return tab
}

func showErr(parent *qt.QWidget, msg string) {
	m := qt.NewQMessageBox3(qt.QMessageBox__Warning, "Ásbrú CM", msg)
	m.AddButtonWithButton(qt.QMessageBox__Ok)
	m.Exec()
}

// showConfirm — вопрос Да/Нет (возвращает выбор ОК).
// ВАЖНО: QMessageBox.Exec() возвращает нажатую StandardButton (Ok=1024),
// а НЕ QDialog__Accepted (1) — сравнение с последним всегда давало false
// («Ok не работает»: закрытие игнорировалось, очный дебаг issue).
func showConfirm(parent *qt.QWidget, msg string) bool {
	m := qt.NewQMessageBox3(qt.QMessageBox__Warning, "Ásbrú CM", msg)
	m.AddButtonWithButton(qt.QMessageBox__Ok)
	m.AddButtonWithButton(qt.QMessageBox__Cancel)
	return m.Exec() == int(qt.QMessageBox__Ok)
}

// showPreferences — модальное окно настроек; сохраняет в конфиг сразу.
func showPreferences(parent *qt.QWidget, cfg *config.Config, cfgPath string) {
	d := qt.NewQDialog2()
	d.SetWindowTitle(i18n.Tr("Prefs", "Настройки"))
	d.SetMinimumWidth(560)
	tabs := qt.NewQTabWidget2()

	// поля в порядке реестра, сгруппированы по табам
	type row struct {
		f     prefField
		check *qt.QCheckBox
		edit  *qt.QLineEdit
		combo *qt.QComboBox
	}
	byTab := map[string][]prefField{}
	var order []string
	for _, f := range prefFields() {
		if _, ok := byTab[f.Tab]; !ok {
			order = append(order, f.Tab)
		}
		byTab[f.Tab] = append(byTab[f.Tab], f)
	}

	rows := map[string]*row{}
	bind := func(f prefField, form *qt.QFormLayout) *row {
		r := &row{f: f}
		switch f.Kind {
		case prefCheck:
			r.check = qt.NewQCheckBox3(f.Label)
			r.check.SetChecked(f.Get(&cfg.Defaults) == "1")
			form.AddRow3("", r.check.QWidget)
		case prefCombo:
			r.combo = qt.NewQComboBox2()
			for _, c := range f.Combo {
				r.combo.AddItem(c)
			}
			// значение + fallback на первый пункт (пустое/неизвестное → 'No').
			r.combo.SetCurrentIndex(comboIndex(f.Combo, f.Get(&cfg.Defaults)))
			form.AddRow3(f.Label, r.combo.QWidget)
		default:
			r.edit = qt.NewQLineEdit3(f.Get(&cfg.Defaults))
			if f.Kind == prefPass {
				r.edit.SetEchoMode(qt.QLineEdit__Password)
			}
			form.AddRow3(f.Label, r.edit.QWidget)
		}
		rows[f.Key] = r
		return r
	}

	// GUI-пароль: строки «текущий/новый» внутри вкладки Общие.
	cur := qt.NewQLineEdit2()
	next := qt.NewQLineEdit2()
	rep := qt.NewQLineEdit2()
	for _, e := range []*qt.QLineEdit{cur, next, rep} {
		e.SetEchoMode(qt.QLineEdit__Password)
	}

	for _, tab := range order {
		form := qt.NewQFormLayout2()
		for _, f := range byTab[tab] {
			bind(f, form)
			if f.Key == "use gui password" {
				form.AddRow3(i18n.Tr("Prefs", "Текущий пароль"), cur.QWidget)
				form.AddRow3(i18n.Tr("Prefs", "Новый пароль"), next.QWidget)
				form.AddRow3(i18n.Tr("Prefs", "Повтор нового пароля"), rep.QWidget) // легаси new1/new2 (M19-ревью)
			}
		}
		w, _ := mkVTab(form)
		tabs.AddTab(w, tabLabel(tab))
	}

	box := qt.NewQVBoxLayout2()
	box.AddWidget(tabs.QWidget)

	dialogButtons(box, d, i18n.Tr("Dialog", "Сохранить"), func() {
		vals := map[string]string{}
		for key, r := range rows {
			if r.check != nil {
				v := "0"
				if r.check.IsChecked() {
					v = "1"
				}
				vals[key] = v
			} else if r.combo != nil {
				vals[key] = r.combo.CurrentText()
			} else {
				vals[key] = r.edit.Text()
			}
		}
		// GUI-пароль — особый поток: только при реальном изменении.
		wantPass := vals["use gui password"] == "1"
		wasEnabled := cfg.Defaults.UseGUIPassword == 1
		// M19-ревью (паритет): снятие защиты требует текущий пароль
		// (легаси PACMain.pm:2451-2452, _unlockAsbru).
		if wasEnabled && !wantPass && !guiPassCheck(&cfg.Defaults, cur.Text()) {
			showErr(d.QWidget, i18n.Tr("Prefs", "Для снятия защиты введите текущий пароль в поле «Текущий пароль»"))
			return
		}
		if next.Text() != "" && next.Text() != rep.Text() {
			showErr(d.QWidget, i18n.Tr("Prefs", "Новый пароль и его повтор не совпадают"))
			return
		}
		if wantPass && cfg.Defaults.GUIPassword == "" && next.Text() == "" {
			showErr(d.QWidget, i18n.Tr("Prefs", "Защита включена, но пароль не задан.\nВведите новый пароль или отключите защиту."))
			return
		}
		if next.Text() != "" {
			if err := guiPassSet(&cfg.Defaults, cur.Text(), next.Text()); err != nil {
				showErr(d.QWidget, i18n.Tr("Prefs", "Ошибка смены пароля GUI: ")+err.Error())
				return
			}
		}
		// item 7-x: зашифрованный GUI-пароль держим и в SecretService (best-effort,
		// не блокирует сохранение — конфиг уже содержит зашифрованный). Это даёт
		// resolveGUIPasswordFromSS что подставить на старте до гейта.
		if err := applyPrefs(&cfg.Defaults, vals); err != nil {
			showErr(d.QWidget, err.Error())
			return
		}
		// item 7-x: зашифрованный GUI-пароль держим и в SecretService (best-effort,
		// не блокирует сохранение — конфиг уже содержит зашифрованный). Это даёт
		// resolveGUIPasswordFromSS что подставить на старте до гейта. MINOR-2:
		// при снятии галки — убрать blob из SS (иначе остаётся насовсем).
		if cfg.Defaults.UseGUIPassword == 1 {
			_ = StoreGUIPasswordToSS(&cfg.Defaults)
		} else {
			_ = DeleteGUIPasswordFromSS()
		}
		if err := saveCfg(cfg, cfgPath); err != nil {
			slog.Error("сохранение настроек", "err", err)
			showErr(d.QWidget, i18n.Tr("Prefs", "Не удалось сохранить настройки: ")+err.Error())
			return
		}
		d.Accept()
	})
	d.SetLayout(box.QLayout)
	d.Exec()
}

// guiPassGate — пароль на старте (легаси PACMain.pm:243-258): один ввод,
// неверный — приложение закрывается. Возвращает false, если надо выйти.
// M25: cliPass (--password) подставляется вместо диалога; при неверном
// CLI-пароле — stderr, без GUI-диалога (приложение всё равно выходит).
func guiPassGate(cfg *config.Config, parent *qt.QWidget, cliPass string) bool {
	if cfg.Defaults.UseGUIPassword != 1 {
		return true
	}
	// item 7-x: GUI-пароль может жить в SecretService (зашифрованный блоб).
	// Подставить, если есть, — иначе остаётся сохранённый в конфиге.
	resolveGUIPasswordFromSS(&cfg.Defaults)
	pass := cliPass
	if pass == "" {
		pass = inputTextEcho(parent, i18n.Tr("Prefs", "Пароль GUI"), i18n.Tr("Prefs", "Введите пароль GUI..."), "", true)
		if pass == "" {
			return false // отмена = выход, как в оригинале (exit 0)
		}
	}
	if !guiPassCheck(&cfg.Defaults, pass) {
		if cliPass == "" {
			showErr(parent, i18n.Tr("Prefs", "ERROR: Неверный пароль!!"))
		} else {
			fmt.Fprintln(os.Stderr, "ERROR: Wrong password!!")
		}
		return false
	}
	return true
}
