package session

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/DunkanAidaho/asbru-go/internal/config"
)

// M36: пользовательский перезапуск (ControlRestart) — runOnce закрывает поток,
// run() безусловно переподключается (независимо от autoreconnect/retries).
func TestSessionRestartReconnects(t *testing.T) {
	calls := 0
	factory := func() (io.ReadWriteCloser, error) {
		calls++
		st := &pipeStream{r: make(chan []byte, 16), w: make(chan []byte, 16)}
		st.serverSend("login as: ")
		st.serverSend("Password: ")
		st.serverSend("root@host:~# ")
		return st, nil
	}
	s, err := New(context.Background(), BuildEnv{UUID: "u1"},
		&config.Environment{Name: "x", Method: "SSH", User: "u", Pass: "p"},
		WithPrompts(Prompts{Username: "login as:", Password: "Password:", Command: DefaultCommandPrompt}),
		WithStreamFactory(factory))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Control <- ControlMsg{Kind: ControlDisconnect} }()

	// первое подключение → CONNECTED.
	if !waitConnected(t, s.Out) {
		t.Fatal("нет первого CONNECTED")
	}
	// пользовательский рестарт.
	s.Control <- ControlMsg{Kind: ControlRestart}
	// второе подключение → CONNECTED (переподключение).
	if !waitConnected(t, s.Out) {
		t.Fatal("нет второго CONNECTED (рестарт не переподключил)")
	}
	if calls < 2 {
		t.Fatalf("вызовов фабрики %d, хотим ≥2 (переподключение)", calls)
	}
}

// waitConnected — читает события до EventStatusConnected (с прочим дрейфом).
func waitConnected(t *testing.T, out <-chan Event) bool {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case e := <-out:
			t.Logf("event kind=%d text=%q", e.Kind, e.Text)
			if e.Kind == EventStatusConnected {
				return true
			}
		case <-deadline:
			return false
		}
	}
}

// M36-ревью MAJOR: «Отредактировать сессию» затем «Перезапустить» должен
// применять правки. Session копирует cfg при старте; RefreshConfig заменяет
// указатель, и следующий runOnce (переподключение) использует новое значение.
func TestSessionRefreshConfigAppliedOnRestart(t *testing.T) {
	calls := 0
	factory := func() (io.ReadWriteCloser, error) {
		calls++
		st := &pipeStream{r: make(chan []byte, 16), w: make(chan []byte, 16)}
		st.serverSend("login as: ")
		st.serverSend("Password: ")
		st.serverSend("root@host:~# ")
		return st, nil
	}
	s, err := New(context.Background(), BuildEnv{UUID: "u1"},
		&config.Environment{Name: "x", Method: "SSH", User: "old", Pass: "p"},
		WithPrompts(Prompts{Username: "login as:", Password: "Password:", Command: DefaultCommandPrompt}),
		WithStreamFactory(factory))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Control <- ControlMsg{Kind: ControlDisconnect} }()

	// после рестарта login-блок отправляет обновлённого пользователя: читаем
	// записанные байты СЕГО потока и ждём 'newuser', а не 'old'.
	if !waitConnected(t, s.Out) {
		t.Fatal("нет первого CONNECTED")
	}
	// «правим» подключение (M36: editSessionForID → RefreshConfig после окна).
	s.RefreshConfig(&config.Environment{Name: "x", Method: "SSH", User: "newuser", Pass: "p"})
	s.Control <- ControlMsg{Kind: ControlRestart}
	if !waitConnected(t, s.Out) {
		t.Fatal("нет второго CONNECTED (рестарт не переподключил)")
	}
	// переподключение создало второй поток; читаем его записанные байты.
	if st := s.getCur(); st != nil {
		if ps, ok := st.(*pipeStream); ok {
			got := ps.written(t)
			if !strings.Contains(got, "newuser") {
				t.Fatalf("после рестарта отправлено %q, хотим 'newuser' (правка не применена)", got)
			}
		}
	}
}
