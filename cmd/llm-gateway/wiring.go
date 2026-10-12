package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/seungkyua/llm-gateway/internal/admin"
	"github.com/seungkyua/llm-gateway/internal/auth"
	"github.com/seungkyua/llm-gateway/internal/config"
	"github.com/seungkyua/llm-gateway/internal/healthcheck"
	"github.com/seungkyua/llm-gateway/internal/loadbalancer"
	"github.com/seungkyua/llm-gateway/internal/metrics"
	"github.com/seungkyua/llm-gateway/internal/proxy"
	"github.com/seungkyua/llm-gateway/internal/ratelimit"
	"github.com/seungkyua/llm-gateway/internal/storage"
	"github.com/seungkyua/llm-gateway/internal/tokencount"
	"github.com/seungkyua/llm-gateway/internal/tokenlimit"
)

// healthCheckFailureThreshold is the number of consecutive health-check
// failures before a backend is marked Unhealthy (spec.md default: 1).
const healthCheckFailureThreshold = 1

const (
	strategyLeastRequests = "least_requests"
	logOutputFile         = "file"
)

// filePermission matches storage.FileStore's (0600): log files may contain
// operationally sensitive request metadata.
const logFilePermission = 0o600

// logDirPermission is used when auto-creating the parent directory of the
// log file (e.g. "logs/") on first boot, mirroring storage's dataDirPermission.
const logDirPermission = 0o700

// buildEngine wires config + all internal/* components into one
// *gin.Engine (spec.md 마일스톤 10). The returned cleanup func must be
// called on shutdown (stops the health checker goroutine and closes any
// open log file).
func buildEngine(cfg *config.Config) (*gin.Engine, func(), error) {
	store, err := storage.NewFileStore(cfg.Storage.KeysFile, cfg.Storage.UsageFile)
	if err != nil {
		return nil, nil, fmt.Errorf("wiring: failed to open storage: %w", err)
	}

	backends := make([]*loadbalancer.Backend, 0, len(cfg.Backends))
	for _, b := range cfg.Backends {
		backends = append(backends, loadbalancer.NewBackend(b.URL))
	}

	var strategy loadbalancer.Strategy = &loadbalancer.RoundRobinHealthcheck{}
	if cfg.LoadBalancer.Strategy == strategyLeastRequests {
		strategy = &loadbalancer.LeastRequests{}
	}
	lb := loadbalancer.New(backends, strategy)

	hc := healthcheck.New(backends, cfg.HealthCheckIntervalDuration(), healthCheckFailureThreshold)
	// Run one synchronous check up front so /readyz and the very first
	// proxied request reflect real backend health immediately, instead of
	// waiting for the first ticker interval.
	hc.CheckOnce(context.Background())
	hcCtx, hcCancel := context.WithCancel(context.Background())
	go hc.Run(hcCtx)

	rl := ratelimit.New()
	tl := tokenlimit.New(store)

	logWriter, logFile, err := openLogWriter(cfg)
	if err != nil {
		hcCancel()
		return nil, nil, err
	}
	logFormat := cfg.Logging.Format
	if logFormat == "" {
		logFormat = metrics.FormatJSON
	}
	logger := metrics.NewLogger(logFormat, logWriter)

	tokenCountingMethod := cfg.TokenCounting.Method
	if tokenCountingMethod == "" {
		tokenCountingMethod = tokencount.MethodUsageField
	}

	px := proxy.New(lb, proxy.NewHTTPClient(), proxy.WithOnComplete(
		makeOnComplete(tl, rl, logger, tokenCountingMethod),
	))

	defaultLimits := storage.Limits{
		RPM:                 cfg.RateLimit.Default.RPM,
		TPM:                 cfg.RateLimit.Default.TPM,
		MaxTokensPerRequest: cfg.TokenLimit.Default.MaxTokensPerRequest,
		QuotaPeriod:         cfg.TokenLimit.Default.QuotaPeriod,
		QuotaTokens:         cfg.TokenLimit.Default.QuotaTokens,
	}

	r := gin.New()
	r.Use(gin.Recovery())

	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})
	r.GET("/readyz", func(c *gin.Context) {
		healthy := lb.HealthyCount()
		total := len(backends)
		status := 200
		readyStatus := "ready"
		if healthy == 0 {
			status = 503
			readyStatus = "not_ready"
		}
		c.JSON(status, gin.H{"status": readyStatus, "healthy_backends": healthy, "total_backends": total})
	})

	adminGroup := r.Group("/admin")
	adminGroup.Use(admin.AuthMiddleware(cfg.Admin.Token))
	admin.RegisterRoutes(adminGroup, admin.NewHandler(store, cfg.Admin.Token, defaultLimits))

	v1 := r.Group("/v1")
	v1.Use(auth.Middleware(store))
	v1.Use(requestIDMiddleware())
	v1.Use(rateLimitMiddleware(rl))
	v1.Use(tokenLimitMiddleware(store))
	v1.POST("/chat/completions", px.Handler())
	v1.POST("/completions", px.Handler())

	cleanup := func() {
		hcCancel()
		if logFile != nil {
			logFile.Close()
		}
	}

	return r, cleanup, nil
}

