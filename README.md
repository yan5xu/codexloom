# codex-hub

本地常驻服务,把 **codex session 当作独立主体**来持有:每个 session 是一个有名字、
工作目录和累积上下文的实体,由 hub 管理,不属于任何调用方。人和其他 AI agent
通过同一套 HTTP API 使用同一个 session——派任务、对话、实时观察。调用方断开,
session 不死;多个观察者可以同时看同一个 session 的工作流;hub 重启后 session
列表、历史全部还在。

底层:每个 session 对应一个长驻的 `codex app-server` 子进程(OpenAI codex 编码
agent),协议细节见 [docs/codex-app-server-protocol.md](docs/codex-app-server-protocol.md)。

## 核心设计:薄壳,不存历史

codex-hub 是 codex thread 库之上的**薄壳**,不维护自己的平行历史日志:

- **历史直接读 codex rollout 文件**。任何 session 的真实历史本来就存在 codex 自己写的
  rollout 文件里(`~/.codex/sessions/YYYY/MM/DD/rollout-<ISO>-<threadId>.jsonl`)。
  `chub history` / `GET .../history` 按 threadId glob 找到 rollout 文件、直接解析——
  见 [`internal/rollout`](internal/rollout/rollout.go)。因此**导入/接管的 session
  立刻显示完整历史**,不存在"迁移/转换"这一步。
- **只保留一份极小的注册表**:`~/.codex-hub/sessions.json` 存 name →(threadId, cwd)
  加少量运行态,**只存映射,不存历史/事件**。注册表由 hub 进程在内存里管、原子落盘,
  外部改文件不会互相覆盖。
- **已有 session 的来源**:启动时读 pinix-edge 的 `~/.pinix/code_agents/names.json`
  (只读镜像,永不回写),edge 建的 session 自动出现在这里、历史立即可看。给某个 edge
  镜像 `send` 任务即把它"接管"为 codex-hub 自己的 session(落盘持久化)。
- **实时 vs 历史**:codex-hub 正在驱动的活 session,实时事件仍从 app-server 流来并经
  ndjson 事件日志广播给 SSE 订阅者(仅用于 live);查看非驱动中的 session,历史一律走
  rollout。两者能对上——app-server 就是往同一个 rollout 追加。

## 架构

```
┌─ chub (Go CLI) ──┐      ┌───────────── codex-hub (Go daemon :4870) ─────────────┐
│ create/send/watch│ HTTP │  httpapi: REST + SSE(seq 回放→实时,多观察者)          │
└──────────────────┘ ───► │  hub:     session 生命周期 / 审批 / 看门狗 / interrupt │
┌─ React Web ──────┐      │  codex:   每 session 一个 codex app-server 子进程      │
│ (Go 二进制内嵌)  │      │  rollout: 历史直接读 ~/.codex/sessions/**/rollout-*   │
│                  │      │  store:   注册表 sessions.json + live events/*.ndjson │
└──────────────────┘      └────────────────────────────────────────────────────────┘
```

- **进程模型**:session 创建即 spawn `codex app-server`(懒重启);进程死了或
  hub 重启后,靠 `thread/resume` 续上下文,session 不丢。
- **历史**:`GET .../history` 不碰事件日志,直接读该 thread 的 rollout 文件解析成
  turn/item(user / answer / thinking / command / file_change)。查历史不需要 spawn codex。
- **事件流(仅 live)**:hub 正在驱动的 session,codex 的每条 JSON-RPC 通知包成
  `{seq, ts, type, data}` 追加进 ndjson 日志并广播给 SSE 订阅者。新观察者先按 seq 回放
  再接实时,断线用 `Last-Event-ID` 续传。
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
| GET | `/api/sessions/{key}/history?count=N` | 对话历史(直接读 codex rollout 文件) |
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
