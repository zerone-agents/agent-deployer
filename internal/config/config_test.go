package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad_RequiredDataDir(t *testing.T) {
	t.Setenv("AGENT_DEPLOYER_DATA_DIR", "")
	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AGENT_DEPLOYER_DATA_DIR")
}

func TestLoad_Defaults(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Setenv("AGENT_DEPLOYER_DATA_DIR", `C:\data`)
	} else {
		t.Setenv("AGENT_DEPLOYER_DATA_DIR", "/data")
	}
	cfg, err := Load()
	require.NoError(t, err)
	if os.PathSeparator == '\\' {
		assert.Equal(t, `C:\data`, cfg.DataDir)
	} else {
		assert.Equal(t, "/data", cfg.DataDir)
	}
	assert.Equal(t, 8080, cfg.Port)
	assert.Equal(t, "open-agent-runtime:latest", cfg.RuntimeImage)
	assert.Equal(t, 3000, cfg.RuntimeContainerPort)
}

func TestLoad_Overrides(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Setenv("AGENT_DEPLOYER_DATA_DIR", `C:\data`)
	} else {
		t.Setenv("AGENT_DEPLOYER_DATA_DIR", "/data")
	}
	t.Setenv("AGENT_DEPLOYER_PORT", "9090")
	t.Setenv("AGENT_DEPLOYER_RUNTIME_IMAGE", "runtime:1")
	t.Setenv("AGENT_DEPLOYER_RUNTIME_CONTAINER_PORT", "4000")
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 9090, cfg.Port)
	assert.Equal(t, "runtime:1", cfg.RuntimeImage)
	assert.Equal(t, 4000, cfg.RuntimeContainerPort)
}

func TestLoad_PortValidation(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Setenv("AGENT_DEPLOYER_DATA_DIR", `C:\data`)
	} else {
		t.Setenv("AGENT_DEPLOYER_DATA_DIR", "/data")
	}

	t.Setenv("AGENT_DEPLOYER_PORT", "abc")
	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid AGENT_DEPLOYER_PORT")

	t.Setenv("AGENT_DEPLOYER_PORT", "0")
	_, err = Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AGENT_DEPLOYER_PORT must be between 1 and 65535")

	t.Setenv("AGENT_DEPLOYER_PORT", "65536")
	_, err = Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AGENT_DEPLOYER_PORT must be between 1 and 65535")
}

func TestLoad_RuntimeContainerPortValidation(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Setenv("AGENT_DEPLOYER_DATA_DIR", `C:\data`)
	} else {
		t.Setenv("AGENT_DEPLOYER_DATA_DIR", "/data")
	}

	t.Setenv("AGENT_DEPLOYER_RUNTIME_CONTAINER_PORT", "abc")
	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid AGENT_DEPLOYER_RUNTIME_CONTAINER_PORT")

	t.Setenv("AGENT_DEPLOYER_RUNTIME_CONTAINER_PORT", "0")
	_, err = Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AGENT_DEPLOYER_RUNTIME_CONTAINER_PORT must be between 1 and 65535")

	t.Setenv("AGENT_DEPLOYER_RUNTIME_CONTAINER_PORT", "70000")
	_, err = Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AGENT_DEPLOYER_RUNTIME_CONTAINER_PORT must be between 1 and 65535")
}

func TestLoad_DataDirAbsolute(t *testing.T) {
	t.Setenv("AGENT_DEPLOYER_DATA_DIR", "relative/path")
	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be an absolute path")
}

// --- issue #11: runtime expose topology ---

func TestLoad_ExposeDefaultsToPublic(t *testing.T) {
	t.Setenv("AGENT_DEPLOYER_DATA_DIR", t.TempDir())
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, ExposePublic, cfg.RuntimeExpose)
}

func TestLoad_InvalidExposeValue(t *testing.T) {
	t.Setenv("AGENT_DEPLOYER_DATA_DIR", t.TempDir())
	t.Setenv("AGENT_DEPLOYER_RUNTIME_EXPOSE", "carrier-pigeon")
	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AGENT_DEPLOYER_RUNTIME_EXPOSE")
}

