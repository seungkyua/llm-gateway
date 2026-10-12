// Package ratelimit enforces per-key RPM/TPM limits using fixed
// (per-minute) in-memory windows (see spec.md Assumptions: 슬라이딩
// 윈도우/토큰버킷 대신 고정 윈도우 방식 채택).
package ratelimit

import (
	"math"
	"sync"
	"time"

	"llm-gateway/internal/storage"
)

const window = time.Minute

// Limiter tracks per-key RPM/TPM counters in memory (never persisted;
// see spec.md 인메모리 vs 파일 상태).
type Limiter struct {
	mu    sync.Mutex
	state map[string]*keyState
}

type keyState struct {
	rpmWindowStart time.Time
	rpmCount       int
	tpmWindowStart time.Time
	tpmTokens      int64
}

// New builds an empty Limiter.
func New() *Limiter {
	return &Limiter{state: map[string]*keyState{}}
}

func (l *Limiter) getOrCreate(keyID string, now time.Time) *keyState {
	st, ok := l.state[keyID]
	if !ok {
		st = &keyState{rpmWindowStart: now, tpmWindowStart: now}
		l.state[keyID] = st
	}
	if now.Sub(st.rpmWindowStart) >= window {
		st.rpmWindowStart = now
		st.rpmCount = 0
	}
	if now.Sub(st.tpmWindowStart) >= window {
		st.tpmWindowStart = now
		st.tpmTokens = 0
	}
	return st
}

func retryAfterFor(windowStart, now time.Time) time.Duration {
	remaining := window - now.Sub(windowStart)
	if remaining <= 0 {
		return time.Second
	}
	seconds := math.Ceil(remaining.Seconds())
	return time.Duration(seconds) * time.Second
}

// Allow checks limits.RPM/limits.TPM for keyID. If allowed, it reserves one
// request against the RPM counter. If blocked, reason is "rpm" or "tpm" and
// retryAfter is how long until the current window resets (rounded up to
// whole seconds).
func (l *Limiter) Allow(keyID string, limits storage.Limits) (allowed bool, reason string, retryAfter time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	st := l.getOrCreate(keyID, now)

	if st.tpmTokens >= int64(limits.TPM) {
		return false, "tpm", retryAfterFor(st.tpmWindowStart, now)
	}
	if st.rpmCount >= limits.RPM {
		return false, "rpm", retryAfterFor(st.rpmWindowStart, now)
	}

	st.rpmCount++
	return true, "", 0
}

// RecordTokens adds tokens to keyID's current TPM window. Called once the
// actual token usage of a request is known (after proxying completes).
func (l *Limiter) RecordTokens(keyID string, tokens int64) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	st := l.getOrCreate(keyID, now)
	st.tpmTokens += tokens
}
