package session

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DunkanAidaho/asbru-go/internal/config"
	"github.com/DunkanAidaho/asbru-go/internal/keepass"
	"github.com/DunkanAidaho/asbru-go/internal/method"
	"github.com/DunkanAidaho/asbru-go/internal/stats"
	"golang.org/x/sys/unix"
)

// ControlKind — тип управляющего сообщения в Control.
type ControlKind int

const (
	ControlResize ControlKind = iota
	ControlRunCmd
	ControlDisconnect
	ControlAnswer  // ответ на EventAsk (Text; Cancel=true — отмена)
	ControlRestart // M36: пользовательский перезапуск соединения (закрыть и переподключить)
)

// ControlMsg — управляющее сообщение (внутрипроцессное, вместо сигналов легаси).
type ControlMsg struct {
	Kind   ControlKind
	Cols   int
	Rows   int
	Cmd    string
	Text   string
	Cancel bool
}

// Session — соединение: одна горутина, каналы вместо IPC.
//
//	Out     — события для терминала/статуса
//	In      — ввод пользователя (нажатия)
//	Control — resize, запуск команды, отключение
type Session struct {
	Out     <-chan Event
	In      chan<- []byte
	Control chan<- ControlMsg

	ctx   context.Context
	cfg   *config.Environment
	env   BuildEnv
	opts  sessionOptions
	out   chan Event
	in    chan []byte
	ctrl  chan ControlMsg
	ans   chan ControlMsg
	known string // known_hosts-путь (перезапись при auto-fix)

	curMu sync.RWMutex
	cur   io.ReadWriteCloser // текущий поток (переставляется при рестарте)

	// cfgMu — M36-reвью MAJOR: s.cfg можно перечитать (edit→restart применяет
	// правки). Защищает указатель cfg; чтения через cfg(); запись — RefreshConfig.
	cfgMu sync.RWMutex

	// Finding #9: opts.resolver пересобирается RefreshConfig (правки variables/
	// local-макросов применяются на restart); cfgNotify будит send-string тикер.
	optsMu    sync.RWMutex
	cfgNotify chan struct{}

	lastCol, lastRow int // последний известный размер (M1: рестарт без ресайза)
	lastSet          bool

	passAttempts int  // счётчики попыток до/после подтверждений
	passAutoSent bool // реальный пароль уже отправлен автоматически (дальше — вопрос)
	sudoAttempts int
	cancel       context.CancelFunc

	// connected — M30-ревью #1: send-string шлётся ТОЛЬКО после CONNECTED
	// (легаси PACTerminal.pm:666-674); сброс на каждом реконнекте.
	connected atomic.Bool

	// restartReq — M36: пользовательский перезапуск (ControlRestart):
	// run() после runOnce безусловно переподключает (свежий бюджет).
	restartReq atomic.Bool

	// startupDone — М31: 'startup script' запускается один раз на подключение.
	startupDone bool

	// termOnce — завершение эмитится ровно один раз: первый Closed/Failed
	// блокирующе отдаётся заведомо живому читателю; повторная эмиссия
	// (движковое + из ctx.Done) — no-op, без выбора «дроп или висящий
	// send» (M13-ре-ревью: полный буфер + ушедший reader вешали runOnce).
	termOnce sync.Once
}

// setCur/getCur — поток для фуд-горутин: при EventRestart горутины НЕ
// пересоздаются (ревью M1 — старые на закрытом потоке теряли ввод).
func (s *Session) setCur(st io.ReadWriteCloser) {
	s.curMu.Lock()
	s.cur = st
	s.curMu.Unlock()
}

func (s *Session) getCur() io.ReadWriteCloser {
	s.curMu.RLock()
	defer s.curMu.RUnlock()
	return s.cur
}

// envConfig — конфигурационное окружение сессии под cfgMu (M36-ревью MAJOR:
// edit→restart перечитывает cfg; без мьютекса — data race с RefreshConfig).
func (s *Session) envConfig() *config.Environment {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return s.cfg
}

// resolver — текущий резолвер подстановок под optsMu (Finding #9: RefreshConfig
// может его заменить параллельно с run-горутиной).
func (s *Session) resolver() Resolver {
	s.optsMu.RLock()
	defer s.optsMu.RUnlock()
	return s.opts.resolver
}

// RefreshConfig — M36-ревью MAJOR: применить правки соединения к работающей
// сессии (после «Отредактировать сессию»), чтобы «Перезапустить»/реконнект
// использовали обновлённые значения (иначе restart переподключал бы старый
// host/port/password). cfg не мутируется на месте: заменяем указатель атомарно.
//
// Finding #9: пересобираем env-зависимую часть резолвера (variables/LOCAL_VARS)
// и будим send-string тикер, иначе правки 'send string'/переменных не
// применялись после edit→Restart (спецификация тикера захватывалась в run()).
// ag-vl1: fillFromEnv публикует НОВЫЕ карты Local/Vars (старые не мутируются) —
// их читают горутины, держащие копию Resolver, уже вне optsMu.
func (s *Session) RefreshConfig(env *config.Environment) {
	s.cfgMu.Lock()
	s.cfg = env
	s.cfgMu.Unlock()
	s.optsMu.Lock()
	if r := s.opts.resolver; r.active() {
		r.fillFromEnv(s.env, env)
		s.opts.resolver = r
	}
	s.optsMu.Unlock()
	if s.cfgNotify != nil {
		select {
		case s.cfgNotify <- struct{}{}:
		default: // уже есть необработанное уведомление
		}
	}
}

type sessionOptions struct {
	factory       streamFactory
	resolver      Resolver
	clock         Clock
	vnc           method.VNCBackend
	prompts       Prompts
	promptsSet    bool // WithPrompts передан: значения точные (без fill — R1)
	known         string
	kp            keepass.FieldResolver         // KeePass-маски (<field|uuid>)
	retries       int                           // M24: max попыток автоподключения
	startupRunner func(context.Context, string) // M31: раннер 'startup script' (тесты)
	stats         *stats.Store                  // M32: PACStatistics (OnStart/OnStop)
	defaults      *config.Defaults              // M31 #4: defaults-fallback (logs/startup)
}

