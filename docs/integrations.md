# Integrations

CodexLoom 的 Integration 把外部 IM 变成 Agent 组织的通信边界。平台账号不是 Agent 本体；
Connection、Agent Address 和 Conversation Membership 分别回答三个问题：

1. **Connection**：Loom 通过哪个平台应用或机器人连接外部组织。
2. **Agent Address**：这个外部身份绑定到哪个长期 Agent，采用什么触发、回复和信任策略。
3. **Conversation Membership**：同一个 Agent 面对某个具体群、channel 或私聊联系人时的目的、角色和行为约束。

凭据不写入 `integrations.json`。Connection 的 `credentialRef` 只是运维引用，真实 token 由独立
gateway 进程从操作系统 Keychain、环境变量或受权限保护的 env file 读取。

## 通用流程

1. 在平台创建 Bot/App，并开启所需事件和权限。
2. 在 WebUI 的 **Integrations** 新建 Connection。
3. 在该 Connection 下绑定一个 Agent Address。
4. 在 Agent Config 的 **Connections** 中为允许进入的群或 channel 创建 Conversation Membership。
5. 由 CodexLoom 自动托管 gateway，或按 Integrations 页面生成的命令手动启动，并确认状态变为 `online`。
6. 在外部平台发 DM 或结构化 @，从 Inbox、Agent Thread 和 Outbox 检查完整处理轨迹。

一个 Connection 对应一个平台应用身份。一个 Agent 可以绑定多个平台 Address；这些 Address 必须
处于兼容的 trust domain。不同组织或隐私边界应使用不同 Agent，而不是把 allowlist 当作唯一隔离。

## 统一出站发送

Agent 不直接调用 `lark-cli`、Slack API 或 Parall CLI。所有外部消息都通过 Loom 发送，由 Loom 根据
Inbox 或 Membership 解析平台身份、会话、thread、权限和审计记录：

```sh
# 回复一条外部 Inbox 消息。路由从 Inbox 继承。
loom integration send --from AGENT --reply-to INBOX_ITEM_ID \
  --body-file /absolute/path/to/reply.md \
  --file /absolute/path/to/image.png \
  --file /absolute/path/to/report.pdf

# 主动向一个受治理的 Membership 发送。
loom integration send --from AGENT --to MEMBERSHIP_ID \
  --body "Status update" \
  --file /absolute/path/to/report.pdf \
  --expect-reply none \
  --idempotency-key stable-business-operation-id
```

`--reply-to` 与 `--to` 必须二选一。主动发送只接受已启用且 `outboundPolicy=proactive` 的 Membership；
不能绕过治理直接传 provider conversation ID。`reply_only` 是默认值，允许回复已进入 Inbox 的消息，
但禁止主动发起；`none` 禁止该 Membership 的全部出站消息。CLI 默认等待 Connector 返回平台结果，
需要只排队时显式传 `--async`。消息正文先发送，附件按多个 `--file` 的顺序发送；Outbox 保存正文
message ID，并为每个 Loom Artifact 保存 provider attachment ID 与对应 message ID。Connector 缺少任一
附件回执时整条 Outbox 必须是 `failed`，不能只因正文成功就标记 `sent`。一次最多 8 个附件，单个不超过 25 MB。

Restart Loom 会同时重启已启用的 managed Feishu、Slack 和 Parall gateway，使 Hub、WebUI 与 Connector
使用同一次构建。未由 Loom 托管的外部进程不会被自动接管。

## 飞书 / Lark

推荐从 WebUI 的 **Integrations → Add integration** 开始。飞书使用 CodexLoom 内置的原生 Go
gateway 和飞书官方 SDK，不依赖 `lark-cli`。首次连接需要从飞书开发者后台复制 App ID 和 App
Secret；CodexLoom 验证身份后将 Secret 写入操作系统 Keychain，并自动识别 Bot 身份及已加入的群。

用户只需要：

1. 在飞书开发者后台创建企业自建应用，启用机器人、长连接事件订阅和消息/表情权限。另开启 `im:chat.members:read`，让 Loom 能把成员 Open ID 解析为姓名；没有该权限时消息仍可正常收发，但发送者会显示为 `ou_...`。
2. 在向导中输入 App ID 和 App Secret；Secret 只需输入一次。
3. 选择代表这个飞书身份的长期 Agent。
4. 选择允许 Agent 工作的群，或者只启用私聊。
5. 描述群的用途、Agent 在群里的职责和需要遵守的边界。
6. 保存后点击群右侧的发送按钮；只有测试消息真实经过 Outbox 和 gateway 发到飞书，UI 才显示成功。

