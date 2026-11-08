package ui

import (
	"testing"

	"github.com/DunkanAidaho/asbru-go/internal/pve"
)

// Дерево из фикстуры Task 2 (cluster/resources: нода + qemu + lxc + storage)
// → плоские строки в порядке [нода pve1, qemu 101, lxc 200, сторажи].
func TestPveTreeRowsFixture(t *testing.T) {
	rs := []pve.Resource{
		{ID: "node/pve1", NodeType: "node", Node: "pve1", Name: "pve1", Status: "online"},
		{ID: "qemu/101", NodeType: "qemu", Node: "pve1", Name: "web1", Status: "running", Tags: "prod;web", VMID: 101},
		{ID: "lxc/200", NodeType: "lxc", Node: "pve1", Name: "dns", Status: "stopped", VMID: 200},
		{ID: "storage/pve1/local", NodeType: "storage", Node: "pve1", Name: "pve1", Status: "available"},
	}
	rows := PveTreeRows(rs)
	wantKeys := []string{"node:pve1", "qemu:pve1:101", "lxc:pve1:200", "storagehdr", "storage:pve1:local"}
	if len(rows) != len(wantKeys) {
		t.Fatalf("строк %d, хотим %d: %+v", len(rows), len(wantKeys), rows)
	}
	for i, k := range wantKeys {
		if rows[i].Key != k {
			t.Errorf("rows[%d].Key = %q, хотим %q", i, rows[i].Key, k)
		}
	}
	// нода: корень секции «Ноды», гостей нет — Родитель пуст
	n := rows[0]
	if n.ParentKey != "" || n.Kind != "node" || n.Name != "pve1" || n.Status != "online" || n.VMID != 0 {
		t.Errorf("нода: %+v", n)
	}
	// гость qemu: родитель — нода, теги и статус как в ресурсе
	q := rows[1]
	if q.ParentKey != "node:pve1" || q.Kind != "qemu" || q.VMID != 101 ||
		q.Name != "web1" || q.Status != "running" || q.Tags != "prod;web" {
		t.Errorf("qemu: %+v", q)
	}
	// гость lxc
	l := rows[2]
	if l.ParentKey != "node:pve1" || l.Kind != "lxc" || l.VMID != 200 || l.Name != "dns" || l.Status != "stopped" {
		t.Errorf("lxc: %+v", l)
	}
	// секция «Сторажи»: заголовок, стор с именем из ID (у стора Resource.Name
	// — fallback на имя ноды), статус как есть
	h := rows[3]
	if h.Kind != "storagehdr" || h.ParentKey != "" || h.Name != "Сторажи" {
		t.Errorf("заголовок сторов: %+v", h)
	}
	s := rows[4]
	if s.ParentKey != "storagehdr" || s.Kind != "storage" || s.Name != "local" || s.Status != "available" || s.VMID != 0 {
		t.Errorf("стор: %+v", s)
	}
	// IPs заполняет виджет из кэша — модель отдаёт пусто
	for i, r := range rows {
		if r.IPs != "" {
			t.Errorf("rows[%d].IPs = %q, хотим пусто", i, r.IPs)
		}
	}
}

// Сортировка: секция «Ноды» — ноды по имени, внутри ноды гости по VMID↑;
// затем «Сторажи» (по ноде, потом по имени стора). Без сторов заголовка нет.
func TestPveTreeRowsSorting(t *testing.T) {
	rs := []pve.Resource{
		{ID: "lxc/200", NodeType: "lxc", Node: "pve2", Name: "b", Status: "stopped", VMID: 200},
		{ID: "qemu/101", NodeType: "qemu", Node: "pve2", Name: "a", Status: "running", VMID: 101},
		{ID: "qemu/20", NodeType: "qemu", Node: "pve2", Name: "c", Status: "stopped", VMID: 20},
		{ID: "node/pve2", NodeType: "node", Node: "pve2", Name: "pve2", Status: "online"},
		{ID: "node/alpha", NodeType: "node", Node: "alpha", Name: "alpha", Status: "online"},
		{ID: "qemu/5", NodeType: "qemu", Node: "alpha", Name: "x", Status: "running", VMID: 5},
		{ID: "storage/pve2/local-lvm", NodeType: "storage", Node: "pve2", Name: "pve2", Status: "available"},
	}
	rows := PveTreeRows(rs)
	wantKeys := []string{
		"node:alpha", "qemu:alpha:5",
		"node:pve2", "qemu:pve2:20", "qemu:pve2:101", "lxc:pve2:200",
		"storagehdr", "storage:pve2:local-lvm",
	}
	if len(rows) != len(wantKeys) {
		t.Fatalf("строк %d, хотим %d: %+v", len(rows), len(wantKeys), rows)
	}
	for i, k := range wantKeys {
		if rows[i].Key != k {
			t.Errorf("rows[%d].Key = %q, хотим %q", i, rows[i].Key, k)
		}
	}
	// без сторов секция «Сторажи» не рисуется
	rows = PveTreeRows(rs[:6])
	for _, r := range rows {
		if r.Kind == "storage" || r.Kind == "storagehdr" {
			t.Errorf("лишняя стор-строка без сторов: %+v", r)
		}
	}
}

