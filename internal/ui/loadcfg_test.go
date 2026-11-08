package ui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/DunkanAidaho/asbru-go/internal/config"
)

// Битый/нечитаемый asbru.yml не должен превращаться в ПУСТОЙ сохраняемый
// конфиг: GUI продолжил бы работу, а следующее сохранение (edit/add/prefs/
// import) атомарно переименовало бы свежий конфиг поверх оригинала —
// все соединения пользователя уничтожались бы. loadCfg обязан отказать.
func TestLoadCfgParseErrorDoesNotReturnSaveableConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "asbru.yml")
	if err := os.WriteFile(path, []byte("environments: [oops\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadCfg(path)
	if err == nil {
		t.Fatal("ошибка разбора проглочена")
	}
	if cfg != nil {
		t.Fatalf("ошибка разбора вернула сохраняемый конфиг (%d соединений) — следующее сохранение затрёт asbru.yml", len(cfg.Environments))
	}
	if errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ошибка разбора ошибочно опознана как отсутствие файла: %v", err)
	}
}

// Отсутствие asbru.yml — первый запуск: пустой конфиг и никакой ошибки.
func TestLoadCfgMissingFileYieldsEmptyConfig(t *testing.T) {
	cfg, err := loadCfg(filepath.Join(t.TempDir(), "asbru.yml"))
	if err != nil {
		t.Fatalf("отсутствие файла дало ошибку: %v", err)
	}
	if cfg == nil || len(cfg.Environments) != 0 {
		t.Fatalf("cfg = %#v, want пустой конфиг", cfg)
	}
}

// loadDecision — чистое решение: ErrNotExist → пустой конфиг; любая иная
// ошибка → nil-конфиг и проброшенная ошибка (сохранять нечего и нельзя).
func TestLoadDecision(t *testing.T) {
	t.Run("not-exist", func(t *testing.T) {
		cfg, err := loadDecision(nil, fmt.Errorf("read config: %w", os.ErrNotExist))
		if err != nil || cfg == nil || len(cfg.Environments) != 0 {
			t.Fatalf("cfg=%#v err=%v, want пустой конфиг без ошибки", cfg, err)
		}
	})
	t.Run("real-error", func(t *testing.T) {
		boom := errors.New("boom")
		cfg, err := loadDecision(&config.Config{Environments: map[string]config.Environment{}}, boom)
		if !errors.Is(err, boom) {
			t.Fatalf("err = %v, хотим проброшенную boom", err)
		}
		if cfg != nil {
			t.Fatalf("cfg = %#v, ошибка не должна давать сохраняемый конфиг", cfg)
		}
	})
}
