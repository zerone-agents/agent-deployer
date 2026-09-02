<div align="center">

# Zerone Agent Deployer

**[agent-runtime](https://github.com/zerone-agents/agent-runtime) Docker 容器的生命周期管理器。**<br/>
通过 REST API 创建、查询、停止、重启和删除 agent 容器——
每个 `agent.name` 对应唯一实例，并挂载正确的 config / sessions / skills。

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow)](./LICENSE)
[![GitHub stars](https://img.shields.io/github/stars/zerone-agents/agent-deployer?style=flat)](https://github.com/zerone-agents/agent-deployer/stargazers)
[![Go](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![Docker](https://img.shields.io/badge/Docker-必需-2496ED?logo=docker&logoColor=white)](https://www.docker.com/)

[快速开始](#快速开始) · [API](#api-参考) · [配置](#环境变量) · [许可证](#许可证)

**[English](README.md) | 简体中文**

</div>

---

## Zerone Agent Deployer 是什么？

Zerone Agent Deployer 是 AI agent 容器的编排层 —— 一个轻量 Go HTTP 服务，对接宿主机的 Docker daemon，为每个声明的 agent 维护一个独立容器。它把请求体序列化为 [`agent-runtime`](https://github.com/zerone-agents/agent-runtime) 消费的 `agents.yaml`（双方契约），下载并校验声明的 skill 压缩包，并把正确的 config / sessions / skills 路径 bind-mount 进每个容器。

**单实例约束 · 归档式 skill 注入 · Docker label 全链路审计。**

## 工作原理

```
┌──────────────┐   HTTP    ┌──────────────────────┐   Docker API   ┌─────────────────┐
│  middle-     │ ────────▶ │  agent-deployer      │ ─────────────▶ │  Docker Engine  │
│  ground /    │           │  (本服务)            │                │  (宿主 daemon)  │
│  任意客户端   │ ◀──────── │                      │ ◀───────────── │                  │
└──────────────┘   JSON    └──────────┬───────────┘                └─────────────────┘
                                       │ bind mount                       ▲
                                       ▼ $AGENT_DEPLOYER_DATA_DIR           │ bind mount
                              ┌────────────────────┐                     │ 同一路径
                              │  /var/lib/         │ ┐                   │
                              │  agent-deployer/   │ │ docker.sock       │
                              │   ├── agents/      │ │                   │
                              │   ├── sessions/    │ ┘                   │
                              │   └── skills/      │ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ┘
                              └────────────────────┘
```

- deployer 自身运行在容器里，通过 `/var/run/docker.sock` 控制**宿主机 Docker daemon**。
- 每个被管理的 runtime 容器会收到：
  - `agents/<name>/agents.yaml` → bind-mount 到 `/app/config`（runtime 镜像默认 CMD 通过 `--config /app/config` 读取）
  - `sessions/<name>/` → bind-mount 到 `/root/.agents`（会话持久化）
  - `agents/<name>/skills/<agentId>/` → 同样经 `/app/config` bind-mount 进容器（每 agent 独立目录，无 docker cp）
- 请求中的 `provider` 凭证会写入 runtime `agents.yaml` 的 root entry（runtime-global，仅 root 条目）。
- 单实例约束通过 Docker label 实现（`agent-deployer/managed`、`agent-deployer/agent.name`）。每次 create 还会打一个唯一的 `agent-deployer/agent.instance-id`，为未来 HA 场景预留。

## 快速开始

```bash
# 1. 通过 docker compose 构建并运行
export AGENT_DEPLOYER_DATA_DIR=/var/lib/agent-deployer
# Agent 图协议要求 runtime v2.4.0+。请 pin 到 v2.4.0+ tag...
export AGENT_DEPLOYER_RUNTIME_IMAGE=open-agent-runtime:v2.4.0
# ...或者，如果你运行 :latest 且已确认它是 v2.4.0+，显式开启豁免：
# export AGENT_DEPLOYER_RUNTIME_IMAGE_ASSUME_LATEST=true
docker compose up -d --build

# 2. 创建 agent（完整 Agent 图；rootAgentId 同时充当部署名）
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
    "force": false,
    "runtime_token": "set-your-own-runtime-token"
  }'

# 3. 查询 / 操作 agent
curl http://localhost:8080/api/v1/agents/coder
curl -X POST http://localhost:8080/api/v1/agents/coder/stop
curl -X POST http://localhost:8080/api/v1/agents/coder/restart
curl -X DELETE 'http://localhost:8080/api/v1/agents/coder?removeData=true'
```

## 环境变量

所有变量在 deployer 启动时读取。

| 变量 | 必填 | 默认值 | 说明 |
|---|---|---|---|
| `AGENT_DEPLOYER_DATA_DIR` | **是** | — | 宿主机侧持久化状态根目录。必须为绝对路径。会以相同路径 bind-mount 进 deployer 和每个 runtime 容器。 |
| `AGENT_DEPLOYER_PORT` | 否 | `8080` | deployer HTTP server 监听端口（容器内）。 |
| `AGENT_DEPLOYER_RUNTIME_IMAGE` | 否 | `open-agent-runtime:latest` | 所有 runtime 容器使用的固定镜像。Agent 图协议要求 runtime **v2.4.0+**：请 pin 到 `v2.4.0+` tag，或对已确认版本的 `:latest` 设置 `AGENT_DEPLOYER_RUNTIME_IMAGE_ASSUME_LATEST=true`。 |
| `AGENT_DEPLOYER_RUNTIME_IMAGE_ASSUME_LATEST` | 否 | `false` | 允许向 `:latest`（或无 tag）runtime 镜像部署 Agent 图。仅当 `:latest` 已确认指向 v2.4.0+ 构建时才设置；否则图部署会以 503 显式失败，而非静默降级。 |
| `AGENT_DEPLOYER_RUNTIME_CONTAINER_PORT` | 否 | `3000` | runtime 镜像在容器内监听的端口。deployer 会把它映射到动态分配的宿主端口。 |
| `AGENT_DEPLOYER_API_KEY` | 否 | — | 设置后所有 `/api/v1/*` 端点都需要认证。客户端需在请求中带 `Authorization: Bearer <key>` 或 `X-API-Key: <key>`。未设置时认证关闭（仅本地开发使用）。 |
| `AGENT_DEPLOYER_CONTAINER_CPUS` | 否 | `2` | 每个 runtime 容器分配的 CPU 核数（如 `1.5`）。设为 `0` 表示不限。 |
| `AGENT_DEPLOYER_CONTAINER_MEMORY` | 否 | `2048` | 每个 runtime 容器的内存上限（MB）。设为 `0` 表示不限。 |

docker-compose 部署方式还额外接受：

| 变量 | 默认值 | 说明 |
|---|---|---|
| `AGENT_DEPLOYER_HOST_PORT` | `8080` | deployer HTTP server 对外暴露的宿主端口。 |

## API 参考

所有端点位于 `/api/v1` 下。响应使用统一封装：

```jsonc
// 成功
{ "success": true, "data": { ... } }
// 失败
{ "success": false, "error": "人类可读的错误信息" }
```

| 方法 | 路径 | Body / Query | 状态码 | 说明 |
|---|---|---|---|---|
| `POST` | `/agents` | `CreateAgentRequest` | 200 / 201 / 400 / 422 / 500 / 502 / 503 | 部署完整 Agent 图（或幂等返回已存在容器）。传 `force: true` 会停止+删除旧容器并以新配置重建；session 卷保留。 |
| `GET` | `/agents` | `?includeArchived=true` | 200 / 500 | 列出受管的 agent 容器。默认只返回活跃容器；传 `includeArchived=true` 也返回容器已删除但磁盘数据尚存的 agent（`status: "archived"`）。 |
| `GET` | `/agents/:name` | — | 200 / 404 / 500 | 查询单个 agent。只要磁盘数据尚存，就返回 archived 的 agent（`status: "archived"`）。 |
| `POST` | `/agents/:name/stop` | — | 200 / 404 / 500 | 优雅停止容器。 |
| `POST` | `/agents/:name/restart` | — | 200 / 404 / 500 | 重启容器。 |
| `DELETE` | `/agents/:name` | `?purge=true` | 200 / 500 | 删除容器并**归档** agent：磁盘上的 agent 数据保留，可通过 `GET /agents?includeArchived=true` 发现。传 `?purge=true` 同时删除磁盘数据。幂等。 |

### `POST /agents` 请求体

部署**完整 Agent 图**（issue #16）：`rootAgentId` 指定入口 agent 并同时充当部署名；`agents` 携带部署闭包内每个 agent 的完整 Agent-local 定义。`subagents` 是纯 id 引用 —— 被挂载的 agent 不继承父级任何能力，空字段保持为空。旧的 inline 形态（`"agent"` 字段）会返回 400 显式拒绝。

```jsonc
{
  "rootAgentId": "coder",                 // 必填；入口 agent id == 部署名 [a-z0-9-]
  "agents": [                             // 必填：完整部署闭包
    {
      "name": "coder",                    // agent id；root 的 name == rootAgentId
      "description": "Writes and edits code", // 每个 entry 必填
      "model": "claude-sonnet-4-6",       // 仅 root 可声明（runtime-global）；非 root 声明会被拒绝
      "systemPrompt": "You are a coder.", // root 必填
      "maxTurns": null,                   // agent-local；null = 不限
      "permissionMode": "auto",           // 仅 root（runtime-global）
      "tools": ["Task"],                  // agent-local 允许列表
      "subagents": ["reviewer"]           // 纯 id 引用；禁止环/自引用/重复引用
    },
    {
      "name": "reviewer",
      "description": "Reviews code",
      "systemPrompt": "You are a reviewer.",
      "tools": ["Read"],
      "disallowedTools": ["Bash"],        // agent-local 拒绝列表（runtime v2.4.0+）
      "skills": [                         // 每 agent 独立的 skill zip；要求 settingSources 含 "user"
        {
          "name": "code-review",          // 必须匹配 [A-Za-z0-9._-]{1,64}
          "url": "https://example.com/skills/code-review.zip",
          "hash": "sha256:535c085bbb8d31d9d3ea2e0a1eb1f0eb22fa5b6ddc46b67ebc9433a7d35d1d4f"
        }
      ],
      "settingSources": ["user"],         // root 省略时默认 ["project"]；非 root 空即空，不继承
      "datasets": {                       // dataset_id -> 描述，写入该 agent 自己的 agents.yaml entry
        "dataset-1": "Primary dataset for code review"
      }
    }
  ],
  "provider": {                           // runtime-global；仅写入 root entry
    "protocol": "anthropic-messages",     // "anthropic-messages" | "openai-completions"
    "baseUrl": "https://api.anthropic.com",
    "lockedApiKey": "sk-ant-xxx"
  },
  "force": false,
  "runtime_token": "must-be-set"          // 必填；注入为 ZERONE_AGENT_HTTP_API_KEY，仅 Create 响应返回一次
}
```

### Provider → agents.yaml 字段映射

创建时,`provider` 凭证与 root 的 `model` 会写入 runtime `agents.yaml` 的 **root entry**（而非环境变量）。这些是 runtime-global 字段：被挂载的子 agent 复用 root runtime 的 provider、model、凭证、cwd 与进程环境，不会得到自己的副本:

| 请求字段 | agents.yaml 字段 | 说明 |
|---|---|---|
| `provider.protocol` | `apiType` | 枚举值与 runtime 一致,直接透传 |
| `provider.baseUrl` | `baseURL` | LLM API 基础 URL |
| `provider.lockedApiKey` | `apiKey` | 明文落盘于 dataDir,权限 0644(与 `docker inspect` 可见 env 风险面相当) |

容器内唯一注入的 `ZERONE_AGENT_*` 环境变量是 `ZERONE_AGENT_HTTP_API_KEY`(来自请求的 `runtime_token`),用于 runtime 自身 HTTP API 鉴权,与 model 凭证无关。`runtime_token` 由调用方在 Create 请求中提供,deployer **不会**生成或持久化它,客户端需自行管理与轮转。

为提高可读性,system prompt 默认外挂存储:每个 agent 的 `systemPrompt` 写入 `agents/<name>/agents/prompts/<id>-<sha16>.md`(与 `agents.yaml` 同目录、同一 bind mount),YAML entry 通过 `systemPromptFile` 引用(相对 config 目录,runtime v2.4.0+)——长 prompt 不再平铺在 `agents.yaml` 中。读回时解析文件恢复为响应中的 `systemPrompt` 文本,API 形态不变。

## Skills

图内各 agent 的 `skills` 与 `settingSources` 协同工作：

- 每个 agent 的 `skills` 是一组待下载的 skill zip 包。每个包会先下载、按 hash 校验，再解压到**该 agent 自己的** skill 目录（`$DATA_DIR/<agentName>/agents/skills/<agentId>/<skill>/`），整个过程发生在 runtime 容器启动**之前**。
- skills 经既有的 `/app/config` bind-mount 进入容器（**没有 docker cp**）。生成的 `agents.yaml` 中该 agent 的 entry 会自动注入 `extraUserSkillDirs: ["/app/config/skills/<agentId>"]`，配合 user 级扫描实现 per-agent 可见性隔离 —— agent 之间互不可见对方的 skill。
- `settingSources` 告诉 runtime 扫描哪些文件系统位置来发现 skill。**声明了 `skills` 的 agent 必须在 `settingSources` 里包含 `"user"`**（否则 400）；root 省略 `settingSources` 时默认 `["project"]`，非 root 空即空、不继承。

生成的 `agents.yaml` **不**包含 skill 白名单 —— runtime 会自动发现扫描目录下的所有 skill。

| 字段 | 说明 |
|---|---|
| `name` | Skill 目录名。必须匹配 `[A-Za-z0-9._-]{1,64}`。 |
| `url`  | zip 包的 `http(s)` URL。 |
| `hash` | zip 字节的 sha256（64 位小写十六进制，可选 `sha256:` 前缀）。 |

| 请求字段 | Runtime `agents.yaml` 字段 | 含义 |
|---|---|---|
| `agents[].skills[].name` | （不写入 —— runtime 自动发现） | 下载目标 |
| `agents[].settingSources` | `settingSources: [<source>, ...]` | 该 agent 的文件系统扫描来源 |
| `agents[].skills`（声明时） | `extraUserSkillDirs: ["/app/config/skills/<agentId>"]` | 自动注入的 per-agent skill 目录 |
| `agents[].datasets` | `datasets: {<id>: <description>, ...}` | 写入该 agent 自己 entry 的可选 dataset 目录 |

deployer 会按 hash 缓存每个 skill：相同 hash 的后续 Create 直接跳过下载；hash 不同则触发删除 + 重新下载。下载字节会与 `hash` 比对以检测篡改或损坏。Zip slip 路径、超大条目、过多文件数都会被拒绝。安装器**不检查**压缩包的目录结构 —— zip 原样解压到 `skills/<name>/`，保留发布方打包时的任意结构。

硬编码限额：zip ≤ 100 MB，单条目 ≤ 50 MB，解压总量 ≤ 200 MB，条目数 ≤ 1000，下载超时 60 秒。

## 开发

依赖：Go 1.25+（或 `go.mod` 声明的版本）、Docker（用于集成测试）。

```bash
# 本地运行 server（需 Docker daemon 可达）
export AGENT_DEPLOYER_DATA_DIR="$PWD/.data"
go run ./cmd/server

# 运行单元测试
go test ./internal/...

# 详细模式跑某个包
go test ./internal/service -v

# 构建静态二进制
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o agent-deployer ./cmd/server

# 集成测试需要运行中的 Docker daemon，以及本地已存在的 runtime 镜像
# （默认 open-agent-runtime:v2.4.0，图协议要求 v2.4.0+；可用替身 tag）。
# 默认跳过，仅在 integration 构建标签下运行。
go test -tags=integration ./tests/integration/... -v -timeout 5m

# 真实 runtime 验收测试（agent detail API 能力隔离断言 + 确定性 mock
# provider/MCP fixture 驱动的 parent → Task(child) 执行链路）额外需要真实
# v2.4.0+ 镜像（如 ECS 宿主机上），否则自动 skip：
CAM_INTEGRATION_REAL_RUNTIME_IMAGE=swr.cn-east-3.myhuaweicloud.com/zerone/runtime:v2.4.0 \
  go test -tags=integration ./tests/integration/... -run "TestIntegration_AgentGraphRuntimeIsolation|TestIntegration_AgentGraphTaskExecution" -v
```

## 项目结构

```
cmd/server/main.go              # HTTP server 启动入口
internal/config/                # 基于环境变量的配置
internal/model/                 # 请求/响应 DTO 与校验
internal/naming/                # 名称归一化与容器/目录命名
internal/storage/               # YAML 持久化与目录辅助
internal/docker/                # Docker SDK 封装（RuntimeContainer DTO）
internal/service/               # 业务逻辑，串联 docker + storage
internal/handler/               # /api/v1 下的 Gin HTTP handler
docker-compose.yaml             # 生产式部署
Dockerfile                      # deployer 镜像的多阶段构建
```

## 设计说明

- **单例键**：`rootAgentId`（即部署名）。deployer 要求它已经是归一化形态（小写、`[a-z0-9-]`），并在 Docker label、文件系统路径、YAML 的 root entry `id`/`name` 中统一使用。`POST /agents` 对同名幂等，除非传 `force: true`。
- **实例 ID**：每个容器都会被打上一个短的随机 instance ID label（`agent-deployer/agent.instance-id`），让未来的 HA / 多实例模式能与当前的"每名称单实例"模型共存。
- **持久化**：所有状态都位于宿主机的 `$AGENT_DEPLOYER_DATA_DIR` 下，并以相同路径 bind-mount 进 deployer 容器和每个 runtime 容器。不使用 Docker named volume。
- **删除是幂等的**：即使容器已经不存在，`DELETE /agents/:name` 也返回 200。默认行为是归档（删容器、保留数据），仍可通过 `GET /agents?includeArchived=true` 发现。要一并删除磁盘上的 agent 与 session 目录，传 `?purge=true`。

## 许可证

[MIT](./LICENSE) © zerone-agents
