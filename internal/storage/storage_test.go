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
	agents := []model.AgentDefinition{
		{
			Name:         "coder",
			Description:  "Writes and edits code",
			Model:        "claude-sonnet-4-6",
			SystemPrompt: "You are a coding assistant.",
			Tools:        []string{"Read", "Write"},
			Subagents:    []string{"reviewer"},
		},
		{
			Name:         "reviewer",
			Description:  "Review code",
			SystemPrompt: "You are a code reviewer.",
			Tools:        []string{"Read"},
			MaxTurns:     &subMaxTurns,
		},
	}

	require.NoError(t, store.WriteAgentYAML("coder", agents, model.ProviderConfig{}, nil, nil, nil))

	data, err := os.ReadFile(filepath.Join(tmpDir, "coder", "agents", "agents.yaml"))
	require.NoError(t, err)

	var doc map[string]interface{}
	require.NoError(t, yaml.Unmarshal(data, &doc))

	entries, ok := doc["agents"].([]interface{})
	require.True(t, ok, "top-level agents key should be a list")
	require.Len(t, entries, 2, "main agent + each subagent should be a top-level entry")

	main, ok := entries[0].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "coder", main["id"])
	assert.Equal(t, "Writes and edits code", main["description"],
		"runtime 2.0 requires description on every agent entry")
	assert.Equal(t, []interface{}{"reviewer"}, main["subagents"],
		"subagents should be an id reference list, not inline definitions")

	sub, ok := entries[1].(map[string]interface{})
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
	agents := []model.AgentDefinition{
		{
			Name:         "coder",
			Description:  "Writes and edits code",
			Model:        "claude-sonnet-4-6",
			SystemPrompt: "You are a coding assistant.",
			Subagents:    []string{"reviewer"},
		},
		{
			Name:         "reviewer",
			Description:  "Review code",
			SystemPrompt: "You are a code reviewer.",
			Tools:        []string{"Read"},
			MaxTurns:     &subMaxTurns,
		},
	}

	require.NoError(t, store.WriteAgentYAML("coder", agents, model.ProviderConfig{}, nil, nil, nil))

	graph, err := store.ReadAgentYAML("coder")
	require.NoError(t, err)
	assert.Equal(t, "coder", graph.RootAgentID)
	require.Len(t, graph.Agents, 2)
	assert.Equal(t, "coder", graph.Agents[0].Name)
	assert.Equal(t, "Writes and edits code", graph.Agents[0].Description)
	assert.Equal(t, []string{"reviewer"}, graph.Agents[0].Subagents)
	assert.Equal(t, agents[1].Name, graph.Agents[1].Name)
	assert.Equal(t, agents[1].Description, graph.Agents[1].Description)
	assert.Equal(t, agents[1].SystemPrompt, graph.Agents[1].SystemPrompt)
	assert.Equal(t, agents[1].Tools, graph.Agents[1].Tools)
	assert.Equal(t, *agents[1].MaxTurns, *graph.Agents[1].MaxTurns)
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
	assert.Equal(t, "coder", readAgent.RootAgentID)
	require.Len(t, readAgent.Agents, 2)
	byID := map[string]model.AgentDefinition{}
	for _, a := range readAgent.Agents {
		byID[a.Name] = a
	}
	root := byID["coder"]
	assert.Equal(t, "Writes and edits code", root.Description)
	assert.Equal(t, "claude-sonnet-4-6", root.Model)
	assert.Equal(t, []string{"reviewer"}, root.Subagents)
	sub := byID["reviewer"]
	assert.Equal(t, "Review code", sub.Description)
	assert.Equal(t, "You are a code reviewer.", sub.SystemPrompt)
}

