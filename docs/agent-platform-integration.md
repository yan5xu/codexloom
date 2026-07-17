# Agent 平台集成设计

## 目标

CodexLoom 把长期 Agent 当作组织中的独立协作者。一个人可以同时从多个 IM 账号、群和私聊收到
消息；Agent 也一样。平台连接只是通信入口，不是 Agent 本体：断开飞书、迁移 Parall 账号或
增加 Slack Address，都不应改变 Agent ID、Profile、Relationship 或 Codex Thread。

```text
Agent (stable agentId)
  ├── primary Codex Thread          思考、执行和长期上下文
  ├── Address on Parall             外部身份 1
  ├── Address on Lark               外部身份 2
  ├── Conversation Candidates       已加入但尚未授权的会话目录
  └── Conversation Memberships      每个群/DM 的目的和行为边界
```

一个 Agent 可以绑定多个 Address。一个 Address 可以加入多个 Conversation，但每个 Conversation
必须有独立 Membership，因为“在这个群里该说什么”不能只由全局 Profile 决定。

## 领域对象

### Connection

到一个平台账号、租户或应用的长连接配置，例如一个飞书 Bot 或 Parall Agent account。

```text
id, provider, accountRef, credentialRef, capabilities,
status, cursor, lastHeartbeatAt, lastError, enabled
```

`credentialRef` 只允许 `env:`、keychain 等引用，不保存 secret 明文。

### AgentAddress

Agent 在某个 Connection 上的外部身份。

```text
id, agentId, connectionId, externalIdentity, displayName,
triggerPolicy, replyPolicy, trustDomain, allow/block lists, enabled
```

跨领域关联只使用稳定 `agentId`。外部平台 ID 和 Agent 名称都不能作为 Agent 主键。

### ConversationCandidate

Connector 以 Address 身份从 Provider 读取“我已经加入的会话”，并向 Hub 上报完整快照。Candidate 保存稳定 conversation ID、显示信息、可用状态和首次/最近观察时间，但不参与消息路由。快照中消失的项标记为 unavailable，不自动删除历史 Membership。这个投影让用户可以先看到“已加入 · 尚未配置”，再决定是否建立长期行为边界。

### ConversationMembership

Address 在一个具体群、频道或 DM 中的长期上下文：

```text
id, addressId, conversationId, displayName,
purpose, role, guidance, triggerPolicy, replyPolicy, outboundPolicy,
trustDomain, enabled, version
```

- `purpose`：这个会话长期讨论什么。
- `role`：Agent 在这里扮演什么角色。
- `guidance`：该说什么、不该说什么、何时交接。
- `triggerPolicy`：DM、mention、dispatch 或受控主动处理。
- `replyPolicy`：例如只发送 final answer，或要求显式 reply/no-reply。
- `outboundPolicy`：默认 `reply_only`；只有 `proactive` 允许 Agent 主动向该 Membership 发消息。

Membership 是行为上下文，不是安全沙箱。不同信任域不应共享同一个高权限 Agent Thread。

### InboxMessage 与 InboxItem

`InboxMessage` 是平台无关的入站事实，保留 sender、conversation、content、attachments、replyTo、
responseExpectation、providerMetadata 和原始 external key。thread 触发还可以携带受限的 `threadContext`
快照：root、触发前的最近回复、是否截断及读取失败原因。快照属于外部用户内容，不是 Membership
规则或 developer context；它随 InboxMessage 持久化，使 Agent 输入、Inbox UI 和事后审计看到同一份事实。

`InboxItem` 是“某个 Agent 如何处理这条消息”的队列状态：

```text
queued -> handling -> handled
   |          |          ├── outcome=reply
   |          |          └── outcome=no_reply
   |          └── deferred
   └── failed / retry
```

同一消息未来可以路由给多个 Agent，因此 Message 和 Item 不能合并成一个表。

### HandlingAttempt

一次实际 Agent Turn 的处理记录，保存 `agentId`、`turnId`、Membership version、有效 reply policy、
final answer 和失败信息。旧 `sessionId` 只作为持久化兼容镜像。

### OutboxItem

平台无关的出站意图。状态为 `pending|sending|sent|failed`，使用稳定 idempotency key。回复必须
引用 InboxItem；主动发送必须指定 Address 和 Conversation。`deliveryReceipts` 将 Loom Artifact ID
关联到 provider attachment ID 和 message ID；带附件的结果只有在每个 Artifact 都有回执时才能进入 `sent`。

## 统一语言与边界

- Agent 是治理实体，Thread 是执行载体。
- Connection 不是 Agent，群也不是 Agent。
- Inbox/Outbox 是通信真相源，Codex rollout 是对话历史真相源。
- gateway cursor/ack 是平台状态，不能从 rollout 推断。
- Messages UI 可以组合内部与外部通信，但必须保留 `origin`、Address 和 Conversation 标识。
- 第一方 Agent Message 的规范 origin 是 `loom`；`chub` 只作为旧查询与深链兼容值。

