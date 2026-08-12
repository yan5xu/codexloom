# 数据字典

## PlatformConnection.domain

- 类型：可选字符串枚举。
- 允许值：`feishu`（中国 Feishu）、`lark`（全球 Lark）。
- 空值：兼容旧数据，运行时按 `feishu` 处理。
- 来源：Lark/Feishu 设置向导或 Connection 更新 API。
- 消费者：凭据验证与群发现 REST client、Gateway REST/WebSocket clients、Gateway launch descriptor 和服务单元渲染。
- 安全属性：非敏感配置；不得替代或包含 App ID、App Secret、credential reference 或任意 URL。
