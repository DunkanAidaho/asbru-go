package ui

import (
	"fmt"
	"log/slog"
	"strings"
	"unsafe"

	"github.com/DunkanAidaho/asbru-go/internal/config"
	"github.com/DunkanAidaho/asbru-go/internal/i18n"
	"github.com/DunkanAidaho/asbru-go/internal/session"
	"github.com/DunkanAidaho/asbru-go/internal/tree"
	"github.com/DunkanAidaho/asbru-go/internal/ui/wgt"
	qt "github.com/mappu/miqt/qt6"
)

// showTreeMenu — контекстное меню узла дерева (M14; P0-3 аудита):
// соединение — Подключиться/Редактировать/Переименовать/Клонировать/
// Копировать/Вырезать/Удалить; группа — добавить группу/соединение,
// переименовать/клонировать/удалить и вставку.
func showTreeMenu(tw *qt.QTreeWidget, cfg **config.Config, cfgPath string, reload func(),
	uuid string, gx, gy int, selectedByItem map[unsafe.Pointer]string,
	launch func(string, *config.Environment, string)) {
	menu := wgt.NewMenu()
	defer menu.Free()

	cfgx := *cfg
	var e *config.Environment
	if uuid != "" {
		if v, ok := cfgx.Environments[uuid]; ok {
			e = &v
		}
	}
	if e == nil && uuid != config.RootUUID {
		uuid = config.RootUUID
	}

	save := func() {
		if err := saveCfgUI(cfgx, cfgPath, tw.QWidget); err != nil {
			// диалог уже показан (ErrReadOnly) или ошибка в логе
		}
		reload()
	}
	addGroup := func() {
		// M36-ревью MAJOR: нельзя создавать подзащищённым узлом (легаси 2906).
		if p := parentOf(uuid, e); nodeProtected(cfgx, p) {
			showErr(tw.QWidget, i18n.Tr("Tree", "Узел защищён: создание группы под ним запрещено"))
			return
		}
		// TrimSpace: ввод из одних пробелов не должен создавать невидимую
		// группу (легаси отменял по ^\s*$).
		if name := strings.TrimSpace(inputText(tw.QWidget, i18n.Tr("Tree", "Новая группа"), i18n.Tr("Tree", "Имя группы"), "")); name != "" {
			if _, err := addEnv(cfgx, parentOf(uuid, e), &config.Environment{Name: name, IsGroup: 1}); err != nil {
				// Ревью 2026-10-14: тихое проглатывание — класс «пропало без следа».
				slog.Error("создание группы", "err", err)
				showErr(tw.QWidget, i18n.Tr("Tree", "Не удалось создать группу")+": "+err.Error())
				return
			}
			save()
		}
	}
	addConn := func() {
		// M36-ревью MAJOR: нельзя создавать подзащищённым узлом (легаси 2906).
		if p := parentOf(uuid, e); nodeProtected(cfgx, p) {
			showErr(tw.QWidget, i18n.Tr("Tree", "Узел защищён: создание соединения под ним запрещено"))
			return
		}
		if name := strings.TrimSpace(inputText(tw.QWidget, i18n.Tr("Tree", "Новое соединение"), i18n.Tr("Tree", "Имя соединения"), "")); name != "" {
			// M34: создание тоже протокол-зависимо — сразу открываем редактор
			// (auth-тип/опции зависят от метода), а не сохраняем пустой SSH-заглушкой.
			newUUID, err := addEnv(cfgx, parentOf(uuid, e), &config.Environment{Name: name, Method: "SSH"})
			if err != nil {
				// Живой инцидент 2026-10-14 («соединение пропало»): тихий slog
				// пользователь не видит — соединение не создано, редактор не
				// открылся, диалог исчез. Показываем причину явно.
				slog.Error("создание соединения", "err", err)
				showErr(tw.QWidget, i18n.Tr("Tree", "Не удалось создать соединение")+": "+err.Error())
				return
			}
			if env, ok := cfgx.Environments[newUUID]; ok {
				saved := editConnection(tw.QWidget, &env, newUUID, func(updated *config.Environment) {
					cfgx.Environments[newUUID] = *updated
					save()
				})
				if !saved {
					// M34-ревью: отмена редактора — убрать заглушку (иначе
					// сохранится фантомная SSH-связь при следующем save()).
					if err := removeEnv(cfgx, newUUID); err != nil {
						slog.Error("откат нового соединения", "err", err)
					}
					save()
				}
			}
		}
	}
	rename := func() {
		if isProtected(e) {
			showErr(tw.QWidget, i18n.Tr("Tree", "Узел защищён: переименование запрещено"))
			return
		}
		if newName := strings.TrimSpace(inputText(tw.QWidget, e.Name, i18n.Tr("Tree", "Новое имя"), e.Name)); newName != "" {
			e.Name = newName
			cfgx.Environments[uuid] = *e
			save()
		} else if e.Name != "" {
			// отмена — без изменений
		}
	}
	clone := func() {
		if _, err := cloneEnv(cfgx, uuid); err != nil {
			slog.Error("клонирование", "err", err)
			return
		}
		save()
	}
	remove := func() {
		if isProtected(e) {
			showErr(tw.QWidget, i18n.Tr("Tree", "Узел защищён: удаление запрещено"))
			return
		}
		if err := removeEnv(cfgx, uuid); err != nil {
			slog.Error("удаление", "err", err)
			return
		}
		save()
	}

	type entry struct {
		ptr unsafe.Pointer
		on  func()
	}
	var entries []entry
	add := func(label string, on func()) unsafe.Pointer {
		p := menu.Item(label)
		entries = append(entries, entry{p, on})
		return p
	}

	switch {
	case e == nil:
		add(i18n.Tr("Tree", "Добавить группу"), addGroup)
		add(i18n.Tr("Tree", "Добавить соединение"), addConn)
	case e.IsGroup == 1 && uuid == config.RootUUID:
		add(i18n.Tr("Tree", "Добавить группу"), addGroup)
		add(i18n.Tr("Tree", "Добавить соединение"), addConn)
	case e.IsGroup == 1:
		add(i18n.Tr("Tree", "Добавить группу"), addGroup)
		add(i18n.Tr("Tree", "Добавить соединение"), addConn)
		// M36 tree_menu: «Start Group» — запустить все соединения группы
		// (легаси PACMain.pm:2917-2934; _launchTerminals пропускает группы).
		// Сбор потомков — общий config.GroupConnections (бэклог: двойной клик).
		add(i18n.Tr("Tree", "Запустить группу"), func() {
			for _, u := range cfgx.GroupConnections(uuid) {
				if env, ok := cfgx.Environments[u]; ok {
					launch(u, &env, firstCluster(env.Cluster))
				}
			}
		})
		addProtectToggle(add, cfgx, uuid, e, save)
		add(i18n.Tr("Tree", "Переименовать"), rename)
		add(i18n.Tr("Tree", "Клонировать"), clone)
		add(i18n.Tr("Tree", "Удалить"), remove)
	default:
		add(i18n.Tr("Tree", "Подключиться"), func() { launch(uuid, e, firstCluster(e.Cluster)) })
		// M36 tree_menu: «Edit Local Variables» (легаси PACMain.pm:2880-2903) —
		// подменю «<V:N> = значение» соединения: клик → новый текст, save.
		// M36-ревью MAJOR: у защищённого соединения правка переменных запрещена.
		if len(e.Variables) > 0 && !isProtected(e) {
			vsub := menu.Submenu(i18n.Tr("Tree", "Изменить переменные…"))
			for j := range e.Variables {
				idx := j
				label := fmt.Sprintf(i18n.Tr("Tree", "<V:%d> = %s"), idx, varText(e.Variables[idx]))
				p := vsub.Item(label)
				entries = append(entries, entry{p, func() {
					// M36-ревью MINOR: в диалог предзаполняем РЕАЛЬНЫЙ текст
					// (не маску '<hidden>') — иначе правка скрытой переменной без
					// ввода затирала бы секрет литералом '<hidden>' (легаси-порт
					// PACMain:2888 унаследовал этот foot-gun). Диалог на экране,
					// значение не утекает в лог; маска остаётся в пункте меню.
					old := rawVarText(e.Variables[idx])
					nv, ok := inputTextOK(tw.QWidget, fmt.Sprintf(i18n.Tr("Tree", "Переменная <V:%d>"), idx), i18n.Tr("Tree", "Новое значение"), old)
					if !ok {
						return // отмена — без изменений (легаси _wEnterValue → undef)
					}
					// M36-ревью MINOR: не мутируем общую с живым конфигом
					// Variables-карту in-place (алиасинг) — собираем новую
					// копию среза и записываем целиком (как mergeVariables).
					vars := e.Variables
					copyVars := make([]map[string]any, len(vars))
					for k, v := range vars {
						m := map[string]any{}
						for kk, vv := range v {
							m[kk] = vv
						}
						copyVars[k] = m
					}
					if copyVars[idx] == nil {
						copyVars[idx] = map[string]any{}
					}
					copyVars[idx]["txt"] = nv
					envCopy := *e
					envCopy.Variables = copyVars
					cfgx.Environments[uuid] = envCopy
					save()
				}})
			}
		}
		// M36 tree_menu: «Start N instances» (легаси PACMain.pm:2659-2672) —
		// подменю «N экземпляров» (2..9): запустить соединение N раз.
		sub := menu.Submenu(i18n.Tr("Tree", "Запустить несколько…"))
		for i := 2; i <= 9; i++ {
			cnt := i
			p := sub.Item(fmt.Sprintf(i18n.Tr("Tree", "%d экземпляров"), cnt))
			entries = append(entries, entry{p, func() {
				for j := 0; j < cnt; j++ {
					launch(uuid, e, firstCluster(e.Cluster))
				}
			}})
		}
		add(i18n.Tr("Tree", "Редактировать"), func() {
			if isProtected(e) {
				showErr(tw.QWidget, i18n.Tr("Tree", "Узел защищён: редактирование запрещено"))
				return
			}
			editConnection(tw.QWidget, e, uuid, func(updated *config.Environment) {
				cfgx.Environments[uuid] = *updated
				save()
			})
		})
		addProtectToggle(add, cfgx, uuid, e, save)
		add(i18n.Tr("Tree", "Переименовать"), rename)
		add(i18n.Tr("Tree", "Клонировать"), clone)
		// Очный дебаг #3: панель «Избранные» была, а добавить в неё нельзя.
		// Тумблер favourite=1/0 в контекстном меню соединения.
		favLabel := i18n.Tr("Tree", "Добавить в избранные")
		if e.Favourite == 1 {
			favLabel = i18n.Tr("Tree", "Убрать из избранного")
		}
		add(favLabel, func() {
			if isProtected(e) {
				showErr(tw.QWidget, i18n.Tr("Tree", "Узел защищён: изменение избранного запрещено"))
				return
			}
			envx := cfgx.Environments[uuid]
			if envx.Favourite == 1 {
				envx.Favourite = 0
			} else {
				envx.Favourite = 1
			}
			cfgx.Environments[uuid] = envx
			*e = envx // синхронизировать локальную копию
			save()
		})
		// M36 tree_menu: «Copy Password» (легаси PACMain.pm:2828 — только при
		// непустом pass/passphrase; иначе пункт скрыт, а не no-op).
		if e.Pass != "" || e.Passphrase != "" {
			add(i18n.Tr("Tree", "Копировать пароль"), func() { copyPasswordForUUID(uuid) })
		}
		add(i18n.Tr("Tree", "Копировать"), func() {
			if isProtected(e) {
				showErr(tw.QWidget, i18n.Tr("Tree", "Узел защищён: копирование запрещено"))
				return
			}
			if err := copyEnv(cfgx, uuid); err != nil {
				slog.Error("копирование", "err", err)
			}
		})
		add(i18n.Tr("Tree", "Вырезать"), func() {
			// M36-ревью MINOR: вырезка группы с защищённым потомком запрещена
			// (иначе paste→removeEnv дал бы частичное удаление, легаси 4140).
			if isProtected(e) || selHasProtected(cfgx, []string{uuid}, true) {
				showErr(tw.QWidget, i18n.Tr("Tree", "Узел защищён: вырезка запрещена"))
				return
			}
			if err := cutEnv(cfgx, uuid); err != nil {
				slog.Error("вырезка", "err", err)
			}
		})
		add(i18n.Tr("Tree", "Удалить"), remove)
		// №8: Wake On LAN (легаси PACMain.pm «Wake On LAN...») — magic packet.
		add(i18n.Tr("Tree", "Wake On LAN…"), func() {
			if e.Mac == "" {
				slog.Warn("Wake On LAN: MAC не задан", "conn", e.Name)
				return
			}
			if err := session.SendWOL(e.IP, e.Mac); err != nil {
				slog.Error("Wake On LAN", "err", err)
			}
		})
	}
	// M36 tree_menu: Bulk Edit — массовое изменение полей выбранных
	// соединений (легаси PACMain.pm «Bulk Edit connections...», 2606/2839).
	// Показываем, когда выбрано >1 узла ИЛИ выбранная группа (правка её детей).
	// Ревью MAJOR: счётчик — только реально выбранные узлы (tw.SelectedItems()),
	// а не полный byItem (иначе пункт предлагался при любое числе узлов).
	var sel []string
	for _, it := range tw.SelectedItems() {
		if u, ok := selectedByItem[itemID(it)]; ok {
			sel = append(sel, u)
		}
	}
	if len(sel) > 1 || (e != nil && e.IsGroup == 1) {
		add(i18n.Tr("Tree", "Групповое редактирование…"), func() {
			if len(sel) == 0 {
				return
			}
			edits, ok := bulkEditDialog(tw.QWidget, sel)
			if !ok {
				return
			}
			// M36-ревью MAJOR: групповое редактирование НЕ трогает защищённые
			// узлы (легаси PACMain.pm:2106-2107) — отказ на весь батч.
			if selHasProtected(cfgx, sel, true) {
				showErr(tw.QWidget, i18n.Tr("Tree", "Групповое редактирование: в выборке есть защищённые узлы"))
				return
			}
			changed := tree.ApplyBulkEdits(cfgx.Environments, sel, edits, true)
			if changed == 0 {
				showErr(tw.QWidget, i18n.Tr("Tree", "Групповое редактирование: изменения не применились (нет совпадений/пусто)"))
				return
			}
			save()
		})
	}
	// M20: «Execute in New Cluster...» (легаси PACMain.pm:2692-2707 +
	// раскрытие групп из PACCluster.pm:703-714; _launchTerminals сам
	// пропускает группы, PACMain.pm:3229-3231): выбранные соединения
	// запускаются кластером с указанным именем — PCC найдёт их по имени.
	add(i18n.Tr("Tree", "Execute in New Cluster..."), func() {
		seen := map[string]bool{}
		var ids []string
		// группы раскрываются в детей (легаси PACMain.pm:2696-2705)
		var collect func(string)
		collect = func(u string) {
			if seen[u] {
				return
			}
			seen[u] = true
			if e, ok := cfgx.Environments[u]; ok && e.IsGroup == 1 {
				for cu := range e.Children {
					collect(cu)
				}
				return
			}
			ids = append(ids, u)
		}
		for _, it := range tw.SelectedItems() {
			if u, ok := selectedByItem[itemID(it)]; ok {
				collect(u)
			}
		}
		if len(ids) == 0 {
			return
		}
		name := strings.TrimSpace(inputText(tw.QWidget, i18n.Tr("Tree", "Новый кластер"), i18n.Tr("Tree", "Имя кластера"), ""))
		if name == "" {
			return // легаси: ^\s*$ → отмена (PACMain.pm:2695-2697)
		}
		for _, u := range ids {
			if e, ok := cfgx.Environments[u]; ok {
				launch(u, &e, name)
			}
		}
	})
	if clipboard.env != nil {
		add(i18n.Tr("Tree", "Вставить"), func() {
			// M36-ревью MAJOR: нельзя вставлять подзащищённый узел (легаси 3008).
			if p := parentOf(uuid, e); nodeProtected(cfgx, p) {
				showErr(tw.QWidget, i18n.Tr("Tree", "Узел защищён: вставка под ним запрещена"))
				return
			}
			if _, err := pasteEnv(cfgx, parentOf(uuid, e)); err != nil {
				slog.Error("вставка", "err", err)
				return
			}
			save()
		})
	}

	// M36 tree_menu: импорт/экспорт (легаси PACConfig.pm _exporter/_importer +
	// PACMain.pm:2850-2877 «Export SELECTED/ALL»/«Import connection(s)...»).
	add(i18n.Tr("Tree", "Экспорт соединений…"), func() { exportConnections(tw.QWidget, cfgx, cfgPath, uuid == config.RootUUID, sel, uuid) })
	add(i18n.Tr("Tree", "Импорт соединений…"), func() {
		if configExportImport(tw.QWidget, cfgx) {
			save()
		}
	})

	// M36 tree_menu: «Expand all»/«Collapse all» (легаси PACMain.pm:2786-2803).
	// Раскрыть/свернуть ВСЕ узлы (включая поддеревья).
	add(i18n.Tr("Tree", "Развернуть все"), func() { tw.ExpandAll() })
	add(i18n.Tr("Tree", "Свернуть все"), func() { tw.CollapseAll() })

	// Backlog M32: точки входа для построенных поверхностей (меню дерева).
	add(i18n.Tr("Tree", "Кластеры…"), func() { showClusterDialog(cfgx, cfgPath) })
	add(i18n.Tr("Tree", "Статистика…"), func() { showStats(appStats, tw.QWidget) })
	// M29-остаток: редакторы глобальных настроек-списков.
	add(i18n.Tr("Tree", "Глобальные переменные…"), func() { showGlobalVarsEditor(cfgx, cfgPath, tw.QWidget) })
	add(i18n.Tr("Tree", "Локальные команды…"), func() {
		showCommandsEditor(cfgx, cfgPath, tw.QWidget, "local commands", i18n.Tr("Tree", "Локальные команды"))
	})
	add(i18n.Tr("Tree", "Удалённые команды…"), func() {
		showCommandsEditor(cfgx, cfgPath, tw.QWidget, "remote commands", i18n.Tr("Tree", "Удалённые команды"))
	})
	add(i18n.Tr("Tree", "Привязки клавиш…"), func() {
		showKeybindingsEditor(cfgx, cfgPath, tw.QWidget)
	})

	chosen := menu.Check(gx, gy)
	for _, it := range entries {
		if it.ptr == chosen {
			it.on()
			return
		}
	}
}

