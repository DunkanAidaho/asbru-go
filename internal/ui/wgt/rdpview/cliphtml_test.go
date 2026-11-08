package rdpview

// CF_HTML и CF_TEXT в буфере обмена RDP (M39-остаток). Сборка/разбор заголовка
// CF_HTML и предикат ASCII — чистые функции C-моста; Qt-обвязка (cliprdr
// колбэки, QClipboard/QMimeData) проверяется только cgo-сборкой и трассировкой
// кода: C++-стенда и живого Windows/RDP-сервера на хосте нет.

import (
	"bytes"
	"strings"
	"testing"
)

func mustBuild(t *testing.T, html string) []byte {
	t.Helper()
	out, ok := cfHTMLBuild([]byte(html))
	if !ok {
		t.Fatalf("cfHTMLBuild отверг валидный HTML (%d байт)", len(html))
	}
	return out
}

// Заголовок: Version:0.9 + четыре десятичных поля фиксированной ширины (10
// цифр). Длина заголовка постоянна (105 байт), поэтому смещения — прямые
// байтовые позиции в итоговом UTF-8 payload.
func TestCFHTMLBuildHeader(t *testing.T) {
	html := []byte("<html><body>hello</body></html>")
	payload := mustBuild(t, string(html))

	head := string(payload[:cfHTMLHeaderLen])
	for _, want := range []string{
		"Version:0.9\r\n",
		"StartHTML:0000000105\r\n",
		"EndHTML:0000000136\r\n", // 105 + 31
		"StartFragment:0000000105\r\n",
		"EndFragment:0000000136\r\n",
	} {
		if !strings.Contains(head, want) {
			t.Errorf("заголовок без %q:\n%s", want, head)
		}
	}
	if !bytes.Equal(payload[cfHTMLHeaderLen:], html) {
		t.Fatal("HTML после заголовка повреждён")
	}
}

// Смещения — именно БАЙТОВЫЕ (UTF-8), а не в символах: многобайтовые буквы
// (2/3/4 байта) не должны сдвигать StartFragment/EndFragment.
func TestCFHTMLBuildOffsetsAreBytes(t *testing.T) {
	// «Привет» — 12 байт UTF-8, emoji — 4 байта.
	html := "<p>Привет 🌍</p>"
	payload := mustBuild(t, html)
	start, end, ok := cfHTMLParse(payload)
	if !ok {
		t.Fatal("cfHTMLParse отверг собранный payload")
	}
	if got := string(payload[start:end]); got != html {
		t.Fatalf("фрагмент = %q, ожидалось %q", got, html)
	}
	if int(end-start) != len(html) {
		t.Fatalf("длина фрагмента = %d, ожидалось %d байт", end-start, len(html))
	}
}

// Маркеры <!--StartFragment-->/<!--EndFragment--> внутри HTML: в заголовок
// попадают смещения ВНУТРИ маркеров, а не всего документа.
func TestCFHTMLBuildHonorsFragmentMarkers(t *testing.T) {
	html := "<html><body><!--StartFragment--><b>кусок</b><!--EndFragment--></body></html>"
	payload := mustBuild(t, html)
	start, end, ok := cfHTMLParse(payload)
	if !ok {
		t.Fatal("cfHTMLParse отверг payload с маркерами")
	}
	if got := string(payload[start:end]); got != "<b>кусок</b>" {
		t.Fatalf("фрагмент = %q, ожидалось %q", got, "<b>кусок</b>")
	}
}

// Разбор без заголовка: голый HTML с маркерами — берём маркеры.
func TestCFHTMLParseBareHTMLWithMarkers(t *testing.T) {
	raw := []byte("<div><!--StartFragment-->text<!--EndFragment--></div>")
	start, end, ok := cfHTMLParse(raw)
	if !ok {
		t.Fatal("голый HTML с маркерами отвергнут")
	}
	if got := string(raw[start:end]); got != "text" {
		t.Fatalf("фрагмент = %q, ожидалось %q", got, "text")
	}
}

// Заголовок без маркеров: голый HTML целиком становится фрагментом.
func TestCFHTMLParseBareHTMLWithoutMarkers(t *testing.T) {
	raw := []byte("<p>просто html</p>")
	start, end, ok := cfHTMLParse(raw)
	if !ok {
		t.Fatal("голый HTML отвергнут")
	}
	if start != 0 || int(end) != len(raw) {
		t.Fatalf("фрагмент = [%d,%d), ожидалось [0,%d)", start, end, len(raw))
	}
}

// Битые (нулевые/выходящие за буфер) смещения в заголовке: разбор переходит на
// маркеры-комментарии, а не режет по мусору.
func TestCFHTMLParseMalformedOffsetsFallBackToMarkers(t *testing.T) {
	html := "<!--StartFragment-->ok<!--EndFragment-->"
	payload := mustBuild(t, html)
	// Обнуляем StartFragment: 10 цифр после «StartFragment:».
	i := bytes.Index(payload, []byte("StartFragment:"))
	if i < 0 {
		t.Fatal("в заголовке нет StartFragment")
	}
	copy(payload[i+len("StartFragment:"):], "0000000000")

	start, end, ok := cfHTMLParse(payload)
	if !ok {
		t.Fatal("разбор не восстановился по маркерам")
	}
	if got := string(payload[start:end]); got != "ok" {
		t.Fatalf("фрагмент = %q, ожидалось %q", got, "ok")
	}
}

// Заголовок испорчен, маркеров нет — разбор отказывает, чтобы в буфер не
// попал текст самого заголовка (Version:0.9 и т.п.).
func TestCFHTMLParseRejectsMalformedHeader(t *testing.T) {
	payload := mustBuild(t, "<p>x</p>")
	i := bytes.Index(payload, []byte("EndFragment:"))
	if i < 0 {
		t.Fatal("в заголовке нет EndFragment")
	}
	copy(payload[i+len("EndFragment:"):], "9999999999") // за пределами буфера

	if _, _, ok := cfHTMLParse(payload); ok {
		t.Fatal("битый заголовок без маркеров принят")
	}
}

func TestCFHTMLParseRejectsEmpty(t *testing.T) {
	if _, _, ok := cfHTMLParse(nil); ok {
		t.Fatal("nil принят")
	}
	if _, _, ok := cfHTMLParse([]byte{}); ok {
		t.Fatal("пустой payload принят")
	}
}

func TestCFHTMLBuildRejectsEmpty(t *testing.T) {
	if _, ok := cfHTMLBuild(nil); ok {
		t.Fatal("пустой HTML собран")
	}
	if _, ok := cfHTMLBuild([]byte{}); ok {
		t.Fatal("пустой HTML собран")
	}
}

// CF_TEXT рекламируется только для чистого ASCII: иначе на провод (ANSI-
// кодовую страницу Windows) уехал бы UTF-8 и получилась бы мозаика.
func TestIsASCII(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", true},
		{"hello, world\r\n", true},
		{"\t\n ~!@#$%^&*()", true},
		{"Привет", false},
		{"café", false}, // é — 2 байта UTF-8
		{"🌍", false},
	}
	for _, c := range cases {
		if got := isASCII([]byte(c.in)); got != c.want {
			t.Errorf("isASCII(%q) = %v, ожидалось %v", c.in, got, c.want)
		}
	}
}
