package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/zerone-agent/agent-deployer/internal/model"
	"github.com/zerone-agent/agent-deployer/internal/naming"
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
	DisallowedTools []string `yaml:"disallowedTools,omitempty"`
	// CustomTools lists verified tool file paths relative to the configDir (issue
	// #10); each entry carries only the paths its own agent declared. The
	// runtime resolves them itself.
	CustomTools        []string                         `yaml:"customTools,omitempty"`
	SettingSources     []string                         `yaml:"settingSources,omitempty"`
	ExtraUserSkillDirs []string                         `yaml:"extraUserSkillDirs,omitempty"`
	Subagents          []string                         `yaml:"subagents,omitempty"`
	McpServers         map[string]model.McpServerConfig `yaml:"mcpServers,omitempty"`
	Datasets           map[string]string                `yaml:"datasets,omitempty"`
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
	// XDeployerManifest embeds the deployer-private artifact declarations
	// (Skill/Tool url+hash) in the SAME authoritative file. The runtime's
	// zod schema strips unknown top-level keys, so this section is invisible
	// to the runtime while making the write a single atomic file replacement
	// (fourth review round: one authoritative persisted document).
	XDeployerManifest map[string]agentArtifacts `yaml:"x-deployer-manifest,omitempty"`
}

// WriteAgentYAML writes the complete agent graph to
// <dataDir>/<rootName>/agents/agents.yaml in the runtime v2.4.0+ format: every
// agent is a first-class entry and subagents are pure id references. Provider
// credentials and model (runtime-global) are written to the root entry only.
// toolPaths maps agent id -> verified "./tools/..." paths for that agent.
// Agents that declare skills get their per-agent skill dir injected as an
// extraUserSkillDirs entry (user-level scan; see the service install layout).
func (s *AgentStorage) WriteAgentYAML(rootName string, agents []model.AgentDefinition, provider model.ProviderConfig, aigc *model.AigcConfig, hub *model.HubConfig, toolPaths map[string][]string) error {
	// Defense-in-depth: reject path traversal in the name parameter so a
	// caller cannot escape dataDir via "../" or absolute paths.
	if rootName == "" || rootName == "." || rootName == ".." || strings.ContainsAny(rootName, `/\`) {
		return fmt.Errorf("invalid agent name %q: must be a single path segment", rootName)
	}

	byID := make(map[string]bool, len(agents))
	for _, a := range agents {
		byID[a.Name] = true
	}
	if !byID[rootName] {
		return fmt.Errorf("no agent with id %q in graph", rootName)
	}

	entries := make([]runtimeAgentEntry, 0, len(agents))
	for _, a := range agents {
		entry := runtimeAgentEntry{
			ID:              a.Name,
			Name:            a.Name,
			Description:     a.Description,
			SystemPrompt:    a.SystemPrompt,
			MaxTurns:        a.MaxTurns,
			AllowedTools:    a.Tools,
			DisallowedTools: a.DisallowedTools,
			SettingSources:  a.SettingSources,
			Subagents:       a.Subagents,
			McpServers:      a.McpServers,
			Datasets:        a.Datasets,
		}

		if a.Name == rootName {
			// Runtime-global execution environment (issue #16): credentials and
			// model on the root entry only; mounted agents reuse the root
			// process environment and never receive their own copy.
			entry.Model = a.Model
			entry.MaxSessionTurns = a.MaxSessionTurns
			entry.PermissionMode = a.PermissionMode
			entry.APIKey = provider.APIKey
			entry.BaseURL = provider.BaseURL
			entry.APIType = provider.Protocol
			if len(entry.SettingSources) == 0 {
				entry.SettingSources = []string{"project"}
			}
		}

		if paths := toolPaths[a.Name]; len(paths) > 0 {
			sorted := make([]string, len(paths))
			copy(sorted, paths)
			sort.Strings(sorted)
			entry.CustomTools = sorted
		}

		// Per-agent skill visibility: skills install under
		// <agentsDir>/skills/<id>/ and are scanned at user level. model
		// validation already guarantees settingSources contains "user".
		if len(a.Skills) > 0 {
			skillDir := naming.ContainerSkillDir(a.Name)
			if !containsString(entry.ExtraUserSkillDirs, skillDir) {
				entry.ExtraUserSkillDirs = append(entry.ExtraUserSkillDirs, skillDir)
			}
		}

		entries = append(entries, entry)
	}

	doc := runtimeAgentsYAML{Agents: entries}

	// Embed the artifact declarations that the runtime schema cannot express.
	// Keeping them in the SAME file is what makes the update transactional:
	// one document, one atomic rename, no cross-file window.
	embedded := make(map[string]agentArtifacts, len(agents))
	for _, a := range agents {
		if len(a.Skills) == 0 && len(a.CustomTools) == 0 {
			continue
		}
		embedded[a.Name] = agentArtifacts{Skills: a.Skills, CustomTools: a.CustomTools}
	}
	if len(embedded) > 0 {
		doc.XDeployerManifest = embedded
	}

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

	agentDir := filepath.Join(s.dataDir, rootName, "agents")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		return fmt.Errorf("create agent directory: %w", err)
	}

	// Migrate away any legacy sidecar from pre-embedded-section deployments;
	// the embedded section above supersedes it, and read-back must never mix
	// the two representations.
	if err := removeLegacyManifest(agentDir); err != nil {
		return err
	}

	// agents.yaml is the SINGLE authoritative document (graph + artifact
	// declarations): staged tmp + atomic rename. A failed or interrupted
	// update leaves the previous deployment fully intact and losslessly
	// readable — no separate manifest file can get out of sync.
	yamlTmp := filepath.Join(agentDir, "agents.yaml.tmp")
	if err := os.WriteFile(yamlTmp, data, 0644); err != nil {
		return fmt.Errorf("write agent YAML: %w", err)
	}
	if err := os.Rename(yamlTmp, filepath.Join(agentDir, "agents.yaml")); err != nil {
		return fmt.Errorf("replace agent YAML: %w", err)
	}

	return nil
}

// removeLegacyManifest deletes the deploy-manifest.json sidecar written by
// pre-embedded-section deployer versions. New deployments embed artifact
// declarations inside agents.yaml; leftover sidecars are migrated away on the
// next write so read-back never mixes representations.
func removeLegacyManifest(agentDir string) error {
	if err := os.Remove(filepath.Join(agentDir, "deploy-manifest.json")); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove legacy deploy manifest: %w", err)
	}
	return nil
}

// deploymentManifest is the LEGACY sidecar format written by deployer
// versions before artifact declarations were embedded in agents.yaml's
// x-deployer-manifest section. It is kept read-only for compatibility:
// YAMLDigest binds it to the exact agents.yaml generation it was written
// for, and ReadAgentYAML merges its artifacts only on a digest match — a
// leftover from an aborted generation is ignored instead of polluting the
// current graph. New writes embed the section and remove the sidecar.
type deploymentManifest struct {
	RootAgentID string                    `json:"rootAgentId"`
	YAMLDigest  string                    `json:"yamlDigest"`
	Artifacts   map[string]agentArtifacts `json:"artifacts"` // agent id → declared artifacts
}

type agentArtifacts struct {
	Skills      []model.SkillSource `yaml:"skills,omitempty" json:"skills,omitempty"`
	CustomTools []model.ToolSource  `yaml:"customTools,omitempty" json:"customTools,omitempty"`
}

// loadDeploymentManifest merges the persisted artifact declarations into the
// graph read from agents.yaml — but ONLY when the manifest's recorded
// yamlDigest matches the agents.yaml actually being read (committed
// generation). A mismatched manifest is an aborted-generation leftover and is
// silently ignored: the interruption window fails toward "artifacts lost",
// never "artifacts resurrected". A missing manifest (pre-manifest or
// hand-written deployments) likewise reads back artifact-free; a corrupt
// manifest fails explicitly because a silently lossy round trip is worse.
func (s *AgentStorage) loadDeploymentManifest(name string, graph *AgentGraph, yamlDigest string) error {
	data, err := os.ReadFile(filepath.Join(s.dataDir, name, "agents", "deploy-manifest.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read deploy-manifest: %w", err)
	}
	var m deploymentManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return fmt.Errorf("parse deploy-manifest: %w", err)
	}
	if m.YAMLDigest == "" || m.YAMLDigest != yamlDigest {
		return nil
	}
	for i := range graph.Agents {
		arts := m.Artifacts[graph.Agents[i].Name]
		if arts.Skills != nil {
			graph.Agents[i].Skills = arts.Skills
		}
		if arts.CustomTools != nil {
			graph.Agents[i].CustomTools = arts.CustomTools
		}
	}
	return nil
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// AgentGraph is the complete agent graph read back from agents.yaml.
type AgentGraph struct {
	RootAgentID string
	Agents      []model.AgentDefinition
}

// ReadAgentYAML reads the complete agent graph from
// <dataDir>/<name>/agents/agents.yaml. The root entry is the one whose id
// matches the storage name. Artifact declarations (Skill/Tool url+hash) are
// not expressible in the runtime YAML; they are restored from the
// deploy-manifest.json sidecar when present (see deploymentManifest).
func (s *AgentStorage) ReadAgentYAML(name string) (*AgentGraph, error) {
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

	graph := &AgentGraph{RootAgentID: name}
	foundRoot := false
	for _, e := range doc.Agents {
		entryName := e.Name
		if entryName == "" {
			entryName = e.ID
		}
		if e.ID == name {
			foundRoot = true
		}
		graph.Agents = append(graph.Agents, model.AgentDefinition{
			Name:            entryName,
			Description:     e.Description,
			Model:           e.Model,
			SystemPrompt:    e.SystemPrompt,
			MaxTurns:        e.MaxTurns,
			MaxSessionTurns: e.MaxSessionTurns,
			PermissionMode:  e.PermissionMode,
			Tools:           e.AllowedTools,
			DisallowedTools: e.DisallowedTools,
			SettingSources:  e.SettingSources,
			McpServers:      e.McpServers,
			Datasets:        e.Datasets,
			Subagents:       e.Subagents,
		})
	}
	if !foundRoot {
		return nil, fmt.Errorf("no agent entry with id %q in YAML", name)
	}
	// Artifact declarations: prefer the embedded x-deployer-manifest section —
	// same file, same generation, atomic by construction. Deployments written
	// before the section existed fall back to the legacy sidecar, merged only
	// on a digest match.
	if len(doc.XDeployerManifest) > 0 {
		for i := range graph.Agents {
			arts := doc.XDeployerManifest[graph.Agents[i].Name]
			if arts.Skills != nil {
				graph.Agents[i].Skills = arts.Skills
			}
			if arts.CustomTools != nil {
				graph.Agents[i].CustomTools = arts.CustomTools
			}
		}
		return graph, nil
	}
	if err := s.loadDeploymentManifest(name, graph, sha256Hex(data)); err != nil {
		return nil, err
	}
	return graph, nil
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
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
