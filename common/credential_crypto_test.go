package common

import (
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestCredentialEncryptionRoundTrip(t *testing.T) {
	originalSecret := CryptoSecret
	t.Cleanup(func() { CryptoSecret = originalSecret })
	CryptoSecret = "credential-test-secret"

	first, err := EncryptCredential("sensitive-token")
	require.NoError(t, err)
	second, err := EncryptCredential("sensitive-token")
	require.NoError(t, err)
	if !strings.HasPrefix(first, "enc:v1:") {
		t.Fatalf("encrypted value has unexpected prefix: %q", first)
	}
	if strings.Contains(first, "sensitive-token") {
		t.Fatal("encrypted value contains plaintext")
	}
	if first == second {
		t.Fatal("two encryptions unexpectedly produced the same ciphertext")
	}
	plaintext, err := DecryptCredential(first)
	require.NoError(t, err)
	if plaintext != "sensitive-token" {
		t.Fatalf("plaintext = %q", plaintext)
	}
}

func TestDecryptCredentialRejectsWrongKey(t *testing.T) {
	originalSecret := CryptoSecret
	t.Cleanup(func() { CryptoSecret = originalSecret })
	CryptoSecret = "first-secret"
	stored, err := EncryptCredential("token")
	require.NoError(t, err)
	CryptoSecret = "different-secret"
	require.Error(t, func() error { _, err := DecryptCredential(stored); return err }())
}

func TestDecryptCredentialAcceptsLegacyPlaintext(t *testing.T) {
	plaintext, err := DecryptCredential("legacy-token")
	require.NoError(t, err)
	if plaintext != "legacy-token" {
		t.Fatalf("plaintext = %q", plaintext)
	}
}
