package ipc

// Аудит #8: IPC между инстансами Ásbrú через D-Bus (org.asbru_cm.Forth).
// Полный контракт против собственного dbus-daemon (паттерн internal/tray):
// Register name + Export Forward (сервер), клиентский Send → handle.
// Unix-domain сокет удалён (переход только на D-Bus).

import (
	"errors"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// readAddr — прочитать адрес шины из stdout dbus-daemon.
func readAddr(cmd *exec.Cmd, t *testing.T) string {
	t.Helper()
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	buf := make([]byte, 0, 128)
	b := make([]byte, 1)
	for {
		if _, err := pipe.Read(b); err != nil {
			t.Fatal("нет адреса шины")
		}
		buf = append(buf, b[0])
		if b[0] == '\n' {
			break
		}
	}
	return strings.TrimSpace(string(buf))
}

// TestIPCRoundTrip — D-Bus: Send → Forward → handle.
func TestIPCRoundTrip(t *testing.T) {
	if _, err := exec.LookPath("dbus-daemon"); err != nil {
		t.Skip("dbus-daemon отсутствует")
	}
	cmd := exec.Command("dbus-daemon", "--session", "--nofork", "--print-address=1")
	addr := readAddr(cmd, t)
	old := sessionBus
	t.Cleanup(func() { sessionBus = old })
	sessionBus = func() (*dbus.Conn, error) { return dbus.Connect(addr) }

	got := make(chan string, 1)
	conn, closeFn, err := Listen(Name(""), func(msg string) { got <- msg })
	if err != nil {
		t.Fatal(err)
	}
	defer closeFn()
	if conn == nil {
		t.Fatal("conn == nil")
	}
	if err := Send(Name(""), "start-uuid|u1:prod"); err != nil {
		t.Fatal(err)
	}
	select {
	case m := <-got:
		if m != "start-uuid|u1:prod" {
			t.Fatalf("сообщение = %q, want start-uuid|u1:prod", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("сообщение не доставлено")
	}
}

// TestIPCSendNoServer — Send без живого инстанса → ErrNotRunning.
func TestIPCSendNoServer(t *testing.T) {
	if _, err := exec.LookPath("dbus-daemon"); err != nil {
		t.Skip("dbus-daemon отсутствует")
	}
	cmd := exec.Command("dbus-daemon", "--session", "--nofork", "--print-address=1")
	addr := readAddr(cmd, t)
	old := sessionBus
	t.Cleanup(func() { sessionBus = old })
	sessionBus = func() (*dbus.Conn, error) { return dbus.Connect(addr) }

	if err := Send(Name(""), "start-shell"); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("ожидали ErrNotRunning, got %v", err)
	}
}

// TestIPCListenInUse — второй Listen при занятом имени → ErrInUse.
func TestIPCListenInUse(t *testing.T) {
	if _, err := exec.LookPath("dbus-daemon"); err != nil {
		t.Skip("dbus-daemon отсутствует")
	}
	cmd := exec.Command("dbus-daemon", "--session", "--nofork", "--print-address=1")
	addr := readAddr(cmd, t)
	old := sessionBus
	t.Cleanup(func() { sessionBus = old })
	sessionBus = func() (*dbus.Conn, error) { return dbus.Connect(addr) }

	_, closeFn, err := Listen(Name(""), func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	defer closeFn()
	// второй Listen должен вернуть ErrInUse
	if _, _, err := Listen(Name(""), func(string) {}); !errors.Is(err, ErrInUse) {
		t.Fatalf("ожидали ErrInUse, got %v", err)
	}
}

// MINOR-2: Name завязан на каталог конфигурации (разные диры → разные имена).
func TestIPCName(t *testing.T) {
	if Name("") != busName {
		t.Fatalf("Name(\"\") = %q, want %q", Name(""), busName)
	}
	a := Name("/home/u/config/asbru")
	b := Name("/home/u/other/asbru")
	if a == b {
		t.Fatalf("разные каталоги дали одно имя: %q", a)
	}
	if Name("/home/u/config/asbru") != a {
		t.Fatal("Name не детерминирован")
	}
	if len(a) <= len(busName) {
		t.Fatalf("имя не расширено каталогом: %q", a)
	}
}

// MINOR-2: Name нормализует каталог (Clean) — cli/ui дают одно имя даже для
// неканонического ASBRU_CFG (хвостовой слэш/`..`).
func TestIPCNameNormalize(t *testing.T) {
	base := "/home/u/.config/asbru"
	if Name(base+"/") != Name(base) {
		t.Fatalf("trailing slash: %q != %q", Name(base+"/"), Name(base))
	}
	// cli: Name(cfgDir()); ui: Name(filepath.Dir(filepath.Join(cfgDir, "asbru.yml")))
	cfgDir := base + "/"
	cliName := Name(cfgDir)
	uiName := Name(filepath.Dir(filepath.Join(cfgDir, "asbru.yml")))
	if cliName != uiName {
		t.Fatalf("cli %q != ui %q (неканонический cfgDir)", cliName, uiName)
	}
	if Name("/a/../b") != Name("/b") {
		t.Fatal(".. не нормализован")
	}
}

// B2: sessionBus по умолчанию — СВЕЖЕЕ подключение (ConnectSessionBus), не
// кэш-синглтон dbus.SessionBus: иначе ipc.Send закрыл бы общий коннект, на
// котором живут экспорты трея/IPC (имя/объекты исчезли бы насовсем).
func TestSendUsesFreshSessionBus(t *testing.T) {
	if reflect.ValueOf(sessionBus).Pointer() == reflect.ValueOf(dbus.SessionBus).Pointer() {
		t.Fatal("sessionBus == dbus.SessionBus (кэш-синглтон) — ipc.Send закроет общий коннект (B2)")
	}
}
