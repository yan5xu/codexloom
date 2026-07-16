# Conversation Membership

## 为什么需要

Agent Profile 说明“这个长期主体是谁、长期负责什么”；Conversation Membership 说明“它在这个
具体群或 DM 中为什么存在、扮演什么角色、应该怎样说话”。一个 Agent 可以绑定多个平台身份，
同一个 Address 也可以进入多个群，因此群级上下文不能写回全局 Profile。

```text
Agent Profile                  全局长期身份与 Scope
AgentAddress                   Agent 在平台上的外部身份
ConversationCandidate         Provider 观察到该身份已加入、但 Loom 尚未授权的会话
ConversationMembership        该身份在某个会话中的目的和行为边界
InboxMessage                   某次真实入站消息
HandlingAttempt                按某一 Membership version 执行的 Turn
```

`ConversationCandidate` 与 Membership 必须分开。Candidate 只证明外部身份已经加入该会话，不能触发 Agent，也不授予回复权限。Integrations 页面会把它显示为“已加入 · 尚未配置”；用户填写 purpose、role 和 guidance 后才创建 Membership，新 Membership 默认保持暂停，检查后再启用。

## 数据模型

```json
{
  "id": "mem_...",
  "addressId": "addr_...",
  "conversationId": "oc_...",
  "displayName": "CodexLoom test group",
  "purpose": "讨论 CodexLoom 的使用和组织接入。",
  "role": "回答产品和集成问题，收集可复现缺口。",
  "guidance": "只在被 mention 时回复；不披露其他群或私聊内容。",
  "triggerPolicy": "mention",
  "replyPolicy": "final_answer",
  "outboundPolicy": "reply_only",
  "trustDomain": "external-test",
  "enabled": true,
  "version": 3
}
```

字段：

- `purpose`：会话长期主题。
- `role`：Agent 在该会话中的职责。
- `guidance`：该说什么、不说什么、何时交接。
- `triggerPolicy`：`direct|mention|dispatch` 等进入队列的条件。
- `replyPolicy`：如何把 Agent Turn 结果映射为平台回复。
- `outboundPolicy`：`reply_only|proactive|none`，控制该 Membership 是否允许主动发起外部消息。
- `trustDomain`：审查和路由标签，不是沙箱实现。
- `version`：乐观并发和事后解释依据。

## 上下文注入

外部消息被领取时，CodexLoom 将 Membership 投影为 developer context，并将消息投影为
`<inbox_message>` user Item。示意：

```xml
<conversation_context version="3" membership_id="mem_...">
  <name><![CDATA[CodexLoom test group]]></name>
  <purpose><![CDATA[讨论 CodexLoom 的使用和组织接入。]]></purpose>
  <role><![CDATA[回答产品和集成问题，收集可复现缺口。]]></role>
  <guidance><![CDATA[只在被 mention 时回复；不披露其他群或私聊内容。]]></guidance>
</conversation_context>
```

```xml
<inbox_message version="1" id="inb_..." origin="lark" response="required">
  <timing sent_at="2026-07-15T08:30:00+08:00" received_at="2026-07-15T08:30:01+08:00" current_time="2026-07-15T08:35:00+08:00" />
  <sender provider="lark" external_id="ou_..." display_name="Alice" />
  <conversation id="oc_..." type="group" display_name="CodexLoom test group" />
  <body><![CDATA[请解释 Remote 和 WebUI 如何同步。]]></body>
  <reply_command>loom integration send --from support --reply-to inb_... --body "..."</reply_command>
</inbox_message>
```

`HandlingAttempt.membershipVersion` 记录实际使用的版本。Membership 在 Turn 运行中更新不会改变该
Turn；下一条消息才使用新版本。

## 触发规则

- DM 可以按 Address policy 直接进入。
- 群消息默认要求平台结构化 mention 或明确 dispatch。
- 正文中手写 `@名字` 不算 mention。
- block list 优先于 allow list；同时设置 actor 与 conversation allowlist 时必须同时满足。
- Membership disabled 或缺失时，要求 Membership 的群消息进入 failed，不偷偷使用全局 Profile。

## 回复规则

- `replyPolicy=final_answer`：只把正式 final answer 发送到平台，thinking/tool output 不发送。
- `outboundPolicy=reply_only`：可回复该 Membership 进入 Inbox 的消息，但不能主动发起；这是默认值。
- `outboundPolicy=proactive`：除回复外，还允许使用 `loom integration send --to mem_...` 主动发送。
- `outboundPolicy=none`：禁止该 Membership 的全部出站消息。
- `responseExpectation=required`：必须 reply、no-reply、defer 或 failed，不允许静默 ack。
- `responseExpectation=none`：可以显式 no-reply；Parall 才可设置 `hints.no_reply=true`。
- provider send 成功后才将 InboxItem 标成 reply handled 并执行平台 ack/reaction cleanup。

## CLI

```sh
loom conversation list [agent] [--address ADDRESS_ID]
loom conversation discover [agent] [--address ADDRESS_ID] [--all]
loom conversation get <membership-id>
loom conversation set <address-id> <conversation-id> \
  --type group \
  --name "CodexLoom test group" \
  --purpose "讨论 CodexLoom 使用和接入" \
  --role "回答产品问题" \
  --guidance "仅 mention 回复；不泄漏其他会话" \
  --trigger mention \
  --reply-policy final_answer \
  --outbound-policy reply_only \
  --enabled false
loom conversation get <membership-id>
loom conversation enable <membership-id>
loom conversation disable <membership-id>
```

`conversation discover` 读取 Connector 最近上报的会话目录；默认只显示当前仍可用的候选项，`--all` 同时显示已经离开或不可访问的历史项。它不会创建 Membership。`conversation list` 只列出已配置的 durable Membership，两者不要混用。

DM 还必须传 `--type dm --actor <stable-actor-id>`，把稳定会话 ID 与稳定联系人 ID 同时绑定。长内容可放 JSON 文件，避免 shell 转义损坏换行。更新已有 Membership 时可传 `--expected-version <version>` 防止覆盖并发修改。

## UI

Agent Config 的 Connections 区域按 Address 展示 Membership。编辑器必须使用多行字段，并显示
Conversation、trust domain、policy、version 和 enabled 状态。Agent Thread feed 中的外部消息
显示 provider badge、sender、conversation、REQ/NOTIFY、Markdown body，raw XML 可展开。

## 安全边界

Membership 不是数据隔离或权限系统。同一个 Codex Thread 能看到其长期历史，因此：

- 内部群和外部群不应共用一个高权限 Agent。
- 不同组织、隐私域或监管域应创建不同 Agent/Thread。
- 外部消息不能提升 sandbox、approval policy 或本地文件权限。
- Profile 与 Membership 冲突时采用更严格的约束，并在需要时交给用户决策。

完整 Connection、Inbox/Outbox 与 gateway 设计见
[agent-platform-integration.md](agent-platform-integration.md)。
