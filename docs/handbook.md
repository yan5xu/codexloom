# CodexLoom 开发手册

这份手册记录 CodexLoom 的产品心智、DDD 边界、运行架构、数据流、危险边界和验证流程。
它由 CodexLoom 维护者持续更新；遇到事故或踩坑后，应把可复用的约束补到这里。

## 产品定义

CodexLoom 是建立在 Codex 之上的 Agent 治理与组织集成平台。

- 日常使用 Agent：Codex Desktop/Mobile 或 CodexLoom WebUI。
- 自动化使用 Agent：`loom` CLI 和 HTTP/SSE API。
- 治理 Agent：Profile、模型配置、Team Map、Relationships、Messages、Schedules、备份。
- 组织集成：同一个 Agent 可以绑定多个飞书、Parall 或其他 IM Address，并在不同群里拥有
  不同 Conversation Membership。

Agent 不是一次性 task，也不等于一次 Turn。它是稳定、可多轮交互的长期主体。Codex Thread
是 Agent 的主要上下文和执行载体；未来即使迁移 Thread，Agent ID、Profile、关系与 Address
也不应变化。

## 统一语言

- **Agent**：稳定治理实体，拥有 `agentId`、名称、Profile、关系和外部地址。
- **Thread**：Agent 的主要 Codex 历史和上下文绑定，标识为 `threadId`。
- **Turn**：Thread 上的一次执行；一个 Thread 同时只有一个 active Turn。
- **Item**：Turn 内的输入、输出、推理、命令、文件变更、图片等事件。
- **CodexHost**：CodexLoom 维护的共享 `codex app-server` 进程。
- **Profile**：长期协作身份、Domain 和 Scope，不是本轮任务描述。
- **Relationship**：Agent 间显式长期关系。
- **Agent Message**：Agent 间需要或不需要回复的结构化通信。
- **Connection**：到外部平台账号或租户的连接。
- **Address**：某个 Agent 在一个 Connection 上的外部身份。
- **Conversation Membership**：Agent 在具体群或 DM 中的目的、角色、策略和边界。
- **Inbox / Outbox**：跨内部和外部来源的收件处理与发送账本。
- **Schedule**：以 `scheduler` 系统身份定时生成 Agent Message 的长期规则。

`Session`、`chub`、`codex-hub` 只在兼容 API、旧二进制、历史 wire value 或真实旧 launchd
label 中出现。新代码、UI 文案和文档叙述使用 Agent、Thread、Turn、Item、Loom。

## DDD 边界

### Agent Governance

聚合根是 Agent。它维护稳定 ID、名称、工作目录、Codex Thread 绑定、模型配置、Profile 与
归档状态。Agent 名称可变，其他领域只能持有稳定 `agentId`。

不变量：

- 名称匹配 `[a-zA-Z0-9_-]+` 且不能与其他 Agent 名称或 ID 冲突。
- 一个 Agent 首期绑定一个主 Thread。
- 运行中的 Agent 不能修改会影响下一 Turn 的配置。
- 归档 Agent 前先调用 Codex `thread/archive`；失败不能静默丢注册表。

### Codex Runtime

负责 CodexHost 生命周期、Thread start/resume/name、Turn start/interrupt、Item 通知和审批。
它不拥有 Profile、Team 或外部平台语义。

不变量：

- 一个 CodexLoom 实例只有一个共享 CodexHost。
- 不在持有 `Hub.mu` 时调用 `client.Request`；reader callback 也需要该锁。
- 所有 JSON-RPC 通知必须按 `threadId` 路由到 Agent。
- Remote 打开未注册的既有 Thread 时，在 `turn/started` 上即时收养，不能丢后续 Item。
- Host 崩溃只中断 active Turn；Agent 和 Thread 绑定继续持久化，下一次操作重建 Host。

### Communication

