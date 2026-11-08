package ui

// M32: панель статистики — форматирование накопленных stats.Store.

import (
	"testing"

	"github.com/DunkanAidaho/asbru-go/internal/stats"
)

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		sec  int
		want string
	}{
		{0, "0s"},
		{59, "59s"},
		{90, "1m 30s"},
		{3600, "1h"},
		{3661, "1h 1m"},
		{86400, "1d"},
		{90061, "1d 1h"},
	}
	for _, c := range cases {
		if got := formatDuration(c.sec); got != c.want {
			t.Errorf("formatDuration(%d)=%q want %q", c.sec, got, c.want)
		}
	}
}

func TestStatsLine(t *testing.T) {
	got := statsLine(stats.ConnStats{TotalConnections: 7, TotalTime: 3661})
	want := "Подключений: 7, Время: 1h 1m"
	if got != want {
		t.Fatalf("statsLine=%q want %q", got, want)
	}
}
