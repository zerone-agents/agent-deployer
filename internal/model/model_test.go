package model

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func intPtr(v int) *int {
	return &v
}

func strPtr(v string) *string {
	return &v
}

func TestCreateAgentRequest_Validate_ValidRequest(t *testing.T) {
	req := CreateAgentRequest{
		RootAgentID: "coder",
		Agents: []AgentDefinition{
			{
				Name:         "coder",
				Description:  "Writes and edits code",
				Model:        "claude-sonnet-4-6",
				SystemPrompt: "You are a coding assistant.",
				MaxTurns:     nil,
				Tools:        []string{"Read", "Write", "Edit", "Bash"},
				Skills: []SkillSource{
					{Name: "code-review", URL: "https://example.com/code-review.zip", Hash: strings.Repeat("a", 64)},
				},
				SettingSources: []string{"user"},
				Subagents:      []string{"reviewer"},
			},
			{
				Name:        "reviewer",
				Description: "Review code",
			},
		},
		Provider: ProviderConfig{
			Protocol: "anthropic-messages",
			BaseURL:  "https://api.anthropic.com",
			APIKey:   "sk-ant-xxx",
		},
		Force:        false,
		RuntimeToken: "test-token",
	}

	err := req.Validate()
	require.NoError(t, err)
}

func TestCreateAgentRequest_Validate_MissingAgentName(t *testing.T) {
	req := CreateAgentRequest{
		RootAgentID: "coder",
		Agents: []AgentDefinition{
			{
				Name:         "",
				Description:  "Writes and edits code",
				Model:        "claude-sonnet-4-6",
				SystemPrompt: "You are a coding assistant.",
			},
		},
		Provider: ProviderConfig{
			Protocol: "anthropic-messages",
			BaseURL:  "https://api.anthropic.com",
			APIKey:   "sk-ant-xxx",
		},
		RuntimeToken: "test-token",
	}

	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "name is required")
}

func TestCreateAgentRequest_Validate_MissingAgentModel(t *testing.T) {
	req := CreateAgentRequest{
		RootAgentID: "coder",
		Agents: []AgentDefinition{
			{
				Name:         "coder",
				Description:  "Writes and edits code",
				Model:        "",
				SystemPrompt: "You are a coding assistant.",
			},
		},
		Provider: ProviderConfig{
			Protocol: "anthropic-messages",
			BaseURL:  "https://api.anthropic.com",
			APIKey:   "sk-ant-xxx",
		},
		RuntimeToken: "test-token",
	}

	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "model is required")
}

func TestCreateAgentRequest_Validate_MissingAgentDescription(t *testing.T) {
	req := CreateAgentRequest{
		RootAgentID: "coder",
		Agents: []AgentDefinition{
			{
				Name:         "coder",
				Description:  "",
				Model:        "claude-sonnet-4-6",
				SystemPrompt: "You are a coding assistant.",
			},
		},
		Provider: ProviderConfig{
			Protocol: "anthropic-messages",
			BaseURL:  "https://api.anthropic.com",
			APIKey:   "sk-ant-xxx",
		},
		RuntimeToken: "test-token",
	}

	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "description is required")
}

func TestCreateAgentRequest_Validate_WhitespaceOnlyDescription(t *testing.T) {
	req := CreateAgentRequest{
		RootAgentID: "coder",
		Agents: []AgentDefinition{
			{
				Name:         "coder",
				Description:  "   ",
				Model:        "claude-sonnet-4-6",
				SystemPrompt: "You are a coding assistant.",
			},
		},
		Provider: ProviderConfig{
			Protocol: "anthropic-messages",
			BaseURL:  "https://api.anthropic.com",
			APIKey:   "sk-ant-xxx",
		},
		RuntimeToken: "test-token",
	}

	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "description is required")
}

func TestCreateAgentRequest_Validate_MissingSystemPrompt(t *testing.T) {
	req := CreateAgentRequest{
		RootAgentID: "coder",
		Agents: []AgentDefinition{
			{
				Name:         "coder",
				Description:  "Writes and edits code",
				Model:        "claude-sonnet-4-6",
				SystemPrompt: "",
			},
		},
		Provider: ProviderConfig{
			Protocol: "anthropic-messages",
			BaseURL:  "https://api.anthropic.com",
			APIKey:   "sk-ant-xxx",
		},
		RuntimeToken: "test-token",
	}

	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "systemPrompt is required")
}

func TestCreateAgentRequest_Validate_InvalidProviderProtocol(t *testing.T) {
	req := CreateAgentRequest{
		RootAgentID: "coder",
		Agents: []AgentDefinition{
			{
				Name:         "coder",
				Description:  "Writes and edits code",
				Model:        "claude-sonnet-4-6",
				SystemPrompt: "You are a coding assistant.",
			},
		},
		Provider: ProviderConfig{
			Protocol: "invalid",
			BaseURL:  "https://api.anthropic.com",
			APIKey:   "sk-ant-xxx",
		},
		RuntimeToken: "test-token",
	}

	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "protocol must be one of: anthropic-messages, openai-completions")
}

func TestCreateAgentRequest_Validate_AgentMissingName(t *testing.T) {
	req := CreateAgentRequest{
		RootAgentID: "coder",
		Agents: []AgentDefinition{
			{
				Name:         "coder",
				Description:  "Writes and edits code",
				Model:        "claude-sonnet-4-6",
				SystemPrompt: "You are a coding assistant.",
				Subagents:    []string{"reviewer"},
			},
			{
				Name:        "",
				Description: "Review code",
			},
		},
		Provider: ProviderConfig{
			Protocol: "anthropic-messages",
			BaseURL:  "https://api.anthropic.com",
			APIKey:   "sk-ant-xxx",
		},
		RuntimeToken: "test-token",
	}

	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "name is required")
}

func TestCreateAgentRequest_Validate_AgentMissingDescription(t *testing.T) {
	req := CreateAgentRequest{
		RootAgentID: "coder",
		Agents: []AgentDefinition{
			{
				Name:         "coder",
				Description:  "Writes and edits code",
				Model:        "claude-sonnet-4-6",
				SystemPrompt: "You are a coding assistant.",
				Subagents:    []string{"reviewer"},
			},
			{
				Name:        "reviewer",
				Description: "",
			},
		},
		Provider: ProviderConfig{
			Protocol: "anthropic-messages",
			BaseURL:  "https://api.anthropic.com",
			APIKey:   "sk-ant-xxx",
		},
		RuntimeToken: "test-token",
	}

	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "description is required")
}

func TestCreateAgentRequest_Validate_NilRequest(t *testing.T) {
	var req *CreateAgentRequest
	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "request is nil")
}

func TestCreateAgentRequest_Validate_MaxTurnsZeroIsValid(t *testing.T) {
	req := CreateAgentRequest{
		RootAgentID: "coder",
		Agents: []AgentDefinition{
			{
				Name:         "coder",
				Description:  "Writes and edits code",
				Model:        "claude-sonnet-4-6",
				SystemPrompt: "You are a coding assistant.",
				MaxTurns:     intPtr(0),
			},
		},
		Provider: ProviderConfig{
			Protocol: "anthropic-messages",
			BaseURL:  "https://api.anthropic.com",
			APIKey:   "sk-ant-xxx",
		},
		RuntimeToken: "test-token",
	}

	err := req.Validate()
	require.NoError(t, err)
}

func TestAgentDefinition_Validate_DuplicateAgentIDs(t *testing.T) {
	req := CreateAgentRequest{
		RootAgentID: "coder",
		Agents: []AgentDefinition{
			{
				Name:         "coder",
				Description:  "Writes and edits code",
				Model:        "claude-sonnet-4-6",
				SystemPrompt: "You are a coding assistant.",
				Subagents:    []string{"reviewer"},
			},
			{
				Name:        "reviewer",
				Description: "Review code",
			},
			{
				Name:        "reviewer",
				Description: "Another reviewer",
			},
		},
		Provider: ProviderConfig{
			Protocol: "anthropic-messages",
			BaseURL:  "https://api.anthropic.com",
			APIKey:   "sk-ant-xxx",
		},
		RuntimeToken: "test-token",
	}

	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate agent id \"reviewer\"")
}

