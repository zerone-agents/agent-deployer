package handler

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// ContextKeyHubScope marks requests authenticated with the hub-scoped API
// key. It is the only scope that is ever served the trusted upstream locator
// (issue #11).
const ContextKeyHubScope = "agent-deployer/hub-scope"

// AuthMiddleware returns a Gin middleware that validates the request's API
// key against the configured general key and hub-scoped key using
// constant-time comparison.
//
// Either key authenticates the request; the hub key additionally marks the
// request as hub-scoped via ContextKeyHubScope. Handlers strip the trusted
// upstream locator from responses served to any other caller.
//
// When both keys are empty the middleware is a no-op (auth disabled), which
// keeps local development frictionless. Config validation enforces that
// non-public expose modes set the hub key, so the no-op only ever happens in
// public mode, which emits no locator.
//
// Accepted headers (either one):
//   - Authorization: Bearer <key>
//   - X-API-Key: <key>
func AuthMiddleware(apiKey, hubAPIKey string) gin.HandlerFunc {
	if apiKey == "" && hubAPIKey == "" {
		return func(c *gin.Context) { c.Next() }
	}

	return func(c *gin.Context) {
		provided := extractAPIKey(c.GetHeader("Authorization"), c.GetHeader("X-API-Key"))
		if provided != "" && hubAPIKey != "" && subtle.ConstantTimeCompare([]byte(provided), []byte(hubAPIKey)) == 1 {
			c.Set(ContextKeyHubScope, true)
			c.Next()
			return
		}
		if provided != "" && apiKey != "" && subtle.ConstantTimeCompare([]byte(provided), []byte(apiKey)) == 1 {
			c.Next()
			return
		}
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"error":   "unauthorized",
		})
	}
}

// extractAPIKey pulls the token out of either the Bearer header or the
// X-API-Key header. Bearer takes precedence.
func extractAPIKey(authHeader, xAPIKey string) string {
	if authHeader != "" {
		if strings.HasPrefix(authHeader, "Bearer ") {
			return strings.TrimSpace(authHeader[7:])
		}
		return strings.TrimSpace(authHeader)
	}
	return strings.TrimSpace(xAPIKey)
}
