package embed

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// M39: контрактный тест таблицы printableScancode из rdpview.cpp.
//
// Зачем: таблица — это ЧИСТЫЕ ДАННЫЕ, где ошибка не падает и не логируется, а
// молча печатает в сессии чужой символ (живой симптом: не работали Ctrl+C/V,
// потому что для букв не было ни одного соответствия). Тот же приём, что уже
// применён к битмаске флагов в bridgeflags_test.go: Go-тест читает C++-файл.
const rdpviewCpp = "../ui/wgt/rdpview/rdpview.cpp"

// \s* после «{» — чтобы переформатирование (clang-format) не ломало разбор.
var scancodePairRe = regexp.MustCompile(`\{\s*Qt::Key_([A-Za-z0-9_]+)\s*,\s*(RDP_SCANCODE_[A-Z0-9_]+)\s*\}`)

// goldenPunct — единственные соответствия, которые НЕ выводимы из имени
// (буквы и цифры обязаны ссылаться на одноимённый макрос — это проверяется
// структурно ниже). Значения — US-раскладка, как в VK_OEM_* самого FreeRDP.
var goldenPunct = map[string]string{
	"Minus":        "RDP_SCANCODE_OEM_MINUS",
	"Equal":        "RDP_SCANCODE_OEM_PLUS",
	"BracketLeft":  "RDP_SCANCODE_OEM_4",
	"BracketRight": "RDP_SCANCODE_OEM_6",
	"Semicolon":    "RDP_SCANCODE_OEM_1",
	"Apostrophe":   "RDP_SCANCODE_OEM_7",
	"QuoteLeft":    "RDP_SCANCODE_OEM_3",
	"Backslash":    "RDP_SCANCODE_OEM_5",
	"Comma":        "RDP_SCANCODE_OEM_COMMA",
	"Period":       "RDP_SCANCODE_OEM_PERIOD",
	"Slash":        "RDP_SCANCODE_OEM_2",
	"Space":        "RDP_SCANCODE_SPACE",
}

// parseScancodeTable — пары (Qt-клавиша, макрос) из таблицы printableScancode.
func parseScancodeTable(t *testing.T) (map[string]string, string) {
	t.Helper()
	src, err := os.ReadFile(rdpviewCpp)
	if err != nil {
		t.Fatalf("не читается %s: %v", rdpviewCpp, err)
	}
	text := string(src)
	i := strings.Index(text, "static uint32_t printableScancode(")
	if i < 0 {
		t.Fatalf("в %s нет printableScancode — тест потерял цель", rdpviewCpp)
	}
	body := text[i:]
	if j := strings.Index(body, "for (const auto& e : tab)"); j > 0 {
		body = body[:j]
	}
	tab := map[string]string{}
	for _, m := range scancodePairRe.FindAllStringSubmatch(body, -1) {
		if _, dup := tab[m[1]]; dup {
			t.Errorf("клавиша Qt::Key_%s встречается в таблице дважды", m[1])
		}
		tab[m[1]] = m[2]
	}
	if len(tab) == 0 {
		t.Fatalf("таблица не распознана (ожидались пары {Qt::Key_X, RDP_SCANCODE_Y})")
	}
	return tab, text
}

