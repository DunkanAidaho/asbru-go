package session

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// EventKind — тип события сессии (аналог сообщений ctrl() легаси, но типизированный).
type EventKind int

const (
	EventOutput          EventKind = iota // данные терминала
	EventStatusConnected                  // рукопожатие завершено
	EventTitle                            // заголовок изменился
	EventExpectWaiting                    // блок ожидает совпадения
	EventExpectSending                    // отправка текста
	EventSpawned                          // команда запущена
	EventClosed                           // соединение закрыто
	EventFailed                           // ошибка/паника сессии
	EventAsk                              // вопрос пользователю (пароль/sudo/hostkey)
	EventRestart                          // перезапуск соединения (auto-fix known_hosts)
)

// MetaHidden — маркер скрытой отправки (пароли) в Meta события EventExpectSending.
const MetaHidden = "hidden"

// Meta события EventAsk — что именно запрашивается (легаси wEnterValue/askPassword).
const (
	MetaAskPassword = "password"
	MetaAskSudo     = "sudo"
	MetaAskHostKey  = "hostkey"
)

// Event — типизированное событие вместо строк легаси.
type Event struct {
	Kind EventKind
	Text string
	Meta any
}

// ExpectEntry — expect-блок из конфига (портируемая семантика YAML-блоков).
type ExpectEntry struct {
	Active   int
	Expect   string
	Send     string
	Hidden   int
	Return   int
	OnMatch  int     // -1 следующий, -2 connected, N>0 прыжок (1-based, как в YAML)
	OnFail   int     // -1 close, -2 connected, N>0 прыжок
	TimeOut  int     // секунд; -1 = без таймаута
	SendSlow int     // секунд на символ; 0 = мгновенно
	Stay     bool    // после отправки остаётся на месте (повторные промпты: пароль/sudo)
	Ask      AskFunc // интерактивная отправка: решается в момент совпадения
}

// AskOutcome — исход интерактивного запроса (легаси wEnterValue/askPassword).
type AskOutcome int

const (
	AskSent    AskOutcome = iota // отправлен текст ответа
	AskCancel                    // отмена пользователем → close
	AskReject                    // введённые данные отвергнуты → close с текстом
	AskRestart                   // перезапустить соединение (auto-fix known_hosts)
)

// AskFunc возвращает текст к отправке и исход; caps — группы захвата совпадения.
type AskFunc func(caps []string) (string, AskOutcome)

// ExpectEntryFromMap строит блок из items конфига (ключи: expect/send/hidden/
// return/on_match/on_fail/time_out/send slow/active) с легаси-дефолтами.
func ExpectEntryFromMap(m map[string]any) ExpectEntry {
	return ExpectEntry{
		Active:   num(m, "active", 1),
		Expect:   str(m, "expect"),
		Send:     str(m, "send"),
		Hidden:   num(m, "hidden", 0),
		Return:   num(m, "return", 1),
		OnMatch:  num(m, "on_match", -1),
		OnFail:   num(m, "on_fail", -1),
		TimeOut:  num(m, "time_out", -1),
		SendSlow: num(m, "send slow", 0),
	}
}

