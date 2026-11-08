// Пакет notify — отправка уведомлений рабочего стола через D-Bus
// org.freedesktop.Notifications (аудит #8, нативно к KDE/Plasma; лёгкий
// фолбэк для любого клиента). Инжектируемая шина для юнита.

package notify

import (
	"github.com/godbus/dbus/v5"
)

const (
	serviceName = "org.freedesktop.Notifications"
	objectPath  = dbus.ObjectPath("/org/freedesktop/Notifications")
	iface       = "org.freedesktop.Notifications"
)

// IconApp — иконка приложения в уведомлениях (по имени (тема) или пути).
const IconApp = "asbru-cm"

// sessionBus — подключение к сессионной шине (инжектируемо в тестах).
// По умолчанию СВЕЖЕЕ подключение (ConnectSessionBus), НЕ кэш-синглтон
// dbus.SessionBus: otherwise Send/Close would close the shared conn on which
// the tray/IPC exports live (B2 — killed them for good, dead tray menu/IPC).
var sessionBus = func() (*dbus.Conn, error) { return dbus.ConnectSessionBus() }

// Send — показать уведомление. summary — заголовок, body — текст.
// timeoutMs — авто-скрытие (мс); -1 (или <=0) — по-умолчанию сервера.
// Возвращает id уведомления (для Close). Ошибка — если нет демона уведомлений.
func Send(summary, body string, timeoutMs int) (uint32, error) {
	conn, err := sessionBus()
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	var id uint32
	err = conn.Object(serviceName, objectPath).Call(iface+".Notify", 0,
		"asbru-cm", uint32(0), IconApp, summary, body,
		[]string{}, map[string]dbus.Variant{}, timeoutMs).Store(&id)
	return id, err
}

// Close — закрыть уведомление по id (best-effort).
func Close(id uint32) error {
	conn, err := sessionBus()
	if err != nil {
		return err
	}
	defer conn.Close()
	return conn.Object(serviceName, objectPath).Call(iface+".CloseNotification", 0, id).Err
}
