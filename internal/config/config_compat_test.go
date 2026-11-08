package config

import (
	"os"
	"path/filepath"
	"testing"
)

// YAML::Tiny (легаси) сохраняет числа строками/булями: `use gui password: "1"`,
// `jump port: "22"`, `use proxy: "true"` — конфиг обязан загружаться (ревью major #1).
func TestLoadStringlyInts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "asbru.yml")
	content := `defaults:
  version: "6.4.2"
  use gui password: "1"
  jump port: "22"
  global variables:
    v:
      value: x
      hidden: "1"
  unknown-defaults-key: keep-me
environments:
  aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa:
    name: t
    method: SSH
    ip: 10.0.0.1
    port: "22"
    use proxy: "true"
    children: {}
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path, NewCBC())
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Defaults.UseGUIPassword != 1 || cfg.Defaults.JumpPort != 22 {
		t.Fatalf("defaults coerce: %+v", cfg.Defaults)
	}
	if cfg.Defaults.GlobalVariables["v"].Hidden != 1 {
		t.Fatalf("var hidden: %+v", cfg.Defaults.GlobalVariables["v"])
	}
	// легаси-истина — только "1": строка "true" читается как 0 (ревью minor)
	if cfg.Environments["aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"].UseProxy != 0 {
		t.Fatalf("use proxy: %+v", cfg.Environments["aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"].UseProxy)
	}
	if cfg.Defaults.UnknownDefaultKey() != "keep-me" {
		t.Fatalf("unknown key lost")
	}
}
