// Команда asbru-i18n — генератор/проверка Qt Linguist .ts из исходников Go.
//
// lupdate не понимает i18n.Tr (собственная Go-обёртка), поэтому мы сканируем
// исходники сами: cmd/AST-экстрактор из internal/i18n.ExtractTree находит все
// литеральные вызовы i18n.Tr("Context", "Source") и пишет .ts. Затем
// `lrelease6` (Make-цель `make i18n`) компилирует его в .qm для QTranslator.
//
// Режимы:
//
//	-gen  (по умолчанию)  -root internal -lang ru -out i18n/asbru_ru.ts
//	-check <ts>           -root internal -lang ru: проверяет, что все извлечённые
//	                        (context, source) есть в <ts> (без учёта переводов).
//
// Пример:
//
//	go run ./cmd/asbru-i18n -root internal -lang ru -out i18n/asbru_ru.ts
//	go run ./cmd/asbru-i18n -root internal -lang ru -check i18n/asbru_ru.ts
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/DunkanAidaho/asbru-go/internal/i18n"
)

func main() {
	root := flag.String("root", ".", "корень для рекурсивного сканирования *.go")
	lang := flag.String("lang", "", "код локали (напр. ru); пусто = без атрибута language")
	out := flag.String("out", "", "выходной .ts; пусто = asbru_<lang>.ts в текущем каталоге")
	check := flag.String("check", "", "режим проверки: путь к коммитнутому .ts (не пишет, а сверяет каталог)")
	flag.Parse()

	msgs, err := i18n.ExtractTree(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "i18n: extract:", err)
		os.Exit(1)
	}

	if *check != "" {
		ts, err := os.ReadFile(*check)
		if err != nil {
			fmt.Fprintln(os.Stderr, "i18n: read ts:", err)
			os.Exit(1)
		}
		tsMsgs, err := i18n.CatalogFromTS(ts)
		if err != nil {
			fmt.Fprintln(os.Stderr, "i18n: parse ts:", err)
			os.Exit(1)
		}
		missing := i18n.CheckCatalog(msgs, tsMsgs)
		if len(missing) > 0 {
			fmt.Fprintf(os.Stderr, "i18n: %d строк без записи в %s (запустите 'make i18n-ts'):\n", len(missing), *check)
			for _, m := range missing {
				fmt.Fprintf(os.Stderr, "  context=%q source=%q\n", m.Context, m.Source)
			}
			os.Exit(1)
		}
		fmt.Printf("i18n: %d уникальных (context, source) — каталог %s актуален\n", len(msgs), *check)
		return
	}

	if *out == "" {
		name := "asbru.ts"
		if *lang != "" {
			name = "asbru_" + *lang + ".ts"
		}
		*out = name
	}
	if dir := filepath.Dir(*out); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Fprintln(os.Stderr, "i18n: mkdir:", err)
			os.Exit(1)
		}
	}
	f, err := os.Create(*out)
	if err != nil {
		fmt.Fprintln(os.Stderr, "i18n: create:", err)
		os.Exit(1)
	}
	defer f.Close()
	if err := i18n.WriteTS(f, *lang, msgs); err != nil {
		fmt.Fprintln(os.Stderr, "i18n: write:", err)
		os.Exit(1)
	}
	fmt.Printf("i18n: %d уникальных (context, source) → %s (lang=%q)\n", len(msgs), *out, *lang)
}
