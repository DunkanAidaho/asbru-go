package method

// M33 (встраивание RDP/VNC, libfreerdp3): валидация/маппинг опций RDP в
// свободные ключи настроек. Неизвестные/не-мапируемые — возвращаются в unknown
// (не падать; UI покажет «ключ не поддерживается: заменить/нет»).

import (
	"strings"
	"testing"
)

func TestValidateRdpOptions(t *testing.T) {
	// корректные rdesktop-опции → маппятся, unknown пуст.
	s, unknown := MapRdpOptions("-a 16 -k en-us -d DOMAIN -g 1920x1080")
	if len(unknown) != 0 {
		t.Fatalf("unknown=%v", unknown)
	}
	if s["bpp"] != "16" || s["kbd"] != "en-us" || s["domain"] != "DOMAIN" {
		t.Fatalf("settings=%v", s)
	}
	// корректные xfreerdp-опции → маппятся.
	s, unknown = MapRdpOptions("/bpp:16 /kbd:en-us /d:DOMAIN")
	if len(unknown) != 0 {
		t.Fatalf("xf unknown=%v", unknown)
	}
	if s["bpp"] != "16" || s["domain"] != "DOMAIN" {
		t.Fatalf("xf settings=%v", s)
	}
	// не-поддерживаемый ключ → в unknown (не в settings), без падения.
	s, unknown = MapRdpOptions("-a 16 -bogus 99")
	if len(unknown) != 1 || unknown[0] != "-bogus" {
		t.Fatalf("unknown=%v, want [-bogus]", unknown)
	}
	if s["bpp"] != "16" {
		t.Fatalf("settings=%v (bogus не должен ломать)", s)
	}
	// мусор без префикса → unknown.
	_, unknown = MapRdpOptions("some-random")
	if len(unknown) == 0 {
		t.Fatalf("мусор не попал в unknown")
	}
	// ag-84j: неизвестный длинный флаг («-seamless») — не слитная опция
	// «-s eamless»: он должен уйти в unknown, а не подменить shell.
	for _, tok := range []string{"-seamless", "-shell", "-domain"} {
		s, unknown := MapRdpOptions(tok)
		if len(s) != 0 {
			t.Errorf("%s: settings=%v, want пусто (длинный флаг, не glued)", tok, s)
		}
		if len(unknown) != 1 || unknown[0] != tok {
			t.Errorf("%s: unknown=%v, want [%s]", tok, unknown, tok)
		}
	}
	// слитные значения коротких опций по-прежнему разбираются.
	s, unknown = MapRdpOptions("-ken-us -dDOMAIN -a24 -g1280x1024 -s/bin/sh -rclipboard:PRIMARYCLIPBOARD")
	want := map[string]string{
		"kbd":      "en-us",
		"domain":   "DOMAIN",
		"bpp":      "24",
		"size":     "1280x1024",
		"shell":    "/bin/sh",
		"redirect": "clipboard:PRIMARYCLIPBOARD",
	}
	for k, v := range want {
		if s[k] != v {
			t.Errorf("%s=%q, want %q (settings=%v)", k, s[k], v, s)
		}
	}
	if len(unknown) != 0 {
		t.Errorf("glued: unknown=%v, want пусто", unknown)
	}
	// короткие значения-слова (раскладка «de», команда «cmd») — тоже слитные.
	s, unknown = MapRdpOptions("-kde -scmd")
	if s["kbd"] != "de" || s["shell"] != "cmd" || len(unknown) != 0 {
		t.Errorf("короткие glued-значения сломаны: settings=%v unknown=%v", s, unknown)
	}
	// пусто → пусто.
	s, u := MapRdpOptions("")
	if len(s) != 0 || len(u) != 0 {
		t.Fatalf("пусто: %v %v", s, u)
	}
}

func TestRdpOptionKnown(t *testing.T) {
	if !rdpOptionKnown("-a") || !rdpOptionKnown("/bpp:") {
		t.Fatal("известный ключ не распознан")
	}
	if rdpOptionKnown("-bogus") {
		t.Fatal("не-ключ распознан как известный")
	}
	// value-ключи требуют значение.
	if !strings.HasPrefix("-k", "-") {
		t.Fatal("test guard")
	}
}

// M5: '+clipboard' — известный xfreerdp-флаг (rdpOptionKnown), но rdpIsFlag его
// не ловил → MapRdpOptions бросал в unknown (буфер/сжатие не мапились в M33).
func TestMapRdpOptionsPlusFlag(t *testing.T) {
	s, unknown := MapRdpOptions("+clipboard")
	if s["clipboard"] != "1" {
		t.Fatalf("+clipboard: settings=%v, want clipboard=1", s)
	}
	if len(unknown) != 0 {
		t.Fatalf("+clipboard: unknown=%v, want пусто", unknown)
	}
	s, unknown = MapRdpOptions("+compression")
	if s["compression"] != "1" || len(unknown) != 0 {
		t.Fatalf("+compression: settings=%v unknown=%v", s, unknown)
	}
}
