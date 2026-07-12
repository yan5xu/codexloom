# CodexLoom

CodexLoom 是一个建立在 Codex 之上的 **Agent 治理与组织集成平台**。

它把 Codex Thread 长程化为可治理的 Agent：Agent 有稳定 ID、名称、工作目录、Profile、
团队关系和外部平台地址；Codex Thread 是 Agent 的主要执行与上下文载体，而不是 Agent
本身。用户可以在 Codex Desktop/Mobile、CodexLoom WebUI 或 `loom` CLI 上操作同一个
Thread，消息和运行状态实时同步。

CodexLoom 的重点不是再造一个聊天客户端，而是让维护者能够管理一组长期 Agent，并把这些
Agent 作为组织资产接入飞书、Parall 等沟通环境。

## 核心对象

- **Agent**：稳定的治理实体。名称可改，Profile、关系和外部地址都绑定稳定 `agentId`。
- **Thread**：Agent 的主要 Codex 上下文与历史载体，`threadId` 绑定在 Agent 上。
- **Turn**：Thread 上的一次执行。一个 Thread 同时只允许一个 active Turn。
- **Item**：Turn 内的用户输入、Agent 输出、推理、命令、文件变更或图片等事件。
- **Profile**：Agent 的长期身份、Domain 和 Scope，是协作契约，不是单次任务提示词。
- **Relationship**：Agent 之间的长期协作关系；Messages 提供实际通信证据。
- **Address**：Agent 在飞书、Parall 等平台上的外部身份。一个 Agent 可以有多个 Address。
- **Conversation Membership**：Agent 在某个群或会话中的目的、角色与行为边界。

## 运行模型

```text
Codex Desktop / Mobile ── Remote ─┐
                                  │
React WebUI ───── REST + SSE ─────┼── CodexLoom :4870
loom CLI ───────── REST + SSE ────┤     │
Parall / Lark gateways ───────────┘     ├── Hub domains: Agent, Team, Messages,
                                        │   Inbox/Outbox, Schedules, Integrations
                                        └── one shared CodexHost
                                             └── codex app-server
                                                  ├── Thread A
                                                  ├── Thread B
                                                  └── Remote clients
```

一个 CodexLoom 实例只维护一个共享 `codex app-server`，称为 **CodexHost**。所有 Agent
Thread、Web/CLI 操作和 Codex Remote 客户端都进入这个 Host。app-server 会把已加载 Thread
的通知广播给已初始化连接，因此手机端发起的 Turn 可以直接更新 WebUI 和 CLI，无需刷新。

这取代了旧的“每 Session 一个 app-server + 单独 Remote app-server”模型。共享 Host 的边界
是一个 `CODEX_HOME`、一个登录账号和一个信任域；需要账号或权限隔离时，应运行另一个
CodexLoom 实例，而不是在同一 Host 内混用。

## 真相源

CodexLoom 不复制 Codex 对话历史：

- Agent 对话历史来自 Codex rollout：
  `~/.codex/sessions/YYYY/MM/DD/rollout-<timestamp>-<threadId>.jsonl`。
- `~/.codex-loom/agents.json` 只保存 Agent 注册表和 Thread 绑定。
- `events/<agent-id>.ndjson` 只用于 live SSE 回放和断线续传。
- `comms.ndjson` 保存 Agent 间通信。
- `messages.ndjson`、`inbox.ndjson`、`attempts.ndjson`、`outbox.ndjson` 保存外部通信事实、
  处理队列和发送结果；它们不是 Agent 的思考历史。

## 构建与启动

前置条件：本机已安装并使用 ChatGPT 身份登录 `codex` CLI。

```sh
make release
./bin/codex-loom
```

打开 <http://localhost:4870>。

`make release` 会构建规范入口和兼容入口：

```text
bin/codex-loom             bin/codex-hub             (compatibility)
bin/codex-loom-reloader    bin/codex-hub-reloader    (compatibility)
bin/loom                   bin/chub                   (compatibility)
bin/loom-gateway           bin/chub-gateway           (compatibility)
```

规范环境变量：

```text
CODEX_LOOM_PORT             默认 4870
CODEX_LOOM_DATA             默认 ~/.codex-loom
CODEX_LOOM_URL              CLI/gateway 服务地址
CODEX_LOOM_CODEX_BIN        共享 CodexHost 使用的 codex 可执行文件
CODEX_LOOM_ADMIN_TOKEN      非 localhost 管理操作令牌
CODEX_LOOM_CONNECTOR_TOKEN  gateway 连接令牌
CODEX_LOOM_BACKUP_KEEP      本地快照保留数量，默认 25
```

对应的 `CODEX_HUB_*` 和 `CHUB_URL` 仍是兼容别名。

### 数据迁移

首次以默认路径启动 CodexLoom 时，如果只有 `~/.codex-hub`：

1. 原子改名为 `~/.codex-loom`；
2. 创建 `~/.codex-hub -> ~/.codex-loom` 软链接；
3. 从 `sessions.json` 读取旧注册表，并写入 `agents.json`；
4. 暂时继续镜像写 `sessions.json`，供旧二进制使用。

