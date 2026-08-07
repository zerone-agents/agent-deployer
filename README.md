# agent-deployer

A service that manages the lifecycle of [open-agent-runtime](../open-agent-runtime) Docker containers.
It exposes a REST API for creating, inspecting, stopping, restarting, and deleting agent containers,
ensuring a single instance per `agent.name` and mounting the right config, sessions, and skills into
each container.

## How it works

```
┌──────────────┐   HTTP    ┌──────────────────────┐   Docker API   ┌─────────────────┐
│  middle-     │ ────────▶ │  agent-deployer │ ─────────────▶ │  Docker Engine  │
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

- The deployer itself runs in a container and controls the **host Docker daemon** via
  `/var/run/docker.sock`.
- Each managed runtime container receives:
  - `agents/<name>/agents.yaml` → bind-mounted to `/app/config` (read by the runtime
    image's default CMD via `--config /app/config`)
  - `sessions/<name>/` → bind-mounted to `/root/.agents` (session persistence)
  - `skills/` → copied into container at `/workdir/.agents/skills/` via `docker cp` (only when the agent declares skills)
- The `provider` part of the request is injected as `ZERONE_AGENT_*` environment variables.
- Singleton enforcement uses Docker labels (`agent-deployer/managed`,
  `agent-deployer/agent.name`). Each create also stamps a unique
  `agent-deployer/agent.instance-id` for future HA scenarios.

## Quick start

```bash
# 1. Build and run via docker compose
export AGENT_DEPLOYER_DATA_DIR=/var/lib/agent-deployer
docker compose up -d --build

# 2. Create an agent
curl -X POST http://localhost:8080/api/v1/agents \
  -H 'Content-Type: application/json' \
  -d '{
    "agent": {
      "name": "coder",
      "model": "claude-sonnet-4-6",
      "systemPrompt": "You are a coding assistant.",
      "maxTurns": null,
      "permissionMode": "auto",
      "tools": ["Read", "Write", "Edit", "Bash"],
      "skills": [
        {
          "name": "code-review",
          "url": "https://example.com/skills/code-review.zip",
          "hash": "sha256:535c085bbb8d31d9d3ea2e0a1eb1f0eb22fa5b6ddc46b67ebc9433a7d35d1d4f"
        }
      ],
      "settingSources": ["project", "user"],
      "datasets": {
        "dataset-1": "Primary dataset for code generation",
        "dataset-2": "Secondary dataset for testing"
      }
    },
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
| `AGENT_DEPLOYER_RUNTIME_IMAGE` | no | `open-agent-runtime:latest` | The fixed image used for every managed runtime container. |
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
| `POST` | `/agents` | `CreateAgentRequest` | 201 / 400 / 500 | Create (or idempotently return) an agent container. Set `force: true` to stop+remove the old container and recreate with the new config; the session volume is preserved. |
| `GET` | `/agents` | `?includeArchived=true` | 200 / 500 | List managed agent containers. By default only active containers are returned; pass `includeArchived=true` to also include agents whose container was deleted but whose data remains on disk (`status: "archived"`). |
| `GET` | `/agents/:name` | — | 200 / 404 / 500 | Inspect a single agent. Returns archived agents (`status: "archived"`) as long as their on-disk data still exists. |
| `POST` | `/agents/:name/stop` | — | 200 / 404 / 500 | Stop the container (graceful). |
| `POST` | `/agents/:name/restart` | — | 200 / 404 / 500 | Restart the container. |
| `DELETE` | `/agents/:name` | `?purge=true` | 200 / 500 | Delete the container and **archive** the agent: the per-agent data on disk is preserved and the agent becomes discoverable via `GET /agents?includeArchived=true`. Pass `?purge=true` to also remove the on-disk data. Idempotent. |

### Request body for `POST /agents`

