package session

import (
	"bytes"
	"context"
	"io"
	"sync"
	"testing"
	"time"
)

// fakeClock — инжектируемые часы: движение управляет сам тест.
// Мьютекс обязателен: Now читается из горутины движка (ревью: race под -race).
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{t: time.Unix(0, 0)} }
func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}
func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// chanReader — блокируемый источник: engine читает только когда идёт чанк.
type chanReader chan []byte

func (c chanReader) Read(p []byte) (int, error) {
	b, ok := <-c
	if !ok {
		return 0, io.EOF
	}
	return copy(p, b), nil
}

func drain(t *testing.T, ch <-chan Event) []Event {
	t.Helper()
	var evs []Event
	for {
		select {
		case e := <-ch:
			evs = append(evs, e)
			if len(evs) > 200 {
				t.Fatal("слишком много событий")
			}
			if e.Kind == EventClosed || e.Kind == EventStatusConnected || e.Kind == EventFailed {
				return evs
			}
		case <-time.After(2 * time.Second):
			return evs
		}
	}
}

func hasEvent(evs []Event, k EventKind, text string) bool {
	for _, e := range evs {
		if e.Kind == k && (text == "" || e.Text == text) {
			return true
		}
	}
	return false
}

func block(expect, send string) ExpectEntry {
	return ExpectEntry{Active: 1, Expect: expect, Send: send, Return: 1, OnMatch: -1, OnFail: -1, TimeOut: -1}
}

func newPipe(t *testing.T) (chanReader, chan []byte) {
	t.Helper()
	ch := make(chan []byte)
	return chanReader(ch), ch
}

// Последовательность: login → пароль → CONNECTED (on_match -2 у Password).
// M35: фантомный таймаут — блок с TimeOut≥0 НЕ должен падать сразу после
// активации, ДО первого вывода (e.last был zero-time).
func TestEngineNoPhantomTimeoutBeforeFeed(t *testing.T) {
	clk := newFakeClock()
	blocks := []ExpectEntry{
		{Active: 1, Expect: "never-matches", OnFail: -1, TimeOut: 30},
	}
	var out bytes.Buffer
	eng := NewEngine(EngineConfig{Blocks: blocks, Clock: clk, Writer: &out})
	eng.Start()
	eng.Tick() // сразу после активации, вывода не было → не должно быть фантомного таймаута
	if !eng.active {
		t.Fatalf("фантомный таймаут до вывода: active=%v", eng.active)
	}
}

func TestEngineBlockSequence(t *testing.T) {
	src, w := newPipe(t)
	blocks := []ExpectEntry{
		block("login as:", "root"),
		func() ExpectEntry { b := block("Password:", "secret"); b.OnMatch = -2; return b }(),
	}
	var out bytes.Buffer
	eng := NewEngine(EngineConfig{Blocks: blocks, Clock: newFakeClock(), Writer: &out})
	ch := eng.Events()
	go func() { eng.Run(context.Background(), src) }()
	w <- []byte("login as: root\n")
	w <- []byte("Password: ")

	evs := drain(t, ch)
	if got := out.String(); got != "root\nsecret\n" {
		t.Fatalf("sends = %q", got)
	}
	if !hasEvent(evs, EventExpectWaiting, "login as:") || !hasEvent(evs, EventExpectSending, "root") {
		t.Fatalf("events: %#v", evs)
	}
	if !hasEvent(evs, EventStatusConnected, "") {
		t.Fatalf("нет CONNECTED: %#v", evs)
	}
}

// time_out: блок без совпадения; on_fail -1 → Close, -2 → Connected, N → jump (1-based из YAML).
func TestEngineOnFailSemantics(t *testing.T) {
	cases := []struct {
		name     string
		onFail   int
		wantKind EventKind
	}{
		{"close", -1, EventClosed},
		{"finish", -2, EventStatusConnected},
		{"jump", 2, EventStatusConnected},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			clk := newFakeClock()
			src, w := newPipe(t)
			blocks := []ExpectEntry{
				{Active: 1, Expect: "never-matches", OnFail: c.onFail, TimeOut: 1}, // 1 «фейковая» секунда
				func() ExpectEntry { b := block("final prompt:", "go"); b.OnMatch = -2; return b }(),
			}
			var out bytes.Buffer
			eng := NewEngine(EngineConfig{Blocks: blocks, Clock: clk, Writer: &out})
			ch := eng.Events()
			go func() { eng.Run(context.Background(), src) }()
			// несовпадающие данные (для jump — заранее и финальный prompt),
			// затем проталкиваем фейковые часы вперёд
			w <- []byte("some output ")
			if c.name == "jump" {
				w <- []byte("final prompt: ")
			}
			go func() {
				for i := 0; i < 60; i++ {
					time.Sleep(5 * time.Millisecond)
					clk.advance(50 * time.Millisecond)
				}
			}()
			evs := drain(t, ch)
			var final Event
			found := false
			for _, e := range evs {
				if e.Kind == EventClosed || e.Kind == EventStatusConnected {
					if found {
						t.Fatalf("двойное завершение: %#v", evs)
					}
					final, found = e, true
				}
			}
			if !found || final.Kind != c.wantKind {
				t.Fatalf("final = %+v, want %v (events %#v)", final, c.wantKind, evs)
			}
		})
	}
}

