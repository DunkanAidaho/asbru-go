package method

import (
	"fmt"
	"strconv"
	"strings"
)

// M38: графические опции RDP — размер экрана, глубина цвета, раскладка.
// Формы токенов диалект-зависимы: xfreerdp пишет слитно («/size:WxH», «/bpp:24»,
// «/kbd:en-us»), rdesktop — раздельно («-g WxH», «-a 24», «-k en-us»). Поэтому
// чтение/запись идут через эти хелперы (чистые, тестируемые), а не правкой
// строки «Опции» вслепую.

// Группы галок: раскладка редактора по блокам (GroupScreen → блок «Экран»).
const (
	GroupOptions = ""       // обычные опции (вкладка «Опции»)
	GroupScreen  = "screen" // «картиночные» (блок «Экран» в «Основных»)
)

// HasScreenOptions — показывать ли блок «Экран» (размер/глубина/раскладка):
// только RDP-диалектам — VNC/SSH таких опций не имеют.
func HasScreenOptions(d Dialect) bool {
	return d == RDesktop || d == XFreeRDP
}

// ScreenSizePresets — типовые разрешения для комбо.
func ScreenSizePresets() []string {
	return []string{"1024x768", "1280x720", "1280x1024", "1440x900", "1600x900", "1920x1080", "2560x1440"}
}

// ValidColorDepth — допустимая глубина цвета.
func ValidColorDepth(s string) bool {
	switch s {
	case "8", "15", "16", "24", "32":
		return true
	}
	return false
}

// parseSizeLiteral — «ШxВ» (только положительные).
func parseSizeLiteral(s string) (int, int, bool) {
	i := strings.IndexByte(s, 'x')
	if i <= 0 {
		return 0, 0, false
	}
	w, err1 := strconv.Atoi(s[:i])
	h, err2 := strconv.Atoi(s[i+1:])
	if err1 != nil || err2 != nil || w <= 0 || h <= 0 {
		return 0, 0, false
	}
	return w, h, true
}

// SplitSize — «ШxВ» → части (для префилла полей «Свой…» в редакторе).
func SplitSize(s string) (w, h string, ok bool) {
	if _, _, ok := parseSizeLiteral(s); !ok {
		return "", "", false
	}
	i := strings.IndexByte(s, 'x')
	return s[:i], s[i+1:], true
}

// screenForms — как диалект пишет размер/глубину/раскладку.
type screenForms struct {
	sizeKey, bppKey, kbdKey string
	full                    string // токен «во весь экран»
	glued                   bool   // слитная форма (xfreerdp) vs раздельная (rdesktop)
}

func screenFormsFor(d Dialect) screenForms {
	if d == RDesktop {
		return screenForms{sizeKey: "-g", bppKey: "-a", kbdKey: "-k", full: "-f"}
	}
	return screenForms{sizeKey: "/size:", bppKey: "/bpp:", kbdKey: "/kbd:", full: "/f", glued: true}
}

// matchesKey — токен принадлежит ключу. Для слитной формы — префикс; для
// раздельной — точное совпадение ЛИБО слитная запись со значением («-a24»).
// ВНИМАНИЕ: наивный HasPrefix дал бы алиасинг «-authentication» → «-a»+значение
// (тот же класс, что чинили в rdpKey): буква после ключа = длинный флаг.
func matchesKey(tok, key string, glued bool) (value string, ok bool) {
	if glued {
		if strings.HasPrefix(tok, key) {
			return strings.TrimPrefix(tok, key), true
		}
		return "", false
	}
	if tok == key {
		return "", true // значение — следующим токеном
	}
	if strings.HasPrefix(tok, key) && len(tok) > len(key) {
		if c := tok[len(key)]; (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			return "", false // «-authentication» — не «-a» со значением
		}
		return tok[len(key):], true
	}
	return "", false
}

