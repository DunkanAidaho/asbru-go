package method

import (
	"strconv"
	"strings"
	"testing"
)

// M37 (п.2): галки RDP должны покрывать ВЕСЬ набор легаси
// (PACMethod_xfreerdp.pm:_parseOptionsToCfg) — при переносе потерялись
// sec-опции и часть флагов, без которых подключение не работает.
func TestOptionsForCoversLegacyXFreeRDP(t *testing.T) {
	want := []string{
		"/f", "/admin", "+compression", "+clipboard", "-grab-keyboard", // были
		"/cert-ignore", "-authentication", "-fast-path", "/rfx", "/nsc",
		"/dynamic-resolution", "+fonts",
		"-sec-rdp", "-sec-tls", "-sec-nla", // ← «sec-опции»
	}
	got := map[string]bool{}
	for _, tg := range OptionsFor(XFreeRDP) {
		for _, tk := range tg.Tokens {
			got[tk] = true
		}
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("нет галки для %q", w)
		}
	}
}

// M37: у rdesktop потерялась галка кэша битмапов (-P).
func TestOptionsForCoversLegacyRDesktop(t *testing.T) {
	want := []string{"-f", "-0", "-z", "-K", "-P"}
	got := map[string]bool{}
	for _, tg := range OptionsFor(RDesktop) {
		for _, tk := range tg.Tokens {
			got[tk] = true
		}
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("нет галки для %q", w)
		}
	}
}

// M37: каждая галка обязана быть снимаемой и ставимой (галка ↔ токен в «Опции»).
func TestToggleRoundTripAllDialects(t *testing.T) {
	for _, d := range []Dialect{XFreeRDP, RDesktop, SSH, SFTP, VNC} {
		for _, tg := range OptionsFor(d) {
			on, err := SetToggle("", tg, true)
			if err != nil {
				t.Fatalf("%s/%s: %v", d, tg.Setting, err)
			}
			if !TogglePresent(on, tg) {
				t.Errorf("%s/%s: галка не встала (%q)", d, tg.Setting, on)
			}
			off, err := SetToggle(on, tg, false)
			if err != nil {
				t.Fatalf("%s/%s: %v", d, tg.Setting, err)
			}
			if TogglePresent(off, tg) {
				t.Errorf("%s/%s: галка не снялась (%q)", d, tg.Setting, off)
			}
		}
	}
}

// M37: /tls-seclevel:N — опция-значение (0 = выключено, токена нет).
func TestTLSecLevelValue(t *testing.T) {
	v := tlsSecLevelOption()
	if v.Prefix != "/tls-seclevel:" {
		t.Fatalf("префикс %q", v.Prefix)
	}
	on, err := SetValue("", v, "3")
	if err != nil {
		t.Fatal(err)
	}
	if on != "/tls-seclevel:3" {
		t.Fatalf("включение: %q", on)
	}
	if got := ValuePresent(on, v); got != "3" {
		t.Fatalf("чтение: %q", got)
	}
	// смена значения не дублирует токен
	two, _ := SetValue(on, v, "5")
	if two != "/tls-seclevel:5" {
		t.Fatalf("смена: %q", two)
	}
	// 0 = выключено → токен убирается
	off, _ := SetValue(two, v, "0")
	if strings.Contains(off, "tls-seclevel") {
		t.Fatalf("выключение не убрало токен: %q", off)
	}
	if got := ValuePresent(off, v); got != "" {
		t.Fatalf("выключено, но читается %q", got)
	}
	// прочие токены не трогаются
	keep, _ := SetValue("/bpp:24 +clipboard", v, "2")
	if keep != "/bpp:24 +clipboard /tls-seclevel:2" {
		t.Fatalf("соседние токены: %q", keep)
	}
}

// M37: значения вне набора отклоняются (комбо не даёт мусора в «Опции»).
func TestTLSecLevelRejectsUnknown(t *testing.T) {
	if _, err := SetValue("", tlsSecLevelOption(), "9"); err == nil {
		t.Fatal("значение 9 принято")
	}
}

// M37: токены галок не должны АЛИАСИТЬ на короткие ключи и портить чужие
// настройки (rdpKey усекал неизвестный длинный флаг до tok[:2]: «-sec-nla»→«-s»
// (shell), «-authentication»→«-a» (bpp), «-fast-path»→«-f» (fullscreen)).
func TestToggleTokensDoNotPoisonSettings(t *testing.T) {
	for _, d := range []Dialect{RDesktop, XFreeRDP} {
		for _, tg := range OptionsFor(d) {
			settings, unknown := MapRdpOptions(JoinArgs(tg.Tokens))
			if v, ok := settings["bpp"]; ok {
				if _, err := strconv.Atoi(v); err != nil {
					t.Errorf("%s/%s: bpp испорчен (%q) токенами %v (unknown=%v)",
						d, tg.Setting, v, tg.Tokens, unknown)
				}
			}
			for _, k := range []string{"kbd", "shell", "domain", "size", "fullscreen", "redirect"} {
				if v, ok := settings[k]; ok && strings.HasPrefix(v, "-") {
					t.Errorf("%s/%s: %s испорчен (%q) токенами %v", d, tg.Setting, k, v, tg.Tokens)
				}
			}
			// Алиасинг ловится по «хвосту» токена: длинный флаг, усечённый до
			// короткого ключа, отдаёт значением свой же остаток («-sec-nla» →
			// shell="ec-nla"). Без этой проверки ловился только bpp.
			for _, tk := range tg.Tokens {
				if !strings.HasPrefix(tk, "-") || len(tk) <= 2 {
					continue
				}
				tail := tk[2:]
				for k, v := range settings {
					if v == tail {
						t.Errorf("%s/%s: токен %q алиасится в %s=%q", d, tg.Setting, tk, k, v)
					}
				}
			}
		}
	}
}

// M39 / решение пользователя: галки VNC, которые встроенный клиент не читает,
// не должны показываться в редакторе — иначе они врут. Встроенному VNC опции не
// передаются ВООБЩЕ (vncview.Start получает host/port/user/pass/title/security,
// internal/embed.VNCParamsFrom опции не смотрит), поэтому мертвы обе галки —
// и viewonly, и fullscreen.
func TestVNCHasNoDeadToggles(t *testing.T) {
	for _, tg := range OptionsFor(VNC) {
		switch tg.Setting {
		case "viewonly", "fullscreen":
			t.Errorf("галка %q мертва для встроенного VNC — не должна показываться", tg.Setting)
		}
	}
}
