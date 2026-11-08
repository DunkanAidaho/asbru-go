package ui

// Task 9: «Proxmox VE (PVE)» — третий встроенный тип вкладки (EmbedPVE).
// Тесты — по образцу lifecycle-тестов (embedclose_test.go): швы без Qt,
// uiLive=false, PVE-виджет в тестах не создаётся.

import (
	"testing"

	"github.com/DunkanAidaho/asbru-go/internal/config"
)

// embeddedKind по диалекту: PVE — встроенный (EmbedPVE), SSH — нет.
func TestEmbeddedKindPVE(t *testing.T) {
	k, ok := embeddedKind("Proxmox VE (PVE)")
	if !ok || k != EmbedPVE {
		t.Fatalf("kind=%v ok=%v, хотим EmbedPVE/true", k, ok)
	}
	if k, ok := embeddedKind("SSH"); ok {
		t.Fatalf("SSH внезапно embed: %v", k)
	}
}

// launchEmbedded для PVE уходит в launchPveTab и НЕ создаёт cgo-канвас
// (rdpview/vncview): в тестах без Qt это означало бы креш, без ветки —
// путь провалился бы в EmbedRDP.
func TestLaunchEmbeddedPVENoCanvas(t *testing.T) {
	env := &config.Environment{Method: "Proxmox VE (PVE)", Name: "pve-box", IP: "10.0.0.9"}
	launchEmbedded("u-launch", env, EmbedPVE) // uiLive=false: launchPveTab — no-op

	tabMu.Lock()
	defer tabMu.Unlock()
	for id, u := range tabUUID {
		if u == "u-launch" {
			t.Fatalf("PVE-вкладка не должна регистрироваться без Qt (id=%d)", id)
		}
	}
}

// У PVE нет C++-Done — крестик обязан убрать вкладку сам (иначе зомби):
// stopEmbed гасит виджет (pveShutdown), removeTab чистит карты,
// tabCloseReq сбрасывается — ждать Done некому.
func TestCloseEmbedTabPVENoZombie(t *testing.T) {
	oldPost := postMain
	postMain = func(fn func()) { fn() } // тесты без Qt-петли: прямой вызов
	defer func() { postMain = oldPost }()

	tabMu.Lock()
	tabID[121] = nil
	tabUUID[121] = "u-pve"
	tabName[121] = "pve-box"
	tabEmbed[121] = EmbedPVE
	tabMu.Unlock()

	closeEmbedTab(121)

	tabMu.Lock()
	_, alive := tabID[121]
	_, req := tabCloseReq[121]
	tabMu.Unlock()
	if alive {
		t.Fatal("PVE-вкладка осталась после крестика (Done не придёт — убирать должен сам stop)")
	}
	if req {
		t.Fatal("tabCloseReq не очищен — у PVE нет Done, флаг бы протух")
	}
}