func TestWriteAndReadAgentYAML(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewAgentStorage(tmpDir)

	maxTurns := 20
	subMaxTurns := 10
	agents := []model.AgentDefinition{
		{
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
			Subagents:      []string{"reviewer"},
			Datasets: map[string]string{
				"dataset-1": "Primary dataset for code review",
				"dataset-2": "Secondary dataset for testing",
			},
		},
		{
			Name:         "reviewer",
			Description:  "Review code",
			SystemPrompt: "You are a code reviewer.",
			Tools:        []string{"Read"},
			MaxTurns:     &subMaxTurns,
		},
	}

	err := store.WriteAgentYAML("coder", agents, model.ProviderConfig{}, nil, nil, nil)
	require.NoError(t, err)

	// Verify file is created at the correct path.
	expectedPath := filepath.Join(tmpDir, "coder", "agents", "agents.yaml")
	_, err = os.Stat(expectedPath)
	require.NoError(t, err, "agents.yaml should be created at expected path")

	// Read back and verify identity.
	graph, err := store.ReadAgentYAML("coder")
	require.NoError(t, err)
	require.Len(t, graph.Agents, 2)
	readAgent := graph.Agents[0]
	assert.Equal(t, agents[0].Name, readAgent.Name)
	assert.Equal(t, agents[0].Model, readAgent.Model)
	assert.Equal(t, agents[0].SystemPrompt, readAgent.SystemPrompt)
	assert.Equal(t, *agents[0].MaxTurns, *readAgent.MaxTurns)
	assert.Equal(t, agents[0].PermissionMode, readAgent.PermissionMode)
	assert.Equal(t, agents[0].Tools, readAgent.Tools)
	assert.Equal(t, agents[0].Skills, readAgent.Skills,
		"Skills round-trip losslessly via the deploy-manifest sidecar")
	assert.Equal(t, []string{"reviewer"}, readAgent.Subagents)
	readSub := graph.Agents[1]
	assert.Equal(t, agents[1].Name, readSub.Name)
	assert.Equal(t, agents[1].Description, readSub.Description)
	assert.Equal(t, agents[1].SystemPrompt, readSub.SystemPrompt)
	assert.Equal(t, agents[1].Tools, readSub.Tools)
	assert.Equal(t, *agents[1].MaxTurns, *readSub.MaxTurns)
	assert.Equal(t, agents[0].SettingSources, readAgent.SettingSources)
	assert.Equal(t, agents[0].Datasets, readAgent.Datasets)
}

func TestWriteAgentYAML_ContainsExpectedKeys(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewAgentStorage(tmpDir)

	maxTurns := 20
	agents := []model.AgentDefinition{
		{
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
			Subagents:      []string{"reviewer"},
			Datasets: map[string]string{
				"dataset-1": "Primary dataset",
			},
		},
		{
			Name:         "reviewer",
			Description:  "Review code",
			SystemPrompt: "You are a code reviewer.",
			Tools:        []string{"Read"},
			MaxTurns:     func() *int { v := 10; return &v }(),
		},
	}

	err := store.WriteAgentYAML("coder", agents, model.ProviderConfig{}, nil, nil, nil)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(tmpDir, "coder", "agents", "agents.yaml"))
	require.NoError(t, err)

	var doc map[string]interface{}
	err = yaml.Unmarshal(data, &doc)
	require.NoError(t, err)

	entries, ok := doc["agents"].([]interface{})
	require.True(t, ok, "top-level agents key should be a list")
	require.Len(t, entries, 2, "main agent + subagent as first-class entries")

	entry, ok := entries[0].(map[string]interface{})
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

	reviewer, ok := entries[1].(map[string]interface{})
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

	err := store.WriteAgentYAML("coder", []model.AgentDefinition{agent}, model.ProviderConfig{}, nil, nil, nil)
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

