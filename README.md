<div align="center">

# Zerone Agent Deployer

**Lifecycle manager for [agent-runtime](https://github.com/zerone-agents/agent-runtime) Docker containers.**<br/>
Expose a REST API to create, inspect, stop, restart, and delete agent containers —
one instance per deployment name (`rootAgentId`), with the right config, sessions, and skills mounted in.

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow)](./LICENSE)
[![GitHub stars](https://img.shields.io/github/stars/zerone-agents/agent-deployer?style=flat)](https://github.com/zerone-agents/agent-deployer/stargazers)
[![Go](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![Docker](https://img.shields.io/badge/Docker-required-2496ED?logo=docker&logoColor=white)](https://www.docker.com/)

[Quick Start](#quick-start) · [API](#api-reference) · [Configuration](#environment-variables) · [License](#license)

**English | [简体中文](README.zh-CN.md)**

</div>

---

## What is Zerone Agent Deployer?

Zerone Agent Deployer is the orchestration layer for AI agent containers — a small Go HTTP service that talks to the host Docker daemon and manages one container per declared agent. It serializes the request payload into `agents.yaml` (the contract consumed by [`agent-runtime`](https://github.com/zerone-agents/agent-runtime)), downloads and verifies declared skill archives, and bind-mounts the right config / sessions / skills paths into each container.

**Single-instance enforcement · archive-based skills · full audit via Docker labels.**

## How it works

```
┌──────────────┐   HTTP    ┌──────────────────────┐   Docker API   ┌─────────────────┐
│  middle-     │ ────────▶ │  agent-deployer      │ ─────────────▶ │  Docker Engine  │
│  ground /    │           │  (this service)      │                │  (host daemon)  │
│  any client  │ ◀──────── │                      │ ◀───────────── │                  │
└──────────────┘   JSON    └──────────┬───────────┘                └─────────────────┘
                                       │ bind mount                       ▲
                                       ▼ $AGENT_DEPLOYER_DATA_DIR           │ bind mount
                              ┌────────────────────┐                     │ same paths
                              │  /var/lib/         │ ┐                   │
                              │  agent-deployer/   │ │ docker.sock       │
                              │   ├── agents/      │ │                   │
                              │   ├── sessions/    │ ┘                   │
                              │   └── skills/      │ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ┘
                              └────────────────────┘
```

- The deployer itself runs in a container and controls the **host Docker daemon** via `/var/run/docker.sock`.
- Each managed runtime container receives:
  - `agents/<name>/agents.yaml` → bind-mounted to `/app/config` (read by the runtime image's default CMD via `--config /app/config`)
  - `sessions/<name>/` → bind-mounted to `/root/.agents` (session persistence)
  - `agents/<name>/skills/<agentId>/` → reaches the container through the same `/app/config` bind mount (per-agent directory; no docker cp)
- The `provider` credentials are written into the runtime `agents.yaml` (root entry only; runtime-global).
- Singleton enforcement uses Docker labels (`agent-deployer/managed`, `agent-deployer/agent.name`). Each create also stamps a unique `agent-deployer/agent.instance-id` for future HA scenarios.

## Quick Start

```bash
# 1. Build and run via docker compose
export AGENT_DEPLOYER_DATA_DIR=/var/lib/agent-deployer
docker compose up -d --build

# 2. Create an agent (complete agent graph; rootAgentId doubles as the name)
curl -X POST http://localhost:8080/api/v1/agents \
  -H 'Content-Type: application/json' \
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
        "settingSources": ["user"],
        "skills": [
          {
            "name": "code-review",
            "url": "https://example.com/skills/code-review.zip",
            "hash": "sha256:535c085bbb8d31d9d3ea2e0a1eb1f0eb22fa5b6ddc46b67ebc9433a7d35d1d4f"
          }
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
    "force": false
  }'

# 3. Inspect / operate on the agent
curl http://localhost:8080/api/v1/agents/coder
curl -X POST http://localhost:8080/api/v1/agents/coder/stop
curl -X POST http://localhost:8080/api/v1/agents/coder/restart
curl -X DELETE 'http://localhost:8080/api/v1/agents/coder?removeData=true'
```

## Environment variables

All variables are read by the deployer on startup.

| Variable | Required | Default | Description |
|---|---|---|---|
| `AGENT_DEPLOYER_DATA_DIR` | **yes** | — | Host-side root for persistent state. Must be absolute. Bind-mounted into both the deployer and each runtime container at the same path. |
| `AGENT_DEPLOYER_PORT` | no | `8080` | Port the deployer HTTP server listens on (inside the container). |
| `AGENT_DEPLOYER_RUNTIME_IMAGE` | no | `open-agent-runtime:latest` | The fixed image used for every managed runtime container. The agent graph protocol requires runtime **v2.4.0+**; pin a `v2.4.0+` tag or set `AGENT_DEPLOYER_RUNTIME_IMAGE_ASSUME_LATEST=true` for a verified `:latest`. |
| `AGENT_DEPLOYER_RUNTIME_IMAGE_ASSUME_LATEST` | no | `false` | Allows deploying the agent graph protocol to a `:latest` (or untagged) runtime image. Only set when `:latest` is known to point at a v2.4.0+ build; otherwise graph deployments fail with 503 instead of silently degrading. |
| `AGENT_DEPLOYER_RUNTIME_CONTAINER_PORT` | no | `3000` | Port the runtime image listens on inside its container. The deployer maps this to a dynamically assigned host port. |
| `AGENT_DEPLOYER_API_KEY` | no | — | When set, all `/api/v1/*` endpoints require authentication. Clients must send `Authorization: Bearer <key>` or `X-API-Key: <key>`. When empty, auth is disabled (local dev only). |
| `AGENT_DEPLOYER_CONTAINER_CPUS` | no | `2` | CPU cores allocated to each runtime container (e.g. `1.5`). Set to `0` for unlimited. |
| `AGENT_DEPLOYER_CONTAINER_MEMORY` | no | `2048` | Memory limit per runtime container in MB. Set to `0` for unlimited. |

The docker-compose deployment additionally accepts:

| Variable | Default | Description |
|---|---|---|
| `AGENT_DEPLOYER_HOST_PORT` | `8080` | Host port to publish for the deployer HTTP server. |

## API reference

All endpoints are under `/api/v1`. Responses use a standard envelope:

```jsonc
// success
{ "success": true, "data": { ... } }
// error
{ "success": false, "error": "human-readable message" }
```

| Method | Path | Body / Query | Status codes | Description |
|---|---|---|---|---|
| `POST` | `/agents` | `CreateAgentRequest` | 200 / 201 / 400 / 422 / 500 / 502 / 503 | Deploy a complete agent graph (or idempotently return an existing container). Set `force: true` to stop+remove the old container and recreate with the new config; the session volume is preserved. |
| `GET` | `/agents` | `?includeArchived=true` | 200 / 500 | List managed agent containers. By default only active containers are returned; pass `includeArchived=true` to also include agents whose container was deleted but whose data remains on disk (`status: "archived"`). |
| `GET` | `/agents/:name` | — | 200 / 404 / 500 | Inspect a single agent. Returns archived agents (`status: "archived"`) as long as their on-disk data still exists. |
| `POST` | `/agents/:name/stop` | — | 200 / 404 / 500 | Stop the container (graceful). |
| `POST` | `/agents/:name/restart` | — | 200 / 404 / 500 | Restart the container. |
| `DELETE` | `/agents/:name` | `?purge=true` | 200 / 500 | Delete the container and **archive** the agent: the per-agent data on disk is preserved and the agent becomes discoverable via `GET /agents?includeArchived=true`. Pass `?purge=true` to also remove the on-disk data. Idempotent. |

### Request body for `POST /agents`

Deploys a **complete agent graph** (issue #16): `rootAgentId` names the entry agent and doubles as the deployment name; `agents` carries the full Agent-local definition of every agent in the closure. `subagents` are pure id references — mounted agents never inherit parent capabilities, and an empty field stays empty. The legacy inline shape (`"agent"` field) is rejected with 400.

```jsonc
{
  "rootAgentId": "coder",                 // required; entry agent id == deployment name [a-z0-9-]
  "agents": [                             // required: full deployment closure
    {
      "name": "coder",                    // agent id; root's name == rootAgentId
      "description": "Writes and edits code", // required on every entry
      "model": "claude-sonnet-4-6",       // root-only (runtime-global); forbidden on non-root agents
      "systemPrompt": "You are a coder.", // required on the root
      "maxTurns": null,                   // agent-local; null = unlimited
      "permissionMode": "auto",           // root-only (runtime-global)
      "tools": ["Task"],                  // agent-local allow-list
      "subagents": ["reviewer"]           // pure id references; no cycles/self/duplicates
    },
    {
      "name": "reviewer",
      "description": "Reviews code",
      "systemPrompt": "You are a reviewer.",
      "tools": ["Read"],
      "disallowedTools": ["Bash"],        // agent-local deny-list (runtime v2.4.0+)
      "skills": [                         // per-agent skill zips; requires "user" in settingSources
        {
          "name": "code-review",          // must match [A-Za-z0-9._-]{1,64}
          "url": "https://example.com/skills/code-review.zip",
          "hash": "sha256:535c085bbb8d31d9d3ea2e0a1eb1f0eb22fa5b6ddc46b67ebc9433a7d35d1d4f"
        }
      ],
      "settingSources": ["user"],         // root defaults to ["project"] when omitted; empty stays empty for children
      "datasets": {                       // dataset_id -> description, written to this agent's agents.yaml entry
        "dataset-1": "Primary dataset for code review"
      }
    }
  ],
  "provider": {                           // runtime-global; written to the root entry only
    "protocol": "anthropic-messages",     // "anthropic-messages" | "openai-completions"
    "baseUrl": "https://api.anthropic.com",
    "lockedApiKey": "sk-ant-xxx"
  },
  "force": false
}
```

### Provider → agents.yaml field mapping

At create time, `provider` credentials and the root's `model` are written into the runtime `agents.yaml` **root entry only** (not environment variables). These are runtime-global fields: mounted agents reuse the root runtime's provider, model, credentials, cwd, and process environment.

| Request field | agents.yaml field | Notes |
|---|---|---|
| `provider.protocol` | `apiType` | Enum value matches the runtime, passed through as-is |
| `provider.baseUrl` | `baseURL` | LLM API base URL |
| `provider.lockedApiKey` | `apiKey` | Written in plaintext to dataDir with 0644 permissions (equivalent risk surface to env vars visible via `docker inspect`) |

The only `ZERONE_AGENT_*` environment variable injected into the container is `ZERONE_AGENT_HTTP_API_KEY` (from the request's `runtime_token`), used for the runtime's own HTTP API auth — unrelated to model credentials. The `runtime_token` is supplied by the caller in the Create request; the deployer does NOT generate or persist it, so clients must manage and rotate it themselves.

## Skills

Each agent's `skills` and `settingSources` work together:

- An agent's `skills` is a list of skill zip archives to download. Each is downloaded, hash-verified, and extracted into **that agent's own** skills directory (`$DATA_DIR/<agentName>/agents/skills/<agentId>/<skill>/`) before the runtime container starts.
- Skills reach the container through the existing `/app/config` bind mount (**no docker cp**). The agent's `agents.yaml` entry automatically gets `extraUserSkillDirs: ["/app/config/skills/<agentId>"]` injected, pairing with user-level scanning for per-agent visibility isolation — agents never see each other's skills.
- `settingSources` tells the runtime which filesystem locations to scan for skills. **An agent declaring `skills` must include `"user"` in its `settingSources`** (otherwise 400); the root defaults to `["project"]` when omitted, and empty stays empty for non-root agents (no inheritance).

The generated `agents.yaml` does **not** include a `skills` whitelist — the runtime auto-discovers all skills under the scanned directories.

| Field | Description |
|---|---|
| `name` | Skill directory name. Must match `[A-Za-z0-9._-]{1,64}`. |
| `url`  | `http(s)` URL of the zip archive. |
| `hash` | sha256 of the zip bytes (64 lowercase hex, optional `sha256:` prefix). |

| Request field | Runtime `agents.yaml` field | Meaning |
|---|---|---|
| `agents[].skills[].name` | _(not written — runtime auto-discovers)_ | Download target |
| `agents[].settingSources` | `settingSources: [<source>, ...]` | That agent's filesystem scan sources |
| `agents[].skills` (when declared) | `extraUserSkillDirs: ["/app/config/skills/<agentId>"]` | Auto-injected per-agent skills directory |
| `agents[].datasets` | `datasets: {<id>: <description>, ...}` | Optional dataset catalog written to that agent's own entry |

The deployer caches each skill by hash: a subsequent Create with the same hash skips download. A different hash triggers delete + re-download. The downloaded bytes are verified against `hash` to detect tampering or corruption. Zip slip paths, oversize entries, and excessive file counts are rejected. The installer does not inspect the archive's layout: the zip is extracted verbatim into `skills/<name>/`, preserving whatever structure the publisher shipped.

Limits (hard-coded): zip ≤ 100 MB, single entry ≤ 50 MB, total extracted ≤ 200 MB, ≤ 1000 entries, download timeout 60 s.

## Development

Requirements: Go 1.25+ (or whatever `go.mod` declares), Docker for integration tests.

```bash
# Run the server locally (requires Docker daemon access)
export AGENT_DEPLOYER_DATA_DIR="$PWD/.data"
go run ./cmd/server

# Run unit tests
go test ./internal/...

# Run a specific package with verbose output
go test ./internal/service -v

# Build a static binary
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o agent-deployer ./cmd/server

# Integration tests require a running Docker daemon and the runtime image
# (open-agent-runtime:v2.4.0 by default — the graph protocol requires
# v2.4.0+; a stand-in tag works for topology assertions) already present
# locally. They are skipped by default and only run with the integration tag.
go test -tags=integration ./tests/integration/... -v -timeout 5m
```

## Project layout

```
cmd/server/main.go              # HTTP server bootstrap
internal/config/                # Environment-variable based configuration
internal/model/                 # Request/response DTOs and validation
internal/naming/                # Name sanitization and container/dir naming
internal/storage/               # YAML persistence + directory helpers
internal/docker/                # Docker SDK wrapper (RuntimeContainer DTO)
internal/service/               # Business logic orchestrating docker + storage
internal/handler/               # Gin HTTP handlers under /api/v1
docker-compose.yaml             # Production-style deployment
Dockerfile                      # Multi-stage build for the deployer image
```

## Design notes

- **Singleton key**: the deployment name (`rootAgentId`). The deployer requires it to already be in sanitized form (lowercase, `[a-z0-9-]`) and uses it consistently for Docker labels, filesystem paths, and the YAML root entry's `id`/`name`. `POST /agents` is idempotent for the same name unless `force: true` is sent.
- **Instance IDs**: every container gets a short random instance ID label (`agent-deployer/agent.instance-id`) so future HA / multi-instance modes can coexist with the current single-instance-per-name model.
- **Persistence**: all state lives under `$AGENT_DEPLOYER_DATA_DIR` on the host and is bind-mounted into both the deployer container and each runtime container at identical paths. No named Docker volumes are used.
- **Delete is idempotent**: `DELETE /agents/:name` returns 200 even if the container is already gone. The default behavior archives the agent (container removed, data preserved) so it can still be discovered via `GET /agents?includeArchived=true`. Use `?purge=true` to also remove the on-disk agent and session directories.

## License

[MIT](./LICENSE) © zerone-agents
