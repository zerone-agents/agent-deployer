# agent-deployer API Reference

agent-deployer is a service that manages the lifecycle of [open-agent-runtime](../open-agent-runtime) Docker containers. This document describes all REST APIs it exposes, for integration by middle-ground or any upstream caller.

## Table of Contents

- [Conventions](#conventions)
  - [Base URL](#base-url)
  - [Authentication](#authentication)
  - [Response Format](#response-format)
  - [HTTP Status Codes](#http-status-codes)
- [Endpoints](#endpoints)
  - [1. Create Agent](#1-create-agent)
  - [2. List All Agents](#2-list-all-agents)
  - [3. Get Agent Details](#3-get-agent-details)
  - [4. Get Live Status](#4-get-live-status)
  - [5. Get Container Logs](#5-get-container-logs)
  - [6. Stop Agent](#6-stop-agent)
  - [7. Restart Agent](#7-restart-agent)
  - [8. Delete Agent](#8-delete-agent)
- [Data Models](#data-models)
- [Provider Field Mapping](#provider-field-mapping)
- [Typical Workflow](#typical-workflow)
- [Troubleshooting](#troubleshooting)

---

## Conventions

### Base URL

```
http://<host>:<port>/api/v1
```

- The default port inside the container is `8080` (controlled by `AGENT_DEPLOYER_PORT`).
- With docker-compose deployment, the host published port is controlled by `AGENT_DEPLOYER_HOST_PORT`, also defaulting to `8080`.

### Authentication

Authentication is controlled by the server-side environment variable `AGENT_DEPLOYER_API_KEY`:

- **Not set (empty)**: authentication is disabled; all `/api/v1/*` endpoints are directly accessible. Recommended only for local development.
- **Set**: every request must carry one of the following headers; the server uses constant-time comparison (to prevent timing attacks):

| Header | Format | Example |
|---|---|---|
| `Authorization` | `Bearer <key>` | `Authorization: Bearer my-secret-key` |
| `X-API-Key` | `<key>` | `X-API-Key: my-secret-key` |

`Authorization` takes precedence over `X-API-Key`. If both are present and the Bearer token is invalid, a 401 is returned immediately without falling back to `X-API-Key`.

**Authentication failure response** (HTTP 401):

```json
{ "success": false, "error": "unauthorized" }
```

### Response Format

All endpoints share a common response envelope:

```jsonc
// Success (with data)
{ "success": true, "data": { ... } }

// Success (no data, e.g. stop / restart / delete)
{ "success": true }

// Failure
{ "success": false, "error": "human-readable error message" }
```

> Exception: the success response payload of `GET /agents/:name/logs` uses the field `logs` (a string) instead of `data`; see [§5](#5-get-container-logs).

### HTTP Status Codes

| Status | Meaning | Triggered by |
|---|---|---|
| 200 | Success | All queries; successful stop / restart / delete; idempotent create returning an existing container unchanged |
| 201 | Created | `POST /agents` successfully created a new container |
| 400 | Bad request | Body cannot be parsed, required fields are missing, graph validation fails, or the request uses the removed legacy inline shape (`"agent"` field) |
| 401 | Unauthorized | Missing or incorrect API Key |
| 404 | Not found | No agent container exists with the given `name` (and no on-disk data remains) |
| 422 | Unprocessable entity | Declared skill/tool hash mismatch or artifact violates size constraints |
| 500 | Server error | Docker call failure, disk write failure, etc. |
| 502 | Bad gateway | Skill/tool download upstream failure (non-2xx, network error, timeout) |
| 503 | Service unavailable | Runtime image cannot run the graph protocol: pin `AGENT_DEPLOYER_RUNTIME_IMAGE` to a v2.4.0+ tag, or set `AGENT_DEPLOYER_RUNTIME_IMAGE_ASSUME_LATEST=true` for a verified `:latest` |

---

## Endpoints

All paths are relative to the Base URL `/api/v1`. The examples below assume a Base URL of `http://localhost:8080/api/v1` and the following:

```bash
export DEPLOYER=http://localhost:8080/api/v1
export API_KEY=<your key>   # can be omitted when authentication is disabled
```

### 1. Create Agent

Creates and starts an agent runtime container from a **complete agent graph** (issue #16): `rootAgentId` names the container's entry agent, and `agents` carries the full Agent-local definition of every agent in the deployment closure. `subagents` are pure id references — mounted agents never inherit, merge, or fall back to parent capabilities; an empty field stays empty. The deployment name is the `rootAgentId` itself (it must already be in sanitized form: lowercase alphanumeric and hyphens). Custom tools and skills declared anywhere in the graph are downloaded and hash-verified before the container is created; any install failure aborts the request without starting a container.

> **Breaking change (deployer v3.0.0)**: the legacy inline shape (`"agent": {...}` with `subagents` as five-field stubs) is rejected with an explicit 400 diagnostic. See [Migration from the inline protocol](#migration-from-the-inline-protocol).

- **Idempotency**: if a container with the same name already exists and `force=false` (default), the existing container is returned unchanged with 200; with `force=true`, the old container is stopped + deleted first, then rebuilt with the new configuration (session data is preserved).
- **Runtime image floor**: the graph protocol requires runtime **v2.4.0+**. The deployer refuses deployment (503) unless `AGENT_DEPLOYER_RUNTIME_IMAGE` pins a v2.4.0+ tag, or `AGENT_DEPLOYER_RUNTIME_IMAGE_ASSUME_LATEST=true` is set for a `:latest` image — a complete agent graph is never silently handed to an old runtime.
- **Method**: `POST /agents`
- **Content-Type**: `application/json`

#### Request Body

| Field | Type | Required | Description |
|---|---|---|---|
| `rootAgentId` | string | Yes | Id of the container's entry agent. Doubles as the deployment name: must already match `[a-z0-9-]` (sanitized form), and a definition with this id must exist in `agents` |
| `agents` | AgentDefinition[] | Yes | The complete deployment closure: one full Agent-local definition per agent, including the root. Every `subagents` reference must resolve to exactly one entry in this list |
| `provider` | object | Yes | Runtime-global LLM provider configuration (written to the root entry only), see [ProviderConfig](#providerconfig) |
| `aigc` | object | No | AIGC content labeling configuration (GB 45438-2025), see [AigcConfig](#aigcconfig). When omitted or `enabled=false`, the runtime does not add labels |
| `hub` | object | No | Agent-hub chat record push configuration, see [HubConfig](#hubconfig). When omitted or `enabled=false`, the runtime does not push chat records |
| `force` | boolean | No | Defaults to `false`. When `true`, forces rebuilding a container with the same name |
| `runtime_token` | string | **Yes** | Runtime token specified by the caller. The deployer no longer generates tokens; this value is injected directly into the container as `ZERONE_AGENT_HTTP_API_KEY`. Must be non-empty with no leading/trailing whitespace |

#### Request Example

```bash
curl -X POST "$DEPLOYER/agents" \
  -H 'Content-Type: application/json' \
  ${API_KEY:+-H "Authorization: Bearer $API_KEY"} \
  -d '{
    "rootAgentId": "coder",
    "agents": [
      {
        "name": "coder",
        "description": "Writes and edits code",
        "model": "claude-sonnet-4-6",
        "systemPrompt": "You are a coding assistant.",
        "maxTurns": null,
        "permissionMode": "auto",
        "tools": ["Task"],
        "subagents": ["reviewer"]
      },
      {
        "name": "reviewer",
        "description": "Reviews code",
        "systemPrompt": "You are a code reviewer.",
        "tools": ["Read"],
        "disallowedTools": ["Bash"],
        "settingSources": ["user"],
        "skills": [
          {"name": "code-review", "url": "https://example.com/code-review.zip", "hash": "<sha256>"}
        ],
        "datasets": {
          "dataset-1": "Primary dataset for code review"
        }
      }
    ],
    "provider": {
      "protocol": "anthropic-messages",
      "baseUrl": "https://api.anthropic.com",
      "lockedApiKey": "sk-ant-xxx"
    },
    "force": false,
    "runtime_token": "caller-provided-runtime-token"
  }'
```

#### Success Response (201)

```json
{
  "success": true,
  "data": {
    "agentName": "coder",
    "instanceId": "a1b2c3d4",
    "containerId": "9f3c...e21",
    "containerName": "cloud-agent-coder-a1b2c3d4",
    "status": "running",
    "hostPort": 32768,
    "createdAt": "2026-06-25T10:00:00Z",
    "yamlPath": "/var/lib/agent-deployer/coder/agents/agents.yaml",
    "sessionDir": "/var/lib/agent-deployer/coder/sessions",
    "skillsDir": "/var/lib/agent-deployer/coder/agents/skills",
    "containerSkillsDir": "/app/config/skills",
    "runtimeToken": "caller-provided-runtime-token"
  }
}
```

#### Field Descriptions

| Field | Type | Description |
|---|---|---|
| `agentName` | string | Deployment name (= rootAgentId, unique key) |
| `instanceId` | string | Short random ID generated at creation time, attached to the Docker label `agent-deployer/agent.instance-id`, reserved for future HA scenarios |
| `containerId` | string | Full Docker container ID |
| `containerName` | string | Docker container name, of the form `cloud-agent-<name>-<instanceId>` |
| `status` | string | Status enum, see [AgentStatus](#agentstatus) |
| `hostPort` | int | Port mapped to the host (the runtime's internal container port is fixed, controlled by `AGENT_DEPLOYER_RUNTIME_CONTAINER_PORT`, default 3000) |
| `createdAt` | string | RFC3339 UTC timestamp |
| `yamlPath` | string | Path to the complete agent graph YAML (mounted into the container at `/app/config`) |
| `sessionDir` | string | Session persistence directory (mounted into the container at `/root/.agents`) |
| `skillsDir` | string | Host-side root of the per-agent skill directories (returned only when any agent in the graph declares skills). Skills reach the container through the `/app/config` bind mount — there is no docker cp anymore |
| `containerSkillsDir` | string | In-container counterpart of `skillsDir` (`/app/config/skills`); each agent's YAML entry declares its own subdirectory via `extraUserSkillDirs` |

> **Note**: the returned `hostPort` is stable only for the lifetime of the container. The port changes after a container rebuild, so always query it again each time.

---

### 2. List All Agents

Returns all agents managed by the current deployer (filtered by the Docker label `agent-deployer/managed=true`).

- **Method**: `GET /agents`

#### Request Example

```bash
curl "$DEPLOYER/agents" ${API_KEY:+-H "Authorization: Bearer $API_KEY"}
```

#### Success Response (200)

```json
{
  "success": true,
  "data": [
    {
      "agentName": "coder",
      "instanceId": "a1b2c3d4",
      "containerId": "9f3c...",
      "containerName": "cloud-agent-coder-a1b2c3d4",
      "status": "running",
      "hostPort": 32768
    }
  ]
}
```

> The list response does **not** include `createdAt / yamlPath / sessionDir / skillsDir` (these fields are only returned by `POST /agents`). Core fields of each element match [AgentResponse](#agentresponse).

---

### 3. Get Agent Details

- **Method**: `GET /agents/:name`
- **Path parameters**: `name` — agent name (sanitized the same way, case-insensitive)

#### Request Example

```bash
curl "$DEPLOYER/agents/coder" ${API_KEY:+-H "Authorization: Bearer $API_KEY"}
```

#### Success Response (200)

```json
{
  "success": true,
  "data": {
    "agentName": "coder",
    "instanceId": "a1b2c3d4",
    "containerId": "9f3c...",
    "containerName": "cloud-agent-coder-a1b2c3d4",
    "status": "running",
    "hostPort": 32768
  }
}
```

#### Failure Response

- `404`: `{ "success": false, "error": "agent \"coder\": agent not found" }`
- `500`: Docker query failure

---

### 4. Get Live Status

Returns the container's live Docker state and health check result. **After creation, poll this endpoint until `health=healthy` before dispatching tasks** to confirm the runtime is ready.

- **Method**: `GET /agents/:name/status`

#### Request Example

```bash
curl "$DEPLOYER/agents/coder/status" ${API_KEY:+-H "Authorization: Bearer $API_KEY"}
```

#### Success Response (200)

```json
{
  "success": true,
  "data": {
    "agentName": "coder",
    "containerName": "cloud-agent-coder-a1b2c3d4",
    "containerId": "9f3c...",
    "status": "running",
    "health": "healthy",
    "hostPort": 32768,
    "image": "open-agent-runtime:latest"
  }
}
```

| Field | Description |
|---|---|
| `status` | Native Docker state: `running` / `created` / `exited` / `paused`, etc. |
| `health` | Docker health check: `starting` / `healthy` / `unhealthy` / `none` (when no health check is configured or inspect fails) |
| `image` | Image name used by the container |

> Difference from [§3](#3-get-agent-details): this endpoint additionally runs a `docker inspect` to obtain the health check result; if the inspect fails, it degrades to `health=none` instead of returning an error.

---

### 5. Get Container Logs

Returns the container's recent combined stdout + stderr output, useful for diagnosing startup failures and similar issues.

- **Method**: `GET /agents/:name/logs`
- **Query parameters**:

| Parameter | Type | Default | Description |
|---|---|---|---|
| `tail` | int | `100` | Return the last N lines; falls back to the default when not a positive integer |

#### Request Example

```bash
# Fetch the last 200 lines
curl "$DEPLOYER/agents/coder/logs?tail=200" ${API_KEY:+-H "Authorization: Bearer $API_KEY"}
```

#### Success Response (200)

> **Note: the payload field is `logs`, not `data`.**

```json
{
  "success": true,
  "logs": "[runtime] starting...\n[runtime] listening on :3000\n..."
}
```

---

### 6. Stop Agent

Gracefully stops the container. Both the container itself and the mounted data directories are preserved and can be restored at any time via [restart](#7-restart-agent).

- **Method**: `POST /agents/:name/stop`

#### Request Example

```bash
curl -X POST "$DEPLOYER/agents/coder/stop" ${API_KEY:+-H "Authorization: Bearer $API_KEY"}
```

#### Success Response (200)

```json
{ "success": true }
```

---

### 7. Restart Agent

Restarts the container (equivalent to `docker restart`). Configuration and data remain unchanged.

- **Method**: `POST /agents/:name/restart`

#### Request Example

```bash
curl -X POST "$DEPLOYER/agents/coder/restart" ${API_KEY:+-H "Authorization: Bearer $API_KEY"}
```

#### Success Response (200)

```json
{ "success": true }
```

---

### 8. Delete Agent

Stops and removes the container. **This endpoint is idempotent**: it returns 200 even if the container no longer exists.

- **Method**: `DELETE /agents/:name`
- **Query parameters**:

| Parameter | Type | Default | Description |
|---|---|---|---|
| `removeData` | bool | `false` | When `true`, also deletes the agent directory on the host (including `agents.yaml`, sessions, skills) |

#### Request Example

```bash
# Delete the container only, keep the data
curl -X DELETE "$DEPLOYER/agents/coder" ${API_KEY:+-H "Authorization: Bearer $API_KEY"}

# Clean up the data as well (full destruction)
curl -X DELETE "$DEPLOYER/agents/coder?removeData=true" ${API_KEY:+-H "Authorization: Bearer $API_KEY"}
```

#### Success Response (200)

```json
{ "success": true }
```

> Even if the container cannot be found, 200 is returned as long as `removeData=false`. If `removeData=true` and the container no longer exists, directory cleanup is still performed (best-effort).

---

## Data Models

### CreateAgentRequest

```jsonc
{
  "rootAgentId": "coder",          // string, required; entry agent id == deployment name
  "agents": [ /* AgentDefinition[] */ ],
  "provider": ProviderConfig,       // runtime-global; written to the root entry only
  "force": false,                   // boolean
  "runtime_token": "must-be-set"    // string, required, used as the runtime token
}
```

Graph-level validation (all failures return 400 with an explicit diagnostic):

- `rootAgentId` is required, must match `[a-z0-9-]` (already sanitized), and must have a definition in `agents`.
- Agent ids are unique across the graph (`duplicate agent id`).
- Every `subagents` reference must exist (`references unknown subagent`), must not repeat (`duplicates subagent reference`), and must not point at itself (`references itself`).
- Cycles are rejected outright (`subagent reference cycle detected`), even though the runtime truncates delegation at depth 1.
- Runtime-global fields are root-only: a non-root agent declaring `model` / `maxSessionQueries` / `permissionMode` is rejected instead of being silently ignored at mount time.
- The root requires both `model` and `systemPrompt`.
- An agent declaring `skills` must include `"user"` in its `settingSources` (per-agent skill directories are scanned at user level).
- The same tool/skill `name` across agents requires an identical `url`+`hash` declaration (`conflicting declarations`); identical declarations are shared artifacts and download once (tools) / install per agent (skills).

### AgentDefinition

One full Agent-local definition inside the deployment graph. Every entry — root or mounted — carries its own capabilities; nothing is inherited from, merged with, or fallen back to the parent.

| Field | Type | Required | Validation Rules | Description |
|---|---|---|---|---|
| `name` | string | Yes | Matches `[A-Za-z0-9._-]{1,64}`; unique across the graph | Agent id. The root's name must equal `rootAgentId` in sanitized form |
| `description` | string | Yes | Non-empty | Description of the agent's capabilities; what the parent agent's Task tool displays when mounting this agent |
| `model` | string | Root only | Required on the root; forbidden on non-root agents | Runtime-global model name, e.g. `claude-sonnet-4-6`. Mounted agents reuse the root runtime's execution environment |
| `systemPrompt` | string | Root: yes | Non-empty on the root; optional for mounted agents | System prompt. The deployer externalizes it for readability: staged as `prompts/<id>-<sha16>.md` next to `agents.yaml` (bind-mounted into the container) and referenced via the entry's `systemPromptFile` (runtime v2.4.0+ mutual-exclusion refine, relative to the config dir). Read-back restores the text, so the API shape never changes |
| `maxTurns` | int \| null | No | `null` means unlimited | Maximum conversation turns for this agent (agent-local) |
| `maxSessionQueries` | int \| null | Root only | — | Runtime-global session-level turn limit. YAML contract key introduced with runtime v2.6.0 (SDK 3.1.0 rename of `maxSessionTurns`); on v2.4.0/v2.5.0 the legacy key name applies, on v2.6.0+ only `maxSessionQueries` is honored |
| `permissionMode` | string | Root only | — | Runtime-global permission mode, e.g. `auto` |
| `tools` | string[] | No | — | Agent-local allow-list of tool names |
| `disallowedTools` | string[] | No | — | Agent-local deny-list of tool names (runtime v2.4.0+). Round-trips losslessly into agents.yaml and read-back |
| `customTools` | ToolSource[] | No | See ToolSource definition; duplicate names/local files rejected; same name across agents requires identical declaration | Tool files downloaded, hash-verified, and installed into the shared flat `<agentsDir>/tools` directory before container creation; this agent's YAML entry references its own installed paths |
| `skills` | SkillSource[] | No | See SkillSource definition; requires `"user"` in `settingSources` | Skill zips downloaded and installed per-agent under `<agentsDir>/skills/<agentId>/`; the YAML entry gets `/app/config/skills/<agentId>` injected into `extraUserSkillDirs` (user-level scan). Raw scan paths are NOT part of the deployment API — only the deployer writes `extraUserSkillDirs`, which keeps per-agent skill isolation caller-proof |
| `settingSources` | string[] | No | — | Sources that trigger the runtime to scan the skills filesystem (`"user"` / `"project"`). Empty stays empty for non-root agents; the root defaults to `["project"]` when unset |
| `datasets` | map<string,string> | No | Both `id` and `description` must be non-empty | Mapping of dataset_id to dataset_description, written into this agent's `datasets` field in agents.yaml |
| `subagents` | string[] | No | Must reference existing ids; no duplicates, no self references, no cycles | Pure id references to other entries in the same graph. Delegation depth is fixed at 1: an agent mounted as a subagent does not mount its own `subagents` |

### SkillSource

| Field | Type | Required | Validation Rules | Description |
|---|---|---|---|---|
| `name` | string | Yes | Matches `[A-Za-z0-9._-]{1,64}` | Skill directory name, also the runtime skill whitelist entry |
| `url` | string | Yes | `http(s)` with a host | Download URL of the skill zip |
| `hash` | string | Yes | 64-char hex (may carry a `sha256:` prefix) | sha256 of the zip file, used for verification and caching |

### ToolSource

Describes a single custom Tool file to download, hash-verify, and install for the agent (issue #10).

| Field | Type | Required | Validation Rules | Description |
|---|---|---|---|---|
| `name` | string | Yes | Matches `[A-Za-z0-9._-]{1,64}`, must not be `.` or `..` | Safe identifier; determines the local file name (`tools/<name><ext>`) |
| `url` | string | Yes | `http(s)` with a host | Download URL of the tool file |
| `hash` | string | Yes | 64-char hex (may carry a `sha256:` prefix) | sha256 of the file bytes, used for verification and caching |
| `fileName` | string | Yes | Extension must be `.ts` / `.mts` / `.js` / `.mjs` (case-sensitive) | Original file name. Metadata + extension source only; its directory components are never used |

Behavior: files are downloaded (max 5 MiB), stream-hash-verified, and atomically installed under the agent config mount before the replacement container is created. Any tool install failure aborts the request (HTTP 422/502) — no container is started. The runtime derives the tool name from the file's default-exported definition, not the filename. Tools carry full Node.js privileges: only Node built-ins, `@zerone-agent/agent-runtime/tools`, and `zod` are supported; dependencies are not installed.

### Migration from the inline protocol

`SubagentDefinition` (the inline five-field stub: `name`/`description`/`prompt`/`tools`/`maxTurns`) was **removed** in deployer v3.0.0. Requests carrying the legacy `"agent"` field are rejected with 400 and an explicit diagnostic — the legacy shape is never silently reinterpreted, because field loss would silently downgrade child capabilities.

Migration mapping:

| Legacy (inline) | v3.0.0 (complete graph) |
|---|---|
| `"agent": { ... }` | `"rootAgentId": "<agent.name>"` + one entry in `"agents"` carrying the same object (name must equal rootAgentId) |
| `agent.subagents[i]` stub | A full `AgentDefinition` in `agents` with `name` = stub name; `prompt` → `systemPrompt`; add `description` (was already required) |
| Parent's `subagents` stub array | `subagents: ["<stub1-name>", "<stub2-name>"]` (pure id references) |
| (not expressible before) | Child `mcpServers` / `customTools` / `skills` / `settingSources` / `datasets` / `disallowedTools` / `maxTurns` — each child now owns its full Agent-local profile |

Note: child `model` / `maxSessionQueries` / `permissionMode` remain root-only runtime-global fields (mounted agents reuse the root runtime's provider, model, credentials, cwd, and process environment).

### ProviderConfig

| Field | Type | Required | Validation Rules | Description |
|---|---|---|---|---|
| `protocol` | string | Yes | Enum: `anthropic-messages` / `openai-completions` | Provider protocol type, passed through directly as the `apiType` field of the runtime agents.yaml |
| `baseUrl` | string | Yes | Non-empty | LLM API base URL |
| `lockedApiKey` | string | Yes | Non-empty | API Key used to call the LLM (the field name is a historical artifact; it is simply the API Key) |

#### AigcConfig

Configuration for AIGC-generated synthetic content labeling (GB 45438-2025). Written into the top-level `aigc:` section of the runtime agents.yaml; the runtime injects implicit labels into responses (the `aigc` field, containing the `ContentProducer` / `ProduceID` / `ReservedCode1` signature).

| Field | Type | Required | Description |
|---|---|---|---|
| `enabled` | boolean | Yes | Whether to enable labeling. When `false` or the entire `aigc` section is omitted, the runtime does not add labels |
| `contentProducer` | string | Required when `enabled=true` | 27-character service provider code; the last 4 characters are the model/application code slot |
| `signingKey` | string | No | Label integrity signing key (SHA-256), generated and kept by the caller; if not configured, the label carries no `ReservedCode1` |
| `explicitHint` | boolean | No | Whether to include `aigcExplicitHint: true` in responses, prompting downstream UIs to display an explicit label. **Defaults to `true` when not provided**; only an explicit `false` disables it |
| `label` | enum | No | Implicit label type per GB 45438-2025: `"1"` = AI-generated, `"2"` = suspected AI-generated, `"3"` = suspected. **Defaults to `"1"` in the runtime when not provided** |
| `produceIdPrefix` | string | No | `ProduceID` prefix, concatenated as `<prefix><timestamp>-<uuid12>` to facilitate downstream content tracing. No format constraints; an empty string is equivalent to unset |
| `modelCodes` | object | No | Model name → 4-character model code mapping. On a match, replaces the last 4 characters of `contentProducer` |

Example:

```json
"aigc": {
  "enabled": true,
  "contentProducer": "001191320118MAK93FC72D10001",
  "signingKey": "<key generated and kept by the caller>",
  "label": "2",
  "produceIdPrefix": "tenant-A/",
  "modelCodes": { "glm-4.5": "0001" }
}
```

Note: `aigc` contents (including `signingKey`) never appear in any API response; when rebuilding with `force` and no `aigc` provided, the old labeling configuration is discarded.

#### HubConfig

Configuration for pushing chat records (sessions / messages) back to an agent-hub instance. Written into the top-level `hub:` section of the runtime agents.yaml; the runtime (≥ v2.1.1) pushes records via the `X-Chat-Push-Key` channel.

| Field | Type | Required | Description |
|---|---|---|---|
| `enabled` | boolean | Yes | Whether to enable chat record push. When `false` or the entire `hub` section is omitted, the runtime does not push |
| `baseUrl` | string | Required when `enabled=true` | Absolute `http`/`https` URL of the agent-hub instance (the bare API base, without a `/api/v1` suffix) |
| `chatPushKey` | string | Required when `enabled=true` | Shared secret for the push channel, identical to the hub-side `CHAT_PUSH_API_KEY` env |
| `org` | string | No | Deployment-level tenant for chat-record push. Written verbatim into the runtime agents.yaml `hub.org`. The deployer neither derives it from client request headers nor fills in a default — the value must come from the hub's deploy request; when omitted, the hub resolves the default tenant by deploy mode |

Example:

```json
"hub": {
  "enabled": true,
  "baseUrl": "http://agent-hub:8080",
  "chatPushKey": "<same value as the hub-side CHAT_PUSH_API_KEY>",
  "org": "tenant-a"
}
```

Note: `chatPushKey` is a shared secret — it is never logged and never appears in any API response; it only lands in the runtime agents.yaml on disk. Requests with `enabled=true` but a missing/invalid `baseUrl` or `chatPushKey` are rejected with 400 before any container is created (otherwise the runtime would crash at startup). `org` is passed through with no content validation — if org format constraints are needed, they belong at the hub's trusted deployment boundary. When rebuilding with `force`, the `hub` section is rewritten from the new request — a rebuild without `hub` (or without `org`) discards the old push configuration / tenant; a rebuild under a different `org` replaces it without leaking the stale value.

### AgentResponse

The data structure returned by `POST /agents`, `GET /agents/:name`, and `GET /agents`.

| Field | Type | Always Present | Description |
|---|---|---|---|
| `agentName` | string | Yes | |
| `instanceId` | string | Yes | |
| `containerId` | string | Yes | |
| `containerName` | string | Yes | |
| `status` | AgentStatus | Yes | |
| `hostPort` | int | Yes | |
| `createdAt` | string | `POST` only | RFC3339 UTC |
| `yamlPath` | string | `POST` only | Path to the complete agent graph YAML |
| `sessionDir` | string | `POST` only | |
| `skillsDir` | string | `POST` only, and only when any agent in the graph declares skills | Host-side root of per-agent skill directories (`<dataDir>/<name>/agents/skills`) |
| `containerSkillsDir` | string | `POST` only, together with `skillsDir` | In-container root (`/app/config/skills`); skills reach the runtime via the `/app/config` bind mount |
| `toolsDir` | string | `POST` only, and only when custom tools are declared | Host-side shared flat tool directory (`<dataDir>/<name>/agents/tools`) |
| `runtimeToken` | string | `POST` only | Token provided by the caller, identical to the `ZERONE_AGENT_HTTP_API_KEY` injected into the container; not returned by Get / List |

### AgentStatus

Status enum (string):

| Value | Meaning |
|---|---|
| `running` | Running |
| `stopped` | Stopped |
| `exited` | Exited |
| `not_found` | Not found |
| `unknown` | Unknown Docker state |

> The `status` field of `GET /agents/:name/status` returns the native Docker state (e.g. `created`), not normalized.

---

## Provider Field Mapping

At creation time, `provider` credentials and the root's `model` are written into the **root entry only** of the runtime `agents.yaml` (not into environment variables). These are runtime-global fields: mounted agents reuse the root runtime's provider, model, credentials, cwd, and process environment, and never receive their own copy:

| Request Field | agents.yaml Field | Description |
|---|---|---|
| `provider.protocol` | `apiType` | Enum values match the runtime, passed through directly |
| `provider.baseUrl` | `baseURL` | LLM API base URL |
| `provider.lockedApiKey` | `apiKey` | Written to disk in plaintext under dataDir with mode 0644 (comparable in risk to env vars visible via `docker inspect`) |

The only `ZERONE_AGENT_*` environment variable injected into the container is `ZERONE_AGENT_HTTP_API_KEY` (from the request's `runtime_token`), used for the runtime's own HTTP API authentication; it is unrelated to model credentials.

---

## Typical Workflow

A complete "create → wait for readiness → use → destroy" workflow:

```bash
# 1. Create
RESP=$(curl -s -X POST "$DEPLOYER/agents" \
  -H 'Content-Type: application/json' \
  ${API_KEY:+-H "Authorization: Bearer $API_KEY"} \
  -d '{ "rootAgentId": "coder", "agents": [ { "name": "coder", "description": "...", "model": "...", "systemPrompt": "..." } ], "provider": { ... }, "runtime_token": "..." }')

# 2. Poll health status until healthy
while :; do
  HEALTH=$(curl -s "$DEPLOYER/agents/coder/status" \
    ${API_KEY:+-H "Authorization: Bearer $API_KEY"} \
    | jq -r '.data.health')
  [ "$HEALTH" = "healthy" ] && break
  [ "$HEALTH" = "unhealthy" ] && { echo "startup failed"; exit 1; }
  sleep 2
done

# 3. Get the runtime port and talk to the agent directly through it (protocol determined by open-agent-runtime)
PORT=$(echo "$RESP" | jq -r '.data.hostPort')

# 4. Stop / restart (as needed)
curl -X POST "$DEPLOYER/agents/coder/stop"    ${API_KEY:+-H "Authorization: Bearer $API_KEY"}
curl -X POST "$DEPLOYER/agents/coder/restart" ${API_KEY:+-H "Authorization: Bearer $API_KEY"}

# 5. Check logs to troubleshoot
curl "$DEPLOYER/agents/coder/logs?tail=500" ${API_KEY:+-H "Authorization: Bearer $API_KEY"}

# 6. Full destruction
curl -X DELETE "$DEPLOYER/agents/coder?removeData=true" ${API_KEY:+-H "Authorization: Bearer $API_KEY"}
```

---

## Troubleshooting

| Symptom | What to Check |
|---|---|
| Create returns 400 `invalid request` | Check `rootAgentId` + `agents[]`, the root entry's `model`/`systemPrompt`, and `provider.protocol/baseUrl/lockedApiKey`; graph references must resolve with no duplicates/self references/cycles; agents declaring `skills` need `"user"` in `settingSources`; `model`/`maxSessionQueries`/`permissionMode` are root-only |
| Create returns 400 mentioning `legacy request shape` | The request uses the removed inline `"agent"` field — migrate to `rootAgentId` + `agents` (see Migration from the inline protocol) |
| Create returns 503 `runtime image incompatible` | Pin `AGENT_DEPLOYER_RUNTIME_IMAGE` to a v2.4.0+ tag, or set `AGENT_DEPLOYER_RUNTIME_IMAGE_ASSUME_LATEST=true` when `:latest` is known to be v2.4.0+ |
| Create returns 500 `find existing container` | Check whether the deployer container can access `/var/run/docker.sock` |
| `health` stays `starting` for a long time after creation | Check `GET /logs`; usually the agents.yaml `apiKey` is invalid or `baseUrl` is unreachable (see the "Provider Field Mapping" section for field origins) |
| `health=unhealthy` | Runtime image health check failed; check the logs for the specific error |
| 401 `unauthorized` | The server has `AGENT_DEPLOYER_API_KEY` enabled, but the request is missing the key or the key does not match |
| 404 `agent not found` | Misspelled name, or the agent was fully deleted (container + data); archived agents (container gone, data kept) return 200 with `status=archived` |
| `hostPort` changed | Rebuilding a container reallocates the port; always query it dynamically each time |
