package ui

import (
	"log/slog"
	"sort"
	"strings"

	"github.com/DunkanAidaho/asbru-go/internal/config"
	"github.com/DunkanAidaho/asbru-go/internal/i18n"
	qt "github.com/mappu/miqt/qt6"
)

// M21: горячие клавиши (легаси PACKeyBindings.pm). Конфиг:
// defaults.keybindings{appwin}{легаси-запись} = [window, action, desc].
// Qt-часть: QShortcut на главном окне (installHotkeys).

// hotkey — одна привязанная клавиша (легаси-запись + действие).
type hotkey struct {
	Seq    string
	Action string
}

// supportedActions — действия, реализованные в Go-порте (предпочтения
// показывают только их; reset/reset-clear/sftp/remove_from_cluster — задел).
var supportedActions = map[string]bool{
	"showconnections": true,
	"connect_node":    true,
	"edit_node":       true,
	// Batch A (дерево): действия с готовыми обработчиками (tree_menu/treeops).
	"rename":      true,
	"clone":       true,
	"protection":  true,
	"start":       true,
	"expand_all":  true,
	"collaps_all": true,
	// Batch B (терминал): действия по текущей вкладке/сессии.
	"close":        true,
	"closealltabs": true,
	"disconnect":   true,
	"duplicate":    true,
	"restart":      true,
	"hostname":     true,
	"sftp":         true,
	"find":         true,
	"paste":        true,
	"copy":         true,
	// Batch C (глобальные): quit/fullscreen/next/previous/last/infotab/localshell/
	// showkeybindings (легаси PACKeyBindings.pm:494-522).
	"quit":            true,
	"fullscreen":      true,
	"next":            true,
	"previous":        true,
	"last":            true,
	"infotab":         true,
	"localshell":      true,
	"showkeybindings": true,
}

// kbEntry — одна активная привязка клавиш (для показа showKeybindings и
// редактора KeyBindings). App — ключ appwin-секции (pacmain/terminal/tabs/…),
// Orig — исходная легаси-запись (для перемещения при редактировании).
type kbEntry struct {
	Window string
	Action string
	Key    string // легаси-запись (напр. "Ctrl+q")
	Desc   string
	App    string
	Orig   string
}

// keybindingsList — активные привязки (фильтр поддерживаемых действий) в
// детерминированном порядке (Window, Action, Key), как легаси ListKeybindings.
func keybindingsList(cfg *config.Config) []kbEntry {
	var out []kbEntry
	for app, perApp := range cfg.Defaults.KeyBindings {
		for seq, spec := range perApp {
			if strings.HasPrefix(seq, "undef-") {
				continue // легаси-заглушка (undef-infotab): не показываем
			}
			if len(spec) < 3 {
				continue
			}
			if !supportedActions[spec[1]] {
				continue
			}
			out = append(out, kbEntry{Window: spec[0], Action: spec[1], Key: seq, Desc: spec[2], App: app, Orig: seq})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Window != out[j].Window {
			return out[i].Window < out[j].Window
		}
		if out[i].Action != out[j].Action {
			return out[i].Action < out[j].Action
		}
		return out[i].Key < out[j].Key
	})
	return out
}

