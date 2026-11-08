package ui

import "testing"

// M37 (п.3): глаз у поля пароля. Иконка отражает ДЕЙСТВИЕ кнопки:
// пароль скрыт → «показать» (view-visible); пароль виден → «скрыть» (view-hidden).
func TestPassEyeIcon(t *testing.T) {
	if got := passEyeIcon(false); got != "view-visible" {
		t.Fatalf("скрытый пароль: иконка %q, хотим view-visible", got)
	}
	if got := passEyeIcon(true); got != "view-hidden" {
		t.Fatalf("видимый пароль: иконка %q, хотим view-hidden", got)
	}
}

// M37: переключение эха — пароль ↔ обычный текст, и иконка меняется вместе с ним.
func TestPassToggleState(t *testing.T) {
	shown, echo, icon := passToggleState(false)
	if !shown || echo != passEchoNormal || icon != "view-hidden" {
		t.Fatalf("скрыт→показан: shown=%v echo=%d icon=%q", shown, echo, icon)
	}
	shown, echo, icon = passToggleState(true)
	if shown || echo != passEchoPassword || icon != "view-visible" {
		t.Fatalf("виден→скрыт: shown=%v echo=%d icon=%q", shown, echo, icon)
	}
}
