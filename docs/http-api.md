# CodexLoom HTTP / SSE API Reference

本文档以 `origin/main` 的 `internal/httpapi/routes_*.go` 当前注册为准，按领域整理
REST 与 SSE 入口。业务对象、字段和命令语义以对应领域文档为权威；这里只负责 HTTP
层的契约速查。

所有 `/api/...` 响应默认是 JSON。错误响应由 `writeErr` 统一编码，通常包含
`{"error": "..."}` 和对应的 4xx/5xx HTTP 状态。WebUI 之外的客户端应优先使用规范
`/api/agents/...`，不要新增对 `/api/sessions/...` 的依赖。

## 通用约定

- Agent 或 Session 的 `key` 是稳定 ID，不是展示名。
- 列表查询支持按领域过滤参数，例如 `agent`、`status`、`state`、`days`、`date`、
  `tz`、`count`、`offset`。
- 写操作使用 JSON body，字段名按 Go struct 的 `json:"..."` 标签。
- `POST /api/agents/{key}/artifacts` 使用 `multipart/form-data`，字段名为 `file`；
  `?publish=true` 会上传后立即发布。
- Admin 与 Connector 路由使用 `X-Codex-Loom-Admin-Token`、
  `X-Codex-Loom-Connector-Token` 以及兼容头；认证与权限边界见技术债务审计。
- `readOnly` 模式会拒绝非 GET/HEAD/SSE 写请求。

## System and Operations

| Method | Path | Purpose |
|---|---|---|
| GET | `/api/version` | Build, commit, builtAt, dataDir, mode, webAsset |
| GET | `/api/health` | Liveness and current Agent count |
| GET | `/api/usage` | Team token usage overview |
| GET | `/api/workload` | Team workload overview |
| GET | `/api/activity/daily` | Daily activity buckets |
| GET | `/api/remote` | Remote control snapshot |
| POST | `/api/remote/enable` | Enable remote control |
| POST | `/api/remote/disable` | Disable remote control |
| POST | `/api/remote/pairing` | Start pairing session |
| GET | `/api/remote/pairing` | Read current pairing |
| GET | `/api/remote/devices` | List paired devices |
| DELETE | `/api/remote/devices/{id}` | Revoke device |
| POST | `/api/skills/reload` | Reload built-in Skills inventory |
| POST | `/api/admin/restart` | Graceful restart request |
| GET | `/api/admin/restart/status` | Restart state, active Turns, active operations |
| GET | `/api/admin/backups` | List compressed snapshots |
| POST | `/api/admin/backup` | Create snapshot |
| POST | `/api/admin/backups/prune` | Prune expired snapshots |

`GET /api/usage`、`GET /api/workload`、`GET /api/activity/daily` 支持
`from`/`to`/`tz` 或 `days`/`date` 查询参数。没有显式窗口时使用默认窗口。

## Agent, Turn, and Thread

| Method | Path | Purpose |
|---|---|---|
| GET | `/api/agents` | List Agents |
| POST | `/api/agents` | Create Agent |
| POST | `/api/agents/restore` | Restore an Agent from an external key |
| GET | `/api/agents/{key}` | Get one Agent |
| PATCH | `/api/agents/{key}/config` | Update Agent config |
| GET | `/api/agents/{key}/skills` | Get Skill config and inventory |
| PATCH | `/api/agents/{key}/skills/config` | Update Skill config |
| GET | `/api/agents/{key}/profile` | Get Profile |
| PUT | `/api/agents/{key}/profile` | Update Profile |
| GET | `/api/agents/{key}/usage` | Agent token usage |
| GET | `/api/agents/{key}/workload` | Agent workload |
| GET | `/api/agents/{key}/goal` | Get active Goal |
| PUT | `/api/agents/{key}/goal` | Set or update Goal |
| DELETE | `/api/agents/{key}/goal` | Clear Goal |
| POST | `/api/agents/{key}/compact` | Start manual Codex Thread compaction |
| DELETE | `/api/agents/{key}` | Archive Agent |
| GET | `/api/turns/{turnId}` | Get one Turn by stable ID |
| POST | `/api/agents/{key}/turns` | Start a Turn; body `{text, artifactIds, timeoutSec}` |
| POST | `/api/agents/{key}/turns/current/interrupt` | Interrupt current Turn |
| POST | `/api/agents/{key}/turns/interrupted/continue` | Continue interrupted Turn |
| POST | `/api/agents/{key}/turns/interrupted/dismiss` | Dismiss interrupted Turn |
| POST | `/api/agents/{key}/provider` | Switch Agent model Provider |
| GET | `/api/agents/{key}/thread/history` | History from Codex rollout |
| GET | `/api/agents/{key}/thread/events` | Thread SSE stream |
| POST | `/api/agents/{key}/thread/approvals/{approvalId}` | Resolve approval |
| POST | `/api/agents/{key}/artifacts` | Stage/publish artifact |
| GET | `/api/agents/{key}/artifacts` | List published artifacts |
| GET | `/api/agents/{key}/artifacts/{artifactId}` | Download or preview artifact |

