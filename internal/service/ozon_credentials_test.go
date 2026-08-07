package service

import (
	"testing"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
)

func TestOzonCredentialsEncryptDecryptRoundTrip(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	original := domain.OzonCredentials{
		ClientID:         "123456",
		APIKey:           "seller-api-key",
		PerfClientID:     "789-perf@advertising.performance.ozon.ru",
		PerfClientSecret: "perf-secret",
	}

	encrypted, err := encryptOzonCredentials(original, key)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if encrypted == "" {
		t.Fatal("encrypt returned empty blob")
	}

	decrypted, err := decryptOzonCredentialsBlob(encrypted, key)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if decrypted != original {
		t.Fatalf("round trip mismatch: got %+v, want %+v", decrypted, original)
	}
}

func TestDecryptOzonCredentialsBlob_EmptyErrors(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	if _, err := decryptOzonCredentialsBlob("", key); err == nil {
		t.Fatal("expected error for empty blob")
	}
}
