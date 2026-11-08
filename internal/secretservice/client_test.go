package secretservice

// Покрытие логики клиента (секрет-кодирование) без D-Bus: newSecret шифрует через
// DH-сессию. Сам D-Bus-контракт (OpenSession/SearchItems/GetSecret/CreateItem) —
// порт проверенной go-keychain-реализации; интеграция на фейковом dbus-daemon
// упирается в ограничение godbus v5 (серверная сериализация reply c *dbus.Error+
// dbus.ObjectPath), не в наш код.

import (
	"bytes"
	"math/big"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

func TestClientSecretEncodeRoundTripDHAES(t *testing.T) {
	g := rfc2409SecondOakleyGroup()
	priv, pub, err := g.NewKeypair()
	if err != nil {
		t.Fatal(err)
	}
	// общий ключ "сам с собой" — для шифрования/дешифровки в одном процессе.
	shared := new(big.Int).SetBytes(pub.Bytes())
	aesKey, err := g.dhKDF(shared, priv)
	if err != nil {
		t.Fatal(err)
	}
	c := &Client{session: &session{path: "/fake", aesKey: aesKey, mode: authDHAES}}

	password := "super-secret-pw"
	sec, err := c.newSecret([]byte(password))
	if err != nil {
		t.Fatalf("newSecret: %v", err)
	}
	if bytes.Equal(sec.Value, []byte(password)) {
		t.Fatal("DHAES-секрет не должен быть plaintext в Value")
	}
	if sec.Session != "/fake" {
		t.Fatalf("Session = %q", sec.Session)
	}
	pt, err := unauthenticatedAESCBCDecrypt(sec.Parameters, sec.Value, c.session.aesKey)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if string(pt) != password {
		t.Fatalf("round-trip = %q want %q", pt, password)
	}
}

func TestClientSecretEncodePlain(t *testing.T) {
	c := &Client{session: &session{path: "/fake", mode: authPlain}}
	sec, err := c.newSecret([]byte("pw"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(sec.Value, []byte("pw")) {
		t.Fatalf("plain Value = %x", sec.Value)
	}
	if sec.Parameters != nil || sec.ContentType != "application/octet-stream" {
		t.Fatalf("plain params/ct = %v/%q", sec.Parameters, sec.ContentType)
	}
}

func TestClientUnknownMode(t *testing.T) {
	c := &Client{session: &session{path: "/f", mode: authMode("bogus")}}
	if _, err := c.newSecret([]byte("x")); err == nil {
		t.Fatal("bogus mode — ошибка")
	}
}

// TestSessionBusIsIsolated — MAJOR-1 regression: Connections must be isolated.
// sessionBus = dbus.ConnectSessionBus (fresh per-call, ИзОЛИРОВАННОЕ, полный
// Dialog+Auth+Hello) — НЕ dbus.SessionBus (shared singleton = tray). Поэтому
// Client.Close() (и New()-error-путь) могут закрывать ИХ conn безопасно, не
// трогая общую шину tray. Проверяем: (1) sessionBus — НЕ dbus.SessionBus
// (сравнение указателей), (2) Close() стирает AES-ключ.
func TestSessionBusIsIsolated(t *testing.T) {
	if sessionBus == nil {
		t.Fatal("sessionBus не задан")
	}
	// sessionBus должен быть ConnectSessionBus (свежее изолированное), а НЕ
	// dbus.SessionBus (shared singleton) — иначе закрытие задело бы шину tray.
	if reflect.ValueOf(sessionBus).Pointer() == reflect.ValueOf(dbus.SessionBus).Pointer() {
		t.Fatal("sessionBus = dbus.SessionBus (shared singleton) — закрытие разорвало бы шину tray")
	}
	// Close() стирает AES-ключ.
	c := &Client{session: &session{path: "/f", mode: authPlain, aesKey: []byte("0123456789abcdef")}}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if c.session.aesKey != nil {
		t.Fatal("Close() не стёр AES-ключ")
	}
}

// startSecretBus — собственный dbus-daemon (сессия пользователя не трогается;
// паттерн internal/ipc, internal/tray).
func startSecretBus(t *testing.T) string {
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
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
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

// Регресс: conn.Signal(ch) регистрирует только ЛОКАЛЬНЫЙ канал. Широковещательный
// org.freedesktop.Secret.Prompt.Completed шина присылает лишь при match-правиле
// (AddMatchSignal); без него promptAndWait висел до таймаута. Проверяем доставку
// и снятие правила при отписке.
func TestPromptMatchRuleDeliversSignal(t *testing.T) {
	addr := startSecretBus(t)
	conn, err := dbus.Connect(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	ch := make(chan *dbus.Signal, 4)
	if err := subscribePromptSignals(conn, ch); err != nil {
		t.Fatalf("subscribePromptSignals: %v", err)
	}
	emit := func() {
		if err := conn.Emit("/org/freedesktop/secrets/prompt/p1",
			ServiceIface+".Prompt.Completed", false,
			dbus.MakeVariant(dbus.ObjectPath("/"))); err != nil {
			t.Fatalf("Emit: %v", err)
		}
	}
	// На соединении могут быть и служебные directed-сигналы (NameAcquired),
	// которые приходят без match-правила; ждём именно Prompt.Completed.
	waitCompleted := func(timeout time.Duration) bool {
		deadline := time.After(timeout)
		for {
			select {
			case sig := <-ch:
				if sig != nil && sig.Name == ServiceIface+".Prompt.Completed" {
					return true
				}
			case <-deadline:
				return false
			}
		}
	}
	emit()
	if !waitCompleted(2 * time.Second) {
		t.Fatal("Prompt.Completed не доставлен — нет match-правила (AddMatchSignal)")
	}

	// Снятие правила: после отписки сигнал больше не приходит.
	unsubscribePromptSignals(conn, ch)
	emit()
	if waitCompleted(300 * time.Millisecond) {
		t.Fatal("Prompt.Completed доставлен после отписки")
	}
}

// fakeCollection — минимальный сервер Collection.SearchItems для теста
// уникальности Lookup/Delete: name=="one" → один элемент, иначе — два (дубли).
type fakeCollection struct{}

func (fakeCollection) SearchItems(attrs map[string]string) ([]dbus.ObjectPath, *dbus.Error) {
	if attrs["name"] == "one" {
		return []dbus.ObjectPath{"/org/freedesktop/secrets/item/1"}, nil
	}
	return []dbus.ObjectPath{
		"/org/freedesktop/secrets/item/1",
		"/org/freedesktop/secrets/item/2",
	}, nil
}

// fakeItem — GetSecret/Delete элемента фейкового сервиса (plain-сессия).
type fakeItem struct{ pw string }

func (f fakeItem) GetSecret(session dbus.ObjectPath) (Secret, *dbus.Error) {
	return Secret{Session: session, Value: []byte(f.pw), ContentType: "application/octet-stream"}, nil
}

func (fakeItem) Delete() (dbus.ObjectPath, *dbus.Error) { return NullPrompt, nil }

// Регресс: Lookup/Delete брали items[0], хотя GetByUUID уже режет >1. При
// дубликатах это вернуло бы ЧУЖОЙ пароль (или удалило чужую запись); нужен тот
// же guard неоднозначности.
func TestLookupDeleteRequireUniqueMatch(t *testing.T) {
	addr := startSecretBus(t)
	svc, err := dbus.Connect(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	if err := svc.Export(fakeCollection{}, DefaultColPath, ServiceIface+".Collection"); err != nil {
		t.Fatal(err)
	}
	for _, it := range []struct {
		path string
		pw   string
	}{
		{"/org/freedesktop/secrets/item/1", "pw1"},
		{"/org/freedesktop/secrets/item/2", "pw2"},
	} {
		if err := svc.Export(fakeItem{pw: it.pw}, dbus.ObjectPath(it.path), ServiceIface+".Item"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.RequestName(BusName, 0); err != nil {
		t.Fatal(err)
	}

	conn, err := dbus.Connect(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	c := &Client{
		conn:     conn,
		signalCh: make(chan *dbus.Signal, 1),
		session:  &session{path: "/sessions/s1", mode: authPlain},
	}

	// Ровно один элемент — штатный путь продолжает работать.
	if pw, err := c.Lookup("one"); err != nil || pw != "pw1" {
		t.Fatalf("Lookup(one) = %q, %v; хотим pw1, nil", pw, err)
	}
	if err := c.Delete("one"); err != nil {
		t.Fatalf("Delete(one): %v", err)
	}

	// Дубликаты — ошибка неоднозначности, а не первый/чужой секрет.
	if _, err := c.Lookup("dup"); err == nil || !strings.Contains(err.Error(), "non-unique") {
		t.Fatalf("Lookup(dup) err = %v, хотим non-unique", err)
	}
	if err := c.Delete("dup"); err == nil || !strings.Contains(err.Error(), "non-unique") {
		t.Fatalf("Delete(dup) err = %v, хотим non-unique", err)
	}
}
