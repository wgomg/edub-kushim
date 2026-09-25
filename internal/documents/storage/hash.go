package storage

import (
	"crypto/sha256"
	"encoding/hex"
)

// SHA256Hex returns the hex SHA-256 of a string, the content hash stored
// alongside document text for dedup and integrity checks.
func SHA256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