负责 Agent Message、response required/none、reply/no-reply、按目标排队和 Messages 历史。
它是通信账本，不是 Codex 对话历史。

不变量：

- 目标 Agent busy 时消息保持 queued；Turn 结束立刻且一次只投递一条。
- `required` 必须由 reply 或 no-reply 明确关闭。
- 消息保存发送/接收双方稳定 Agent ID，并保留发送时名称快照。
- XML envelope 由 Loom 生成，调用方不手写。

### Platform Integration

负责 Connection、Address、Conversation Membership、Inbox、Handling Attempt、Outbox 和
gateway connector protocol。凭据只保存 `env:` / `keychain:` 引用，不保存明文。

不变量：

- 平台账号不是 Agent；Address 才是 Agent 在平台上的身份。
- 一个 Agent 可拥有多个平台 Address。
- 群聊行为由 Membership 定义；不能只看 Agent Profile。
- 入站事件按 provider external key 幂等；出站按稳定 idempotency key 幂等。
- gateway 是独立长生命周期进程，不和 CodexHost 同生共死。

### Team

Team 是组合读模型：Agent registry + Profile + 显式 Relationship + Messages 观察关系。Graph
是关系导航，不是真相源，也不负责直接编辑原始消息证据。

### Scheduler

`scheduler` 是稳定系统身份，不是 Codex Agent。Schedule 触发后生成标准 Agent Message，仍走
目标队列与回复协议，不直接绕过 Communication 调用 Turn。

### Disaster Recovery

负责一致的本地快照与重启前保护。快照必须是普通 tar.gz，服务不可用时仍能手工恢复。

## 模块职责

### `cmd/codex-loom`

构建规范服务二进制 `bin/codex-loom`。入口读取
`CODEX_LOOM_PORT` / `CODEX_LOOM_DATA`，旧 `CODEX_HUB_*` 为 fallback；收到 SIGTERM 后调用
`Hub.Shutdown()`，只关闭一次共享 CodexHost。

### `cmd/loom`

同一实现构建 `bin/loom` 和兼容 `bin/chub`。规范命令：

```sh
loom agent create|list|get|rename|archive ...
loom thread send|watch|history|interrupt ...
loom profile|team|msg|inbox|outbox|integration|conversation|schedule|remote ...
```

CLI 不直接启动 Codex，也不读取业务数据文件，全部走 HTTP/SSE。

### `internal/hub`

领域编排层。`Agent` 是规范实体，`Session` 只作为源码兼容别名；`Hub.agents` 保存聚合根。
`codex_host.go` 持有唯一 Host，并将通知按 Thread 路由；`remote.go` 只管理共享 Host 上的
`remoteControl/*` 状态，不再维护第二个 app-server。

### `internal/codex`

line-framed JSON-RPC 客户端。它负责 subprocess、initialize、pending response、server request、
notification 和 close，不理解 Agent 领域。

server-initiated request 也有 `id` 与 `method`，必须先于普通 response 识别；否则审批可能与
client request ID 碰撞并被误消费。

### `internal/rollout`

按 Thread 读取 Codex rollout，并投影为 history Turn/Item。它是 Agent 对话历史的读取适配器，
不是写模型。

### `internal/store`

默认 `~/.codex-loom`，规范环境变量 `CODEX_LOOM_DATA`。布局：

```text
agents.json                 Agent registry
sessions.json               旧二进制兼容镜像
profiles.json
team-links.json
schedules.json
comms.ndjson
integrations.json
messages.ndjson
inbox.ndjson
attempts.ndjson
outbox.ndjson
events/<agent-id>.ndjson
gateway/*.json
backups/
```

Agent history 不在这里，仍在 `~/.codex/sessions/**/rollout-*.jsonl`。

### `internal/httpapi`

提供 REST、SSE、内嵌 WebUI、图片读取和管理 API。