func TestLoad_LoopbackMode(t *testing.T) {
	t.Setenv("AGENT_DEPLOYER_DATA_DIR", t.TempDir())
	t.Setenv("AGENT_DEPLOYER_API_KEY", "test-key")
	t.Setenv("AGENT_DEPLOYER_RUNTIME_EXPOSE", "loopback")
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, ExposeLoopback, cfg.RuntimeExpose)
}

func TestLoad_LoopbackModeWithHostOverride(t *testing.T) {
	t.Setenv("AGENT_DEPLOYER_DATA_DIR", t.TempDir())
	t.Setenv("AGENT_DEPLOYER_API_KEY", "test-key")
	t.Setenv("AGENT_DEPLOYER_RUNTIME_EXPOSE", "loopback")
	t.Setenv("AGENT_DEPLOYER_UPSTREAM_HOST", "host.docker.internal")
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "host.docker.internal", cfg.UpstreamHost)
}

func TestLoad_LoopbackRejectsBindIP(t *testing.T) {
	t.Setenv("AGENT_DEPLOYER_DATA_DIR", t.TempDir())
	t.Setenv("AGENT_DEPLOYER_RUNTIME_EXPOSE", "loopback")
	t.Setenv("AGENT_DEPLOYER_RUNTIME_BIND_IP", "10.0.0.1")
	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AGENT_DEPLOYER_RUNTIME_BIND_IP")
}

func TestLoad_PrivateModeRequiresBindIP(t *testing.T) {
	t.Setenv("AGENT_DEPLOYER_DATA_DIR", t.TempDir())
	t.Setenv("AGENT_DEPLOYER_RUNTIME_EXPOSE", "private")
	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AGENT_DEPLOYER_RUNTIME_BIND_IP")
}

func TestLoad_PrivateModeRejectsNonPrivateAddresses(t *testing.T) {
	// Public, loopback, wildcard, IPv6 and garbage values must all be
	// rejected: the private-topology locator must never be able to point
	// at a public host (issue #11, PR #12 review P1-2).
	for _, ip := range []string{"47.116.185.214", "8.8.8.8", "127.0.0.1", "0.0.0.0", "fd00::1", "not-an-ip"} {
		t.Run(ip, func(t *testing.T) {
			t.Setenv("AGENT_DEPLOYER_DATA_DIR", t.TempDir())
			t.Setenv("AGENT_DEPLOYER_RUNTIME_EXPOSE", "private")
			t.Setenv("AGENT_DEPLOYER_RUNTIME_BIND_IP", ip)
			_, err := Load()
			require.Error(t, err, "IP %q must be rejected", ip)
		})
	}
}

func TestLoad_PrivateModeAcceptsPrivateIPv4(t *testing.T) {
	for _, ip := range []string{"10.2.0.5", "172.16.0.5", "192.168.1.5"} {
		t.Run(ip, func(t *testing.T) {
			t.Setenv("AGENT_DEPLOYER_DATA_DIR", t.TempDir())
			t.Setenv("AGENT_DEPLOYER_API_KEY", "test-key")
			t.Setenv("AGENT_DEPLOYER_RUNTIME_EXPOSE", "private")
			t.Setenv("AGENT_DEPLOYER_RUNTIME_BIND_IP", ip)
			cfg, err := Load()
			require.NoError(t, err)
			assert.Equal(t, ip, cfg.RuntimeBindIP)
		})
	}
}

func TestLoad_DockerNetworkRequiresNetwork(t *testing.T) {
	t.Setenv("AGENT_DEPLOYER_DATA_DIR", t.TempDir())
	t.Setenv("AGENT_DEPLOYER_RUNTIME_EXPOSE", "docker-network")
	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AGENT_DEPLOYER_RUNTIME_NETWORK")
}

func TestLoad_DockerNetworkMode(t *testing.T) {
	t.Setenv("AGENT_DEPLOYER_DATA_DIR", t.TempDir())
	t.Setenv("AGENT_DEPLOYER_API_KEY", "test-key")
	t.Setenv("AGENT_DEPLOYER_HUB_LOCATOR_CAPABILITY", "locator-v1")
	t.Setenv("AGENT_DEPLOYER_RUNTIME_EXPOSE", "docker-network")
	t.Setenv("AGENT_DEPLOYER_RUNTIME_NETWORK", "hubnet")
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, ExposeDockerNetwork, cfg.RuntimeExpose)
	assert.Equal(t, "hubnet", cfg.RuntimeNetwork)
}

