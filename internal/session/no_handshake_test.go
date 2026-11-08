package session

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/DunkanAidaho/asbru-go/internal/config"
)

// M35-ревью MINOR-1: no-handshake метод (Generic Command), процесс которого
// завершается НЕнулевым кодом (exitRemote), НЕ должен бесконечно реконнектиться.
// Раньше (энтерпрайз-ревью MAJOR-1) ветка eng==nil ставила sawConnected → run()
// сбрасывал retries=0 на каждом runOnce → вечный цикл. Теперь бюджет применяется
// честно: 1 старт + WithRetries(3) ретрая, затем Closed. При этом no-handshake
// метод ОБЯЗАН поднять EventStatusConnected (иначе «зомби» — GUI «connecting»).
func TestNoHandshakeReconnectBudgetBinds(t *testing.T) {
	oldWait, oldSleep := waitSleep, reconnectWait
	reconnectWait = func(n int) time.Duration { return 0 }
	waitSleep = func(context.Context, time.Duration) bool { return true }
	defer func() { waitSleep, reconnectWait = oldWait, oldSleep }()

	calls := 0
	factory := func() (io.ReadWriteCloser, error) {
		calls++
		st := &statusStream{
			pipeStream: &pipeStream{r: make(chan []byte, 1), w: make(chan []byte, 1)},
			code:       1,
		}
		st.Close() // удалённый конец закрыт, процесс завершился кодом != 0
		return st, nil
	}
	s, err := New(context.Background(), BuildEnv{UUID: "u1"},
		&config.Environment{Name: "x", Method: "Generic Command", AutoReconnect: 1},
		WithStreamFactory(factory), WithRetries(3))
	if err != nil {
		t.Fatal(err)
	}
	// NIT-1/2: гармонизируем с TestUserpassMethodNotZombie — отменяем ctx,
	// чтобы forwardInput/forwardControl не висели до завершения процесса.
	defer func() { s.Control <- ControlMsg{Kind: ControlDisconnect} }()
	sawConnected := false
	deadline := time.After(3 * time.Second)
	for {
		select {
		case e := <-s.Out:
			t.Logf("event kind=%d text=%q", e.Kind, e.Text)
			if e.Kind == EventStatusConnected {
				sawConnected = true
			}
			if e.Kind == EventClosed {
				goto closed
			}
		case <-deadline:
			t.Fatal("нет Closed")
		}
	}
closed:
	if !sawConnected {
		t.Fatal("no-handshake метод не поднял EventStatusConnected (зомби)")
	}
	// 1 старт + 3 ретрая с WithRetries(3); если бы бюджет сбрасывался на
	// CONNECTED — реконнект был бы бесконечным и тест завис бы до deadline.
	if calls != 4 {
		t.Fatalf("попыток %d, хотим 4 (1 старт + 3 ретрая; бюджет НЕ сброшен на CONNECTED)", calls)
	}
}

// M35-ревью MINOR-2: MOSH/WebDAV/Serial (remote-tty) ранее НЕ были в
// isUserpassMethod → builtinBlocks возвращал nil → eng==nil → не было
// EventStatusConnected → GUI зависал «connecting» (зомби). Теперь они проходят
// рукопожатие (login/password/command-prompt) и поднимают CONNECTED.
func TestUserpassMethodNotZombie(t *testing.T) {
	cases := []string{"MOSH", "WebDAV", "Serial (remote-tty)"}
	for _, m := range cases {
		t.Run(m, func(t *testing.T) {
			stream := &pipeStream{r: make(chan []byte, 16), w: make(chan []byte, 16)}
			stream.serverSend("login as: ")
			stream.serverSend("Password: ")
			stream.serverSend("root@host:~# ")
			s, err := New(context.Background(), BuildEnv{UUID: "u1"},
				&config.Environment{Name: "x", Method: m, User: "u", Pass: "p"},
				WithPrompts(Prompts{Username: "login as:", Password: "Password:", Command: DefaultCommandPrompt}),
				WithStreamFactory(func() (io.ReadWriteCloser, error) { return stream, nil }))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { s.Control <- ControlMsg{Kind: ControlDisconnect} }()
			deadline := time.After(3 * time.Second)
			for {
				select {
				case e := <-s.Out:
					t.Logf("event kind=%d text=%q", e.Kind, e.Text)
					if e.Kind == EventStatusConnected {
						return // зомби устранён: CONNECTED поднят
					}
				case <-deadline:
					t.Fatalf("%s: не поднят EventStatusConnected (зомби)", m)
				}
			}
		})
	}
}
