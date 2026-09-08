package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"llm-gateway/internal/config"
)

func newMockVLLM(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		if stream, _ := req["stream"].(bool); stream {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			flusher := w.(http.Flusher)
			for i := 0; i < 3; i++ {
				w.Write([]byte(`data: {"choices":[{"delta":{"content":"x"}}]}` + "\n\n"))
				flusher.Flush()
			}
			w.Write([]byte("data: [DONE]\n\n"))
			flusher.Flush()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"chatcmpl-1","choices":[{"message":{"content":"hi"}}],"usage":{"prompt_tokens":7,"completion_tokens":3}}`))
	})
	return httptest.NewServer(mux)
}

func testConfig(t *testing.T, backendURL string) *config.Config {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{}
	cfg.Server.ListenAddr = ":0"
	cfg.Admin.Token = "admin-secret"
	cfg.Backends = []config.BackendConfig{{URL: backendURL}}
	cfg.LoadBalancer.Strategy = "round_robin_healthcheck"
	cfg.LoadBalancer.HealthCheckInterval = "5s"
	cfg.RateLimit.Default.RPM = 60
	cfg.RateLimit.Default.TPM = 1000000
	cfg.TokenLimit.Default.MaxTokensPerRequest = 4096
	cfg.TokenLimit.Default.QuotaPeriod = "monthly"
	cfg.TokenLimit.Default.QuotaTokens = 1000000
	cfg.TokenCounting.Method = "usage_field"
	cfg.Storage.KeysFile = filepath.Join(dir, "keys.json")
	cfg.Storage.UsageFile = filepath.Join(dir, "usage.json")
	cfg.Logging.Format = "json"
	cfg.Logging.Output = "file"
	cfg.Logging.FilePath = filepath.Join(dir, "gateway.log")
	return cfg
}

func issueAdminKey(t *testing.T, r *gin.Engine, adminToken string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/admin/keys", bytes.NewBufferString(`{"name":"team-a"}`))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 issuing admin key, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	return resp["key"].(string)
}

func TestHealthzShouldReturn200Ok(t *testing.T) {
	gin.SetMode(gin.TestMode)
	backend := newMockVLLM(t)
	defer backend.Close()
	cfg := testConfig(t, backend.URL)
	r, cleanup, err := buildEngine(cfg)
	if err != nil {
		t.Fatalf("buildEngine failed: %v", err)
	}
	defer cleanup()

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["status"] != "ok" {
		t.Errorf("expected status ok, got %v", resp)
	}
}

func TestReadyzShouldReturn200WhenBackendHealthy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	backend := newMockVLLM(t)
	defer backend.Close()
	cfg := testConfig(t, backend.URL)
	r, cleanup, err := buildEngine(cfg)
	if err != nil {
		t.Fatalf("buildEngine failed: %v", err)
	}
	defer cleanup()

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["healthy_backends"].(float64) != 1 {
		t.Errorf("expected 1 healthy backend, got %v", resp)
	}
}

func TestReadyzShouldReturn503WhenNoBackendConfigured(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := testConfig(t, "http://127.0.0.1:1") // unreachable, health check will fail
	cfg.Backends = nil
	r, cleanup, err := buildEngine(cfg)
	if err != nil {
		t.Fatalf("buildEngine failed: %v", err)
	}
	defer cleanup()

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestEndToEndShouldProxyChatCompletionsWithAdminIssuedKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	backend := newMockVLLM(t)
	defer backend.Close()
	cfg := testConfig(t, backend.URL)
	r, cleanup, err := buildEngine(cfg)
	if err != nil {
		t.Fatalf("buildEngine failed: %v", err)
	}
	defer cleanup()

	apiKey := issueAdminKey(t, r, cfg.Admin.Token)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"m","messages":[]}`))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "chatcmpl-1") {
		t.Errorf("expected backend response relayed, got %s", w.Body.String())
	}
}

