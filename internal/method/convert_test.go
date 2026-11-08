package method

import "testing"

// Ключевой тест — ВОСПРОИЗВЕДЕНИЕ бага rdesktop→xfreerdp.
// Раньше (Perl) эта строка уходила в otherOptions целиком и ломала запуск.
func TestConvertRDPRepro(t *testing.T) {
	in := " -5 -a 24 -k en-us -d DOMAIN -r clipboard:PRIMARYCLIPBOARD"
	out, unknown := ConvertOptions(RDesktop, XFreeRDP, in)
	if out != "/bpp:24 /kbd:en-us /d:DOMAIN +clipboard" {
		t.Fatalf("repro: %q (unknown=%v)", out, unknown)
	}
	if len(unknown) != 0 {
		t.Fatalf("repro: unexpected unknown %v", unknown)
	}
}

// Таблицы направленных правил: rdesktop → xfreerdp.
func TestConvertTableForward(t *testing.T) {
	cases := []struct {
		in, want string
		unknown  int
	}{
		{"", "", 0},
		{"-g 1920x1080 -f -z", "/size:1920x1080 /f +compression", 0},
		{"-0 -s /bin/bash -K", "/admin /shell:/bin/bash -grab-keyboard", 0},
		{"-r sound:sys:0", "/sound:sys:alsa", 0},
		{`-r disk:share="/home/share"`, `/drive:share,/home/share`, 0},
		{"-4 -5 -P -A -r clipboard:CLIPBOARD", "+clipboard", 0},
		{"-m 4x4", "-m 4x4", 2},
		{"-r clipboard:", "", 0},
		{`-s "/bin/bash -l"`, `"/shell:/bin/bash -l"`, 0},
		{`-r disk:share="C:\Program Files"`, `"/drive:share,C:\\Program Files"`, 0},
	}
	for _, c := range cases {
		out, unknown := ConvertOptions(RDesktop, XFreeRDP, c.in)
		if out != c.want {
			t.Errorf("Convert(rd→xf, %q) = %q, want %q", c.in, out, c.want)
			continue
		}
		if len(unknown) != c.unknown {
			t.Errorf("Convert(rd→xf, %q): unknown = %v, want %d записей", c.in, unknown, c.unknown)
		}
	}
}

// Обратный ход: xfreerdp → rdesktop.
func TestConvertTableBackward(t *testing.T) {
	cases := []struct {
		in, want string
		unknown  int
	}{
		{"/bpp:24 /kbd:en-us /d:DOMAIN +clipboard", "-a 24 -k en-us -d DOMAIN -r clipboard:PRIMARYCLIPBOARD", 0},
		{"/size:1920x1080", "-g 1920x1080", 0},
		{"/f +compression /admin", "-f -z -0", 0},
		{"/shell:/bin/bash -grab-keyboard", "-s /bin/bash -K", 0},
		{"/sound:sys:alsa", "-r sound:sys:0", 0},
		{"/drive:share,/home/user", `-r disk:share="/home/user"`, 0},
		{`"/drive:share,C:\Program Files"`, `-r "disk:share=\"C:\\Program Files\""`, 0},
		{"/unknown:foo", "/unknown:foo", 1},
	}
	for _, c := range cases {
		out, unknown := ConvertOptions(XFreeRDP, RDesktop, c.in)
		if out != c.want {
			t.Errorf("Convert(xf→rd, %q) = %q, want %q", c.in, out, c.want)
			continue
		}
		if len(unknown) != c.unknown {
			t.Errorf("Convert(xf→rd, %q): unknown = %v, want %d", c.in, unknown, c.unknown)
		}
	}
}

