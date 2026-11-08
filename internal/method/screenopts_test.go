package method

import (
	"strings"
	"testing"
)

// M38: графические опции RDP — размер экрана, глубина цвета, раскладка.
// Формы токенов зависят от диалекта: xfreerdp «/size:WxH», «/f», «/bpp:N»,
// «/kbd:NAME»; rdesktop — раздельные «-g WxH», «-f», «-a N», «-k NAME».

func TestScreenSizeRoundTrip(t *testing.T) {
	cases := []struct {
		d      Dialect
		size   string
		wantIn string
	}{
		{XFreeRDP, "1280x1024", "/size:1280x1024"},
		{XFreeRDP, "full", "/f"},
		{RDesktop, "1280x1024", "-g 1280x1024"},
		{RDesktop, "full", "-f"},
	}
	for _, c := range cases {
		got, err := SetScreenSize("", c.d, c.size)
		if err != nil {
			t.Fatalf("%s/%s: %v", c.d, c.size, err)
		}
		if got != c.wantIn {
			t.Errorf("%s: %q → %q, хотим %q", c.d, c.size, got, c.wantIn)
		}
		if back := ScreenSize(got, c.d); back != c.size {
			t.Errorf("%s: чтение %q → %q, хотим %q", c.d, got, back, c.size)
		}
	}
}

// По размеру окна ("") — токенов размера нет; переключение снимает прежний.
func TestSetScreenSizeReplacesAndClears(t *testing.T) {
	on, _ := SetScreenSize("/bpp:24", XFreeRDP, "800x600")
	if on != "/bpp:24 /size:800x600" && on != "/size:800x600 /bpp:24" {
		t.Fatalf("соседние токены: %q", on)
	}
	full, _ := SetScreenSize(on, XFreeRDP, "full")
	if full != "/bpp:24 /f" && full != "/f /bpp:24" {
		t.Fatalf("замена на full: %q", full)
	}
	for _, s := range []string{full, on} {
		off, err := SetScreenSize(s, XFreeRDP, "")
		if err != nil {
			t.Fatal(err)
		}
		if off != "/bpp:24" {
			t.Errorf("снятие размера из %q → %q", s, off)
		}
		if ScreenSize(off, XFreeRDP) != "" {
			t.Errorf("размер остался: %q", off)
		}
	}
	// rdesktop: раздельная форма -g тоже снимается
	rd, _ := SetScreenSize("-a 24 -g 800x600", RDesktop, "")
	if rd != "-a 24" {
		t.Errorf("rdesktop снятие: %q", rd)
	}
}

// Глубина цвета.
func TestColorDepthRoundTrip(t *testing.T) {
	for _, c := range []struct {
		d      Dialect
		wantIn string
	}{{XFreeRDP, "/bpp:24"}, {RDesktop, "-a 24"}} {
		got, err := SetColorDepth("", c.d, "24")
		if err != nil {
			t.Fatal(err)
		}
		if got != c.wantIn {
			t.Errorf("%s: %q, хотим %q", c.d, got, c.wantIn)
		}
		if back := ColorDepth(got, c.d); back != "24" {
			t.Errorf("%s: чтение %q → %q", c.d, got, back)
		}
	}
	// мусор отвергается
	if _, err := SetColorDepth("", XFreeRDP, "17"); err == nil {
		t.Error("bpp 17 принят")
	}
	// снятие
	off, _ := SetColorDepth("/bpp:24 /size:800x600", XFreeRDP, "")
	if off != "/size:800x600" {
		t.Errorf("снятие глубины: %q", off)
	}
}

// Раскладка клавиатуры — та же диалект-зависимая пара.
func TestKeyboardLayoutRoundTrip(t *testing.T) {
	for _, c := range []struct {
		d      Dialect
		wantIn string
	}{{XFreeRDP, "/kbd:en-us"}, {RDesktop, "-k en-us"}} {
		got, err := SetKeyboardLayout("", c.d, "en-us")
		if err != nil {
			t.Fatal(err)
		}
		if got != c.wantIn {
			t.Errorf("%s: %q, хотим %q", c.d, got, c.wantIn)
		}
		if back := KeyboardLayout(got, c.d); back != "en-us" {
			t.Errorf("%s: чтение %q → %q", c.d, got, back)
		}
	}
	off, _ := SetKeyboardLayout("/kbd:en-us", XFreeRDP, "")
	if off != "" {
		t.Errorf("снятие раскладки: %q", off)
	}
}

