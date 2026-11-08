package ui

import (
	"log/slog"
	"os/exec"
	"strconv"
	"strings"

	"github.com/DunkanAidaho/asbru-go/internal/config"
	"github.com/DunkanAidaho/asbru-go/internal/i18n"
	"github.com/DunkanAidaho/asbru-go/internal/session"
	qt "github.com/mappu/miqt/qt6"
)

// M23: локальные команды — «local before/connected/after»
// (легаси PACTerminal.pm:3376-3470 _wPrePostExec). Записи:
// {command, ask, default}; ask → пользовательский список с предвыбором
// default, без ask → все непустые команды выполняются сразу.

// runLocal — инжектируемый запуск локальной команды (тесты подменяют).
// Легаси: system($cmd) — через sh; команда ЖДЁТ завершения (progress).
var runLocal = func(cmd string) {
	_ = exec.Command("/bin/sh", "-c", cmd).Run()
}

// runLocalDetached — запустить локальную команду ФОНОМ (не блокируя Qt-нить).
// Легаси «Локальные команды» в меню: system("$cmd &") (PACTerminal.pm:3344) —
// GUI не замирает на время исполнения. .Start() возвращает сразу; Wait() в
// горутине, чтобы не копить зомби (M36-ревью MINOR).
var runLocalDetached = func(cmd string) {
	c := exec.Command("/bin/sh", "-c", cmd)
	if err := c.Start(); err != nil {
		slog.Debug("локальная команда: запуск не удался", "err", err, "cmd", cmd)
		return
	}
	go func() { _ = c.Wait() }() // reap — иначе зомби на время жизни приложения
}

// localSelected — политика отбора команд. Любой ask → диалог, в нём ВСЕ
// записи с предвыбором default (легаси _ppeGUI: all rows, check per default);
// без ask вообще — выполнить все (сэнтайзер легаси ставит default//=1
// при первом прогоне, PACUtils.pm:2437-2439 — бездиалоговый эквивалент).
func localSelected(entries []map[string]any) (dialog bool, selected []string) {
	anyAsk := false
	for _, e := range entries {
		if asInt(e["ask"]) == 1 {
			anyAsk = true
			break
		}
	}
	for _, e := range entries {
		cmd, _ := e["command"].(string)
		if strings.TrimSpace(cmd) == "" {
			continue
		}
		if anyAsk {
			if asInt(e["default"]) == 1 {
				selected = append(selected, cmd)
			}
		} else {
			selected = append(selected, cmd)
		}
	}
	return anyAsk, selected
}

func asInt(v any) int {
	switch n := v.(type) {
	case string: // легаси YAML::Tiny: ask: '1' — кавычки (см. FlexInt)
		if i, err := strconv.Atoi(n); err == nil {
			return i
		}
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case bool:
		if n {
			return 1
		}
	}
	return 0
}

// runLocalCommands — выполнить команды detached sh (M36-ревью MINOR: НЕ блокируем
// Qt-нить, как «detached» в комментарии; порядок как в конфиге). reap — в горутине.
func runLocalCommands(cmds []string) {
	for _, c := range cmds {
		runLocalDetached(c)
	}
}

// runWhenLocal — исполнение секции local-* (лёгкая: подстановка + запуск).
func runWhenLocal(env *config.Environment, when string, res session.Resolver) {
	entries := entriesOf(env, when)
	if len(entries) == 0 {
		return
	}
	dialog, cmds := localSelected(entries)
	if dialog {
		// Ревью M23 (блокер): runWhenLocal вызывается и из event-цикла
		// (goroutine!) для connected/after — QDialog обязан жить на
		// Qt-нити; mainthread.Wait исполняется там напрямую (fast-path).
		mainthreadW(func() { cmds = askLocalDialog(entries, when) })
	}
	if len(cmds) == 0 {
		return
	}
	for i, c := range cmds {
		cmds[i] = session.Substitute(c, res) // легаси _subst (PACTerminal.pm:3442)
	}
	runLocalCommands(cmds) // блокирующий вызов ZERO: только порождает (detached)
}

// askLocalDialog — чекбоксы команд (ask=1); default — предвыбран. Qt-нить.
func askLocalDialog(entries []map[string]any, when string) []string {
	d := qt.NewQDialog2()
	d.SetWindowTitle(i18n.Tr("LocalCmd", "Локальные команды"))
	var checks []*qt.QCheckBox
	var cmds []string
	box := qt.NewQVBoxLayout2()
	for _, e := range entries {
		cmd, _ := e["command"].(string)
		if strings.TrimSpace(cmd) == "" {
			continue
		}
		c := qt.NewQCheckBox3(cmd)
		c.SetChecked(asInt(e["default"]) == 1)
		box.AddWidget(c.QWidget)
		checks = append(checks, c)
		cmds = append(cmds, cmd)
	}
	dialogButtons(box, d, i18n.Tr("LocalCmd", "OK"), d.Accept)
	d.SetLayout(box.QLayout)
	if d.Exec() != int(qt.QDialog__Accepted) {
		slog.Debug("локальные команды: отменено", "when", when)
		return nil
	}
	var out []string
	for i, c := range checks {
		if c.IsChecked() {
			out = append(out, cmds[i])
		}
	}
	return out
}

// EntriesOf — команды секции local-* (хелпер для чтения и диалога).
func entriesOf(env *config.Environment, when string) []map[string]any {
	switch when {
	case "local before":
		return env.LocalBefore
	case "local connected":
		return env.LocalConnected
	case "local after":
		return env.LocalAfter
	}
	return nil
}
