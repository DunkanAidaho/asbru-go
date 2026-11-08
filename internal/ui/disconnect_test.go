package ui

import (
	"testing"

	"github.com/DunkanAidaho/asbru-go/internal/session"
)

// бэклог: закрывать ли вкладку при завершении сессии.
// Ошибку от библиотеки (EventFailed) считаем «нештатной» → вкладку НЕ закрываем.
func TestShouldCloseOnDisconnect(t *testing.T) {
	cases := []struct {
		closeOn bool
		kind    session.EventKind
		want    bool
	}{
		{true, session.EventFailed, false},  // ошибка/разрыв от библиотеки → оставить
		{true, session.EventClosed, true},   // чистый выход + вкл → закрыть
		{false, session.EventClosed, false}, // настройка выкл → оставить
		{false, session.EventFailed, false}, // ошибка → оставить
	}
	for _, c := range cases {
		if got := shouldCloseOnDisconnect(c.closeOn, c.kind); got != c.want {
			t.Errorf("shouldCloseOnDisconnect(%v, %v) = %v, хотим %v", c.closeOn, c.kind, got, c.want)
		}
	}
}
