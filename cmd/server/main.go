package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/zerone-agent/agent-deployer/internal/config"
	"github.com/zerone-agent/agent-deployer/internal/docker"
	"github.com/zerone-agent/agent-deployer/internal/handler"
	"github.com/zerone-agent/agent-deployer/internal/service"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	dc, err := docker.NewClient()
	if err != nil {
		log.Fatalf("failed to create docker client: %v", err)
	}
	defer dc.Close()

	// Fail fast in docker-network mode when the shared network is missing:
	// agents created without a published port would be unreachable, so refuse
	// to enter that state (issue #11, acceptance #9).
	if cfg.RuntimeExpose == config.ExposeDockerNetwork {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		exists, err := dc.NetworkExists(ctx, cfg.RuntimeNetwork)
		if err != nil {
			log.Fatalf("failed to inspect runtime network %q: %v", cfg.RuntimeNetwork, err)
		}
		if !exists {
			log.Fatalf("AGENT_DEPLOYER_RUNTIME_NETWORK %q does not exist; refusing to start in docker-network mode", cfg.RuntimeNetwork)
		}
	}

	svc := service.NewAgentService(cfg, dc)
	h := handler.NewAgentHandler(svc)

	r := gin.Default()
	r.GET("/health", handler.Health)
	api := r.Group("/api/v1")
	api.Use(handler.AuthMiddleware(cfg.APIKey, cfg.HubAPIKey))
	h.Register(api)

	addr := fmt.Sprintf(":%d", cfg.Port)
	log.Printf("agent-deployer listening on %s (auth: %v, hub-scope: %v)", addr, cfg.APIKey != "" || cfg.HubAPIKey != "", cfg.HubAPIKey != "")
	if err := http.ListenAndServe(addr, r); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
