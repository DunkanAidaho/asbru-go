package session

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/DunkanAidaho/asbru-go/internal/config"
	"github.com/DunkanAidaho/asbru-go/internal/keepass"
)

// fakeKP — резолвер масок (Vault иначе не открыть в тестах без kdbx).
type fakeKP struct{}

func (fakeKP) Field(field, path string) (string, error) {
	if field == "password" && path == "ent1" {
		return "kpx-pass", nil
	}
	if field == "username" && path == "ent1" {
		return "kpx-user", nil
	}
	return "", keepass.ErrNotFound
}

// M10b: маски <password|uuid> в pass/options/expect разрешаются в
// preparedEnv (легаси applyMask перед subst);
// без резолвера маски остаются как есть.
func TestSessionKeepassMasksResolved(t *testing.T) {
	cfg := &config.Environment{Name: "k", Method: "SSH", User: "root",
		Pass:    "<password|ent1>",
		Options: "-o <username|ent1>",
		Expect:  []map[string]any{{"expect": "<password|ent1>"}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, err := New(ctx, BuildEnv{UUID: "u1"}, cfg,
		WithKeepassResolver(fakeKP{}),
		WithStreamFactory(func() (io.ReadWriteCloser, error) { return &pipeStream{}, nil }))
	if err != nil {
		t.Fatal(err)
	}
	p, _ := s.preparedEnv()
	if p.Pass != "kpx-pass" {
		t.Fatalf("Pass: %q", p.Pass)
	}
	if p.Options != "-o kpx-user" {
		t.Fatalf("Options: %q", p.Options)
	}
	if e := p.Expect[0]["expect"]; e != "kpx-pass" {
		t.Fatalf("expect: %v", e)
	}
	if cfg.Pass != "<password|ent1>" {
		t.Fatalf("оригинал изменён: %q", cfg.Pass)
	}
}

// Без резолвера маски не трогаются.
func TestSessionKeepassMasksInert(t *testing.T) {
	cfg := &config.Environment{Name: "k", Method: "SSH", Pass: "<password|ent1>"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, err := New(ctx, BuildEnv{UUID: "u1"}, cfg,
		WithStreamFactory(func() (io.ReadWriteCloser, error) { return &pipeStream{}, nil }))
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := s.preparedEnv(); p.Pass != "<password|ent1>" {
		t.Fatalf("маска разрешена без резолвера: %q", p.Pass)
	}
}

// H1 (ревью): движок рукопожатия должен использовать РАЗРЕШЁННОЕ окружение
// (маски/подстановки), а не сырой s.cfg — пароль на «Password:» уходит
// уже разрешённым, а не литеральной маской.
func TestSessionKeepassMasksInEngine(t *testing.T) {
	cfg := &config.Environment{Name: "k", Method: "SSH", User: "root",
		Pass: "<password|ent1>"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := &pipeStream{r: make(chan []byte, 16), w: make(chan []byte, 16)}
	s, err := New(ctx, BuildEnv{UUID: "u1"}, cfg,
		WithKeepassResolver(fakeKP{}),
		WithStreamFactory(func() (io.ReadWriteCloser, error) { return stream, nil }))
	if err != nil {
		t.Fatal(err)
	}
	stream.serverSend("login as: ")
	stream.serverSend("Password: ")
	deadline := time.After(2 * time.Second)
	for {
		select {
		case e := <-s.Out:
			if e.Kind == EventExpectSending && e.Text == "kpx-pass" {
				return
			}
		case <-deadline:
			t.Fatalf("движок не отправил разрешённый пароль; wrote=%q", stream.written(t))
		}
	}
}

// H1 (доп.): маски в user-поле тоже разрешаются (login-блок).
func TestSessionKeepassMasksInUser(t *testing.T) {
	cfg := &config.Environment{Name: "k", Method: "SSH", User: "<username|ent1>",
		Pass: "secret"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := &pipeStream{r: make(chan []byte, 16), w: make(chan []byte, 16)}
	s, err := New(ctx, BuildEnv{UUID: "u1"}, cfg,
		WithKeepassResolver(fakeKP{}),
		WithStreamFactory(func() (io.ReadWriteCloser, error) { return stream, nil }))
	if err != nil {
		t.Fatal(err)
	}
	stream.serverSend("login as: ")
	deadline := time.After(2 * time.Second)
	for {
		select {
		case e := <-s.Out:
			if e.Kind == EventExpectSending && e.Text == "kpx-user" {
				return
			}
		case <-deadline:
			t.Fatalf("логин не разрешён: %q", stream.written(t))
		}
	}
}

// M35: маска в ПАРОЛЕ, которую резолвер не может разрешить (нет записи) →
// preparedEnv возвращает ошибку (иначе литеральная маска ушла бы как пароль,
// легаси _dieMissingKeePassEntry).
func TestSessionKeepassUnresolvedMaskFails(t *testing.T) {
	cfg := &config.Environment{Name: "k", Method: "SSH", User: "root",
		Pass: "<password|nope>"} // ent1 есть, nope — нет
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, err := New(ctx, BuildEnv{UUID: "u1"}, cfg,
		WithKeepassResolver(fakeKP{}),
		WithStreamFactory(func() (io.ReadWriteCloser, error) { return &pipeStream{}, nil }))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.preparedEnv(); err == nil {
		t.Fatal("ожидалась ошибка разрешения маски в пароле")
	}
}

// M35-ревью: неразрешимая маска и в поле ПОЛЬЗОВАТЕЛЯ → ошибка (не только Pass).
func TestSessionKeepassUnresolvedUserMaskFails(t *testing.T) {
	cfg := &config.Environment{Name: "k", Method: "SSH", User: "<username|nope>", Pass: "secret"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, err := New(ctx, BuildEnv{UUID: "u1"}, cfg,
		WithKeepassResolver(fakeKP{}),
		WithStreamFactory(func() (io.ReadWriteCloser, error) { return &pipeStream{}, nil }))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.preparedEnv(); err == nil {
		t.Fatal("ожидалась ошибка разрешения маски в пользователе")
	}
}