一个飞书 App 身份只能归属于一个 Agent。同一个 Agent 可以进入多个群，但每个群都有独立的
Conversation Membership。App ID、Open ID、credential ref 和 gateway 信息默认收在 **Advanced
settings** 中，需要排障或自动化时再展开。

### 私聊访问策略

每个 Agent Address 有独立的私聊策略：

- `open`：任何人都可以触发 Agent；已经配置的联系人仍使用自己的 Conversation Membership。
- `managed`：只有配置过的联系人可以触发 Agent。陌生人的第一条消息保存为 `pending_access`，供管理员查看，但不会投递给 Agent。
- `closed`：拒绝全部私聊消息。

联系人配置和群配置使用同一个版本化模型，包含关系用途、Agent 对此人的角色、行为边界、触发方式和回复方式。Membership 同时绑定稳定的 conversation ID 与 actor ID，防止其他身份冒用已有私聊。管理员批准待授权联系人时，Loom 创建 Membership 并重新投递其第一条消息；此后每条消息都会携带该联系人的最新 `conversation_context`。

可信 DM 如果需要发送本地图片或文件，应使用 `explicit` 回复策略，并在 Membership guidance 中写清楚允许的来源、接收者和用途。Agent 会收到受治理的 `reply_with_attachment_command`，使用：

```sh
loom integration send --from <agent> --reply-to <inbox-item-id> \
  --body "说明" --file /absolute/path/to/image.png
```

飞书 Connector 将图片作为原生 image message、其他附件作为原生 file message 发送。图片不超过
10 MB，其他文件遵循 Loom 的 25 MB 上限。

向导保存后，CodexLoom 会在 macOS 使用 launchd、在 Linux 使用 user systemd 托管
`loom-feishu-gateway`。gateway 不在启动参数或配置文件中携带 App Secret，而是按 App ID 从
Keychain 读取。需要手动诊断时可以运行：

```sh
./bin/loom-feishu-gateway \
  --connection <connection-id> \
  --address <address-id> \
  --app-id cli_your_app_id
```

gateway 通过飞书长连接监听 `im.message.receive_v1`，因此不需要公网域名或 webhook。DM 按 Address 的私聊策略进入；
群消息必须先建立 Conversation Membership，并按结构化 mention 判断是否 @ 当前 Bot。消息进入 Agent
队列后添加 eyes reaction，reply/no-reply/failed 结束后移除。

已有 `gateway/lark.mjs` 部署可以在迁移期间继续运行。新向导安装原生 gateway 时会停止同一
Connection 的旧 launchd 服务，避免一条飞书事件被重复消费；确认原生收发正常后即可删除旧服务。

飞书自定义群机器人 webhook 只有发送能力，不等价于完整 Bot。需要双向通信时必须使用可订阅消息
事件的应用机器人。

## Slack

推荐从 WebUI 的 **Integrations → Add integration → Slack** 开始。Slack 使用 Socket Mode，因此不需要公网域名或 webhook；CodexLoom 直接管理 Socket Mode gateway，也不依赖 Slack CLI 或 Slack MCP。

首次连接需要先在 Slack App 管理页导入：

```text
gateway/slack-app-manifest.yaml
```

然后在 Slack 完成：

1. 安装或重新安装 App 到目标 Workspace，取得 Bot User OAuth Token（`xoxb-...`）。
2. 在 **Basic Information → App-Level Tokens** 创建具有 `connections:write` scope 的 token（`xapp-...`）。
3. 确认 **Socket Mode** 已启用。
4. 在 Slack 里把 Bot 邀请进允许它工作的 channel；私有 channel 必须先邀请，Loom 才能发现它。

回到 CodexLoom 向导：

1. 输入 Bot token 和 App token。Loom 会先验证 Bot、Workspace、Socket Mode 和 scope，再把 token 写入操作系统 Keychain。
2. 选择代表这个 Slack 身份的长期 Agent。
3. 选择一个 Bot 已加入的 channel，或者先建立仅私聊连接。
4. 描述 channel 的用途、Agent 在其中的职责和行为边界。
5. 保存后在 Connection 详情继续添加 channel；每个 channel 都有独立 Conversation Membership。

如果向导显示缺少 scope，先在 Slack **OAuth & Permissions → Bot Token Scopes** 补齐提示的权限，再 **Reinstall to Workspace**。只有重新授权后的 token 才具备新增权限，随后点击向导中的 **Check again**。Loom 不会把无权限导致的空列表误报为“没有 channel”。