// Правила контекстного меню по всем статусам/типам строк.
func TestPveMenuAllowed(t *testing.T) {
	rows := []PveRow{
		{Key: "qemu:r", Kind: "qemu", Status: "running"},
		{Key: "qemu:s", Kind: "qemu", Status: "stopped"},
		{Key: "lxc:r", Kind: "lxc", Status: "running"},
		{Key: "lxc:s", Kind: "lxc", Status: "stopped"},
		{Key: "node:o", Kind: "node", Status: "online"},
		{Key: "node:x", Kind: "node", Status: "offline"},
		{Key: "storagehdr", Kind: "storagehdr"},
		{Key: "storage:p:local", Kind: "storage", Status: "available"},
	}
	cases := []struct {
		action string
		want   map[string]bool // Key → разрешено
	}{
		{action: "console", want: map[string]bool{"qemu:r": true, "lxc:r": true}},
		{action: "start", want: map[string]bool{"qemu:s": true, "lxc:s": true}},
		{action: "stop", want: map[string]bool{"qemu:r": true, "lxc:r": true}},
		{action: "reboot", want: map[string]bool{"qemu:r": true, "lxc:r": true}},
		{action: "shutdown", want: map[string]bool{"qemu:r": true, "lxc:r": true}},
		// снапшоты и конфиг — гостю любого статуса, ноде и стору — нет
		{action: "snapshots", want: map[string]bool{
			"qemu:r": true, "qemu:s": true, "lxc:r": true, "lxc:s": true}},
		{action: "config", want: map[string]bool{
			"qemu:r": true, "qemu:s": true, "lxc:r": true, "lxc:s": true}},
	}
	for _, c := range cases {
		allowed := map[string]bool{}
		for _, r := range rows {
			if PveMenuAllowed(r, c.action) {
				allowed[r.Key] = true
			}
		}
		if len(allowed) != len(c.want) {
			t.Errorf("%s: разрешено %v, хотим %v", c.action, allowed, c.want)
			continue
		}
		for k, w := range c.want {
			if allowed[k] != w {
				t.Errorf("%s/%s: %v, хотим %v", c.action, k, allowed[k], w)
			}
		}
	}
	if PveMenuAllowed(rows[0], "неведомо") {
		t.Error("неизвестное действие не может быть разрешено")
	}
}

// Confirm-счётчик: 1 — обратимые, 2 — опасные (двойное подтверждение),
// 0 — неизвестное действие.
func TestPveConfirmCount(t *testing.T) {
	cases := []struct {
		action string
		want   int
	}{
		{"start", 1},
		{"reboot", 1},
		{"snapCreate", 1},
		{"stop", 2},
		{"shutdown", 2},
		{"snapRollback", 2},
		{"snapDelete", 2},
		{"console", 0},
		{"неведомо", 0},
	}
	for _, c := range cases {
		if got := PveConfirmCount(c.action); got != c.want {
			t.Errorf("PveConfirmCount(%q) = %d, хотим %d", c.action, got, c.want)
		}
	}
}

// Кэш IP: слияние без дублей с сохранением порядка; пустой список (агента
// нет) кэш не чистит; показ — join "; ".
func TestPveMergeIPs(t *testing.T) {
	cache := map[int][]string{101: {"10.0.0.2"}}
	PveMergeIPs(cache, 101, []string{"10.0.0.2", "fe80::1"})
	if got := cache[101]; len(got) != 2 || got[0] != "10.0.0.2" || got[1] != "fe80::1" {
		t.Fatalf("слияние: %v", got)
	}
	PveMergeIPs(cache, 101, nil)
	if len(cache[101]) != 2 {
		t.Fatalf("пустой список почистил кэш: %v", cache[101])
	}
	PveMergeIPs(cache, 200, []string{"10.0.0.3"})
	if got := cache[200]; len(got) != 1 || got[0] != "10.0.0.3" {
		t.Fatalf("новый vmid: %v", got)
	}
	if got := PveIPsText(cache[101]); got != "10.0.0.2; fe80::1" {
		t.Fatalf("join: %q", got)
	}
	if got := PveIPsText(nil); got != "" {
		t.Fatalf("join пустых: %q", got)
	}
}

// M45-остаток: консоль доступна и LXC (termproxy → VNC); бэкап — любому
// гостю в любом статусе, одно подтверждение.
func TestPveMenuAllowedLxcConsoleAndBackup(t *testing.T) {
	runLxc := PveRow{Key: "lxc:pve1:200", Kind: "lxc", Status: "running", VMID: 200}
	stopQemu := PveRow{Key: "qemu:pve1:300", Kind: "qemu", Status: "stopped", VMID: 300}
	if !PveMenuAllowed(runLxc, "console") {
		t.Errorf("LXC running: консоль недоступна")
	}
	if PveMenuAllowed(PveRow{Key: "lxc:x:1", Kind: "lxc", Status: "stopped"}, "console") {
		t.Errorf("LXC stopped: консоль должна быть погашена")
	}
	if !PveMenuAllowed(runLxc, "backup") || !PveMenuAllowed(stopQemu, "backup") {
		t.Errorf("backup: должен быть доступен гостю любого статуса")
	}
	if PveConfirmCount("backup") != 1 {
		t.Errorf("backup: ожидалось 1 подтверждение")
	}
}
