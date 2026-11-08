package session

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/DunkanAidaho/asbru-go/internal/config"
)

// Подстановки значений (<V:>, <GV:>, <ENV:>, <ASK:>, <CMD:>) — порт _subst (asbru_conn:386).
func TestSubstitute(t *testing.T) {
	r := Resolver{
		Vars:  map[string]string{"user": "root", "host": "10.0.0.1"},
		GVars: map[string]string{"gv": "global-value"},
		Env:   func(k string) (string, bool) { return "env-" + k, true },
		Ask:   func(q string) (string, error) { return "asked:" + q, nil },
	}
	in := "<V:user>@<V:host>|<GV:gv>|<ENV:TESTVAR>|<ASK:Пароль?>"
	want := "root@10.0.0.1|global-value|env-TESTVAR|asked:Пароль?"
	if got := Substitute(in, r); got != want {
		t.Fatalf("Substitute = %q, want %q", got, want)
	}
}

// Пустой резолвер = нет источников: маски остаются как есть.
func TestSubstituteLeavesUnknownMask(t *testing.T) {
	r := Resolver{}
	if got := Substitute("<FOO:bar> <V:missing>", r); got != "<FOO:bar> <V:missing>" {
		t.Fatalf("unknown masks должны оставаться: %q", got)
	}
}

// CMD выполняется только при активном резолвере; вывод chomp'ится (ревью H5).
func TestSubstituteCMD(t *testing.T) {
	r := Resolver{Vars: map[string]string{}}
	if got := Substitute("<CMD:echo hi>", r); got != "hi" {
		t.Fatalf("CMD: %q", got)
	}
	// нулевой резолвер — неактивен: даже CMD не запускается
	if got := Substitute("<CMD:echo hi>", Resolver{}); got != "<CMD:echo hi>" {
		t.Fatalf("нулевой резолвер должен оставлять маску: %q", got)
	}
}

// M10a: локальные маски <NAME>/<IP>/<TIMESTAMP> (легаси LOCAL_VARS).
func TestSubstituteLocalVars(t *testing.T) {
	r := Resolver{Local: map[string]string{"NAME": "web1", "IP": "10.0.0.5"}}
	if got := Substitute("<NAME> @ <IP> <TIMESTAMP>", r); got != "web1 @ 10.0.0.5 <TIMESTAMP>" {
		t.Fatalf("local: %q", got)
	}
	if got := Substitute("<NAME>", Resolver{}); got != "<NAME>" {
		t.Fatalf("нулевой резолвер не трогает маски: %q", got)
	}
}

// M10a: GVars из defaults, Vars из variables[N], Env из окружения.
func TestResolverFromConfig(t *testing.T) {
	t.Setenv("ABC_TEST", "E")
	cfg := &config.Config{Defaults: config.Defaults{GlobalVariables: map[string]config.VarEntry{
		"gv1": {Value: "G"},
	}}}
	env := &config.Environment{
		Name: "web", IP: "10.1.1.1", Port: "22", User: "root", Pass: "p",
		Variables: []map[string]any{{"txt": "conn-var"}},
	}
	r := ResolverFromConfig(BuildEnv{UUID: "u1", Name: "web", TmpDir: "/tmp"}, cfg, env)
	got := Substitute("<GV:gv1>|<V:0>|<ENV:ABC_TEST>|<NAME>|<IP>|<PORT>|<UUID>", r)
	if got != "G|conn-var|E|web|10.1.1.1|22|u1" {
		t.Fatalf("resolver: %q", got)
	}
}

