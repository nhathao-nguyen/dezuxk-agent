package session_test

import (
	"bytes"
	"testing"

	"dezuxk-gateway/internal/adapters/outbound/session"
)

func TestVault_EncryptDecrypt(t *testing.T) {
	vault := session.NewVault("my-super-secret-passphrase")

	original := []byte(`{"__Secure-1PSID":"secret_cookie_val","OSID":"flow_osid"}`)
	encrypted, err := vault.Encrypt(original)
	if err != nil {
		t.Fatalf("Encrypt error: %v", err)
	}

	if encrypted == string(original) {
		t.Fatal("Encrypted string should not match original plaintext")
	}

	decrypted, err := vault.Decrypt(encrypted)
	if err != nil {
		t.Fatalf("Decrypt error: %v", err)
	}

	if !bytes.Equal(decrypted, original) {
		t.Fatalf("Decrypted mismatch. Got %s, want %s", string(decrypted), string(original))
	}
}

func TestVault_BackwardCompatibilityPlaintext(t *testing.T) {
	vault := session.NewVault("my-super-secret-passphrase")

	plaintext := []byte(`{"legacy":"cookie"}`)
	// Passing unencrypted string directly to Decrypt
	decrypted, err := vault.Decrypt(string(plaintext))
	if err != nil {
		t.Fatalf("Decrypt error on plaintext: %v", err)
	}

	if !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("Decrypted legacy mismatch. Got %s, want %s", string(decrypted), string(plaintext))
	}
}
