package storage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/zerone-agent/agent-deployer/internal/model"
	"gopkg.in/yaml.v3"
)

// ErrNoAgents is returned when a YAML document contains no agents.
var ErrNoAgents = errors.New("no agents found in YAML")

// AgentStorage persists agent definitions as YAML files.
type AgentStorage struct {
	dataDir string
}

// NewAgentStorage creates a new AgentStorage rooted at dataDir.
// Each agent's files live under <dataDir>/<name>/agents/.
func NewAgentStorage(dataDir string) *AgentStorage {
	return &AgentStorage{dataDir: dataDir}
}

// runtimeAgentEntry is the shape of a single agent inside the runtime agents.yaml list.
// Field names match the open-agent-runtime Zod schema (config.js).
type runtimeAgentEntry struct {
	ID              string                           `yaml:"id"`
	Name            string                           `yaml:"name,omitempty"`
	Model           string                           `yaml:"model,omitempty"`
	SystemPrompt    string                           `yaml:"systemPrompt,omitempty"`
	MaxTurns        *int                             `yaml:"maxTurns,omitempty"`
	MaxSessionTurns *int                             `yaml:"maxSessionTurns,omitempty"`
	PermissionMode  string                           `yaml:"permissionMode,omitempty"`
	AllowedTools    []string                         `yaml:"allowedTools,omitempty"`
	SettingSources  []string                         `yaml:"settingSources,omitempty"`
	Subagents       map[string]runtimeSubagent       `yaml:"subagents,omitempty"`
	McpServers      map[string]model.McpServerConfig `yaml:"mcpServers,omitempty"`
	Datasets        map[string]string                `yaml:"datasets,omitempty"`
}

// runtimeSubagent is the shape of a subagent in the runtime YAML format.
type runtimeSubagent struct {
	Description string   `yaml:"description,omitempty"`
	Prompt      string   `yaml:"prompt,omitempty"`
	Tools       []string `yaml:"tools,omitempty"`
	MaxTurns    *int     `yaml:"maxTurns,omitempty"`
}

// runtimeAigcSection is the shape of the top-level "aigc" section in the
// runtime agents.yaml. Field names mirror the open-agent-runtime Zod schema
// (AigcConfigSchema in src/config.ts) exactly.
type runtimeAigcSection struct {
	Enabled         bool              `yaml:"enabled"`
	ContentProducer string            `yaml:"contentProducer"`
	SigningKey      string            `yaml:"signingKey,omitempty"`
	ExplicitHint    *bool             `yaml:"explicitHint,omitempty"`
	Label           *string           `yaml:"label,omitempty"`
	ProduceIdPrefix string            `yaml:"produceIdPrefix,omitempty"`
	ModelCodes      map[string]string `yaml:"modelCodes,omitempty"`
}

// runtimeAgentsYAML is the top-level runtime YAML document.
type runtimeAgentsYAML struct {
	Aigc   *runtimeAigcSection `yaml:"aigc,omitempty"`
	Agents []runtimeAgentEntry `yaml:"agents"`
}

// WriteAgentYAML writes the agent definition to <agentsDir>/<name>/agents.yaml
// in the runtime agents.yaml format.
func (s *AgentStorage) WriteAgentYAML(name string, agent model.AgentDefinition, aigc *model.AigcConfig) error {
	if strings.TrimSpace(agent.Name) == "" {
		return fmt.Errorf("agent.Name is required")
	}
	if agent.Name != name {
		return fmt.Errorf("agent.Name %q does not match storage name %q", agent.Name, name)
	}
	// Defense-in-depth: reject path traversal in the name parameter so a
	// caller cannot escape agentsDir via "../" or absolute paths.
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("invalid agent name %q: must be a single path segment", name)
	}

	settingSources := agent.SettingSources
	if len(settingSources) == 0 {
		settingSources = []string{"project"}
	}

	entry := runtimeAgentEntry{
		ID:              name,
		Name:            name,
		Model:           agent.Model,
		SystemPrompt:    agent.SystemPrompt,
		MaxTurns:        agent.MaxTurns,
		MaxSessionTurns: agent.MaxSessionTurns,
		PermissionMode:  agent.PermissionMode,
		AllowedTools:    agent.Tools,
		SettingSources:  settingSources,
		McpServers:      agent.McpServers,
		Datasets:        agent.Datasets,
	}

	if len(agent.Subagents) > 0 {
		entry.Subagents = make(map[string]runtimeSubagent, len(agent.Subagents))
		for _, sub := range agent.Subagents {
			entry.Subagents[sub.Name] = runtimeSubagent{
				Description: sub.Description,
				Prompt:      sub.Prompt,
				Tools:       sub.Tools,
				MaxTurns:    sub.MaxTurns,
			}
		}
	}

	doc := runtimeAgentsYAML{Agents: []runtimeAgentEntry{entry}}

	if aigc != nil && aigc.Enabled {
		hint := aigc.ExplicitHint
		if hint == nil {
			defaultTrue := true
			hint = &defaultTrue
		}
		doc.Aigc = &runtimeAigcSection{
			Enabled:         aigc.Enabled,
			ContentProducer: aigc.ContentProducer,
			SigningKey:      aigc.SigningKey,
			ExplicitHint:    hint,
			Label:           aigc.Label,
			ProduceIdPrefix: aigc.ProduceIdPrefix,
			ModelCodes:      aigc.ModelCodes,
		}
	}

	data, err := yaml.Marshal(&doc)
	if err != nil {
		return fmt.Errorf("marshal agent YAML: %w", err)
	}

	agentDir := filepath.Join(s.dataDir, name, "agents")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		return fmt.Errorf("create agent directory: %w", err)
	}

	filePath := filepath.Join(agentDir, "agents.yaml")
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("write agent YAML: %w", err)
	}

	return nil
}

