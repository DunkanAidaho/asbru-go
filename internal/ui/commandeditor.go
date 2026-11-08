package ui

// M29-остаток: редакторы глобальных команд (LocalCommands/RemoteCommands —
// defaults.'local commands'/'remote commands'). Общий табличный редактор
// (имя/команда) поверх чистой пары commandRows/applyCommandRows.

import (
	"sort"

	"github.com/DunkanAidaho/asbru-go/internal/config"
	"github.com/DunkanAidaho/asbru-go/internal/i18n"
	qt "github.com/mappu/miqt/qt6"
)

// commandRow — строка глобальной команды (имя/описание + команда).
// orig — исходная мапа (для сохранения незатронутых ключей confirm/intro…).
type commandRow struct {
	Name string
	Cmd  string
	orig map[string]any
}

// commandRows — команды defaults.Raw[key] (local/remote commands) как строки,
// отсортированные по имени. orig сохраняется для потери ключей (коном.
// confirm/intro — легаси PACTerminal.pm:1976-2048).
func commandRows(cfg *config.Config, key string) []commandRow {
	if cfg == nil || cfg.Defaults.Raw == nil {
		return nil
	}
	var out []commandRow
	for _, m := range defaultsCommandList(cfg, key) {
		cmd := str(m, "command")
		if cmd == "" {
			cmd = str(m, "txt")
		}
		if cmd == "" {
			continue
		}
		name := str(m, "description")
		if name == "" {
			name = cmd
		}
		out = append(out, commandRow{Name: name, Cmd: cmd, orig: m})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// applyCommandRows — перестроить defaults.Raw[key] из строк. Незатронутые
// ключи исходной мапы (confirm/intro/…) СОХРАНЯЮТСЯ; перезаписываются только
// command/txt/description (как macroFromCells). Пустые команды отбрасываются.
func applyCommandRows(cfg *config.Config, key string, rows []commandRow) {
	if cfg == nil {
		return
	}
	if cfg.Defaults.Raw == nil {
		cfg.Defaults.Raw = map[string]any{}
	}
	list := make([]any, 0, len(rows))
	for _, r := range rows {
		if r.Cmd == "" {
			continue
		}
		m := map[string]any{}
		for k, v := range r.orig {
			m[k] = v
		}
		m["command"], m["txt"], m["description"] = r.Cmd, r.Cmd, r.Name
		list = append(list, m)
	}
	cfg.Defaults.Raw[key] = list
}

// showCommandsEditor — табличный редактор глобальных команд (имя/команда),
// локальных ('local commands') или удалённых ('remote commands'). Qt-нить.
func showCommandsEditor(cfg *config.Config, cfgPath string, parent *qt.QWidget, key, windowTitle string) {
	d := qt.NewQDialog2()
	d.SetWindowTitle(windowTitle)
	d.SetMinimumSize(qt.NewQSize2(520, 360))
	table := qt.NewQTableWidget3(0, 2)
	table.SetHorizontalHeaderLabels([]string{i18n.Tr("Global", "Имя"), i18n.Tr("Global", "Команда")})
	// Параллельный слайс origRows: ДЕРЖИМ В СИНХРОНЕ с таблицей (append на add,
	// remove на delete), чтобы каждая строка несла свою исходную мапу (confirm/
	// intro…). Без item user-data (miqt Data/SetData — через *QVariant, сложно).
	loaded := commandRows(cfg, key)
	origRows := make([]map[string]any, len(loaded))
	for i, r := range loaded {
		origRows[i] = r.orig
		row := table.RowCount()
		table.SetRowCount(row + 1)
		table.SetItem(row, 0, qt.NewQTableWidgetItem2(r.Name))
		table.SetItem(row, 1, qt.NewQTableWidgetItem2(r.Cmd))
	}
	add := qt.NewQPushButton3(i18n.Tr("Global", "+ команду"))
	add.OnClicked(func() {
		table.SetRowCount(table.RowCount() + 1)
		origRows = append(origRows, nil) // новая строка — без orig
	})
	del := qt.NewQPushButton3(i18n.Tr("Global", "Убрать выделенное"))
	del.OnClicked(func() {
		if i := table.CurrentRow(); i >= 0 {
			table.RemoveRow(i)
			if i < len(origRows) {
				origRows = append(origRows[:i], origRows[i+1:]...)
			}
		}
	})
	applyCmds := func() {
		rows := make([]commandRow, 0, table.RowCount())
		for i := 0; i < table.RowCount(); i++ {
			cell := func(col int) string {
				if it := table.Item(i, col); it != nil {
					return it.Text()
				}
				return ""
			}
			row := commandRow{Name: cell(0), Cmd: cell(1)}
			if i < len(origRows) {
				row.orig = origRows[i]
			}
			rows = append(rows, row)
		}
		applyCommandRows(cfg, key, rows)
		d.Accept()
	}
	box := qt.NewQVBoxLayout2()
	box.AddWidget(table.QWidget)
	dialogButtons(box, d, i18n.Tr("Global", "Сохранить"), applyCmds, add, del)
	d.SetLayout(box.QLayout)
	if d.Exec() == int(qt.QDialog__Accepted) {
		_ = saveCfgUI(cfg, cfgPath, parent)
	}
}
