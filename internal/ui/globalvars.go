package ui

// M29-остаток: редакторы глобальных настроек-списков (GlobalVariables,
// LocalCommands, RemoteCommands). Сначала GlobalVariables (map[string]VarEntry
// {Type, Value, Hidden}) — табличный редактор, как переменные соединения.

import (
	"sort"

	"github.com/DunkanAidaho/asbru-go/internal/config"
	"github.com/DunkanAidaho/asbru-go/internal/i18n"
	qt "github.com/mappu/miqt/qt6"
)

// globalVarRow — строка глобальной переменной.
type globalVarRow struct {
	Name   string
	Type   string
	Value  string
	Hidden bool
}

// globalVarRows — глобальные переменные, отсортированные по имени.
func globalVarRows(cfg *config.Config) []globalVarRow {
	if cfg == nil || cfg.Defaults.GlobalVariables == nil {
		return nil
	}
	out := make([]globalVarRow, 0, len(cfg.Defaults.GlobalVariables))
	for name, ve := range cfg.Defaults.GlobalVariables {
		out = append(out, globalVarRow{Name: name, Type: ve.Type, Value: ve.Value, Hidden: ve.Hidden == 1})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// applyGlobalVars — перестроить map из строк редактора (пустые имена отбрасываются,
// тип/значение из строк, hidden из bool).
func applyGlobalVars(cfg *config.Config, rows []globalVarRow) {
	if cfg == nil {
		return
	}
	m := map[string]config.VarEntry{}
	for _, r := range rows {
		if r.Name == "" {
			continue
		}
		m[r.Name] = config.VarEntry{Type: r.Type, Value: r.Value, Hidden: config.FlexInt(boolToInt(r.Hidden))}
	}
	cfg.Defaults.GlobalVariables = m
}

// showGlobalVarsEditor — редактор глобальных переменных (имя/тип/значение/скрыт),
// таблица + добавить/убрать. Сохраняет через saveCfgUI. Qt-нить.
func showGlobalVarsEditor(cfg *config.Config, cfgPath string, parent *qt.QWidget) {
	d := qt.NewQDialog2()
	d.SetWindowTitle(i18n.Tr("Global", "Глобальные переменные"))
	d.SetMinimumSize(qt.NewQSize2(520, 380))
	table := qt.NewQTableWidget3(0, 4)
	table.SetHorizontalHeaderLabels([]string{i18n.Tr("Global", "Имя"), i18n.Tr("Global", "Тип"), i18n.Tr("Global", "Значение"), i18n.Tr("Global", "Скрыт")})
	for _, r := range globalVarRows(cfg) {
		row := table.RowCount()
		table.SetRowCount(row + 1)
		table.SetItem(row, 0, qt.NewQTableWidgetItem2(r.Name))
		table.SetItem(row, 1, qt.NewQTableWidgetItem2(r.Type))
		table.SetItem(row, 2, qt.NewQTableWidgetItem2(r.Value))
		cb := qt.NewQTableWidgetItem2("")
		cb.SetCheckState(checkStateOrUn(r.Hidden))
		table.SetItem(row, 3, cb)
	}
	add := qt.NewQPushButton3(i18n.Tr("Global", "+ переменную"))
	add.OnClicked(func() { table.SetRowCount(table.RowCount() + 1) })
	del := qt.NewQPushButton3(i18n.Tr("Global", "Убрать выделенное"))
	del.OnClicked(func() {
		if table.CurrentRow() >= 0 {
			table.RemoveRow(table.CurrentRow())
		}
	})
	applyVars := func() {
		rows := make([]globalVarRow, 0, table.RowCount())
		for i := 0; i < table.RowCount(); i++ {
			row := globalVarRow{}
			cell := func(col int) string {
				if it := table.Item(i, col); it != nil {
					return it.Text()
				}
				return ""
			}
			row.Name, row.Type, row.Value = cell(0), cell(1), cell(2)
			if it := table.Item(i, 3); it != nil {
				row.Hidden = it.CheckState() == qt.Checked
			}
			rows = append(rows, row)
		}
		applyGlobalVars(cfg, rows)
		d.Accept()
	}
	box := qt.NewQVBoxLayout2()
	box.AddWidget(table.QWidget)
	dialogButtons(box, d, i18n.Tr("Global", "Сохранить"), applyVars, add, del)
	d.SetLayout(box.QLayout)
	if d.Exec() == int(qt.QDialog__Accepted) {
		_ = saveCfgUI(cfg, cfgPath, parent)
	}
}

// checkStateOrUn — bool → CheckState (для колонки-галки).
func checkStateOrUn(b bool) qt.CheckState {
	if b {
		return qt.Checked
	}
	return qt.Unchecked
}
