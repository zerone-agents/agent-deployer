package model

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
)

// SubagentDefinition defines a subagent within an agent configuration.
//
// Note: there is intentionally no MaxSessionTurns field here. Subagents are
// spawned one-shot (a fresh sessionId per spawn, single submitMessage), so the
// "session-level history truncation" semantic of maxSessionTurns does not
// apply. Tighten MaxTurns if a subagent runs too long. See agent-runtime
// issue #1 for the full rationale.
type SubagentDefinition struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Prompt      string            `json:"prompt"`
	Tools       []string          `json:"tools,omitempty"`
	MaxTurns    *int              `json:"maxTurns,omitempty"`
	Datasets    map[string]string `json:"datasets,omitempty"`
}

// McpServerConfig describes a single MCP server configuration.
// The JSON API uses the "type" field name (matching middle-ground's McpClientDTO),
// while the YAML persisted to disk uses "transport" (matching open-agent-runtime).
// Only "sse" and "http" transports are supported; stdio is intentionally disabled
// because middle-ground does not expose stdio MCP servers.
type McpServerConfig struct {
	Type    string            `json:"type" yaml:"transport"`
	URL     string            `json:"url" yaml:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty" yaml:"headers,omitempty"`
}

// Validate checks the MCP server configuration for supported transports and required fields.
func (m McpServerConfig) Validate() error {
	switch m.Type {
	case "sse", "http":
		if strings.TrimSpace(m.URL) == "" {
			return fmt.Errorf("url is required for %s transport", m.Type)
		}
		u, err := url.Parse(m.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("url must be http(s) with a host")
		}
	case "":
		return fmt.Errorf("type is required")
	default:
		return fmt.Errorf("type must be one of: sse, http")
	}
	return nil
}

// artifactNamePattern restricts artifact source names (skill AND tool) to
// safe path characters. Allowed: letters, digits, dot, underscore, hyphen.
// Length 1-64.
var artifactNamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// validateArtifactName checks that an artifact source name is present and a
// single safe path segment. Shared by SkillSource and ToolSource.
func validateArtifactName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("name is required")
	}
	if name == "." || name == ".." {
		return fmt.Errorf("name cannot be %q", name)
	}
	if !artifactNamePattern.MatchString(name) {
		return fmt.Errorf("name must match [A-Za-z0-9._-]{1,64}: %q", name)
	}
	return nil
}

// validateArtifactURL checks that an artifact source URL is an absolute
// http(s) URL with a host. Shared by SkillSource and ToolSource.
func validateArtifactURL(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return fmt.Errorf("url is required")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("url must be http(s) with a host")
	}
	return nil
}

// validateArtifactHash checks that a declared hash is a 64-hex sha256,
// optionally prefixed with "sha256:". Shared by SkillSource and ToolSource.
func validateArtifactHash(hash string) error {
	if strings.TrimSpace(hash) == "" {
		return fmt.Errorf("hash is required")
	}
	if !isValidSha256Hex(normalizeSha256(hash)) {
		return fmt.Errorf("hash must be 64 hex chars (with optional sha256: prefix)")
	}
	return nil
}

// normalizeSha256 trims, lowercases, and strips the optional "sha256:" prefix.
func normalizeSha256(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	return strings.TrimPrefix(h, "sha256:")
}

// SkillSource describes a single skill zip archive to download and extract.
// Hash is the sha256 of the zip bytes, optionally prefixed with "sha256:".
type SkillSource struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Hash string `json:"hash"`
}

// Validate checks required fields and formats.
func (s SkillSource) Validate() error {
	if err := validateArtifactName(s.Name); err != nil {
		return err
	}
	if err := validateArtifactURL(s.URL); err != nil {
		return err
	}
	return validateArtifactHash(s.Hash)
}

// NormalizedHash returns the hash with optional "sha256:" prefix stripped,
// lowercased, and whitespace trimmed. Does NOT revalidate.
func (s SkillSource) NormalizedHash() string { return normalizeSha256(s.Hash) }

// isValidSha256Hex reports whether s is exactly 64 lowercase hex chars.
func isValidSha256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		default:
			return false
		}
	}
	return true
}

// toolFileExts are the file extensions the runtime's customTools loader
// accepts (issue #10). Case-sensitive: ".TS" is rejected.
var toolFileExts = map[string]struct{}{".ts": {}, ".mts": {}, ".js": {}, ".mjs": {}}

