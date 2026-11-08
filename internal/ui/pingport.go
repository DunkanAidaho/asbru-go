package ui

import (
	"net"
	"time"
)

// reachPort — доступен ли host:port (TCP) в пределах таймаута (бэклог M29
// «ping port before connect»). Пустой host/port или сетевая ошибка → false.
func reachPort(host, port string, timeout time.Duration) bool {
	if host == "" || port == "" {
		return false
	}
	if timeout <= 0 { // net.DialTimeout: zero/negative = БЕЗ таймаута (опасно)
		return false
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), timeout)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// pingPort — инжектируемая проверка порта (для юнит-тестов launchConnection).
var pingPort = reachPort
