package method

// M33 (встраивание RDP через libfreerdp3): маппинг опций RDP (rdesktop/xfreerdp)
// в свободные настройки (bpp/kbd/domain/size/shell/fullscreen/…). Неизвестные и
// не-мапируемые ключи — в unknown (НЕ падаем; UI покажет «ключ не поддерживается:
// заменить/нет»). Устойчивость к протуханию опций: чем больше незнакомых токенов,
// тем больше в unknown, а не крах.

import "strings"

// rdpSettingMap — известные RDP-ключи (rdesktop и xfreerdp) → имя настройки.
var rdpSettingMap = map[string]string{
	"-a": "bpp", "/bpp:": "bpp",
	"-k": "kbd", "/kbd:": "kbd",
	"-d": "domain", "/d:": "domain",
	"-g": "size", "/size:": "size",
	"-s": "shell", "/shell:": "shell",
	"-f": "fullscreen", "/f": "fullscreen", "/fullscreen": "fullscreen",
	"-0": "admin", "/admin": "admin",
	"-z": "compression", "+compression": "compression", "/compression": "compression",
	"-K": "grab-keyboard", "-grab-keyboard": "grab-keyboard",
	"-B": "background", "/background": "background",
	"-C": "clipboard", "+clipboard": "clipboard", "/clipboard": "clipboard",
	"-4": "rdpversion", "-5": "rdpversion", // rdesktop версия протокола
	"-r": "redirect", // rdesktop редиректы (значение — след. токен)
	// M39: /drive:<share>,<path> — известный ключ (не «не поддерживается»).
	// Диски извлекает ParseRdpDrives (rdpdrive.go): MapRdpOptions кладёт лишь
	// последнее значение, множественность ему недоступна.
	"/drive:": "drive",
	// M39: звук. Значение извлекает ParseRdpSound (rdpsound.go); известный
	// ключ, чтобы не показывать «ключ не поддерживается» в UI.
	"/sound": "sound", "/sound:": "sound",
	// M37: галки безопасности/производительности → настройки ВСТРОЕННОГО
	// клиента (M33). Без них опции молча игнорировались, и галки врали.
	"-sec-rdp": "noRDP", "-sec-tls": "noTLS", "-sec-nla": "noNLA",
	"/cert-ignore":        "certIgnore",
	"-authentication":     "noAuth",
	"-fast-path":          "noFastPath",
	"/rfx":                "rfx",
	"/nsc":                "nsc",
	"/dynamic-resolution": "dynamicResolution",
	"+fonts":              "fontSmooth",
	"/tls-seclevel:":      "tlsSecLevel",
}

// rdpGluedShort — короткие опции-значения rdesktop, у которых значение может
// быть записано слитно с флагом: -dDOMAIN, -ken-us, -a24, -g1280x1024,
// -s/bin/sh, -rclipboard:….
var rdpGluedShort = map[string]bool{
	"-a": true, "-k": true, "-d": true, "-g": true, "-s": true, "-r": true,
}

// rdpLongFlagWord — остаток токена выглядит как слово длинного флага
// («seamless», «shell», «domain»), а не как слитное значение короткой опции.
// Значения: числа (-a24), геометрия (-g1280x1024), пути (-s/bin/sh), редиректы
// с ':' (-rclipboard:…), домены/раскладки с заглавными или дефисом (-dDOMAIN,
// -ken-us). Раскладки и команды короче 4 букв (-kde, -scmd) остаются значениями.
func rdpLongFlagWord(s string) bool {
	if len(s) < 4 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 'a' || s[i] > 'z' {
			return false
		}
	}
	return true
}

// splitRdpGlued разбирает слитную короткую опцию-значение. Известные длинные
// флаги («-grab-keyboard», «-sec-nla», «-fast-path», …) не расщепляются —
// иначе «-grab-keyboard» стал бы size=rab-keyboard. Неизвестные длинные флаги
// («-seamless») тоже: rdesktop-getopt съел бы их как «-s eamless», но для
// встроенного клиента это молчаливая подмена настройки — они уходят в unknown.
func splitRdpGlued(tok string) (key, val string, ok bool) {
	if len(tok) < 3 || tok[0] != '-' || tok[1] == '-' {
		return "", "", false
	}
	if _, exact := rdpSettingMap[tok]; exact {
		return "", "", false
	}
	if strings.HasPrefix(tok, "-sec-") {
		return "", "", false
	}
	key = tok[:2]
	if !rdpGluedShort[key] {
		return "", "", false
	}
	if rdpLongFlagWord(tok[2:]) {
		return "", "", false
	}
	return key, tok[2:], true
}