// parentOfCopy — родитель для новых узлов из меню.
func parentOf(uuid string, e *config.Environment) string {
	if e == nil {
		return config.RootUUID
	}
	if e.IsGroup == 1 {
		return uuid
	}
	if e.Parent != "" {
		return e.Parent
	}
	return config.RootUUID
}

// bulkEditDialog — окно группового редактирования полей. Пользователь вводит
// новое значение для одного или нескольких полей (title/ip/port/user/options/
// public key/passphrase user) — литерал или regex-подстановку. Возвращает
// список FieldEdit и ok=false при отмене.
func bulkEditDialog(parent *qt.QWidget, sel []string) ([]tree.FieldEdit, bool) {
	_ = sel // модель собрана в ApplyBulkEdits; диалог — только ввод полей
	d := qt.NewQDialog2()
	d.SetWindowTitle(i18n.Tr("Tree", "Групповое редактирование"))
	d.SetMinimumWidth(460)

	fields := []struct {
		label string
		field string
	}{
		{i18n.Tr("Tree", "Название"), "name"},
		{i18n.Tr("Tree", "Заголовок"), "title"},
		{i18n.Tr("Tree", "IP"), "ip"},
		{i18n.Tr("Tree", "Порт"), "port"},
		{i18n.Tr("Tree", "Пользователь"), "user"},
		{i18n.Tr("Tree", "Опции"), "options"},
		{i18n.Tr("Tree", "Ключ (public key)"), "public key"},
		{i18n.Tr("Tree", "Пользователь ключа"), "passphrase user"},
	}

	type row struct {
		check *qt.QCheckBox
		edit  *qt.QLineEdit // литеральное значение (или replace при regex)
		match *qt.QLineEdit // regex-паттерн (виден при включённом regex)
		regex *qt.QCheckBox
	}
	var rows []row

	form := qt.NewQFormLayout2()
	for _, f := range fields {
		check := qt.NewQCheckBox3(f.label)
		edit := qt.NewQLineEdit3("")
		edit.SetEnabled(false)
		match := qt.NewQLineEdit3("")
		match.SetEnabled(false)
		match.SetPlaceholderText(i18n.Tr("Tree", "regex-паттерн"))
		regex := qt.NewQCheckBox3(i18n.Tr("Tree", "regexp"))
		regex.SetEnabled(false)
		visibility := func() {
			on := check.IsChecked()
			edit.SetEnabled(on)
			match.SetEnabled(on)
			regex.SetEnabled(on)
			// паттерн виден только когда строка активна И включён regex.
			match.SetVisible(on && regex.IsChecked())
		}
		check.OnToggled(func(bool) { visibility() })
		regex.OnToggled(func(bool) { visibility() })
		// изначально паттерн скрыт (литеральный/неактивный режим).
		match.SetVisible(false)
		rows = append(rows, row{check: check, edit: edit, match: match, regex: regex})

		// строка = контейнер: match + edit + regex (+ флаг «задать» в чекбоксе).
		editRow := qt.NewQHBoxLayout2()
		editRow.AddWidget(match.QWidget)
		editRow.AddWidget(edit.QWidget)
		editRow.AddWidget(regex.QWidget)
		h := qt.NewQWidget2()
		h.SetLayout(editRow.QLayout)
		form.AddRow3("", h)
	}

	box := qt.NewQVBoxLayout2()
	box.AddLayout(form.QLayout)
	dialogButtons(box, d, i18n.Tr("Dialog", "Применить"), func() { d.Accept() })
	d.SetLayout(box.QLayout)
	d.Exec()

	if d.Result() != int(qt.QDialog__Accepted) {
		return nil, false
	}

	var edits []tree.FieldEdit
	for i, f := range fields {
		if !rows[i].check.IsChecked() {
			continue
		}
		val := rows[i].edit.Text()
		if rows[i].regex.IsChecked() {
			// regex-подстановка: match (свой entry) → replace (общий entry),
			// как легаси-пара «change/with» (без инъекции '/' в match/replace).
			m := rows[i].match.Text()
			if m == "" {
				continue
			}
			edits = append(edits, tree.FieldEdit{Field: f.field, IsRegex: true, Match: m, Replace: val})
		} else {
			edits = append(edits, tree.FieldEdit{Field: f.field, Value: val})
		}
	}
	if len(edits) == 0 {
		return nil, false
	}
	return edits, true
}

