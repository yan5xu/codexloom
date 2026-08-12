# 全球 Lark 域名支持

## Task Contract

- 目标：让全球 Lark App 的 REST 与 WebSocket 客户端稳定使用 `open.larksuite.com`，并在重启后保持一致。
- 范围内：显式域名选择、Connection 持久化、发现与凭据验证、legacy/typed Gateway 启动参数、WebUI、回归测试和部署验证。
- 范围外：更换 App、读取或迁移 Secret、发送测试消息、改动现有 Hermes `inwish-bugbot`。
- 验收标准：聚焦测试和 `make build` 通过；Mac mini 上 Gateway 进程存活、Connection 为 `connected`、heartbeat 更新、日志出现 WebSocket connected；Inbox/Outbox 在人工群测前保持为空。
- 关键不变量：旧 Connection 未设置域名时仍按 Feishu；Secret 不进入参数、日志、仓库或普通备份；Agent/Address/Membership 绑定保持不变。
- 风险与回滚：部署失败或连接不稳定时禁用目标 Connection，停止 Gateway 重试循环，并回滚到部署前备份；不影响 Hermes。
- 失败分类：域名契约不一致为 Bug；SDK/构建环境失败为 Noise；缺少 Lark 权限或群配置为外部配置门禁。

## 设计

Connection 新增可选 `domain`，仅允许 `feishu` 或 `lark`。空值是旧数据兼容值，运行时等价于 `feishu`。Gateway 的持久启动描述符复制该字段；只在显式 `lark` 时渲染 `--domain lark`，因此旧 Feishu launchd/systemd 单元保持原样。

数据流：WebUI 选择区域 → onboarding 验证并保存 Connection.domain → Discovery REST client 与 Gateway REST/WS clients 解析同一枚举 → legacy 或 typed launch unit 固化 `--domain` → 重启后恢复相同区域。

## Data Dictionary Delta

新增数据：

- `PlatformConnection.domain`：可选枚举，`lark` 表示全球 Lark，`feishu` 表示中国 Feishu。

修改语义：

- 旧 Connection 的空值保持兼容，运行时继续按 `feishu` 处理。

废弃数据：

- 无。

关系变化：

- Gateway launch descriptor 与 frozen binding 复制 Connection 的同一 `domain`，完整性校验要求两者一致。

新增/修改不变量：

- REST 与 WebSocket 客户端必须使用相同区域；不允许任意 URL。
- 未传 `chatId` 的修复调用不得重建或删除既有 Membership。

已同步到 `docs/current/data-dictionary.md`：

- yes。

## 验证

- `go test ./internal/feishu ./internal/feishugw ./internal/httpapi ./internal/hub ./internal/store ./cmd/loom-feishu-gateway ./cmd/loom`
- `pnpm --dir web test`
- `make build`
- 发布后核对 `/api/version` 的 `build.webAsset`、Connection/domain、launchd 参数、进程、heartbeat 与 Gateway 日志。
