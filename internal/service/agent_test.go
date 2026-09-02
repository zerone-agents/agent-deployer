package service

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/zerone-agent/agent-deployer/internal/config"
	"github.com/zerone-agent/agent-deployer/internal/docker"
	"github.com/zerone-agent/agent-deployer/internal/model"
)

// fakeDockerClient is a test double for the DockerClient interface.
type fakeDockerClient struct {
	existing   *docker.RuntimeContainer
	created    *docker.CreateOpts
	stoppedID  string
	removedID  string
	removed    bool
	listResult []docker.RuntimeContainer
	createErr  error
	findErr    error
}

func (f *fakeDockerClient) FindAgentContainer(ctx context.Context, agentName string) (*docker.RuntimeContainer, error) {
	if f.findErr != nil {
		return nil, f.findErr
	}
	if f.existing != nil && f.existing.AgentName != "" && f.existing.AgentName != agentName {
		return nil, nil
	}
	return f.existing, nil
}

func (f *fakeDockerClient) ListManagedContainers(ctx context.Context) ([]docker.RuntimeContainer, error) {
	return f.listResult, nil
}

func (f *fakeDockerClient) CreateAgentContainer(ctx context.Context, opts docker.CreateOpts) (string, int, error) {
	f.created = &opts
	if f.createErr != nil {
		return "", 0, f.createErr
	}
	return "container-id-123", 32768, nil
}

func (f *fakeDockerClient) StopContainer(ctx context.Context, id string) error {
	f.stoppedID = id
	return nil
}

func (f *fakeDockerClient) RestartContainer(ctx context.Context, id string) error { return nil }

func (f *fakeDockerClient) RemoveContainer(ctx context.Context, id string) error {
	f.removedID = id
	f.removed = true
	return nil
}

func (f *fakeDockerClient) InspectContainer(ctx context.Context, containerID string) (*docker.RuntimeContainer, error) {
	if f.existing != nil && f.existing.ID == containerID {
		rc := *f.existing
		if rc.Health == "" {
			rc.Health = "healthy"
		}
		return &rc, nil
	}
	return &docker.RuntimeContainer{ID: containerID, Health: "healthy"}, nil
}

func (f *fakeDockerClient) ContainerLogs(_ context.Context, id string, tail int) (string, error) {
	return "fake log line\n", nil
}

// validRequest returns a CreateAgentRequest with all required fields populated.
func validRequest() *model.CreateAgentRequest {
	return &model.CreateAgentRequest{
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
	}
}

// newTestService constructs an AgentService rooted at a temporary data dir.
// The default runtime image opts into the assume-latest gate so graph
// deployments pass; the image-gate tests override these fields.
func newTestService(t *testing.T, fake *fakeDockerClient) (*AgentService, string) {
	t.Helper()
	dataDir := t.TempDir()
	cfg := &config.Config{
		DataDir:                  dataDir,
		Port:                     8080,
		RuntimeImage:             "open-agent-runtime:latest",
		RuntimeContainerPort:     3000,
		RuntimeImageAssumeLatest: true,
	}
	return NewAgentService(cfg, fake), dataDir
}

// TestAgentService_Create_WritesProviderCredentialsToYAML guards the
// service→storage passthrough: the provider triple from the request must end
// up in the main agent entry of the runtime agents.yaml.
func TestAgentService_Create_WritesProviderCredentialsToYAML(t *testing.T) {
	fake := &fakeDockerClient{}
	svc, dataDir := newTestService(t, fake)

	req := validRequest()
	req.Provider = model.ProviderConfig{
		Protocol: "openai-completions",
		BaseURL:  "https://api.deepseek.com",
		APIKey:   "sk-e2e",
	}

	_, _, err := svc.Create(context.Background(), req)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dataDir, "coder", "agents", "agents.yaml"))
	require.NoError(t, err)

	var doc map[string]interface{}
	require.NoError(t, yaml.Unmarshal(data, &doc))
	main := doc["agents"].([]interface{})[0].(map[string]interface{})
	assert.Equal(t, "sk-e2e", main["apiKey"])
	assert.Equal(t, "https://api.deepseek.com", main["baseURL"])
	assert.Equal(t, "openai-completions", main["apiType"])
}

func TestAgentService_Create_NewAgent(t *testing.T) {
	fake := &fakeDockerClient{}
	svc, dataDir := newTestService(t, fake)

	resp, created, err := svc.Create(context.Background(), validRequest())
	if err != nil {
		t.Fatalf("Create returned unexpected error: %v", err)
	}
	if !created {
		t.Errorf("created = false on new Create, want true")
	}

	if resp.AgentName != "coder" {
		t.Errorf("AgentName = %q, want %q", resp.AgentName, "coder")
	}
	if resp.ContainerID != "container-id-123" {
		t.Errorf("ContainerID = %q, want %q", resp.ContainerID, "container-id-123")
	}
	if resp.HostPort != 32768 {
		t.Errorf("HostPort = %d, want %d", resp.HostPort, 32768)
	}
	if resp.Status != model.StatusRunning {
		t.Errorf("Status = %q, want %q", resp.Status, model.StatusRunning)
	}
	if resp.InstanceID == "" || len(resp.InstanceID) != 8 {
		t.Errorf("InstanceID = %q, want 8 hex chars", resp.InstanceID)
	}
	if resp.YamlPath != filepath.Join(dataDir, "coder", "agents", "agents.yaml") {
		t.Errorf("YamlPath = %q", resp.YamlPath)
	}

	if fake.created == nil {
		t.Fatalf("CreateAgentContainer was not called")
	}
	opts := fake.created
	if opts.AgentName != "coder" {
		t.Errorf("opts.AgentName = %q, want %q", opts.AgentName, "coder")
	}
	if opts.Image != "open-agent-runtime:latest" {
		t.Errorf("opts.Image = %q, want %q", opts.Image, "open-agent-runtime:latest")
	}
	if opts.RuntimeContainerPort != 3000 {
		t.Errorf("opts.RuntimeContainerPort = %d, want 3000", opts.RuntimeContainerPort)
	}
	if opts.AgentDir != filepath.Join(dataDir, "coder", "agents") {
		t.Errorf("opts.AgentDir = %q, want %q", opts.AgentDir, filepath.Join(dataDir, "coder", "agents"))
	}
	if opts.SessionDir != filepath.Join(dataDir, "coder", "sessions") {
		t.Errorf("opts.SessionDir = %q, want %q", opts.SessionDir, filepath.Join(dataDir, "coder", "sessions"))
	}

	// Runtime token is supplied by the caller and injected into the runtime
	// container's env unchanged.
	if resp.RuntimeToken != "test-runtime-token" {
		t.Errorf("resp.RuntimeToken = %q, want test-runtime-token", resp.RuntimeToken)
	}
	if fake.created.RuntimeToken != "test-runtime-token" {
		t.Errorf("docker opts RuntimeToken = %q, want test-runtime-token", fake.created.RuntimeToken)
	}

	// Verify agents.yaml exists on disk.
	yamlPath := filepath.Join(dataDir, "coder", "agents", "agents.yaml")
	if _, err := os.Stat(yamlPath); err != nil {
		t.Errorf("agents.yaml was not written: %v", err)
	}
}

