package storage

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zerone-agent/agent-deployer/internal/model"
	"gopkg.in/yaml.v3"
)

func strPtr(v string) *string { return &v }

func TestWriteAgentYAML_Runtime20Format(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewAgentStorage(tmpDir)

	subMaxTurns := 10
	agent := model.AgentDefinition{
		Name:         "coder",
		Description:  "Writes and edits code",
		Model:        "claude-sonnet-4-6",
		SystemPrompt: "You are a coding assistant.",
		Tools:        []string{"Read", "Write"},
		Subagents: []model.SubagentDefinition{
			{
				Name:        "reviewer",
				Description: "Review code",
				Prompt:      "You are a code reviewer.",
				Tools:       []string{"Read"},
				MaxTurns:    &subMaxTurns,
			},
		},
	}

	require.NoError(t, store.WriteAgentYAML("coder", agent, model.ProviderConfig{}, nil, nil))

	data, err := os.ReadFile(filepath.Join(tmpDir, "coder", "agents", "agents.yaml"))
	require.NoError(t, err)

	var doc map[string]interface{}
	require.NoError(t, yaml.Unmarshal(data, &doc))

	agents, ok := doc["agents"].([]interface{})
	require.True(t, ok, "top-level agents key should be a list")
	require.Len(t, agents, 2, "main agent + each subagent should be a top-level entry")

	main, ok := agents[0].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "coder", main["id"])
	assert.Equal(t, "Writes and edits code", main["description"],
		"runtime 2.0 requires description on every agent entry")
	assert.Equal(t, []interface{}{"reviewer"}, main["subagents"],
		"subagents should be an id reference list, not inline definitions")

	sub, ok := agents[1].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "reviewer", sub["id"])
	assert.Equal(t, "reviewer", sub["name"])
	assert.Equal(t, "Review code", sub["description"])
	assert.Equal(t, "You are a code reviewer.", sub["systemPrompt"])
	assert.Equal(t, []interface{}{"Read"}, sub["allowedTools"])
	assert.Equal(t, 10, sub["maxTurns"])
	_, hasModel := sub["model"]
	assert.False(t, hasModel, "subagent entry should not pin a model; mounting ignores it")
	_, hasSettingSources := sub["settingSources"]
	assert.False(t, hasSettingSources, "subagent entry should not carry settingSources")
}

func TestReadAgentYAML_Runtime20RoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewAgentStorage(tmpDir)

	subMaxTurns := 10
	agent := model.AgentDefinition{
		Name:         "coder",
		Description:  "Writes and edits code",
		Model:        "claude-sonnet-4-6",
		SystemPrompt: "You are a coding assistant.",
		Subagents: []model.SubagentDefinition{
			{Name: "reviewer", Description: "Review code", Prompt: "You are a code reviewer.", Tools: []string{"Read"}, MaxTurns: &subMaxTurns},
		},
	}

	require.NoError(t, store.WriteAgentYAML("coder", agent, model.ProviderConfig{}, nil, nil))

	readAgent, err := store.ReadAgentYAML("coder")
	require.NoError(t, err)
	assert.Equal(t, "coder", readAgent.Name)
	assert.Equal(t, "Writes and edits code", readAgent.Description)
	require.Len(t, readAgent.Subagents, 1)
	assert.Equal(t, agent.Subagents[0], readAgent.Subagents[0])
}

func TestReadAgentYAML_MainEntryFoundByIDNotPosition(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewAgentStorage(tmpDir)

	agentDir := filepath.Join(tmpDir, "coder", "agents")
	require.NoError(t, os.MkdirAll(agentDir, 0755))
	yamlBytes := []byte(`agents:
  - id: reviewer
    description: Review code
    systemPrompt: You are a code reviewer.
  - id: coder
    description: Writes and edits code
    model: claude-sonnet-4-6
    systemPrompt: You are a coding assistant.
    subagents:
      - reviewer
`)
	require.NoError(t, os.WriteFile(filepath.Join(agentDir, "agents.yaml"), yamlBytes, 0644))

	readAgent, err := store.ReadAgentYAML("coder")
	require.NoError(t, err)
	assert.Equal(t, "coder", readAgent.Name)
	assert.Equal(t, "Writes and edits code", readAgent.Description)
	assert.Equal(t, "claude-sonnet-4-6", readAgent.Model)
	require.Len(t, readAgent.Subagents, 1)
	assert.Equal(t, "reviewer", readAgent.Subagents[0].Name)
	assert.Equal(t, "Review code", readAgent.Subagents[0].Description)
	assert.Equal(t, "You are a code reviewer.", readAgent.Subagents[0].Prompt)
}

