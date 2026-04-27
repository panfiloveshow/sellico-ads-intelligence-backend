package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

var (
	ErrInvalidKeySize     = errors.New("crypto: key must be exactly 32 bytes for AES-256")
	ErrDecryptionFailed   = errors.New("crypto: decryption failed")
	ErrInvalidCiphertext  = errors.New("crypto: invalid ciphertext")
	ErrCiphertextTooShort = errors.New("crypto: ciphertext too short")
	ErrKeyVersionUnknown  = errors.New("crypto: ciphertext encrypted under an unknown key version")
)

// Versioned ciphertext format: "v<N>:<base64-payload>".
// Unversioned ciphertext (legacy) is "<base64-payload>" with no prefix —
// Decrypt accepts both so existing data keeps working without migration.
const versionPrefix = "v"

// Encrypt encrypts plaintext using AES-256-GCM and returns a base64-encoded
// string with the nonce prepended to the ciphertext.
func Encrypt(plaintext string, key []byte) (string, error) {
	if len(key) != 32 {
		return "", ErrInvalidKeySize
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("crypto: failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("crypto: failed to create GCM: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("crypto: failed to generate nonce: %w", err)
	}

	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Decrypt decrypts a ciphertext using AES-256-GCM. Accepts both legacy
// (unversioned) and versioned ciphertext — the version prefix is stripped
// silently for single-key callers, who are responsible for supplying the
// correct key. Multi-key callers should use a Keyring with DecryptWithKeyring.
func Decrypt(ciphertext string, key []byte) (string, error) {
	if len(key) != 32 {
		return "", ErrInvalidKeySize
	}
	_, payload := stripVersion(ciphertext)
	return decryptPayload(payload, key)
}

// decryptPayload performs the GCM open on the base64-encoded "nonce||sealed"
// blob (i.e. the ciphertext minus any version prefix).
func decryptPayload(payload string, key []byte) (string, error) {
	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidCiphertext, err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("crypto: failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("crypto: failed to create GCM: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", ErrCiphertextTooShort
	}

	nonce, sealed := data[:nonceSize], data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return "", ErrDecryptionFailed
	}

	return string(plaintext), nil
}

// stripVersion returns (version, payload). version=0 means "legacy/unversioned".
// This is the boundary between the wire format and the GCM payload.
func stripVersion(ciphertext string) (int, string) {
	if !strings.HasPrefix(ciphertext, versionPrefix) {
		return 0, ciphertext
	}
	colon := strings.IndexByte(ciphertext, ':')
	if colon < 2 {
		return 0, ciphertext
	}
	v, err := strconv.Atoi(ciphertext[len(versionPrefix):colon])
	if err != nil || v <= 0 {
		return 0, ciphertext
	}
	return v, ciphertext[colon+1:]
}

// addVersion wraps a base64 payload with the "vN:" prefix.
func addVersion(version int, payload string) string {
	return fmt.Sprintf("%s%d:%s", versionPrefix, version, payload)
}

// Keyring holds a versioned set of AES-256 keys. The key with the highest
// version is the "active" key used for new encryption. Older keys remain
// available so previously-encrypted data can still be read.
//
// Lifecycle for rotation:
//  1. Operator generates a new 32-byte key.
//  2. Update env: ENCRYPTION_KEYS_V2=<new key, hex>, keep ENCRYPTION_KEYS_V1 alive.
//  3. Restart api/worker — they now read both versions, write with v2.
//  4. Run `cmd/rotate-encryption-key` to re-encrypt every existing v1
//     ciphertext under v2.
//  5. After the migration completes, drop ENCRYPTION_KEYS_V1 from env.
type Keyring struct {
	keys     map[int][]byte // version → key bytes (32B each)
	latest   int
}

// NewKeyring constructs a Keyring from version→key map. It validates that
// every key is exactly 32 bytes and that at least one key is present.
func NewKeyring(keys map[int][]byte) (*Keyring, error) {
	if len(keys) == 0 {
		return nil, errors.New("crypto: keyring must contain at least one key")
	}
	latest := 0
	for v, k := range keys {
		if v <= 0 {
			return nil, fmt.Errorf("crypto: key version must be positive, got %d", v)
		}
		if len(k) != 32 {
			return nil, ErrInvalidKeySize
		}
		if v > latest {
			latest = v
		}
	}
	return &Keyring{keys: keys, latest: latest}, nil
}

// LatestVersion returns the version used for new encryptions.
func (k *Keyring) LatestVersion() int { return k.latest }

// EncryptWithKeyring produces a versioned ciphertext using the keyring's
// latest key.
func EncryptWithKeyring(plaintext string, kr *Keyring) (string, error) {
	key, ok := kr.keys[kr.latest]
	if !ok {
		return "", ErrKeyVersionUnknown
	}
	raw, err := encryptRaw(plaintext, key)
	if err != nil {
		return "", err
	}
	return addVersion(kr.latest, raw), nil
}

// DecryptWithKeyring handles both legacy (unversioned) and versioned
// ciphertext. For legacy input it tries every key in the ring until one
// succeeds — slow but rare; almost all production data should be versioned
// after the rotation runs. Versioned input goes straight to the right key.
func DecryptWithKeyring(ciphertext string, kr *Keyring) (string, error) {
	version, payload := stripVersion(ciphertext)
	if version != 0 {
		key, ok := kr.keys[version]
		if !ok {
			return "", fmt.Errorf("%w: v%d", ErrKeyVersionUnknown, version)
		}
		return decryptPayload(payload, key)
	}
	// Legacy ciphertext: try each key in the ring (newest first).
	for v := kr.latest; v >= 1; v-- {
		key, ok := kr.keys[v]
		if !ok {
			continue
		}
		if pt, err := decryptPayload(payload, key); err == nil {
			return pt, nil
		}
	}
	return "", ErrDecryptionFailed
}

// encryptRaw performs GCM seal and returns the base64 payload (no version).
func encryptRaw(plaintext string, key []byte) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("crypto: failed to create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("crypto: failed to create GCM: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("crypto: failed to generate nonce: %w", err)
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}