func TestCreateAgentRequest_Validate_OpenAIProtocol(t *testing.T) {
	req := CreateAgentRequest{
		RootAgentID: "coder",
		Agents: []AgentDefinition{
			{
				Name:         "coder",
				Description:  "Writes and edits code",
				Model:        "gpt-4",
				SystemPrompt: "You are a coding assistant.",
			},
		},
		Provider: ProviderConfig{
			Protocol: "openai-completions",
			BaseURL:  "https://api.openai.com",
			APIKey:   "sk-openai-xxx",
		},
		RuntimeToken: "test-token",
	}

	err := req.Validate()
	require.NoError(t, err)
}

func TestCreateAgentRequest_Validate_MissingProviderBaseURL(t *testing.T) {
	req := CreateAgentRequest{
		RootAgentID: "coder",
		Agents: []AgentDefinition{
			{
				Name:         "coder",
				Description:  "Writes and edits code",
				Model:        "claude-sonnet-4-6",
				SystemPrompt: "You are a coding assistant.",
			},
		},
		Provider: ProviderConfig{
			Protocol: "anthropic-messages",
			BaseURL:  "",
			APIKey:   "sk-ant-xxx",
		},
		RuntimeToken: "test-token",
	}

	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "baseUrl is required")
}

func TestCreateAgentRequest_Validate_MissingProviderAPIKey(t *testing.T) {
	req := CreateAgentRequest{
		RootAgentID: "coder",
		Agents: []AgentDefinition{
			{
				Name:         "coder",
				Description:  "Writes and edits code",
				Model:        "claude-sonnet-4-6",
				SystemPrompt: "You are a coding assistant.",
			},
		},
		Provider: ProviderConfig{
			Protocol: "anthropic-messages",
			BaseURL:  "https://api.anthropic.com",
		},
	}

	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "lockedApiKey is required")
}

func TestCreateAgentRequest_Validate_WhitespaceOnlyName(t *testing.T) {
	req := CreateAgentRequest{
		RootAgentID: "coder",
		Agents: []AgentDefinition{
			{
				Name:         "   ",
				Description:  "Writes and edits code",
				Model:        "claude-sonnet-4-6",
				SystemPrompt: "You are a coding assistant.",
			},
		},
		Provider: ProviderConfig{
			Protocol: "anthropic-messages",
			BaseURL:  "https://api.anthropic.com",
			APIKey:   "sk-ant-xxx",
		},
		RuntimeToken: "test-token",
	}

	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "name is required")
}

func TestCreateAgentRequest_Validate_MultipleAgentsOneInvalid(t *testing.T) {
	req := CreateAgentRequest{
		RootAgentID: "coder",
		Agents: []AgentDefinition{
			{
				Name:         "coder",
				Description:  "Writes and edits code",
				Model:        "claude-sonnet-4-6",
				SystemPrompt: "You are a coding assistant.",
				Subagents:    []string{"reviewer", "tester"},
			},
			{
				Name:        "reviewer",
				Description: "Review code",
			},
			{
				Name:        "",
				Description: "Test code",
			},
		},
		Provider: ProviderConfig{
			Protocol: "anthropic-messages",
			BaseURL:  "https://api.anthropic.com",
			APIKey:   "sk-ant-xxx",
		},
		RuntimeToken: "test-token",
	}

	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "name is required")
}

func TestAgentDefinition_JSONMarshal_NilMaxTurnsIsNull(t *testing.T) {
	agent := AgentDefinition{
		Name:         "coder",
		Model:        "claude-sonnet-4-6",
		SystemPrompt: "You are a coding assistant.",
		MaxTurns:     nil,
	}

	data, err := json.Marshal(agent)
	require.NoError(t, err)

	var parsed map[string]interface{}
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	assert.Equal(t, "coder", parsed["name"])
	assert.Equal(t, "claude-sonnet-4-6", parsed["model"])
	assert.Equal(t, "You are a coding assistant.", parsed["systemPrompt"])
	// nil MaxTurns must serialize as JSON null, not be omitted
	maxTurns, exists := parsed["maxTurns"]
	assert.True(t, exists, "maxTurns key must be present")
	assert.Nil(t, maxTurns, "maxTurns must be null when nil")
}

func TestAgentDefinition_JSONMarshal_MaxTurnsValue(t *testing.T) {
	agent := AgentDefinition{
		Name:         "coder",
		Model:        "claude-sonnet-4-6",
		SystemPrompt: "You are a coding assistant.",
		MaxTurns:     intPtr(42),
	}

	data, err := json.Marshal(agent)
	require.NoError(t, err)

	var parsed map[string]interface{}
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	assert.Equal(t, float64(42), parsed["maxTurns"])
}

func TestAgentStatus_JSONRoundTrip(t *testing.T) {
	status := StatusRunning
	data, err := json.Marshal(status)
	require.NoError(t, err)
	assert.Equal(t, "\"running\"", string(data))

	var parsed AgentStatus
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)
	assert.Equal(t, StatusRunning, parsed)
}

func TestAgentStatus_UnmarshalJSON_NonString(t *testing.T) {
	var status AgentStatus
	err := json.Unmarshal([]byte(`123`), &status)
	require.Error(t, err)

	err = json.Unmarshal([]byte(`null`), &status)
	require.Error(t, err)
}

func TestAgentStatus_UnmarshalJSON_InvalidStatus(t *testing.T) {
	var status AgentStatus
	err := json.Unmarshal([]byte(`"invalid_status"`), &status)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid agent status")
}

func TestContainerInfo_JSONMarshal(t *testing.T) {
	info := ContainerInfo{
		AgentName:     "coder",
		InstanceID:    "a1b2c3d4",
		ContainerID:   "abc123",
		ContainerName: "cloud-agent-coder-a1b2c3d4",
		Status:        StatusRunning,
		HostPort:      32768,
		CreatedAt:     "2026-06-22T10:00:00Z",
		YAMLPath:      "/var/lib/agent-deployer/agents/coder/agents.yaml",
		SessionDir:    "/var/lib/agent-deployer/sessions/coder",
	}

	data, err := json.Marshal(info)
	require.NoError(t, err)

	var parsed map[string]interface{}
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	assert.Equal(t, "coder", parsed["agentName"])
	assert.Equal(t, "a1b2c3d4", parsed["instanceId"])
	assert.Equal(t, "abc123", parsed["containerId"])
	assert.Equal(t, "cloud-agent-coder-a1b2c3d4", parsed["containerName"])
	assert.Equal(t, "running", parsed["status"])
	assert.Equal(t, float64(32768), parsed["hostPort"])
	assert.Equal(t, "2026-06-22T10:00:00Z", parsed["createdAt"])
	assert.Equal(t, "/var/lib/agent-deployer/agents/coder/agents.yaml", parsed["yamlPath"])
	assert.Equal(t, "/var/lib/agent-deployer/sessions/coder", parsed["sessionDir"])
}

