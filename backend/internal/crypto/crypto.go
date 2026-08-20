// Package crypto provides AES-256-GCM helpers for encrypting sensitive
// columns at rest (Cloudflare tunnel secrets, domain certs).
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"io"
)

type Box struct {
	gcm cipher.AEAD
}

func New(key []byte) (*Box, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm: %w", err)
	}
	return &Box{gcm: gcm}, nil
}

// Encrypt returns nonce||ciphertext.
func (b *Box) Encrypt(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, b.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("nonce: %w", err)
	}
	return b.gcm.Seal(nonce, nonce, plaintext, nil), nil
}

func (b *Box) Decrypt(data []byte) ([]byte, error) {
	n := b.gcm.NonceSize()
	if len(data) < n {
		return nil, fmt.Errorf("ciphertext too short")
	}
	nonce, ct := data[:n], data[n:]
	return b.gcm.Open(nil, nonce, ct, nil)
}