func TestWriteAndReadAgentYAML(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewAgentStorage(tmpDir)

	maxTurns := 20
	agent := model.AgentDefinition{
		Name:           "coder",
		Model:          "claude-sonnet-4-6",
		SystemPrompt:   "You are a coding assistant.",
		MaxTurns:       &maxTurns,
		PermissionMode: "auto",
		Tools:          []string{"Read", "Write"},
		Skills: []model.SkillSource{
			{Name: "code-review", URL: "https://example.com/cr.zip", Hash: strings.Repeat("a", 64)},
		},
		SettingSources: []string{"user", "project"},
		Subagents: []model.SubagentDefinition{
			{
				Name:        "reviewer",
				Description: "Review code",
				Prompt:      "You are a code reviewer.",
				Tools:       []string{"Read"},
				MaxTurns:    func() *int { v := 10; return &v }(),
			},
		},
		Datasets: map[string]string{
			"dataset-1": "Primary dataset for code review",
			"dataset-2": "Secondary dataset for testing",
		},
	}

	err := store.WriteAgentYAML("coder", agent, model.ProviderConfig{}, nil, nil)
	require.NoError(t, err)

	// Verify file is created at the correct path.
	expectedPath := filepath.Join(tmpDir, "coder", "agents", "agents.yaml")
	_, err = os.Stat(expectedPath)
	require.NoError(t, err, "agents.yaml should be created at expected path")

	// Read back and verify identity.
	readAgent, err := store.ReadAgentYAML("coder")
	require.NoError(t, err)
	assert.Equal(t, agent.Name, readAgent.Name)
	assert.Equal(t, agent.Model, readAgent.Model)
	assert.Equal(t, agent.SystemPrompt, readAgent.SystemPrompt)
	assert.Equal(t, *agent.MaxTurns, *readAgent.MaxTurns)
	assert.Equal(t, agent.PermissionMode, readAgent.PermissionMode)
	assert.Equal(t, agent.Tools, readAgent.Tools)
	assert.Empty(t, readAgent.Skills, "Skills should not be persisted to or read from YAML")
	require.Len(t, readAgent.Subagents, 1)
	assert.Equal(t, agent.Subagents[0].Name, readAgent.Subagents[0].Name)
	assert.Equal(t, agent.Subagents[0].Description, readAgent.Subagents[0].Description)
	assert.Equal(t, agent.Subagents[0].Prompt, readAgent.Subagents[0].Prompt)
	assert.Equal(t, agent.Subagents[0].Tools, readAgent.Subagents[0].Tools)
	assert.Equal(t, *agent.Subagents[0].MaxTurns, *readAgent.Subagents[0].MaxTurns)
	assert.Equal(t, agent.SettingSources, readAgent.SettingSources)
	assert.Equal(t, agent.Datasets, readAgent.Datasets)
}

func TestWriteAgentYAML_ContainsExpectedKeys(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewAgentStorage(tmpDir)

	maxTurns := 20
	agent := model.AgentDefinition{
		Name:           "coder",
		Model:          "claude-sonnet-4-6",
		SystemPrompt:   "You are a coding assistant.",
		MaxTurns:       &maxTurns,
		PermissionMode: "auto",
		Tools:          []string{"Read", "Write"},
		Skills: []model.SkillSource{
			{Name: "code-review", URL: "https://example.com/cr.zip", Hash: strings.Repeat("a", 64)},
		},
		SettingSources: []string{"user", "project"},
		Subagents: []model.SubagentDefinition{
			{
				Name:        "reviewer",
				Description: "Review code",
				Prompt:      "You are a code reviewer.",
				Tools:       []string{"Read"},
				MaxTurns:    func() *int { v := 10; return &v }(),
			},
		},
		Datasets: map[string]string{
			"dataset-1": "Primary dataset",
		},
	}

	err := store.WriteAgentYAML("coder", agent, model.ProviderConfig{}, nil, nil)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(tmpDir, "coder", "agents", "agents.yaml"))
	require.NoError(t, err)

	var doc map[string]interface{}
	err = yaml.Unmarshal(data, &doc)
	require.NoError(t, err)

	agents, ok := doc["agents"].([]interface{})
	require.True(t, ok, "top-level agents key should be a list")
	require.Len(t, agents, 2, "main agent + subagent as first-class entries")

	entry, ok := agents[0].(map[string]interface{})
	require.True(t, ok)

	assert.Equal(t, "coder", entry["id"])
	assert.Equal(t, "coder", entry["name"])
	assert.Equal(t, "claude-sonnet-4-6", entry["model"])
	assert.Equal(t, "You are a coding assistant.", entry["systemPrompt"])
	assert.Equal(t, "auto", entry["permissionMode"])
	assert.Equal(t, []interface{}{"Read", "Write"}, entry["allowedTools"])
	_, hasSkills := entry["skills"]
	assert.False(t, hasSkills, "skills field should not be present in YAML")
	assert.Equal(t, []interface{}{"user", "project"}, entry["settingSources"])

	datasets, ok := entry["datasets"].(map[string]interface{})
	require.True(t, ok, "datasets should be present")
	assert.Equal(t, "Primary dataset", datasets["dataset-1"])

	assert.Equal(t, []interface{}{"reviewer"}, entry["subagents"],
		"subagents should be an id reference list")

	reviewer, ok := agents[1].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "reviewer", reviewer["id"])
	assert.Equal(t, "Review code", reviewer["description"])
	assert.Equal(t, "You are a code reviewer.", reviewer["systemPrompt"])
	assert.Equal(t, []interface{}{"Read"}, reviewer["allowedTools"])
	assert.Equal(t, 10, reviewer["maxTurns"])
}