func TestCreateAgentRequest_JSONUnmarshal(t *testing.T) {
	jsonData := `{
		"rootAgentId": "coder",
		"agents": [
			{
				"name": "coder",
				"model": "claude-sonnet-4-6",
				"systemPrompt": "You are a coding assistant.",
				"maxTurns": null,
				"permissionMode": "auto",
				"tools": ["Read", "Write", "Edit", "Bash"],
				"skills": [{"name": "code-review", "url": "https://example.com/cr.zip", "hash": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}],
				"settingSources": ["user"],
				"subagents": ["reviewer"]
			},
			{
				"name": "reviewer",
				"description": "Review code",
				"systemPrompt": "You are a code reviewer.",
				"tools": ["Read"],
				"disallowedTools": ["Bash"],
				"maxTurns": 10
			}
		],
		"provider": {
			"protocol": "anthropic-messages",
			"baseUrl": "https://api.anthropic.com",
			"lockedApiKey": "sk-ant-xxx"
		},
		"force": false
	}`

	var req CreateAgentRequest
	err := json.Unmarshal([]byte(jsonData), &req)
	require.NoError(t, err)

	assert.Equal(t, "coder", req.RootAgentID)
	require.Len(t, req.Agents, 2)
	root := req.Agents[0]
	assert.Equal(t, "coder", root.Name)
	assert.Equal(t, "claude-sonnet-4-6", root.Model)
	assert.Equal(t, "You are a coding assistant.", root.SystemPrompt)
	assert.Nil(t, root.MaxTurns)
	assert.Equal(t, "auto", root.PermissionMode)
	assert.Equal(t, []string{"Read", "Write", "Edit", "Bash"}, root.Tools)
	require.Len(t, root.Skills, 1)
	assert.Equal(t, "code-review", root.Skills[0].Name)
	assert.Equal(t, "https://example.com/cr.zip", root.Skills[0].URL)
	assert.Equal(t, strings.Repeat("a", 64), root.Skills[0].NormalizedHash())
	assert.Equal(t, []string{"reviewer"}, root.Subagents)

	reviewer := req.Agents[1]
	assert.Equal(t, "reviewer", reviewer.Name)
	assert.Equal(t, "Review code", reviewer.Description)
	assert.Equal(t, "You are a code reviewer.", reviewer.SystemPrompt)
	assert.Equal(t, []string{"Read"}, reviewer.Tools)
	assert.Equal(t, []string{"Bash"}, reviewer.DisallowedTools)
	require.NotNil(t, reviewer.MaxTurns)
	assert.Equal(t, 10, *reviewer.MaxTurns)

	assert.Equal(t, "anthropic-messages", req.Provider.Protocol)
	assert.Equal(t, "https://api.anthropic.com", req.Provider.BaseURL)
	assert.Equal(t, "sk-ant-xxx", req.Provider.APIKey)
	assert.False(t, req.Force)
}

func TestCreateAgentRequest_JSONUnmarshal_RuntimeToken(t *testing.T) {
	jsonData := `{
		"rootAgentId": "coder",
		"agents": [
			{
				"name": "coder",
				"description": "Writes and edits code",
				"model": "claude-sonnet-4-6",
				"systemPrompt": "You are a coding assistant."
			}
		],
		"provider": {
			"protocol": "anthropic-messages",
			"baseUrl": "https://api.anthropic.com",
			"lockedApiKey": "sk-ant-xxx"
		},
		"runtime_token": "my-token"
	}`

	var req CreateAgentRequest
	err := json.Unmarshal([]byte(jsonData), &req)
	require.NoError(t, err)

	assert.Equal(t, "my-token", req.RuntimeToken)
	assert.NoError(t, req.Validate())
}

func TestCreateAgentRequest_JSONUnmarshal_MaxTurnsZero(t *testing.T) {
	jsonData := `{
		"rootAgentId": "coder",
		"agents": [
			{
				"name": "coder",
				"description": "Writes and edits code",
				"model": "claude-sonnet-4-6",
				"systemPrompt": "You are a coding assistant.",
				"maxTurns": 0
			}
		],
		"provider": {
			"protocol": "anthropic-messages",
			"baseUrl": "https://api.anthropic.com",
			"lockedApiKey": "sk-ant-xxx"
		},
		"force": false,
		"runtime_token": "test-token"
	}`

	var req CreateAgentRequest
	err := json.Unmarshal([]byte(jsonData), &req)
	require.NoError(t, err)

	require.NotNil(t, req.Agents[0].MaxTurns)
	assert.Equal(t, 0, *req.Agents[0].MaxTurns)

	// Validate should pass with maxTurns = 0
	assert.NoError(t, req.Validate())
}

func TestCreateAgentRequest_JSONUnmarshal_OmittedMaxTurns(t *testing.T) {
	jsonData := `{
		"rootAgentId": "coder",
		"agents": [
			{
				"name": "coder",
				"model": "claude-sonnet-4-6",
				"systemPrompt": "You are a coding assistant."
			}
		],
		"provider": {
			"protocol": "anthropic-messages",
			"baseUrl": "https://api.anthropic.com",
			"lockedApiKey": "sk-ant-xxx"
		},
		"force": false
	}`

	var req CreateAgentRequest
	err := json.Unmarshal([]byte(jsonData), &req)
	require.NoError(t, err)

	assert.Nil(t, req.Agents[0].MaxTurns)
	assert.Empty(t, req.Agents[0].Tools)
	assert.Empty(t, req.Agents[0].Skills)
	assert.Empty(t, req.Agents[0].Subagents)
	assert.Empty(t, req.Agents[0].PermissionMode)
}

func TestAgentResponse_JSONMarshal(t *testing.T) {
	resp := AgentResponse{
		AgentName:     "coder",
		InstanceID:    "a1b2c3d4",
		ContainerID:   "abc123",
		ContainerName: "cloud-agent-coder-a1b2c3d4",
		Status:        StatusRunning,
		HostPort:      32768,
		CreatedAt:     "2026-06-22T10:00:00Z",
		YamlPath:      "/var/lib/agent-deployer/agents/coder/agents.yaml",
		SessionDir:    "/var/lib/agent-deployer/sessions/coder",
		SkillsDir:     "/var/lib/agent-deployer/skills/coder",
	}

	data, err := json.Marshal(resp)
	require.NoError(t, err)

	var parsed map[string]interface{}
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	assert.Equal(t, "coder", parsed["agentName"])
	assert.Equal(t, "a1b2c3d4", parsed["instanceId"])
	assert.Equal(t, "abc123", parsed["containerId"])
	assert.Equal(t, "cloud-agent-coder-a1b2c3d4", parsed["containerName"])
	assert.Equal(t, "running", parsed["status"])
	assert.Equal(t, float64(32768), parsed["hostPort"])
	assert.Equal(t, "2026-06-22T10:00:00Z", parsed["createdAt"])
	assert.Equal(t, "/var/lib/agent-deployer/agents/coder/agents.yaml", parsed["yamlPath"])
	assert.Equal(t, "/var/lib/agent-deployer/sessions/coder", parsed["sessionDir"])
	assert.Equal(t, "/var/lib/agent-deployer/skills/coder", parsed["skillsDir"])
}

func TestAgentResponse_JSONMarshal_OmitsEmptySkillsDir(t *testing.T) {
	resp := AgentResponse{
		AgentName:  "coder",
		InstanceID: "a1b2c3d4",
		Status:     StatusRunning,
	}

	data, err := json.Marshal(resp)
	require.NoError(t, err)

	var parsed map[string]interface{}
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	_, exists := parsed["skillsDir"]
	assert.False(t, exists, "skillsDir should be omitted when empty")
}

func TestErrorResponse_JSONMarshal_OmitsEmptyError(t *testing.T) {
	resp := ErrorResponse{
		Success: false,
		Error:   "",
	}

	data, err := json.Marshal(resp)
	require.NoError(t, err)

	var parsed map[string]interface{}
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	_, exists := parsed["error"]
	assert.False(t, exists, "error field should be omitted when empty")
}

func TestErrorResponse_JSONMarshal(t *testing.T) {
	resp := ErrorResponse{
		Success: false,
		Error:   "something went wrong",
	}

	data, err := json.Marshal(resp)
	require.NoError(t, err)

	var parsed map[string]interface{}
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	assert.Equal(t, false, parsed["success"])
	assert.Equal(t, "something went wrong", parsed["error"])
}

func TestSuccessResponse_JSONMarshal(t *testing.T) {
	resp := SuccessResponse{
		Success: true,
		Data:    map[string]string{"key": "value"},
	}

	data, err := json.Marshal(resp)
	require.NoError(t, err)

	var parsed map[string]interface{}
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	assert.Equal(t, true, parsed["success"])
	require.NotNil(t, parsed["data"])
	dataMap, ok := parsed["data"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "value", dataMap["key"])
}