`GET /api/agents/{key}/usage` 和 `workload` 支持 `from`/`to`/`tz` 或 `days`。
Turn 启动返回 `202 Accepted`；重启 pending 时相关写入口返回 `409`。

### Model Providers

| Method | Path | Purpose |
|---|---|---|
| GET | `/api/model-providers` | List custom Providers and model catalog status |
| GET | `/api/model-providers/{id}` | Get one Provider projection |
| PUT | `/api/model-providers/{id}` | Upsert Provider definition/credential |
| DELETE | `/api/model-providers/{id}` | Disable custom Provider |
| POST | `/api/model-providers/{id}/verify` | Verify Provider credential and minimal request |

Provider 写操作和 verify 只允许 loopback 或 `CODEX_LOOM_ADMIN_TOKEN`。模型目录
`model_catalog_json` 是 startup-only，重启 Host 后才生效；详细语义见
[model-provider.md](model-provider.md)。

Artifact 下载支持 `?preview=1` 与 `?download=1`。PNG/JPEG/GIF/WebP 允许跨
Origin 嵌入，其他类型保持 `same-origin`。

## Context and Epoch Coverage

| Method | Path | Purpose |
|---|---|---|
| GET | `/api/context/agent-prompt` | Read Loom agent prompt |
| PUT | `/api/context/agent-prompt` | Update Loom agent prompt |
| DELETE | `/api/context/agent-prompt` | Clear Loom agent prompt |
| GET | `/api/agents/{key}/context/explain` | Explain compiled context |
| GET | `/api/agents/{key}/context/coverage` | Read epoch coverage ledger |

`DELETE` 支持 `expectedVersion` 做并发保护。完整语义见
[epoch-context-coverage.md](epoch-context-coverage.md)。

## Organization, Communication, and Team

| Method | Path | Purpose |
|---|---|---|
| GET | `/api/comms` | List internal Agent Messages |
| POST | `/api/comms/messages` | Send Agent Message |
| GET | `/api/comms/messages/{id}` | Get one Message |
| POST | `/api/comms/messages/{id}/cancel` | Cancel queued Message |
| POST | `/api/comms/messages/{id}/retry` | Retry Message |
| POST | `/api/comms/messages/{id}/resolve` | Resolve Message |
| POST | `/api/comms/messages/{id}/no-reply` | Mark no-reply |
| GET | `/api/team` | Team snapshot |
| GET | `/api/team/activity` | Team activity observations |
| GET | `/api/team/relationships` | List collaboration relationships |
| POST | `/api/team/relationships` | Create relationship |
| PATCH | `/api/team/relationships/{id}` | Update relationship |
| DELETE | `/api/team/relationships/{id}` | Delete relationship |
| GET | `/api/team/organization` | List organization relationships |
| POST | `/api/team/organization` | Create organization relationship |
| PATCH | `/api/team/organization/{id}` | Update organization relationship |
| DELETE | `/api/team/organization/{id}` | Delete organization relationship |
| GET | `/api/team/collaboration-groups` | List collaboration groups |
| POST | `/api/team/collaboration-groups` | Create group |
| GET | `/api/team/collaboration-groups/{id}` | Get group |
| PATCH | `/api/team/collaboration-groups/{id}` | Update group |
| DELETE | `/api/team/collaboration-groups/{id}` | Delete group |
| GET | `/api/schedules` | List schedules |
| POST | `/api/schedules` | Create schedule |
| GET | `/api/schedules/{id}` | Get schedule |
| POST | `/api/schedules/{id}/run` | Run schedule |
| POST | `/api/schedules/{id}/enable` | Enable schedule |
| POST | `/api/schedules/{id}/disable` | Disable schedule |
| DELETE | `/api/schedules/{id}` | Delete schedule |

