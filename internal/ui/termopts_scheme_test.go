package ui

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/DunkanAidaho/asbru-go/internal/config"
)

// M35 P0: палитра — Intense-варианты (bold) отличаются от обычных:
// Color<X>Intense = яркий аналог Color<X+8> (иначе «bold/intense == normal»).
func TestWriteTermColorSchemeIntense(t *testing.T) {
	o := TermOpts{TextColor: "#000000000000", BackColor: "#ffffffffffff"}
	path, err := writeTermColorScheme(o, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	// обычный Colour0 (black=0,0,0) ≠ Intense (яркий 127,127,127).
	if sectionColor(s, "Color0") == sectionColor(s, "Color0Intense") {
		t.Fatalf("Color0 == Color0Intense: bold/intense не разделён")
	}
	if sectionColor(s, "Color0") != "0,0,0" || sectionColor(s, "Color0Intense") != "127,127,127" {
		t.Fatalf("Color0/Intense: %q/%q",
			sectionColor(s, "Color0"), sectionColor(s, "Color0Intense"))
	}
	// яркие Colour8..15: Intense == сам (уже яркий).
	if sectionColor(s, "Color8") != sectionColor(s, "Color8Intense") {
		t.Fatalf("Color8 != Color8Intense (яркий не должен прыгать)")
	}
	// секций всех 16 цветов + Intense-варианты.
	for i := 0; i < 16; i++ {
		if sectionColor(s, "Color"+strconv.Itoa(i)) == "" {
			t.Fatalf("нет секции Color%d", i)
		}
		if sectionColor(s, "Color"+strconv.Itoa(i)+"Intense") == "" {
			t.Fatalf("нет секции Color%dIntense", i)
		}
	}
}

// M35 P0: палитра читается из defaults (defaults.color * / color bright *),
// а не только дефолт QTermWidget (раньше «игнор config palette»).
func TestWriteTermColorSchemeConfigPalette(t *testing.T) {
	o := TermOpts{TextColor: "#000000000000", BackColor: "#ffffffffffff"}
	d := &config.Defaults{Raw: map[string]any{
		"color red":          "#ffff00000000", // FF,00,00 → 255,0,0
		"color bright green": "#0000ffff0000", // 00,FF,00 → 0,255,0
	}}
	path, err := writeTermColorScheme(o, d)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	data, _ := os.ReadFile(path)
	s := string(data)
	// Color1 (red) из конфига, а не дефолт 205,0,0.
	if sectionColor(s, "Color1") != "255,0,0" {
		t.Fatalf("Color1 = %q, хотим 255,0,0 (из config)", sectionColor(s, "Color1"))
	}
	// Color2Intense (bold green, ЧИТАЕМЫЙ QTermWidget) из конфиг color bright green.
	if sectionColor(s, "Color2Intense") != "0,255,0" {
		t.Fatalf("Color2Intense = %q, хотим 0,255,0 (color bright green)", sectionColor(s, "Color2Intense"))
	}
	if sectionColor(s, "Color2") != "0,205,0" {
		t.Fatalf("Color2 = %q, хотим дефолт 0,205,0", sectionColor(s, "Color2"))
	}
}

// M35 P0/review: схема пишет ForegroundIntense/BackgroundIntense (= текст/фон),
// иначе qtermwidget падает на чёрный для bold-текста по умолчанию.
func TestWriteTermColorSchemeIntenseSections(t *testing.T) {
	o := TermOpts{TextColor: "#0f0f0f0f0f0f", BackColor: "#ffffffffffff"}
	path, err := writeTermColorScheme(o, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	data, _ := os.ReadFile(path)
	s := string(data)
	fg := sectionColor(s, "Foreground")
	if sectionColor(s, "ForegroundIntense") != fg || sectionColor(s, "ForegroundIntense") == "" {
		t.Fatalf("ForegroundIntense = %q (хотим = Foreground %q)", sectionColor(s, "ForegroundIntense"), fg)
	}
	if sectionColor(s, "BackgroundIntense") != sectionColor(s, "Background") {
		t.Fatalf("BackgroundIntense != Background")
	}
	// Foreground = fg из текста (#0f0f0f0f0f0f → 15,15,15).
	if fg != "15,15,15" {
		t.Fatalf("Foreground = %q, хотим 15,15,15", fg)
	}
}

// M35 P0/review MINOR: 'bold color like text'=0 + 'bold color' → ForegroundIntense
// = тот цвет (иначе текст-цвет).
func TestWriteTermColorSchemeBoldColor(t *testing.T) {
	o := TermOpts{TextColor: "#111111111111", BoldColor: "#eeeeeeeeeeee", BoldLikeText: false}
	path, err := writeTermColorScheme(o, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	data, _ := os.ReadFile(path)
	s := string(data)
	if sectionColor(s, "ForegroundIntense") != "238,238,238" {
		t.Fatalf("ForegroundIntense = %q, хотим 238,238,238 (bold color)", sectionColor(s, "ForegroundIntense"))
	}
	// bold like text=1 → текст-цвет.
	o2 := TermOpts{TextColor: "#111111111111", BoldColor: "#eeeeeeeeeeee", BoldLikeText: true}
	path2, err := writeTermColorScheme(o2, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path2)
	data2, _ := os.ReadFile(path2)
	if sectionColor(string(data2), "ForegroundIntense") != "17,17,17" {
		t.Fatalf("bold like text=1 → ForegroundIntense, хотим 17,17,17 (текст)")
	}
}

// sectionColor — значение Color= в секции [name] генерируемой схемы.
func sectionColor(s, name string) string {
	in := false
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			in = strings.Trim(line, "[]") == name
			continue
		}
		if in && strings.HasPrefix(line, "Color=") {
			return strings.TrimPrefix(line, "Color=")
		}
	}
	return ""
}
