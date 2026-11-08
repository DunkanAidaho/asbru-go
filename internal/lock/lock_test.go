package lock

import (
	"path/filepath"
	"testing"
)

// Второй подсчёте держатель не проходит; после release — проходит.
func TestAcquireRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "asbru.lock")

	release1, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(path); err != ErrHeld {
		t.Fatalf("ожидали ErrHeld, получили %v", err)
	}
	release1()
	release2, err := Acquire(path)
	if err != nil {
		t.Fatalf("после release: %v", err)
	}
	release2()
}
