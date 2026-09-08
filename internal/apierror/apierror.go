// Package apierror builds OpenAI-compatible error response bodies:
// {"error": {"message": "...", "type": "...", "code": "..."}}
package apierror

import "github.com/gin-gonic/gin"

// Error types (spec.md 에러 응답 매트릭스).
const (
	TypeAuthenticationError = "authentication_error"
	TypeRateLimitError      = "rate_limit_error"
	TypeInvalidRequestError = "invalid_request_error"
	TypeAPIError            = "api_error"
)

// Error codes (spec.md 에러 응답 매트릭스).
const (
	CodeInvalidAPIKey      = "invalid_api_key"
	CodeRateLimitExceeded  = "rate_limit_exceeded"
	CodeMaxTokensExceeded  = "max_tokens_exceeded"
	CodeQuotaExceeded      = "quota_exceeded"
	CodeNoAvailableBackend = "no_available_backend"
	CodeInvalidAdminToken  = "invalid_admin_token"
	CodeKeyNotFound        = "key_not_found"
	CodeBadGateway         = "bad_gateway"
	CodeInvalidRequest     = "invalid_request_error"
)

// Detail is the inner "error" object.
type Detail struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}

// Body is the full OpenAI-compatible error response.
type Body struct {
	Error Detail `json:"error"`
}

// New builds an error Body.
func New(message, errType, code string) Body {
	return Body{Error: Detail{Message: message, Type: errType, Code: code}}
}

// Abort writes status and the OpenAI-compatible error body to c and stops
// the middleware chain.
func Abort(c *gin.Context, status int, message, errType, code string) {
	c.AbortWithStatusJSON(status, New(message, errType, code))
}
