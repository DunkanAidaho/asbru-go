// Пакет ipc: межпроцессное взаимодействие между инстансами Ásbrú через
// D-Bus (аудит #8, нативно к KDE). Регистрирует имя org.asbru_cm и экспортирует
// объект с методом Forward; второй инстанс вызывает Forward и выходит.
// Меняет прежний Unix-domain сокет (M26) — сокет УДАЛЁН (переход только на D-Bus).
//
// Протокол — та же строка "command|text" (легаси _sendAppMessage,
// PACMain.pm:264-298, 1130-1154), но по D-Bus (метод Forward(string)).
package ipc

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

// sendTimeout — максимальное ожидание ответа Forward (защита от «повисшего»
// сервера). Локальный D-Bus отвечает мгновенно; таймаут — только страховка.
const sendTimeout = 3 * time.Second

// Сервис/объект/интерфейс IPC.
const (
	busName    = "org.asbru_cm"
	objectPath = dbus.ObjectPath("/org/asbru_cm")
	iface      = "org.asbru_cm.Ipc"
)

// ErrNotRunning — нет живого инстанса для IPC (имя не занято) — клиент.
var ErrNotRunning = errors.New("asbru: нет запущенного инстанса (IPC)")

// ErrInUse — имя уже занято другим живым инстансом (сервер).
var ErrInUse = errors.New("asbru: IPC-имя уже занято")

// Name — D-Bus-имя инстанса, завязанное на каталог конфигурации: несколько
// каталогов → разные имена (иначе второй GUI не примет форвард, ревью MINOR-2).
// Каталог нормализуется (filepath.Clean), чтобы cli (cfgDir()) и ui
// (filepath.Dir(cfgPath)) дали ОДНО имя для неканонического ASBRU_CFG
// (хвостовой слэш/`..`) — иначе форвард не дойдёт. Пустой → базовое org.asbru_cm.
func Name(cfgDir string) string {
	if cfgDir == "" {
		return busName
	}
	cfgDir = filepath.Clean(cfgDir)
	h := sha256.Sum256([]byte(cfgDir))
	return fmt.Sprintf("%s_%x", busName, h[:6])
}

// sessionBus — подключение к сессионной шине (инжектируемо в тестах).
// По умолчанию СВЕЖЕЕ подключение (ConnectSessionBus), НЕ кэш-синглтон
// dbus.SessionBus: otherwise ipc.Send would close the shared conn on which
// the tray/IPC exports live (B2 — killed them for good). Listen keeps its own
// conn (never closed until closeFn); Send closes only its fresh conn.
var sessionBus = func() (*dbus.Conn, error) { return dbus.ConnectSessionBus() }

// ReplaceSessionBus — временная замена sessionBus (для юнит-тестов с
// изолированным dbus-daemon). Возвращает restore-функцию.
func ReplaceSessionBus(f func() (*dbus.Conn, error)) func() {
	old := sessionBus
	sessionBus = f
	return func() { sessionBus = old }
}

// ipcService — экспортируемый объект: Forward(msg) принимает сообщение
// от второго инстанса и отдаёт его в handle.
type ipcService struct {
	handle func(string)
}

// Forward — D-Bus-метод: принять IPC-сообщение и диспетчеризовать.
func (s *ipcService) Forward(msg string) *dbus.Error {
	if s.handle != nil {
		s.handle(msg)
	}
	return nil
}

// Listen регистрирует имя (Name(cfgDir)) и экспортирует объект Forward.
// handle — вызывается на Qt-нити (через mainthreadW; см. ui) для каждого
// сообщения. Возвращает Close-функцию (освободить имя + закрыть шину).
// Ошибка ErrInUse, если имя уже занято живым инстансом.
func Listen(name string, handle func(string)) (conn *dbus.Conn, closeFn func(), err error) {
	c, err := sessionBus()
	if err != nil {
		return nil, nil, err
	}
	svc := &ipcService{handle: handle}
	if err := c.Export(svc, objectPath, iface); err != nil {
		c.Close()
		return nil, nil, err
	}
	reply, err := c.RequestName(name, dbus.NameFlagDoNotQueue)
	if err != nil {
		c.Close()
		return nil, nil, err
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		c.Close()
		return nil, nil, ErrInUse
	}
	closeFn = func() {
		_, _ = c.ReleaseName(name)
		_ = c.Close()
	}
	return c, closeFn, nil
}

// Send подключается к живому инстансу (по имени Name(cfgDir)) и вызывает
// Forward(msg). Имя не занято → ErrNotRunning (смысл: инстанс не запущен).
func Send(name, msg string) error {
	c, err := sessionBus()
	if err != nil {
		return err
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
	defer cancel()
	err = c.Object(name, objectPath).CallWithContext(ctx, iface+".Forward", 0, msg).Err
	if isNoService(err) {
		return ErrNotRunning
	}
	return err
}

// isNoService — D-Bus-ошибка «имя не занято» (нет живого инстанса).
// godbus возвращает org.freedesktop.DBus.Error.ServiceUnknown / имя не найдено.
func isNoService(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "ServiceUnknown") ||
		strings.Contains(s, "not provided by any") ||
		strings.Contains(s, "not found")
}