func TestSkillSource_Validate(t *testing.T) {
	cases := []struct {
		name    string
		skill   SkillSource
		wantErr string
	}{
		{
			name:    "valid",
			skill:   SkillSource{Name: "code-review", URL: "https://x.com/s.zip", Hash: strings.Repeat("a", 64)},
			wantErr: "",
		},
		{
			name:    "valid hash with sha256 prefix",
			skill:   SkillSource{Name: "x", URL: "https://x.com/s.zip", Hash: "sha256:" + strings.Repeat("a", 64)},
			wantErr: "",
		},
		{
			name:    "valid hash uppercase",
			skill:   SkillSource{Name: "x", URL: "https://x.com/s.zip", Hash: strings.Repeat("A", 64)},
			wantErr: "",
		},
		{
			name:    "valid name length 64 (boundary)",
			skill:   SkillSource{Name: strings.Repeat("a", 64), URL: "https://x.com/s.zip", Hash: strings.Repeat("a", 64)},
			wantErr: "",
		},
		{
			name:    "empty name",
			skill:   SkillSource{Name: "", URL: "https://x.com/s.zip", Hash: strings.Repeat("a", 64)},
			wantErr: "name is required",
		},
		{
			name:    "name with slash",
			skill:   SkillSource{Name: "a/b", URL: "https://x.com/s.zip", Hash: strings.Repeat("a", 64)},
			wantErr: "name must match",
		},
		{
			name:    "name with space",
			skill:   SkillSource{Name: "a b", URL: "https://x.com/s.zip", Hash: strings.Repeat("a", 64)},
			wantErr: "name must match",
		},
		{
			name:    "name is dot",
			skill:   SkillSource{Name: ".", URL: "https://x.com/s.zip", Hash: strings.Repeat("a", 64)},
			wantErr: "name cannot be",
		},
		{
			name:    "name is dotdot",
			skill:   SkillSource{Name: "..", URL: "https://x.com/s.zip", Hash: strings.Repeat("a", 64)},
			wantErr: "name cannot be",
		},
		{
			name:    "name too long",
			skill:   SkillSource{Name: strings.Repeat("a", 65), URL: "https://x.com/s.zip", Hash: strings.Repeat("a", 64)},
			wantErr: "name must match",
		},
		{
			name:    "url empty",
			skill:   SkillSource{Name: "x", URL: "", Hash: strings.Repeat("a", 64)},
			wantErr: "url is required",
		},
		{
			name:    "url ftp scheme",
			skill:   SkillSource{Name: "x", URL: "ftp://x.com/s.zip", Hash: strings.Repeat("a", 64)},
			wantErr: "url must be http(s)",
		},
		{
			name:    "hash empty",
			skill:   SkillSource{Name: "x", URL: "https://x.com/s.zip", Hash: ""},
			wantErr: "hash is required",
		},
		{
			name:    "hash wrong length",
			skill:   SkillSource{Name: "x", URL: "https://x.com/s.zip", Hash: strings.Repeat("a", 63)},
			wantErr: "hash must be 64 hex chars",
		},
		{
			name:    "hash too long",
			skill:   SkillSource{Name: "x", URL: "https://x.com/s.zip", Hash: strings.Repeat("a", 65)},
			wantErr: "hash must be 64 hex chars",
		},
		{
			name:    "hash non-hex",
			skill:   SkillSource{Name: "x", URL: "https://x.com/s.zip", Hash: strings.Repeat("z", 64)},
			wantErr: "hash must be 64 hex chars",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.skill.Validate()
			if tc.wantErr == "" {
				assert.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
			}
		})
	}
}

func TestSkillSource_NormalizedHash(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{strings.Repeat("a", 64), strings.Repeat("a", 64)},
		{"sha256:" + strings.Repeat("a", 64), strings.Repeat("a", 64)},
		{strings.Repeat("A", 64), strings.Repeat("a", 64)},
		{"  " + strings.Repeat("a", 64) + "  ", strings.Repeat("a", 64)},
	}
	for _, tc := range cases {
		s := SkillSource{Name: "x", URL: "https://x.com/s.zip", Hash: tc.in}
		assert.Equal(t, tc.want, s.NormalizedHash())
	}
}

func TestMcpServerConfig_Validate(t *testing.T) {
	cases := []struct {
		name    string
		mcp     McpServerConfig
		wantErr string
	}{
		{
			name: "valid sse",
			mcp:  McpServerConfig{Type: "sse", URL: "https://api.example.com/sse"},
		},
		{
			name: "valid http with headers",
			mcp: McpServerConfig{Type: "http", URL: "https://api.example.com/mcp", Headers: map[string]string{
				"Authorization": "Bearer xxx",
			}},
		},
		{
			name:    "missing type",
			mcp:     McpServerConfig{URL: "https://api.example.com/sse"},
			wantErr: "type is required",
		},
		{
			name:    "unsupported stdio",
			mcp:     McpServerConfig{Type: "stdio", URL: "https://cmd.example.com"},
			wantErr: "type must be one of: sse, http",
		},
		{
			name:    "missing url for sse",
			mcp:     McpServerConfig{Type: "sse"},
			wantErr: "url is required",
		},
		{
			name:    "invalid url scheme",
			mcp:     McpServerConfig{Type: "http", URL: "ftp://example.com"},
			wantErr: "url must be http(s)",
		},
		{
			name:    "empty url",
			mcp:     McpServerConfig{Type: "http", URL: "   "},
			wantErr: "url is required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.mcp.Validate()
			if tc.wantErr == "" {
				assert.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
			}
		})
	}
}

func TestAgentDefinition_Validate_ValidMcpServers(t *testing.T) {
	req := CreateAgentRequest{
		RootAgentID: "coder",
		Agents: []AgentDefinition{
			{
				Name:         "coder",
				Description:  "Writes and edits code",
				Model:        "claude-sonnet-4-6",
				SystemPrompt: "You are a coding assistant.",
				McpServers: map[string]McpServerConfig{
					"remote-api": {Type: "sse", URL: "https://api.example.com/sse"},
				},
				Subagents: []string{"reviewer"},
			},
			{
				Name:        "reviewer",
				Description: "Review code",
			},
		},
		Provider: ProviderConfig{
			Protocol: "anthropic-messages",
			BaseURL:  "https://api.anthropic.com",
			APIKey:   "sk-ant-xxx",
		},
		RuntimeToken: "test-token",
	}
	assert.NoError(t, req.Validate())
}

func TestAgentDefinition_Validate_InvalidMcpType(t *testing.T) {
	req := CreateAgentRequest{
		RootAgentID: "coder",
		Agents: []AgentDefinition{
			{
				Name:         "coder",
				Description:  "Writes and edits code",
				Model:        "claude-sonnet-4-6",
				SystemPrompt: "You are a coding assistant.",
				McpServers: map[string]McpServerConfig{
					"remote-api": {Type: "sse", URL: "not-a-url"},
				},
			},
		},
		Provider: ProviderConfig{
			Protocol: "anthropic-messages",
			BaseURL:  "https://api.anthropic.com",
			APIKey:   "sk-ant-xxx",
		},
		RuntimeToken: "test-token",
	}
	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mcpServers[\"remote-api\"]")
	assert.Contains(t, err.Error(), "url must be http(s)")
}

func TestCreateAgentRequest_JSONUnmarshal_WithMcpServers(t *testing.T) {
	jsonData := `{
		"rootAgentId": "coder",
		"agents": [
			{
				"name": "coder",
				"model": "claude-sonnet-4-6",
				"systemPrompt": "You are a coding assistant.",
				"mcpServers": {
					"remote-api": {
						"type": "sse",
						"url": "https://api.example.com/sse",
						"headers": {"Authorization": "Bearer xxx"}
					}
				},
				"subagents": ["reviewer"]
			},
			{
				"name": "reviewer",
				"description": "Review code"
			}
		],
		"provider": {
			"protocol": "anthropic-messages",
			"baseUrl": "https://api.anthropic.com",
			"lockedApiKey": "sk-ant-xxx"
		},
		"force": false
	}`

	var req CreateAgentRequest
	err := json.Unmarshal([]byte(jsonData), &req)
	require.NoError(t, err)

	require.Len(t, req.Agents[0].McpServers, 1)
	mcp, ok := req.Agents[0].McpServers["remote-api"]
	require.True(t, ok)
	assert.Equal(t, "sse", mcp.Type)
	assert.Equal(t, "https://api.example.com/sse", mcp.URL)
	assert.Equal(t, "Bearer xxx", mcp.Headers["Authorization"])

	assert.Equal(t, []string{"reviewer"}, req.Agents[0].Subagents)
}

