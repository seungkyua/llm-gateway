// Package tokenlimit enforces per-request max_tokens caps and per-period
// cumulative token quotas backed by storage.Store.
package tokenlimit

import (
	"context"
	"time"

	"github.com/seungkyua/llm-gateway/internal/storage"
)

const (
	monthlyPeriodLayout = "2006-01"
	dailyPeriodLayout   = "2006-01-02"
)

// PeriodKey computes the usage.json period key for quotaPeriod ("daily" or
// "monthly") at the given instant, e.g. "2026-09" or "2026-09-08".
func PeriodKey(quotaPeriod string, now time.Time) string {
	if quotaPeriod == "daily" {
		return now.UTC().Format(dailyPeriodLayout)
	}
	return now.UTC().Format(monthlyPeriodLayout)
}

// CheckMaxTokens returns false when requestedMaxTokens is set and exceeds
// limits.MaxTokensPerRequest. A nil requestedMaxTokens (field absent from
// the request body) always passes.
func CheckMaxTokens(limits storage.Limits, requestedMaxTokens *int) bool {
	if requestedMaxTokens == nil {
		return true
	}
	return *requestedMaxTokens <= limits.MaxTokensPerRequest
}

// Checker checks and records cumulative token quota usage via a
// storage.Store.
type Checker struct {
	store storage.Store
}

// New builds a Checker backed by store.
func New(store storage.Store) *Checker {
	return &Checker{store: store}
}

// CheckQuota returns whether keyID still has quota remaining for the period
// containing now.
func (c *Checker) CheckQuota(ctx context.Context, keyID string, limits storage.Limits, now time.Time) (bool, error) {
	usage, err := c.store.GetUsage(ctx, keyID, PeriodKey(limits.QuotaPeriod, now))
	if err != nil {
		return false, err
	}
	return usage.TokensUsed < limits.QuotaTokens, nil
}

// RecordUsage adds tokens to keyID's usage for the period containing now.
func (c *Checker) RecordUsage(ctx context.Context, keyID string, limits storage.Limits, tokens int64, now time.Time) error {
	return c.store.IncrementUsage(ctx, keyID, PeriodKey(limits.QuotaPeriod, now), tokens)
}
