package ui

// M27: Terminal Options (Look&Feel) — разрешённые параметры терминала.
// Глобальные значения берутся из defaults (Raw-ключи, легаси PACUtils.pm:2055+),
// per-connection override — из env.terminal options при 'use personal settings'
// (легаси PACTermOpts.pm, PACTerminal.pm:3975-4003).

import (
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/DunkanAidaho/asbru-go/internal/config"
	"github.com/DunkanAidaho/asbru-go/internal/ui/wgt"
)

// TermOpts — разрешённые параметры терминала после резолвинга.
type TermOpts struct {
	Font         string // "Monospace 9"
	TextColor    string // RGBA hex "#cc62cc62cc62" (GTK 16-bit)
	BackColor    string
	CursorShape  string // block | underline | ibeam
	Scrollback   int    // строк истории (0=нет, <0=∞)
	Transparency int    // 0-100 (percent)
	WordChars    string // "select words" (double-click)
	BoldLikeText bool
	BoldColor    string
}

// Дефолты терминальных опций для ОТСУТСТВУЮЩЕГО ключа. Держим их в одном месте,
// чтобы диалог настроек, редактор соединения и рантайм не расходились (M44-ревью
// MAJOR-1: настройки показывали 0 и записывали его же — при «Сохранить» история
// терминала молча выключалась).
const (
	defaultScrollbackLines = 5000
	defaultBoldLikeText    = 1
)

// resolveTermOpts — резолвинг Terminal Options: личные перекрывают глобальные
// только при 'use personal settings'.
func resolveTermOpts(d *config.Defaults, env *config.Environment) TermOpts {
	raw := func(key string) string {
		if d == nil {
			return ""
		}
		if v, ok := d.Raw[key]; ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
		return ""
	}
	personal := false
	var to map[string]any
	if env != nil {
		to = env.TerminalOptions
		if v, ok := to["use personal settings"]; ok {
			personal = mapBool(v) // тип-ко-верс: bool/int/float/string (см. M29-ревью MINOR C)
		}
	}
	get := func(key, global string) string {
		if personal && to != nil {
			if v, ok := to[key]; ok {
				if s, ok := v.(string); ok && s != "" {
					return s
				}
			}
		}
		return global
	}
	getInt := func(key string, global int) int {
		if personal && to != nil {
			if v, ok := to[key]; ok {
				switch n := v.(type) {
				case int:
					return n
				case int64:
					return int(n)
				case float64:
					return int(n)
				}
			}
		}
		return global
	}

	// 'terminal transparency' в конфиге/сиде — 0-1 доля (seed 0.3), а не
	// проценты. Читаем как долю и конвертируем; личный override — тоже доля
	// (M29-ревью I1: rawInt делал int(0.3)=0, теряя прозрачность).
	transparency := fracToPercent(fracVal(d, "terminal transparency"))
	if personal && to != nil {
		if v, ok := to["terminal transparency"]; ok {
			transparency = fracToPercent(valueFrac(v))
		}
	}

	o := TermOpts{
		Font:        get("terminal font", raw("terminal font")),
		TextColor:   get("text color", raw("text color")),
		BackColor:   get("back color", raw("back color")),
		CursorShape: get("cursor shape", raw("cursor shape")),
		// M44: при отсутствии ключа — дефолт (тот же, что в сиде, редакторе и
		// настройках), иначе setHistorySize(0) означает HistoryTypeNone: история не
		// копится, а колесо уходит в синтез стрелок (см. wgt/widget.cpp).
		// Явные значения (включая 0) не трогаем.
		Scrollback:   getInt("terminal scrollback lines", rawIntOr(d, "terminal scrollback lines", defaultScrollbackLines)),
		Transparency: transparency,
		WordChars:    get("terminal select words", raw("terminal select words")),
		// M44-ревью MINOR-4: дефолт 1 — как в редакторе соединения
		// (termOptsEditFromEnv); раньше отсутствие ключа давало 0, и рантайм
		// расходился с показанной в редакторе галочкой.
		BoldLikeText: getInt("bold color like text", rawIntOr(d, "bold color like text", defaultBoldLikeText)) == 1,
		BoldColor:    get("bold color", raw("bold color")),
	}
	if o.CursorShape == "" {
		o.CursorShape = "block"
	}
	return o
}

