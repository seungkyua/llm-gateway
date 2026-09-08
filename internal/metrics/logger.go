package metrics

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// Log format names as used in config.yaml logging.format.
const (
	FormatJSON = "json"
	FormatText = "text"
)

// LogEntry is exactly what gets written per request. It deliberately has
// no field for API key plaintext, Authorization header value, or
// prompt/response bodies (spec.md 로그의 민감정보).
type LogEntry struct {
	Timestamp        string    `json:"ts"`
	RequestID        string    `json:"request_id"`
	KeyID            string    `json:"key_id"`
	Model            string    `json:"model"`
	Backend          string    `json:"backend"`
	Stream           bool      `json:"stream"`
	TTFTMillis       float64   `json:"ttft_ms"`
	TPOTMillis       float64   `json:"tpot_ms"`
	ITLMillis        []float64 `json:"itl_ms"`
	PromptTokens     int       `json:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens"`
	Status           int       `json:"status"`
}

// Logger writes one LogEntry per request, either as structured JSON
// (default) or a single human-readable text line.
type Logger struct {
	mu     sync.Mutex
	format string
	out    io.Writer
}

// NewLogger builds a Logger. format is FormatJSON or FormatText; any other
// value falls back to FormatJSON.
func NewLogger(format string, out io.Writer) *Logger {
	return &Logger{format: format, out: out}
}

// Log writes entry as one line to the configured output.
func (l *Logger) Log(entry LogEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.format == FormatText {
		fmt.Fprintf(l.out, "ts=%s request_id=%s key_id=%s model=%s backend=%s stream=%t ttft_ms=%.2f tpot_ms=%.2f itl_ms=%v prompt_tokens=%d completion_tokens=%d status=%d\n",
			entry.Timestamp, entry.RequestID, entry.KeyID, entry.Model, entry.Backend, entry.Stream,
			entry.TTFTMillis, entry.TPOTMillis, entry.ITLMillis, entry.PromptTokens, entry.CompletionTokens, entry.Status)
		return
	}

	raw, err := json.Marshal(entry)
	if err != nil {
		// Marshaling a plain struct of primitives/slices cannot realistically
		// fail; if it ever does, fall back to a minimal error line rather
		// than losing the request entirely.
		fmt.Fprintf(l.out, `{"error":"failed to marshal log entry: %s"}`+"\n", err)
		return
	}
	l.out.Write(append(raw, '\n'))
}
