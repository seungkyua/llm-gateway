package main

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/seungkyua/llm-gateway/internal/apierror"
	"github.com/seungkyua/llm-gateway/internal/auth"
	"github.com/seungkyua/llm-gateway/internal/storage"
	"github.com/seungkyua/llm-gateway/internal/tokenlimit"
)

// Gin context keys used by this package's own middleware/handlers (the
// authenticated key record itself is read via auth.KeyRecordFromContext,
// set by auth.Middleware).
const (
	requestMetaContextKey = "request.meta"
	requestBodyContextKey = "request.body"
)

// chatRequestBody is the minimal subset of the OpenAI-compatible request
// body the gateway needs to read (never mutates or forwards a modified
// copy — the raw bytes are still proxied unchanged).
type chatRequestBody struct {
	Model     string `json:"model"`
	MaxTokens *int   `json:"max_tokens"`
	Stream    bool   `json:"stream"`
}

// rateLimiter is the subset of *ratelimit.Limiter used by this middleware.
type rateLimiter interface {
	Allow(keyID string, limits storage.Limits) (allowed bool, reason string, retryAfter time.Duration)
}

func keyRecordFromGinContext(c *gin.Context) storage.KeyRecord {
	rec, _ := auth.KeyRecordFromContext(c)
	return rec
}

// rateLimitMiddleware enforces the authenticated key's RPM/TPM limits
// (internal/ratelimit), matching spec.md's error matrix: 429
// rate_limit_error/rate_limit_exceeded with Retry-After.
func rateLimitMiddleware(rl rateLimiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		rec := keyRecordFromGinContext(c)

		allowed, _, retryAfter := rl.Allow(rec.KeyID, rec.Limits)
		if !allowed {
			setRetryAfterHeader(c, retryAfter)
			apierror.Abort(c, http.StatusTooManyRequests, "rate limit exceeded", apierror.TypeRateLimitError, apierror.CodeRateLimitExceeded)
			return
		}
		c.Next()
	}
}

// tokenLimitMiddleware enforces the per-request max_tokens cap and the
// cumulative quota (internal/tokenlimit), matching spec.md's error matrix:
// 400 max_tokens_exceeded / 429 quota_exceeded with Retry-After. It reads
// the request body once and restores it so the proxy can still forward it
// unchanged.
func tokenLimitMiddleware(store storage.Store) gin.HandlerFunc {
	checker := tokenlimit.New(store)
	return func(c *gin.Context) {
		rec := keyRecordFromGinContext(c)

		bodyBytes, err := io.ReadAll(c.Request.Body)
		if err != nil {
			apierror.Abort(c, http.StatusBadGateway, "failed to read request body", apierror.TypeAPIError, apierror.CodeBadGateway)
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))

		var meta chatRequestBody
		_ = json.Unmarshal(bodyBytes, &meta) // best-effort; malformed body is left for the backend/proxy to reject

		if !tokenlimit.CheckMaxTokens(rec.Limits, meta.MaxTokens) {
			apierror.Abort(c, http.StatusBadRequest, "max_tokens exceeds limits.max_tokens_per_request", apierror.TypeInvalidRequestError, apierror.CodeMaxTokensExceeded)
			return
		}

		now := time.Now()
		ok, err := checker.CheckQuota(c.Request.Context(), rec.KeyID, rec.Limits, now)
		if err != nil {
			apierror.Abort(c, http.StatusInternalServerError, "failed to check quota", apierror.TypeAPIError, "internal_error")
			return
		}
		if !ok {
			setRetryAfterHeader(c, periodRetryAfter(rec.Limits.QuotaPeriod, now))
			apierror.Abort(c, http.StatusTooManyRequests, "token quota exceeded", apierror.TypeRateLimitError, apierror.CodeQuotaExceeded)
			return
		}

		c.Set(requestMetaContextKey, meta)
		c.Set(requestBodyContextKey, bodyBytes)
		c.Next()
	}
}

func setRetryAfterHeader(c *gin.Context, d time.Duration) {
	seconds := int(math.Ceil(d.Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	c.Header("Retry-After", strconv.Itoa(seconds))
}

// periodRetryAfter returns how long until the current quota period
// (quotaPeriod: "daily" or "monthly") ends, i.e. when quota next resets.
func periodRetryAfter(quotaPeriod string, now time.Time) time.Duration {
	now = now.UTC()
	var end time.Time
	if quotaPeriod == "daily" {
		end = time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
	} else {
		end = time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC)
	}
	return end.Sub(now)
}

// requestIDMiddleware assigns a unique request id, used to correlate a
// request across the pipeline and its log line (spec.md 마일스톤 9).
func requestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(requestIDContextKey, newRequestID())
		c.Next()
	}
}

const requestIDContextKey = "request.id"

func requestIDFromGinContext(c *gin.Context) string {
	v, _ := c.Get(requestIDContextKey)
	id, _ := v.(string)
	return id
}