func TestWriteAgentYAML_EmptyName(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewAgentStorage(tmpDir)

	err := store.WriteAgentYAML("", nil, model.ProviderConfig{}, nil, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be a single path segment")
}

func TestWriteAgentYAML_PathTraversalRejected(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewAgentStorage(tmpDir)

	cases := []string{"../etc", "a/b", "/abs", ".", ".."}
	for _, bad := range cases {
		agent := model.AgentDefinition{Name: bad, Model: "m", SystemPrompt: "p"}
		err := store.WriteAgentYAML(bad, []model.AgentDefinition{agent}, model.ProviderConfig{}, nil, nil, nil)
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

	agents := []model.AgentDefinition{
		{
			Name:      "orchestrator",
			Subagents: []string{"zeta", "alpha", "mid"},
		},
		{Name: "zeta", Description: "z desc"},
		{Name: "alpha", Description: "a desc"},
		{Name: "mid", Description: "m desc"},
	}

	require.NoError(t, store.WriteAgentYAML("orchestrator", agents, model.ProviderConfig{}, nil, nil, nil))

	graph, err := store.ReadAgentYAML("orchestrator")
	require.NoError(t, err)
	require.Len(t, graph.Agents, 4)

	assert.Equal(t, []string{"zeta", "alpha", "mid"}, graph.Agents[0].Subagents,
		"subagents should preserve the definition order of the id reference list")
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
	agents := []model.AgentDefinition{
		{
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
			Subagents: []string{"reviewer"},
		},
		{
			Name:         "reviewer",
			Description:  "Review code",
			SystemPrompt: "You are a code reviewer.",
			Tools:        []string{"Read"},
		},
	}

	err := store.WriteAgentYAML("coder", agents, model.ProviderConfig{}, nil, nil, nil)
	require.NoError(t, err)

	graph, err := store.ReadAgentYAML("coder")
	require.NoError(t, err)

	readAgent := graph.Agents[0]
	require.Len(t, readAgent.McpServers, 1)
	mcp, ok := readAgent.McpServers["remote-api"]
	require.True(t, ok)
	assert.Equal(t, "sse", mcp.Type)
	assert.Equal(t, "https://api.example.com/sse", mcp.URL)
	assert.Equal(t, "Bearer xxx", mcp.Headers["Authorization"])

	assert.Equal(t, []string{"reviewer"}, readAgent.Subagents)
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

	err := store.WriteAgentYAML("coder", []model.AgentDefinition{agent}, model.ProviderConfig{}, nil, nil, nil)
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

	err := store.WriteAgentYAML("coder", []model.AgentDefinition{agent}, model.ProviderConfig{}, nil, nil, nil)
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

	graph, err := store.ReadAgentYAML("coder")
	require.NoError(t, err)
	require.Len(t, graph.Agents, 1)
	assert.Empty(t, graph.Agents[0].McpServers)
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

	require.NoError(t, store.WriteAgentYAML("coder", []model.AgentDefinition{agent}, model.ProviderConfig{}, nil, nil, nil))

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

	require.NoError(t, store.WriteAgentYAML("coder", []model.AgentDefinition{agent}, model.ProviderConfig{}, nil, nil, nil))

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

	require.NoError(t, store.WriteAgentYAML("coder", []model.AgentDefinition{agent}, model.ProviderConfig{}, nil, nil, nil))

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

	require.NoError(t, store.WriteAgentYAML("coder", []model.AgentDefinition{agent}, model.ProviderConfig{}, nil, nil, nil))

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
	agents := []model.AgentDefinition{
		{
			Name:            "coder",
			Model:           "claude-sonnet-4-6",
			SystemPrompt:    "You are a coding assistant.",
			MaxSessionTurns: &maxSessionTurns,
			Subagents:       []string{"reviewer"},
		},
		{
			Name:         "reviewer",
			Description:  "Review code",
			SystemPrompt: "You are a code reviewer.",
		},
	}

	err := store.WriteAgentYAML("coder", agents, model.ProviderConfig{}, nil, nil, nil)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(tmpDir, "coder", "agents", "agents.yaml"))
	require.NoError(t, err)

	var doc map[string]interface{}
	err = yaml.Unmarshal(data, &doc)
	require.NoError(t, err)

	entries := doc["agents"].([]interface{})
	require.Len(t, entries, 2)
	entry := entries[0].(map[string]interface{})
	assert.Equal(t, 50, entry["maxSessionTurns"])
	assert.Equal(t, []interface{}{"reviewer"}, entry["subagents"])

	// Subagent should NOT carry maxSessionTurns (agent-runtime issue #1: won't fix).
	reviewer := entries[1].(map[string]interface{})
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

	err := store.WriteAgentYAML("coder", []model.AgentDefinition{agent}, model.ProviderConfig{}, nil, nil, nil)
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
	agents := []model.AgentDefinition{
		{
			Name:            "coder",
			Model:           "claude-sonnet-4-6",
			SystemPrompt:    "You are a coding assistant.",
			MaxSessionTurns: &maxSessionTurns,
			Subagents:       []string{"reviewer"},
		},
		{
			Name:         "reviewer",
			Description:  "Review code",
			SystemPrompt: "You are a code reviewer.",
		},
	}

	err := store.WriteAgentYAML("coder", agents, model.ProviderConfig{}, nil, nil, nil)
	require.NoError(t, err)

	graph, err := store.ReadAgentYAML("coder")
	require.NoError(t, err)

	readAgent := graph.Agents[0]
	require.NotNil(t, readAgent.MaxSessionTurns)
	assert.Equal(t, 50, *readAgent.MaxSessionTurns)

	assert.Equal(t, []string{"reviewer"}, readAgent.Subagents)
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

	err := store.WriteAgentYAML("coder", []model.AgentDefinition{agent}, model.ProviderConfig{}, aigc, nil, nil)
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

	require.NoError(t, store.WriteAgentYAML("coder", []model.AgentDefinition{agent}, model.ProviderConfig{}, aigc, nil, nil))

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
			require.NoError(t, store.WriteAgentYAML("coder", []model.AgentDefinition{agent}, model.ProviderConfig{}, aigc, nil, nil))

			data, err := os.ReadFile(filepath.Join(tmpDir, "coder", "agents", "agents.yaml"))
			require.NoError(t, err)
			assert.NotContains(t, string(data), "aigc:")
		})
	}
}