func TestWriteAgentYAML_NilMaxTurnsOmitted(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewAgentStorage(tmpDir)

	agent := model.AgentDefinition{
		Name:         "coder",
		Model:        "claude-sonnet-4-6",
		SystemPrompt: "You are a coding assistant.",
		MaxTurns:     nil,
	}

	err := store.WriteAgentYAML("coder", agent, model.ProviderConfig{}, nil, nil)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(tmpDir, "coder", "agents", "agents.yaml"))
	require.NoError(t, err)

	var doc map[string]interface{}
	err = yaml.Unmarshal(data, &doc)
	require.NoError(t, err)

	agents := doc["agents"].([]interface{})
	entry := agents[0].(map[string]interface{})
	_, present := entry["maxTurns"]
	assert.False(t, present, "maxTurns should be omitted when nil, runtime defaults apply")
}

func TestReadAgentYAML_NonExistent(t *testing.T) {
	dir := t.TempDir()
	s := NewAgentStorage(dir)
	_, err := s.ReadAgentYAML("missing")
	require.Error(t, err)
	assert.True(t, os.IsNotExist(err) || errors.Is(err, os.ErrNotExist),
		"expected a file-not-found error, got: %v", err)
}

func TestWriteAgentYAML_NameMismatch(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewAgentStorage(tmpDir)

	agent := model.AgentDefinition{
		Name: "coder",
	}
	err := store.WriteAgentYAML("different-name", agent, model.ProviderConfig{}, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not match storage name")
}

func TestWriteAgentYAML_EmptyName(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewAgentStorage(tmpDir)

	agent := model.AgentDefinition{
		Name: "",
	}
	err := store.WriteAgentYAML("", agent, model.ProviderConfig{}, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "agent.Name is required")
}

func TestWriteAgentYAML_PathTraversalRejected(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewAgentStorage(tmpDir)

	cases := []string{"../etc", "a/b", "/abs", ".", ".."}
	for _, bad := range cases {
		agent := model.AgentDefinition{Name: bad, Model: "m", SystemPrompt: "p"}
		err := store.WriteAgentYAML(bad, agent, model.ProviderConfig{}, nil, nil)
		require.Error(t, err, "name %q should be rejected", bad)
		assert.Contains(t, err.Error(), "must be a single path segment")
	}
}

func TestReadAgentYAML_EmptyAgentsList(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewAgentStorage(tmpDir)

	agentDir := filepath.Join(tmpDir, "empty", "agents")
	require.NoError(t, os.MkdirAll(agentDir, 0755))
	yamlBytes := []byte("agents: []\n")
	require.NoError(t, os.WriteFile(filepath.Join(agentDir, "agents.yaml"), yamlBytes, 0644))

	_, err := store.ReadAgentYAML("empty")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNoAgents), "expected ErrNoAgents, got: %v", err)
}

func TestReadAgentYAML_SubagentsPreserveDefinitionOrder(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewAgentStorage(tmpDir)

	agent := model.AgentDefinition{
		Name: "orchestrator",
		Subagents: []model.SubagentDefinition{
			{Name: "zeta", Description: "z desc"},
			{Name: "alpha", Description: "a desc"},
			{Name: "mid", Description: "m desc"},
		},
	}

	require.NoError(t, store.WriteAgentYAML("orchestrator", agent, model.ProviderConfig{}, nil, nil))

	readAgent, err := store.ReadAgentYAML("orchestrator")
	require.NoError(t, err)
	require.Len(t, readAgent.Subagents, 3)

	expected := []string{"zeta", "alpha", "mid"}
	actual := make([]string, len(readAgent.Subagents))
	for i, s := range readAgent.Subagents {
		actual[i] = s.Name
	}
	assert.Equal(t, expected, actual, "subagents should preserve the definition order of the id reference list")
}

