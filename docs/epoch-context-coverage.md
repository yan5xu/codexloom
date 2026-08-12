# Epoch Context Coverage

> 本文是 CodexLoom durable context 注入与覆盖证明的当前实现参考。它说明
> Loom Agent Prompt、Agent Profile、Relationships 和本次工作上下文如何进入
> 模型真正可见的 Codex 请求，以及 compaction 后如何恢复。

## 目标

长期 Agent 不能只依赖自然语言 trajectory 保存身份、边界和稳定工作方法。Codex
compaction 会替换模型可见历史；普通摘要不能保证逐字保留某条 Profile、关系合同或
长期规则。

CodexLoom 因此在每个 context epoch 中维护以下保证：

1. 当前 Loom Agent Prompt 在模型可见历史中至少完整出现一次。
2. 当前完整 Agent Profile 在模型可见历史中至少完整出现一次。
3. 当前 Agent 直接参与的 active Organization / Collaboration 完整快照在模型可见
   历史中至少完整出现一次。
4. Topic、Message、Needs You、Trigger、Schedule、External Inbox 和附件等本次工作
   上下文，在相关 Turn 的最终 input 中出现。
5. 只有可回放历史与同 Turn 模型事件都能证明模型开始消费该输入，durable source
   才标记为 `covered`。

这里的保证对象不是“某段文字曾写入 Loom Thread”，而是 Codex 在推理时实际可见的
请求历史。

## Epoch 与 Turn

`Turn` 和 `context epoch` 是两个独立维度：

- 一个 epoch 可以跨越多个 Turn。
- 一个 Turn 开始时，不一定开启新 epoch。
- Codex rollout 中的 compaction marker 开启新 epoch。
- compaction 前的 coverage 不能证明 compaction 后的模型历史仍包含对应内容。

没有 compaction 时，当前 revision 在一个 epoch 中成功覆盖后，后续 Turn 不重复注入。
发生 compaction 后，ledger 切换到新 epoch，并在**下一次 Turn 开始**时重新覆盖当前
durable sources。

当前版本不处理中途 compact 的同 Turn 恢复。如果 active Turn 在模型请求之间发生
compaction，该 Turn 后续请求可能暂时缺少 Loom durable context；下一 Turn 会恢复。
因此 mid-Turn compact 后立即观察到 `missing` 是已知边界，不能单独判为回归。

## Context Sources

| Source | Revision | Wire channel | 覆盖策略 |
|---|---|---|---|
| Loom Agent Prompt | `builtin:N` 或 `owner:N` | `role=developer` | 每个 revision、每个 epoch 至少一次 |
| Agent Profile | `profile:N` | 与 Prompt 同一条 `role=developer` message | 每个 revision、每个 epoch 至少一次 |
| Direct active Relationships | `relationships:<hash-prefix>` | Turn `input` 中的 user-role context | 每个完整 snapshot、每个 epoch 至少一次 |
| Turn work context | 不进入 coverage ledger | Turn `input` | 每个相关 Turn 按来源编译 |
| Conversation Membership context | 现有外部会话 developer-context 路径 | `role=developer` | 由外部会话生命周期管理，不归本 ledger 合并 |

首版只覆盖当前 Agent 的直接 active Organization / Collaboration。它不自动注入：

- counterpart 的完整 Profile；
- 整个 Team Graph；
- Collaboration Group；
- Activity；
- Conversation Membership；
- 间接组织关系；
- ACL、凭证、工具或生产授权。

Relationships 是声明性工作合同，不是权限。快照为空时仍注入带空
`<relationships>` 的完整快照，用于明确撤销旧关系。

## Developer 与 Input 分层

### Developer context

Loom Agent Prompt 和完整 Agent Profile 被渲染成一个原子的 Developer payload：