func TestWriteAgentYAML_ProviderCredentialsOnMainEntryOnly(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewAgentStorage(tmpDir)

	agents := []model.AgentDefinition{
		{
			Name:         "coder",
			Description:  "Writes and edits code",
			Model:        "claude-sonnet-4-6",
			SystemPrompt: "You are a coding assistant.",
			Subagents:    []string{"reviewer"},
		},
		{Name: "reviewer", Description: "Review code", SystemPrompt: "You are a code reviewer."},
	}
	provider := model.ProviderConfig{
		Protocol: "anthropic-messages",
		BaseURL:  "https://api.anthropic.com",
		APIKey:   "sk-secret",
	}

	require.NoError(t, store.WriteAgentYAML("coder", agents, provider, nil, nil, nil))

	data, err := os.ReadFile(filepath.Join(tmpDir, "coder", "agents", "agents.yaml"))
	require.NoError(t, err)

	var doc map[string]interface{}
	require.NoError(t, yaml.Unmarshal(data, &doc))
	entries := doc["agents"].([]interface{})
	require.Len(t, entries, 2)

	main := entries[0].(map[string]interface{})
	assert.Equal(t, "sk-secret", main["apiKey"])
	assert.Equal(t, "https://api.anthropic.com", main["baseURL"])
	assert.Equal(t, "anthropic-messages", main["apiType"])

	sub := entries[1].(map[string]interface{})
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

	require.NoError(t, store.WriteAgentYAML("coder", []model.AgentDefinition{agent}, provider, nil, nil, nil))

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

	require.NoError(t, store.WriteAgentYAML("coder", []model.AgentDefinition{agent}, model.ProviderConfig{}, nil, hub, nil))

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

	require.NoError(t, store.WriteAgentYAML("coder", []model.AgentDefinition{agent}, model.ProviderConfig{}, nil, hub, nil))

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
			require.NoError(t, store.WriteAgentYAML("coder", []model.AgentDefinition{agent}, model.ProviderConfig{}, nil, hub, nil))

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

	require.NoError(t, store.WriteAgentYAML("coder", []model.AgentDefinition{agent}, model.ProviderConfig{}, aigc, hub, nil))

	data, err := os.ReadFile(filepath.Join(tmpDir, "coder", "agents", "agents.yaml"))
	require.NoError(t, err)

	var doc runtimeAgentsYAML
	require.NoError(t, yaml.Unmarshal(data, &doc))
	require.NotNil(t, doc.Aigc, "aigc section should still be present")
	require.NotNil(t, doc.Hub, "hub section should be present")
	assert.True(t, doc.Hub.Enabled)
}

