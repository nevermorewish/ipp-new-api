package common

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
)

const (
	credentialEncryptionPrefix = "enc:v1:"
	credentialEncryptionAAD    = "huanxing:weixin-monitor:v1"
)

// EncryptCredential encrypts a credential with AES-256-GCM using CryptoSecret.
func EncryptCredential(plaintext string) (string, error) {
	if plaintext == "" {
		return "", fmt.Errorf("credential must not be empty")
	}
	gcm, err := newCredentialGCM()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("failed to generate credential nonce: %w", err)
	}
	sealed := gcm.Seal(nil, nonce, []byte(plaintext), []byte(credentialEncryptionAAD))
	payload := append(nonce, sealed...)
	return credentialEncryptionPrefix + base64.RawStdEncoding.EncodeToString(payload), nil
}

// DecryptCredential decrypts an encrypted credential. Values without the
// versioned prefix are treated as legacy plaintext for backwards compatibility.
func DecryptCredential(stored string) (string, error) {
	if !strings.HasPrefix(stored, credentialEncryptionPrefix) {
		return stored, nil
	}
	payload, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(stored, credentialEncryptionPrefix))
	if err != nil {
		return "", fmt.Errorf("invalid encrypted credential encoding: %w", err)
	}
	gcm, err := newCredentialGCM()
	if err != nil {
		return "", err
	}
	if len(payload) < gcm.NonceSize()+gcm.Overhead() {
		return "", fmt.Errorf("invalid encrypted credential payload")
	}
	nonce, ciphertext := payload[:gcm.NonceSize()], payload[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, []byte(credentialEncryptionAAD))
	if err != nil {
		return "", fmt.Errorf("failed to decrypt credential: %w", err)
	}
	return string(plaintext), nil
}

func newCredentialGCM() (cipher.AEAD, error) {
	key := sha256.Sum256([]byte(CryptoSecret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("failed to initialize credential cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize credential GCM: %w", err)
	}
	return gcm, nil
}