// exportConnections — экспорт соединений. all=true (root) — полный бэкап
// (маркер __PAC__EXPORTED__FULL__); иначе — только выбранное подмножество
// (SubsetConfig: группа+потомки или выделенное соединение).
func exportConnections(parent *qt.QWidget, cfgx *config.Config, cfgPath string, all bool, sel []string, curUUID string) {
	path := inputText(parent, i18n.Tr("Tree", "Экспорт соединений"), i18n.Tr("Tree", "Файл экспорта"), cfgPath+".export.yml")
	if path == "" {
		return
	}
	if all {
		// полный бэкап: слой-копия, маркер не трогает живой конфиг.
		backup := *cfgx
		config.WithExportMarker(&backup)
		if err := backup.Save(path, config.NewCBC()); err != nil {
			showErr(parent, i18n.Tr("Tree", "Экспорт: ")+err.Error())
			return
		}
		slog.Info("конфиг экспортирован (полный бэкап)", "path", path)
		return
	}
	// выбранные uuid: sel уже собран из tw.SelectedItems(); при пустом (правый
	// клик по листу без мультивыбора) — включаем сам правокликнутый узел.
	targets := append([]string(nil), sel...)
	has := false
	for _, u := range targets {
		if u == curUUID {
			has = true
			break
		}
	}
	if !has && curUUID != "" && curUUID != config.RootUUID {
		targets = append(targets, curUUID)
	}
	if len(targets) == 0 {
		showErr(parent, i18n.Tr("Tree", "Экспорт: нет выбранных соединений"))
		return
	}
	part, n := config.SubsetConfig(cfgx, targets)
	if n == 0 {
		showErr(parent, i18n.Tr("Tree", "Экспорт: нечего экспортировать"))
		return
	}
	if err := part.Save(path, config.NewCBC()); err != nil {
		showErr(parent, i18n.Tr("Tree", "Экспорт: ")+err.Error())
		return
	}
	slog.Info("соединения экспортированы", "path", path, "count", n)
}

