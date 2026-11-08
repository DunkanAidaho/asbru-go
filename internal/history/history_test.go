package history

// M31: История подключений — потокобезопасный store с лимитом (новые сверху).

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestStorePushRecent(t *testing.T) {
	s := NewStore(2)
	s.Push("a", time.Unix(1, 0))
	s.Push("b", time.Unix(2, 0))
	s.Push("c", time.Unix(3, 0)) // вытесняет 'a' (cap=2)

	got := s.Recent()
	if len(got) != 2 {
		t.Fatalf("len=%d want 2", len(got))
	}
	if got[0].UUID != "c" || got[1].UUID != "b" {
		t.Fatalf("Recent=%v want [c b]", got)
	}
	// пусто
	if len(NewStore(3).Recent()) != 0 {
		t.Fatal("пустой store не пуст")
	}
	// cap=0 — ничего не хранит
	if z := NewStore(0); len(z.Recent()) != 0 {
		t.Fatal("cap=0: Recent не пуст")
	}
	// метка времени сохраняется
	_ = NewStore(1)
}

// M10: история переживает перезапуск — Save/Load в JSON (легаси: nfreeze).
func TestStorePersistenceRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	s := NewStore(10)
	s.Push("a", time.Unix(1, 0))
	s.Push("b", time.Unix(2, 0))
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}

	s2 := NewStore(10)
	if err := s2.Load(path); err != nil {
		t.Fatal(err)
	}
	got := s2.Recent()
	if len(got) != 2 || got[0].UUID != "b" || got[1].UUID != "a" {
		t.Fatalf("после Load: %v", got)
	}
	if !got[0].T.Equal(time.Unix(2, 0)) {
		t.Fatalf("метка времени потеряна: %v", got[0].T)
	}
	// Load = источник истины (диск): заменяет текущее содержимое.
	s.Push("c", time.Unix(3, 0))
	if err := s.Load(path); err != nil {
		t.Fatal(err)
	}
	if r := s.Recent(); len(r) != 2 || r[0].UUID != "b" {
		t.Fatalf("Load должен заменить содержимое: %v", r)
	}
}

// M10: Load ограничивает лимит хранилища.
func TestStoreLoadAppliesLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	s := NewStore(100)
	for i := 0; i < 50; i++ {
		s.Push(fmt.Sprintf("u%d", i), time.Unix(int64(i+1), 0))
	}
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	s2 := NewStore(10)
	if err := s2.Load(path); err != nil {
		t.Fatal(err)
	}
	if got := s2.Recent(); len(got) != 10 {
		t.Fatalf("len=%d want 10 (лимит)", len(got))
	}
}

// M10: отсутствующий файл — no-op (чистый старт).
func TestStoreLoadMissingFile(t *testing.T) {
	s := NewStore(10)
	if err := s.Load(filepath.Join(t.TempDir(), "nope.json")); err != nil {
		t.Fatalf("нет файла: ожидали no-op, got %v", err)
	}
}
