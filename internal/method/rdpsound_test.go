package method

// M39: звук (rdpsnd) для ВСТРОЕННОГО RDP-клиента. Источник — существующее поле
// «options» соединения в обоих диалектах: xfreerdp `/sound:sys:alsa` (канон
// редактора Ásbrú) и rdesktop `-r sound:local[:backend]` (наследие Perl-конфигов;
// легаси по умолчанию пишет `off`). Разбор должен сохранять легаси-паритет:
// звук включается ТОЛЬКО явным токеном, а нераспознанная/выключенная форма не
// должна молча включить воспроизведение.

import "testing"

func TestParseRdpSound(t *testing.T) {
	tests := []struct {
		name    string
		opts    string
		mode    RdpSoundMode
		backend string
		arg     string // ChannelArg — аргумент статического канала rdpsnd
	}{
		// Нет токена — поведение по умолчанию (FreeRDP объявит sys:fake, звука нет).
		{"пусто", "", RdpSoundUnset, "", ""},
		{"другие опции", "-k en-us +clipboard /size:1280x1024", RdpSoundUnset, "", ""},

		// xfreerdp: канонический синтаксис.
		{"xf alsa", "/sound:sys:alsa", RdpSoundLocal, "alsa", "sys:alsa"},
		{"xf pulse", "/sound:sys:pulse", RdpSoundLocal, "pulse", "sys:pulse"},
		{"xf oss", "/sound:sys:oss", RdpSoundLocal, "oss", "sys:oss"},
		// Доп. поля FreeRDP (dev/format/…) идут через запятую — берём только sys.
		{"xf дополнительные поля", "/sound:sys:alsa,dev:hw:0,format:1", RdpSoundLocal, "alsa", "sys:alsa"},
		{"xf хвостовая запятая", "/sound:sys:pulse,", RdpSoundLocal, "pulse", "sys:pulse"},
		// Регистр бэкенда нормализуется: FreeRDP ищет подсистему по имени.
		{"xf регистр", "/sound:sys:ALSA", RdpSoundLocal, "alsa", "sys:alsa"},
		// `/sound` без значения и `/sound:` — «локально, бэкенд по умолчанию».
		{"xf голый", "/sound", RdpSoundLocal, "", "sys:alsa"},
		{"xf пустое значение", "/sound:", RdpSoundLocal, "", "sys:alsa"},
		// Легаси-формы, которые может встретить конвертированная строка.
		{"xf local", "/sound:local", RdpSoundLocal, "", "sys:alsa"},
		{"xf local:alsa", "/sound:local:alsa", RdpSoundLocal, "alsa", "sys:alsa"},
		{"xf local:pulseaudio", "/sound:local:pulseaudio", RdpSoundLocal, "pulse", "sys:pulse"},
		{"xf off", "/sound:off", RdpSoundOff, "", ""},
		{"xf remote", "/sound:remote", RdpSoundRemote, "", ""},
		// Неизвестная форма — НЕ включаем звук молча (легаси-паритет).
		{"xf мусор", "/sound:bogus", RdpSoundUnset, "", ""},

		// rdesktop: отдельным значением и слитно.
		{"rd local", "-r sound:local", RdpSoundLocal, "", "sys:alsa"},
		{"rd local:alsa", "-r sound:local:alsa", RdpSoundLocal, "alsa", "sys:alsa"},
		{"rd local:pulseaudio", "-r sound:local:pulseaudio", RdpSoundLocal, "pulse", "sys:pulse"},
		{"rd off", "-r sound:off", RdpSoundOff, "", ""},
		{"rd remote", "-r sound:remote", RdpSoundRemote, "", ""},
		{"rd слитно", "-rsound:local:alsa", RdpSoundLocal, "alsa", "sys:alsa"},
		// Канон convert.go: rdesktop `sound:sys:0` <-> xfreerdp `sys:alsa`.
		{"rd sys:0 (канон)", "-r sound:sys:0", RdpSoundLocal, "alsa", "sys:alsa"},
		// Числовой индекс, которого нет в каноне, не превращается в «бэкенд 1».
		{"rd sys:1 (неизвестный индекс)", "-r sound:sys:1", RdpSoundUnset, "", ""},
		{"rd мусор", "-r sound:bogus", RdpSoundUnset, "", ""},
		// Без значения / не sound-редирект — не трогаем.
		{"rd без значения", "-r", RdpSoundUnset, "", ""},
		{"rd другой редирект", "-r clipboard:PRIMARYCLIPBOARD", RdpSoundUnset, "", ""},

		// Несколько токенов: побеждает последний (как у FreeRDP/настроек).
		{"последний выигрывает (off)", "/sound:sys:alsa -r sound:off", RdpSoundOff, "", ""},
		{"последний выигрывает (alsa)", "-r sound:off /sound:sys:alsa", RdpSoundLocal, "alsa", "sys:alsa"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseRdpSound(tt.opts)
			if got.Mode != tt.mode || got.Backend != tt.backend {
				t.Errorf("ParseRdpSound(%q) = {%v, %q}, want {%v, %q}",
					tt.opts, got.Mode, got.Backend, tt.mode, tt.backend)
			}
			if arg := got.ChannelArg(); arg != tt.arg {
				t.Errorf("ParseRdpSound(%q).ChannelArg() = %q, want %q", tt.opts, arg, tt.arg)
			}
		})
	}
}

// `/sound:` — известный ключ: не должен попадать в unknown (UI-предупреждение
// «ключ не поддерживается»), при этом значение извлекается отдельным парсером.
func TestRdpSoundOptionIsKnown(t *testing.T) {
	s, unknown := MapRdpOptions("/sound:sys:alsa /bpp:16")
	if len(unknown) != 0 {
		t.Fatalf("unknown=%v, /sound: должен быть известен", unknown)
	}
	if s["sound"] != "sys:alsa" {
		t.Fatalf("settings[\"sound\"] = %q, want sys:alsa (%v)", s["sound"], s)
	}
	// Голый `/sound` (без значения) — тоже известный ключ.
	_, unknown = MapRdpOptions("/sound")
	if len(unknown) != 0 {
		t.Fatalf("/sound: unknown=%v", unknown)
	}
}

// Имя подсистемы уходит в C-строку (C.CString): NUL или недопустимые символы
// обрезали бы/исказили её. Такие значения не принимаем вовсе.
func TestParseRdpSoundRejectsUnsafeBackend(t *testing.T) {
	for _, opts := range []string{"/sound:sys:al\x00sa", "/sound:sys:../x", "-r sound:local:.."} {
		got := ParseRdpSound(opts)
		if got.Mode != RdpSoundUnset || got.ChannelArg() != "" {
			t.Errorf("ParseRdpSound(%q) = {%v, %q} (arg %q), хотим Unset/пусто",
				opts, got.Mode, got.Backend, got.ChannelArg())
		}
	}
}