// M10a: поля команды подставляются перед сборкой argv (копия, оригинал не тронут).
func TestSessionSubstBeforeBuild(t *testing.T) {
	cfg := &config.Environment{Name: "s", IP: "10.0.0.1", Port: "22", User: "root", Pass: "x",
		Method: "SSH", Options: "-P <ENV:PORT>"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, err := New(ctx, BuildEnv{UUID: "u1"}, cfg, WithSubstitute(Resolver{
		Env: func(string) (string, bool) { return "2222", true },
	}), WithStreamFactory(func() (io.ReadWriteCloser, error) { return &pipeStream{}, nil }))
	if err != nil {
		t.Fatal(err)
	}
	prepared, _ := s.preparedEnv()
	if prepared.Options != "-P 2222" || prepared.IP != "10.0.0.1" {
		t.Fatalf("prepared: %+v", prepared.Options)
	}
	if cfg.Options != "-P <ENV:PORT>" {
		t.Fatalf("оригинал изменён: %q", cfg.Options)
	}
}

// Finding #9: RefreshConfig должен обновлять и env-зависимую часть резолвера
// (LOCAL_VARS/variables), иначе после edit→Restart макросы подставляют старые
// значения. GVars/Env/Ask-источники сохраняются.
func TestRefreshConfigUpdatesResolver(t *testing.T) {
	env := &config.Environment{Name: "old", IP: "<V:0>", Method: "SSH",
		Variables: []map[string]any{{"txt": "10.0.0.1"}}}
	s := &Session{
		env: BuildEnv{UUID: "u1"},
		cfg: env,
		opts: sessionOptions{resolver: Resolver{
			Vars:  map[string]string{"0": "10.0.0.1"},
			Local: map[string]string{"UUID": "u1"},
			Env:   func(string) (string, bool) { return "ENVVAL", true },
		}},
	}
	if p, _ := s.preparedEnv(); p.IP != "10.0.0.1" {
		t.Fatalf("стартовое значение: %q", p.IP)
	}
	s.RefreshConfig(&config.Environment{Name: "new", IP: "<V:0>", Method: "SSH",
		Variables: []map[string]any{{"txt": "10.0.0.2"}}})
	p, err := s.preparedEnv()
	if err != nil {
		t.Fatal(err)
	}
	if p.IP != "10.0.0.2" {
		t.Fatalf("после RefreshConfig подставлено %q, хотим 10.0.0.2", p.IP)
	}
	if p.Name != "new" {
		t.Fatalf("NAME не обновлён: %q", p.Name)
	}
	// не-env источники сохранены
	if got := Substitute("<ENV:X>", s.resolver()); got != "ENVVAL" {
		t.Fatalf("Env-источник потерян: %q", got)
	}
}

// ag-vl1: RefreshConfig не должен мутировать карты уже опубликованного
// резолвера — читатели держат копию структуры Resolver и читают Local/Vars
// после отпускания RLock (fatal: concurrent map read/write).
func TestRefreshConfigPublishesFreshResolverMaps(t *testing.T) {
	s := &Session{
		env: BuildEnv{UUID: "u1"},
		cfg: &config.Environment{Name: "old", Method: "SSH"},
		opts: sessionOptions{resolver: Resolver{
			Vars:  map[string]string{"0": "old-var"},
			Local: map[string]string{"NAME": "old", "UUID": "u1"},
		}},
	}
	published := s.resolver() // снимок, который читают «в полёте» горутины
	oldLocal, oldVars := published.Local, published.Vars
	s.RefreshConfig(&config.Environment{Name: "new", Method: "SSH",
		Variables: []map[string]any{{"txt": "new-var"}}})

	if got := oldLocal["NAME"]; got != "old" {
		t.Fatalf("опубликованная карта Local мутирована: NAME = %q, хотим old", got)
	}
	if got := oldVars["0"]; got != "old-var" {
		t.Fatalf("опубликованная карта Vars мутирована: Vars[0] = %q, хотим old-var", got)
	}
	// Новый снимок видит свежие значения.
	fresh := s.resolver()
	if got := fresh.Local["NAME"]; got != "new" {
		t.Fatalf("после RefreshConfig NAME = %q, хотим new", got)
	}
	if got := fresh.Vars["0"]; got != "new-var" {
		t.Fatalf("после RefreshConfig Vars[0] = %q, хотим new-var", got)
	}
}

// ag-vl1: гонка «RefreshConfig vs Substitute» — репро из отчёта. Под -race
// ловит data race; без него — возможный fatal concurrent map read/write.
func TestRefreshConfigConcurrentWithSubstitute(t *testing.T) {
	s := &Session{
		env: BuildEnv{UUID: "u1"},
		cfg: &config.Environment{Name: "old", IP: "10.0.0.1", Method: "SSH",
			Variables: []map[string]any{{"txt": "10.0.0.1"}}},
		opts: sessionOptions{resolver: Resolver{
			Vars:  map[string]string{"0": "10.0.0.1"},
			Local: map[string]string{"NAME": "old", "UUID": "u1", "IP": "10.0.0.1"},
		}},
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
				_ = Substitute("<NAME>|<UUID>|<IP>|<V:0>", s.resolver())
			}
		}
	}()
	for i := 0; i < 2000; i++ {
		s.RefreshConfig(&config.Environment{Name: "new", IP: "10.0.0.2", Method: "SSH",
			Variables: []map[string]any{{"txt": "10.0.0.2"}}})
	}
	close(stop)
	<-done
}

// Finding #8: proxy/jump поля тоже проходят подстановки (легаси
// asbru_conn:249-256 — subst для proxy ip/user/pass и jump ip/user/pass).
func TestPreparedEnvSubstitutesProxyJumpFields(t *testing.T) {
	env := &config.Environment{
		Name: "s", Method: "SSH",
		ProxyIP: "<V:proxy>", ProxyPort: "<V:pport>", ProxyUser: "<V:puser>", ProxyPass: "<V:ppass>",
		JumpIP: "<V:jump>", JumpUser: "<V:juser>", JumpPass: "<V:jpass>", JumpKey: "<V:jkey>",
	}
	s := &Session{cfg: env, opts: sessionOptions{resolver: Resolver{Vars: map[string]string{
		"proxy": "10.0.0.1", "pport": "1080", "puser": "pu", "ppass": "pp",
		"jump": "jmp.example", "juser": "ju", "jpass": "jp", "jkey": "/tmp/key",
	}}}}
	p, err := s.preparedEnv()
	if err != nil {
		t.Fatal(err)
	}
	if p.ProxyIP != "10.0.0.1" || p.ProxyPort != "1080" || p.ProxyUser != "pu" || p.ProxyPass != "pp" {
		t.Fatalf("proxy не подставлен: %+v", p)
	}
	if p.JumpIP != "jmp.example" || p.JumpUser != "ju" || p.JumpPass != "jp" || p.JumpKey != "/tmp/key" {
		t.Fatalf("jump не подставлен: %+v", p)
	}
	// оригинал не мутируется
	if env.ProxyIP != "<V:proxy>" || env.JumpIP != "<V:jump>" {
		t.Fatalf("оригинал изменён: %+v", env)
	}
}

