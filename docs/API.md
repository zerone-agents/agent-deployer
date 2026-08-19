# agent-deployer 接口调用说明

agent-deployer 是一个管理 [open-agent-runtime](../open-agent-runtime) Docker 容器生命周期的服务。本文档描述其对外暴露的全部 REST API,供 middle-ground 或任何上层调用方集成使用。

## 目录

- [通用约定](#通用约定)
  - [Base URL](#base-url)
  - [认证](#认证)
  - [统一响应格式](#统一响应格式)
  - [HTTP 状态码](#http-状态码)
- [接口列表](#接口列表)
  - [1. 创建 Agent](#1-创建-agent)
  - [2. 列出全部 Agent](#2-列出全部-agent)
  - [3. 查询 Agent 详情](#3-查询-agent-详情)
  - [4. 查询实时状态](#4-查询实时状态)
  - [5. 查询容器日志](#5-查询容器日志)
  - [6. 停止 Agent](#6-停止-agent)
  - [7. 重启 Agent](#7-重启-agent)
  - [8. 删除 Agent](#8-删除-agent)
- [数据模型](#数据模型)
- [Provider 字段映射](#provider-字段映射)
- [典型调用流程](#典型调用流程)
- [错误排查](#错误排查)

---

## 通用约定

### Base URL

```
http://<host>:<port>/api/v1
```

- 容器内默认端口 `8080`(由 `AGENT_DEPLOYER_PORT` 控制)。
- docker-compose 部署时,主机发布端口由 `AGENT_DEPLOYER_HOST_PORT` 控制,默认同样为 `8080`。

### 认证

认证由服务端环境变量 `AGENT_DEPLOYER_API_KEY` 控制:

- **未设置(空)**:认证关闭,所有 `/api/v1/*` 接口可直接访问。仅建议本地开发使用。
- **已设置**:每个请求必须携带下列任一 Header,服务端使用常量时间比较(防时序攻击):

| Header | 格式 | 示例 |
|---|---|---|
| `Authorization` | `Bearer <key>` | `Authorization: Bearer my-secret-key` |
| `X-API-Key` | `<key>` | `X-API-Key: my-secret-key` |

`Authorization` 优先于 `X-API-Key`。若同时设置 Bearer 失效会直接返回 401,不会回退到 `X-API-Key`。

**认证失败响应**(HTTP 401):

```json
{ "success": false, "error": "unauthorized" }
```

### 统一响应格式

所有接口共用一个响应信封:

```jsonc
// 成功(带数据)
{ "success": true, "data": { ... } }

// 成功(无数据,如 stop / restart / delete)
{ "success": true }

// 失败
{ "success": false, "error": "人类可读的错误信息" }
```

> 例外:`GET /agents/:name/logs` 成功响应的载荷字段为 `logs`(字符串),而非 `data`,详见 [§5](#5-查询容器日志)。

### HTTP 状态码

| 状态码 | 含义 | 触发场景 |
|---|---|---|
| 200 | 成功 | 所有查询、停止 / 重启 / 删除成功 |
| 201 | 已创建 | `POST /agents` 成功创建新容器 |
| 400 | 请求非法 | 请求体无法解析或必填字段缺失 |
| 401 | 未认证 | 未携带 / 携带了错误的 API Key |
| 404 | 未找到 | 指定 `name` 的 agent 容器不存在 |
| 500 | 服务端错误 | Docker 调用失败、磁盘写入失败等 |

---

## 接口列表

所有路径均相对于 Base URL `/api/v1`。下文示例假设 Base URL 为 `http://localhost:8080/api/v1`,并已设置:

```bash
export DEPLOYER=http://localhost:8080/api/v1
export API_KEY=<你的 key>   # 未启用认证时可不导出
```

### 1. 创建 Agent

创建并启动一个 agent 运行时容器。**同名 agent 唯一(单例)**,容器名经清洗后小写化为 `[a-z0-9-]`,作为整个系统中的唯一标识。

- **幂等性**:若同名容器已存在,且 `force=false`(默认),直接返回已有容器(状态码 200 语义,实际仍为 201);`force=true` 时会先停止 + 删除旧容器,再用新配置重建(会话数据保留)。
- **方法**:`POST /agents`
- **Content-Type**:`application/json`

#### 请求体

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `agent` | object | 是 | Agent 配置,详见 [AgentDefinition](#agentdefinition) |
| `provider` | object | 是 | LLM 提供商配置,详见 [ProviderConfig](#providerconfig) |
| `aigc` | object | 否 | AIGC 内容标识配置(GB 45438-2025),详见 [AigcConfig](#aigcconfig)。省略或 `enabled=false` 时 runtime 不打标识 |
| `force` | boolean | 否 | 默认 `false`。为 `true` 时强制重建同名容器 |
| `runtime_token` | string | **是** | 调用方指定的运行时 Token。deployer 不再生成 Token,直接将该值作为 `ZERONE_AGENT_HTTP_API_KEY` 注入容器。要求非空且不含首尾空白 |

#### 请求示例

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

#### 成功响应(201)

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

#### 字段说明

| 字段 | 类型 | 说明 |
|---|---|---|
| `agentName` | string | 清洗后的 agent 名称(单例键) |
| `instanceId` | string | 本次创建生成的短随机 ID,贴入 Docker label `agent-deployer/agent.instance-id`,为未来 HA 场景预留 |
| `containerId` | string | Docker 容器完整 ID |
| `containerName` | string | Docker 容器名,形如 `cloud-agent-<name>-<instanceId>` |
| `status` | string | 状态枚举,详见 [AgentStatus](#agentstatus) |
| `hostPort` | int | 映射到宿主机的端口(运行时容器内部端口固定,由 `AGENT_DEPLOYER_RUNTIME_CONTAINER_PORT` 控制,默认 3000) |
| `createdAt` | string | RFC3339 UTC 时间戳 |
| `yamlPath` | string | 该 agent 的 YAML 配置路径(挂载到容器内 `/app/config`) |
| `sessionDir` | string | 会话持久化目录(挂载到容器内 `/root/.agents`) |
| `skillsDir` | string | 技能目录(仅当声明了 skills 时返回, copied into `/workdir/.agents/skills`) |

> **注意**:返回的 `hostPort` 仅在容器运行期内稳定。重建容器后端口会变化,务必每次都重新查询。

---

### 2. 列出全部 Agent

返回当前 deployer 管理的所有 agent(通过 Docker label `agent-deployer/managed=true` 过滤)。

- **方法**:`GET /agents`

#### 请求示例

```bash
curl "$DEPLOYER/agents" ${API_KEY:+-H "Authorization: Bearer $API_KEY"}
```

#### 成功响应(200)

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

> 列表响应**不包含** `createdAt / yamlPath / sessionDir / skillsDir`(这些字段仅在 `POST /agents` 返回)。每个元素的核心字段同 [AgentResponse](#agentresponse)。

---

### 3. 查询 Agent 详情

- **方法**:`GET /agents/:name`
- **路径参数**:`name` — agent 名称(会被同样清洗,大小写不敏感)

#### 请求示例

```bash
curl "$DEPLOYER/agents/coder" ${API_KEY:+-H "Authorization: Bearer $API_KEY"}
```

#### 成功响应(200)

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

#### 失败响应

- `404`:`{ "success": false, "error": "agent \"coder\": agent not found" }`
- `500`:Docker 查询失败

---

### 4. 查询实时状态

返回容器的实时 Docker 状态与健康检查结果。**创建后应轮询此接口直到 `health=healthy` 再下发任务**,以确认运行时就绪。

- **方法**:`GET /agents/:name/status`

#### 请求示例

```bash
curl "$DEPLOYER/agents/coder/status" ${API_KEY:+-H "Authorization: Bearer $API_KEY"}
```

#### 成功响应(200)

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

| 字段 | 说明 |
|---|---|
| `status` | Docker 原生 state:`running` / `created` / `exited` / `paused` 等 |
| `health` | Docker 健康检查:`starting` / `healthy` / `unhealthy` / `none`(未配置健康检查或 inspect 失败时) |
| `image` | 容器使用的镜像名 |

> 与 [§3](#3-查询-agent-详情) 的区别:此接口会额外执行一次 `docker inspect` 拿到健康检查结果;inspect 失败时降级返回 `health=none`,不报错。

---

### 5. 查询容器日志

返回容器最近的 stdout + stderr 合并输出,用于排查启动失败等问题。

- **方法**:`GET /agents/:name/logs`
- **查询参数**:

| 参数 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `tail` | int | `100` | 返回最后 N 行;非正整数时回退为默认值 |

#### 请求示例

```bash
# 取最近 200 行
curl "$DEPLOYER/agents/coder/logs?tail=200" ${API_KEY:+-H "Authorization: Bearer $API_KEY"}
```

#### 成功响应(200)

> **注意:载荷字段为 `logs`,不是 `data`。**

```json
{
  "success": true,
  "logs": "[runtime] starting...\n[runtime] listening on :3000\n..."
}
```

---

### 6. 停止 Agent

优雅停止容器。容器本身和挂载的数据目录都保留,可随时通过 [restart](#7-重启-agent) 恢复。

- **方法**:`POST /agents/:name/stop`

#### 请求示例

```bash
curl -X POST "$DEPLOYER/agents/coder/stop" ${API_KEY:+-H "Authorization: Bearer $API_KEY"}
```

#### 成功响应(200)

```json
{ "success": true }
```

---

### 7. 重启 Agent

重启容器(等同于 `docker restart`)。配置和数据保持不变。

- **方法**:`POST /agents/:name/restart`

#### 请求示例

```bash
curl -X POST "$DEPLOYER/agents/coder/restart" ${API_KEY:+-H "Authorization: Bearer $API_KEY"}
```

#### 成功响应(200)

```json
{ "success": true }
```

---

### 8. 删除 Agent

停止并移除容器。**该接口是幂等的**:即便容器已经不存在,也返回 200。

- **方法**:`DELETE /agents/:name`
- **查询参数**:

| 参数 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `removeData` | bool | `false` | 为 `true` 时,同时删除宿主机上的 agent 目录(含 `agents.yaml`、会话、技能) |

#### 请求示例

```bash
# 仅删容器,保留数据
curl -X DELETE "$DEPLOYER/agents/coder" ${API_KEY:+-H "Authorization: Bearer $API_KEY"}

# 连同数据一起清理(彻底销毁)
curl -X DELETE "$DEPLOYER/agents/coder?removeData=true" ${API_KEY:+-H "Authorization: Bearer $API_KEY"}
```

#### 成功响应(200)

```json
{ "success": true }
```

> 即便找不到容器,只要 `removeData=false` 也会返回 200。若 `removeData=true` 且容器已不存在,仍会执行目录清理(尽力而为,best-effort)。

---

## 数据模型

### CreateAgentRequest

```jsonc
{
  "agent": AgentDefinition,
  "provider": ProviderConfig,
  "force": false,                  // boolean
  "runtime_token": "must-be-set"   // string, 必填,作为运行时 Token
}
```

### AgentDefinition

| 字段 | 类型 | 必填 | 校验规则 | 说明 |
|---|---|---|---|---|
| `name` | string | 是 | 非空;会被清洗为 `[a-z0-9-]` | 单例键 |
| `description` | string | 是 | 非空 | agent 功能描述。runtime 2.0 起必填;挂载 subagent 时父 agent 的 Task 工具展示的就是它 |
| `model` | string | 是 | 非空 | 模型名,如 `claude-sonnet-4-6` |
| `systemPrompt` | string | 是 | 非空 | 系统提示词 |
| `maxTurns` | int \| null | 否 | `null` 表示无限制 | Agent 最大对话轮数 |
| `permissionMode` | string | 否 | — | 权限模式,如 `auto` |
| `tools` | string[] | 否 | — | 启用的工具名,如 `["Read","Write"]` |
| `skills` | SkillSource[] | 否 | 见 SkillSource 定义 | 要下载/安装的技能 zip 列表;传到 runtime 时只保留 name 作为 skill 白名单 |
| `settingSources` | string[] | 否 | — | 触发 runtime 扫描 skills 文件系统的来源(如 `["user","project"]`)**;不传则 skills 不会被加载** |
| `datasets` | map<string,string> | 否 | `id` 和 `description` 均非空;JSON 中重复 `id` 仅保留最后一个 | dataset_id 到 dataset_description 的映射,会写入 `agents.yaml` 的 `datasets` 字段 |
| `subagents` | SubagentDefinition[] | 否 | 子 agent 名称不可重复 | 子 agent 配置。runtime 2.0 采用引用挂载:每个 subagent 会被展开为 agents.yaml 中的一等 agent entry,主 entry 只按 id 引用;挂载时仅 `description`/`prompt`/`tools`/`maxTurns` 生效,模型与凭证跟随主 agent |

### SkillSource

| 字段 | 类型 | 必填 | 校验规则 | 说明 |
|---|---|---|---|---|
| `name` | string | 是 | 匹配 `[A-Za-z0-9._-]{1,64}` | skill 目录名,也是 runtime skill 白名单条目 |
| `url` | string | 是 | `http(s)` 且有 host | skill zip 下载地址 |
| `hash` | string | 是 | 64 位 hex(可带 `sha256:` 前缀) | zip 文件 sha256,用于校验和缓存 |

### SubagentDefinition

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `name` | string | 是 | 子 agent 名称,同一 agent 内不可重复 |
| `description` | string | 是 | 描述 |
| `prompt` | string | 是 | 子 agent 的提示词 |
| `tools` | string[] | 否 | 子 agent 启用的工具 |
| `maxTurns` | int | 否 | 子 agent 最大轮数 |

### ProviderConfig

| 字段 | 类型 | 必填 | 校验规则 | 说明 |
|---|---|---|---|---|
| `protocol` | string | 是 | 枚举:`anthropic-messages` / `openai-completions` | 提供商协议类型,直接透传为 runtime 的 `ZERONE_AGENT_API_TYPE` |
| `baseUrl` | string | 是 | 非空 | LLM API 基础 URL |
| `lockedApiKey` | string | 是 | 非空 | 调用 LLM 的 API Key(字段名是历史遗留,实际就是 API Key) |

#### AigcConfig

AIGC 生成合成内容标识配置(GB 45438-2025)。写入 runtime agents.yaml 顶层 `aigc:` 段,由 runtime 在响应中注入隐式标识(`aigc` 字段,含 `ContentProducer` / `ProduceID` / `ReservedCode1` 签名)。

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `enabled` | boolean | 是 | 是否启用标识。`false` 或省略整个 `aigc` 时 runtime 不打标识 |
| `contentProducer` | string | `enabled=true` 时必填 | 27 位服务提供者编码,后 4 位为模型/应用码槽位 |
| `signingKey` | string | 否 | 标签完整性签名密钥(SHA-256),由调用方生成并保管;不配置则标识无 `ReservedCode1` |
| `explicitHint` | boolean | 否 | 是否在响应中带 `aigcExplicitHint: true`,提示下游 UI 展示显式标识。**未传时默认 `true`**;显式传 `false` 才关闭 |
| `label` | enum | 否 | 隐式标识类型,对应 GB 45438-2025: `"1"` = AI生成、`"2"` = 疑似AI生成、`"3"` = 疑似。**未传时 runtime 默认 `"1"`** |
| `produceIdPrefix` | string | 否 | `ProduceID` 前缀,拼接为 `<prefix><timestamp>-<uuid12>`,便于下游内容溯源。无格式约束,空字符串等同未设置 |
| `modelCodes` | object | 否 | 模型名 → 4 位模型码映射。命中时替换 `contentProducer` 后 4 位 |

示例:

```json
"aigc": {
  "enabled": true,
  "contentProducer": "001191320118MAK93FC72D10001",
  "signingKey": "<调用方生成并保管的密钥>",
  "label": "2",
  "produceIdPrefix": "tenant-A/",
  "modelCodes": { "glm-4.5": "0001" }
}
```

注意:`aigc` 内容(含 `signingKey`)不会出现在任何 API 响应中;`force` 重建时不传 `aigc` 即丢弃旧标识配置。

### AgentResponse

`POST /agents`、`GET /agents/:name`、`GET /agents` 返回的数据结构。

| 字段 | 类型 | 必出现 | 说明 |
|---|---|---|---|
| `agentName` | string | 是 | |
| `instanceId` | string | 是 | |
| `containerId` | string | 是 | |
| `containerName` | string | 是 | |
| `status` | AgentStatus | 是 | |
| `hostPort` | int | 是 | |
| `createdAt` | string | 仅 `POST` 返回 | RFC3339 UTC |
| `yamlPath` | string | 仅 `POST` 返回 | |
| `sessionDir` | string | 仅 `POST` 返回 | |
| `skillsDir` | string | 仅 `POST` 且声明 skills 时 | |
| `runtimeToken` | string | 仅 `POST` 返回 | 调用方传入的 Token,与注入容器的 `ZERONE_AGENT_HTTP_API_KEY` 一致;Get / List 不返回 |

### AgentStatus

状态枚举(字符串):

| 值 | 含义 |
|---|---|
| `running` | 运行中 |
| `stopped` | 已停止 |
| `exited` | 已退出 |
| `not_found` | 未找到 |
| `unknown` | 未知 Docker 状态 |

> `GET /agents/:name/status` 的 `status` 字段返回的是 Docker 原生 state(如 `created`),未做归一化。

---

## Provider 字段映射

创建时,`provider` 和 `agent.model` 等字段会作为环境变量注入到运行时容器:

| 请求字段 | 运行时容器内的环境变量 |
|---|---|
| `provider.lockedApiKey` | `ZERONE_AGENT_API_KEY` |
| `provider.baseUrl` | `ZERONE_AGENT_BASE_URL` |
| `provider.protocol` | `ZERONE_AGENT_API_TYPE` |
| `agent.model` | `ZERONE_AGENT_MODEL` |
| `runtime_token` | `ZERONE_AGENT_HTTP_API_KEY` |

---

## 典型调用流程

一次完整的"创建 → 等待就绪 → 使用 → 销毁"流程:

```bash
# 1. 创建
RESP=$(curl -s -X POST "$DEPLOYER/agents" \
  -H 'Content-Type: application/json' \
  ${API_KEY:+-H "Authorization: Bearer $API_KEY"} \
  -d '{ "agent": { ... }, "provider": { ... } }')

# 2. 轮询健康状态直到 healthy
while :; do
  HEALTH=$(curl -s "$DEPLOYER/agents/coder/status" \
    ${API_KEY:+-H "Authorization: Bearer $API_KEY"} \
    | jq -r '.data.health')
  [ "$HEALTH" = "healthy" ] && break
  [ "$HEALTH" = "unhealthy" ] && { echo "startup failed"; exit 1; }
  sleep 2
done

# 3. 拿到运行时端口,通过该端口直接与 agent 对话(协议由 open-agent-runtime 决定)
PORT=$(echo "$RESP" | jq -r '.data.hostPort')

# 4. 停止 / 重启(按需)
curl -X POST "$DEPLOYER/agents/coder/stop"    ${API_KEY:+-H "Authorization: Bearer $API_KEY"}
curl -X POST "$DEPLOYER/agents/coder/restart" ${API_KEY:+-H "Authorization: Bearer $API_KEY"}

# 5. 查日志排查问题
curl "$DEPLOYER/agents/coder/logs?tail=500" ${API_KEY:+-H "Authorization: Bearer $API_KEY"}

# 6. 彻底销毁
curl -X DELETE "$DEPLOYER/agents/coder?removeData=true" ${API_KEY:+-H "Authorization: Bearer $API_KEY"}
```

---

## 错误排查

| 现象 | 排查方向 |
|---|---|
| 创建返回 400 `invalid request` | 检查 `agent.name/model/systemPrompt`、`provider.protocol/baseUrl/lockedApiKey` 是否齐全;`protocol` 必须是 `anthropic-messages` 或 `openai-completions` |
| 创建返回 500 `find existing container` | 检查 deployer 容器是否能访问 `/var/run/docker.sock` |
| 创建后 `health` 长时间 `starting` | 查 `GET /logs`,通常是 `ZERONE_AGENT_API_KEY` 无效或 `baseUrl` 不通 |
| `health=unhealthy` | 运行时镜像健康检查失败;查日志确认具体异常 |
| 401 `unauthorized` | 服务端启用了 `AGENT_DEPLOYER_API_KEY`,但请求未携带或 Key 不匹配 |
| 404 `agent not found` | 名称拼写错误,或容器已被删除;注意名称会被小写化清洗 |
| `hostPort` 变了 | 重建容器会重新分配端口,务必每次动态查询 |