type streamFactory func() (io.ReadWriteCloser, error)

type Option func(*sessionOptions)

// WithStreamFactory — подмена канала связи (тесты; по умолчанию PTY).
func WithStreamFactory(f func() (io.ReadWriteCloser, error)) Option {
	return func(o *sessionOptions) { o.factory = f }
}

// WithSubstitute — переменные для макросов.
func WithSubstitute(r Resolver) Option {
	return func(o *sessionOptions) { o.resolver = r }
}

// WithClock — тестовые часы.
func WithClock(c Clock) Option {
	return func(o *sessionOptions) { o.clock = c }
}

// WithStartupRunner — инъекция раннера 'startup script' при подключении (тесты).
func WithStartupRunner(f func(context.Context, string)) Option {
	return func(o *sessionOptions) { o.startupRunner = f }
}

// WithStats — PACStatistics: OnStart при подключении, OnStop при завершении (M32).
func WithStats(st *stats.Store) Option {
	return func(o *sessionOptions) { o.stats = st }
}

// WithDefaults — defaults для defaults-fallback (M31 #4): save session logs/startup.
func WithDefaults(d *config.Defaults) Option {
	return func(o *sessionOptions) { o.defaults = d }
}

// WithVNCBackend — явный VNC-бэкенд (иначе Detected через vncviewer --help).
func WithVNCBackend(b method.VNCBackend) Option {
	return func(o *sessionOptions) { o.vnc = b }
}

// WithPrompts — промпты подключения (дефолт — легаси-значения PACUtils).
func WithPrompts(p Prompts) Option {
	return func(o *sessionOptions) { o.prompts = p; o.promptsSet = true }
}

// WithKnownHostsFile — путь known_hosts (тесты; дефолт ~/.ssh/known_hosts).
func WithKnownHostsFile(path string) Option {
	return func(o *sessionOptions) { o.known = path }
}

// WithKeepassResolver — KeePass-маски <field|uuid> в полях команды (M10b).
func WithKeepassResolver(kp keepass.FieldResolver) Option {
	return func(o *sessionOptions) { o.kp = kp }
}

// New создаёт сессию соединения для env+cfg и запускает её горутиной.
func New(ctx context.Context, env BuildEnv, cfg *config.Environment, opts ...Option) (*Session, error) {
	var o sessionOptions
	for _, opt := range opts {
		opt(&o)
	}
	sctx, cancel := context.WithCancel(ctx)
	s := &Session{
		ctx:    sctx,
		cfg:    cfg,
		env:    env,
		opts:   o,
		out:    make(chan Event, 256),
		in:     make(chan []byte, 32),
		ctrl:   make(chan ControlMsg, 8),
		ans:    make(chan ControlMsg, 4),
		known:  defaultKnownHosts(),
		cancel: cancel,
		// Finding #9: тикер send-string перечитывает cfg по уведомлению.
		cfgNotify: make(chan struct{}, 1),
	}
	if o.known != "" {
		s.known = o.known
	}
	if !o.promptsSet {
		s.opts.prompts = o.prompts.fill() // иначе значения точные (R1)
	} else {
		s.opts.prompts = o.prompts
	}
	s.Out = s.out
	s.In = s.in
	s.Control = s.ctrl
	go s.run()
	return s, nil
}

func (s *Session) emit(ev Event) {
	switch ev.Kind {
	case EventAsk, EventRestart:
		// Вопрос/рестарт не дропаются, но и не вешаемся навечно (L3).
		select {
		case s.out <- ev:
		case <-s.ctx.Done():
		}
	case EventClosed, EventFailed:
		// Завершение: блокирующая доставка ровно одного события (H1 — без
		// ctx-побега читатель всегда жив: он потребляет САМОЕ ПЕРВОЕ
		// завершение; повторные — no-op через termOnce).
		s.termOnce.Do(func() {
			s.out <- ev
		})
	default:
		select {
		case s.out <- ev:
		default:
		}
	}
}

// runExit — чем закончился один проход подключения.
type runExit int

const (
	exitHalt    runExit = iota // без переподключения (ошибка подготовки/ctx)
	exitRemote                 // удалённый конец закрыл поток — можно ретраить
	exitRestart                // EventRestart (auto-fix known_hosts)
)

// runOnceResult — итог одного прохода + флаги реконнекта.
type runOnceResult struct {
	exit      runExit
	connected bool // EventStatusConnected видели (легаси: _RECONNECTS=0 на CONNECTED)
}

// run — жизненный цикл: чтение → двигатель → пассивная передача.
// EventRestart (auto-fix known_hosts) переоткрывает поток — до 3 попыток;
// M24: exitRemote + autoreconnect → прайм-ожидание и новая попытка.
func (s *Session) run() {
	defer func() {
		if p := recover(); p != nil {
			s.emit(Event{Kind: EventFailed, Text: fmt.Sprintf("session panic: %v", p)})
		}
		s.cancel() // MINOR-7: естественное завершение — остановить input/control-горутины
		close(s.out)
	}()
	go s.forwardInput()
	go s.forwardControl()
	// M30: периодический 'send string' — горутина-тикер живёт между реконнектами.
	// Finding #9: спецификация перечитывается на каждом тике (cfgNotify), поэтому
	// правки применяются после edit→Restart; запускаем всегда, чтобы включение
	// фичи правкой тоже сработало.
	go s.sendStringLoop()
	retries := 0
	for i := 0; ; i++ {
		res := s.runOnce()
		// M36: пользовательский перезапуск (ControlRestart) — безусловно
		// переподключаемся, свежий бюджет, независимо от exit/autoreconnect.
		if s.restartReq.CompareAndSwap(true, false) {
			i = 0
			continue
		}
		if res.connected {
			// легаси PACTerminal.pm:1456 — CONNECTED обнуляет _RECONNECTS:
			// каждая успешная сессия получает свежий бюджет реконнектов.
			retries = 0
		}
		switch res.exit {
		case exitRestart:
			if i >= 2 {
				s.emit(Event{Kind: EventClosed, Text: "too many restarts"})
				return
			}
			continue
		case exitRemote:
			if c := s.envConfig(); c != nil && c.AutoReconnect == 1 && retries < s.opts.retries {
				// прайм-пауза (легаси:1421); Finding #7: отмена ctx прерывает
				// ожидание, после чего новый runOnce уже не запускается.
				if !waitSleep(s.ctx, reconnectWait(retries)) {
					s.emit(Event{Kind: EventClosed, Text: "interrupted"})
					return
				}
				retries++
				i = 0 // бюджет auto-fix-рестартов свежий после реконнекта
				continue
			}
			s.emit(Event{Kind: EventClosed, Text: "Connection ended by remote peer"})
			return
		default:
			return
		}
	}
}