func TestEnsureDirs_CreatesMultipleDirs(t *testing.T) {
	tmpDir := t.TempDir()
	dir1 := filepath.Join(tmpDir, "a")
	dir2 := filepath.Join(tmpDir, "b", "c")

	require.NoError(t, EnsureDirs(dir1, dir2))

	info, err := os.Stat(dir1)
	require.NoError(t, err)
	assert.True(t, info.IsDir())

	info, err = os.Stat(dir2)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestEnsureDirs_NestedParentsCreated(t *testing.T) {
	tmpDir := t.TempDir()
	deepDir := filepath.Join(tmpDir, "x", "y", "z", "w")

	require.NoError(t, EnsureDirs(deepDir))

	info, err := os.Stat(deepDir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestRemoveAll_RemovesDirectoryWithFiles(t *testing.T) {
	tmpDir := t.TempDir()
	target := filepath.Join(tmpDir, "to-remove")
	require.NoError(t, os.MkdirAll(target, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(target, "file.txt"), []byte("data"), 0644))

	require.NoError(t, RemoveAll(target))

	_, err := os.Stat(target)
	assert.True(t, os.IsNotExist(err), "directory should be removed")
}

func TestRemoveAll_NonExistentPathNoError(t *testing.T) {
	tmpDir := t.TempDir()
	missing := filepath.Join(tmpDir, "does-not-exist")
	assert.NoError(t, RemoveAll(missing))
}

func TestWriteAndReadAgentYAML_WithMcpServers(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewAgentStorage(tmpDir)

	maxTurns := 20
	agent := model.AgentDefinition{
		Name:           "coder",
		Model:          "claude-sonnet-4-6",
		SystemPrompt:   "You are a coding assistant.",
		MaxTurns:       &maxTurns,
		PermissionMode: "auto",
		Tools:          []string{"Read", "Write"},
		McpServers: map[string]model.McpServerConfig{
			"remote-api": {
				Type:    "sse",
				URL:     "https://api.example.com/sse",
				Headers: map[string]string{"Authorization": "Bearer xxx"},
			},
		},
		Subagents: []model.SubagentDefinition{
			{
				Name:        "reviewer",
				Description: "Review code",
				Prompt:      "You are a code reviewer.",
				Tools:       []string{"Read"},
			},
		},
	}

	err := store.WriteAgentYAML("coder", agent, model.ProviderConfig{}, nil, nil)
	require.NoError(t, err)

	readAgent, err := store.ReadAgentYAML("coder")
	require.NoError(t, err)

	require.Len(t, readAgent.McpServers, 1)
	mcp, ok := readAgent.McpServers["remote-api"]
	require.True(t, ok)
	assert.Equal(t, "sse", mcp.Type)
	assert.Equal(t, "https://api.example.com/sse", mcp.URL)
	assert.Equal(t, "Bearer xxx", mcp.Headers["Authorization"])

	require.Len(t, readAgent.Subagents, 1)
	assert.Equal(t, "reviewer", readAgent.Subagents[0].Name)
}

func TestWriteAgentYAML_McpServersUsesTransportField(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewAgentStorage(tmpDir)

	agent := model.AgentDefinition{
		Name:         "coder",
		Model:        "claude-sonnet-4-6",
		SystemPrompt: "You are a coding assistant.",
		McpServers: map[string]model.McpServerConfig{
			"remote-api": {
				Type:    "http",
				URL:     "https://api.example.com/mcp",
				Headers: map[string]string{"Authorization": "Bearer xxx"},
			},
		},
	}

	err := store.WriteAgentYAML("coder", agent, model.ProviderConfig{}, nil, nil)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(tmpDir, "coder", "agents", "agents.yaml"))
	require.NoError(t, err)

	var doc map[string]interface{}
	err = yaml.Unmarshal(data, &doc)
	require.NoError(t, err)

	agents := doc["agents"].([]interface{})
	entry := agents[0].(map[string]interface{})
	mcpServers := entry["mcpServers"].(map[string]interface{})
	remoteApi := mcpServers["remote-api"].(map[string]interface{})
	assert.Equal(t, "http", remoteApi["transport"])
	assert.Equal(t, "https://api.example.com/mcp", remoteApi["url"])
	headers := remoteApi["headers"].(map[string]interface{})
	assert.Equal(t, "Bearer xxx", headers["Authorization"])
}

func TestWriteAndReadAgentYAML_McpServersEmptyOmitted(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewAgentStorage(tmpDir)

	agent := model.AgentDefinition{
		Name:         "coder",
		Model:        "claude-sonnet-4-6",
		SystemPrompt: "You are a coding assistant.",
	}

	err := store.WriteAgentYAML("coder", agent, model.ProviderConfig{}, nil, nil)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(tmpDir, "coder", "agents", "agents.yaml"))
	require.NoError(t, err)

	var doc map[string]interface{}
	err = yaml.Unmarshal(data, &doc)
	require.NoError(t, err)

	agents := doc["agents"].([]interface{})
	entry := agents[0].(map[string]interface{})
	_, present := entry["mcpServers"]
	assert.False(t, present, "mcpServers should be omitted when empty")

	readAgent, err := store.ReadAgentYAML("coder")
	require.NoError(t, err)
	assert.Empty(t, readAgent.McpServers)
}

func TestWriteAgentYAML_SettingSourcesDefaultsToProject(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewAgentStorage(tmpDir)

	agent := model.AgentDefinition{
		Name:           "coder",
		Model:          "claude-sonnet-4-6",
		SystemPrompt:   "You are a coding assistant.",
		SettingSources: nil, // explicitly nil
	}

	require.NoError(t, store.WriteAgentYAML("coder", agent, model.ProviderConfig{}, nil, nil))

	data, err := os.ReadFile(filepath.Join(tmpDir, "coder", "agents", "agents.yaml"))
	require.NoError(t, err)

	var doc map[string]interface{}
	require.NoError(t, yaml.Unmarshal(data, &doc))

	agents := doc["agents"].([]interface{})
	entry := agents[0].(map[string]interface{})
	assert.Equal(t, []interface{}{"project"}, entry["settingSources"])
}

func TestWriteAgentYAML_SettingSourcesEmptySliceDefaultsToProject(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewAgentStorage(tmpDir)

	agent := model.AgentDefinition{
		Name:           "coder",
		Model:          "claude-sonnet-4-6",
		SystemPrompt:   "You are a coding assistant.",
		SettingSources: []string{}, // explicitly empty
	}

	require.NoError(t, store.WriteAgentYAML("coder", agent, model.ProviderConfig{}, nil, nil))

	data, err := os.ReadFile(filepath.Join(tmpDir, "coder", "agents", "agents.yaml"))
	require.NoError(t, err)

	var doc map[string]interface{}
	require.NoError(t, yaml.Unmarshal(data, &doc))

	agents := doc["agents"].([]interface{})
	entry := agents[0].(map[string]interface{})
	assert.Equal(t, []interface{}{"project"}, entry["settingSources"])
}

func TestWriteAgentYAML_SettingSourcesPassthrough(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewAgentStorage(tmpDir)

	agent := model.AgentDefinition{
		Name:           "coder",
		Model:          "claude-sonnet-4-6",
		SystemPrompt:   "You are a coding assistant.",
		SettingSources: []string{"user", "project"},
	}

	require.NoError(t, store.WriteAgentYAML("coder", agent, model.ProviderConfig{}, nil, nil))

	data, err := os.ReadFile(filepath.Join(tmpDir, "coder", "agents", "agents.yaml"))
	require.NoError(t, err)

	var doc map[string]interface{}
	require.NoError(t, yaml.Unmarshal(data, &doc))

	agents := doc["agents"].([]interface{})
	entry := agents[0].(map[string]interface{})
	assert.Equal(t, []interface{}{"user", "project"}, entry["settingSources"])
}

func TestWriteAgentYAML_DatasetsOmittedWhenEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewAgentStorage(tmpDir)

	agent := model.AgentDefinition{
		Name:         "coder",
		SystemPrompt: "No datasets.",
		Datasets:     map[string]string{},
	}

	require.NoError(t, store.WriteAgentYAML("coder", agent, model.ProviderConfig{}, nil, nil))

	data, err := os.ReadFile(filepath.Join(tmpDir, "coder", "agents", "agents.yaml"))
	require.NoError(t, err)

	var doc map[string]interface{}
	require.NoError(t, yaml.Unmarshal(data, &doc))

	agents := doc["agents"].([]interface{})
	entry := agents[0].(map[string]interface{})
	_, present := entry["datasets"]
	assert.False(t, present, "datasets should be omitted when empty")
}

