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

func TestExtractRequestTextShouldJoinMessageContentWhenChatRequest(t *testing.T) {
	body := []byte(`{"model":"m","messages":[{"role":"user","content":"one two three"}]}`)

	text := ExtractRequestText(body)

	if text != "one two three" {
		t.Errorf("expected extracted text %q, got %q", "one two three", text)
	}
}

func TestExtractRequestTextShouldIgnoreJSONFieldNamesAndPunctuationWhenChatRequest(t *testing.T) {
	plain := ExtractRequestText([]byte(`{"model":"m","messages":[{"role":"user","content":"one two three four five"}]}`))
	heavy := ExtractRequestText([]byte(`{"model":"m","messages":[{"role":"user","content":"one two three four five"}],"metadata":{"a":"aaaaaaaaaa","b":"bbbbbbbbbb"}}`))

	if CountApprox(plain) != 5 {
		t.Errorf("expected 5 tokens from message content, got %d (%q)", CountApprox(plain), plain)
	}
	if CountApprox(plain) != CountApprox(heavy) {
		t.Errorf("expected JSON overhead to not affect extracted text token count, got plain=%d heavy=%d", CountApprox(plain), CountApprox(heavy))
	}
}

func TestExtractRequestTextShouldReturnPromptWhenCompletionsRequest(t *testing.T) {
	body := []byte(`{"model":"m","prompt":"complete this sentence"}`)

	text := ExtractRequestText(body)

	if text != "complete this sentence" {
		t.Errorf("expected extracted text %q, got %q", "complete this sentence", text)
	}
}

func TestExtractRequestTextShouldFallBackToRawBodyWhenNeitherFieldPresent(t *testing.T) {
	body := []byte(`{"model":"m"}`)

	text := ExtractRequestText(body)

	if text != string(body) {
		t.Errorf("expected fallback to raw body %q, got %q", body, text)
	}
}

func TestExtractRequestTextShouldFallBackToRawBodyWhenInvalidJSON(t *testing.T) {
	body := []byte("not json")

	text := ExtractRequestText(body)

	if text != string(body) {
		t.Errorf("expected fallback to raw body %q, got %q", body, text)
	}
}

func TestExtractResponseTextShouldUseChoiceMessageContentWhenChatResponse(t *testing.T) {
	body := []byte(`{"id":"x","choices":[{"message":{"content":"hello there"}}]}`)

	text := ExtractResponseText(body)

	if text != "hello there" {
		t.Errorf("expected extracted text %q, got %q", "hello there", text)
	}
}

func TestExtractResponseTextShouldUseChoiceTextWhenCompletionsResponse(t *testing.T) {
	body := []byte(`{"id":"x","choices":[{"text":"a plain completion"}]}`)

	text := ExtractResponseText(body)

	if text != "a plain completion" {
		t.Errorf("expected extracted text %q, got %q", "a plain completion", text)
	}
}

func TestExtractResponseTextShouldFallBackToRawBodyWhenNoChoices(t *testing.T) {
	body := []byte(`{"id":"x"}`)

	text := ExtractResponseText(body)

	if text != string(body) {
		t.Errorf("expected fallback to raw body %q, got %q", body, text)
	}
}
