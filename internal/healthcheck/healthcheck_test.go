package healthcheck

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/seungkyua/llm-gateway/internal/loadbalancer"
)

func TestCheckOnceShouldMarkBackendHealthyWhenHealthEndpointSucceeds(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	backend := loadbalancer.NewBackend(server.URL)
	hc := New([]*loadbalancer.Backend{backend}, time.Second, 1)

	hc.CheckOnce(context.Background())

	if backend.Status() != loadbalancer.Healthy {
		t.Errorf("expected backend to become Healthy, got status %v", backend.Status())
	}
}

func TestCheckOnceShouldMarkBackendUnhealthyAfterOneConsecutiveFailureWhenThresholdIsOne(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	backend := loadbalancer.NewBackend(server.URL)
	backend.SetStatus(loadbalancer.Healthy)
	hc := New([]*loadbalancer.Backend{backend}, time.Second, 1)

	hc.CheckOnce(context.Background())

	if backend.Status() != loadbalancer.Unhealthy {
		t.Errorf("expected backend to become Unhealthy, got status %v", backend.Status())
	}
}

func TestCheckOnceShouldMarkBackendUnhealthyWhenConnectionRefused(t *testing.T) {
	backend := loadbalancer.NewBackend("http://127.0.0.1:1") // nothing listens here
	hc := New([]*loadbalancer.Backend{backend}, time.Second, 1)

	hc.CheckOnce(context.Background())

	if backend.Status() != loadbalancer.Unhealthy {
		t.Errorf("expected backend to become Unhealthy on connection error, got status %v", backend.Status())
	}
}
