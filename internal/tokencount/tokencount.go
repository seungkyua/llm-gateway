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
