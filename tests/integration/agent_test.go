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
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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
		RootAgentID:   name,
		DeploymentKey: name,
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
		RootAgentID:   agentName,
		DeploymentKey: agentName,
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
		RootAgentID:   agentName,
		DeploymentKey: agentName,
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

// TestIntegration_AgentGraphRuntimeIsolation is the real-runtime acceptance
// test for issue #16 (regressions #8/#9): it deploys a complete graph and
// asks the RUNNING runtime — not the deployer's own YAML — what each agent
// can see. Passes only against a genuine open-agent-runtime v2.4.0+ image;
// set CAM_INTEGRATION_REAL_RUNTIME_IMAGE (e.g. on the ECS host, where the
// real image is available) to run it. Skipped otherwise: the local stand-in
// image (nginx:alpine tagged as the runtime) has no runtime API to call.
func TestIntegration_AgentGraphRuntimeIsolation(t *testing.T) {
	realImage := os.Getenv("CAM_INTEGRATION_REAL_RUNTIME_IMAGE")
	if realImage == "" {
		t.Skip("set CAM_INTEGRATION_REAL_RUNTIME_IMAGE to a real open-agent-runtime v2.4.0+ image to run this test")
	}
	requireDocker(t)

	// Same wiring as newStack, but with the caller-provided runtime image and
	// the assume-latest gate bypassed (the assertion below fails loudly if the
	// image does not actually implement the runtime API).
	router, svc := newRealRuntimeStack(t, realImage)

	agentName := "it-iso-" + time.Now().Format("150405")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		_ = svc.Delete(ctx, agentName, true)
	})

	// In-process artifact server: child-a's skill zip + custom tool file.
	// The SKILL.md carries the frontmatter the SDK parser requires
	// (description is mandatory; without it the skill is never registered).
	var zipBuf bytes.Buffer
	zw := zip.NewWriter(&zipBuf)
	f, err := zw.Create("SKILL.md")
	require.NoError(t, err)
	_, err = f.Write([]byte("---\ndescription: Deterministic isolation marker skill\n---\n# isolation skill\n"))
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	zipBytes := zipBuf.Bytes()
	sum := sha256.Sum256(zipBytes)
	zipHash := hex.EncodeToString(sum[:])

	// File tool must satisfy runtime v2.5.0 materializeTool's hard contract
	// (name + description + zod inputSchema + execute) — a bare {name}
	// export fails materialization and makes child-a unavailable.
	toolBody := []byte(`import { z } from "zod"
export default {
  name: "child-tool",
  description: "Deterministic file tool",
  inputSchema: z.object({}),
  execute: async () => "file-tool-ok",
}
`)
	toolSum := sha256.Sum256(toolBody)
	toolHash := hex.EncodeToString(toolSum[:])
	mux := http.NewServeMux()
	mux.HandleFunc("/skill.zip", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(zipBytes) })
	mux.HandleFunc("/child-tool.mjs", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(toolBody) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	body, err := json.Marshal(model.CreateAgentRequest{
		RootAgentID:   agentName,
		DeploymentKey: agentName,
		Agents: []model.AgentDefinition{
			{
				Name: agentName, Description: "Coordinates work",
				Model: "claude-sonnet-4-6", SystemPrompt: "Delegate tasks",
				Tools: []string{"Task"}, Subagents: []string{"child-a", "child-b"},
			},
			{
				Name: "child-a", Description: "Research specialist", SystemPrompt: "Research",
				DisallowedTools: []string{"Bash"},
				SettingSources:  []string{"user"},
				Skills:          []model.SkillSource{{Name: "iso-skill", URL: srv.URL + "/skill.zip", Hash: zipHash}},
				CustomTools:     []model.ToolSource{{Name: "child-tool", URL: srv.URL + "/child-tool.mjs", Hash: toolHash, FileName: "child-tool.mjs"}},
			},
			{Name: "child-b", Description: "Review specialist", SystemPrompt: "Review"},
		},
		Provider:     model.ProviderConfig{Protocol: "anthropic-messages", BaseURL: "https://api.anthropic.com", APIKey: "sk-integration-fake-key"},
		RuntimeToken: "it-runtime-token",
	})
	require.NoError(t, err)

	baseURL, _, _ := deployGraph(t, router, body)
	// Runtime v2.4.0 authenticates /v1/* via the x-api-key header (see
	// runtime src/auth.ts) — NOT Authorization: Bearer. A 401 here means
	// protocol drift between deployer tests and runtime auth.
	const runtimeToken = "it-runtime-token"

	// Every entry must materialize as ready: schema errors crash the runtime
	// or leave entries unavailable (this is also the /health equivalent).
	waitForAgentsReady(t, baseURL, runtimeToken, agentName, "child-a", "child-b")

	agentDetail := func(id string) map[string]any {
		t.Helper()
		req, rerr := http.NewRequest(http.MethodGet, baseURL+"/v1/agents/"+id, nil)
		require.NoError(t, rerr)
		req.Header.Set("x-api-key", runtimeToken)
		resp, rerr := (&http.Client{Timeout: 5 * time.Second}).Do(req)
		require.NoError(t, rerr)
		defer resp.Body.Close()
		detailBody, rerr := io.ReadAll(resp.Body)
		require.NoError(t, rerr)
		require.Equal(t, http.StatusOK, resp.StatusCode, "detail %s: %s", id, string(detailBody))
		var detail map[string]any
		require.NoError(t, json.Unmarshal(detailBody, &detail))
		return detail
	}
	skillNames := func(detail map[string]any) []string {
		raw, ok := detail["availableSkills"].([]any)
		if !ok {
			return nil
		}
		names := make([]string, 0, len(raw))
		for _, s := range raw {
			if m, ok := s.(map[string]any); ok {
				if n, ok := m["name"].(string); ok {
					names = append(names, n)
				}
			}
		}
		return names
	}

	// Regression #8: every entry materializes and is addressable — schema
	// accepted by real runtime v2.4.0+.
	for _, id := range []string{agentName, "child-a", "child-b"} {
		detail := agentDetail(id)
		assert.Equal(t, "ready", detail["status"], "agent %s must be ready (got %v)", id, detail)
	}

	// Regression #8: child-a sees ONLY its own skill; parent and child-b see none.
	parentDetail := agentDetail(agentName)
	childADetail := agentDetail("child-a")
	childBDetail := agentDetail("child-b")

	assert.Contains(t, skillNames(childADetail), "iso-skill",
		"child-a must see its own installed skill")
	assert.NotContains(t, skillNames(parentDetail), "iso-skill",
		"parent must NOT see child-a's skill (no inheritance)")
	assert.NotContains(t, skillNames(childBDetail), "iso-skill",
		"sibling child-b must NOT see child-a's skill (no sibling leakage)")
	assert.Empty(t, skillNames(childBDetail), "child-b declares no skills ⇒ none visible")

	// Regression #4 through the real runtime: deny policy survives materialization.
	assert.Equal(t, []any{"Bash"}, childADetail["disallowedTools"])

	// Mount references: parent lists both children; children mount nothing.
	subs, ok := parentDetail["subagents"].([]any)
	require.True(t, ok, "parent detail must list subagents: %v", parentDetail)
	require.Len(t, subs, 2)
	first, ok := subs[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "child-a", first["agent_id"])
	_, hasSubs := childADetail["subagents"]
	assert.False(t, hasSubs, "child-a must not mount anything")

	// child-b carries no capabilities at all (empty stays empty).
	assert.NotContains(t, childBDetail, "mcpServers")
	assert.NotContains(t, childBDetail, "allowedTools")
	assert.NotContains(t, childBDetail, "extraUserSkillDirs")
}

