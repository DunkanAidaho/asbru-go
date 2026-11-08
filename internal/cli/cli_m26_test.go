package cli

// M26 → аудит #8: IPC-форвардинг стартовых флагов живому инстансу через D-Bus
// (org.asbru_cm.Forward; легаси PACMain.pm:264-298 — _sendAppMessage).
// Unix-socket удалён.

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/DunkanAidaho/asbru-go/internal/ipc"
	"github.com/DunkanAidaho/asbru-go/internal/lock"
	ui "github.com/DunkanAidaho/asbru-go/internal/ui"
	"github.com/godbus/dbus/v5"
)

// dbusAddr — адрес изолированного dbus-daemon (паттерн internal/tray).
func dbusAddr(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("dbus-daemon"); err != nil {
		t.Skip("dbus-daemon отсутствует")
	}
	cmd := exec.Command("dbus-daemon", "--session", "--nofork", "--print-address=1")
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	var buf strings.Builder
	b := make([]byte, 1)
	for buf.Len() < 256 {
		if _, err := pipe.Read(b); err != nil {
			t.Fatal("нет адреса шины")
		}
		buf.WriteByte(b[0])
		if b[0] == '\n' {
			break
		}
	}
	return strings.TrimSpace(buf.String())
}

// M26: приоритет сообщения (легаси PACMain.pm:269-282: start-shell >
// quick-conn > start-uuid > edit-uuid; иначе show-conn).
func TestIPCMessagePriority(t *testing.T) {
	cases := []struct {
		start ui.Startup
		want  string
	}{
		{ui.Startup{}, "show-conn"},
		{ui.Startup{StartShell: true}, "start-shell"},
		{ui.Startup{QuickConn: true}, "quick-conn"},
		{ui.Startup{StartUUID: "u1:prod"}, "start-uuid|u1:prod"},
		{ui.Startup{EditUUID: "u2"}, "edit-uuid|u2"},
		// приоритет: start-shell побеждает
		{ui.Startup{StartShell: true, StartUUID: "u1"}, "start-shell"},
	}
	for _, c := range cases {
		if got := ipcMessage(c.start); got != c.want {
			t.Fatalf("ipcMessage(%+v) = %q, want %q", c.start, got, c.want)
		}
	}
}

// Аудит #8: forwardToRunning — шлёт сообщение живому инстансу через D-Bus.
func TestForwardToRunning(t *testing.T) {
	setEnv(t, "ASBRU_CFG", t.TempDir())
	addr := dbusAddr(t)
	ost := ipc.ReplaceSessionBus(func() (*dbus.Conn, error) { return dbus.Connect(addr) })
	defer ost()

	got := make(chan string, 1)
	_, closeFn, err := ipc.Listen(ipc.Name(cfgDir()), func(m string) { got <- m })
	if err != nil {
		t.Fatal(err)
	}
	defer closeFn()

	if err := forwardToRunning(ui.Startup{StartUUID: "u1:prod"}); err != nil {
		t.Fatal(err)
	}
	select {
	case m := <-got:
		if m != "start-uuid|u1:prod" {
			t.Fatalf("msg = %q, want start-uuid|u1:prod", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("сообщение не доставлено")
	}
}

// Аудит #8: launchGUIWith при занятой блокировке форвардит, а не падает.
func TestLaunchGUIForwardsWhenHeld(t *testing.T) {
	setEnv(t, "ASBRU_CFG", t.TempDir())
	addr := dbusAddr(t)
	ost := ipc.ReplaceSessionBus(func() (*dbus.Conn, error) { return dbus.Connect(addr) })
	defer ost()

	got := make(chan string, 1)
	_, closeFn, err := ipc.Listen(ipc.Name(cfgDir()), func(m string) { got <- m })
	if err != nil {
		t.Fatal(err)
	}
	defer closeFn()

	defer func(prev func() (func(), error)) { acquireLock = prev }(acquireLock)
	acquireLock = func() (func(), error) { return nil, lock.ErrHeld }

	if err := launchGUIWith(ui.Startup{QuickConn: true}); err != nil {
		t.Fatalf("ожидали nil при форвардинге, got %v", err)
	}
	select {
	case m := <-got:
		if m != "quick-conn" {
			t.Fatalf("msg = %q, want quick-conn", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("форвардинг не отправлен")
	}
}