func engineFor(t *testing.T, b ExpectEntry, w io.Writer, clk Clock) (*Engine, chanReader, chan []byte) {
	t.Helper()
	src, wc := newPipe(t)
	eng := NewEngine(EngineConfig{Blocks: []ExpectEntry{b}, Clock: clk, Writer: w})
	return eng, src, wc
}

// hidden send — помечается MetaHidden.
func TestEngineHiddenSend(t *testing.T) {
	b := block("Password:", "s3cret")
	b.Hidden = 1
	eng, src, w := engineFor(t, b, io.Discard, newFakeClock())
	ch := eng.Events()
	go func() { eng.Run(context.Background(), src) }()
	w <- []byte("Password: ")
	close(w)
	for _, e := range drain(t, ch) {
		if e.Kind == EventExpectSending && e.Meta == MetaHidden {
			return
		}
	}
	t.Fatal("hidden send не помечен")
}

// return=0: send без перевода строки.
func TestEngineNoReturn(t *testing.T) {
	b := block("cmd>", "ls")
	b.Return = 0
	var out bytes.Buffer
	eng, src, w := engineFor(t, b, &out, newFakeClock())
	ch := eng.Events()
	go func() { eng.Run(context.Background(), src) }()
	w <- []byte("cmd>")
	close(w)
	_ = drain(t, ch)
	if out.String() != "ls" {
		t.Fatalf("send без return: %q", out.String())
	}
}

// send_slow: строки пишутся посимвольно (шаг clock), завершение за конечное время.
func TestEngineSendSlow(t *testing.T) {
	clk := newFakeClock()
	b := block("cmd>", "ab")
	b.SendSlow = 1
	var out bytes.Buffer
	eng, src, w := engineFor(t, b, &out, clk)
	ch := eng.Events()
	go func() {
		go func() {
			for i := 0; i < 40; i++ {
				time.Sleep(5 * time.Millisecond)
				clk.advance(time.Second)
			}
		}()
		eng.Run(context.Background(), src)
	}()
	w <- []byte("cmd>")
	close(w)
	_ = drain(t, ch)
	if out.String() != "ab\n" { // с return=1 перевод строки пишется так же посимвольно
		t.Fatalf("slow send: %q", out.String())
	}
}

// Подстановки $1…$9 (и $0, $10) в send из групп захвата.
func TestEngineCaptureSubstitution(t *testing.T) {
	b := block(`(user|root):`, "$1 found")
	var out bytes.Buffer
	eng, src, w := engineFor(t, b, &out, newFakeClock())
	ch := eng.Events()
	go func() { eng.Run(context.Background(), src) }()
	w <- []byte("root:")
	close(w)
	_ = drain(t, ch)
	if out.String() != "root found\n" {
		t.Fatalf("capture: %q", out.String())
	}
}

// $0 — полное совпадение; $10 — оба разряда, без слияния с "0" (ревью M2).
func TestEngineCaptureTwoDigit(t *testing.T) {
	b := block(`(a)(b)(c)(d)(e)(f)(g)(h)(i)(j)`, "$0|$10")
	var out bytes.Buffer
	eng, src, w := engineFor(t, b, &out, newFakeClock())
	ch := eng.Events()
	go func() { eng.Run(context.Background(), src) }()
	w <- []byte("abcdefghij")
	close(w)
	_ = drain(t, ch)
	if out.String() != "abcdefghij|j\n" {
		t.Fatalf("$0/$10: %q", out.String())
	}
}

