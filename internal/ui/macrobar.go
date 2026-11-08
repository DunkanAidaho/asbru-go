package ui

// M28/#4: панель макросов терминала (легаси PACTerminal.pm:3737-3921).
// Режим панели — defaults 'show commands box' (0=Нет, 1=комбобокс, 2=кнопки),
// включение глобальных команд — 'show global commands box'. Список команд:
// per-conn env.Macros + глобальные defaults.'remote commands'.

import (
	"sort"
	"strings"

	"github.com/DunkanAidaho/asbru-go/internal/config"
	"github.com/DunkanAidaho/asbru-go/internal/i18n"
	"github.com/DunkanAidaho/asbru-go/internal/session"
	qt "github.com/mappu/miqt/qt6"
)

// macroBarItem — один пункт панели макросов.
type macroBarItem struct {
	Desc    string // подпись (fallback — команда)
	Cmd     string // команда для отправки
	Confirm bool   // подтверждение перед выполнением
	Intro   string // intro-строка (легаси $$hash{intro})
}

// macroBarMode — режим панели макросов из defaults 'show commands box'.
// Значение: целое 0/1/2 либо строка-метка ("Never"/"Combobox"/"Buttons").
// Отсутствие/невалидное значение → 0 (Нет).
func macroBarMode(d *config.Defaults) int {
	if d == nil {
		return 0
	}
	switch n := d.Raw["show commands box"].(type) {
	case int:
		return clampMode(n)
	case int64:
		return clampMode(int(n))
	case float64:
		return clampMode(int(n))
	case string:
		switch strings.ToLower(n) {
		case "combobox", "combo":
			return 1
		case "buttons":
			return 2
		}
	}
	return 0
}

func clampMode(n int) int {
	if n < 0 {
		return 0
	}
	if n > 2 {
		return 2
	}
	return n
}

// showGlobalCommands — включать ли глобальные 'remote commands' в панель
// (defaults 'show global commands box'; отсутствие → false).
func showGlobalCommands(d *config.Defaults) bool {
	if d == nil {
		return false
	}
	return mapBool(d.Raw["show global commands box"])
}

// macroBarList — команды панели: per-conn env.Macros + глобальные
// 'remote commands' (при show global commands box). Пустые команды
// отбрасываются; сортировка по description (case-insensitive, stable,
// как легаси). Подпись — desc (иначе cmd).
func macroBarList(cfg *config.Config, env *config.Environment) []macroBarItem {
	var out []macroBarItem
	if env != nil {
		for _, m := range env.Macros {
			cmd := str(m, "txt")
			if cmd == "" {
				cmd = str(m, "command")
			}
			if strings.TrimSpace(cmd) == "" {
				continue
			}
			desc := str(m, "description")
			if strings.TrimSpace(desc) == "" {
				desc = cmd
			}
			out = append(out, macroBarItem{Desc: desc, Cmd: cmd,
				Confirm: num(m, "confirm", 0) == 1, Intro: str(m, "intro")})
		}
	}
	if cfg != nil && showGlobalCommands(&cfg.Defaults) {
		for _, m := range defaultsCommandList(cfg, "remote commands") {
			cmd := str(m, "txt")
			if cmd == "" {
				cmd = str(m, "command")
			}
			if strings.TrimSpace(cmd) == "" {
				continue
			}
			desc := str(m, "description")
			if strings.TrimSpace(desc) == "" {
				desc = cmd
			}
			out = append(out, macroBarItem{Desc: desc, Cmd: cmd,
				Confirm: num(m, "confirm", 0) == 1, Intro: str(m, "intro")})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return strings.ToLower(out[i].Desc) < strings.ToLower(out[j].Desc)
	})
	return out
}

// buildMacroBar — виджет панели макросов терминала (M28/#4): mode 1 = комбобокс
// + кнопка «Выполнить», mode 2 = ряд кнопок. Отправка команды в сессию через
// ControlRunCmd (+ showConfirm при Confirm). Вызывается на Qt-нити.
func buildMacroBar(id, mode int, items []macroBarItem) *qt.QWidget {
	bar := qt.NewQWidget2()
	lay := qt.NewQHBoxLayout2()
	bar.SetLayout(lay.QLayout)
	send := func(it macroBarItem) {
		if it.Confirm {
			if !showConfirm(bar, i18n.Tr("Term", "Выполнить команду: ")+it.Desc+"?") {
				return
			}
		}
		tabMu.Lock()
		sess := tabSess[id]
		tabMu.Unlock()
		if sess == nil {
			return
		}
		select {
		case sess.Control <- session.ControlMsg{Kind: session.ControlRunCmd, Cmd: it.Cmd}:
		default:
		}
	}
	if mode == 1 {
		combo := qt.NewQComboBox2()
		for _, it := range items {
			label := it.Desc
			if it.Confirm {
				label = i18n.Tr("Term", "CONFIRM: ") + label
			}
			combo.AddItem(label)
		}
		btn := qt.NewQPushButton3(i18n.Tr("Term", "Выполнить"))
		btn.OnClicked(func() {
			idx := combo.CurrentIndex()
			if idx >= 0 && idx < len(items) {
				send(items[idx])
			}
		})
		lay.AddWidget(combo.QWidget)
		lay.AddWidget(btn.QWidget)
	} else {
		for _, it := range items {
			b := it
			btn := qt.NewQPushButton3(it.Desc)
			btn.OnClicked(func() { send(b) })
			lay.AddWidget(btn.QWidget)
		}
	}
	return bar
}
