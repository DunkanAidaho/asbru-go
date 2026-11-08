package ui

import "testing"

// patchEmbed — подмена швов встроенного клиента (тесты без Qt/cgo).
func patchEmbed(stop func(id int, kind EmbedKind), destroy func(id int, kind EmbedKind)) func() {
	oldStop, oldDestroy := stopEmbedFn, destroyEmbedFn
	stopEmbedFn, destroyEmbedFn = stop, destroy
	return func() { stopEmbedFn, destroyEmbedFn = oldStop, oldDestroy }
}

// noopEmbed — швы-заглушки (клиент не трогаем).
func noopEmbed() func() {
	return patchEmbed(func(int, EmbedKind) {}, func(int, EmbedKind) {})
}

// M37: после НЕУДАЧНОГО подключения клиент уже отчитался Done
// (одноразовый m_reported в C++) — повторного колбэка не будет, поэтому
// крестик обязан убрать вкладку сам, иначе она не закрывается вообще.
func TestCloseEmbedTabAfterFailure(t *testing.T) {
	defer noopEmbed()()

	tabMu.Lock()
	tabID[101] = nil
	tabEmbed[101] = EmbedRDP
	tabMu.Unlock()

	embedDone("u1", "HP", 101, false, "connect failed") // ошибка: вкладка остаётся

	if _, ok := tabID[101]; !ok {
		t.Fatal("после ошибки вкладка должна остаться (показ причины)")
	}
	closeEmbedTab(101)
	if _, ok := tabID[101]; ok {
		t.Fatal("вкладка не закрылась крестиком после ошибки подключения")
	}
}

// M37: пока клиент жив — крестик только ПРОСИТ остановиться; вкладку
// убирает embedDone (иначе убьём канвас под работающим потоком).
func TestCloseEmbedTabWhileRunning(t *testing.T) {
	stopped := 0
	defer patchEmbed(func(id int, kind EmbedKind) {
		if id == 102 && kind == EmbedVNC {
			stopped++
		}
	}, func(int, EmbedKind) {})()

	tabMu.Lock()
	tabID[102] = nil
	tabEmbed[102] = EmbedVNC
	tabMu.Unlock()

	closeEmbedTab(102)
	if stopped != 1 {
		t.Fatalf("stopEmbed вызван %d раз, хотим 1", stopped)
	}
	if _, ok := tabID[102]; !ok {
		t.Fatal("вкладка убрана до Done — канвас под живым потоком")
	}

	// клиент остановился сам (Done) → вкладка уходит
	embedDone("u2", "VNC", 102, true, "")
	if _, ok := tabID[102]; ok {
		t.Fatal("после Done вкладка не убрана")
	}
}

// M37: успешное завершение клиента закрывает вкладку (регресс-страховка).
func TestEmbedDoneClosesOnSuccess(t *testing.T) {
	defer noopEmbed()()

	tabMu.Lock()
	tabID[103] = nil
	tabEmbed[103] = EmbedRDP
	tabMu.Unlock()

	embedDone("u3", "WIN", 103, true, "")
	if _, ok := tabID[103]; ok {
		t.Fatal("успешное завершение не закрыло вкладку")
	}
}

// M37: закрытие крестиком до Done (клиент жив) — вкладку убирает Done,
// а не сам крестик; флаг tabEmbedDone после этого не «протухает».
func TestEmbedDoneStateClearedOnRemove(t *testing.T) {
	defer noopEmbed()()

	tabMu.Lock()
	tabID[104] = nil
	tabEmbed[104] = EmbedRDP
	tabMu.Unlock()

	embedDone("u4", "X", 104, true, "")
	tabMu.Lock()
	_, leaked := tabEmbedDone[104]
	tabMu.Unlock()
	if leaked {
		t.Fatal("tabEmbedDone не очищен при удалении вкладки (утечка карты)")
	}
}
