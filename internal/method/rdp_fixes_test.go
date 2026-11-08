package method

// Регрессионные тесты на находки ревью: варианты rdesktop-буфера обмена,
// слитные значения коротких опций rdesktop, пустые квотированные аргументы.

import (
	"strings"
	"testing"
)

func rdpClipboardToggle(t *testing.T) OptionToggle {
	t.Helper()
	for _, o := range OptionsFor(RDesktop) {
		if o.Setting == "clipboard" {
			return o
		}
	}
	t.Fatal("rdesktop clipboard toggle не найден")
	return OptionToggle{}
}

// MINOR: галка буфера распознавала только точную пару
// {"-r","clipboard:PRIMARYCLIPBOARD"}; прочие селекции и слитная форма не
// удалялись, а повторное включение добавляло дубль.
func TestRdpClipboardVariants(t *testing.T) {
	cb := rdpClipboardToggle(t)
	variants := []string{
		"-r clipboard:PRIMARYCLIPBOARD",
		"-r clipboard:CLIPBOARD",
		"-r clipboard:",
		"-rclipboard:CLIPBOARD", // слитная форма
	}
	for _, opts := range variants {
		if !TogglePresent(opts, cb) {
			t.Errorf("TogglePresent не видит буфер в %q", opts)
			continue
		}
		off, err := SetToggle(opts, cb, false)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(off, "clipboard") {
			t.Errorf("выключение %q оставило буфер: %q", opts, off)
		}
		on, err := SetToggle(opts, cb, true)
		if err != nil {
			t.Fatal(err)
		}
		if n := strings.Count(on, "clipboard"); n != 1 {
			t.Errorf("включение %q дало %d буферов: %q", opts, n, on)
		}
	}
}

// Буфер удаляется, не задевая другие -r-редиректы.
func TestRdpClipboardVariantsKeepOtherRedirects(t *testing.T) {
	cb := rdpClipboardToggle(t)
	base := "-r sound:sys:0 -r clipboard:CLIPBOARD -r disk:share=/tmp"
	off, err := SetToggle(base, cb, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(off, "sound:sys:0") || !strings.Contains(off, "disk:share=/tmp") {
		t.Fatalf("потеряны другие редиректы: %q", off)
	}
	if strings.Contains(off, "clipboard") {
		t.Fatalf("буфер остался: %q", off)
	}
}

// MINOR: слитные значения коротких опций ("-dDOMAIN", "-ken-us") считались
// длинными флагами и не мапились.
func TestMapRdpOptionsGluedValues(t *testing.T) {
	s, unknown := MapRdpOptions("-dDOMAIN -ken-us -a24 -g1280x1024")
	if s["domain"] != "DOMAIN" || s["kbd"] != "en-us" || s["bpp"] != "24" || s["size"] != "1280x1024" {
		t.Fatalf("слитные значения не смапились: %#v", s)
	}
	if len(unknown) != 0 {
		t.Fatalf("лишние unknown: %v", unknown)
	}
	// длинные флаги по-прежнему не усекаются до коротких.
	if s, _ := MapRdpOptions("-grab-keyboard"); s["size"] != "" || s["grab-keyboard"] != "1" {
		t.Fatalf("-grab-keyboard сломан: %#v", s)
	}
}

// Слитные значения конвертируются в xfreerdp-диалект.
func TestConvertGluedShortValues(t *testing.T) {
	out, unknown := ConvertOptions(RDesktop, XFreeRDP, "-dDOMAIN -ken-us -a24")
	if out != "/d:DOMAIN /kbd:en-us /bpp:24" {
		t.Fatalf("Convert = %q, want %q", out, "/d:DOMAIN /kbd:en-us /bpp:24")
	}
	if len(unknown) != 0 {
		t.Fatalf("unexpected unknown: %v", unknown)
	}
}

// MINOR: SplitArgs терял явные пустые квотированные аргументы, из-за чего
// "-d \"\"" после round-trip превращалось в "-d".
func TestSplitArgsPreservesEmptyQuoted(t *testing.T) {
	got := SplitArgs(`-d ""`)
	if len(got) != 2 || got[0] != "-d" || got[1] != "" {
		t.Fatalf(`SplitArgs(-d "") = %#v, want ["-d" ""]`, got)
	}
	joined := JoinArgs(got)
	if joined != `-d ""` {
		t.Fatalf("JoinArgs = %q, want %q", joined, `-d ""`)
	}
	round := SplitArgs(joined)
	if len(round) != 2 || round[1] != "" {
		t.Fatalf("round-trip потерял пустой аргумент: %#v", round)
	}
	// пустые одинарные кавычки тоже явный аргумент.
	if q := SplitArgs(`-d ''`); len(q) != 2 || q[1] != "" {
		t.Fatalf("single-quoted empty = %#v", q)
	}
}
