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

func TestLoad_RuntimeImageAssumeLatest(t *testing.T) {
	t.Setenv("AGENT_DEPLOYER_DATA_DIR", "/tmp/x")
	t.Setenv("AGENT_DEPLOYER_RUNTIME_IMAGE_ASSUME_LATEST", "true")
	cfg, err := Load()
	require.NoError(t, err)
	assert.True(t, cfg.RuntimeImageAssumeLatest)
}

func TestLoad_RuntimeImageAssumeLatestDefaultFalse(t *testing.T) {
	t.Setenv("AGENT_DEPLOYER_DATA_DIR", "/tmp/x")
	cfg, err := Load()
	require.NoError(t, err)
	assert.False(t, cfg.RuntimeImageAssumeLatest)
}