## Topics

| Method | Path | Purpose |
|---|---|---|
| GET | `/api/topics` | List Topics |
| POST | `/api/topics` | Create Topic |
| GET | `/api/topics/{id}` | Get Topic |
| PATCH | `/api/topics/{id}` | Update Topic brief/state |
| POST | `/api/topics/{id}/participants` | Add Participant |
| DELETE | `/api/topics/{id}/participants/{agent}` | Remove Participant |
| POST | `/api/topics/{id}/links` | Link a Goal/Message/Artifact/Provider fact |
| POST | `/api/topics/{id}/send` | Send Owner input to Responsible |
| POST | `/api/topics/{id}/read` | Mark Topic read |
| POST | `/api/topics/{id}/interventions` | Steer/interrupt Participant Turn |
| GET | `/api/topics/{id}/artifacts` | List Topic artifacts |
| GET | `/api/topics/{id}/artifacts/{artifactId}` | Download/preview Topic artifact |

## Triggers

| Method | Path | Purpose |
|---|---|---|
| GET | `/api/triggers` | List Triggers |
| POST | `/api/triggers` | Create Trigger |
| GET | `/api/triggers/sources` | List Connections with trigger capability |
| POST | `/api/triggers/poll` | Poll all armed Triggers now |
| GET | `/api/triggers/{id}` | Get Trigger |
| POST | `/api/triggers/{id}/pause` | Pause Trigger |
| POST | `/api/triggers/{id}/resume` | Resume Trigger |
| POST | `/api/triggers/{id}/cancel` | Cancel Trigger |

## Integrations and External Communication

| Method | Path | Purpose |
|---|---|---|
| GET | `/api/integrations/connections` | List platform Connections |
| POST | `/api/integrations/connections` | Create Connection |
| PATCH | `/api/integrations/connections/{id}` | Update Connection |
| POST | `/api/integrations/connections/{id}/heartbeat` | Connector heartbeat |
| GET | `/api/integrations/connections/{id}/commands` | Connector command catalog |
| POST | `/api/integrations/connections/{id}/outbox/{outboxId}/result` | Connector delivery result |
| POST | `/api/integrations/connections/{id}/provider-operations/{operationId}/result` | Connector operation result |
| GET | `/api/integrations/addresses` | List addresses |
| PATCH | `/api/integrations/addresses/{id}` | Update address |
| POST | `/api/integrations/addresses/{id}/lifecycle` | Preflight/apply archive, restore, delete, or transfer |
| GET | `/api/integrations/address-operations` | List lifecycle receipts; optional `?address=addr_...` |
| GET | `/api/integrations/address-operations/{id}` | Get lifecycle receipt |
| POST | `/api/integrations/address-operations/{id}/rollback` | Preflight/apply clean transfer rollback |
| GET | `/api/agents/{agent}/addresses` | Agent addresses |
| POST | `/api/agents/{agent}/addresses` | Add Agent address |
| GET | `/api/integrations/conversations` | List conversations |
| GET | `/api/integrations/conversations/{id}` | Get conversation |
| PATCH | `/api/integrations/conversations/{id}` | Update conversation |
| GET | `/api/integrations/conversation-candidates` | Discover conversation candidates |
| PUT | `/api/integrations/addresses/{addressId}/conversation-candidates` | Confirm candidate |
| PUT | `/api/integrations/addresses/{addressId}/conversations/{conversationId}` | Attach conversation |
| POST | `/api/integrations/ingress` | Ingest external envelope |
| GET | `/api/human-requests` | List Human Requests |
| POST | `/api/human-requests` | Create Human Request |
| GET | `/api/human-requests/{id}` | Get Human Request |
| POST | `/api/human-requests/{id}/answer` | Answer Human Request |
| POST | `/api/human-requests/{id}/cancel` | Cancel Human Request |
| POST | `/api/human-requests/{id}/retry` | Retry Human Request |
| GET | `/api/inbox` | List external Inbox |
| GET | `/api/inbox/{id}` | Get Inbox item |
| POST | `/api/inbox/{id}/reply` | Reply to Inbox item |
| POST | `/api/inbox/{id}/no-reply` | Mark no-reply |
| POST | `/api/inbox/{id}/defer` | Defer Inbox item |
| POST | `/api/inbox/{id}/retry` | Retry Inbox handling |
| GET | `/api/outbox` | List external Outbox |
| GET | `/api/outbox/{id}` | Get Outbox item |
| POST | `/api/outbox` | Create external send |
| POST | `/api/outbox/{id}/retry` | Retry external send |
| POST | `/api/integrations/send` | Alias/send path for external send |