func TestLoad_BindIPAndNetworkOnlyValidInTheirModes(t *testing.T) {
	t.Setenv("AGENT_DEPLOYER_DATA_DIR", t.TempDir())
	t.Setenv("AGENT_DEPLOYER_RUNTIME_BIND_IP", "10.0.0.1")
	_, err := Load()
	require.Error(t, err, "BIND_IP without private mode must fail")

	t.Setenv("AGENT_DEPLOYER_DATA_DIR", t.TempDir())
	t.Setenv("AGENT_DEPLOYER_RUNTIME_BIND_IP", "")
	t.Setenv("AGENT_DEPLOYER_RUNTIME_NETWORK", "hubnet")
	_, err = Load()
	require.Error(t, err, "RUNTIME_NETWORK without docker-network mode must fail")
}

func TestLoad_UpstreamHostOnlyValidInLoopbackOrPrivate(t *testing.T) {
	t.Setenv("AGENT_DEPLOYER_DATA_DIR", t.TempDir())
	t.Setenv("AGENT_DEPLOYER_UPSTREAM_HOST", "hub.internal")
	_, err := Load()
	require.Error(t, err, "UPSTREAM_HOST in public mode must fail")
}

func TestLoad_UpstreamHostRejectsGarbage(t *testing.T) {
	// Arbitrary hostnames cannot be verified to resolve inside the private
	// network, and public IPs must never serve as the locator host
	// (issue #11, PR #12 review P1-2).
	for _, host := range []string{"http://x:1", "evil.example.com", "hub.internal", "8.8.8.8", "47.116.185.214"} {
		t.Run(host, func(t *testing.T) {
			t.Setenv("AGENT_DEPLOYER_DATA_DIR", t.TempDir())
			t.Setenv("AGENT_DEPLOYER_RUNTIME_EXPOSE", "loopback")
			t.Setenv("AGENT_DEPLOYER_UPSTREAM_HOST", host)
			_, err := Load()
			require.Error(t, err, "host %q must be rejected", host)
		})
	}
}

func TestLoad_UpstreamHostAcceptsPrivateIPv4OrDockerInternal(t *testing.T) {
	for _, host := range []string{"192.168.1.10", "10.0.0.1", "host.docker.internal"} {
		t.Run(host, func(t *testing.T) {
			t.Setenv("AGENT_DEPLOYER_DATA_DIR", t.TempDir())
			t.Setenv("AGENT_DEPLOYER_API_KEY", "test-key")
			t.Setenv("AGENT_DEPLOYER_RUNTIME_EXPOSE", "loopback")
			t.Setenv("AGENT_DEPLOYER_UPSTREAM_HOST", host)
			cfg, err := Load()
			require.NoError(t, err)
			assert.Equal(t, host, cfg.UpstreamHost)
		})
	}
}

func TestLoad_ProbeRequiresNonPublicMode(t *testing.T) {
	t.Setenv("AGENT_DEPLOYER_DATA_DIR", t.TempDir())
	t.Setenv("AGENT_DEPLOYER_UPSTREAM_PROBE", "true")
	_, err := Load()
	require.Error(t, err, "probe in public mode has no locator to dial")

	t.Setenv("AGENT_DEPLOYER_DATA_DIR", t.TempDir())
	t.Setenv("AGENT_DEPLOYER_API_KEY", "test-key")
	t.Setenv("AGENT_DEPLOYER_RUNTIME_EXPOSE", "loopback")
	t.Setenv("AGENT_DEPLOYER_UPSTREAM_PROBE", "true")
	cfg, err := Load()
	require.NoError(t, err)
	assert.True(t, cfg.UpstreamProbe)
}

// --- PR #12 review P1-1: locator-bearing topologies require auth ---

