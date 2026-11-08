// Package tray: SNI-трей (org.kde.StatusNotifierItem) через D-Bus
// (godbus). Plasma-требование: Gtk-StatusIcon не работает, заменяем
// на SNI (P1-11 аудита; легаси PACTray/PACTrayUnity).
//
// Меню правой кнопки — com.canonical.dbusmenu (единственный интерфейс,
// который хосты открывают по свойству Menu; ревью M18 H2). Подменю —
// через те же пункты (родитель с Submenu): хост раскрывает их вызовом
// GetLayout(id родителя) (бэклог «tray-подменю»).
package tray

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/DunkanAidaho/asbru-go/internal/i18n"
	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/prop"
)

// sessionBus — подключение к сессионной шине (инжектируемо в тестах:
// интеграция крутит свой dbus-daemon, пользовательский не трогается).
var sessionBus = dbus.SessionBus

const (
	itemIface = "org.kde.StatusNotifierItem"
	menuIface = "com.canonical.dbusmenu"
	// Хосты фильтруют по префиксу org.kde.StatusNotifierItem → <pid>
	watcherName = "org.kde.StatusNotifierWatcher"
	watcherPath = dbus.ObjectPath("/StatusNotifierWatcher")
	itemPath    = dbus.ObjectPath("/StatusNotifierItem")
	menuPath    = dbus.ObjectPath("/StatusNotifierMenu")
	registerTTL = 2 * time.Second // зависший демон не вешает старт GUI
)

// LayoutItem — пункт меню. Если Submenu не пуст — заголовок подменю (On игнорируется);
// id назначаются автоматически при построении дерева (корень меню = 0).
type LayoutItem struct {
	Label   string
	On      func()
	Submenu []LayoutItem // вложенные пункты (подменю)
}

func serviceName() string {
	return "org.kde.StatusNotifierItem-" + fmt.Sprint(os.Getpid())
}

// menuNode — узел дерева меню с уникальным id и предком.
type menuNode struct {
	id     int32
	label  string
	parent int32
	on     func()
	sub    []*menuNode // дети, если это подменю
}

// Tray — SNI-объект на сессионной шине + dbusmenu.
type Tray struct {
	conn  *dbus.Conn
	props *prop.Properties
	menu  *menuObj
	show  func() // левый клик (Activate) — показать окно (первый пункт с колбэком)
}

type menuObj struct {
	nodes map[int32]*menuNode
	root  []*menuNode // top-level пункты (родитель = корень меню 0)
}

// buildMenu — дерево из планировки; возвращает меню + колбэк «show» (первый
// top-level пункт с On — традиционно «Показать»).
func buildMenu(items []LayoutItem) (*menuObj, func()) {
	m := &menuObj{nodes: map[int32]*menuNode{}}
	var show func()
	next := int32(1) // id 0 = корень меню
	var add func(parent int32, items []LayoutItem) []*menuNode
	add = func(parent int32, items []LayoutItem) []*menuNode {
		var out []*menuNode
		for _, it := range items {
			id := next
			next++
			on := it.On
			if len(it.Submenu) > 0 {
				on = nil // подменю — только заголовок (On игнорируется; ревью)
			}
			n := &menuNode{id: id, label: it.Label, parent: parent, on: on}
			m.nodes[id] = n
			if len(it.Submenu) > 0 {
				n.sub = add(id, it.Submenu)
			}
			out = append(out, n)
		}
		return out
	}
	m.root = add(0, items)
	for _, n := range m.root {
		if n.on != nil {
			show = n.on
			break
		}
	}
	return m, show
}

// ---- org.kde.StatusNotifierItem ----

// Activate — левый клик.
func (t *Tray) Activate(x, y int32) *dbus.Error {
	t.triggerShow()
	return nil
}

// SecondaryActivate — двойной/средний клик.
func (t *Tray) SecondaryActivate(x, y int32) *dbus.Error {
	t.triggerShow()
	return nil
}

// ContextMenu — правый клик (если хост обходит Menu-свойство).
func (t *Tray) ContextMenu(x, y int32) *dbus.Error {
	t.triggerShow()
	return nil
}

func (t *Tray) triggerShow() {
	if t.show != nil {
		t.show()
	}
}

// ---- com.canonical.dbusmenu ----

// Event — событие пункта меню (clicked → id).
func (m *menuObj) Event(id int32, eventID string, data dbus.Variant, timestamp uint32) *dbus.Error {
	if eventID == "clicked" {
		if n, ok := m.nodes[id]; ok && n.on != nil {
			n.on()
		}
	}
	return nil
}