func TestWriteAgentYAML_ContainsMaxSessionTurns(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewAgentStorage(tmpDir)

	maxSessionTurns := 50
	agent := model.AgentDefinition{
		Name:            "coder",
		Model:           "claude-sonnet-4-6",
		SystemPrompt:    "You are a coding assistant.",
		MaxSessionTurns: &maxSessionTurns,
		Subagents: []model.SubagentDefinition{
			{
				Name:        "reviewer",
				Description: "Review code",
				Prompt:      "You are a code reviewer.",
			},
		},
	}

	err := store.WriteAgentYAML("coder", agent, model.ProviderConfig{}, nil, nil)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(tmpDir, "coder", "agents", "agents.yaml"))
	require.NoError(t, err)

	var doc map[string]interface{}
	err = yaml.Unmarshal(data, &doc)
	require.NoError(t, err)

	agents := doc["agents"].([]interface{})
	require.Len(t, agents, 2)
	entry := agents[0].(map[string]interface{})
	assert.Equal(t, 50, entry["maxSessionTurns"])
	assert.Equal(t, []interface{}{"reviewer"}, entry["subagents"])

	// Subagent should NOT carry maxSessionTurns (agent-runtime issue #1: won't fix).
	reviewer := agents[1].(map[string]interface{})
	_, present := reviewer["maxSessionTurns"]
	assert.False(t, present, "subagent maxSessionTurns should not be serialized")
}

