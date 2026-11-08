package i18n

// Экстрактор строк для локализации (аудит #4 / KDE Tier-1).
//
// lupdate/lupdate-qt5 не понимают вызовы i18n.Tr из Go (они ищут tr() в C++),
// поэтому мы сканируем исходники сами через go/ast: находим литеральные
// вызовы i18n.Tr("Context", "Source") и собираем пары (Context, Source).
// Далее WriteTS пишет Qt Linguist .ts, который lrelease6 компилирует в .qm,
// а installTranslator() в ui поднимает QTranslator по $LANG.
//
// Чистая логика (без Qt) — покрыта юнит-тестами (extract_test.go).

import (
	"bytes"
	"encoding/xml"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Message — одна переводимая строка: контекст (первый аргумент i18n.Tr) и
// исходный текст (второй аргумент). Контекст соответствует <name> в .ts,
// Source — <source>.
type Message struct {
	Context string
	Source  string
}

// ExtractFile — разбирает один исходник .go и возвращает все полулитеральные
// вызовы i18n.Tr(ctx, text). Составные выражения аргументов (конкатенация,
// переменные) пропускаются: экстрактору нужны только реальные литералы.
// Пакет i18n резолвится по import-path (не по имени алиаса), поэтому смена
// алиаса (import x "…/internal/i18n") не теряет строки.
func ExtractFile(path string, src []byte) ([]Message, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	pkg, ok := i18nPkgName(f)
	if !ok {
		return nil, nil // i18n не импортирован — в этом файле строк нет.
	}
	var out []Message
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true // не выражение-вызов — идём вглубь.
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel == nil || sel.Sel.Name != "Tr" {
			return true
		}
		// Часть до точки должна быть идентификатором, равным локальному имени
		// импортируемого пакета i18n (см. i18nPkgName).
		id, ok := sel.X.(*ast.Ident)
		if !ok || id.Name != pkg {
			return true
		}
		if len(call.Args) != 2 {
			return true // i18n.Tr берёт ровно 2 аргумента (context, text).
		}
		ctx, ok1 := strLiteral(call.Args[0])
		text, ok2 := strLiteral(call.Args[1])
		if !ok1 || !ok2 {
			return true
		}
		out = append(out, Message{Context: ctx, Source: text})
		return true
	})
	return out, nil
}

// i18nPkgName — локальное имя пакета i18n (alias при его задании) по import-path
// "…/internal/i18n"; false, если пакет не импортирован в этом файле. Это
// устраняет ложные пропуски при смене алиаса (ревью E1 #2) и лишние срабатывания
// на чужом пакете/локальном значении с методом Tr, если имя импорта иное.
func i18nPkgName(f *ast.File) (string, bool) {
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		if path == "github.com/DunkanAidaho/asbru-go/internal/i18n" || strings.HasSuffix(path, "/internal/i18n") {
			name := "i18n"
			if imp.Name != nil {
				name = imp.Name.Name
			}
			return name, true
		}
	}
	return "", false
}

// strLiteral — извлекает строковой литерал из ast.Expr (двойные кавычки или
// backtick-сырые). Не-литерал / не-строка → (_, false).
func strLiteral(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return s, true
}

// DedupSort — убирает дубликаты (Context, Source) и сортирует по контексту,
// затем по источнику. Детерминированный порядок для стабильного .ts.
func DedupSort(msgs []Message) []Message {
	seen := make(map[Message]bool, len(msgs))
	out := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		if seen[m] {
			continue
		}
		seen[m] = true
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Context != out[j].Context {
			return out[i].Context < out[j].Context
		}
		return out[i].Source < out[j].Source
	})
	return out
}

// ExtractTree — рекурсивно сканирует корень (и подкаталоги) на *.go (кроме
// *_test.go), собирает i18n.Tr и возвращает дедуплицированный отсортированный
// список. Пропускает скрытые каталоги (напр. .gocache) и vendor.
func ExtractTree(root string) ([]Message, error) {
	var msgs []Message
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (name == "vendor" || strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		m, err := ExtractFile(path, src)
		if err != nil {
			return err
		}
		msgs = append(msgs, m...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return DedupSort(msgs), nil
}

// WriteTS — пишет Qt Linguist .ts (XML) с сообщениями. lang (код локали, напр.
// "ru") кладётся в атрибут language; при пустом lang пишем только
// sourcelanguage="en". Переводы помечаются type="unfinished" (пустые) — их
// lrelease по умолчанию не выпускает в .qm, они остаются для переводчика.
func WriteTS(w io.Writer, lang string, msgs []Message) error {
	if _, err := io.WriteString(w, xml.Header); err != nil {
		return err
	}
	if _, err := io.WriteString(w, "<!DOCTYPE TS>\n"); err != nil {
		return err
	}
	if _, err := io.WriteString(w, `<TS version="2.1"`); err != nil {
		return err
	}
	if lang != "" {
		if _, err := io.WriteString(w, ` language="`+esc(lang)+`"`); err != nil {
			return err
		}
	}
	if _, err := io.WriteString(w, ` sourcelanguage="en">`+"\n"); err != nil {
		return err
	}

	msgs = DedupSort(msgs)
	var cur string
	open := false
	for _, m := range msgs {
		if m.Context != cur {
			if open {
				if _, err := io.WriteString(w, "</context>\n"); err != nil {
					return err
				}
			}
			if _, err := io.WriteString(w, "<context>\n    <name>"+esc(m.Context)+"</name>\n"); err != nil {
				return err
			}
			cur = m.Context
			open = true
		}
		if _, err := io.WriteString(w, "    <message>\n        <source>"+esc(m.Source)+"</source>\n        <translation type=\"unfinished\"></translation>\n    </message>\n"); err != nil {
			return err
		}
	}
	if open {
		if _, err := io.WriteString(w, "</context>\n"); err != nil {
			return err
		}
	}
	_, err := io.WriteString(w, "</TS>\n")
	return err
}

// esc — XML-экранирование для содержимого и атрибутов (полагается на
// encoding/xml; корректно экранирует &, <, >, ", ').
func esc(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// tsDoc — миним-структура Qt Linguist .ts для чтения каталога (context/source).
// Переводы (translation) читаются, чтобы не путать их с source, но в каталоге
// участвуют только name+source.
type tsDoc struct {
	Contexts []struct {
		Name     string `xml:"name"`
		Messages []struct {
			Source string `xml:"source"`
		} `xml:"message"`
	} `xml:"context"`
}

// CatalogFromTS — каталог (context, source) из содержимого Qt Linguist .ts.
func CatalogFromTS(ts []byte) ([]Message, error) {
	var doc tsDoc
	if err := xml.Unmarshal(ts, &doc); err != nil {
		return nil, err
	}
	var out []Message
	for _, c := range doc.Contexts {
		for _, m := range c.Messages {
			out = append(out, Message{Context: c.Name, Source: m.Source})
		}
	}
	return DedupSort(out), nil
}

// CheckCatalog — сообщения из extracted, у которых НЕТ пары (context, source)
// в каталоге ts (committed). Пустой результат = каталог актуален. Проверка
// учитывает только ключи (контекст+источник), поэтому заполненные переводы
// в .ts не ломают её (ревью E1 #5).
func CheckCatalog(extracted, ts []Message) []Message {
	have := make(map[Message]bool, len(ts))
	for _, m := range ts {
		have[m] = true
	}
	var missing []Message
	for _, m := range extracted {
		if !have[m] {
			missing = append(missing, m)
		}
	}
	return missing
}
