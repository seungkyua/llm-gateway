package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"llm-gateway/internal/storage"
)

func newTestRouter(t *testing.T) (*gin.Engine, *storage.FileStore) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	fs, err := storage.NewFileStore(dir+"/keys.json", dir+"/usage.json")
	if err != nil {
		t.Fatalf("NewFileStore failed: %v", err)
	}

	h := NewHandler(fs, "admin-secret", storage.Limits{RPM: 60, TPM: 100000, MaxTokensPerRequest: 4096, QuotaPeriod: "monthly", QuotaTokens: 1000000})
	r := gin.New()
	group := r.Group("/admin")
	group.Use(AuthMiddleware("admin-secret"))
	RegisterRoutes(group, h)
	return r, fs
}

func doRequest(r *gin.Engine, method, path, token string, body any) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestPostAdminKeysShouldReturn201WithPlaintextKeyWhenRequestValid(t *testing.T) {
	r, _ := newTestRouter(t)

	w := doRequest(r, http.MethodPost, "/admin/keys", "admin-secret", map[string]any{"name": "team-a"})

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["key_id"] == nil || resp["key_id"] == "" {
		t.Error("expected key_id in response")
	}
	key, _ := resp["key"].(string)
	if key == "" || key[:3] != "sk-" {
		t.Errorf("expected key to start with sk-, got %q", key)
	}
	if resp["limits"] == nil {
		t.Error("expected limits in response")
	}
}

func TestPostAdminKeysShouldReturn401WhenAdminTokenMissing(t *testing.T) {
	r, _ := newTestRouter(t)

	w := doRequest(r, http.MethodPost, "/admin/keys", "", map[string]any{"name": "team-a"})

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
	var resp map[string]map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error"]["code"] != "invalid_admin_token" {
		t.Errorf("expected invalid_admin_token, got %v", resp["error"])
	}
}

func TestPostAdminKeysShouldReturn401WhenAdminTokenWrong(t *testing.T) {
	r, _ := newTestRouter(t)

	w := doRequest(r, http.MethodPost, "/admin/keys", "wrong-token", map[string]any{"name": "team-a"})

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestPostAdminKeysShouldReturn400WhenNameMissing(t *testing.T) {
	r, _ := newTestRouter(t)

	w := doRequest(r, http.MethodPost, "/admin/keys", "admin-secret", map[string]any{})

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error"]["code"] != "invalid_request_error" {
		t.Errorf("expected invalid_request_error, got %v", resp["error"])
	}
}

func TestGetAdminKeysShouldNotIncludeKeyHashOrPlaintext(t *testing.T) {
	r, _ := newTestRouter(t)
	doRequest(r, http.MethodPost, "/admin/keys", "admin-secret", map[string]any{"name": "team-a"})

	w := doRequest(r, http.MethodGet, "/admin/keys", "admin-secret", nil)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if bytes.Contains(w.Body.Bytes(), []byte("key_hash")) {
		t.Error("expected key_hash to be excluded from list response")
	}
	if bytes.Contains(w.Body.Bytes(), []byte(`"key":"sk-`)) {
		t.Errorf("expected plaintext key to be excluded, got %s", body)
	}
}

func TestDeleteAdminKeyShouldReturn200AndSetRevokedAtWhenKeyExists(t *testing.T) {
	r, _ := newTestRouter(t)
	createResp := doRequest(r, http.MethodPost, "/admin/keys", "admin-secret", map[string]any{"name": "team-a"})
	var created map[string]any
	json.Unmarshal(createResp.Body.Bytes(), &created)
	keyID := created["key_id"].(string)

	w := doRequest(r, http.MethodDelete, "/admin/keys/"+keyID, "admin-secret", nil)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["revoked_at"] == "" {
		t.Error("expected revoked_at to be set")
	}
}

func TestDeleteAdminKeyShouldReturn404WhenKeyDoesNotExist(t *testing.T) {
	r, _ := newTestRouter(t)

	w := doRequest(r, http.MethodDelete, "/admin/keys/no-such-key", "admin-secret", nil)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
	var resp map[string]map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error"]["code"] != "key_not_found" {
		t.Errorf("expected key_not_found, got %v", resp["error"])
	}
}