迁移不会改写 Codex rollout。现有 gateway 的旧 state-file 路径也会通过软链接继续工作。

## CLI

规范 CLI 是 `loom`。领域化命令用于日常操作：

```sh
loom agent create research --cwd /path/to/repo
loom agent list
loom agent get research
loom agent rename research research-lead
loom agent archive research-lead

loom thread send research-lead "检查当前实现并报告风险"
loom thread watch research-lead
loom thread history research-lead --count 10
loom thread interrupt research-lead
```

治理和通信命令：

```sh
loom profile get|set|clear ...
loom team [agent]
loom team links [agent]
loom msg ...
loom inbox ...
loom outbox ...
loom integration ...
loom conversation ...
loom schedule ...
loom remote status|enable|disable|pair|devices|revoke
loom backup --reason before-risky-change
loom backups
```

旧 `chub create/list/send/watch/...` 命令仍可用，并已切到规范 Agent API。完整通信说明见
[docs/loom-cli.md](docs/loom-cli.md)，该文件同时记录 `chub` 兼容用法。

## HTTP API

规范 Agent API：

| 方法 | 路径 | 说明 |
|---|---|---|
| GET/POST | `/api/agents` | 列出或创建 Agent |
| GET | `/api/agents/{key}` | Agent 详情，key 为 ID 或名称 |
| PATCH | `/api/agents/{key}/config` | 名称、模型、effort、sandbox、审批策略 |
| GET/PUT | `/api/agents/{key}/profile` | 长期协作 Profile |
| DELETE | `/api/agents/{key}` | 归档 Agent 对应 Thread 并移除注册表 |
| POST | `/api/agents/{key}/turns` | 启动 Turn，body 为 `{text, timeoutSec?}` |
| POST | `/api/agents/{key}/turns/current/interrupt` | 中断当前 Turn |
| GET | `/api/agents/{key}/thread/history` | 从 rollout 读取 Thread 历史 |
| GET | `/api/agents/{key}/thread/events` | Agent/Thread SSE 实时事件 |
| POST | `/api/agents/{key}/thread/approvals/{id}` | 处理审批 |

`/api/sessions/...` 保留为旧客户端兼容路由。规范 Agent SSE 将旧事件投影为 `loom/*`，例如
`loom/agent-created`、`loom/turn-started`；Codex 原生 `turn/*`、`item/*` 通知保持原名。
旧 Session SSE 继续输出 `hub/*`。

其他主要 API：

- `/api/team`、`/api/team/relationships`
- `/api/comms`、`/api/comms/messages`
- `/api/inbox`、`/api/outbox`
- `/api/integrations/connections|addresses|conversations`
- `/api/schedules`
- `/api/remote`
- `/api/admin/backup`、`/api/admin/backups`、`/api/admin/restart`
- `/api/events`：全局 SSE，规范 `loom/*` 与旧 `hub/*` 事件同时输出

## Remote

WebUI 的 Remote 页面和 `loom remote` 管理 Codex Remote：

```sh
loom remote enable
loom remote pair
loom remote devices
loom remote revoke <client-id>
loom remote disable
```

Remote 使用共享 CodexHost，不改变 Profile developer message、Web/CLI API、rollout 或 Agent
通信机制。设备名称由 Codex Remote enrollment/backend 决定；CodexLoom 只展示 app-server 返回的
server name、environment ID 和已配对设备，不修改 Codex 私有数据库。

## 备份与重启

新快照位于：

```text
~/.codex-loom/backups/codex-loom-<UTC timestamp>-<reason>.tar.gz
```

列表同时识别旧 `codex-hub-*.tar.gz`。快照包含 Loom 数据、当前可见 Agent 的 rollout、
pinix-edge registry、Codex config 和 `manifest.json`。

WebUI 的 Restart Loom 会先创建 `pre-restart` 快照。如果存在 active Agent，则进入 waiting，
拒绝新 Turn，等全部 Turn 结束后再由独立 reloader 进程切换服务。开发 Agent 不应直接 kill
承载自己的生产进程。

## 开发闭环

```sh
/usr/local/go/bin/go test ./...
cd web && npm run build
make build
```

前端或共享 Host 改动还必须走真实路径：

1. 用独立端口和临时 `CODEX_LOOM_DATA` 启动 canary；
2. `curl` 验证 health、Agent API、SSE 和静态资源；
3. 创建至少两个 Agent，确认只有一个 `codex app-server` 子进程；
4. 真实执行 Turn，验证 Web/CLI history 和 live stream；
5. 用 `/tmp/pinixc browser ... --profile default` 做桌面与移动视口截图、automation assert；
6. 构建完成后由用户触发生产 Restart Loom，再验证 Codex Mobile/Desktop Remote 与 WebUI
   的同一 Thread 实时双向同步。

架构、限制和完整 SOP 见 [docs/handbook.md](docs/handbook.md)。