// ToolSource describes a single custom Tool file to download and install.
// Hash is the sha256 of the file bytes, optionally prefixed with "sha256:".
//
// FileName is metadata and an extension source ONLY: its directory
// components are never used when constructing the local path — the local
// file name is always derived from Name plus the validated extension.
// The runtime derives the Tool name from the file's default-exported
// definition (required `name` field), never from this file name.
type ToolSource struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	Hash     string `json:"hash"`
	FileName string `json:"fileName"`
}

// Ext returns the file extension of FileName (with leading dot).
func (t ToolSource) Ext() string { return filepath.Ext(t.FileName) }

// LocalFileName returns the deterministic local file name for this Tool:
// the validated Name plus the validated extension, e.g. "GetWeather.mjs".
func (t ToolSource) LocalFileName() string { return t.Name + t.Ext() }

// LocalRelPath returns the path written into the runtime agents.yaml,
// relative to the configDir (the agents/ directory bind-mounted at
// /app/config): "./tools/GetWeather.mjs".
func (t ToolSource) LocalRelPath() string { return "./tools/" + t.LocalFileName() }

// NormalizedHash returns the hash with optional "sha256:" prefix stripped,
// lowercased, and whitespace trimmed. Does NOT revalidate.
func (t ToolSource) NormalizedHash() string { return normalizeSha256(t.Hash) }

// Validate checks required fields and formats (issue #10 contract).
func (t ToolSource) Validate() error {
	if err := validateArtifactName(t.Name); err != nil {
		return err
	}
	if err := validateArtifactURL(t.URL); err != nil {
		return err
	}
	if err := validateArtifactHash(t.Hash); err != nil {
		return err
	}

	if strings.TrimSpace(t.FileName) == "" {
		return fmt.Errorf("fileName is required")
	}
	if _, ok := toolFileExts[t.Ext()]; !ok {
		return fmt.Errorf("fileName extension must be one of: .ts, .mts, .js, .mjs")
	}
	return nil
}

// AgentDefinition defines the agent configuration received from the client.
type AgentDefinition struct {
	Name string `json:"name"`
	// Description is a short human/agent-readable summary of what the agent
	// does. Required: agent-runtime 2.0 rejects configs without it, and it is
	// what the parent agent's Task tool shows when mounting subagents.
	Description     string   `json:"description"`
	Model           string   `json:"model"`
	SystemPrompt    string   `json:"systemPrompt"`
	MaxTurns        *int     `json:"maxTurns"`
	MaxSessionTurns *int     `json:"maxSessionTurns,omitempty"`
	PermissionMode  string   `json:"permissionMode,omitempty"`
	Tools           []string `json:"tools,omitempty"`
	// CustomTools lists single-file Tool artifacts to download and install
	// (issue #10). Tools remains the complete allow-list; CustomTools only
	// carries the artifacts selected for this agent.
	CustomTools    []ToolSource               `json:"customTools,omitempty"`
	Skills         []SkillSource              `json:"skills,omitempty"`
	SettingSources []string                   `json:"settingSources,omitempty"`
	Subagents      []SubagentDefinition       `json:"subagents,omitempty"`
	McpServers     map[string]McpServerConfig `json:"mcpServers,omitempty"`
	Datasets       map[string]string          `json:"datasets,omitempty"`
}

// ProviderConfig defines the provider configuration for the LLM API.
type ProviderConfig struct {
	Protocol string `json:"protocol"`
	BaseURL  string `json:"baseUrl"`
	APIKey   string `json:"lockedApiKey"`
}

// AigcConfig carries the AIGC content-labeling configuration (GB 45438-2025)
// for the deployed runtime. It is written verbatim into the runtime agents.yaml
// top-level "aigc" section; field names and constraints mirror the runtime's
// Zod schema (open-agent-runtime src/config.ts).
type AigcConfig struct {
	Enabled         bool   `json:"enabled"`
	ContentProducer string `json:"contentProducer"`
	SigningKey      string `json:"signingKey,omitempty"`
	// ExplicitHint signals downstream UIs to show a prominent AI-generated
	// notice. Nil means "not set" and is materialized to true at YAML write
	// time; only an explicit false disables it.
	ExplicitHint *bool             `json:"explicitHint,omitempty"`
	ModelCodes   map[string]string `json:"modelCodes,omitempty"`
	// Label is the implicit AIGC label: "1" = AI-generated, "2" = possibly
	// AI-generated, "3" = suspected. Nil means "not set"; runtime defaults
	// to "1".
	Label *string `json:"label,omitempty"`
	// ProduceIdPrefix is prepended to the generated ProduceID
	// (<prefix><timestamp>-<uuid12>). Empty means no prefix. Used for
	// downstream content traceability.
	ProduceIdPrefix string `json:"produceIdPrefix,omitempty"`
}

