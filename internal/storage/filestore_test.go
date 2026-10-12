package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func newTestFileStore(t *testing.T) *FileStore {
	t.Helper()
	dir := t.TempDir()
	fs, err := NewFileStore(filepath.Join(dir, "keys.json"), filepath.Join(dir, "usage.json"))
	if err != nil {
		t.Fatalf("NewFileStore failed: %v", err)
	}
	return fs
}

func TestFileStoreShouldReturnCreatedKeyWhenGetKeyByHashCalledWithMatchingHash(t *testing.T) {
	fs := newTestFileStore(t)
	ctx := context.Background()
	rec := KeyRecord{KeyID: "key_1", KeyHash: "hash_1", Name: "team-a", CreatedAt: "2026-09-08T00:00:00Z"}

	if err := fs.CreateKey(ctx, rec); err != nil {
		t.Fatalf("CreateKey failed: %v", err)
	}

	got, found, err := fs.GetKeyByHash(ctx, "hash_1")
	if err != nil {
		t.Fatalf("GetKeyByHash failed: %v", err)
	}
	if !found {
		t.Fatal("expected key to be found")
	}
	if got.KeyID != "key_1" {
		t.Errorf("expected key_1, got %q", got.KeyID)
	}
}

func TestFileStoreShouldReturnNotFoundWhenGetKeyByHashCalledWithUnknownHash(t *testing.T) {
	fs := newTestFileStore(t)
	ctx := context.Background()

	_, found, err := fs.GetKeyByHash(ctx, "no-such-hash")

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if found {
		t.Fatal("expected key not to be found")
	}
}

func TestFileStoreShouldSetRevokedAtWhenRevokeKeyCalled(t *testing.T) {
	fs := newTestFileStore(t)
	ctx := context.Background()
	rec := KeyRecord{KeyID: "key_1", KeyHash: "hash_1", Name: "team-a", CreatedAt: "2026-09-08T00:00:00Z"}
	fs.CreateKey(ctx, rec)

	if err := fs.RevokeKey(ctx, "key_1"); err != nil {
		t.Fatalf("RevokeKey failed: %v", err)
	}

	got, found, err := fs.GetKeyByHash(ctx, "hash_1")
	if err != nil || !found {
		t.Fatalf("expected key found, err=%v found=%v", err, found)
	}
	if got.RevokedAt == nil || *got.RevokedAt == "" {
		t.Error("expected revoked_at to be set")
	}
}

func TestFileStoreShouldReturnErrorWhenRevokeKeyCalledWithUnknownID(t *testing.T) {
	fs := newTestFileStore(t)
	ctx := context.Background()

	err := fs.RevokeKey(ctx, "no-such-id")

	if err == nil {
		t.Fatal("expected error for unknown key_id")
	}
}

func TestFileStoreShouldListAllCreatedKeysWhenListKeysCalled(t *testing.T) {
	fs := newTestFileStore(t)
	ctx := context.Background()
	fs.CreateKey(ctx, KeyRecord{KeyID: "key_1", KeyHash: "hash_1", Name: "a", CreatedAt: "2026-09-08T00:00:00Z"})
	fs.CreateKey(ctx, KeyRecord{KeyID: "key_2", KeyHash: "hash_2", Name: "b", CreatedAt: "2026-09-08T00:00:00Z"})

	list, err := fs.ListKeys(ctx)

	if err != nil {
		t.Fatalf("ListKeys failed: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("expected 2 keys, got %d", len(list))
	}
}

func TestFileStoreShouldReturnZeroUsageWhenGetUsageCalledForUnknownKey(t *testing.T) {
	fs := newTestFileStore(t)
	ctx := context.Background()

	usage, err := fs.GetUsage(ctx, "key_1", "2026-09")

	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}
	if usage.TokensUsed != 0 {
		t.Errorf("expected 0 tokens used, got %d", usage.TokensUsed)
	}
}

func TestFileStoreShouldAccumulateTokensWhenIncrementUsageCalledMultipleTimes(t *testing.T) {
	fs := newTestFileStore(t)
	ctx := context.Background()

	fs.IncrementUsage(ctx, "key_1", "2026-09", 100)
	fs.IncrementUsage(ctx, "key_1", "2026-09", 50)

	usage, err := fs.GetUsage(ctx, "key_1", "2026-09")
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}
	if usage.TokensUsed != 150 {
		t.Errorf("expected 150 tokens used, got %d", usage.TokensUsed)
	}
}

func TestFileStoreShouldResetTokensToZeroWhenResetUsageCalled(t *testing.T) {
	fs := newTestFileStore(t)
	ctx := context.Background()
	fs.IncrementUsage(ctx, "key_1", "2026-09", 100)

	if err := fs.ResetUsage(ctx, "key_1", "2026-09"); err != nil {
		t.Fatalf("ResetUsage failed: %v", err)
	}

	usage, err := fs.GetUsage(ctx, "key_1", "2026-09")
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}
	if usage.TokensUsed != 0 {
		t.Errorf("expected 0 tokens used after reset, got %d", usage.TokensUsed)
	}
}

func TestNewFileStoreShouldCreateFilesWithPermission0600WhenFilesDoNotExist(t *testing.T) {
	dir := t.TempDir()
	keysPath := filepath.Join(dir, "keys.json")
	usagePath := filepath.Join(dir, "usage.json")

	if _, err := NewFileStore(keysPath, usagePath); err != nil {
		t.Fatalf("NewFileStore failed: %v", err)
	}

	for _, p := range []string{keysPath, usagePath} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatalf("expected file %q to exist: %v", p, err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("expected 0600 permission for %q, got %v", p, perm)
		}
	}
}

// TestNewFileStoreShouldCreateParentDirectoryWhenItDoesNotExist reproduces
// a clean-checkout first boot: the parent directory ("data/") is not
// pre-created (unlike t.TempDir(), which always already exists), so
// NewFileStore itself must MkdirAll it before writing keys.json/usage.json.
func TestNewFileStoreShouldCreateParentDirectoryWhenItDoesNotExist(t *testing.T) {
	base := t.TempDir()
	// "data" is a nested subdirectory that does not exist yet under base.
	dataDir := filepath.Join(base, "data")
	keysPath := filepath.Join(dataDir, "keys.json")
	usagePath := filepath.Join(dataDir, "usage.json")

	if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
		t.Fatalf("precondition failed: dataDir %q must not exist yet", dataDir)
	}

	if _, err := NewFileStore(keysPath, usagePath); err != nil {
		t.Fatalf("NewFileStore failed when parent directory did not exist: %v", err)
	}

	for _, p := range []string{keysPath, usagePath} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatalf("expected file %q to exist after auto-creating parent dir: %v", p, err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("expected 0600 permission for %q, got %v", p, perm)
		}
	}
}
