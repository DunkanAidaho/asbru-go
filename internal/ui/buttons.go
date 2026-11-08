package ui

import (
	"log/slog"
	"strings"
	"unsafe"

	"github.com/DunkanAidaho/asbru-go/internal/config"
	"github.com/DunkanAidaho/asbru-go/internal/i18n"
	"github.com/DunkanAidaho/asbru-go/internal/ui/wgt"
	qt "github.com/mappu/miqt/qt6"
)

// buildTreeButtons — панель под деревом. user-тест #2 «выделить частые»:
// частые действия (Start selected / Добавить группу / Добавить соединение /
// Редактировать) — иконки в ряд; остальные (Переименовать / Экспорт / Импорт /
// PCC / Настройки / Удалить) — в меню «Ещё…».
func buildTreeButtons(tw *qt.QTreeWidget, cfg **config.Config, byItem *map[unsafe.Pointer]string,
	cfgPath string, reload func()) *qt.QWidget {
	box := qt.NewQHBoxLayout2()

	// иконки-без-подписей (дизайн-аудит P0: текстовые кнопки в ряд ломали сплит).
	// user-тест #2: частые — иконки, реже — «Ещё…» (текстовая кнопка-меню).
	mk := func(icon, tooltip string, fn func()) *qt.QPushButton {
		b := qt.NewQPushButton3("")
		b.SetIcon(qt.QIcon_FromTheme(icon))
		b.SetIconSize(qt.NewQSize2(18, 18))
		b.SetFixedHeight(32)
		b.SetToolTip(tooltip)
		b.OnClicked(fn)
		box.AddWidget(b.QWidget)
		return b
	}

	selectedEnv := func() (string, *config.Environment) {
		item := tw.CurrentItem()
		if item == nil {
			return "", nil
		}
		uuid, ok := (*byItem)[itemID(item)]
		if !ok {
			return "", nil
		}
		e := (*cfg).Environments[uuid]
		return uuid, &e
	}
	parentOf := func(uuid string, env *config.Environment) string {
		if env.IsGroup == 1 {
			return uuid
		}
		if env.Parent != "" && (*cfg).Environments[env.Parent].IsGroup == 1 {
			return env.Parent
		}
		return config.RootUUID
	}

	save := func() {
		if err := saveCfgUI(*cfg, cfgPath, tw.QWidget); err != nil {
			// диалог уже показан (ErrReadOnly) или ошибка в логе
		}
		reload()
	}

	// ---- ЧАСТЫЕ действия: иконки в ряд -------------------------------
	mk("media-playback-start", i18n.Tr("Toolbar", "Запустить выделенное"), func() {
		uuid, env := selectedEnv()
		if env == nil {
			return
		}
		if env.IsGroup == 1 {
			for _, u := range (*cfg).GroupConnections(uuid) {
				if e, okc := (*cfg).Environments[u]; okc {
					launchConnection(u, &e, (*cfg), firstCluster(e.Cluster))
				}
			}
			return
		}
		launchConnection(uuid, env, (*cfg), firstCluster(env.Cluster))
	})
	mk("folder-new", i18n.Tr("Toolbar", "Добавить группу"), func() {
		uuid, env := selectedEnv()
		parent := config.RootUUID
		if env != nil {
			parent = parentOf(uuid, env)
		}
		if nodeProtected(*cfg, parent) {
			showErr(tw.QWidget, i18n.Tr("Toolbar", "Узел защищён: создание группы под ним запрещено"))
			return
		}
		name := strings.TrimSpace(inputText(tw.QWidget, i18n.Tr("Toolbar", "Новая группа"), i18n.Tr("Toolbar", "Имя группы"), ""))
		if name == "" {
			return
		}
		if _, err := addEnv(*cfg, parent, &config.Environment{Name: name, IsGroup: 1, Parent: parent}); err != nil {
			slog.Error("создание группы", "err", err)
			showErr(tw.QWidget, i18n.Tr("Toolbar", "Не удалось создать группу")+": "+err.Error())
			return
		}
		save()
	})
	mk("list-add", i18n.Tr("Toolbar", "Добавить соединение"), func() {
		uuid, env := selectedEnv()
		parent := config.RootUUID
		if env != nil {
			parent = parentOf(uuid, env)
		}
		if nodeProtected(*cfg, parent) {
			showErr(tw.QWidget, i18n.Tr("Toolbar", "Узел защищён: создание соединения под ним запрещено"))
			return
		}
		name := strings.TrimSpace(inputText(tw.QWidget, i18n.Tr("Toolbar", "Новое соединение"), i18n.Tr("Toolbar", "Имя соединения"), ""))
		if name == "" {
			return
		}
		newUUID, err := addEnv(*cfg, parent, &config.Environment{Name: name, Method: "SSH"})
		if err != nil {
			slog.Error("создание соединения", "err", err)
			showErr(tw.QWidget, i18n.Tr("Toolbar", "Не удалось создать соединение")+": "+err.Error())
			return
		}
		save()
		// Ревью 2026-10-14: симметрия с tree_menu addConn — сразу открываем
		// редактор (создание протокол-зависимо: auth/опции зависят от метода),
		// при отмене откатываем заглушку (иначе фантомный SSH при следующем save).
		if env, ok := (*cfg).Environments[newUUID]; ok {
			saved := editConnection(tw.QWidget, &env, newUUID, func(updated *config.Environment) {
				(*cfg).Environments[newUUID] = *updated
				save()
			})
			if !saved {
				if rmErr := removeEnv(*cfg, newUUID); rmErr != nil {
					slog.Error("откат нового соединения", "err", rmErr)
				}
				save()
			}
		}
	})
	mk("document-edit", i18n.Tr("Toolbar", "Редактировать"), func() {
		uuid, env := selectedEnv()
		if env == nil || env.IsGroup == 1 {
			return
		}
		if isProtected(env) {
			showErr(tw.QWidget, i18n.Tr("Toolbar", "Узел защищён: редактирование запрещено"))
			return
		}
		editConnection(tw.QWidget, env, uuid, func(updated *config.Environment) {
			(*cfg).Environments[uuid] = *updated
			save()
		})
	})

	// ---- Реже используемые — меню «Ещё…» ------------------------------
	more := qt.NewQPushButton3(i18n.Tr("Toolbar", "Ещё…"))
	more.SetFixedHeight(32)
	more.SetToolTip(i18n.Tr("Toolbar", "Дополнительные действия"))
	more.OnClicked(func() {
		m := wgt.NewMenu()
		defer m.Free() // иначе каждый клик утекает native QMenu (ревью)
		renameItem := m.Item(i18n.Tr("Toolbar", "Переименовать"))
		exportItem := m.Item(i18n.Tr("Toolbar", "Экспорт (полный бэкап)"))
		importItem := m.Item(i18n.Tr("Toolbar", "Импорт"))
		pccItem := m.Item(i18n.Tr("Toolbar", "Power Cluster Controller"))
		prefsItem := m.Item(i18n.Tr("Toolbar", "Настройки"))
		delItem := m.Item(i18n.Tr("Toolbar", "Удалить"))
		g := more.QWidget.MapToGlobalWithQPoint(qt.NewQPoint2(0, more.QWidget.Height()))
		chosen := m.Check(g.X(), g.Y())
		switch chosen {
		case renameItem:
			uuid, env := selectedEnv()
			if env == nil {
				return
			}
			if isProtected(env) {
				showErr(tw.QWidget, i18n.Tr("Toolbar", "Узел защищён: переименование запрещено"))
				return
			}
			if newName := strings.TrimSpace(inputText(tw.QWidget, env.Name, i18n.Tr("Toolbar", "Новое имя"), env.Name)); newName != "" {
				env.Name = newName
				(*cfg).Environments[uuid] = *env
				save()
			}
		case exportItem:
			path := inputText(tw.QWidget, i18n.Tr("Toolbar", "Экспорт"), i18n.Tr("Toolbar", "Файл экспорта"), cfgPath+".bak")
			if path == "" {
				return
			}
			cfg := *(*cfg)
			config.WithExportMarker(&cfg)
			if err := cfg.Save(path, config.NewCBC()); err != nil {
				showErr(tw.QWidget, i18n.Tr("Toolbar", "Экспорт: ")+err.Error())
				return
			}
			slog.Info("конфиг экспортирован", "path", path)
		case importItem:
			path := inputText(tw.QWidget, i18n.Tr("Toolbar", "Импорт"), i18n.Tr("Toolbar", "Файл импорта"), "")
			if path == "" {
				return
			}
			imp, err := config.LoadConfig(path, config.NewCBC())
			if err != nil {
				showErr(tw.QWidget, i18n.Tr("Toolbar", "Импорт: ")+err.Error())
				return
			}
			if len(imp.Exported) > 0 {
				showErr(tw.QWidget, i18n.Tr("Toolbar", "Импорт: легаси-экспорт выделенных узлов (__PAC__EXPORTED__) не поддерживается.\nЭкспортируйте полный бэкап или импортируйте файл как обычный asbru.yml."))
				return
			}
			if imp.ExportedFull == 1 {
				if !showConfirm(tw.QWidget, i18n.Tr("Toolbar", "Файл — ПОЛНЫЙ бэкап.\nЗаменить им текущую конфигурацию?")) {
					return
				}
				*(*cfg) = *imp
				(*cfg).Exported, (*cfg).ExportedFull = nil, 0
				(*cfg).ExportedPartial = nil
			} else {
				if n := config.MergeConfig(*cfg, imp); n == 0 {
					showErr(tw.QWidget, i18n.Tr("Toolbar", "Импорт: новых соединений нет (uuid уже существуют)"))
					return
				}
			}
			if err := saveCfg(*cfg, cfgPath); err != nil {
				showErr(tw.QWidget, i18n.Tr("Toolbar", "Импорт: сохранение: ")+err.Error())
				return
			}
			reload()
		case pccItem:
			showPCC(tw.QWidget)
		case prefsItem:
			showPreferences(tw.QWidget, (*cfg), cfgPath)
		case delItem:
			uuid, env := selectedEnv()
			if env == nil {
				return
			}
			if err := removeEnv(*cfg, uuid); err != nil {
				slog.Error("удаление", "err", err)
				return
			}
			save()
		}
	})
	box.AddWidget(more.QWidget)

	box.SetSpacing(4)
	holder := qt.NewQWidget2()
	holder.SetLayout(box.QLayout)
	return holder
}

