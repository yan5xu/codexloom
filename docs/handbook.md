# codex-hub 开发手册

这份手册记录 codex-hub 的产品心智模型、架构、数据流、限制和开发流程。它不是 README 的复述，而是开发时要用来判断改动是否正确的工作手册。后续踩到的坑、验证方法和运行约束都要继续补进这里。

## 产品心智模型

codex-hub 是一个本地常驻的 code agent 操作台。它把 codex session 当成独立主体持有，而不是把 session 绑定到某个 CLI、网页、AI 调用方或浏览器连接上。

核心目标：

- 一个 session 有自己的名字、工作目录、threadId、运行态和上下文。
- 人、Web 控制台、`chub` CLI、其他 AI agent 都通过同一套 HTTP API 操作同一个 session。
- 调用方断开不会杀 session；`chub watch` 的 Ctrl-C 只断观察，任务继续跑。
- 多个观察者可以同时订阅同一个 session 的实时事件。
- hub 重启后，session 注册表和 codex 真实历史仍可恢复。

最重要的架构原则：codex-hub 是 codex thread/rollout 之上的薄壳，不维护自己的平行历史。历史的单一真相源是 codex 自己写的 rollout 文件。

## 模块职责

### `cmd/codex-hub`

Go daemon 入口。读取 `CODEX_HUB_PORT` / `CODEX_HUB_DATA`，打开 store，创建 hub，挂 HTTP API 和内嵌 Web UI，默认监听 `:4870`。

收到 SIGINT/SIGTERM 时调用 `Hub.Shutdown()`，再关闭 HTTP server。

### `cmd/chub`

Go CLI。默认访问 `http://127.0.0.1:4870`，可用 `CHUB_URL` 覆盖。

它不直接碰 codex app-server，也不持有 session。所有操作都走 HTTP：

- `create/list/get/send/watch/interrupt/history/approve/reject/kill`
- `watch` 订阅 session SSE，断开只停止观察。
- `history` 调 `/history`，读的是 rollout 解析结果，不是 live event log。

### `internal/hub`

核心编排层，拥有 session 生命周期、codex runtime、审批、事件广播、中断和 watchdog。

关键对象：

- `Session`：持久化元数据，包含 `id/name/cwd/threadId/sandbox/approvalPolicy/model/effort/status/currentTask/currentTurnId/lastError/lastTurn/source`。
- `runtime`：进程内运行态，包含一个 `codex.Client`、初始化状态、当前 turn、待审批请求。
- `Hub`：内存 session map、每 session seq、runtime map、session 订阅者、全局订阅者。

关键规则：

- 不要在持有 `h.mu` 时调用 `client.Request`。codex reader goroutine 处理通知时也会回调 hub 并拿锁，持锁请求容易死锁。
- 创建 session 会立即 spawn `codex app-server` 并 `thread/start`，拿到 threadId 后持久化。
- 发送任务前若 session 是 pinix-edge mirror，会将 `Source` 清空并持久化，表示被 codex-hub 接管。
- 同一 session 同时只能有一个 active turn；运行中重复 `send` 返回 409。
- watchdog 每 5 秒检查，默认 30 分钟无活动 interrupt，绝对 4 小时上限 interrupt。

### `internal/codex`

`codex app-server` 的 line-framed JSON-RPC 客户端。

传输规则：

- spawn `codex app-server`，stdio 每行一个 JSON-RPC 对象。
- 启动前过滤 `CODEX_SANDBOX`、`CODEX_SANDBOX_NETWORK_DISABLED`、`CODEX_CI`，避免触发受管网络限制。
- dispatch 顺序必须先识别 server-initiated request，再查 pending response。审批请求也有 `id` 和 `method`，双方 id 空间可能碰撞。

生命周期：

- `Initialize()` 发送 `initialize`，再发 `initialized` 通知，并等待约 100ms。
- 请求超时后会删除 pending。
- app-server stdout 关闭时标记 closed、失败所有 pending，并触发 `OnClose`。

### `internal/store`

本地持久化层，默认目录 `~/.codex-hub`，可用 `CODEX_HUB_DATA` 覆盖。

布局：

```text
~/.codex-hub/
  sessions.json
  events/<session-id>.ndjson
```

职责边界：

- `sessions.json` 是小注册表，保存 codex-hub 自己拥有的 sessions。
- `events/*.ndjson` 是 live SSE 事件日志，只服务实时回放和断线续传。
- store 不保存完整历史。完整历史在 codex rollout。
- `sessions.json` 原子写入。
- pinix-edge registry 只读：默认读取 `~/.pinix/code_agents/names.json`，可用 `PINIX_EDGE_NAMES` 覆盖。

### `internal/rollout`

读取 codex 的真实历史文件：

