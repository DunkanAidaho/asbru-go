package session

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DunkanAidaho/asbru-go/internal/config"
)

// newPromptSession — сессия с фейковым потоком + опции подтверждений.
func newPromptSession(t *testing.T, cfg *config.Environment,
	opts ...Option) (*Session, *pipeStream, <-chan Event) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	stream := &pipeStream{r: make(chan []byte, 16), w: make(chan []byte, 16)}
	all := append([]Option{WithStreamFactory(func() (io.ReadWriteCloser, error) {
		return stream, nil
	})}, opts...)
	s, err := New(ctx, BuildEnv{UUID: "u1"}, cfg, all...)
	if err != nil {
		t.Fatal(err)
	}
	return s, stream, s.Out
}

// race-сборщик событий с дедлайном.
func waitEvent(t *testing.T, out <-chan Event, kind EventKind, timeout time.Duration) Event {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case e := <-out:
			if e.Kind == kind {
				return e
			}
		case <-deadline:
			t.Fatalf("событие %v не пришло", kind)
		}
	}
}

func answerPass(t *testing.T, s *Session, text string) {
	t.Helper()
	s.Control <- ControlMsg{Kind: ControlAnswer, Text: text}
}

func waitFor(t *testing.T, what string, deadline time.Duration, cond func() bool) {
	t.Helper()
	d := time.After(deadline)
	for {
		if cond() {
			return
		}
		select {
		case <-d:
			t.Fatalf("не дождались: %s", what)
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}

// M9-1: hostkey с авто-приёмом — блок из списка отвечает "yes" (группа
// захвата из \(yes/no\)), затем хендшейк идёт дальше.
func TestSessionHostkeyAutoAccept(t *testing.T) {
	cfg := &config.Environment{Name: "h", User: "root", Pass: "secret", Method: "SSH"}
	s, stream, out := newPromptSession(t, cfg,
		WithPrompts(Prompts{AutoAcceptKey: true,
			Username: "login as:", Password: "Password:",
			HostkeyChanged: DefaultHostkeyChangedPrompt,
			Command:        DefaultCommandPrompt}))
	stream.serverSend("The authenticity of host 'loki' can't be established.\n" +
		"Are you sure you want to continue connecting (yes/no)? ")

	e := waitEvent(t, out, EventExpectSending, 2*time.Second)
	if !strings.Contains(e.Text, "yes") {
		t.Fatalf("отправлено %q, хотим yes", e.Text)
	}
	if e.Meta == MetaHidden {
		t.Fatalf("hostkey-отправка не должна быть hidden, meta=%v", e.Meta)
	}
	stream.serverSend("login as: ")
	stream.serverSend("Password: ")
	stream.serverSend("root@host:~# ")
	waitEvent(t, out, EventStatusConnected, 2*time.Second)
	_ = s
}

// M9-2: ручной ответ на hostkey — EventAsk, ответ через Control{ControlAnswer}.
func TestSessionHostkeyManualAsk(t *testing.T) {
	cfg := &config.Environment{Name: "h", User: "root", Pass: "secret", Method: "SSH"}
	s, stream, out := newPromptSession(t, cfg)
	stream.serverSend("...continue connecting (yes/no)? ")

	e := waitEvent(t, out, EventAsk, 2*time.Second)
	if !strings.Contains(e.Text, "yes/no") {
		t.Fatalf("EventAsk без промпта: %q", e.Text)
	}
	answerPass(t, s, "no")
	waitEvent(t, out, EventExpectSending, 2*time.Second)
}

// M9-2b: отмена ручного ответа → закрытие (легаси зависал — не переносим).
func TestSessionHostkeyCancel(t *testing.T) {
	cfg := &config.Environment{Name: "h", User: "root", Method: "SSH"}
	s, stream, out := newPromptSession(t, cfg)
	stream.serverSend("...continue connecting (yes/no)? ")
	waitEvent(t, out, EventAsk, 2*time.Second)
	go func() {
		time.Sleep(20 * time.Millisecond)
		s.Control <- ControlMsg{Kind: ControlAnswer, Cancel: true}
	}()
	e := waitEvent(t, out, EventClosed, 2*time.Second)
	if !strings.Contains(e.Text, "cancelled") {
		t.Fatalf("отмена без 'cancelled': %q", e.Text)
	}
}

// M9-3: sudo-промпт — литеральная строка Prompts{SudoPrompt}, пароль
// отправляется скрыто, дальше хендшейк продолжается.
func TestSessionSudoPromptSendsPassword(t *testing.T) {
	cfg := &config.Environment{Name: "s", User: "root", Pass: "secret", Method: "SSH"}
	_, stream, out := newPromptSession(t, cfg,
		WithPrompts(Prompts{SudoPrompt: "sudo: password", SudoPassword: "s3cret"}))
	stream.serverSend("sudo: password for root: ")

	e := waitEvent(t, out, EventExpectSending, 2*time.Second)
	if e.Text != "s3cret" || e.Meta != MetaHidden {
		t.Fatalf("sudo: (%q, %v), хотим (s3cret, hidden)", e.Text, e.Meta)
	}
}

// M9-4: sudo <<ASK_PASS>> → вопрос, ответ идёт в поток.
func TestSessionSudoAskPass(t *testing.T) {
	cfg := &config.Environment{Name: "s", User: "root", Method: "SSH"}
	s, stream, out := newPromptSession(t, cfg,
		WithPrompts(Prompts{SudoPrompt: "sudo: password", SudoPassword: "<<ASK_PASS>>"}))
	stream.serverSend("sudo: password for root: ")

	e := waitEvent(t, out, EventAsk, 2*time.Second)
	if !strings.Contains(e.Text, "sudo") {
		t.Fatalf("EventAsk без слова sudo: %q", e.Text)
	}
	answerPass(t, s, "s4")
	waitEvent(t, out, EventExpectSending, 2*time.Second)
}

// M9-5: повторный sudo-промпт = пароль неверный → закрытие «rejected».
func TestSessionSudoSecondAttemptClosed(t *testing.T) {
	cfg := &config.Environment{Name: "s", User: "root", Method: "SSH"}
	s, stream, out := newPromptSession(t, cfg,
		WithPrompts(Prompts{SudoPrompt: "sudo: password", SudoPassword: "bad"}))
	stream.serverSend("sudo: password for root: ")
	waitEvent(t, out, EventExpectSending, 2*time.Second)
	stream.serverSend("sudo: password for root: ")

	e := waitEvent(t, out, EventClosed, 2*time.Second)
	if !strings.Contains(e.Text, "rejected") {
		t.Fatalf("закрытие без 'rejected': %q", e.Text)
	}
	_ = s
}

// M9-6: «Press any key to continue» → Enter без вопросов.
func TestSessionPressAnyKey(t *testing.T) {
	cfg := &config.Environment{Name: "p", User: "root", Pass: "secret", Method: "SSH"}
	_, stream, out := newPromptSession(t, cfg)
	stream.serverSend("Warning: Permanently added to known hosts.\n** Press any key to continue **")

	e := waitEvent(t, out, EventExpectSending, 2*time.Second)
	if e.Text != "\n" {
		t.Fatalf("anykey: отправлено %q, хотим \"\\n\"", e.Text)
	}
	stream.serverSend("login as: ")
	stream.serverSend("Password: ")
	stream.serverSend("root@host:~# ")
	waitEvent(t, out, EventStatusConnected, 2*time.Second)
}

// M9-7: REMOTE HOST — авто-фикс: offending-строка удаляется из known_hosts,
// поток переоткрывается (factory вызывается повторно), хендшейк — заново.
func TestSessionRemoteHostChangedAutoFix(t *testing.T) {
	dir := t.TempDir()
	kh := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(kh, []byte("h1 AAA...\nh2 BBB...\nh3 CCC...\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	first := &pipeStream{r: make(chan []byte, 16), w: make(chan []byte, 16)}
	second := &pipeStream{r: make(chan []byte, 16), w: make(chan []byte, 16)}
	var calls atomic.Int32 // счётчик пересекает границу горутин (race-отчёт)
	cfg := &config.Environment{Name: "ch", User: "root", Pass: "secret", Method: "SSH"}
	s, err := New(ctx, BuildEnv{UUID: "u1"}, cfg,
		WithPrompts(Prompts{AutoAcceptKey: true, RemoteHostChanged: DefaultRemoteHostPrompt,
			Username: "login as:", Password: "Password:", Command: DefaultCommandPrompt}),
		WithKnownHostsFile(kh),
		WithStreamFactory(func() (io.ReadWriteCloser, error) {
			if calls.Add(1) == 1 {
				return first, nil
			}
			return second, nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	first.serverSend("WARNING: REMOTE HOST IDENTIFICATION HAS CHANGED!\n" +
		"Offending ECDSA key in " + kh + ":2\n" +
		"Password authentication for 'root' is rejected.")

	// строка-хост 2 удалена из known_hosts (дождаться обработки чанка)
	waitFor(t, "удаление offending в known_hosts", 2*time.Second, func() bool {
		b, _ := os.ReadFile(kh)
		return !strings.Contains(string(b), "h2 BBB")
	})
	if calls.Load() < 2 {
		t.Fatalf("поток не переоткрыт (calls=%d)", calls.Load())
	}
	second.serverSend("login as: ")
	second.serverSend("Password: ")
	second.serverSend("root@host:~# ")

	deadline := time.After(2 * time.Second)
	for {
		select {
		case e := <-s.Out:
			if e.Kind == EventStatusConnected {
				return
			}
			if e.Kind == EventClosed {
				t.Fatalf("перезапуск закрыл сессию: %q", e.Text)
			}
		case <-deadline:
			t.Fatal("после авто-фикса нет CONNECTED")
		}
	}
}

// M9-8: REMOTE HOST без AUTO-ACCEPT → закрытие, known_hosts не тронута.
func TestSessionRemoteHostChangedReject(t *testing.T) {
	dir := t.TempDir()
	kh := filepath.Join(dir, "known_hosts")
	orig := "h1 AAA...\nh2 BBB...\n"
	if err := os.WriteFile(kh, []byte(orig), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Environment{Name: "rh", User: "root", Method: "SSH"}
	_, stream, out := newPromptSession(t, cfg, WithKnownHostsFile(kh))
	stream.serverSend("Offending ECDSA key in " + kh + ":2\n")

	e := waitEvent(t, out, EventClosed, 2*time.Second)
	if !strings.Contains(e.Text, "offending") {
		t.Fatalf("закрытие без offending: %q", e.Text)
	}
	got, _ := os.ReadFile(kh)
	if string(got) != orig {
		t.Fatalf("known_hosts изменена при отказе: %q", got)
	}
}

// M9-9: <<ASK_PASS>> — каждый Password: даёт вопрос; отмена → «cancelled».
func TestSessionPasswordAskPassInteractive(t *testing.T) {
	cfg := &config.Environment{Name: "p", User: "root", Pass: "<<ASK_PASS>>", Method: "SSH"}
	s, stream, out := newPromptSession(t, cfg)
	stream.serverSend("login as: ")
	stream.serverSend("Password: ")

	e := waitEvent(t, out, EventAsk, 2*time.Second)
	if !strings.Contains(e.Text, "Password") {
		t.Fatalf("EventAsk без Password: %q", e.Text)
	}
	go func() {
		time.Sleep(20 * time.Millisecond)
		s.Control <- ControlMsg{Kind: ControlAnswer, Cancel: true}
	}()
	e = waitEvent(t, out, EventClosed, 2*time.Second)
	if !strings.Contains(e.Text, "cancelled") {
		t.Fatalf("отмена без 'cancelled': %q", e.Text)
	}
	_ = stream
}

// M35 (was M9-10): неверный пароль — реальный пароль шлём ОДИН раз автоматически,
// при повторном промпте СПРАШИВАЕМ пользователя (легаси $PASS='<<ASK_PASS>>'
// после 1-й, asbru_conn:1814) — а не 6 слепых пересылок.
func TestSessionPasswordMaxAttempts(t *testing.T) {
	cfg := &config.Environment{Name: "p", User: "root", Pass: "wrong", Method: "SSH"}
	s, stream, out := newPromptSession(t, cfg)
	stream.serverSend("login as: ")
	stream.serverSend("Password: ")
	stream.serverSend("Password: ") // отклонён → повторный промпт → вопрос
	e := waitEvent(t, out, EventAsk, 3*time.Second)
	if !strings.Contains(e.Text, "Password") {
		t.Fatalf("ожидался вопрос пароля: %q", e.Text)
	}
	if n := strings.Count(stream.written(t), "wrong"); n != 1 {
		t.Fatalf("wrong отправлено %d раз, хотим 1 (авто-посылка; дальше — вопрос)", n)
	}
	go func() {
		time.Sleep(20 * time.Millisecond)
		s.Control <- ControlMsg{Kind: ControlAnswer, Cancel: true}
	}()
	waitEvent(t, out, EventClosed, 2*time.Second)
}

// M1 (ревью): после авто-фикса ввод пользователя должен идти в НОВЫЙ поток —
// старые фуд-горутины не должны висеть на закрытом потоке.
func TestSessionInputSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	kh := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(kh, []byte("h1\nh2\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	first := &pipeStream{r: make(chan []byte, 16), w: make(chan []byte, 16)}
	second := &pipeStream{r: make(chan []byte, 16), w: make(chan []byte, 16)}
	var calls atomic.Int32
	cfg := &config.Environment{Name: "i", User: "root", Pass: "secret", Method: "SSH"}
	s, err := New(ctx, BuildEnv{UUID: "u1"}, cfg,
		WithPrompts(Prompts{AutoAcceptKey: true, RemoteHostChanged: DefaultRemoteHostPrompt,
			Username: "login as:", Password: "Password:", Command: DefaultCommandPrompt}),
		WithKnownHostsFile(kh),
		WithStreamFactory(func() (io.ReadWriteCloser, error) {
			if calls.Add(1) == 1 {
				return first, nil
			}
			return second, nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	first.serverSend("Offending ECDSA key in " + kh + ":2\n")
	waitFor(t, "рестарт", 2*time.Second, func() bool { return calls.Load() >= 2 })

	s.In <- []byte("hi\n")
	second.serverSend("root@host:~# ")
	w := second.written(t)
	if !strings.Contains(w, "hi") {
		t.Fatalf("ввод не дошёл до нового потока: %q", w)
	}
}

// M2 (ревью): login/password берутся из Prompts — PAM-промпт
// «user@host's password: » обязан матчиться (легаси-регэксп).
func TestSessionPAMPrompt(t *testing.T) {
	cfg := &config.Environment{Name: "pam", User: "root", Pass: "secret", Method: "SSH"}
	_, stream, out := newPromptSession(t, cfg)
	stream.serverSend("login as: ")
	stream.serverSend("user@host's password: ")
	stream.serverSend("root@host:~# ")
	waitEvent(t, out, EventStatusConnected, 2*time.Second)
	w := stream.written(t)
	if !strings.Contains(w, "root\n") || !strings.Contains(w, "secret") {
		t.Fatalf("PAM-хендшейк: %q", w)
	}
}

// M3 (ревью): «Last login: » (без хвостового \\n) не должен считаться
// command prompt — коннектим только на реальный шелл.
func TestSessionLastLoginNotPrompt(t *testing.T) {
	cfg := &config.Environment{Name: "ll", User: "root", Pass: "secret", Method: "SSH"}
	_, stream, out := newPromptSession(t, cfg)
	stream.serverSend("login as: ")
	stream.serverSend("Password: ")
	stream.serverSend("Last login: ")
	// никакого CONNECTED на «Last login: » (300мс тишины; дренаж событий)
	deadline := time.After(300 * time.Millisecond)
waitQuiet:
	for {
		select {
		case e := <-out:
			if e.Kind == EventStatusConnected {
				t.Fatalf("CONNECTED на «Last login»: %#v", e)
			}
		case <-deadline:
			break waitQuiet
		}
	}
	stream.serverSend("root@host:~# ")
	waitEvent(t, out, EventStatusConnected, 2*time.Second)
}

// L1 (ревью): путь known_hosts из вывода сервера, отличный от
// сконфигурированного, не правится и закрывает соединение.
func TestSessionRemoteHostPathMismatch(t *testing.T) {
	dir := t.TempDir()
	kh := filepath.Join(dir, "known_hosts")
	orig := "h1 AAA...\n"
	if err := os.WriteFile(kh, []byte(orig), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Environment{Name: "rh", User: "root", Method: "SSH"}
	_, stream, out := newPromptSession(t, cfg,
		WithPrompts(Prompts{AutoAcceptKey: true, RemoteHostChanged: DefaultRemoteHostPrompt}), WithKnownHostsFile(kh))
	stream.serverSend("Offending ECDSA key in /tmp/evil/known_hosts:2\n")

	e := waitEvent(t, out, EventClosed, 2*time.Second)
	if !strings.Contains(e.Text, "known_hosts") && !strings.Contains(e.Text, "offending") {
		t.Fatalf("не закрылось по чужому пути: %q", e.Text)
	}
	got, _ := os.ReadFile(kh)
	if string(got) != orig {
		t.Fatalf("чужой путь был исправлен: %q", got)
	}
}

// L4 (ревью): sudo-промпт после логина (rc-скрипт) — скан с начала списка.
func TestSessionSudoAfterLogin(t *testing.T) {
	cfg := &config.Environment{Name: "s", User: "root", Pass: "secret", Method: "SSH"}
	_, stream, out := newPromptSession(t, cfg,
		WithPrompts(Prompts{SudoPrompt: "sudo: password", SudoPassword: "su"}))
	stream.serverSend("login as: ")
	stream.serverSend("root@host: sudo: password for root: ")

	waitEvent(t, out, EventExpectSending, 2*time.Second)
	waitFor(t, "sudo-ивент", 2*time.Second, func() bool {
		return strings.Contains(stream.written(t), "su")
	})
}

// L5 (ревью): два промпта в одном чанке — «Password: root@host:~# » —
// второй не теряется после Stay.
func TestSessionSameChunkPrompts(t *testing.T) {
	cfg := &config.Environment{Name: "sc", User: "root", Pass: "secret", Method: "SSH"}
	_, stream, out := newPromptSession(t, cfg)
	stream.serverSend("login as: ")
	stream.serverSend("Password: root@host:~# ")
	waitEvent(t, out, EventStatusConnected, 2*time.Second)
}

// R1 (ре-ревью): явно переданные пустые промпты (command: ” у пользователя)
// НЕ заполняются fill() — тогда отсутствует command-блок и рукопожатие
// завершается по паролю (легаси: отключённый промпт = без ожидания шелла).
func TestSessionExplicitEmptyPromptNotFilled(t *testing.T) {
	cfg := &config.Environment{Name: "e", User: "root", Pass: "secret", Method: "SSH"}
	_, stream, out := newPromptSession(t, cfg,
		WithPrompts(Prompts{Username: "login as:", Password: "pw:"}))
	stream.serverSend("login as: ")
	stream.serverSend("pw: ")
	// без command-блока CONNECTED наступает сразу после пароля
	waitEvent(t, out, EventStatusConnected, 2*time.Second)
	w := stream.written(t)
	if !strings.Contains(w, "secret") {
		t.Fatalf("пароль не отправлен: %q", w)
	}
}

// M35: методы без expect-движка (Generic Command / Local Shell / 3270 без
// блоков) — процесс запущен → CONNECTED сразу (иначе зомби-вкладка).
func TestSessionConnectedForEnginelessMethods(t *testing.T) {
	cfg := &config.Environment{Name: "g", Method: "Generic Command", IP: "ls -la"}
	_, stream, out := newPromptSession(t, cfg)
	stream.serverSend("hello\n")
	if e := waitEvent(t, out, EventStatusConnected, 2*time.Second); e.Kind != EventStatusConnected {
		t.Fatalf("ожидался CONNECTED для движко-less метода: %#v", e)
	}
}

// M35-ревью: reject при переборе пароля — 7-й промпт (после 6 отправок:
// 1 авто + 5 ручных, либо 6 ручных, если пароль не сохранён) → EventClosed
// 'rejected'. Граничное условие passAttempts > 6 закреплено в
// TestPasswordChoice («seven-attempts»).