func openLogWriter(cfg *config.Config) (io.Writer, *os.File, error) {
	if cfg.Logging.Output != logOutputFile {
		return os.Stdout, nil, nil
	}
	// Fresh install / clean checkout: the parent directory (e.g. "logs/")
	// is git-ignored and therefore does not exist yet. It must be created
	// before the file itself so first boot succeeds without any manual
	// "mkdir logs" step (spec.md data model note, symmetric with data/).
	if err := os.MkdirAll(filepath.Dir(cfg.Logging.FilePath), logDirPermission); err != nil {
		return nil, nil, fmt.Errorf("wiring: failed to create log directory for %q: %w", cfg.Logging.FilePath, err)
	}
	f, err := os.OpenFile(cfg.Logging.FilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, logFilePermission)
	if err != nil {
		return nil, nil, fmt.Errorf("wiring: failed to open log file %q: %w", cfg.Logging.FilePath, err)
	}
	return f, f, nil
}

// makeOnComplete builds the proxy.WithOnComplete callback that counts
// tokens, records rate-limit/quota usage, and writes the structured
// request log line (spec.md 마일스톤 9).
func makeOnComplete(tl *tokenlimit.Checker, rl *ratelimit.Limiter, logger *metrics.Logger, method string) func(*gin.Context, proxy.Result) {
	return func(c *gin.Context, res proxy.Result) {
		rec, _ := auth.KeyRecordFromContext(c)

		var meta chatRequestBody
		if v, ok := c.Get(requestMetaContextKey); ok {
			meta, _ = v.(chatRequestBody)
		}

		promptTokens, completionTokens := countTokens(method, res)

		now := time.Now()
		totalTokens := int64(promptTokens + completionTokens)
		if totalTokens > 0 {
			_ = tl.RecordUsage(c.Request.Context(), rec.KeyID, rec.Limits, totalTokens, now)
		}
		rl.RecordTokens(rec.KeyID, totalTokens)

		timing := metrics.Timing{Start: res.Start, ChunkArrivals: res.ChunkTimes, End: res.End}
		logger.Log(metrics.LogEntry{
			Timestamp:        now.UTC().Format(time.RFC3339),
			RequestID:        requestIDFromGinContext(c),
			KeyID:            rec.KeyID,
			Model:            meta.Model,
			Backend:          res.BackendURL,
			Stream:           res.Stream,
			TTFTMillis:       timing.TTFTMillis(),
			TPOTMillis:       timing.TPOTMillis(),
			ITLMillis:        timing.ITLMillis(),
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
			Status:           res.Status,
		})
	}
}

func countTokens(method string, res proxy.Result) (promptTokens, completionTokens int) {
	if method == tokencount.MethodTokenizer {
		// Only the actual prompt/completion text (messages[].content or
		// prompt / choices[].message.content or choices[].text) is
		// counted, not the raw request/response JSON, so JSON field
		// names/punctuation/unrelated fields don't inflate quota or TPM
		// accounting (spec.md 마일스톤 9, 인수 기준 21).
		return tokencount.CountApprox(tokencount.ExtractRequestText(res.RequestBody)),
			tokencount.CountApprox(tokencount.ExtractResponseText(res.ResponseBody))
	}
	usage, ok := tokencount.FromUsageField(res.ResponseBody)
	if !ok {
		return 0, 0
	}
	return usage.PromptTokens, usage.CompletionTokens
}

// Note: no panic-inducing route is registered in buildEngine. Recover
// middleware behavior (acceptance criterion 26) is verified with a
// test-only handler instead, per spec.md Assumptions ("실제 프로덕션
// 코드에 의도적 패닉을 넣지 않고 ... 테스트 전용 핸들러로 검증").
