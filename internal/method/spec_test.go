package method

import "testing"

// M37 (п.4): единый дескриптор «метод → клиент → опции → разделы редактора».
// RDP/VNC рисуются встроенным клиентом внутри вкладки, остальное — внешний
// процесс в PTY.
func TestSpecClientKind(t *testing.T) {
	embedded := []Dialect{RDesktop, XFreeRDP, VNC}
	for _, d := range embedded {
		if got := SpecFor(d).Client; got != ClientEmbedded {
			t.Errorf("%s: клиент %v, хотим ClientEmbedded", d, got)
		}
	}
	external := []Dialect{SSH, SFTP, Telnet, FTP, WebDAV, MOSH, Generic, LocalShell, IBM3270}
	for _, d := range external {
		if got := SpecFor(d).Client; got != ClientExternal {
			t.Errorf("%s: клиент %v, хотим ClientExternal", d, got)
		}
	}
}

// M37: «Безопасность VNC» — только для VNC (для RDP показывалась зря).
func TestSpecVNCSecurityOnlyForVNC(t *testing.T) {
	for _, d := range AllDialects() {
		want := d == VNC
		if got := SpecFor(d).VNCSecurity; got != want {
			t.Errorf("%s: VNCSecurity=%v, хотим %v", d, got, want)
		}
	}
}

// M37: терминально-командные поля — только командным методам.
func TestSpecCommandable(t *testing.T) {
	for _, d := range []Dialect{RDesktop, XFreeRDP, VNC} {
		if SpecFor(d).Commandable {
			t.Errorf("%s: командные поля показываться не должны", d)
		}
	}
	for _, d := range []Dialect{SSH, Telnet, Generic, LocalShell} {
		if !SpecFor(d).Commandable {
			t.Errorf("%s: командные поля обязаны быть", d)
		}
	}
}

// M37: дескриптор — ЕДИНЫЙ источник: галки/значения/auth-типы берутся из
// реестров, иначе редактор и сессия разъедутся.
func TestSpecAggregatesRegistries(t *testing.T) {
	for _, d := range AllDialects() {
		s := SpecFor(d)
		if len(s.Flags) != len(OptionsFor(d)) {
			t.Errorf("%s: флагов %d, в реестре %d", d, len(s.Flags), len(OptionsFor(d)))
		}
		if len(s.Values) != len(OptionValuesFor(d)) {
			t.Errorf("%s: значений %d, в реестре %d", d, len(s.Values), len(OptionValuesFor(d)))
		}
		if len(s.AuthTypes) != len(FeaturesFor(d).AuthTypes) {
			t.Errorf("%s: auth-типов %d, в реестре %d", d, len(s.AuthTypes), len(FeaturesFor(d).AuthTypes))
		}
	}
}

// M37: у каждого диалекта реестра есть заполненный дескриптор.
func TestSpecExistsForAllDialects(t *testing.T) {
	all := AllDialects()
	if len(all) != len(displayNames) || len(all) < 14 {
		t.Fatalf("диалектов %d (реестр %d) — обрезан?", len(all), len(displayNames))
	}
	for _, d := range all {
		if SpecFor(d).Dialect != d {
			t.Errorf("%s: дескриптор не заполнен", d)
		}
	}
}

// M37: AllDialects детерминирован (карта иначе даёт произвольный порядок).
func TestAllDialectsDeterministic(t *testing.T) {
	a, b := AllDialects(), AllDialects()
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("порядок нестабилен: %v vs %v", a, b)
		}
	}
}

// M37: RDP-диалектам sec-галки обязаны быть доступны (галки — из спеки).
func TestSpecRDPHasSecFlags(t *testing.T) {
	for _, d := range []Dialect{RDesktop, XFreeRDP} {
		got := map[string]bool{}
		for _, f := range SpecFor(d).Flags {
			for _, tk := range f.Tokens {
				got[tk] = true
			}
		}
		if d == XFreeRDP {
			for _, want := range []string{"-sec-rdp", "-sec-tls", "-sec-nla"} {
				if !got[want] {
					t.Errorf("%s: нет галки %s", d, want)
				}
			}
		}
	}
}