// requireOnOk — структурный гвард контракта dialogButtons: действие OK
// обязательно, onOk навешивает сам хелпер. nil означает «забыл OnClicked» —
// паникуем при построении ряда, а не отдаём пользователю мёртвую кнопку
// (класс Critical из ревью 85ecdb1: четыре диалога с мёртвым OK).
func requireOnOk(onOk func()) func() {
	if onOk == nil {
		panic("dialogButtons: onOk обязателен — OK-кнопка без действия (забыт OnClicked)")
	}
	return onOk
}

// dialogButtons — стандартный ряд кнопок диалога: [extra…] [stretch] [Отмена]
// [ok]. ok помечается default (SetDefault): Enter в любом поле жмёт ok.
// QDialog на Enter нажимает ПЕРВЫЙ autoDefault — «Отмену», если она добавлена
// в layout раньше (живой баг 2026-10-14: редактор соединения и ещё девять
// диалогов молча теряли ввод по Enter). Cancel вешается на d.Reject; onOk —
// обязательный параметр (requireOnOk), поэтому «забытая» привязка OnClicked
// структурно невозможна. Возвращает OK-кнопку (для доп. настроек). По
// конвенции вызывать ДО d.SetLayout.
func dialogButtons(box *qt.QVBoxLayout, d *qt.QDialog, okText string, onOk func(), extra ...*qt.QPushButton) *qt.QPushButton {
	row := qt.NewQHBoxLayout2()
	for _, b := range extra {
		row.AddWidget(b.QWidget)
	}
	row.AddStretch()
	cancel := qt.NewQPushButton3(i18n.Tr("Dialog", "Отмена"))
	cancel.OnClicked(d.Reject)
	row.AddWidget(cancel.QWidget)
	ok := qt.NewQPushButton3(okText)
	ok.SetDefault(true)
	ok.OnClicked(requireOnOk(onOk))
	row.AddWidget(ok.QWidget)
	box.AddLayout(row.QLayout)
	return ok
}