// optValue — значение диалектного ключа ("" — токена нет). Для раздельной формы
// значение берётся из следующего токена — но только если тот НЕ флаг: писатель
// (setOptValue) флаги значением не считает, и без этой проверки «-a -z» читалось
// как глубина «-z», а «-g +fonts» — как размер «+fonts» (ревью MINOR-1).
func optValue(options string, f screenForms, key string) string {
	toks := SplitArgs(options)
	for i, tok := range toks {
		v, ok := matchesKey(tok, key, f.glued)
		if !ok {
			continue
		}
		if v != "" {
			return v
		}
		if i+1 < len(toks) && !rdpIsFlag(toks[i+1]) {
			return toks[i+1]
		}
		return ""
	}
	return ""
}

// setOptValue — заменить/убрать значение ключа; прочие токены сохраняются.
func setOptValue(options string, f screenForms, key, val string) string {
	toks := SplitArgs(options)
	out := make([]string, 0, len(toks)+2)
	for i := 0; i < len(toks); i++ {
		tok := toks[i]
		v, ok := matchesKey(tok, key, f.glued)
		if !ok {
			out = append(out, tok)
			continue
		}
		// Ключ найден: если значение шло отдельным токеном — съедаем и его.
		// Проверка через rdpIsFlag, а не «-»/«/»: маппер считает флагом и «+»
		// (+fonts/+clipboard/+compression) — иначе сосед-«+»-токен тихо съедался
		// при правке размера/глубины/раскладки (ревью MINOR-2).
		if v == "" && i+1 < len(toks) && !rdpIsFlag(toks[i+1]) {
			i++
		}
	}
	if val != "" {
		if f.glued {
			out = append(out, key+val)
		} else {
			out = append(out, key, val)
		}
	}
	return JoinArgs(out)
}

// dropFlag — убрать токен-флаг («/f», «-f»).
func dropFlag(options, flag string) string {
	return JoinArgs(removeTokenGroup(SplitArgs(options), []string{flag}))
}

// ScreenSize — текущий размер: "" (по размеру окна), "full" (во весь экран)
// или «ШxВ».
func ScreenSize(options string, d Dialect) string {
	f := screenFormsFor(d)
	for _, tok := range SplitArgs(options) {
		if tok == f.full {
			return "full"
		}
	}
	return optValue(options, f, f.sizeKey)
}

// SetScreenSize — выставить размер экрана ("" — по размеру окна).
func SetScreenSize(options string, d Dialect, size string) (string, error) {
	f := screenFormsFor(d)
	switch size {
	case "":
		return dropFlag(setOptValue(options, f, f.sizeKey, ""), f.full), nil
	case "full":
		out := dropFlag(setOptValue(options, f, f.sizeKey, ""), f.full)
		return JoinArgs(append(SplitArgs(out), f.full)), nil
	default:
		if _, _, ok := parseSizeLiteral(size); !ok {
			return options, fmt.Errorf("недопустимый размер %q (ожидается ШxВ)", size)
		}
		return dropFlag(setOptValue(options, f, f.sizeKey, size), f.full), nil
	}
}

// ColorDepth — текущая глубина цвета ("" — не задана).
func ColorDepth(options string, d Dialect) string {
	f := screenFormsFor(d)
	return optValue(options, f, f.bppKey)
}

// SetColorDepth — выставить глубину цвета ("" — убрать).
func SetColorDepth(options string, d Dialect, bpp string) (string, error) {
	if bpp != "" && !ValidColorDepth(bpp) {
		return options, fmt.Errorf("недопустимая глубина цвета %q", bpp)
	}
	f := screenFormsFor(d)
	return setOptValue(options, f, f.bppKey, bpp), nil
}

// KeyboardLayout — текущая раскладка ("" — не задана).
func KeyboardLayout(options string, d Dialect) string {
	f := screenFormsFor(d)
	return optValue(options, f, f.kbdKey)
}

// SetKeyboardLayout — выставить раскладку ("" — убрать).
func SetKeyboardLayout(options string, d Dialect, name string) (string, error) {
	f := screenFormsFor(d)
	return setOptValue(options, f, f.kbdKey, name), nil
}
