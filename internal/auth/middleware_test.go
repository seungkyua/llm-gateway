package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"llm-gateway/internal/storage"
)

// fakeStore is a minimal in-memory storage.Store for middleware tests.
type fakeStore struct {
	keysByHash map[string]storage.KeyRecord
}

func newFakeStore() *fakeStore {
	return &fakeStore{keysByHash: map[string]storage.KeyRecord{}}
}

func (f *fakeStore) CreateKey(ctx context.Context, rec storage.KeyRecord) error {
	f.keysByHash[rec.KeyHash] = rec
	return nil
}
func (f *fakeStore) GetKeyByHash(ctx context.Context, keyHash string) (storage.KeyRecord, bool, error) {
	rec, ok := f.keysByHash[keyHash]
	return rec, ok, nil
}
func (f *fakeStore) RevokeKey(ctx context.Context, keyID string) error { return nil }
func (f *fakeStore) ListKeys(ctx context.Context) ([]storage.KeyRecord, error) {
	return nil, nil
}
func (f *fakeStore) GetUsage(ctx context.Context, keyID, period string) (storage.UsageRecord, error) {
	return storage.UsageRecord{}, nil
}
func (f *fakeStore) IncrementUsage(ctx context.Context, keyID, period string, tokens int64) error {
	return nil
}
func (f *fakeStore) ResetUsage(ctx context.Context, keyID, period string) error { return nil }

func newTestRouter(store storage.Store) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(Middleware(store))
	r.GET("/v1/test", func(c *gin.Context) {
		rec, ok := KeyRecordFromContext(c)
		if !ok {
			c.String(http.StatusInternalServerError, "no key record")
			return
		}
		c.String(http.StatusOK, rec.KeyID)
	})
	return r
}

func doGet(r *gin.Engine, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestMiddlewareShouldPassThroughAndSetKeyRecordWhenKeyValid(t *testing.T) {
	store := newFakeStore()
	store.CreateKey(context.Background(), storage.KeyRecord{KeyID: "key_1", KeyHash: HashAPIKey("sk-valid")})
	r := newTestRouter(store)

	w := doGet(r, "sk-valid")

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if w.Body.String() != "key_1" {
		t.Errorf("expected key record to be passed to handler, got %q", w.Body.String())
	}
}

func TestMiddlewareShouldReturn401WhenAuthorizationHeaderMissing(t *testing.T) {
	store := newFakeStore()
	r := newTestRouter(store)

	w := doGet(r, "")

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestMiddlewareShouldReturn401WhenKeyUnknown(t *testing.T) {
	store := newFakeStore()
	r := newTestRouter(store)

	w := doGet(r, "sk-unknown")

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestSetKeyRecordShouldMakeRecordRetrievableViaKeyRecordFromContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	want := storage.KeyRecord{KeyID: "key_42"}

	SetKeyRecord(c, want)
	got, ok := KeyRecordFromContext(c)

	if !ok {
		t.Fatal("expected key record to be found")
	}
	if got.KeyID != want.KeyID {
		t.Errorf("expected KeyID %q, got %q", want.KeyID, got.KeyID)
	}
}

func TestMiddlewareShouldReturn401WhenKeyRevoked(t *testing.T) {
	store := newFakeStore()
	revokedAt := "2026-09-08T00:00:00Z"
	store.CreateKey(context.Background(), storage.KeyRecord{KeyID: "key_1", KeyHash: HashAPIKey("sk-revoked"), RevokedAt: &revokedAt})
	r := newTestRouter(store)

	w := doGet(r, "sk-revoked")

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}