// configExportImport — импорт соединений из файла: полный бэкап → замещение
// (с подтверждением), иначе merge. МУТИРУЕТ cfgx в памяти; сохранение и
// перезагрузку делает вызывающий (save()). Возвращает true, если изменилось.
func configExportImport(parent *qt.QWidget, cfgx *config.Config) bool {
	path := inputText(parent, i18n.Tr("Tree", "Импорт соединений"), i18n.Tr("Tree", "Файл импорта"), "")
	if path == "" {
		return false
	}
	imp, err := config.LoadConfig(path, config.NewCBC())
	if err != nil {
		showErr(parent, i18n.Tr("Tree", "Импорт: ")+err.Error())
		return false
	}
	if len(imp.Exported) > 0 {
		// легаси-экспорт выделенных узлов (__PAC__EXPORTED__) не читаем честно.
		showErr(parent, i18n.Tr("Tree", "Импорт: легаси-экспорт выделенных узлов (__PAC__EXPORTED__) не поддерживается.\nЭкспортируйте полный бэкап или импортируйте файл как обычный asbru.yml."))
		return false
	}
	if imp.ExportedFull == 1 {
		if !showConfirm(parent, i18n.Tr("Tree", "Файл — ПОЛНЫЙ бэкап.\nЗаменить им текущую конфигурацию?")) {
			return false
		}
		*cfgx = stripExportMarkers(*imp)
	} else {
		if n := config.MergeConfig(cfgx, imp); n == 0 {
			showErr(parent, i18n.Tr("Tree", "Импорт: новых соединений нет (uuid уже существуют)"))
			return false
		}
	}
	return true
}

