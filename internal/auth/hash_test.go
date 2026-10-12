package auth

import "testing"

func TestHashAPIKeyShouldReturnSameHashForSameInput(t *testing.T) {
	h1 := HashAPIKey("sk-abc123")
	h2 := HashAPIKey("sk-abc123")

	if h1 != h2 {
		t.Errorf("expected identical hashes, got %q and %q", h1, h2)
	}
}

func TestHashAPIKeyShouldReturnDifferentHashForDifferentInput(t *testing.T) {
	h1 := HashAPIKey("sk-abc123")
	h2 := HashAPIKey("sk-xyz789")

	if h1 == h2 {
		t.Error("expected different hashes for different inputs")
	}
}
