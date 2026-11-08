package ui

import (
	"errors"
	"testing"

	"github.com/DunkanAidaho/asbru-go/internal/config"
	"github.com/DunkanAidaho/asbru-go/internal/session"
	"github.com/DunkanAidaho/asbru-go/internal/ui/wgt"
)

// patchMainthread — выполнить «на Qt-нити» inline (в тестах Qt-петли нет).
// Возвращает счётчик вызовов через указатель.
func patchMainthread(calls *int) func() {
	old := mainthreadW
	mainthreadW = func(f func()) {
		if calls != nil {
			*calls++
		}
		f()
	}
	return func() { mainthreadW = old }
}

// BACKLOG:35 — покрытие-гэп api_backend: проверяем валидацию и проводку
// к сессиям без живого Qt-цикла.

func TestAPIBackendConnectValidation(t *testing.T) {
	// cfg.Environments теперь читается на Qt-нити (mainthreadW) — в тестах без
	// Qt-петли выполняем колбэк inline.
	defer patchMainthread(nil)()
	var nilB *apiBackend
	if err := nilB.Connect("u1"); err == nil {
		t.Fatal("nil-бэкенд принят")
	}
	if err := (&apiBackend{}).Connect("u1"); err == nil {
		t.Fatal("без конфигурации ошибки нет")
	}
	cfg := &config.Config{Environments: map[string]config.Environment{
		"grp": {Name: "Группа", IsGroup: 1},
	}}
	b := &apiBackend{cfg: cfg}
	if err := b.Connect("nope"); err == nil {
		t.Fatal("несуществующий uuid принят")
	}
	if err := b.Connect("grp"); err == nil {
		t.Fatal("группа принята как соединение")
	}
}

func TestAPIBackendConnectLaunches(t *testing.T) {
	calls := 0
	defer patchMainthread(&calls)()
	// запуск соединения не выполняем: подменяем launch-шов
	// Qt-виджет в тесте не создаём: launchConnection должен дойти до
	// wgtCreate и корректно отступить (без живого QApplication).
	old := wgtCreate
	wgtCreate = func(*wgt.Callback) (int, error) { return 0, errors.New("тест: без Qt") }
	defer func() { wgtCreate = old }()

	cfg := &config.Config{Environments: map[string]config.Environment{
		"u1": {Name: "c", Method: "SSH"},
	}}
	b := &apiBackend{cfg: cfg}
	if err := b.Connect("u1"); err != nil {
		t.Fatalf("валидное соединение отклонено: %v", err)
	}
	if calls != 1 {
		t.Fatalf("на Qt-нить ушло %d вызовов, хотим 1", calls)
	}
}

func TestAPIBackendStopConn(t *testing.T) {
	defer patchMainthread(nil)()
	b := &apiBackend{}
	if err := b.StopConn("u1"); err == nil {
		t.Fatal("нет сессии, но ошибки нет")
	}
	tabMu.Lock()
	tabUUID[7] = "u1"
	tabMu.Unlock()
	defer func() {
		tabMu.Lock()
		delete(tabUUID, 7)
		tabMu.Unlock()
	}()
	if err := b.StopConn("u1"); err != nil {
		t.Fatalf("сессия есть, но ошибка: %v", err)
	}
}

