package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/zerone-agent/agent-deployer/internal/handler"
)

// TestHealth_Returns200 verifies that the liveness probe responds with 200 and
// the expected minimal body, WITHOUT requiring any authentication.
func TestHealth_Returns200(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/health", handler.Health)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp handler.HealthResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, w.Body.String())
	}
	if resp.Status != "ok" {
		t.Errorf("status field = %q, want %q", resp.Status, "ok")
	}
	if resp.Time == "" {
		t.Errorf("time field is empty; want RFC3339 timestamp")
	}
}
