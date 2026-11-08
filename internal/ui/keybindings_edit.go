package ui

// Интерактивный редактор KeyBindings (бэклог): добавление/удаление привязок и
// правка клавиши. Правка клавиши (mover/delete/resolve) делает applyKeyBindings
// (state-машина, ревью) — здесь только ЧИСТЫЕ операции «добавить новую» и
// «удалить существующую», TDD-покрытые. UI — showKeybindingsEditor.

import (
	"errors"
	"fmt"
	"strings"

	"github.com/DunkanAidaho/asbru-go/internal/config"
)

// keyBindWinLabel — appwin-ключ → подпись окна (для новой привязки).
var keyBindWinLabel = map[string]string{
	"pacmain":         "Main Window",
	"terminal":        "Terminal",
	"treeConnections": "Connections Tree",
}

// keyBindAppOrder — известные appwin-ключи в порядке показа (константный).
var keyBindAppOrder = []string{"pacmain", "terminal", "treeConnections"}

// keyBindDesc — действие → человекочитаемое описание (для новой привязки;
// неполное — fallback на само действие).
var keyBindDesc = map[string]string{
	"showconnections": "Show/Hide connections list",
	"connect_node":    "Connect selected node",
	"edit_node":       "Edit connection",
	"rename":          "Rename node",
	"clone":           "Clone node",
	"protection":      "Toggle protection",
	"start":           "Start connection",
	"expand_all":      "Expand tree completely",
	"collaps_all":     "Collapse tree completely",
	"close":           "Close Terminal",
	"closealltabs":    "Close all tabs",
	"disconnect":      "Disconnect Terminal",
	"duplicate":       "Duplicate connection",
	"restart":         "Restart connection (close and start)",
	"hostname":        "Guess hostname",
	"sftp":            "Open SFTP session",
	"find":            "Find in connection tree",
	"paste":           "Paste clipboard into terminal",
	"copy":            "Copy selection to clipboard",
	"quit":            "Quit",
	"fullscreen":      "Toggle fullscreen",
	"next":            "Next tab",
	"previous":        "Previous tab",
	"last":            "Last tab",
	"infotab":         "Show connection info",
	"localshell":      "Open local shell",
	"showkeybindings": "Show key bindings",
}

var (
	errKeyBindNoCfg       = errors.New("keybindings: конфиг не инициализирован")
	errKeyBindKeyRequired = errors.New("клавиша не задана")
	errKeyBindUnsupported = errors.New("действие не поддерживается")
	errKeyBindAppUnknown  = errors.New("неизвестный appwin")
)

// addKeyBinding — добавить новую привязку (app, seq)→[window, action, desc].
// Ключ проверяется на коллизию (EqualFold, как легаси-записи); действие — на
// поддержку. Возвращает ошибку, чтобы UI показал её, а не молча не добавил.
func addKeyBinding(cfg *config.Config, app, action, key string) error {
	if cfg == nil || cfg.Defaults.KeyBindings == nil {
		return errKeyBindNoCfg
	}
	if key == "" {
		return errKeyBindKeyRequired
	}
	if !supportedActions[action] {
		return errKeyBindUnsupported
	}
	window := keyBindWinLabel[app]
	if window == "" {
		return errKeyBindAppUnknown
	}
	desc := keyBindDesc[action]
	if desc == "" {
		desc = action
	}
	perApp := cfg.Defaults.KeyBindings[app]
	if perApp == nil {
		perApp = map[string][]string{}
		cfg.Defaults.KeyBindings[app] = perApp
	}
	// Коллизию проверяем по ВСЕМ секциям: bindingActions дедуплицирует seq
	// между appwin (первый по детерминированной сортировке), поэтому привязка,
	// чья клавиша уже занята в другой секции, молча никогда бы не сработала.
	for otherApp, other := range cfg.Defaults.KeyBindings {
		for k := range other {
			if strings.EqualFold(k, key) {
				return fmt.Errorf("клавиша уже занята: %s (%s)", k, otherApp)
			}
		}
	}
	perApp[key] = []string{window, action, desc}
	return nil
}

// removeKeyBinding — удалить привязку (app, seq). Нет записи — no-op.
func removeKeyBinding(cfg *config.Config, app, seq string) {
	if cfg == nil || cfg.Defaults.KeyBindings == nil {
		return
	}
	if perApp := cfg.Defaults.KeyBindings[app]; perApp != nil {
		delete(perApp, seq)
	}
}

// keyBindApps — appwin-ключи для селектора: известные + уже существующие в конфиге.
func keyBindApps(cfg *config.Config) []string {
	seen := map[string]bool{}
	var out []string
	for _, a := range keyBindAppOrder {
		seen[a] = true
		out = append(out, a)
	}
	if cfg != nil {
		for a := range cfg.Defaults.KeyBindings {
			if !seen[a] {
				seen[a] = true
				out = append(out, a)
			}
		}
	}
	return out
}