- 规范路由：`/api/agents/...`。
- 兼容路由：`/api/sessions/...`。
- 规范 Agent SSE 把旧持久化事件投影为 `loom/*`；Codex `turn/*` / `item/*` 保持原名。
- 兼容 Session SSE 保持 `hub/*`。
- 全局 SSE 同时输出规范和兼容事件，旧页面和新页面可跨版本重启继续工作。

### `web`

React/Vite/TypeScript/Tailwind 治理工作台。第一屏是实际工作台，不是 landing page。

- Sidebar：全局 active/idle 状态、Agent 列表和管理入口。
- AgentPane：Thread history/live、Turn 输入、配置、Profile、Address 和 Membership。
- Inbox/Messages：内部和外部消息统一观察，但保留 origin 区分和 raw XML。
- Team：列表、Graph 和 Inspector；Graph 卡片可拖拽，位置稳定保存。
- Remote：连接状态、配对码、二维码和设备列表。
- automation：规范入口 `window.codexLoom`，`window.codexHub` 是兼容别名。

### `cmd/loom-gateway` 与 `gateway/`

规范二进制名 `loom-gateway`。Go fake provider 用于协议与幂等测试；`parall.mjs`、`lark.mjs`
负责真实平台 cursor、ack、reaction/read-state 和发送。环境变量优先 `CODEX_LOOM_*`，旧变量可用。

## 共享 CodexHost

### 为什么共享

旧模型为每 Agent spawn 一个 app-server，Remote 又维护一个独立 app-server。这样 Web 驱动的
Thread 与手机驱动的 Thread 不在同一通知总线上，手机消息只能在刷新 rollout 后看到；进程数
也随 Agent 数增长。

新模型只有一条 app-server 进程边界：

```text
CodexLoom HTTP connection ─┐
Codex Remote websocket ────┼── shared app-server ── N Threads
future local connections ──┘
```

Codex app-server 会将已加载 Thread 的事件附着到所有已初始化 connection。Loom 的 connection
因此能直接收到 Remote Turn/Item 通知并通过 SSE 广播。

共享 Host initialize 暂时保留 legacy client name `codex-hub-remote`。该值参与 Remote enrollment
scope；直接改成 `codex-loom` 会让现有配对设备落到另一份 enrollment。它是 wire compatibility
identity，不是产品名称，待官方提供显式 enrollment 迁移后再移除。

### 路由

1. 从通知顶层 `threadId`、`thread.id`、`turn.threadId` 或 `item.threadId` 提取 Thread。
2. 查 Agent registry 的 Thread 绑定。
3. 已知 Thread 复用对应 runtime bookkeeping；runtime 只保存 active Turn、审批和锁，不拥有进程。
4. Remote 新建 Thread 时，`thread/started` 创建 source=`remote` Agent。
5. Remote resume 未注册旧 Thread 时，`turn/started` 创建占位 Agent，确保后续 Item 不丢，再用
   `thread/read(includeTurns:false)` 回填 cwd 和合法 Thread name。
6. 本地并发 `thread/start` 通知若无法唯一匹配 pending Agent，不收养；等 JSON-RPC response 绑定，
   防止生成重复 Agent。

### 信任边界

一个 Host 共享 `CODEX_HOME`、ChatGPT 登录、installation/enrollment 和本地权限。需要不同账号、
sandbox 信任域或数据隔离时运行另一个 CodexLoom 实例与独立 `CODEX_HOME`。

Remote APIs 是 Codex experimental API。不要直接修改 Codex SQLite、daemon PID 文件、私有
websocket wire protocol 或隐藏环境变量。

Remote enrollment 以 app-server `clientInfo.name` 为持久 scope。共享 Host 暂时保留旧 wire identity
`codex-hub-remote`，只把可见 title 改为 CodexLoom，从而让已配对设备跨升级继续可用；这个旧值
属于兼容边界，不是产品名称。

## 核心数据流

### 创建 Agent