func TestEndToEndShouldReturn401WhenAuthorizationHeaderMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	backend := newMockVLLM(t)
	defer backend.Close()
	cfg := testConfig(t, backend.URL)
	r, cleanup, err := buildEngine(cfg)
	if err != nil {
		t.Fatalf("buildEngine failed: %v", err)
	}
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"m"}`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
	var resp map[string]map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error"]["code"] != "invalid_api_key" {
		t.Errorf("expected invalid_api_key, got %v", resp["error"])
	}
}

func TestEndToEndShouldStreamSSEResponseWithAdminIssuedKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	backend := newMockVLLM(t)
	defer backend.Close()
	cfg := testConfig(t, backend.URL)
	r, cleanup, err := buildEngine(cfg)
	if err != nil {
		t.Fatalf("buildEngine failed: %v", err)
	}
	defer cleanup()

	apiKey := issueAdminKey(t, r, cfg.Admin.Token)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"m","stream":true}`))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Errorf("expected text/event-stream, got %q", ct)
	}
	chunkCount := strings.Count(w.Body.String(), "data: {")
	if chunkCount != 3 {
		t.Errorf("expected 3 data chunks, got %d: %s", chunkCount, w.Body.String())
	}
}

func TestEndToEndShouldEnforceRPMLimitOnSecondRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	backend := newMockVLLM(t)
	defer backend.Close()
	cfg := testConfig(t, backend.URL)
	cfg.RateLimit.Default.RPM = 1
	r, cleanup, err := buildEngine(cfg)
	if err != nil {
		t.Fatalf("buildEngine failed: %v", err)
	}
	defer cleanup()

	apiKey := issueAdminKey(t, r, cfg.Admin.Token)

	makeReq := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"m"}`))
		req.Header.Set("Authorization", "Bearer "+apiKey)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	first := makeReq()
	second := makeReq()

	if first.Code != http.StatusOK {
		t.Fatalf("expected first request to succeed, got %d: %s", first.Code, first.Body.String())
	}
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("expected second request to be rate limited, got %d: %s", second.Code, second.Body.String())
	}
	if second.Header().Get("Retry-After") == "" {
		t.Error("expected Retry-After header on rate limited response")
	}
}

func TestEndToEndShouldWriteJSONLogEntryWithRequiredFieldsAndNoSensitiveData(t *testing.T) {
	gin.SetMode(gin.TestMode)
	backend := newMockVLLM(t)
	defer backend.Close()
	cfg := testConfig(t, backend.URL)
	r, cleanup, err := buildEngine(cfg)
	if err != nil {
		t.Fatalf("buildEngine failed: %v", err)
	}

	apiKey := issueAdminKey(t, r, cfg.Admin.Token)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"m"}`))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	cleanup() // flush/close the log file before reading it

	raw, err := os.ReadFile(cfg.Logging.FilePath)
	if err != nil {
		t.Fatalf("failed to read log file: %v", err)
	}
	line := strings.TrimSpace(string(raw))
	if strings.Count(line, "\n") != 0 {
		t.Fatalf("expected exactly one log line, got: %q", line)
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(line), &parsed); err != nil {
		t.Fatalf("expected valid JSON log line, got error %v: %s", err, line)
	}
	for _, field := range []string{"ttft_ms", "tpot_ms", "itl_ms", "prompt_tokens", "completion_tokens", "status", "request_id", "key_id"} {
		if _, ok := parsed[field]; !ok {
			t.Errorf("expected field %q in log line, got %v", field, parsed)
		}
	}
	if parsed["prompt_tokens"].(float64) != 7 || parsed["completion_tokens"].(float64) != 3 {
		t.Errorf("expected usage_field token counts 7/3, got %v/%v", parsed["prompt_tokens"], parsed["completion_tokens"])
	}
	for _, forbidden := range []string{"key_hash", "Authorization", apiKey, "messages"} {
		if strings.Contains(line, forbidden) {
			t.Errorf("expected log line not to contain %q, got %s", forbidden, line)
		}
	}
}

func TestEndToEndShouldReturn503WhenAllBackendsUnhealthy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := testConfig(t, "http://127.0.0.1:1") // nothing listens here -> health check fails
	r, cleanup, err := buildEngine(cfg)
	if err != nil {
		t.Fatalf("buildEngine failed: %v", err)
	}
	defer cleanup()

	apiKey := issueAdminKey(t, r, cfg.Admin.Token)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"m"}`))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error"]["code"] != "no_available_backend" {
		t.Errorf("expected no_available_backend, got %v", resp["error"])
	}
}

// TestPanicRecoveryShouldReturn500InsteadOfCrashing verifies gin.Recovery()
// behavior with a test-only panicking handler (spec.md Assumptions: no
// deliberate panic in production code).
func TestPanicRecoveryShouldReturn500InsteadOfCrashing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.GET("/panic-for-test", func(c *gin.Context) {
		panic("boom")
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/panic-for-test", nil))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}