## 入站流程

```text
platform event
  -> gateway normalize + external-key dedupe
  -> POST CodexLoom ingress API
  -> validate Connection / Address / allow-block / mention
  -> persist InboxMessage + InboxItem(queued)
  -> if Agent idle, reserve one HandlingAttempt
  -> inject Membership context + <inbox_message> into Agent Thread
  -> turn/start on shared CodexHost
  -> capture final_answer
  -> create Outbox reply or explicit no_reply
  -> provider send succeeds
  -> platform ack/read/reaction cleanup
```

每个目标 Agent 一次只处理一条消息。Agent busy 时保持 queued；Turn 结束后立即领取下一条。Hub
重启时，`handling` 恢复为 queued，`sending` 恢复为 pending，依靠 provider idempotency key
防止重复发送。

### Envelope

外部消息进入 Thread 时由 CodexLoom 生成 XML，调用方不手写：

```xml
<inbox_message version="1" id="inb_..." origin="lark" response="required">
  <timing sent_at="2026-07-15T08:30:00+08:00" received_at="2026-07-15T08:30:01+08:00" current_time="2026-07-15T08:35:00+08:00" />
  <sender provider="lark" external_id="ou_..." display_name="Alice" />
  <conversation id="oc_..." type="group" display_name="Product" />
  <subject><![CDATA[...]]></subject>
  <body><![CDATA[Markdown body]]></body>
  <reply_command>loom integration send --from my-agent --reply-to inb_... --body "..."</reply_command>
</inbox_message>
```

UI 将 envelope 渲染成带 origin 的消息卡片，body 走 Markdown；raw XML 可展开审查。Agent 内部
`<agent_message>` 也遵循同样原则，并区分 REQ、RES、NOTIFY。

`sent_at` 是消息在来源处发生或创建的时间，`received_at` 是 Loom 接收外部事件的时间，`current_time` 是本次向 Agent 投递时的参考时间。内部消息没有独立接收阶段，因此只携带 `sent_at` 和 `current_time`。同一消息排队或重投时，`sent_at` 保持不变，`current_time` 随实际投递更新。

## 回复语义

入站处理必须显式结束为以下之一：

- `reply`：形成 OutboxItem，平台发送成功后才算 handled。
- `no_reply`：业务上明确无需回复，gateway 才可 ack。
- `defer`：设置下次可处理时间和原因。
- `failed`：保留错误并允许 retry。

不能仅根据 final answer 是否像问句来猜 `no_reply`。特别是 Agent 对 Agent 的 Parall DM，多轮追问
必须保持可 dispatch；只有 response expectation 明确为 `none` 时，provider 才设置
`hints.no_reply=true`。这避免无界循环，同时保留正常多轮通信。

## 平台 adapter

gateway 是独立长生命周期进程，不嵌入 CodexLoom：

```text
Parall websocket/poll ─┐
Lark event consume ────┼── gateway adapter ── CodexLoom Integration API
Slack Socket Mode ──────┤
future Teams channel ───┘
```

这样平台 SDK 闪退不会拖垮 CodexHost，gateway 也可以独立升级。共享 connector protocol 提供：

- heartbeat 与 capability；
- 独占 command SSE stream；
- durable outbox claim；
- delivery result ack；
- connector token 鉴权。

规范变量为 `CODEX_LOOM_URL`、`CODEX_LOOM_CONNECTOR_TOKEN`、
`CODEX_LOOM_CONNECTION_ID`、`CODEX_LOOM_ADDRESS_ID`；旧 `CHUB_*` / `CODEX_HUB_*` 兼容。

### Parall

- WebSocket 用于被动接收 dispatch，HTTP/CLI 用于主动发送与恢复查询。
- thread 内的显式 dispatch 由 Gateway 使用受管 Agent key 读取 root 和 dispatch 之前的最近回复；Gateway
  默认保留 12 条、24,000 字符，Hub 的协议拒绝上限为 16 条、64 KiB，并以 `<thread_context>` 注入当前
  Inbox。读取失败会显式标记 unavailable，而不会把不完整上下文伪装成完整历史。
- 需要更多上下文时，Agent 使用 `loom prll chats|messages ... --address <address-id>`。这是 Loom 受治理的
  只读 provider surface：保留 Parall 原语和原生 JSON，由 managed Gateway 持有 key 并执行，Agent 不接触
  credential，也不能通过该入口发送或修改平台状态。
- provider 的 dispatch-effect idempotency 前缀与普通 Outbox key 分离。
- queued 时不标记 reading；HandlingAttempt 开始后才 mark received。
- reply 发送成功或显式 no-reply 后才 ack。