// exitStatus — поток, способный сообщить код завершения дочернего процесса
// (M24: легаси ретраит только при $why ne 0 — asbru_conn:2258).
type exitStatus interface {
	ExitStatus() (int, bool)
}

// exitCodeOf — код выхода дочернего процесса; ok=false: неизвестен/не
// применимо (тестовые фабрики, пассивные потоки).
func exitCodeOf(st io.ReadWriteCloser) (int, bool) {
	if es, ok := st.(exitStatus); ok {
		return es.ExitStatus()
	}
	return 0, false
}

// runOnce — одно подключение; возвращает чем закончился проход
// (M24: exitRemote отдельно от exitHalt — первый ретраится по
// autoreconnect, второй нет).
func (s *Session) runOnce() runOnceResult {
	defer s.recoverRunOnce()
	// Finding #7: отменённый ctx не должен доходить до preparedEnv (<CMD:>)
	// и spawn — проверяем до любой подготовительной работы. EventClosed всё
	// равно эмитим: отключение пользователя обязано завершать сессию событием,
	// как и ветка ctx.Done в select ниже (иначе возможен «тихий» close канала).
	if s.ctx.Err() != nil {
		s.emit(Event{Kind: EventClosed, Text: "interrupted"})
		return runOnceResult{exit: exitHalt}
	}
	s.connected.Store(false) // M30-ревью #1: свежий проход — ещё не подключён
	s.startupDone = false    // M31: 'startup script' — заново на новом подключении
	// M35: счётчики попыток пароля — заново НА КАЖДОМ подключении (реконнект
	// шлёт сохранённый пароль автоматически, а не спрашивает).
	s.passAttempts = 0
	s.passAutoSent = false
	s.sudoAttempts = 0 // M35-ревью: иначе на реконнекте 1-й sudo-промпт сразу «rejected»
	// M32: PACStatistics — конец сессии (no-op, если OnStart не было).
	defer func() {
		if s.opts.stats != nil {
			s.opts.stats.OnStop(s.env.UUID, time.Now())
		}
	}()
	pe, perr := s.preparedEnv() // один раз на runOnce (R3: <CMD:> не перевыполняется)
	if perr != nil {
		s.emit(Event{Kind: EventFailed, Text: perr.Error()})
		return runOnceResult{exit: exitHalt}
	}
	// Finding #7: подготовка (в т.ч. <CMD:>) могла занять время — повторная
	// проверка ctx перед spawn, чтобы не запускать процесс после отмены.
	// EventClosed эмитим и здесь: иначе отмена во время preparedEnv закрывала
	// канал без терминального события (флейк TestNoReconnectOnUserDisconnect).
	if s.ctx.Err() != nil {
		s.emit(Event{Kind: EventClosed, Text: "interrupted"})
		return runOnceResult{exit: exitHalt}
	}
	stream, err := s.openStream(pe)
	if err != nil {
		s.emit(Event{Kind: EventFailed, Text: err.Error()})
		return runOnceResult{exit: exitHalt}
	}
	// M35-ревью: канал окончания этого runOnce — reader выйдет по нему даже на
	// exitRestart (ctx НЕ done, stream.Close() не разблокирует send в канал).
	runDone := make(chan struct{})
	defer func() {
		close(runDone)
		s.curMu.Lock() // закрытие под мьютексом: resizeTo не гоняет Fd (H1)
		s.cur = nil
		stream.Close()
		s.curMu.Unlock()
	}()
	s.setCur(stream)
	s.reapplyWinsize() // свежий pty после рестарта без ресайза (M1)

	// MINOR-6: standalone VNCGeneric — пароль в stdin процесса (легаси
	// `echo "$PASS" | vncviewer ...`, asbru_conn:1284-1285). Для бэкендов с
	// файлом пароля (TigerVNC -PasswordFile / RealVNC UserName) — не шлём
	// (иначе дублировался бы файловый механизм).
	if p := vncStdinPassword(pe, s.vncBackend()); len(p) > 0 {
		feedPasswordStdin(stream, p)
	}

	// M31: 'save session logs' — файл лога сессии (create + append) на этот проход.
	logW, logOK := s.sessionLogWriter(pe)
	if logOK {
		defer logW.Close()
	}

	eng := s.newEngine(stream, pe)
	sawConnected := false // EventStatusConnected: сброс бюджета реконнектов (M24)
	if eng != nil {
		eng.Start()
	} else if c := s.envConfig(); c != nil && (isNoHandshakeMethod(c.Method) || isManualAuth(c)) {
		// M35: методы без рукопожатия и без expect-блоков (Local Shell / Generic
		// Command / Serial cu / 3270) — процесс УЖЕ запущен, «подключено» сразу.
		// Finding #1: manual-auth тоже не ждёт промптов (легаси !$MANUAL) —
		// CONNECTED сразу, рукопожатие делает пользователь вручную.
		// markConnected: флаг connected + startup-script + stats.OnStart (как в
		// drainEvents) — иначе send-string/startup/stats не срабатывали (MAJOR-2).
		s.markConnected()
		s.emit(Event{Kind: EventStatusConnected})
		// НЕ ставим sawConnected (сброс бюджета реконнектов): для no-handshake
		// «процесс запущен» ≠ «соединение стабильно»; иначе падающая Generic
		// Command (exit!==0) сбрасывала ретраи в 0 на каждом runOnce → бесконечный
		// реконнект (энтерпрайз-ревью MAJOR-1). Бюджет применяется честно.
		// sawConnected  // НЕ СТАВИМ — см. выше
	}
	chunks := make(chan []byte, 16)
	go func() {
		defer close(chunks)
		defer func() {
			if p := recover(); p != nil {
				s.emit(Event{Kind: EventFailed, Text: fmt.Sprintf("read panic: %v", p)})
			}
		}()
		buf := make([]byte, 8192)
		for {
			n, err := stream.Read(buf)
			if n > 0 {
				// M35: ctx-aware отправка (ctx.Done) + канал runDone (на restart,
				// когда ctx НЕ done) — иначе reader блокировался на полном канале.
				select {
				case chunks <- append([]byte(nil), buf[:n]...):
				case <-s.ctx.Done():
					return
				case <-runDone:
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-s.ctx.Done():
			s.emit(Event{Kind: EventClosed, Text: "interrupted"})
			return runOnceResult{exit: exitHalt}
		case c, ok := <-chunks:
			if !ok {
				// M24: чистый выход (ssh exit 0 — пользовательский exit) НЕ
				// ретраится: легаси asbru_conn:2258 ctrl('RESTART') только при
				// $why ne 0; неизвестный статус (тестовые фабрики) — ретраим.
				// M3: дождаться cmd.Wait() (done) — иначе ExitStatus ещё не установлен
				// и «чистый exit 0» уходит в exitRemote (ложный реконнект, гонка).
				if ws, ok := stream.(interface{ Done() <-chan struct{} }); ok {
					<-ws.Done()
				}
				if code, have := exitCodeOf(stream); have && code == 0 {
					s.emit(Event{Kind: EventClosed, Text: "Connection ended by remote peer"})
					return runOnceResult{exit: exitHalt, connected: sawConnected}
				}
				return runOnceResult{exit: exitRemote, connected: sawConnected}
			}
			// M31: лог сессии — пишем каждый чанк вывода (как терминальный feed).
			if logW != nil {
				_, _ = logW.Write(c)
			}
			if eng != nil {
				eng.Feed(c)
				if s.drainEvents(eng) {
					sawConnected = true
				}
				if eng.RestartRequested() {
					// auto-fix known_hosts: закрываем поток и открываем заново
					return runOnceResult{exit: exitRestart, connected: sawConnected}
				}
				if eng.Done() {
					if !sawConnected {
						// M35 P0: движок завершился БЕЗ CONNECTED (таймаут/брак
						// рукопожатия) — закрываем сессию, иначе «чёрный хост»
						// висит до закрытия потока (нет EventClosed) (asbru_conn:
						// CLOSE:TIMEOUT после expect).
						return runOnceResult{exit: exitHalt}
					}
					eng = nil // пассивный режим: только вывод
				}
			} else {
				s.emit(Event{Kind: EventOutput, Text: string(c)})
			}
		case <-tick.C:
			if eng != nil {
				eng.Tick()
				if s.drainEvents(eng) {
					sawConnected = true
				}
				// M35-ревью MINOR-2: зеркалим Feed-ветку — рестарт (hostkey
				// auto-fix) проверяется ДО Done, иначе exitHalt завершал бы
				// сессию вместо переоткрытия потока.
				if eng.RestartRequested() {
					return runOnceResult{exit: exitRestart, connected: sawConnected}
				}
				if eng.Done() {
					if !sawConnected {
						// таймаут рукопожатия (см. выше) — без CONNECTED.
						return runOnceResult{exit: exitHalt}
					}
					eng = nil
				}
			}
		}
	}
}

// recoverRunOnce: аварийная печать в run>runOnce не роняет процесс (ревью H4).
func (s *Session) recoverRunOnce() {
	if p := recover(); p != nil {
		s.emit(Event{Kind: EventFailed, Text: fmt.Sprintf("session panic: %v", p)})
	}
}

// drainEvents — пересылка событий движка в Out; true = было
// EventStatusConnected (M24: сброс счётчика реконнектов, легаси:1456).
func (s *Session) drainEvents(eng *Engine) bool {
	saw := false
	for {
		select {
		case ev := <-eng.Events():
			if ev.Kind == EventStatusConnected {
				saw = true
				s.markConnected()
			}
			s.emit(ev)
		default:
			return saw
		}
	}
}

// markConnected — побочные эффекты установки подключения (M35-ревью MAJOR-2):
// флаг connected (send-string), startup-скрипт (один раз), stats.OnStart.
// Вызывается из drainEvents (по EventStatusConnected) и из ветки eng==nil
// для no-handshake методов (Local Shell/Generic/cu/3270).
func (s *Session) markConnected() {
	s.connected.Store(true) // M30-ревью #1: send-string после логина
	if !s.startupDone {     // M31: 'startup script' — один раз на подключение
		s.startupDone = true
		s.runStartup()
	}
	// M32: PACStatistics — начало сессии (per connection attempt).
	if s.opts.stats != nil {
		s.opts.stats.OnStart(s.env.UUID, time.Now())
	}
}

func (s *Session) openStream(pe *config.Environment) (io.ReadWriteCloser, error) {
	if s.opts.factory != nil {
		return s.opts.factory()
	}
	return s.spawnPTY(pe)
}

// forwardInput/forwardControl — фоновая запись из каналов в поток; паника
// в stream.Write не роняет процесс (ревью H4), а выход по ctx завершает
// горутины после отключения (ревью M4).
func (s *Session) forwardInput() {
	defer func() {
		if p := recover(); p != nil {
			s.emit(Event{Kind: EventFailed, Text: fmt.Sprintf("input panic: %v", p)})
		}
	}()
	for {
		select {
		case <-s.ctx.Done():
			return
		case data := <-s.in:
			if st := s.getCur(); st != nil {
				_, _ = st.Write(data) // ошибки при рестарте-закрытии не теряем ввод (M1)
			}
		}
	}
}

func (s *Session) forwardControl() {
	defer func() {
		if p := recover(); p != nil {
			s.emit(Event{Kind: EventFailed, Text: fmt.Sprintf("control panic: %v", p)})
		}
	}()
	for {
		select {
		case <-s.ctx.Done():
			return
		case msg := <-s.ctrl:
			switch msg.Kind {
			case ControlAnswer:
				select {
				case s.ans <- msg:
				case <-s.ctx.Done():
				}
			case ControlRunCmd:
				if st := s.getCur(); st != nil { // рестарт-окно: не падаем (R1)
					_, _ = st.Write(append([]byte(msg.Cmd), '\n'))
				}
			case ControlDisconnect:
				s.cancel()
			case ControlRestart:
				// M36: закрыть текущий поток (runOnce выйдет) + пометить рестарт —
				// run() переподключит безусловно (см. заголовок цикла).
				s.restartReq.Store(true)
				if st := s.getCur(); st != nil {
					_ = st.Close()
				}
			case ControlResize:
				s.resizeTo(msg.Cols, msg.Rows)
			}
		}
	}
}

// newEngine — единый state machine: фаза списка builtin-промптов (sudo/
// hostkey/press-any-key/remote-host/login/password/command), затем
// последовательная фаза пользовательских expect-блоков.
func (s *Session) newEngine(w io.Writer, pe *config.Environment) *Engine {
	if pe == nil {
		return nil
	}
	// Finding #1: manual-auth — легаси пропускает ВСЕ expect-блоки и сразу
	// эмитит CONNECTED (asbru_conn:1680,1993-1999); пользователь вводит сам.
	if isManualAuth(pe) {
		return nil
	}
	builtins, pb := s.builtinBlocks(pe)
	configured := expectBlocksFromConfig(pe.Expect)
	if len(builtins) == 0 && len(configured) == 0 {
		return nil
	}
	blocks := append(builtins, configured...)
	cfg := EngineConfig{
		Blocks:     blocks,
		Clock:      s.opts.clock,
		Resolver:   s.resolver(),
		Writer:     w,
		PhaseBreak: pb,
	}
	if strings.Contains(s.envConfig().Method, "3270") {
		cfg.Enter = "\r\f" // легаси: для c3270 Enter — \r\f
	}
	return NewEngine(cfg)
}

// builtinBlocks — встроенные промпты рукопожатия (легаси-внешний expect):
// any-of по порядку появления промптов; самые поздние — command prompt.
func (s *Session) builtinBlocks(pe *config.Environment) ([]ExpectEntry, int) {
	if pe == nil || !isUserpassMethod(pe.Method) || isManualAuth(pe) {
		return nil, 0
	}
	// pe — подготовленное окружение: маски/подстановки разрешены (H1)
	p := s.opts.prompts
	tc := s.timeoutConnect()   // M35 P0: connect-фаза (login/password/sudo/...)
	tcmd := s.timeoutCommand() // M35 P0: command-промпт
	builtins := make([]ExpectEntry, 0, 7)
	if p.SudoPrompt != "" {
		b := s.sudoBlock(p)
		b.TimeOut = tc
		builtins = append(builtins, b)
	}
	if p.RemoteHostChanged != "" {
		b := s.remoteHostBlock(p)
		b.TimeOut = tc
		builtins = append(builtins, b)
	}
	if p.HostkeyChanged != "" {
		b := s.hostkeyBlock(p)
		b.TimeOut = tc
		builtins = append(builtins, b)
	}
	if p.AnyKey != "" {
		builtins = append(builtins, ExpectEntry{Active: 1, Expect: dotAll(p.AnyKey),
			Send: "\n", Return: 0, OnMatch: -1, OnFail: -1, TimeOut: tc})
	}
	loginExpect := "login as:"
	if p.Username != "" {
		loginExpect = p.Username + "|login as:" // пустой Username не делает «пустую» альтернативу
	}
	login := ExpectEntry{Active: 1,
		Expect: loginExpect, Send: pe.User, Return: 1, OnMatch: -1, OnFail: -1, TimeOut: tc}
	// M35 P0: для publickey кред для password-блока — Passphrase (не Pass);
	// иначе короткий цикл на пустом Pass пропускал "Enter passphrase for key"
	// и ключ с passphrase не аутентифицировался автоматически.
	cred := pe.Pass
	if pe.AuthType == "publickey" {
		cred = pe.Passphrase
	}
	if cred == "" {
		// M35 P0 MAJOR-1: беспарольный ключ (agent/без passphrase). ssh-семейство
		// идёт с -l — «login as:» не появляется, поэтому [login] сам по себе не
		// даст CONNECTED (GUI виснет). Для publickey добавляем command-промпт,
		// чтобы рукопожатие завершилось у шелла. Методы с реальным «login as:»
		// (telnet/cu без -l) оставляем на [login] — OnMatch=-2.
		login.OnMatch = -2
		builtins = append(builtins, login)
		// Finding #2: command-промпт вооружается независимо от $USER (легаси
		// asbru_conn:1851+); пустой User+Pass не должен оставлять только login.
		if pe.AuthType == "publickey" || pe.User == "" {
			builtins = appendBuiltinCommand(builtins, p, tcmd)
		}
		return builtins, len(builtins)
	}
	pw := s.passwordBlock(p, pe)
	pw.TimeOut = tc
	builtins = append(builtins, login, pw)
	builtins = appendBuiltinCommand(builtins, p, tcmd)
	return builtins, len(builtins)
}

// appendBuiltinCommand — command-промпт завершает рукопожатие: дальше —
// пользовательская фаза (expect-блоки) или CONNECTED при исчерпании. Если
// command-промпт отключён (явная пустота — R1): коннектим сразу после
// предпоследнего блока (без Stay — иначе OnMatch недостижим).
func appendBuiltinCommand(builtins []ExpectEntry, p Prompts, tcmd int) []ExpectEntry {
	if p.Command != "" {
		return append(builtins, ExpectEntry{Active: 1, Expect: dotAllMulti(p.Command),
			Return: 0, OnMatch: -1, OnFail: -1, TimeOut: tcmd})
	}
	last := len(builtins) - 1
	builtins[last].OnMatch = -2
	builtins[last].Stay = false
	return builtins
}

// timeoutConnect — defaults 'timeout connect' (легаси asbru_conn:150, дефолт 40):
// сколько ждать рукопожатие (login/password/sudo/hostkey) до EventClosed.
func (s *Session) timeoutConnect() int {
	if s.opts.defaults != nil && int(s.opts.defaults.TimeOutConnect) > 0 {
		return int(s.opts.defaults.TimeOutConnect)
	}
	return 40
}

// timeoutCommand — defaults 'timeout command' (легаси asbru_conn:151, дефолт 60):
// сколько ждать command-промпт/пользовательскую фазу после рукопожатия.
func (s *Session) timeoutCommand() int {
	if s.opts.defaults != nil && int(s.opts.defaults.TimeOutCommand) > 0 {
		return int(s.opts.defaults.TimeOutCommand)
	}
	return 60
}

// passwordBlock: Stay + интерактивный/статичный ответ; легаси — до 6 отправок,
// 7-й промпт → «rejected»; <<ASK_PASS>> или пусто → вопрос.
func (s *Session) passwordBlock(p Prompts, pe *config.Environment) ExpectEntry {
	block := ExpectEntry{Active: 1, Expect: s.opts.prompts.Password, Return: 1,
		OnMatch: -1, OnFail: -1, TimeOut: -1, Stay: true, Hidden: 1}
	block.Ask = func([]string) (string, AskOutcome) {
		s.passAttempts++ // = ответ на текущий промпт
		val, ask, reject := passwordChoice(pe, s.passAttempts, s.passAutoSent)
		if reject {
			return "Provided username/password '" + pe.User + "/<<hidden_password>>' was rejected", AskReject
		}
		if ask {
			txt, ok := s.ask("Password requested", MetaAskPassword)
			if !ok || txt == "" { // MINOR-5: пустой ввод = отмена (легаси «Password input cancelled»)
				return "", AskCancel
			}
			return txt, AskSent
		}
		s.passAutoSent = true
		return val, AskSent
	}
	return block
}

// passwordChoice — решение на очередной попытке ввода пароля (M35): реальный
// пароль шлём АВТОМАТИЧЕСКИ один раз; если отклонён (повторный промпт) и/или
// пароля нет — спрашиваем у пользователя (легаси: $PASS='<<ASK_PASS>>' после 1-й,
// asbru_conn:1814-1816). reject — 7-й промпт (до 6 отправок, как легаси-коммент:
// 1 авто + 5 ручных, 7-й → «rejected»).
func passwordChoice(pe *config.Environment, passAttempts int, passAutoSent bool) (value string, ask bool, reject bool) {
	if passAttempts > 6 { // 7-й промпт и позже → reject
		return "", false, true
	}
	// M35 P0: publickey — отправляем Passphrase (ключевая фраза), не Pass.
	cred := pe.Pass
	if pe.AuthType == "publickey" {
		cred = pe.Passphrase
	}
	if cred != "" && cred != "<<ASK_PASS>>" && !passAutoSent {
		return cred, false, false
	}
	return "", true, false
}

// sudoBlock: клиентский sudo-промпт (sudo -p ...); 2-й промпт → «rejected».
func (s *Session) sudoBlock(p Prompts) ExpectEntry {
	block := ExpectEntry{Active: 1, Expect: regexpQuote(p.SudoPrompt), Return: 1,
		OnMatch: -1, OnFail: -1, TimeOut: -1, Stay: true, Hidden: 1}
	block.Ask = func([]string) (string, AskOutcome) {
		if s.sudoAttempts >= 1 {
			return "Provided 'sudo' password was rejected", AskReject
		}
		s.sudoAttempts++
		if p.SudoPassword == "" || p.SudoPassword == "<<ASK_PASS>>" {
			txt, ok := s.ask("sudo password requested", MetaAskSudo)
			if !ok || txt == "" { // MINOR-5: пустой ввод = отмена
				return "", AskCancel
			}
			return txt, AskSent
		}
		return p.SudoPassword, AskSent
	}
	return block
}

// hostkeyBlock: авто-приём (yes из группы захвата) или вопрос пользователю.
func (s *Session) hostkeyBlock(p Prompts) ExpectEntry {
	block := ExpectEntry{Active: 1, Expect: dotAll(p.HostkeyChanged), Return: 1,
		OnMatch: -1, OnFail: -1, TimeOut: -1}
	block.Ask = func(caps []string) (string, AskOutcome) {
		if p.AutoAcceptKey {
			if len(caps) > 1 {
				return caps[1], AskSent
			}
			return "yes", AskSent
		}
		text := "Answer yes/no"
		if len(caps) > 0 {
			text = caps[0]
		}
		txt, ok := s.ask(text, MetaAskHostKey)
		if !ok {
			return "", AskCancel
		}
		return txt, AskSent
	}
	return block
}

// remoteHostBlock: «HOST KEY HAS CHANGED» — auto-accept чистит offending-строку
// known_hosts и просит перезапустить поток; иначе — закрытие.
func (s *Session) remoteHostBlock(p Prompts) ExpectEntry {
	block := ExpectEntry{Active: 1, Expect: dotAll(p.RemoteHostChanged), Return: 0,
		OnMatch: -1, OnFail: -1, TimeOut: -1}
	block.Ask = func(caps []string) (string, AskOutcome) {
		if !p.AutoAcceptKey {
			return "HOSTKEY:ignoring offending hostkey by configuration", AskReject
		}
		if len(caps) < 3 || caps[1] == "" || filepath.Clean(caps[1]) != filepath.Clean(s.known) {
			return "HOSTKEY:offending key in unexpected file (ignored)", AskReject
		}
		line, err := strconv.Atoi(caps[2])
		if err != nil || line < 1 {
			return "cannot fix remote host key: bad line " + caps[2], AskReject
		}
		if err := s.fixKnownHosts(caps[1], line); err != nil {
			return "cannot fix known_hosts: " + err.Error(), AskReject
		}
		return "", AskRestart
	}
	return block
}

// preparedEnv — копия окружения с подстановками в полях команды (сборка argv
// использует ИТОГ, оригинал конфига не мутируется). M35: если маска KeePass в
// ПАРОЛЕ/ПОЛЬЗОВАТЕЛЕ не разрешилась — возвращаем ошибку (иначе литеральная
// маска ушла бы как кред (легаси _dieMissingKeePassEntry)).
func (s *Session) preparedEnv() (*config.Environment, error) {
	// M36-ревью MAJOR: перечитываем cfg под cfgMu (edit→restart). Экземпляр
	// конфига меняется целиком, а не мутируется — работаем на копии.
	src := s.envConfig()
	if src == nil {
		return src, nil
	}
	cp := *src
	// маски KeePass разрешаются независимо от подстановок (M10b)
	if s.opts.kp != nil {
		// легаси: applyMask выполняется до subst (asbru_conn)
		var perr error
		cp.Pass, perr = keepass.ReplaceMasks(cp.Pass, s.opts.kp)
		if perr != nil {
			return nil, fmt.Errorf("KeePass: не удалось разрешить маску в пароле: %w", perr)
		}
		cp.User, perr = keepass.ReplaceMasks(cp.User, s.opts.kp)
		if perr != nil {
			return nil, fmt.Errorf("KeePass: не удалось разрешить маску в пользователе: %w", perr)
		}
		// M35 P0 MAJOR-2: publickey ПАРОЛЬНАЯ ФРАЗА теперь уходит как кред
		// (builtinBlocks/passwordChoice) — маска там ДОЛЖНА разрешиться или
		// стать ошибкой, иначе литеральная маска аутентифицирует (инвариант
		// «маска в кредах → resolve or error», как в Pass/User/Options/expect).
		cp.Passphrase, perr = keepass.ReplaceMasks(cp.Passphrase, s.opts.kp)
		if perr != nil {
			return nil, fmt.Errorf("KeePass: не удалось разрешить маску в passphrase: %w", perr)
		}
		cp.PassphraseUser, perr = keepass.ReplaceMasks(cp.PassphraseUser, s.opts.kp)
		if perr != nil {
			return nil, fmt.Errorf("KeePass: не удалось разрешить маску в passphrase user: %w", perr)
		}
		cp.Options, perr = keepass.ReplaceMasks(cp.Options, s.opts.kp)
		if perr != nil {
			return nil, fmt.Errorf("KeePass: не удалось разрешить маску в опциях: %w", perr)
		}
		// глубокое копирование expect — мутация оригинала недопустима (-race)
		if len(cp.Expect) > 0 {
			exp := make([]map[string]any, len(cp.Expect))
			for i, m := range src.Expect {
				nm := make(map[string]any, len(m))
				for k, v := range m {
					nm[k] = v
				}
				exp[i] = nm
			}
			// M35-ревью (MAJOR-3): mask в expect SEND (уходит по проводу как кред) —
			// ошибка, не литерал. В expect-РЕГЭКСПЕ — не ошибка (редкий ложный
			// позитив на паттерн, похожий на маску), оставляем как есть.
			for _, m := range exp {
				if sv, ok := m["send"].(string); ok {
					if rv, err := keepass.ReplaceMasks(sv, s.opts.kp); err != nil {
						return nil, fmt.Errorf("KeePass: не удалось разрешить маску в expect send: %w", err)
					} else {
						m["send"] = rv
					}
				}
				if sv, ok := m["expect"].(string); ok {
					if rv, err := keepass.ReplaceMasks(sv, s.opts.kp); err == nil {
						m["expect"] = rv
					} // err != nil: литерал — НЕ ошибка (см. выше)
				}
			}
			cp.Expect = exp
		}
	}
	r := s.resolver()
	cp.IP = Substitute(cp.IP, r)
	cp.Port = Substitute(cp.Port, r)
	cp.User = Substitute(cp.User, r)
	cp.Pass = Substitute(cp.Pass, r)
	cp.Passphrase = Substitute(cp.Passphrase, r)
	cp.Options = Substitute(cp.Options, r)
	cp.PublicKey = Substitute(cp.PublicKey, r)
	cp.PassphraseUser = Substitute(cp.PassphraseUser, r)
	cp.Title = Substitute(cp.Title, r)
	// Finding #8: proxy/jump поля тоже проходят подстановки (легаси
	// asbru_conn:249-256). Иначе макросы уходили литералами в ssh/tunnel.
	cp.ProxyIP = Substitute(cp.ProxyIP, r)
	cp.ProxyPort = Substitute(cp.ProxyPort, r)
	cp.ProxyUser = Substitute(cp.ProxyUser, r)
	cp.ProxyPass = Substitute(cp.ProxyPass, r)
	cp.JumpIP = Substitute(cp.JumpIP, r)
	cp.JumpUser = Substitute(cp.JumpUser, r)
	cp.JumpPass = Substitute(cp.JumpPass, r)
	cp.JumpKey = Substitute(cp.JumpKey, r)
	// JumpPort — FlexInt: подстановка возможна только через строковую форму
	// (нечисловой макрос при разборе YAML уже стал 0, поэтому это no-op guard).
	if int(cp.JumpPort) != 0 {
		raw := strconv.Itoa(int(cp.JumpPort))
		if sub := Substitute(raw, r); sub != raw {
			if n, err := strconv.Atoi(sub); err == nil {
				cp.JumpPort = config.FlexInt(n)
			}
		}
	}
	return &cp, nil
}

// ask — вопрос пользователю: EventAsk + ожидание ControlAnswer/отмены/контекста.
func (s *Session) ask(text, meta string) (string, bool) {
	// устаревшие ответы (до текущего вопроса) не должны «прорастать» в новый;
	// дренаж ДО emit — ответ на только что увиденный EventAsk не съедается (R2)
	for {
		select {
		case <-s.ans:
			continue
		default:
		}
		break
	}
	s.emit(Event{Kind: EventAsk, Text: text, Meta: meta})
	for {
		select {
		case <-s.ctx.Done():
			return "", false
		case m := <-s.ans:
			if m.Cancel {
				return "", false
			}
			return m.Text, true
		}
	}
}

// fixKnownHosts — удаление offending-строки (легаси-эхо-в-файл, но без
// шелловой команды — прямой rewrite).
func (s *Session) fixKnownHosts(path string, line int) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	if line < 1 || line > len(lines) {
		return fmt.Errorf("строка %d вне файла (%d строк)", line, len(lines))
	}
	lines = append(lines[:line-1], lines[line:]...)
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0600)
}