func TestAgentDefinition_JSONMarshal_McpServersUsesTypeField(t *testing.T) {
	agent := AgentDefinition{
		Name: "coder",
		McpServers: map[string]McpServerConfig{
			"remote-api": {Type: "http", URL: "https://api.example.com/mcp"},
		},
	}

	data, err := json.Marshal(agent)
	require.NoError(t, err)

	var parsed map[string]interface{}
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	mcpServers, ok := parsed["mcpServers"].(map[string]interface{})
	require.True(t, ok)
	remoteApi, ok := mcpServers["remote-api"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "http", remoteApi["type"])
	assert.Equal(t, "https://api.example.com/mcp", remoteApi["url"])
}

func TestAgentDefinition_Validate_DatasetsValid(t *testing.T) {
	req := CreateAgentRequest{
		RootAgentID: "coder",
		Agents: []AgentDefinition{
			{
				Name:         "coder",
				Description:  "Writes and edits code",
				Model:        "claude-sonnet-4-6",
				SystemPrompt: "You are a coding assistant.",
				Datasets: map[string]string{
					"dataset-1": "Primary dataset",
					"dataset-2": "Secondary dataset",
				},
			},
		},
		Provider: ProviderConfig{
			Protocol: "anthropic-messages",
			BaseURL:  "https://api.anthropic.com",
			APIKey:   "sk-ant-xxx",
		},
		RuntimeToken: "test-token",
	}

	require.NoError(t, req.Validate())
}

func TestAgentDefinition_Validate_DatasetsEmptyID(t *testing.T) {
	req := CreateAgentRequest{
		RootAgentID: "coder",
		Agents: []AgentDefinition{
			{
				Name:         "coder",
				Description:  "Writes and edits code",
				Model:        "claude-sonnet-4-6",
				SystemPrompt: "You are a coding assistant.",
				Datasets: map[string]string{
					"": "No id",
				},
			},
		},
		Provider: ProviderConfig{
			Protocol: "anthropic-messages",
			BaseURL:  "https://api.anthropic.com",
			APIKey:   "sk-ant-xxx",
		},
		RuntimeToken: "test-token",
	}

	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "datasets: empty dataset id")
}

func TestAgentDefinition_Validate_DatasetsEmptyDescription(t *testing.T) {
	req := CreateAgentRequest{
		RootAgentID: "coder",
		Agents: []AgentDefinition{
			{
				Name:         "coder",
				Description:  "Writes and edits code",
				Model:        "claude-sonnet-4-6",
				SystemPrompt: "You are a coding assistant.",
				Datasets: map[string]string{
					"dataset-1": "",
				},
			},
		},
		Provider: ProviderConfig{
			Protocol: "anthropic-messages",
			BaseURL:  "https://api.anthropic.com",
			APIKey:   "sk-ant-xxx",
		},
		RuntimeToken: "test-token",
	}

	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "datasets[\"dataset-1\"]: description is required")
}

func TestAgentDefinition_Validate_DatasetsWhitespaceDescription(t *testing.T) {
	req := CreateAgentRequest{
		RootAgentID: "coder",
		Agents: []AgentDefinition{
			{
				Name:         "coder",
				Description:  "Writes and edits code",
				Model:        "claude-sonnet-4-6",
				SystemPrompt: "You are a coding assistant.",
				Datasets: map[string]string{
					"dataset-1": "   ",
				},
			},
		},
		Provider: ProviderConfig{
			Protocol: "anthropic-messages",
			BaseURL:  "https://api.anthropic.com",
			APIKey:   "sk-ant-xxx",
		},
		RuntimeToken: "test-token",
	}

	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "datasets[\"dataset-1\"]: description is required")
}

func TestAgentDefinition_JSONMarshalWithMaxSessionQueries(t *testing.T) {
	maxTurns := 10
	maxSessionQueries := 50
	agent := AgentDefinition{
		Name:              "assistant",
		Model:             "claude-sonnet-4-6",
		SystemPrompt:      "You are helpful.",
		MaxTurns:          &maxTurns,
		MaxSessionQueries: &maxSessionQueries,
	}

	data, err := json.Marshal(agent)
	require.NoError(t, err)

	var parsed map[string]interface{}
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	assert.Equal(t, float64(10), parsed["maxTurns"])
	assert.Equal(t, float64(50), parsed["maxSessionQueries"])
}

func TestAgentDefinition_JSONMarshalNilMaxSessionQueriesOmitted(t *testing.T) {
	agent := AgentDefinition{
		Name:              "assistant",
		Model:             "claude-sonnet-4-6",
		SystemPrompt:      "You are helpful.",
		MaxSessionQueries: nil,
	}

	data, err := json.Marshal(agent)
	require.NoError(t, err)

	var parsed map[string]interface{}
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	_, present := parsed["maxSessionQueries"]
	assert.False(t, present, "maxSessionQueries should be omitted when nil")
}

func TestAgentDefinition_JSONUnmarshalMaxSessionQueries(t *testing.T) {
	input := `{"name":"assistant","model":"claude-sonnet-4-6","systemPrompt":"helpful","maxSessionQueries":30}`

	var agent AgentDefinition
	err := json.Unmarshal([]byte(input), &agent)
	require.NoError(t, err)

	require.NotNil(t, agent.MaxSessionQueries)
	assert.Equal(t, 30, *agent.MaxSessionQueries)
}

