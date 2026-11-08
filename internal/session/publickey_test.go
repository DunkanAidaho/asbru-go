package session

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/DunkanAidaho/asbru-go/internal/config"
	"github.com/DunkanAidaho/asbru-go/internal/method"
)

// M35 P0: publickey auth — ключ с passphrase должен аутентифицироваться
// автоматически. Раньше builtinBlocks short-circuit'ил при пустом Pass
// (для publickey парольная фраза лежит в Passphrase, не в Pass) →
// промпт "Enter passphrase for key" не получал ответа → вечный вопрос.
func TestPublickeyPassphraseAutoSend(t *testing.T) {
	stream := &pipeStream{r: make(chan []byte, 16), w: make(chan []byte, 16)}
	cfg := &config.Environment{
		Name: "pk", Method: "SSH", User: "u", AuthType: "publickey",
		PublicKey: "/id", PassphraseUser: "ru", Passphrase: "ph",
	}
	s, err := New(context.Background(), BuildEnv{UUID: "u1"}, cfg,
		WithPrompts(Prompts{Username: "login as:", Password: DefaultPasswordPrompt, Command: DefaultCommandPrompt}),
		WithStreamFactory(func() (io.ReadWriteCloser, error) { return stream, nil }))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Control <- ControlMsg{Kind: ControlDisconnect} }()
	stream.serverSend("Enter passphrase for key '/id': ")
	stream.serverSend("root@host:~# ")
	connected := false
	deadline := time.After(3 * time.Second)
	for !connected {
		select {
		case e := <-s.Out:
			t.Logf("event kind=%d text=%q", e.Kind, e.Text)
			if e.Kind == EventStatusConnected {
				connected = true
			}
		case <-deadline:
			t.Fatal("нет CONNECTED при publickey-passphrase")
		}
	}
	written := stream.written(t)
	// только парольная фраза уходит (ssh c -l не шлёт «login as:»): не User,
	// не PassphraseUser. Точное «ph\n» — login-блок молчит.
	if written != "ph\n" {
		t.Fatalf("написано %q, хотим ровно \"ph\\n\" (login-блок не должен слать пользователя)", written)
	}
}

// M35 P0 MAJOR-1: беспарольный ключ (agent/без passphrase) — ssh-семейство идёт
// с -l, «login as:» не появляется, поэтому [login] не давал CONNECTED (GUI
// виснет). Раньше builtinBlocks short-circuit'ил на пустом Passphrase → нет
// command-промпта → нет «подключено». Теперь command-промпт добавляется.
func TestPublickeyPasswordlessConnects(t *testing.T) {
	stream := &pipeStream{r: make(chan []byte, 16), w: make(chan []byte, 16)}
	cfg := &config.Environment{Name: "pk", Method: "SSH", User: "u",
		AuthType: "publickey", PublicKey: "/id", PassphraseUser: "ru"} // Passphrase ""
	s, err := New(context.Background(), BuildEnv{UUID: "u1"}, cfg,
		WithPrompts(Prompts{Username: "login as:", Password: DefaultPasswordPrompt, Command: DefaultCommandPrompt}),
		WithStreamFactory(func() (io.ReadWriteCloser, error) { return stream, nil }))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Control <- ControlMsg{Kind: ControlDisconnect} }()
	stream.serverSend("root@host:~# ") // ключ без passphrase — сразу шелл
	connected := false
	deadline := time.After(3 * time.Second)
	for !connected {
		select {
		case e := <-s.Out:
			t.Logf("event kind=%d text=%q", e.Kind, e.Text)
			if e.Kind == EventStatusConnected {
				connected = true
			}
		case <-deadline:
			t.Fatal("нет CONNECTED для беспарольного publickey (MAJOR-1)")
		}
	}
}