// isNoHandshakeMethod — метод без рукопожатия: процесс И ЕСТЬ сессия (Local Shell,
// Generic Command, Serial cu, 3270 — класс «auth disabled → manual only» из
// method.FeaturesFor). Для них «подключено» = процесс запущен (M35: иначе зомби).
func isNoHandshakeMethod(m string) bool {
	switch m {
	case "Local Shell", "Generic Command", "Serial (cu)", "IBM 3270/5250",
		// M2: standalone RDP/VNC — «подключено» = процесс клиента запущен (не
		// userpass/expect; иначе GUI висит в «подключение»).
		"RDP (rdesktop)", "RDP (xfreerdp)", "VNC":
		return true
	}
	return false
}

// isManualAuth — auth type "manual" (легаси $MANUAL): креды вводит
// пользователь, автоматических expect-блоков нет; CONNECTED — сразу после
// запуска процесса (asbru_conn:177,1993-1999).
func isManualAuth(e *config.Environment) bool {
	return e != nil && strings.EqualFold(e.AuthType, "manual")
}

func isUserpassMethod(m string) bool {
	switch m {
	// M35-ревью MINOR-2: MOSH/WebDAV/Serial (remote-tty) тоже умеют userpass
	// (FeaturesFor: [userpass, manual]); иначе builtinBlocks возвращал nil →
	// eng==nil → не было EventStatusConnected → GUI «connecting» (зомби).
	case "SSH", "ssh", "SFTP", "sftp", "Telnet", "telnet", "FTP", "ftp",
		"MOSH", "mosh", "WebDAV", "cadaver", "Serial (remote-tty)", "remote-tty":
		return true
	}
	return false
}