// stripExportMarkers — снимает маркеры бэкапа с конфига после полного импорта.
func stripExportMarkers(c config.Config) config.Config {
	c.Exported, c.ExportedFull, c.ExportedPartial = nil, 0, nil
	return c
}

// varText — отображаемый текст локальной переменной (легаси: hide → '<hidden>').
func varText(m map[string]any) string {
	if m == nil {
		return ""
	}
	if varHidden(m) {
		return "<hidden>"
	}
	return rawVarText(m)
}

// rawVarText — реальный текст переменной (без маски), для предзаполнения
// диалога правки скрытой переменной (иначе '<hidden>' затирал бы секрет).
func rawVarText(m map[string]any) string {
	if m == nil {
		return ""
	}
	if t, ok := m["txt"].(string); ok {
		return t
	}
	return ""
}

// varHidden — истинность 'hide' как в Perl (любое истинное значение).
var varHidden = func(m map[string]any) bool {
	switch v := m["hide"].(type) {
	case bool:
		return v
	case int:
		return v != 0
	case int64: // yaml-иногда даёт int64
		return v != 0
	case float64: // yaml-float (редко) — не маскировать нельзя (иначе утечка)
		return v != 0
	case string:
		return v != "" && v != "0"
	default:
		return false
	}
}

// isProtected — признак защищённого узла (легаси _protected: 1/true).
func isProtected(e *config.Environment) bool {
	if e == nil {
		return false
	}
	return e.Protected == 1
}

