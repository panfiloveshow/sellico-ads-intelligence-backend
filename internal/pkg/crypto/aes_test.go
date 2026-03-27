package crypto

import (
	"crypto/rand"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, 32)
	_, err := rand.Read(key)
	require.NoError(t, err)
	return key
}

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	key := validKey(t)
	plaintext := "wb-api-token-abc123"

	encrypted, err := Encrypt(plaintext, key)
	require.NoError(t, err)
	assert.NotEmpty(t, encrypted)
	assert.NotEqual(t, plaintext, encrypted)

	decrypted, err := Decrypt(encrypted, key)
	require.NoError(t, err)
	assert.Equal(t, plaintext, decrypted)
}

func TestEncryptDecrypt_EmptyPlaintext(t *testing.T) {
	key := validKey(t)

	encrypted, err := Encrypt("", key)
	require.NoError(t, err)
	assert.NotEmpty(t, encrypted)

	decrypted, err := Decrypt(encrypted, key)
	require.NoError(t, err)
	assert.Equal(t, "", decrypted)
}

func TestEncrypt_InvalidKeySize(t *testing.T) {
	tests := []struct {
		name string
		key  []byte
	}{
		{"too short", make([]byte, 16)},
		{"too long", make([]byte, 64)},
		{"empty", []byte{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Encrypt("test", tt.key)
			assert.ErrorIs(t, err, ErrInvalidKeySize)
		})
	}
}

func TestDecrypt_InvalidKeySize(t *testing.T) {
	// First encrypt with a valid key to get valid ciphertext
	key := validKey(t)
	encrypted, err := Encrypt("test", key)
	require.NoError(t, err)

	_, err = Decrypt(encrypted, make([]byte, 16))
	assert.ErrorIs(t, err, ErrInvalidKeySize)
}

func TestDecrypt_WrongKey(t *testing.T) {
	key1 := validKey(t)
	key2 := validKey(t)

	encrypted, err := Encrypt("secret-token", key1)
	require.NoError(t, err)

	_, err = Decrypt(encrypted, key2)
	assert.ErrorIs(t, err, ErrDecryptionFailed)
}

func TestDecrypt_InvalidBase64(t *testing.T) {
	key := validKey(t)
	_, err := Decrypt("not-valid-base64!!!", key)
	assert.ErrorIs(t, err, ErrInvalidCiphertext)
}

func TestDecrypt_TamperedCiphertext(t *testing.T) {
	key := validKey(t)
	encrypted, err := Encrypt("test", key)
	require.NoError(t, err)

	// Tamper with the ciphertext
	tampered := encrypted[:len(encrypted)-2] + "AA"
	_, err = Decrypt(tampered, key)
	assert.Error(t, err)
}

func TestEncrypt_ProducesDifferentCiphertexts(t *testing.T) {
	key := validKey(t)
	plaintext := "same-input"

	enc1, err := Encrypt(plaintext, key)
	require.NoError(t, err)

	enc2, err := Encrypt(plaintext, key)
	require.NoError(t, err)

	// Different nonces should produce different ciphertexts
	assert.NotEqual(t, enc1, enc2)
}
