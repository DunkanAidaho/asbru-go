package session

// M32: PACStatistics wiring — OnStart при подключении, OnStop при завершении.

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/DunkanAidaho/asbru-go/internal/config"
	"github.com/DunkanAidaho/asbru-go/internal/stats"
)

func TestSessionStatsWiring(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st := stats.NewStore()
	stream := &pipeStream{r: make(chan []byte, 8), w: make(chan []byte, 8)}
	cfg := &config.Environment{Name: "fake", User: "root", Pass: "secret", Method: "SSH"}
	s, err := New(ctx, BuildEnv{UUID: "u1"}, cfg,
		WithStreamFactory(func() (io.ReadWriteCloser, error) { return stream, nil }),
		WithStats(st))
	if err != nil {
		t.Fatal(err)
	}

	stream.serverSend("login as: ")
	stream.serverSend("Password: ")
	stream.serverSend("root@host:~# ")

	deadline := time.After(2 * time.Second)
connected:
	for {
		select {
		case e := <-s.Out:
			if e.Kind == EventStatusConnected {
				break connected
			}
		case <-deadline:
			t.Fatal("нет CONNECTED")
		}
	}

	cancel()
	time.Sleep(60 * time.Millisecond) // дать runOnce завершиться (OnStop)

	got := st.Get("u1")
	if got.TotalConnections != 1 {
		t.Fatalf("TotalConnections=%d want 1", got.TotalConnections)
	}
}
