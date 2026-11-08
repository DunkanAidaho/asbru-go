package scripts

// M32: PACScripts — поиск и запуск пользовательских скриптов (subprocess).

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLookup(t *testing.T) {
	dir := t.TempDir()
	// создаём скрипт
	if err := os.WriteFile(filepath.Join(dir, "backup.sh"), []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if p, ok := Lookup(dir, "backup.sh"); !ok || p != filepath.Join(dir, "backup.sh") {
		t.Fatalf("Lookup=%q,%v", p, ok)
	}
	// нет файла
	if _, ok := Lookup(dir, "missing.sh"); ok {
		t.Fatal("missing найден")
	}
	// path-traversal отклоняется
	for _, bad := range []string{"../secret", "a/b.sh", ""} {
		if _, ok := Lookup(dir, bad); ok {
			t.Fatalf("path-traversal допущен: %q", bad)
		}
	}
	// каталог, не файл
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ok := Lookup(dir, "sub"); ok {
		t.Fatal("каталог принят за скрипт")
	}
}

func TestRun(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := Run(ctx, "/bin/echo", []string{"hello"})
	if err != nil {
		t.Fatal(err)
	}
	if out != "hello\n" {
		t.Fatalf("out=%q want 'hello\\n'", out)
	}
}

// Редактор скриптов: ValidName не пускает path-traversal/имена с сепараторами.
func TestValidName(t *testing.T) {
	good := []string{"script.pl", "my backup.sh", "a-b_c.txt"}
	for _, n := range good {
		if !ValidName(n) {
			t.Errorf("ValidName(%q) = false, want true", n)
		}
	}
	bad := []string{"", "..", "../x", "a/b", "a\\b", ".hidden", "a/../b", ".", "..\\x"}
	for _, n := range bad {
		if ValidName(n) {
			t.Errorf("ValidName(%q) = true, want false", n)
		}
	}
}

// Path — путь с проверкой имени; каталог создаётся, имя невалидно → ok=false.
func TestPath(t *testing.T) {
	dir := t.TempDir()
	if p, ok := Path(dir, "script.pl"); !ok || p != filepath.Join(dir, "script.pl") {
		t.Fatalf("Path=%q,%v", p, ok)
	}
	if _, ok := Path(dir, "../evil"); ok {
		t.Fatal("Path допускает traversal")
	}
	if _, ok := Path(dir, "a/b"); ok {
		t.Fatal("Path допускает сепаратор")
	}
	// каталог создан
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		t.Fatalf("директория: %v", err)
	}
}

// BACKLOG:35 — ScriptsDir был не покрыт вовсе (0%), а используется в ui
// (каталог скриптов рядом с конфигом).
func TestScriptsDir(t *testing.T) {
	if got := ScriptsDir("/home/u/.config/asbru"); got != "/home/u/.config/asbru/scripts" {
		t.Fatalf("ScriptsDir = %q", got)
	}
	if got := ScriptsDir(""); got != "scripts" {
		t.Fatalf("пустой каталог: %q", got)
	}
}
