package docker

import (
	"testing"

	"github.com/docker/docker/api/types"
	"github.com/stretchr/testify/assert"

	"github.com/zerone-agent/agent-deployer/internal/naming"
)

func TestBuildEnvVars_OnlyHTTPAPIKey(t *testing.T) {
	env := buildEnvVars("runtime-http-key")
	assert.Equal(t, []string{"ZERONE_AGENT_HTTP_API_KEY=runtime-http-key"}, env,
		"credentials/model now live in agents.yaml; only the runtime's own API key stays in env")
}

func TestBuildEnvVars_EmptyTokenNoEnv(t *testing.T) {
	env := buildEnvVars("")
	assert.Empty(t, env)
}

func TestToRuntimeContainer(t *testing.T) {
	input := types.Container{
		ID:    "abc123",
		Names: []string{"/cloud-agent-coder-aaaaaaaa"},
		Image: "runtime:latest",
		State: "running",
		Labels: map[string]string{
			LabelManaged:     "true",
			LabelAgentName:   "coder",
			LabelRootAgentID: "coder",
			LabelInstanceID:  "aaaaaaaa",
			LabelCreatedAt:   "2026-06-25T10:00:00Z",
		},
		Ports: []types.Port{
			{IP: "0.0.0.0", PrivatePort: 3000, PublicPort: 0, Type: "tcp"},
			{IP: "0.0.0.0", PrivatePort: 3000, PublicPort: 32768, Type: "tcp"},
		},
	}
	rc := toRuntimeContainer(input)
	assert.Equal(t, "abc123", rc.ID)
	assert.Equal(t, "cloud-agent-coder-aaaaaaaa", rc.Name)
	assert.Equal(t, naming.DeploymentKey("coder"), rc.DeploymentKey,
		"deployment key comes from the agent.name label (issue #18)")
	assert.Equal(t, naming.RootAgentID("coder"), rc.RootAgentID,
		"bare root agent id comes from the agent.root-id label")
	assert.Equal(t, "aaaaaaaa", rc.InstanceID)
	assert.Equal(t, "running", rc.Status)
	assert.Equal(t, "runtime:latest", rc.Image)
	assert.Equal(t, 32768, rc.HostPort)
	assert.Equal(t, "2026-06-25T10:00:00Z", rc.CreatedAt, "CreatedAt must be read from label")
}

func TestToRuntimeContainer_NoPorts(t *testing.T) {
	input := types.Container{
		ID:     "x",
		Names:  []string{"/x"},
		State:  "exited",
		Labels: map[string]string{},
	}
	rc := toRuntimeContainer(input)
	assert.Equal(t, "x", rc.Name)
	assert.Equal(t, "exited", rc.Status)
	assert.Equal(t, 0, rc.HostPort)
	assert.Empty(t, rc.CreatedAt, "CreatedAt must be empty when label is missing")
	assert.Empty(t, rc.RootAgentID, "RootAgentID must be empty on pre-split containers")
}

func TestToRuntimeContainer_EmptyName(t *testing.T) {
	input := types.Container{
		ID:     "x",
		Names:  nil,
		Labels: map[string]string{},
	}
	rc := toRuntimeContainer(input)
	assert.Equal(t, "", rc.Name)
}
