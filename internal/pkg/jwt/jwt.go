package jwt

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrInvalidToken     = errors.New("invalid token")
	ErrExpiredToken     = errors.New("token has expired")
	ErrInvalidSignature = errors.New("invalid token signature")
)

// TokenClaims holds the parsed claims from a JWT token.
type TokenClaims struct {
	UserID    uuid.UUID
	ExpiresAt time.Time
	IssuedAt  time.Time
	TokenType string // "access" or "refresh"
	JTI       string // only for refresh tokens
}

// header is the fixed JWT header for HS256.
type header struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
}

// payload represents the JWT payload claims.
type payload struct {
	Sub  string `json:"sub"`
	Exp  int64  `json:"exp"`
	Iat  int64  `json:"iat"`
	Type string `json:"type"`
	JTI  string `json:"jti,omitempty"`
}

// base64URLEncode encodes data using base64url encoding without padding per RFC 7515.
func base64URLEncode(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}

// base64URLDecode decodes a base64url-encoded string without padding.
func base64URLDecode(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}

// sign creates an HMAC-SHA256 signature for the given message using the secret.
func sign(message, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(message))
	return base64URLEncode(mac.Sum(nil))
}

// generateToken creates a JWT token string from the given payload and secret.
func generateToken(p payload, secret string) (string, error) {
	h := header{Alg: "HS256", Typ: "JWT"}

	headerJSON, err := json.Marshal(h)
	if err != nil {
		return "", fmt.Errorf("marshal header: %w", err)
	}

	payloadJSON, err := json.Marshal(p)
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}

	headerEncoded := base64URLEncode(headerJSON)
	payloadEncoded := base64URLEncode(payloadJSON)

	signingInput := headerEncoded + "." + payloadEncoded
	signature := sign(signingInput, secret)

	return signingInput + "." + signature, nil
}

// GenerateAccessToken creates a new JWT access token for the given user.
func GenerateAccessToken(userID uuid.UUID, secret string, ttl time.Duration) (string, error) {
	now := time.Now()
	p := payload{
		Sub:  userID.String(),
		Exp:  now.Add(ttl).Unix(),
		Iat:  now.Unix(),
		Type: "access",
	}
	return generateToken(p, secret)
}

// GenerateRefreshToken creates a new JWT refresh token for the given user.
// Returns the token string and the JTI (for storing hash in DB).
func GenerateRefreshToken(userID uuid.UUID, secret string, ttl time.Duration) (token string, jti string, err error) {
	jti, err = generateJTI()
	if err != nil {
		return "", "", fmt.Errorf("generate jti: %w", err)
	}

	now := time.Now()
	p := payload{
		Sub:  userID.String(),
		Exp:  now.Add(ttl).Unix(),
		Iat:  now.Unix(),
		Type: "refresh",
		JTI:  jti,
	}

	token, err = generateToken(p, secret)
	if err != nil {
		return "", "", err
	}
	return token, jti, nil
}

// ValidateToken parses and validates a JWT token, returning the claims.
func ValidateToken(tokenString string, secret string) (*TokenClaims, error) {
	parts := strings.Split(tokenString, ".")
	if len(parts) != 3 {
		return nil, ErrInvalidToken
	}

	headerEncoded := parts[0]
	payloadEncoded := parts[1]
	signatureEncoded := parts[2]

	// Verify signature.
	signingInput := headerEncoded + "." + payloadEncoded
	expectedSignature := sign(signingInput, secret)
	if !hmac.Equal([]byte(signatureEncoded), []byte(expectedSignature)) {
		return nil, ErrInvalidSignature
	}

	// Decode and parse payload.
	payloadBytes, err := base64URLDecode(payloadEncoded)
	if err != nil {
		return nil, fmt.Errorf("%w: decode payload: %v", ErrInvalidToken, err)
	}

	var p payload
	if err := json.Unmarshal(payloadBytes, &p); err != nil {
		return nil, fmt.Errorf("%w: unmarshal payload: %v", ErrInvalidToken, err)
	}

	// Check expiration.
	if time.Now().Unix() > p.Exp {
		return nil, ErrExpiredToken
	}

	// Parse user ID.
	userID, err := uuid.Parse(p.Sub)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid user id: %v", ErrInvalidToken, err)
	}

	return &TokenClaims{
		UserID:    userID,
		ExpiresAt: time.Unix(p.Exp, 0),
		IssuedAt:  time.Unix(p.Iat, 0),
		TokenType: p.Type,
		JTI:       p.JTI,
	}, nil
}

// HashToken returns the SHA-256 hash of a token string (for storing refresh tokens in DB).
func HashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// generateJTI creates a random UUID-like identifier for refresh token JTI.
func generateJTI() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
