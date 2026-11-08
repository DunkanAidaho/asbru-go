// Package secretservice — клиент freedesktop Secret Service (org.freedesktop.secrets),
// унифицированный для KWallet5/6, встроенного секрет-сервиса KeePassXC и
// gnome-keyring (аудит #7; отдельный KWallet-бэкенд НЕ делаем — все они говорят
// по одному D-Bus-API). Пароль соединения (env.Pass) — опциональный внешний
// источник: Lookup/Store/Delete по label-метке.
//
// Крипто-сессия dh-ietf1024-sha256-aes128-cbc-pkcs7 (RFC 2409 Second Oakley
// Group, MODP-2) портирована из подтверждённой реализации go-keychain
// (secretservice/dh_ietf1024_sha256_aes128_cbc_pkcs7.go). ВНИМАНИЕ: протокол
// НЕ аутентифицирован, НЕ защищён от malleation и НЕ CCA2-безопасен — только
// скрывает D-Bus-сообщения от системных логирующих сервисов.
package secretservice

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"math/big"

	"golang.org/x/crypto/hkdf"
)

// dhGroup — Diffie-Hellman-группа (MODP-2 для крипто-сессии Secret Service).
type dhGroup struct {
	g, p, pMinus1 *big.Int
}

var bigOne = big.NewInt(1)

// NewKeypair — генерация приватного/публичного ключей группы.
func (group *dhGroup) NewKeypair() (private *big.Int, public *big.Int, err error) {
	for {
		if private, err = cryptorand.Int(cryptorand.Reader, group.pMinus1); err != nil {
			return nil, nil, err
		}
		if private.Sign() > 0 {
			break
		}
	}
	public = new(big.Int).Exp(group.g, private, group.p)
	return private, public, nil
}

// diffieHellman — общий секрет по публичному ключу партнёра и своему приватному.
func (group *dhGroup) diffieHellman(theirPublic, myPrivate *big.Int) (*big.Int, error) {
	if theirPublic.Cmp(bigOne) <= 0 || theirPublic.Cmp(group.pMinus1) >= 0 {
		return nil, errors.New("secretservice: DH parameter out of bounds")
	}
	return new(big.Int).Exp(theirPublic, myPrivate, group.p), nil
}

// rfc2409SecondOakleyGroup — группа MODP-2 (1024-bit prime, RFC 2409).
func rfc2409SecondOakleyGroup() *dhGroup {
	p, _ := new(big.Int).SetString("FFFFFFFFFFFFFFFFC90FDAA22168C234C4C6628B80DC1CD129024E088A67CC74020BBEA63B139B22514A08798E3404DDEF9519B3CD3A431B302B0A6DF25F14374FE1356D6D51C245E485B576625E7EC6F44C42E9A637ED6B0BFF5CB6F406B7EDEE386BFB5A899FA5AE9F24117C4B1FE649286651ECE65381FFFFFFFFFFFFFFFF", 16)
	return &dhGroup{
		g:       new(big.Int).SetInt64(2),
		p:       p,
		pMinus1: new(big.Int).Sub(p, bigOne),
	}
}

// dhKDF — HKDF-SHA256 от общего секрета → 16-байт AES-128 ключ (как в go-keychain).
func (group *dhGroup) dhKDF(theirPublic *big.Int, myPrivate *big.Int) ([]byte, error) {
	sharedSecret, err := group.diffieHellman(theirPublic, myPrivate)
	if err != nil {
		return nil, err
	}
	sharedSecretBytes := sharedSecret.Bytes()
	r := hkdf.New(sha256.New, sharedSecretBytes, nil, nil)
	aesKey := make([]byte, 16)
	if _, err := io.ReadFull(r, aesKey); err != nil {
		return nil, err
	}
	return aesKey, nil
}

// unauthenticatedAESCBCEncrypt — AES-128-CBC с PKCS7 (параметр IV возвращается
// отдельно — это «Session» параметр Secret Service).
func unauthenticatedAESCBCEncrypt(plaintext []byte, key []byte) (iv, ciphertext []byte, err error) {
	padded := padPKCS7(plaintext, aes.BlockSize)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, err
	}
	iv = make([]byte, aes.BlockSize)
	if _, err := io.ReadFull(cryptorand.Reader, iv); err != nil {
		return nil, nil, err
	}
	ciphertext = make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ciphertext, padded)
	return iv, ciphertext, nil
}

// unauthenticatedAESCBCDecrypt — обратная AES-128-CBC с PKCS7. Шифротекст
// расшифровывается in-place (как в референсе).
func unauthenticatedAESCBCDecrypt(iv, ciphertext, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if len(iv) != aes.BlockSize {
		return nil, fmt.Errorf("secretservice: iv length not aes blocksize")
	}
	if len(ciphertext) < aes.BlockSize {
		return nil, fmt.Errorf("secretservice: ciphertext smaller than AES block size")
	}
	if len(ciphertext)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("secretservice: aes ciphertext not a multiple of blocksize")
	}
	mode := cipher.NewCBCDecrypter(block, iv)
	mode.CryptBlocks(ciphertext, ciphertext)
	plaintext, err := unpadPKCS7(ciphertext, aes.BlockSize)
	if err != nil {
		return nil, err
	}
	return plaintext, nil
}

// padPKCS7 — PKCS7-паддинг до блока n (полный блок → доп. полный блок).
func padPKCS7(xs []byte, n int) []byte {
	m := byte(n - (len(xs) % n))
	if m == 0 {
		m = byte(n)
	}
	return append(xs, bytes.Repeat([]byte{m}, int(m))...)
}

// unpadPKCS7 — снятие PKCS7-паддинга (проверка валидности).
func unpadPKCS7(xs []byte, n int) ([]byte, error) {
	if len(xs) == 0 {
		return nil, fmt.Errorf("secretservice: cannot unpad empty bytearray")
	}
	if len(xs)%n != 0 {
		return nil, fmt.Errorf("secretservice: length of bytearray not a multiple of blocksize")
	}
	last := xs[len(xs)-1]
	if last == 0 || int(last) > n { // ревью MINOR-4: строгий PKCS7 (last==0/over — невалид)
		return nil, fmt.Errorf("secretservice: invalid pkcs7 padding byte %d", last)
	}
	padStart := len(xs) - int(last)
	if padStart < 0 {
		return nil, fmt.Errorf("secretservice: invalid padding")
	}
	for i := padStart; i < len(xs); i++ {
		if xs[i] != last {
			return nil, fmt.Errorf("secretservice: expected pad %x, got %x", last, xs[i])
		}
	}
	return xs[:padStart], nil
}