// applyKeyBindings — применить отредактированные Key (seq) к cfg.Defaults.
// KeyBindings. Стате-машина (pending/granted/stayed) — резолвер переездов без
// потерь: mover на новый Key едет, только если тот свободен ИЛИ освобождается
// переезжающим mover'ом; владелец, оставшийся на месте, каскадно блокирует
// переместившихся в его ключ; чистый цикл (своп) — переезжают оба. Ни один
// биндинг не теряется. Мутирует map на месте.
func applyKeyBindings(cfg *config.Config, entries []kbEntry) {
	if cfg == nil || cfg.Defaults.KeyBindings == nil {
		return
	}
	kb := cfg.Defaults.KeyBindings
	type mover struct {
		old, new string
		orig     []string
		state    int // 0 pending, 1 granted, 2 stayed
	}
	byApp := map[string][]*mover{}
	for _, e := range entries {
		if e.Orig == "" || e.Key == e.Orig {
			continue
		}
		perApp := kb[e.App]
		if perApp == nil {
			continue
		}
		orig, ok := perApp[e.Orig]
		if !ok || len(orig) < 2 || orig[1] != e.Action {
			continue
		}
		byApp[e.App] = append(byApp[e.App], &mover{old: e.Orig, new: e.Key, orig: orig})
	}
	for app, movers := range byApp {
		perApp := kb[app]
		// byOld — домен (old-ключ → mover); byNew — цели (для конфликтов).
		byOld := map[string]*mover{}
		for _, m := range movers {
			byOld[m.old] = m
		}
		static := map[string]bool{}
		for k := range perApp {
			if byOld[k] == nil {
				static[k] = true
			}
		}
		grantedNew := map[string]bool{}
		for {
			progress := false
			for _, m := range movers {
				if m.state != 0 {
					continue
				}
				switch {
				case m.new == "":
					m.state = 1 // удаление
					progress = true
				case static[m.new]:
					m.state = 2 // цель — статик
					progress = true
				case grantedNew[m.new]:
					m.state = 2 // цель занята переместившимся
					progress = true
				default:
					if owner := byOld[m.new]; owner != nil {
						switch owner.state {
						case 1:
							m.state = 1 // владелец уехал — можно
							grantedNew[m.new] = true
							progress = true
						case 2:
							m.state = 2 // владелец остаётся — каскад
							progress = true
						}
						// owner.state==0 → подождём следующего раунда
					} else {
						m.state = 1 // свободный ключ
						grantedNew[m.new] = true
						progress = true
					}
				}
			}
			if !progress {
				break
			}
		}
		// Оставшиеся pending — чистый цикл (своп): переезжают, если целевой ключ
		// не занят статиком/уехавшим и не конфликтует по цели.
		for _, m := range movers {
			if m.state == 0 {
				if static[m.new] || grantedNew[m.new] {
					m.state = 2 // конфликт — остаётся
				} else {
					m.state = 1
					grantedNew[m.new] = true
				}
			}
		}
		// собрать финаль + проверка на ПОТЕРЮ: если после разрешения какой-то
		// биндинг пропал/задублировался — отбрасываем батч текущего appwin
		// (no-op + warn), гарантируя «ни один биндинг не теряется».
		final := make(map[string][]string, len(perApp))
		for k := range static {
			final[k] = perApp[k]
		}
		for _, m := range movers {
			if m.state == 1 {
				if m.new != "" {
					final[m.new] = m.orig // переехал/insert
				}
				// state==1 && new=="" → удаление: НЕ кладём на старый ключ
			} else {
				final[m.old] = m.orig // stayed — держит свой ключ
			}
		}
		// no-loss проверка: ожидаемые остатки (статики + не-удалённые mover'ы)
		// должны точно совпасть с финалом (иначе перезапись/потеря → отброс батча).
		specKey := func(s []string) string { return strings.Join(s, "\x00") }
		expected := map[string]int{}
		for k := range static {
			expected[specKey(perApp[k])]++
		}
		for _, m := range movers {
			if m.new != "" {
				expected[specKey(m.orig)]++
			}
		}
		finalCnt := map[string]int{}
		for _, v := range final {
			finalCnt[specKey(v)]++
		}
		lossless := len(expected) == len(finalCnt)
		if lossless {
			for k, c := range expected {
				if finalCnt[k] != c {
					lossless = false
					break
				}
			}
		}
		if !lossless {
			slog.Warn("keybindings: батч не разрешим без потерь — оставлен как был",
				"app", app, "entries", len(entries))
			continue
		}
		for k := range perApp {
			delete(perApp, k)
		}
		for k, v := range final {
			perApp[k] = v
		}
	}
}

