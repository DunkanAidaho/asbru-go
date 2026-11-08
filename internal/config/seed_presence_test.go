package config

import (
	"os"
	"path/filepath"
	"testing"
)

// M2 (ревью): явный 'auto accept key: 0' в YAML — НЕ перетирается сидом
// (yaml-присутствие), как в легаси //= (0 — определённое значение).
func TestDefaultsExplicitZeroPreserved(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "asbru.yml")
	data := []byte("defaults:\n  auto accept key: 0\n  version: \"1.0\"\nenvironments: {}\n")
	if err := os.WriteFile(p, data, 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(p, NewCBC())
	if err != nil {
		t.Fatal(err)
	}
	Sanitize(cfg)
	if cfg.Defaults.AutoAcceptKey != 0 {
		t.Fatalf("явный 0 перетёрт сидом: %d", cfg.Defaults.AutoAcceptKey)
	}
	if cfg.Defaults.TimeOutConnect != 40 {
		t.Fatalf("отсутствующий ключ не посеян: %d", cfg.Defaults.TimeOutConnect)
	}
}