// termOptsEdit — значения вкладки Terminal Options редактора соединения
// (M29-остаток #2): raw-строки, соответствующие ключам resolveTermOpts.
type termOptsEdit struct {
	UsePersonal  bool
	Font         string
	TextColor    string
	BackColor    string
	CursorShape  string
	Scrollback   string
	Transparency string // проценты (0-100), для ввода
	WordChars    string
	BoldLikeText bool
	BoldColor    string
}

// mapBool — произвольное значение (bool/int/…) → bool (для Raw/мап).
func mapBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case int:
		return t == 1
	case int64:
		return t == 1
	case float64:
		return t == 1
	case string:
		return t != "" && t != "0" && t != "false"
	}
	return false
}

// termOptsEditFromEnv — эффективные значения вкладки: глобальные defaults,
// перекрытые per-conn при 'use personal settings'. Для отображения в редакторе.
func termOptsEditFromEnv(d *config.Defaults, env *config.Environment) termOptsEdit {
	personal := false
	var to map[string]any
	if env != nil {
		to = env.TerminalOptions
		if v, ok := to["use personal settings"]; ok {
			personal = mapBool(v)
		}
	}
	global := func(key, def string) string {
		if d != nil {
			if v, ok := d.Raw[key].(string); ok && v != "" {
				return v
			}
		}
		return def
	}
	globalInt := func(key string, def int) int {
		if d != nil {
			switch n := d.Raw[key].(type) {
			case int:
				return n
			case int64:
				return int(n)
			case float64:
				return int(n)
			}
		}
		return def
	}
	get := func(key, def string) string {
		if personal && to != nil {
			if v, ok := to[key].(string); ok && v != "" {
				return v
			}
		}
		return global(key, def)
	}
	getInt := func(key string, def int) int {
		if personal && to != nil {
			switch n := to[key].(type) {
			case int:
				return n
			case int64:
				return int(n)
			case float64:
				return int(n)
			case string:
				if x, err := strconv.Atoi(n); err == nil {
					return x
				}
			}
		}
		return globalInt(key, def)
	}
	// transparency: 0-1 доля в обоих хранилищах → проценты для ввода
	// (M29-ревью I1: rawInt делал int(0.3)=0, теряя прозрачность).
	trans := fracToPercent(fracVal(d, "terminal transparency"))
	if personal && to != nil {
		if v, ok := to["terminal transparency"]; ok {
			trans = fracToPercent(valueFrac(v))
		}
	}
	return termOptsEdit{
		UsePersonal: personal,
		// РЕВЬЮ MINOR A: дисплей-фолбэки "Monospace 9"/5000 (seed res/asbru.yml
		// всегда задаёт эти ключи, поэтому d.Raw обычно имеет их). Если key
		// отсутствует вовсе, редактор показывает разумный дефолт; терминал теперь
		// берёт ТЕ ЖЕ значения (M44: defaultScrollbackLines — раньше scrollback
		// уходил в 0 = HistoryTypeNone, и редактор с рантаймом расходились).
		// Остаётся расхождение только для font: пустой font = дефолт QTermWidget.
		Font:         get("terminal font", global("terminal font", "Monospace 9")),
		TextColor:    get("text color", global("text color", "")),
		BackColor:    get("back color", global("back color", "")),
		CursorShape:  get("cursor shape", global("cursor shape", "block")),
		Scrollback:   strconv.Itoa(getInt("terminal scrollback lines", defaultScrollbackLines)),
		Transparency: strconv.Itoa(trans),
		WordChars:    get("terminal select words", ""),
		BoldLikeText: getInt("bold color like text", defaultBoldLikeText) == 1,
		BoldColor:    get("bold color", ""),
	}
}