// nodeProtected — защищён ли узел uuid (прямо, не поддерево).
func nodeProtected(cfg *config.Config, uuid string) bool {
	e, ok := cfg.Environments[uuid]
	return ok && e.Protected == 1
}

// selHasProtected — есть ли в выборке (или её раскрытых потомках) защищённый.
func selHasProtected(cfg *config.Config, sel []string, recurse bool) bool {
	seen := map[string]bool{}
	var check func(string) bool
	check = func(u string) bool {
		if seen[u] {
			return false
		}
		seen[u] = true
		e, ok := cfg.Environments[u]
		if !ok {
			return false
		}
		if e.Protected == 1 {
			return true
		}
		if recurse && e.IsGroup == 1 {
			for c := range e.Children {
				if check(c) {
					return true
				}
			}
		}
		return false
	}
	for _, u := range sel {
		if check(u) {
			return true
		}
	}
	return false
}

// addProtectToggle — пункт «Защитить»/«Снять защиту» (легаси Toggle Protect).
func addProtectToggle(add func(string, func()) unsafe.Pointer, cfg *config.Config, uuid string, e *config.Environment, save func()) {
	label := i18n.Tr("Tree", "Защитить")
	if isProtected(e) {
		label = i18n.Tr("Tree", "Снять защиту")
	}
	add(label, func() { toggleProtect(cfg, uuid, save) })
}

// toggleProtect — переключить защиту узла (легаси __treeToggleProtection):
// сохранить и перезагрузить дерево.
func toggleProtect(cfg *config.Config, uuid string, save func()) {
	e, ok := cfg.Environments[uuid]
	if !ok {
		return
	}
	if e.Protected == 1 {
		e.Protected = 0
	} else {
		e.Protected = 1
	}
	cfg.Environments[uuid] = e
	if save != nil {
		save()
	}
}
