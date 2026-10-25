// Package passwords hashes and verifies credentials without logging secret data.
package passwords

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

const (
	memory      = 64 * 1024 // KiB per invocation; RFC 9106 second recommended profile.
	iterations  = 3
	lanes       = 4
	saltSize    = 16
	keySize     = 32
	prefix      = "$argon2id$v=19$m=65536,t=3,p=4$"
	encodedSize = len(prefix) + 22 + 1 + 43
)

var (
	ErrInvalidPassword = errors.New("invalid password")
	ErrInvalidHash     = errors.New("invalid or unsupported password hash")
)

// Validate enforces 12–128 Unicode code points without trimming or normalization.
func Validate(password string) error {
	if len(password) > 128*utf8.UTFMax || !utf8.ValidString(password) {
		return ErrInvalidPassword
	}
	n := utf8.RuneCountInString(password)
	if n < 12 || n > 128 {
		return ErrInvalidPassword
	}
	return nil
}

// Hash returns a PHC-format credential for private storage only. Callers must
// never put the result in logs, errors or public responses.
func Hash(password string) (string, error) {
	if err := Validate(password); err != nil {
		return "", err
	}
	salt := make([]byte, saltSize)
	// crypto/rand.Read fails irrecoverably if the operating system RNG fails.
	_, _ = rand.Read(salt)
	key := argon2.IDKey([]byte(password), salt, iterations, memory, lanes, keySize)
	defer clear(key)
	return prefix + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(key), nil
}

// Verify returns false, nil for a password mismatch. Malformed/unsupported
// credentials return ErrInvalidHash without using stored cost parameters.
// Callers must map authentication failures to generic public responses.
func Verify(password, encoded string) (bool, error) {
	if err := Validate(password); err != nil {
		return false, err
	}
	if len(encoded) != encodedSize || !strings.HasPrefix(encoded, prefix) {
		return false, ErrInvalidHash
	}
	parts := strings.Split(strings.TrimPrefix(encoded, prefix), "$")
	if len(parts) != 2 {
		return false, ErrInvalidHash
	}
	salt, err := decode(parts[0], saltSize)
	if err != nil {
		return false, err
	}
	expected, err := decode(parts[1], keySize)
	if err != nil {
		return false, err
	}
	actual := argon2.IDKey([]byte(password), salt, iterations, memory, lanes, keySize)
	defer clear(actual)
	defer clear(expected)
	return subtle.ConstantTimeCompare(actual, expected) == 1, nil
}

func decode(value string, size int) ([]byte, error) {
	decoded, err := base64.RawStdEncoding.Strict().DecodeString(value)
	if err != nil || len(decoded) != size || base64.RawStdEncoding.EncodeToString(decoded) != value {
		return nil, ErrInvalidHash
	}
	return decoded, nil
}