func TestWriteAgentYAML_CustomTools(t *testing.T) {
	dir := t.TempDir()
	s := NewAgentStorage(dir)
	agent := model.AgentDefinition{
		Name: "coder", Description: "d", Model: "m", SystemPrompt: "s",
		Tools: []string{"Bash", "GetWeather"},
	}
	provider := model.ProviderConfig{Protocol: "anthropic-messages", BaseURL: "https://api.anthropic.com", APIKey: "sk-x"}

	err := s.WriteAgentYAML("coder", []model.AgentDefinition{agent}, provider, nil, nil, map[string][]string{
		"coder": {"./tools/Zebra.mjs", "./tools/Alpha.ts"},
	})
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, "coder", "agents", "agents.yaml"))
	require.NoError(t, err)

	var doc map[string]interface{}
	require.NoError(t, yaml.Unmarshal(data, &doc))
	main := doc["agents"].([]interface{})[0].(map[string]interface{})

	// CustomTools paths written verbatim, sorted, under customTools.
	ct, ok := main["customTools"].([]interface{})
	require.True(t, ok, "customTools key must exist, got %v", main)
	assert.Equal(t, []interface{}{"./tools/Alpha.ts", "./tools/Zebra.mjs"}, ct)

	// Regression guard (issue #10): Tools still maps to allowedTools and
	// nothing else; customTools carries only file paths.
	at, ok := main["allowedTools"].([]interface{})
	require.True(t, ok)
	assert.Equal(t, []interface{}{"Bash", "GetWeather"}, at)
}

func TestWriteAgentYAML_CustomTools_OmittedWhenEmpty(t *testing.T) {
	dir := t.TempDir()
	s := NewAgentStorage(dir)
	agent := model.AgentDefinition{Name: "coder", Description: "d", Model: "m", SystemPrompt: "s"}
	provider := model.ProviderConfig{Protocol: "anthropic-messages", BaseURL: "https://api.anthropic.com", APIKey: "sk-x"}

	require.NoError(t, s.WriteAgentYAML("coder", []model.AgentDefinition{agent}, provider, nil, nil, nil))

	data, err := os.ReadFile(filepath.Join(dir, "coder", "agents", "agents.yaml"))
	require.NoError(t, err)
	assert.NotContains(t, string(data), "customTools")
}

func TestWriteAgentYAML_CustomTools_DoesNotTouchSubagentEntries(t *testing.T) {
	// toolPaths is keyed by agent id: paths registered for the root entry only
	// must not leak into other entries.
	dir := t.TempDir()
	s := NewAgentStorage(dir)
	agents := []model.AgentDefinition{
		{Name: "coder", Description: "d", Model: "m", SystemPrompt: "s", Subagents: []string{"helper"}},
		{Name: "helper", Description: "h", SystemPrompt: "p"},
	}
	provider := model.ProviderConfig{Protocol: "anthropic-messages", BaseURL: "https://api.anthropic.com", APIKey: "sk-x"}

	require.NoError(t, s.WriteAgentYAML("coder", agents, provider, nil, nil, map[string][]string{"coder": {"./tools/A.mjs"}}))

	data, err := os.ReadFile(filepath.Join(dir, "coder", "agents", "agents.yaml"))
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(string(data), "customTools"), "exactly one customTools key (main entry only)")
}

func TestWriteAgentYAML_CompleteGraph(t *testing.T) {
	dir := t.TempDir()
	store := NewAgentStorage(dir)

	agents := []model.AgentDefinition{
		{
			Name:         "parent",
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
			Tools:           []string{"WebSearch"},
			DisallowedTools: []string{"Bash"},
			McpServers: map[string]model.McpServerConfig{
				"knowledge": {Type: "http", URL: "https://example.invalid/mcp"},
			},
			SettingSources: []string{"user"},
			Skills:         []model.SkillSource{{Name: "skill-a", URL: "https://example.com/s.zip", Hash: strings.Repeat("b", 64)}},
			Datasets:       map[string]string{"knowledge-a": "Child A knowledge"},
		},
		{
			Name:         "child-b",
			Description:  "Review specialist",
			SystemPrompt: "Review the result",
		},
	}
	provider := model.ProviderConfig{Protocol: "anthropic-messages", BaseURL: "https://api.anthropic.com", APIKey: "sk-root"}
	toolPaths := map[string][]string{"child-a": {"./tools/child-a-tool.mjs"}}

	require.NoError(t, store.WriteAgentYAML("parent", agents, provider, nil, nil, toolPaths))

	data, err := os.ReadFile(filepath.Join(dir, "parent", "agents", "agents.yaml"))
	require.NoError(t, err)
	yamlStr := string(data)

	var doc struct {
		Agents []map[string]any `yaml:"agents"`
	}
	require.NoError(t, yaml.Unmarshal(data, &doc))
	require.Len(t, doc.Agents, 3)

	byID := map[string]map[string]any{}
	for _, e := range doc.Agents {
		byID[e["id"].(string)] = e
	}

	// Root: provider credentials + model; subagents are pure id refs.
	root := byID["parent"]
	assert.Equal(t, "sk-root", root["apiKey"])
	assert.Equal(t, "anthropic-messages", root["apiType"])
	assert.Equal(t, "claude-sonnet-4-6", root["model"])
	assert.Equal(t, []any{"child-a", "child-b"}, root["subagents"])

	// child-a: full Agent-local profile; NO provider credentials, NO model.
	childA := byID["child-a"]
	assert.Equal(t, "Research specialist", childA["description"])
	assert.Equal(t, []any{"Bash"}, childA["disallowedTools"])
	assert.Equal(t, []any{"./tools/child-a-tool.mjs"}, childA["customTools"])
	assert.Equal(t, []any{"user"}, childA["settingSources"])
	assert.Equal(t, []any{"/app/config/skills/child-a"}, childA["extraUserSkillDirs"])
	assert.Contains(t, childA, "mcpServers")
	assert.Equal(t, map[string]any{"knowledge-a": "Child A knowledge"}, childA["datasets"])
	assert.NotContains(t, childA, "apiKey")
	assert.NotContains(t, childA, "model")
	_, hasSub := childA["subagents"]
	assert.False(t, hasSub, "child with no subagents must omit the key")

	// child-b: empty capabilities stay empty (no inheritance from parent).
	childB := byID["child-b"]
	assert.NotContains(t, childB, "mcpServers")
	assert.NotContains(t, childB, "customTools")
	assert.NotContains(t, childB, "datasets")
	assert.NotContains(t, childB, "disallowedTools")
	assert.NotContains(t, childB, "allowedTools")

	assert.NotContains(t, yamlStr, "sk-child", "no child credentials anywhere")
}

