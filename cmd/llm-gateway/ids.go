package main

import (
	"crypto/rand"
	"encoding/hex"
)

// requestIDRandomBytes sized generously to make collisions practically
// impossible for the lifetime of a single log stream.
const requestIDRandomBytes = 8

// newRequestID generates a "req_xxxxxxxxxxxxxxxx" identifier for
// correlating one client request across the pipeline and its log line.
func newRequestID() string {
	buf := make([]byte, requestIDRandomBytes)
	if _, err := rand.Read(buf); err != nil {
		return "req_unknown"
	}
	return "req_" + hex.EncodeToString(buf)
}
