package config

import "testing"

// FuzzCipher — инварианты шифрования: тотальность, round-trip
// Encrypt→Decrypt возвращает исходник, повторная дешифровка стабильна.
func FuzzCipher(f *testing.F) {
	f.Add("")
	f.Add("test")
	f.Add("пароль 🔐")
	f.Add("53616c7465645f5f4e61bc00000000007bc84fc432ae3881") // валидный шифртекст
	f.Add("zzzz")
	f.Fuzz(func(t *testing.T, s string) {
		c := NewCBC()
		e := c.Encrypt(s)
		if c.Decrypt(e) != s {
			t.Fatalf("round-trip: %q -> %q -> %q", s, e, c.Decrypt(e))
		}
		if d := c.Decrypt(s); c.Decrypt(d) != d {
			t.Fatalf("unstable decrypt for %q", s)
		}
	})
}
