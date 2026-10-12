package metrics

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func sampleEntry() LogEntry {
	return LogEntry{
		Timestamp:        "2026-09-08T10:00:00Z",
		RequestID:        "req_abc123",
		KeyID:            "key_001",
		Model:            "qwen2.5-7b",
		Backend:          "http://vllm-1:8000",
		Stream:           true,
		TTFTMillis:       120,
		TPOTMillis:       18.4,
		ITLMillis:        []float64{15, 17, 19, 18},
		PromptTokens:     42,
		CompletionTokens: 256,
		Status:           200,
	}
}

func TestLoggerShouldWriteOneValidJSONLineWhenFormatIsJSON(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(FormatJSON, &buf)

	logger.Log(sampleEntry())

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected exactly 1 log line, got %d", len(lines))
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &parsed); err != nil {
		t.Fatalf("expected valid JSON line, got error: %v", err)
	}
	for _, field := range []string{"ttft_ms", "tpot_ms", "itl_ms", "prompt_tokens", "completion_tokens", "status", "request_id", "key_id"} {
		if _, ok := parsed[field]; !ok {
			t.Errorf("expected field %q in log JSON, got %v", field, parsed)
		}
	}
}

func TestLoggerShouldNotIncludeSensitiveFieldsInJSONOutput(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(FormatJSON, &buf)

	logger.Log(sampleEntry())

	out := buf.String()
	for _, forbidden := range []string{"key_hash", "Authorization", "authorization", "sk-"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("expected log output not to contain %q, got %s", forbidden, out)
		}
	}
}

func TestLoggerShouldWriteHumanReadableSingleLineWhenFormatIsText(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(FormatText, &buf)

	logger.Log(sampleEntry())

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected exactly 1 log line, got %d", len(lines))
	}
	if !strings.Contains(lines[0], "request_id=req_abc123") {
		t.Errorf("expected text log to contain request_id, got %s", lines[0])
	}
	if !strings.Contains(lines[0], "key_id=key_001") {
		t.Errorf("expected text log to contain key_id, got %s", lines[0])
	}
}

func TestLoggerShouldIncludeITLArrayWhenStreaming(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(FormatJSON, &buf)
	entry := sampleEntry()

	logger.Log(entry)

	var parsed map[string]any
	json.Unmarshal(buf.Bytes(), &parsed)
	itl, ok := parsed["itl_ms"].([]any)
	if !ok || len(itl) != 4 {
		t.Errorf("expected itl_ms array of length 4, got %v", parsed["itl_ms"])
	}
}
