package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/zerone-agent/agent-deployer/internal/config"
	"github.com/zerone-agent/agent-deployer/internal/docker"
	"github.com/zerone-agent/agent-deployer/internal/handler"
	"github.com/zerone-agent/agent-deployer/internal/model"
	"github.com/zerone-agent/agent-deployer/internal/service"
)

// fakeDockerForHandler is a minimal DockerClient implementation used to test
// the HTTP handler layer without touching Docker. It keeps the containers it
// creates in memory keyed by agent name so subsequent Get/List/Delete calls
// see them.
type fakeDockerForHandler struct {
	containers map[string]*docker.RuntimeContainer
	tokens     map[string]string
	nextPort   int
}

func newFakeDockerForHandler() *fakeDockerForHandler {
	return &fakeDockerForHandler{
		containers: make(map[string]*docker.RuntimeContainer),
		tokens:     make(map[string]string),
		nextPort:   32768,
	}
}

func (f *fakeDockerForHandler) FindAgentContainer(_ context.Context, agentName string) (*docker.RuntimeContainer, error) {
	c, ok := f.containers[agentName]
	if !ok {
		return nil, nil
	}
	return c, nil
}

func (f *fakeDockerForHandler) ListManagedContainers(_ context.Context) ([]docker.RuntimeContainer, error) {
	out := make([]docker.RuntimeContainer, 0, len(f.containers))
	for _, c := range f.containers {
		out = append(out, *c)
	}
	return out, nil
}

func (f *fakeDockerForHandler) CreateAgentContainer(_ context.Context, opts docker.CreateOpts) (string, int, error) {
	port := f.nextPort
	f.nextPort++
	c := &docker.RuntimeContainer{
		ID:         "cid-" + opts.AgentName,
		Name:       opts.ContainerName,
		AgentName:  opts.AgentName,
		InstanceID: opts.InstanceID,
		Status:     "running",
		HostPort:   port,
		Image:      opts.Image,
	}
	f.containers[opts.AgentName] = c
	f.tokens[c.ID] = opts.RuntimeToken
	return c.ID, port, nil
}

func (f *fakeDockerForHandler) StopContainer(_ context.Context, id string) error {
	for _, c := range f.containers {
		if c.ID == id {
			c.Status = "exited"
		}
	}
	return nil
}

func (f *fakeDockerForHandler) RestartContainer(_ context.Context, id string) error {
	for _, c := range f.containers {
		if c.ID == id {
			c.Status = "running"
		}
	}
	return nil
}

func (f *fakeDockerForHandler) RemoveContainer(_ context.Context, id string) error {
	for name, c := range f.containers {
		if c.ID == id {
			delete(f.containers, name)
			delete(f.tokens, id)
		}
	}
	return nil
}

func (f *fakeDockerForHandler) InspectContainer(_ context.Context, containerID string) (*docker.RuntimeContainer, error) {
	for _, c := range f.containers {
		if c.ID == containerID {
			if c.Health == "" {
				c.Health = "healthy"
			}
			return c, nil
		}
	}
	return &docker.RuntimeContainer{ID: containerID, Health: "healthy"}, nil
}

func (f *fakeDockerForHandler) ContainerLogs(_ context.Context, id string, tail int) (string, error) {
	return "fake log output\n", nil
}

// setupTestRouter builds a gin engine wired to a real AgentService backed by
// the fake docker client, rooted at a per-test temp directory. The default
// runtime image opts into the assume-latest gate so graph deployments pass.
func setupTestRouter(t *testing.T) (*gin.Engine, *service.AgentService, *fakeDockerForHandler) {
	t.Helper()
	return setupTestRouterWithImage(t, "open-agent-runtime:latest", true)
}

// setupTestRouterWithImage builds a test router with an explicit runtime
// image and assume-latest setting, for exercising the image-version gate.
func setupTestRouterWithImage(t *testing.T, image string, assumeLatest bool) (*gin.Engine, *service.AgentService, *fakeDockerForHandler) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	cfg := &config.Config{
		DataDir:                  dir,
		Port:                     8080,
		RuntimeImage:             image,
		RuntimeContainerPort:     3000,
		RuntimeImageAssumeLatest: assumeLatest,
	}
	fakeDC := newFakeDockerForHandler()
	svc := service.NewAgentService(cfg, fakeDC)
	h := handler.NewAgentHandler(svc)
	r := gin.New()
	api := r.Group("/api/v1")
	h.Register(api)
	return r, svc, fakeDC
}