func num(m map[string]any, k string, def int) int {
	if v, ok := m[k]; ok {
		if n, ok := v.(int); ok {
			return n
		}
	}
	return def
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

// Clock — инжектируемые часы (для детерминированных тестов времени).
type Clock interface{ Now() time.Time }

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// EngineConfig — конфигурация движка.
type EngineConfig struct {
	Blocks   []ExpectEntry
	Clock    Clock
	Resolver Resolver
	Writer   io.Writer // куда уходит send
	Enter    string    // завершение send при Return=1 ("\n"; для c3270 "\r\f")
	// PhaseBreak — первые N блоков идут фазой списка (легаси-any-of внешнего
	// expect: любой из них может сработать первым), остальные — последовательно
	// (фаза пользовательских expect-блоков).
	PhaseBreak int
}

// Engine — Expect-движок: единый state machine для встроенных промптов
// и конфигурационных блоков.
//
// Finding #4: накопленный буфер ограничен. Сохраняем последние keepExpectTail
// байт перед обрезкой до maxExpectBuffer — этого достаточно для обычных
// промптов и не даёт неограниченной памяти/O(n²) на медленном потоке с
// TimeOut=-1. Компромисс: регэксп, которому для совпадения нужно более
// keepExpectTail байт НАКОПЛЕННОГО вывода (например, `(?s).*prompt` на
// гигантском «хвосте»), может не сработать; уже пришедшие совпадения
// обнаруживаются ДО обрезки (Feed: tryMatch → trimBuffer).
const (
	maxExpectBuffer = 1 << 20  // порог обрезки, байт
	keepExpectTail  = 64 << 10 // сколько последних байт сохраняем
)

type Engine struct {
	cfg    EngineConfig
	events chan Event

	idx       int
	buf       strings.Builder
	last      time.Time
	active    bool // работаем по блокам
	w         io.Writer
	pattern   string                 // актуальный (подставленный) паттерн текущего блока
	patternRe *regexp.Regexp         // nil → литеральное совпадение по pattern
	scan      map[int]*regexp.Regexp // кэш паттернов фазы списка
	restart   bool                   // запрошен перезапуск (auto-fix known_hosts)
}

// NewEngine собирает движок; подстановка/компиляция паттернов выполняется
// при активации блока (preparePattern) — один раз, а не на каждый чанк.
func NewEngine(cfg EngineConfig) *Engine {
	if cfg.Clock == nil {
		cfg.Clock = realClock{}
	}
	if cfg.Enter == "" {
		cfg.Enter = "\n"
	}
	return &Engine{cfg: cfg, events: make(chan Event, 256), w: cfg.Writer}
}

// Events — канал событий движка.
func (e *Engine) Events() <-chan Event { return e.events }

func (e *Engine) emit(ev Event) {
	select {
	case e.events <- ev:
	default:
	}
}

func (e *Engine) now() time.Time { return e.cfg.Clock.Now() }

// Done — блок движок исчерпан (connected/closed/failed) после вызванного шага.
func (e *Engine) Done() bool { return !e.active || e.idx >= len(e.cfg.Blocks) }

// RestartRequested — движок попросил переоткрыть поток (auto-fix known_hosts).
func (e *Engine) RestartRequested() bool { return e.restart }

// Run (обёртка) читает r и ведёт движок до терминального события;
// используется тестами и автономными прогонами. Если r — io.Closer,
// он закрывается на любом выходе (снимает утечку read-горутины — ревью M1).
func (e *Engine) Run(ctx context.Context, r io.Reader) {
	if c, ok := r.(io.Closer); ok {
		defer c.Close()
	}
	defer func() {
		if p := recover(); p != nil {
			e.emit(Event{Kind: EventFailed, Text: fmt.Sprintf("engine panic: %v", p)})
		}
	}()
	chunks := make(chan []byte)
	go func() {
		defer close(chunks)
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				chunks <- append([]byte(nil), buf[:n]...)
			}
			if err != nil {
				return
			}
		}
	}()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	e.startBlocks()
	for {
		select {
		case <-ctx.Done():
			e.emit(Event{Kind: EventClosed, Text: "interrupted"})
			return
		case c, ok := <-chunks:
			if !ok {
				e.emit(Event{Kind: EventClosed, Text: "Connection ended by remote peer"})
				return
			}
			e.Feed(c)
			if e.Done() && !e.active {
				return
			}
		case <-tick.C:
			e.Tick()
			if e.Done() && !e.active {
				return
			}
		}
	}
}

// Start активирует последовательность блоков (сессия кормит движок через Feed).
func (e *Engine) Start() { e.startBlocks() }

func (e *Engine) startBlocks() {
	if len(e.cfg.Blocks) == 0 {
		return
	}
	e.idx = 0
	e.active = true
	e.emitWait()
}

func (e *Engine) emitWait() {
	if e.idx < len(e.cfg.Blocks) {
		e.emit(Event{Kind: EventExpectWaiting, Text: e.cfg.Blocks[e.idx].Expect})
	}
	// M35: таймер блока стартует при его АКТИВАЦИИ. Иначе e.last == zero-time →
	// первый Tick (до первого вывода) считал now()-zero и сразу «таймаут».
	e.last = e.now()
	e.preparePattern() // подстановка/компиляция один раз на активацию блока (ревью R3)
}

// preparePattern — кэш подстановки паттерна <ASK:>/<CMD:>/<V:> на активацию:
// ASK вызывается один раз, CMD не перевыполняется на каждый чанк (ревью R3).
func (e *Engine) preparePattern() {
	if e.idx >= len(e.cfg.Blocks) {
		return
	}
	b := e.cfg.Blocks[e.idx]
	e.pattern = Substitute(b.Expect, e.cfg.Resolver)
	if re, err := regexp.Compile(e.pattern); err == nil {
		e.patternRe = re
	} else {
		e.patternRe = nil
	}
}

// Feed — синхронный разбор очередного чанка (используется сессией).
func (e *Engine) Feed(data []byte) {
	if len(data) == 0 {
		return
	}
	e.emit(Event{Kind: EventOutput, Text: string(data)})
	e.buf.Write(data)
	e.last = e.now()
	if !e.active {
		e.trimBuffer()
		return // чистый пассив: вывод уже отправлен
	}
	e.tryMatch()
	e.trimBuffer()
}