// Идемпотентность: двойной переход на каноническом входе возвращает исходник.
func TestConvertIdempotentRoundTrip(t *testing.T) {
	canonical := "-a 24 -k en-us -d DOMAIN -r clipboard:PRIMARYCLIPBOARD -g 1920x1080"
	fwd, _ := ConvertOptions(RDesktop, XFreeRDP, canonical)
	back, _ := ConvertOptions(XFreeRDP, RDesktop, fwd)
	if back != canonical {
		t.Fatalf("round-trip: %q (fwd %q)", back, fwd)
	}
}

// MINOR-10: round-trip обязан быть байт-идемпотентен (неподвижная точка) и
// сохранять исходное значение там, где диалект его не теряет: sound:sys:1 →
// себе (не схлопываться в :0), а диск-путь с пробелом — стабилен (g == g2,
// иначе эскейпинг «плывёт» от прохода к проходу).
func TestConvertRoundTripByteIdempotent(t *testing.T) {
	// сохранение значения (нет пробелов → каноническая форма = вход).
	if got, _ := roundTrip(RDesktop, XFreeRDP, "-r sound:sys:1"); got != "-r sound:sys:1" {
		t.Fatalf("sound:sys:1 круг → %q (потерян индекс)", got)
	}

	// неподвижная точка для «грязного» входа (пробел в значении → ре-квот).
	inputs := []string{
		`-r disk:share="C:\Program Files"`,
		"-r clipboard:CLIPBOARD",
		"-r sound:sys:0",
	}
	for _, x := range inputs {
		f, _ := ConvertOptions(RDesktop, XFreeRDP, x)
		g, _ := ConvertOptions(XFreeRDP, RDesktop, f)
		f2, _ := ConvertOptions(RDesktop, XFreeRDP, g)
		g2, _ := ConvertOptions(XFreeRDP, RDesktop, f2)
		if g != g2 {
			t.Errorf("не неподвижная точка: x=%q f=%q g=%q g2=%q", x, f, g, g2)
		}
	}
}

// roundTrip — rd→xf→rd за один вызов.
func roundTrip(from, to Dialect, s string) (string, []string) {
	f, _ := ConvertOptions(from, to, s)
	return ConvertOptions(to, from, f)
}

// ag-84j: неизвестный длинный флаг не должен конвертироваться как слитное
// значение короткой опции («-seamless» ≠ «-s eamless» → /shell:eamless).
func TestConvertLongFlagNotGlued(t *testing.T) {
	out, unknown := ConvertOptions(RDesktop, XFreeRDP, "-seamless")
	if out != "-seamless" {
		t.Fatalf("out = %q, want %q (длинный флаг сохраняется как есть)", out, "-seamless")
	}
	if len(unknown) != 1 || unknown[0] != "-seamless" {
		t.Fatalf("unknown = %v, want [-seamless]", unknown)
	}
}

// Маски <field|uuid> в options не трогаются конвертером.
func TestConvertLeavesMasks(t *testing.T) {
	in := "-k en-us <password|/Group/Entry>"
	out, _ := ConvertOptions(RDesktop, XFreeRDP, in)
	if out != "/kbd:en-us <password|/Group/Entry>" {
		t.Fatalf("mask changed: %q", out)
	}
}

// FuzzConvert — любые входы: без паник, детерминизм.
func FuzzConvertOptions(f *testing.F) {
	f.Add(" -5 -a 24 -k en-us -d DOMAIN -r clipboard:x")
	f.Add(`-r disk:share="/my share"`)
	f.Add("")
	f.Fuzz(func(t *testing.T, s string) {
		out, _ := ConvertOptions(RDesktop, XFreeRDP, s)
		out2, _ := ConvertOptions(RDesktop, XFreeRDP, s)
		if out != out2 {
			t.Fatalf("nondeterministic: %q vs %q", out, out2)
		}
		// обратный проход тоже детерминирован
		back, _ := ConvertOptions(XFreeRDP, RDesktop, s)
		back2, _ := ConvertOptions(XFreeRDP, RDesktop, s)
		if back != back2 {
			t.Fatalf("nondeterministic back: %q vs %q", back, back2)
		}
	})
}