`POST /api/integrations/addresses/{id}/lifecycle` 接受：

```json
{
  "action": "archive|restore|delete|transfer",
  "targetAgent": "required-for-transfer",
  "dryRun": true,
  "expectedVersion": 3,
  "confirm": "addr_..."
}
```

`dryRun=true` 不需要 `confirm`，返回 `preflight.allowed`、blockers、warnings、当前 version、Membership
计数和 catch-up 边界。实际写操作必须提交当前 `expectedVersion`，且 `confirm` 必须精确等于 Address ID。
Transfer rollback 使用原 transfer receipt ID：dry-run 时只传 `dryRun=true`；实际写入必须传当前 Address
version，并让 `confirm` 精确等于 `aop_*` ID。成功结果包含变更后的 Address 和持久 operation receipt。

### Credential Migration Routes

| Method | Route | Meaning |
|---|---|---|
| GET | `/api/integrations/credentials/preflight?connectionId=...` | Inspect one Connection, or enumerate all eligible Keychain Connections when omitted |
| POST | `/api/integrations/connections/{id}/credential-migration` | Dry-run or migrate one Connection |
| GET | `/api/integrations/credential-migrations/{id}` | Read the non-secret durable receipt |
| POST | `/api/integrations/credential-migrations/{id}/rollback` | Dry-run or restore the receipt's previous reference/gateway anchor |

Migration and rollback bodies use `{"dryRun":true}` for preflight. Actual migration requires `confirm` equal to the
Connection ID; actual rollback requires `confirm` equal to the receipt ID. Responses never read/list/export secret
values and keep the existing Connection/Address/Membership wire shape. They always report
`credentialsIncluded=false`, `runnableRestore=false`, and `backupStatus=credentials_excluded`, because ordinary
backup excludes `credentials/**`. `manual_recovery_required` is a terminal fail-closed state, not a successful restore.

Credential preflight, migrate, receipt, rollback, onboarding, import, setup, and repair routes always require the
explicit `X-Codex-Loom-Admin-Token`, including on loopback. The request Origin must match the request scheme and
host; cross-site browser requests and read-only canaries fail before body parsing, credential resolution, provider
access, or service mutation. While a receipt owns a Connection, writes that would change its provider/account/scope,
canonical credential reference, enabled/archive state, or Address identity return HTTP `409`
`credential_migration_in_progress`.

Before the first managed credential write, the current accepted build must have a bounded, verified ordinary backup
manifest at the current format floor, explicitly excluding `credentials/**`; otherwise the operation returns HTTP
`409 credential_rollback_build_floor_unavailable`. Backup archives with legacy, corrupt, ambiguous, or
credential-bearing manifests are `unknown_unverified`, not proof of exclusion. Rollback dry-run and execution share
the same read-only validator and report a non-secret `rollbackStatus`/`rollbackReason`; only `ready` may proceed.
Gateway migration success requires a fresh heartbeat matching the prepared generation, observed executable digest,
and build. An HTTP success still means only that an operation reached its stated durable phase, not that an unrelated
or later heartbeat has recovered the Connection.

### Provider Setup Routes