一个 Slack App 身份只能归属于一个 Agent。同一个 Agent 可以进入多个 channel，也可以同时绑定飞书、Parall 等其他平台身份。Workspace ID、Bot User ID、credential ref 和 gateway 信息默认收在 **Advanced settings** 中。

### 消息边界

Slack DM 遵循 Agent Address 的私聊策略：`open`、`managed` 或 `closed`。`managed` 模式下，陌生人的第一条消息进入待授权列表，不会直接投递给 Agent；批准后可像群配置一样设置这个人的关系用途、Agent 角色、行为边界、触发方式和回复方式。

Channel 消息必须结构化 @ Bot，并且该 channel 已启用 Conversation Membership。回复默认进入原消息 thread；DM 不强制创建 thread。gateway 收到消息后添加 `:eyes:`，Agent 成功回复、明确 no-reply 或处理失败后移除。

Slack 附件使用官方 `files.getUploadURLExternal` + `files.completeUploadExternal` 流程，App 必须具有
`files:write`。正文先通过 `chat.postMessage` 发送，附件随后按顺序共享到同一 channel/thread。

### 运行与排障

向导保存后，CodexLoom 会在 macOS 使用 launchd、在 Linux 使用 user systemd 托管 `loom-slack-gateway`。gateway 的配置文件和进程参数不包含 token；wrapper 按 Slack App ID 从 Keychain 读取凭据，再启动 `gateway/slack.mjs`。Connection 的 heartbeat、last event 和 last error 可在 Integrations 页面检查。

需要手动诊断时可以运行：

```sh
./bin/loom-slack-gateway \
  --connection <connection-id> \
  --address <address-id> \
  --app-id A_YOUR_APP_ID \
  --team-id T_WORKSPACE \
  --bot-user-id U_BOT_USER
```

已有直接运行 `gateway/slack.mjs` 的 launchd 部署可以在迁移期间继续工作。第一次通过新向导保存设置时，Loom 会先创建托管 gateway；启动成功后才停止同一 Connection 的旧 gateway，避免消息中断或重复消费。

Slack CLI 适合创建、安装和部署 Slack App，但不是运行时消息通道。Slack MCP 以用户 OAuth 身份为 Agent 提供搜索、读取和发送等工具，也不负责 Events API 或 Socket Mode 的持续事件接收。两者以后都可以成为可选辅助能力，但不替代当前 Connection、Address、Membership 和 gateway 模型。

## Parall

Parall 是一等 Connector。外部身份运行和 Organization 管理是两项独立能力：

1. 每个 Parall 外部 Agent 使用自己的 key 收发消息、打开 WebSocket，并读取“我已经加入的 Conversation”。这条运行路径不需要 Owner 权限。
2. Gateway 启动后每十秒检查一次已加入的 group。新群先进入 Loom 的 Conversation Candidate 目录，在 Integrations 中显示为“已加入 · 尚未配置”，不会自动触发 Agent。
3. 用户为候选群填写 purpose、role 和 guidance 后创建 Conversation Membership。默认保存为 paused；显式启用后该群才可以投递 dispatch。Direct message 不在这里预绑定，首次收到联系人消息后在 Direct messages 中按人审批和设置边界。
4. Owner key 是可选的管理能力，只在 Loom 需要创建或改名外部 Agent、创建 Agent key，或者主动把尚未入群的身份加入 Conversation 时使用。Owner 凭据不再决定一个现有外部身份是否“已连接”。
5. Loom 在 macOS 使用 launchd、在 Linux 使用 user systemd 托管 `loom-parall-gateway`。wrapper 根据 Organization ID 和稳定的外部 Agent ID 从 Keychain 读取凭据，再启动 `gateway/parall.mjs`。

两种密钥都不会写入 `integrations.json`、launchd plist、systemd unit 或进程参数。Address 使用稳定身份 `prll://<user-id>`，所以 Parall 显示名变化只会更新 UI 名称，不会破坏 Connection、Address、Candidate 或 Membership。外部身份离开群后，Candidate 会标为 unavailable，但历史 Membership 不会被自动删除。

如果外部 Agent 已由正式 Parall CLI 创建，并且一次性 Agent key 已写入当前用户拥有的 `0600` 文件，不需要再配置 Owner：

```sh
./bin/loom integration import parall \
  --agent ai-community \
  --org-id org_YOUR_ORG \
  --external-agent-id usr_YOUR_AGENT \
  --agent-key-file /absolute/path/to/parall-agent.key
```

