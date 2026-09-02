package storage

import (
	"encoding/json"
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
	spf, isSpf := sub["systemPromptFile"].(string)
	require.True(t, isSpf, "systemPrompt must externalize to a systemPromptFile reference")
	assert.True(t, strings.HasPrefix(spf, "./prompts/reviewer-"), "prompt file lives under prompts/: %q", spf)
	_, inline := sub["systemPrompt"]
	assert.False(t, inline, "externalized prompt must not be inlined in the YAML")
	promptBytes, perr := os.ReadFile(filepath.Join(tmpDir, "coder", "agents", filepath.FromSlash(strings.TrimPrefix(spf, "./"))))
	require.NoError(t, perr)
	assert.Equal(t, "You are a code reviewer.", string(promptBytes))
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
	spfMain, isSpfMain := entry["systemPromptFile"].(string)
	require.True(t, isSpfMain, "systemPrompt must externalize to a systemPromptFile reference")
	assert.True(t, strings.HasPrefix(spfMain, "./prompts/coder-"), "prompt file lives under prompts/: %q", spfMain)
	_, inlineMain := entry["systemPrompt"]
	assert.False(t, inlineMain, "externalized prompt must not be inlined in the YAML")
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
	spfRev, isSpfRev := reviewer["systemPromptFile"].(string)
	require.True(t, isSpfRev, "systemPrompt must externalize to a systemPromptFile reference")
	assert.True(t, strings.HasPrefix(spfRev, "./prompts/reviewer-"), "prompt file lives under prompts/: %q", spfRev)
	_, inlineRev := reviewer["systemPrompt"]
	assert.False(t, inlineRev, "externalized prompt must not be inlined in the YAML")
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
	order := []string{"model:", "apiType:", "baseURL:", "apiKey:", "systemPromptFile:"}
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
// round-trip contract (issue #16, fourth review round): artifact
// declarations (Skill/Tool url+hash) live in the x-deployer-manifest
// section EMBEDDED in agents.yaml — one authoritative file, replaced
// atomically, so read-back is lossless by construction.
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

	// The embedded section lives in agents.yaml; no sidecar is written.
	yamlBytes, err := os.ReadFile(filepath.Join(dir, "parent", "agents", "agents.yaml"))
	require.NoError(t, err)
	assert.Contains(t, string(yamlBytes), "x-deployer-manifest")
	assert.NoFileExists(t, filepath.Join(dir, "parent", "agents", "deploy-manifest.json"))

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

// TestReadAgentYAML_NoEmbeddedSectionDegradesToYAML keeps hand-written or
// pre-manifest deployments readable: artifacts come back empty instead of
// failing the whole read.
func TestReadAgentYAML_NoEmbeddedSectionDegradesToYAML(t *testing.T) {
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

// TestReadAgentYAML_CorruptLegacyManifestFailsExplicitly covers the legacy
// sidecar compatibility path: a pre-embedded-section deployment with a
// corrupt sidecar fails explicitly rather than silently degrading.
func TestReadAgentYAML_CorruptLegacyManifestFailsExplicitly(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, "broken", "agents")
	require.NoError(t, os.MkdirAll(agentDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(agentDir, "agents.yaml"), []byte("agents:\n  - id: broken\n    description: d\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(agentDir, "deploy-manifest.json"), []byte("{not json"), 0644))

	_, err := NewAgentStorage(dir).ReadAgentYAML("broken")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deploy-manifest")
}

// TestWriteAgentYAML_ArtifactFreeRedeployDropsEmbeddedSection guards
// against stale-capability resurrection: a redeploy whose graph declares NO
// artifacts omits the embedded section entirely (single-file replacement —
// nothing from the previous deployment survives), and a leftover legacy
// sidecar is migrated away.
func TestWriteAgentYAML_ArtifactFreeRedeployDropsEmbeddedSection(t *testing.T) {
	dir := t.TempDir()
	store := NewAgentStorage(dir)
	provider := model.ProviderConfig{Protocol: "anthropic-messages", BaseURL: "https://x", APIKey: "k"}

	withArtifacts := []model.AgentDefinition{
		{Name: "parent", Description: "d", Model: "m", SystemPrompt: "s", Subagents: []string{"child-a"}},
		{
			Name: "child-a", Description: "c", SettingSources: []string{"user"},
			Skills:      []model.SkillSource{{Name: "old-skill", URL: "https://example.com/o.zip", Hash: strings.Repeat("a", 64)}},
			CustomTools: []model.ToolSource{{Name: "old-tool", URL: "https://example.com/o.mjs", Hash: strings.Repeat("b", 64), FileName: "o.mjs"}},
		},
	}
	require.NoError(t, store.WriteAgentYAML("parent", withArtifacts, provider, nil, nil, nil))

	// Plant a legacy sidecar to prove it is migrated away on redeploy.
	agentDir := filepath.Join(dir, "parent", "agents")
	require.NoError(t, os.WriteFile(filepath.Join(agentDir, "deploy-manifest.json"), []byte(`{"rootAgentId":"parent","artifacts":{}}`), 0644))

	withoutArtifacts := []model.AgentDefinition{
		{Name: "parent", Description: "d2", Model: "m", SystemPrompt: "s2", Subagents: []string{"child-a"}},
		{Name: "child-a", Description: "c2"},
	}
	require.NoError(t, store.WriteAgentYAML("parent", withoutArtifacts, provider, nil, nil, nil))

	yamlBytes, err := os.ReadFile(filepath.Join(agentDir, "agents.yaml"))
	require.NoError(t, err)
	assert.NotContains(t, string(yamlBytes), "x-deployer-manifest", "artifact-free graph must not carry an embedded section")
	assert.NoFileExists(t, filepath.Join(agentDir, "deploy-manifest.json"), "legacy sidecar must be migrated away")

	graph, err := store.ReadAgentYAML("parent")
	require.NoError(t, err)
	for _, a := range graph.Agents {
		assert.Empty(t, a.Skills, "agent %s must not resurrect removed skills", a.Name)
		assert.Empty(t, a.CustomTools, "agent %s must not resurrect removed tools", a.Name)
	}
}

// TestWriteAgentYAML_RedeployReplacesEmbeddedDeclarations keeps a redeploy
// with DIFFERENT artifacts from merging stale and fresh declarations.
func TestWriteAgentYAML_RedeployReplacesEmbeddedDeclarations(t *testing.T) {
	dir := t.TempDir()
	store := NewAgentStorage(dir)
	provider := model.ProviderConfig{Protocol: "anthropic-messages", BaseURL: "https://x", APIKey: "k"}

	first := []model.AgentDefinition{
		{Name: "parent", Description: "d", Model: "m", SystemPrompt: "s"},
		{Name: "child-a", Description: "c", SettingSources: []string{"user"},
			Skills: []model.SkillSource{{Name: "skill-one", URL: "https://example.com/1.zip", Hash: strings.Repeat("a", 64)}}},
	}
	require.NoError(t, store.WriteAgentYAML("parent", first, provider, nil, nil, nil))

	second := []model.AgentDefinition{
		{Name: "parent", Description: "d2", Model: "m", SystemPrompt: "s2"},
		{Name: "child-a", Description: "c2", SettingSources: []string{"user"},
			Skills: []model.SkillSource{{Name: "skill-two", URL: "https://example.com/2.zip", Hash: strings.Repeat("c", 64)}}},
	}
	require.NoError(t, store.WriteAgentYAML("parent", second, provider, nil, nil, nil))

	graph, err := store.ReadAgentYAML("parent")
	require.NoError(t, err)
	require.Len(t, graph.Agents, 2)
	var child *model.AgentDefinition
	for i := range graph.Agents {
		if graph.Agents[i].Name == "child-a" {
			child = &graph.Agents[i]
		}
	}
	require.NotNil(t, child)
	require.Len(t, child.Skills, 1)
	assert.Equal(t, "skill-two", child.Skills[0].Name, "redeploy must replace, not merge, artifact declarations")
}

// TestWriteAgentYAML_StageFailurePreservesPreviousDeployment is the
// transactional guarantee the fourth review round asked for: agents.yaml is
// the SINGLE authoritative file (artifacts embedded), staged via tmp +
// atomic rename. A staging failure leaves the previous deployment — graph
// AND artifact declarations — fully intact and losslessly readable.
func TestWriteAgentYAML_StageFailurePreservesPreviousDeployment(t *testing.T) {
	dir := t.TempDir()
	store := NewAgentStorage(dir)
	provider := model.ProviderConfig{Protocol: "anthropic-messages", BaseURL: "https://x", APIKey: "k"}

	first := []model.AgentDefinition{
		{Name: "parent", Description: "first", Model: "m", SystemPrompt: "s"},
		{Name: "child-a", Description: "c", SettingSources: []string{"user"},
			Skills: []model.SkillSource{{Name: "skill-one", URL: "https://example.com/1.zip", Hash: strings.Repeat("a", 64)}}},
	}
	require.NoError(t, store.WriteAgentYAML("parent", first, provider, nil, nil, nil))

	// Squat the staging path: the second write fails BEFORE agents.yaml is
	// touched — there is no separate manifest file to get out of sync.
	require.NoError(t, os.Mkdir(filepath.Join(dir, "parent", "agents", "agents.yaml.tmp"), 0755))

	second := []model.AgentDefinition{
		{Name: "parent", Description: "second", Model: "m", SystemPrompt: "s2"},
		{Name: "child-a", Description: "c2", SettingSources: []string{"user"},
			Skills: []model.SkillSource{{Name: "skill-two", URL: "https://example.com/2.zip", Hash: strings.Repeat("c", 64)}}},
	}
	err := store.WriteAgentYAML("parent", second, provider, nil, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "agent YAML")

	// The previous deployment reads back COMPLETELY intact: the old graph
	// with its own artifact declarations — no loss, no resurrection.
	graph, rerr := store.ReadAgentYAML("parent")
	require.NoError(t, rerr)
	require.Len(t, graph.Agents, 2)
	assert.Equal(t, "first", graph.Agents[0].Description)
	var child *model.AgentDefinition
	for i := range graph.Agents {
		if graph.Agents[i].Name == "child-a" {
			child = &graph.Agents[i]
		}
	}
	require.NotNil(t, child)
	require.Len(t, child.Skills, 1)
	assert.Equal(t, "skill-one", child.Skills[0].Name,
		"the previous deployment's artifact declarations must survive a failed update losslessly")
}

// TestReadAgentYAML_LegacySidecarGenerationMismatchIgnored covers the
// legacy sidecar compatibility path (pre-embedded-section deployments):
// artifacts are merged only when the sidecar's yamlDigest matches the
// agents.yaml actually being read; a mismatched leftover is ignored.
func TestReadAgentYAML_LegacySidecarGenerationMismatchIgnored(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, "parent", "agents")
	require.NoError(t, os.MkdirAll(agentDir, 0755))
	yamlBytes := []byte("agents:\n  - id: parent\n    description: d\n    systemPrompt: s\n")
	require.NoError(t, os.WriteFile(filepath.Join(agentDir, "agents.yaml"), yamlBytes, 0644))

	legacy := map[string]any{
		"rootAgentID": "parent",
		"yamlDigest":  strings.Repeat("0", 64), // digest of a YAML that never landed
		"artifacts": map[string]any{
			"parent": map[string]any{
				"skills": []any{map[string]any{"name": "ghost-skill", "url": "https://example.com/g.zip", "hash": strings.Repeat("e", 64)}},
			},
		},
	}
	out, err := json.Marshal(legacy)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(agentDir, "deploy-manifest.json"), out, 0644))

	graph, rerr := NewAgentStorage(dir).ReadAgentYAML("parent")
	require.NoError(t, rerr)
	require.Len(t, graph.Agents, 1)
	assert.Empty(t, graph.Agents[0].Skills,
		"artifacts from a mismatched legacy manifest generation must not be merged")
}

// TestWriteAgentYAML_LegacySidecarNotRemovedBeforeYAMLCommit covers the
// fifth-review-round window: the legacy sidecar migration must happen AFTER
// the new agents.yaml commits. A failed update leaves the old deployment —
// pre-embedded-section YAML AND its matching sidecar — untouched, so the old
// graph's artifact metadata survives losslessly through the legacy path.
func TestWriteAgentYAML_LegacySidecarNotRemovedBeforeYAMLCommit(t *testing.T) {
	dir := t.TempDir()
	store := NewAgentStorage(dir)
	agentDir := filepath.Join(dir, "parent", "agents")
	require.NoError(t, os.MkdirAll(agentDir, 0755))

	// Pre-embedded-section deployment: YAML without the section + a matching
	// legacy sidecar (correct digest binding).
	oldYAML := []byte("agents:\n  - id: parent\n    description: d\n    systemPrompt: s\n  - id: child-a\n    description: c\n    systemPrompt: p\n")
	require.NoError(t, os.WriteFile(filepath.Join(agentDir, "agents.yaml"), oldYAML, 0644))
	legacy := map[string]any{
		"rootAgentID": "parent",
		"yamlDigest":  sha256Hex(oldYAML),
		"artifacts": map[string]any{
			"child-a": map[string]any{
				"skills": []any{map[string]any{"name": "old-skill", "url": "https://example.com/o.zip", "hash": strings.Repeat("a", 64)}},
			},
		},
	}
	out, err := json.Marshal(legacy)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(agentDir, "deploy-manifest.json"), out, 0644))

	// The redeploy fails while STAGING the new YAML — before any commit.
	require.NoError(t, os.Mkdir(filepath.Join(agentDir, "agents.yaml.tmp"), 0755))
	agents := []model.AgentDefinition{
		{Name: "parent", Description: "d2", Model: "m", SystemPrompt: "s2"},
		{Name: "child-a", Description: "c2", SettingSources: []string{"user"},
			Skills: []model.SkillSource{{Name: "new-skill", URL: "https://example.com/n.zip", Hash: strings.Repeat("c", 64)}}},
	}
	err = store.WriteAgentYAML("parent", agents,
		model.ProviderConfig{Protocol: "anthropic-messages", BaseURL: "https://x", APIKey: "k"}, nil, nil, nil)
	require.Error(t, err)

	// The old deployment — YAML AND its legacy sidecar — is untouched and
	// still reads back with its original artifact metadata.
	assert.FileExists(t, filepath.Join(agentDir, "deploy-manifest.json"),
		"legacy sidecar must survive a failed update")
	graph, rerr := store.ReadAgentYAML("parent")
	require.NoError(t, rerr)
	require.Len(t, graph.Agents, 2)
	var child *model.AgentDefinition
	for i := range graph.Agents {
		if graph.Agents[i].Name == "child-a" {
			child = &graph.Agents[i]
		}
	}
	require.NotNil(t, child)
	require.Len(t, child.Skills, 1)
	assert.Equal(t, "old-skill", child.Skills[0].Name,
		"the old deployment's artifacts must survive via the legacy sidecar path")
}

