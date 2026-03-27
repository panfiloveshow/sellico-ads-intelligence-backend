package jwt

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateAccessToken_RoundTrip(t *testing.T) {
	userID := uuid.New()
	secret := "test-secret-key-for-jwt"
	ttl := 15 * time.Minute

	token, err := GenerateAccessToken(userID, secret, ttl)
	require.NoError(t, err)
	assert.NotEmpty(t, token)

	// Token should have 3 parts separated by dots.
	parts := strings.Split(token, ".")
	assert.Len(t, parts, 3)

	// Validate the token.
	claims, err := ValidateToken(token, secret)
	require.NoError(t, err)
	assert.Equal(t, userID, claims.UserID)
	assert.Equal(t, "access", claims.TokenType)
	assert.Empty(t, claims.JTI)
	assert.WithinDuration(t, time.Now(), claims.IssuedAt, 2*time.Second)
	assert.WithinDuration(t, time.Now().Add(ttl), claims.ExpiresAt, 2*time.Second)
}

func TestGenerateRefreshToken_RoundTrip(t *testing.T) {
	userID := uuid.New()
	secret := "test-secret-key-for-jwt"
	ttl := 7 * 24 * time.Hour

	token, jti, err := GenerateRefreshToken(userID, secret, ttl)
	require.NoError(t, err)
	assert.NotEmpty(t, token)
	assert.NotEmpty(t, jti)

	// Validate the token.
	claims, err := ValidateToken(token, secret)
	require.NoError(t, err)
	assert.Equal(t, userID, claims.UserID)
	assert.Equal(t, "refresh", claims.TokenType)
	assert.Equal(t, jti, claims.JTI)
	assert.WithinDuration(t, time.Now(), claims.IssuedAt, 2*time.Second)
	assert.WithinDuration(t, time.Now().Add(ttl), claims.ExpiresAt, 2*time.Second)
}

func TestValidateToken_Expired(t *testing.T) {
	userID := uuid.New()
	secret := "test-secret-key-for-jwt"
	// Use a negative TTL to create an already-expired token.
	ttl := -1 * time.Minute

	token, err := GenerateAccessToken(userID, secret, ttl)
	require.NoError(t, err)

	_, err = ValidateToken(token, secret)
	assert.ErrorIs(t, err, ErrExpiredToken)
}

func TestValidateToken_InvalidSignature(t *testing.T) {
	userID := uuid.New()
	secret := "test-secret-key-for-jwt"
	wrongSecret := "wrong-secret-key"
	ttl := 15 * time.Minute

	token, err := GenerateAccessToken(userID, secret, ttl)
	require.NoError(t, err)

	// Validate with wrong secret should fail.
	_, err = ValidateToken(token, wrongSecret)
	assert.ErrorIs(t, err, ErrInvalidSignature)
}

func TestValidateToken_TamperedPayload(t *testing.T) {
	userID := uuid.New()
	secret := "test-secret-key-for-jwt"
	ttl := 15 * time.Minute

	token, err := GenerateAccessToken(userID, secret, ttl)
	require.NoError(t, err)

	// Tamper with the payload by replacing the second part.
	parts := strings.Split(token, ".")
	require.Len(t, parts, 3)

	// Modify the payload (change a character).
	tampered := parts[0] + "." + parts[1] + "x" + "." + parts[2]
	_, err = ValidateToken(tampered, secret)
	assert.ErrorIs(t, err, ErrInvalidSignature)
}

func TestValidateToken_InvalidFormat(t *testing.T) {
	secret := "test-secret-key-for-jwt"

	_, err := ValidateToken("not-a-jwt", secret)
	assert.ErrorIs(t, err, ErrInvalidToken)

	_, err = ValidateToken("only.two", secret)
	assert.ErrorIs(t, err, ErrInvalidToken)

	_, err = ValidateToken("", secret)
	assert.ErrorIs(t, err, ErrInvalidToken)
}

func TestHashToken_Consistent(t *testing.T) {
	token := "some-token-string-for-hashing"

	hash1 := HashToken(token)
	hash2 := HashToken(token)

	assert.Equal(t, hash1, hash2)
	assert.Len(t, hash1, 64) // SHA-256 hex = 64 chars
}

func TestHashToken_DifferentInputs(t *testing.T) {
	token1 := "first-token"
	token2 := "second-token"

	hash1 := HashToken(token1)
	hash2 := HashToken(token2)

	assert.NotEqual(t, hash1, hash2)
}
