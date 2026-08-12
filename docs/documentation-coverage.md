# CodexLoom 代码文档覆盖矩阵

维护日期：2026-08-02

本文档用于核对代码区域与权威文档之间的关系。目标是让新加入 CodexLoom 的开发者能
从一份文档地图找到：某个代码入口属于哪个领域、由哪份文档负责、当前覆盖到什么程度。
它不是业务语义文档，不替代各领域文档。

## 覆盖状态定义

| 状态 | 含义 |
|---|---|
| 完整 | 主要代码入口、命令/API、对象语义、运行方式和已知边界都有对应文档 |
| 部分 | 有权威文档，但仍有代码入口、新领域或实现细节未落到文档 |
| 缺口 | 当前仓库没有对应权威文档 |

## 覆盖矩阵

| 代码区域 | 主要入口 | 权威文档 | 状态 |
|---|---|---|---|
| 服务入口与构建 | `cmd/codex-loom`、`cmd/codex-loom-reloader`、`Makefile`、`AGENTS.md` | `docs/handbook.md`、根 `README.zh-CN.md` | 完整 |
| CLI 命令面 | `cmd/loom/*.go` | `docs/loom-cli.md` | 完整 |
| HTTP / SSE 契约 | `internal/httpapi/routes_*.go`、`internal/httpapi/server.go` | `docs/http-api.md`、`docs/handbook.md` | 完整 |
| Hub 领域编排 | `internal/hub/*.go` | `docs/handbook.md`、`topics.md`、`triggers.md`、`thread-artifacts.md`、`agent-profile.md`、`integrations.md`、`model-provider.md`、`epoch-context-coverage.md` | 完整 |
| Model Provider 与模型目录 | `internal/hub/model_provider.go`、`internal/hub/provider_switch.go`、`internal/modelcatalog/`、`cmd/loom/commands_provider.go` | `docs/model-provider.md`、`docs/http-api.md`、`docs/loom-cli.md` | 完整 |
| Durable Store 与事件 | `internal/store/*.go`、`internal/backup/*.go`、`internal/rollout/*.go` | `docs/handbook.md`、`docs/technical-debt-audit.md` | 完整 |
| Codex runtime 适配 | `internal/codex/*.go`、`internal/rollout/*.go` | `docs/codex-app-server-protocol.md`、`docs/epoch-context-coverage.md` | 完整 |
| Connector 与 Gateway | `gateway/*.mjs`、`cmd/loom-*-gateway/*.go`、`internal/feishu`、`internal/slack`、`internal/parall`、`internal/github` | `docs/integrations.md`、`docs/agent-platform-integration.md` | 完整 |
| 其他 internal 工具包 | `internal/buildinfo`、`internal/devcanary`、`internal/feishugw`、`internal/webui` | `docs/handbook.md`、`docs/operations.md`、`docs/integrations.md`、`docs/technical-debt-audit.md` | 完整 |
| WebUI | `web/src/*.tsx`、`web/src/*.ts` | `docs/handbook.md`、`docs/webui-panes.md`、`docs/webui-validation.md` | 完整 |
| Skills | `skills/*/SKILL.md`、`internal/hub/agent_skills.go` | `docs/skills.md` | 完整 |
| Owner 与产品语义 | `docs/owner-guide.zh-CN.md`、`docs/product-design.md`、`docs/product-walkthrough.md` | `docs/README.zh-CN.md` | 完整 |
| 测试与质量审计 | `internal/**/*_test.go`、`web/src/*.test.*` | `docs/webui-validation.md`、`docs/technical-debt-audit.md`、`docs/operations.md` | 完整 |
| 运维与恢复 | `bin/*`、`Makefile`、`~/.codex-loom/backups/` | `docs/operations.md`、`docs/handbook.md` | 完整 |

### Hub 文件映射

| Hub 文件 | 权威文档 |
|---|---|
| `agent.go` | `handbook.md`、`loom-cli.md`、`webui-panes.md` |
| `agent_skills.go` | `skills.md` |
| `artifact.go` | `thread-artifacts.md`、`http-api.md` |
| `codex_host.go` | `handbook.md`、`codex-app-server-protocol.md`、`model-provider.md` |
| `collaboration_group.go` | `loom-cli.md`、`webui-panes.md` |
| `communication.go` / `communication_store.go` | `loom-cli.md`、`http-api.md` |
| `connector_delivery.go` | `integrations.md` |
| `context.go` | `epoch-context-coverage.md`、`loom-cli.md` |
| `conversation.go` | `conversation-membership.md`、`integrations.md` |
| `daily_activity.go` | `handbook.md`、`webui-panes.md` |
| `drain.go` | `handbook.md`、`operations.md` |
| `event_maintenance.go` | `handbook.md`、`technical-debt-audit.md` |
| `external_message.go` | `integrations.md`、`agent-platform-integration.md` |
| `goal.go` | `loom-cli.md`、`codex-app-server-protocol.md` |
| `hub.go` | `handbook.md` |
| `human_request.go` | `loom-cli.md`、`webui-panes.md` |
| `inbox.go` | `loom-cli.md`、`integrations.md`、`http-api.md` |
| `integration.go` | `integrations.md`、`http-api.md` |
| `interrupted_turn.go` | `handbook.md`、`loom-cli.md` |
| `lifecycle.go` | `handbook.md`、`operations.md` |
| `model_provider.go` / `provider_switch.go` | `model-provider.md`、`codex-app-server-protocol.md` |
| `organization.go` | `agent-profile.md`、`loom-cli.md` |
| `outbox.go` | `integrations.md`、`http-api.md` |
| `profile.go` | `agent-profile.md` |
| `provider_operation.go` | `integrations.md` |
| `remote.go` | `loom-cli.md`、`webui-panes.md` |
| `scheduler.go` | `loom-cli.md` |
| `shutdown.go` | `handbook.md`、`operations.md` |
| `team.go` | `loom-cli.md`、`webui-panes.md` |
| `topic.go` | `topics.md`、`loom-cli.md` |
| `trigger.go` | `triggers.md`、`loom-cli.md` |
| `usage.go` / `workload.go` | `handbook.md`、`loom-cli.md`、`webui-panes.md` |

## 已识别缺口

当前覆盖矩阵没有已知缺口。若后续新增代码区域、CI/release 门禁或自动化 restore
命令，应先更新本表，再决定是否新增权威文档。

## 维护规则

- 新增代码入口时，先在本文档找到对应代码区域，确认权威文档是否已覆盖。
- 新增 REST/SSE 路由时，更新 `docs/http-api.md`；新增 CLI 子命令时，更新
  `docs/loom-cli.md`。
- 文档声明“完整”后，必须能通过本文档中的代码入口反向找到具体章节，而不是只有一句
  概括。
- 状态降级（从完整改为部分）也要记录原因，避免文档声称覆盖但实际已过期。