func TestAgentService_Create_ExistingWithoutForce(t *testing.T) {
	existing := &docker.RuntimeContainer{
		ID:         "existing-id",
		Name:       "cloud-agent-coder-oldid",
		AgentName:  "coder",
		InstanceID: "oldid",
		Status:     "running",
		HostPort:   30000,
	}
	fake := &fakeDockerClient{existing: existing}
	svc, _ := newTestService(t, fake)

	req := validRequest() // Force defaults to false
	resp, created, err := svc.Create(context.Background(), req)
	if err != nil {
		t.Fatalf("Create returned unexpected error: %v", err)
	}

	if resp.ContainerID != "existing-id" {
		t.Errorf("ContainerID = %q, want %q", resp.ContainerID, "existing-id")
	}
	if resp.AgentName != "coder" {
		t.Errorf("AgentName = %q, want %q", resp.AgentName, "coder")
	}
	if created {
		t.Errorf("created = true on idempotent Create, want false")
	}
	if fake.created != nil {
		t.Errorf("CreateAgentContainer should not be called when existing and !Force; got opts %+v", fake.created)
	}
	if fake.stoppedID != "" {
		t.Errorf("StopContainer should not be called when !Force; got %q", fake.stoppedID)
	}
}

func TestAgentService_Create_ExistingWithForce(t *testing.T) {
	existing := &docker.RuntimeContainer{
		ID:         "existing-id",
		Name:       "cloud-agent-coder-oldid",
		AgentName:  "coder",
		InstanceID: "oldid",
		Status:     "running",
		HostPort:   30000,
	}
	fake := &fakeDockerClient{existing: existing}
	svc, _ := newTestService(t, fake)

	req := validRequest()
	req.Force = true

	resp, created, err := svc.Create(context.Background(), req)
	if err != nil {
		t.Fatalf("Create returned unexpected error: %v", err)
	}

	if !created {
		t.Errorf("created = false on Force-rebuild, want true")
	}
	if fake.stoppedID != "existing-id" {
		t.Errorf("StopContainer id = %q, want %q", fake.stoppedID, "existing-id")
	}
	if fake.removedID != "existing-id" {
		t.Errorf("RemoveContainer id = %q, want %q", fake.removedID, "existing-id")
	}
	if fake.created == nil {
		t.Fatalf("CreateAgentContainer was not called")
	}
	if resp.ContainerID != "container-id-123" {
		t.Errorf("ContainerID = %q, want %q", resp.ContainerID, "container-id-123")
	}
}

func TestAgentService_Create_ProvidedToken(t *testing.T) {
	fake := &fakeDockerClient{}
	svc, _ := newTestService(t, fake)

	req := validRequest()
	req.RuntimeToken = "my-custom-token-123"

	resp, created, err := svc.Create(context.Background(), req)
	require.NoError(t, err)
	require.True(t, created)
	require.NotNil(t, fake.created)

	assert.Equal(t, "my-custom-token-123", fake.created.RuntimeToken,
		"provided runtime token must be used as-is")
	assert.Equal(t, "my-custom-token-123", resp.RuntimeToken)
}

