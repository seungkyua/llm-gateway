package auth

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/seungkyua/llm-gateway/internal/apierror"
	"github.com/seungkyua/llm-gateway/internal/storage"
)

const (
	bearerPrefix     = "Bearer "
	contextKeyRecord = "auth.key_record"
)

// Middleware requires "Authorization: Bearer <api_key>", hashes it, and
// looks it up via store.GetKeyByHash. On success the matching
// storage.KeyRecord is stored in the gin context for downstream handlers
// (rate limiter, token limiter, proxy, logging).
func Middleware(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if !strings.HasPrefix(header, bearerPrefix) {
			apierror.Abort(c, http.StatusUnauthorized, "missing or malformed API key", apierror.TypeAuthenticationError, apierror.CodeInvalidAPIKey)
			return
		}
		plainKey := strings.TrimPrefix(header, bearerPrefix)

		rec, found, err := store.GetKeyByHash(c.Request.Context(), HashAPIKey(plainKey))
		if err != nil {
			apierror.Abort(c, http.StatusInternalServerError, "failed to verify API key", apierror.TypeAPIError, "internal_error")
			return
		}
		if !found {
			apierror.Abort(c, http.StatusUnauthorized, "invalid API key", apierror.TypeAuthenticationError, apierror.CodeInvalidAPIKey)
			return
		}
		if rec.RevokedAt != nil {
			apierror.Abort(c, http.StatusUnauthorized, "API key has been revoked", apierror.TypeAuthenticationError, apierror.CodeInvalidAPIKey)
			return
		}

		c.Set(contextKeyRecord, rec)
		c.Next()
	}
}

// KeyRecordFromContext retrieves the storage.KeyRecord set by Middleware.
func KeyRecordFromContext(c *gin.Context) (storage.KeyRecord, bool) {
	v, ok := c.Get(contextKeyRecord)
	if !ok {
		return storage.KeyRecord{}, false
	}
	rec, ok := v.(storage.KeyRecord)
	return rec, ok
}

// SetKeyRecord stores rec in c the same way Middleware does. It is the
// setter counterpart to KeyRecordFromContext, intended for other packages
// (and their tests) that need to seed the authenticated key record without
// depending on Middleware's private context key string.
func SetKeyRecord(c *gin.Context, rec storage.KeyRecord) {
	c.Set(contextKeyRecord, rec)
}
