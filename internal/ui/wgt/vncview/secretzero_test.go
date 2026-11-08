package vncview

import "testing"

// zeroBytes стирает секрет перед C.free: C.CString-копия пароля не должна
// оставаться в куче после освобождения.
func TestZeroBytes(t *testing.T) {
	b := []byte("super-secret-pw")
	zeroBytes(b)
	for i, v := range b {
		if v != 0 {
			t.Fatalf("байт %d не затёрт: %#x", i, v)
		}
	}
}
