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
		Agent: AgentDefinition{
			Name:         "coder",
			Description:  "Writes and edits code",
			Model:        "claude-sonnet-4-6",
			SystemPrompt: "You are a coding assistant.",
			MaxTurns:     nil,
			Tools:        []string{"Read", "Write", "Edit", "Bash"},
			Skills: []SkillSource{
				{Name: "code-review", URL: "https://example.com/code-review.zip", Hash: strings.Repeat("a", 64)},
			},
			Subagents: []SubagentDefinition{
				{
					Name:        "reviewer",
					Description: "Review code",
					Prompt:      "You are a code reviewer.",
					Tools:       []string{"Read"},
					MaxTurns:    nil,
				},
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
		Agent: AgentDefinition{
			Name:         "",
			Description:  "Writes and edits code",
			Model:        "claude-sonnet-4-6",
			SystemPrompt: "You are a coding assistant.",
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
		Agent: AgentDefinition{
			Name:         "coder",
			Description:  "Writes and edits code",
			Model:        "",
			SystemPrompt: "You are a coding assistant.",
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
		Agent: AgentDefinition{
			Name:         "coder",
			Description:  "",
			Model:        "claude-sonnet-4-6",
			SystemPrompt: "You are a coding assistant.",
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
		Agent: AgentDefinition{
			Name:         "coder",
			Description:  "   ",
			Model:        "claude-sonnet-4-6",
			SystemPrompt: "You are a coding assistant.",
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
		Agent: AgentDefinition{
			Name:         "coder",
			Description:  "Writes and edits code",
			Model:        "claude-sonnet-4-6",
			SystemPrompt: "",
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
		Agent: AgentDefinition{
			Name:         "coder",
			Description:  "Writes and edits code",
			Model:        "claude-sonnet-4-6",
			SystemPrompt: "You are a coding assistant.",
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

func TestCreateAgentRequest_Validate_SubagentMissingName(t *testing.T) {
	req := CreateAgentRequest{
		Agent: AgentDefinition{
			Name:         "coder",
			Description:  "Writes and edits code",
			Model:        "claude-sonnet-4-6",
			SystemPrompt: "You are a coding assistant.",
			Subagents: []SubagentDefinition{
				{
					Name:        "",
					Description: "Review code",
					Prompt:      "You are a code reviewer.",
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
	assert.Contains(t, err.Error(), "subagents[0]")
	assert.Contains(t, err.Error(), "name is required")
}

func TestCreateAgentRequest_Validate_SubagentMissingDescription(t *testing.T) {
	req := CreateAgentRequest{
		Agent: AgentDefinition{
			Name:         "coder",
			Description:  "Writes and edits code",
			Model:        "claude-sonnet-4-6",
			SystemPrompt: "You are a coding assistant.",
			Subagents: []SubagentDefinition{
				{
					Name:        "reviewer",
					Description: "",
					Prompt:      "You are a code reviewer.",
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
	assert.Contains(t, err.Error(), "subagents[0]")
	assert.Contains(t, err.Error(), "description is required")
}

func TestCreateAgentRequest_Validate_SubagentMissingPrompt(t *testing.T) {
	req := CreateAgentRequest{
		Agent: AgentDefinition{
			Name:         "coder",
			Description:  "Writes and edits code",
			Model:        "claude-sonnet-4-6",
			SystemPrompt: "You are a coding assistant.",
			Subagents: []SubagentDefinition{
				{
					Name:        "reviewer",
					Description: "Review code",
					Prompt:      "",
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
	assert.Contains(t, err.Error(), "subagents[0]")
	assert.Contains(t, err.Error(), "prompt is required")
}

func TestCreateAgentRequest_Validate_NilRequest(t *testing.T) {
	var req *CreateAgentRequest
	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "request is nil")
}

func TestCreateAgentRequest_Validate_MaxTurnsZeroIsValid(t *testing.T) {
	req := CreateAgentRequest{
		Agent: AgentDefinition{
			Name:         "coder",
			Description:  "Writes and edits code",
			Model:        "claude-sonnet-4-6",
			SystemPrompt: "You are a coding assistant.",
			MaxTurns:     intPtr(0),
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

func TestAgentDefinition_Validate_DuplicateSubagentNames(t *testing.T) {
	req := CreateAgentRequest{
		Agent: AgentDefinition{
			Name:         "coder",
			Description:  "Writes and edits code",
			Model:        "claude-sonnet-4-6",
			SystemPrompt: "You are a coding assistant.",
			Subagents: []SubagentDefinition{
				{
					Name:        "reviewer",
					Description: "Review code",
					Prompt:      "You are a code reviewer.",
				},
				{
					Name:        "reviewer",
					Description: "Another reviewer",
					Prompt:      "You are another code reviewer.",
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
	assert.Contains(t, err.Error(), "subagents[1]: duplicate name \"reviewer\"")
}

func TestCreateAgentRequest_Validate_OpenAIProtocol(t *testing.T) {
	req := CreateAgentRequest{
		Agent: AgentDefinition{
			Name:         "coder",
			Description:  "Writes and edits code",
			Model:        "gpt-4",
			SystemPrompt: "You are a coding assistant.",
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
		Agent: AgentDefinition{
			Name:         "coder",
			Description:  "Writes and edits code",
			Model:        "claude-sonnet-4-6",
			SystemPrompt: "You are a coding assistant.",
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
		Agent: AgentDefinition{
			Name:         "coder",
			Description:  "Writes and edits code",
			Model:        "claude-sonnet-4-6",
			SystemPrompt: "You are a coding assistant.",
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
		Agent: AgentDefinition{
			Name:         "   ",
			Description:  "Writes and edits code",
			Model:        "claude-sonnet-4-6",
			SystemPrompt: "You are a coding assistant.",
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

func TestCreateAgentRequest_Validate_MultipleSubagentsOneInvalid(t *testing.T) {
	req := CreateAgentRequest{
		Agent: AgentDefinition{
			Name:         "coder",
			Description:  "Writes and edits code",
			Model:        "claude-sonnet-4-6",
			SystemPrompt: "You are a coding assistant.",
			Subagents: []SubagentDefinition{
				{
					Name:        "reviewer",
					Description: "Review code",
					Prompt:      "You are a code reviewer.",
				},
				{
					Name:        "",
					Description: "Test code",
					Prompt:      "You are a tester.",
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
	assert.Contains(t, err.Error(), "subagents[1]")
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

func TestSubagentDefinition_JSONMarshal_NilMaxTurnsOmits(t *testing.T) {
	sub := SubagentDefinition{
		Name:        "reviewer",
		Description: "Review code",
		Prompt:      "You are a code reviewer.",
		MaxTurns:    nil,
	}

	data, err := json.Marshal(sub)
	require.NoError(t, err)

	var parsed map[string]interface{}
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	assert.Equal(t, "reviewer", parsed["name"])
	_, exists := parsed["maxTurns"]
	assert.False(t, exists, "subagent maxTurns should be omitted when nil")
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
		"agent": {
			"name": "coder",
			"model": "claude-sonnet-4-6",
			"systemPrompt": "You are a coding assistant.",
			"maxTurns": null,
			"permissionMode": "auto",
			"tools": ["Read", "Write", "Edit", "Bash"],
			"skills": [{"name": "code-review", "url": "https://example.com/cr.zip", "hash": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}],
			"subagents": [
				{
					"name": "reviewer",
					"description": "Review code",
					"prompt": "You are a code reviewer.",
					"tools": ["Read"],
					"maxTurns": 10
				}
			]
		},
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

	assert.Equal(t, "coder", req.Agent.Name)
	assert.Equal(t, "claude-sonnet-4-6", req.Agent.Model)
	assert.Equal(t, "You are a coding assistant.", req.Agent.SystemPrompt)
	assert.Nil(t, req.Agent.MaxTurns)
	assert.Equal(t, "auto", req.Agent.PermissionMode)
	assert.Equal(t, []string{"Read", "Write", "Edit", "Bash"}, req.Agent.Tools)
	require.Len(t, req.Agent.Skills, 1)
	assert.Equal(t, "code-review", req.Agent.Skills[0].Name)
	assert.Equal(t, "https://example.com/cr.zip", req.Agent.Skills[0].URL)
	assert.Equal(t, strings.Repeat("a", 64), req.Agent.Skills[0].NormalizedHash())
	require.Len(t, req.Agent.Subagents, 1)
	assert.Equal(t, "reviewer", req.Agent.Subagents[0].Name)
	assert.Equal(t, "Review code", req.Agent.Subagents[0].Description)
	assert.Equal(t, "You are a code reviewer.", req.Agent.Subagents[0].Prompt)
	assert.Equal(t, []string{"Read"}, req.Agent.Subagents[0].Tools)
	require.NotNil(t, req.Agent.Subagents[0].MaxTurns)
	assert.Equal(t, 10, *req.Agent.Subagents[0].MaxTurns)

	assert.Equal(t, "anthropic-messages", req.Provider.Protocol)
	assert.Equal(t, "https://api.anthropic.com", req.Provider.BaseURL)
	assert.Equal(t, "sk-ant-xxx", req.Provider.APIKey)
	assert.False(t, req.Force)
}

func TestCreateAgentRequest_JSONUnmarshal_RuntimeToken(t *testing.T) {
	jsonData := `{
		"agent": {
			"name": "coder",
			"description": "Writes and edits code",
			"model": "claude-sonnet-4-6",
			"systemPrompt": "You are a coding assistant."
		},
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
		"agent": {
			"name": "coder",
			"description": "Writes and edits code",
			"model": "claude-sonnet-4-6",
			"systemPrompt": "You are a coding assistant.",
			"maxTurns": 0
		},
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

	require.NotNil(t, req.Agent.MaxTurns)
	assert.Equal(t, 0, *req.Agent.MaxTurns)

	// Validate should pass with maxTurns = 0
	assert.NoError(t, req.Validate())
}

func TestCreateAgentRequest_JSONUnmarshal_OmittedMaxTurns(t *testing.T) {
	jsonData := `{
		"agent": {
			"name": "coder",
			"model": "claude-sonnet-4-6",
			"systemPrompt": "You are a coding assistant."
		},
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

	assert.Nil(t, req.Agent.MaxTurns)
	assert.Empty(t, req.Agent.Tools)
	assert.Empty(t, req.Agent.Skills)
	assert.Empty(t, req.Agent.Subagents)
	assert.Empty(t, req.Agent.PermissionMode)
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
		Agent: AgentDefinition{
			Name:         "coder",
			Description:  "Writes and edits code",
			Model:        "claude-sonnet-4-6",
			SystemPrompt: "You are a coding assistant.",
			McpServers: map[string]McpServerConfig{
				"remote-api": {Type: "sse", URL: "https://api.example.com/sse"},
			},
			Subagents: []SubagentDefinition{
				{
					Name:        "reviewer",
					Description: "Review code",
					Prompt:      "You are a code reviewer.",
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
	assert.NoError(t, req.Validate())
}

func TestAgentDefinition_Validate_InvalidMcpType(t *testing.T) {
	req := CreateAgentRequest{
		Agent: AgentDefinition{
			Name:         "coder",
			Description:  "Writes and edits code",
			Model:        "claude-sonnet-4-6",
			SystemPrompt: "You are a coding assistant.",
			McpServers: map[string]McpServerConfig{
				"remote-api": {Type: "sse", URL: "not-a-url"},
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
		"agent": {
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
			"subagents": [
				{
					"name": "reviewer",
					"description": "Review code",
					"prompt": "You are a code reviewer."
				}
			]
		},
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

	require.Len(t, req.Agent.McpServers, 1)
	mcp, ok := req.Agent.McpServers["remote-api"]
	require.True(t, ok)
	assert.Equal(t, "sse", mcp.Type)
	assert.Equal(t, "https://api.example.com/sse", mcp.URL)
	assert.Equal(t, "Bearer xxx", mcp.Headers["Authorization"])

	require.Len(t, req.Agent.Subagents, 1)
	assert.Equal(t, "reviewer", req.Agent.Subagents[0].Name)
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
		Agent: AgentDefinition{
			Name:         "coder",
			Description:  "Writes and edits code",
			Model:        "claude-sonnet-4-6",
			SystemPrompt: "You are a coding assistant.",
			Datasets: map[string]string{
				"dataset-1": "Primary dataset",
				"dataset-2": "Secondary dataset",
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
		Agent: AgentDefinition{
			Name:         "coder",
			Description:  "Writes and edits code",
			Model:        "claude-sonnet-4-6",
			SystemPrompt: "You are a coding assistant.",
			Datasets: map[string]string{
				"": "No id",
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
		Agent: AgentDefinition{
			Name:         "coder",
			Description:  "Writes and edits code",
			Model:        "claude-sonnet-4-6",
			SystemPrompt: "You are a coding assistant.",
			Datasets: map[string]string{
				"dataset-1": "",
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
		Agent: AgentDefinition{
			Name:         "coder",
			Description:  "Writes and edits code",
			Model:        "claude-sonnet-4-6",
			SystemPrompt: "You are a coding assistant.",
			Datasets: map[string]string{
				"dataset-1": "   ",
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

func TestAgentDefinition_JSONMarshalWithMaxSessionTurns(t *testing.T) {
	maxTurns := 10
	maxSessionTurns := 50
	agent := AgentDefinition{
		Name:            "assistant",
		Model:           "claude-sonnet-4-6",
		SystemPrompt:    "You are helpful.",
		MaxTurns:        &maxTurns,
		MaxSessionTurns: &maxSessionTurns,
	}

	data, err := json.Marshal(agent)
	require.NoError(t, err)

	var parsed map[string]interface{}
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	assert.Equal(t, float64(10), parsed["maxTurns"])
	assert.Equal(t, float64(50), parsed["maxSessionTurns"])
}

func TestAgentDefinition_JSONMarshalNilMaxSessionTurnsOmitted(t *testing.T) {
	agent := AgentDefinition{
		Name:            "assistant",
		Model:           "claude-sonnet-4-6",
		SystemPrompt:    "You are helpful.",
		MaxSessionTurns: nil,
	}

	data, err := json.Marshal(agent)
	require.NoError(t, err)

	var parsed map[string]interface{}
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	_, present := parsed["maxSessionTurns"]
	assert.False(t, present, "maxSessionTurns should be omitted when nil")
}

func TestAgentDefinition_JSONUnmarshalMaxSessionTurns(t *testing.T) {
	input := `{"name":"assistant","model":"claude-sonnet-4-6","systemPrompt":"helpful","maxSessionTurns":30}`

	var agent AgentDefinition
	err := json.Unmarshal([]byte(input), &agent)
	require.NoError(t, err)

	require.NotNil(t, agent.MaxSessionTurns)
	assert.Equal(t, 30, *agent.MaxSessionTurns)
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
		Agent: AgentDefinition{
			Name:         "coder",
			Description:  "Writes and edits code",
			Model:        "claude-sonnet-4-6",
			SystemPrompt: "You are a coding assistant.",
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
		Agent: AgentDefinition{
			Name:         "coder",
			Description:  "Writes and edits code",
			Model:        "claude-sonnet-4-6",
			SystemPrompt: "You are a coding assistant.",
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
		Agent: AgentDefinition{
			Name:         "coder",
			Description:  "Writes and edits code",
			Model:        "claude-sonnet-4-6",
			SystemPrompt: "You are a coding assistant.",
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
		Agent: AgentDefinition{
			Name:         "coder",
			Description:  "Writes and edits code",
			Model:        "claude-sonnet-4-6",
			SystemPrompt: "You are a coding assistant.",
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
		Agent: AgentDefinition{
			Name:         "coder",
			Description:  "Writes and edits code",
			Model:        "claude-sonnet-4-6",
			SystemPrompt: "You are a coding assistant.",
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
		Agent: AgentDefinition{
			Name:         "coder",
			Description:  "Writes and edits code",
			Model:        "claude-sonnet-4-6",
			SystemPrompt: "You are a coding assistant.",
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