// legacyToQt — легаси-запись → Qt-последовательность: AltCtrl+X → Ctrl+Alt+X
// (модификаторы по порядку Qt; клавиша — как в записи, верхний регистр).
func legacyToQt(seq string) string {
	if seq == "" {
		return ""
	}
	// Мультибуквенные клавиши, чьи легаси-имена Qt не знает в верхнем регистре
	// (ревью 2026-10-14: «Ctrl+PAGE_DOWN» невалиден — Qt ждёт PgDown/PgUp).
	named := map[string]string{
		"Page_Down": "PgDown",
		"Page_Up":   "PgUp",
		"Insert":    "Insert",
		"Return":    "Return",
		"Tab":       "Tab",
	}
	parts := strings.Split(seq, "+")
	var mods, key []string
	for _, p := range parts {
		switch p {
		case "Alt", "Ctrl", "Shift", "Meta":
			mods = append(mods, p)
		case "AltCtrl", "CtrlAlt":
			mods = append(mods, "Ctrl", "Alt")
		case "CtrlShift", "ShiftCtrl":
			// Ревью 2026-10-14: токен сид-дефолта closealltabs не входил в
			// свитч — «CtrlShift+F4» превращался в невалидный «CTRLSHIFT+F4»
			// и биндинг никогда не срабатывал.
			mods = append(mods, "Ctrl", "Shift")
		default:
			if n, ok := named[p]; ok {
				key = append(key, n)
				continue
			}
			key = append(key, strings.ToUpper(p)) // Qt: клавиша верхним регистром
		}
	}
	// Qt-порядок модификаторов: Ctrl, Alt, Shift, Meta — независимо от
	// легаси-порядка в конфиге.
	sortMods := map[string]int{"Ctrl": 0, "Alt": 1, "Shift": 2, "Meta": 3}
	sort.SliceStable(mods, func(i, j int) bool { return sortMods[mods[i]] < sortMods[mods[j]] })
	mods = append(mods, key...)
	return strings.Join(mods, "+")
}

// bindingActions — flatten конфиг-биндингов в детерминированный список
// (по seq) с фильтром неподдерживаемых действий.
func bindingActions(cfg *config.Config) []hotkey {
	var out []hotkey
	for _, perApp := range cfg.Defaults.KeyBindings {
		for seq, spec := range perApp {
			if strings.HasPrefix(seq, "undef-") {
				continue // легаси-заглушка (undef-infotab): не биндим (ревью Batch C)
			}
			if len(spec) >= 2 && supportedActions[spec[1]] {
				out = append(out, hotkey{Seq: seq, Action: spec[1]})
			}
		}
	}
	// ревью M21 P3: слайс-сортировка недетерминированная — сортируем
	// устойчиво и по (Seq, Action).
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Seq != out[j].Seq {
			return out[i].Seq < out[j].Seq
		}
		return out[i].Action < out[j].Action
	})
	// Ревью 2026-10-14: дубли seq между секциями ставили два одинаковых
	// QShortcut — Qt объявляет пару ambiguous и НЕ СРАБАТЫВАЕТ НИ ОДИН
	// (на Perl-конфигах так умирали Alt+N и Alt+E). Дедуп: первый по
	// детерминированной сортировке.
	dedup := out[:0]
	for i, hk := range out {
		if i > 0 && out[i-1].Seq == hk.Seq {
			continue
		}
		dedup = append(dedup, hk)
	}
	return dedup
}

// altDigitTabSeq — встроенное переключение вкладки Alt+1..9 (иначе "").
func altDigitTabSeq(n int) string {
	if n >= 1 && n <= 9 {
		return "Alt+" + string(rune('0'+n))
	}
	return ""
}

// installedShortcuts — созданные QShortcut'ы (пользовательские + встроенные
// Alt+1..9). Нужны, чтобы повторная установка после правки привязок сняла
// старые: иначе правки применялись бы только после перезапуска (Qt объявил бы
// старую и новую пары ambiguous). Трогается только на Qt-нити.
var installedShortcuts []*qt.QShortcut

