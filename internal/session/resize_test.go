package session

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/DunkanAidaho/asbru-go/internal/config"
	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

// M11: ControlResize → TIOCSWINSZ на текущем PTY (ssh-процесс получает
// реальный размер — аудит: оставался 80×24 при изменении окна).
func TestSessionResizeSetsWinsize(t *testing.T) {
	master, tty, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer tty.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := &config.Environment{Name: "r", Method: "SSH"}
	s, err := New(ctx, BuildEnv{UUID: "u1"}, cfg,
		WithStreamFactory(func() (io.ReadWriteCloser, error) { return master, nil }))
	if err != nil {
		t.Fatal(err)
	}
	s.Control <- ControlMsg{Kind: ControlResize, Cols: 120, Rows: 40}

	d := time.After(2 * time.Second)
	for {
		ws, werr := unix.IoctlGetWinsize(int(tty.Fd()), unix.TIOCGWINSZ)
		if werr == nil && ws.Col == 120 && ws.Row == 40 {
			return
		}
		select {
		case <-d:
			t.Fatalf("winsize не применён: %+v (%v)", ws, werr)
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}
