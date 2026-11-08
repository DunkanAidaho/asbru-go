package ui

// Task 10: консоль RUNNING qemu PVE — VNCProxy REST-API → вкладка vncview.
// Тесты — швы без Qt/cgo и БЕЗ живых серверов (по образцу stopEmbedFn):
// pveVncProxyFn/pveVncCreateFn/pveVncStartFn/pveSetConnStatus подменяются.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DunkanAidaho/asbru-go/internal/config"
	"github.com/DunkanAidaho/asbru-go/internal/pve"
	"github.com/DunkanAidaho/asbru-go/internal/ui/wgt/vncview"
)

const (
	pveConMgmtID    = 231 // фейковая PVE-вкладка управления
	pveConConsoleID = 232 // фейковая вкладка консоли (вместо cgo-канваса)
)

// patchPveConsole — подмена швов консоли; возвращает func() отката.
func patchPveConsole(proxy func(context.Context, *pve.Client, string, string, string, int) (pve.VNCProxy, error),
	create func(*vncview.Callback) (int, error),
	start func(int, string, int, string, string, string, string)) func() {
	oldProxy, oldCreate, oldStart := pveVncProxyFn, pveVncCreateFn, pveVncStartFn
	pveVncProxyFn, pveVncCreateFn, pveVncStartFn = proxy, create, start
	return func() { pveVncProxyFn, pveVncCreateFn, pveVncStartFn = oldProxy, oldCreate, oldStart }
}

// regPveConTabs — фейковые вкладки: PVE-управление + место под консоль.
func regPveConTabs(t *testing.T) {
	t.Helper()
	tabMu.Lock()
	tabID[pveConMgmtID] = nil
	tabUUID[pveConMgmtID] = "u-pve"
	tabName[pveConMgmtID] = "pve-box"
	tabEmbed[pveConMgmtID] = EmbedPVE
	tabMu.Unlock()
	t.Cleanup(func() {
		tabMu.Lock()
		for _, id := range []int{pveConMgmtID, pveConConsoleID} {
			delete(tabID, id)
			delete(tabUUID, id)
			delete(tabName, id)
			delete(tabEmbed, id)
			delete(tabLog, id)
			delete(tabConn, id)
			delete(tabEmbedDone, id)
			delete(tabCloseReq, id)
		}
		tabMu.Unlock()
	})
}

func pveConEnv() *config.Environment {
	return &config.Environment{
		Method: "Proxmox VE (PVE)", Name: "pve-box",
		IP: "192.0.2.10", Port: "8006", User: "root@pam", Pass: "secret",
	}
}

