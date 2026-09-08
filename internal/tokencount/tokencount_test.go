package tokencount

import "testing"

func TestFromUsageFieldShouldExtractPromptAndCompletionTokensWhenPresent(t *testing.T) {
	body := []byte(`{"id":"x","usage":{"prompt_tokens":42,"completion_tokens":256,"total_tokens":298}}`)

	result, ok := FromUsageField(body)

	if !ok {
		t.Fatal("expected FromUsageField to succeed")
	}
	if result.PromptTokens != 42 {
		t.Errorf("expected prompt_tokens 42, got %d", result.PromptTokens)
	}
	if result.CompletionTokens != 256 {
		t.Errorf("expected completion_tokens 256, got %d", result.CompletionTokens)
	}
}

func TestFromUsageFieldShouldReturnFalseWhenBodyIsInvalidJSON(t *testing.T) {
	_, ok := FromUsageField([]byte("not json"))

	if ok {
		t.Error("expected FromUsageField to fail on invalid JSON")
	}
}

func TestCountApproxShouldCountWhitespaceSeparatedTokens(t *testing.T) {
	count := CountApprox("hello world this is a test")

	if count != 6 {
		t.Errorf("expected 6 tokens, got %d", count)
	}
}

func TestCountApproxShouldReturnZeroForEmptyText(t *testing.T) {
	count := CountApprox("")

	if count != 0 {
		t.Errorf("expected 0 tokens for empty text, got %d", count)
	}
}