```text
~/.codex/sessions/YYYY/MM/DD/rollout-<ISO8601>-<threadId>.jsonl
```

`FindRollout(threadID)` 递归查找匹配 threadId 的 rollout，若有多个取词典序最新。`Read()` 解析为 transcript。

解析策略：

- `event_msg/task_started` 分 turn。
- `event_msg/user_message` 作为干净用户输入，避免把注入上下文当用户消息。
- `event_msg/agent_message` 根据 `phase` 区分 answer/thinking。
- `response_item/function_call` 且 name 为 `exec_command` 时渲染 shell 命令。
- `response_item/function_call_output` 通过 call_id 回连命令输出。
- `event_msg/patch_apply_end` 渲染文件变更。
- `image_generation_call` 会转成 data URI 供 UI 内联展示。

### `internal/httpapi`

HTTP API、SSE 和内嵌 Web UI。

REST：

- `/api/health`
- `/api/sessions`
- `/api/sessions/{key}`
- `/api/sessions/{key}/messages`
- `/api/sessions/{key}/interrupt`
- `/api/sessions/{key}/history`
- `/api/sessions/{key}/approvals/{approvalId}`
- `/api/sessions/{key}/events`
- `/api/events`

SSE 细节：

- session SSE 先订阅，再读历史事件回放，最后转实时，避免回放期间丢事件。
- `Last-Event-ID` 优先，其次 `?since=SEQ`。
- `?tail=N` 只回放最近 N 条事件。
- `?replay=0` 表示前端已经用 rollout `/history` 加载历史，SSE 只从当前 last seq 之后接 live，避免历史和 live event log 重复。
- 每 15 秒写一次 ping 注释。
- 慢订阅者 channel 满时会被移除，客户端应重连并靠 seq 回放补齐。

### `internal/webui`

Go embed Web 构建产物。`web/` 通过 Vite 构建到 `internal/webui/dist`，Go binary 内嵌该目录。

`FS()` 找不到 `index.html` 时返回 nil，HTTP 层会返回 `web console not built (run: make web)`。

### `web`

React/Vite/TypeScript/Tailwind 前端。

当前数据策略：

- `App.tsx` 订阅 `/api/events`，拿全量 session snapshot 和 session status 更新。
- `SessionPane.tsx` 先调 `/history?count=25&offset=0` 从 rollout 加载最新历史。
- 滚到顶部时继续用 offset 分页加载更早 turns。
- 同时打开 `/events?replay=0` 接 live，避免重复展示历史。
- `feed.ts` 把 raw hub/codex events 归并成可渲染 blocks；stream delta、item updated、item completed 通过 itemId 合并。

## 核心数据流

### 创建 session

1. 调用方 `POST /api/sessions`。
2. hub 校验 name/cwd，默认 `sandbox=danger-full-access`、`approvalPolicy=never`。
3. hub spawn `codex app-server`。
4. codex client `initialize` / `initialized`。
5. 无 threadId 时 `thread/start`。
6. hub 保存 `sessions.json`。
7. hub 写入 `hub/session-created` 到 `events/<id>.ndjson` 并广播。

### 发送任务

1. 调用方 `POST /api/sessions/{key}/messages`。
2. hub 确认没有 active turn。
3. 若没有 runtime 或进程已死，spawn 并 `thread/resume`；rollout 不存在时回退 `thread/start`。
4. 若是 edge mirror，清 `Source` 并持久化，表示接管。
5. hub 记录 running 状态，写 `hub/user-message`，广播 global status。
6. hub 启动 watchdog。
7. 调 `turn/start`，透传 session 的 `model` / `effort` / `approvalPolicy`，请求返回后写 `hub/turn-started`。
8. codex 后续通知经 `onNotification` 写入 event log 并广播。
9. 收到 `turn/completed` / `turn/failed` / `turn/aborted` 后，hub 结束 turn，更新 session 为 idle，写 `hub/turn-*`。

### Session 配置

Web header 会显示当前 session 的 model、thinking effort、sandbox 和 approval policy。model/effort 空值表示使用 codex 默认值。

`PATCH /api/sessions/{key}/config` 可以修改：

- `model`
- `effort`：`minimal` / `low` / `medium` / `high`
- `sandbox`
- `approvalPolicy`

配置只允许在 session 空闲时修改；running 时返回 409。修改后写入 `sessions.json`，下一次 `turn/start` 生效。Codex app-server 会把 turn-level overrides 作为同一 thread 后续 turn 的默认设置。

### 查看历史

1. 调用方 `GET /api/sessions/{key}/history?count=N&offset=M`。
2. hub 只用 session 的 threadId。
3. `rollout.Read(threadId)` 找 codex rollout 并解析。
4. 从末尾按 `offset/count` 返回窗口。

