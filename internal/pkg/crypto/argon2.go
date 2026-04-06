package crypto

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	argon2SaltLength = 16
	argon2KeyLength  = 32
)

const (
	defaultArgon2Memory      = 64 * 1024
	defaultArgon2Iterations  = 3
	defaultArgon2Parallelism = 2

	testArgon2Memory      = 8 * 1024
	testArgon2Iterations  = 1
	testArgon2Parallelism = 1
)

var (
	ErrInvalidHashFormat = errors.New("crypto: invalid argon2id hash format")
	ErrIncompatibleHash  = errors.New("crypto: incompatible argon2id hash version")
)

// HashPassword hashes a password using argon2id and returns an encoded hash string
// in the format: $argon2id$v=19$m=65536,t=3,p=2$<base64-salt>$<base64-hash>
func HashPassword(password string) (string, error) {
	params := currentArgon2Params()

	salt := make([]byte, argon2SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("crypto: failed to generate salt: %w", err)
	}

	hash := argon2.IDKey([]byte(password), salt, params.iterations, params.memory, params.parallelism, argon2KeyLength)

	encodedSalt := base64.RawStdEncoding.EncodeToString(salt)
	encodedHash := base64.RawStdEncoding.EncodeToString(hash)

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, params.memory, params.iterations, params.parallelism,
		encodedSalt, encodedHash), nil
}

// VerifyPassword verifies a password against a stored argon2id hash.
func VerifyPassword(password, encodedHash string) (bool, error) {
	salt, hash, params, err := decodeHash(encodedHash)
	if err != nil {
		return false, err
	}

	computedHash := argon2.IDKey([]byte(password), salt, params.iterations, params.memory, params.parallelism, uint32(len(hash)))

	return subtle.ConstantTimeCompare(hash, computedHash) == 1, nil
}

type argon2Params struct {
	memory      uint32
	iterations  uint32
	parallelism uint8
}

func currentArgon2Params() *argon2Params {
	if os.Getenv("FAST_ARGON2") == "1" {
		return &argon2Params{
			memory:      testArgon2Memory,
			iterations:  testArgon2Iterations,
			parallelism: testArgon2Parallelism,
		}
	}
	return &argon2Params{
		memory:      defaultArgon2Memory,
		iterations:  defaultArgon2Iterations,
		parallelism: defaultArgon2Parallelism,
	}
}

func decodeHash(encodedHash string) (salt, hash []byte, params *argon2Params, err error) {
	parts := strings.Split(encodedHash, "$")
	if len(parts) != 6 {
		return nil, nil, nil, ErrInvalidHashFormat
	}

	if parts[1] != "argon2id" {
		return nil, nil, nil, ErrInvalidHashFormat
	}

	var version int
	_, err = fmt.Sscanf(parts[2], "v=%d", &version)
	if err != nil {
		return nil, nil, nil, ErrInvalidHashFormat
	}
	if version != argon2.Version {
		return nil, nil, nil, ErrIncompatibleHash
	}

	params = &argon2Params{}
	_, err = fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &params.memory, &params.iterations, &params.parallelism)
	if err != nil {
		return nil, nil, nil, ErrInvalidHashFormat
	}

	salt, err = base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return nil, nil, nil, fmt.Errorf("%w: invalid salt encoding", ErrInvalidHashFormat)
	}

	hash, err = base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return nil, nil, nil, fmt.Errorf("%w: invalid hash encoding", ErrInvalidHashFormat)
	}

	return salt, hash, params, nil
}