// Паттерн блока тоже проходит подстановки <V:…> (ревью M3).
func TestEnginePatternSubstituted(t *testing.T) {
	b := block("<V:user>:", "ok")
	var out bytes.Buffer
	eng := NewEngine(EngineConfig{Blocks: []ExpectEntry{b}, Clock: newFakeClock(), Writer: &out,
		Resolver: Resolver{Vars: map[string]string{"user": "admin"}}})
	ch := eng.Events()
	src, w := newPipe(t)
	go func() { eng.Run(context.Background(), src) }()
	w <- []byte("admin: ")
	close(w)
	_ = drain(t, ch)
	if out.String() != "ok\n" {
		t.Fatalf("pattern subst: %q", out.String())
	}
}

// Enter для c3270 (ревью M3: \r\f вместо \n).
func TestEngineEnterFor3270(t *testing.T) {
	b := block("cmd>", "ls")
	var out bytes.Buffer
	eng := NewEngine(EngineConfig{Blocks: []ExpectEntry{b}, Clock: newFakeClock(), Writer: &out, Enter: "\r\f"})
	ch := eng.Events()
	src, w := newPipe(t)
	go func() { eng.Run(context.Background(), src) }()
	w <- []byte("cmd>")
	close(w)
	_ = drain(t, ch)
	if out.String() != "ls\r\f" {
		t.Fatalf("3270 enter: %q", out.String())
	}
}

// EOF → EventClosed.
func TestEngineEOFClosed(t *testing.T) {
	eng, src, w := engineFor(t, block("x", "y"), io.Discard, newFakeClock())
	ch := eng.Events()
	go func() { eng.Run(context.Background(), src) }()
	w <- []byte("nothing")
	close(w)
	if !hasEvent(drain(t, ch), EventClosed, "") {
		t.Fatal("EOF не превратился в EventClosed")
	}
}

// Исчерпание блока с дефолтным on_match -1 → CONNECTED (ревью R1).
func TestEngineLastDefaultBlockConnects(t *testing.T) {
	src, w := newPipe(t)
	var out bytes.Buffer
	eng := NewEngine(EngineConfig{Blocks: []ExpectEntry{
		{Active: 1, Expect: ">", Send: "cmd", OnMatch: -1, OnFail: -1, TimeOut: -1},
	}, Clock: newFakeClock(), Writer: &out})
	ch := eng.Events()
	go func() { eng.Run(context.Background(), src) }()
	w <- []byte(">")
	close(w)
	if !hasEvent(drain(t, ch), EventStatusConnected, "") {
		t.Fatal("после последнего блока ожидался CONNECTED")
	}
}

// Прыжок за пределы блоков — деградированный конфиг: не тихая пассивка (остаток ревью #1).
func TestEngineJumpOutOfRangeConnects(t *testing.T) {
	src, w := newPipe(t)
	var out bytes.Buffer
	eng := NewEngine(EngineConfig{Blocks: []ExpectEntry{
		{Active: 1, Expect: ">", Send: "x", OnMatch: 9, OnFail: -1, TimeOut: -1},
	}, Clock: newFakeClock(), Writer: &out})
	ch := eng.Events()
	go func() { eng.Run(context.Background(), src) }()
	w <- []byte(">")
	close(w)
	if !hasEvent(drain(t, ch), EventStatusConnected, "") {
		t.Fatal("ожидался CONNECTED при невалидном прыжке")
	}
}

// Finding #3: не-Stay блок не должен терять хвост чанка после совпадения.
// Легаси (asbru_conn:1690-1700) пересканирует after-match аккумулятор: два
// промпта в одном чтении («login as: Password: ») обязаны обработаться оба.
func TestEngineRescansTailAfterNonStayMatch(t *testing.T) {
	src, w := newPipe(t)
	blocks := []ExpectEntry{
		block("login as:", "root"),
		func() ExpectEntry { b := block("Password:", "secret"); b.OnMatch = -2; return b }(),
	}
	var out bytes.Buffer
	eng := NewEngine(EngineConfig{Blocks: blocks, Clock: newFakeClock(), Writer: &out})
	ch := eng.Events()
	go func() { eng.Run(context.Background(), src) }()
	w <- []byte("login as: Password: ")
	evs := drain(t, ch)
	if got := out.String(); got != "root\nsecret\n" {
		t.Fatalf("sends = %q, хотим root+secret (хвост чанка потерян)", got)
	}
	if !hasEvent(evs, EventStatusConnected, "") {
		t.Fatalf("нет CONNECTED: %#v", evs)
	}
}