这个路径不需要 spawn codex，也不依赖 codex-hub 自己的 ndjson event log。因此 imported/adopted/idle session 也能立即显示完整历史。

### 实时观察

1. 调用方打开 `/api/sessions/{key}/events`。
2. HTTP 层先 `Hub.Subscribe(key)` 注册 live channel。
3. 再从 `events/<id>.ndjson` 读取 `seq > since` 的事件。
4. 写完回放后发送 `hub/live` 标记。
5. 后续从 channel 写实时事件。

前端默认使用 `/events?replay=0`，因为历史已经从 rollout 加载；CLI `chub watch` 默认用 event log tail 回放最近 live 事件。

### 审批

1. codex app-server 发 server request，方法名包含 `approval`。
2. codex client 先按 server request 分发，不与 pending response 混淆。
3. hub 生成 `ap-<rpc-id>`，保存在 runtime approvals。
4. hub 写 `hub/approval-requested`。
5. Web 或 CLI 调 approval API。
6. hub 回应 codex `{"decision":"accept"}` 或 `{"decision":"cancel"}`。

注意：CLI 的 `reject` 最终会映射为 codex decision `cancel`。

### 中断和 kill

中断：

1. 优先调用 `turn/interrupt`，参数包含 `threadId`，有 turnId 时也带上。
2. 如果 interrupt 请求失败，关闭 codex 进程兜底。
3. codex 应发 `turn/completed(status=interrupted)`；若 3 秒内没来，hub 主动 finish bookkeeping。

kill：

1. 若 active turn 存在，先 interrupt。
2. live client 可用时 best-effort `thread/archive`。
3. 关闭 client，删除 runtime 和 session 注册表。
4. 写 `hub/session-killed`，global status 发 killed。

## 已知限制和危险边界

- 同一个 codex thread 同时只能由一个 driver 操作。codex-hub 在接管已有 thread 前会用 `lsof` 找 rollout 文件持有者，并对非本进程/非本 hub 子进程的 holder 发 SIGTERM。这可以修复“看起来 running 但无法 interrupt”的孤儿进程问题，但也意味着接管 edge mirror 会主动终止 pinix-edge 或其他持有该 rollout 的 codex 进程。
- `lsof` 不可用或查不到 holder 时，接管退化为 best-effort，不会报错。
- `sessions.json` 只持久化 codex-hub 自有 session。edge mirror 每次启动从 pinix-edge registry 重新导入，永不写回。
- event log 不是历史真相源。不要从 `events/*.ndjson` 重建完整对话历史。
- `/history` 依赖 rollout 文件存在。刚创建但还未产生/flush rollout 的 session 会返回空历史，不算错误。
- rollout 解析是基于 codex 当前文件格式的适配层。codex 升级后若 event/item 形状变化，历史展示可能漏项，需要补 `internal/rollout` 和前端 block 映射。
- command output 和 diff 展示会截断到约 4000 字符。
- session name 只允许 `[a-zA-Z0-9_-]+`。
- 当前没有鉴权，服务默认是本地操作台。暴露到 Tailscale 地址前，要明确信任网络边界。
- 默认 approval policy 是 `never`，sandbox 是 `danger-full-access`，适合无人值守实战，但风险也真实存在。
- hub 重启时把上次 running 的任务标记为 interrupted，并清理 current task。真实 thread 内容仍在 rollout 中。

## 开发流程

### 常用命令

```sh
go test ./...
go build ./...
make release
./bin/codex-hub
./bin/chub list
```

前端开发：

```sh
cd web
npm run dev
```

Vite dev server 代理 API 到 `:4870`，生产/常驻服务使用 Go embed 的 `internal/webui/dist`。

### 前端改动交付闭环

前端改动不能只看 TypeScript 或 Vite build 通过。交付前必须走完整闭环：

1. `cd web && npm run build`
2. 确认 `internal/webui/dist` 已更新并纳入提交。
3. 重启常驻 hub：优先 `launchctl kickstart`；没有 launchd 管理时手动重启 `./bin/codex-hub`。
4. `curl http://localhost:4870/api/health` 验证服务。
5. 用浏览器真实打开 `http://localhost:4870`，通过截图或 DOM/eval 验证页面和关键交互。

如果只是文档或纯后端改动，不需要提交 dist，但仍要跑与改动匹配的真实验证。

### 重启与自更新

codex-hub 不能由当前承载 agent 的 hub 进程直接 `kill` 自己或粗暴重启 `:4870`。这样会打断当前 session 的控制通道，导致正在执行的修复流程被中断。

正确模型是外部进程接管重启：