1. `POST /api/agents`。
2. 创建稳定 Agent ID 和空 Thread binding。
3. 确保共享 CodexHost 已 initialize。
4. `thread/start`，收到 response 后绑定 `threadId`。
5. `thread/name/set` 同步 Agent 名称到 Codex Thread title。
6. 持久化 `agents.json` 和兼容 `sessions.json`。
7. 发 Agent lifecycle 事件。

### Web/CLI 启动 Turn

1. `POST /api/agents/{key}/turns`。
2. 检查 Agent idle，并在每个 Turn 前幂等执行 `thread/resume`。共享 app-server 可以卸载空闲
   Thread，runtime 存在不代表 Thread 仍驻留；不能复用旧的“每 Thread 一个进程”心智。
3. Profile 版本尚未注入时，先加入 developer context；空 Profile 不注入。
4. 外部 Conversation 有有效 Membership 时，加入该会话的 developer context。
5. `turn/start` 透传 model、effort、approval policy。
6. Codex 原生通知进入 event log 和 SSE。
7. Turn 结束更新 Agent idle，并立即投递该 Agent 队列中的下一封消息。

### Remote 启动 Turn

1. Codex Mobile/Desktop 经 Remote backend 连接共享 app-server。
2. app-server 发 `turn/started`；Loom 按 Thread 找到或收养 Agent。
3. `userMessage` Item 更新 `currentTask`；Agent 状态变 running。
4. 所有 Item delta/completed 进入同一 Agent SSE。
5. `turn/completed|failed|aborted` 更新 idle，WebUI/CLI 无需刷新。

### History 与 Live

- History：`/thread/history` 直接解析 rollout，能看到 Desktop/Mobile/其他连接产生的完整历史。
- Live：`/thread/events?replay=0` 只接打开后的事件，避免与 history 重复。
- CLI watch 可按 seq 回放 live event tail，并用 `Last-Event-ID` 续传。
- event log 不是完整历史，不能用它重建上下文。

### Profile 注入

Profile 只在用户通过 CLI/UI 显式设置后有版本。新版本在下一个 Turn 前注入 developer role；
`profileVersionSeen` 防止每轮重复。compact 后是否重注入不能只看 rollout，应以 Codex 实际请求
上下文能力为准；在 app-server 暂未暴露可靠“本次发给模型的完整 history”前，不要猜测 compact。

完整设计和写作方法见 [agent-profile.md](agent-profile.md)。

### Agent 与外部消息

Agent 内部 XML `<agent_message>` 和外部 `<inbox_message>` 在 Thread 中都使用专门 UI：

- 明确区分 REQ / RES / NOTIFY 或 provider origin。
- body 使用安全 Markdown renderer，不以 `<pre>` 显示 Markdown 源码。
- raw XML 可展开，便于审查协议。
- 旧调用方把换行写成字面量 `\n` 时，只在 display parser 兼容还原，不改 ledger。
- 发送按钮在请求开始时立即禁用，成功后立即清空输入，避免网络响应期间重复点击。

## 数据迁移与兼容

默认启动且仅存在 `~/.codex-hub` 时：原子 rename 到 `~/.codex-loom`，再建立旧路径软链接。
旧 gateway、launchd plist 和二进制可继续访问同一数据。

注册表迁移采用双文件：读取优先 `agents.json`、fallback `sessions.json`；写入同时更新两者。
备份列表同时识别 `codex-loom-*.tar.gz` 与 `codex-hub-*.tar.gz`。

兼容不是永久双领域模型：新代码只能写 Agent 语义，旧 Session 名称集中在 HTTP/CLI/storage
adapter 边界，并在未来明确版本窗口后删除。

## 备份与恢复

规范快照：

```text
~/.codex-loom/backups/codex-loom-<UTC timestamp>-<reason>.tar.gz
```

包含：