func TestWriteAgentYAML_NilMaxSessionTurnsOmitted(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewAgentStorage(tmpDir)

	agent := model.AgentDefinition{
		Name:            "coder",
		Model:           "claude-sonnet-4-6",
		SystemPrompt:    "You are a coding assistant.",
		MaxSessionTurns: nil,
	}

	err := store.WriteAgentYAML("coder", agent, model.ProviderConfig{}, nil, nil)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(tmpDir, "coder", "agents", "agents.yaml"))
	require.NoError(t, err)

	var doc map[string]interface{}
	err = yaml.Unmarshal(data, &doc)
	require.NoError(t, err)

	agents := doc["agents"].([]interface{})
	entry := agents[0].(map[string]interface{})
	_, present := entry["maxSessionTurns"]
	assert.False(t, present, "maxSessionTurns should be omitted when nil")
}

func TestReadAgentYAML_MaxSessionTurns(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewAgentStorage(tmpDir)

	maxSessionTurns := 50
	agent := model.AgentDefinition{
		Name:            "coder",
		Model:           "claude-sonnet-4-6",
		SystemPrompt:    "You are a coding assistant.",
		MaxSessionTurns: &maxSessionTurns,
		Subagents: []model.SubagentDefinition{
			{
				Name:        "reviewer",
				Description: "Review code",
				Prompt:      "You are a code reviewer.",
			},
		},
	}

	err := store.WriteAgentYAML("coder", agent, model.ProviderConfig{}, nil, nil)
	require.NoError(t, err)

	readAgent, err := store.ReadAgentYAML("coder")
	require.NoError(t, err)

	require.NotNil(t, readAgent.MaxSessionTurns)
	assert.Equal(t, 50, *readAgent.MaxSessionTurns)

	require.Len(t, readAgent.Subagents, 1)
}

func TestWriteAgentYAML_WithAigc(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewAgentStorage(tmpDir)

	agent := model.AgentDefinition{
		Name:         "coder",
		Model:        "glm-4.5",
		SystemPrompt: "You are a coding assistant.",
	}
	aigc := &model.AigcConfig{
		Enabled:         true,
		ContentProducer: "001191320118MAK93FC72D10001",
		SigningKey:      "secret-key",
		ModelCodes:      map[string]string{"glm-4.5": "0001"},
		Label:           strPtr("2"),
		ProduceIdPrefix: "tenant-A/",
		// ExplicitHint 未传：应物化为 true
	}

	err := store.WriteAgentYAML("coder", agent, model.ProviderConfig{}, aigc, nil)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(tmpDir, "coder", "agents", "agents.yaml"))
	require.NoError(t, err)

	var doc runtimeAgentsYAML
	require.NoError(t, yaml.Unmarshal(data, &doc))
	require.NotNil(t, doc.Aigc, "aigc section should be present")
	assert.True(t, doc.Aigc.Enabled)
	assert.Equal(t, "001191320118MAK93FC72D10001", doc.Aigc.ContentProducer)
	assert.Equal(t, "secret-key", doc.Aigc.SigningKey)
	require.NotNil(t, doc.Aigc.ExplicitHint, "explicitHint should be materialized")
	assert.True(t, *doc.Aigc.ExplicitHint, "explicitHint should default to true")
	assert.Equal(t, map[string]string{"glm-4.5": "0001"}, doc.Aigc.ModelCodes)
	require.NotNil(t, doc.Aigc.Label, "label should be serialized")
	assert.Equal(t, "2", *doc.Aigc.Label)
	assert.Equal(t, "tenant-A/", doc.Aigc.ProduceIdPrefix)
}

