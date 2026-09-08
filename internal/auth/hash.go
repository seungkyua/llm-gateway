// Package auth verifies client API keys (Authorization: Bearer <api_key>)
// against storage.Store by SHA-256 hash.
package auth

import (
	"crypto/sha256"
	"encoding/hex"
)

// HashAPIKey returns the SHA-256 hex digest of a plaintext API key. The
// plaintext is never persisted; only this hash is stored/compared.
func HashAPIKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}
