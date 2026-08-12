# BUG-001 现场摘要

- 页面症状：`connector command stream closed`
- Gateway provider 错误：`1000040351: Incorrect domain name`
- 受影响 App：全球 Lark App `crm-rebot-AI`
- 受影响 Connection：`conn_2fa613bda1c65352`
- 受影响 Agent：`crm-rebot`
- 已采取保护：部署前备份已创建，Connection 已禁用，Gateway 重启循环已停止。
- Secret 处理：凭据仅确认由 Mac mini Keychain 管理；本证据包未读取或记录 Secret。