// hotkeyReinstall — пересборка QShortcut после сохранения редактора привязок.
// Задаётся в ui.go при старте (Qt-нить); nil в тестах без Qt.
var hotkeyReinstall func()

// reinstallHotkeys — применить текущие привязки без перезапуска приложения.
func reinstallHotkeys() {
	if hotkeyReinstall != nil {
		hotkeyReinstall()
	}
}

// installHotkeys — QShortcut на главном окне: биндинги из defaults
// (фильтр поддерживаемых действий) + встроенные Alt+1..9 → вкладки.
// Повторный вызов (после правки привязок) сначала снимает прежние шорткаты.
func installHotkeys(w *qt.QMainWindow, cfg *config.Config, actions map[string]func()) {
	for _, sc := range installedShortcuts {
		sc.SetEnabled(false)
		sc.DeleteLater()
	}
	installedShortcuts = nil
	// Ревью 2 (Minor): встроенные Alt+1..9 не должны дублировать пользовательский
	// биндинг на ту же клавишу — Qt объявляет пару ambiguous и не срабатывает
	// ни один. Занятые seq не биндим (пользовательский приоритет).
	taken := map[string]bool{}
	for _, hk := range bindingActions(cfg) {
		taken[legacyToQt(hk.Seq)] = true
	}
	for i := 1; i <= 9; i++ {
		if taken[altDigitTabSeq(i)] {
			continue
		}
		sc := qt.NewQShortcut2(qt.QKeySequence_FromString(altDigitTabSeq(i)), w.QObject)
		sc.OnActivated(func() {
			mainthreadW(func() {
				if i-1 < mainTabs.Count() {
					mainTabs.SetCurrentIndex(i - 1)
				}
			})
		})
		installedShortcuts = append(installedShortcuts, sc)
	}
	for _, hk := range bindingActions(cfg) {
		fn, ok := actions[hk.Action]
		if !ok {
			continue
		}
		qseq := qt.QKeySequence_FromString(legacyToQt(hk.Seq))
		// Ревью 2026-10-14: невалидная для Qt последовательность (например
		// легаси «Ctrl+Page_Down» до маппинга в PgDown) молча давала мёртвый
		// шорткат — пользователь не видел клавишу в списке И в приложении.
		// Теперь такое пропускаем с предупреждением.
		if qseq.Count() == 0 {
			slog.Warn("горячая клавиша не распознана Qt — пропущена", "seq", hk.Seq, "action", hk.Action)
			continue
		}
		sc := qt.NewQShortcut2(qseq, w.QObject)
		sc.OnActivated(fn)
		installedShortcuts = append(installedShortcuts, sc)
	}
}

