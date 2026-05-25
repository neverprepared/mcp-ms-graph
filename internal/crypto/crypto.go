// AES-256-GCM with PBKDF2-SHA256. Wire format and parameters are byte-compatible
// with chrome-extensions/microsoft/crypto.js: base64(salt[32] || nonce[12] || ciphertext+tag).
// PBKDF2: SHA-256, 100 000 iterations, 32-byte key. If you change this, change crypto.js too.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"

	"golang.org/x/crypto/pbkdf2"
)

const (
	saltLen    = 32
	nonceLen   = 12
	iterations = 100_000
	keyLen     = 32
)

func deriveKey(passphrase string, salt []byte) []byte {
	return pbkdf2.Key([]byte(passphrase), salt, iterations, keyLen, sha256.New)
}

func Encrypt(plaintext, passphrase string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return "", err
	}
	nonce := make([]byte, nonceLen)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	key := deriveKey(passphrase, salt)
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	ct := gcm.Seal(nil, nonce, []byte(plaintext), nil)
	payload := make([]byte, 0, saltLen+nonceLen+len(ct))
	payload = append(payload, salt...)
	payload = append(payload, nonce...)
	payload = append(payload, ct...)
	return base64.StdEncoding.EncodeToString(payload), nil
}

func Decrypt(encoded, passphrase string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("base64 decode: %w", err)
	}
	if len(raw) < saltLen+nonceLen+1 {
		return "", fmt.Errorf("ciphertext too short")
	}
	salt := raw[:saltLen]
	nonce := raw[saltLen : saltLen+nonceLen]
	ct := raw[saltLen+nonceLen:]
	key := deriveKey(passphrase, salt)
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	plaintext, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}
	return string(plaintext), nil
}
