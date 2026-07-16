# CodexLoom 开发手册

这份手册记录 CodexLoom 的产品心智、DDD 边界、运行架构、数据流、危险边界和验证流程。
它由 CodexLoom 维护者持续更新；遇到事故或踩坑后，应把可复用的约束补到这里。

## 产品定义

CodexLoom 是建立在 Codex 之上的长期领域 Agent 组织与治理平台。它把一次性任务上下文转变为
持续负责某个领域的 Agent，并进一步把多个 Agent 组织成可以协作、对外沟通和持续优化的团队。

Task Agent 围绕一个目标建立，cold start 是正常成本；Domain Agent 围绕长期责任建立，cold
start 是连续性故障。Domain Agent 可以 idle，但它应通过 Profile、Skills、Thread、Trajectory
和 Relationships 保持身份、能力与工作的连续性。CodexLoom 不排斥 Task Agent，但当前治理
对象是没有预设结束时间的 Domain Agent。

大多数 Multi-Agent 系统首先建模 workflow；CodexLoom 首先建模 organization：工作流描述事情
如何流动，组织描述谁长期对什么负责。Lead、Internal Agent 和 Interface Agent 是由 Profile、
关系、可达入口与 Conversation Membership 组合出来的组织模式，不是互斥的 Runtime 类型。

- 日常使用 Agent：Codex Desktop/Mobile 或 CodexLoom WebUI。
- 自动化使用 Agent：`loom` CLI 和 HTTP/SSE API。
- 治理 Agent：Profile、模型配置、Team Map、Relationships、Messages、Schedules、备份。
- 组织集成：同一个 Agent 可以绑定多个飞书、Slack、Parall 或其他 IM Address，并在不同群里拥有
  不同 Conversation Membership；Microsoft Teams adapter 为 TODO。

Agent 不是一次性 task，也不等于一次 Turn。它是稳定、可多轮交互的长期主体。Codex Thread
是 Agent 的主要上下文和执行载体；未来即使迁移 Thread，Agent ID、Profile、关系与 Address
也不应变化。

Profile 定义长期职责与协作契约；Skill 保存可复用方法和固化经验；Thread/Trajectory 保存实际
工作的连续性。Skill 仍是 Agent 的重要资产，差异在于 Domain Agent 通常已经拥有稳定能力组合，
新任务到来时不需要重新决定自己是谁、应该装配哪些基础能力。

## 统一语言

- **Agent**：稳定治理实体，拥有 `agentId`、名称、Profile、关系和外部地址。
- **Thread**：Agent 的主要 Codex 历史和上下文绑定，标识为 `threadId`。
- **Turn**：Thread 上的一次执行；一个 Thread 同时只有一个 active Turn。
- **Goal**：Codex 持久化在 Thread 上的当前阶段性成果，可以跨多个 Turn 自动 continuation；一个
  Thread 同时只有一个当前 Goal。
- **Item**：Turn 内的输入、输出、推理、命令、文件变更、图片等事件。
- **CodexHost**：CodexLoom 维护的共享 `codex app-server` 进程。
- **Profile**：长期协作身份、Domain 和 Scope，不是本轮任务描述。
- **Skill**：可复用的工作方法；CodexLoom 内置 Skill 由共享 CodexHost 通过 extra roots 发现。
- **Relationship**：Agent 间显式长期关系。
- **Agent Message**：Agent 间需要或不需要回复的结构化通信。
- **Connection**：到外部平台账号或租户的连接。
- **Address**：某个 Agent 在一个 Connection 上的外部身份。
- **Conversation Membership**：Agent 在具体群或 DM 中的目的、角色、策略和边界。
- **Conversation Candidate**：Connector 观察到外部身份已经加入、但尚未获得 Loom 行为授权的会话；它只提供发现和配置入口。
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

Team 是组合读模型：Agent registry + Profile + Organization Relationship + Collaboration Relationship + Messages Activity。三个 Graph 是各自语义下的关系导航，不是真相源；Activity 也不负责直接编辑原始消息证据。

### Scheduler

`scheduler` 是稳定系统身份，不是 Codex Agent。Schedule 触发后生成标准 Agent Message，仍走
目标队列与回复协议，不直接绕过 Communication 调用 Turn。每次触发以
`(scheduleId, scheduledAt)` 标识一个 durable occurrence：先提交 Message，再推进 Schedule；崩溃后重放会复用同一条 Message，不会跳过或复制 occurrence。

### Disaster Recovery

