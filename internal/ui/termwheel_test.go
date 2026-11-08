package ui

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// cppFuncBody — тело C++-функции по её сигнатуре: от первой '{' после сигнатуры
// до парной '}' (с учётом вложенности). Строчные комментарии срезаются, чтобы
// скобки в тексте комментариев не сбивали счёт. Нужно, чтобы проверять обвязку
// Qt по исходнику: headless-тестами её не достать (стиль
// internal/embed/scancodes_test.go).
func cppFuncBody(t *testing.T, src, sig string) string {
	t.Helper()
	i := strings.Index(src, sig)
	if i < 0 {
		t.Fatalf("в исходнике нет %s", sig)
	}
	var b strings.Builder
	for _, line := range strings.Split(src[i:], "\n") {
		if k := strings.Index(line, "//"); k >= 0 {
			line = line[:k]
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	body := b.String()
	open := strings.Index(body, "{")
	if open < 0 {
		t.Fatalf("у %s нет тела", sig)
	}
	depth := 0
	for idx := open; idx < len(body); idx++ {
		switch body[idx] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return body[open : idx+1]
			}
		}
	}
	t.Fatalf("у %s не нашлась закрывающая скобка", sig)
	return ""
}

// M44: колесо мыши в терминале. Две причины «не крутится» — дефолт истории
// (termopts_test.go: без ключа раньше выходило 0 = HistoryTypeNone) и события
// плавной прокрутки без angleDelta (тачпад/hi-res/Wayland), которые QTermWidget
// не разбирает (Konsole::TerminalDisplay::wheelEvent: `if (ev->angleDelta().y() == 0)
// return;`). Проверяем именно УСЛОВИЕ ВЕТКИ, а не наличие строк: подстановка
// `if (false && …)` (ветка выключена, текст на месте) обязана ронять тест.
func TestTerminalWidgetTranslatesPixelWheel(t *testing.T) {
	raw, err := os.ReadFile("wgt/widget.cpp")
	if err != nil {
		t.Fatalf("не читается wgt/widget.cpp: %v", err)
	}
	body := cppFuncBody(t, string(raw), "bool eventFilter(QObject *watched, QEvent *ev) override")
	cond := regexp.MustCompile(`(?m)^\s*if \(we->angleDelta\(\)\.isNull\(\) && !we->pixelDelta\(\)\.isNull\(\)\) \{`)
	if !cond.MatchString(body) {
		t.Error("в TerminalWidget::eventFilter нет действующей ветки плавного скролла (pixel-only события)")
	}
	for _, want := range []string{
		"ev->type() == QEvent::Wheel",
		"QPoint(0, notches * 120)", // эквивалент дискретного щелчка
		"QApplication::sendEvent(watched, &tr)",
		"m_pixelAccum = 0", // сброс накопления на границе жеста
	} {
		if !strings.Contains(body, want) {
			t.Errorf("в TerminalWidget::eventFilter нет %q", want)
		}
	}
	// Сброс накопления обязан идти ПОСЛЕ добавления кадра: иначе кадр с
	// phase=ScrollEnd, несущий свой pixelDelta, сам заводит остаток.
	if acc, reset := strings.Index(body, "m_pixelAccum += we->pixelDelta().y();"), strings.Index(body, "m_pixelAccum = 0;"); acc < 0 || reset < acc {
		t.Error("сброс накопления стоит до накопления кадра — остаток всё равно переедет в следующий жест")
	}
	// Контекстное меню (user-тест #2) не должно пострадать от новой ветки.
	if !regexp.MustCompile(`(?m)QEvent::ContextMenu[\s\S]*?return true;`).MatchString(body) {
		t.Error("в TerminalWidget::eventFilter пропала обработка контекстного меню")
	}
	// Тело должно быть ограничено функцией, а не «остатком файла».
	if len(body) > 4500 {
		t.Errorf("извлечённое тело eventFilter подозрительно велико (%d байт) — проверка вышла за функцию", len(body))
	}
}
