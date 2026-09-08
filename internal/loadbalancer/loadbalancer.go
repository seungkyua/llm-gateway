// Package loadbalancer selects a healthy vLLM backend for each request
// using a pluggable Strategy (round_robin_healthcheck / least_requests).
// Backend health is written by internal/healthcheck and read here.
package loadbalancer

import (
	"errors"
	"sync"
	"sync/atomic"
)

// Status is a backend's health state machine value: Unknown → (Healthy |
// Unhealthy). Unknown backends are excluded from routing (cold-start
// protection, spec.md 상태 머신).
type Status int32

const (
	Unknown Status = iota
	Healthy
	Unhealthy
)

// ErrNoAvailableBackend is returned by Pick when no backend is Healthy.
var ErrNoAvailableBackend = errors.New("no available backend")

// Backend is one configured vLLM instance and its live health/inflight
// state.
type Backend struct {
	URL string

	status   atomic.Int32
	inFlight atomic.Int64
	failures atomic.Int32
}

// NewBackend creates a Backend in the Unknown state.
func NewBackend(url string) *Backend {
	b := &Backend{URL: url}
	b.status.Store(int32(Unknown))
	return b
}

// Status returns the backend's current health state.
func (b *Backend) Status() Status {
	return Status(b.status.Load())
}

// SetStatus updates the backend's health state.
func (b *Backend) SetStatus(s Status) {
	b.status.Store(int32(s))
}

// InFlight returns the number of requests currently being proxied to this
// backend.
func (b *Backend) InFlight() int64 {
	return b.inFlight.Load()
}

// IncInFlight/DecInFlight track in-progress requests for the
// least_requests strategy.
func (b *Backend) IncInFlight() { b.inFlight.Add(1) }
func (b *Backend) DecInFlight() { b.inFlight.Add(-1) }

// ConsecutiveFailures returns the current consecutive health-check failure
// count (used by internal/healthcheck).
func (b *Backend) ConsecutiveFailures() int32 {
	return b.failures.Load()
}

// RecordHealthCheckSuccess resets the failure counter and marks the
// backend Healthy.
func (b *Backend) RecordHealthCheckSuccess() {
	b.failures.Store(0)
	b.SetStatus(Healthy)
}

// RecordHealthCheckFailure increments the failure counter and marks the
// backend Unhealthy once threshold consecutive failures have occurred.
func (b *Backend) RecordHealthCheckFailure(threshold int32) {
	n := b.failures.Add(1)
	if n >= threshold {
		b.SetStatus(Unhealthy)
	}
}

func healthyOnly(backends []*Backend) []*Backend {
	out := make([]*Backend, 0, len(backends))
	for _, b := range backends {
		if b.Status() == Healthy {
			out = append(out, b)
		}
	}
	return out
}

// Strategy picks one backend among the given (already-configured) set.
// Implementations must themselves filter to Healthy backends.
type Strategy interface {
	Pick(backends []*Backend) (*Backend, error)
}

// RoundRobinHealthcheck cycles through Healthy backends in order.
type RoundRobinHealthcheck struct {
	mu   sync.Mutex
	next int
}

// Pick implements Strategy.
func (s *RoundRobinHealthcheck) Pick(backends []*Backend) (*Backend, error) {
	healthy := healthyOnly(backends)
	if len(healthy) == 0 {
		return nil, ErrNoAvailableBackend
	}
	s.mu.Lock()
	idx := s.next % len(healthy)
	s.next++
	s.mu.Unlock()
	return healthy[idx], nil
}

// LeastRequests picks the Healthy backend with the fewest in-flight
// requests.
type LeastRequests struct{}

// Pick implements Strategy.
func (s *LeastRequests) Pick(backends []*Backend) (*Backend, error) {
	healthy := healthyOnly(backends)
	if len(healthy) == 0 {
		return nil, ErrNoAvailableBackend
	}
	best := healthy[0]
	for _, b := range healthy[1:] {
		if b.InFlight() < best.InFlight() {
			best = b
		}
	}
	return best, nil
}

// LoadBalancer holds the configured backend set and picks among them via
// Strategy.
type LoadBalancer struct {
	backends []*Backend
	strategy Strategy
}

// New builds a LoadBalancer.
func New(backends []*Backend, strategy Strategy) *LoadBalancer {
	return &LoadBalancer{backends: backends, strategy: strategy}
}

// Pick selects one backend via the configured Strategy.
func (lb *LoadBalancer) Pick() (*Backend, error) {
	return lb.strategy.Pick(lb.backends)
}

// Backends returns all configured backends (Healthy or not), e.g. for
// health checking and /readyz reporting.
func (lb *LoadBalancer) Backends() []*Backend {
	return lb.backends
}

// HealthyCount returns how many configured backends are currently Healthy.
func (lb *LoadBalancer) HealthyCount() int {
	return len(healthyOnly(lb.backends))
}
