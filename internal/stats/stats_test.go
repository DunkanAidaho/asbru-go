package stats

// M32: PACStatistics (per-connection usage stats). Современная реализация:
// потокобезопасный store (uuid → totalConnections/totalTime), без freeze/thaw.

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStoreOnStartStop(t *testing.T) {
	s := NewStore()
	s.OnStart("u1", time.Unix(100, 0))
	s.OnStop("u1", time.Unix(160, 0)) // 60с
	s.OnStart("u1", time.Unix(200, 0))
	s.OnStop("u1", time.Unix(230, 0)) // 30с

	got := s.Get("u1")
	if got.TotalConnections != 2 {
		t.Fatalf("TotalConnections=%d want 2", got.TotalConnections)
	}
	if got.TotalTime != 90 {
		t.Fatalf("TotalTime=%d want 90", got.TotalTime)
	}

	// другой uuid — отдельно
	s.OnStart("u2", time.Unix(0, 0))
	s.OnStop("u2", time.Unix(10, 0))
	if g := s.Get("u2"); g.TotalConnections != 1 || g.TotalTime != 10 {
		t.Fatalf("u2=%+v", g)
	}
	// неизвестный uuid → нули
	if g := s.Get("ghost"); g.TotalConnections != 0 || g.TotalTime != 0 {
		t.Fatalf("ghost=%+v", g)
	}
	// OnStop без OnStart — не падаем, не мутируем
	s.OnStop("ghost2", time.Unix(999, 0))
	if g := s.Get("ghost2"); g.TotalConnections != 0 || g.TotalTime != 0 {
		t.Fatalf("ghost2=%+v", g)
	}
}

// M9: несколько сессий одного UUID («Start N instances») не должны затирать
// друг друга — каждая OnStart даёт свою точку старта, OnStop кладёт своё время.
func TestOnStartStopOverlapping(t *testing.T) {
	s := NewStore()
	t1 := time.Unix(100, 0) // A старт
	t2 := time.Unix(200, 0) // B старт (перекрытие)
	t3 := time.Unix(250, 0) // A стоп → +150с
	t4 := time.Unix(400, 0) // B стоп → +200с
	s.OnStart("u1", t1)
	s.OnStart("u1", t2)
	s.OnStop("u1", t3)
	s.OnStop("u1", t4)
	got := s.Get("u1")
	if got.TotalTime != 350 {
		t.Fatalf("TotalTime = %d, want 350 (150+200)", got.TotalTime)
	}
	if got.TotalConnections != 2 {
		t.Fatalf("TotalConnections = %d, want 2", got.TotalConnections)
	}
}

// M10: статистика переживает перезапуск — Save/Load в JSON (легаси: nfreeze).
// В полёте (OnStart без OnStop) не сохраняется — только завершённые сессии.
func TestStorePersistenceRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stats.json")
	s := NewStore()
	s.OnStart("u1", time.Unix(0, 0))
	s.OnStop("u1", time.Unix(10, 0)) // 10с, 1 соединение
	s.OnStart("u1", time.Unix(100, 0))
	s.OnStop("u1", time.Unix(130, 0)) // 30с, 2-е соединение
	s.OnStart("u2", time.Unix(0, 0))
	// u2 в полёте (без OnStop) — в сохранение не попадёт как время
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}

	s2 := NewStore()
	if err := s2.Load(path); err != nil {
		t.Fatal(err)
	}
	g := s2.Get("u1")
	if g.TotalConnections != 2 || g.TotalTime != 40 {
		t.Fatalf("u1 после Load: %+v (want 2/40)", g)
	}
	if g2 := s2.Get("u2"); g2.TotalConnections != 0 || g2.TotalTime != 0 {
		t.Fatalf("u2 (в полёте) после Load: %+v", g2)
	}
}

// M10: отсутствующий файл — no-op (чистый старт).
func TestStoreLoadMissingFile(t *testing.T) {
	s := NewStore()
	if err := s.Load(filepath.Join(t.TempDir(), "nope.json")); err != nil {
		t.Fatalf("нет файла: ожидали no-op, got %v", err)
	}
}
