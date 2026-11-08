package ui

import (
	"context"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/DunkanAidaho/asbru-go/internal/config"
	"github.com/DunkanAidaho/asbru-go/internal/session"
	"github.com/DunkanAidaho/asbru-go/internal/ui/wgt"
)

// бэклог M29: reachPort — доступен ли host:port (TCP) в пределах таймаута.
func TestReachPort(t *testing.T) {
	if reachPort("127.0.0.1", "1", 200*time.Millisecond) {
		t.Fatal("закрытый порт должен быть недоступен")
	}
	if reachPort("", "", 200*time.Millisecond) {
		t.Fatal("пустой host/port → недоступен")
	}
	if reachPort("nonexistent.invalid", "1", 200*time.Millisecond) {
		t.Fatal("невалидный хост → недоступен")
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	port := strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
	if !reachPort("127.0.0.1", port, time.Second) {
		t.Fatal("слушающий порт должен быть доступен")
	}
}

// бэклог M29: при 'ping port before connect'=1 и недоступном порте сессия не
// запускается, виджет уничтожается. MINOR-4: продолжение асинхронное (ping на
// worker-горутине) — ждём его через сигнал и синхронную postMain.
func TestLaunchConnectionPingGate(t *testing.T) {
	old := pingPort
	pingPort = func(string, string, time.Duration) bool { return false }
	defer func() { pingPort = old }()

	oldPost := postMain
	postMain = func(fn func()) { fn() }
	defer func() { postMain = oldPost }()

	destroyed := make(chan struct{}, 1)
	newSCalled := false
	restore := patchLaunch(func(*wgt.Callback) (int, error) { return 11, nil },
		func(int) {},
		func(int) { destroyed <- struct{}{} },
		func(context.Context, session.BuildEnv, *config.Environment, ...session.Option) (*session.Session, error) {
			newSCalled = true
			return &session.Session{}, nil
		})
	defer restore()

	cfg := &config.Config{Defaults: config.Defaults{Raw: map[string]any{"ping port before connect": 1}}}
	launchConnection("u11", &config.Environment{IP: "10.0.0.1", Port: "1"}, cfg, "")

	select {
	case <-destroyed:
	case <-time.After(time.Second):
		t.Fatal("wgtDestroy не вызван (асинхронное продолжение ping)")
	}
	if newSCalled {
		t.Fatal("sessionNew не должен вызываться при недоступном порте")
	}
}

// бэклог M29: при пустом порте (SSH-дефолт 22) / пустом IP (Local Shell) гейт
// пропускается — не блокируем подключение (ревью BLOCKER).
func TestLaunchConnectionPingGateSkipsEmptyPort(t *testing.T) {
	called := false
	old := pingPort
	pingPort = func(_, _ string, _ time.Duration) bool { called = true; return false }
	defer func() { pingPort = old }()

	newSCalled := false
	restore := patchLaunch(func(*wgt.Callback) (int, error) { return 12, nil },
		func(int) {},
		func(int) {},
		func(context.Context, session.BuildEnv, *config.Environment, ...session.Option) (*session.Session, error) {
			newSCalled = true
			return &session.Session{Out: make(chan session.Event), In: make(chan []byte, 1), Control: make(chan session.ControlMsg, 1)}, nil
		})
	defer restore()

	cfg := &config.Config{Defaults: config.Defaults{Raw: map[string]any{"ping port before connect": 1}}}
	// SSH с пустым портом (дефолт 22) — гейт пропускается.
	launchConnection("u12", &config.Environment{IP: "10.0.0.1", Method: "SSH"}, cfg, "")
	if called {
		t.Fatal("pingPort не должен вызываться при пустом порте")
	}
	if !newSCalled {
		t.Fatal("sessionNew должен вызываться (пустой порт → гейт пропущен)")
	}
}

// MINOR-4: 'ping port before connect' НЕ должен фризить Qt-нить. launchConnection
// обязана вернуться, пока ping ещё выполняется; продолжение (создание сессии /
// уничтожение виджета) диспатчится обратно на Qt-нить (в тестах — синхронно).
func TestLaunchConnectionPingDoesNotBlock(t *testing.T) {
	releaseCh := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(releaseCh) }) }
	defer release()

	pingEntered := make(chan struct{})
	oldPing := pingPort
	pingPort = func(_, _ string, _ time.Duration) bool {
		close(pingEntered)
		<-releaseCh
		return true
	}
	defer func() { pingPort = oldPing }()

	// Qt-петли в тестах нет — продолжение диспатчим синхронно.
	oldPost := postMain
	postMain = func(fn func()) { fn() }
	defer func() { postMain = oldPost }()

	newSCalled := make(chan struct{}, 1)
	restore := patchLaunch(func(*wgt.Callback) (int, error) { return 30, nil },
		func(int) {},
		func(int) {},
		func(context.Context, session.BuildEnv, *config.Environment, ...session.Option) (*session.Session, error) {
			newSCalled <- struct{}{}
			return &session.Session{Out: make(chan session.Event), In: make(chan []byte, 1), Control: make(chan session.ControlMsg, 1)}, nil
		})
	defer restore()

	cfg := &config.Config{Defaults: config.Defaults{Raw: map[string]any{"ping port before connect": 1}}}

	got := make(chan struct{}, 1)
	go func() {
		launchConnection("u30", &config.Environment{IP: "10.0.0.1", Port: "1"}, cfg, "")
		got <- struct{}{}
	}()

	select {
	case <-pingEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("pingPort не вызван")
	}

	// КЛЮЧ: пока ping заблокирован, launchConnection уже обязана вернуться.
	select {
	case <-got:
		// ok — не блокировала Qt-нить
	case <-time.After(500 * time.Millisecond):
		t.Fatal("launchConnection блокировала Qt-нить, ожидая ping")
	}

	release()
	select {
	case <-newSCalled:
	case <-time.After(time.Second):
		t.Fatal("продолжение после ping не создало сессию")
	}
}
