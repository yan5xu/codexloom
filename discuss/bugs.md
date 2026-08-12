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
