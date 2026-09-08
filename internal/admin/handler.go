// Package admin implements the admin REST API (POST/GET /admin/keys,
// DELETE /admin/keys/{key_id}) used to manage client API keys.
package admin

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"llm-gateway/internal/apierror"
	"llm-gateway/internal/auth"
	"llm-gateway/internal/storage"
)

// Handler holds the dependencies needed by the admin API handlers.
type Handler struct {
	store         storage.Store
	adminToken    string
	defaultLimits storage.Limits
}

// NewHandler builds a Handler. adminToken is currently unused by the
// handlers themselves (AuthMiddleware enforces it) but is kept so callers
// can construct Handler and its middleware from the same config value.
func NewHandler(store storage.Store, adminToken string, defaultLimits storage.Limits) *Handler {
	return &Handler{store: store, adminToken: adminToken, defaultLimits: defaultLimits}
}

// RegisterRoutes wires the admin endpoints onto group (expected to already
// have AuthMiddleware applied).
func RegisterRoutes(group *gin.RouterGroup, h *Handler) {
	group.POST("/keys", h.createKey)
	group.GET("/keys", h.listKeys)
	group.DELETE("/keys/:key_id", h.revokeKey)
}

type createKeyRequest struct {
	Name                string  `json:"name"`
	RPM                 *int    `json:"rpm"`
	TPM                 *int    `json:"tpm"`
	MaxTokensPerRequest *int    `json:"max_tokens_per_request"`
	QuotaPeriod         *string `json:"quota_period"`
	QuotaTokens         *int64  `json:"quota_tokens"`
}

// keyListItem is KeyRecord without KeyHash, used for GET /admin/keys.
type keyListItem struct {
	KeyID     string         `json:"key_id"`
	Name      string         `json:"name"`
	CreatedAt string         `json:"created_at"`
	RevokedAt *string        `json:"revoked_at"`
	Limits    storage.Limits `json:"limits"`
}

func (h *Handler) createKey(c *gin.Context) {
	var req createKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apierror.Abort(c, http.StatusBadRequest, "invalid request body", apierror.TypeInvalidRequestError, apierror.CodeInvalidRequest)
		return
	}
	if len(req.Name) < 1 || len(req.Name) > 100 {
		apierror.Abort(c, http.StatusBadRequest, "name is required and must be 1-100 characters", apierror.TypeInvalidRequestError, apierror.CodeInvalidRequest)
		return
	}

	limits := h.defaultLimits
	if req.RPM != nil {
		limits.RPM = *req.RPM
	}
	if req.TPM != nil {
		limits.TPM = *req.TPM
	}
	if req.MaxTokensPerRequest != nil {
		limits.MaxTokensPerRequest = *req.MaxTokensPerRequest
	}
	if req.QuotaPeriod != nil {
		limits.QuotaPeriod = *req.QuotaPeriod
	}
	if req.QuotaTokens != nil {
		limits.QuotaTokens = *req.QuotaTokens
	}

	keyID, err := newKeyID()
	if err != nil {
		apierror.Abort(c, http.StatusInternalServerError, "failed to generate key id", apierror.TypeAPIError, "internal_error")
		return
	}
	plainKey, err := newPlainAPIKey()
	if err != nil {
		apierror.Abort(c, http.StatusInternalServerError, "failed to generate api key", apierror.TypeAPIError, "internal_error")
		return
	}

	rec := storage.KeyRecord{
		KeyID:     keyID,
		KeyHash:   auth.HashAPIKey(plainKey),
		Name:      req.Name,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		RevokedAt: nil,
		Limits:    limits,
	}

	if err := h.store.CreateKey(c.Request.Context(), rec); err != nil {
		apierror.Abort(c, http.StatusInternalServerError, "failed to persist key", apierror.TypeAPIError, "internal_error")
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"key_id":     rec.KeyID,
		"key":        plainKey,
		"name":       rec.Name,
		"created_at": rec.CreatedAt,
		"limits":     rec.Limits,
	})
}

func (h *Handler) listKeys(c *gin.Context) {
	recs, err := h.store.ListKeys(c.Request.Context())
	if err != nil {
		apierror.Abort(c, http.StatusInternalServerError, "failed to list keys", apierror.TypeAPIError, "internal_error")
		return
	}

	items := make([]keyListItem, 0, len(recs))
	for _, r := range recs {
		items = append(items, keyListItem{
			KeyID:     r.KeyID,
			Name:      r.Name,
			CreatedAt: r.CreatedAt,
			RevokedAt: r.RevokedAt,
			Limits:    r.Limits,
		})
	}

	c.JSON(http.StatusOK, items)
}

func (h *Handler) revokeKey(c *gin.Context) {
	keyID := c.Param("key_id")

	if err := h.store.RevokeKey(c.Request.Context(), keyID); err != nil {
		apierror.Abort(c, http.StatusNotFound, "key_id not found", apierror.TypeInvalidRequestError, apierror.CodeKeyNotFound)
		return
	}

	recs, err := h.store.ListKeys(c.Request.Context())
	if err != nil {
		apierror.Abort(c, http.StatusInternalServerError, "failed to load revoked key", apierror.TypeAPIError, "internal_error")
		return
	}
	var revokedAt string
	for _, r := range recs {
		if r.KeyID == keyID && r.RevokedAt != nil {
			revokedAt = *r.RevokedAt
			break
		}
	}

	c.JSON(http.StatusOK, gin.H{"key_id": keyID, "revoked_at": revokedAt})
}