// applyTermOptsEdit — запись вкладки в env.TerminalOptions. Флаг
// 'use personal settings' управляет применением (resolveTermOpts гейтится им).
// При personal=OFF блок не разворачивается без надобности: если карты ещё нет —
// не создаём (иначе у каждого сохранённого соединения появится пустой
// 'terminal options'); если есть — только ставим флаг false, а ключи оставляем
// (инертны; сохранены для повторного включения). При ON пишем все ключи.
func applyTermOptsEdit(env *config.Environment, e termOptsEdit) {
	if !e.UsePersonal {
		if env.TerminalOptions != nil {
			env.TerminalOptions["use personal settings"] = false
		}
		return
	}
	if env.TerminalOptions == nil {
		env.TerminalOptions = map[string]any{}
	}
	to := env.TerminalOptions
	to["use personal settings"] = true
	to["terminal font"] = e.Font
	to["text color"] = e.TextColor
	to["back color"] = e.BackColor
	to["cursor shape"] = e.CursorShape
	to["terminal scrollback lines"] = atoiOr(e.Scrollback, defaultScrollbackLines)
	pct, _ := strconv.Atoi(e.Transparency)
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	to["terminal transparency"] = float64(pct) / 100.0
	to["terminal select words"] = e.WordChars
	to["bold color like text"] = boolToInt(e.BoldLikeText)
	to["bold color"] = e.BoldColor
}

// cursorShapeInt — строка курсора → enum QTermWidget KeyboardCursorShape
// (BlockCursor=0, UnderlineCursor=1, IBeamCursor=2; легаси — PACTermOpts.pm).
func cursorShapeInt(s string) int {
	switch s {
	case "underline":
		return 1
	case "ibeam":
		return 2
	default:
		return 0
	}
}

// colorToRGB — GTK RGBA hex (#rrrrggggbbbb, 16-бит/канал) → "r,g,b" (0-255).
func colorToRGB(hex string) string {
	h := hex
	if len(h) > 0 && h[0] == '#' {
		h = h[1:]
	}
	if len(h) != 12 {
		return "0,0,0"
	}
	r := rgbaByte(h[0:4])
	g := rgbaByte(h[4:8])
	b := rgbaByte(h[8:12])
	return strconv.Itoa(r) + "," + strconv.Itoa(g) + "," + strconv.Itoa(b)
}

func rgbaByte(s string) int {
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return 0
	}
	return int(v >> 8) // старший байт 16-битного канала
}

// defaultPalette16 — базовая палитра 16 цветов для генерируемого .colorscheme.
var defaultPalette16 = [16][3]int{
	{0, 0, 0}, {205, 0, 0}, {0, 205, 0}, {205, 205, 0},
	{0, 0, 238}, {205, 0, 205}, {0, 205, 205}, {229, 229, 229},
	{127, 127, 127}, {255, 0, 0}, {0, 255, 0}, {255, 255, 0},
	{92, 92, 255}, {255, 0, 255}, {0, 255, 255}, {255, 255, 255},
}

// paletteNames — порядок палитры: 8 обычных + 8 ярких (легаси
// PACUtils.pm:2055-2070; совпадает с `defaultPalette16` по индексу).
var paletteNames = [16]string{
	"color black", "color red", "color green", "color yellow",
	"color blue", "color magenta", "color cyan", "color white",
	"color bright black", "color bright red", "color bright green", "color bright yellow",
	"color bright blue", "color bright magenta", "color bright cyan", "color bright white",
}

// paletteFromDefaults — 16-цветная палитра из defaults (GTK RGBA 12-hex).
// Если ни одного цвета не настроено — нулевая (вызывающий берёт defaultPalette16).
func paletteFromDefaults(d *config.Defaults) [16][3]int {
	if d == nil {
		return [16][3]int{}
	}
	var p [16][3]int
	found := false
	for i, n := range paletteNames {
		if v, ok := d.Raw[n].(string); ok && v != "" {
			p[i] = parseRGB(colorToRGB(v))
			found = true
		} else {
			p[i] = defaultPalette16[i]
		}
	}
	if !found {
		return [16][3]int{}
	}
	return p
}

