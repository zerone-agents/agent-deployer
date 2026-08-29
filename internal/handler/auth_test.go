package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func setupAuthRouter(apiKey, hubAPIKey string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	g := r.Group("/api/v1")
	g.Use(AuthMiddleware(apiKey, hubAPIKey))
	g.GET("/ping", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	g.GET("/scope", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"hub": c.GetBool(ContextKeyHubScope)}) })
	return r
}

func doAuthRequest(r *gin.Engine, authHeader, xAPIKey string) *httptest.ResponseRecorder {
	req, _ := http.NewRequest("GET", "/api/v1/ping", nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	if xAPIKey != "" {
		req.Header.Set("X-API-Key", xAPIKey)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestAuthMiddleware_DisabledWhenNoKey(t *testing.T) {
	r := setupAuthRouter("", "")
	w := doAuthRequest(r, "", "")
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestAuthMiddleware_BearerToken(t *testing.T) {
	r := setupAuthRouter("secret-key", "")
	w := doAuthRequest(r, "Bearer secret-key", "")
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestAuthMiddleware_XAPIKey(t *testing.T) {
	r := setupAuthRouter("secret-key", "")
	w := doAuthRequest(r, "", "secret-key")
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestAuthMiddleware_MissingKey(t *testing.T) {
	r := setupAuthRouter("secret-key", "hub-key")
	w := doAuthRequest(r, "", "")
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAuthMiddleware_WrongKey(t *testing.T) {
	r := setupAuthRouter("secret-key", "hub-key")
	w := doAuthRequest(r, "Bearer wrong-key", "")
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAuthMiddleware_EmptyBearer(t *testing.T) {
	r := setupAuthRouter("secret-key", "")
	w := doAuthRequest(r, "Bearer ", "")
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// --- hub-scoped key (issue #11, PR #12 review round 2) ---

func doScopeRequest(r *gin.Engine, xAPIKey string) *httptest.ResponseRecorder {
	req, _ := http.NewRequest("GET", "/api/v1/scope", nil)
	if xAPIKey != "" {
		req.Header.Set("X-API-Key", xAPIKey)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestAuthMiddleware_HubKeyAuthenticatesAndSetsScope(t *testing.T) {
	r := setupAuthRouter("general-key", "hub-key")
	w := doScopeRequest(r, "hub-key")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"hub":true}`, w.Body.String(), "hub key must mark the request hub-scoped")
}

func TestAuthMiddleware_GeneralKeyAuthenticatesWithoutScope(t *testing.T) {
	r := setupAuthRouter("general-key", "hub-key")
	w := doScopeRequest(r, "general-key")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"hub":false}`, w.Body.String(), "general key must not be hub-scoped")
}

func TestAuthMiddleware_OnlyHubKeyConfigured(t *testing.T) {
	r := setupAuthRouter("", "hub-key")
	w := doScopeRequest(r, "hub-key")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"hub":true}`, w.Body.String())
}