// TestWriteAgentYAML_LegacySidecarCleanupFailureIsDeferred covers the
// sixth-review-round P2: sidecar cleanup is best-effort and runs AFTER the
// new agents.yaml committed. A cleanup failure must NOT fail the deployment —
// the durable state is the new document, and the leftover sidecar is
// harmless because read-back prefers the embedded section.
func TestWriteAgentYAML_LegacySidecarCleanupFailureIsDeferred(t *testing.T) {
	dir := t.TempDir()
	store := NewAgentStorage(dir)
	agentDir := filepath.Join(dir, "parent", "agents")
	require.NoError(t, os.MkdirAll(agentDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(agentDir, "agents.yaml"), []byte("agents:\n  - id: parent\n    description: d\n"), 0644))

	// Plant a legacy sidecar, then make it undeletable: a non-empty
	// directory squatting on the manifest path defeats os.Remove.
	require.NoError(t, os.Mkdir(filepath.Join(agentDir, "deploy-manifest.json"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(agentDir, "deploy-manifest.json", "junk"), []byte("x"), 0644))

	agents := []model.AgentDefinition{
		{Name: "parent", Description: "d2", Model: "m", SystemPrompt: "s2"},
		{Name: "child-a", Description: "c2", SettingSources: []string{"user"},
			Skills: []model.SkillSource{{Name: "new-skill", URL: "https://example.com/n.zip", Hash: strings.Repeat("c", 64)}}},
	}
	// The deployment SUCCEEDS despite the undeletable residue.
	err := store.WriteAgentYAML("parent", agents,
		model.ProviderConfig{Protocol: "anthropic-messages", BaseURL: "https://x", APIKey: "k"}, nil, nil, nil)
	require.NoError(t, err, "post-commit cleanup failure must not fail the deployment")

	// Durable state is the new document: read-back returns the new graph
	// with its embedded artifact declarations.
	graph, rerr := store.ReadAgentYAML("parent")
	require.NoError(t, rerr)
	require.Len(t, graph.Agents, 2)
	var child *model.AgentDefinition
	for i := range graph.Agents {
		if graph.Agents[i].Name == "child-a" {
			child = &graph.Agents[i]
		}
	}
	require.NotNil(t, child)
	require.Len(t, child.Skills, 1)
	assert.Equal(t, "new-skill", child.Skills[0].Name)
}

// TestWriteAgentYAML_SystemPromptExternalized covers the default
// systemPromptFile externalization (issue #17 follow-up): long prompts are
// staged into prompts/<id>-<sha8>.md and the YAML entry only carries a
// relative reference; agents without a prompt write nothing.
func TestWriteAgentYAML_SystemPromptExternalized(t *testing.T) {
	dir := t.TempDir()
	store := NewAgentStorage(dir)

	agents := []model.AgentDefinition{
		{Name: "parent", Description: "d", Model: "m", SystemPrompt: "You are the parent agent.", Subagents: []string{"child-a"}},
		{Name: "child-a", Description: "c", SystemPrompt: "You are the child agent."},
		{Name: "child-b", Description: "empty"}, // no prompt
	}
	require.NoError(t, store.WriteAgentYAML("parent", agents, model.ProviderConfig{}, nil, nil, nil))

	agentDir := filepath.Join(dir, "parent", "agents")
	yamlBytes, err := os.ReadFile(filepath.Join(agentDir, "agents.yaml"))
	require.NoError(t, err)
	text := string(yamlBytes)
	assert.NotContains(t, text, "systemPrompt:", "prompts must not be inlined")
	assert.NotContains(t, text, "You are the parent agent.", "prompt text must not appear in the YAML")

	// Each prompting agent gets an external reference + a staged file.
	var doc struct {
		Agents []map[string]any `yaml:"agents"`
	}
	require.NoError(t, yaml.Unmarshal(yamlBytes, &doc))
	byID := map[string]map[string]any{}
	for _, e := range doc.Agents {
		byID[e["id"].(string)] = e
	}
	for _, id := range []string{"parent", "child-a"} {
		ref, ok := byID[id]["systemPromptFile"].(string)
		require.True(t, ok, "agent %s must externalize its prompt", id)
		assert.True(t, strings.HasPrefix(ref, "./prompts/"+id+"-"), "ref %q must live under prompts/ with a hash suffix", ref)
		content, rerr := os.ReadFile(filepath.Join(agentDir, filepath.FromSlash(strings.TrimPrefix(ref, "./"))))
		require.NoError(t, rerr)
		want := "You are the parent agent."
		if id == "child-a" {
			want = "You are the child agent."
		}
		assert.Equal(t, want, string(content))
	}
	_, hasEmpty := byID["child-b"]["systemPromptFile"]
	assert.False(t, hasEmpty, "agent without a prompt must not carry a prompt file reference")
	assert.NotContains(t, text, "child-b-", "no prompt file for empty prompts")
}

// TestReadAgentYAML_SystemPromptRoundTrip ensures the graph read-back keeps
// the prompt TEXT (API shape unchanged) even though the YAML stores a file
// reference.
func TestReadAgentYAML_SystemPromptRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store := NewAgentStorage(dir)

	agents := []model.AgentDefinition{
		{Name: "parent", Description: "d", Model: "m", SystemPrompt: "Long parent prompt", Subagents: []string{"child-a"}},
		{Name: "child-a", Description: "c", SystemPrompt: "Long child prompt"},
	}
	require.NoError(t, store.WriteAgentYAML("parent", agents, model.ProviderConfig{}, nil, nil, nil))

	graph, err := store.ReadAgentYAML("parent")
	require.NoError(t, err)
	require.Len(t, graph.Agents, 2)
	assert.Equal(t, agents[0].SystemPrompt, graph.Agents[0].SystemPrompt)
	assert.Equal(t, agents[1].SystemPrompt, graph.Agents[1].SystemPrompt)
}

