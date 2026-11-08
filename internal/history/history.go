// Package history — история последних подключений (панель History, M31).
// Потокобезопасный, с лимитом: новые записи сверху, старые вытесняются.
package history

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

// Entry — одна запись истории подключения.
type Entry struct {
	UUID string
	T    time.Time
}

// Store — потокобезопасная история подключений с лимитом.
type Store struct {
	mu    sync.Mutex
	limit int
	items []Entry
}

// NewStore — пустое хранилище с лимитом записей (0 — ничего не хранить).
func NewStore(limit int) *Store {
	return &Store{limit: limit}
}

// Push — добавляет подключение (метка времени now) и вытесняет старые сверх лимита.
func (s *Store) Push(uuid string, at time.Time) {
	if s.limit <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = append([]Entry{{UUID: uuid, T: at}}, s.items...)
	if len(s.items) > s.limit {
		s.items = s.items[:s.limit]
	}
}

// Recent — новейшие подключения (сверху вниз). Копия — безопасна для читателя.
func (s *Store) Recent() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Entry, len(s.items))
	copy(out, s.items)
	return out
}

// Save — персист истории в JSON (M10); файл 0600, как конфиг.
func (s *Store) Save(path string) error {
	s.mu.Lock()
	items := make([]Entry, len(s.items))
	copy(items, s.items)
	s.mu.Unlock()
	data, err := json.Marshal(items)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// Load — чтение истории из JSON; отсутствующий файл — no-op (чистый старт).
// Загруженные записи — источник истины: заменяют текущее содержимое (новые
// сверху), затем применяется лимит хранилища.
func (s *Store) Load(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var items []Entry
	if err := json.Unmarshal(data, &items); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = items
	if s.limit > 0 && len(s.items) > s.limit {
		s.items = s.items[:s.limit]
	}
	return nil
}
