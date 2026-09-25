package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// APIKeyPrefix is the raw key's leading marker.
const APIKeyPrefix = "ek_"
const apiKeyBytes = 32

// GenerateAPIKey mints a random key, returning the raw value (shown
// once), the SHA-256 hash to store, and the display prefix for the UI.
func GenerateAPIKey() (rawKey string, hash string, displayPrefix string, err error) {
	buf := make([]byte, apiKeyBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", "", "", fmt.Errorf("generate api key: %w", err)
	}
	rawKey = APIKeyPrefix + hex.EncodeToString(buf)
	hash = HashAPIKey(rawKey)
	displayPrefix = rawKey[:len(APIKeyPrefix)+9]
	return rawKey, hash, displayPrefix, nil
}

// HashAPIKey returns the hex SHA-256 of the raw key, the only form kept
// server-side.
func HashAPIKey(rawKey string) string {
	sum := sha256.Sum256([]byte(rawKey))
	return hex.EncodeToString(sum[:])
}