func TestAigcConfig_Validate(t *testing.T) {
	validProducer := "001191320118MAK93FC72D10001" // 27 chars
	require.Len(t, validProducer, 27)

	tests := []struct {
		name    string
		aigc    *AigcConfig
		wantErr string // 空串表示期望通过
	}{
		{
			name:    "nil config passes",
			aigc:    nil,
			wantErr: "",
		},
		{
			name: "disabled passes even with invalid fields",
			aigc: &AigcConfig{
				Enabled:         false,
				ContentProducer: "short",
				ModelCodes:      map[string]string{"m": "x"},
			},
			wantErr: "",
		},
		{
			name: "enabled with valid fields passes",
			aigc: &AigcConfig{
				Enabled:         true,
				ContentProducer: validProducer,
				SigningKey:      "secret",
				ModelCodes:      map[string]string{"glm-4.5": "0001"},
			},
			wantErr: "",
		},
		{
			name: "enabled without modelCodes passes",
			aigc: &AigcConfig{
				Enabled:         true,
				ContentProducer: validProducer,
			},
			wantErr: "",
		},
		{
			name: "enabled with empty modelCodes passes",
			aigc: &AigcConfig{
				Enabled:         true,
				ContentProducer: validProducer,
				ModelCodes:      map[string]string{},
			},
			wantErr: "",
		},
		{
			name:    "enabled without contentProducer fails",
			aigc:    &AigcConfig{Enabled: true},
			wantErr: "contentProducer",
		},
		{
			name:    "contentProducer 26 chars fails",
			aigc:    &AigcConfig{Enabled: true, ContentProducer: validProducer[:26]},
			wantErr: "contentProducer",
		},
		{
			name:    "contentProducer 28 chars fails",
			aigc:    &AigcConfig{Enabled: true, ContentProducer: validProducer + "X"},
			wantErr: "contentProducer",
		},
		{
			name: "modelCodes value 3 chars fails",
			aigc: &AigcConfig{
				Enabled:         true,
				ContentProducer: validProducer,
				ModelCodes:      map[string]string{"glm-4.5": "001"},
			},
			wantErr: "modelCodes",
		},
		{
			name: "modelCodes value 5 chars fails",
			aigc: &AigcConfig{
				Enabled:         true,
				ContentProducer: validProducer,
				ModelCodes:      map[string]string{"glm-4.5": "00001"},
			},
			wantErr: "modelCodes",
		},
		{
			name: "modelCodes empty key fails",
			aigc: &AigcConfig{
				Enabled:         true,
				ContentProducer: validProducer,
				ModelCodes:      map[string]string{"  ": "0001"},
			},
			wantErr: "modelCodes",
		},
		{
			name: "blank signingKey fails",
			aigc: &AigcConfig{
				Enabled:         true,
				ContentProducer: validProducer,
				SigningKey:      "   ",
			},
			wantErr: "signingKey",
		},
		{
			name: "label 1 passes",
			aigc: &AigcConfig{
				Enabled:         true,
				ContentProducer: validProducer,
				Label:           strPtr("1"),
			},
			wantErr: "",
		},
		{
			name: "label 2 passes",
			aigc: &AigcConfig{
				Enabled:         true,
				ContentProducer: validProducer,
				Label:           strPtr("2"),
			},
			wantErr: "",
		},
		{
			name: "label 3 passes",
			aigc: &AigcConfig{
				Enabled:         true,
				ContentProducer: validProducer,
				Label:           strPtr("3"),
			},
			wantErr: "",
		},
		{
			name: "label 0 fails",
			aigc: &AigcConfig{
				Enabled:         true,
				ContentProducer: validProducer,
				Label:           strPtr("0"),
			},
			wantErr: "label",
		},
		{
			name: "label 4 fails",
			aigc: &AigcConfig{
				Enabled:         true,
				ContentProducer: validProducer,
				Label:           strPtr("4"),
			},
			wantErr: "label",
		},
		{
			name: "label empty string fails",
			aigc: &AigcConfig{
				Enabled:         true,
				ContentProducer: validProducer,
				Label:           strPtr(""),
			},
			wantErr: "label",
		},
		{
			name: "produceIdPrefix any string passes",
			aigc: &AigcConfig{
				Enabled:         true,
				ContentProducer: validProducer,
				ProduceIdPrefix: "tenant-A/",
			},
			wantErr: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.aigc.Validate()
			if tt.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			}
		})
	}
}

func TestCreateAgentRequest_Validate_InvalidAigc(t *testing.T) {
	req := CreateAgentRequest{
		RootAgentID: "coder",
		Agents: []AgentDefinition{
			{
				Name:         "coder",
				Description:  "Writes and edits code",
				Model:        "claude-sonnet-4-6",
				SystemPrompt: "You are a coding assistant.",
			},
		},
		Provider: ProviderConfig{
			Protocol: "anthropic-messages",
			BaseURL:  "https://api.anthropic.com",
			APIKey:   "sk-ant-xxx",
		},
		RuntimeToken: "test-token",
		Aigc:         &AigcConfig{Enabled: true, ContentProducer: "too-short"},
	}

	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "aigc: ")
}

func TestCreateAgentRequest_Validate_RuntimeTokenRequired(t *testing.T) {
	req := CreateAgentRequest{
		RootAgentID: "coder",
		Agents: []AgentDefinition{
			{
				Name:         "coder",
				Description:  "Writes and edits code",
				Model:        "claude-sonnet-4-6",
				SystemPrompt: "You are a coding assistant.",
			},
		},
		Provider: ProviderConfig{
			Protocol: "anthropic-messages",
			BaseURL:  "https://api.anthropic.com",
			APIKey:   "sk-ant-xxx",
		},
		RuntimeToken: "",
	}

	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "runtimeToken")
}

func TestCreateAgentRequest_Validate_RuntimeTokenLeadingWhitespace(t *testing.T) {
	req := CreateAgentRequest{
		RootAgentID: "coder",
		Agents: []AgentDefinition{
			{
				Name:         "coder",
				Description:  "Writes and edits code",
				Model:        "claude-sonnet-4-6",
				SystemPrompt: "You are a coding assistant.",
			},
		},
		Provider: ProviderConfig{
			Protocol: "anthropic-messages",
			BaseURL:  "https://api.anthropic.com",
			APIKey:   "sk-ant-xxx",
		},
		RuntimeToken: " token-with-leading-space",
	}

	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "runtimeToken")
}

func TestCreateAgentRequest_Validate_RuntimeTokenTrailingWhitespace(t *testing.T) {
	req := CreateAgentRequest{
		RootAgentID: "coder",
		Agents: []AgentDefinition{
			{
				Name:         "coder",
				Description:  "Writes and edits code",
				Model:        "claude-sonnet-4-6",
				SystemPrompt: "You are a coding assistant.",
			},
		},
		Provider: ProviderConfig{
			Protocol: "anthropic-messages",
			BaseURL:  "https://api.anthropic.com",
			APIKey:   "sk-ant-xxx",
		},
		RuntimeToken: "token-with-trailing-space ",
	}

	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "runtimeToken")
}

func TestHubConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		hub     *HubConfig
		wantErr string // 空串表示期望通过
	}{
		{
			name:    "nil config passes",
			hub:     nil,
			wantErr: "",
		},
		{
			name: "disabled passes even with invalid fields",
			hub: &HubConfig{
				Enabled:     false,
				BaseURL:     "not a url",
				ChatPushKey: "",
			},
			wantErr: "",
		},
		{
			name: "enabled with valid fields passes",
			hub: &HubConfig{
				Enabled:     true,
				BaseURL:     "http://agent-hub:8080",
				ChatPushKey: "push-secret",
			},
			wantErr: "",
		},
		{
			name: "enabled with org passes",
			hub: &HubConfig{
				Enabled:     true,
				BaseURL:     "http://agent-hub:8080",
				ChatPushKey: "push-secret",
				Org:         "tenant-a",
			},
			wantErr: "",
		},
		{
			name: "enabled with blank org passes through verbatim",
			hub: &HubConfig{
				Enabled:     true,
				BaseURL:     "http://agent-hub:8080",
				ChatPushKey: "push-secret",
				Org:         "   ",
			},
			wantErr: "",
		},
		{
			name: "enabled without baseUrl fails",
			hub: &HubConfig{
				Enabled:     true,
				ChatPushKey: "push-secret",
			},
			wantErr: "baseUrl",
		},
		{
			name: "enabled with blank baseUrl fails",
			hub: &HubConfig{
				Enabled:     true,
				BaseURL:     "   ",
				ChatPushKey: "push-secret",
			},
			wantErr: "baseUrl",
		},
		{
			name: "enabled without chatPushKey fails",
			hub: &HubConfig{
				Enabled: true,
				BaseURL: "http://agent-hub:8080",
			},
			wantErr: "chatPushKey",
		},
		{
			name: "enabled with blank chatPushKey fails",
			hub: &HubConfig{
				Enabled:     true,
				BaseURL:     "http://agent-hub:8080",
				ChatPushKey: "   ",
			},
			wantErr: "chatPushKey",
		},
		{
			name: "baseUrl without scheme fails",
			hub: &HubConfig{
				Enabled:     true,
				BaseURL:     "agent-hub:8080",
				ChatPushKey: "push-secret",
			},
			wantErr: "baseUrl",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.hub.Validate()
			if tt.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			}
		})
	}
}

func TestCreateAgentRequest_Validate_InvalidHub(t *testing.T) {
	req := CreateAgentRequest{
		RootAgentID: "coder",
		Agents: []AgentDefinition{
			{
				Name:         "coder",
				Description:  "Writes and edits code",
				Model:        "claude-sonnet-4-6",
				SystemPrompt: "You are a coding assistant.",
			},
		},
		Provider: ProviderConfig{
			Protocol: "anthropic-messages",
			BaseURL:  "https://api.anthropic.com",
			APIKey:   "sk-ant-xxx",
		},
		RuntimeToken: "test-token",
		Hub:          &HubConfig{Enabled: true, BaseURL: "http://agent-hub:8080"},
	}

	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hub: ")
}