负责压缩本地快照、重启前保护和历史淘汰。快照必须是普通 tar.gz，服务不可用时仍能手工恢复。

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
loom profile|team|msg|inbox|outbox|integration|prll|conversation|schedule|remote ...
```

CLI 不直接启动 Codex，也不读取业务数据文件，全部走 HTTP/SSE。
`main.go` 只保留参数解析、HTTP client 与命令分发；Agent、Integration、Messages、Governance、
Observe 命令分别放在 `commands_*.go`。新增命令必须归入所属领域，不得重新堆回 `main.go`。

### `internal/hub`

领域编排层。`Agent` 是规范实体，`Session` 只作为源码兼容别名；`Hub.agents` 保存聚合根。
`codex_host.go` 持有唯一 Host，并将通知按 Thread 路由；`goal.go` 直接适配 Codex 原生
`thread/goal/*`，只维护内存投影和队列保留语义；`remote.go` 只管理共享 Host 上的
`remoteControl/*` 状态，不再维护第二个 app-server。

核心文件按状态所有权划分：`hub.go` 是共享 runtime/event 基础，`agent.go` 管 Agent 与 Turn，
`communication.go` 管内部 Agent Message，`integration.go` 管 Connection/Address/Ingress，
`inbox.go` 管入站处理，`outbox.go` 管受治理的外部投递与 claim fencing，
`external_message.go` 管 envelope、附件与 policy 规范化，`shutdown.go` 管关闭顺序。跨聚合动作
必须通过明确的 reconciliation/commit helper，不应在一个巨型文件中直接修改多个 projection。

### `internal/httpapi`

HTTP 是 adapter，不拥有业务状态。`server.go` 只保留 Server、SSE、共享 response/helper；
System、Integration、Agent、Organization 和兼容 API 分别由 `routes_*.go` 注册。新增路由应调用
Hub application method，而不是在 handler 内直接组合持久化步骤。

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
provider-operations.ndjson
human-requests.ndjson
events/<agent-id>.ndjson
events/__global__.ndjson      WebUI 全局 SSE replay cache
events/*.ndjson.gz            已轮转的诊断段，按策略淘汰
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
- 全局 SSE 使用持久单调 seq 作为 SSE `id`，按 `Last-Event-ID` 重放。cursor 已被压缩时发送
  `loom/reconcile`，所有打开的 Agent pane 和治理页面重新读取权威快照/history，而不是静默漏事件。

### `web`

React/Vite/TypeScript/Tailwind 治理工作台。第一屏是实际工作台，不是 landing page。

- Sidebar：全局 active/idle 状态、Agent 列表和管理入口。
- AgentPane：Thread history/live、Turn 输入、配置、Profile、Address 和 Membership。
- Inbox/Messages：内部和外部消息统一观察，但保留 origin 区分和 raw XML。
- Team：列表、Graph 和 Inspector；Graph 卡片可拖拽，位置稳定保存。
- Remote：连接状态、配对码、二维码和设备列表。
- automation：规范入口 `window.codexLoom`，`window.codexHub` 是兼容别名。

### `cmd/loom-gateway` 与 `gateway/`

规范通用二进制名是 `loom-gateway`。Go fake provider 用于协议与幂等测试。`parall.mjs` 由 `loom-parall-gateway` 从 Keychain 加载每个外部 Agent 的独立凭据，并通过 launchd/systemd 托管；`lark.mjs`、`slack.mjs` 负责真实平台 cursor、ack、reaction/read-state 和发送。飞书由
`loom-feishu-gateway` 使用官方 SDK 和长连接托管；Slack 由 `loom-slack-gateway` 从 Keychain
读取 token，再通过 Socket Mode 运行 `slack.mjs`。Slack App manifest 位于
`gateway/slack-app-manifest.yaml`。平台 Secret 不得出现在 launchd/systemd 参数、plist/unit 或仓库文件中。

Slack CLI 负责 App 开发、安装和部署，Slack MCP 为 Agent 提供用户身份下的消息工具；它们都不消费
Events API，也不替代 Loom 的 Connection、Address、Membership 和持续运行 gateway。接入新平台时先区分
开发工具、Agent 工具和 Runtime Connector，不能因为平台提供 CLI/MCP 就跳过入站事件与幂等账本设计。

## 构建、运行与配置

前置条件：本机已安装 `codex` CLI，并使用 ChatGPT 身份完成登录。普通开发构建只编译 Go；
发布构建会先生成并提交 WebUI dist，再构建内嵌静态资源的二进制：

```sh
make build
make release
./bin/codex-loom
```

默认服务地址是 <http://localhost:4870>。`make release` 生成规范入口和迁移期兼容入口：

```text
bin/codex-loom             bin/codex-hub             compatibility
bin/codex-loom-reloader    bin/codex-hub-reloader    compatibility
bin/loom                   bin/chub                  compatibility
bin/loom-gateway           bin/chub-gateway          compatibility
bin/loom-feishu-gateway
bin/loom-slack-gateway
```

规范环境变量：

```text
CODEX_LOOM_PORT             HTTP 端口，默认 4870
CODEX_LOOM_DATA             数据目录，默认 ~/.codex-loom
CODEX_LOOM_URL              CLI 和 gateway 连接的服务地址
CODEX_LOOM_CODEX_BIN        共享 CodexHost 使用的 codex 可执行文件
CODEX_LOOM_ADMIN_TOKEN      非 localhost 管理操作令牌
CODEX_LOOM_CONNECTOR_TOKEN  gateway 连接令牌
CODEX_LOOM_BACKUP_MIN_KEEP  恢复底线，始终保留的最新快照数，默认 2
CODEX_LOOM_BACKUP_KEEP      本地快照最大数量，默认 5
CODEX_LOOM_BACKUP_MAX_GB    本地快照总容量上限，默认 2 GiB
CODEX_LOOM_BACKUP_MAX_AGE_DAYS  快照最长保留天数，默认 30
CODEX_LOOM_EVENT_ACTIVE_MB   每个 active SSE replay log 轮转阈值，默认 64 MiB
CODEX_LOOM_EVENT_REPLAY_COUNT  active log 保留的最新事件数，默认 10000
CODEX_LOOM_EVENT_REPLAY_MB   active replay window 最大读取量，默认 32 MiB
CODEX_LOOM_EVENT_ARCHIVES    每个 Agent 保留的压缩诊断段数，默认 2
CODEX_LOOM_EVENT_ARCHIVE_MB  每个 Agent 压缩诊断段总量上限，默认 128 MiB
CODEX_LOOM_EVENT_ARCHIVE_DAYS  压缩诊断段最长保留天数，默认 7
```

对应的 `CODEX_HUB_*` 和 `CHUB_URL` 只作为迁移期兼容别名。新脚本、部署文件和文档必须使用
`CODEX_LOOM_*`。

日常 CLI 入口是 `loom`：

```sh
loom agent create research --cwd /path/to/repo
loom agent list
loom thread send research "检查当前领域状态"
loom thread watch research
```

Thread 输入不只包含文本。WebUI 或 CLI 上传的图片和文件先进入 Agent 所有的受管 Artifact store；图片映射为 app-server `localImage`，普通文件通过结构化附件清单提供稳定本地路径。Agent 生成的文件用 `loom artifact publish --from AGENT --file PATH` 快照并回显到 trajectory。详细契约见 [Thread Artifacts](thread-artifacts.md)。

完整 Agent 通信、Remote、Integration、Schedule 和 Backup 命令见
[loom-cli.md](loom-cli.md)。

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
7. Turn 结束更新 Agent idle，并由 Hub-owned worker 或持久队列投递下一封消息；所有异步 worker
   都登记到 lifecycle，`Shutdown()` 会等待它们退出。

### Goal 跨 Turn 自动继续

1. Web/CLI 调 `thread/goal/set` 创建 Goal，或 Agent 在 Turn 中使用 Codex 原生 Goal tool。
2. `thread/goal/updated` 更新 `AgentView.goal`；Goal 本体只由 Codex state DB 和 rollout 持久化。
3. status=`active` 时，Codex 在 Thread idle 后自动创建 continuation Turn。Loom 不手工循环
   `turn/start`，也不复制 Goal objective。
4. Loom 重启后先用 `thread/goal/get` 恢复投影，再对 active Goal `thread/resume`；paused、blocked、
   limited 和 complete Goal 只恢复显示，不自动启动。
5. 只有 `active` Goal 在两个自动 continuation Turn 之间保留下一次执行权。普通
   Inbox/Schedule/Agent Message 排队；Goal 中发出的 required 请求之回复和 Needs You 回答
   可以继续进入该 Thread。
6. `paused`、`blocked`、`usageLimited`、`budgetLimited` 和 `complete` Goal 继续显示并保留历史，
   但不占用 Agent：新消息可以正常投递，尤其可能为 blocked Goal 提供解除阻塞的信息。Restart Loom
   只等待当前 active Turn；重启后的 active Goal 由 Codex 继续。

### Remote 启动 Turn

1. Codex Mobile/Desktop 经 Remote backend 连接共享 app-server。
2. app-server 发 `turn/started`；Loom 按 Thread 找到或收养 Agent。
3. `userMessage` Item 更新 `currentTask`；Agent 状态变 running。
4. 所有 Item delta/completed 进入同一 Agent SSE。
5. `turn/completed|failed|aborted` 更新 idle，WebUI/CLI 无需刷新。

### History 与 Live

- History：`/thread/history` 直接解析 rollout，能看到 Desktop/Mobile/其他连接产生的完整历史。
- Rollout turn offset index 按文件 size 增量更新；分页只解析目标字节窗口，成本不再随 Thread 全部历史线性增长。
- Live：`/thread/events?replay=0` 只接打开后的事件，避免与 history 重复。
- CLI watch 可按 seq 回放 live event tail，并用 `Last-Event-ID` 续传。
- event log 只保留有界 replay window，不是完整历史，不能用它重建上下文；完整历史仍以 rollout 为准。

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
- envelope 显式携带来源时间与本次投递时间；外部消息另带 Loom 接收时间，Agent 不依赖对话位置猜测时序。
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

- `codex-loom/`：registry、Profile、Team、Schedules、Communication/Inbox/Outbox ledgers；不包含可重建的 SSE event cache。
- `codex-sessions/`：当前可见 Agent 对应 rollout。
- `pinix-edge/names.json`（存在时）。
- `codex/config.toml`（存在时）。
- `manifest.json`：Agent/Thread 清单、源路径、文件列表和 warning。

快照使用 `tar.gz` 打包压缩。创建新快照前后都会执行组合保留策略：至少保留最新 2 份，同时受
最大 5 份、总量 2 GiB、最长 30 天约束；恢复底线优先于容量和时间限制。可以通过上述环境变量
调整策略，也可以执行 `loom backups prune` 立即应用当前策略。淘汰结果会返回删除数量与释放空间。

Event cache 独立轮转：active NDJSON 保留最新 replay window，旧段压缩为 gzip 后按数量、总字节和
年龄组合淘汰。备份与 Event cache 都是派生运维数据，不能替代 Codex rollout 或业务 ledger。

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
- Graph 是 read projection；Organization 以 `organization-links.json` 为准，Collaboration 以 `team-links.json` 为准，Activity 以原始 Message ledger 为准。

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

完整的桌面/移动 viewport、`pinixc`、结构化断言、真机和验收记录方法见 [WebUI 检查与移动端验收](webui-validation.md)。

前端基础约束：

- HTTP 快照由 TanStack Query 持有；全局 SSE 直接更新同一 Query cache，不再为同一资源维护平行
  `useState`。
- Button、Dialog、Input、Separator 等基础交互优先使用 shadcn 组件；业务组件只负责组合与领域状态。
- 桌面 sidebar 的 expanded/compact 状态可以持久化，但移动端始终使用完整 drawer，不能继承桌面窄栏。
- 折叠后必须保留可发现的恢复入口；不能要求用户清 localStorage 或猜测隐藏热区。

### Team Graph 经验

- React Flow 负责 pan/zoom/selection/drag，不手写 hit-test。
- Graph 是拓扑导航，列表和 Inspector 仍是精确操作入口。
- Organization、Collaboration 和 Activity 必须是三个独立投影，不能靠不同线型混在一张图里让用户猜语义；外部平台连接留在 Integrations。
- Organization 是持久化的 parent/child 树：每个 Agent 最多一个直接上级并拒绝环路；未进入树的 Agent 放在独立 Unassigned 列表。
- Collaboration 是声明的长期跨领域关系，不代表上下级；Activity 是 Messages 在明确时间窗口内产生的只读证据，双向通信在画布上聚合成一条边。
- Activity 默认只展示风险优先、消息量较高的有限边数；全量历史留给 Messages 和 Inspector，不能把 ledger 全塞进画布。
- 布局要 deterministic；用户拖拽位置按 Agent stable ID 持久化，刷新不能随机跳。
- 不同关系视图的拖拽位置要分别持久化，不能让 Organization 的空间安排污染 Activity。
- 节点卡片只显示名称、状态、Domain 摘要和计数；长内容进 Inspector。
- 默认过滤低权重边；选择节点时突出 1-hop 邻域。
- 移动端默认 Directory，Graph 是可横向 pan 的工作区，Inspector 用 sheet。
- 两 Agent fixture 只能验证交互，不能证明布局；至少再用 20+ Agent 和高边密度 ledger 截图。
- automation 至少暴露当前语义视图、nodes/edges、selection、filters、visible IDs、fitView、focusAgent；Directory 不能误报隐藏 Graph 的节点数。

### Token Usage 口径

- 用量读取 Codex rollout 的 `event_msg/token_count`，不依赖浏览器在线，也不从 Loom live event 日志反推。
- `total_token_usage` 是 Thread 累计快照，`last_token_usage` 是最近一次模型调用。聚合必须对各 token 维度维护累计高水位，只记录超过历史最大值的增量；app-server 重连和多端交错写入可能产生重复或较旧快照，直接累加 `last`、相邻差值或把回退视为新分段都会重复计数。
- `cachedInputTokens` 已包含在 `inputTokens` 中，`reasoningOutputTokens` 已包含在 `outputTokens` 中；总量只计算 input + output。
- Usage 的主查询使用包含首尾日期的日历窗口 `from/to`，并显式携带 IANA 时区。`Day` 可以选择任意历史日期；7/30/90 日只是以选定结束日期回溯的快捷方式，`Custom` 可以选择最多 366 天的任意范围。包含今天的窗口截止当前时刻并标记 `live`，而不是假装今天已经结束。旧的 `days=N` 查询继续兼容现有调用方。
- 每个窗口同时计算紧邻的等长前一周期。Live 窗口的前一周期只计算到最后一天的相同时刻，避免用未结束的今天与完整历史日直接比较。
- WebUI `#usage` 用 KPI 表达总量，用逐日柱状图表达时间分布，用 Treemap 的矩形面积表达 Agent 占比，用堆叠条表达 token composition，并用表格提供精确比较。Treemap 可以按显式 Organization root 分组；没有 Organization Relationship 的 Agent 归入 Independent。
- Agent 当前 Context 是实时 Thread 快照，不属于历史查询窗口，只能显示在选中 Agent Inspector 中，不能与周期统计列混排。
- 全局 Usage 只聚合当前 Agent registry 中的 Agent。
- Token usage 是模型调用量，不等于货币成本或订阅额度。模型价格、Codex 计划和缓存计费没有可靠映射时，不展示推测成本。
- `/api/usage` 返回全局聚合，`/api/agents/{key}/usage` 返回单 Agent 聚合；history Turn 同时携带该 Turn 的 `model` 与 `usage`。

### Agent 负载与容量信号

Organization → Usage 的 **Capacity signals** 是组织诊断入口，不是自动拆分、合并或绩效判断。它把两类
事实放在一起：Codex Thread 实际执行了多久，以及进入 Loom durable queue 的工作等待了多久。

- **Executing** 来自 Codex rollout 的 `task_started` 到 `task_complete` / `turn_aborted` 区间，因此
  WebUI、CLI、Codex Desktop 和 Mobile 在同一 Thread 上发起的 Turn 都进入统计。相邻或重叠区间会合并，
  新 Turn 出现但上一 Turn 没有终止事件时，上一段以新 Turn 的开始时间作为推断终点并计入数据质量。
- **Idle proxy** 是所选日历窗口减去 executing 的剩余比例。Loom 尚未持久化机器、Hub、Codex Host 的
  历史在线区间，所以它包含离线、睡眠和服务停机，不能解释成“Agent 在线但无事可做”。没有可读 rollout
  的 Agent 不进入全局 observed 分母，UI 用 coverage 明确显示可观测 Agent 数量。
- **New-work wait** 只计算工作进入 durable queue 到首次开始处理之间的时间，不包含 Turn 处理时长。MVP
  覆盖 root internal message、external Inbox、Schedule 和已回答的 Needs You；进入正在运行 Turn 的因果
  reply 单列为 continuation，不混入 new-work p50/p90。
- **Current backlog** 是查询时仍 queued / delivering / deferred / handling 的工作。它始终是实时快照，
  不会为历史日伪造当时的 backlog，也不受所选样本窗口裁剪，因而不会漏掉更早进入队列但至今未开始的工作。
  Evidence 保留稳定记录 ID、来源、目标 Agent、排队时间、当前原因和跳转入口。
- 历史 ledger 没有保存每一刻的等待原因，因此历史样本只可靠表达等待时长；当前 backlog 才能投影
  `agent_busy`、`active_goal`、`restart_drain`、`deferred_until` 或 `ready_pending_dispatch`。旧 internal
  message 若没有 handling attempt，会以 delivery acceptance 估算首次开始时间，并在 Data quality 中披露。
- Turn 执行区间包含模型、工具、网络和审批等待。它表达 Thread 占用，不表达人的认知负担，也不能单独证明
  Scope 应拆分或合并。组织操作者应结合 backlog 趋势、等待分位数、真实事故与 Domain 边界共同判断。

Capacity 与 Token Usage 使用同一套包含首尾日期的日历选择器：`Day` 可选任意历史日，7/30/90 日以
所选结束日回溯，`Custom` 支持最多 366 天；多日执行柱可点击下钻到单日。稳定入口是 WebUI
`#capacity?mode=7d&from=YYYY-MM-DD&to=YYYY-MM-DD&tz=Area/City`、
`GET /api/workload?from=...&to=...&tz=...`，以及
`GET /api/agents/{key}/workload?from=...&to=...&tz=...`。旧的 `days=1|7|30|90` 继续兼容 CLI 和现有调用方。
接口返回 `live` 与 `dataQuality`；新增数据源时必须同步更新限制说明与测试。

### 重启纪律

承载当前开发 Agent 的服务不能由 Agent 直接 kill 自己。正确流程：

1. 完成 build。
2. canary 验证。
3. 告诉用户点击 Restart Loom。
4. restart API 先做 `pre-restart` 备份。
5. active Agent、尚在 claim lease 内的 Outbox send 或 ProviderOperation 存在时进入 waiting，拒绝
   新 Turn、新 root Agent Message 和新 Connector claim。Hub 进入 drain 后不再领取 queued Agent Message、
   Inbox、Needs You answer、Outbox 或 ProviderOperation，防止旧进程在收尾期间被队列持续续占；这些
   durable work 由新进程继续。当前 required 工作的因果 reply 仍可通过 `turn/steer` 进入原 Turn，
   否则 restart waiting 会与当前 Turn 的回复义务形成死锁。备份或 reloader 启动失败时必须取消 drain。
6. 独立 `codex-loom-reloader` 在 HTTP response 返回后 SIGTERM 旧服务。服务先停止 HTTP，再等待
   scheduler/inbox/delivery/remote/event loops 和所有已登记 worker，最后关闭共享 CodexHost。
7. reloader 最多给旧进程 60 秒优雅退出，再启动同一 executable。
8. 新 Hub 对每个 enabled managed gateway 执行 launchd `bootout` + `bootstrap` + `kickstart`，不能只
   `kickstart`：gateway binary 被 build 替换后，launchd 的 cached lightweight code requirement 会失效，
   单纯 kickstart 会以 `EX_CONFIG (78)` 退出。完成标准是 Connection heartbeat 恢复为 `connected`。

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

### Agent 停止与归档纪律

停止当前工作使用 `loom thread interrupt <agent>`，它只中断 active Turn。归档长期 Agent 使用
`loom agent archive <agent>`，它会中断 Turn、调用 Codex `thread/archive` 并从 Agent registry
删除该 Agent。顶层 `loom/chub kill` 禁用，避免把暂停误操作成归档。

如果 active Turn 由内部 Agent Message 启动，interrupt 只结束当前 handling attempt。消息已经进入
Codex Thread，因此 Delivery 继续是 `delivered`，Handling 变为 `interrupted`，不会重新排队。
操作者可以在 Agent 输入区点击 Continue，或执行 `loom msg retry <message-id>`，以同一 message id
建立新的 attempt；也可以查看消息或由接收方显式 No reply。这个边界避免 Stop 触发无限重投，同时保留
原请求和完整处理轨迹。

误归档恢复必须保留原 `agentId` 和 `threadId`：先创建备份，确认没有待投递消息，再调用 Codex
`thread/unarchive`，最后通过 `POST /api/agents/restore` 重新登记原身份。Profile 和 Team
Relationship 按 stable ID 独立保存，会自动重新关联；恢复只进入 idle，不启动 Turn。

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
POST   /api/agents/restore
GET    /api/agents/{key}
PATCH  /api/agents/{key}/config
GET    /api/agents/{key}/profile
PUT    /api/agents/{key}/profile
GET    /api/agents/{key}/usage?from=YYYY-MM-DD&to=YYYY-MM-DD&tz=Area/City
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
GET    /api/usage?from=YYYY-MM-DD&to=YYYY-MM-DD&tz=Area/City
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