// Finding #3 (фаза списка): builtin-промпты в одном чанке — login переходит к
// password (Stay), затем password-хвост — к command-промпту.
func TestEnginePhaseListRescansTail(t *testing.T) {
	src, w := newPipe(t)
	blocks := []ExpectEntry{
		block("login as:", "root"),
		func() ExpectEntry { b := block("Password:", "secret"); b.Stay = true; return b }(),
		func() ExpectEntry { b := block(`root@host:~# `, ""); b.OnMatch = -2; b.Return = 0; return b }(),
	}
	var out bytes.Buffer
	eng := NewEngine(EngineConfig{Blocks: blocks, Clock: newFakeClock(), Writer: &out, PhaseBreak: 3})
	ch := eng.Events()
	go func() { eng.Run(context.Background(), src) }()
	w <- []byte("login as: Password: root@host:~# ")
	evs := drain(t, ch)
	if got := out.String(); got != "root\nsecret\n" {
		t.Fatalf("sends = %q, хотим root+secret (хвост списка потерян)", got)
	}
	if !hasEvent(evs, EventStatusConnected, "") {
		t.Fatalf("нет CONNECTED: %#v", evs)
	}
}

// Finding #4: буфер ожидания не должен расти безгранично при TimeOut=-1 и
// потоке вывода без совпадений (иначе память + O(n²) regex на каждый чанк).
// Хвост сохраняется, поэтому «отложенное» совпадение после флуда находится.
func TestEngineBufferBoundedOnFlood(t *testing.T) {
	_, _ = newPipe(t)
	var out bytes.Buffer
	eng := NewEngine(EngineConfig{Blocks: []ExpectEntry{block("never-matches", "x")},
		Clock: newFakeClock(), Writer: &out})
	_ = eng.Events() // события никто не читает: emit не блокирует (drop)
	eng.Start()
	chunk := bytes.Repeat([]byte("a"), 1024)
	for i := 0; i < 2048; i++ { // 2 MiB вывода без совпадений
		eng.Feed(chunk)
	}
	if n := len(eng.buf.String()); n > 1<<20 {
		t.Fatalf("буфер вырос до %d байт (>1 MiB) — нет ограничения", n)
	}
	// «отложенный» промпт после флуда должен матчиться (хвост сохранён).
	eng.Feed([]byte("never-matches"))
	if eng.active {
		t.Fatal("совпадение после флуда потеряно (буфер обрезан небезопасно)")
	}
}

// M9: движение работает по фазам — первые PhaseBreak блоков сканируются
// списком (легаси-any-of внешнего expect), остальные идут последовательно.
func TestEnginePhaseBreakScan(t *testing.T) {
	src, w := newPipe(t)
	blocks := []ExpectEntry{
		block("sudo: password", "s3"), // builtin, никогда не матчится в фикстуре
		func() ExpectEntry { b := block("login as:", "root"); b.OnMatch = -2; return b }(),
	}
	var out bytes.Buffer
	eng := NewEngine(EngineConfig{Blocks: blocks, Clock: newFakeClock(), Writer: &out, PhaseBreak: 2})
	ch := eng.Events()
	go func() { eng.Run(context.Background(), src) }()
	w <- []byte("login as: ") // второй блок должен сработать из любого места списка

	evs := drain(t, ch)
	if !hasEvent(evs, EventStatusConnected, "") {
		t.Fatalf("нет CONNECTED: %#v", evs)
	}
}

// M9: Stay-блок после отправки остаётся на месте (повторный промпт вызывает
// повторную отправку), затем уходит дальше по совпадению следующего блока.
func TestEngineRetryStay(t *testing.T) {
	src, w := newPipe(t)
	blocks := []ExpectEntry{
		func() ExpectEntry {
			b := block("Password:", "wrong")
			b.Stay = true
			return b
		}(),
		func() ExpectEntry { b := block("root@host:~# ", ""); b.OnMatch = -2; b.Return = 0; return b }(),
	}
	var out bytes.Buffer
	eng := NewEngine(EngineConfig{Blocks: blocks, Clock: newFakeClock(), Writer: &out, PhaseBreak: 2})
	_ = out
	ch := eng.Events()
	go func() { eng.Run(context.Background(), src) }()
	w <- []byte("Password: ")
	w <- []byte("Password: ")
	w <- []byte("root@host:~# ")

	evs := drain(t, ch)
	if got := out.String(); got != "wrong\nwrong\n" {
		t.Fatalf("sends = %q, хотим два wrong", got)
	}
	if !hasEvent(evs, EventStatusConnected, "") {
		t.Fatalf("нет CONNECTED: %#v", evs)
	}
}

