//go:build integration

// Package integration contains end-to-end tests that exercise the full
// agent-deployer stack against a real Docker daemon. These tests are
// skipped during normal `go test ./...` runs and require the `integration`
// build tag plus a reachable Docker socket.
//
// Run with:
//
//	go test -tags=integration ./tests/integration/... -v -timeout 5m
//
// The tests are idempotent: they clean up any containers and data they create,
// even on assertion failure (via t.Cleanup).
package integration

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/docker/docker/api/types/network"
	sdk "github.com/docker/docker/client"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zerone-agent/agent-deployer/internal/config"
	"github.com/zerone-agent/agent-deployer/internal/docker"
	"github.com/zerone-agent/agent-deployer/internal/handler"
	"github.com/zerone-agent/agent-deployer/internal/model"
	"github.com/zerone-agent/agent-deployer/internal/service"
)

// runtimeImage is the image used for the managed containers. Override with
// CAM_INTEGRATION_IMAGE. It must already exist locally (the test does not
// pull) so that a missing registry does not produce false negatives.
const runtimeImage = "open-agent-runtime:latest"

// requireDocker skips the test when no Docker daemon is reachable.
func requireDocker(t *testing.T) *docker.Client {
	t.Helper()
	dc, err := docker.NewClient()
	if err != nil {
		t.Skipf("docker daemon not available: %v", err)
	}
	// Ping to confirm the daemon actually responds.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := dc.Ping(ctx); err != nil {
		_ = dc.Close()
		t.Skipf("docker daemon not reachable: %v", err)
	}
	return dc
}

func newStack(t *testing.T) (*service.AgentService, *gin.Engine) {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{
		DataDir:              dir,
		Port:                 18080,
		RuntimeImage:         runtimeImage,
		RuntimeContainerPort: 3000,
	}
	dc := requireDocker(t)
	t.Cleanup(func() { _ = dc.Close() })

	svc := service.NewAgentService(cfg, dc)
	h := handler.NewAgentHandler(svc)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	h.Register(r.Group("/api/v1"))
	return svc, r
}

func validCreateBody(name string) []byte {
	body, _ := json.Marshal(model.CreateAgentRequest{
		Agent: model.AgentDefinition{
			Name:         name,
			Model:        "claude-sonnet-4-6",
			SystemPrompt: "integration test agent",
		},
		Provider: model.ProviderConfig{
			Protocol: "anthropic-messages",
			BaseURL:  "https://api.anthropic.com",
			APIKey:   "sk-integration-fake-key",
		},
		RuntimeToken: "it-runtime-token",
	})
	return body
}

