package i18n

import (
	"testing"
)

// #5: разбор $LANG → код языка.
func TestLangFromEnv(t *testing.T) {
	cases := []struct {
		env  string
		want string
	}{
		{"ru_RU.UTF-8", "ru"},
		{"ru_RU", "ru"},
		{"ru", "ru"},
		{"RU.UTF-8", "ru"},
		{"en_US.UTF-8", "en"},
		{"en_GB", "en"},
		{"de_DE@euro", "de"},
		{"C", ""},
		{"POSIX", ""},
		{"C.UTF-8", ""},
		{"../../evil", ""},
		{"../en", ""},
		{"foo/bar", ""},
		{"", ""},
		{"   ", ""},
		{"ru_RU.UTF-8 ", "ru"},
	}
	for _, c := range cases {
		if got := LangFromEnv(c.env); got != c.want {
			t.Errorf("LangFromEnv(%q) = %q, want %q", c.env, got, c.want)
		}
	}
}

// #5: tr() индирекция — по умолчанию passthrough, после SetTranslate — реальный.
func TestTrIndirection(t *testing.T) {
	if got := Tr("Ásbrú CM", "Настроек"); got != "Настроек" {
		t.Fatalf("passthrough: %q", got)
	}
	captured := ""
	SetTranslate(func(context, text string) string {
		captured = context + "|" + text
		return "переведено"
	})
	if got := Tr("Menu", "Настройки"); got != "переведено" {
		t.Fatalf("translate: %q", got)
	}
	if captured != "Menu|Настройки" {
		t.Fatalf("context+text = %q", captured)
	}
	// restore passthrough
	SetTranslate(func(_, text string) string { return text })
	if got := Tr("Menu", "Настройки"); got != "Настройки" {
		t.Fatalf("after restore: %q", got)
	}
}

func TestQMPath(t *testing.T) {
	if got := QMPath("ru", "/usr/share/asbru"); got != "/usr/share/asbru/asbru_ru.qm" {
		t.Fatalf("QMPath = %q", got)
	}
	if got := QMPath("", "/usr/share/asbru"); got != "" {
		t.Fatalf("QMPath empty lang = %q", got)
	}
}
