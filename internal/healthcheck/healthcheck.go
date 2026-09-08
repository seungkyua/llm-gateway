// Package healthcheck actively probes vLLM backends (GET <backend>/health)
// on a periodic interval and updates their loadbalancer.Backend status.
package healthcheck

import (
	"context"
	"net/http"
	"time"

	"llm-gateway/internal/loadbalancer"
)

// checkTimeout bounds a single backend health probe so one slow/hanging
// backend cannot stall the whole check cycle.
const checkTimeout = 3 * time.Second

// HealthChecker periodically GETs <backend URL>/health for each configured
// backend and transitions its Status accordingly (spec.md 상태 머신:
// 연속 실패 N회 → Unhealthy, 성공 1회 → Healthy).
type HealthChecker struct {
	backends         []*loadbalancer.Backend
	interval         time.Duration
	failureThreshold int32
	client           *http.Client
}

// New builds a HealthChecker. failureThreshold is the number of
// consecutive failures required before a backend transitions to Unhealthy
// (spec.md default: 1).
func New(backends []*loadbalancer.Backend, interval time.Duration, failureThreshold int32) *HealthChecker {
	return &HealthChecker{
		backends:         backends,
		interval:         interval,
		failureThreshold: failureThreshold,
		client:           &http.Client{Timeout: checkTimeout},
	}
}

// CheckOnce probes every backend exactly once, synchronously.
func (hc *HealthChecker) CheckOnce(ctx context.Context) {
	for _, b := range hc.backends {
		hc.checkBackend(ctx, b)
	}
}

func (hc *HealthChecker) checkBackend(ctx context.Context, b *loadbalancer.Backend) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.URL+"/health", nil)
	if err != nil {
		b.RecordHealthCheckFailure(hc.failureThreshold)
		return
	}

	resp, err := hc.client.Do(req)
	if err != nil {
		b.RecordHealthCheckFailure(hc.failureThreshold)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		b.RecordHealthCheckSuccess()
		return
	}
	b.RecordHealthCheckFailure(hc.failureThreshold)
}

// Run performs an initial check immediately, then repeats every interval
// until ctx is cancelled.
func (hc *HealthChecker) Run(ctx context.Context) {
	hc.CheckOnce(ctx)

	ticker := time.NewTicker(hc.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			hc.CheckOnce(ctx)
		}
	}
}