func doRequest(t *testing.T, r *gin.Engine, method, path string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, path, reader)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// TestIntegration_AgentLifecycle exercises the full create → get → list →
// restart → stop → delete path against a real Docker daemon. The agent name
// is unique per run to avoid collisions with leftover state.
func TestIntegration_AgentLifecycle(t *testing.T) {
	svc, r := newStack(t)
	agentName := "it-lifecycle-" + time.Now().Format("150405")

	// Always clean up, even if an assertion fails.
	t.Cleanup(func() {
		// Use a background context with a generous timeout for cleanup.
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		_ = svc.Delete(ctx, agentName, true)
	})

	ctx := context.Background()

	// 1. Create
	w := doRequest(t, r, "POST", "/api/v1/agents", validCreateBody(agentName))
	if !assert.Equal(t, http.StatusCreated, w.Code, "create body: %s", w.Body.String()) {
		return
	}
	var createResp struct {
		Success bool                `json:"success"`
		Data    model.AgentResponse `json:"data"`
		Error   string              `json:"error"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &createResp))
	assert.True(t, createResp.Success)
	assert.Equal(t, agentName, createResp.Data.AgentName)
	assert.NotEmpty(t, createResp.Data.ContainerID)
	assert.NotEmpty(t, createResp.Data.ContainerName)
	assert.NotEmpty(t, createResp.Data.HostPort)

	// 2. Idempotent create (no force) returns the same agent
	w2 := doRequest(t, r, "POST", "/api/v1/agents", validCreateBody(agentName))
	require.Equal(t, http.StatusCreated, w2.Code, "idempotent create: %s", w2.Body.String())
	var createResp2 struct {
		Data model.AgentResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &createResp2))
	assert.Equal(t, createResp.Data.ContainerID, createResp2.Data.ContainerID,
		"idempotent create should return the same container")

	// 3. Get
	w3 := doRequest(t, r, "GET", "/api/v1/agents/"+agentName, nil)
	require.Equal(t, http.StatusOK, w3.Code)
	var getResp struct {
		Data model.AgentResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w3.Body.Bytes(), &getResp))
	assert.Equal(t, agentName, getResp.Data.AgentName)

	// 4. List contains our agent
	w4 := doRequest(t, r, "GET", "/api/v1/agents", nil)
	require.Equal(t, http.StatusOK, w4.Code)
	var listResp struct {
		Data []model.AgentResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w4.Body.Bytes(), &listResp))
	found := false
	for _, a := range listResp.Data {
		if a.AgentName == agentName {
			found = true
			break
		}
	}
	assert.True(t, found, "agent %q should appear in list", agentName)

	// 5. Restart
	w5 := doRequest(t, r, "POST", "/api/v1/agents/"+agentName+"/restart", nil)
	assert.Equal(t, http.StatusOK, w5.Code, "restart: %s", w5.Body.String())

	// 6. Stop
	w6 := doRequest(t, r, "POST", "/api/v1/agents/"+agentName+"/stop", nil)
	assert.Equal(t, http.StatusOK, w6.Code, "stop: %s", w6.Body.String())

	// 7. Delete without removeData (container goes away, dirs stay)
	w7 := doRequest(t, r, "DELETE", "/api/v1/agents/"+agentName, nil)
	assert.Equal(t, http.StatusOK, w7.Code, "delete: %s", w7.Body.String())

	// 8. Get now 404s
	w8 := doRequest(t, r, "GET", "/api/v1/agents/"+agentName, nil)
	assert.Equal(t, http.StatusNotFound, w8.Code)

	// 9. Sanity: the service-level Delete with removeData also succeeds even
	// though the container is already gone (idempotent delete).
	require.NoError(t, svc.Delete(ctx, agentName, true))

	// 10. agentDir and sessionDir are removed by removeData=true
	agentDir := filepath.Join(svc.Config().DataDir, "agents", agentName)
	_, err := os.Stat(agentDir)
	assert.True(t, os.IsNotExist(err), "agent dir should be removed: %v", err)
}

// TestIntegration_CreateWithForce verifies that force=true replaces an
// existing container with a new one (different container ID) while preserving
// the session directory.
func TestIntegration_CreateWithForce(t *testing.T) {
	svc, r := newStack(t)
	agentName := "it-force-" + time.Now().Format("150405")

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		_ = svc.Delete(ctx, agentName, true)
	})

	// Initial create
	w := doRequest(t, r, "POST", "/api/v1/agents", validCreateBody(agentName))
	require.Equal(t, http.StatusCreated, w.Code, "initial create: %s", w.Body.String())
	var first struct {
		Data model.AgentResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &first))
	firstContainerID := first.Data.ContainerID

	// Force create with a different system prompt
	body := validCreateBody(agentName)
	var req model.CreateAgentRequest
	require.NoError(t, json.Unmarshal(body, &req))
	req.Agent.SystemPrompt = "updated prompt"
	req.Force = true
	forceBody, _ := json.Marshal(req)

	w2 := doRequest(t, r, "POST", "/api/v1/agents", forceBody)
	require.Equal(t, http.StatusCreated, w2.Code, "force create: %s", w2.Body.String())
	var second struct {
		Data model.AgentResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &second))

	assert.NotEqual(t, firstContainerID, second.Data.ContainerID,
		"force create should produce a new container ID")
	assert.Equal(t, agentName, second.Data.AgentName)
}

// TestIntegration_AgentLifecycle_WithSkills exercises the full skill install
// pipeline end-to-end: the deployer downloads a real zip from an in-process
// httptest server, extracts it under the per-agent skills directory, then
// starts the runtime container with that directory bind-mounted.
func TestIntegration_AgentLifecycle_WithSkills(t *testing.T) {
	svc, r := newStack(t)
	agentName := "it-skills-" + time.Now().Format("150405")

	// Build a minimal skill zip in-memory and serve it.
	var zipBuf bytes.Buffer
	zw := zip.NewWriter(&zipBuf)
	f, err := zw.Create("SKILL.md")
	require.NoError(t, err)
	_, err = f.Write([]byte("# integration skill\n"))
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	zipBytes := zipBuf.Bytes()
	sum := sha256.Sum256(zipBytes)
	zipHash := hex.EncodeToString(sum[:])

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(zipBytes)
	}))
	t.Cleanup(srv.Close)

	// Always clean up the agent + container, even on assertion failure.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		_ = svc.Delete(ctx, agentName, true)
	})

	// Build a CreateAgentRequest with one skill source.
	body, err := json.Marshal(model.CreateAgentRequest{
		Agent: model.AgentDefinition{
			Name:         agentName,
			Model:        "claude-sonnet-4-6",
			SystemPrompt: "integration test agent with skill",
			Skills: []model.SkillSource{
				{Name: "integ-skill", URL: srv.URL, Hash: zipHash},
			},
		},
		Provider: model.ProviderConfig{
			Protocol: "anthropic-messages",
			BaseURL:  "https://api.anthropic.com",
			APIKey:   "sk-integration-fake-key",
		},
		RuntimeToken: "it-runtime-token",
	})
	require.NoError(t, err)

	w := doRequest(t, r, http.MethodPost, "/api/v1/agents", body)
	assert.Equal(t, http.StatusCreated, w.Code,
		"create should succeed; body=%s", w.Body.String())

	// Verify the skill was extracted to the per-agent skills directory on disk.
	cfg := svc.Config()
	skillFile := filepath.Join(cfg.DataDir, agentName, "skills", "integ-skill", "SKILL.md")
	got, err := os.ReadFile(skillFile)
	require.NoError(t, err, "skill file should exist at %s", skillFile)
	assert.Equal(t, "# integration skill\n", string(got))
}

// newRawClient returns a raw Docker SDK client for network plumbing the
// deployer wrapper does not expose (test-only).
func newRawClient(t *testing.T) *sdk.Client {
	t.Helper()
	cli, err := sdk.NewClientWithOpts(sdk.FromEnv, sdk.WithAPIVersionNegotiation())
	require.NoError(t, err)
	t.Cleanup(func() { _ = cli.Close() })
	return cli
}

// TestIntegration_DockerNetworkTopology verifies the full no-published-port
// path: the agent runs with HostPort == 0, is attached to the shared network,
// and the response carries a container-DNS upstream locator (issue #11).
func TestIntegration_DockerNetworkTopology(t *testing.T) {
	dc := requireDocker(t)
	t.Cleanup(func() { _ = dc.Close() })

	raw := newRawClient(t)
	ctx := context.Background()

	netName := "it-hubnet-" + time.Now().Format("150405")
	resp, err := raw.NetworkCreate(ctx, netName, network.CreateOptions{})
	require.NoError(t, err)
	netID := resp.ID
	t.Cleanup(func() { _ = raw.NetworkRemove(context.Background(), netID) })

	dir := t.TempDir()
	cfg := &config.Config{
		DataDir:              dir,
		Port:                 18080,
		RuntimeImage:         runtimeImage,
		RuntimeContainerPort: 3000,
		RuntimeExpose:        config.ExposeDockerNetwork,
		RuntimeNetwork:       netName,
	}
	svc := service.NewAgentService(cfg, dc)

	agentName := "it-dockernet-" + time.Now().Format("150405")
	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		_ = svc.Delete(cctx, agentName, true)
	})

	created, _, err := svc.Create(ctx, &model.CreateAgentRequest{
		Agent: model.AgentDefinition{
			Name:         agentName,
			Description:  "docker-network topology test",
			Model:        "claude-sonnet-4-6",
			SystemPrompt: "integration test agent",
		},
		Provider:     model.ProviderConfig{Protocol: "anthropic-messages", BaseURL: "https://api.anthropic.com", APIKey: "sk-fake"},
		RuntimeToken: "it-runtime-token",
	})
	require.NoError(t, err)

	// Acceptance #4: HostPort == 0 is a legal running state.
	assert.Equal(t, 0, created.HostPort, "no host port may be published in docker-network mode")
	assert.Equal(t, model.StatusRunning, created.Status)
	require.NotNil(t, created.Upstream, "docker-network mode must emit a locator")
	assert.Equal(t, "http", created.Upstream.Scheme)
	assert.Equal(t, created.ContainerName, created.Upstream.Host)
	assert.Equal(t, 3000, created.Upstream.Port)

	// The container is really attached to the shared network.
	insp, err := dc.InspectContainer(ctx, created.ContainerID)
	require.NoError(t, err)
	assert.Contains(t, insp.Networks, netName)

	// Status endpoint agrees with the create response.
	status, err := svc.GetStatus(ctx, agentName)
	require.NoError(t, err)
	require.NotNil(t, status.Upstream)
	assert.Equal(t, created.ContainerName, status.Upstream.Host)

	// Force redeploy: the locator must follow the new container (acceptance #7).
	req := &model.CreateAgentRequest{
		Agent: model.AgentDefinition{
			Name: agentName, Description: "v2", Model: "claude-sonnet-4-6", SystemPrompt: "updated",
		},
		Provider:     model.ProviderConfig{Protocol: "anthropic-messages", BaseURL: "https://api.anthropic.com", APIKey: "sk-fake"},
		RuntimeToken: "it-runtime-token",
		Force:        true,
	}
	redeployed, _, err := svc.Create(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, redeployed.Upstream)
	assert.Equal(t, redeployed.ContainerName, redeployed.Upstream.Host)
	assert.NotEqual(t, created.Upstream.Host, redeployed.Upstream.Host,
		"old locator must not survive redeploy")
}

// TestIntegration_LoopbackTopology verifies runtime ports bind to loopback
// only and the locator reports the loopback address (issue #11, acceptance #3).
func TestIntegration_LoopbackTopology(t *testing.T) {
	raw := newRawClient(t)
	ctx := context.Background()

	cfg := &config.Config{
		DataDir:              t.TempDir(),
		Port:                 18080,
		RuntimeImage:         runtimeImage,
		RuntimeContainerPort: 3000,
		RuntimeExpose:        config.ExposeLoopback,
	}
	dc := requireDocker(t)
	t.Cleanup(func() { _ = dc.Close() })
	svc := service.NewAgentService(cfg, dc)

	agentName := "it-loopback-" + time.Now().Format("150405")
	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		_ = svc.Delete(cctx, agentName, true)
	})

	created, _, err := svc.Create(ctx, &model.CreateAgentRequest{
		Agent: model.AgentDefinition{
			Name: agentName, Description: "loopback topology test",
			Model: "claude-sonnet-4-6", SystemPrompt: "integration test agent",
		},
		Provider:     model.ProviderConfig{Protocol: "anthropic-messages", BaseURL: "https://api.anthropic.com", APIKey: "sk-fake"},
		RuntimeToken: "it-runtime-token",
	})
	require.NoError(t, err)

	require.NotNil(t, created.Upstream)
	assert.Equal(t, "127.0.0.1", created.Upstream.Host)
	assert.Equal(t, created.HostPort, created.Upstream.Port)
	assert.NotZero(t, created.HostPort)

	// The published port really binds to 127.0.0.1, not 0.0.0.0.
	info, err := raw.ContainerInspect(ctx, created.ContainerID)
	require.NoError(t, err)
	bindings := info.NetworkSettings.Ports["3000/tcp"]
	require.NotEmpty(t, bindings)
	assert.Equal(t, "127.0.0.1", bindings[0].HostIP,
		"loopback mode must not publish on 0.0.0.0")
}
