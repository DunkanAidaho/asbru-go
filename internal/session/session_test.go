package session

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DunkanAidaho/asbru-go/internal/config"
	"github.com/DunkanAidaho/asbru-go/internal/method"
)

// pipeStream — фейковый канал связи с «сервером».
// Close идемпотентен: M24-тесты закрывают поток сами (имитация «remote end»),
// а runOnce всё равно вызывает Close в defer — повторный close паниковал.
type pipeStream struct {
	r chan []byte
	w chan []byte

	mu     sync.Mutex
	closed bool
}

func (p *pipeStream) Read(b []byte) (int, error) {
	chunk, ok := <-p.r
	if !ok {
		return 0, io.EOF
	}
	return copy(b, chunk), nil
}

func (p *pipeStream) Write(b []byte) (int, error) {
	select {
	case p.w <- append([]byte(nil), b...):
	default:
	}
	return len(b), nil
}

func (p *pipeStream) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.closed {
		p.closed = true
		close(p.r)
	}
	return nil
}

func (p *pipeStream) serverSend(s string) { p.r <- []byte(s) }

// written — накопленные байты, записанные клиентом.
func (p *pipeStream) written(t *testing.T) string {
	t.Helper()
	var got strings.Builder
	for {
		select {
		case b := <-p.w:
			got.Write(b)
		case <-time.After(40 * time.Millisecond):
			return got.String()
		}
	}
}

// panicStream — поток, падающий при чтении (проверка изоляции паник).
type panicStream struct{}

func (panicStream) Read([]byte) (int, error)    { panic("stream exploded") }
func (panicStream) Write(p []byte) (int, error) { return len(p), nil }
func (panicStream) Close() error                { return nil }

// Паника при чтении потока не роняет вызывающего — сессия сообщает EventFailed.
func TestSessionPanicIsolation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, err := New(ctx, BuildEnv{UUID: "u1"}, nil,
		WithStreamFactory(func() (io.ReadWriteCloser, error) { return panicStream{}, nil }))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-s.Out:
		if e.Kind != EventFailed {
			t.Fatalf("событие %+v, хотим EventFailed", e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("нет EventFailed после паники")
	}
}

