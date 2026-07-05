# codex-hub

本地常驻服务,把 **codex session 当作独立主体**来持有:每个 session 是一个有名字、
工作目录和累积上下文的实体,由 hub 管理,不属于任何调用方。人和其他 AI agent
通过同一套 HTTP API 使用同一个 session——派任务、对话、实时观察。调用方断开,
session 不死;多个观察者可以同时看同一个 session 的工作流;hub 重启后 session
列表、历史和事件日志全部还在。

底层:每个 session 对应一个长驻的 `codex app-server` 子进程(OpenAI codex 编码
agent),协议细节见 [docs/codex-app-server-protocol.md](docs/codex-app-server-protocol.md)。

## 架构

```
┌─ chub (Go CLI) ──┐      ┌───────────── codex-hub (Go daemon :4870) ─────────────┐
│ create/send/watch│ HTTP │  httpapi: REST + SSE(seq 回放→实时,多观察者)          │
└──────────────────┘ ───► │  hub:     session 生命周期 / 审批 / 看门狗 / interrupt │
┌─ React Web ──────┐      │  codex:   每 session 一个 codex app-server 子进程      │
│ (Go 二进制内嵌)  │      │  store:   ~/.codex-hub/sessions.json + events/*.ndjson │
└──────────────────┘      └────────────────────────────────────────────────────────┘
```

- **进程模型**:session 创建即 spawn `codex app-server`(懒重启);进程死了或
  hub 重启后,靠 `thread/resume` 续上下文,session 不丢。
- **事件流**:codex 的每条 JSON-RPC 通知都包成 `{seq, ts, type, data}`,追加写入
  该 session 的 ndjson 日志并广播给所有 SSE 订阅者。新观察者先按 seq 回放近期
  事件再接实时,断线用 `Last-Event-ID` 续传。
- **中断语义**:`turn/interrupt` 优先;失败则杀进程兜底(线程状态在 codex rollout
  里,下一轮 resume 恢复)。hub 重启时正在跑的任务标记 `interrupted`。

## 启动

```sh
make release   # 构建 web 控制台 + 两个二进制(bin/codex-hub, bin/chub)
./bin/codex-hub                # 默认 :4870,数据在 ~/.codex-hub
./bin/codex-hub -port 4870 -data ~/.codex-hub   # 显式参数
```

打开 <http://localhost:4870> 即 Web 操作台。环境变量:`CODEX_HUB_PORT`、
`CODEX_HUB_DATA`;CLI 用 `CHUB_URL` 指向非默认 hub。

前置:本机已安装并登录 `codex` CLI(hub 直接 spawn `codex app-server`)。

## CLI 用法

```sh
chub create <name> --cwd <path> [--approval never|on-request] [--sandbox MODE] [--model M]
chub list                      # 所有 session 及状态
chub get <name|id>             # session 详情(JSON)
chub send <name|id> "<task>"   # 派任务,异步,立即返回
chub watch <name|id> [--tail N]  # 终端实时跟随事件流(Ctrl-C 只断观察,任务照跑)
chub interrupt <name|id>       # 中断当前任务
chub history <name|id> [--count N]  # 对话历史(turn/item)
chub approve <name|id> <approvalId> / chub reject ...   # 处理审批
chub kill <name|id>            # 归档线程并移除 session
```

## HTTP API(一页)

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/health` | 健康检查 |
| GET | `/api/sessions` | session 列表(含运行态、待审批) |
| POST | `/api/sessions` | 创建 `{name, cwd, sandbox?, approvalPolicy?, model?}` |
| GET | `/api/sessions/{key}` | 详情(key = id 或 name) |
| DELETE | `/api/sessions/{key}` | kill(归档线程) |
| POST | `/api/sessions/{key}/messages` | 派任务 `{text, timeoutSec?}` → 202 |
| POST | `/api/sessions/{key}/interrupt` | 中断当前任务 |
| GET | `/api/sessions/{key}/history?count=N` | 对话历史(thread/read) |
| GET | `/api/sessions/{key}/events?since=SEQ&tail=N` | **SSE**:按 seq 回放后转实时;支持 `Last-Event-ID` |
| POST | `/api/sessions/{key}/approvals/{approvalId}` | `{decision: accept\|reject}` |
| GET | `/api/events` | **SSE**:hub 级 session 状态流(先发全量快照) |

事件类型:codex 通知原样透传(`item/started`、`item/completed`、
`item/agentMessage/delta`、`turn/completed` …),hub 生命周期事件以 `hub/` 前缀
(`hub/user-message`、`hub/turn-started|completed|interrupted|failed`、
`hub/approval-requested|resolved`、`hub/session-created|killed`、`hub/live`)。

## 开发

```sh
go build ./...            # 后端
cd web && npm run dev     # 前端热更新(代理 /api 到 :4870)
```
