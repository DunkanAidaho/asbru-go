package session

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DunkanAidaho/asbru-go/internal/config"
)

// Finding #7: пауза между реконнектами должна прерываться отменой ctx; после
// отмены новый runOnce (с preparedEnv/<CMD:>/spawn) НЕ запускается.
func TestReconnectBackoffCancellable(t *testing.T) {
	oldWait, oldRW := waitSleep, reconnectWait
	started := make(chan struct{}, 1)
	waitSleep = func(ctx context.Context, d time.Duration) bool {
		select {
		case started <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return false
	}
	reconnectWait = func(int) time.Duration { return time.Hour }
	defer func() { waitSleep, reconnectWait = oldWait, oldRW }()

	var calls atomic.Int32
	factory := func() (io.ReadWriteCloser, error) {
		calls.Add(1)
		st := &pipeStream{r: make(chan []byte, 1), w: make(chan []byte, 1)}
		st.Close() // удалённый конец закрыт → exitRemote → backoff
		return st, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, err := New(ctx, BuildEnv{UUID: "u1"},
		&config.Environment{Name: "x", Method: "SSH", AutoReconnect: 1},
		WithStreamFactory(factory), WithRetries(5))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("не вошли в паузу реконнекта")
	}
	cancel()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-s.Out:
			if !ok {
				goto ended
			}
		case <-deadline:
			t.Fatal("сессия не завершилась после отмены backoff")
		}
	}
ended:
	if n := calls.Load(); n != 1 {
		t.Fatalf("попыток %d, хотим 1 (после отмены runOnce не должен запускаться)", n)
	}
}

// Finding #7: отменённый ctx проверяется ДО preparedEnv (иначе <CMD:>
// исполняется после отмены) и ДО spawn.
func TestRunOnceAbortsBeforePrepareOnCancel(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "cmd-executed")
	env := &config.Environment{Name: "x", Method: "SSH", Options: "<CMD:touch " + marker + ">"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	factoryCalled := false
	s := &Session{
		ctx:  ctx,
		cfg:  env,
		out:  make(chan Event, 8),
		opts: sessionOptions{resolver: Resolver{Vars: map[string]string{}}},
	}
	s.opts.factory = func() (io.ReadWriteCloser, error) {
		factoryCalled = true
		return &pipeStream{}, nil
	}
	res := s.runOnce()
	if res.exit != exitHalt {
		t.Fatalf("exit = %v, хотим exitHalt", res.exit)
	}
	if factoryCalled {
		t.Fatal("openStream вызван после отмены ctx")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("<CMD:> исполнен после отмены ctx (marker=%v)", err)
	}
}

// statusStream — pipeStream с кодом выхода дочернего процесса (M24:
// exitStatus-контракт; чистый exit 0 не ретраится).
type statusStream struct {
	*pipeStream
	code int
}

func (s *statusStream) ExitStatus() (int, bool) { return s.code, true }

// M24: автоподключение (легаси PACTerminal.pm:1409-1424): N повторных
// попыток (прайм-ожидание), затем одно финальное Closed. Поток закрывается
// сразу — «remote end» на каждой попытке.
func TestAutoReconnectRetries(t *testing.T) {
	oldWait, oldSleep := waitSleep, reconnectWait
	reconnectWait = func(n int) time.Duration { return 0 } // тест: без ожидания
	sleeps := []time.Duration{}
	waitSleep = func(ctx context.Context, d time.Duration) bool { sleeps = append(sleeps, d); return true }
	defer func() { waitSleep, reconnectWait = oldWait, oldSleep }()

	calls := 0
	factory := func() (io.ReadWriteCloser, error) {
		calls++
		stream := &pipeStream{r: make(chan []byte, 1), w: make(chan []byte, 1)}
		stream.Close() // удалённый конец сразу закрыт (идемпотентно)
		return stream, nil
	}
	s, err := New(context.Background(), BuildEnv{UUID: "u1"},
		&config.Environment{Name: "x", Method: "SSH", User: "u", Pass: "p", AutoReconnect: 1},
		WithStreamFactory(factory), WithRetries(3))
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.After(3 * time.Second)
	for {
		select {
		case e := <-s.Out:
			t.Logf("event kind=%d text=%q", e.Kind, e.Text)
			if e.Kind == EventClosed {
				goto closed
			}
		case <-deadline:
			t.Fatal("нет Closed")
		}
	}
closed:
	if calls != 4 { // 1 старт + 3 ретрая
		t.Fatalf("попыток %d, хотим 4", calls)
	}
	if len(sleeps) != 3 {
		t.Fatalf("ожиданий %d, хотим 3", len(sleeps))
	}
}

