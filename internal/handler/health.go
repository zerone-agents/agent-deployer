package handler

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// HealthResponse is the body returned by Health.
type HealthResponse struct {
	Status string `json:"status"`
	Time   string `json:"time"`
}

// Health is a liveness probe. It returns 200 / `{"status":"ok"}` as long as the
// deployer process itself is alive and able to serve requests. It deliberately
// does NOT check Docker connectivity: a deployer whose Docker daemon is briefly
// unavailable should still be observable by load balancers and orchestrators.
//
// Register this on a path OUTSIDE the AuthMiddleware group so probes can reach
// it without credentials.
func Health(c *gin.Context) {
	c.JSON(http.StatusOK, HealthResponse{
		Status: "ok",
		Time:   time.Now().UTC().Format(time.RFC3339),
	})
}