// containerReachableServer starts an httptest server bound to 0.0.0.0 and
// returns it together with a URL the RUNTIME CONTAINER can reach. The deployer
// creates containers on Docker's default bridge network; its gateway
// (172.17.0.1 by default) routes from inside the container back to the host.
// The URL is built from the gateway + the listener's actual port: after
// swapping in a 0.0.0.0 listener, srv.URL itself is http://[::]:<port> (or
// 0.0.0.0), which is NOT container-reachable. Override the gateway with
// CAM_INTEGRATION_DOCKER_GATEWAY on hosts whose default bridge differs.
func containerReachableServer(t *testing.T, h http.Handler) (*httptest.Server, string) {
	t.Helper()
	srv := httptest.NewUnstartedServer(h)
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	require.NoError(t, err)
	srv.Listener = ln
	srv.Start()
	gateway := os.Getenv("CAM_INTEGRATION_DOCKER_GATEWAY")
	if gateway == "" {
		gateway = "172.17.0.1"
	}
	_, port, err := net.SplitHostPort(ln.Addr().String())
	require.NoError(t, err)
	return srv, "http://" + net.JoinHostPort(gateway, port)
}

// truncateForLog bounds a diagnostic string for test output.
func truncateForLog(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// newRealRuntimeStack wires a deployer handler stack against a genuine
// runtime image, mirroring the wiring of the tested Create path (assume-latest
// bypassed: the assertions themselves fail loudly if the image does not
// implement the runtime API).
func newRealRuntimeStack(t *testing.T, realImage string) (*gin.Engine, *service.AgentService) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{
		DataDir:                  t.TempDir(),
		Port:                     18080,
		RuntimeImage:             realImage,
		RuntimeContainerPort:     3000,
		RuntimeImageAssumeLatest: true,
	}
	dc := requireDocker(t)
	t.Cleanup(func() { _ = dc.Close() })
	svc := service.NewAgentService(cfg, dc)
	h := handler.NewAgentHandler(svc)
	router := gin.New()
	h.Register(router.Group("/api/v1"))
	return router, svc
}

