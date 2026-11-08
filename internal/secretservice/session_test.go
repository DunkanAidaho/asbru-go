package secretservice

// Крипто-сессия dh-ietf1024-sha256-aes128-cbc-pkcs7: AES-128-CBC + PKCS7.
// Юнит без D-Bus: round-trip, паддинг, границы.

import (
	"bytes"
	"crypto/aes"
	"math/big"
	"testing"
)

func TestPadPKCS7(t *testing.T) {
	for _, tc := range []struct {
		in   []byte
		want int
	}{
		{[]byte(""), 16},                    // пустой → полный блок
		{bytes.Repeat([]byte{'a'}, 15), 16}, // 15 → 1 pad
		{bytes.Repeat([]byte{'a'}, 16), 32}, // полный блок → +полный блок
		{bytes.Repeat([]byte{'a'}, 1), 16},  // 1 → 15 pad
	} {
		padded := padPKCS7(tc.in, aes.BlockSize)
		if len(padded) != tc.want {
			t.Fatalf("pad len=%d want %d", len(padded), tc.want)
		}
		un, err := unpadPKCS7(padded, aes.BlockSize)
		if err != nil {
			t.Fatalf("unpad: %v", err)
		}
		if !bytes.Equal(un, tc.in) {
			t.Fatalf("round-trip: got %q want %q", un, tc.in)
		}
	}
}

func TestUnpadPKCS7Errors(t *testing.T) {
	// пустой
	if _, err := unpadPKCS7(nil, aes.BlockSize); err == nil {
		t.Fatal("empty должен дать ошибку")
	}
	// не кратно блоку
	if _, err := unpadPKCS7([]byte{1, 2, 3}, aes.BlockSize); err == nil {
		t.Fatal("некратно — ошибка")
	}
	// pad больше длины
	if _, err := unpadPKCS7([]byte{1, 2, 3, 4, 0x10}, aes.BlockSize); err == nil {
		t.Fatal("pad>len — ошибка")
	}
	// несоответствующие pad-байты
	if _, err := unpadPKCS7([]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 2}, aes.BlockSize); err == nil {
		t.Fatal("несоотв. pad — ошибка")
	}
}

func TestAESCBCRoundTrip(t *testing.T) {
	key := []byte("0123456789abcdef") // 16 байт
	plaintext := []byte("secret-password-here")
	iv, ct, err := unauthenticatedAESCBCEncrypt(plaintext, key)
	if err != nil {
		t.Fatal(err)
	}
	if len(iv) != aes.BlockSize {
		t.Fatalf("iv len=%d", len(iv))
	}
	out, err := unauthenticatedAESCBCDecrypt(iv, ct, key)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, plaintext) {
		t.Fatalf("round-trip: got %q want %q", out, plaintext)
	}
}

func TestAESCBCDekryptWrongKey(t *testing.T) {
	key := []byte("0123456789abcdef")
	bad := []byte("fedcba9876543210")
	iv, ct, err := unauthenticatedAESCBCEncrypt([]byte("msg"), key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := unauthenticatedAESCBCDecrypt(iv, ct, bad); err == nil {
		t.Fatal("неверный ключ должен дать ошибку паддинга")
	}
}

func TestDHDeriveKeyDeterministicPublic(t *testing.T) {
	// известные приватные ключи → устойчивый (детерминированный) AES-ключ для
	// заданной пары (проверяет согласованность DH+KDF, не завязываясь на rand).
	g := rfc2409SecondOakleyGroup()
	privA := big.NewInt(12345)
	// публичный по известному приватному
	pubA := new(big.Int).Exp(g.g, privA, g.p)
	privB := big.NewInt(67890)
	pubB := new(big.Int).Exp(g.g, privB, g.p)
	kA, err := g.dhKDF(pubB, privA)
	if err != nil {
		t.Fatal(err)
	}
	kB, err := g.dhKDF(pubA, privB)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(kA, kB) {
		t.Fatalf("ключи A/B отличаются: %x vs %x", kA, kB)
	}
	if len(kA) != 16 {
		t.Fatalf("ключ len=%d want 16", len(kA))
	}
}