func TestAgentService_Create_InvalidRequest(t *testing.T) {
	fake := &fakeDockerClient{}
	svc, _ := newTestService(t, fake)

	// Missing required Name on the root agent definition.
	req := &model.CreateAgentRequest{
		RootAgentID: "coder",
		Agents: []model.AgentDefinition{
			{
				Name:         "",
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
	}

	_, _, err := svc.Create(context.Background(), req)
	if err == nil {
		t.Fatalf("Create should return a validation error for invalid request")
	}
	if fake.created != nil {
		t.Errorf("CreateAgentContainer must not be called on validation failure")
	}
}

func TestAgentService_Create_MissingRuntimeToken(t *testing.T) {
	fake := &fakeDockerClient{}
	svc, _ := newTestService(t, fake)

	req := validRequest()
	req.RuntimeToken = ""

	_, _, err := svc.Create(context.Background(), req)
	if err == nil {
		t.Fatalf("Create should return a validation error for missing runtime token")
	}
	if fake.created != nil {
		t.Errorf("CreateAgentContainer must not be called when runtime token is missing")
	}
}

func TestAgentService_Get_Found(t *testing.T) {
	existing := &docker.RuntimeContainer{
		ID:         "id-abc",
		Name:       "cloud-agent-coder-xyz",
		AgentName:  "coder",
		InstanceID: "xyz",
		Status:     "running",
		HostPort:   32100,
		CreatedAt:  "2026-06-25T10:00:00Z",
	}
	fake := &fakeDockerClient{existing: existing}
	svc, dataDir := newTestService(t, fake)

	resp, err := svc.Get(context.Background(), "coder")
	if err != nil {
		t.Fatalf("Get returned unexpected error: %v", err)
	}
	if resp.ContainerID != "id-abc" {
		t.Errorf("ContainerID = %q, want %q", resp.ContainerID, "id-abc")
	}
	if resp.AgentName != "coder" {
		t.Errorf("AgentName = %q, want %q", resp.AgentName, "coder")
	}
	if resp.Status != model.StatusRunning {
		t.Errorf("Status = %q, want %q", resp.Status, model.StatusRunning)
	}
	if resp.HostPort != 32100 {
		t.Errorf("HostPort = %d, want %d", resp.HostPort, 32100)
	}
	// Persistent path fields must be derived from cfg.DataDir + agentName, and
	// CreatedAt must come from the Docker label.
	if resp.YamlPath != filepath.Join(dataDir, "coder", "agents", "agents.yaml") {
		t.Errorf("YamlPath = %q, want %q", resp.YamlPath, filepath.Join(dataDir, "coder", "agents", "agents.yaml"))
	}
	if resp.SessionDir != filepath.Join(dataDir, "coder", "sessions") {
		t.Errorf("SessionDir = %q, want %q", resp.SessionDir, filepath.Join(dataDir, "coder", "sessions"))
	}
	if resp.SkillsDir != filepath.Join(dataDir, "coder", "agents", "skills") {
		t.Errorf("SkillsDir = %q, want %q", resp.SkillsDir, filepath.Join(dataDir, "coder", "agents", "skills"))
	}
	if resp.CreatedAt != "2026-06-25T10:00:00Z" {
		t.Errorf("CreatedAt = %q, want %q", resp.CreatedAt, "2026-06-25T10:00:00Z")
	}
}

func TestAgentService_Get_NotFound(t *testing.T) {
	fake := &fakeDockerClient{}
	svc, _ := newTestService(t, fake)

	_, err := svc.Get(context.Background(), "coder")
	if err == nil {
		t.Fatalf("Get should return an error when no container exists")
	}
}

func TestAgentService_Stop(t *testing.T) {
	existing := &docker.RuntimeContainer{
		ID:        "id-stop",
		AgentName: "coder",
		Status:    "running",
	}
	fake := &fakeDockerClient{existing: existing}
	svc, _ := newTestService(t, fake)

	if err := svc.Stop(context.Background(), "coder"); err != nil {
		t.Fatalf("Stop returned unexpected error: %v", err)
	}
	if fake.stoppedID != "id-stop" {
		t.Errorf("StopContainer id = %q, want %q", fake.stoppedID, "id-stop")
	}
}

func TestAgentService_Delete_RemoveContainer(t *testing.T) {
	existing := &docker.RuntimeContainer{
		ID:        "id-del",
		AgentName: "coder",
		Status:    "running",
	}
	fake := &fakeDockerClient{existing: existing}
	svc, _ := newTestService(t, fake)

	if err := svc.Delete(context.Background(), "coder", false); err != nil {
		t.Fatalf("Delete returned unexpected error: %v", err)
	}
	if fake.stoppedID != "id-del" {
		t.Errorf("StopContainer id = %q, want %q", fake.stoppedID, "id-del")
	}
	if fake.removedID != "id-del" {
		t.Errorf("RemoveContainer id = %q, want %q", fake.removedID, "id-del")
	}
}

func TestAgentService_Delete_ArchivedIdempotent(t *testing.T) {
	// No container, but data exists on disk. A non-purge Delete must succeed
	// (idempotent) and leave the data in place, turning the agent into archived.
	fake := &fakeDockerClient{}
	svc, dataDir := newTestService(t, fake)

	agentRoot := filepath.Join(dataDir, "coder")
	require.NoError(t, os.MkdirAll(filepath.Join(agentRoot, "agents"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(agentRoot, "agents", "agents.yaml"), []byte("x"), 0644))

	if err := svc.Delete(context.Background(), "coder", false); err != nil {
		t.Fatalf("Delete on archived agent returned unexpected error: %v", err)
	}
	if fake.stoppedID != "" {
		t.Errorf("StopContainer should not be called when no container exists; got %q", fake.stoppedID)
	}
	if fake.removedID != "" {
		t.Errorf("RemoveContainer should not be called when no container exists; got %q", fake.removedID)
	}
	if _, err := os.Stat(agentRoot); os.IsNotExist(err) {
		t.Errorf("agent root dir should still exist after non-purge Delete")
	}
}

func TestAgentService_Delete_Purge(t *testing.T) {
	existing := &docker.RuntimeContainer{
		ID:        "id-del2",
		AgentName: "coder",
		Status:    "running",
	}
	fake := &fakeDockerClient{existing: existing}
	svc, dataDir := newTestService(t, fake)

	// Pre-create the per-agent directory so we can verify it is removed.
	agentRoot := filepath.Join(dataDir, "coder")
	if err := os.MkdirAll(filepath.Join(agentRoot, "agents"), 0755); err != nil {
		t.Fatalf("mkdir agentRoot: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(agentRoot, "sessions"), 0755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}

	if err := svc.Delete(context.Background(), "coder", true); err != nil {
		t.Fatalf("Delete returned unexpected error: %v", err)
	}

	if fake.removedID != "id-del2" {
		t.Errorf("RemoveContainer id = %q, want %q", fake.removedID, "id-del2")
	}
	if _, err := os.Stat(agentRoot); !os.IsNotExist(err) {
		t.Errorf("agent root dir should be removed; stat err = %v", err)
	}
}

func TestAgentService_List(t *testing.T) {
	listResult := []docker.RuntimeContainer{
		{
			ID:         "id-1",
			Name:       "cloud-agent-coder-aaa",
			AgentName:  "coder",
			InstanceID: "aaa",
			Status:     "running",
			HostPort:   31000,
			CreatedAt:  "2026-06-25T10:00:00Z",
		},
		{
			ID:         "id-2",
			Name:       "cloud-agent-writer-bbb",
			AgentName:  "writer",
			InstanceID: "bbb",
			Status:     "exited",
			HostPort:   0,
			CreatedAt:  "2026-06-25T11:00:00Z",
		},
	}
	fake := &fakeDockerClient{listResult: listResult}
	svc, dataDir := newTestService(t, fake)

	responses, err := svc.List(context.Background(), false)
	if err != nil {
		t.Fatalf("List returned unexpected error: %v", err)
	}
	if len(responses) != 2 {
		t.Fatalf("len(responses) = %d, want 2", len(responses))
	}
	if responses[0].ContainerID != "id-1" || responses[0].AgentName != "coder" {
		t.Errorf("responses[0] = %+v", responses[0])
	}
	if responses[0].Status != model.StatusRunning {
		t.Errorf("responses[0].Status = %q, want %q", responses[0].Status, model.StatusRunning)
	}
	if responses[1].Status != model.StatusExited {
		t.Errorf("responses[1].Status = %q, want %q", responses[1].Status, model.StatusExited)
	}
	// Persistent fields must be filled from cfg.DataDir + agentName + CreatedAt label.
	if responses[0].YamlPath != filepath.Join(dataDir, "coder", "agents", "agents.yaml") {
		t.Errorf("responses[0].YamlPath = %q", responses[0].YamlPath)
	}
	if responses[0].SessionDir != filepath.Join(dataDir, "coder", "sessions") {
		t.Errorf("responses[0].SessionDir = %q", responses[0].SessionDir)
	}
	if responses[0].SkillsDir != filepath.Join(dataDir, "coder", "agents", "skills") {
		t.Errorf("responses[0].SkillsDir = %q", responses[0].SkillsDir)
	}
	if responses[0].CreatedAt != "2026-06-25T10:00:00Z" {
		t.Errorf("responses[0].CreatedAt = %q", responses[0].CreatedAt)
	}
	if responses[1].CreatedAt != "2026-06-25T11:00:00Z" {
		t.Errorf("responses[1].CreatedAt = %q", responses[1].CreatedAt)
	}
}

// TestAgentService_List_IncludeArchived verifies that archived agents (container
// gone but data on disk) appear when includeArchived is true and are hidden
// otherwise. Active containers always win over archived entries of the same name.
func TestAgentService_List_IncludeArchived(t *testing.T) {
	listResult := []docker.RuntimeContainer{
		{
			ID:        "id-active",
			AgentName: "active-agent",
			Status:    "running",
			HostPort:  31000,
		},
	}
	fake := &fakeDockerClient{listResult: listResult}
	svc, dataDir := newTestService(t, fake)

	// Simulate an archived agent: no container, but yaml exists on disk.
	archivedName := "archived-agent"
	archivedRoot := filepath.Join(dataDir, archivedName)
	require.NoError(t, os.MkdirAll(filepath.Join(archivedRoot, "agents"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(archivedRoot, "agents", "agents.yaml"), []byte("x"), 0644))

	// Default List: only active containers.
	responses, err := svc.List(context.Background(), false)
	require.NoError(t, err)
	assert.Len(t, responses, 1, "default List must not include archived agents")
	assert.Equal(t, "active-agent", responses[0].AgentName)
	assert.Equal(t, model.StatusRunning, responses[0].Status)

	// includeArchived=true: both active and archived.
	responses, err = svc.List(context.Background(), true)
	require.NoError(t, err)
	require.Len(t, responses, 2, "includeArchived must merge active + archived")

	archived := responses[0]
	if archived.AgentName != archivedName {
		archived = responses[1]
	}
	assert.Equal(t, archivedName, archived.AgentName)
	assert.Equal(t, model.StatusArchived, archived.Status)
	assert.Empty(t, archived.ContainerID, "archived agent must have no container id")
	assert.Equal(t, 0, archived.HostPort, "archived agent must have no host port")
	assert.Equal(t, filepath.Join(dataDir, archivedName, "agents", "agents.yaml"), archived.YamlPath)
	assert.Equal(t, filepath.Join(dataDir, archivedName, "sessions"), archived.SessionDir)
	assert.Equal(t, filepath.Join(dataDir, archivedName, "agents", "skills"), archived.SkillsDir)
}

// TestAgentService_Get_Archived verifies that Get returns an archived agent
// (status="archived") when the container is gone but on-disk data remains.
func TestAgentService_Get_Archived(t *testing.T) {
	fake := &fakeDockerClient{}
	svc, dataDir := newTestService(t, fake)

	agentRoot := filepath.Join(dataDir, "coder")
	require.NoError(t, os.MkdirAll(filepath.Join(agentRoot, "agents"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(agentRoot, "agents", "agents.yaml"), []byte("x"), 0644))

	resp, err := svc.Get(context.Background(), "coder")
	require.NoError(t, err)
	assert.Equal(t, "coder", resp.AgentName)
	assert.Equal(t, model.StatusArchived, resp.Status)
	assert.Empty(t, resp.ContainerID)
	assert.Equal(t, filepath.Join(dataDir, "coder", "agents", "agents.yaml"), resp.YamlPath)
}

// skillZip builds an in-memory zip with the given files and returns the bytes
// and their sha256 hex. Used by service-level skill install tests.
func skillZip(t *testing.T, files map[string]string) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, body := range files {
		f, err := w.Create(name)
		require.NoError(t, err)
		_, err = f.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	sum := sha256.Sum256(buf.Bytes())
	return buf.Bytes(), hex.EncodeToString(sum[:])
}

func TestAgentService_Create_InstallsSkills(t *testing.T) {
	zipBytes, zipHash := skillZip(t, map[string]string{
		"SKILL.md": "# code-review\n",
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(zipBytes)
	}))
	defer srv.Close()

	fake := &fakeDockerClient{}
	svc, dataDir := newTestService(t, fake)

	req := validRequest()
	req.Agents[0].SettingSources = []string{"user"}
	req.Agents[0].Skills = []model.SkillSource{
		{Name: "code-review", URL: srv.URL, Hash: zipHash},
	}
	resp, _, err := svc.Create(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, "container-id-123", resp.ContainerID)

	// Skill extracted to the per-agent dir: <agentsDir>/skills/coder/code-review/SKILL.md
	got, err := os.ReadFile(filepath.Join(dataDir, "coder", "agents", "skills", "coder", "code-review", "SKILL.md"))
	require.NoError(t, err)
	assert.Equal(t, "# code-review\n", string(got))

	// Container was created AFTER skill install succeeded.
	require.NotNil(t, fake.created)
}

func TestAgentService_Create_SkillFailure_AbortsBeforeContainerCreate(t *testing.T) {
	// Serve HTTP 500 to force a download failure.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	fake := &fakeDockerClient{}
	svc, _ := newTestService(t, fake)

	req := validRequest()
	req.Agents[0].SettingSources = []string{"user"}
	req.Agents[0].Skills = []model.SkillSource{
		{Name: "broken", URL: srv.URL, Hash: strings.Repeat("a", 64)},
	}
	_, _, err := svc.Create(context.Background(), req)
	require.Error(t, err)
	assert.Nil(t, fake.created, "container must NOT be created when skill install fails")
	assert.Contains(t, err.Error(), "broken")
}

func TestAgentService_Create_SkillFailure_PreservesAlreadyDownloadedSkills(t *testing.T) {
	// Two skills: one succeeds, one fails.
	goodZip, goodHash := skillZip(t, map[string]string{"SKILL.md": "good\n"})
	goodSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(goodZip)
	}))
	defer goodSrv.Close()
	badSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer badSrv.Close()

	fake := &fakeDockerClient{}
	svc, dataDir := newTestService(t, fake)

	req := validRequest()
	req.Agents[0].SettingSources = []string{"user"}
	req.Agents[0].Skills = []model.SkillSource{
		{Name: "good", URL: goodSrv.URL, Hash: goodHash},
		{Name: "bad", URL: badSrv.URL, Hash: strings.Repeat("a", 64)},
	}
	_, _, err := svc.Create(context.Background(), req)
	require.Error(t, err)
	assert.Nil(t, fake.created)

	// The "good" skill is preserved on disk — it's a legit cache.
	got, readErr := os.ReadFile(filepath.Join(dataDir, "coder", "agents", "skills", "coder", "good", "SKILL.md"))
	require.NoError(t, readErr)
	assert.Equal(t, "good\n", string(got))
}

// TestAgentService_Create_NoSkills_DoesNotInvokeInstaller is a regression
// guard for the (common) case where the request declares no skills. The
// skill install loop must be a no-op and not interfere with container creation.
func TestAgentService_Create_NoSkills_DoesNotInvokeInstaller(t *testing.T) {
	fake := &fakeDockerClient{}
	svc, _ := newTestService(t, fake)

	req := validRequest() // validRequest does not set Skills
	require.Empty(t, req.Agents[0].Skills, "test precondition: validRequest must have no skills")

	resp, _, err := svc.Create(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, "container-id-123", resp.ContainerID)
	require.NotNil(t, fake.created, "container must be created normally when no skills")
}

// TestAgentService_Get_OmitsRuntimeToken verifies the token is NEVER returned
// by Get. The deployer intentionally does not persist the token; clients must
// store it at Create time.
func TestAgentService_Get_OmitsRuntimeToken(t *testing.T) {
	existing := &docker.RuntimeContainer{
		ID:        "id-abc",
		AgentName: "coder",
		Status:    "running",
	}
	fake := &fakeDockerClient{existing: existing}
	svc, _ := newTestService(t, fake)

	resp, err := svc.Get(context.Background(), "coder")
	require.NoError(t, err)
	assert.Empty(t, resp.RuntimeToken,
		"Get must NOT return the runtime token; it is not persisted and only Create hands it out")
}

// TestAgentService_List_OmitsRuntimeToken verifies the token is NEVER returned
// by List. Returning N tokens in one response would magnify the leak surface.
func TestAgentService_List_OmitsRuntimeToken(t *testing.T) {
	listResult := []docker.RuntimeContainer{
		{ID: "id-1", AgentName: "coder", Status: "running"},
		{ID: "id-2", AgentName: "writer", Status: "running"},
	}
	fake := &fakeDockerClient{listResult: listResult}
	svc, _ := newTestService(t, fake)

	responses, err := svc.List(context.Background(), false)
	require.NoError(t, err)
	for i, r := range responses {
		assert.Empty(t, r.RuntimeToken,
			"responses[%d] (%s) must not contain runtime token; List never returns it",
			i, r.AgentName)
	}
}

// TestAgentService_Create_SkillsServedViaBindMount guards the issue #16
// layout change: skills are installed under <agentsDir>/skills/<agentId>/ and
// reach the container through the /app/config bind mount. The DockerClient
// interface no longer carries any copy-to-container operation, so a Create
// with skills succeeding proves nothing is copied post-create.
func TestAgentService_Create_SkillsServedViaBindMount(t *testing.T) {
	fake := &fakeDockerClient{}
	svc, dataDir := newTestService(t, fake)

	zipBytes, zipHash := skillZip(t, map[string]string{
		"demo-skill/SKILL.md": "# Demo Skill\n",
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(zipBytes)
	}))
	defer srv.Close()

	req := validRequest()
	req.Agents[0].SettingSources = []string{"user"}
	req.Agents[0].Skills = []model.SkillSource{
		{Name: "demo-skill", URL: srv.URL, Hash: zipHash},
	}

	resp, created, err := svc.Create(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, created)
	assert.Equal(t, "container-id-123", resp.ContainerID)

	// Response advertises both host-side and in-container skill roots.
	assert.Equal(t, filepath.Join(dataDir, "coder", "agents", "skills"), resp.SkillsDir)
	assert.Equal(t, "/app/config/skills", resp.ContainerSkillsDir)
}

func TestAgentService_Create_WithAigc(t *testing.T) {
	fake := &fakeDockerClient{}
	svc, dataDir := newTestService(t, fake)

	req := validRequest()
	req.Aigc = &model.AigcConfig{
		Enabled:         true,
		ContentProducer: "001191320118MAK93FC72D10001",
		SigningKey:      "secret-key",
		ModelCodes:      map[string]string{"glm-4.5": "0001"},
	}

	_, created, err := svc.Create(context.Background(), req)
	if err != nil {
		t.Fatalf("Create returned unexpected error: %v", err)
	}
	if !created {
		t.Fatalf("created = false, want true")
	}

	data, err := os.ReadFile(filepath.Join(dataDir, "coder", "agents", "agents.yaml"))
	if err != nil {
		t.Fatalf("read agents.yaml: %v", err)
	}
	content := string(data)
	for _, want := range []string{
		"aigc:",
		"enabled: true",
		"contentProducer: 001191320118MAK93FC72D10001",
		"signingKey: secret-key",
		"explicitHint: true",
		"glm-4.5: \"0001\"",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("agents.yaml missing %q; content:\n%s", want, content)
		}
	}
}

func TestAgentService_Create_WithoutAigc(t *testing.T) {
	fake := &fakeDockerClient{}
	svc, dataDir := newTestService(t, fake)

	_, _, err := svc.Create(context.Background(), validRequest())
	if err != nil {
		t.Fatalf("Create returned unexpected error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dataDir, "coder", "agents", "agents.yaml"))
	if err != nil {
		t.Fatalf("read agents.yaml: %v", err)
	}
	if strings.Contains(string(data), "aigc:") {
		t.Errorf("agents.yaml should not contain aigc section; content:\n%s", data)
	}
}

func TestAgentService_Create_WithHub(t *testing.T) {
	fake := &fakeDockerClient{}
	svc, dataDir := newTestService(t, fake)

	req := validRequest()
	req.Hub = &model.HubConfig{
		Enabled:     true,
		BaseURL:     "http://agent-hub:8080",
		ChatPushKey: "push-secret",
	}

	_, created, err := svc.Create(context.Background(), req)
	if err != nil {
		t.Fatalf("Create returned unexpected error: %v", err)
	}
	if !created {
		t.Fatalf("created = false, want true")
	}

	data, err := os.ReadFile(filepath.Join(dataDir, "coder", "agents", "agents.yaml"))
	if err != nil {
		t.Fatalf("read agents.yaml: %v", err)
	}
	content := string(data)
	for _, want := range []string{
		"hub:",
		"enabled: true",
		"baseUrl: http://agent-hub:8080",
		"chatPushKey: push-secret",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("agents.yaml missing %q; content:\n%s", want, content)
		}
	}
}

func TestAgentService_Create_WithoutHub(t *testing.T) {
	fake := &fakeDockerClient{}
	svc, dataDir := newTestService(t, fake)

	req := validRequest()
	req.Hub = &model.HubConfig{Enabled: false}

	_, _, err := svc.Create(context.Background(), req)
	if err != nil {
		t.Fatalf("Create returned unexpected error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dataDir, "coder", "agents", "agents.yaml"))
	if err != nil {
		t.Fatalf("read agents.yaml: %v", err)
	}
	if strings.Contains(string(data), "hub:") {
		t.Errorf("agents.yaml should not contain hub section; content:\n%s", data)
	}
}

// TestAgentService_Create_HubOrgForceRebuild guards the issue #7 acceptance
// criteria: the same agent name redeployed under a different tenant must get
// the new hub.org, a force rebuild without org must discard the old tenant,
// and neither path may leak the stale value into the rewritten agents.yaml.
func TestAgentService_Create_HubOrgForceRebuild(t *testing.T) {
	fake := &fakeDockerClient{}
	svc, dataDir := newTestService(t, fake)

	yamlPath := filepath.Join(dataDir, "coder", "agents", "agents.yaml")

	readYAML := func(t *testing.T) string {
		t.Helper()
		data, err := os.ReadFile(yamlPath)
		if err != nil {
			t.Fatalf("read agents.yaml: %v", err)
		}
		return string(data)
	}

	existing := func() *docker.RuntimeContainer {
		return &docker.RuntimeContainer{
			ID:         "existing-id",
			Name:       "cloud-agent-coder-oldid",
			AgentName:  "coder",
			InstanceID: "oldid",
			Status:     "running",
			HostPort:   30000,
		}
	}

	// Phase 1: initial deploy carries hub.org=tenant-a.
	req := validRequest()
	req.Hub = &model.HubConfig{
		Enabled:     true,
		BaseURL:     "http://agent-hub:8080",
		ChatPushKey: "push-secret",
		Org:         "tenant-a",
	}
	if _, _, err := svc.Create(context.Background(), req); err != nil {
		t.Fatalf("Create (initial) returned unexpected error: %v", err)
	}
	if got := readYAML(t); !strings.Contains(got, "org: tenant-a") {
		t.Errorf("agents.yaml missing org: tenant-a after initial deploy; content:\n%s", got)
	}

	// Phase 2: force rebuild under a different tenant must overwrite, not
	// merge or drop, the org.
	fake.existing = existing()
	req = validRequest()
	req.Force = true
	req.Hub = &model.HubConfig{
		Enabled:     true,
		BaseURL:     "http://agent-hub:8080",
		ChatPushKey: "push-secret",
		Org:         "tenant-b",
	}
	if _, _, err := svc.Create(context.Background(), req); err != nil {
		t.Fatalf("Create (force, tenant-b) returned unexpected error: %v", err)
	}
	got := readYAML(t)
	if !strings.Contains(got, "org: tenant-b") {
		t.Errorf("agents.yaml missing org: tenant-b after force rebuild; content:\n%s", got)
	}
	if strings.Contains(got, "tenant-a") {
		t.Errorf("agents.yaml still contains stale org tenant-a after force rebuild; content:\n%s", got)
	}

	// Phase 3: force rebuild without org discards the old tenant entirely
	// (hub then resolves the default tenant by deploy mode).
	fake.existing = existing()
	req = validRequest()
	req.Force = true
	req.Hub = &model.HubConfig{
		Enabled:     true,
		BaseURL:     "http://agent-hub:8080",
		ChatPushKey: "push-secret",
	}
	if _, _, err := svc.Create(context.Background(), req); err != nil {
		t.Fatalf("Create (force, no org) returned unexpected error: %v", err)
	}
	if got := readYAML(t); strings.Contains(got, "org:") {
		t.Errorf("agents.yaml should have no org after force rebuild without org; content:\n%s", got)
	}
}

// toolFile starts a server serving body and returns a matching ToolSource.
func toolFile(t *testing.T, name, fileName string, body []byte) (model.ToolSource, func()) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	sum := sha256.Sum256(body)
	return model.ToolSource{
		Name:     name,
		URL:      srv.URL,
		Hash:     hex.EncodeToString(sum[:]),
		FileName: fileName,
	}, srv.Close
}

func TestAgentService_Create_InstallsToolsAndEmitsYAMLPaths(t *testing.T) {
	body := []byte("export default { name: \"GetWeather\" }")
	src, stop := toolFile(t, "GetWeather", "downloaded.mjs", body)
	defer stop()

	fake := &fakeDockerClient{}
	svc, dataDir := newTestService(t, fake)

	req := validRequest()
	req.Agents[0].Tools = []string{"GetWeather"}
	req.Agents[0].CustomTools = []model.ToolSource{src}
	resp, _, err := svc.Create(context.Background(), req)
	require.NoError(t, err)

	// File installed under the config bind-mount directory.
	got, err := os.ReadFile(filepath.Join(dataDir, "coder", "agents", "tools", "GetWeather.mjs"))
	require.NoError(t, err)
	assert.Equal(t, string(body), string(got))

	// agents.yaml carries the relative path.
	data, err := os.ReadFile(filepath.Join(dataDir, "coder", "agents", "agents.yaml"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "./tools/GetWeather.mjs")

	// Response exposes toolsDir only when custom tools are declared.
	assert.Equal(t, filepath.Join(dataDir, "coder", "agents", "tools"), resp.ToolsDir)

	// Container was created (install succeeded first).
	require.NotNil(t, fake.created)
}

func TestAgentService_Create_ToolFailure_AbortsBeforeContainerCreate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	fake := &fakeDockerClient{}
	svc, _ := newTestService(t, fake)

	req := validRequest()
	req.Agents[0].CustomTools = []model.ToolSource{
		{Name: "broken", URL: srv.URL, Hash: strings.Repeat("a", 64), FileName: "b.js"},
	}
	_, _, err := svc.Create(context.Background(), req)
	require.Error(t, err)
	assert.Nil(t, fake.created, "container must NOT be created when tool install fails")
	assert.Contains(t, err.Error(), "broken")
}

func TestAgentService_Create_ToolFailure_LeavesEarlierToolsAsCache(t *testing.T) {
	good := []byte("export default { name: 'Good' }")
	goodSrc, stop := toolFile(t, "Good", "g.mjs", good)
	defer stop()
	badSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer badSrv.Close()

	fake := &fakeDockerClient{}
	svc, dataDir := newTestService(t, fake)

	req := validRequest()
	req.Agents[0].CustomTools = []model.ToolSource{
		goodSrc,
		{Name: "bad", URL: badSrv.URL, Hash: strings.Repeat("a", 64), FileName: "b.js"},
	}
	_, _, err := svc.Create(context.Background(), req)
	require.Error(t, err)
	assert.Nil(t, fake.created)

	// The successfully-installed tool stays on disk as a valid cache.
	got, readErr := os.ReadFile(filepath.Join(dataDir, "coder", "agents", "tools", "Good.mjs"))
	require.NoError(t, readErr)
	assert.Equal(t, string(good), string(got))
}

func TestAgentService_Create_NoCustomTools_NoToolsDir(t *testing.T) {
	fake := &fakeDockerClient{}
	svc, dataDir := newTestService(t, fake)

	resp, _, err := svc.Create(context.Background(), validRequest())
	require.NoError(t, err)
	assert.Empty(t, resp.ToolsDir, "toolsDir must be omitted when no custom tools declared")

	// agents.yaml has no customTools key.
	data, err := os.ReadFile(filepath.Join(dataDir, "coder", "agents", "agents.yaml"))
	require.NoError(t, err)
	assert.NotContains(t, string(data), "customTools")

	// tools/ directory is not created.
	if _, statErr := os.Stat(filepath.Join(dataDir, "coder", "agents", "tools")); statErr == nil {
		t.Fatal("tools dir must not be created when no custom tools declared")
	}
}

// TestAgentService_Create_InstallsArtifactsForWholeClosure verifies the
// issue #16 orchestration: artifacts are resolved per agent across the whole
// deployment graph — tools into the shared flat <agentsDir>/tools, skills
// into per-agent <agentsDir>/skills/<agentId>/ directories — and the YAML
// keeps each agent's own customTools / extraUserSkillDirs declarations.
func TestAgentService_Create_InstallsArtifactsForWholeClosure(t *testing.T) {
	zipBytes, zipHash := skillZip(t, map[string]string{"SKILL.md": "# child skill\n"})
	skillSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(zipBytes)
	}))
	defer skillSrv.Close()

	childToolBody := []byte("export default { name: 'child-tool' }")
	childTool, stopChild := toolFile(t, "child-tool", "ct.mjs", childToolBody)
	defer stopChild()
	rootToolBody := []byte("export default { name: 'root-tool' }")
	rootTool, stopRoot := toolFile(t, "root-tool", "rt.mjs", rootToolBody)
	defer stopRoot()

	fake := &fakeDockerClient{}
	svc, dataDir := newTestService(t, fake)

	req := validRequest()
	req.RootAgentID = "parent"
	req.Agents = []model.AgentDefinition{
		{
			Name:         "parent",
			Description:  "Coordinates work",
			Model:        "claude-sonnet-4-6",
			SystemPrompt: "Delegate",
			Subagents:    []string{"child-a"},
			CustomTools:  []model.ToolSource{rootTool},
		},
		{
			Name:           "child-a",
			Description:    "Research",
			SettingSources: []string{"user"},
			Skills:         []model.SkillSource{{Name: "skill-a", URL: skillSrv.URL, Hash: zipHash}},
			CustomTools:    []model.ToolSource{childTool},
		},
	}

	resp, created, err := svc.Create(context.Background(), req)
	require.NoError(t, err)
	require.True(t, created)
	assert.Equal(t, "container-id-123", resp.ContainerID)

	// Tools land in the shared flat <agentsDir>/tools.
	assert.FileExists(t, filepath.Join(dataDir, "parent", "agents", "tools", "root-tool.mjs"))
	assert.FileExists(t, filepath.Join(dataDir, "parent", "agents", "tools", "child-tool.mjs"))
	// Skills install per-agent; root declares none ⇒ no root skill dir.
	assert.FileExists(t, filepath.Join(dataDir, "parent", "agents", "skills", "child-a", "skill-a", "SKILL.md"))
	_, statErr := os.Stat(filepath.Join(dataDir, "parent", "agents", "skills", "parent"))
	assert.True(t, os.IsNotExist(statErr), "root declares no skills ⇒ no root skill dir")

	// YAML keeps per-agent declarations: root.customTools has only root-tool,
	// child-a.customTools has only child-tool, child-a gets its skill dir.
	yamlBytes, err := os.ReadFile(filepath.Join(dataDir, "parent", "agents", "agents.yaml"))
	require.NoError(t, err)
	var doc struct {
		Agents []map[string]any `yaml:"agents"`
	}
	require.NoError(t, yaml.Unmarshal(yamlBytes, &doc))
	require.Len(t, doc.Agents, 2)
	byID := map[string]map[string]any{}
	for _, e := range doc.Agents {
		byID[e["id"].(string)] = e
	}
	assert.Equal(t, []any{"./tools/root-tool.mjs"}, byID["parent"]["customTools"])
	assert.Equal(t, []any{"./tools/child-tool.mjs"}, byID["child-a"]["customTools"])
	assert.Equal(t, []any{"/app/config/skills/child-a"}, byID["child-a"]["extraUserSkillDirs"])
	_, rootHasExtra := byID["parent"]["extraUserSkillDirs"]
	assert.False(t, rootHasExtra, "root without skills gets no extraUserSkillDirs")
}