// resizeTo — TIOCSWINSZ на текущий PTY (SIGWINCH-эквивалент; M11 —
// аудит: ssh-процесс оставался 80×24 при изменении окна).
func (s *Session) resizeTo(cols, rows int) {
	if cols <= 0 || rows <= 0 {
		return
	}
	s.curMu.Lock()
	defer s.curMu.Unlock()
	s.lastCol, s.lastRow, s.lastSet = cols, rows, true
	setWinsize(s.cur, cols, rows)
}

// reapplyWinsize — после открытия нового потока (рестарт) свежий pty
// получает последний известный размер, иначе ssh остаётся 80×24 (M1).
func (s *Session) reapplyWinsize() {
	s.curMu.Lock()
	defer s.curMu.Unlock()
	if s.lastSet {
		setWinsize(s.cur, s.lastCol, s.lastRow)
	}
}

// setWinsize — ioctl; вызывается только под curMu (H1: Fd vs Close).
func setWinsize(st io.ReadWriteCloser, cols, rows int) {
	f, ok := st.(*os.File)
	if !ok || f == nil {
		return
	}
	_ = unix.IoctlSetWinsize(int(f.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: uint16(rows), Col: uint16(cols)})
}

// regexpQuote — литеральная подстрока как паттерн (\Q...\E легаси).
func regexpQuote(s string) string { return regexp.QuoteMeta(s) }

