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
- [Runtime Network Topologies](#runtime-network-topologies)
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
| 200 | Success | All queries; successful stop / restart / delete |
| 201 | Created | `POST /agents` successfully created a new container |
| 400 | Bad request | Request body cannot be parsed or required fields are missing |
| 401 | Unauthorized | Missing or incorrect API Key |
| 404 | Not found | No agent container exists with the given `name` |
| 500 | Server error | Docker call failure, disk write failure, etc. |

---

## Endpoints

All paths are relative to the Base URL `/api/v1`. The examples below assume a Base URL of `http://localhost:8080/api/v1` and the following:

```bash
export DEPLOYER=http://localhost:8080/api/v1
export API_KEY=<your key>   # can be omitted when authentication is disabled
```

### 1. Create Agent

Creates and starts an agent runtime container. **Agent names are unique (singleton)**; the container name is sanitized and lowercased to `[a-z0-9-]`, serving as the unique identifier across the system.

- **Idempotency**: if a container with the same name already exists and `force=false` (default), the existing container is returned directly (200 semantics, though the actual status code remains 201); with `force=true`, the old container is stopped + deleted first, then rebuilt with the new configuration (session data is preserved).
- **Method**: `POST /agents`
- **Content-Type**: `application/json`

#### Request Body

| Field | Type | Required | Description |
|---|---|---|---|
| `agent` | object | Yes | Agent configuration, see [AgentDefinition](#agentdefinition) |
| `provider` | object | Yes | LLM provider configuration, see [ProviderConfig](#providerconfig) |
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
    "agent": {
      "name": "coder",
      "description": "Writes and edits code",
      "model": "claude-sonnet-4-6",
      "systemPrompt": "You are a coding assistant.",
      "maxTurns": null,
      "permissionMode": "auto",
      "tools": ["Read", "Write", "Edit", "Bash"],
      "skills": ["code-review"],
      "datasets": {
        "dataset-1": "Primary dataset for code generation",
        "dataset-2": "Secondary dataset for testing"
      },
      "subagents": [
        {
          "name": "reviewer",
          "description": "Reviews code",
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
    "skillsDir": "/var/lib/agent-deployer/coder/skills",
    "runtimeToken": "caller-provided-runtime-token"
  }
}
```

#### Field Descriptions

| Field | Type | Description |
|---|---|---|
| `agentName` | string | Sanitized agent name (unique key) |
| `instanceId` | string | Short random ID generated at creation time, attached to the Docker label `agent-deployer/agent.instance-id`, reserved for future HA scenarios |
| `containerId` | string | Full Docker container ID |
| `containerName` | string | Docker container name, of the form `cloud-agent-<name>-<instanceId>` |
| `status` | string | Status enum, see [AgentStatus](#agentstatus) |
| `hostPort` | int | Port mapped to the host (the runtime's internal container port is fixed, controlled by `AGENT_DEPLOYER_RUNTIME_CONTAINER_PORT`, default 3000). `0` when the topology publishes no host port — a legal running state in `docker-network` mode |
| `createdAt` | string | RFC3339 UTC timestamp |
| `yamlPath` | string | Path to this agent's YAML configuration (mounted into the container at `/app/config`) |
| `sessionDir` | string | Session persistence directory (mounted into the container at `/root/.agents`) |
| `skillsDir` | string | Skills directory (returned only when skills are declared, copied into `/workdir/.agents/skills`) |
| `upstream` | object | Server-generated upstream locator, `{ "scheme": "http", "host": "<container-dns-or-ip>", "port": <port> }`. Only present when the server runs a non-public `AGENT_DEPLOYER_RUNTIME_EXPOSE` mode; derived solely from server-side topology configuration — no request field can influence it. See [Runtime Network Topologies](#runtime-network-topologies) |

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

> In non-public `AGENT_DEPLOYER_RUNTIME_EXPOSE` modes the response additionally carries a server-generated `upstream` object (same shape as in [§1](#1-create-agent), see [Runtime Network Topologies](#runtime-network-topologies)). It is absent from the example above because the default `public` mode emits no locator.

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
| `upstream` | Server-generated upstream locator, present only in non-public `AGENT_DEPLOYER_RUNTIME_EXPOSE` modes (same shape as in [§1](#1-create-agent), see [Runtime Network Topologies](#runtime-network-topologies)) |
| `upstreamReachable` | Boolean; present only when the server is configured with `AGENT_DEPLOYER_UPSTREAM_PROBE=true` — whether the deployer's TCP probe of `upstream` succeeded |

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
  "agent": AgentDefinition,
  "provider": ProviderConfig,
  "force": false,                  // boolean
  "runtime_token": "must-be-set"   // string, required, used as the runtime token
}
```

### AgentDefinition

| Field | Type | Required | Validation Rules | Description |
|---|---|---|---|---|
| `name` | string | Yes | Non-empty; sanitized to `[a-z0-9-]` | Unique key |
| `description` | string | Yes | Non-empty | Description of the agent's capabilities. Required as of runtime 2.0; it is what the parent agent's Task tool displays when mounting the subagent |
| `model` | string | Yes | Non-empty | Model name, e.g. `claude-sonnet-4-6` |
| `systemPrompt` | string | Yes | Non-empty | System prompt |
| `maxTurns` | int \| null | No | `null` means unlimited | Maximum conversation turns for the agent |
| `permissionMode` | string | No | — | Permission mode, e.g. `auto` |
| `tools` | string[] | No | — | Enabled tool names, e.g. `["Read","Write"]` |
| `skills` | SkillSource[] | No | See SkillSource definition | List of skill zips to download/install; only the name is kept as a skill whitelist entry when passed to the runtime |
| `settingSources` | string[] | No | — | Sources that trigger the runtime to scan the skills filesystem (e.g. `["user","project"]`) **; if not provided, skills are not loaded** |
| `datasets` | map<string,string> | No | Both `id` and `description` must be non-empty; duplicate `id` in JSON keeps only the last one | Mapping of dataset_id to dataset_description, written into the `datasets` field of `agents.yaml` |
| `subagents` | SubagentDefinition[] | No | Subagent names must be unique | Subagent configurations. Runtime 2.0 uses reference-based mounting: each subagent is expanded into a first-class agent entry in agents.yaml, with the main entry referencing it by id; on mount only `description`/`prompt`/`tools`/`maxTurns` take effect, while the model and credentials follow the main agent |

### SkillSource

| Field | Type | Required | Validation Rules | Description |
|---|---|---|---|---|
| `name` | string | Yes | Matches `[A-Za-z0-9._-]{1,64}` | Skill directory name, also the runtime skill whitelist entry |
| `url` | string | Yes | `http(s)` with a host | Download URL of the skill zip |
| `hash` | string | Yes | 64-char hex (may carry a `sha256:` prefix) | sha256 of the zip file, used for verification and caching |

### SubagentDefinition

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | Yes | Subagent name, must be unique within the same agent |
| `description` | string | Yes | Description |
| `prompt` | string | Yes | Prompt for the subagent |
| `tools` | string[] | No | Tools enabled for the subagent |
| `maxTurns` | int | No | Maximum turns for the subagent |

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
| `yamlPath` | string | `POST` only | |
| `sessionDir` | string | `POST` only | |
| `skillsDir` | string | `POST` only, and only when skills are declared | |
| `runtimeToken` | string | `POST` only | Token provided by the caller, identical to the `ZERONE_AGENT_HTTP_API_KEY` injected into the container; not returned by Get / List |
| `upstream` | object | Non-public `AGENT_DEPLOYER_RUNTIME_EXPOSE` modes only | Server-generated locator `{ "scheme": "http", "host": "<container-dns-or-ip>", "port": <port> }`; re-derived per request from live container state, never influenced by client input. See [Runtime Network Topologies](#runtime-network-topologies) |

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

At creation time, `provider` credentials are written into the main agent entry of the runtime `agents.yaml` (not into environment variables):

| Request Field | agents.yaml Field | Description |
|---|---|---|
| `provider.protocol` | `apiType` | Enum values match the runtime, passed through directly |
| `provider.baseUrl` | `baseURL` | LLM API base URL |
| `provider.lockedApiKey` | `apiKey` | Written to disk in plaintext under dataDir with mode 0644 (comparable in risk to env vars visible via `docker inspect`) |

The only `ZERONE_AGENT_*` environment variable injected into the container is `ZERONE_AGENT_HTTP_API_KEY` (from the request's `runtime_token`), used for the runtime's own HTTP API authentication; it is unrelated to model credentials.

---

## Runtime Network Topologies

`AGENT_DEPLOYER_RUNTIME_EXPOSE` selects how agent-runtime containers are
reached. It is server-side configuration only — clients can never influence
the resulting upstream locator.

| Mode | Port publish | Upstream locator | Required env |
|------|--------------|------------------|--------------|
| `public` (default) | dynamic port on `0.0.0.0` | none emitted | — |
| `loopback` | dynamic port on `127.0.0.1` | `http://127.0.0.1:<port>` (or `AGENT_DEPLOYER_UPSTREAM_HOST`) | — |
| `docker-network` | none | `http://<container-name>:<container-port>` via Docker DNS | `AGENT_DEPLOYER_RUNTIME_NETWORK` |
| `private` | dynamic port on `AGENT_DEPLOYER_RUNTIME_BIND_IP` | `http://<bind-ip>:<port>` (or `AGENT_DEPLOYER_UPSTREAM_HOST`) | `AGENT_DEPLOYER_RUNTIME_BIND_IP` |

Notes:
- `hostPort` in responses always reflects the host published port; `0` is a
  legal running state in `docker-network` mode.
- The locator is re-derived on every request from live container state. After
  `force` redeploy the old locator stops being returned automatically.
- Containers created before switching to `docker-network` mode are not on the
  shared network: they get **no** locator (fail closed) and must be recreated
  with `force: true`.
- The deployer refuses to start in `docker-network` mode when the configured
  network does not exist.
- Upgrade order with agent-hub: upgrade the hub to a locator-aware version
  **first**, then switch this deployer to `docker-network` mode, then
  force-redeploy existing agents (`force: true`) so they attach to the shared
  network and receive a locator, then close the runtime dynamic port range
  (32768-60999) in the host firewall/security group.

---

## Typical Workflow

A complete "create → wait for readiness → use → destroy" workflow:

```bash
# 1. Create
RESP=$(curl -s -X POST "$DEPLOYER/agents" \
  -H 'Content-Type: application/json' \
  ${API_KEY:+-H "Authorization: Bearer $API_KEY"} \
  -d '{ "agent": { ... }, "provider": { ... } }')

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
| Create returns 400 `invalid request` | Check that `agent.name/model/systemPrompt` and `provider.protocol/baseUrl/lockedApiKey` are all present; `protocol` must be `anthropic-messages` or `openai-completions` |
| Create returns 500 `find existing container` | Check whether the deployer container can access `/var/run/docker.sock` |
| `health` stays `starting` for a long time after creation | Check `GET /logs`; usually the agents.yaml `apiKey` is invalid or `baseUrl` is unreachable (see the "Provider Field Mapping" section for field origins) |
| `health=unhealthy` | Runtime image health check failed; check the logs for the specific error |
| 401 `unauthorized` | The server has `AGENT_DEPLOYER_API_KEY` enabled, but the request is missing the key or the key does not match |
| 404 `agent not found` | Misspelled name, or the container was already deleted; note that names are sanitized to lowercase |
| `hostPort` changed | Rebuilding a container reallocates the port; always query it dynamically each time |
