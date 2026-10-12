// Package tokencount provides the two token-counting strategies selected
// by config token_counting.method: "usage_field" (trust vLLM's own usage
// object) and "tokenizer" (Gateway self-computed approximate count).
//
// The "tokenizer" strategy uses a whitespace-based approximate counter
// rather than an external BPE tokenizer library, per spec.md Assumptions
// ("공백/서브워드 근사 카운팅 중 Generator가 선택"). It has zero external
// dependencies and therefore needs no additional license entry.
package tokencount

import (
	"encoding/json"
	"strings"
)

// Method names as used in config.yaml token_counting.method.
const (
	MethodUsageField = "usage_field"
	MethodTokenizer  = "tokenizer"
)

// UsageFieldResult holds token counts extracted from a vLLM response body.
type UsageFieldResult struct {
	PromptTokens     int
	CompletionTokens int
}

type usageFieldBody struct {
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

// FromUsageField parses a vLLM (OpenAI-compatible) JSON response body and
// extracts usage.prompt_tokens / usage.completion_tokens. ok is false when
// body is not valid JSON.
func FromUsageField(body []byte) (UsageFieldResult, bool) {
	var parsed usageFieldBody
	if err := json.Unmarshal(body, &parsed); err != nil {
		return UsageFieldResult{}, false
	}
	return UsageFieldResult{
		PromptTokens:     parsed.Usage.PromptTokens,
		CompletionTokens: parsed.Usage.CompletionTokens,
	}, true
}

// CountApprox approximates a token count for text by counting
// whitespace-separated fields.
func CountApprox(text string) int {
	return len(strings.Fields(text))
}

// requestTextBody captures the fields of an OpenAI-compatible chat/
// completions or completions request body that actually contain prompt
// text, per spec.md 마일스톤 9: the "tokenizer" strategy must count only
// messages[].content (chat) / prompt (completions), not the surrounding
// JSON (field names, punctuation, unrelated fields like "model").
type requestTextBody struct {
	Messages []struct {
		Content string `json:"content"`
	} `json:"messages"`
	Prompt string `json:"prompt"`
}

// ExtractRequestText pulls the prompt text a tokenizer should count out of
// a chat/completions ("messages[].content") or completions ("prompt")
// request body. If the body doesn't parse as JSON, or parses but contains
// neither field (unexpected schema), it falls back to the raw body so
// unrecognized requests are still counted for something rather than
// silently producing 0 (spec.md Assumptions: "텍스트 추출에 실패하는 경우에만
// fallback으로 raw body를 사용해도 된다").
func ExtractRequestText(body []byte) string {
	var parsed requestTextBody
	if err := json.Unmarshal(body, &parsed); err != nil {
		return string(body)
	}
	var parts []string
	for _, m := range parsed.Messages {
		if m.Content != "" {
			parts = append(parts, m.Content)
		}
	}
	if parsed.Prompt != "" {
		parts = append(parts, parsed.Prompt)
	}
	if len(parts) == 0 {
		return string(body)
	}
	return strings.Join(parts, " ")
}

// responseTextBody captures the fields of an OpenAI-compatible chat/
// completions or completions response body that contain generated text:
// choices[].message.content (chat) or choices[].text (completions).
type responseTextBody struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		Text string `json:"text"`
	} `json:"choices"`
}

// ExtractResponseText pulls the generated completion text a tokenizer
// should count out of a chat/completions ("choices[].message.content") or
// completions ("choices[].text") response body, with the same raw-body
// fallback behavior as ExtractRequestText.
func ExtractResponseText(body []byte) string {
	var parsed responseTextBody
	if err := json.Unmarshal(body, &parsed); err != nil {
		return string(body)
	}
	var parts []string
	for _, c := range parsed.Choices {
		switch {
		case c.Message.Content != "":
			parts = append(parts, c.Message.Content)
		case c.Text != "":
			parts = append(parts, c.Text)
		}
	}
	if len(parts) == 0 {
		return string(body)
	}
	return strings.Join(parts, " ")
}