- `codex-loom/`：registry、Profile、Team、Schedules、Communication ledgers、live events。
- `codex-sessions/`：当前可见 Agent 对应 rollout。
- `pinix-edge/names.json`（存在时）。
- `codex/config.toml`（存在时）。
- `manifest.json`：Agent/Thread 清单、源路径、文件列表和 warning。

本地快照不等于异地备份。需要抗磁盘损坏时，把 backups 同步到另一个磁盘或远端。

恢复：停 Loom，检查 manifest，将 `codex-loom/` 恢复到数据目录，将 `codex-sessions/` 恢复到
`CODEX_SESSIONS_DIR` 或 `~/.codex/sessions`，再启动并验证 Agent/Thread 数量与 history。

## 已知限制

- Remote 是 experimental Codex API，backend 产品策略可能变化。
- 未注册 Remote Thread 会先使用 `remote-<suffix>`，随后 `thread/read` 回填合法 name/cwd；名称
  不符合 Agent 标识规则时仍保留占位名，维护者可在 Agent Config 改名。
- Agent 首期只有一个主 Thread；Thread 迁移与多 Thread Agent 尚未建模。
- Profile compact 后重注入策略缺少“实际模型请求 history”官方接口，不能保证精确。
- event log 只用于 live，不保证包含 Desktop 在 Loom 离线期间发生的 Item；刷新后 history 可见。
- 外部 gateway 各自负责 cursor 和平台 ack，Loom 无法替代平台端的最终投递保证。
- Membership 是行为上下文，不是安全沙箱；高权限 Agent 不应绑定不可信外部群。
- Graph 是 read projection；关系证据冲突时以显式 Relationship 和原始 Message ledger 为准。

## 开发流程

### 改前检查

1. `git status --short`，不要覆盖用户或其他 Agent 的工作。
2. 先读调用链与测试；不要只改表面文案。
3. 判断真相源：Agent registry、rollout、live event、Communication ledger、platform state 不可混用。
4. 涉及 Codex 协议时查官方 app-server 文档和本地源码，不凭记忆。

### 后端闭环

```sh
/usr/local/go/bin/gofmt -w <changed-go-files>
/usr/local/go/bin/go test ./...
make build
```

共享 Host 改动还要真实验证：

1. 独立 `CODEX_LOOM_DATA` 和端口启动 canary。
2. 创建两个合成 Agent。
3. 用进程树确认只有一个 `codex app-server` 子进程。
4. 两个 Agent 各执行真实 Turn，验证 `/thread/history` 与 SSE。
5. 协议与并发测试默认使用合成 Agent，不 resume 生产 Thread。
6. 生产规模 UI 测试可以复制 Agent/Profile/Relationship/Communication ledger 到临时数据目录，
   但必须保持 Remote disabled 且不启动 Turn。
7. 只有精确复现 Thread 绑定问题时，确认目标 Agent idle、保证没有第二个写入者后，才允许短暂
   resume 一条真实 Thread；完成一次探针后立即停 canary。

### 前端闭环

铁律：构建绿不等于完成。

```sh
cd web && npm run build
make build
```

然后：

1. 启动独立 canary，不复用生产 4870。
2. `curl` 验证 health、HTML 和静态 asset。
3. `/tmp/pinixc browser open <url> --profile default`。
4. 已有 tabid 后无需再传 profile。
5. 用 `window.codexLoom.state()` / pane automation 做结构化 assert。
6. 截 desktop 和 mobile viewport；检查长名称、窄顶部、滚动、Graph、弹层和空/错/加载状态。
7. 构建最终 dist 后再构建 Go embed binary。
8. 用户触发生产 Restart Loom；重启后 curl + automation + screenshot 再验一次。

### Team Graph 经验

