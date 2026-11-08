package embed

import (
	"testing"

	"github.com/DunkanAidaho/asbru-go/internal/config"
)

// RDPParamsFrom: базовые поля + порт-дефолт.
func TestRDPParamsFromBasic(t *testing.T) {
	e := &config.Environment{IP: "10.0.0.5", User: "admin", Pass: "secret"}
	p := RDPParamsFrom(e, "")
	if p.Host != "10.0.0.5" {
		t.Errorf("Host = %q", p.Host)
	}
	if p.Port != rdpDefaultPort {
		t.Errorf("Port = %d (decimal %d)", p.Port, rdpDefaultPort)
	}
	if p.Username != "admin" || p.Password != "secret" {
		t.Errorf("user/pass = %q/%q", p.Username, p.Password)
	}
	if p.BPP != 16 {
		t.Errorf("BPP default = %d", p.BPP)
	}
	if len(p.Unknown) != 0 {
		t.Errorf("Unknown = %#v", p.Unknown)
	}
}

// Очный дебаг «Remmina работает, asbru — Logon failed»: в поле user хранится
// «DOMAIN\user», а RDP ждёт домен отдельным полем. Freerdp с username
// «EXAMPLE\Administrator» и пустым доменом шлёт login «EXAMPLEAdministrator»
// (обратный слеш теряется) → LOGON_FAILURE. Разделяем на домен+пользователя.
func TestRDPParamsSplitDomainUser(t *testing.T) {
	e := &config.Environment{IP: "192.0.2.10", Port: "3389", User: "EXAMPLE\\Administrator"}
	p := RDPParamsFrom(e, "")
	if p.Domain != "EXAMPLE" {
		t.Errorf("Domain = %q, want EXAMPLE", p.Domain)
	}
	if p.Username != "Administrator" {
		t.Errorf("Username = %q, want Administrator", p.Username)
	}
	// без обратного слеша — как есть.
	p2 := RDPParamsFrom(&config.Environment{IP: "x", User: "root"}, "")
	if p2.Domain != "" || p2.Username != "root" {
		t.Errorf("no-backslash: domain/user = %q/%q, want /root", p2.Domain, p2.Username)
	}
	// явный домен в опциях (/d:) НЕ перекрывается split'ом username'а.
	p3 := RDPParamsFrom(&config.Environment{IP: "x", User: "CORP\\admin"}, "/d:EXPLICIT")
	if p3.Domain != "EXPLICIT" {
		t.Errorf("domain option = %q, want EXPLICIT (приоритет опции)", p3.Domain)
	}
	if p3.Username != "admin" {
		t.Errorf("username = %q, want admin", p3.Username)
	}
}

// Shell/Fullscreen парсятся. Fullscreen теперь применяется встроенным клиентом
// (M39-fullscreen: вкладка разворачивается на весь экран), Shell — по-прежнему
// только для внешних методов RDP (в argv клиента).
func TestRDPParamsShellAndFullscreenParsed(t *testing.T) {
	p := RDPParamsFrom(&config.Environment{IP: "h"}, "/shell:/bin/bash /fullscreen")
	if p.Shell != "/bin/bash" {
		t.Errorf("Shell = %q, хотим /bin/bash", p.Shell)
	}
	if !p.Fullscreen {
		t.Error("Fullscreen не извлечён")
	}
}

// RDPParamsFrom: xfreerdp-опции /size, /bpp, /d, /clipboard, /fullscreen.
func TestRDPParamsFromXFREERDP(t *testing.T) {
	e := &config.Environment{IP: "win", Port: "3389"}
	p := RDPParamsFrom(e, "/d:CORP /size:1920x1080 /bpp:32 +clipboard /fullscreen /v:whatever")
	if p.Host != "win" || p.Port != 3389 {
		t.Errorf("host/port = %q/%d", p.Host, p.Port)
	}
	if p.Domain != "CORP" {
		t.Errorf("Domain = %q", p.Domain)
	}
	if p.Width != 1920 || p.Height != 1080 {
		t.Errorf("size = %dx%d", p.Width, p.Height)
	}
	if p.BPP != 32 {
		t.Errorf("BPP = %d", p.BPP)
	}
	if !p.Clipboard || !p.Fullscreen {
		t.Errorf("clipboard=%v fullscreen=%v", p.Clipboard, p.Fullscreen)
	}
	// A1: rdesktop "-r clipboard:…" тоже включает буфер (не только -C/+clipboard).
	pr := RDPParamsFrom(&config.Environment{IP: "h"}, "-r clipboard:PRIMARYCLIPBOARD")
	if !pr.Clipboard {
		t.Errorf("rdesktop -r clipboard: не включает Clipboard")
	}
	// /v: просто проглочен (значение без известного ключа) — не падаем.
	if len(p.Unknown) == 0 {
		t.Errorf("expected unknown tokens for /v:...")
	}
}