// Пресеты размеров — валидные «ШxВ», без дублей.
func TestScreenSizePresets(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range ScreenSizePresets() {
		if seen[p] {
			t.Errorf("дубль пресета %q", p)
		}
		seen[p] = true
		if _, _, ok := parseSizeLiteral(p); !ok {
			t.Errorf("пресет %q не разбирается как ШxВ", p)
		}
	}
	if len(ScreenSizePresets()) < 5 {
		t.Errorf("пресетов мало: %v", ScreenSizePresets())
	}
}

// Группы галок: «картиночные» помечены для блока «Экран».
func TestToggleGroups(t *testing.T) {
	var screen []string
	for _, tg := range OptionsFor(XFreeRDP) {
		if InScreenGroup(tg.Setting) {
			screen = append(screen, tg.Setting)
		}
	}
	want := map[string]bool{"compression": true, "fontSmooth": true, "rfx": true, "nsCodec": true}
	for _, s := range screen {
		delete(want, s)
	}
	if len(want) != 0 {
		t.Errorf("в группе «Экран» нет галок: %v", want)
	}
}

// M38: графические опции показываются только RDP-диалектам.
func TestHasScreenOptions(t *testing.T) {
	for _, d := range []Dialect{RDesktop, XFreeRDP} {
		if !HasScreenOptions(d) {
			t.Errorf("%s: графические опции обязаны быть", d)
		}
	}
	for _, d := range []Dialect{SSH, SFTP, Telnet, VNC, Generic} {
		if HasScreenOptions(d) {
			t.Errorf("%s: графических опций быть не должно", d)
		}
	}
}

// M38: разбор «ШxВ» для UI (префилл полей «Свой…»).
func TestSplitSize(t *testing.T) {
	if w, h, ok := SplitSize("1280x1024"); !ok || w != "1280" || h != "1024" {
		t.Fatalf("SplitSize: %q %q %v", w, h, ok)
	}
	for _, bad := range []string{"", "1280", "1280x", "x1024", "1280x0", "-5x10", "axb"} {
		if _, _, ok := SplitSize(bad); ok {
			t.Errorf("%q принят", bad)
		}
	}
}

// Ревью MINOR-2: соседние «+»-токены НЕ должны съедаться как значение ключа
// (setOptValue проверял только «-»/«/», а весь маппер считает «+» флагом).
func TestSetOptValueKeepsPlusTokens(t *testing.T) {
	got, _ := SetKeyboardLayout("-k +fonts", RDesktop, "en-us")
	if !strings.Contains(got, "+fonts") {
		t.Errorf("+fonts съеден при правке раскладки: %q", got)
	}
	got2, _ := SetScreenSize("-g +clipboard", RDesktop, "1024x768")
	if !strings.Contains(got2, "+clipboard") {
		t.Errorf("+clipboard съеден при правке размера: %q", got2)
	}
	got3, _ := SetColorDepth("-a +compression", RDesktop, "24")
	if !strings.Contains(got3, "+compression") {
		t.Errorf("+compression съеден при правке глубины: %q", got3)
	}
}

// Ревью MINOR-1: чтение не должно принимать ФЛАГ за значение ключа —
// писатель такие токены значением не считает (асимметрия давала «-z» в комбо).
func TestOptValueDoesNotReadFlagAsValue(t *testing.T) {
	if v := ColorDepth("-a -z", RDesktop); v != "" {
		t.Errorf("глубина прочитана как %q", v)
	}
	if v := ScreenSize("-g -z", RDesktop); v != "" {
		t.Errorf("размер прочитан как %q", v)
	}
	if v := ScreenSize("-g +fonts", RDesktop); v != "" {
		t.Errorf("размер прочитан как %q", v)
	}
	// неразбираемые значения (проценты) читаются как есть — редактор покажет
	// их отдельным пунктом, а не соврёт пресетом
	if v := ScreenSize("/size:90%", XFreeRDP); v != "90%" {
		t.Errorf("проценты: %q", v)
	}
}