// TestWriteAgentYAML_PromptStageFailurePreservesDeployment guards the write
// order: system prompt files are staged BEFORE the atomic agents.yaml
// commit, so a prompt staging failure leaves the previous deployment fully
// intact.
func TestWriteAgentYAML_PromptStageFailurePreservesDeployment(t *testing.T) {
	dir := t.TempDir()
	store := NewAgentStorage(dir)

	first := []model.AgentDefinition{
		{Name: "parent", Description: "first", Model: "m", SystemPrompt: "Old prompt"},
	}
	require.NoError(t, store.WriteAgentYAML("parent", first, model.ProviderConfig{}, nil, nil, nil))
	agentDir := filepath.Join(dir, "parent", "agents")

	// Make prompt staging fail: replace the prompts directory with a file.
	require.NoError(t, os.RemoveAll(filepath.Join(agentDir, "prompts")))
	require.NoError(t, os.WriteFile(filepath.Join(agentDir, "prompts"), []byte("blocker"), 0644))

	second := []model.AgentDefinition{
		{Name: "parent", Description: "second", Model: "m", SystemPrompt: "New prompt"},
	}
	err := store.WriteAgentYAML("parent", second, model.ProviderConfig{}, nil, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "prompts")

	// Old deployment untouched: agents.yaml still describes the first
	// graph. (Read-back through the deliberately broken prompts path fails
	// explicitly by design — disk corruption is surfaced, never silently
	// degraded, same philosophy as the corrupt-manifest regression.)
	yamlBytes, rerr := os.ReadFile(filepath.Join(agentDir, "agents.yaml"))
	require.NoError(t, rerr)
	assert.Contains(t, string(yamlBytes), "first")
}

// TestReadAgentYAML_SystemPromptFileTraversalRejected keeps a hand-edited
// YAML's systemPromptFile reference inside the agents directory: a reference
// escaping prompts/ is an explicit error, not a silent read.
func TestReadAgentYAML_SystemPromptFileTraversalRejected(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, "evil", "agents")
	require.NoError(t, os.MkdirAll(agentDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(agentDir, "agents.yaml"),
		[]byte("agents:\n  - id: evil\n    description: d\n    systemPromptFile: ../../victim.txt\n"), 0644))

	_, err := NewAgentStorage(dir).ReadAgentYAML("evil")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "escapes the prompts directory")
}