// validRequestBody returns a JSON body for a valid create-agent request.
func validRequestBody() []byte {
	body, _ := json.Marshal(model.CreateAgentRequest{
		RootAgentID: "coder",
		Agents: []model.AgentDefinition{
			{
				Name:         "coder",
				Description:  "Writes and edits code",
				Model:        "claude-sonnet-4-6",
				SystemPrompt: "You are a coding assistant.",
			},
		},
		Provider: model.ProviderConfig{
			Protocol: "anthropic-messages",
			BaseURL:  "https://api.anthropic.com",
			APIKey:   "sk-test",
		},
		RuntimeToken: "test-runtime-token",
	})
	return body
}

// doRequest issues a request against the router and returns the recorded
// response. It t.Fatals on construction errors.
func doRequest(t *testing.T, r http.Handler, method, path string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestCreateAgent_201(t *testing.T) {
	r, _, _ := setupTestRouter(t)
	w := doRequest(t, r, http.MethodPost, "/api/v1/agents", validRequestBody())

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusCreated, w.Body.String())
	}

	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			AgentName    string `json:"agentName"`
			RuntimeToken string `json:"runtimeToken"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !resp.Success {
		t.Errorf("success = false, want true")
	}
	if resp.Data.AgentName != "coder" {
		t.Errorf("data.agentName = %q, want %q", resp.Data.AgentName, "coder")
	}
	// Create MUST hand out the runtime token supplied by the caller.
	if resp.Data.RuntimeToken != "test-runtime-token" {
		t.Errorf("data.runtimeToken = %q, want test-runtime-token; body=%s", resp.Data.RuntimeToken, w.Body.String())
	}
}

// TestCreateAgent_AcceptsRuntimeToken verifies the HTTP API accepts a
// runtime_token field and uses it verbatim instead of generating a fresh token.
func TestCreateAgent_AcceptsRuntimeToken(t *testing.T) {
	r, _, fakeDC := setupTestRouter(t)

	req := model.CreateAgentRequest{
		RootAgentID: "coder",
		Agents: []model.AgentDefinition{
			{
				Name:         "coder",
				Description:  "Writes and edits code",
				Model:        "claude-sonnet-4-6",
				SystemPrompt: "You are a coding assistant.",
			},
		},
		Provider: model.ProviderConfig{
			Protocol: "anthropic-messages",
			BaseURL:  "https://api.anthropic.com",
			APIKey:   "sk-test",
		},
		RuntimeToken: "caller-provided-token",
	}
	body, _ := json.Marshal(req)
	w := doRequest(t, r, http.MethodPost, "/api/v1/agents", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusCreated, w.Body.String())
	}

	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			RuntimeToken string `json:"runtimeToken"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !resp.Success {
		t.Fatalf("success = false, want true")
	}
	if resp.Data.RuntimeToken != "caller-provided-token" {
		t.Errorf("runtimeToken = %q, want caller-provided-token", resp.Data.RuntimeToken)
	}
	if len(fakeDC.containers) != 1 {
		t.Fatalf("expected exactly one container, got %d", len(fakeDC.containers))
	}
	for _, c := range fakeDC.containers {
		if fakeDC.tokens[c.ID] != "caller-provided-token" {
			t.Errorf("container RuntimeToken = %q, want caller-provided-token", fakeDC.tokens[c.ID])
		}
	}
}

