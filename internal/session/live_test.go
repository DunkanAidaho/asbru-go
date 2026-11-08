//go:build integration

package session

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/DunkanAidaho/asbru-go/internal/config"
)

// Живой PTY-e2e (без фейков потока): реальный creack/pty + локальная
// команда через Generic Command. Проверяет spawn, поток вывода и корректное
// закрытие — то, что headless-тесты не охватывают.
func TestLivePTYGeneric(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := &config.Environment{Name: "live", Method: "Generic Command", IP: `printf 'live-e2e-ok\n'; sleep 0.05`}
	s, err := New(ctx, BuildEnv{UUID: "live"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	deadline := time.After(5 * time.Second)
	closed := false
	for !closed {
		select {
		case e := <-s.Out:
			switch e.Kind {
			case EventOutput:
				out.WriteString(e.Text)
			case EventClosed:
				closed = true
			case EventFailed:
				t.Fatalf("Failed: %s", e.Text)
			}
		case <-deadline:
			t.Fatal("нет Closed за 5с")
		}
	}
	if !strings.Contains(out.String(), "live-e2e-ok") {
		t.Fatalf("вывод не содержит маркера: %q", out.String())
	}
}
