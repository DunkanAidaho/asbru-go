package session

import (
	"strings"
	"testing"

	"github.com/DunkanAidaho/asbru-go/internal/config"
	"github.com/DunkanAidaho/asbru-go/internal/method"
)

func proxyBE(t *testing.T, d *config.Defaults) BuildEnv {
	t.Helper()
	return BuildEnv{UUID: "u1", Name: "test", TmpDir: t.TempDir(), Defaults: d}
}

// M35 P0: use proxy=0 — использовать глобальные defaults-proxy ('Proxy':
// SOCKS через defaults proxy ip/port). Легаси asbru_conn:825 ('Use global
// configuration settings' → defaults.proxy eq 'Proxy').
func TestBuildCmdUseProxy0DefaultsProxy(t *testing.T) {
	d := &config.Defaults{Proxy: "Proxy", ProxyIP: "10.0.0.1", ProxyPort: "1080",
		ProxyUser: "puser", ProxyPass: "ppass"}
	e := &config.Environment{Name: "x", Method: "SSH", User: "u", IP: "10.0.0.9"}
	argv, err := BuildCmd(proxyBE(t, d), e, method.VNCGeneric)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(argv, " ")
	// пароль — через 0600-файл: в argv НЕ должно быть самого "ppass", но есть
	// $(cat <путь>) + все поля shellQuote (инъекция закрыта).
	if !strings.Contains(joined, "ProxyCommand=ncat --proxy '10.0.0.1':'1080' --proxy-type socks5 --proxy-auth 'puser':\"$(cat ") {
		t.Fatalf("defaults-Proxy нет ncat c auth-файлом: %v", argv)
	}
	if strings.Contains(joined, "ppass") {
		t.Fatalf("пароль прокси утёк в argv: %v", argv)
	}
	// без auth — nc (кавычки вокруг полей)
	d2 := &config.Defaults{Proxy: "Proxy", ProxyIP: "10.0.0.2", ProxyPort: "1081"}
	argv, err = BuildCmd(proxyBE(t, d2), e, method.VNCGeneric)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(argv, " "), "ProxyCommand=nc -x '10.0.0.2':'1081' %h %p") {
		t.Fatalf("defaults-Proxy без auth — nc: %v", argv)
	}
}

// M35 P0: use proxy=0, defaults.proxy='Jump' — -J с defaults jump полями.
func TestBuildCmdUseProxy0DefaultsJump(t *testing.T) {
	d := &config.Defaults{Proxy: "Jump", JumpIP: "10.0.0.2", JumpPort: 22, JumpUser: "ju"}
	e := &config.Environment{Name: "x", Method: "SSH", User: "u", IP: "10.0.0.9"}
	argv, err := BuildCmd(proxyBE(t, d), e, method.VNCGeneric)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "-J ju@10.0.0.2:22") {
		t.Fatalf("defaults-Jump нет -J: %v", argv)
	}
	// sftp — Proxyjump=
	e2 := &config.Environment{Name: "x", Method: "SFTP", User: "u", IP: "10.0.0.9"}
	argv, err = BuildCmd(proxyBE(t, d), e2, method.VNCGeneric)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(argv, " "), "Proxyjump=ju@10.0.0.2:22") {
		t.Fatalf("defaults-Jump sftp — Proxyjump: %v", argv)
	}
}

// M35 P0: use proxy=1 с ProxyUser/Pass — auth через ncat. (раньше — всегда nc,
// auth игнорировался).
func TestBuildCmdProxyAuth(t *testing.T) {
	e := &config.Environment{Name: "x", Method: "SSH", User: "u", IP: "10.0.0.9",
		UseProxy: 1, ProxyIP: "10.0.0.5", ProxyPort: "1080", ProxyUser: "pu", ProxyPass: "pp"}
	argv, err := BuildCmd(proxyBE(t, nil), e, method.VNCGeneric)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "ProxyCommand=ncat --proxy '10.0.0.5':'1080' --proxy-type socks5 --proxy-auth 'pu':\"$(cat ") {
		t.Fatalf("proxy auth: %v", argv)
	}
	if strings.Contains(joined, ":pp\"") { // пароль НЕ в argv
		t.Fatalf("пароль прокси утёк в argv: %v", argv)
	}
	// без user — nc (как раньше)
	e2 := &config.Environment{Name: "x", Method: "SSH", User: "u", IP: "10.0.0.9",
		UseProxy: 1, ProxyIP: "10.0.0.5", ProxyPort: "1080"}
	argv, err = BuildCmd(proxyBE(t, nil), e2, method.VNCGeneric)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(argv, " "), "ProxyCommand=nc -x '10.0.0.5':'1080' %h %p") {
		t.Fatalf("proxy без auth — nc: %v", argv)
	}
}

// M35-ревью MAJOR (безопасность): значение из конфига с метасимволами не
// должно разорвать ProxyCommand (ssh исполняет через /bin/sh -c) и пароль не
// должен попасть в argv. Поля экранируются; инъекция закрыта.
func TestBuildCmdProxyInjectionQuoted(t *testing.T) {
	e := &config.Environment{Name: "x", Method: "SSH", User: "u", IP: "10.0.0.9",
		UseProxy: 1, ProxyIP: "10.0.0.5; rm -rf /", ProxyPort: "1080", ProxyUser: "pu;id", ProxyPass: "pp\" ; id"}
	argv, err := BuildCmd(proxyBE(t, nil), e, method.VNCGeneric)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(argv, " ")
	// метасимволы из конфига — в одиночных кавычках (не исполняемые операторы sh)
	if !strings.Contains(joined, "--proxy '10.0.0.5; rm -rf /':'1080'") {
		t.Fatalf("ip с метасимволом не кавычен: %v", argv)
	}
	if !strings.Contains(joined, "--proxy-auth 'pu;id':\"$(cat ") {
		t.Fatalf("user с метасимволом не кавычен: %v", argv)
	}
	// гарантия «в кавычках»: токен с метасимволом закрывается апострофом
	// (rm -rf /'), а не остаётся голым разделителем. Проверяем явно.
	if !strings.Contains(joined, "rm -rf /'") {
		t.Fatalf("ip с ';' не закрыт кавычкой: %v", argv)
	}
	// пароль — только в 0600-файле, не в argv
	if strings.Contains(joined, ":pp\" ; id") {
		t.Fatalf("пароль утёк в argv: %v", argv)
	}
}

// M35 P0: use proxy=0 и defaults.proxy пуст (или defaults nil) — no-op.
func TestBuildCmdUseProxy0NoDefaults(t *testing.T) {
	e := &config.Environment{Name: "x", Method: "SSH", User: "u", IP: "10.0.0.9", UseProxy: 0}
	argv, err := BuildCmd(proxyBE(t, nil), e, method.VNCGeneric)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(argv, " ")
	if strings.Contains(joined, "ProxyCommand") || strings.Contains(joined, "-J") {
		t.Fatalf("use proxy=0 без defaults — должен быть no-op: %v", argv)
	}
	// defaults.proxy='' тоже no-op
	argv, err = BuildCmd(proxyBE(t, &config.Defaults{}), e, method.VNCGeneric)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(argv, " "), "ProxyCommand") || strings.Contains(strings.Join(argv, " "), "-J") {
		t.Fatalf("defaults.proxy пуст — no-op: %v", argv)
	}
}
