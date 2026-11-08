package ui

import (
	"os"
	"strings"
	"testing"
)

// A2: состояние подключения показывается индикатором (🟡 попытка / 🟢 подключено /
// 🔴 ошибка), а не текстом в заголовке вкладки. Легаси-паритет: в Perl состояние
// тоже было иконкой (PACTerminal.pm:2624 — gtk-connect/gtk-disconnect), а не
// суффиксом заголовка.

// Заголовок вкладки — всегда только имя сессии: статус его не удлиняет.
func TestPresentTabTitleIsNameOnly(t *testing.T) {
	for _, status := range []string{"", "подключено", "подключение…", "ошибка: связь потеряна"} {
		p := presentTab("web-01", status, connConnected)
		if p.Title != "web-01" {
			t.Errorf("status=%q: заголовок вкладки %q, ожидалось имя без статуса", status, p.Title)
		}
	}
}

// Текст статуса не теряется: он уходит в подсказку вкладки (и в футер).
func TestPresentTabTooltipCarriesStatus(t *testing.T) {
	p := presentTab("web-01", "ошибка: связь потеряна", connError)
	if !strings.Contains(p.Tooltip, "web-01") {
		t.Errorf("подсказка %q потеряла имя сессии", p.Tooltip)
	}
	if !strings.Contains(p.Tooltip, "ошибка: связь потеряна") {
		t.Errorf("подсказка %q потеряла текст статуса", p.Tooltip)
	}
}

// Пустой статус не оставляет висящий разделитель в подсказке.
func TestPresentTabTooltipWithoutStatus(t *testing.T) {
	if got := presentTab("web-01", "", connNone).Tooltip; got != "web-01" {
		t.Errorf("подсказка без статуса %q, ожидалось ровно имя", got)
	}
}

// Цвета трёх состояний заданы и различимы; connNone индикатора не рисует.
func TestConnStateRGB(t *testing.T) {
	seen := map[[3]int]connState{}
	for _, st := range []connState{connConnecting, connConnected, connError} {
		r, g, b, ok := connStateRGB(st)
		if !ok {
			t.Fatalf("состояние %v: ожидался цвет индикатора", st)
		}
		key := [3]int{r, g, b}
		if prev, dup := seen[key]; dup {
			t.Errorf("состояния %v и %v одного цвета %v", prev, st, key)
		}
		seen[key] = st
	}
	if _, _, _, ok := connStateRGB(connNone); ok {
		t.Error("connNone не должен рисовать индикатор")
	}
}

// Семантика цветов из требования: зелёный — подключено, жёлтый — попытка,
// красный — ошибка.
func TestConnStateRGBSemantics(t *testing.T) {
	r, g, b, _ := connStateRGB(connConnected)
	if !(g > r && g > b) {
		t.Errorf("подключено: цвет (%d,%d,%d) не зелёный", r, g, b)
	}
	r, g, b, _ = connStateRGB(connConnecting)
	if !(r > b && g > b) {
		t.Errorf("попытка подключения: цвет (%d,%d,%d) не жёлтый", r, g, b)
	}
	r, g, b, _ = connStateRGB(connError)
	if !(r > g && r > b) {
		t.Errorf("ошибка: цвет (%d,%d,%d) не красный", r, g, b)
	}
}

// uiFuncBody — тело функции по её заголовку (до следующего "\nfunc " в начале
// строки). Нужно, чтобы проверять Qt-обвязку по исходнику: headless-тестами её
// не достать (как таблицу сканкодов в internal/embed/scancodes_test.go).
func uiFuncBody(t *testing.T, src, sig string) string {
	t.Helper()
	i := strings.Index(src, sig)
	if i < 0 {
		t.Fatalf("в исходнике нет %s", sig)
	}
	rest := src[i:]
	if j := strings.Index(rest[1:], "\nfunc "); j >= 0 {
		return rest[:j+1]
	}
	return rest
}

// callArg — первый аргумент вызова call в теле (до закрывающей скобки на глубине 0).
func callArg(t *testing.T, body, call string) string {
	t.Helper()
	k := strings.Index(body, call)
	if k < 0 {
		t.Fatalf("в теле нет вызова %s", call)
	}
	arg := body[k+len(call):]
	depth := 0
	for idx, ch := range arg {
		switch ch {
		case '(', '[':
			depth++
		case ')', ']':
			if depth == 0 {
				return arg[:idx]
			}
			depth--
		}
	}
	t.Fatalf("у вызова %s не нашлась закрывающая скобка", call)
	return ""
}

// Контракт: в setConnStatus заголовок вкладки — ровно p.Title и рядом ставится
// иконка-индикатор. Регрессия A2 («статус удлиняет название») ловится здесь:
// склейка со статусом в SetTabText снова уронит этот тест.
func TestSetConnStatusAppliesIndicatorNotStatusText(t *testing.T) {
	raw, err := os.ReadFile("ui.go")
	if err != nil {
		t.Fatalf("не читается ui.go: %v", err)
	}
	body := uiFuncBody(t, string(raw), "func setConnStatus(")
	if got := callArg(t, body, "mainTabs.SetTabText(i, "); got != "p.Title" {
		t.Errorf("заголовок вкладки задаётся как %q — статус не должен попадать в название", got)
	}
	if !strings.Contains(body, "mainTabs.SetTabIcon(i, connStateIcon(p.State))") {
		t.Error("в setConnStatus не ставится иконка состояния (индикатор)")
	}
	if !strings.Contains(body, "mainTabs.SetTabToolTip(i, p.Tooltip)") {
		t.Error("в setConnStatus не обновляется подсказка вкладки — текст статуса потеряется")
	}
}

