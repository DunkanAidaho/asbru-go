package tray

import (
	"testing"

	"github.com/godbus/dbus/v5"
)

// M18/бэклог: dbusmenu GetLayout — кортежи (id, a{sv}, av) для хостов.
func TestGetLayoutTuple(t *testing.T) {
	m, _ := buildMenu([]LayoutItem{{Label: "Выход"}})
	_, items, err := m.GetLayout(0, 0, nil)
	if err != nil {
		t.Fatalf("GetLayout: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("пунктов: %d", len(items))
	}
	rec := items[0]
	if rec.ID != 1 || len(rec.Props) != 3 || rec.Children == nil {
		t.Fatalf("запись: %#v", items[0])
	}
}

// бэклог: подменю — GetLayout(id подменю) возвращает его детей.
func TestGetLayoutSubmenu(t *testing.T) {
	m, _ := buildMenu([]LayoutItem{
		{Label: "Группы", Submenu: []LayoutItem{
			{Label: "Web", On: func() {}},
			{Label: "DB", On: func() {}},
		}},
	})
	// top-level: 1 пункт (подменю, без On).
	_, items, _ := m.GetLayout(0, 0, nil)
	if len(items) != 1 {
		t.Fatalf("top-level пунктов: %d, хотим 1", len(items))
	}
	sub := items[0]
	if sub.ID != 1 {
		t.Fatalf("id подменю: %d", sub.ID)
	}
	if _, ok := sub.Props["children-display"]; !ok {
		t.Fatalf("у подменю нет children-display: %#v", sub.Props)
	}
	// дети подменю.
	_, kids, _ := m.GetLayout(1, 0, nil)
	if len(kids) != 2 {
		t.Fatalf("детей подменю: %d, хотим 2", len(kids))
	}
	if kids[0].ID != 2 || kids[0].Props["label"].Value().(string) != "Web" {
		t.Fatalf("первый ребёнок: %#v", kids[0])
	}
	if kids[1].ID != 3 || kids[1].Props["label"].Value().(string) != "DB" {
		t.Fatalf("второй ребёнок: %#v", kids[1])
	}
}

// M18: Event(requested from host) → колбэк (и для пункта подменю).
func TestMenuEvent(t *testing.T) {
	got := 0
	m, _ := buildMenu([]LayoutItem{
		{Label: "Группы", Submenu: []LayoutItem{{Label: "Web", On: func() { got++ }}}},
	})
	if e := m.Event(2, "clicked", dbus.Variant{}, 0); e != nil {
		t.Fatalf("event: %v", e)
	}
	if got != 1 {
		t.Fatal("колбэк подменю не вызван")
	}
	if e := m.Event(9, "clicked", dbus.Variant{}, 0); e != nil {
		t.Fatal("неизвестный id — ошибка")
	}
}

// M18: GetProperty по свойствам пункта.
func TestMenuGetProperty(t *testing.T) {
	m, _ := buildMenu([]LayoutItem{{Label: "Показать"}})
	v, e := m.GetProperty(1, "label")
	if e != nil || v.Value().(string) != "Показать" {
		t.Fatalf("label: %v (%v)", v, e)
	}
	if _, e := m.GetProperty(1, "nope"); e == nil {
		t.Fatal("неизвестное свойство принято")
	}
}

// M18: SNI-клики (Activate/ContextMenu → show).
func TestTrayClicks(t *testing.T) {
	got := 0
	tr := &Tray{show: func() { got++ }}
	if e := tr.Activate(0, 0); e != nil {
		t.Fatalf("activate: %v", e)
	}
	if e := tr.ContextMenu(0, 0); e != nil {
		t.Fatalf("context: %v", e)
	}
	if got != 2 {
		t.Fatalf("колбэки: %d", got)
	}
}