- 本地常驻服务优先交给 `launchd` 管理，重启通过 `launchctl kickstart -k <label>` 从进程外执行。
- 开发验证先启动 canary 端口，例如 `./bin/codex-hub -port 4871 -data /tmp/codex-hub-canary`，确认新 binary 和内嵌前端可用后，再切换主服务。
- Web UI 的“更新/重启”按钮调用受保护的 admin API，由 admin API 启动一个脱离当前进程生命周期的 helper 进程。handler 返回后，当前 hub 进程不应在请求栈内直接退出。
- 按钮必须明确展示构建日志、目标 git revision/binary mtime、canary 健康检查结果和最终切换状态；失败时保留旧 `:4870` 服务继续运行。

当前最小实现是“只重启，不 build”：

- Web UI 的 `Restart Hub` 调 `POST /api/admin/restart`。
- API 默认只允许 localhost 请求；若设置 `CODEX_HUB_ADMIN_TOKEN`，则要求 `X-Codex-Hub-Admin-Token` 或 `Authorization: Bearer <token>`。
- 如果有 session 正在 running，restart API 不会立刻切换进程，而是进入 `waiting` 状态，UI 会显示正在等待的 session 名称。等待期间新的 `send` 请求返回 409，避免一边 drain 一边继续塞新任务。
- 当所有 running session 结束后，hub 才会启动 reloader。状态可通过 `GET /api/admin/restart/status` 或 `/api/events` 里的 `hub/restart-status` 观察。
- API 启动同目录的 `codex-hub-reloader` 进程，也可用 `CODEX_HUB_RELOADER` 指向自定义 reloader。
- reloader 收到旧 hub PID、当前 `codex-hub` executable、工作目录和原启动参数；它先让 HTTP handler 返回，再 SIGTERM 旧进程，必要时 SIGKILL，最后启动新的 `codex-hub`。
- reloader 日志默认写入 `/tmp/codex-hub-reloader.log`，可用 `CODEX_HUB_RESTART_LOG` 覆盖；新 hub stdout/stderr 默认写入 `/tmp/codex-hub.log`，可用 `CODEX_HUB_LOG` 覆盖。
- 注意：优雅等待能力在新 binary 被加载后才生效。第一次从旧版本升级到带该功能的版本时，必须确认没有 running session，再用旧按钮或外部命令重启一次。

后续完整“更新并重启”版本：

1. Web UI 放一个本机可见的 admin 按钮。
2. `POST /api/admin/reload` 只在 localhost 或带本机 token 时允许。
3. 后端启动 updater 进程，updater 在仓库目录执行 `npm run build && make build`。
4. updater 先用新 binary 起 canary 端口并 curl `/api/health`。
5. canary 通过后，updater 再调用 reloader 切换主服务。

不要实现“HTTP handler 里 sleep 后 `os.Exit(0)`”这种方案。它看似简单，但会造成请求未收尾、SSE 断流不可控、当前 agent 自己断电，并且失败时没有回滚面。

### 后端改动验证

按风险选择验证层级：

- 协议/rollout/store 变更：至少 `go test ./...`，并用真实 session 验证 `create/send/history/watch` 中受影响路径。
- SSE 变更：用 `curl -N` 或浏览器观察 `/api/events`、`/api/sessions/{id}/events`，确认 replay、live、ping、断线续传语义。
- interrupt/kill/runtime 变更：必须用真实 codex turn 验证。不要只依赖 unit test。
- edge mirror/takeover 变更：准备 pinix-edge registry 或测试 names 文件，验证 mirror 可见、history 可读、send 后被接管并持久化。

### 文档改动验证

文档改动至少检查：

```sh
git diff --check
```

如果文档描述了命令或 API，优先实际跑对应命令或 curl，避免把过期流程写进手册。

## API 速查

```text
GET    /api/health
GET    /api/sessions
POST   /api/sessions
GET    /api/sessions/{key}
PATCH  /api/sessions/{key}/config
DELETE /api/sessions/{key}
POST   /api/sessions/{key}/messages
POST   /api/sessions/{key}/interrupt
GET    /api/sessions/{key}/history?count=N&offset=M
GET    /api/sessions/{key}/events?since=SEQ&tail=N&replay=0
POST   /api/sessions/{key}/approvals/{approvalId}
POST   /api/admin/restart
GET    /api/admin/restart/status
GET    /api/events
```

`key` 可以是 session id 或 name。

## 维护纪律

- 改代码前先确认该路径的真相源：session 注册表、live event log、codex rollout、codex app-server runtime 分别解决不同问题，不要混用。
- 新增历史展示能力优先从 rollout 解析补齐，不要把 live event log 扩展成第二历史库。
- 新增实时能力走 hub event，保证 seq、SSE replay 和多观察者语义。
- 涉及 codex JSON-RPC 的变更要同步更新 `docs/codex-app-server-protocol.md`。
- 每次修复线上/实战问题后，把根因、验证方法和回归风险补进本手册。
