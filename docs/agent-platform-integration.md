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

### ConversationMembership

Address 在一个具体群、频道或 DM 中的长期上下文：

```text
id, addressId, conversationId, displayName,
purpose, role, guidance, triggerPolicy, replyPolicy,
trustDomain, enabled, version
```

- `purpose`：这个会话长期讨论什么。
- `role`：Agent 在这里扮演什么角色。
- `guidance`：该说什么、不该说什么、何时交接。
- `triggerPolicy`：DM、mention、dispatch 或受控主动处理。
- `replyPolicy`：例如只发送 final answer，或要求显式 reply/no-reply。

Membership 是行为上下文，不是安全沙箱。不同信任域不应共享同一个高权限 Agent Thread。

### InboxMessage 与 InboxItem

`InboxMessage` 是平台无关的入站事实，保留 sender、conversation、content、attachments、replyTo、
responseExpectation、providerMetadata 和原始 external key。

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
引用 InboxItem；主动发送必须指定 Address 和 Conversation。

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
  <sender provider="lark" external_id="ou_..." display_name="Alice" />
  <conversation id="oc_..." type="group" display_name="Product" />
  <subject><![CDATA[...]]></subject>
  <body><![CDATA[Markdown body]]></body>
  <reply_command>loom inbox reply inb_... --agent my-agent --body "..."</reply_command>
</inbox_message>
```

UI 将 envelope 渲染成带 origin 的消息卡片，body 走 Markdown；raw XML 可展开审查。Agent 内部
`<agent_message>` 也遵循同样原则，并区分 REQ、RES、NOTIFY。

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
future Slack socket ───┘
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
- provider 的 dispatch-effect idempotency 前缀与普通 Outbox key 分离。
- queued 时不标记 reading；HandlingAttempt 开始后才 mark received。
- reply 发送成功或显式 no-reply 后才 ack。

### 飞书

- 外部群可添加自定义机器人或开启对外共享的机器人；仍必须按 Address/Membership 限制范围。
- 收到并开始处理时添加 eyes reaction；reply/no-reply/failed 终态后移除。
- mention 必须使用平台结构化 mention，不把正文里的 `@名字` 当真实触发。
- 飞书群机器人 webhook 只能主动推送，缺少完整事件订阅时应建成 send-only Connection，不伪装成
  双向 Bot。

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
loom integration bind <agent> <connection-id> --identity <external-id> ...
loom conversation set <address-id> <conversation-id> --purpose ... --role ... --guidance ...
loom inbox [agent] --origin <provider>
loom inbox reply|no-reply|defer|retry ...
loom outbox [agent]
loom outbox send|retry ...
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
- 飞书/Parall 的 read、reaction、ack 能力不完全对称，统一领域只保证业务语义，不伪造平台能力。
- Slack 仅有领域抽象和 connector 边界，尚未实现 adapter。