| Provider | Exact route |
|---|---|
| GitHub | `POST /api/integrations/providers/github/token` |
| GitHub | `POST /api/integrations/providers/github/credential` |
| GitHub | `POST /api/integrations/providers/github/device` |
| GitHub | `GET /api/integrations/providers/github/device/{id}` |
| Lark / Feishu | `GET /api/integrations/providers/lark/discovery` |
| Lark / Feishu | `POST /api/integrations/providers/lark/credentials` |
| Lark / Feishu | `POST /api/integrations/providers/lark/setup` |
| Lark / Feishu | `POST /api/integrations/providers/lark/operations` |
| Slack | `GET /api/integrations/providers/slack/discovery` |
| Slack | `POST /api/integrations/providers/slack/credentials` |
| Slack | `POST /api/integrations/providers/slack/setup` |
| Parall | `GET /api/integrations/providers/parall/discovery` |
| Parall | `POST /api/integrations/providers/parall/credentials` |
| Parall | `POST /api/integrations/providers/parall/agent-credentials` |
| Parall | `POST /api/integrations/providers/parall/import` |
| Parall | `POST /api/integrations/providers/parall/setup` |
| Parall | `POST /api/integrations/providers/parall/gateway` |
| Parall | `POST /api/integrations/providers/parall/operations` |

Parall gateway repair accepts only an enabled, non-archived Connection with an active Address and a valid
Hub-issued `managed:` credential reference. A legacy `keychain:` or `env:` reference fails before credential
resolution, provider access, or service mutation with HTTP `409` and `{"error":"migration_required"}`; migrate
that Connection through the managed credential operation first. A successful repair response means that the
managed repair/restart was initiated after credential binding, provider identity/status, and WebSocket readiness
checks. It does not assert that a subsequent gateway heartbeat has arrived.

`GET /api/integrations/provider-operations/{id}` 用于读取异步 Provider
Operation 状态。

## SSE Streams

### Agent Thread Events

`GET /api/agents/{key}/thread/events`

- `Last-Event-ID` 优先；也接受 `since`。
- `tail=N` 限制重放条数。
- `replay=0` 只订阅后续 live 事件，不重放历史。
- 重放窗口被压缩时发送 `loom/reconcile`；客户端应重新拉取权威 history。
- 规范流使用 `loom/*` 事件名，兼容流保留 `hub/*` 和 Codex `turn/*` / `item/*`。

### Global Events

`GET /api/events`

- 使用持久单调 seq 作为 SSE `id`。
- `Last-Event-ID` 或 `since` 控制重放。
- cursor 已压缩时发送 `loom/reconcile`。

### Compatibility

`/api/sessions/...` 是 `/api/agents/...` 的兼容别名，用于旧客户端迁移；
`/api/images` 是受管 Artifact 服务化之前的图片读取兼容入口。

| Method | Path | Purpose |
|---|---|---|
| GET | `/api/sessions` | Compatibility list |
| POST | `/api/sessions` | Compatibility create |
| GET | `/api/sessions/{key}` | Compatibility get |
| PATCH | `/api/sessions/{key}/config` | Compatibility update config |
| GET | `/api/sessions/{key}/profile` | Compatibility get profile |
| PUT | `/api/sessions/{key}/profile` | Compatibility update profile |
| DELETE | `/api/sessions/{key}` | Compatibility archive |
| POST | `/api/sessions/{key}/messages` | Compatibility legacy message path |
| POST | `/api/sessions/{key}/interrupt` | Compatibility interrupt |
| POST | `/api/sessions/{key}/approvals/{approvalId}` | Compatibility approval |
| GET | `/api/sessions/{key}/history` | Compatibility history |
| GET | `/api/sessions/{key}/events` | Compatibility SSE |
| GET | `/api/images` | Legacy image serving |

## 维护方式

新增 HTTP/SSE 路由时：

1. 在 `internal/httpapi/routes_*.go` 按领域注册，不要堆进 `server.go`。
2. 更新本文档对应表格。
3. 如果是新领域，同时更新 `docs/README.md` 的文档索引。
4. 验证 `/api/...` 在重启后的二进制中返回 JSON；HTML fallback 说明二进制内嵌
   WebUI 已过期。