// Validate checks the AIGC config against the runtime schema constraints.
// A nil config or Enabled == false is always valid (the section is simply
// omitted from the runtime YAML).
//
// Length checks use byte length (len): GB 45438-2025 codes are alphanumeric
// ASCII, where byte length equals both rune count and JavaScript's UTF-16
// .length used by the runtime's Zod schema, so all three agree.
func (a *AigcConfig) Validate() error {
	if a == nil || !a.Enabled {
		return nil
	}
	if len(a.ContentProducer) != 27 {
		return fmt.Errorf("contentProducer must be exactly 27 characters, got %d", len(a.ContentProducer))
	}
	if a.SigningKey != "" && strings.TrimSpace(a.SigningKey) == "" {
		return fmt.Errorf("signingKey must not be blank")
	}
	if a.Label != nil && *a.Label != "1" && *a.Label != "2" && *a.Label != "3" {
		return fmt.Errorf("label must be one of: 1, 2, 3, got %q", *a.Label)
	}
	for name, code := range a.ModelCodes {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("modelCodes: empty model name")
		}
		if len(code) != 4 {
			return fmt.Errorf("modelCodes[%q]: code must be exactly 4 characters, got %d", name, len(code))
		}
	}
	return nil
}

// HubConfig carries the agent-hub chat-record push configuration for the
// deployed runtime. It is written verbatim into the runtime agents.yaml
// top-level "hub" section; field names mirror the runtime's schema
// (open-agent-runtime src/config.ts, resolveHubConfig). When the section is
// absent or enabled is false, the runtime does not push chat records.
//
// ChatPushKey is a shared secret (same value as the hub-side CHAT_PUSH_API_KEY
// env): it must never be logged or echoed back in API responses.
//
// Org is the deployment-level tenant for chat-record push, supplied by the
// hub's deploy request (issue #7). The deployer must NOT derive it from
// client request headers or fill in a default: it is a pure passthrough, and
// tenant authorization stays on the hub side. When unset, the field is
// omitted and the hub resolves the default tenant by deploy mode.
type HubConfig struct {
	Enabled     bool   `json:"enabled"`
	BaseURL     string `json:"baseUrl"`
	ChatPushKey string `json:"chatPushKey,omitempty"`
	Org         string `json:"org,omitempty"`
}

// Validate checks the hub config against the runtime's fail-fast constraints:
// when enabled, both baseUrl (an absolute http/https URL) and chatPushKey must
// be present, otherwise the runtime container would crash at startup. A nil
// config or Enabled == false is always valid (the section is simply omitted
// from the runtime YAML).
func (h *HubConfig) Validate() error {
	if h == nil || !h.Enabled {
		return nil
	}
	if strings.TrimSpace(h.BaseURL) == "" {
		return fmt.Errorf("baseUrl is required when hub is enabled")
	}
	u, err := url.Parse(h.BaseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("baseUrl must be an absolute http(s) URL, got %q", h.BaseURL)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("baseUrl scheme must be http or https, got %q", u.Scheme)
	}
	if strings.TrimSpace(h.ChatPushKey) == "" {
		return fmt.Errorf("chatPushKey is required when hub is enabled")
	}
	// org is a pure passthrough per issue #7: no content validation, no
	// defaulting, no derivation — tenant authorization stays on the hub side.
	return nil
}

// CreateAgentRequest is the top-level request body for creating an agent.
type CreateAgentRequest struct {
	Agent        AgentDefinition `json:"agent"`
	Provider     ProviderConfig  `json:"provider"`
	Aigc         *AigcConfig     `json:"aigc,omitempty"`
	Force        bool            `json:"force"`
	RuntimeToken string          `json:"runtime_token"`
	Hub          *HubConfig      `json:"hub,omitempty"`
}

// AgentStatus represents the state of an agent container.
type AgentStatus string

