// Package i18n — локализация Ásbrú (аудит #4, KDE Tier-1). Тонкая обёртка над
// Qt QCoreApplication::translate (context, text) с резолюцией языка из $LANG и
// путём к .qm. Сама логика — чистая (без Qt); Qt-привязка (установка
// QTranslator + реального translate) — в ui (за startup), инжектируемая сюда.
package i18n

import (
	"os"
	"path/filepath"
	"strings"
)

// translate — перевод (context, text)→строку. По умолчанию passthrough (исходный
// текст), реальный Qt-перевод подставляется из ui (SetTranslate). Инжектируемо
// для юнит-тестов (без Qt).
type trFunc func(context, text string) string

var translate trFunc = func(_ string, text string) string { return text }

// SetTranslate — подставляет реальную функцию перевода (Qt) из ui.
func SetTranslate(f trFunc) {
	translate = f
}

// Tr — переведённая строка (context для QCoreApplication::translate).
// ВАЖНО: вызывать только на Qt-ните (список переводчиков читается Qt там);
// пакетный глобал translate не синхронизирован, но безопасен, т.к. SetTranslate
// вызывается один раз при старте, а все чтения — на GUI-ните (аудит #4/MINOR).
func Tr(context, text string) string {
	return translate(context, text)
}

// LangFromEnv — язык из $LANG ("ru_RU.UTF-8"→"ru", "en_US"→"en"). Пустая/не
// распознанная → "" (без перевода/системный). Регистр нормализуется в нижний.
func LangFromEnv(env string) string {
	env = strings.TrimSpace(env)
	if env == "" {
		return ""
	}
	// "$LANG" может быть "ru_RU.UTF-8", "ru_RU", "ru", "C".
	// Берём первый сегмент до '.' или '_' как код языка.
	// Сначала до '.' (кодировка), затем до '_' (страна)/конца.
	seg := env
	if i := strings.IndexAny(seg, ".@"); i >= 0 {
		seg = seg[:i]
	}
	if i := strings.IndexByte(seg, '_'); i >= 0 {
		seg = seg[:i]
	}
	lang := strings.ToLower(seg)
	if lang == "c" || lang == "posix" {
		return ""
	}
	for _, r := range lang {
		if r < 'a' || r > 'z' {
			return ""
		}
	}
	return lang
}

// QMPath — путь к .qm-переводу ресурса (локаль, напр. "ru"):
// dir/asbru_<lang>.qm (KDE-конвенция: asbru_ru.qm).
func QMPath(lang, dir string) string {
	if lang == "" {
		return ""
	}
	return filepath.Join(dir, "asbru_"+lang+".qm")
}

// CurrentLang — язык из окружения (обёртка над LangFromEnv + os.Getenv).
func CurrentLang() string {
	return LangFromEnv(os.Getenv("LANG"))
}
