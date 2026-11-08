package method

// M39: звук (rdpsnd) для ВСТРОЕННОГО RDP-клиента.
//
// Источник настройки — существующее поле соединения «options» (новых полей
// конфига нет). Поддерживаются оба диалекта, встречающиеся в конфигах Ásbrú:
//
//	/sound:sys:<backend>   (xfreerdp; канон редактора Ásbrú — /sound:sys:alsa)
//	-r sound:<local|off|remote|local:<backend>|sys:<n>>   (rdesktop)
//
// Легаси-паритет (PACMethod_xfreerdp.pm:232, PACMethod_rdesktop.pm:186):
// по умолчанию звук ВЫКЛЮЧЕН (`redirSound = 0` у xfreerdp, `off` у rdesktop).
// Поэтому здесь мы ничего не включаем, пока в options нет явного токена, а
// нераспознанная форма токена не «угадывается» в бэкенд — иначе звук включился
// бы там, где пользователь его не просил.
//
// Регистрация реального бэкенда — в C++ (internal/ui/wgt/rdpview): Go отдаёт
// ChannelArg (например, "sys:alsa"), а C++ проверяет наличие подсистемы через
// freerdp_load_channel_addin_entry и при её отсутствии оставляет дефолтный
// sys:fake, чтобы не сорвать connect (load_addins на неразрешимом аддине
// возвращает FALSE и убивает всё подключение).

import "strings"

// RdpSoundMode — режим перенаправления звука, запрошенный в options.
type RdpSoundMode int

const (
	// RdpSoundUnset — токена нет (или форма не распознана): поведение по
	// умолчанию, то есть без звука (FreeRDP объявит sys:fake).
	RdpSoundUnset RdpSoundMode = iota
	// RdpSoundOff — звук явно выключен (`/sound:off`, `-r sound:off`).
	RdpSoundOff
	// RdpSoundRemote — воспроизведение на стороне сервера (`remote`).
	// Встроенный клиент локально не воспроизводит звук: для него это то же,
	// что «без звука» (ChannelArg пуст). Полноценный AUDIO_MODE_PLAY_ON_SERVER
	// не выставлен — документированное ограничение (PLAN.md, M39/sound).
	RdpSoundRemote
	// RdpSoundLocal — перенаправление на локальное устройство.
	RdpSoundLocal
)

// rdpSoundDefaultBackend — канонический бэкенд для `local` без явной
// подсистемы. `alsa` — тот же канон, что в convert.go (rdesktop
// `sound:sys:0` <-> xfreerdp `/sound:sys:alsa`, легаси-хардкод).
const rdpSoundDefaultBackend = "alsa"

// RdpSound — разобранное звуковое предпочтение соединения.
type RdpSound struct {
	Mode RdpSoundMode
	// Backend — имя подсистемы FreeRDP (alsa/pulse/oss/sndio/fake/…) для
	// RdpSoundLocal. Пусто — бэкенд по умолчанию (rdpSoundDefaultBackend).
	// Имя не проверяется на существование: наличие подсистемы в текущей сборке
	// проверяет C++ (freerdp_load_channel_addin_entry) перед регистрацией канала.
	Backend string
}

// ChannelArg — аргумент статического канала rdpsnd ("sys:<backend>") либо ""
// (канал не регистрируем — остаётся дефолтный sys:fake, то есть без звука).
func (s RdpSound) ChannelArg() string {
	if s.Mode != RdpSoundLocal {
		return ""
	}
	backend := s.Backend
	if backend == "" {
		backend = rdpSoundDefaultBackend
	}
	return "sys:" + backend
}

// ParseRdpSound извлекает звуковую настройку из строки опций RDP. Порядок
// токенов сохраняется; при нескольких токенах побеждает последний (как у
// FreeRDP и остальных настроек ввода). Нераспознанная форма токена даёт
// RdpSoundUnset — звук не включается.
func ParseRdpSound(options string) RdpSound {
	tokens := SplitArgs(options)
	var out RdpSound
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		switch {
		case tok == "/sound" || strings.HasPrefix(tok, "/sound:"):
			val := ""
			if strings.HasPrefix(tok, "/sound:") {
				val = strings.TrimPrefix(tok, "/sound:")
			}
			out = soundFromXF(val)
		case tok == "-r" || strings.HasPrefix(tok, "-r"):
			// «-r sound:…» отдельным значением; «-rsound:…» — слитно.
			val := ""
			if tok == "-r" {
				if i+1 < len(tokens) {
					val = tokens[i+1]
					i++
				}
			} else if key, glued, ok := splitRdpGlued(tok); ok && key == "-r" {
				val = glued
			}
			if strings.HasPrefix(val, "sound:") {
				out = soundFromLegacy(strings.TrimPrefix(val, "sound:"))
			}
		}
	}
	return out
}