// Контракт по исходнику: терминальный путь (launchAfterPing) обязан проводить
// вкладку через все три состояния — жёлтый при попытке подключения, зелёный при
// CONNECTED и красный везде, где вкладка остаётся жить без соединения. Headless
// Qt не проверить, поэтому проверяем по исходнику (как таблицу сканкодов в
// internal/embed/scancodes_test.go). Ревью A2 MAJOR: без этого теста регрессия
// «вкладка без индикатора / застряла зелёной» проходила мимо всех тестов.
func TestTerminalPathDrivesAllConnStates(t *testing.T) {
	raw, err := os.ReadFile("ui.go")
	if err != nil {
		t.Fatalf("не читается ui.go: %v", err)
	}
	fn := uiFuncBody(t, string(raw), "func launchAfterPing(")
	const yellow = "setConnStatus(id, connConnecting, i18n.Tr(\"Status\", \"подключение…\"))"
	if !strings.Contains(fn, yellow) {
		t.Error("терминальная вкладка не получает жёлтый индикатор на время подключения")
	}
	if !strings.Contains(fn, "setConnStatus(id, connConnected,") {
		t.Error("в терминальном пути нет зелёного индикатора на EventStatusConnected")
	}
	// Красный — в трёх местах, где вкладка остаётся жить: соединение не поднялось,
	// обрыв при closeOn=нет и защитная ветка закрытия канала.
	if got := strings.Count(fn, "setConnStatus(id, connError,"); got != 3 {
		t.Errorf("красный индикатор выставляется %d раз(а) в терминальном пути, ожидалось 3", got)
	}
	// Соединение не поднялось и обрыв уже подключённой сессии — разные флаги
	// connected (текст статуса выбирает endStatusText, см. TestEndStatusText).
	for _, call := range []string{
		"endStatusText(e.Kind == session.EventFailed, false, e.Text)",
		"endStatusText(e.Kind == session.EventFailed, true, e.Text)",
	} {
		if !strings.Contains(fn, call) {
			t.Errorf("в терминальном пути нет вызова %s", call)
		}
	}
}

// Перезапуск сессии — снова рукопожатие: вкладка должна стать жёлтой, иначе
// держит прежний красный/зелёный до CONNECTED.
func TestRestartTermSetsConnecting(t *testing.T) {
	raw, err := os.ReadFile("ui.go")
	if err != nil {
		t.Fatalf("не читается ui.go: %v", err)
	}
	if !strings.Contains(uiFuncBody(t, string(raw), "func restartTerm("), "setConnStatus(id, connConnecting,") {
		t.Error("перезапуск не переводит индикатор в «идёт подключение»")
	}
}

// Переименование вкладки не должно терять текст состояния (и не должно
// показывать старое имя): подсказка собирается из нового имени и последнего
// известного статуса (tabConn).
func TestSetTermTitleKeepsStatus(t *testing.T) {
	raw, err := os.ReadFile("ui.go")
	if err != nil {
		t.Fatalf("не читается ui.go: %v", err)
	}
	if !strings.Contains(uiFuncBody(t, string(raw), "func setTermTitle("), "presentTab(nv, prev.text, prev.st)") {
		t.Error("после переименования подсказка вкладки не собирается из последнего статуса")
	}
}

// Текст статуса для завершившейся сессии: сбой (EventFailed) — «ошибка: причина»,
// штатное завершение или обрыв — «отключено» («не подключено», если соединение так
// и не поднялось). Причины пользовательских действий («interrupted» от
// «Отключиться») не должны читаться как сбой (ревью A2 MINOR).
func TestEndStatusText(t *testing.T) {
	cases := []struct {
		failed, connected bool
		reason, want      string
	}{
		{true, false, "auth failed", "ошибка: auth failed"},
		{true, true, "read panic: boom", "ошибка: read panic: boom"},
		{true, false, "", "не подключено"},
		{true, true, "", "отключено"},
		// Настоящие сбои приходят как EventClosed (session/expect): таймаут
		// рукопожатия, отвергнутый пароль, исчерпанные перезапуски — их причина
		// обязана остаться в статусе (ревью A2 MINOR-1).
		{false, false, "timeout", "ошибка: timeout"},
		{false, false, "too many restarts", "ошибка: too many restarts"},
		{false, false, "Provided username/password 'x' was rejected", "ошибка: Provided username/password 'x' was rejected"},
		// Нейтральные причины — не сбой: их инициировал пользователь или это
		// нормальное завершение со стороны сервера.
		{false, false, "interrupted", "не подключено"},
		{false, true, "interrupted", "отключено"},
		{false, false, "cancelled", "не подключено"},
		{false, true, "cancelled", "отключено"},
		{false, false, "Connection ended by remote peer", "не подключено"},
		{false, true, "Connection ended by remote peer", "отключено"},
		{false, true, "", "отключено"},
	}
	for _, c := range cases {
		if got := endStatusText(c.failed, c.connected, c.reason); got != c.want {
			t.Errorf("endStatusText(%v, %v, %q) = %q, ожидалось %q", c.failed, c.connected, c.reason, got, c.want)
		}
	}
}
