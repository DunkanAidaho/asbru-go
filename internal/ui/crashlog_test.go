package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Опциональная трассировка паник: logCrash пишет panic + стек в файл (очный
// дебаг «вылетает без ошибки» — стек по файлу, а не гадание).
func TestLogCrashWritesFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "crash.log")
	old := os.Getenv("ASBRU_CRASH_LOG")
	os.Setenv("ASBRU_CRASH_LOG", p)
	defer os.Setenv("ASBRU_CRASH_LOG", old)

	logCrash("launchConnection", "boom")

	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("crash-файл не создан: %v", err)
	}
	s := string(data)
	if !strings.Contains(s, "boom") || !strings.Contains(s, "launchConnection") {
		t.Fatalf("crash log = %q (нет panic-значения/места)", s)
	}
	if !strings.Contains(s, "goroutine") { // debug.Stack()
		t.Fatalf("crash log без стека адресов: %q", s)
	}
}

// crashRecover по умолчанию не ре-паникует (иначе краш убьёт приложение до
// записи стека при GUI-лаунчере); repanic=true — ре-паникуем.
func TestCrashRecoverDefaultNoRepanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("crashRecover не должен ре-паниковать по умолчанию: %v", r)
		}
	}()
	func() {
		defer crashRecover("t")
		panic("x")
	}()
}

func TestCrashRecoverRepanic(t *testing.T) {
	defer func() {
		if r := recover(); r != "y" {
			t.Fatalf("repanic: %v", r)
		}
	}()
	func() {
		defer crashRecover("t", true)
		panic("y")
	}()
}
