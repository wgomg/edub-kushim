package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

const APIKeyPrefix = "ek_"
const apiKeyBytes = 32

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

func HashAPIKey(rawKey string) string {
	sum := sha256.Sum256([]byte(rawKey))
	return hex.EncodeToString(sum[:])
}