// ReadAgentYAML reads the agent definition from <agentsDir>/<name>/agents.yaml.
func (s *AgentStorage) ReadAgentYAML(name string) (*model.AgentDefinition, error) {
	filePath := filepath.Join(s.dataDir, name, "agents", "agents.yaml")
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("read agent YAML: %w", err)
	}

	var doc runtimeAgentsYAML
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("unmarshal agent YAML: %w", err)
	}

	if len(doc.Agents) == 0 {
		return nil, ErrNoAgents
	}

	entry := doc.Agents[0]
	agent := model.AgentDefinition{
		Name:            entry.Name,
		Model:           entry.Model,
		SystemPrompt:    entry.SystemPrompt,
		MaxTurns:        entry.MaxTurns,
		MaxSessionTurns: entry.MaxSessionTurns,
		PermissionMode:  entry.PermissionMode,
		Tools:           entry.AllowedTools,
		SettingSources:  entry.SettingSources,
		McpServers:      entry.McpServers,
		Datasets:        entry.Datasets,
	}

	if len(entry.Subagents) > 0 {
		names := make([]string, 0, len(entry.Subagents))
		for subName := range entry.Subagents {
			names = append(names, subName)
		}
		sort.Strings(names)
		agent.Subagents = make([]model.SubagentDefinition, 0, len(names))
		for _, subName := range names {
			sub := entry.Subagents[subName]
			agent.Subagents = append(agent.Subagents, model.SubagentDefinition{
				Name:        subName,
				Description: sub.Description,
				Prompt:      sub.Prompt,
				Tools:       sub.Tools,
				MaxTurns:    sub.MaxTurns,
			})
		}
	}

	return &agent, nil
}

// EnsureDirs creates all the given directories with mode 0755 if they don't exist.
// Returns an error if any directory cannot be created. All parent directories are
// created as needed.
func EnsureDirs(dirs ...string) error {
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			return fmt.Errorf("create directory %q: %w", d, err)
		}
	}
	return nil
}

// RemoveAll removes the given path and everything it contains. Returns an error
// wrapped with context if removal fails. A non-existent path is not an error.
func RemoveAll(path string) error {
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("remove %q: %w", path, err)
	}
	return nil
}

// Exists reports whether an agent's storage directory exists on disk.
// It returns true when <dataDir>/<name>/agents/agents.yaml is present.
func (s *AgentStorage) Exists(name string) bool {
	yamlPath := filepath.Join(s.dataDir, name, "agents", "agents.yaml")
	_, err := os.Stat(yamlPath)
	return err == nil
}

// ListAgentDirs scans dataDir and returns the names of all immediate child
// directories that look like agent storage roots. A directory qualifies when
// it contains an "agents/agents.yaml" file. The returned slice is sorted.
// Errors reading dataDir itself are returned; unreadable sub-directories are
// skipped so that one bad agent directory does not break the whole listing.
func (s *AgentStorage) ListAgentDirs() ([]string, error) {
	entries, err := os.ReadDir(s.dataDir)
	if err != nil {
		return nil, fmt.Errorf("read data dir %q: %w", s.dataDir, err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		// Skip the internal temp dir used by skill installs.
		if name == ".skills-tmp" {
			continue
		}
		yamlPath := filepath.Join(s.dataDir, name, "agents", "agents.yaml")
		if _, err := os.Stat(yamlPath); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			// Skip unreadable entries gracefully.
			continue
		}
		names = append(names, name)
	}

	sort.Strings(names)
	return names, nil
}
