# codex app-server 协议实测笔记

实测环境:`codex-cli 0.142.5`(darwin/arm64,2026-07-05)。
本文是 codex-hub 的协议依据,来源:

1. 本机对 `codex app-server` 的直接 JSON-RPC 探测(方法表、错误形状为实测)。
2. 参考实现 `pinix-edge/main/handlers/code_agent.mjs`(只读,协议细节:resume 回退、审批应答、turn 生命周期)。
3. 官方文档 <https://developers.openai.com/codex/app-server>(thread/turn/item 模型)。

## 传输层

- `spawn("codex", ["app-server"])`,stdio 三管道;**按行分帧的 JSON-RPC**(每行一个 JSON 对象,`\n` 结尾),无 Content-Length 头。
- 启动前需从环境剔除 `CODEX_SANDBOX`、`CODEX_SANDBOX_NETWORK_DISABLED`、`CODEX_CI`(否则触发受管网络限制)。保留 `CODEX_MANAGED_BY_NPM` / `CODEX_MANAGED_PACKAGE_ROOT`。
- 消息三类,**分发顺序必须先判 server 请求再查 pending**(双方 id 空间可能碰撞):
  - client→server 请求:`{id, method, params}` → 回 `{id, result|error}`
  - server→client 请求(审批等):`{id, method, params}`,client 必须应答 `{id, result}`
  - 通知:`{method, params}`(无 id)

## 握手

```json
→ {"id":0,"method":"initialize","params":{
     "clientInfo":{"name":"codex-hub","title":"Codex Hub","version":"0.1.0"},
     "capabilities":{"experimentalApi":true}}}
← {"id":0,"result":{"userAgent":"codex-hub/0.142.5 (Mac OS ...) ...",
     "codexHome":"/Users/cp/.codex","platformFamily":"unix","platformOs":"macos"}}
→ {"method":"initialized"}          // 通知,无 id;之后稍等 ~100ms 再发请求
```

未 initialize 就发请求会被拒。启动后会先收到一条通知
`remoteControl/status/changed`(可忽略)。

## 方法表(0.142.5 实测完整枚举)

通过对未知方法的报错 `unknown variant ..., expected one of ...` 提取,共 120+ 方法。
codex-hub 用到的核心子集:

| 方法 | 用途 | 关键参数 / 返回 |
|---|---|---|
| `initialize` | 握手 | 见上 |
| `thread/start` | 新建线程 | `{cwd, sandbox}` → `{thread:{id}}` |
| `thread/resume` | 恢复线程(跨进程续上下文) | `{threadId, sandbox, cwd}`;线程 rollout 不存在时报错含 `no rollout` / `not found` → 回退 `thread/start` |
| `thread/read` | 读历史(**运行中也安全**) | `{threadId, includeTurns:true}` → `{thread:{turns:[{id,status,items:[...]}]}}` |
| `thread/archive` | 归档线程(kill 时用) | `{threadId}` |
| `turn/start` | 派任务(异步,响应先回,事件走通知) | `{threadId, input:[{type:"text",text}], approvalPolicy, model?}` → `{turn:{id}}` |
| `turn/interrupt` | 中断当前 turn | `{threadId, turnId?}`(实测缺 `threadId` 报 `missing field \`threadId\``,即方法存在) |
| `turn/steer` | 运行中追加引导(未用,留作扩展) | — |
| `thread/list` | 列全部线程 | `{}` → `{data:[{id,sessionId,preview,...}]}` |

其余可用方法(未用,备查):`thread/fork`、`thread/delete`、`thread/rollback`、
`thread/compact/start`、`thread/name/set`、`thread/turns/list`、`thread/turns/items/list`、
`model/list`、`account/read`、`getAuthStatus`、`fuzzyFileSearch`、`fs/*`、`process/*`、
`command/exec*`、`review/start`、`skills/list`、`mcpServer/*`、`config/*` 等。

错误形状:`{"code":-32600,"message":"Invalid request: missing field `threadId`"}`。

## 通知流(turn 生命周期,item/* 是 source of truth)

turn/start 之后事件全部以通知到达:

- `item/started` / `item/updated` / `item/completed`:`params = {threadId, turnId, item}`
- `item/agentMessage/delta`:`params = {threadId, turnId, itemId, delta}`,`delta`
  为字符串或 `{text}`(两种形状都要兼容)
- reasoning 相关 delta 方法名同样以 `/delta` 结尾,携带 `itemId`,通用处理即可
- `turn/completed`:`params = {turn:{id, status, ...}}`,`status` 可为
  `completed` / `interrupted`;turn 结束的唯一可靠信号

### item 类型(item.type)

| type | 关键字段 |
|---|---|
| `userMessage` | `content:[{text}]` |
| `agentMessage` | `text`,`phase`(`final_answer` = 正式回答,否则视为 thinking) |
| `commandExecution` | `id, command, cwd, status, aggregatedOutput, exitCode, durationMs` |
| `fileChange` | `changes:[{path, kind:{type}, diff}]`(diff 为 unified 格式) |
| `reasoning` | `text` / `summary` |

## 审批(server→client 请求)

- 方法名包含 `requestApproval`(如 `item/commandExecution/requestApproval`),
  带 `id`,**必须应答**:`{"id":<同 id>,"result":{"decision":"accept"}}`,
  decision 取 `accept` / `reject`。
- `turn/start` 传 `approvalPolicy:"never"` + `sandbox:"danger-full-access"`
  可全程免审批(无人值守模式);`on-request` 则会收到审批请求。
- 不认识的 server→client 请求要回 error(`-32601`),不要挂着不答。

## 经验值(来自参考实现,生产验证过)

- sandbox 用 `danger-full-access`,approvalPolicy 默认 `never`。
- 请求超时:普通请求 10~30s;turn 完成不靠请求返回,靠 `turn/completed` 通知。
- turn 看门狗:无活动 30min 或绝对 4h 上限 → interrupt。
- 命令输出截断 4000 字符;diff 截断展示。
- 进程退出即所有 pending 请求置错;线程状态在 `~/.codex` rollout 里,进程死了
  `thread/resume` 就能续——**这是 session 可以跨 hub 重启存活的根基**。
