package method

import "strings"

// ConvertOptions переводит опционную строку из одного диалекта в другой.
// Неизвестные токены проходят на своих местах и возвращаются в unknown.
// Точное поведение — тест-таблицы convert_test.go (см. баг rdesktop→xfreerdp).
func ConvertOptions(from, to Dialect, options string) (out string, unknown []string) {
	tokens := SplitArgs(options)
	if len(tokens) == 0 {
		return "", nil
	}
	rule := rulesFor(from, to)
	if rule == nil {
		// Не-RDP пара диалектов (или одинаковые): passthrough целиком (лев.
		// закомментирован «passthrough», но без guard вызывать nil-rule нельзя —
		// паника при смене SSH→Telnet/FTP и т.п.).
		return joinTokens(tokens), nil
	}
	var res []string
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		next := ""
		if i+1 < len(tokens) {
			next = tokens[i+1]
		}
		if rep, consumed, ok := rule(tok, next); ok {
			res = append(res, rep...)
			if consumed > 1 {
				i += consumed - 1
			}
			continue
		}
		res = append(res, tok)
		unknown = append(unknown, tok)
	}
	return joinTokens(res), unknown
}

type ruleFunc func(tok, next string) (rep []string, consumed int, ok bool)

func rulesFor(from, to Dialect) ruleFunc {
	switch {
	case from == RDesktop && to == XFreeRDP:
		return rdToXf
	case from == XFreeRDP && to == RDesktop:
		return xfToRd
	}
	return nil // прочие пары диалектов: passthrough целиком
}

// DialectOf — диалект опционной строки RDP по первому токену: rdesktop («-…»),
// xfreerdp («/…»), иначе «» (неопределённо). user-тест #3 (миграция rdesktop↔xfreerdp).
func DialectOf(options string) Dialect {
	for _, t := range SplitArgs(options) {
		switch {
		case len(t) > 0 && t[0] == '-':
			return RDesktop
		case len(t) > 0 && (t[0] == '/' || t[0] == '+'):
			// M4: xfreerdp-булевы флаги вида «+clipboard» — тоже xfreerdp.
			return XFreeRDP
		}
	}
	return ""
}

// rdpValueFlag — rdesktop-опция-значение → соответствующий xfreerdp-префикс.
var rdpValueFlag = map[string]string{
	"-k": "/kbd:", "-d": "/d:", "-g": "/size:", "-a": "/bpp:", "-s": "/shell:",
}

// rdToXf — rdesktop-диалект → xfreerdp (правила из плана и _parseOptionsToCfg).
func rdToXf(tok, next string) (rep []string, consumed int, ok bool) {
	// Слитная запись короткой опции-значения: -dDOMAIN, -ken-us, -a24,
	// -rclipboard:… (ревью MINOR).
	if key, val, glued := splitRdpGlued(tok); glued {
		switch key {
		case "-k", "-d", "-g", "-a", "-s":
			return sv(rdpValueFlag[key] + val), 1, true
		case "-r":
			if rep, ok := rdToXfRedirect(val); ok {
				return rep, 1, true
			}
		}
		return nil, 0, false
	}
	switch tok {
	case "-4", "-5", "-P", "-A":
		return nil, 1, true // нет xfreerdp-полей — дроп
	case "-f":
		return sv("/f"), 1, true
	case "-z":
		return sv("+compression"), 1, true
	case "-0":
		return sv("/admin"), 1, true
	case "-K":
		return sv("-grab-keyboard"), 1, true
	case "-k", "-d", "-g", "-a", "-s":
		if next == "" {
			return nil, 0, false
		}
		return sv(rdpValueFlag[tok] + next), 2, true
	case "-r":
		if next == "" {
			return nil, 0, false
		}
		if rep, ok := rdToXfRedirect(next); ok {
			return rep, 2, true
		}
	}
	return nil, 0, false
}

