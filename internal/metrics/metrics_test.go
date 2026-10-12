package metrics

import (
	"testing"
	"time"
)

func TestTTFTMillisShouldEqualFirstChunkArrivalMinusStartWhenStreamingChunksPresent(t *testing.T) {
	start := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	timing := Timing{
		Start:         start,
		ChunkArrivals: []time.Time{start.Add(120 * time.Millisecond), start.Add(140 * time.Millisecond)},
	}

	got := timing.TTFTMillis()

	if got != 120 {
		t.Errorf("expected 120ms TTFT, got %v", got)
	}
}

func TestTTFTMillisShouldEqualTotalDurationWhenNoStreamingChunks(t *testing.T) {
	start := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	timing := Timing{Start: start, End: start.Add(300 * time.Millisecond)}

	got := timing.TTFTMillis()

	if got != 300 {
		t.Errorf("expected 300ms TTFT for non-streaming, got %v", got)
	}
}

func TestITLMillisShouldReturnGapsBetweenConsecutiveChunks(t *testing.T) {
	start := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	timing := Timing{
		ChunkArrivals: []time.Time{
			start,
			start.Add(15 * time.Millisecond),
			start.Add(32 * time.Millisecond),
		},
	}

	got := timing.ITLMillis()

	if len(got) != 2 {
		t.Fatalf("expected 2 ITL entries, got %d", len(got))
	}
	if got[0] != 15 || got[1] != 17 {
		t.Errorf("expected [15,17], got %v", got)
	}
}

func TestTPOTMillisShouldReturnAverageOfITL(t *testing.T) {
	start := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	timing := Timing{
		ChunkArrivals: []time.Time{
			start,
			start.Add(10 * time.Millisecond),
			start.Add(30 * time.Millisecond),
		},
	}

	got := timing.TPOTMillis()

	if got != 15 {
		t.Errorf("expected TPOT 15ms (avg of 10,20), got %v", got)
	}
}
