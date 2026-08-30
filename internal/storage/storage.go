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
// Field names match the agent-runtime 2.0 Zod schema (src/config.ts).
//
// Since 2.0, every agent is a first-class top-level entry: `description` is
// required and `subagents` is a list of id references to other entries in the
// same file (inline subagent definitions were removed).
type runtimeAgentEntry struct {
	ID          string `yaml:"id"`
	Name        string `yaml:"name,omitempty"`
	Description string `yaml:"description"`
	Model       string `yaml:"model,omitempty"`
	// Provider credentials, written to the main entry only (runtime 2.0
	// per-agent credentials; subagent mounting ignores them). apiType is
	// provider.Protocol passed through verbatim — both sides use the same
	// enum (anthropic-messages / openai-completions). Field order mirrors
	// the runtime's own docs example: model, apiType, baseURL, apiKey.
	APIType         string   `yaml:"apiType,omitempty"`
	BaseURL         string   `yaml:"baseURL,omitempty"`
	APIKey          string   `yaml:"apiKey,omitempty"`
	SystemPrompt    string   `yaml:"systemPrompt,omitempty"`
	MaxTurns        *int     `yaml:"maxTurns,omitempty"`
	MaxSessionTurns *int     `yaml:"maxSessionTurns,omitempty"`
	PermissionMode  string   `yaml:"permissionMode,omitempty"`
	AllowedTools    []string `yaml:"allowedTools,omitempty"`
	// CustomTools lists verified tool file paths relative to the configDir
	// (issue #10). Main entry only; the runtime resolves them itself.
	CustomTools    []string                         `yaml:"customTools,omitempty"`
	SettingSources []string                         `yaml:"settingSources,omitempty"`
	Subagents      []string                         `yaml:"subagents,omitempty"`
	McpServers     map[string]model.McpServerConfig `yaml:"mcpServers,omitempty"`
	Datasets       map[string]string                `yaml:"datasets,omitempty"`
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

// runtimeHubSection is the shape of the top-level "hub" section in the
// runtime agents.yaml. Field names mirror the open-agent-runtime schema
// (src/config.ts, resolveHubConfig) exactly. The section is written only
// when the push channel is enabled; absence means "push disabled".
type runtimeHubSection struct {
	Enabled     bool   `yaml:"enabled"`
	BaseURL     string `yaml:"baseUrl"`
	ChatPushKey string `yaml:"chatPushKey"`
	Org         string `yaml:"org,omitempty"`
}

// runtimeAgentsYAML is the top-level runtime YAML document.
type runtimeAgentsYAML struct {
	Aigc   *runtimeAigcSection `yaml:"aigc,omitempty"`
	Hub    *runtimeHubSection  `yaml:"hub,omitempty"`
	Agents []runtimeAgentEntry `yaml:"agents"`
}

// WriteAgentYAML writes the agent definition to <agentsDir>/<name>/agents.yaml
// in the runtime agents.yaml format. customToolPaths are written verbatim
// (sorted, "./"-relative to the configDir) under the main entry's customTools;
// nil/empty omits the key.
func (s *AgentStorage) WriteAgentYAML(name string, agent model.AgentDefinition, provider model.ProviderConfig, aigc *model.AigcConfig, hub *model.HubConfig, customToolPaths []string) error {
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
		Description:     agent.Description,
		Model:           agent.Model,
		SystemPrompt:    agent.SystemPrompt,
		MaxTurns:        agent.MaxTurns,
		MaxSessionTurns: agent.MaxSessionTurns,
		PermissionMode:  agent.PermissionMode,
		AllowedTools:    agent.Tools,
		SettingSources:  settingSources,
		McpServers:      agent.McpServers,
		Datasets:        agent.Datasets,
		APIKey:          provider.APIKey,
		BaseURL:         provider.BaseURL,
		APIType:         provider.Protocol,
	}

	if len(customToolPaths) > 0 {
		sorted := make([]string, len(customToolPaths))
		copy(sorted, customToolPaths)
		sort.Strings(sorted)
		entry.CustomTools = sorted
	}

	entries := []runtimeAgentEntry{entry}

	// Runtime 2.0 mount-by-reference: each subagent becomes a first-class
	// top-level entry, and the main entry references it by id. Subagent
	// entries carry only the 5 fields the runtime maps when mounting
	// (description, systemPrompt, allowedTools, disallowedTools, maxTurns) —
	// model, mcpServers and per-agent credentials are intentionally omitted
	// because they do not apply in the mounted context.
	if len(agent.Subagents) > 0 {
		entries[0].Subagents = make([]string, 0, len(agent.Subagents))
		for _, sub := range agent.Subagents {
			entries[0].Subagents = append(entries[0].Subagents, sub.Name)
			entries = append(entries, runtimeAgentEntry{
				ID:           sub.Name,
				Name:         sub.Name,
				Description:  sub.Description,
				SystemPrompt: sub.Prompt,
				AllowedTools: sub.Tools,
				MaxTurns:     sub.MaxTurns,
			})
		}
	}

	doc := runtimeAgentsYAML{Agents: entries}

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

	if hub != nil && hub.Enabled {
		doc.Hub = &runtimeHubSection{
			Enabled:     hub.Enabled,
			BaseURL:     hub.BaseURL,
			ChatPushKey: hub.ChatPushKey,
			Org:         hub.Org,
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

	// The main entry is the one whose id matches the storage name; subagent
	// entries share the same file as first-class agents.
	byID := make(map[string]runtimeAgentEntry, len(doc.Agents))
	for _, e := range doc.Agents {
		byID[e.ID] = e
	}
	entry, ok := byID[name]
	if !ok {
		return nil, fmt.Errorf("no agent entry with id %q in YAML", name)
	}

	// `name` is optional in the runtime schema (falls back to id); mirror that.
	entryName := entry.Name
	if entryName == "" {
		entryName = entry.ID
	}

	agent := model.AgentDefinition{
		Name:            entryName,
		Description:     entry.Description,
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

	// Resolve subagent id references back to their definitions. Unknown
	// references are skipped: the runtime rejects such configs at startup, so
	// a readable file here implies they resolve; defensively ignore anyway.
	for _, subID := range entry.Subagents {
		sub, ok := byID[subID]
		if !ok {
			continue
		}
		agent.Subagents = append(agent.Subagents, model.SubagentDefinition{
			Name:        subID,
			Description: sub.Description,
			Prompt:      sub.SystemPrompt,
			Tools:       sub.AllowedTools,
			MaxTurns:    sub.MaxTurns,
		})
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