// M9: исходы интерактивного Ask — Sent/Cancel/Reject/Restart.
func TestEngineAskOutcomes(t *testing.T) {
	cases := []struct {
		name     string
		outcome  AskOutcome
		text     string
		wantKind EventKind
		wantText string
	}{
		{"sent", AskSent, "yes", EventExpectSending, "yes"},
		{"cancel", AskCancel, "", EventClosed, "cancelled"},
		{"reject", AskReject, "rejected by config", EventClosed, "rejected by config"},
		{"restart", AskRestart, "", EventRestart, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src, w := newPipe(t)
			blk := block("pw:", "")
			blk.Ask = func([]string) (string, AskOutcome) { return c.text, c.outcome }
			var out bytes.Buffer
			eng := NewEngine(EngineConfig{Blocks: []ExpectEntry{blk}, Clock: newFakeClock(), Writer: &out, PhaseBreak: 1})
			ch := eng.Events()
			go func() { eng.Run(context.Background(), src) }()
			w <- []byte("pw: ")
			evs := drain(t, ch)
			if !hasEvent(evs, c.wantKind, c.wantText) {
				t.Fatalf("нет %v(%q): %#v", c.wantKind, c.wantText, evs)
			}
			if c.outcome == AskSent && out.String() != c.text+"\n" {
				t.Fatalf("write = %q, хотим %q", out.String(), c.text+"\n")
			}
		})
	}
}

// M1: jump(n) в конфиге с PhaseBreak>0 должен целиться в ПОЛЬЗОВАТЕЛЬСКИЙ блок
// (смещение = PhaseBreak), а не во встроенный. n — 1-based (легаси asbru_conn).
func TestJumpIndex(t *testing.T) {
	cases := []struct{ phase, n, blockLen, want int }{
		{0, 2, 3, 1},  // без builtin'ов: n-1
		{1, 1, 3, 1},  // 1 builtin: U1 → idx1
		{1, 2, 3, 2},  // 1 builtin: U2 → idx2
		{2, 1, 4, 2},  // 2 builtin'а: U1 → idx2
		{2, 2, 4, 3},  // 2 builtin'а: U2 → idx3
		{1, 3, 3, -1}, // за границей user-блоков (userCount=2)
		{0, 0, 2, -1}, // n<=0
		{2, 2, 3, -1}, // userCount=1, n=2 → вне
	}
	for _, c := range cases {
		if got := jumpIndex(c.phase, c.n, c.blockLen); got != c.want {
			t.Fatalf("jumpIndex(%d,%d,%d)=%d want %d", c.phase, c.n, c.blockLen, got, c.want)
		}
	}
}

// ag-jmu: regex нулевой длины (a* на "xyz" матчит "" в позиции 0) + on_match,
// прыгающий на тот же блок, уводили tryMatch в бесконечную рекурсию: tail ==
// всему буферу, пересканирование не продвигает разбор → stack overflow.
// Теперь пересканирование без прогресса пропускается — один матч на Feed.
func TestEngineZeroLengthSelfJumpNoRecursion(t *testing.T) {
	var out bytes.Buffer
	blocks := []ExpectEntry{
		{Active: 1, Expect: "a*", Send: "x", Return: 0, OnMatch: 1, OnFail: -1, TimeOut: -1},
	}
	eng := NewEngine(EngineConfig{Blocks: blocks, Clock: newFakeClock(), Writer: &out})
	eng.Start()
	eng.Feed([]byte("xyz")) // до фикса: stack overflow
	if got := out.String(); got != "x" {
		t.Fatalf("send = %q, хотим один x (пересканирование без прогресса)", got)
	}
	if !eng.active {
		t.Fatal("self-jump не должен завершать движок")
	}
	if got := eng.buf.String(); got != "xyz" {
		t.Fatalf("буфер = %q, хотим исходный чанк xyz", got)
	}
}

// ag-jmu: тот же класс отказа для Stay-блока — после нулевого матча хвост
// равен буферу, повторный скан зацикливается. Отправка должна быть однократной.
func TestEngineZeroLengthStayNoRecursion(t *testing.T) {
	var out bytes.Buffer
	blocks := []ExpectEntry{
		{Active: 1, Expect: "a*", Send: "y", Return: 0, Stay: true, OnMatch: -1, OnFail: -1, TimeOut: -1},
	}
	eng := NewEngine(EngineConfig{Blocks: blocks, Clock: newFakeClock(), Writer: &out})
	eng.Start()
	eng.Feed([]byte("xyz")) // до фикса: stack overflow
	if got := out.String(); got != "y" {
		t.Fatalf("send = %q, хотим один y", got)
	}
	if got := eng.buf.String(); got != "xyz" {
		t.Fatalf("буфер = %q, хотим исходный чанк xyz", got)
	}
}