```xml
<loom_developer_context
  version="1"
  compiled_at="..."
  epoch_id="window:..."
  delivery_id="ctxa_...:developer"
  complete="true"
  atomic="true"
  prompt_revision="builtin:2"
  profile_revision="profile:5">
  ...Loom Agent Prompt...
  <loom_agent_profile_data
    version="1"
    revision="profile:5"
    agent_id="..."
    name="..."
    complete="true"
    supersedes_previous="true"
    declarative_not_authorization="true">
    ...
  </loom_agent_profile_data>
</loom_developer_context>
```

Hub 在 `turn/start` 前调用 Codex app-server `thread/inject_items`，写入一条原生
`role=developer` message。它会进入 Codex rollout 和后续模型请求，但：

- 不等于 Responses API 顶层 `instructions`；
- 不伪装成 Owner user message；
- 不触发一个新 Turn；
- 不在普通用户对话投影中显示为 `YOU`；
- 不会被 Codex compaction 特殊保留，必须由 epoch coverage 在下一 Turn 重注入。

Prompt 和 Profile 在 wire 上是一个不可拆分的 payload，在 ledger 中仍是两个独立
logical fragments。任一 source 变化时，Hub 重渲染并重新投递完整组合：

- Profile 更新不会增加 Prompt version；
- Prompt 更新不会增加 Profile version；
- 不会再发送第二份 sibling Profile；
- 空 Profile 仍渲染为 `complete="true"` 的空 snapshot。

### Turn input

原始业务输入保持在前，Loom input context 作为同一 user input 的尾部：

```text
本次原始输入

<loom_context
  version="1"
  compiled_at="..."
  epoch_id="window:..."
  delivery_id="ctxa_...:input">
  <loom_agent_relationships ...>
    ...
  </loom_agent_relationships>
  <loom_turn_context
    origin="internal_agent"
    trust="loom_managed"
    authority="business_input"
    kind="agent_message"
    ref_id="msg_..."
    topic_id="tpc_...">
    <original_input location="preceding_turn_input_item" />
    <payload>...</payload>
  </loom_turn_context>
</loom_context>
```

`loom_turn_context` 记录本次输入的来源和授权等级。当前来源包括 Owner direct input、
Needs You answer、Agent Message、Topic、Trigger、Schedule、External Inbox 和 runtime
recovery。物理位置不提升 authority：普通内部或外部业务输入不能覆盖平台规则、Profile、
Relationship 或真实权限。

`loom_turn_context` 不是 durable fragment，不进入 once-per-epoch ledger。它只在本次
Turn 需要工作 envelope 时出现。普通 Owner direct input 在 durable sources 已覆盖、
没有 Topic、引用、work context 或附件时，不再追加空 XML。

Relationships 是 durable input fragment。它只在当前 revision 尚未覆盖时进入
`loom_context`；后续同 epoch Turn 不重复。

## Turn 开始时的编译流程

每个 Agent runtime 使用同一把 start lock 串行以下步骤，防止并发 Turn 重复注入：

1. 等待 shared Codex app-server ready。
2. `thread/resume`，确保空闲时被 app-server 卸载的 Thread 已重新加载。
3. 从 canonical rollout 读取当前 epoch。
4. 加载该 Thread 的 coverage ledger；epoch 不一致时建立新的空 coverage。
5. 尝试用当前 rollout 补齐上一轮 pending attempt 的证据。
6. 编译当前 Prompt、Profile、Relationships 的 revision 和 hash。
7. 比较 ledger，找出 missing fragments。
8. Prompt 或 Profile 任一个 missing 时，将两者作为完整原子 Developer pair 重发。
9. 保存 `planned` attempt、delivery marker 和完整 payload SHA。
10. 通过 `thread/inject_items` 写入 Developer context。
11. 构造最终 Turn input：原始输入在前，必要的 `<loom_context>` 在后。
12. 调用 `turn/start`，将 attempt 绑定到真实 Turn ID。
13. 观察同 Turn 首个模型产生事件，并回读 rollout。
14. 两类证据都成立后，将对应 fragments 标记为 `covered`。