// Полнота и корректность пар: буквы и цифры обязаны ссылаться на ОДНОИМЁННЫЙ
// макрос (так вписать число вместо макроса структурно невозможно), пунктуация —
// совпадать с золотой картой, лишних/пропущенных клавиш быть не должно.
func TestPrintableScancodeTable(t *testing.T) {
	tab, _ := parseScancodeTable(t)

	want := map[string]string{}
	for c := 'A'; c <= 'Z'; c++ {
		n := string(c)
		want[n] = "RDP_SCANCODE_KEY_" + n
	}
	for c := '0'; c <= '9'; c++ {
		n := string(c)
		want[n] = "RDP_SCANCODE_KEY_" + n
	}
	for qt, sc := range goldenPunct {
		want[qt] = sc
	}

	for qt, sc := range tab {
		exp, ok := want[qt]
		if !ok {
			t.Errorf("лишняя клавиша в таблице: Qt::Key_%s → %s", qt, sc)
			continue
		}
		if sc != exp {
			t.Errorf("Qt::Key_%s → %s, а должно быть %s (иначе в сессию уйдёт чужой символ)", qt, sc, exp)
		}
	}
	for qt := range want {
		if _, ok := tab[qt]; !ok {
			t.Errorf("в таблице нет Qt::Key_%s — эта клавиша при Ctrl/Alt не отправится вовсе", qt)
		}
	}
	// Обратная сторона дублей: один макрос на две РАЗНЫЕ клавиши (копипаста при
	// правке таблицы) — два Qt-ключа начнут слать одну позицию, и одна из них
	// перестанет работать. Проверка длин выше этого не ловит.
	bySc := map[string]string{}
	for qt, sc := range tab {
		if prev, dup := bySc[sc]; dup {
			t.Errorf("%s назначен и Qt::Key_%s, и Qt::Key_%s — одна клавиша не отправится", sc, prev, qt)
		}
		bySc[sc] = qt
	}
	if len(tab) != len(want) {
		t.Errorf("пар %d, ожидалось %d", len(tab), len(want))
	}
}

// Все макросы таблицы должны существовать в FreeRDP (иначе компилятор бы не
// пропустил, но проверка ловит опечатку в имени при правке без сборки) и
// совпадать по значению с Set 1 для ключевых клавиш — именно эта привязка
// «имя ↔ число» и была источником риска.
func TestPrintableScancodeMacrosExist(t *testing.T) {
	tab, _ := parseScancodeTable(t)

	hdr, err := os.ReadFile("/usr/include/freerdp3/freerdp/scancode.h")
	if err != nil {
		t.Skipf("нет заголовка FreeRDP (%v) — проверка имён пропущена", err)
	}
	// Часть макросов в scancode.h объявлена МНОГОСТРОЧНО (через обратный слэш,
	// например RDP_SCANCODE_OEM_3) — склеиваем продолжения, иначе они «не найдены».
	joined := regexp.MustCompile(`\\\r?\n\s*`).ReplaceAllString(string(hdr), " ")
	defs := map[string]string{}
	for _, m := range regexp.MustCompile(`#define (RDP_SCANCODE_[A-Z0-9_]+)\s+MAKE_RDP_SCANCODE\((0x[0-9A-Fa-f]+)`).
		FindAllStringSubmatch(joined, -1) {
		defs[m[1]] = strings.ToLower(m[2])
	}
	for qt, sc := range tab {
		val, ok := defs[sc]
		if !ok {
			t.Errorf("Qt::Key_%s ссылается на %s, которого нет в scancode.h", qt, sc)
			continue
		}
		// Якорные значения Set 1 — те, где ошибка даёт неверный символ молча.
		switch qt {
		case "A":
			if val != "0x1e" {
				t.Errorf("RDP_SCANCODE_KEY_A = %s, в Set 1 ожидается 0x1e", val)
			}
		case "V":
			if val != "0x2f" {
				t.Errorf("RDP_SCANCODE_KEY_V = %s, в Set 1 ожидается 0x2f", val)
			}
		case "1":
			if val != "0x02" {
				t.Errorf("RDP_SCANCODE_KEY_1 = %s, в Set 1 ожидается 0x02", val)
			}
		case "0":
			if val != "0x0b" {
				t.Errorf("RDP_SCANCODE_KEY_0 = %s, в Set 1 ожидается 0x0b (0 после 9)", val)
			}
		case "Semicolon":
			if val != "0x27" {
				t.Errorf("RDP_SCANCODE_OEM_1 = %s, в Set 1 ожидается 0x27 (';' на US)", val)
			}
		case "Slash":
			if val != "0x35" {
				t.Errorf("RDP_SCANCODE_OEM_2 = %s, в Set 1 ожидается 0x35 ('/' на US)", val)
			}
		case "QuoteLeft":
			if val != "0x29" {
				t.Errorf("RDP_SCANCODE_OEM_3 = %s, в Set 1 ожидается 0x29 ('`' на US)", val)
			}
		case "Space":
			if val != "0x39" {
				t.Errorf("RDP_SCANCODE_SPACE = %s, в Set 1 ожидается 0x39", val)
			}
		}
	}
}
