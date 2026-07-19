package connectors

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	// CredentialBlobPrefix versions the ciphertext envelope.
	CredentialBlobPrefix = "v1:"
)

// EncryptionKey holds a versioned 32-byte AES-256 key.
type EncryptionKey struct {
	Version int
	Key     []byte
}

// ParseEncryptionKey decodes DONNA_CONNECTOR_ENCRYPTION_KEY (base64 of 32 bytes).
func ParseEncryptionKey(raw string) (EncryptionKey, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return EncryptionKey{}, errors.New("DONNA_CONNECTOR_ENCRYPTION_KEY is required to enable connectors")
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		// Also accept URL-safe base64 without padding.
		key, err = base64.RawURLEncoding.DecodeString(raw)
		if err != nil {
			return EncryptionKey{}, fmt.Errorf("invalid DONNA_CONNECTOR_ENCRYPTION_KEY encoding: %w", err)
		}
	}
	if len(key) != 32 {
		return EncryptionKey{}, fmt.Errorf("DONNA_CONNECTOR_ENCRYPTION_KEY must decode to 32 bytes, got %d", len(key))
	}
	return EncryptionKey{Version: 1, Key: key}, nil
}

// Encrypt encrypts plaintext with AES-256-GCM and a random nonce.
// Output: "v1:" + base64(nonce|ciphertext|tag)
func Encrypt(key EncryptionKey, plaintext []byte) (string, error) {
	if len(key.Key) != 32 {
		return "", errors.New("encryption key must be 32 bytes")
	}
	block, err := aes.NewCipher(key.Key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, plaintext, nil)
	return CredentialBlobPrefix + base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt reverses Encrypt.
func Decrypt(key EncryptionKey, blob string) ([]byte, error) {
	blob = strings.TrimSpace(blob)
	if !strings.HasPrefix(blob, CredentialBlobPrefix) {
		return nil, errors.New("unsupported credential blob version")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(blob, CredentialBlobPrefix))
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key.Key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(raw) < gcm.NonceSize() {
		return nil, errors.New("ciphertext too short")
	}
	nonce, ciphertext := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	return gcm.Open(nil, nonce, ciphertext, nil)
}

func EncryptJSON(key EncryptionKey, v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return Encrypt(key, b)
}

func DecryptJSON[T any](key EncryptionKey, blob string) (T, error) {
	var zero T
	raw, err := Decrypt(key, blob)
	if err != nil {
		return zero, err
	}
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		return zero, err
	}
	return out, nil
}

// HashState returns a hex SHA-256 of the raw OAuth state (never store raw state).
func HashState(rawState string) string {
	sum := sha256.Sum256([]byte(rawState))
	return hex.EncodeToString(sum[:])
}

// RandomURLSafe returns n random bytes encoded as raw URL-safe base64.
func RandomURLSafe(n int) (string, error) {
	b := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