编译后的 Developer payload 上限是 128 KiB。超限时 Hub fail closed 并拒绝启动 Turn，
不会截断 Prompt 或 Profile。Owner-managed Prompt template 本身上限是 64 KiB。

## Coverage Ledger

每个 Thread 有一份 ledger，保存在：

```text
~/.codex-loom/context-coverage/<sha256(thread-id)>.json
```

主要字段：

```json
{
  "schemaVersion": 2,
  "agentId": "...",
  "threadId": "...",
  "epoch": {
    "id": "window:...",
    "windowNumber": 3,
    "compactedAt": "..."
  },
  "covered": {
    "loom_agent_prompt": {
      "revision": "builtin:2",
      "hash": "...",
      "coveredAt": "...",
      "turnId": "..."
    }
  },
  "pending": {
    "id": "ctxa_...",
    "state": "submitted",
    "fragments": [],
    "deliveries": []
  }
}
```

Attempt 的主要状态是：

| State | 含义 |
|---|---|
| `planned` | payload 已编译，ledger 已保存，但尚未绑定真实 Turn |
| `submitted` | `turn/start` 已返回真实 Turn ID |
| `model_observed` | 已观察到同 Turn 的首个模型产生事件 |
| `covered` | 当前 epoch 的 replayable rollout 也包含 exact delivery |

`thread/inject_items` RPC 成功本身不等于 covered。完整证明同时要求：

1. exact fragment revision 和 hash 已进入本次 delivery；
2. exact role、`delivery_id` marker 和完整 payload SHA 存在于当前 epoch 的 canonical
   replayable rollout；
3. user-role input delivery 属于同一个 Turn ID；
4. 已观察到同一 Turn、同一 epoch 的模型产生事件。

任一证据缺失时保持 pending 或 missing，并在下一 Turn 保守重发。这是
at-least-once，而不是 exactly-once。重复的完整声明是安全的，漏掉当前声明不是。

旧 epoch 的迟到事件不能推进新 epoch ledger。

## Source 变化

### Prompt 更新

`loom context prompt set` 创建 Owner-managed Prompt。它必须包含且安全渲染恰好一个
完整 `loom_agent_profile_data` block。更新后：

- Prompt revision 变化；
- 下一 Turn 重发 Prompt + Profile 原子 pair；
- Profile revision 不变。

`loom context prompt clear` 删除 Owner override，并恢复当前 builtin Prompt，不是关闭
durable Prompt。

### Profile 更新或清空

Profile version 变化后，下一 Turn 重发 Prompt + Profile 原子 pair。Profile 内容使用
XML attribute escaping 与 CDATA-safe splitting，`]]>`、伪 closing tag 或伪 policy
不能逃逸数据边界。

清空 Profile 仍输出完整空 snapshot，避免旧 Profile 在模型历史中继续被当作当前事实。

### Relationship 更新或删除

Relationships 使用稳定排序后的完整 JSON 计算 hash。任何新增、修改或删除都会生成新的
完整 XML snapshot。删除最后一条关系时也发送空列表；不会因“当前没有关系”而省略。

## 诊断入口

查看有效 Prompt：

```sh
loom context prompt get
loom context prompt get --json
```

查看当前 source revisions、channel 和 covered 判断：

```sh
loom context explain AGENT
loom context explain AGENT --json
```

查看原始 epoch ledger、pending attempt 和 coverage 证据：

```sh
loom context coverage AGENT --json
```

对应 HTTP API：

```text
GET    /api/context/agent-prompt
PUT    /api/context/agent-prompt
DELETE /api/context/agent-prompt
GET    /api/agents/{agent}/context/explain
GET    /api/agents/{agent}/context/coverage
```

`explain` 和 `coverage` 是只读观察，不会因为读取而把 missing source 标成 covered。

### 常见判断

