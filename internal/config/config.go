package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

type Config struct {
	DataDir              string
	Port                 int
	RuntimeImage         string
	RuntimeContainerPort int
	APIKey               string
	ContainerMemoryBytes int64 // 0 = unlimited
	ContainerNanoCPUs    int64 // 0 = unlimited
	// RuntimeImageAssumeLatest permits deploying the agent graph protocol to a
	// :latest (or untagged) runtime image. Only set when :latest is known to
	// point at a v2.4.0+ build.
	RuntimeImageAssumeLatest bool
}

func Load() (*Config, error) {
	dataDir := os.Getenv("AGENT_DEPLOYER_DATA_DIR")
	if dataDir == "" {
		return nil, fmt.Errorf("AGENT_DEPLOYER_DATA_DIR is required")
	}
	if !filepath.IsAbs(dataDir) {
		return nil, fmt.Errorf("AGENT_DEPLOYER_DATA_DIR must be an absolute path")
	}

	port := 8080
	if v := os.Getenv("AGENT_DEPLOYER_PORT"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("invalid AGENT_DEPLOYER_PORT: %w", err)
		}
		if n < 1 || n > 65535 {
			return nil, fmt.Errorf("AGENT_DEPLOYER_PORT must be between 1 and 65535")
		}
		port = n
	}

	runtimeImage := os.Getenv("AGENT_DEPLOYER_RUNTIME_IMAGE")
	if runtimeImage == "" {
		runtimeImage = "open-agent-runtime:latest"
	}

	assumeLatest := false
	if v := os.Getenv("AGENT_DEPLOYER_RUNTIME_IMAGE_ASSUME_LATEST"); v == "true" || v == "1" {
		assumeLatest = true
	}

	containerPort := 3000
	if v := os.Getenv("AGENT_DEPLOYER_RUNTIME_CONTAINER_PORT"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("invalid AGENT_DEPLOYER_RUNTIME_CONTAINER_PORT: %w", err)
		}
		if n < 1 || n > 65535 {
			return nil, fmt.Errorf("AGENT_DEPLOYER_RUNTIME_CONTAINER_PORT must be between 1 and 65535")
		}
		containerPort = n
	}

	// Resource limits: default 2 cores + 2 GB memory per runtime container.
	containerCPUs := 2.0
	if v := os.Getenv("AGENT_DEPLOYER_CONTAINER_CPUS"); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid AGENT_DEPLOYER_CONTAINER_CPUS: %w", err)
		}
		if f < 0 {
			return nil, fmt.Errorf("AGENT_DEPLOYER_CONTAINER_CPUS must be >= 0")
		}
		containerCPUs = f
	}

	containerMemoryMB := int64(2048)
	if v := os.Getenv("AGENT_DEPLOYER_CONTAINER_MEMORY"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid AGENT_DEPLOYER_CONTAINER_MEMORY: %w", err)
		}
		if n < 0 {
			return nil, fmt.Errorf("AGENT_DEPLOYER_CONTAINER_MEMORY must be >= 0")
		}
		containerMemoryMB = n
	}

	return &Config{
		DataDir:                  dataDir,
		Port:                     port,
		RuntimeImage:             runtimeImage,
		RuntimeContainerPort:     containerPort,
		APIKey:                   os.Getenv("AGENT_DEPLOYER_API_KEY"),
		ContainerMemoryBytes:     containerMemoryMB * 1024 * 1024,
		ContainerNanoCPUs:        int64(containerCPUs * 1e9),
		RuntimeImageAssumeLatest: assumeLatest,
	}, nil
}