// TestGetAgent_OmitsRuntimeToken verifies the runtime token is NEVER returned
// by Get, even for an agent that was just created (and thus has a live token
// injected into its container env). The deployer does not persist the token;
// Create is the one and only time it is exposed.
func TestGetAgent_OmitsRuntimeToken(t *testing.T) {
	r, _, _ := setupTestRouter(t)
	// Seed an agent so Get has something to return.
	if w := doRequest(t, r, http.MethodPost, "/api/v1/agents", validRequestBody()); w.Code != http.StatusCreated {
		t.Fatalf("seed create: status = %d, body=%s", w.Code, w.Body.String())
	}

	w := doRequest(t, r, http.MethodGet, "/api/v1/agents/coder", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			RuntimeToken string `json:"runtimeToken"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Data.RuntimeToken != "" {
		t.Errorf("Get must not return runtimeToken; got %q", resp.Data.RuntimeToken)
	}
}

// TestListAgents_OmitsRuntimeToken verifies List never includes runtimeToken
// in any element. Returning N tokens in one response would amplify leak risk.
func TestListAgents_OmitsRuntimeToken(t *testing.T) {
	r, _, _ := setupTestRouter(t)
	if w := doRequest(t, r, http.MethodPost, "/api/v1/agents", validRequestBody()); w.Code != http.StatusCreated {
		t.Fatalf("seed create: status = %d, body=%s", w.Code, w.Body.String())
	}

	w := doRequest(t, r, http.MethodGet, "/api/v1/agents", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Success bool `json:"success"`
		Data    []struct {
			RuntimeToken string `json:"runtimeToken"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	require.NotEmpty(t, resp.Data, "precondition: seeded agent should appear in list")
	for i, a := range resp.Data {
		assert.Empty(t, a.RuntimeToken, "list[%d] must not contain runtimeToken", i)
	}
}

// TestListAgents_IncludeArchived verifies ?includeArchived=true merges archived
// agents (container deleted, data on disk) into the list, while the default
// list only shows active agents.
func TestListAgents_IncludeArchived(t *testing.T) {
	r, _, _ := setupTestRouter(t)
	if w := doRequest(t, r, http.MethodPost, "/api/v1/agents", validRequestBody()); w.Code != http.StatusCreated {
		t.Fatalf("seed create: status = %d, body=%s", w.Code, w.Body.String())
	}

	// Delete without purge => archived.
	w := doRequest(t, r, http.MethodDelete, "/api/v1/agents/coder", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("delete: status = %d, body=%s", w.Code, w.Body.String())
	}

	// Default list should be empty.
	w = doRequest(t, r, http.MethodGet, "/api/v1/agents", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("default list status = %d, body=%s", w.Code, w.Body.String())
	}
	var defaultList struct {
		Success bool `json:"success"`
		Data    []struct {
			AgentName string `json:"agentName"`
			Status    string `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &defaultList); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	assert.Empty(t, defaultList.Data, "default list must not include archived agents")

	// includeArchived=true should show the archived agent.
	w = doRequest(t, r, http.MethodGet, "/api/v1/agents?includeArchived=true", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("archived list status = %d, body=%s", w.Code, w.Body.String())
	}
	var archivedList struct {
		Success bool `json:"success"`
		Data    []struct {
			AgentName string `json:"agentName"`
			Status    string `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &archivedList); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	require.Len(t, archivedList.Data, 1, "includeArchived must show the archived agent")
	assert.Equal(t, "coder", archivedList.Data[0].AgentName)
	assert.Equal(t, string(model.StatusArchived), archivedList.Data[0].Status)
}

// TestCreateAgent_IdempotentReturns200 verifies that posting the same agent
// definition twice without Force returns 200 (existing container) on the
// second call, not 201 (created).
func TestCreateAgent_IdempotentReturns200(t *testing.T) {
	r, _, _ := setupTestRouter(t)

	first := doRequest(t, r, http.MethodPost, "/api/v1/agents", validRequestBody())
	if first.Code != http.StatusCreated {
		t.Fatalf("first create: status = %d, want %d; body=%s", first.Code, http.StatusCreated, first.Body.String())
	}

	second := doRequest(t, r, http.MethodPost, "/api/v1/agents", validRequestBody())
	if second.Code != http.StatusOK {
		t.Fatalf("second (idempotent) create: status = %d, want %d; body=%s", second.Code, http.StatusOK, second.Body.String())
	}

	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			AgentName string `json:"agentName"`
		} `json:"data"`
	}
	if err := json.Unmarshal(second.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.Success {
		t.Errorf("success = false, want true on idempotent create")
	}
	if resp.Data.AgentName != "coder" {
		t.Errorf("data.agentName = %q, want %q", resp.Data.AgentName, "coder")
	}
}

func TestCreateAgent_400_InvalidJSON(t *testing.T) {
	r, _, _ := setupTestRouter(t)
	w := doRequest(t, r, http.MethodPost, "/api/v1/agents", []byte("{not json"))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	var resp model.ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Success {
		t.Errorf("success = true, want false")
	}
	if resp.Error == "" {
		t.Errorf("error is empty")
	}
}

func TestCreateAgent_400_MissingFields(t *testing.T) {
	r, _, _ := setupTestRouter(t)
	// Valid JSON, but missing required model field.
	body, _ := json.Marshal(model.CreateAgentRequest{
		RootAgentID: "coder",
		Agents: []model.AgentDefinition{
			{
				Name:         "coder",
				Description:  "Writes and edits code",
				SystemPrompt: "You are a coding assistant.",
			},
		},
		Provider: model.ProviderConfig{
			Protocol: "anthropic-messages",
			BaseURL:  "https://api.anthropic.com",
			APIKey:   "sk-test",
		},
	})
	w := doRequest(t, r, http.MethodPost, "/api/v1/agents", body)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	var resp model.ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Error == "" {
		t.Errorf("error is empty")
	}
}

func TestGetAgent_200(t *testing.T) {
	r, _, _ := setupTestRouter(t)
	if w := doRequest(t, r, http.MethodPost, "/api/v1/agents", validRequestBody()); w.Code != http.StatusCreated {
		t.Fatalf("seed create: status = %d, body=%s", w.Code, w.Body.String())
	}

	w := doRequest(t, r, http.MethodGet, "/api/v1/agents/coder", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			AgentName string `json:"agentName"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.Success {
		t.Errorf("success = false, want true")
	}
	if resp.Data.AgentName != "coder" {
		t.Errorf("data.agentName = %q, want %q", resp.Data.AgentName, "coder")
	}
}

func TestGetAgent_GoneReturns404(t *testing.T) {
	r, _, _ := setupTestRouter(t)
	w := doRequest(t, r, http.MethodGet, "/api/v1/agents/does-not-exist", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusNotFound, w.Body.String())
	}
	var resp model.ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Success {
		t.Errorf("success = true, want false")
	}
}

func TestGetAgent_Archived(t *testing.T) {
	r, _, _ := setupTestRouter(t)
	if w := doRequest(t, r, http.MethodPost, "/api/v1/agents", validRequestBody()); w.Code != http.StatusCreated {
		t.Fatalf("seed create: status = %d, body=%s", w.Code, w.Body.String())
	}

	// Delete without purge => archived.
	if w := doRequest(t, r, http.MethodDelete, "/api/v1/agents/coder", nil); w.Code != http.StatusOK {
		t.Fatalf("delete: status = %d, body=%s", w.Code, w.Body.String())
	}

	w := doRequest(t, r, http.MethodGet, "/api/v1/agents/coder", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			AgentName string `json:"agentName"`
			Status    string `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.Success {
		t.Errorf("success = false, want true")
	}
	if resp.Data.AgentName != "coder" {
		t.Errorf("data.agentName = %q, want %q", resp.Data.AgentName, "coder")
	}
	if resp.Data.Status != string(model.StatusArchived) {
		t.Errorf("data.status = %q, want %q", resp.Data.Status, model.StatusArchived)
	}
}

func TestListAgents_200(t *testing.T) {
	r, _, _ := setupTestRouter(t)
	if w := doRequest(t, r, http.MethodPost, "/api/v1/agents", validRequestBody()); w.Code != http.StatusCreated {
		t.Fatalf("seed create: status = %d, body=%s", w.Code, w.Body.String())
	}

	w := doRequest(t, r, http.MethodGet, "/api/v1/agents", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp struct {
		Success bool `json:"success"`
		Data    []struct {
			AgentName string `json:"agentName"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.Success {
		t.Errorf("success = false, want true")
	}
	if len(resp.Data) != 1 {
		t.Fatalf("len(data) = %d, want 1", len(resp.Data))
	}
	if resp.Data[0].AgentName != "coder" {
		t.Errorf("data[0].agentName = %q, want %q", resp.Data[0].AgentName, "coder")
	}
}

func TestDeleteAgent_200_AndBecomesArchived(t *testing.T) {
	r, _, _ := setupTestRouter(t)
	if w := doRequest(t, r, http.MethodPost, "/api/v1/agents", validRequestBody()); w.Code != http.StatusCreated {
		t.Fatalf("seed create: status = %d, body=%s", w.Code, w.Body.String())
	}

	w := doRequest(t, r, http.MethodDelete, "/api/v1/agents/coder", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	var resp model.SuccessResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.Success {
		t.Errorf("success = false, want true")
	}

	// After a non-purge Delete the agent data remains on disk, so Get should
	// return it as archived rather than 404.
	w = doRequest(t, r, http.MethodGet, "/api/v1/agents/coder", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get after delete: status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	var getResp struct {
		Success bool `json:"success"`
		Data    struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &getResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if getResp.Data.Status != string(model.StatusArchived) {
		t.Errorf("status = %q, want %q", getResp.Data.Status, model.StatusArchived)
	}
}

// TestDeleteAgent_Purge removes both container and data. After purge the agent
// is fully gone and Get returns 404.
func TestDeleteAgent_Purge(t *testing.T) {
	r, _, _ := setupTestRouter(t)
	if w := doRequest(t, r, http.MethodPost, "/api/v1/agents", validRequestBody()); w.Code != http.StatusCreated {
		t.Fatalf("seed create: status = %d, body=%s", w.Code, w.Body.String())
	}

	w := doRequest(t, r, http.MethodDelete, "/api/v1/agents/coder?purge=true", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	w = doRequest(t, r, http.MethodGet, "/api/v1/agents/coder", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("get after purge: status = %d, want %d; body=%s", w.Code, http.StatusNotFound, w.Body.String())
	}
}

func TestStopAgent_200(t *testing.T) {
	r, _, _ := setupTestRouter(t)
	if w := doRequest(t, r, http.MethodPost, "/api/v1/agents", validRequestBody()); w.Code != http.StatusCreated {
		t.Fatalf("seed create: status = %d, body=%s", w.Code, w.Body.String())
	}

	w := doRequest(t, r, http.MethodPost, "/api/v1/agents/coder/stop", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	var resp model.SuccessResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.Success {
		t.Errorf("success = false, want true")
	}
}

// TestAgentHandler_Create_SkillHashMismatch_Returns422 verifies that a skill
// install failure due to hash mismatch is mapped to HTTP 422 (client metadata
// error), not 500. Per spec §7: the client declared a hash that doesn't match
// the actual downloaded bytes.
func TestAgentHandler_Create_SkillHashMismatch_Returns422(t *testing.T) {
	// Serve bytes that DON'T match the hash the client will declare.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("this is not the zip the hash claims"))
	}))
	defer srv.Close()

	r, _, _ := setupTestRouter(t)
	body, _ := json.Marshal(model.CreateAgentRequest{
		RootAgentID: "coder",
		Agents: []model.AgentDefinition{
			{
				Name:           "coder",
				Description:    "Writes and edits code",
				Model:          "claude-sonnet-4-6",
				SystemPrompt:   "You are a coding assistant.",
				SettingSources: []string{"user"},
				Skills: []model.SkillSource{
					{
						Name: "code-review",
						URL:  srv.URL,
						// Declared hash is all-zeros — guaranteed to mismatch.
						Hash: strings.Repeat("0", 64),
					},
				},
			},
		},
		Provider: model.ProviderConfig{
			Protocol: "anthropic-messages",
			BaseURL:  "https://api.anthropic.com",
			APIKey:   "sk-test",
		},
		RuntimeToken: "test-runtime-token",
	})

	w := doRequest(t, r, http.MethodPost, "/api/v1/agents", body)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d (422 = client hash wrong); body=%s",
			w.Code, http.StatusUnprocessableEntity, w.Body.String())
	}
	var resp model.ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Success {
		t.Errorf("success = true, want false")
	}
	if resp.Error == "" {
		t.Errorf("error message is empty")
	}
}

// TestAgentHandler_Create_SkillDownloadFailed_Returns502 verifies that a
// skill install failure due to an upstream HTTP error (server returns 500)
// is mapped to HTTP 502 (upstream failure), not 500 (deployer crash).
// Per spec §7: the deployer is a proxy; an upstream download error is 502.
func TestAgentHandler_Create_SkillDownloadFailed_Returns502(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	r, _, _ := setupTestRouter(t)
	body, _ := json.Marshal(model.CreateAgentRequest{
		RootAgentID: "coder",
		Agents: []model.AgentDefinition{
			{
				Name:           "coder",
				Description:    "Writes and edits code",
				Model:          "claude-sonnet-4-6",
				SystemPrompt:   "You are a coding assistant.",
				SettingSources: []string{"user"},
				Skills: []model.SkillSource{
					{Name: "broken", URL: srv.URL, Hash: strings.Repeat("a", 64)},
				},
			},
		},
		Provider: model.ProviderConfig{
			Protocol: "anthropic-messages",
			BaseURL:  "https://api.anthropic.com",
			APIKey:   "sk-test",
		},
		RuntimeToken: "test-runtime-token",
	})

	w := doRequest(t, r, http.MethodPost, "/api/v1/agents", body)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d (502 = upstream download failure); body=%s",
			w.Code, http.StatusBadGateway, w.Body.String())
	}
	var resp model.ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Success {
		t.Errorf("success = true, want false")
	}
	if resp.Error == "" {
		t.Errorf("error message is empty")
	}
}

// TestAgentHandler_Create_ToolHashMismatch_Returns422 verifies that a custom
// tool install failure due to hash mismatch is mapped to HTTP 422 (client
// metadata error), not 500. Per issue #10: the client declared a hash that
// doesn't match the actual downloaded bytes.
func TestAgentHandler_Create_ToolHashMismatch_Returns422(t *testing.T) {
	// Serve bytes that DON'T match the hash the client will declare.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("actual content"))
	}))
	defer srv.Close()

	r, _, _ := setupTestRouter(t)
	body, _ := json.Marshal(model.CreateAgentRequest{
		RootAgentID: "coder",
		Agents: []model.AgentDefinition{
			{
				Name:         "coder",
				Description:  "Writes and edits code",
				Model:        "claude-sonnet-4-6",
				SystemPrompt: "You are a coding assistant.",
				CustomTools: []model.ToolSource{
					{
						Name:     "mismatched",
						URL:      srv.URL,
						Hash:     strings.Repeat("a", 64),
						FileName: "m.js",
					},
				},
			},
		},
		Provider: model.ProviderConfig{
			Protocol: "anthropic-messages",
			BaseURL:  "https://api.anthropic.com",
			APIKey:   "sk-test",
		},
		RuntimeToken: "test-runtime-token",
	})

	w := doRequest(t, r, http.MethodPost, "/api/v1/agents", body)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d (422 = client hash wrong); body=%s",
			w.Code, http.StatusUnprocessableEntity, w.Body.String())
	}
	var resp model.ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Success {
		t.Errorf("success = true, want false")
	}
	if !strings.Contains(resp.Error, "mismatched") {
		t.Errorf("error = %q, want it to contain the tool name %q", resp.Error, "mismatched")
	}
}

// TestAgentHandler_Create_ToolDownloadFailure_Returns502 verifies that a
// custom tool install failure due to an upstream HTTP error (server returns
// 500) is mapped to HTTP 502 (upstream failure), not 500 (deployer crash).
// Per issue #10: the deployer is a proxy; an upstream download error is 502.
func TestAgentHandler_Create_ToolDownloadFailure_Returns502(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	r, _, _ := setupTestRouter(t)
	body, _ := json.Marshal(model.CreateAgentRequest{
		RootAgentID: "coder",
		Agents: []model.AgentDefinition{
			{
				Name:         "coder",
				Description:  "Writes and edits code",
				Model:        "claude-sonnet-4-6",
				SystemPrompt: "You are a coding assistant.",
				CustomTools: []model.ToolSource{
					{Name: "down", URL: srv.URL, Hash: strings.Repeat("a", 64), FileName: "d.js"},
				},
			},
		},
		Provider: model.ProviderConfig{
			Protocol: "anthropic-messages",
			BaseURL:  "https://api.anthropic.com",
			APIKey:   "sk-test",
		},
		RuntimeToken: "test-runtime-token",
	})

	w := doRequest(t, r, http.MethodPost, "/api/v1/agents", body)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d (502 = upstream download failure); body=%s",
			w.Code, http.StatusBadGateway, w.Body.String())
	}
	var resp model.ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Success {
		t.Errorf("success = true, want false")
	}
	if resp.Error == "" {
		t.Errorf("error message is empty")
	}
}

func TestCreateAgent_400_InvalidAigc(t *testing.T) {
	r, _, _ := setupTestRouter(t)
	body, _ := json.Marshal(model.CreateAgentRequest{
		RootAgentID: "coder",
		Agents: []model.AgentDefinition{
			{
				Name:         "coder",
				Description:  "Writes and edits code",
				Model:        "glm-4.5",
				SystemPrompt: "You are a coding assistant.",
			},
		},
		Provider: model.ProviderConfig{
			Protocol: "openai-completions",
			BaseURL:  "https://api.openai.com",
			APIKey:   "sk-test",
		},
		Aigc: &model.AigcConfig{
			Enabled:         true,
			ContentProducer: "too-short",
		},
	})
	w := doRequest(t, r, http.MethodPost, "/api/v1/agents", body)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	var resp model.ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Success {
		t.Errorf("success = true, want false")
	}
	if !strings.Contains(resp.Error, "aigc: ") {
		t.Errorf("error = %q, want it to contain %q", resp.Error, "aigc: ")
	}
}

// TestCreateAgent_LegacyInlineRequestRejected guards the issue #16 migration
// contract: the pre-graph request shape ("agent" field) must fail with an
// explicit 400 diagnostic instead of being silently reinterpreted.
func TestCreateAgent_LegacyInlineRequestRejected(t *testing.T) {
	r, _, _ := setupTestRouter(t)
	body := []byte(`{"agent":{"name":"coder","description":"d","model":"m","systemPrompt":"p"},"provider":{"protocol":"anthropic-messages","baseUrl":"https://x","lockedApiKey":"k"},"runtime_token":"tok"}`)
	w := doRequest(t, r, http.MethodPost, "/api/v1/agents", body)
	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "legacy")
	assert.Contains(t, w.Body.String(), "rootAgentId")
}

// TestCreateAgent_RuntimeIncompatibleReturns503 guards the runtime image
// floor: a :latest runtime image without the explicit assume-latest opt-in
// must refuse graph deployments with 503, never silently deploy to an old
// runtime.
func TestCreateAgent_RuntimeIncompatibleReturns503(t *testing.T) {
	r, _, _ := setupTestRouterWithImage(t, "open-agent-runtime:latest", false)
	w := doRequest(t, r, http.MethodPost, "/api/v1/agents", validRequestBody())
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.NotContains(t, w.Body.String(), "sk-test", "error must not echo provider credentials")
	assert.NotContains(t, w.Body.String(), "test-runtime-token", "error must not echo the runtime token")
}

// TestCreateAgent_DisallowedToolsRoundTripsToYAML guards issue #16 regression
// #4: child disallowedTools must survive the full HTTP → service → storage
// pipeline into agents.yaml.
func TestCreateAgent_DisallowedToolsRoundTripsToYAML(t *testing.T) {
	r, svc, _ := setupTestRouter(t)
	body, _ := json.Marshal(model.CreateAgentRequest{
		RootAgentID: "parent",
		Agents: []model.AgentDefinition{
			{
				Name: "parent", Description: "d", Model: "claude-sonnet-4-6", SystemPrompt: "s",
				Subagents: []string{"child-a"},
			},
			{
				Name: "child-a", Description: "c", SystemPrompt: "p",
				DisallowedTools: []string{"Bash", "Write"},
			},
		},
		Provider:     model.ProviderConfig{Protocol: "anthropic-messages", BaseURL: "https://api.anthropic.com", APIKey: "sk-test"},
		RuntimeToken: "test-runtime-token",
	})
	w := doRequest(t, r, http.MethodPost, "/api/v1/agents", body)
	require.Equal(t, http.StatusCreated, w.Code, "body=%s", w.Body.String())

	data, err := os.ReadFile(filepath.Join(svc.Config().DataDir, "parent", "agents", "agents.yaml"))
	require.NoError(t, err)
	var doc struct {
		Agents []struct {
			ID              string   `yaml:"id"`
			DisallowedTools []string `yaml:"disallowedTools"`
		} `yaml:"agents"`
	}
	require.NoError(t, yaml.Unmarshal(data, &doc))
	require.Len(t, doc.Agents, 2)
	var childDisallowed []string
	for _, a := range doc.Agents {
		if a.ID == "child-a" {
			childDisallowed = a.DisallowedTools
		}
	}
	assert.Equal(t, []string{"Bash", "Write"}, childDisallowed)
}
