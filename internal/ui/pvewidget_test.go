package ui

// Тесты ТОЛЬКО чистых функций PVE-виджета (парсинг опций/порта, сборка URL,
// разбор ключей, формат времени) — сборка и запуск без Qt: виджет (pvewidget.go)
// в тестах не создаётся.

import (
	"strings"
	"testing"
)

// pveTlsVerify: токен «verify-tls» в опциях → проверять; по умолчанию
// (пусто/другие опции) — НЕ проверять (self-signed PVE — частый случай).
func TestPveTlsVerify(t *testing.T) {
	cases := []struct {
		options string
		want    bool
	}{
		{"", false},
		{"  ", false},
		{"-xterm-256color", false},
		{"verify-tls", true},
		{"-xterm-256color verify-tls", true},
		{"verify-tls -other=1", true},
		{"'verify-tls'", true}, // кавычки SplitArgs снимает
		{"verify-tlsx", false}, // точный токен, не префикс
		{"-tls", false},
	}
	for _, c := range cases {
		if got := pveTlsVerify(c.options); got != c.want {
			t.Errorf("pveTlsVerify(%q) = %v, хотим %v", c.options, got, c.want)
		}
	}
}

// pvePort: пусто/мусор/вне диапазона → 8006; валидный порт сохраняется.
func TestPvePort(t *testing.T) {
	cases := []struct {
		port string
		want int
	}{
		{"", 8006},
		{"   ", 8006},
		{"abc", 8006},
		{"0", 8006},
		{"-1", 8006},
		{"70000", 8006},
		{"8006", 8006},
		{" 8007 ", 8007},
		{"80", 80},
	}
	for _, c := range cases {
		if got := pvePort(c.port); got != c.want {
			t.Errorf("pvePort(%q) = %d, хотим %d", c.port, got, c.want)
		}
	}
}

// pveBase — https и host:port без пути.
func TestPveBase(t *testing.T) {
	if got := pveBase("pve.example.lan", 8006); got != "https://pve.example.lan:8006" {
		t.Errorf("pveBase = %q", got)
	}
}

// pveRowNode: гость/стор — нода из ParentKey; корень (нода, storagehdr) — пусто.
func TestPveRowNode(t *testing.T) {
	if got := pveRowNode(PveRow{ParentKey: "node:pve1"}); got != "pve1" {
		t.Errorf("гость: %q", got)
	}
	if got := pveRowNode(PveRow{ParentKey: ""}); got != "" {
		t.Errorf("корень: %q", got)
	}
}

// pveGuestCol: гость — «имя (vmid)», нода/стор — имя как есть.
func TestPveGuestCol(t *testing.T) {
	if got := pveGuestCol(PveRow{Name: "web1", VMID: 101}); got != "web1 (101)" {
		t.Errorf("гость: %q", got)
	}
	if got := pveGuestCol(PveRow{Name: "pve1"}); got != "pve1" {
		t.Errorf("нода: %q", got)
	}
}

// pveActionLabel: все мутации имеют человекочитаемую подпись, неизвестное
// действие — как есть (не пустое).
func TestPveActionLabel(t *testing.T) {
	for _, a := range []string{"start", "stop", "reboot", "shutdown",
		"snapCreate", "snapRollback", "snapDelete"} {
		if pveActionLabel(a) == "" {
			t.Errorf("pveActionLabel(%q) пустой", a)
		}
	}
	// Ревью A: снапшотные мутации обязаны иметь ЧЕЛОВЕКОЧИТАЕМУЮ подпись —
	// сырой код в подтверждении опасной операции (откат/удаление) недопустим.
	for _, a := range []string{"snapCreate", "snapRollback", "snapDelete"} {
		if pveActionLabel(a) == a {
			t.Errorf("pveActionLabel(%q) вернул сырой код действия", a)
		}
	}
	if got := pveActionLabel("неведомо"); got != "неведомо" {
		t.Errorf("неизвестное: %q", got)
	}
}

// pveConfigText: ключи сортируются, формат «ключ: значение».
func TestPveConfigText(t *testing.T) {
	got := pveConfigText(map[string]any{"memory": 512, "hostname": "web1"})
	want := "hostname: web1\nmemory: 512\n"
	if got != want {
		t.Fatalf("pveConfigText = %q, хотим %q", got, want)
	}
	if got := pveConfigText(nil); got != "" {
		t.Fatalf("пустой конфиг: %q", got)
	}
}

// pveSnapshotTime: 0 (время не задано) → пусто; ненулевое → непустая строка.
func TestPveSnapshotTime(t *testing.T) {
	if got := pveSnapshotTime(0); got != "" {
		t.Errorf("нулевое время: %q", got)
	}
	if got := pveSnapshotTime(1700000000); !strings.Contains(got, "2023") {
		t.Errorf("формат времени: %q", got)
	}
}

// pveStatusRGB: семантика цвета статуса (A2) — running/online зелёный,
// stopped/offline серый, прочее янтарный; пустой статус (строка-заголовок
// storagehdr) не красится.
func TestPveStatusRGB(t *testing.T) {
	cases := []struct {
		status string
		rgb    [3]int
		ok     bool
	}{
		{"running", [3]int{32, 160, 64}, true},
		{"online", [3]int{32, 160, 64}, true},
		{"stopped", [3]int{128, 128, 128}, true},
		{"offline", [3]int{128, 128, 128}, true},
		{"unknown", [3]int{200, 140, 32}, true},
		{"paused", [3]int{200, 140, 32}, true},
		{"", [3]int{}, false},
	}
	for _, c := range cases {
		r, g, b, ok := pveStatusRGB(c.status)
		if ok != c.ok || r != c.rgb[0] || g != c.rgb[1] || b != c.rgb[2] {
			t.Errorf("pveStatusRGB(%q) = (%d,%d,%d,%v), хотим %v/%v", c.status, r, g, b, ok, c.rgb, c.ok)
		}
	}
}

// Ревью 2 (Minor): IPv6-адрес в env.IP без скобок давал невалидный URL.
func TestPveBaseIPv6(t *testing.T) {
	cases := map[string]string{
		("https://192.0.2.10:8006"): pveBase("192.0.2.10", 8006),
		("https://pve1.lan:8006"):    pveBase("pve1.lan", 8006),
		("https://[::1]:8006"):       pveBase("::1", 8006),
		("https://pve1:8006"):        pveBase("pve1", 8006),
	}
	for want, got := range cases {
		if got != want {
			t.Errorf("pveBase → %q, want %q", got, want)
		}
	}
}