// M2 (ревью): ResolverFromConfig с nil-окружением не падает.
func TestResolverFromConfigNilEnv(t *testing.T) {
	r := ResolverFromConfig(BuildEnv{UUID: "u1"}, &config.Config{}, nil)
	got := Substitute("<NAME>|<PASS>", r)
	if got != "<NAME>|<PASS>" {
		t.Fatalf("nil-env: %q", got)
	}
}

// M2 (ревью): TIMESTAMP/DATE_*/TIME_* заполняются (легаси time/strftime);
// SOCKS5_PORT без туннеля — пустой.
func TestSubstituteDateMacros(t *testing.T) {
	cfg := &config.Config{}
	env := &config.Environment{Name: "w"}
	r := ResolverFromConfig(BuildEnv{UUID: "u1"}, cfg, env)
	got := Substitute("<TIMESTAMP>|<DATE_Y>|<DATE_M>|<DATE_D>|<TIME_H>|<TIME_M>|<TIME_S>|<SOCKS5_PORT>", r)
	p := strings.Split(got, "|")
	if len(p) != 8 || !regexp.MustCompile(`^\d+$`).MatchString(p[0]) {
		t.Fatalf("макросы времени: %q", got)
	}
	if len(p[1]) != 4 || len(p[2]) != 2 || len(p[6]) != 2 {
		t.Fatalf("DATE/TIME: %q", got)
	}
	if p[7] != "" {
		t.Fatalf("SOCKS5_PORT должен быть пуст: %q", got)
	}
}

// M35 P0: неразрешённые <V:>/<GV:>/<ENV:> оставляются литералом (как Perl
// _subst: замена ТОЛЬКО если переменная определена), а не стираются в «».
func TestSubstituteUnresolvedLiteral(t *testing.T) {
	r := Resolver{
		Vars:  map[string]string{"known": "v"},
		GVars: map[string]string{"gknown": "gv"},
		Env: func(k string) (string, bool) {
			if k == "ABC_SET" {
				return "1", true
			}
			return "", false
		},
	}
	got := Substitute("<V:known>|<GV:gknown>|<ENV:ABC_SET>|<V:missing>|<GV:missing>|<ENV:NOPE>", r)
	want := "v|gv|1|<V:missing>|<GV:missing>|<ENV:NOPE>"
	if got != want {
		t.Fatalf("Substitute = %q, want %q (неразрешённые — литерал)", got, want)
	}
}

// M36-ревью MINOR: SubstituteValues подставляет только значения (V/GV/ENV/local),
// НЕ исполняет <CMD:> (иначе «вставить значение» шеллило бы на клиенте).
func TestSubstituteValuesNoCmdExec(t *testing.T) {
	r := Resolver{Vars: map[string]string{"0": "hello"}, GVars: map[string]string{"k": "gv"}, Env: os.LookupEnv}
	in := "<V:0> <GV:k> <CMD:echo SHOULD_NOT_RUN>"
	got := SubstituteValues(in, r)
	if !strings.Contains(got, "hello") || !strings.Contains(got, "gv") {
		t.Fatalf("V/GV не подставлены: %q", got)
	}
	// <CMD:> остаётся ЛИТЕРАЛОМ (не исполнен): его текст целиком присутствует.
	if !strings.Contains(got, "<CMD:echo SHOULD_NOT_RUN>") {
		t.Fatalf("<CMD:> исполнен или изменён: %q", got)
	}
	// а обычный Substitute исполняет <CMD:> (полная семантика).
	_ = Substitute(in, r)
}

// ag-9tj: репро из бага — легаси-конфиг `variables: [42]` после нормализации
// даёт не-string txt, и <V:0> молча остаётся литералом (Perl стрингифицирует).
func TestResolverFromConfigLegacyNonStringScalar(t *testing.T) {
	path := filepath.Join(t.TempDir(), "asbru.yml")
	src := `
environments:
  u1:
    name: legacy
    method: SSH
    variables:
      - 42
    macros:
      - true
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadConfig(path, nil)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	env, ok := cfg.Environments["u1"]
	if !ok {
		t.Fatal("окружение u1 не загружено")
	}
	r := ResolverFromConfig(BuildEnv{UUID: "u1", Name: "legacy"}, cfg, &env)
	if got := Substitute("<V:0>", r); got != "42" {
		t.Fatalf("Substitute(<V:0>) = %q, want %q", got, "42")
	}
}