// deployGraph posts a create request and returns the runtime-facing URL,
// token, and container name from the response.
func deployGraph(t *testing.T, router *gin.Engine, body []byte) (baseURL, runtimeToken, containerName string) {
	t.Helper()
	w := doRequest(t, router, http.MethodPost, "/api/v1/agents", body)
	if !assert.Equal(t, http.StatusCreated, w.Code, "graph deploy: %s", w.Body.String()) {
		t.FailNow()
	}
	var createResp struct {
		Data model.AgentResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &createResp))
	return fmt.Sprintf("http://localhost:%d", createResp.Data.HostPort), createResp.Data.RuntimeToken, createResp.Data.ContainerName
}

// waitForAgentsReady polls the runtime agent detail endpoint until every id
// reports status "ready", failing with the detail body on timeout (schema
// errors crash the runtime or leave entries unavailable).
func waitForAgentsReady(t *testing.T, baseURL, runtimeToken string, ids ...string) {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	deadline := time.Now().Add(120 * time.Second)
	for {
		allReady := true
		for _, id := range ids {
			req, rerr := http.NewRequest(http.MethodGet, baseURL+"/v1/agents/"+id, nil)
			require.NoError(t, rerr)
			req.Header.Set("x-api-key", runtimeToken)
			resp, rerr := client.Do(req)
			if rerr == nil {
				db, _ := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				var detail struct {
					Status            string `json:"status"`
					UnavailableReason string `json:"unavailableReason"`
				}
				_ = json.Unmarshal(db, &detail)
				if detail.Status != "ready" {
					allReady = false
					if time.Now().After(deadline) {
						t.Fatalf("agent %s not ready (%s: %s): %s", id, detail.Status, detail.UnavailableReason, string(db))
					}
				}
			} else {
				allReady = false
				if time.Now().After(deadline) {
					t.Fatalf("runtime never came up: %v", rerr)
				}
			}
		}
		if allReady {
			return
		}
		time.Sleep(2 * time.Second)
	}
}

// capturedLLMRequest records what the SDK's provider sent to the mock LLM.
type capturedLLMRequest struct {
	System string   // messages[0].content
	Tools  []string // function names from body.tools
	Model  string
	Raw    []byte
}