const (
	StatusRunning  AgentStatus = "running"
	StatusStopped  AgentStatus = "stopped"
	StatusExited   AgentStatus = "exited"
	StatusArchived AgentStatus = "archived"
	StatusNotFound AgentStatus = "not_found"
	StatusUnknown  AgentStatus = "unknown"
)

// ContainerInfo represents metadata returned by the service about a container.
type ContainerInfo struct {
	AgentName     string      `json:"agentName"`
	InstanceID    string      `json:"instanceId"`
	ContainerID   string      `json:"containerId"`
	ContainerName string      `json:"containerName"`
	Status        AgentStatus `json:"status"`
	HostPort      int         `json:"hostPort"`
	CreatedAt     string      `json:"createdAt"`
	YAMLPath      string      `json:"yamlPath"`
	SessionDir    string      `json:"sessionDir"`
}

// AgentResponse represents the response data for an agent.
type AgentResponse struct {
	AgentName     string      `json:"agentName"`
	InstanceID    string      `json:"instanceId"`
	ContainerID   string      `json:"containerId"`
	ContainerName string      `json:"containerName"`
	Status        AgentStatus `json:"status"`
	HostPort      int         `json:"hostPort"`
	CreatedAt     string      `json:"createdAt"`
	YamlPath      string      `json:"yamlPath"`
	SessionDir    string      `json:"sessionDir"`
	SkillsDir     string      `json:"skillsDir,omitempty"`
	// ToolsDir is the host-side directory holding installed custom Tool
	// files. Populated ONLY by Create and only when custom Tools are
	// declared (parity with skillsDir; not required by runtime behavior).
	ToolsDir string `json:"toolsDir,omitempty"`
	// RuntimeToken is the per-agent random token protecting the runtime
	// container's own HTTP API (injected as ZERONE_AGENT_HTTP_API_KEY).
	// Populated ONLY by Create: the deployer does not persist it, so Get / List
	// leave it empty. Clients must store it at creation time.
	RuntimeToken string `json:"runtimeToken,omitempty"`
}

// ErrorResponse represents a standard error response.
type ErrorResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// SuccessResponse represents a standard success response with data.
type SuccessResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
}

// AgentStatusResponse is the lightweight status payload for GET /agents/:name/status.
// Clients poll this after Create to detect when the runtime is healthy.
type AgentStatusResponse struct {
	AgentName     string `json:"agentName"`
	ContainerName string `json:"containerName"`
	ContainerID   string `json:"containerId"`
	Status        string `json:"status"` // Docker state: running, exited, created, etc.
	Health        string `json:"health"` // Docker health: starting, healthy, unhealthy, none
	HostPort      int    `json:"hostPort"`
	Image         string `json:"image"`
}

// ValidateCreateRequest validates a CreateAgentRequest at the package level.
func ValidateCreateRequest(req *CreateAgentRequest) error {
	if req == nil {
		return fmt.Errorf("request is required")
	}
	return req.Validate()
}

// Validate checks the CreateAgentRequest for required fields and valid values.
func (r *CreateAgentRequest) Validate() error {
	if r == nil {
		return fmt.Errorf("request is nil")
	}

	if err := r.Agent.Validate(); err != nil {
		return fmt.Errorf("agent: %w", err)
	}

	if err := r.Provider.Validate(); err != nil {
		return fmt.Errorf("provider: %w", err)
	}

	if err := r.Aigc.Validate(); err != nil {
		return fmt.Errorf("aigc: %w", err)
	}

	if err := r.Hub.Validate(); err != nil {
		return fmt.Errorf("hub: %w", err)
	}

	if strings.TrimSpace(r.RuntimeToken) == "" {
		return fmt.Errorf("runtimeToken is required")
	}
	if strings.TrimSpace(r.RuntimeToken) != r.RuntimeToken {
		return fmt.Errorf("runtimeToken must not contain leading or trailing whitespace")
	}

	return nil
}

