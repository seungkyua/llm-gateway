package admin

import (
	"crypto/rand"
	"encoding/hex"
)

// keyIDRandomBytes/plainKeyRandomBytes are sized generously to make
// collisions practically impossible without needing a database-level
// uniqueness check.
const (
	keyIDRandomBytes    = 6
	plainKeyRandomBytes = 32
)

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// newKeyID generates a "key_xxxxxxxxxxxx" identifier.
func newKeyID() (string, error) {
	suffix, err := randomHex(keyIDRandomBytes)
	if err != nil {
		return "", err
	}
	return "key_" + suffix, nil
}

// newPlainAPIKey generates a "sk-..." plaintext API key.
func newPlainAPIKey() (string, error) {
	suffix, err := randomHex(plainKeyRandomBytes)
	if err != nil {
		return "", err
	}
	return "sk-" + suffix, nil
}