// M24: disconnect пользователя НЕ переподключает.
func TestNoReconnectOnUserDisconnect(t *testing.T) {
	oldWait, oldSleep := waitSleep, reconnectWait
	reconnectWait = func(n int) time.Duration { return 0 }
	waitSleep = func(context.Context, time.Duration) bool { return true }
	defer func() { waitSleep, reconnectWait = oldWait, oldSleep }()

	stream := &pipeStream{r: make(chan []byte, 2), w: make(chan []byte, 2)}
	var calls atomic.Int32
	s, err := New(context.Background(), BuildEnv{UUID: "u1"},
		&config.Environment{Name: "x", Method: "SSH", AutoReconnect: 1},
		WithStreamFactory(func() (io.ReadWriteCloser, error) {
			calls.Add(1)
			return stream, nil
		}),
		WithRetries(5))
	if err != nil {
		t.Fatal(err)
	}
	s.Control <- ControlMsg{Kind: ControlDisconnect}
	// Первым может прийти доброкачественный Output — ждём именно EventClosed
	// (интент теста: «нет реконнекта», а не «Closed первое событие»).
	deadline := time.After(2 * time.Second)
	for {
		select {
		case e, ok := <-s.Out:
			if !ok {
				t.Fatal("канал закрыт без EventClosed после disconnect")
			}
			if e.Kind == EventClosed {
				goto closed
			}
		case <-deadline:
			t.Fatal("нет Closed после disconnect")
		}
	}
closed:
	// После Closed не должно быть событий; закрытие канала — штатный конец.
	for {
		select {
		case e, ok := <-s.Out:
			if !ok {
				goto done
			}
			t.Fatalf("лишнее событие после Closed: %v", e.Kind)
		case <-time.After(200 * time.Millisecond):
			// канал ещё не закрыт, но событий нет.
			goto done
		}
	}
done:
	// Реконнект означал бы повторный вызов фабрики (AutoReconnect=1). Ноль
	// вызовов допустим: disconnect мог прийти до первого spawn.
	if n := calls.Load(); n > 1 {
		t.Fatalf("фабрика вызвана %d раз, хотим ≤1 (реконнект при пользовательском disconnect)", n)
	}
}

// M24: чистый выход (ssh exit 0 — пользовательский `exit`) НЕ
// переподключается даже с autoreconnect: легаси asbru_conn:2258 шлёт
// ctrl('RESTART') только при $why ne 0.
func TestCleanExitNoReconnect(t *testing.T) {
	oldWait, oldSleep := waitSleep, reconnectWait
	reconnectWait = func(n int) time.Duration { return 0 }
	waitSleep = func(context.Context, time.Duration) bool { return true }
	defer func() { waitSleep, reconnectWait = oldWait, oldSleep }()

	calls := 0
	factory := func() (io.ReadWriteCloser, error) {
		calls++
		st := &statusStream{
			pipeStream: &pipeStream{r: make(chan []byte, 1), w: make(chan []byte, 1)},
			code:       0,
		}
		st.Close() // удалённый конец закрыт, процесс завершился кодом 0
		return st, nil
	}
	s, err := New(context.Background(), BuildEnv{UUID: "u1"},
		&config.Environment{Name: "x", Method: "SSH", AutoReconnect: 1},
		WithStreamFactory(factory), WithRetries(5))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-s.Out:
		if e.Kind != EventClosed {
			t.Fatalf("event: %v %q", e.Kind, e.Text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("нет Closed")
	}
	if calls != 1 {
		t.Fatalf("попыток %d, хотим 1 (чистый exit не ретраится)", calls)
	}
	select {
	case e, ok := <-s.Out:
		if ok { // закрытый канал даёт нулевое событие — штатный конец
			t.Fatalf("лишнее событие после Closed: %v", e.Kind)
		}
	default:
	}
}

// M24: успешное подключение обнуляет бюджет реконнектов (легаси:1456 —
// CONNECTED → _RECONNECTS=0): после CONNECTED + обрыва попытки считаются
// с нуля. Без сброса второй обрыв исчерпал бы WithRetries(2) и закрыл бы
// сессию — здесь доходит до третьей попытки (фабрика отвечает ошибкой).
func TestReconnectBudgetResetOnConnected(t *testing.T) {
	oldWait, oldSleep := waitSleep, reconnectWait
	reconnectWait = func(n int) time.Duration { return 0 }
	waitSleep = func(context.Context, time.Duration) bool { return true }
	defer func() { waitSleep, reconnectWait = oldWait, oldSleep }()

	var calls atomic.Int32
	conn := func() *pipeStream {
		st := &pipeStream{r: make(chan []byte, 16), w: make(chan []byte, 16)}
		st.serverSend("login as: ")
		st.serverSend("Password: ")
		st.serverSend("root@host:~# ")
		st.Close() // после рукопожатия удалённый конец закрыт
		return st
	}
	s, err := New(context.Background(), BuildEnv{UUID: "u1"},
		&config.Environment{Name: "x", Method: "SSH", User: "u", Pass: "p", AutoReconnect: 1},
		WithPrompts(Prompts{Username: "login as:", Password: "Password:", Command: DefaultCommandPrompt}),
		WithStreamFactory(func() (io.ReadWriteCloser, error) {
			if calls.Add(1) <= 2 {
				return conn(), nil
			}
			return nil, errors.New("conn refused")
		}),
		WithRetries(2))
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.After(3 * time.Second)
	for {
		select {
		case e := <-s.Out:
			t.Logf("event kind=%d text=%q", e.Kind, e.Text)
			if e.Kind == EventFailed {
				if calls.Load() != 3 {
					t.Fatalf("попыток %d, хотим 3 (бюджет не сброшен на CONNECTED)", calls.Load())
				}
				return
			}
			if e.Kind == EventClosed {
				t.Fatalf("Closed раньше исчерпания: %q (бюджет не сброшен?)", e.Text)
			}
		case <-deadline:
			t.Fatal("нет Failed (бюджет не исчерпан)")
		}
	}
}

// M24: прайм-прогрессия ожиданий (легаси: простые числа до 500).
func TestReconnectPrimeWait(t *testing.T) {
	want := []time.Duration{2, 3, 5, 7, 11, 13, 17}
	for i, sec := range want {
		if got := reconnectWait(i); got != sec*time.Second {
			t.Fatalf("wait(%d) = %v, want %v", i, got, sec*time.Second)
		}
	}
}
