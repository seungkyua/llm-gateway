package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// filePermission is used for both keys.json and usage.json since they hold
// sensitive data (key hashes, usage figures).
const filePermission = 0o600

// FileStore is a JSON-file-backed implementation of Store. It keeps an
// in-memory copy for fast reads and rewrites the whole file on every
// mutation (sufficient for the single-instance file-storage scope of this
// project; see spec.md Out of scope).
type FileStore struct {
	mu sync.Mutex

	keysPath  string
	usagePath string

	keys  []KeyRecord
	usage map[string]map[string]UsageRecord // keyID -> period -> record
}

// NewFileStore opens (creating if absent, with 0600 permission) keysPath and
// usagePath and loads their current contents into memory.
func NewFileStore(keysPath, usagePath string) (*FileStore, error) {
	fs := &FileStore{
		keysPath:  keysPath,
		usagePath: usagePath,
		usage:     map[string]map[string]UsageRecord{},
	}

	if err := ensureJSONFile(keysPath, []byte("[]")); err != nil {
		return nil, fmt.Errorf("storage: failed to initialize %q: %w", keysPath, err)
	}
	if err := ensureJSONFile(usagePath, []byte("{}")); err != nil {
		return nil, fmt.Errorf("storage: failed to initialize %q: %w", usagePath, err)
	}

	if err := fs.loadKeys(); err != nil {
		return nil, err
	}
	if err := fs.loadUsage(); err != nil {
		return nil, err
	}

	return fs, nil
}

func ensureJSONFile(path string, emptyContent []byte) error {
	if _, err := os.Stat(path); err == nil {
		// Existing file: normalize permission in case it was created
		// externally with a wider mode.
		return os.Chmod(path, filePermission)
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.WriteFile(path, emptyContent, filePermission)
}

func (fs *FileStore) loadKeys() error {
	raw, err := os.ReadFile(fs.keysPath)
	if err != nil {
		return fmt.Errorf("storage: failed to read %q: %w", fs.keysPath, err)
	}
	var keys []KeyRecord
	if err := json.Unmarshal(raw, &keys); err != nil {
		return fmt.Errorf("storage: failed to parse %q: %w", fs.keysPath, err)
	}
	fs.keys = keys
	return nil
}

func (fs *FileStore) loadUsage() error {
	raw, err := os.ReadFile(fs.usagePath)
	if err != nil {
		return fmt.Errorf("storage: failed to read %q: %w", fs.usagePath, err)
	}
	var usage map[string]map[string]UsageRecord
	if err := json.Unmarshal(raw, &usage); err != nil {
		return fmt.Errorf("storage: failed to parse %q: %w", fs.usagePath, err)
	}
	if usage == nil {
		usage = map[string]map[string]UsageRecord{}
	}
	fs.usage = usage
	return nil
}

func (fs *FileStore) persistKeysLocked() error {
	raw, err := json.MarshalIndent(fs.keys, "", "  ")
	if err != nil {
		return fmt.Errorf("storage: failed to encode keys: %w", err)
	}
	return os.WriteFile(fs.keysPath, raw, filePermission)
}

func (fs *FileStore) persistUsageLocked() error {
	raw, err := json.MarshalIndent(fs.usage, "", "  ")
	if err != nil {
		return fmt.Errorf("storage: failed to encode usage: %w", err)
	}
	return os.WriteFile(fs.usagePath, raw, filePermission)
}

// CreateKey appends rec and persists keys.json.
func (fs *FileStore) CreateKey(ctx context.Context, rec KeyRecord) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	fs.keys = append(fs.keys, rec)
	return fs.persistKeysLocked()
}

// GetKeyByHash returns the record whose KeyHash matches, if any.
func (fs *FileStore) GetKeyByHash(ctx context.Context, keyHash string) (KeyRecord, bool, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	for _, k := range fs.keys {
		if k.KeyHash == keyHash {
			return k, true, nil
		}
	}
	return KeyRecord{}, false, nil
}

// RevokeKey sets RevokedAt on the matching record (soft delete) and
// persists keys.json. Returns an error if keyID is unknown.
func (fs *FileStore) RevokeKey(ctx context.Context, keyID string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	for i, k := range fs.keys {
		if k.KeyID == keyID {
			now := time.Now().UTC().Format(time.RFC3339)
			fs.keys[i].RevokedAt = &now
			return fs.persistKeysLocked()
		}
	}
	return fmt.Errorf("storage: key_id %q not found", keyID)
}

// ListKeys returns a copy of all known key records.
func (fs *FileStore) ListKeys(ctx context.Context) ([]KeyRecord, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	out := make([]KeyRecord, len(fs.keys))
	copy(out, fs.keys)
	return out, nil
}

// GetUsage returns the usage record for keyID/period, or a zero-value
// record (TokensUsed=0) if none exists yet.
func (fs *FileStore) GetUsage(ctx context.Context, keyID string, period string) (UsageRecord, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	if periods, ok := fs.usage[keyID]; ok {
		if rec, ok := periods[period]; ok {
			return rec, nil
		}
	}
	return UsageRecord{KeyID: keyID, Period: period, TokensUsed: 0}, nil
}

// IncrementUsage adds tokens to the running total for keyID/period and
// persists usage.json.
func (fs *FileStore) IncrementUsage(ctx context.Context, keyID string, period string, tokens int64) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	periods, ok := fs.usage[keyID]
	if !ok {
		periods = map[string]UsageRecord{}
		fs.usage[keyID] = periods
	}
	rec := periods[period]
	rec.KeyID = keyID
	rec.Period = period
	rec.TokensUsed += tokens
	rec.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	periods[period] = rec

	return fs.persistUsageLocked()
}

// ResetUsage zeroes the usage counter for keyID/period and persists
// usage.json.
func (fs *FileStore) ResetUsage(ctx context.Context, keyID string, period string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	periods, ok := fs.usage[keyID]
	if !ok {
		periods = map[string]UsageRecord{}
		fs.usage[keyID] = periods
	}
	periods[period] = UsageRecord{
		KeyID:      keyID,
		Period:     period,
		TokensUsed: 0,
		UpdatedAt:  time.Now().UTC().Format(time.RFC3339),
	}

	return fs.persistUsageLocked()
}

var _ Store = (*FileStore)(nil)
