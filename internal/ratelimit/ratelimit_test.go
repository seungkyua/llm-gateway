package ratelimit

import (
	"testing"

	"llm-gateway/internal/storage"
)

func TestAllowShouldReturnTrueWhenWithinLimits(t *testing.T) {
	l := New()
	limits := storage.Limits{RPM: 60, TPM: 100000}

	allowed, _, _ := l.Allow("key_1", limits)

	if !allowed {
		t.Error("expected request within limits to be allowed")
	}
}

func TestAllowShouldReturnFalseWithRetryAfterWhenRPMExceeded(t *testing.T) {
	l := New()
	limits := storage.Limits{RPM: 1, TPM: 100000}

	first, _, _ := l.Allow("key_1", limits)
	second, reason, retryAfter := l.Allow("key_1", limits)

	if !first {
		t.Fatal("expected first request to be allowed")
	}
	if second {
		t.Fatal("expected second request to be blocked by RPM limit")
	}
	if reason != "rpm" {
		t.Errorf("expected reason rpm, got %q", reason)
	}
	if retryAfter <= 0 {
		t.Errorf("expected positive retry-after, got %v", retryAfter)
	}
}

func TestAllowShouldReturnFalseWhenTPMExceeded(t *testing.T) {
	l := New()
	limits := storage.Limits{RPM: 100, TPM: 1000}
	l.RecordTokens("key_1", 1000)

	allowed, reason, retryAfter := l.Allow("key_1", limits)

	if allowed {
		t.Fatal("expected request to be blocked by TPM limit")
	}
	if reason != "tpm" {
		t.Errorf("expected reason tpm, got %q", reason)
	}
	if retryAfter <= 0 {
		t.Errorf("expected positive retry-after, got %v", retryAfter)
	}
}

func TestAllowShouldBlockImmediatelyWhenLimitIsZero(t *testing.T) {
	l := New()
	limits := storage.Limits{RPM: 0, TPM: 100000}

	allowed, reason, _ := l.Allow("key_1", limits)

	if allowed {
		t.Fatal("expected request to be blocked when rpm limit is 0")
	}
	if reason != "rpm" {
		t.Errorf("expected reason rpm, got %q", reason)
	}
}

func TestAllowShouldCountDifferentKeysIndependently(t *testing.T) {
	l := New()
	limits := storage.Limits{RPM: 1, TPM: 100000}

	l.Allow("key_1", limits) // consumes key_1's single allowed request
	allowedForKey2, _, _ := l.Allow("key_2", limits)

	if !allowedForKey2 {
		t.Error("expected key_2 to be unaffected by key_1's rate limit usage")
	}
}
