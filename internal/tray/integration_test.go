package tray

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// M18 (рекомендация ревьюера): полный контракт SNI против живой шины.
// Собственный dbus-daemon (пользовательская сессия не трогается): сеам
// sessionBus → dbus.Dial(addr), фейковый watcher, затем клиентские вызовы
// как хост.
func TestIntegrationSNIWire(t *testing.T) {
	if _, err := exec.LookPath("dbus-daemon"); err != nil {
		t.Skip("dbus-daemon отсутствует")
	}
	cmd := exec.Command("dbus-daemon", "--session", "--nofork", "--print-address=1")
	addrPipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	buf := make([]byte, 0, 128)
	for {
		b := make([]byte, 1)
		if _, err := addrPipe.Read(b); err != nil {
			t.Fatal("нет адреса шины")
		}
		buf = append(buf, b[0])
		if b[0] == '\n' {
			break
		}
	}
	addr := strings.TrimSpace(string(buf))
	old := sessionBus
	t.Cleanup(func() { sessionBus = old })
	sessionBus = func() (*dbus.Conn, error) { return dbus.Connect(addr) }

	// 1) фейковый watcher принимает регистрацию
	w := &fakeWatcher{reg: make(chan string, 1)}
	// dbus.Dial = private-подключение (без Auth/Hello, «не готово»),
	// поэтому — dbus.Connect, как SessionBus внутри (интеграция в проде).
	conn, err := dbus.Connect(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if e := conn.Export(w, watcherPath, "org.kde.StatusNotifierWatcher"); e != nil {
		t.Fatal(e)
	}
	if _, e := conn.RequestName(watcherName, 0); e != nil {
		t.Fatal(e)
	}

	// 2) старт трея → watcher получил имя org.kde.StatusNotifierItem-<pid>
	var fired, subFired atomic.Bool
	tr, err := Start([]LayoutItem{
		{Label: "Показать", On: func() { fired.Store(true) }},
		{Label: "Выход"},
		{Label: "Подменю", Submenu: []LayoutItem{{Label: "Web", On: func() { subFired.Store(true) }}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	select {
	case got := <-w.reg:
		if !strings.HasPrefix(got, "org.kde.StatusNotifierItem-") {
			t.Fatalf("имя: %q", got)
		}
		if !strings.HasSuffix(got, strconv.Itoa(os.Getpid())) {
			t.Fatalf("суффикс pid: %q", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("watcher не получил регистрацию")
	}

	// 3) GetLayout: кортеж u a(ia{sv}av), НЕ a(v) (классическая регрессия
	// godbus: []interface{} оборачивается в Variant, хост не распарсит).
	rep := conn.Object(serviceName(), menuPath).Call(menuIface+".GetLayout", 0, 0, 0, []string{})
	if rep.Err != nil {
		t.Fatal(rep.Err)
	}
	if rev, ok := rep.Body[0].(uint32); !ok || rev != 1 {
		t.Fatalf("revision: %#v", rep.Body[0])
	}
	switch rv := rep.Body[1].(type) {
	case [][]interface{}:
		if len(rv) != 3 {
			t.Fatalf("rows: %d", len(rv))
		}
		if _, ok := rv[0][1].(map[string]dbus.Variant); !ok {
			t.Fatalf("props: %T", rv[0][1])
		}
	case []interface{}:
		if _, isVar := rv[0].(dbus.Variant); isVar {
			t.Fatal("wire-форма a(v) — регрессия контракта")
		}
		t.Fatalf("rows: %T", rep.Body[1])
	default:
		t.Fatalf("rows: %T", rep.Body[1])
	}

	// 4) GetStatus / GetProperty / Activate / Event(clicked)
	rep = conn.Object(serviceName(), menuPath).Call(menuIface+".GetStatus", 0)
	if rep.Err != nil || rep.Body[0].(string) != "normal" {
		t.Fatalf("GetStatus: %v %#v", rep.Err, rep.Body)
	}
	rep = conn.Object(serviceName(), menuPath).Call(menuIface+".GetProperty", 0, int32(2), "label")
	if rep.Err != nil {
		t.Fatal(rep.Err)
	}
	if s := variantStr(rep.Body[0]); s != "Выход" {
		t.Fatalf("GetProperty label: %q", s)
	}
	rep = conn.Object(serviceName(), itemPath).Call(itemIface+".Activate", 0, int32(0), int32(0))
	if rep.Err != nil {
		t.Fatal(rep.Err)
	}
	if !fired.Load() {
		t.Fatal("Activate не вызвал колбэк")
	}
	// подменю: GetLayout(3) → дети (Web, id 4); Event(4, clicked) → колбэк.
	rep = conn.Object(serviceName(), menuPath).Call(menuIface+".GetLayout", 0, int32(3), 0, []string{})
	if rep.Err != nil {
		t.Fatal(rep.Err)
	}
	rows, ok := rep.Body[1].([][]interface{})
	if !ok || len(rows) != 1 {
		t.Fatalf("подменю rows: %T %#v", rep.Body[1], rep.Body[1])
	}
	rep = conn.Object(serviceName(), menuPath).
		Call(menuIface+".Event", 0, int32(4), "clicked", dbus.MakeVariant(int32(1)), uint32(0))
	if rep.Err != nil {
		t.Fatal(rep.Err)
	}
	if !subFired.Load() {
		t.Fatal("подменю-пункт не вызвал колбэк")
	}
}

// variantStr — label из dbus-ответа (Variant либо строка).
func variantStr(v any) string {
	switch x := v.(type) {
	case dbus.Variant:
		s, _ := x.Value().(string)
		return s
	case string:
		return x
	}
	return ""
}

// fakeWatcher — именованный тип: godbus регистрирует только методы.
type fakeWatcher struct{ reg chan string }

func (w *fakeWatcher) RegisterStatusNotifierItem(s string) *dbus.Error {
	w.reg <- s
	return nil
}