// rdpKey — нормализованный ключ токена: xfreerdp "/key:value" → "/key:",
// rdesktop "-Xvalue/-X" → "-X"; ДЛИННЫЙ флаг "-grab-keyboard" — как есть
// (M35: иначе усекается до "-g" → мапится в size).
func rdpKey(tok string) string {
	if strings.HasPrefix(tok, "/") {
		if i := strings.IndexByte(tok, ':'); i >= 0 {
			return tok[:i+1]
		}
		return tok
	}
	if strings.HasPrefix(tok, "-") && len(tok) >= 2 {
		// известный длинный rdesktop-флаг → не усекаем (иначе путаем с коротким).
		if _, ok := rdpSettingMap[tok]; ok {
			return tok
		}
		// короткая опция-значение со слитным значением: -dDOMAIN, -ken-us.
		if key, _, ok := splitRdpGlued(tok); ok {
			return key
		}
		// Усекаем до короткого ключа ТОЛЬКО когда дальше идёт ЗНАЧЕНИЕ
		// («-a24», «-g1280x1024», «-s/bin/sh»). Если третий символ — буква, это
		// длинный флаг («-sec-nla», «-fast-path», «-authentication»): усечение
		// дало бы алиас на чужую настройку («-s»→shell, «-a»→bpp, «-f»→
		// fullscreen) и молча перетёрло бы её. Неизвестные — в unknown.
		if len(tok) > 2 {
			if c := tok[2]; (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
				return tok
			}
		}
		return tok[:2]
	}
	return tok
}

func rdpKnown(tok string) bool {
	_, ok := rdpSettingMap[rdpKey(tok)]
	return ok
}

func rdpIsFlag(tok string) bool {
	return strings.HasPrefix(tok, "-") || strings.HasPrefix(tok, "/") || strings.HasPrefix(tok, "+")
}

// MapRdpOptions — опции RDP → настройки libfreerdp3; unknown — не-мапируемые токены.
func MapRdpOptions(options string) (map[string]string, []string) {
	tokens := SplitArgs(options)
	settings := map[string]string{}
	var unknown []string
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		if !rdpIsFlag(tok) {
			unknown = append(unknown, tok) // значение/мусор без префикса
			continue
		}
		setting := rdpSettingMap[rdpKey(tok)]
		if setting == "" {
			unknown = append(unknown, tok)
			// неизвестный rdesktop-флаг: проглотить следующее значение (если есть)
			if strings.HasPrefix(tok, "-") && !strings.HasPrefix(tok, "--") &&
				i+1 < len(tokens) && !rdpIsFlag(tokens[i+1]) {
				i++
			}
			continue
		}
		// известный ключ — извлечь значение
		switch {
		case strings.HasPrefix(tok, "/"): // xfreerdp "/key:value"
			v := strings.TrimPrefix(tok, rdpKey(tok))
			settings[setting] = v
		default: // rdesktop "-X value" / "-Xvalue" / булев флаг
			if len(tok) > 2 {
				// известный ДЛИННЫЙ булев флаг («-grab-keyboard») — "1"; иначе
				// "-Xvalue" слитно (M35-ревью: иначе grab-keyboard="rab-keyboard").
				if _, exact := rdpSettingMap[tok]; exact {
					settings[setting] = "1"
				} else {
					settings[setting] = tok[2:]
				}
			} else if i+1 < len(tokens) && !rdpIsFlag(tokens[i+1]) {
				settings[setting] = tokens[i+1]
				i++
			} else {
				settings[setting] = "1" // булев флаг (-f, -0, -z, -K, …)
			}
		}
	}
	return settings, unknown
}

// rdpOptionKnown — известный ли RDP-ключ (для UI-валидации).
func rdpOptionKnown(tok string) bool { return rdpKnown(tok) }
