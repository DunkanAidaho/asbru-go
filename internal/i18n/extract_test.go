package i18n

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #E1: экстрактор находит литеральные i18n.Tr(ctx, text) из исходника.
func TestExtractFile(t *testing.T) {
	src := []byte(`
package demo
import "github.com/DunkanAidaho/asbru-go/internal/i18n"
func f() {
	_ = i18n.Tr("Cluster", "Кластеры")
	// многострочный вызов:
	_ = i18n.Tr(
		"Menu",
		"Настройки",
	)
	_ = i18n.Tr("Tray", "Соединение установлено")
	// контекст переменной или конкатенация — пропускаем:
	ctx := "X"
	_ = i18n.Tr(ctx, "skip1")
	_ = i18n.Tr("Skip", "а"+"б")
	// чужой пакет с Tr:
	_ = other.Tr("No", "skip2")
	// третий аргумент/иной пакет-алиас:
	_ = i18n.Tr("A", "b", "extra")
}
`)
	msgs, err := ExtractFile("demo.go", src)
	if err != nil {
		t.Fatalf("ExtractFile err: %v", err)
	}
	got := DedupSort(msgs)
	want := []Message{
		{"Cluster", "Кластеры"},
		{"Menu", "Настройки"},
		{"Tray", "Соединение установлено"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d msgs, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("msg[%d] = %#v, want %#v", i, got[i], want[i])
		}
	}
}

// #E1: backtick-сырые строки и экранированные кавычки распознаются.
func TestExtractFileRawAndEscapes(t *testing.T) {
	src := []byte("package d\nimport \"github.com/DunkanAidaho/asbru-go/internal/i18n\"\nfunc f() {\n\t_ = i18n.Tr(`Raw`, `path \" x`)\n\t_ = i18n.Tr(\"Q\", \"say \\\"hi\\\"\")\n}\n")
	msgs, err := ExtractFile("d.go", src)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	got := DedupSort(msgs)
	want := []Message{
		{"Q", `say "hi"`},
		{"Raw", `path " x`},
	}
	if len(got) != len(want) {
		t.Fatalf("got %#v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("msg[%d]=%q want %q", i, got[i], want[i])
		}
	}
}

// #E1: DedupSort — дедупликация и детерминированный порядок.
func TestDedupSort(t *testing.T) {
	in := []Message{
		{"B", "z"}, {"A", "1"}, {"B", "z"}, {"A", "0"}, {"A", "1"},
	}
	got := DedupSort(in)
	want := []Message{{"A", "0"}, {"A", "1"}, {"B", "z"}}
	if len(got) != len(want) {
		t.Fatalf("len %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("[%d]=%#v want %#v", i, got[i], want[i])
		}
	}
}

// #E1: ExtractTree по каталогу; *_test.go и не-строки пропускаются.
func TestExtractTree(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.go", "package a\nimport \"github.com/DunkanAidaho/asbru-go/internal/i18n\"\nfunc f(){ _ = i18n.Tr(\"A\", \"a\"); _ = i18n.Tr(\"A\", \"b\") }\n")
	write("b_test.go", "package a\nimport \"github.com/DunkanAidaho/asbru-go/internal/i18n\"\nfunc TestX(){ _ = i18n.Tr(\"TestOnly\", \"no\") }\n")
	write("c.go", "package a\nimport \"github.com/DunkanAidaho/asbru-go/internal/i18n\"\nfunc g(){ _ = i18n.Tr(\"A\", \"a\") }\n")

	msgs, err := ExtractTree(dir)
	if err != nil {
		t.Fatalf("ExtractTree err: %v", err)
	}
	got := DedupSort(msgs)
	want := []Message{{"A", "a"}, {"A", "b"}}
	if len(got) != len(want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("[%d]=%#v want %#v", i, got[i], want[i])
		}
	}
}

// #E1: WriteTS — валидный .ts XML; переводы unfinished; пустой lang без атрибута.
func TestWriteTS(t *testing.T) {
	var b strings.Builder
	msgs := []Message{
		{"Cluster", "Кластеры"},
		{"Menu", "Настройки"},
	}
	if err := WriteTS(&b, "ru", msgs); err != nil {
		t.Fatalf("WriteTS: %v", err)
	}
	out := b.String()
	for _, sub := range []string{
		`<?xml version="1.0" encoding="UTF-8"?>`,
		`<TS version="2.1" language="ru" sourcelanguage="en">`,
		`<name>Cluster</name>`,
		`<source>Кластеры</source>`,
		`<translation type="unfinished"></translation>`,
		`<name>Menu</name>`,
		`</TS>`,
	} {
		if !strings.Contains(out, sub) {
			t.Errorf("output missing %q\n%s", sub, out)
		}
	}
	// порядок контекстов: Cluster раньше Menu (DedupSort внутри).
	if strings.Index(out, "<name>Cluster</name>") > strings.Index(out, "<name>Menu</name>") {
		t.Errorf("contexts not sorted:\n%s", out)
	}
}

