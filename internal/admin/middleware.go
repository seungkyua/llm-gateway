package admin

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/seungkyua/llm-gateway/internal/apierror"
)

const bearerPrefix = "Bearer "

// AuthMiddleware requires "Authorization: Bearer <adminToken>" and compares
// it against adminToken in constant time to avoid timing attacks.
func AuthMiddleware(adminToken string) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if !strings.HasPrefix(header, bearerPrefix) {
			apierror.Abort(c, http.StatusUnauthorized, "missing or malformed admin token", apierror.TypeAuthenticationError, apierror.CodeInvalidAdminToken)
			return
		}
		provided := strings.TrimPrefix(header, bearerPrefix)
		if subtle.ConstantTimeCompare([]byte(provided), []byte(adminToken)) != 1 {
			apierror.Abort(c, http.StatusUnauthorized, "invalid admin token", apierror.TypeAuthenticationError, apierror.CodeInvalidAdminToken)
			return
		}
		c.Next()
	}
}
