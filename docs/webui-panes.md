# CodexLoom WebUI Pane Reference

维护日期：2026-08-02

本文档以 `web/src/App.tsx` 的视图切换和 `web/src/*Pane.tsx` 的 API 调用为当前事实，
记录每个 WebUI Pane 的数据源、写操作、SSE 订阅和关键状态。它不是视觉设计文档；
视觉与移动验证见 [webui-validation.md](webui-validation.md)。

## 全局工作台状态

`App.tsx` 维护以下工作台级状态：

- Agent 列表来自 `GET /api/agents`，并用 `GET /api/events` 全局 SSE 做增量更新。
- 当前/打开 Agent 使用 `sessionStorage`，Sidebar 折叠状态使用 `localStorage`。
- 打开多个 Agent Tab 时，每个 Tab 通过 `web/src/thread-events.ts` 订阅 Agent 级
  `GET /api/agents/{key}/thread/events`。
- `loom/reconcile` 会触发所有打开 Agent、pending work、Human Requests、Topics 和
  Remote 的快照重读，不依赖 SSE 重放窗口里的静默丢事件。
- 创建 Agent 后默认写入空 Profile；归档 Agent 后同时从打开 Tab 和当前选中状态移除。

## Pane 契约

### Agent Pane

入口：`AgentPane.tsx`，用于一个长期 Agent 的 Thread 观察与治理操作。

数据源：

- `GET /api/agents/{key}/profile`
- `GET /api/agents/{key}/addresses`
- `GET /api/integrations/connections`
- `GET /api/integrations/conversations?agent={key}`
- `GET /api/team`
- `GET /api/triggers?agent={key}`
- `GET /api/schedules`
- `GET /api/agents/{key}/usage?days=7`
- `GET /api/agents/{key}/thread/history?count=25&offset=N`
- `GET /api/agents/{key}/artifacts`
- `GET /api/agents/{key}/thread/events`（SSE）

写操作：

- `POST /api/agents/{key}/turns`
- `POST /api/agents/{key}/turns/current/interrupt`
- `POST /api/agents/{key}/turns/interrupted/continue`
- `POST /api/agents/{key}/turns/interrupted/dismiss`
- `POST /api/agents/{key}/thread/approvals/{approvalId}`
- `PATCH /api/agents/{key}/config`
- `PUT /api/agents/{key}/profile`
- `PUT|DELETE /api/agents/{key}/goal`
- `POST /api/agents/{key}/artifacts`
- `POST /api/human-requests/{id}/answer`
- `POST /api/comms/messages/{id}/retry|no-reply`
- `POST /api/triggers`
- `POST /api/triggers/{id}/pause|resume|cancel`
- `PATCH /api/integrations/addresses/{id}`
- `PUT /api/integrations/addresses/{addressId}/conversations/{conversationId}`
- `PATCH /api/integrations/conversations/{id}`

关键状态：

- 当前 Agent 从 URL hash / sessionStorage 恢复。
- feed 虚拟滚动使用 `web/src/feed.ts`，新增消息时不清空已缓存行高；行高由
  `measureElement()` 和 `resizeItem()` 维护。
- Goal、Profile、Trigger、Schedule、Artifact 和 Membership 均为可折叠面板，不改变
  Thread 历史投影。

### Needs You Pane

入口：`NeedsYouPane.tsx`。

数据源：

- `GET /api/human-requests`

写操作：

- `POST /api/human-requests/{id}/answer`
- `POST /api/human-requests/{id}/cancel`
- `POST /api/human-requests/{id}/retry`

关键状态：通过全局 SSE 的 `loom/human-request` 失效并重读；Sidebar 显示未处理数量。

### Topics Pane

入口：`TopicsPane.tsx`。

数据源：

- `GET /api/topics`
- `GET /api/topics/{id}`
- `GET /api/topics/{id}/artifacts`

写操作：

- `POST /api/topics`
- `POST /api/topics/{id}/read`
- `POST /api/topics/{id}/links`
- `POST /api/topics/{id}/send`
- `POST /api/topics/{id}/interventions`

关键状态：全局 SSE `loom/topic-updated` / `loom/topic-event` 触发 Topics 重读；
选中 Topic 的 Artifact URL 统一为 `/api/topics/{id}/artifacts/{artifactId}`。

### Inbox Pane

入口：`InboxPane.tsx`。

数据源：

- `GET /api/inbox`
- `GET /api/outbox`
- `GET /api/integrations/addresses`
- `GET /api/integrations/conversations`
- `GET /api/events`（SSE）

写操作：

- `POST /api/inbox/{id}/reply|no-reply|defer|retry`
- `POST /api/outbox/{id}/retry`
- `POST /api/integrations/send`
- `POST /api/comms/messages`
- `POST /api/comms/messages/{id}/no-reply|retry`

关键状态：Inbox/Outbox 保留外部 origin 和 provider 原语；全局事件中的
`loom/inbox-message`、`loom/inbox-item`、`loom/outbox-item` 和
`loom/comms-message` 会触发 pending work 重读。

