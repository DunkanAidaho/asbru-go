package scripts

// M32 backlog №4: API-контракт — реализуемость и фиксация действий.

import "testing"

// memoAPI — тестовая реализация API (запоминает вызовы; не настоящая сессия).
type memoAPI struct {
	conns []string
	stop  []string
	sent  []string
	// тестовые хуки (server_test.go):
	errOn  string       // метод, на котором возвращать ошибку ("connect" и т.д.)
	openFn func() error // кастомный OpenConn (иначе no-op)
}

func (m *memoAPI) Connect(u string) error {
	if m.errOn == "connect" {
		return errBoom
	}
	m.conns = append(m.conns, u)
	return nil
}
func (m *memoAPI) StopConn(u string) error {
	if m.errOn == "stop" {
		return errBoom
	}
	m.stop = append(m.stop, u)
	return nil
}
func (m *memoAPI) Send(u, t string) error {
	if m.errOn == "send" {
		return errBoom
	}
	m.sent = append(m.sent, u+":"+t)
	return nil
}
func (m *memoAPI) OpenConn() error {
	if m.openFn != nil {
		return m.openFn()
	}
	if m.errOn == "open" {
		return errBoom
	}
	return nil
}

var errBoom = &apiTestErr{"boom"}

type apiTestErr struct{ msg string }

func (e *apiTestErr) Error() string { return e.msg }

func TestAPIContract(t *testing.T) {
	// Контракт компилируется и реализуем; действия фиксируются.
	var a API = &memoAPI{}
	if err := a.Connect("u1"); err != nil {
		t.Fatal(err)
	}
	if err := a.Send("u1", "uptime\n"); err != nil {
		t.Fatal(err)
	}
	if err := a.StopConn("u1"); err != nil {
		t.Fatal(err)
	}
	m := a.(*memoAPI)
	if len(m.conns) != 1 || m.conns[0] != "u1" {
		t.Fatalf("conns=%v", m.conns)
	}
	if len(m.sent) != 1 || m.sent[0] != "u1:uptime\n" {
		t.Fatalf("sent=%v", m.sent)
	}
	if len(m.stop) != 1 || m.stop[0] != "u1" {
		t.Fatalf("stop=%v", m.stop)
	}
}
