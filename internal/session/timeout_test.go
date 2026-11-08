package session

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/DunkanAidaho/asbru-go/internal/config"
)

// M35 P0: таймаут рукопожатия применяет defaults 'timeout connect' (легаси
// asbru_conn:1403/1483 — CLOSE:TIMEOUT:«N seconds trying to connect»). На
// «чёрном» хосте (нет вывода) должен быть EventClosed, а не вечное «connecting».
func TestHandshakeTimeoutBlackHost(t *testing.T) {
	stream := &pipeStream{r: make(chan []byte, 16), w: make(chan []byte, 16)}
	cfg := &config.Environment{Name: "t", Method: "SSH", User: "u", Pass: "p"}
	// defaults: таймаут подключения 1с (реальные дефолты 40/60 — слишком долго).
	d := &config.Defaults{TimeOutConnect: 1}
	s, err := New(context.Background(), BuildEnv{UUID: "u1"}, cfg,
		WithDefaults(d),
		WithStreamFactory(func() (io.ReadWriteCloser, error) { return stream, nil }))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Control <- ControlMsg{Kind: ControlDisconnect} }()
	// ничего не шлём серверу — «чёрный» хост.
	deadline := time.After(3 * time.Second)
	for {
		select {
		case e := <-s.Out:
			t.Logf("event kind=%d text=%q", e.Kind, e.Text)
			if e.Kind == EventClosed {
				return // таймаут рукопожатия сработал — не висим
			}
		case <-deadline:
			t.Fatal("нет EventClosed: таймаут рукопожатия не применён (чёрный хост)")
		}
	}
}

// M35 P0: хелперы таймаутов — дефолты 40/60, когда defaults пуст/не задан.
func TestTimeoutHelpersDefaults(t *testing.T) {
	if got := (&Session{}).timeoutConnect(); got != 40 {
		t.Fatalf("timeoutConnect дефолт = %d, хотим 40", got)
	}
	if got := (&Session{}).timeoutCommand(); got != 60 {
		t.Fatalf("timeoutCommand дефолт = %d, хотим 60", got)
	}
	s := &Session{opts: sessionOptions{defaults: &config.Defaults{TimeOutConnect: 5, TimeOutCommand: 7}}}
	if s.timeoutConnect() != 5 || s.timeoutCommand() != 7 {
		t.Fatalf("настроенные таймауты: %d/%d, хотим 5/7", s.timeoutConnect(), s.timeoutCommand())
	}
}
