# Lark 中央唯一派发

## Task Contract

- 目标：让 `crm-rebot` 成为客服群唯一原始消息入口；当它判断问题属于 qdomain.com 管理后台时，由 CodexLoom Hub 显式委派给 `inwish-admin`，并保证同一原始消息始终只有一个 Agent 拥有回复权。
- 范围内：Inbox 委派状态机、持久化与重启恢复、Relationship/Membership 校验、API、CLI、Agent Inbox 指令、回归测试、数据字典和事故记录、Mac mini 灰度部署与真实 Lark 验收。
- 范围外：基于关键词的中央业务分类、让 `crm-rebot-auto-reply` 成为独立 Lark 入口、复制或轮换 Lark Secret、扩大 GitHub/服务器权限、修改 inwish-admin 或 crm-rebot 业务代码。
- 验收标准：CRM 原始 Inbox 进入 `handled/delegated`；Inwish 委派 Inbox 进入 `queued` 并可处理；只有 Inwish 能为该原始消息创建回复 Outbox；Outbox 使用 Inwish Address/Connection 且 provider receipt 为 `sent`；重复委派幂等；重启可收敛中断的委派；CRM 普通问题仍由 CRM 唯一回复。
- 关键不变量：原始 `InboxMessage` 的正文、附件、sender、conversation、thread、external message ID 不复制也不改写；一个 source Inbox 只能委派给一个 target；source 一旦委派即不可 reply/no-reply；target 必须由 source→target 的有效 Relationship 授权，并在同 provider/conversation 下恰有一个启用且允许 `explicit_dispatch` 的 Address/Membership；未完成委派不得让两个 Agent 同时进入可处理状态。
- 风险与回滚：NDJSON 跨文件无事务，进程中断可能留下半完成状态；通过 `pending_delegation`、补偿写和启动恢复收敛。部署保留 `035151cbb989` release；若唯一回复、连接或 receipt 验收失败，恢复旧二进制和部署前 Membership/Guidance 快照。
- 失败分类：同一消息双回复、丢回复、错误 Connection、重启后双重可处理属于 Bug；Agent 语义分类错误属于路由策略/评测问题；Lark 未把事件交给 CRM、权限或配额错误属于外部配置门禁。

## 负载决策

业务分类仍由 `crm-rebot` 的模型和领域上下文完成，Hub 不维护关键词规则。Hub 只实现机械化授权、所有权转移、幂等和唯一回复权，因此分类策略可以独立演进，传输正确性不依赖两个 Bot 是否同时收到同一 Lark 事件。

## 数据流与状态机

1. CRM Address 接收 Lark 事件，Hub 创建一份 `InboxMessage` 和 CRM source Inbox。
2. CRM 通过受控 `delegate` 命令提交 source Inbox、source Agent、target Agent 和 reason。
3. Hub 校验 source 所有权、终态、既有回复、定向 Relationship，以及 target 在同 provider/conversation 下唯一启用的 Address/Membership；target Membership 必须解析为 `triggerPolicy=explicit_dispatch` 且允许回复，并继续执行 target 的 allow/block、DM contact 与 conversation type 边界。
4. Hub 以同一个 `MessageID` 创建 target Inbox，先写为 `pending_delegation`，并记录 `delegatedFromInboxItemId`。
5. Hub 将 source 写成 `handled/delegated`，记录 `delegatedToInboxItemId`，再把 target 写成 `queued`。任何中间失败都以追加快照补偿；不会把 source 恢复为可处理且同时把 target 排队。
6. Inwish 回复时，Outbox 从 target Inbox 的 Address/Membership 解析 Inwish Connection；conversation/thread/external message ID 仍来自原始 `InboxMessage`。
7. 周期队列恢复与启动恢复使用同一规则：source 已是 `delegated` 时把匹配的 pending target 转为 `queued`；source 尚未 delegated 时把 pending target 转为 `cancelled`。相同 source+target 的重复请求返回现有 target。

## API 与 CLI

- `POST /api/inbox/{id}/delegate`
- 请求：`{"agent":"crm-rebot","to":"inwish-admin","reason":"..."}`
- 响应：source `item` 与 target `delegatedInboxItem`。
- CLI：`loom integration delegate --reply-to <inbox-id> --from <source-agent> --to <target-agent> --reason "..."`
- `replyPolicy=explicit` 的 Inbox envelope 只在 source Agent 存在可用的出向 Relationship 时展示对应 `delegate_command`。

## Data Dictionary Delta

新增数据：

- `InboxItem.delegatedFromInboxItemId`：target Inbox 指向 source Inbox。
- `InboxItem.delegatedToInboxItemId`：source Inbox 指向唯一 target Inbox。

修改语义：

- `InboxItem.state` 新增内部过渡值 `pending_delegation`，不可被 Agent 处理。
- `InboxItem.outcome` 新增终态值 `delegated`；对应 source 的 `state=handled`。
- 一个 `InboxMessage` 可被 source 与一个 target Inbox 引用，但回复权仅随 Inbox 委派链当前 owner 移动。

废弃数据：

- 无。

关系变化：

- `TeamRelationship(fromAgentId,toAgentId)` 从纯上下文关系扩展为 Inbox 显式委派的必要授权边。
- target Address/Membership 必须与 source 的 provider 和 conversation 匹配，但允许使用不同 Connection/Bot identity。

新增/修改不变量：

- 委派链当前只允许一跳；target Inbox 不可再次委派，避免循环和多 owner。
- source 与 target 不得同时拥有 reply Outbox。
- target Membership 必须启用、允许 `explicit_dispatch`，且 `outboundPolicy` 不得为 `none`。

计划同步到 `docs/current/data-dictionary.md`：

- yes。

## 验证

- 先运行新增聚焦测试并保存修复前失败证据。
- `go test ./internal/hub ./internal/httpapi ./cmd/loom -count=1`
- `go test ./... -count=1`
- `go vet ./...`
- `scripts/verify.sh`
- `scripts/postmortem-lint.sh`
- `scripts/postmortem-scan.sh`
- 更新 Code Graph 并复查 `IngestMessage`、Inbox action、Outbox creation、HTTP route 和 CLI 的最终影响面。
- 在干净 release Auxiliary 运行 canonical `make build`，部署后核对 `/api/version.build.webAsset`。
- Lark 实测保留 source/target Inbox ID、唯一 Outbox ID、Inwish Connection、external message ID 与 delivery receipt；无这些证据不得报告上线成功。