// TestAgentService_Create_RuntimeImageGate guards the runtime version floors
// for the agent graph protocol: the base v2.4.0 floor, raised to v2.6.0 when
// the graph declares the maxSessionQueries contract key (pre-2.6.0 runtimes
// silently strip it after the SDK 3.1.0 rename). Pinned tags below the floor
// and unassumed :latest images must refuse deployment instead of silently
// degrading.
func TestAgentService_Create_RuntimeImageGate(t *testing.T) {
	cases := []struct {
		name         string
		image        string
		assumeLatest bool
		declaresCap  bool
		wantErr      bool
	}{
		{"pinned 2.4.0 ok", "open-agent-runtime:v2.4.0", false, false, false},
		{"pinned 2.5.1 ok with registry prefix", "swr.cn-east-3.myhuaweicloud.com/zerone/runtime:v2.5.1", false, false, false},
		{"pinned 2.3.1 rejected", "open-agent-runtime:v2.3.1", false, false, true},
		{"latest rejected without assume", "open-agent-runtime:latest", false, false, true},
		{"latest allowed with assume", "open-agent-runtime:latest", true, false, false},
		{"untagged treated as latest", "open-agent-runtime", false, false, true},
		{"v2.5.0 rejects declared session cap", "open-agent-runtime:v2.5.0", false, true, true},
		{"v2.6.0 accepts declared session cap", "open-agent-runtime:v2.6.0", false, true, false},
		{"latest with assume accepts declared session cap", "open-agent-runtime:latest", true, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeDockerClient{}
			svc, _ := newTestService(t, fake)
			svc.Config().RuntimeImage = tc.image
			svc.Config().RuntimeImageAssumeLatest = tc.assumeLatest

			req := validRequest()
			if tc.declaresCap {
				queries := 50
				req.Agents[0].MaxSessionQueries = &queries
			}
			_, _, err := svc.Create(context.Background(), req)
			if tc.wantErr {
				require.ErrorIs(t, err, ErrRuntimeIncompatible)
				assert.Nil(t, fake.created, "container must not be created behind the gate")
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// TestAgentService_Create_SharedToolDedupedButReferencedPerAgent guards the
// issue #16 artifact contract: a tool declared identically by several agents
// downloads ONCE into the shared flat tools dir, while every declaring
// agent's YAML entry keeps its own customTools reference.
func TestAgentService_Create_SharedToolDedupedButReferencedPerAgent(t *testing.T) {
	body := []byte("export default { name: 'shared' }")
	var downloads int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&downloads, 1)
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	sum := sha256.Sum256(body)
	shared := model.ToolSource{
		Name:     "shared-tool",
		URL:      srv.URL,
		Hash:     hex.EncodeToString(sum[:]),
		FileName: "shared.mjs",
	}

	fake := &fakeDockerClient{}
	svc, dataDir := newTestService(t, fake)

	req := validRequest()
	req.RootAgentID = "parent"
	req.Agents = []model.AgentDefinition{
		{
			Name: "parent", Description: "d", Model: "claude-sonnet-4-6", SystemPrompt: "s",
			Subagents:   []string{"child-a"},
			CustomTools: []model.ToolSource{shared},
		},
		{
			Name: "child-a", Description: "c",
			CustomTools: []model.ToolSource{shared},
		},
	}

	_, _, err := svc.Create(context.Background(), req)
	require.NoError(t, err)

	assert.Equal(t, int32(1), atomic.LoadInt32(&downloads),
		"identical tool declarations across agents must download exactly once")

	// Exactly one file on disk...
	assert.FileExists(t, filepath.Join(dataDir, "parent", "agents", "tools", "shared-tool.mjs"))

	// ...but BOTH agents' entries reference it.
	data, err := os.ReadFile(filepath.Join(dataDir, "parent", "agents", "agents.yaml"))
	require.NoError(t, err)
	var doc struct {
		Agents []map[string]any `yaml:"agents"`
	}
	require.NoError(t, yaml.Unmarshal(data, &doc))
	require.Len(t, doc.Agents, 2)
	for _, e := range doc.Agents {
		assert.Equal(t, []any{"./tools/shared-tool.mjs"}, e["customTools"],
			"agent %v must keep its own customTools declaration", e["id"])
	}
}