// M35 P0 MAJOR-2: маска KeePass в publickey-парольной фразе разрешается
// (иначе литеральная <password|ent1> ушла бы как кред).
func TestPublickeyPassphraseMaskResolved(t *testing.T) {
	cfg := &config.Environment{Name: "pk", Method: "SSH", User: "u",
		AuthType: "publickey", Passphrase: "<password|ent1>"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, err := New(ctx, BuildEnv{UUID: "u1"}, cfg,
		WithKeepassResolver(fakeKP{}),
		WithStreamFactory(func() (io.ReadWriteCloser, error) { return &pipeStream{}, nil }))
	if err != nil {
		t.Fatal(err)
	}
	p, _ := s.preparedEnv()
	if p.Passphrase != "kpx-pass" {
		t.Fatalf("Passphrase: %q, want kpx-pass (маска не разрешена)", p.Passphrase)
	}
}

// M35 P0 MAJOR-2: неразрешённая маска в passphrase → ошибка (не литерал).
func TestPublickeyPassphraseMaskErr(t *testing.T) {
	cfg := &config.Environment{Name: "pk", Method: "SSH", User: "u",
		AuthType: "publickey", Passphrase: "<password|missing>"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, err := New(ctx, BuildEnv{UUID: "u1"}, cfg,
		WithKeepassResolver(fakeKP{}),
		WithStreamFactory(func() (io.ReadWriteCloser, error) { return &pipeStream{}, nil }))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.preparedEnv(); err == nil {
		t.Fatal("неразрешённая маска в passphrase должна давать ошибку")
	}
}

// M35 P0: passwordChoice для publickey берёт Passphrase (а не Pass).
func TestPasswordChoicePublickey(t *testing.T) {
	pe := &config.Environment{Pass: "", AuthType: "publickey", Passphrase: "ph"}
	// первый промпт — автопосылка парольной фразы (не вопрос)
	if v, ask, rej := passwordChoice(pe, 1, false); v != "ph" || ask || rej {
		t.Fatalf("publickey 1-й: got (%q,%v,%v), want (ph,false,false)", v, ask, rej)
	}
	// после отклонения (повторный промпт) — вопрос пользователю
	if v, ask, rej := passwordChoice(pe, 2, true); v != "" || !ask || rej {
		t.Fatalf("publickey 2-й: got (%q,%v,%v), want (\"\",true,false)", v, ask, rej)
	}
}

// M35 P0: <USER>/<PASS> для publickey резолвятся в passphrase-user/passphrase.
func TestResolveUserPassPublickey(t *testing.T) {
	cfg := &config.Environment{
		User: "u", Pass: "pw", AuthType: "publickey",
		PassphraseUser: "ru", Passphrase: "ph",
	}
	r := ResolverFromConfig(BuildEnv{}, nil, cfg)
	got := Substitute("<USER>/<PASS>", r)
	if got != "ru/ph" {
		t.Fatalf("publickey <USER>/<PASS> = %q, want \"ru/ph\"", got)
	}
	// userpass — как раньше (<USER>=User, <PASS>=Pass)
	up := &config.Environment{User: "u", Pass: "pw"}
	got = Substitute("<USER>/<PASS>", ResolverFromConfig(BuildEnv{}, nil, up))
	if got != "u/pw" {
		t.Fatalf("userpass <USER>/<PASS> = %q, want \"u/pw\"", got)
	}
}

// M35 P0: mosh publickey — пустой порт не даёт мусорное "--ssh=ssh -p ",
// дефолт 22 без -p, непустой — с -p.
func TestBuildCMDMoshPublickeyPort(t *testing.T) {
	cases := []struct {
		name string
		port string
		want string
	}{
		{"empty", "", "ssh -i '/id'"},
		{"default-22", "22", "ssh -i '/id'"}, // дефолт — без -p (как userpass)
		{"custom-2222", "2222", "ssh -p 2222 -i '/id'"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := &config.Environment{Method: "MOSH", User: "u", IP: "10.0.0.1", Port: c.port,
				AuthType: "publickey", PublicKey: "/id"}
			argv, err := BuildCmd(BuildEnv{}, e, method.VNCGeneric)
			if err != nil {
				t.Fatal(err)
			}
			joined := strings.Join(argv, " ")
			if !strings.Contains(joined, "--ssh="+c.want) {
				t.Fatalf("mosh publickey: %q, хотим содержать %q", joined, "--ssh="+c.want)
			}
		})
	}
}
