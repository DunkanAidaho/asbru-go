package ui

// Тесты фиксов исполнителя (pvewidget.go): нода строки стора, node-aware
// кэш IP и освобождение клиента (Close) на выходе pveLoop/trackTask.
// Голые struct pveWidget без Qt-объектов; postMain подменяется прямым вызовом.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DunkanAidaho/asbru-go/internal/config"
	"github.com/DunkanAidaho/asbru-go/internal/pve"
)

// Строка стора: ParentKey — «storagehdr», нода берётся из Key
// («storage:<node>:<name>»), иначе фильтр бэкапа по ноде не находит сторы.
func TestPveRowNodeStorageFromKey(t *testing.T) {
	row := PveRow{Key: "storage:pve1:local", ParentKey: "storagehdr", Kind: "storage"}
	if got := pveRowNode(row); got != "pve1" {
		t.Fatalf("pveRowNode(storage) = %q, хотим pve1", got)
	}
	// неразбираемый ключ стора — пусто, без паники
	if got := pveRowNode(PveRow{Key: "storage:local", Kind: "storage"}); got != "" {
		t.Errorf("кривой ключ стора: %q, хотим пусто", got)
	}
	// гость по-прежнему из ParentKey
	if got := pveRowNode(PveRow{Key: "qemu:pve1:101", ParentKey: "node:pve1", Kind: "qemu"}); got != "pve1" {
		t.Errorf("гость: %q, хотим pve1", got)
	}
}

// Кэш IP node-aware: одинаковый VMID на разных нодах — разные записи.
func TestPveIPCacheNodeAware(t *testing.T) {
	k1, k2 := pveIPKey("pve1", 101), pveIPKey("pve2", 101)
	if k1 == k2 {
		t.Fatalf("ключи совпали: %q", k1)
	}
	cache := map[string][]string{}
	pveCacheIPs(cache, k1, []string{"10.0.0.2"})
	pveCacheIPs(cache, k2, []string{"10.0.0.3"})
	if got := PveIPsText(cache[k1]); got != "10.0.0.2" {
		t.Errorf("pve1: %q", got)
	}
	if got := PveIPsText(cache[k2]); got != "10.0.0.3" {
		t.Errorf("pve2: %q", got)
	}
	// набор замещается (DHCP), пустой ответ кэш не чистит
	pveCacheIPs(cache, k1, []string{"10.0.0.9"})
	pveCacheIPs(cache, k1, nil)
	if got := PveIPsText(cache[k1]); got != "10.0.0.9" {
		t.Errorf("после замещения/пустого: %q", got)
	}
}

// pveLoop обязан освободить клиент на выходе (отмена ctx из pveShutdown).
func TestPveLoopClosesClientOnCancel(t *testing.T) {
	old := closePveClient
	defer func() { closePveClient = old }()
	closed := make(chan struct{}, 1)
	closePveClient = func(c *pve.Client) { closed <- struct{}{} }

	ctx, cancel := context.WithCancel(context.Background())
	w := &pveWidget{ctx: ctx, cancel: cancel, ops: make(chan pveOp), client: &pve.Client{}}
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.pveLoop()
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("pveLoop не завершился после отмены")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close не вызван на выходе pveLoop")
	}
}

// trackTask создаёт короткоживущий клиент — обязан закрыть его в конце.
func TestPveTrackTaskClosesClient(t *testing.T) {
	oldNew, oldClose, oldPost := newPveClient, closePveClient, postMain
	defer func() { newPveClient, closePveClient, postMain = oldNew, oldClose, oldPost }()
	postMain = func(fn func()) { fn() }

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/access/ticket"):
			w.Write([]byte(`{"data":{"ticket":"PVE:root@pam:t","CSRFPreventionToken":"CSRF"}}`))
		case strings.HasSuffix(r.URL.Path, "/status"):
			w.Write([]byte(`{"data":{"status":"stopped","exitstatus":"OK"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	newPveClient = func(string, bool, time.Duration) *pve.Client {
		return pve.NewClient(srv.URL, true, time.Second)
	}
	closed := make(chan struct{}, 1)
	closePveClient = func(c *pve.Client) { c.Close(); closed <- struct{}{} }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := &pveWidget{
		id:     999999, // нет в tabID — alive() == false, Qt-объекты не нужны
		ctx:    ctx,
		cancel: cancel,
		env:    &config.Environment{IP: "pve1", Port: "8006", User: "root@pam", Pass: "pw"},
	}
	w.trackTask("UPID:pve1:x:x:qemu:start:root@pam:", "запуск", nil)
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("Close не вызван в конце trackTask")
	}
}
