package loadbalancer

import "testing"

func TestPickShouldAlwaysReturnTheSameBackendWhenOnlyOneHealthyBackendExists(t *testing.T) {
	b := NewBackend("http://vllm-1:8000")
	b.SetStatus(Healthy)
	lb := New([]*Backend{b}, &RoundRobinHealthcheck{})

	for i := 0; i < 3; i++ {
		picked, err := lb.Pick()
		if err != nil {
			t.Fatalf("Pick failed: %v", err)
		}
		if picked.URL != "http://vllm-1:8000" {
			t.Errorf("expected the single backend, got %q", picked.URL)
		}
	}
}

func TestPickShouldCycleThroughHealthyBackendsWhenRoundRobinStrategyUsed(t *testing.T) {
	b1 := NewBackend("http://vllm-1:8000")
	b2 := NewBackend("http://vllm-2:8000")
	b1.SetStatus(Healthy)
	b2.SetStatus(Healthy)
	lb := New([]*Backend{b1, b2}, &RoundRobinHealthcheck{})

	counts := map[string]int{}
	for i := 0; i < 4; i++ {
		picked, err := lb.Pick()
		if err != nil {
			t.Fatalf("Pick failed: %v", err)
		}
		counts[picked.URL]++
	}

	if counts["http://vllm-1:8000"] != 2 || counts["http://vllm-2:8000"] != 2 {
		t.Errorf("expected even round-robin distribution, got %v", counts)
	}
}

func TestPickShouldExcludeUnhealthyBackendWhenRoundRobinStrategyUsed(t *testing.T) {
	b1 := NewBackend("http://vllm-1:8000")
	b2 := NewBackend("http://vllm-2:8000")
	b1.SetStatus(Healthy)
	b2.SetStatus(Unhealthy)
	lb := New([]*Backend{b1, b2}, &RoundRobinHealthcheck{})

	for i := 0; i < 3; i++ {
		picked, err := lb.Pick()
		if err != nil {
			t.Fatalf("Pick failed: %v", err)
		}
		if picked.URL != "http://vllm-1:8000" {
			t.Errorf("expected unhealthy backend to be excluded, got %q", picked.URL)
		}
	}
}

func TestPickShouldReturnErrorWhenAllBackendsUnhealthy(t *testing.T) {
	b1 := NewBackend("http://vllm-1:8000")
	b2 := NewBackend("http://vllm-2:8000")
	b1.SetStatus(Unhealthy)
	b2.SetStatus(Unhealthy)
	lb := New([]*Backend{b1, b2}, &RoundRobinHealthcheck{})

	_, err := lb.Pick()

	if err != ErrNoAvailableBackend {
		t.Errorf("expected ErrNoAvailableBackend, got %v", err)
	}
}

func TestPickShouldExcludeUnknownStatusBackendWhenColdStart(t *testing.T) {
	b1 := NewBackend("http://vllm-1:8000") // defaults to Unknown

	lb := New([]*Backend{b1}, &RoundRobinHealthcheck{})

	_, err := lb.Pick()

	if err != ErrNoAvailableBackend {
		t.Errorf("expected ErrNoAvailableBackend for cold-start Unknown backend, got %v", err)
	}
}

func TestLeastRequestsShouldPickBackendWithFewestInFlightRequests(t *testing.T) {
	b1 := NewBackend("http://vllm-1:8000")
	b2 := NewBackend("http://vllm-2:8000")
	b1.SetStatus(Healthy)
	b2.SetStatus(Healthy)
	b1.IncInFlight()
	b1.IncInFlight()
	b2.IncInFlight()
	lb := New([]*Backend{b1, b2}, &LeastRequests{})

	picked, err := lb.Pick()

	if err != nil {
		t.Fatalf("Pick failed: %v", err)
	}
	if picked.URL != "http://vllm-2:8000" {
		t.Errorf("expected backend with fewer in-flight requests, got %q", picked.URL)
	}
}
