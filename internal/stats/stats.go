// Package stats — статистика использования соединений (M32, PACStatistics).
// Потокобезопасный store: uuid → totalConnections/totalTime (сек).
package stats

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

// ConnStats — накопленная статистика по одному соединению.
type ConnStats struct {
	TotalConnections int   `json:"total_connections"`
	TotalTime        int64 `json:"total_time"` // секунды
}

// Store — потокобезопасная статистика; отслеживает открытые сессии для
// накопления длительности (start/stop, без легаси freeze/thaw).
type Store struct {
	mu      sync.Mutex
	stats   map[string]*ConnStats
	started map[string][]time.Time // per-uuid FIFO: M9 (перекрывающиеся сессии не затирают)
}

// NewStore — пустое хранилище.
func NewStore() *Store {
	return &Store{stats: map[string]*ConnStats{}, started: map[string][]time.Time{}}
}

// OnStart — фиксирует начало сессии uuid (для подсчёта длительности).
func (s *Store) OnStart(uuid string, t time.Time) {
	if uuid == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.stats[uuid]; !ok {
		s.stats[uuid] = &ConnStats{}
	}
	s.started[uuid] = append(s.started[uuid], t)
}

// OnStop — завершает сессию: добавляет длительность в total_time и инкрементит
// total_connections. Без OnStart — no-op (не создаём мусор).
func (s *Store) OnStop(uuid string, t time.Time) {
	if uuid == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	starts := s.started[uuid]
	if len(starts) == 0 {
		return
	}
	// FIFO: самая ранняя сессия завершается раньше (M9 — «Start N instances»).
	start := starts[0]
	s.started[uuid] = starts[1:]
	st := s.stats[uuid]
	if st == nil {
		st = &ConnStats{}
		s.stats[uuid] = st
	}
	if d := t.Sub(start); d > 0 {
		st.TotalTime += int64(d / time.Second)
	}
	st.TotalConnections++
}

// Get — накопленная статистика uuid (нули для неизвестного).
func (s *Store) Get(uuid string) ConnStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st := s.stats[uuid]; st != nil {
		return *st
	}
	return ConnStats{}
}

// Snapshot — копия всех статистик (для отображения; безопасна для читателя).
func (s *Store) Snapshot() map[string]ConnStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]ConnStats, len(s.stats))
	for k, v := range s.stats {
		out[k] = *v
	}
	return out
}

// Save — персист статистики в JSON (M10); файл 0600, как конфиг. В полёте
// (OnStart без OnStop) сохраняется только накопленный счётчик — не «закрытое»
// время, т.к. незавершённая сессия ещё не дала длительности.
func (s *Store) Save(path string) error {
	s.mu.Lock()
	stats := make(map[string]*ConnStats, len(s.stats))
	for k, v := range s.stats {
		c := *v
		stats[k] = &c
	}
	s.mu.Unlock()
	data, err := json.Marshal(stats)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// Load — чтение статистики из JSON; отсутствующий файл — no-op. Загруженные
// записи — источник истины: заменяют текущие (стартовый сценарий, пустой store).
func (s *Store) Load(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var stats map[string]*ConnStats
	if err := json.Unmarshal(data, &stats); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range stats {
		if v == nil {
			continue
		}
		s.stats[k] = v
	}
	return nil
}