func TestAPIBackendSendWritesToSession(t *testing.T) {
	defer patchMainthread(nil)()
	in := make(chan []byte, 1)
	s := &session.Session{}
	s.In = in
	tabMu.Lock()
	tabUUID[11] = "u1"
	tabSess[11] = s
	tabMu.Unlock()
	defer func() {
		tabMu.Lock()
		delete(tabUUID, 11)
		delete(tabSess, 11)
		tabMu.Unlock()
	}()

	b := &apiBackend{}
	if err := b.Send("nope", "x"); err == nil {
		t.Fatal("нет сессии, но ошибки нет")
	}
	if err := b.Send("u1", "whoami"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	select {
	case got := <-in:
		if string(got) != "whoami" {
			t.Fatalf("в сессию ушло %q", got)
		}
	default:
		t.Fatal("текст не дошёл до канала сессии")
	}
}

// Переполненный канал — лог-дроп, но НЕ ошибка скрипту (как cb.Data).
func TestAPIBackendSendDropOnFullIsNotError(t *testing.T) {
	defer patchMainthread(nil)()
	in := make(chan []byte, 1)
	in <- []byte("занято")
	s := &session.Session{}
	s.In = in
	tabMu.Lock()
	tabUUID[12] = "u2"
	tabSess[12] = s
	tabMu.Unlock()
	defer func() {
		tabMu.Lock()
		delete(tabUUID, 12)
		delete(tabSess, 12)
		tabMu.Unlock()
	}()

	if err := (&apiBackend{}).Send("u2", "x"); err != nil {
		t.Fatalf("дроп полного канала вернул ошибку: %v", err)
	}
}

// Сессия есть, но не готова (нет канала) — понятная ошибка, не паника.
func TestAPIBackendSendNoSessionChannel(t *testing.T) {
	defer patchMainthread(nil)()
	tabMu.Lock()
	tabUUID[13] = "u3"
	tabSess[13] = &session.Session{}
	tabMu.Unlock()
	defer func() {
		tabMu.Lock()
		delete(tabUUID, 13)
		delete(tabSess, 13)
		tabMu.Unlock()
	}()

	if err := (&apiBackend{}).Send("u3", "x"); err == nil {
		t.Fatal("сессия без канала: ошибки нет")
	}
}

// tabIDsForUUID — детерминированный порядок (скрипт видит стабильные id).
func TestTabIDsForUUIDDeterministic(t *testing.T) {
	tabMu.Lock()
	tabUUID[30] = "same"
	tabUUID[10] = "same"
	tabUUID[20] = "same"
	tabUUID[40] = "other"
	tabMu.Unlock()
	defer func() {
		tabMu.Lock()
		for _, id := range []int{30, 10, 20, 40} {
			delete(tabUUID, id)
		}
		tabMu.Unlock()
	}()

	got := tabIDsForUUID("same")
	if len(got) != 3 || got[0] != 10 || got[1] != 20 || got[2] != 30 {
		t.Fatalf("ids = %v, хотим [10 20 30]", got)
	}
	if ids := tabIDsForUUID("missing"); ids != nil {
		t.Fatalf("нет сессий, а вернулось %v", ids)
	}
}

// activeTabAmong без Qt (mainTabs==nil) — первая вкладка, без паники.
func TestActiveTabAmongNoQt(t *testing.T) {
	if got := activeTabAmong([]int{5, 6}); got != 5 {
		t.Fatalf("выбрано %d, хотим первую (5)", got)
	}
}

// pickSplitMember — активный член split-страницы: по фокусу, иначе меньший id.
func TestPickSplitMember(t *testing.T) {
	cases := []struct {
		a, b             int
		aActive, bActive bool
		want             int
	}{
		{7, 3, true, false, 7},
		{7, 3, false, true, 3},
		{7, 3, false, false, 3}, // без фокуса — детерминированно меньший
		{7, 3, true, true, 3},   // оба «активны» — детерминированно меньший
	}
	for _, c := range cases {
		if got := pickSplitMember(c.a, c.b, c.aActive, c.bActive); got != c.want {
			t.Errorf("pickSplitMember(%d,%d,%v,%v) = %d, хотим %d",
				c.a, c.b, c.aActive, c.bActive, got, c.want)
		}
	}
}

func TestAPIBackendOpenConnValidation(t *testing.T) {
	var nilB *apiBackend
	if err := nilB.OpenConn(); err == nil {
		t.Fatal("nil-бэкенд принят")
	}
	if err := (&apiBackend{}).OpenConn(); err == nil {
		t.Fatal("без конфигурации ошибки нет")
	}
}
