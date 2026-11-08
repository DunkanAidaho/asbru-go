package session

// M30: периодический 'send string' (легаси PACTerminal.pm:664-678): раз в
// N секунд в поток отдаётся SendStringTxt (+"\n" при 'send string intro').

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DunkanAidaho/asbru-go/internal/config"
)

// sendStringSpec — базовая строка, флаг перевода строки и интервал; ok=false —
// фича выключена (SendStringActive!=1). Intro-строка — как легаси Perl-истинность.
func TestSendStringSpec(t *testing.T) {
	cases := []struct {
		name    string
		cfg     *config.Environment
		txt     string
		newline bool
		every   time.Duration
		ok      bool
	}{
		{"disabled", &config.Environment{SendStringActive: 0}, "", false, 0, false},
		{"active-no-intro", &config.Environment{SendStringActive: 1, SendStringTxt: "uptime", SendStringEvery: 30}, "uptime", false, 30 * time.Second, true},
		{"active-intro", &config.Environment{SendStringActive: 1, SendStringTxt: "uptime", SendStringIntro: "1", SendStringEvery: 10}, "uptime", true, 10 * time.Second, true},
		// guard: 0 секунд не должно давать тик на 0.
		{"zero-every-guard", &config.Environment{SendStringActive: 1, SendStringTxt: "x", SendStringEvery: 0}, "x", false, time.Second, true},
		{"intro-zero-string", &config.Environment{SendStringActive: 1, SendStringTxt: "who", SendStringIntro: "0", SendStringEvery: 5}, "who", false, 5 * time.Second, true},
		{"nil", nil, "", false, 0, false},
	}
	for _, c := range cases {
		txt, nl, every, ok := sendStringSpec(c.cfg)
		if ok != c.ok || txt != c.txt || nl != c.newline || every != c.every {
			t.Errorf("%s: got (%q,%v,%v,%v), want (%q,%v,%v,%v)", c.name, txt, nl, every, ok, c.txt, c.newline, c.every, c.ok)
		}
	}
}

// byteBufStream — тестовый поток, запоминающий записи (io.ReadWriteCloser).
type byteBufStream struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *byteBufStream) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}
func (b *byteBufStream) Read([]byte) (int, error) { return 0, io.EOF }
func (b *byteBufStream) Close() error             { return nil }
func (b *byteBufStream) String() string           { b.mu.Lock(); defer b.mu.Unlock(); return b.buf.String() }

// waitStreamHas — читает записи потока до появления подстроки (или таймаут).
func waitStreamHas(t *testing.T, st *pipeStream, sub string) bool {
	t.Helper()
	var got strings.Builder
	deadline := time.After(3 * time.Second)
	for {
		select {
		case b := <-st.w:
			got.Write(b)
			if strings.Contains(got.String(), sub) {
				return true
			}
		case <-deadline:
			return false
		}
	}
}

// Finding #9: правка 'send string' (edit→Restart) должна применяться. Раньше
// спецификация тикера захватывалась в run() при старте и не перечитывалась.
func TestRefreshConfigAppliesSendStringOnRestart(t *testing.T) {
	streams := make(chan *pipeStream, 4)
	factory := func() (io.ReadWriteCloser, error) {
		st := &pipeStream{r: make(chan []byte, 1), w: make(chan []byte, 64)}
		streams <- st
		return st, nil
	}
	cfg := &config.Environment{Name: "x", Method: "Generic Command",
		SendStringActive: 1, SendStringTxt: "old", SendStringEvery: 1}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, err := New(ctx, BuildEnv{UUID: "u1"}, cfg, WithStreamFactory(factory))
	if err != nil {
		t.Fatal(err)
	}
	first := <-streams
	if !waitStreamHas(t, first, "old") {
		t.Fatal("исходная send string не отправлена")
	}
	edited := *cfg
	edited.SendStringTxt = "new"
	s.RefreshConfig(&edited)
	s.Control <- ControlMsg{Kind: ControlRestart}
	var second *pipeStream
	select {
	case second = <-streams:
	case <-time.After(3 * time.Second):
		t.Fatal("рестарт не создал новый поток")
	}
	if !waitStreamHas(t, second, "new") {
		t.Fatal("после edit→Restart отправляется старая send string (правка не применена)")
	}
}

// M30-ревью #1/#2/#4: sendStringLoop шлёт ТОЛЬКО после CONNECTED, применяет
// подстановки (легаси _subst per tick) и останавливается по отмене контекста.
func TestSendStringLoopGateAndSubst(t *testing.T) {
	// ДО CONNECTED — не пишем (легаси PACTerminal.pm:666-674). Интервал —
	// минимум 1с (sendStringSpec), ждём больше одного тика.
	ctx0, cancel0 := context.WithCancel(context.Background())
	defer cancel0()
	st0 := &byteBufStream{}
	s0 := &Session{
		ctx:       ctx0,
		out:       make(chan Event, 16),
		cfg:       &config.Environment{SendStringActive: 1, SendStringTxt: "x", SendStringEvery: 1},
		cfgNotify: make(chan struct{}, 1),
	}
	s0.setCur(st0) // connected NOT set
	go s0.sendStringLoop()
	time.Sleep(1200 * time.Millisecond)
	if got := st0.String(); got != "" {
		t.Fatalf("до CONNECTED отправлено: %q", got)
	}
	cancel0()

	// После CONNECTED — пишем с подстановкой макросов.
	ctx, cancel := context.WithCancel(context.Background())
	st := &byteBufStream{}
	s := &Session{
		ctx: ctx,
		out: make(chan Event, 16),
		cfg: &config.Environment{SendStringActive: 1, SendStringTxt: "ping <IP> <TIMESTAMP>",
			SendStringIntro: "1", SendStringEvery: 1},
		cfgNotify: make(chan struct{}, 1),
	}
	s.setCur(st)
	s.connected.Store(true)
	s.opts.resolver = Resolver{Local: map[string]string{"IP": "10.0.0.1", "TIMESTAMP": "12345"}}
	go s.sendStringLoop()
	time.Sleep(1200 * time.Millisecond) // первый тик
	got := st.String()
	if !strings.Contains(got, "ping 10.0.0.1 12345\n") {
		t.Fatalf("send string = %q, want 'ping 10.0.0.1 12345\\n'", got)
	}
	// Отмена → горутина выходит (waitSend по ctx.Done).
	cancel()
	time.Sleep(100 * time.Millisecond)
	base := st.String()
	time.Sleep(1100 * time.Millisecond) // больше одного потенциального тика
	if after := st.String(); after != base {
		t.Fatalf("после cancel продолжает писать: %q", after)
	}
}