func TestWriteAgentYAML_AigcExplicitHintFalsePreserved(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewAgentStorage(tmpDir)

	agent := model.AgentDefinition{
		Name:         "coder",
		Model:        "glm-4.5",
		SystemPrompt: "You are a coding assistant.",
	}
	explicitFalse := false
	aigc := &model.AigcConfig{
		Enabled:         true,
		ContentProducer: "001191320118MAK93FC72D10001",
		ExplicitHint:    &explicitFalse,
	}

	require.NoError(t, store.WriteAgentYAML("coder", agent, model.ProviderConfig{}, aigc, nil))

	data, err := os.ReadFile(filepath.Join(tmpDir, "coder", "agents", "agents.yaml"))
	require.NoError(t, err)

	var doc runtimeAgentsYAML
	require.NoError(t, yaml.Unmarshal(data, &doc))
	require.NotNil(t, doc.Aigc)
	require.NotNil(t, doc.Aigc.ExplicitHint)
	assert.False(t, *doc.Aigc.ExplicitHint, "explicit false must be preserved")
}

func TestWriteAgentYAML_NoAigcSectionWhenNilOrDisabled(t *testing.T) {
	cases := map[string]*model.AigcConfig{
		"nil":      nil,
		"disabled": {Enabled: false, ContentProducer: "001191320118MAK93FC72D10001"},
	}
	for name, aigc := range cases {
		t.Run(name, func(t *testing.T) {
			tmpDir := t.TempDir()
			store := NewAgentStorage(tmpDir)
			agent := model.AgentDefinition{
				Name:         "coder",
				Model:        "glm-4.5",
				SystemPrompt: "You are a coding assistant.",
			}
			require.NoError(t, store.WriteAgentYAML("coder", agent, model.ProviderConfig{}, aigc, nil))

			data, err := os.ReadFile(filepath.Join(tmpDir, "coder", "agents", "agents.yaml"))
			require.NoError(t, err)
			assert.NotContains(t, string(data), "aigc:")
		})
	}
}

func TestWriteAgentYAML_ProviderCredentialsOnMainEntryOnly(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewAgentStorage(tmpDir)

	agent := model.AgentDefinition{
		Name:         "coder",
		Description:  "Writes and edits code",
		Model:        "claude-sonnet-4-6",
		SystemPrompt: "You are a coding assistant.",
		Subagents: []model.SubagentDefinition{
			{Name: "reviewer", Description: "Review code", Prompt: "You are a code reviewer."},
		},
	}
	provider := model.ProviderConfig{
		Protocol: "anthropic-messages",
		BaseURL:  "https://api.anthropic.com",
		APIKey:   "sk-secret",
	}

	require.NoError(t, store.WriteAgentYAML("coder", agent, provider, nil, nil))

	data, err := os.ReadFile(filepath.Join(tmpDir, "coder", "agents", "agents.yaml"))
	require.NoError(t, err)

	var doc map[string]interface{}
	require.NoError(t, yaml.Unmarshal(data, &doc))
	agents := doc["agents"].([]interface{})
	require.Len(t, agents, 2)

	main := agents[0].(map[string]interface{})
	assert.Equal(t, "sk-secret", main["apiKey"])
	assert.Equal(t, "https://api.anthropic.com", main["baseURL"])
	assert.Equal(t, "anthropic-messages", main["apiType"])

	sub := agents[1].(map[string]interface{})
	for _, f := range []string{"apiKey", "baseURL", "apiType"} {
		_, present := sub[f]
		assert.False(t, present, "subagent entry should not carry %s", f)
	}
}

func TestWriteAgentYAML_CredentialFieldOrder(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewAgentStorage(tmpDir)

	agent := model.AgentDefinition{
		Name:         "coder",
		Description:  "Writes and edits code",
		Model:        "claude-sonnet-4-6",
		SystemPrompt: "You are a coding assistant.",
	}
	provider := model.ProviderConfig{
		Protocol: "anthropic-messages",
		BaseURL:  "https://api.anthropic.com",
		APIKey:   "sk-secret",
	}

	require.NoError(t, store.WriteAgentYAML("coder", agent, provider, nil, nil))

	data, err := os.ReadFile(filepath.Join(tmpDir, "coder", "agents", "agents.yaml"))
	require.NoError(t, err)
	text := string(data)

	// yaml.v3 emits struct fields in declaration order; guard that the
	// credential fields sit right after model (mirroring the runtime's own
	// docs example: id, description, model, apiType, baseURL, apiKey, ...).
	order := []string{"model:", "apiType:", "baseURL:", "apiKey:", "systemPrompt:"}
	prev := -1
	for _, key := range order {
		idx := strings.Index(text, key)
		require.GreaterOrEqual(t, idx, 0, "field %s missing from yaml", key)
		assert.Greater(t, idx, prev, "field %s should come after the previous field", key)
		prev = idx
	}
}

