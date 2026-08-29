package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	DataDir              string
	Port                 int
	RuntimeImage         string
	RuntimeContainerPort int
	APIKey               string
	ContainerMemoryBytes int64         // 0 = unlimited
	ContainerNanoCPUs    int64         // 0 = unlimited
	RuntimeExpose        RuntimeExpose // default public
	RuntimeBindIP        string        // private mode: RFC1918 IPv4 the runtime ports bind to
	RuntimeNetwork       string        // docker-network mode: shared Docker network name
	UpstreamHost         string        // loopback/private mode: locator host override (private IPv4 literal or host.docker.internal); "" = derive
	UpstreamProbe        bool          // dial the locator in GET status (non-public modes only)
}

// RuntimeExpose selects how runtime container ports are exposed and how the
// trusted upstream locator is derived (issue #11). It is chosen server-side
// only — never inferred from client input.
type RuntimeExpose string

const (
	// ExposePublic publishes dynamic host ports on 0.0.0.0 (legacy behavior,
	// default). No upstream locator is emitted in responses.
	ExposePublic RuntimeExpose = "public"
	// ExposeLoopback publishes dynamic host ports on 127.0.0.1 only.
	ExposeLoopback RuntimeExpose = "loopback"
	// ExposeDockerNetwork publishes no host ports; runtimes are reached via
	// container DNS on a shared Docker network.
	ExposeDockerNetwork RuntimeExpose = "docker-network"
	// ExposePrivate publishes dynamic host ports on a configured private IP.
	ExposePrivate RuntimeExpose = "private"
)

// UpstreamHostDockerInternal is the one non-literal host accepted as an
// AGENT_DEPLOYER_UPSTREAM_HOST override: the Docker Desktop host gateway used
// when the hub runs in a container on the same daemon. Every other override
// must be an IPv4 private literal — arbitrary hostnames cannot be verified to
// resolve inside the private network (issue #11).
const UpstreamHostDockerInternal = "host.docker.internal"

// isPrivateIPv4 reports whether s is an IPv4 literal in an RFC1918 private
// range (10/8, 172.16/12, 192.168/16). Everything else — public, loopback,
// wildcard, IPv6, hostnames — is rejected: locator-bearing topologies must
// never be able to point at a public or unverified host (issue #11).
func isPrivateIPv4(s string) bool {
	ip := net.ParseIP(s)
	if ip == nil || ip.To4() == nil {
		return false
	}
	return !ip.IsLoopback() && !ip.IsUnspecified() && ip.IsPrivate()
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

	// Runtime expose topology (issue #11). Default public preserves the legacy
	// behavior exactly: 0.0.0.0 dynamic ports and no upstream locator.
	expose := ExposePublic
	if v := os.Getenv("AGENT_DEPLOYER_RUNTIME_EXPOSE"); v != "" {
		switch RuntimeExpose(v) {
		case ExposePublic, ExposeLoopback, ExposeDockerNetwork, ExposePrivate:
			expose = RuntimeExpose(v)
		default:
			return nil, fmt.Errorf("invalid AGENT_DEPLOYER_RUNTIME_EXPOSE %q: must be one of public, loopback, docker-network, private", v)
		}
	}

	bindIP := os.Getenv("AGENT_DEPLOYER_RUNTIME_BIND_IP")
	switch expose {
	case ExposePrivate:
		if bindIP == "" {
			return nil, fmt.Errorf("AGENT_DEPLOYER_RUNTIME_BIND_IP is required when AGENT_DEPLOYER_RUNTIME_EXPOSE=private")
		}
		if !isPrivateIPv4(bindIP) {
			return nil, fmt.Errorf("AGENT_DEPLOYER_RUNTIME_BIND_IP must be an IPv4 private address (RFC1918: 10/8, 172.16/12, 192.168/16), got %q: public, loopback, wildcard and IPv6 addresses are rejected so the locator can never point at a public host (issue #11)", bindIP)
		}
	default:
		if bindIP != "" {
			return nil, fmt.Errorf("AGENT_DEPLOYER_RUNTIME_BIND_IP is only valid with AGENT_DEPLOYER_RUNTIME_EXPOSE=private")
		}
	}

	runtimeNetwork := strings.TrimSpace(os.Getenv("AGENT_DEPLOYER_RUNTIME_NETWORK"))
	if expose == ExposeDockerNetwork {
		if runtimeNetwork == "" {
			return nil, fmt.Errorf("AGENT_DEPLOYER_RUNTIME_NETWORK is required when AGENT_DEPLOYER_RUNTIME_EXPOSE=docker-network")
		}
	} else if runtimeNetwork != "" {
		return nil, fmt.Errorf("AGENT_DEPLOYER_RUNTIME_NETWORK is only valid with AGENT_DEPLOYER_RUNTIME_EXPOSE=docker-network")
	}

	upstreamHost := strings.TrimSpace(os.Getenv("AGENT_DEPLOYER_UPSTREAM_HOST"))
	if upstreamHost != "" {
		if expose != ExposeLoopback && expose != ExposePrivate {
			return nil, fmt.Errorf("AGENT_DEPLOYER_UPSTREAM_HOST is only valid with loopback or private expose modes")
		}
		if upstreamHost != UpstreamHostDockerInternal && !isPrivateIPv4(upstreamHost) {
			return nil, fmt.Errorf("AGENT_DEPLOYER_UPSTREAM_HOST must be an IPv4 private address or %q, got %q: arbitrary hostnames cannot be verified to resolve inside the private network (issue #11)", UpstreamHostDockerInternal, upstreamHost)
		}
	}

	upstreamProbe := os.Getenv("AGENT_DEPLOYER_UPSTREAM_PROBE") == "true"
	if upstreamProbe && expose == ExposePublic {
		return nil, fmt.Errorf("AGENT_DEPLOYER_UPSTREAM_PROBE=true requires a non-public AGENT_DEPLOYER_RUNTIME_EXPOSE (no locator to probe in public mode)")
	}

	// Locator-bearing topologies require an authenticated boundary: with no
	// API key the auth middleware is a no-op, and every caller would see
	// container DNS / private addresses in responses (issue #11, trust
	// boundary). Public mode keeps the frictionless dev default.
	apiKey := os.Getenv("AGENT_DEPLOYER_API_KEY")
	if expose != ExposePublic && apiKey == "" {
		return nil, fmt.Errorf("AGENT_DEPLOYER_API_KEY must be set when AGENT_DEPLOYER_RUNTIME_EXPOSE is not public: non-public topologies emit the trusted upstream locator, which must only be served to authenticated callers (issue #11)")
	}

	return &Config{
		DataDir:              dataDir,
		Port:                 port,
		RuntimeImage:         runtimeImage,
		RuntimeContainerPort: containerPort,
		APIKey:               apiKey,
		ContainerMemoryBytes: containerMemoryMB * 1024 * 1024,
		ContainerNanoCPUs:    int64(containerCPUs * 1e9),
		RuntimeExpose:        expose,
		RuntimeBindIP:        bindIP,
		RuntimeNetwork:       runtimeNetwork,
		UpstreamHost:         upstreamHost,
		UpstreamProbe:        upstreamProbe,
	}, nil
}
