package notify

// Аудит #8: уведомления через org.freedesktop.Notifications. Полный контракт
// против собственного dbus-daemon + фейковый Notifications-сервис (паттерн
// internal/tray): Send → Notify → id; Close → CloseNotification.

import (
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/godbus/dbus/v5"
)

// fakeNotify — минимальный org.freedesktop.Notifications сервер.
type fakeNotify struct {
	mu       sync.Mutex
	summary  string
	body     string
	lastID   uint32
	closedID uint32
	conn     *dbus.Conn
}

// Notify — D-Bus-метод: вернуть увеличивающийся id, запомнить summary/body.
func (f *fakeNotify) Notify(app string, id uint32, icon, summary, body string,
	actions []string, hints map[string]dbus.Variant, timeout int32) (uint32, *dbus.Error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastID++
	f.summary = summary
	f.body = body
	return f.lastID, nil
}

// CloseNotification — запомнить закрытый id.
func (f *fakeNotify) CloseNotification(id uint32) *dbus.Error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closedID = id
	return nil
}

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
	var b strings.Builder
	buf := make([]byte, 1)
	for b.Len() < 256 {
		if _, err := pipe.Read(buf); err != nil {
			t.Fatal("нет адреса шины")
		}
		b.WriteByte(buf[0])
		if buf[0] == '\n' {
			break
		}
	}
	return strings.TrimSpace(b.String())
}

func TestNotifySend(t *testing.T) {
	addr := dbusAddr(t)
	conn, err := dbus.Connect(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fn := &fakeNotify{conn: conn}
	if err := conn.Export(fn, objectPath, iface); err != nil {
		t.Fatalf("export: %v", err)
	}
	if _, err := conn.RequestName(serviceName, 0); err != nil {
		t.Fatalf("request name: %v", err)
	}

	old := sessionBus
	t.Cleanup(func() { sessionBus = old })
	sessionBus = func() (*dbus.Conn, error) { return dbus.Connect(addr) }

	id, err := Send("Подключено", "Сервер готов", -1)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if id == 0 {
		t.Fatal("id должен быть > 0")
	}
	fn.mu.Lock()
	defer fn.mu.Unlock()
	if fn.summary != "Подключено" || fn.body != "Сервер готов" {
		t.Fatalf("summary/body = %q/%q, want Подключено/Сервер готов", fn.summary, fn.body)
	}
	if fn.lastID != 1 {
		t.Fatalf("lastID = %d, want 1", fn.lastID)
	}
}

func TestNotifyClose(t *testing.T) {
	addr := dbusAddr(t)
	conn, err := dbus.Connect(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fn := &fakeNotify{conn: conn}
	if err := conn.Export(fn, objectPath, iface); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.RequestName(serviceName, 0); err != nil {
		t.Fatal(err)
	}
	old := sessionBus
	t.Cleanup(func() { sessionBus = old })
	sessionBus = func() (*dbus.Conn, error) { return dbus.Connect(addr) }

	id, _ := Send("x", "y", -1)
	if err := Close(id); err != nil {
		t.Fatalf("Close: %v", err)
	}
	fn.mu.Lock()
	defer fn.mu.Unlock()
	if fn.closedID != id {
		t.Fatalf("closedID = %d, want %d", fn.closedID, id)
	}
}

func TestNotifyNoDaemon(t *testing.T) {
	addr := dbusAddr(t)
	old := sessionBus
	t.Cleanup(func() { sessionBus = old })
	sessionBus = func() (*dbus.Conn, error) { return dbus.Connect(addr) }
	if _, err := Send("a", "b", -1); err == nil {
		t.Fatal("без демона — ошибка")
	}
}

// B2: sessionBus по умолчанию должен быть СВЕЖИМ подключением (ConnectSessionBus),
// а не кэш-синглтоном dbus.SessionBus — иначе notify.Send/Close закрыли бы общий
// коннект, на котором живут экспорты трея/IPC (закрытие разорвало бы имя/объекты).
func TestSendUsesFreshSessionBus(t *testing.T) {
	if reflect.ValueOf(sessionBus).Pointer() == reflect.ValueOf(dbus.SessionBus).Pointer() {
		t.Fatal("sessionBus == dbus.SessionBus (кэш-синглтон) — Send закроет общий коннект (B2)")
	}
}