// rdToXfRedirect — значение rdesktop-редиректа `-r` → xfreerdp-токены.
// ok=false — редирект неизвестен (останется в unknown).
func rdToXfRedirect(next string) ([]string, bool) {
	switch {
	case next == "clipboard:":
		return nil, true
	case strings.HasPrefix(next, "clipboard:"):
		// rdesktop `-r clipboard:<sel>` → `+clipboard`; xfreerdp-диалект
		// не различает выбор (CLIPBOARD/PRIMARYCLIPBOARD), поэтому имя
		// селекции на round-trip схлопывается в канонический
		// PRIMARYCLIPBOARD — известный инherent-loss (MINOR-10).
		return sv("+clipboard"), true
	case strings.HasPrefix(next, "sound:"):
		// rdesktop `-r sound:sys:<idx>` → xfreerdp `/sound:sys:<бэкенд>`.
		// Индекс по умолчанию `0` мапится на именованный бэкенд `alsa`
		// (легаси-хардкод), прочие значения сохраняются — round-trip
		// байт-идемпотентен (MINOR-10).
		snd := strings.TrimPrefix(next, "sound:")
		switch snd {
		case "sys:0":
			return sv("/sound:sys:alsa"), true
		default:
			if snd != "" {
				return sv("/sound:" + snd), true
			}
		}
	case strings.HasPrefix(next, "disk:"):
		rest := strings.TrimPrefix(next, "disk:")
		if i := strings.Index(rest, "="); i >= 0 {
			// Значение из редактора может быть в кавычках; снимаем их,
			// иначе ре-эскейпинг от прохода к проходу «плывёт» (MINOR-10).
			return sv("/drive:" + rest[:i] + "," + strings.Trim(rest[i+1:], `"`)), true
		}
		if rest != "" {
			return sv("/drive:" + rest), true
		}
	}
	return nil, false
}

// xfToRd — обратное направление (зеркальные правила).
func xfToRd(tok, next string) (rep []string, consumed int, ok bool) {
	switch {
	case tok == "/f":
		return sv("-f"), 1, true
	case tok == "+compression":
		return sv("-z"), 1, true
	case tok == "/admin":
		return sv("-0"), 1, true
	case tok == "-grab-keyboard":
		return sv("-K"), 1, true
	case tok == "+clipboard":
		return sv("-r", "clipboard:PRIMARYCLIPBOARD"), 1, true
	case strings.HasPrefix(tok, "/sound:"):
		// xfreerdp `/sound:sys:alsa` → rdesktop `-r sound:sys:0` (канон);
		// прочие значения сохраняются (MINOR-10).
		snd := strings.TrimPrefix(tok, "/sound:")
		if snd == "sys:alsa" {
			return sv("-r", "sound:sys:0"), 1, true
		}
		if snd != "" {
			return sv("-r", "sound:"+snd), 1, true
		}
		return nil, 0, false
	case strings.HasPrefix(tok, "/drive:"):
		v := strings.TrimPrefix(tok, "/drive:")
		if v == "" {
			return nil, 0, false
		}
		if i := strings.Index(v, ","); i >= 0 {
			return sv("-r", `disk:`+v[:i]+`="`+v[i+1:]+`"`), 1, true
		}
		return sv("-r", "disk:"+v), 1, true
	}
	prefixes := []struct{ p, out string }{
		{"/kbd:", "-k"}, {"/d:", "-d"}, {"/size:", "-g"}, {"/bpp:", "-a"}, {"/shell:", "-s"},
	}
	for _, pr := range prefixes {
		if strings.HasPrefix(tok, pr.p) {
			return sv(pr.out, strings.TrimPrefix(tok, pr.p)), 1, true
		}
	}
	return nil, 0, false
}

func sv(v ...string) []string { return v }

// joinTokens собирает результат: значения с пробелами берутся в двойные
// кавычки (с экранированием `"` и `\`); сбалансированные внутренние кавычки
// без пробелов квотирования не требуют (SplitArgs их понимает).
func joinTokens(toks []string) string {
	quoted := make([]string, len(toks))
	escape := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	for i, t := range toks {
		// Пустой токен — явный аргумент (`-d ""`); без кавычек он терялся.
		if t == "" || strings.ContainsAny(t, " \t") {
			quoted[i] = `"` + escape.Replace(t) + `"`
		} else {
			quoted[i] = t
		}
	}
	return strings.Join(quoted, " ")
}