func TestLoad_NonPublicModeRequiresAPIKey(t *testing.T) {
	// With no API key the auth middleware is a no-op, so every caller would
	// see container DNS / private addresses. Non-public topologies emit the
	// trusted upstream locator, which requires an authenticated boundary
	// (issue #11, PR #12 review P1-1).
	cases := map[string]func(t *testing.T){
		"loopback": func(t *testing.T) {
			t.Setenv("AGENT_DEPLOYER_RUNTIME_EXPOSE", "loopback")
		},
		"private": func(t *testing.T) {
			t.Setenv("AGENT_DEPLOYER_RUNTIME_EXPOSE", "private")
			t.Setenv("AGENT_DEPLOYER_RUNTIME_BIND_IP", "10.2.0.5")
		},
		"docker-network": func(t *testing.T) {
			t.Setenv("AGENT_DEPLOYER_RUNTIME_EXPOSE", "docker-network")
			t.Setenv("AGENT_DEPLOYER_RUNTIME_NETWORK", "hubnet")
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv("AGENT_DEPLOYER_DATA_DIR", t.TempDir())
			setup(t)
			_, err := Load()
			require.Error(t, err, "expose=%s without API key must fail", name)
			assert.Contains(t, err.Error(), "AGENT_DEPLOYER_API_KEY")
		})
	}
}

func TestLoad_PublicModeStillWorksWithoutAPIKey(t *testing.T) {
	// Public mode emits no locator and keeps the frictionless dev default.
	t.Setenv("AGENT_DEPLOYER_DATA_DIR", t.TempDir())
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "", cfg.APIKey)
	assert.Equal(t, ExposePublic, cfg.RuntimeExpose)
}

// --- PR #12 review P1-3: hub locator capability credential ---

func TestLoad_DockerNetworkRequiresHubCapability(t *testing.T) {
	// docker-network mode publishes no host ports, so starting it against a
	// pre-locator hub would silently strand every runtime. Entry requires the
	// versioned capability marker exported by locator-aware agent-hub
	// deployments — an old hub cannot produce it (issue #11 acceptance #9).
	base := func(t *testing.T) {
		t.Setenv("AGENT_DEPLOYER_DATA_DIR", t.TempDir())
		t.Setenv("AGENT_DEPLOYER_API_KEY", "test-key")
		t.Setenv("AGENT_DEPLOYER_RUNTIME_EXPOSE", "docker-network")
		t.Setenv("AGENT_DEPLOYER_RUNTIME_NETWORK", "hubnet")
	}

	t.Run("missing", func(t *testing.T) {
		base(t)
		_, err := Load()
		require.Error(t, err, "docker-network without the hub capability must refuse to start")
		assert.Contains(t, err.Error(), "AGENT_DEPLOYER_HUB_LOCATOR_CAPABILITY")
	})

	t.Run("wrong value", func(t *testing.T) {
		base(t)
		t.Setenv("AGENT_DEPLOYER_HUB_LOCATOR_CAPABILITY", "yes")
		_, err := Load()
		require.Error(t, err, "a guessed capability value must be rejected")
		assert.Contains(t, err.Error(), "AGENT_DEPLOYER_HUB_LOCATOR_CAPABILITY")
	})

	t.Run("correct value", func(t *testing.T) {
		base(t)
		t.Setenv("AGENT_DEPLOYER_HUB_LOCATOR_CAPABILITY", "locator-v1")
		cfg, err := Load()
		require.NoError(t, err)
		assert.Equal(t, HubLocatorCapability, cfg.HubLocatorCapability)
	})
}

func TestLoad_HubCapabilityOnlyValidInDockerNetwork(t *testing.T) {
	t.Setenv("AGENT_DEPLOYER_DATA_DIR", t.TempDir())
	t.Setenv("AGENT_DEPLOYER_API_KEY", "test-key")
	t.Setenv("AGENT_DEPLOYER_RUNTIME_EXPOSE", "loopback")
	t.Setenv("AGENT_DEPLOYER_HUB_LOCATOR_CAPABILITY", "locator-v1")
	_, err := Load()
	require.Error(t, err, "capability set outside docker-network mode must fail")
}