func TestCreateAgentRequest_Validate_ValidHub(t *testing.T) {
	req := CreateAgentRequest{
		RootAgentID: "coder",
		Agents: []AgentDefinition{
			{
				Name:         "coder",
				Description:  "Writes and edits code",
				Model:        "claude-sonnet-4-6",
				SystemPrompt: "You are a coding assistant.",
			},
		},
		Provider: ProviderConfig{
			Protocol: "anthropic-messages",
			BaseURL:  "https://api.anthropic.com",
			APIKey:   "sk-ant-xxx",
		},
		RuntimeToken: "test-token",
		Hub: &HubConfig{
			Enabled:     true,
			BaseURL:     "http://agent-hub:8080",
			ChatPushKey: "push-secret",
		},
	}

	err := req.Validate()
	require.NoError(t, err)
}

// ---- ToolSource (issue #10) ----

func TestToolSource_Validate(t *testing.T) {
	valid := func(ext string) ToolSource {
		return ToolSource{
			Name:     "GetWeather",
			URL:      "https://example.com/a/file" + ext,
			Hash:     strings.Repeat("a", 64),
			FileName: "anything" + ext,
		}
	}
	cases := []struct {
		name    string
		mutate  func(*ToolSource)
		wantErr string
	}{
		{"valid ts", func(s *ToolSource) { *s = valid(".ts") }, ""},
		{"valid mts", func(s *ToolSource) { *s = valid(".mts") }, ""},
		{"valid js", func(s *ToolSource) { *s = valid(".js") }, ""},
		{"valid mjs", func(s *ToolSource) { *s = valid(".mjs") }, ""},
		{"valid sha256-prefixed hash", func(s *ToolSource) {
			*s = valid(".ts")
			s.Hash = "sha256:" + strings.Repeat("a", 64)
		}, ""},
		{"path-bearing fileName allowed, only ext used", func(s *ToolSource) {
			*s = valid(".ts")
			s.FileName = "nested/dir/GetWeather.ts"
		}, ""},
		{"missing name", func(s *ToolSource) { s.Name = "" }, "name is required"},
		{"name dot", func(s *ToolSource) { s.Name = "." }, `name cannot be "."`},
		{"name dotdot", func(s *ToolSource) { s.Name = ".." }, `name cannot be ".."`},
		{"name path separator", func(s *ToolSource) { s.Name = "a/b" }, "name must match"},
		{"missing url", func(s *ToolSource) { s.URL = "" }, "url is required"},
		{"url not http(s)", func(s *ToolSource) { s.URL = "ftp://example.com/x.ts" }, "url must be http(s) with a host"},
		{"url no host", func(s *ToolSource) { s.URL = "http://" }, "url must be http(s) with a host"},
		{"missing hash", func(s *ToolSource) { s.Hash = "" }, "hash is required"},
		{"hash too short", func(s *ToolSource) { s.Hash = strings.Repeat("a", 63) }, "hash must be 64 hex"},
		{"hash not hex", func(s *ToolSource) { s.Hash = strings.Repeat("z", 64) }, "hash must be 64 hex"},
		{"missing fileName", func(s *ToolSource) { s.FileName = "" }, "fileName is required"},
		{"unsupported extension", func(s *ToolSource) { s.FileName = "x.py"; s.URL = "https://e.com/x.py" }, "extension must be one of"},
		{"uppercase extension rejected", func(s *ToolSource) { s.FileName = "x.TS"; s.URL = "https://e.com/x.TS" }, "extension must be one of"},
		{"double extension uses last", func(s *ToolSource) { s.FileName = "x.tar.js"; s.URL = "https://e.com/x.tar.js" }, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := valid(".ts")
			tc.mutate(&src)
			err := src.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestToolSource_LocalNames(t *testing.T) {
	src := ToolSource{
		Name:     "GetWeather",
		URL:      "https://example.com/a/whatever.mjs",
		Hash:     strings.Repeat("a", 64),
		FileName: "downloaded-from-hub.mjs",
	}
	if got := src.Ext(); got != ".mjs" {
		t.Errorf("Ext() = %q, want .mjs", got)
	}
	if got := src.LocalFileName(); got != "GetWeather.mjs" {
		t.Errorf("LocalFileName() = %q, want GetWeather.mjs", got)
	}
	if got := src.LocalRelPath(); got != "./tools/GetWeather.mjs" {
		t.Errorf("LocalRelPath() = %q, want ./tools/GetWeather.mjs", got)
	}
	// Directory components of FileName must never leak into the local name.
	nested := src
	nested.FileName = "some/dir/file.mjs"
	if got := nested.LocalFileName(); got != "GetWeather.mjs" {
		t.Errorf("LocalFileName() with path-bearing fileName = %q, want GetWeather.mjs", got)
	}
}

func TestToolSource_NormalizedHash(t *testing.T) {
	src := ToolSource{Hash: "SHA256:" + strings.Repeat("A", 64)}
	want := strings.Repeat("a", 64)
	if got := src.NormalizedHash(); got != want {
		t.Errorf("NormalizedHash() = %q, want %q", got, want)
	}
}

func TestAgentDefinition_Validate_CustomTools(t *testing.T) {
	base := func() AgentDefinition {
		return AgentDefinition{
			Name:         "coder",
			Description:  "d",
			Model:        "m",
			SystemPrompt: "s",
		}
	}
	valid := ToolSource{
		Name:     "GetWeather",
		URL:      "https://example.com/x.mjs",
		Hash:     strings.Repeat("a", 64),
		FileName: "x.mjs",
	}

	t.Run("valid customTools", func(t *testing.T) {
		a := base()
		a.CustomTools = []ToolSource{valid}
		if err := a.Validate(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("duplicate names rejected", func(t *testing.T) {
		a := base()
		a.CustomTools = []ToolSource{valid, valid}
		err := a.Validate()
		if err == nil || !strings.Contains(err.Error(), "duplicate") {
			t.Fatalf("err = %v, want duplicate-name error", err)
		}
	})

	t.Run("invalid entry propagates with index", func(t *testing.T) {
		a := base()
		bad := valid
		bad.URL = "ftp://nope"
		a.CustomTools = []ToolSource{bad}
		err := a.Validate()
		if err == nil || !strings.Contains(err.Error(), "customTools[0]") {
			t.Fatalf("err = %v, want customTools[0] prefix", err)
		}
	})
}

// Regression guard (issue #10): Tools maps to allowedTools semantics (a plain
// string list) while CustomTools is a structured artifact list — the JSON
// field names must not drift.
func TestAgentDefinition_JSONFieldNames(t *testing.T) {
	data, err := json.Marshal(AgentDefinition{
		Name:         "a",
		Description:  "d",
		Model:        "m",
		SystemPrompt: "s",
		Tools:        []string{"Bash"},
		CustomTools:  []ToolSource{{Name: "GetWeather", URL: "https://e.com/x.mjs", Hash: strings.Repeat("a", 64), FileName: "x.mjs"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["tools"]; !ok {
		t.Error("json must keep the tools field")
	}
	ct, ok := m["customTools"].([]interface{})
	if !ok || len(ct) != 1 {
		t.Fatalf("json must keep customTools as a list, got %v", m["customTools"])
	}
	first := ct[0].(map[string]interface{})
	for _, k := range []string{"name", "url", "hash", "fileName"} {
		if _, ok := first[k]; !ok {
			t.Errorf("customTools[0] missing json field %q", k)
		}
	}
}

func validGraphRequest() CreateAgentRequest {
	return CreateAgentRequest{
		RootAgentID: "parent",
		Agents: []AgentDefinition{
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
				McpServers: map[string]McpServerConfig{
					"knowledge": {Type: "http", URL: "https://example.invalid/mcp"},
				},
				CustomTools:    []ToolSource{{Name: "child-a-tool", URL: "https://example.com/t.mjs", FileName: "child-a-tool.mjs", Hash: strings.Repeat("a", 64)}},
				SettingSources: []string{"user"},
				Skills:         []SkillSource{{Name: "skill-a", URL: "https://example.com/s.zip", Hash: strings.Repeat("b", 64)}},
				Datasets:       map[string]string{"knowledge-a": "Child A knowledge"},
			},
			{
				Name:         "child-b",
				Description:  "Review specialist",
				SystemPrompt: "Review the result",
			},
		},
		Provider: ProviderConfig{
			Protocol: "anthropic-messages",
			BaseURL:  "https://api.anthropic.com",
			APIKey:   "sk-ant-xxx",
		},
		RuntimeToken: "test-token",
	}
}

func TestCreateAgentRequest_Validate_ValidGraph(t *testing.T) {
	req := validGraphRequest()
	require.NoError(t, req.Validate())
}

func TestCreateAgentRequest_Validate_MissingRootAgentID(t *testing.T) {
	req := validGraphRequest()
	req.RootAgentID = ""
	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rootAgentId is required")
}

func TestCreateAgentRequest_Validate_RootNotInAgents(t *testing.T) {
	req := validGraphRequest()
	req.RootAgentID = "ghost"
	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), `rootAgentId "ghost" not found in agents`)
}

func TestCreateAgentRequest_Validate_DuplicateAgentID(t *testing.T) {
	req := validGraphRequest()
	req.Agents = append(req.Agents, AgentDefinition{Name: "child-a", Description: "dup"})
	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), `duplicate agent id "child-a"`)
}

func TestCreateAgentRequest_Validate_UnknownSubagentRef(t *testing.T) {
	req := validGraphRequest()
	req.Agents[0].Subagents = []string{"child-a", "ghost"}
	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), `agent "parent" references unknown subagent "ghost"`)
}

func TestCreateAgentRequest_Validate_DuplicateSubagentRef(t *testing.T) {
	req := validGraphRequest()
	req.Agents[0].Subagents = []string{"child-a", "child-a"}
	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), `agent "parent" duplicates subagent reference "child-a"`)
}

func TestCreateAgentRequest_Validate_SelfReference(t *testing.T) {
	req := validGraphRequest()
	req.Agents[1].Subagents = []string{"child-a"}
	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), `agent "child-a" references itself`)
}

