# 数据字典

## PlatformConnection.domain

- 类型：可选字符串枚举。
- 允许值：`feishu`（中国 Feishu）、`lark`（全球 Lark）。
- 空值：兼容旧数据，运行时按 `feishu` 处理。
- 来源：Lark/Feishu 设置向导或 Connection 更新 API。
- 消费者：凭据验证与群发现 REST client、Gateway REST/WebSocket clients、Gateway launch descriptor 和服务单元渲染。
- 安全属性：非敏感配置；不得替代或包含 App ID、App Secret、credential reference 或任意 URL。

## InboxItem delegation

- `delegatedFromInboxItemId`：可选 InboxItem ID。只存在于委派 target，指向最初收到外部消息的 source。
- `delegatedToInboxItemId`：可选 InboxItem ID。只存在于 source，指向唯一 target。
- `state=pending_delegation`：跨 NDJSON 追加期间的内部过渡态；不得投递给 Agent，也不得回复。周期队列恢复与启动恢复根据 source 的持久终态将其收敛为 `queued` 或 `cancelled`。
- `outcome=delegated`：source 已将同一 `InboxMessage` 的处理权和唯一回复权交给 target；此时 source 必须为 `state=handled`。
- 所有权不变量：当前只允许一跳委派；一个 source 只能指向一个 target；target 不得再次委派；source 和 target 不得同时拥有 reply Outbox。
- 消息不变量：委派不复制或改写 `InboxMessage`。正文、附件、sender、conversation、thread 和 external message ID 由 source 与 target 引用同一记录。
- 路由不变量：target reply Outbox 使用 target Inbox 的 Address、Membership 及其 Connection；仅 conversation ID、thread ID 和 provider message ID 沿用原始消息。

## TeamRelationship Inbox delegation authority

- 定向 `TeamRelationship(fromAgentId,toAgentId)` 是 source 向 target 执行显式 Inbox 委派的必要授权边。
- Relationship 不是充分条件：Hub 还要求 target 在原消息 provider/conversation 下恰有一个启用的 Address + Membership，且 Membership 解析为 `triggerPolicy=explicit_dispatch`、允许回复、`outboundPolicy!=none`。
- 委派不绕过 target 的 sender/conversation allow/block、DM contact、conversation type 或 reply policy；任一边界不满足时拒绝委派。
- 此授权仅允许把该 Inbox 的处理权交给目标 Agent；不授予凭据、工具、主动外发、部署、生产或 Membership 之外的数据访问权限。