- **同 epoch、同 revision、covered=true**：后续普通 Turn 不应重复 durable fragment。
- **Profile 已更新但 Prompt revision 未变**：正常；下一次 Developer payload 仍重发完整 pair。
- **compact 后 covered={}**：先确认是否仍在同一个 active Turn。若是，等待下一真实 Turn
  验证，不直接判为回归。
- **下一 Turn 后仍 missing**：检查 pending state、Turn ID、model event、rollout marker、
  role 和完整 payload hash。
- **RPC 成功但没有 model event**：不得标记 covered；下一 Turn 重发。
- **model event 存在但 delivery 不在当前 replayable window**：不得标记 covered。
- **普通 Owner Turn 仍显示空 `<loom_context>`**：除非有附件、Topic 或其他 work envelope，
  否则属于渲染或编译回归。

## 安全与权限边界

- Developer role 是 Codex request `input` 中的原生 developer message，不是顶层 system
  instructions。
- Prompt、Profile、Relationships 都是声明性 context，不授予 ACL、凭证、工具、外发、
  部署或生产权限。
- Profile 是大 Prompt 中的结构化数据，不能重定义外层 Prompt 的 instruction hierarchy。
- 外部正文和 work payload 使用 XML escaping / CDATA-safe splitting，不能逃逸 envelope。
- 当前 authenticated Owner input 可以纠正本次意图或发起正式持久变更，但不会静默改写
  durable source。

## 测试与生产验收

自动测试至少覆盖：

1. fresh epoch 首 Turn注入 Prompt、Profile、Relationships；
2. 同 epoch 第二 Turn不重复同 revision durable sources；
3. Profile 变化只更新 Profile logical coverage，但重发原子 Developer pair；
4. Relationships 变化发送完整 snapshot；
5. 删除最后一条关系发送完整空 snapshot；
6. compaction 后下一 Turn重新覆盖；
7. 旧 epoch 迟到事件不推进新 ledger；
8. crash、缺少 model event 或缺少 replayable history 时保守重发；
9. Profile 与 work payload 的伪 XML 无法逃逸；
10. 并发 Turn 不重复计划同一 coverage。

生产启用后还必须做真实 Codex canary：

1. `loom context explain AGENT --json` 记录当前 epoch 和 revisions。
2. 启动一个只读 Turn。
3. 检查 canonical rollout 中的 Developer role、input 顺序、delivery marker 和完整 hash。
4. 让模型只回报当前可见的 Prompt/Profile revision 与已知标题，证明实际可见。
5. Turn 后检查三类 source 的 `coveredAt` 和 `turnId`。
6. 同 epoch 再启动一个只读 Turn，确认 durable fragments 不重复。
7. 在真实 compaction 后重复上述验证，确认下一 Turn恢复。

生产 binary 嵌入 WebUI。涉及 UI 或 API 变化时，必须使用 `make build`，重启后核验
`/api/version` 的 `build.webAsset` 与 API 返回 JSON；不能用裸 `go build` 发布。

## 当前边界

V2 有意不包含：

- mid-Turn compaction 的同 Turn恢复；
- Active Operating Precedents；
- Domain-first discovery；
- counterpart Profile 或完整 Team Graph；
- 自动组织调整、绩效判断或权限推导。

这些能力未来可以作为新的 Context Source 或 runtime 能力加入，但必须拥有独立 revision、
scope、coverage 和撤销语义，不能通过扩大现有 Profile 或 Relationships 偷渡。

## 权威实现入口

- Prompt template：`internal/hub/prompts/loom-agent-prompt.md.tmpl`
- 编译与 ledger：`internal/hub/context.go`
- Developer 注入：`internal/hub/profile.go`
- Turn 组装：`internal/hub/agent.go`
- Rollout epoch 与 delivery proof：`internal/rollout/context.go`
- Store：`internal/store/context.go`
- HTTP API：`internal/httpapi/routes_context.go`
- CLI：`cmd/loom/commands_context.go`
- 核心测试：`internal/hub/context_test.go`
