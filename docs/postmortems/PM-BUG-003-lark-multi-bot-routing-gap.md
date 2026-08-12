---
schema_version: 1
id: PM-BUG-003-lark-multi-bot-routing-gap
bug_id: BUG-003
legacy_bug_refs: []
title: 双 Lark Bot 事件不对称导致后台问题无人回复
impact_summary: 客服群中的 qdomain.com 管理后台问题被 CRM 放弃回复，但没有进入 Inwish Inbox，最终无人回复。
severity: P1
status: verified
author: codex
created_at: '2026-08-12T02:21:30-07:00'
trigger_commits:
- 035151cbb9898eea68f865f237f5d2814b51841b
fix_commits:
- fcdf7f13a9a7862e03eec7488b7685fce65f151f
- b3e6370ad9f15d22977d909d28b91576e9aae32b
affected_files:
- internal/hub/inbox.go
- internal/hub/integration.go
- internal/hub/external_message.go
- internal/httpapi/routes_integration.go
- cmd/loom/commands_integration.go
- skills/loom-external-messaging/SKILL.md
- skills/builtin_test.go
affected_modules:
- internal/hub
- internal/httpapi
- cmd/loom
risk_patterns:
- id: external-inbox-owner-transfer
  grep_signals:
  - delegated(To|From)InboxItem
  - pending_delegation
dedup_ack:
  checked_at: '2026-08-12T02:21:30-07:00'
  decision: unrelated
  candidate_ids: []
related_pm: []
supersedes: []
migrated_from: []
evidence:
- type: provider_observation
  value: 2026-08-12 修复前 CRM Inbox inb_d767e7f943de8072 结束为 handled/no_reply 且 Inwish 无同一事件；修复后测试 1756 的 CRM source 为 handled/delegated、Inwish target 为 handled/reply，并取得唯一 Inwish Outbox 的 Lark 文本 delivery receipt。
- type: repo_code
  path: internal/hub/inbox_test.go
- type: commit
  value: b3e6370ad9f15d22977d909d28b91576e9aae32b
detection: user_report
---

# PM-BUG-003 双 Lark Bot 事件不对称导致后台问题无人回复

## Impact

- qdomain.com 管理后台问题在客服群中没有得到任何 Bot 回复。
- CRM 已消费原始事件并选择不回复，Inwish 没有同一事件的 Inbox，因此客服没有可见的处理进度或结果。
- 本轮只确认一条标注路由测试受影响；没有证据支持扩大影响数量。

## Timeline

- 2026-08-12：普通 CRM 消息由 `crm-rebot` 唯一认领并取得 provider delivery receipt，证明 CRM 入口和外发链路可用。
- 2026-08-12：发送标注为非真实故障的后台路由测试。
- 2026-08-12：CRM 创建 Inbox `inb_d767e7f943de8072` 并结束为 `handled/no_reply`；Inwish 没有同一 Lark event 的 Inbox，群内无回复。
- 2026-08-12：确认改为 CRM 唯一入口和 Hub 机械委派；先补失败回归，再实现中央所有权转移。
- 2026-08-12：Mac mini 切换到 `b3e6370ad9f1`，CRM Membership 设为 `all`，Inwish Membership 设为 `explicit_dispatch`，两个 Gateway 恢复 connected。
- 2026-08-12：Lark 测试 `1756` 产生 CRM source `inb_9bf9b94ec2ffdd3c` 与 Inwish target `inb_7c2f32cc7292bbac`；两者共享 `imsg_fbddca944f6a4fc7`，只有 Inwish Outbox `out_9876e6fcf25c549d`，发送成功并取得文本 delivery receipt。

## Proximate Cause

- CRM 的 Agent 判断把后台问题排除后执行 no-reply，但 `inwish-admin` 对应的独立 Lark Bot 没有收到同一原始事件。

## Root Cause

- 系统把“两个 Bot 都会收到同一群事件”当作隐含路由前提，没有为跨 Agent 处理建立 Hub 级显式委派。
- Inbox 模型缺少 source/target 关联、唯一 owner、回复权转移、幂等和跨 NDJSON 中断恢复；因此 Agent 判断正确也不能保证消息实际到达下一处理者。

## Detection

- 用户在 Lark 群发送测试消息后观察到无回复；随后通过 Connection、Inbox 和 Outbox 现场记录定位到事件只到 CRM。
- 现有监控能显示 Connection connected，但不能仅凭 connected 证明某个具体 event 已投递给每个 Bot。

## Resolution

- 新增 `POST /api/inbox/{id}/delegate` 与 `loom integration delegate`。
- source 先后经历 target `pending_delegation` 持久化、source `handled/delegated`、target `queued`；周期队列恢复与启动恢复将半完成 target 收敛为 queued 或 cancelled。
- 定向 TeamRelationship 只是必要授权；Hub 还要求 target 在同 provider/conversation 下恰有一个启用的 `explicit_dispatch` Membership。
- target reply Outbox 使用 target Address/Connection，原 conversation/thread/provider message ID 保持不变。
- 内置 `loom-external-messaging` Skill 把受控 `delegate_command` 定义为 source Agent 的合法终态，并明确成功委派后不得再 reply、no-reply 或 defer。
- Mac mini 已完成 release 与 Membership 切换。真实 Lark 回放确认 source/target 共用同一个原始消息，source 失去回复权，唯一 Outbox 使用 Inwish Address、Membership 与 Connection，且 provider 一次发送成功。

## Action Items

| Type | Action | Owner | Status | Verification |
| --- | --- | --- | --- | --- |
| prevent | 为 Inbox 委派增加唯一 owner、Relationship/Membership 校验、幂等和重启恢复回归 | @codex | done | `go test ./internal/hub -run TestInboxDelegation -count=1` |
| detect | 在 Inbox 投影中保留 source/target ID 与 delegated outcome | @codex | done | Hub/API 回归检查两个 receipt |
| prevent | 在 Mac mini 切换 CRM all、Inwish explicit_dispatch，并保留旧 release 与配置快照 | @codex | done | 部署记录与 `/api/version` |
| prevent | 让内置外部消息 Skill 明确委派是 source 的合法终态 | @codex | done | `go test ./skills -run TestExternalMessagingSkillDocumentsDelegationTerminal -count=1` |
| detect | 从 Lark 客户端发送标注路由测试并保存唯一 Inwish Outbox 的 provider receipt | @thinkrandom | done | source `inb_9bf9b94ec2ffdd3c`、target `inb_7c2f32cc7292bbac`、Outbox `out_9876e6fcf25c549d`、Lark 文本 delivery receipt |

## Lessons Learned

- Connection `connected` 只证明 Gateway 活着，不证明某个具体群事件被每个 Bot 接收。
- 业务分类可以由 Agent 完成，但处理权转移必须是 Hub 可审计、幂等、可恢复的机械动作。
- 唯一回复不能依赖多个 Agent 的提示词自觉；它必须由 Inbox owner 和 Outbox 创建约束保证。