// inputText — простейший диалог с одной строкой (аналог QInputDialog,
// в miqt статики GetText нет).
func inputText(parent *qt.QWidget, title, label, initial string) string {
	return inputTextEcho(parent, title, label, initial, false)
}

// inputTextOK — как inputText, но возвращает (значение, ok): ok=false при
// отмене (Reject), true при Accept (даже если поле пустое — «очистить»).
func inputTextOK(parent *qt.QWidget, title, label, initial string) (string, bool) {
	d := qt.NewQDialog2()
	d.SetWindowTitle(title)
	d.SetMinimumWidth(400)
	edit := qt.NewQLineEdit3(initial)
	edit.SelectAll()
	form := qt.NewQFormLayout2()
	form.AddRow3(label, edit.QWidget)
	var result string
	okBtn := qt.NewQPushButton3(i18n.Tr("Dialog", "Сохранить"))
	okBtn.SetDefault(true) // Enter в поле = «Сохранить»; без этого QDialog жмёт первый autoDefault — «Отмена» (живой баг 2026-10-14: имя группы «не сохранялось»)
	okBtn.OnClicked(func() {
		result = edit.Text()
		d.Accept()
	})
	cancelBtn := qt.NewQPushButton3(i18n.Tr("Dialog", "Отмена"))
	cancelBtn.OnClicked(d.Reject)
	row := qt.NewQHBoxLayout2()
	row.AddStretch()
	row.AddWidget(cancelBtn.QWidget)
	row.AddWidget(okBtn.QWidget)
	box := qt.NewQVBoxLayout2()
	box.AddLayout(form.QLayout)
	box.AddLayout(row.QLayout)
	d.SetLayout(box.QLayout)
	d.Exec()
	if d.Result() != int(qt.QDialog__Accepted) {
		return "", false
	}
	return result, true
}