这是导入已有身份的正式入口。Loom 会在写入 Keychain 前验证稳定身份和 WebSocket，幂等创建或复用 Connection/Address，并安装 managed Gateway。导入失败会恢复原 credential，并清理本次新建的配置；成功接管同一 Connection 后会卸载并删除旧 launchd gateway plist，避免下次登录时重复启动。CLI 只允许通过本机 loopback HTTP 或 HTTPS 传输 credential。命令不会删除源 key 文件；确认 `loom integration status <connection-id>` 为 `connected` 后再安全移除。群 Conversation 仍通过 Candidate 与 Membership 单独配置。

Keychain 已有该外部 Agent 凭据时，可用同一命令省略 `--agent-key-file` 做修复或迁移。导入器按 Parall 外部稳定身份和 Loom Agent 收敛记录：单一 legacy `org-agent:<external-id>` Connection 原地迁移，保留稳定 Connection/Address ID；若此前已经生成重复记录，则保留 managed 记录作为 canonical，复制更完整的 Membership 策略后归档旧 Connection、Address 和 Membership。归档对象保留 `supersededBy` 和历史 ID，只用于 Inbox/Outbox 审计，不能重新启用或接收 gateway heartbeat。WebUI 将它们与当前 Integration 分开显示。

Parall 的显式 dispatch、reading 和 ack 语义由 provider adapter 映射，不套用 Slack 或飞书的 reaction 规则。Gateway 会在 Hub 开始处理 dispatch 时报告 reading，在对应 Inbox 工作完成后 ack，并通过 Outbox command stream 回传最终回复。

当 dispatch 位于 thread 中时，Gateway 会在投递前通过自身受管凭据读取 thread root 和当前消息之前的最近回复，形成有界 `threadContext` 快照。Loom 校验 root ID 与当前 thread 一致后，将快照和 InboxMessage 一起持久化，并在 Agent `<inbox_message>` 与 Inbox Inspector 中显示。Gateway 默认保留 12 条、24,000 字符；Hub 的协议拒绝上限为 16 条、64 KiB。被裁剪时标记 `truncated`，Provider 读取失败时标记 `unavailableReason`，避免把缺失上下文静默当成完整上下文。

Agent 需要继续查看 Parall 上下文时，使用 Loom 的 credential-mediated 原生命令，而不是直接调用 `prll` 或 Parall API：

```sh
loom prll chats list --address addr_xxx
loom prll chats get cht_xxx --address addr_xxx
loom prll chats members list cht_xxx --address addr_xxx
loom prll messages list cht_xxx --address addr_xxx --thread-root-id msg_root
loom prll messages get msg_xxx --address addr_xxx
loom prll messages replies msg_root --address addr_xxx
```

命令沿用 Parall 的 `chats`、`messages` 和分页原语，输出原生 JSON。`--address` 明确本次使用的外部身份；Hub 校验 Address 与 Connection，managed Gateway 执行固定白名单中的只读 API，并把请求状态和结果写入 `provider-operations.ndjson`。Provider ID 可以进入 Agent 上下文，Agent key、Owner key 和 token 不会离开 Connector。发送与修改平台状态不属于 `loom prll`，仍走 `loom integration send` 或 Integration 管理流程。

需要手动诊断时可以运行：

```sh
./bin/loom-parall-gateway \
  --connection <connection-id> \
  --address <address-id> \
  --org-id org_YOUR_ORG \
  --agent-id usr_YOUR_AGENT
```

旧的环境变量模式仍可直接运行 `gateway/parall.mjs`，使用 `PRLL_API_URL`、`PRLL_API_KEY` 和 `PRLL_ORG_ID`。它只作为迁移和诊断入口；新配置应使用 Integrations 向导和托管 Gateway。

## 故障检查

```sh
./bin/loom integration list
./bin/loom integration status <connection-id>
./bin/loom conversation discover <agent> [--address <address-id>] [--all]
./bin/loom inbox <agent> --origin lark
./bin/loom inbox <agent> --origin slack
./bin/loom outbox <agent>
```

- `disconnected`：gateway 没有 command stream，先检查进程和 Connection/Address ID。
- `degraded`：查看 `lastError`；常见原因是 token、scope、Bot 未加入 channel 或平台限流。
- 外部消息被忽略：检查 Address trigger/allow/block policy，以及群聊是否有启用的 Membership。
- 已拉群但 UI 未出现：检查 Gateway heartbeat，然后运行 `loom conversation discover <agent>`；候选目录来自外部 Agent 自己的成员会话，不要求 Owner key。
- Agent 已回复但平台未出现：检查 Outbox 是否 `sent`；只有 provider 返回成功后 Inbox 才算完成。

完整领域语义见 [Agent 平台集成设计](agent-platform-integration.md) 和
[Conversation Membership](conversation-membership.md)。