// dotAll — многострочные промпты: легаси-паттерны с ^.* ищут всё подряд,
// а буфер перед промптом уже содержит предыдущие строки вывода.
func dotAll(p string) string {
	if strings.HasPrefix(p, "(?s)") {
		return p
	}
	return "(?s)" + p
}

// dotAllMulti — command prompt: (?m) возвращает якорю ^ смысл «начало строки»,
// чтобы «Last login: » не считался приглашением (ревью M3).
func dotAllMulti(p string) string {
	return "(?s)(?m)" + p
}

// defaultKnownHosts — ~/.ssh/known_hosts (default; тесты переопределяют).
func defaultKnownHosts() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".ssh", "known_hosts")
}

// expectBlocksFromConfig — конвертация YAML-блоков expect: сохраняются
// пользовательские on_match/on_fail (дефолты из ExpectEntryFromMap).
func expectBlocksFromConfig(items []map[string]any) []ExpectEntry {
	if len(items) == 0 {
		return nil
	}
	blocks := make([]ExpectEntry, 0, len(items))
	for _, m := range items {
		b := ExpectEntryFromMap(m)
		if b.Active != 1 {
			continue
		}
		blocks = append(blocks, b)
	}
	return blocks
}

// blocks — итоговая последовательность (фаза списка + пользовательские блоки);
// публичная для тестов. Порядок: builtin → конфигурация. Подготовленное
// окружение (маски/подстановки) — H1.
func (s *Session) blocks() []ExpectEntry {
	pe, err := s.preparedEnv()
	if err != nil || pe == nil {
		return nil
	}
	builtins, _ := s.builtinBlocks(pe)
	var user []ExpectEntry
	if s.envConfig() != nil {
		user = expectBlocksFromConfig(pe.Expect)
	}
	return append(builtins, user...)
}
