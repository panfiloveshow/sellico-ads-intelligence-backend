package crypto

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHashPassword_VerifyPassword_RoundTrip(t *testing.T) {
	password := "secureP@ssw0rd!"

	hash, err := HashPassword(password)
	require.NoError(t, err)
	assert.NotEmpty(t, hash)

	match, err := VerifyPassword(password, hash)
	require.NoError(t, err)
	assert.True(t, match)
}

func TestVerifyPassword_WrongPassword(t *testing.T) {
	hash, err := HashPassword("correct-password")
	require.NoError(t, err)

	match, err := VerifyPassword("wrong-password", hash)
	require.NoError(t, err)
	assert.False(t, match)
}

func TestHashPassword_Format(t *testing.T) {
	hash, err := HashPassword("test-password")
	require.NoError(t, err)

	// Verify format: $argon2id$v=19$m=65536,t=3,p=2$<salt>$<hash>
	assert.True(t, strings.HasPrefix(hash, "$argon2id$v=19$m=65536,t=3,p=2$"))

	parts := strings.Split(hash, "$")
	assert.Len(t, parts, 6)
	assert.Equal(t, "", parts[0])
	assert.Equal(t, "argon2id", parts[1])
	assert.Equal(t, "v=19", parts[2])
	assert.Equal(t, "m=65536,t=3,p=2", parts[3])
	assert.NotEmpty(t, parts[4]) // salt
	assert.NotEmpty(t, parts[5]) // hash
}

func TestHashPassword_UniqueSalts(t *testing.T) {
	password := "same-password"

	hash1, err := HashPassword(password)
	require.NoError(t, err)

	hash2, err := HashPassword(password)
	require.NoError(t, err)

	// Different salts should produce different hashes
	assert.NotEqual(t, hash1, hash2)

	// But both should verify correctly
	match1, err := VerifyPassword(password, hash1)
	require.NoError(t, err)
	assert.True(t, match1)

	match2, err := VerifyPassword(password, hash2)
	require.NoError(t, err)
	assert.True(t, match2)
}

func TestVerifyPassword_InvalidHashFormat(t *testing.T) {
	tests := []struct {
		name string
		hash string
	}{
		{"empty", ""},
		{"garbage", "not-a-hash"},
		{"wrong algorithm", "$bcrypt$v=19$m=65536,t=3,p=2$c2FsdA$aGFzaA"},
		{"missing parts", "$argon2id$v=19$m=65536,t=3,p=2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := VerifyPassword("password", tt.hash)
			assert.Error(t, err)
		})
	}
}

func TestVerifyPassword_EmptyPassword(t *testing.T) {
	hash, err := HashPassword("")
	require.NoError(t, err)

	match, err := VerifyPassword("", hash)
	require.NoError(t, err)
	assert.True(t, match)

	match, err = VerifyPassword("not-empty", hash)
	require.NoError(t, err)
	assert.False(t, match)
}