// trimBuffer — Finding #4: удерживает память ожидания в границах, сохраняя
// последние keepExpectTail байт (совпадение уже проверено tryMatch ДО обрезки).
func (e *Engine) trimBuffer() {
	n := e.buf.Len()
	if n <= maxExpectBuffer {
		return
	}
	s := e.buf.String()
	e.buf.Reset()
	e.buf.WriteString(s[n-keepExpectTail:])
}

// Tick — проверка таймаутов (вызывается периодически сессией/обёрткой).
func (e *Engine) Tick() {
	if !e.active {
		return
	}
	b := e.cfg.Blocks[e.idx]
	if b.TimeOut < 0 {
		return
	}
	if e.now().Sub(e.last) > time.Duration(b.TimeOut)*time.Second {
		e.triggerFail(b)
	}
}

// (хелперы осознанно в конце: Tick обновляется ниже)
func (e *Engine) triggerFail(b ExpectEntry) {
	switch b.OnFail {
	case -1:
		e.emit(Event{Kind: EventClosed, Text: "timeout"})
		e.active = false
	case -2:
		e.emit(Event{Kind: EventStatusConnected})
		e.active = false
	default:
		e.jump(b.OnFail)
		e.tryMatch() // накопленный вывод мог дождаться нового блока
	}
}

// capsFromIndex — группы захвата из FindStringSubmatchIndex (без повторного
// прохода regexp по буферу); несработавшая группа — "" (как FindStringSubmatch).
func capsFromIndex(text string, idx []int) []string {
	caps := make([]string, len(idx)/2)
	for i := range caps {
		if idx[2*i] >= 0 {
			caps[i] = text[idx[2*i]:idx[2*i+1]]
		}
	}
	return caps
}

// triggerMatchTail — совпадение с остатком чанка после матча (L5: два
// промпта в одном чанке; Stay-блок не должен терять хвост).
// Finding #3: и не-Stay блоки передают хвост следующему блоку — легаси
// пересканирует after-match аккумулятор (asbru_conn:1690-1700).
func (e *Engine) triggerMatchTail(b ExpectEntry, caps []string, tail string) {
	// ag-jmu: матч нулевой длины в позиции 0 даёт tail == исходному буферу.
	// Пересканирование тех же байт разбор не продвигает, поэтому Stay/self-jump
	// на таком матче уводили tryMatch в бесконечную рекурсию (stack overflow).
	noProgress := tail == e.buf.String()
	if b.Ask != nil {
		text, outcome := b.Ask(caps)
		switch outcome {
		case AskCancel:
			e.emit(Event{Kind: EventClosed, Text: "cancelled"})
			e.active = false
			return
		case AskReject:
			e.emit(Event{Kind: EventClosed, Text: text})
			e.active = false
			return
		case AskRestart:
			e.emit(Event{Kind: EventRestart})
			e.restart = true
			e.active = false
			return
		case AskSent:
			e.send(b, text)
		}
	} else {
		send := expandCaptures(b.Send, caps)
		e.send(b, send)
	}
	if b.Stay {
		// промпт потреблён; оставшаяся часть чанка — новый буфер и повторный
		// скан (ревью L5: «Password: root@host:~# » в одном чанке).
		// ag-jmu: при нулевом матче хвост совпадает с буфером — повторный скан
		// зациклится (Stay на `a*`), поэтому ждём следующего вывода.
		if !noProgress {
			e.buf.Reset()
			e.buf.WriteString(tail)
			e.tryMatch()
		}
		return
	}
	switch b.OnMatch {
	case -1:
		e.idx++
		if e.idx < len(e.cfg.Blocks) {
			// Finding #3: остаток чанка — буфер нового блока; сканируем его,
			// не дожидаясь следующего Read (легаси after_match).
			e.buf.Reset()
			e.buf.WriteString(tail)
			e.emitWait()
			if tail != "" {
				e.tryMatch()
			}
		} else {
			// исчерпание последовательности = подключение (легаси: ctrl CONNECTED
			// после блочного цикла; ревью R1 — иначе GUI вечно «connecting»)
			e.emit(Event{Kind: EventStatusConnected})
			e.active = false
		}
	case -2:
		e.emit(Event{Kind: EventStatusConnected})
		e.active = false
	default:
		e.jump(b.OnMatch)
		// Finding #3: после прыжка остаток чанка может содержать промпт
		// целевого блока — пересканируем его (легаси after_match).
		// ag-jmu: при нулевом матче хвост == буферу (self-jump на `a*` не
		// продвигает разбор) — пересканирование зациклится, ждём вывода.
		if e.active && !noProgress {
			e.buf.Reset()
			e.buf.WriteString(tail)
			if tail != "" {
				e.tryMatch()
			}
		}
	}
}