// showKeybindingsEditor — редактор привязок клавиш (Окно/Действие/Клавиша/
// Описание; колонка «Клавиша» редактируемая). Save → applyKeyBindings
// (перенос/удаление ключа, прочие биндинги сохраняются). Qt-нить.
func showKeybindingsEditor(cfg *config.Config, cfgPath string, parent *qt.QWidget) {
	d := qt.NewQDialog2()
	d.SetWindowTitle(i18n.Tr("Keys", "Привязки клавиш"))
	d.SetMinimumSize(qt.NewQSize2(560, 420))
	table := qt.NewQTableWidget3(0, 4)
	table.SetHorizontalHeaderLabels([]string{i18n.Tr("Keys", "Окно"), i18n.Tr("Keys", "Действие"), i18n.Tr("Keys", "Клавиша"), i18n.Tr("Keys", "Описание")})
	// ro — нередактируемая ячейка (только колонка «Клавиша» редактируемая, ревью).
	ro := func(text string) *qt.QTableWidgetItem {
		it := qt.NewQTableWidgetItem2(text)
		it.SetFlags(it.Flags() &^ qt.ItemIsEditable)
		return it
	}
	// base — строки под таблицей (индекс row → kbEntry) для Add/Delete-маппинга.
	var base []kbEntry
	box := qt.NewQVBoxLayout2()
	fill := func() {
		table.SetRowCount(0)
		base = keybindingsList(cfg)
		for _, e := range base {
			r := table.RowCount()
			table.SetRowCount(r + 1)
			table.SetItem(r, 0, ro(e.Window))
			table.SetItem(r, 1, ro(e.Action))
			table.SetItem(r, 2, qt.NewQTableWidgetItem2(e.Key))
			table.SetItem(r, 3, ro(e.Desc))
		}
	}
	fill()
	box.AddWidget(table.QWidget)

	// Добавить: выбор appwin + действие + клавиша → addKeyBinding → refresh.
	add := qt.NewQPushButton3(i18n.Tr("Keys", "Добавить"))
	add.OnClicked(func() {
		app, action, key, ok := askKeyBindingDialog(cfg, parent)
		if !ok {
			return
		}
		if err := addKeyBinding(cfg, app, action, key); err != nil {
			showErr(parent, i18n.Tr("Keys", "Добавить привязку: ")+err.Error())
			return
		}
		fill()
	})
	// Удалить: удалить выбранную строку (по app+orig).
	del := qt.NewQPushButton3(i18n.Tr("Keys", "Удалить"))
	del.OnClicked(func() {
		r := table.CurrentRow()
		if r < 0 || r >= len(base) {
			return
		}
		removeKeyBinding(cfg, base[r].App, base[r].Orig)
		fill()
	})
	editBtns := qt.NewQHBoxLayout2()
	editBtns.AddWidget(add.QWidget)
	editBtns.AddWidget(del.QWidget)
	editBtns.AddStretch()
	box.AddLayout(editBtns.QLayout)

	dialogButtons(box, d, i18n.Tr("Keys", "Сохранить"), func() {
		edits := make([]kbEntry, 0, len(base))
		for i, e := range base {
			if it := table.Item(i, 2); it != nil {
				e.Key = it.Text() // редактируемая клавиша
			}
			edits = append(edits, e)
		}
		applyKeyBindings(cfg, edits)
		// Правки должны вступить в силу сразу: пересобираем QShortcut'ы.
		reinstallHotkeys()
		d.Accept()
	})
	d.SetLayout(box.QLayout)
	if d.Exec() == int(qt.QDialog__Accepted) {
		_ = saveCfgUI(cfg, cfgPath, parent)
	}
}

// askKeyBindingDialog — под-диалог новой привязки: appwin, действие, клавиша.
// Возвращает (app, action, key, ok). Qt-нить.
func askKeyBindingDialog(cfg *config.Config, parent *qt.QWidget) (string, string, string, bool) {
	d := qt.NewQDialog2()
	d.SetWindowTitle(i18n.Tr("Keys", "Новая привязка"))
	d.SetMinimumWidth(360)
	form := qt.NewQFormLayout2()

	appCombo := qt.NewQComboBox2()
	for _, a := range keyBindApps(cfg) {
		appCombo.AddItem(a)
	}
	actCombo := qt.NewQComboBox2()
	actions := make([]string, 0, len(supportedActions))
	for a := range supportedActions {
		actions = append(actions, a)
	}
	sort.Strings(actions)
	for _, a := range actions {
		actCombo.AddItem(a)
	}
	keyEdit := qt.NewQLineEdit2()
	form.AddRow3(i18n.Tr("Keys", "Окно (appwin)"), appCombo.QWidget)
	form.AddRow3(i18n.Tr("Keys", "Действие"), actCombo.QWidget)
	form.AddRow3(i18n.Tr("Keys", "Клавиша"), keyEdit.QWidget)

	box := qt.NewQVBoxLayout2()
	box.AddLayout(form.QLayout)
	dialogButtons(box, d, i18n.Tr("Dialog", "Сохранить"), d.Accept)
	d.SetLayout(box.QLayout)
	if d.Exec() != int(qt.QDialog__Accepted) {
		return "", "", "", false
	}
	app := appCombo.CurrentText()
	act := actCombo.CurrentText()
	key := strings.TrimSpace(keyEdit.Text())
	return app, act, key, true
}