### Messages Pane

入口：`MessagesPane.tsx`。

数据源：

- `GET /api/comms`
- `GET /api/events`（SSE）

写操作：

- `POST /api/comms/messages`
- `POST /api/comms/messages/{id}/cancel|retry|resolve|no-reply`

关键状态：用于内部 Agent Message 的统一观察；raw envelope 保留，但创建/回复必须走
`loom msg` / `POST /api/comms/messages`，不手写 XML。

### Integrations Pane

入口：`IntegrationsPane.tsx`。

数据源：

- `GET /api/integrations/connections`
- `GET /api/integrations/addresses`
- `GET /api/integrations/conversations`
- `GET /api/integrations/conversation-candidates`
- `GET /api/inbox`
- `GET /api/events`（SSE）
- 各 Provider discovery endpoint

写操作：

- Provider credentials / setup / gateway / operations endpoint
- `POST /api/integrations/connections`
- `PATCH /api/integrations/connections/{id}`
- `POST /api/agents/{agent}/addresses`
- `PATCH /api/integrations/addresses/{id}`
- `PUT /api/integrations/addresses/{addressId}/conversations/{conversationId}`
- `PATCH /api/integrations/conversations/{id}`
- `POST /api/inbox/{id}/retry`
- `POST /api/integrations/send`

关键状态：支持飞书、Slack、Parall 和 GitHub 的 Connection / Address / Conversation /
Ingress 管理；Gateway 状态与 Provider Operation 结果通过独立路由回传。

### Schedules Pane

入口：`SchedulesPane.tsx`。

数据源：

- `GET /api/schedules`
- `GET /api/events`（SSE）

写操作：

- `POST /api/schedules`
- `POST /api/schedules/{id}/run|enable|disable`
- `DELETE /api/schedules/{id}`

关键状态：Schedule 使用独立 `scheduler` 系统身份投递，不伪装 Agent。

### Team Pane

入口：`TeamPane.tsx`，同时承载 `MessagesPane` 作为内部通信视图。

数据源：

- `GET /api/team`
- `GET /api/team/relationships`
- `GET /api/team/organization`
- `GET /api/team/collaboration-groups`
- `GET /api/comms`

写操作：

- `POST|PATCH|DELETE /api/team/relationships`
- `POST|PATCH|DELETE /api/team/organization`
- `POST|PATCH|DELETE /api/team/collaboration-groups`

关键状态：Graph 布局稳定位置保存在 `localStorage`；删除或归档被 Group 引用的对象前，
必须先更新/归档 Group。

### Overview Pane

入口：`OverviewPane.tsx`，包含 status / usage / capacity 三种 section。

数据源：

- `GET /api/agents`
- `GET /api/remote`
- `GET /api/admin/backups`
- `GET /api/inbox?active=true`
- `GET /api/human-requests`
- `GET /api/topics`
- `GET /api/integrations/connections`
- `GET /api/usage`
- `GET /api/workload`
- `GET /api/activity/daily`

写操作：

- `POST /api/admin/backup`
- `POST /api/admin/restart`
- `POST /api/agents`

关键状态：Sidebar/状态区显示全局 active/idle、pending work、Human Requests 和
Restart/Backup 状态；`loom/restart-status` 驱动重启进度。

### Settings Pane

入口：`SettingsPane.tsx`，包含 remote / connectors / recovery / system / developer
section。

数据源：

- `GET /api/integrations/connections`
- `GET /api/remote`
- `GET /api/admin/backups`
- `GET /api/version`

写操作：

- `POST /api/remote/enable|disable`
- `POST /api/remote/pairing`
- `DELETE /api/remote/devices/{id}`
- GitHub device/token onboarding
- `POST /api/admin/backup`
- `POST /api/admin/restart`

关键状态：开发者 section 暴露 `window.codexLoom` automation API；恢复 section 显示
Backup 保留策略和手动 Prune 入口。

### Remote Pane

入口：`RemotePane.tsx`。

数据源：

- `GET /api/remote`
- `GET /api/remote/devices`
- `GET /api/remote/pairing`

写操作：

- `POST /api/remote/enable|disable`
- `POST /api/remote/pairing`
- `DELETE /api/remote/devices/{clientId}`

关键状态：展示配对码、二维码、连接状态和设备列表；`loom/remote-status` 实时更新。

## 维护规则

- 新增 Pane 时，在 `App.tsx` 的 `view` 状态和 Sidebar 中注册入口。
- Pane 的 API 调用应使用 `web/src/types.ts` 的 `api()`，不要在组件内直接拼
  `fetch` 并绕过错误处理。
- 新增 SSE 事件类型时，同时更新本表和全局事件测试。
- 页面数据源应优先走权威 REST 快照 + 持久 SSE 增量，不依赖 WebUI 本地状态作为
  跨页面真相。
