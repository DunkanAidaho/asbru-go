package ui

import "github.com/DunkanAidaho/asbru-go/internal/i18n"

// A2: состояние подключения показывается индикатором во вкладке, а не текстом в
// её заголовке (текст сильно удлинял название). Здесь — чистая часть: выбор
// цвета и содержимого заголовка/подсказки; Qt-обвязка (рисование кружка и
// применение к вкладке) живёт в ui.go.
// Легаси-паритет: в Perl состояние тоже показывалось иконкой
// (PACTerminal.pm:2624 — gtk-connect/gtk-disconnect).

// connState — состояние подключения сессии для индикатора вкладки.
type connState int

const (
	connNone       connState = iota // состояния нет — индикатор не рисуется
	connConnecting                  // идёт попытка подключения (жёлтый)
	connConnected                   // подключено (зелёный)
	connError                       // ошибка или обрыв (красный)
)

// Цвета индикатора: подобраны читаемыми на светлой и тёмной темах.
const (
	colConnectingR, colConnectingG, colConnectingB = 0xE8, 0xB3, 0x00 // жёлтый
	colConnectedR, colConnectedG, colConnectedB    = 0x1B, 0xA5, 0x3A // зелёный
	colErrorR, colErrorG, colErrorB                = 0xD1, 0x2B, 0x2B // красный
)

// connStateRGB — цвет индикатора для состояния; ok=false, если рисовать нечего.
func connStateRGB(st connState) (r, g, b int, ok bool) {
	switch st {
	case connConnecting:
		return colConnectingR, colConnectingG, colConnectingB, true
	case connConnected:
		return colConnectedR, colConnectedG, colConnectedB, true
	case connError:
		return colErrorR, colErrorG, colErrorB, true
	}
	return 0, 0, 0, false
}

// tabPresentation — то, что вкладка показывает про сессию: заголовок (только имя
// соединения — статус в него НЕ входит), подсказка с полным текстом состояния и
// состояние для индикатора.
type tabPresentation struct {
	Title   string
	Tooltip string
	State   connState
}

// connInfo — последнее состояние подключения вкладки и текст статуса. Нужно там,
// где вкладку пересоздают или переименовывают (reattach, setTermTitle): без него
// индикатор пропадал бы до следующего события сессии, а подсказка врала бы.
type connInfo struct {
	st   connState
	text string
}

// presentTab собирает представление вкладки. Текст статуса не теряется: он уходит
// в подсказку вкладки (и отдельно — в строку состояния в футере).
func presentTab(name, status string, st connState) tabPresentation {
	p := tabPresentation{Title: name, State: st}
	switch {
	case name == "":
		p.Tooltip = status
	case status == "":
		p.Tooltip = name
	default:
		p.Tooltip = name + " — " + status
	}
	return p
}

// neutralEndReason — причины завершения, которые сбоем НЕ являются: их инициировал
// пользователь («interrupted» — «Отключиться», «cancelled» — отказ на запрос
// пароля) или так нормально завершает сервер. Их тексты — константы
// internal/session (session.go:443, expect.go:203, expect.go:314, session.go:315).
// Всё остальное в статусе помечается ошибкой: настоящие сбои (таймаут
// рукопожатия, отвергнутый пароль, исчерпанные перезапуски) приходят как
// EventClosed, а не EventFailed, и их причина должна быть видна.
func neutralEndReason(reason string) bool {
	switch reason {
	case "", "interrupted", "cancelled", "Connection ended by remote peer":
		return true
	}
	return false
}

// endStatusText — текст статуса для завершившейся сессии. Сбой даёт «ошибка:
// причина»; штатное завершение и обрыв — «отключено» (или «не подключено», если
// соединение так и не поднялось).
func endStatusText(failed, connected bool, reason string) string {
	if !failed {
		failed = !neutralEndReason(reason)
	}
	if failed && reason != "" {
		return i18n.Tr("Status", "ошибка: ") + reason
	}
	if connected {
		return i18n.Tr("Status", "отключено")
	}
	return i18n.Tr("Status", "не подключено")
}