// GetLayout — dbusmenu: GetLayout(i,i,as) → (u, a(ia{sv}av)).
// ТИПИЗИРОВАННЫЙ []menuTuple: godbus для []interface{} оборачивает каждый
// элемент в Variant → сигнатура u a(v), а хост ждёт u a(ia{sv}av)
// (ревью M18 — wire-форма: Plasma демаршалит кортежем).
//
// parentID=0 → top-level; иначе — дети пункта-подменю. Подпункты раскрываются
// лениво вызовом GetLayout(id подменю); для них ставим children-display=submenu.
func (m *menuObj) GetLayout(parentID int32, recursionDepth int32, props []string) (uint32, []menuTuple, *dbus.Error) {
	var nodes []*menuNode
	if parentID == 0 {
		nodes = m.root
	} else if n, ok := m.nodes[parentID]; ok {
		nodes = n.sub
	}
	items := make([]menuTuple, 0, len(nodes))
	for _, n := range nodes {
		mp := map[string]dbus.Variant{
			"label":   dbus.MakeVariant(n.label),
			"enabled": dbus.MakeVariant(true),
			"visible": dbus.MakeVariant(true),
		}
		if len(n.sub) > 0 {
			mp["children-display"] = dbus.MakeVariant("submenu")
		}
		items = append(items, menuTuple{ID: n.id, Props: mp, Children: []interface{}{}})
	}
	return 1, items, nil
}

// menuTuple — кортеж пункта dbusmenu (id, a{sv} props, av children).
type menuTuple struct {
	ID       int32
	Props    map[string]dbus.Variant
	Children []interface{}
}

// GetProperty — свойство пункта.
func (m *menuObj) GetProperty(id int32, name string) (dbus.Variant, *dbus.Error) {
	n, ok := m.nodes[id]
	if !ok {
		return dbus.Variant{}, dbus.MakeFailedError(fmt.Errorf("unknown item %d", id))
	}
	switch name {
	case "label":
		return dbus.MakeVariant(n.label), nil
	case "enabled":
		return dbus.MakeVariant(true), nil
	case "visible":
		return dbus.MakeVariant(true), nil
	case "children-display":
		if len(n.sub) > 0 {
			return dbus.MakeVariant("submenu"), nil
		}
		return dbus.Variant{}, dbus.MakeFailedError(fmt.Errorf("unknown property %q", name))
	default:
		return dbus.Variant{}, dbus.MakeFailedError(fmt.Errorf("unknown property %q", name))
	}
}

// GetStatus — dbusmenu: (s status) = "normal" (спека; ревью M18 L3).
func (m *menuObj) GetStatus() (string, *dbus.Error) {
	return "normal", nil
}

// AboutToShow — нет отложенных пунктов.
func (m *menuObj) AboutToShow(id int32) (bool, *dbus.Error) {
	return false, nil
}

// ---- Сборка ----

// Start регистрирует SNI-трей на сессионной шине. Ошибки окружения без
// Plasma-реестра возвращаются — вызывающий продолжает без трея.
func Start(items []LayoutItem) (*Tray, error) {
	if len(items) == 0 {
		items = []LayoutItem{{Label: i18n.Tr("Tray", "Показать")}}
	}
	conn, err := sessionBus()
	if err != nil {
		return nil, err
	}
	name := serviceName()
	reply, err := conn.RequestName(name, dbus.NameFlagDoNotQueue)
	if err != nil {
		return nil, fmt.Errorf("request name: %w", err)
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		return nil, fmt.Errorf("имя %s занято", name)
	}

	menu, show := buildMenu(items)
	t := &Tray{conn: conn, menu: menu, show: show}
	if err := conn.Export(t, itemPath, itemIface); err != nil {
		return nil, err
	}
	if err := conn.Export(menu, menuPath, menuIface); err != nil {
		return nil, err
	}
	t.props, err = prop.Export(conn, itemPath, map[string]map[string]*prop.Prop{
		itemIface: {
			"Category": {Value: "ApplicationStatus"},
			"Id":       {Value: "asbru-cm"},
			"Title":    {Value: "Ásbrú CM"},
			"Status":   {Value: "Active"},
			"IconName": {Value: "utilities-terminal"},
			"Menu":     {Value: menuPath},
		},
	})
	if err != nil {
		return nil, err
	}
	if _, err := prop.Export(conn, menuPath, map[string]map[string]*prop.Prop{
		menuIface: {"Version": {Value: uint32(3)}},
	}); err != nil {
		return nil, err
	}
	// таймаут: зависший демон не вешает старт GUI (L3-ревью M18)
	ctx, cancel := context.WithTimeout(context.Background(), registerTTL)
	defer cancel()
	if err := conn.Object(watcherName, watcherPath).CallWithContext(ctx,
		"RegisterStatusNotifierItem", 0, name).Err; err != nil {
		return nil, err
	}
	return t, nil
}

// Name — сервисное имя (логи).
func (t *Tray) Name() string { return serviceName() }

// Close — снятие трея; обычно не вызывается (трей умирает с процессом).
// ВНИМАНИЕ: закрывает глобальный sessionBus-коннект godbus.
func (t *Tray) Close() { _ = t.conn.Close() }
