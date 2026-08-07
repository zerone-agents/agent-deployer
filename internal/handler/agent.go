// Package handler exposes the agent-deployer business logic over a
// JSON-over-HTTP API using the Gin web framework. Handlers are thin: they map
// HTTP requests to service calls and translate the results into the standard
// SuccessResponse / ErrorResponse envelopes.
package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/zerone-agent/agent-deployer/internal/model"
	"github.com/zerone-agent/agent-deployer/internal/service"
	"github.com/zerone-agent/agent-deployer/internal/skills"
)

// AgentHandler wires agent lifecycle operations to HTTP endpoints.
type AgentHandler struct {
	svc *service.AgentService
}

// NewAgentHandler constructs an AgentHandler backed by the given service.
func NewAgentHandler(svc *service.AgentService) *AgentHandler {
	return &AgentHandler{svc: svc}
}

// Register attaches the agent routes to the provided router group under the
// "/agents" prefix.
func (h *AgentHandler) Register(r *gin.RouterGroup) {
	g := r.Group("/agents")
	g.POST("", h.Create)
	g.GET("", h.List)
	g.GET("/:name", h.Get)
	g.GET("/:name/status", h.Status)
	g.GET("/:name/logs", h.Logs)
	g.POST("/:name/stop", h.Stop)
	g.POST("/:name/restart", h.Restart)
	g.DELETE("/:name", h.Delete)
}

// Create handles POST /agents: validate the body, create the container, and
// return the resulting agent description.
//
// HTTP status:
//   - 201 Created when a new container was actually created (or rebuilt via Force)
//   - 200 OK when an existing container was returned unchanged (idempotent)
//   - 400 on validation error
//   - 422 when a declared skill hash is wrong / malicious
//   - 502 on skill download upstream failure
//   - 500 on internal failure
func (h *AgentHandler) Create(c *gin.Context) {
	var req model.CreateAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{Success: false, Error: err.Error()})
		return
	}

	resp, created, err := h.svc.Create(c.Request.Context(), &req)
	if err != nil {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, service.ErrInvalidRequest):
			status = http.StatusBadRequest
		case errors.Is(err, skills.ErrHashMismatch),
			errors.Is(err, skills.ErrZipSlip),
			errors.Is(err, skills.ErrSizeExceeded):
			// Client metadata error: the hash they declared corresponds to a
			// malicious or oversize zip, or doesn't match the actual download.
			status = http.StatusUnprocessableEntity
		case errors.Is(err, skills.ErrDownloadFailed):
			// Upstream failure: HTTP non-200 from skill URL, network error, timeout.
			status = http.StatusBadGateway
		}
		c.JSON(status, model.ErrorResponse{Success: false, Error: err.Error()})
		return
	}

	if created {
		c.JSON(http.StatusCreated, model.SuccessResponse{Success: true, Data: resp})
	} else {
		c.JSON(http.StatusOK, model.SuccessResponse{Success: true, Data: resp})
	}
}

// Get handles GET /agents/:name.
//
// Returns the agent if it has a container (status=running/stopped/exited) OR
// if on-disk data remains from a previous Delete without purge (status=archived).
// Only agents that are fully gone (no container and no data) return 404.
func (h *AgentHandler) Get(c *gin.Context) {
	name := c.Param("name")
	resp, err := h.svc.Get(c.Request.Context(), name)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, service.ErrAgentNotFound) {
			status = http.StatusNotFound
		}
		c.JSON(status, model.ErrorResponse{Success: false, Error: err.Error()})
		return
	}
	c.JSON(http.StatusOK, model.SuccessResponse{Success: true, Data: resp})
}

// Status handles GET /agents/:name/status. Returns real-time container status
// including the Docker health-check result. Clients poll this after Create.
func (h *AgentHandler) Status(c *gin.Context) {
	name := c.Param("name")
	resp, err := h.svc.GetStatus(c.Request.Context(), name)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, service.ErrAgentNotFound) {
			status = http.StatusNotFound
		}
		c.JSON(status, model.ErrorResponse{Success: false, Error: err.Error()})
		return
	}
	c.JSON(http.StatusOK, model.SuccessResponse{Success: true, Data: resp})
}

// Logs handles GET /agents/:name/logs?tail=100. Returns recent container
// output (stdout + stderr) for diagnosing startup failures.
func (h *AgentHandler) Logs(c *gin.Context) {
	name := c.Param("name")
	tail := 100
	if v := c.Query("tail"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			tail = n
		}
	}
	logs, err := h.svc.GetLogs(c.Request.Context(), name, tail)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, service.ErrAgentNotFound) {
			status = http.StatusNotFound
		}
		c.JSON(status, model.ErrorResponse{Success: false, Error: err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "logs": logs})
}

// List handles GET /agents.
//
// By default only agents with a live container are returned. Pass
// ?includeArchived=true to also include agents whose container has been
// deleted but whose data remains on disk (status="archived").
func (h *AgentHandler) List(c *gin.Context) {
	includeArchived := c.Query("includeArchived") == "true"
	resp, err := h.svc.List(c.Request.Context(), includeArchived)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.ErrorResponse{Success: false, Error: err.Error()})
		return
	}
	c.JSON(http.StatusOK, model.SuccessResponse{Success: true, Data: resp})
}

// Stop handles POST /agents/:name/stop.
func (h *AgentHandler) Stop(c *gin.Context) {
	name := c.Param("name")
	if err := h.svc.Stop(c.Request.Context(), name); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, service.ErrAgentNotFound) {
			status = http.StatusNotFound
		}
		c.JSON(status, model.ErrorResponse{Success: false, Error: err.Error()})
		return
	}
	c.JSON(http.StatusOK, model.SuccessResponse{Success: true})
}

// Restart handles POST /agents/:name/restart.
func (h *AgentHandler) Restart(c *gin.Context) {
	name := c.Param("name")
	if err := h.svc.Restart(c.Request.Context(), name); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, service.ErrAgentNotFound) {
			status = http.StatusNotFound
		}
		c.JSON(status, model.ErrorResponse{Success: false, Error: err.Error()})
		return
	}
	c.JSON(http.StatusOK, model.SuccessResponse{Success: true})
}

// Delete handles DELETE /agents/:name?purge=true.
//
// By default the container is removed and the agent becomes archived: its
// data remains on disk and can be discovered via ?includeArchived=true. Pass
// ?purge=true to also delete the on-disk data.
func (h *AgentHandler) Delete(c *gin.Context) {
	name := c.Param("name")
	purge := c.Query("purge") == "true"
	if err := h.svc.Delete(c.Request.Context(), name, purge); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, service.ErrAgentNotFound) {
			status = http.StatusNotFound
		}
		c.JSON(status, model.ErrorResponse{Success: false, Error: err.Error()})
		return
	}
	c.JSON(http.StatusOK, model.SuccessResponse{Success: true})
}