// #E1: пустой lang → без атрибута language.
func TestWriteTSEmptyLang(t *testing.T) {
	var b strings.Builder
	if err := WriteTS(&b, "", []Message{{"C", "s"}}); err != nil {
		t.Fatalf("err: %v", err)
	}
	if strings.Contains(b.String(), `<TS version="2.1" language="`) {
		t.Errorf("language attribute present for empty lang:\n%s", b.String())
	}
	if !strings.Contains(b.String(), `sourcelanguage="en"`) {
		t.Errorf("sourcelanguage missing:\n%s", b.String())
	}
}

// #E1: XML-экранирование спецсимволов в context/source.
func TestWriteTSEscape(t *testing.T) {
	var b strings.Builder
	if err := WriteTS(&b, "en", []Message{{"a&b", `x<y>"z'`}}); err != nil {
		t.Fatalf("err: %v", err)
	}
	out := b.String()
	if !strings.Contains(out, `<name>a&amp;b</name>`) {
		t.Errorf("context not escaped:\n%s", out)
	}
	if !strings.Contains(out, `<source>x&lt;y&gt;&#34;z&#39;</source>`) {
		t.Errorf("source not escaped:\n%s", out)
	}
}

// #E1-ревью #2: смена алиаса пакета i18n НЕ теряет строки (по import-path).
func TestExtractFileAlias(t *testing.T) {
	src := []byte(`package d
import x "github.com/DunkanAidaho/asbru-go/internal/i18n"
func f() { _ = x.Tr("Alias", "текст") }
`)
	msgs, err := ExtractFile("d.go", src)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(msgs) != 1 || msgs[0] != (Message{"Alias", "текст"}) {
		t.Fatalf("got %#v, want [{Alias текст}]", msgs)
	}
}

// #E1-ревью #1/#2: строки из чужого пакета/не-пакета не ловятся. Затенение
// локальной переменной ровно с именем алиаса пакета остаётся известным
// (латентным) ограничением — в текущем дереве не встречается (ревью E1 #1).
func TestExtractFileNoForeignTr(t *testing.T) {
	src := []byte(`package d
func f() {
	_ = other.Tr("Other", "no")
	_ = other2.Tr("Other2", "no")
}
`)
	msgs, err := ExtractFile("d.go", src)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("expected 0 foreign msgs, got %#v", msgs)
	}
}

// #E1-ревью #5: CatalogFromTS + CheckCatalog — каталог сверяется по (context,source),
// переводы не ломают проверку.
func TestCatalogAndCheck(t *testing.T) {
	ts := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<TS version="2.1" language="ru" sourcelanguage="en">
<context>
    <name>Menu</name>
    <message>
        <source>Settings</source>
        <translation>Настройки</translation>
    </message>
    <message>
        <source>New &amp; X</source>
        <translation type="unfinished"></translation>
    </message>
</context>
</TS>`)
	got, err := CatalogFromTS(ts)
	if err != nil {
		t.Fatalf("CatalogFromTS: %v", err)
	}
	want := []Message{{"Menu", "New & X"}, {"Menu", "Settings"}}
	if len(got) != len(want) {
		t.Fatalf("catalog len %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("cat[%d]=%#v want %#v", i, got[i], want[i])
		}
	}

	// извлечённое подмножество — CheckCatalog пуст (в т.ч. переводы не мешают).
	missing := CheckCatalog([]Message{{"Menu", "Settings"}}, got)
	if len(missing) != 0 {
		t.Fatalf("expected no missing, got %#v", missing)
	}
	// отсутствующее сообщение находится.
	missing = CheckCatalog([]Message{{"Menu", "Settings"}, {"Menu", "Missing"}}, got)
	if len(missing) != 1 || missing[0] != (Message{"Menu", "Missing"}) {
		t.Fatalf("missing = %#v, want [{Menu Missing}]", missing)
	}
}
