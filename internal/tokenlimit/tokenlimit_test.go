package tokenlimit

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"llm-gateway/internal/storage"
)

func newTestStore(t *testing.T) storage.Store {
	t.Helper()
	dir := t.TempDir()
	fs, err := storage.NewFileStore(filepath.Join(dir, "keys.json"), filepath.Join(dir, "usage.json"))
	if err != nil {
		t.Fatalf("NewFileStore failed: %v", err)
	}
	return fs
}

func TestCheckMaxTokensShouldReturnTrueWhenRequestedWithinLimit(t *testing.T) {
	limits := storage.Limits{MaxTokensPerRequest: 4096}
	requested := 1000

	ok := CheckMaxTokens(limits, &requested)

	if !ok {
		t.Error("expected requested tokens within limit to pass")
	}
}

func TestCheckMaxTokensShouldReturnFalseWhenRequestedExceedsLimit(t *testing.T) {
	limits := storage.Limits{MaxTokensPerRequest: 4096}
	requested := 5000

	ok := CheckMaxTokens(limits, &requested)

	if ok {
		t.Error("expected requested tokens exceeding limit to fail")
	}
}

func TestCheckMaxTokensShouldReturnTrueWhenRequestDoesNotSpecifyMaxTokens(t *testing.T) {
	limits := storage.Limits{MaxTokensPerRequest: 4096}

	ok := CheckMaxTokens(limits, nil)

	if !ok {
		t.Error("expected unspecified max_tokens to pass")
	}
}

func TestCheckQuotaShouldReturnTrueWhenUsageBelowQuota(t *testing.T) {
	store := newTestStore(t)
	checker := New(store)
	limits := storage.Limits{QuotaPeriod: "monthly", QuotaTokens: 1000}

	ok, err := checker.CheckQuota(context.Background(), "key_1", limits, time.Now())

	if err != nil {
		t.Fatalf("CheckQuota failed: %v", err)
	}
	if !ok {
		t.Error("expected fresh key to be within quota")
	}
}

func TestCheckQuotaShouldReturnFalseWhenUsageAtOrAboveQuota(t *testing.T) {
	store := newTestStore(t)
	checker := New(store)
	limits := storage.Limits{QuotaPeriod: "monthly", QuotaTokens: 1000}
	now := time.Now()
	store.IncrementUsage(context.Background(), "key_1", PeriodKey(limits.QuotaPeriod, now), 1000)

	ok, err := checker.CheckQuota(context.Background(), "key_1", limits, now)

	if err != nil {
		t.Fatalf("CheckQuota failed: %v", err)
	}
	if ok {
		t.Error("expected exhausted quota to fail check")
	}
}

func TestRecordUsageShouldIncreaseStoredUsageWhenCalled(t *testing.T) {
	store := newTestStore(t)
	checker := New(store)
	limits := storage.Limits{QuotaPeriod: "monthly", QuotaTokens: 1000}
	now := time.Now()

	if err := checker.RecordUsage(context.Background(), "key_1", limits, 150, now); err != nil {
		t.Fatalf("RecordUsage failed: %v", err)
	}

	usage, err := store.GetUsage(context.Background(), "key_1", PeriodKey(limits.QuotaPeriod, now))
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}
	if usage.TokensUsed != 150 {
		t.Errorf("expected 150 tokens used, got %d", usage.TokensUsed)
	}
}

func TestPeriodKeyShouldReturnYearMonthWhenPeriodIsMonthly(t *testing.T) {
	now := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)

	key := PeriodKey("monthly", now)

	if key != "2026-09" {
		t.Errorf("expected 2026-09, got %q", key)
	}
}

func TestPeriodKeyShouldReturnYearMonthDayWhenPeriodIsDaily(t *testing.T) {
	now := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)

	key := PeriodKey("daily", now)

	if key != "2026-09-08" {
		t.Errorf("expected 2026-09-08, got %q", key)
	}
}
