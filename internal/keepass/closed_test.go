package keepass

import (
	"testing"
)

// Случаи после Close: Field возвращает ошибку, а не мусор (ревью M4).
func TestVaultClosedGuard(t *testing.T) {
	path := makeKDBX(t, "master", "")
	v, err := Open(path, "master", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Field("password", "/Group/Entry"); err != ErrClosed {
		t.Fatalf("после Close Field = %v, хотим ErrClosed", err)
	}
}