### 飞书

- 外部群可添加自定义机器人或开启对外共享的机器人；仍必须按 Address/Membership 限制范围。
- 收到并开始处理时添加 eyes reaction；reply/no-reply/failed 终态后移除。
- mention 必须使用平台结构化 mention，不把正文里的 `@名字` 当真实触发。
- 飞书群机器人 webhook 只能主动推送，缺少完整事件订阅时应建成 send-only Connection，不伪装成
  双向 Bot。

### Slack

- `gateway/slack.mjs` 使用 Socket Mode 接收 Events API，不要求公开 webhook URL。
- App-level token（`xapp`）只用于 `apps.connections.open`；Bot token（`xoxb`）用于 Web API。
- 入站 envelope 先写本地 pending state，再 ack Slack；Hub 暂时不可用时可继续重试 ingress。
- DM 直接触发，群消息按结构化 app mention 触发；群消息仍要求存在启用的 Conversation Membership。
- channel 回复默认进入原消息 thread，DM 保持普通消息；已有 `thread_ts` 时继续原 thread。
- 接受任务后添加 `eyes` reaction；reply 发送成功、no-reply 或 failed 后移除。
- Outbox 发送结果先写本地幂等状态，再回报 Hub，降低 ack 中断造成重复消息的风险。
- App manifest 位于 `gateway/slack-app-manifest.yaml`。Socket Mode 和 token 规则以
  [Slack 官方文档](https://docs.slack.dev/apis/events-api/using-socket-mode/)为准。

### Microsoft Teams（TODO）

- 领域模型、Connection、Address、Conversation Membership 与 gateway 边界沿用现有抽象。
- Teams adapter 尚未实现，不能在 UI 或 README 中标记为当前可用。
- 接入时应优先使用平台原生事件流、thread/reply、mention 和 reaction/read-state 能力。

## 信任与权限

路由顺序：block list > allow list > trigger policy > Membership > Agent queue。

- 外部消息不能提升 Agent sandbox 或 approval policy。
- provider metadata 不进入 developer context，除非经过明确白名单映射。
- attachment 下载必须有大小、类型、路径和生命周期限制。
- 高权限开发 Agent 不绑定不可信外部群。
- 同一 Agent 的多个 Address 必须处于可接受的信任域；跨组织或隐私域时创建独立 Agent。

## CLI 与 UI

```sh
loom integration connect <provider> ...
loom integration bind <agent> <connection-id> --identity <external-id> --dm-policy managed ...
loom conversation set <address-id> <conversation-id> --type group --purpose ... --role ... --guidance ...
loom conversation set <address-id> <dm-id> --type dm --actor <stable-actor-id> --purpose ... --role ... --guidance ...
loom inbox [agent] --origin <provider>
loom integration send --from <agent> --reply-to <inbox-item-id> --body ... [--file ...]
loom integration send --from <agent> --to <membership-id> --body ... --idempotency-key ...
loom integration send --from <agent> --to <membership-id> --message-id <provider-message-id> --thread-id <provider-thread-id> --body ... --idempotency-key ...
loom inbox no-reply|defer|retry ...
loom outbox [agent]
loom outbox retry ...
```

WebUI：

- Integrations 管 Connection 和 Address。
- Agent Config 的 Connections 管该 Agent 的 Address 与 Membership。
- Inbox 合并内部 `loom` 与外部 provider 消息，保留 origin badge。
- Outbox 展示 provider send 的最终状态和 retry。
- Agent Thread feed 渲染结构化 envelope，并可展开 raw XML。

## 不变量与测试

- external key 重放不得产生第二条 InboxMessage。
- 同一 Outbox idempotency key 重试不得在 provider 产生第二条消息。
- reply provider send 失败时 InboxItem 不能提前 handled/ack。
- busy Agent 的 FIFO 队列一次只领取一条。
- restart 后 handling/sending 恢复可重试状态。
- Address 禁用后不能新路由；历史仍可查看。
- Membership version 必须记录在 HandlingAttempt，便于解释当时行为。
- UI 必须覆盖长正文、附件、未知 sender、reply thread、no-reply、failed 和 raw XML。

## 已知限制

- 当前一个 Agent 只有一个主 Codex Thread，Membership 不是独立上下文隔离。
- gateway 最终投递受平台 API、权限和 backend 策略约束。
- 飞书、Slack、Parall 的 read、reaction、ack 能力不完全对称，统一领域只保证业务语义，不伪造平台能力。
- Slack 当前面向自托管和单组织安装；Socket Mode app 不能发布到 Slack Marketplace。
- Microsoft Teams 仅有领域抽象和 connector 边界，尚未实现 adapter。