// inputTextEcho — как inputText, но с эхоподавлением (для паролей M19).
func inputTextEcho(parent *qt.QWidget, title, label, initial string, passMode bool) string {
	d := qt.NewQDialog2()
	d.SetWindowTitle(title)
	d.SetMinimumWidth(400)
	edit := qt.NewQLineEdit3(initial)
	if passMode {
		edit.SetEchoMode(qt.QLineEdit__Password)
	}
	edit.SelectAll() // переименование: имя предзаполнено (дизайн-аудит P1)
	form := qt.NewQFormLayout2()
	form.AddRow3(label, edit.QWidget)
	var result string
	okBtn := qt.NewQPushButton3(i18n.Tr("Dialog", "Сохранить"))
	okBtn.SetDefault(true) // Enter = «Сохранить» (см. inputTextOK: первый autoDefault — «Отмена»)
	okBtn.OnClicked(func() {
		result = edit.Text()
		d.Accept()
	})
	cancelBtn := qt.NewQPushButton3(i18n.Tr("Dialog", "Отмена"))
	cancelBtn.OnClicked(d.Reject)
	row := qt.NewQHBoxLayout2()
	row.AddStretch()
	row.AddWidget(cancelBtn.QWidget)
	row.AddWidget(okBtn.QWidget)
	box := qt.NewQVBoxLayout2()
	box.AddLayout(form.QLayout)
	box.AddLayout(row.QLayout)
	d.SetLayout(box.QLayout)
	d.Exec()
	return result
}