// palIsZero — нулевая палитра (значит «не настроена» → взять дефолт).
func palIsZero(p [16][3]int) bool { return p == [16][3]int{} }

// writeTermColorScheme — генерирует .colorscheme с text/back из GTK RGBA и
// 16-цветной палитрой defaults (если настроена; иначе defaultPalette16 —
// дефолт QTermWidget). QTermWidget API даёт только именованный/путь схемы
// (легаси:3979-3981). Color<X>Intense (bold) = яркий аналог Color<X+8>,
// чтобы bold != normal (M35 P0); Foreground/BackgroundIntense = текст/фон
// (иначе qtermwidget падает на чёрный для bold-текста по умолчанию).
// Примечание: 'bold color like text'/'bold color' QTermWidget НЕ выражает
// цветом (setBoldIntense управляет только весом шрифта; цветной intensity
// применяется в эмуляции безусловно) — известное ограничение.
func writeTermColorScheme(o TermOpts, d *config.Defaults) (string, error) {
	dir, err := tmpDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "asbru-term.colorscheme")
	var b strings.Builder
	writeSection := func(name string, c [3]int) {
		b.WriteString("[" + name + "]" + "\n")
		b.WriteString("Color=" + strconv.Itoa(c[0]) + "," + strconv.Itoa(c[1]) + "," + strconv.Itoa(c[2]) + "\n")
		b.WriteString("Transparent=false" + "\n" + "\n")
	}
	palette := defaultPalette16
	if p := paletteFromDefaults(d); !palIsZero(p) {
		palette = p
	}
	fg := colorToRGB(o.TextColor)
	// M35 P0/review: *Intense (bold) — qtermwidget читает эти фиксированные имена
	// ОБЯЗАТЕЛЬНО, иначе fallback на чёрный. Bold-текст по умолчанию использует
	// ForegroundIntense. 'bold color like text'=1 (дефолт) → текст-цвет; =0 и
	// задан 'bold color' → этот цвет (иначе текст-цвет). 'bold color' для
	// ANSI-цветов применяется пер-ANSI через Color<N>Intense (яркая палитра).
	fgIntense := fg
	if !o.BoldLikeText && o.BoldColor != "" {
		fgIntense = colorToRGB(o.BoldColor)
	}
	writeSection("Foreground", parseRGB(fg))
	writeSection("Background", parseRGB(colorToRGB(o.BackColor)))
	writeSection("ForegroundIntense", parseRGB(fgIntense))
	writeSection("BackgroundIntense", parseRGB(colorToRGB(o.BackColor)))
	for i, c := range palette {
		writeSection("Color"+strconv.Itoa(i), c)
	}
	for i := 0; i < 8; i++ {
		writeSection("Color"+strconv.Itoa(i)+"Intense", palette[i+8])
	}
	for i := 8; i < 16; i++ {
		writeSection("Color"+strconv.Itoa(i)+"Intense", palette[i])
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// parseRGB — "r,g,b" → [r,g,b].
func parseRGB(s string) [3]int {
	parts := strings.SplitN(s, ",", 3)
	var c [3]int
	for i, p := range parts {
		c[i], _ = strconv.Atoi(p)
	}
	return c
}

// termConfig — применимый конфиг терминала: резолвинг + генерация схемы.
func termConfig(cfg *config.Config, env *config.Environment) wgt.TermConfig {
	o := resolveTermOpts(&cfg.Defaults, env)
	tc := wgt.TermConfig{
		Font:        o.Font,
		CursorShape: cursorShapeInt(o.CursorShape),
		HistorySize: o.Scrollback,
		Opacity:     float64(100-o.Transparency) / 100.0,
		WordChars:   o.WordChars,
	}
	if o.TextColor != "" || o.BackColor != "" {
		if path, err := writeTermColorScheme(o, &cfg.Defaults); err != nil {
			slog.Warn("генерация схемы цветов терминала", "err", err)
		} else {
			tc.ColorScheme = path
		}
	}
	return tc
}