func TestReadAgentYAML_CompleteGraphRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store := NewAgentStorage(dir)

	agents := []model.AgentDefinition{
		{
			Name:         "parent",
			Description:  "Coordinates work",
			Model:        "claude-sonnet-4-6",
			SystemPrompt: "Delegate tasks",
			Tools:        []string{"Task"},
			Subagents:    []string{"child-a"},
		},
		{
			Name:            "child-a",
			Description:     "Research specialist",
			SystemPrompt:    "Research and summarize",
			DisallowedTools: []string{"Bash"},
			SettingSources:  []string{"user"},
			Skills:          []model.SkillSource{{Name: "skill-a", URL: "https://example.com/s.zip", Hash: strings.Repeat("b", 64)}},
			Datasets:        map[string]string{"knowledge-a": "Child A knowledge"},
		},
	}
	provider := model.ProviderConfig{Protocol: "anthropic-messages", BaseURL: "https://api.anthropic.com", APIKey: "sk-root"}
	require.NoError(t, store.WriteAgentYAML("parent", agents, provider, nil, nil, nil))

	graph, err := store.ReadAgentYAML("parent")
	require.NoError(t, err)
	assert.Equal(t, "parent", graph.RootAgentID)
	require.Len(t, graph.Agents, 2)

	var root, child *model.AgentDefinition
	for i := range graph.Agents {
		switch graph.Agents[i].Name {
		case "parent":
			root = &graph.Agents[i]
		case "child-a":
			child = &graph.Agents[i]
		}
	}
	require.NotNil(t, root)
	require.NotNil(t, child)
	assert.Equal(t, []string{"child-a"}, root.Subagents)
	assert.Equal(t, "claude-sonnet-4-6", root.Model)
	// disallowedTools round-trips losslessly (issue #16 regression #4).
	assert.Equal(t, []string{"Bash"}, child.DisallowedTools)
	assert.Equal(t, map[string]string{"knowledge-a": "Child A knowledge"}, child.Datasets)
	assert.Empty(t, child.Subagents)
	// Artifact declarations round-trip via the manifest sidecar (the YAML
	// itself cannot carry url+hash metadata).
	assert.Equal(t, agents[1].Skills, child.Skills)
}