// RDPParamsFrom: rdesktop-опции -g / -a / -d слитно и раздельно.
func TestRDPParamsFromRDesktop(t *testing.T) {
	e := &config.Environment{IP: "srv"}
	p := RDPParamsFrom(e, "-d WORK -g 1280x720 -a 8 -0")
	if p.Domain != "WORK" {
		t.Errorf("Domain = %q", p.Domain)
	}
	if p.Width != 1280 || p.Height != 720 {
		t.Errorf("size = %dx%d", p.Width, p.Height)
	}
	if p.BPP != 8 {
		t.Errorf("BPP = %d", p.BPP)
	}
	if !p.Admin {
		t.Errorf("Admin = %v", p.Admin)
	}
}

// RDPParamsFrom: неизвестные/устаревшие ключи → Unknown, без падения (M33-г).
func TestRDPParamsFromUnknownNotCrash(t *testing.T) {
	e := &config.Environment{IP: "h"}
	p := RDPParamsFrom(e, "/size:1280x720 /nosuch /whatever:1 -Z")
	if p.Width != 1280 || p.Height != 720 {
		t.Errorf("size = %dx%d", p.Width, p.Height)
	}
	if len(p.Unknown) == 0 {
		t.Errorf("expected unknown tokens")
	}
}

// RDPParamsFrom: 1080p/720p пресеты, workarea.
func TestRDPParamsFromSizePresets(t *testing.T) {
	e := &config.Environment{IP: "h"}
	if p := RDPParamsFrom(e, "/size:1080p"); p.Width != 1920 || p.Height != 1080 {
		t.Errorf("1080p = %dx%d", p.Width, p.Height)
	}
	if p := RDPParamsFrom(e, "/size:workarea"); p.Width != 0 || p.Height != 0 {
		t.Errorf("workarea = %dx%d", p.Width, p.Height)
	}
}

// VNCParamsFrom: базовые поля + порт-дефолт.
func TestVNCParamsFrom(t *testing.T) {
	e := &config.Environment{IP: "box", User: "u", Pass: "pw"}
	p := VNCParamsFrom(e)
	if p.Host != "box" || p.Password != "pw" || p.Username != "u" {
		t.Errorf("vnc params = %#v", p)
	}
	if p.Port != vncDefaultPort {
		t.Errorf("Port = %d", p.Port)
	}
	if p.Scale != 1 {
		t.Errorf("Scale = %d", p.Scale)
	}
	if p.Security != vncSecurityAuto {
		t.Errorf("Security (дефолт) = %q, хотим %q", p.Security, vncSecurityAuto)
	}
}

// VNCParamsFrom: режим VeNCrypt маппится из env.VNCEncryption; "" и мусор → auto.
func TestVNCParamsFromSecurity(t *testing.T) {
	cases := map[string]string{
		"":           vncSecurityAuto,
		"auto":       vncSecurityAuto,
		"vencrypt":   "vencrypt",
		"none":       "none",
		"NOTAMODE":   vncSecurityAuto,
		"VENCrypt":   vncSecurityAuto, // только точечные значения, регистро-чувствит.
		"  vencrypt": vncSecurityAuto, // нет трима — ведущий пробел = мусор → auto
	}
	for in, want := range cases {
		p := VNCParamsFrom(&config.Environment{VNCEncryption: in})
		if p.Security != want {
			t.Errorf("VNCEncryption %q → Security %q, хотим %q", in, p.Security, want)
		}
	}
}

// M39-fullscreen: полноэкранный режим встроенного VNC из легаси-токенов строки
// options (vncviewer/RealVNC «-fullscreen», TigerVNC «-FullScreen=N»).
func TestVNCParamsFromFullscreen(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"-quality 9 -compresslevel 8", false},
		{"-fullscreen", true},
		{"-FullScreen=1 -QualityLevel 9", true},
		{"-FullScreen=0", false},
	}
	for _, c := range cases {
		p := VNCParamsFrom(&config.Environment{IP: "h", Options: c.in})
		if p.Fullscreen != c.want {
			t.Errorf("options %q → Fullscreen %v, хотим %v", c.in, p.Fullscreen, c.want)
		}
	}
}

// normalizeVNCSecurity: нормализация ошибок/дефолта в auto.
func TestNormalizeVNCSecurity(t *testing.T) {
	if normalizeVNCSecurity("") != vncSecurityAuto {
		t.Error("пустое → auto")
	}
	if normalizeVNCSecurity("auto") != "auto" {
		t.Error("auto → auto")
	}
	if normalizeVNCSecurity("vencrypt") != "vencrypt" {
		t.Error("vencrypt → vencrypt")
	}
	if normalizeVNCSecurity("none") != "none" {
		t.Error("none → none")
	}
	if normalizeVNCSecurity("bogus") != "auto" {
		t.Error("мусор → auto")
	}
}

// portOr: пустой/нечисловой порт → дефолт, числовой → как есть.
func TestPortOr(t *testing.T) {
	if portOr("", 3389) != 3389 {
		t.Error("empty port")
	}
	if portOr("5901", 5900) != 5901 {
		t.Error("numeric port")
	}
	if portOr("abc", 3389) != 3389 {
		t.Error("non-numeric port")
	}
}
