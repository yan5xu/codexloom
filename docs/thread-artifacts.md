# Thread Artifacts

CodexLoom 把图片和文件视为 Agent Thread 的受管 Artifact，而不是聊天文本中的临时本地路径。Artifact 属于一个稳定 Agent，文件内容在进入 Turn 或发布时被快照；原文件之后修改、移动或删除，不会改变 Agent 已经收到或用户已经看到的内容。

## 用户向 Agent 发送附件

WebUI 输入框支持文件选择、拖放和粘贴。发送分两步完成：先上传到 `POST /api/agents/{agent}/artifacts`，再把返回的稳定 Artifact ID 放进 `POST /api/agents/{agent}/turns` 的 `artifactIds`。只有 `turn/start` 被 Codex 接受后，输入框才显示发送完成；失败时文本和待发送文件一起恢复，避免用户误以为消息丢失。

图片使用 app-server 原生输入：

```json
{"type":"localImage","path":"/managed/artifact/content.png"}
```

所有附件同时写入一个 `loom_attachments` 清单，包含 Artifact ID、文件名、MIME、大小、受管路径和下载 URL。普通文件依靠这个清单进入模型可见历史，Agent 可以使用本地工具读取受管路径；图片既有清单，也有原生视觉输入。WebUI 渲染清单为预览或文件行，不把内部 XML 暴露为普通消息正文。

CLI 使用：

```sh
loom thread send research "分析这份材料" --attachment /absolute/path/report.pdf
loom thread send vision "比较两张图" --attachment before.png --attachment after.png
loom thread send research --attachment dataset.csv
```

## Agent 向 Thread 发布产物

Agent 完成图片、报告、压缩包或数据文件后，应显式发布最终产物：

```sh
loom artifact publish --from research --file /absolute/path/report.pdf
loom artifact publish --from design --file cover.png --file design-notes.pdf
```

Loom 快照文件，返回稳定 Artifact ID 和下载 URL，并向当前 Agent trajectory 发送 `loom/artifact-published`。Agent 应在最终回答中保留返回 URL，便于用户直接打开。外部平台发送仍由 `loom integration send` 和 Conversation Membership 管理；`artifact publish` 不会绕过 Connector 向飞书、Slack 或 Parall 发消息。

## 存储与限制

- 路径：`$CODEX_LOOM_DATA/attachments/threads/{agentId}/{artifactId}/`。
- Artifact ID 按内容 SHA-256 生成；同一 Agent 重复上传相同内容会复用快照。
- 每个 Turn 最多 8 个附件，单个文件必须为普通文件、大小在 1 byte 到 25 MB 之间。
- 下载入口会重新校验 Agent ownership、metadata 和受管文件状态。
- Codex rollout 仍是 Thread 历史的单一真相源；`local_images` 会被历史投影恢复。普通文件通过模型可见的 `loom_attachments` 清单恢复。

内置 `loom-artifacts` Skill 告诉 Agent 如何读取收到的附件并发布生成文件。Loom 只向 Codex 注册 Skill catalog；完整 Skill 正文仍由 Codex 在匹配场景时渐进加载。