func TestCreateAgentRequest_Validate_Cycle(t *testing.T) {
	req := validGraphRequest()
	req.Agents[1].Subagents = []string{"child-b"}
	req.Agents[2].Subagents = []string{"child-a"}
	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cycle")
}

func TestCreateAgentRequest_Validate_GlobalFieldOnNonRoot(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(a *AgentDefinition)
		want   string
	}{
		{"model", func(a *AgentDefinition) { a.Model = "claude-sonnet-4-6" }, "model is a runtime-global field"},
		{"maxSessionQueries", func(a *AgentDefinition) { a.MaxSessionQueries = intPtr(5) }, "maxSessionQueries is a runtime-global field"},
		{"permissionMode", func(a *AgentDefinition) { a.PermissionMode = "plan" }, "permissionMode is a runtime-global field"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := validGraphRequest()
			tc.mutate(&req.Agents[1])
			err := req.Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestCreateAgentRequest_Validate_RootRequiresModel(t *testing.T) {
	req := validGraphRequest()
	req.Agents[0].Model = ""
	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "model is required")
}

func TestCreateAgentRequest_Validate_SkillsRequireUserSettingSource(t *testing.T) {
	req := validGraphRequest()
	req.Agents[1].SettingSources = []string{"project"}
	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), `settingSources must include "user"`)
}

func TestCreateAgentRequest_Validate_ConflictingToolAcrossAgents(t *testing.T) {
	req := validGraphRequest()
	req.Agents[2].CustomTools = []ToolSource{{Name: "child-a-tool", URL: "https://evil.example/t.mjs", FileName: "evil.mjs", Hash: strings.Repeat("c", 64)}}
	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "conflicting declarations")
}

func TestCreateAgentRequest_Validate_SharedToolSameDeclarationOK(t *testing.T) {
	req := validGraphRequest()
	shared := ToolSource{Name: "shared-tool", URL: "https://example.com/shared.mjs", FileName: "shared.mjs", Hash: strings.Repeat("d", 64)}
	req.Agents[1].CustomTools = append(req.Agents[1].CustomTools, shared)
	req.Agents[2].CustomTools = []ToolSource{shared}
	require.NoError(t, req.Validate())
}

func TestCreateAgentRequest_Validate_ConflictingSkillAcrossAgents(t *testing.T) {
	req := validGraphRequest()
	req.Agents[2].Skills = []SkillSource{{Name: "skill-a", URL: "https://evil.example/s.zip", Hash: strings.Repeat("e", 64)}}
	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "conflicting declarations")
}

func TestCreateAgentRequest_RootAndAgentByID(t *testing.T) {
	req := validGraphRequest()
	require.NoError(t, req.Validate())
	assert.Equal(t, "parent", req.Root().Name)
	a, ok := req.AgentByID("child-b")
	require.True(t, ok)
	assert.Equal(t, "child-b", a.Name)
	_, ok = req.AgentByID("ghost")
	assert.False(t, ok)
}

// TestCreateAgentRequest_ErrorMessagesDoNotLeakSecrets guards issue #16
// regression #10: validation errors must never embed MCP header secrets (or
// any other credential material) even when the request is invalid.
func TestCreateAgentRequest_ErrorMessagesDoNotLeakSecrets(t *testing.T) {
	req := validGraphRequest()
	req.Agents[1].McpServers["knowledge"] = McpServerConfig{
		Type:    "http",
		URL:     "https://example.invalid/mcp",
		Headers: map[string]string{"Authorization": "Bearer SECRET-TOKEN"},
	}
	// Also make the request invalid in an unrelated way (cycle).
	req.Agents[1].Subagents = []string{"child-b"}
	req.Agents[2].Subagents = []string{"child-a"}

	err := req.Validate()
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "SECRET-TOKEN")
	assert.NotContains(t, err.Error(), "Bearer")
}

// TestAgentDefinition_ExtraUserSkillDirsNotExposedInAPI guards the per-agent
// skill isolation contract (issue #16 review): the deployment API must not
// expose raw scan paths. A caller-provided extraUserSkillDirs entry — e.g.
// pointing at another agent's skill directory — is ignored on unmarshal and
// never re-emitted, so a child cannot widen its scan to another agent's
// skills. The deployer is the only writer of extraUserSkillDirs in the
// runtime YAML (auto-injected per-agent directory).
func TestAgentDefinition_ExtraUserSkillDirsNotExposedInAPI(t *testing.T) {
	jsonData := `{
		"rootAgentId": "coder",
		"agents": [
			{
				"name": "coder",
				"description": "Writes and edits code",
				"model": "claude-sonnet-4-6",
				"systemPrompt": "You are a coding assistant.",
				"subagents": ["child-a"]
			},
			{
				"name": "child-a",
				"description": "Research",
				"settingSources": ["user"],
				"extraUserSkillDirs": ["/app/config/skills/coder", "../../etc"],
				"skills": [{"name": "own-skill", "url": "https://example.com/s.zip", "hash": "` + strings.Repeat("b", 64) + `"}]
			}
		],
		"provider": {"protocol": "anthropic-messages", "baseUrl": "https://api.anthropic.com", "lockedApiKey": "sk-ant-xxx"},
		"runtime_token": "test-token"
	}`

	var req CreateAgentRequest
	require.NoError(t, json.Unmarshal([]byte(jsonData), &req))
	require.NoError(t, req.Validate())

	// The injected paths (cross-agent directory + traversal) are silently
	// dropped: unknown JSON fields are ignored and the struct has no such
	// field, so the request stays valid and nothing re-emits them.
	out, err := json.Marshal(&req)
	require.NoError(t, err)
	assert.NotContains(t, string(out), "extraUserSkillDirs")
	assert.NotContains(t, string(out), "/app/config/skills/coder")
}
