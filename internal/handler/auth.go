package handler

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// AuthMiddleware returns a Gin middleware that validates the request's API key
// against the configured key using constant-time comparison.
//
// When apiKey is empty the middleware is a no-op (auth disabled), which keeps
// local development frictionless. In production always set a non-empty key.
//
// Accepted headers (either one):
//   - Authorization: Bearer <key>
//   - X-API-Key: <key>
func AuthMiddleware(apiKey string) gin.HandlerFunc {
	if apiKey == "" {
		return func(c *gin.Context) { c.Next() }
	}

	return func(c *gin.Context) {
		provided := extractAPIKey(c.GetHeader("Authorization"), c.GetHeader("X-API-Key"))
		if provided == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(apiKey)) != 1 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"error":   "unauthorized",
			})
			return
		}
		c.Next()
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
