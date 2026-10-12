// Package storage defines the persistence boundary (Store interface) for
// API key metadata and token usage, plus the FileStore JSON-file-backed
// implementation. Future Redis/RDB implementations only need to satisfy
// Store; internal/admin, internal/auth, and internal/tokenlimit never
// reference FileStore directly.
package storage

import "context"

// Limits holds the per-key rate/token limit configuration.
type Limits struct {
	RPM                 int    `json:"rpm"`
	TPM                 int    `json:"tpm"`
	MaxTokensPerRequest int    `json:"max_tokens_per_request"`
	QuotaPeriod         string `json:"quota_period"`
	QuotaTokens         int64  `json:"quota_tokens"`
}

// KeyRecord is one entry in data/keys.json.
type KeyRecord struct {
	KeyID     string  `json:"key_id"`
	KeyHash   string  `json:"key_hash"`
	Name      string  `json:"name"`
	CreatedAt string  `json:"created_at"`
	RevokedAt *string `json:"revoked_at"`
	Limits    Limits  `json:"limits"`
}

// UsageRecord tracks cumulative token usage for a key within one quota
// period (e.g. "2026-09" for monthly, "2026-09-08" for daily).
type UsageRecord struct {
	KeyID      string `json:"key_id"`
	Period     string `json:"period"`
	TokensUsed int64  `json:"tokens_used"`
	UpdatedAt  string `json:"updated_at"`
}

// Store is the persistence boundary. FileStore is the first
// implementation; future Redis/RDB implementations satisfy the same
// interface so upstream logic never changes.
type Store interface {
	CreateKey(ctx context.Context, rec KeyRecord) error
	GetKeyByHash(ctx context.Context, keyHash string) (KeyRecord, bool, error)
	RevokeKey(ctx context.Context, keyID string) error
	ListKeys(ctx context.Context) ([]KeyRecord, error)

	GetUsage(ctx context.Context, keyID string, period string) (UsageRecord, error)
	IncrementUsage(ctx context.Context, keyID string, period string, tokens int64) error
	ResetUsage(ctx context.Context, keyID string, period string) error
}
