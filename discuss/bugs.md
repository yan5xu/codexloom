# Bug 登记簿

### BUG-001 全球 Lark Gateway 使用了中国 Feishu 域名

- **状态**: ⏳ 待验证
- **commit**: 本提交（2026-08-12）
- **文件**: `internal/feishu/`、`internal/feishugw/`、`internal/httpapi/`、`internal/hub/`、`internal/store/`、`cmd/loom-feishu-gateway/`、`web/src/`
- **根因**: REST 与 WebSocket SDK 客户端未接收区域配置，默认访问 `open.feishu.cn`，全球 Lark App 因域名不匹配退出。
- **修复**: 为 Lark/Feishu Connection 增加受限域名字段，并把同一值贯穿发现、Gateway、持久化启动计划和 WebUI；旧连接为空时继续默认 Feishu。

### BUG-002 Lark 普通群消息未进入自动认领

- **状态**: ⏳ 待验证
- **commit**: 本提交（2026-08-12）
- **文件**: `internal/feishugw/gateway.go`、`internal/feishugw/gateway_test.go`
- **根因**: 全球 Lark 对普通群消息发送旧版 `message` 事件，Gateway 只注册了新版 `im.message.receive_v1`，SDK 在进入 Hub 前以 `not found handler` 丢弃事件。
- **修复**: 注册旧版消息处理器，把旧事件归一化到现有 Inbox 语义，同时保留真实 mention、会话、线程、附件和去重身份。

### BUG-003 双 Lark Bot 事件不对称导致后台问题无人回复

- **状态**: 🛠️ 已实现，待 Mac mini/Lark 验证
- **commit**: 本提交（2026-08-12）
- **文件**: `internal/hub/inbox.go`、`internal/hub/integration.go`、`internal/hub/external_message.go`、`internal/httpapi/routes_integration.go`、`cmd/loom/commands_integration.go`
- **现场证据**: 同一条后台路由测试只在 CRM 产生 Inbox `inb_d767e7f943de8072`，CRM 结束为 `handled/no_reply`；Inwish Connection 保持 connected，但没有收到同一 Lark event，因此群内无回复。
- **近因**: `crm-rebot` 判断后台问题后只执行 no-reply，系统期待另一 Lark Bot 独立收到相同事件再由 `inwish-admin` 认领；现场的第二条事件没有到达 Inwish。
- **根因**: 路由正确性依赖两个独立 Bot 对同一群事件的对称分发，没有 Hub 级的所有权转移、唯一回复权和可恢复委派状态机。
- **修复**: `crm-rebot` 保持唯一原始入口；Hub 在定向 Relationship 与唯一 `explicit_dispatch` Membership 双重校验后，把同一个 `InboxMessage` 委派给 `inwish-admin`。source 进入 `handled/delegated` 并失去回复权，target 使用 Inwish Address/Connection 回复；重复委派幂等，`pending_delegation` 在周期队列恢复或重启后收敛。
- **剩余门禁**: 尚未构建或部署到 Mac mini，也尚无新的 Lark provider delivery receipt；不得把本地测试通过报告为线上修复。
