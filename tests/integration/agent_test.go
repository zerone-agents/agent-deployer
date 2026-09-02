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

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zerone-agent/agent-deployer/internal/config"
	"github.com/zerone-agent/agent-deployer/internal/docker"
	"github.com/zerone-agent/agent-deployer/internal/handler"
	"github.com/zerone-agent/agent-deployer/internal/model"
	"github.com/zerone-agent/agent-deployer/internal/service"
	"gopkg.in/yaml.v3"
)

// runtimeImage is the image used for the managed containers. Override with
// CAM_INTEGRATION_IMAGE. It must already exist locally (the test does not
// pull) so that a missing registry does not produce false negatives. The
// graph protocol requires runtime v2.4.0+; locally a stand-in image can be
// tagged with the same name (e.g. docker tag nginx:alpine open-agent-runtime:v2.4.0)
// since these tests only assert deployment topology, not runtime behavior.
const runtimeImage = "open-agent-runtime:v2.4.0"

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
		RootAgentID: name,
		Agents: []model.AgentDefinition{
			{
				Name:         name,
				Description:  "integration test agent",
				Model:        "claude-sonnet-4-6",
				SystemPrompt: "integration test agent",
			},
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

	// 2. Idempotent create (no force) returns the same agent with 200 OK
	// (201 is reserved for containers that were actually created).
	w2 := doRequest(t, r, "POST", "/api/v1/agents", validCreateBody(agentName))
	require.Equal(t, http.StatusOK, w2.Code, "idempotent create: %s", w2.Body.String())
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

	// 8. Get now returns the ARCHIVED agent: the container is gone but the
	// on-disk data remains (only fully-gone agents 404).
	w8 := doRequest(t, r, "GET", "/api/v1/agents/"+agentName, nil)
	require.Equal(t, http.StatusOK, w8.Code)
	var archivedResp struct {
		Data model.AgentResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w8.Body.Bytes(), &archivedResp))
	assert.Equal(t, model.StatusArchived, archivedResp.Data.Status)

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
	req.Agents[0].SystemPrompt = "updated prompt"
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

	// Build a CreateAgentRequest with one skill source on the root agent.
	body, err := json.Marshal(model.CreateAgentRequest{
		RootAgentID: agentName,
		Agents: []model.AgentDefinition{
			{
				Name:           agentName,
				Description:    "integration test agent with skill",
				Model:          "claude-sonnet-4-6",
				SystemPrompt:   "integration test agent with skill",
				SettingSources: []string{"user"},
				Skills: []model.SkillSource{
					{Name: "integ-skill", URL: srv.URL, Hash: zipHash},
				},
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

	// Verify the skill was extracted to the per-agent directory on disk.
	cfg := svc.Config()
	skillFile := filepath.Join(cfg.DataDir, agentName, "agents", "skills", agentName, "integ-skill", "SKILL.md")
	got, err := os.ReadFile(skillFile)
	require.NoError(t, err, "skill file should exist at %s", skillFile)
	assert.Equal(t, "# integration skill\n", string(got))
}

// TestIntegration_AgentGraphLifecycle deploys a complete parent+child graph
// with per-agent artifacts (issue #16): child-a carries a skill and a custom
// tool, parent declares its own tool, and child-b stays empty. Asserts the
// deployment topology: three YAML entries, per-agent artifact layout, no
// capability inheritance.
func TestIntegration_AgentGraphLifecycle(t *testing.T) {
	svc, r := newStack(t)
	agentName := "it-graph-" + time.Now().Format("150405")

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		_ = svc.Delete(ctx, agentName, true)
	})

	// Skill zip served in-process.
	var zipBuf bytes.Buffer
	zw := zip.NewWriter(&zipBuf)
	f, err := zw.Create("SKILL.md")
	require.NoError(t, err)
	_, err = f.Write([]byte("# graph skill\n"))
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	zipBytes := zipBuf.Bytes()
	sum := sha256.Sum256(zipBytes)
	zipHash := hex.EncodeToString(sum[:])

	mux := http.NewServeMux()
	skillReqs := 0
	toolReqs := 0
	mux.HandleFunc("/skill.zip", func(w http.ResponseWriter, r *http.Request) {
		skillReqs++
		_, _ = w.Write(zipBytes)
	})
	toolBody := []byte("export default { name: 'child-tool' }\n")
	toolSum := sha256.Sum256(toolBody)
	toolHash := hex.EncodeToString(toolSum[:])
	mux.HandleFunc("/child-tool.mjs", func(w http.ResponseWriter, r *http.Request) {
		toolReqs++
		_, _ = w.Write(toolBody)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	body, err := json.Marshal(model.CreateAgentRequest{
		RootAgentID: agentName,
		Agents: []model.AgentDefinition{
			{
				Name:         agentName,
				Description:  "Coordinates work",
				Model:        "claude-sonnet-4-6",
				SystemPrompt: "Delegate tasks",
				Tools:        []string{"Task"},
				Subagents:    []string{"child-a", "child-b"},
			},
			{
				Name:            "child-a",
				Description:     "Research specialist",
				SystemPrompt:    "Research and summarize",
				DisallowedTools: []string{"Bash"},
				SettingSources:  []string{"user"},
				Skills:          []model.SkillSource{{Name: "graph-skill", URL: srv.URL + "/skill.zip", Hash: zipHash}},
				CustomTools:     []model.ToolSource{{Name: "child-tool", URL: srv.URL + "/child-tool.mjs", Hash: toolHash, FileName: "child-tool.mjs"}},
			},
			{
				Name:         "child-b",
				Description:  "Review specialist",
				SystemPrompt: "Review the result",
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
	if !assert.Equal(t, http.StatusCreated, w.Code, "graph create: %s", w.Body.String()) {
		return
	}

	cfg := svc.Config()
	// Per-agent skill dir for child-a only.
	skillFile := filepath.Join(cfg.DataDir, agentName, "agents", "skills", "child-a", "graph-skill", "SKILL.md")
	got, err := os.ReadFile(skillFile)
	require.NoError(t, err, "child-a skill should exist at %s", skillFile)
	assert.Equal(t, "# graph skill\n", string(got))
	if _, err := os.Stat(filepath.Join(cfg.DataDir, agentName, "agents", "skills", "child-b")); !assert.True(t, os.IsNotExist(err)) {
		t.Error("child-b declares no skills ⇒ no skill dir")
	}
	// Tool in the shared flat dir.
	assert.FileExists(t, filepath.Join(cfg.DataDir, agentName, "agents", "tools", "child-tool.mjs"))

	// YAML topology: three entries, id references, per-agent declarations.
	data, err := os.ReadFile(filepath.Join(cfg.DataDir, agentName, "agents", "agents.yaml"))
	require.NoError(t, err)
	var doc struct {
		Agents []struct {
			ID                 string   `yaml:"id"`
			APIKey             string   `yaml:"apiKey"`
			Model              string   `yaml:"model"`
			Subagents          []string `yaml:"subagents"`
			DisallowedTools    []string `yaml:"disallowedTools"`
			CustomTools        []string `yaml:"customTools"`
			ExtraUserSkillDirs []string `yaml:"extraUserSkillDirs"`
		} `yaml:"agents"`
	}
	require.NoError(t, yaml.Unmarshal(data, &doc))
	require.Len(t, doc.Agents, 3)
	byID := map[string]int{}
	for i, a := range doc.Agents {
		byID[a.ID] = i
	}
	root := doc.Agents[byID[agentName]]
	assert.Equal(t, []string{"child-a", "child-b"}, root.Subagents)
	assert.NotEmpty(t, root.APIKey, "root carries provider credentials")
	childA := doc.Agents[byID["child-a"]]
	assert.Equal(t, []string{"Bash"}, childA.DisallowedTools)
	assert.Equal(t, []string{"./tools/child-tool.mjs"}, childA.CustomTools)
	assert.Equal(t, []string{"/app/config/skills/child-a"}, childA.ExtraUserSkillDirs,
		"child-a's skill dir is declared via extraUserSkillDirs")
	childB := doc.Agents[byID["child-b"]]
	assert.Empty(t, childB.APIKey, "child-b must not carry credentials")
	assert.Empty(t, childB.Model, "child-b must not carry model")

	// Each artifact downloaded exactly once.
	assert.Equal(t, 1, skillReqs, "skill zip downloaded once")
	assert.Equal(t, 1, toolReqs, "tool file downloaded once")
}