// soundFromXF разбирает значение xfreerdp `/sound:…`. Формат FreeRDP:
// `[sys:<sys>,][dev:…,][format:…,]…` — поля через запятую; берём только sys.
func soundFromXF(v string) RdpSound {
	v = strings.TrimSpace(v)
	if v == "" {
		// `/sound` / `/sound:` — локальное воспроизведение с бэкендом по умолчанию.
		return RdpSound{Mode: RdpSoundLocal}
	}
	fields := strings.Split(v, ",")
	first := strings.ToLower(strings.TrimSpace(fields[0]))
	switch first {
	case "off":
		return RdpSound{Mode: RdpSoundOff}
	case "remote":
		return RdpSound{Mode: RdpSoundRemote}
	case "local":
		return RdpSound{Mode: RdpSoundLocal}
	}
	if name, ok := sysBackend(fields); ok {
		backend, valid := normalizeSoundBackend(name)
		if !valid {
			return RdpSound{}
		}
		return RdpSound{Mode: RdpSoundLocal, Backend: backend}
	}
	if strings.HasPrefix(first, "local:") {
		backend, valid := normalizeSoundBackend(strings.TrimPrefix(first, "local:"))
		if !valid {
			return RdpSound{}
		}
		return RdpSound{Mode: RdpSoundLocal, Backend: backend}
	}
	return RdpSound{} // неизвестная форма — звук не включаем
}

// sysBackend ищет поле `sys:<name>` среди comma-полей FreeRDP.
func sysBackend(fields []string) (string, bool) {
	for _, f := range fields {
		f = strings.ToLower(strings.TrimSpace(f))
		if strings.HasPrefix(f, "sys:") {
			return strings.TrimPrefix(f, "sys:"), true
		}
	}
	return "", false
}

// soundFromLegacy разбирает значение rdesktop `-r sound:…`:
// local|off|remote|local:<backend>|sys:<index>.
func soundFromLegacy(v string) RdpSound {
	v = strings.ToLower(strings.TrimSpace(v))
	switch {
	case v == "off":
		return RdpSound{Mode: RdpSoundOff}
	case v == "remote":
		return RdpSound{Mode: RdpSoundRemote}
	case v == "local":
		return RdpSound{Mode: RdpSoundLocal}
	case strings.HasPrefix(v, "local:"):
		backend, valid := normalizeSoundBackend(strings.TrimPrefix(v, "local:"))
		if !valid {
			return RdpSound{}
		}
		return RdpSound{Mode: RdpSoundLocal, Backend: backend}
	case strings.HasPrefix(v, "sys:"):
		backend, valid := normalizeSoundBackend(strings.TrimPrefix(v, "sys:"))
		if !valid {
			return RdpSound{}
		}
		return RdpSound{Mode: RdpSoundLocal, Backend: backend}
	}
	return RdpSound{}
}

// normalizeSoundBackend нормализует имя подсистемы FreeRDP. ok=false —
// имя недопустимо (NUL/пробелы/пунктуация: C.CString обрезал бы или исказил
// строку; числовой индекс, кроме канонического 0). Пустая строка — «бэкенд
// по умолчанию».
func normalizeSoundBackend(v string) (string, bool) {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" {
		return "", true
	}
	// Канон convert.go: rdesktop `sound:sys:0` == xfreerdp `sys:alsa`.
	if v == "0" {
		return "alsa", true
	}
	// Прочие числовые индексы rdesktop-редиректа — не имя подсистемы.
	if isDigits(v) {
		return "", false
	}
	// Легаси-имя из комбобокса rdesktop -> имя подсистемы FreeRDP.
	if v == "pulseaudio" {
		return "pulse", true
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' || c == '-' {
			continue
		}
		return "", false
	}
	return v, true
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