- React Flow 负责 pan/zoom/selection/drag，不手写 hit-test。
- Graph 是拓扑导航，列表和 Inspector 仍是精确操作入口。
- 布局要 deterministic；用户拖拽位置按 Agent stable ID 持久化，刷新不能随机跳。
- 节点卡片只显示名称、状态、Domain 摘要和计数；长内容进 Inspector。
- 默认过滤低权重边；选择节点时突出 1-hop 邻域。
- 移动端默认列表，Graph 是可横向 pan 的工作区，Inspector 用 sheet。
- 两 Agent fixture 只能验证交互，不能证明布局；至少再用 20+ Agent 和高边密度 ledger 截图。
- automation 至少暴露 nodes/edges、selection、filters、visible IDs、fitView、focusAgent。

### 重启纪律

承载当前开发 Agent 的服务不能由 Agent 直接 kill 自己。正确流程：

1. 完成 build。
2. canary 验证。
3. 告诉用户点击 Restart Loom。
4. restart API 先做 `pre-restart` 备份。
5. active Agent 存在时进入 waiting，拒绝新 Turn，等待全部结束。
6. 独立 `codex-loom-reloader` 在 HTTP response 返回后 SIGTERM 旧服务并启动同一 executable。

首次从旧 LaunchAgent 切换到规范 label 时，不使用页面内 Restart，因为它只会重启当前旧
executable。完成 canary 后由用户执行一次：

```sh
cp deploy/com.pinix.codex-loom.plist ~/Library/LaunchAgents/
launchctl bootout gui/$(id -u)/com.pinix.codex-hub
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.pinix.codex-loom.plist
launchctl kickstart -k gui/$(id -u)/com.pinix.codex-loom
```

确认新 label 正常后再删除旧 plist。此后页面内 Restart Loom 会查找同目录
`codex-loom-reloader`，失败时再 fallback 到旧 reloader。

失败排查：

```sh
tail -n 200 /tmp/codex-loom-reloader.log
tail -n 200 /tmp/codex-loom.log
curl -fsS http://127.0.0.1:4870/api/health
```

旧日志路径和 `codex-hub-reloader` 仍可能由旧 launchd job 使用；兼容 fallback 会自动查找。

## API 速查

```text
GET    /api/health
GET    /api/agents
POST   /api/agents
GET    /api/agents/{key}
PATCH  /api/agents/{key}/config
GET    /api/agents/{key}/profile
PUT    /api/agents/{key}/profile
DELETE /api/agents/{key}
POST   /api/agents/{key}/turns
POST   /api/agents/{key}/turns/current/interrupt
GET    /api/agents/{key}/thread/history?count=N&offset=M
GET    /api/agents/{key}/thread/events?since=SEQ&tail=N&replay=0
POST   /api/agents/{key}/thread/approvals/{approvalId}

GET    /api/comms
POST   /api/comms/messages
GET    /api/inbox
GET    /api/outbox
GET    /api/team
GET    /api/schedules
GET    /api/integrations/connections
GET    /api/integrations/addresses
GET    /api/integrations/conversations
GET    /api/remote
GET    /api/events

POST   /api/admin/backup
GET    /api/admin/backups
POST   /api/admin/restart
GET    /api/admin/restart/status
```

旧 `/api/sessions/...` 是兼容别名，不应出现在新调用方。

## 延伸文档

- [codex-app-server-protocol.md](codex-app-server-protocol.md)
- [agent-profile.md](agent-profile.md)
- [agent-platform-integration.md](agent-platform-integration.md)
- [conversation-membership.md](conversation-membership.md)
- [loom-cli.md](loom-cli.md)，包含 `loom` 规范命令与 `chub` 兼容说明

## 维护纪律

- 优先修业务不变量，不用 UI 掩盖状态错误。
- 不从 live event 重建完整历史，不从 rollout 推断平台投递状态。
- 不让名称承担稳定身份；跨领域引用只用 stable ID。
- 不把平台账号、群或 Schedule 建模成伪 Agent。
- 不直接修改 Codex 私有数据库或 Remote wire protocol。
- 每次改动必须真实运行受影响工作流，并记录新教训。