// SSH-хендшейк на фейковом сервере: login as: → Password: → CONNECTED,
// и в поток ушли имя пользователя и пароль.
func TestSessionSSHHandshake(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := &pipeStream{r: make(chan []byte, 8), w: make(chan []byte, 8)}
	cfg := &config.Environment{Name: "fake", User: "root", Pass: "secret", Method: "SSH"}
	s, err := New(ctx, BuildEnv{UUID: "u1"}, cfg, WithStreamFactory(func() (io.ReadWriteCloser, error) {
		return stream, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	stream.serverSend("login as: ")
	stream.serverSend("Password: ")
	stream.serverSend("root@host:~# ")

	deadline := time.After(2 * time.Second)
seen:
	for {
		select {
		case e := <-s.Out:
			if e.Kind == EventStatusConnected {
				break seen
			}
		case <-deadline:
			t.Fatal("нет CONNECTED")
		}
	}

	w := stream.written(t)
	if !strings.Contains(w, "root") || !strings.Contains(w, "secret") {
		t.Fatalf("не ушли креды: %q", w)
	}
}

// M13-ревью H1: EventClosed при полном буфере и отменённом ctx всё равно
// доставляется (блокирующая доставка без ctx-побега).
func TestEmitClosedDeliveredWhenFull(t *testing.T) {
	s := &Session{out: make(chan Event, 1), in: make(chan []byte), ctrl: make(chan ControlMsg)}
	ctx, cancel := context.WithCancel(context.Background())
	s.ctx = ctx
	cancel() // ctx отменён — старый select дропал бы
	s.out <- Event{Kind: EventOutput, Text: "f"}

	got := make(chan Event, 1)
	go func() {
		for e := range s.out {
			if e.Kind == EventClosed {
				got <- e
			}
		}
	}()
	done := make(chan struct{})
	go func() {
		s.emit(Event{Kind: EventClosed, Text: "interrupted"})
		close(done)
	}()
	select {
	case e := <-got:
		if e.Kind != EventClosed {
			t.Fatalf("Closed потерян: %+v", e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Closed не доставлен")
	}
	close(s.out)
	<-done
}

// M13-ре-ревью: двойная эмиссия завершения при полном буфере и ушедшем
// reader НЕ виснет (ctx-побег доступен только в этом узком случае).
func TestEmitClosedNoHangWhenReaderGone(t *testing.T) {
	s := &Session{out: make(chan Event, 1), in: make(chan []byte), ctrl: make(chan ControlMsg)}
	ctx, cancel := context.WithCancel(context.Background())
	s.ctx = ctx
	cancel()
	s.out <- Event{Kind: EventOutput, Text: "x"}

	// первый Closed поглощается читателем (однократная эмиссия)
	go func() {
		<-s.out
		<-s.out
	}()
	s.emit(Event{Kind: EventClosed, Text: "first"})
	// reader ушёл: повторный emit обязан вернуться мгновенно (termOnce)
	done := make(chan struct{})
	go func() {
		s.emit(Event{Kind: EventClosed, Text: "second"})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("повторный emit завис")
	}
}

// Finding #1: auth type "manual" — легаси (asbru_conn:1993-1999) не вооружает
// НИ ОДНОГО expect-блока и сразу сообщает CONNECTED (пользователь вводит креды
// сам). Раньше builtinBlocks игнорировал manual и ждал промпты → GUI «connecting».
func TestManualAuthSkipsBuiltinsAndConnects(t *testing.T) {
	stream := &pipeStream{r: make(chan []byte, 8), w: make(chan []byte, 8)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := &config.Environment{Name: "man", Method: "SSH", User: "u", Pass: "p", AuthType: "manual",
		// даже с настроенными expect-блоками manual их не запускает (легаси !$MANUAL).
		Expect: []map[string]any{{"expect": "never", "send": "x", "active": 1}}}
	s, err := New(ctx, BuildEnv{UUID: "u1"}, cfg,
		WithStreamFactory(func() (io.ReadWriteCloser, error) { return stream, nil }))
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.After(2 * time.Second)
	for {
		select {
		case e := <-s.Out:
			if e.Kind == EventStatusConnected {
				if w := stream.written(t); w != "" {
					t.Fatalf("manual-сессия отправила данные автоматически: %q", w)
				}
				return
			}
		case <-deadline:
			t.Fatal("manual-сессия не подняла EventStatusConnected (ожидание промптов)")
		}
	}
}

// Finding #2: userpass с пустым User не должен терять command-промпт. Легаси
// (asbru_conn:1851+) вооружает command-prompt НЕЗАВИСИМО от $USER; иначе
// EventStatusConnected не наступает и GUI висит «connecting».
func TestEmptyUserKeepsCommandPromptAndConnects(t *testing.T) {
	stream := &pipeStream{r: make(chan []byte, 8), w: make(chan []byte, 8)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := &config.Environment{Name: "nou", Method: "SSH", User: "", Pass: "secret"}
	s, err := New(ctx, BuildEnv{UUID: "u1"}, cfg,
		WithStreamFactory(func() (io.ReadWriteCloser, error) { return stream, nil }))
	if err != nil {
		t.Fatal(err)
	}
	stream.serverSend("Password: ")
	stream.serverSend("root@host:~# ")

	deadline := time.After(2 * time.Second)
	for {
		select {
		case e := <-s.Out:
			if e.Kind == EventStatusConnected {
				w := stream.written(t)
				if !strings.Contains(w, "secret") {
					t.Fatalf("пароль не отправлен при пустом User: %q", w)
				}
				return
			}
		case <-deadline:
			t.Fatal("пустой User: не поднят CONNECTED (потерян command-промпт)")
		}
	}
}

// Finding #2 (пустые User и Pass): command-промпт тоже должен остаться
// вооружённым — легаси не ставит его в зависимость от $USER/$PASS.
func TestEmptyUserAndPassStillConnectsOnCommandPrompt(t *testing.T) {
	stream := &pipeStream{r: make(chan []byte, 8), w: make(chan []byte, 8)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := &config.Environment{Name: "nou", Method: "SSH"}
	s, err := New(ctx, BuildEnv{UUID: "u1"}, cfg,
		WithStreamFactory(func() (io.ReadWriteCloser, error) { return stream, nil }))
	if err != nil {
		t.Fatal(err)
	}
	stream.serverSend("root@host:~# ")
	deadline := time.After(2 * time.Second)
	for {
		select {
		case e, ok := <-s.Out:
			if !ok {
				t.Fatal("пустые User/Pass: сессия завершилась без CONNECTED (command-промпт потерян)")
			}
			if e.Kind == EventStatusConnected {
				return
			}
		case <-deadline:
			t.Fatal("пустые User/Pass: нет CONNECTED (command-промпт потерян)")
		}
	}
}

// M2: standalone RDP/VNC — «подключено» = процесс запущен (не userpass, не expect);
// иначе GUI висит в «подключение» (нет EventStatusConnected).
func TestIsNoHandshakeMethodRDPVNC(t *testing.T) {
	for _, m := range []string{"RDP (rdesktop)", "RDP (xfreerdp)", "VNC"} {
		if !isNoHandshakeMethod(m) {
			t.Errorf("isNoHandshakeMethod(%q)=false, want true (standalone RDP/VNC)", m)
		}
	}
}

// MINOR-6: VNCGeneric — пароль уходит в stdin процесса (легаси
// `echo "$PASS" | vncviewer ...`, asbru_conn:1284-1285). Бэкенды с файлом
// пароля (TigerVNC -PasswordFile / RealVNC UserName) пароль в stdin НЕ шлют.
func TestVNCGenericFeedsPasswordToStdin(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := &pipeStream{r: make(chan []byte, 8), w: make(chan []byte, 8)}
	cfg := &config.Environment{Name: "vnc", Pass: "secret", Method: "VNC"}
	s, err := New(ctx, BuildEnv{UUID: "u1"}, cfg,
		WithStreamFactory(func() (io.ReadWriteCloser, error) { return stream, nil }),
		WithVNCBackend(method.VNCGeneric))
	if err != nil {
		t.Fatal(err)
	}

	// дождаться, пока vncviewer (standalone VNC) уйдёт в «подключено», и
	// пароль окажется в stdin процесса.
	deadline := time.After(2 * time.Second)
connected:
	for {
		select {
		case e := <-s.Out:
			if e.Kind == EventStatusConnected {
				break connected
			}
		case <-deadline:
			t.Fatal("нет CONNECTED")
		}
	}
	w := stream.written(t)
	if !strings.Contains(w, "secret\n") {
		t.Fatalf("пароль не ушёл в stdin VNCGeneric: %q", w)
	}
}

// MINOR-6: бэкенды с файлом пароля (TigerVNC/RealVNC) пароль в stdin не шлют —
// иначе PasswordFile/UserName-механизм дублировался бы.
func TestVNCFileBackendsDontFeedStdin(t *testing.T) {
	for _, be := range []method.VNCBackend{method.VNCTigerVNC, method.VNCRealVNC} {
		ctx, cancel := context.WithCancel(context.Background())
		stream := &pipeStream{r: make(chan []byte, 8), w: make(chan []byte, 8)}
		cfg := &config.Environment{Name: "vnc", Pass: "secret", Method: "VNC"}
		s, err := New(ctx, BuildEnv{UUID: "u1"}, cfg,
			WithStreamFactory(func() (io.ReadWriteCloser, error) { return stream, nil }),
			WithVNCBackend(be))
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		deadline := time.After(2 * time.Second)
	cons:
		for {
			select {
			case e := <-s.Out:
				if e.Kind == EventStatusConnected {
					break cons
				}
			case <-deadline:
				cancel()
				t.Fatalf("%s: нет CONNECTED", be)
			}
		}
		w := stream.written(t)
		if strings.Contains(w, "secret") {
			t.Fatalf("%s: пароль не должен уходить в stdin: %q", be, w)
		}
		cancel()
	}
}