// Validate checks the AgentDefinition for required fields and valid subagent configurations.
func (a *AgentDefinition) Validate() error {
	if strings.TrimSpace(a.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if strings.TrimSpace(a.Description) == "" {
		return fmt.Errorf("description is required")
	}
	if strings.TrimSpace(a.Model) == "" {
		return fmt.Errorf("model is required")
	}
	if strings.TrimSpace(a.SystemPrompt) == "" {
		return fmt.Errorf("systemPrompt is required")
	}

	seen := make(map[string]struct{}, len(a.Subagents))
	for i, sub := range a.Subagents {
		if _, ok := seen[sub.Name]; ok {
			return fmt.Errorf("subagents[%d]: duplicate name %q", i, sub.Name)
		}
		seen[sub.Name] = struct{}{}
		if err := sub.Validate(); err != nil {
			return fmt.Errorf("subagents[%d]: %w", i, err)
		}
	}

	seenDatasets := make(map[string]struct{}, len(a.Datasets))
	for id, desc := range a.Datasets {
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("datasets: empty dataset id")
		}
		if _, ok := seenDatasets[id]; ok {
			return fmt.Errorf("datasets: duplicate dataset id %q", id)
		}
		seenDatasets[id] = struct{}{}
		if strings.TrimSpace(desc) == "" {
			return fmt.Errorf("datasets[%q]: description is required", id)
		}
	}

	for i, skill := range a.Skills {
		if err := skill.Validate(); err != nil {
			return fmt.Errorf("skills[%d]: %w", i, err)
		}
	}

	seenToolNames := make(map[string]struct{}, len(a.CustomTools))
	seenToolPaths := make(map[string]struct{}, len(a.CustomTools))
	for i, tool := range a.CustomTools {
		if _, ok := seenToolNames[tool.Name]; ok {
			return fmt.Errorf("customTools[%d]: duplicate name %q", i, tool.Name)
		}
		seenToolNames[tool.Name] = struct{}{}
		if err := tool.Validate(); err != nil {
			return fmt.Errorf("customTools[%d]: %w", i, err)
		}
		local := tool.LocalFileName()
		if _, ok := seenToolPaths[local]; ok {
			return fmt.Errorf("customTools[%d]: duplicate local file %q", i, local)
		}
		seenToolPaths[local] = struct{}{}
	}

	seenMcp := make(map[string]struct{}, len(a.McpServers))
	for name, mcp := range a.McpServers {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("mcpServers: empty server name")
		}
		if _, ok := seenMcp[name]; ok {
			return fmt.Errorf("mcpServers: duplicate name %q", name)
		}
		seenMcp[name] = struct{}{}
		if err := mcp.Validate(); err != nil {
			return fmt.Errorf("mcpServers[%q]: %w", name, err)
		}
	}

	return nil
}

// Validate checks the SubagentDefinition for required fields.
func (s *SubagentDefinition) Validate() error {
	if strings.TrimSpace(s.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if strings.TrimSpace(s.Description) == "" {
		return fmt.Errorf("description is required")
	}
	if strings.TrimSpace(s.Prompt) == "" {
		return fmt.Errorf("prompt is required")
	}
	for id, description := range s.Datasets {
		if strings.TrimSpace(id) == "" || strings.TrimSpace(description) == "" {
			return fmt.Errorf("datasets must have non-empty ids and descriptions")
		}
	}
	return nil
}

// Validate checks the ProviderConfig for required fields and valid protocol.
func (p *ProviderConfig) Validate() error {
	if strings.TrimSpace(p.Protocol) == "" {
		return fmt.Errorf("protocol is required")
	}
	if p.Protocol != "anthropic-messages" && p.Protocol != "openai-completions" {
		return fmt.Errorf("protocol must be one of: anthropic-messages, openai-completions")
	}
	if strings.TrimSpace(p.BaseURL) == "" {
		return fmt.Errorf("baseUrl is required")
	}
	if strings.TrimSpace(p.APIKey) == "" {
		return fmt.Errorf("lockedApiKey is required")
	}
	return nil
}

// MarshalJSON implements custom JSON marshaling for AgentStatus.
func (s AgentStatus) MarshalJSON() ([]byte, error) {
	return json.Marshal(string(s))
}

// UnmarshalJSON implements custom JSON unmarshaling for AgentStatus.
func (s *AgentStatus) UnmarshalJSON(data []byte) error {
	var str string
	if err := json.Unmarshal(data, &str); err != nil {
		return err
	}

	switch AgentStatus(str) {
	case StatusRunning, StatusStopped, StatusExited, StatusArchived, StatusNotFound, StatusUnknown:
		*s = AgentStatus(str)
		return nil
	default:
		return fmt.Errorf("invalid agent status: %q", str)
	}
}
