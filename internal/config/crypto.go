// Пакет config: YAML-конфиг Ásbrú и совместимое с легаси шифрование.
package config

import (
	"bytes"
	"crypto/cipher"
	"crypto/md5"
	"encoding/hex"
	"errors"

	"golang.org/x/crypto/blowfish"
)

// Совместимость с Crypt::CBC Perl-проекта. Это детерминированная обфускация
// с публичным ключом: точность параметров обязательна, криптостойкость нет.

const passphrase = "PAC Manager (David Torrejon Vaquerizas, david.tv@gmail.com)"

// Магический заголовок OpenSSL-совместимого формата + жёстко зашитая соль
// (LE-uint64 от 12345678; в Perl pack('Q', '12345678') зависит от порядка байт,
// поэтому здесь соль захардкожена в байтах, а не пересчитывается).
var (
	saltedPrefix = []byte("Salted__")
	legacySalt   = []byte{0x4e, 0x61, 0xbc, 0x00, 0x00, 0x00, 0x00, 0x00}
)

// Cipher — обфускация значений в конфиге. Реализация одна (Blowfish-совместимость);
// интерфейс оставлен, чтобы позже добавить другой формат записи без смены вызовов.
type Cipher interface {
	Encrypt(plaintext string) string
	// Decrypt тотален: при любой ошибке возвращает вход как есть.
	Decrypt(ciphertext string) string
}

// NewCBC возвращает Blowfish-CBC, совместимый с Crypt::CBC Perl-проекта.
func NewCBC() Cipher { return cbc{} }

type cbc struct{}

// deriveKeyIV — OpenSSL EVP_BytesToKey: D_i = MD5(D_{i-1}‖pass‖salt), набрать
// 64 байта: key = первые 56 (max_keysize Blowfish), IV = следующие 8.
func deriveKeyIV(pass, salt []byte) (key, iv []byte) {
	out := make([]byte, 0, 64)
	var prev []byte
	for len(out) < 64 {
		h := md5.New()
		h.Write(prev)
		h.Write(pass)
		h.Write(salt)
		prev = h.Sum(nil)
		out = append(out, prev...)
	}
	return out[:56], out[56:64]
}

func (cbc) Encrypt(plaintext string) string {
	key, iv := deriveKeyIV([]byte(passphrase), legacySalt)
	blk, err := blowfish.NewCipher(key)
	if err != nil {
		return plaintext
	}
	blockSize := blk.BlockSize()
	data := pkcs7Pad([]byte(plaintext), blockSize)
	out := make([]byte, len(data))
	cipher.NewCBCEncrypter(blk, iv).CryptBlocks(out, data)

	raw := append(append([]byte{}, saltedPrefix...), legacySalt...)
	raw = append(raw, out...)
	return hex.EncodeToString(raw)
}

func (cbc) Decrypt(ciphertext string) string {
	raw, err := hex.DecodeString(ciphertext)
	if err != nil || len(raw) < 16 || !bytes.Equal(raw[:8], saltedPrefix) {
		return ciphertext
	}
	key, iv := deriveKeyIV([]byte(passphrase), raw[8:16])
	blk, err := blowfish.NewCipher(key)
	if err != nil {
		return ciphertext
	}
	// Канонических шифртекстов с неполным блоком не бывает; без этой проверки
	// CBC-декриптер паникует на мусоре (регрессия закрыта фаззером FuzzCipher).
	if (len(raw)-16)%blk.BlockSize() != 0 {
		return ciphertext
	}
	out := make([]byte, len(raw)-16)
	cipher.NewCBCDecrypter(blk, iv).CryptBlocks(out, raw[16:])
	plain, err := pkcs7Unpad(out, blk.BlockSize())
	if err != nil {
		return ciphertext
	}
	return string(plain)
}

func pkcs7Pad(data []byte, size int) []byte {
	n := size - len(data)%size
	return append(data, bytes.Repeat([]byte{byte(n)}, n)...)
}

func pkcs7Unpad(data []byte, size int) ([]byte, error) {
	if len(data) == 0 || len(data)%size != 0 {
		return nil, errors.New("bad data length")
	}
	n := int(data[len(data)-1])
	if n == 0 || n > size || n > len(data) {
		return nil, errors.New("bad padding")
	}
	return data[:len(data)-n], nil
}