// Удачный vncproxy: Start зовётся с хостом/портом из ответа, пароль — тикет,
// security "none" (классический VNC, не VeNCrypt); вкладка регистрируется
// как EmbedVNC с uuid PVE-соединения и титулом «гость — консоль».
func TestPveOpenConsoleSuccess(t *testing.T) {
	regPveConTabs(t)

	oldPost := postMain
	postMain = func(fn func()) { fn() } // тесты без Qt-петли: прямой вызов
	defer func() { postMain = oldPost }()

	oldClient := newPveClient
	newPveClient = func(string, bool, time.Duration) *pve.Client { return nil }
	defer func() { newPveClient = oldClient }()

	proxyNode, proxyVMID := "", 0
	started := make(chan struct{})
	var startID, startPort int
	var startHost, startUser, startPass, startTitle, startSec string
	defer patchPveConsole(
		func(_ context.Context, _ *pve.Client, _, _ string, node string, vmid int) (pve.VNCProxy, error) {
			proxyNode, proxyVMID = node, vmid
			return pve.VNCProxy{Port: 34567, Ticket: "TICKET-0123456789abcdef"}, nil
		},
		func(*vncview.Callback) (int, error) { return pveConConsoleID, nil },
		func(id int, host string, port int, user, pass, title, security string) {
			startID, startHost, startPort = id, host, port
			startUser, startPass, startTitle, startSec = user, pass, title, security
			close(started)
		},
	)()

	pveOpenConsole("u-pve", pveConEnv(), "pve1", "qemu", 101, "web1")

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("vncview.Start не вызван после удачного vncproxy")
	}
	if proxyNode != "pve1" || proxyVMID != 101 {
		t.Errorf("VNCProxy(node=%q vmid=%d), хотим pve1/101", proxyNode, proxyVMID)
	}
	if startID != pveConConsoleID {
		t.Errorf("Start id=%d, хотим %d", startID, pveConConsoleID)
	}
	if startHost != "pve1" || startPort != 34567 {
		t.Errorf("Start хост:порт = %s:%d, хотим pve1:34567", startHost, startPort)
	}
	if startUser != "" {
		t.Errorf("Start user=%q, хотим пусто (vncproxy без пользователя)", startUser)
	}
	if startPass != "TICKET-0123456789abcdef" {
		t.Errorf("Start pass=%q, хотим тикет vncproxy", startPass)
	}
	if startTitle != "web1 — консоль" {
		t.Errorf("титул вкладки %q, хотим %q", startTitle, "web1 — консоль")
	}
	if startSec != "none" {
		t.Errorf("security=%q, хотим none (классический VNC, не VeNCrypt)", startSec)
	}

	tabMu.Lock()
	kind, uuid, name := tabEmbed[pveConConsoleID], tabUUID[pveConConsoleID], tabName[pveConConsoleID]
	tabMu.Unlock()
	if kind != EmbedVNC {
		t.Errorf("tabEmbed=%v, хотим EmbedVNC", kind)
	}
	if uuid != "u-pve" {
		t.Errorf("tabUUID=%q, хотим uuid PVE-соединения u-pve", uuid)
	}
	if name != "web1 — консоль" {
		t.Errorf("tabName=%q, хотим %q", name, "web1 — консоль")
	}
}

// Ошибка vncproxy: вкладка консоли НЕ создаётся (Create/Start не звались),
// статус ошибки уходит на вкладку управления PVE.
func TestPveOpenConsoleProxyError(t *testing.T) {
	regPveConTabs(t)

	oldPost := postMain
	postMain = func(fn func()) { fn() }
	defer func() { postMain = oldPost }()

	oldClient := newPveClient
	newPveClient = func(string, bool, time.Duration) *pve.Client { return nil }
	defer func() { newPveClient = oldClient }()

	statusDone := make(chan struct{})
	var statusID int
	var statusState connState
	defer patchPveConsole(
		func(context.Context, *pve.Client, string, string, string, int) (pve.VNCProxy, error) {
			return pve.VNCProxy{}, errors.New("PVE 500: no such guest")
		},
		func(*vncview.Callback) (int, error) {
			t.Error("Create при ошибке vncproxy вызываться не должен")
			return 0, errors.New("не должен зваться")
		},
		func(int, string, int, string, string, string, string) {
			t.Error("Start при ошибке vncproxy вызываться не должен")
		},
	)()
	oldStatus := pveSetConnStatus
	pveSetConnStatus = func(id int, st connState, text string) {
		statusID, statusState = id, st
		close(statusDone)
	}
	defer func() { pveSetConnStatus = oldStatus }()

	pveOpenConsole("u-pve", pveConEnv(), "pve1", "qemu", 101, "web1")

	select {
	case <-statusDone:
	case <-time.After(2 * time.Second):
		t.Fatal("статус connError при ошибке vncproxy не выставлен")
	}
	if statusID != pveConMgmtID {
		t.Errorf("статус ушел на id=%d, хотим на PVE-вкладку управления %d", statusID, pveConMgmtID)
	}
	if statusState != connError {
		t.Errorf("состояние=%v, хотим connError", statusState)
	}
	tabMu.Lock()
	_, created := tabEmbed[pveConConsoleID]
	tabMu.Unlock()
	if created {
		t.Fatal("вкладка консоли создана при ошибке vncproxy")
	}
}