func TestWriteAgentYAML_RejectsNameMismatch(t *testing.T) {
	dir := t.TempDir()
	store := NewAgentStorage(dir)
	agents := []model.AgentDefinition{{Name: "other", Description: "x", Model: "m"}}
	err := store.WriteAgentYAML("coder", agents, model.ProviderConfig{Protocol: "anthropic-messages", BaseURL: "https://x", APIKey: "k"}, nil, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no agent with id")
}

// TestReadAgentYAML_ManifestRestoresArtifactMetadata guards the lossless
// round-trip contract (issue #16 review): Skills/CustomTools url+hash
// metadata cannot live in the runtime agents.yaml, so a deployment manifest
// sidecar persists them and ReadAgentYAML merges them back.
func TestReadAgentYAML_ManifestRestoresArtifactMetadata(t *testing.T) {
	dir := t.TempDir()
	store := NewAgentStorage(dir)

	agents := []model.AgentDefinition{
		{
			Name: "parent", Description: "Coordinates", Model: "claude-sonnet-4-6", SystemPrompt: "Delegate",
			Subagents: []string{"child-a"},
			CustomTools: []model.ToolSource{
				{Name: "root-tool", URL: "https://example.com/r.mjs", Hash: strings.Repeat("a", 64), FileName: "r.mjs"},
			},
		},
		{
			Name: "child-a", Description: "Research", SystemPrompt: "Research",
			SettingSources: []string{"user"},
			Skills: []model.SkillSource{
				{Name: "skill-a", URL: "https://example.com/s.zip", Hash: strings.Repeat("b", 64)},
			},
			CustomTools: []model.ToolSource{
				{Name: "child-tool", URL: "https://example.com/c.mjs", Hash: strings.Repeat("c", 64), FileName: "c.mjs"},
			},
		},
	}
	provider := model.ProviderConfig{Protocol: "anthropic-messages", BaseURL: "https://api.anthropic.com", APIKey: "sk-root"}

	require.NoError(t, store.WriteAgentYAML("parent", agents, provider, nil, nil,
		map[string][]string{"parent": {"./tools/root-tool.mjs"}, "child-a": {"./tools/child-tool.mjs"}}))

	// The manifest sidecar exists next to agents.yaml.
	assert.FileExists(t, filepath.Join(dir, "parent", "agents", "deploy-manifest.json"))

	graph, err := store.ReadAgentYAML("parent")
	require.NoError(t, err)
	require.Len(t, graph.Agents, 2)

	var root, child *model.AgentDefinition
	for i := range graph.Agents {
		switch graph.Agents[i].Name {
		case "parent":
			root = &graph.Agents[i]
		case "child-a":
			child = &graph.Agents[i]
		}
	}
	require.NotNil(t, root)
	require.NotNil(t, child)

	// Full artifact declarations survive the round trip: url + hash + name.
	require.Len(t, root.CustomTools, 1)
	assert.Equal(t, agents[0].CustomTools[0], root.CustomTools[0])
	require.Len(t, child.Skills, 1)
	assert.Equal(t, agents[1].Skills[0], child.Skills[0])
	require.Len(t, child.CustomTools, 1)
	assert.Equal(t, agents[1].CustomTools[0], child.CustomTools[0])
}

// TestReadAgentYAML_NoManifestDegradesToYAML keeps hand-written or
// pre-manifest deployments readable: artifacts come back empty instead of
// failing the whole read.
func TestReadAgentYAML_NoManifestDegradesToYAML(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, "legacy", "agents")
	require.NoError(t, os.MkdirAll(agentDir, 0755))
	yamlBytes := []byte("agents:\n  - id: legacy\n    description: old deployment\n    systemPrompt: p\n")
	require.NoError(t, os.WriteFile(filepath.Join(agentDir, "agents.yaml"), yamlBytes, 0644))

	graph, err := NewAgentStorage(dir).ReadAgentYAML("legacy")
	require.NoError(t, err)
	require.Len(t, graph.Agents, 1)
	assert.Empty(t, graph.Agents[0].Skills)
	assert.Empty(t, graph.Agents[0].CustomTools)
}

// TestReadAgentYAML_CorruptManifestFailsExplicitly refuses to silently
// degrade when the manifest exists but cannot be parsed: a lossy round trip
// is worse than an explicit error.
func TestReadAgentYAML_CorruptManifestFailsExplicitly(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, "broken", "agents")
	require.NoError(t, os.MkdirAll(agentDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(agentDir, "agents.yaml"), []byte("agents:\n  - id: broken\n    description: d\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(agentDir, "deploy-manifest.json"), []byte("{not json"), 0644))

	_, err := NewAgentStorage(dir).ReadAgentYAML("broken")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deploy-manifest")
}