// jumpIndex — индекс в объединённом массиве для 1-based пользовательского номера
// блока n (M1): user-номера целятся в ПОЛЬЗОВАТЕЛЬСКИЕ блоки после PhaseBreak
// встроенных (иначе jump попадал во встроенную фазу). Вне границ → -1.
func jumpIndex(phaseBreak, n, blockLen int) int {
	if phaseBreak < 0 || n <= 0 {
		return -1
	}
	userCount := blockLen - phaseBreak
	if userCount < 0 || n > userCount {
		return -1
	}
	return phaseBreak + n - 1
}

func (e *Engine) jump(n int) {
	if idx := jumpIndex(e.cfg.PhaseBreak, n, len(e.cfg.Blocks)); idx >= 0 {
		e.idx = idx
		e.emitWait()
	} else {
		// деградированный конфиг (прыжок за пределы): легаси выпадает
		// из блочного цикла с CONNECTED — не оставляем GUI в «connecting»
		e.emit(Event{Kind: EventStatusConnected})
		e.active = false
	}
}

func (e *Engine) tryMatch() {
	if e.idx >= len(e.cfg.Blocks) {
		return
	}
	text := e.buf.String()
	if e.idx < e.cfg.PhaseBreak {
		// фаза списка: any-of легаси-внешнего expect — список остаётся
		// вооружённым целиком (ревью L4: sudo может промптить и после логина)
		for i := 0; i < e.cfg.PhaseBreak; i++ {
			b := e.cfg.Blocks[i]
			if b.Expect == "" {
				continue
			}
			if re := e.scanPat(i); re != nil {
				if m, mm := re.FindStringSubmatch(text), re.FindStringSubmatchIndex(text); m != nil {
					e.idx = i
					e.triggerMatchTail(b, m, text[mm[1]:])
					return
				}
			} else if j := strings.Index(text, b.Expect); j >= 0 {
				e.idx = i
				e.triggerMatchTail(b, nil, text[j+len(b.Expect):])
				return
			}
		}
		return
	}
	b := e.cfg.Blocks[e.idx]
	if b.Expect == "" {
		return
	}
	if e.patternRe != nil {
		// Finding #3: индекс совпадения даёт и хвост чанка после матча —
		// отдаём его следующему блоку (легаси after_match).
		if mm := e.patternRe.FindStringSubmatchIndex(text); mm != nil {
			e.triggerMatchTail(b, capsFromIndex(text, mm), text[mm[1]:])
		}
	} else if j := strings.Index(text, e.pattern); j >= 0 {
		e.triggerMatchTail(b, nil, text[j+len(e.pattern):])
	}
}

// scanPat — паттерн блока фазы списка (кэш компиляции per-block).
func (e *Engine) scanPat(i int) *regexp.Regexp {
	if e.scan == nil {
		e.scan = map[int]*regexp.Regexp{}
	}
	if re, ok := e.scan[i]; ok {
		return re
	}
	p := Substitute(e.cfg.Blocks[i].Expect, e.cfg.Resolver)
	var re *regexp.Regexp
	if r, err := regexp.Compile(p); err == nil {
		re = r
	}
	e.scan[i] = re
	return re
}

func (e *Engine) send(b ExpectEntry, text string) {
	text = Substitute(text, e.cfg.Resolver)
	meta := any(nil)
	if b.Hidden == 1 {
		meta = MetaHidden
	}
	e.emit(Event{Kind: EventExpectSending, Text: text, Meta: meta})
	if b.Return == 1 {
		text += e.cfg.Enter
	}
	if b.SendSlow > 0 {
		step := time.Duration(b.SendSlow) * time.Second
		for i := range text {
			base := e.now()
			for e.now().Sub(base) < step {
				time.Sleep(5 * time.Millisecond)
			}
			if _, err := e.w.Write([]byte{text[i]}); err != nil {
				return
			}
		}
		return
	}
	if _, err := e.w.Write([]byte(text)); err != nil {
		return
	}
}

// expandCaptures: $0 (всё совпадение), $1…$99 из групп захвата regexp.
// \d{1,2} жадно — $10 не сливается с "0" (ревьюва M2).
// capRe — скомпилирован один раз (бэклог-#Cleanup: раньше regexp.Compile на каждый Send).
var capRe = regexp.MustCompile(`\$(\d{1,2})`)

func expandCaptures(send string, caps []string) string {
	if len(caps) == 0 {
		return send
	}
	return capRe.ReplaceAllStringFunc(send, func(m string) string {
		n, err := strconv.Atoi(m[1:])
		if err != nil || n >= len(caps) {
			return m
		}
		return caps[n]
	})
}
