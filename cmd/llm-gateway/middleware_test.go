package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/seungkyua/llm-gateway/internal/auth"
	"github.com/seungkyua/llm-gateway/internal/ratelimit"
	"github.com/seungkyua/llm-gateway/internal/storage"
)

func TestRateLimitMiddlewareShouldReturn429WithRetryAfterWhenRPMExceeded(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rl := ratelimit.New()
	r := gin.New()
	r.Use(func(c *gin.Context) {
		auth.SetKeyRecord(c, storage.KeyRecord{KeyID: "key_1", Limits: storage.Limits{RPM: 1, TPM: 100000}})
		c.Next()
	})
	r.Use(rateLimitMiddleware(rl))
	r.GET("/v1/test", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
	r.ServeHTTP(httptest.NewRecorder(), req) // consume the single allowed request

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/test", nil))

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", w.Code)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Error("expected Retry-After header to be set")
	}
	var resp map[string]map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error"]["code"] != "rate_limit_exceeded" {
		t.Errorf("expected rate_limit_exceeded, got %v", resp["error"])
	}
}

func newTokenLimitTestRouter(t *testing.T, limits storage.Limits) (*gin.Engine, storage.Store) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	store, err := storage.NewFileStore(dir+"/keys.json", dir+"/usage.json")
	if err != nil {
		t.Fatalf("NewFileStore failed: %v", err)
	}

	r := gin.New()
	r.Use(func(c *gin.Context) {
		auth.SetKeyRecord(c, storage.KeyRecord{KeyID: "key_1", Limits: limits})
		c.Next()
	})
	r.Use(tokenLimitMiddleware(store))
	r.POST("/v1/test", func(c *gin.Context) { c.Status(http.StatusOK) })
	return r, store
}

func TestTokenLimitMiddlewareShouldReturn400WhenMaxTokensExceedsLimit(t *testing.T) {
	r, _ := newTokenLimitTestRouter(t, storage.Limits{MaxTokensPerRequest: 100, QuotaPeriod: "monthly", QuotaTokens: 1000000})

	req := httptest.NewRequest(http.MethodPost, "/v1/test", bytes.NewBufferString(`{"max_tokens":500}`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error"]["code"] != "max_tokens_exceeded" {
		t.Errorf("expected max_tokens_exceeded, got %v", resp["error"])
	}
}

func TestTokenLimitMiddlewareShouldReturn429WhenQuotaExhausted(t *testing.T) {
	limits := storage.Limits{MaxTokensPerRequest: 4096, QuotaPeriod: "monthly", QuotaTokens: 100}
	r, store := newTokenLimitTestRouter(t, limits)
	store.IncrementUsage(newTestCtx(), "key_1", currentMonthlyPeriod(), 100)

	req := httptest.NewRequest(http.MethodPost, "/v1/test", bytes.NewBufferString(`{}`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error"]["code"] != "quota_exceeded" {
		t.Errorf("expected quota_exceeded, got %v", resp["error"])
	}
	if w.Header().Get("Retry-After") == "" {
		t.Error("expected Retry-After header to be set")
	}
}

func TestTokenLimitMiddlewareShouldPassThroughWhenWithinLimits(t *testing.T) {
	r, _ := newTokenLimitTestRouter(t, storage.Limits{MaxTokensPerRequest: 4096, QuotaPeriod: "monthly", QuotaTokens: 1000000})

	req := httptest.NewRequest(http.MethodPost, "/v1/test", bytes.NewBufferString(`{"max_tokens":100}`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func newTestCtx() context.Context { return context.Background() }

func currentMonthlyPeriod() string { return time.Now().UTC().Format("2006-01") }
