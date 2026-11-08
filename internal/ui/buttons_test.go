package ui

import "testing"

// Контракт dialogButtons: на действие OK навешивается обязательный onOk.
// nil — «забыл OnClicked»: класс бага 85ecdb1 (мёртвая OK-кнопка). Гвард
// должен падать сразу при построении ряда, а не отдавать диалог с кнопкой,
// которая ничего не делает.

func TestRequireOnOkPanicsOnNil(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("requireOnOk(nil) должен паниковать: OK без действия — структурная ошибка")
		}
	}()
	requireOnOk(nil)
}

func TestRequireOnOkReturnsCallback(t *testing.T) {
	called := false
	got := requireOnOk(func() { called = true })
	if got == nil {
		t.Fatal("requireOnOk вернул nil для валидного колбэка")
	}
	got()
	if !called {
		t.Fatal("requireOnOk вернул не тот колбэк")
	}
}
