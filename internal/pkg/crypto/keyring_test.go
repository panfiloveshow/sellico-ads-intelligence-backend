package crypto

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewKeyring_RejectsBadInput(t *testing.T) {
	_, err := NewKeyring(map[int][]byte{})
	require.Error(t, err)

	_, err = NewKeyring(map[int][]byte{1: make([]byte, 16)})
	require.ErrorIs(t, err, ErrInvalidKeySize)

	_, err = NewKeyring(map[int][]byte{0: validKey(t)})
	require.Error(t, err) // version must be positive
}

func TestEncryptWithKeyring_AddsVersionPrefix(t *testing.T) {
	kr, err := NewKeyring(map[int][]byte{1: validKey(t), 2: validKey(t)})
	require.NoError(t, err)
	assert.Equal(t, 2, kr.LatestVersion())

	ct, err := EncryptWithKeyring("token-xyz", kr)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(ct, "v2:"), "expected v2: prefix, got %q", ct[:8])
}

func TestDecryptWithKeyring_RoundTripVersioned(t *testing.T) {
	kr, err := NewKeyring(map[int][]byte{1: validKey(t), 2: validKey(t)})
	require.NoError(t, err)
	ct, err := EncryptWithKeyring("hello", kr)
	require.NoError(t, err)
	pt, err := DecryptWithKeyring(ct, kr)
	require.NoError(t, err)
	assert.Equal(t, "hello", pt)
}

func TestDecryptWithKeyring_LegacyUnversionedStillWorks(t *testing.T) {
	k1 := validKey(t)
	k2 := validKey(t)
	kr, err := NewKeyring(map[int][]byte{1: k1, 2: k2})
	require.NoError(t, err)

	// Simulate data encrypted before versioning was introduced (no prefix).
	legacy, err := Encrypt("legacy-token", k1)
	require.NoError(t, err)
	assert.False(t, strings.HasPrefix(legacy, "v"), "legacy ciphertext shouldn't carry version prefix")

	pt, err := DecryptWithKeyring(legacy, kr)
	require.NoError(t, err)
	assert.Equal(t, "legacy-token", pt)
}

func TestDecryptWithKeyring_FailsForUnknownVersion(t *testing.T) {
	kr, err := NewKeyring(map[int][]byte{1: validKey(t)})
	require.NoError(t, err)
	_, err = DecryptWithKeyring("v9:somebase64==", kr)
	require.ErrorIs(t, err, ErrKeyVersionUnknown)
}

func TestDecrypt_AcceptsVersionedCiphertext(t *testing.T) {
	// Single-key Decrypt should be able to read versioned ciphertext as
	// long as the supplied key matches the version that produced it.
	key := validKey(t)
	kr, err := NewKeyring(map[int][]byte{3: key})
	require.NoError(t, err)
	ct, err := EncryptWithKeyring("payload", kr)
	require.NoError(t, err)

	pt, err := Decrypt(ct, key)
	require.NoError(t, err)
	assert.Equal(t, "payload", pt)
}

func TestStripVersion_Robust(t *testing.T) {
	cases := []struct {
		in        string
		wantVer   int
		wantPay   string
	}{
		{"v1:abc", 1, "abc"},
		{"v42:longerpayload", 42, "longerpayload"},
		{"v0:bad", 0, "v0:bad"},   // zero version → treated as legacy
		{"vXX:bad", 0, "vXX:bad"}, // non-numeric → legacy
		{"plain-no-prefix", 0, "plain-no-prefix"},
		{":noversion", 0, ":noversion"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			v, p := stripVersion(tc.in)
			assert.Equal(t, tc.wantVer, v)
			assert.Equal(t, tc.wantPay, p)
		})
	}
}
