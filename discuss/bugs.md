# Bug 登记簿

### BUG-001 全球 Lark Gateway 使用了中国 Feishu 域名

- **状态**: ⏳ 待验证
- **commit**: 本提交（2026-08-12）
- **文件**: `internal/feishu/`、`internal/feishugw/`、`internal/httpapi/`、`internal/hub/`、`internal/store/`、`cmd/loom-feishu-gateway/`、`web/src/`
- **根因**: REST 与 WebSocket SDK 客户端未接收区域配置，默认访问 `open.feishu.cn`，全球 Lark App 因域名不匹配退出。
- **修复**: 为 Lark/Feishu Connection 增加受限域名字段，并把同一值贯穿发现、Gateway、持久化启动计划和 WebUI；旧连接为空时继续默认 Feishu。