func TestWriteAgentYAML_WithHub(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewAgentStorage(tmpDir)

	agent := model.AgentDefinition{
		Name:         "coder",
		Description:  "Writes and edits code",
		Model:        "glm-4.5",
		SystemPrompt: "You are a coding assistant.",
	}
	hub := &model.HubConfig{
		Enabled:     true,
		BaseURL:     "http://agent-hub:8080",
		ChatPushKey: "push-secret",
		Org:         "tenant-a",
	}

	require.NoError(t, store.WriteAgentYAML("coder", agent, model.ProviderConfig{}, nil, hub))

	data, err := os.ReadFile(filepath.Join(tmpDir, "coder", "agents", "agents.yaml"))
	require.NoError(t, err)

	var doc runtimeAgentsYAML
	require.NoError(t, yaml.Unmarshal(data, &doc))
	require.NotNil(t, doc.Hub, "hub section should be present")
	assert.True(t, doc.Hub.Enabled)
	assert.Equal(t, "http://agent-hub:8080", doc.Hub.BaseURL)
	assert.Equal(t, "push-secret", doc.Hub.ChatPushKey)
	assert.Equal(t, "tenant-a", doc.Hub.Org)
}

func TestWriteAgentYAML_HubOrgOmittedWhenEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewAgentStorage(tmpDir)

	agent := model.AgentDefinition{
		Name:         "coder",
		Description:  "Writes and edits code",
		Model:        "glm-4.5",
		SystemPrompt: "You are a coding assistant.",
	}
	hub := &model.HubConfig{
		Enabled:     true,
		BaseURL:     "http://agent-hub:8080",
		ChatPushKey: "push-secret",
	}

	require.NoError(t, store.WriteAgentYAML("coder", agent, model.ProviderConfig{}, nil, hub))

	data, err := os.ReadFile(filepath.Join(tmpDir, "coder", "agents", "agents.yaml"))
	require.NoError(t, err)
	assert.NotContains(t, string(data), "org:", "org should be omitted when unset")
}

func TestWriteAgentYAML_NoHubSectionWhenNilOrDisabled(t *testing.T) {
	cases := map[string]*model.HubConfig{
		"nil":      nil,
		"disabled": {Enabled: false, BaseURL: "http://agent-hub:8080", ChatPushKey: "push-secret"},
	}
	for name, hub := range cases {
		t.Run(name, func(t *testing.T) {
			tmpDir := t.TempDir()
			store := NewAgentStorage(tmpDir)
			agent := model.AgentDefinition{
				Name:         "coder",
				Description:  "Writes and edits code",
				Model:        "glm-4.5",
				SystemPrompt: "You are a coding assistant.",
			}
			require.NoError(t, store.WriteAgentYAML("coder", agent, model.ProviderConfig{}, nil, hub))

			data, err := os.ReadFile(filepath.Join(tmpDir, "coder", "agents", "agents.yaml"))
			require.NoError(t, err)
			assert.NotContains(t, string(data), "hub:")
		})
	}
}

func TestWriteAgentYAML_HubAndAigcCoexist(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewAgentStorage(tmpDir)

	agent := model.AgentDefinition{
		Name:         "coder",
		Description:  "Writes and edits code",
		Model:        "glm-4.5",
		SystemPrompt: "You are a coding assistant.",
	}
	aigc := &model.AigcConfig{
		Enabled:         true,
		ContentProducer: "001191320118MAK93FC72D10001",
	}
	hub := &model.HubConfig{
		Enabled:     true,
		BaseURL:     "http://agent-hub:8080",
		ChatPushKey: "push-secret",
	}

	require.NoError(t, store.WriteAgentYAML("coder", agent, model.ProviderConfig{}, aigc, hub))

	data, err := os.ReadFile(filepath.Join(tmpDir, "coder", "agents", "agents.yaml"))
	require.NoError(t, err)

	var doc runtimeAgentsYAML
	require.NoError(t, yaml.Unmarshal(data, &doc))
	require.NotNil(t, doc.Aigc, "aigc section should still be present")
	require.NotNil(t, doc.Hub, "hub section should be present")
	assert.True(t, doc.Hub.Enabled)
}
