package main

import (
	"fmt"
	"log"
	"net/http"

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

	svc := service.NewAgentService(cfg, dc)
	h := handler.NewAgentHandler(svc)

	r := gin.Default()
	r.GET("/health", handler.Health)
	api := r.Group("/api/v1")
	api.Use(handler.AuthMiddleware(cfg.APIKey))
	h.Register(api)

	addr := fmt.Sprintf(":%d", cfg.Port)
	log.Printf("agent-deployer listening on %s (auth: %v)", addr, cfg.APIKey != "")
	if err := http.ListenAndServe(addr, r); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