```jsonc
{
  "agent": {
    "name": "coder",                       // required, singleton key
    "model": "claude-sonnet-4-6",          // required
    "systemPrompt": "You are a coder.",    // required
    "maxTurns": null,                      // null = unlimited (default)
    "permissionMode": "auto",              // optional
    "tools": ["Read", "Write", "Edit"],    // optional
    "skills": [                             // optional: skill zip archives to download and inject into the container
      {
        "name": "code-review",              // must match [A-Za-z0-9._-]{1,64}
        "url": "https://example.com/skills/code-review.zip",
        "hash": "sha256:535c085bbb8d31d9d3ea2e0a1eb1f0eb22fa5b6ddc46b67ebc9433a7d35d1d4f"
      }
    ],
    "settingSources": ["project", "user"],  // optional: runtime filesystem scan sources; defaults to ["project"] when omitted
    "datasets": {                             // optional: dataset_id -> dataset_description map, written to agents.yaml
      "dataset-1": "Primary dataset for code generation",
      "dataset-2": "Secondary dataset for testing"
    },
    "subagents": [                         // optional
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
    "protocol": "anthropic-messages",       // "anthropic-messages" | "openai-completions"
    "baseUrl": "https://api.anthropic.com",
    "lockedApiKey": "sk-ant-xxx"
  },
  "force": false
}
```

### Provider → environment variable mapping

| Request field | Runtime env var |
|---|---|
| `provider.lockedApiKey` | `ZERONE_AGENT_API_KEY` |
| `provider.baseUrl` | `ZERONE_AGENT_BASE_URL` |
| `provider.protocol` | `ZERONE_AGENT_API_TYPE` |
| `agent.model` | `ZERONE_AGENT_MODEL` |
| `runtime_token`(required) | `ZERONE_AGENT_HTTP_API_KEY` — supplied by the caller in the Create request. The deployer does NOT generate or persist it, so clients must manage and rotate it themselves |

## Skills

`agent.skills` and `agent.settingSources` work together:

- `agent.skills` is a list of skill zip archives to download. Each is
  downloaded, hash-verified, and extracted into the per-agent skills directory
  (`$DATA_DIR/<agentName>/skills/<skill>/`) before the runtime container starts.
- After container creation, skills are **copied** into the container at
  `/workdir/.agents/skills/` via `docker cp` (not bind-mounted). This gives the
  runtime its own writable project-level skills directory.
- `agent.settingSources` tells the runtime which filesystem locations to scan
  for skills (e.g. `project`, `user`). **Defaults to `["project"]` when
  omitted.** Pass an explicit list to override.

The generated `agents.yaml` does **not** include a `skills` whitelist — the
runtime auto-discovers all skills under the scanned directories.

| Field | Description |
|---|---|
| `name` | Skill directory name. Must match `[A-Za-z0-9._-]{1,64}`. |
| `url`  | `http(s)` URL of the zip archive. |
| `hash` | sha256 of the zip bytes (64 lowercase hex, optional `sha256:` prefix). |

| Request field | Runtime `agents.yaml` field | Meaning |
|---|---|---|
| `agent.skills[].name` | _(not written — runtime auto-discovers)_ | Download target |
| `agent.settingSources` | `settingSources: [<source>, ...]` | Filesystem scan sources (default: `["project"]`) |
| `agent.datasets` | `datasets: {<id>: <description>, ...}` | Optional dataset catalog written to agents.yaml |

The deployer caches each skill by hash: a subsequent Create with the same hash
skips download. A different hash triggers delete + re-download. The downloaded
bytes are verified against `hash` to detect tampering or corruption. Zip slip
paths, oversize entries, and excessive file counts are rejected. The installer
does not inspect the archive's layout: the zip is extracted verbatim into
`skills/<name>/`, preserving whatever structure the publisher shipped.

Limits (hard-coded): zip ≤ 100 MB, single entry ≤ 50 MB, total extracted ≤ 200 MB,
≤ 1000 entries, download timeout 60 s.

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
# (open-agent-runtime:latest by default) already present locally. They are
# skipped by default and only run with the integration build tag.
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

- **Singleton key**: `agent.name`. The deployer sanitizes the name (lowercase, `[a-z0-9-]`) once
  and uses that sanitized value consistently for Docker labels, filesystem paths, and the YAML
  `id`/`name`. `POST /agents` is idempotent for the same name unless `force: true` is sent.
- **Instance IDs**: every container gets a short random instance ID label
  (`agent-deployer/agent.instance-id`) so future HA / multi-instance modes can coexist with
  the current single-instance-per-name model.
- **Persistence**: all state lives under `$AGENT_DEPLOYER_DATA_DIR` on the host and is bind-mounted
  into both the deployer container and each runtime container at identical paths. No named Docker
  volumes are used.
- **Delete is idempotent**: `DELETE /agents/:name` returns 200 even if the container is already gone.
  The default behavior archives the agent (container removed, data preserved) so it can still be
  discovered via `GET /agents?includeArchived=true`. Use `?purge=true` to also remove the on-disk
  agent and session directories.
