// Package metrics computes per-request performance metrics (TTFT/TPOT/ITL)
// and writes structured/text request logs (never including API key
// plaintext, Authorization headers, or prompt/response bodies).
package metrics

import "time"

// Timing captures the timestamps needed to compute TTFT/TPOT/ITL for one
// request. For non-streaming requests ChunkArrivals is left empty and TTFT
// is treated as the whole request duration (spec.md Assumptions/마일스톤 9).
type Timing struct {
	Start         time.Time
	ChunkArrivals []time.Time
	End           time.Time
}

func millis(d time.Duration) float64 {
	return float64(d.Microseconds()) / 1000.0
}

// TTFTMillis returns time-to-first-token in milliseconds.
func (t Timing) TTFTMillis() float64 {
	if len(t.ChunkArrivals) > 0 {
		return millis(t.ChunkArrivals[0].Sub(t.Start))
	}
	return millis(t.End.Sub(t.Start))
}

// ITLMillis returns the inter-token-latency (gap between consecutive
// streamed chunks) in milliseconds. Empty when fewer than 2 chunks arrived.
func (t Timing) ITLMillis() []float64 {
	if len(t.ChunkArrivals) < 2 {
		return []float64{}
	}
	itl := make([]float64, 0, len(t.ChunkArrivals)-1)
	for i := 1; i < len(t.ChunkArrivals); i++ {
		itl = append(itl, millis(t.ChunkArrivals[i].Sub(t.ChunkArrivals[i-1])))
	}
	return itl
}

// TPOTMillis returns the average inter-token latency (time-per-output-token)
// in milliseconds. Zero when there is no ITL data (e.g. non-streaming).
func (t Timing) TPOTMillis() float64 {
	itl := t.ITLMillis()
	if len(itl) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range itl {
		sum += v
	}
	return sum / float64(len(itl))
}