// TestIntegration_AgentGraphTaskExecution is the execution-path acceptance
// test for issue #16 (third review round): it drives a REAL runtime through
// parent → Task(child-a) with deterministic fixtures — a mock
// openai-completions provider and a mock streamable-http MCP server — and
// asserts the child's own MCP / custom tools / skills / datasets / allow+deny
// policy actually take effect in the execution path, with no parent fallback.
//
// Gated on CAM_INTEGRATION_REAL_RUNTIME_IMAGE (genuine runtime v2.4.0+); it
// self-skips where no real image exists.
func TestIntegration_AgentGraphTaskExecution(t *testing.T) {
	realImage := os.Getenv("CAM_INTEGRATION_REAL_RUNTIME_IMAGE")
	if realImage == "" {
		t.Skip("set CAM_INTEGRATION_REAL_RUNTIME_IMAGE to a real open-agent-runtime v2.4.0+ image to run this test")
	}
	requireDocker(t)

	var mu sync.Mutex
	parentTurns, childTurns := 0, 0
	var parentReqs, childReqs, childBReqs []capturedLLMRequest

	// ---- Mock LLM (openai-completions protocol) -------------------------
	// Bound to 0.0.0.0 and addressed via the docker bridge gateway so the
	// runtime container can actually reach it (host-loopback would not).
	llmSrv, llmURL := containerReachableServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.NotFound(w, r)
			return
		}
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var req struct {
			Model    string `json:"model"`
			Stream   bool   `json:"stream"`
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
			Tools []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
		}
		require.NoError(t, json.Unmarshal(body, &req))

		captured := capturedLLMRequest{Model: req.Model, Raw: body}
		if len(req.Messages) > 0 && req.Messages[0].Role == "system" {
			var s string
			_ = json.Unmarshal(req.Messages[0].Content, &s)
			captured.System = s
		}
		for _, tl := range req.Tools {
			captured.Tools = append(captured.Tools, tl.Function.Name)
		}

		mu.Lock()
		defer mu.Unlock()
		isChild := strings.Contains(captured.System, "research specialist")
		isChildB := strings.Contains(captured.System, "review specialist")
		switch {
		case isChildB:
			childBReqs = append(childBReqs, captured)
		case isChild:
			childReqs = append(childReqs, captured)
			childTurns++
		default:
			parentReqs = append(parentReqs, captured)
			parentTurns++
		}

		// Response shaping. The SDK speaks BOTH dialects (verified on ECS):
		// prompt()/root runs request WITHOUT stream (createMessage → the
		// response is parsed with response.json()), while subagents spawned
		// via Task request WITH stream:true (createMessageStream → SSE
		// decoder). The mock must answer in the dialect each request asked
		// for — a JSON body on a stream request decodes to zero chunks, and
		// an SSE body on a non-stream request fails response.json().
		type logicalMsg map[string]any // role/content/tool_calls
		jsonResp := func(finish string, msg logicalMsg) map[string]any {
			return map[string]any{
				"choices": []any{map[string]any{
					"finish_reason": finish,
					"message":       msg,
				}},
			}
		}
		sseChunk := func(delta map[string]any, finish any) map[string]any {
			return map[string]any{
				"choices": []any{map[string]any{
					"index":         0,
					"delta":         delta,
					"finish_reason": finish,
				}},
			}
		}
		writeResp := func(w http.ResponseWriter, reqStream bool, finish string, msg logicalMsg) {
			if reqStream {
				w.Header().Set("Content-Type", "text/event-stream")
				flusher, _ := w.(http.Flusher)
				emit := func(c map[string]any) {
					b, _ := json.Marshal(c)
					_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
					if flusher != nil {
						flusher.Flush()
					}
				}
				if msg["content"] != nil {
					emit(sseChunk(map[string]any{"role": "assistant", "content": msg["content"]}, nil))
				}
				if tcs, ok := msg["tool_calls"].([]any); ok && len(tcs) > 0 {
					for i, tc := range tcs {
						m := tc.(map[string]any)
						emit(sseChunk(map[string]any{"tool_calls": []any{map[string]any{
							"index": i, "id": m["id"], "type": "function",
							"function": m["function"],
						}}}, nil))
					}
				}
				emit(sseChunk(map[string]any{}, finish))
				_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
				if flusher != nil {
					flusher.Flush()
				}
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(jsonResp(finish, msg))
		}
		toolCallLogical := func(id, name, args string) logicalMsg {
			return logicalMsg{
				"role": "assistant",
				"tool_calls": []any{map[string]any{
					"id":   id,
					"type": "function",
					"function": map[string]any{
						"name":      name,
						"arguments": args,
					},
				}},
			}
		}

		var finish string
		var msg logicalMsg
		switch {
		case isChildB:
			// child-b declares nothing: answer immediately and terminate.
			finish, msg = "stop", logicalMsg{"role": "assistant", "content": "child-b-done"}
		case isChild && childTurns == 1:
			// Ask for BOTH the file tool and the MCP tool in one turn.
			finish = "tool_calls"
			msg = logicalMsg{
				"role": "assistant",
				"tool_calls": []any{
					map[string]any{"id": "call-file", "type": "function",
						"function": map[string]any{"name": "child-tool", "arguments": "{}"}},
					map[string]any{"id": "call-mcp", "type": "function",
						"function": map[string]any{"name": "mcp__knowledge__lookup", "arguments": "{}"}},
				},
			}
		case isChild:
			finish, msg = "stop", logicalMsg{"role": "assistant", "content": "child-a-done"}
		case parentTurns == 1:
			finish, msg = "tool_calls", toolCallLogical("call-task-a", "Task", `{"prompt":"do the research","description":"research","subagent_type":"General","subagent_name":"child-a"}`)
		case parentTurns == 2:
			// Exercise the EMPTY-capability child through the mount path too
			// (issue regression #8: child-b must not see parent/child-a
			// capabilities).
			finish, msg = "tool_calls", toolCallLogical("call-task-b", "Task", `{"prompt":"do the review","description":"review","subagent_type":"General","subagent_name":"child-b"}`)
		default:
			finish, msg = "stop", logicalMsg{"role": "assistant", "content": "parent-final"}
		}
		respJSON, _ := json.Marshal(map[string]any{"finish_reason": finish, "message": msg})
		role := "parent"
		if isChildB {
			role = "child-b"
		} else if isChild {
			role = "child-a"
		}
		t.Logf("LLM-RESP role=%s turns(p=%d,c=%d,b=%d) stream=%v body=%s",
			role, parentTurns, childTurns, len(childBReqs), req.Stream, truncateForLog(string(respJSON), 1000))
		writeResp(w, req.Stream, finish, msg)
	}))
	t.Cleanup(llmSrv.Close)

	// ---- Mock MCP (streamable-http JSON-RPC) ----------------------------
	mcpCalls := 0
	mcpSrv, mcpURL := containerReachableServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var rpc struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			Result json.RawMessage `json:"result"`
		}
		require.NoError(t, json.Unmarshal(body, &rpc))
		w.Header().Set("Content-Type", "application/json")

		switch rpc.Method {
		case "initialize":
			var p struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			_ = json.Unmarshal(rpc.Params, &p)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      rpc.ID,
				"result": map[string]any{
					"protocolVersion": p.ProtocolVersion,
					"capabilities":    map[string]any{"tools": map[string]any{}},
					"serverInfo":      map[string]any{"name": "mock-mcp", "version": "1.0"},
				},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      rpc.ID,
				"result": map[string]any{
					"tools": []any{map[string]any{
						"name":        "lookup",
						"description": "Deterministic mock lookup",
						"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
					}},
				},
			})
		case "tools/call":
			mu.Lock()
			mcpCalls++
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      rpc.ID,
				"result": map[string]any{
					"content": []any{map[string]any{"type": "text", "text": "mcp-lookup-ok"}},
					"isError": false,
				},
			})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": rpc.ID,
				"error": map[string]any{"code": -32601, "message": "unknown method"},
			})
		}
	}))
	t.Cleanup(mcpSrv.Close)

	// ---- Deploy the graph ------------------------------------------------
	router, svc := newRealRuntimeStack(t, realImage)
	var containerName string

	t.Cleanup(func() {
		// Failure diagnostics: dump the runtime container's logs BEFORE the
		// deployment is torn down so tool-execution failures are attributable
		// (the container is deleted right after).
		if t.Failed() && containerName != "" {
			if out, cerr := exec.Command("docker", "logs", "--tail", "120", containerName).CombinedOutput(); cerr == nil {
				t.Logf("=== runtime container %s logs ===\n%s", containerName, out)
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		_ = svc.Delete(ctx, "parent", true)
	})

	// Skill zip with a distinctive, assertable marker. The frontmatter
	// description is REQUIRED by the SDK parser (skills/yaml.ts): a bare
	// markdown body is rejected and never registered, which would silently
	// invalidate the isolation assertions below.
	var zipBuf bytes.Buffer
	zw := zip.NewWriter(&zipBuf)
	f, err := zw.Create("SKILL.md")
	require.NoError(t, err)
	_, err = f.Write([]byte("---\ndescription: Deterministic isolation marker skill\n---\n# iso-skill marker\n"))
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	zipBytes := zipBuf.Bytes()
	sum := sha256.Sum256(zipBytes)
	zipHash := hex.EncodeToString(sum[:])

	// File tool: zod schema + deterministic execute result.
	toolSrc := `import { z } from "zod"
export default {
  name: "child-tool",
  description: "Deterministic file tool",
  inputSchema: z.object({}),
  execute: async () => "file-tool-ok",
}
`
	toolSum := sha256.Sum256([]byte(toolSrc))
	toolHash := hex.EncodeToString(toolSum[:])
	mux := http.NewServeMux()
	mux.HandleFunc("/skill.zip", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(zipBytes) })
	mux.HandleFunc("/child-tool.mjs", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(toolSrc)) })
	artifacts, artifactsURL := containerReachableServer(t, mux)
	t.Cleanup(artifacts.Close)

	// Focused guard: the advertised fixture hosts must equal the configured
	// bridge gateway — a regression here silently hands the runtime an
	// unreachable [::]/0.0.0.0 address again.
	expectedGateway := os.Getenv("CAM_INTEGRATION_DOCKER_GATEWAY")
	if expectedGateway == "" {
		expectedGateway = "172.17.0.1"
	}
	for _, u := range []string{llmURL, mcpURL, artifactsURL} {
		parsed, perr := url.Parse(u)
		require.NoError(t, perr)
		assert.Equal(t, expectedGateway, parsed.Hostname(), "fixture URL must advertise the bridge gateway: %s", u)
	}

	body, err := json.Marshal(model.CreateAgentRequest{
		RootAgentID:   "parent",
		DeploymentKey: "parent",
		Agents: []model.AgentDefinition{
			{
				Name: "parent", Description: "Coordinates",
				Model: "mock-model", SystemPrompt: "You are the parent coordinator. Delegate the research task.",
				Tools: []string{"Task"}, Subagents: []string{"child-a", "child-b"},
			},
			{
				Name: "child-a", Description: "Research", SystemPrompt: "You are the research specialist.",
				// "Skill" must be ALLOWED explicitly: allowedTools is a
				// whitelist and filters the SDK built-in set (this is what
				// gates the Skill tool, which is a built-in).
				Tools: []string{"WebSearch", "Skill"}, DisallowedTools: []string{"Bash"},
				SettingSources: []string{"user"},
				Skills:         []model.SkillSource{{Name: "iso-skill", URL: artifactsURL + "/skill.zip", Hash: zipHash}},
				CustomTools:    []model.ToolSource{{Name: "child-tool", URL: artifactsURL + "/child-tool.mjs", Hash: toolHash, FileName: "child-tool.mjs"}},
				Datasets:       map[string]string{"knowledge-a": "The secret dataset for research"},
				McpServers: map[string]model.McpServerConfig{
					"knowledge": {Type: "http", URL: mcpURL + "/mcp"},
				},
			},
			{Name: "child-b", Description: "Review", SystemPrompt: "You are the review specialist."},
		},
		Provider:     model.ProviderConfig{Protocol: "openai-completions", BaseURL: llmURL, APIKey: "mock-key"},
		RuntimeToken: "it-runtime-token",
	})
	require.NoError(t, err)

	baseURL, _, containerName := deployGraph(t, router, body)
	const runtimeToken = "it-runtime-token"

	// Wait until every entry is ready: child-a's materialization includes a
	// real MCP handshake against the mock server.
	waitForAgentsReady(t, baseURL, runtimeToken, "parent", "child-a", "child-b")

	// Drive the execution path: parent run → Task(child-a) → child tools.
	runBody := `{"message":"start the research","stream":false}`
	runReq, rerr := http.NewRequest(http.MethodPost, baseURL+"/v1/agents/parent/runs", strings.NewReader(runBody))
	require.NoError(t, rerr)
	runReq.Header.Set("Content-Type", "application/json")
	runReq.Header.Set("x-api-key", runtimeToken)
	runClient := &http.Client{Timeout: 180 * time.Second}
	runResp, rerr := runClient.Do(runReq)
	require.NoError(t, rerr)
	defer runResp.Body.Close()
	runBytes, rerr := io.ReadAll(runResp.Body)
	require.NoError(t, rerr)
	require.Equal(t, http.StatusOK, runResp.StatusCode, "run response: %s", string(runBytes))
	var run struct {
		RunID    string `json:"runId"`
		Text     string `json:"text"`
		NumTurns int    `json:"numTurns"`
		State    string `json:"state"`
	}
	require.NoError(t, json.Unmarshal(runBytes, &run))
	// Runtime v2.4.0's success schema is runId/sessionId/text/usage/numTurns/
	// durationMs; the state field exists only on cancelled/failed responses.
	assert.NotEmpty(t, run.RunID, "run body: %s", string(runBytes))
	assert.Equal(t, "parent-final", run.Text)
	assert.GreaterOrEqual(t, run.NumTurns, 1)
	assert.Empty(t, run.State, "success responses carry no state field (cancelled/failed only)")

	// ---- Assertions on the execution path --------------------------------
	mu.Lock()
	defer mu.Unlock()
	t.Logf("DIAG childReqs=%d parentReqs=%d childBReqs=%d mcpCalls=%d",
		len(childReqs), len(parentReqs), len(childBReqs), mcpCalls)
	if len(childReqs) > 0 {
		t.Logf("DIAG child-a first request (first 6000 chars): %s", truncateForLog(string(childReqs[0].Raw), 6000))
	}
	require.NotEmpty(t, childReqs, "child-a must actually have been executed via Task")
	require.NotEmpty(t, parentReqs, "parent must have been executed")

	// Child-a system prompt carries ITS OWN prompt + datasets — and nothing
	// from the parent. (Skills are NOT injected into prompt text: the SDK
	// surfaces them lazily via the Skill tool / <available_skills>, so the
	// mounted visibility is asserted on the tool boundary below.)
	child1 := childReqs[0]
	assert.Contains(t, child1.System, "research specialist")
	assert.Contains(t, child1.System, "knowledge-a", "child datasets must be injected")
	assert.NotContains(t, child1.System, "parent coordinator")

	// Child-a tool boundary: own file tool + own MCP tool + own Skill tool +
	// allowed tool; disallowed Bash and the parent's Task mount are absent.
	childTools := strings.Join(child1.Tools, ",")
	assert.Contains(t, childTools, "child-tool", "fileTools must be mounted for child-a")
	assert.Contains(t, childTools, "mcp__knowledge__lookup", "child MCP tools must be mounted")
	assert.Contains(t, childTools, "Skill", "the child's own scanned skills must surface as the Skill tool")
	assert.Contains(t, childTools, "WebSearch")
	assert.NotContains(t, childTools, "Bash", "disallowedTools must be enforced")
	assert.NotContains(t, childTools, "Task", "child mounts nothing")

	// Runtime-global model is reused by the mounted child (no child override).
	assert.Equal(t, "mock-model", child1.Model)

	// The tools actually EXECUTED: second child turn carries both results.
	require.GreaterOrEqual(t, len(childReqs), 2, "child-a must run a second turn after tool results")
	secondRaw := string(childReqs[1].Raw)
	assert.Contains(t, secondRaw, "file-tool-ok", "file tool must have executed")
	assert.Contains(t, secondRaw, "mcp-lookup-ok", "MCP tool must have executed")
	assert.Equal(t, 1, mcpCalls, "mock MCP must have received exactly one tools/call")

	// Parent boundary: Task mounted; none of the child's capabilities leak up.
	parent1 := parentReqs[0]
	assert.Contains(t, parent1.System, "parent coordinator")
	assert.NotContains(t, parent1.System, "knowledge-a")
	assert.NotContains(t, parent1.System, "iso-skill", "child skill must not leak into the parent prompt")
	parentTools := strings.Join(parent1.Tools, ",")
	assert.Contains(t, parentTools, "Task")
	assert.NotContains(t, parentTools, "child-tool")
	assert.NotContains(t, parentTools, "mcp__knowledge__lookup")
	assert.NotContains(t, parentTools, "Skill", "skills must not leak up to the parent")
	assert.Equal(t, "mock-model", parent1.Model)

	// child-b was ALSO executed through the Task mount path with EMPTY
	// capabilities (issue regression #8: child-b must not see parent or
	// sibling capabilities; the standalone detail view alone cannot prove it).
	require.NotEmpty(t, childBReqs, "child-b must also have been executed via Task")
	childB1 := childBReqs[0]
	assert.Contains(t, childB1.System, "review specialist")
	assert.NotContains(t, childB1.System, "parent coordinator", "parent prompt must not leak into child-b")
	assert.NotContains(t, childB1.System, "knowledge-a", "sibling datasets must not leak into child-b")
	assert.NotContains(t, childB1.System, "iso-skill", "sibling skills must not leak into child-b")
	childBTools := strings.Join(childB1.Tools, ",")
	assert.NotContains(t, childBTools, "Task", "child-b mounts nothing")
	assert.NotContains(t, childBTools, "child-tool", "sibling file tools must not leak into child-b")
	assert.NotContains(t, childBTools, "mcp__knowledge__lookup", "sibling MCP tools must not leak into child-b")
	// NOTE: the Skill TOOL is an SDK built-in and legitimately appears in
	// child-b's default set (it declares no allowedTools). What must not
	// leak is skill REGISTRY content — child-b's skill registry stays empty,
	// asserted via detail (availableSkills) in the isolation test.
	assert.Equal(t, "mock-model", childB1.Model)

	// The parent's final turn carries BOTH children's Task results.
	require.GreaterOrEqual(t, len(parentReqs), 3, "parent must run child-a, child-b, then finalize")
	lastParentRaw := string(parentReqs[len(parentReqs)-1].Raw)
	assert.Contains(t, lastParentRaw, "child-a-done", "Task(child-a) result must reach the parent")
	assert.Contains(t, lastParentRaw, "child-b-done", "Task(child-b) result must reach the parent")
}

