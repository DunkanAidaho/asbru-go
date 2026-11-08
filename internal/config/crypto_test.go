package config

import "testing"

// Проверка совместимости с легаси Crypt::CBC (см. план: Blowfish, EVP_BytesToKey MD5×1,
// соль 4e61bc0000000000, hex("Salted__"‖salt‖ciphertext)).
// Вектор сверен с живым Crypt::CBC 3.07 при фразе из файла lib/PACUtils.pm:146
// (в эталоне плана была опечатка фразы — 53 байта вместо 59).
const testVector = "53616c7465645f5f4e61bc00000000007bc84fc432ae3881"

// Salted__ + salt как байтовый литерал — соль НЕ пересчитывается из pack('Q').
const saltPrefixHex = "53616c7465645f5f4e61bc0000000000"

type fakeCipher struct{}

func (fakeCipher) Encrypt(p string) string { return p }
func (fakeCipher) Decrypt(c string) string { return c }

// TestCryptoVector — красный вектор: encrypt("test") обязан дать тест-вектор ДО реализации.
func TestCryptoVector(t *testing.T) {
	c := NewCBC()
	if got := c.Encrypt("test"); got != testVector {
		t.Fatalf("Encrypt(test) = %s, want %s", got, testVector)
	}
	if c.Decrypt(testVector) != "test" {
		t.Fatalf("Decrypt(vector) != test")
	}
}

func TestCBCCipherInterface(t *testing.T) {
	var _ Cipher = NewCBC()
	var _ Cipher = fakeCipher{}
}

// Deterministic и байтовый префикс Salted__+salt.
func TestCBCSaltPrefix(t *testing.T) {
	c := NewCBC()
	if got := c.Encrypt("x"); got[:len(saltPrefixHex)] != saltPrefixHex {
		t.Fatalf("ciphertext prefix %s, want %s", got[:len(saltPrefixHex)], saltPrefixHex)
	}
}

// Обе функции должны быть тотальными: ничего не паниковать на любом входе.
func TestCBCRoundTripUTF8(t *testing.T) {
	c := NewCBC()
	for _, s := range []string{"", "test", "пароль", "ключ — прецедент", "🤖 KDE"} {
		if got := c.Decrypt(c.Encrypt(s)); got != s {
			t.Fatalf("round-trip %q got %q", s, got)
		}
	}
}

// Толерантная дешифровка: мусор (не-шестнадцетиричный, кривая длина, чужой префикс)
// возвращается как есть, без ошибки.
func TestCBCDecryptTolerant(t *testing.T) {
	c := NewCBC()
	for _, s := range []string{"zzzz", "", "deadbeef", "53616c7465645f5f", testVector[:8]} {
		if got := c.Decrypt(s); got != s {
			t.Fatalf("Decrypt(%q) = %q, want input unchanged", s, got)
		}
	}
}