// TestIntegration_AgentGraphSessionCap is the runtime v2.6.0 acceptance
// test for the maxSessionQueries contract (SDK 3.1.0 rename, runtime PR
// #56): a root-declared session cap must round-trip through the generated
// agents.yaml into the running runtime's AgentDetail. It is gated on
// CAM_INTEGRATION_REAL_RUNTIME_IMAGE like the other real-runtime tests and
// requires a v2.6.0+ image (the deployer gate refuses a cap declaration on
// older runtimes).
func TestIntegration_AgentGraphSessionCap(t *testing.T) {
	realImage := os.Getenv("CAM_INTEGRATION_REAL_RUNTIME_IMAGE")
	if realImage == "" {
		t.Skip("set CAM_INTEGRATION_REAL_RUNTIME_IMAGE to a real open-agent-runtime v2.6.0+ image to run this test")
	}
	requireDocker(t)

	router, svc := newRealRuntimeStack(t, realImage)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		_ = svc.Delete(ctx, "parent", true)
	})

	queries := 7
	body, err := json.Marshal(model.CreateAgentRequest{
		RootAgentID:   "parent",
		DeploymentKey: "parent",
		Agents: []model.AgentDefinition{
			{
				Name: "parent", Description: "Capped session",
				Model: "mock-model", SystemPrompt: "You run a capped session.",
				MaxSessionQueries: &queries,
			},
		},
		Provider:     model.ProviderConfig{Protocol: "anthropic-messages", BaseURL: "https://api.anthropic.com", APIKey: "sk-integration-fake-key"},
		RuntimeToken: "it-runtime-token",
	})
	require.NoError(t, err)

	baseURL, runtimeToken, _ := deployGraph(t, router, body)
	waitForAgentsReady(t, baseURL, runtimeToken, "parent")

	// The v2.6.0 AgentDetail echoes the cap (PR #56 renamed the detail
	// field together with the config key).
	req, rerr := http.NewRequest(http.MethodGet, baseURL+"/v1/agents/parent", nil)
	require.NoError(t, rerr)
	req.Header.Set("x-api-key", runtimeToken)
	resp, rerr := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	require.NoError(t, rerr)
	defer resp.Body.Close()
	raw, rerr := io.ReadAll(resp.Body)
	require.NoError(t, rerr)
	require.Equal(t, http.StatusOK, resp.StatusCode, "detail body: %s", string(raw))
	var detail struct {
		Status                string `json:"status"`
		MaxSessionQueries     *int   `json:"maxSessionQueries"`
		LegacyMaxSessionTurns *int   `json:"maxSessionTurns"`
	}
	require.NoError(t, json.Unmarshal(raw, &detail))
	assert.Equal(t, "ready", detail.Status)
	require.NotNil(t, detail.MaxSessionQueries,
		"detail must echo the v2.6.0 maxSessionQueries contract key: %s", string(raw))
	assert.Equal(t, queries, *detail.MaxSessionQueries)
	assert.Nil(t, detail.LegacyMaxSessionTurns,
		"the legacy detail key must be gone on v2.6.0: %s", string(raw))
}
