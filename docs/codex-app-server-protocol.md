# codex app-server 协议实测笔记

基础枚举实测环境：`codex-cli 0.142.5`（darwin/arm64，2026-07-05）。
`thread/inject_items` 另在 `codex-cli 0.144.1`（2026-07-10）完成实战验证。
本文是 CodexLoom 的协议依据，来源：

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
     "clientInfo":{"name":"codex-hub-remote","title":"CodexLoom","version":"0.1.0"},
     "capabilities":{"experimentalApi":true}}}
← {"id":0,"result":{"userAgent":"codex-hub-remote/0.144.1 (Mac OS ...) ...",
     "codexHome":"/Users/cp/.codex","platformFamily":"unix","platformOs":"macos"}}
→ {"method":"initialized"}          // 通知,无 id;之后稍等 ~100ms 再发请求
```

未 initialize 就发请求会被拒。`clientInfo.name` 是 Remote enrollment 的持久 scope；CodexLoom
保留旧 wire identity `codex-hub-remote`，避免产品改名和共享 Host 迁移让已配对设备失效，
用户可见标题仍是 CodexLoom。启动后可能先收到 `remoteControl/status/changed`，必须更新 Remote
状态读模型，不能覆盖 Thread notification callback。

## 共享 CodexHost

CodexLoom 只 spawn 一个 app-server。Agent runtime 只保存 `agentId -> threadId`、active Turn 和
审批 bookkeeping，不拥有独立进程。Remote 也通过同一个 app-server 的 `remoteControl/*` 能力
接入，不再启动第二个 app-server。

app-server 为每个初始化 transport connection 维护独立 connection state。Thread 被 start/load
后会把 listener 附着到当前 initialized connections；Remote client 的 Turn 因此也会出现在 Loom
stdio connection 上。通知必须先提取 `threadId` 再路由，不能按“当前 active Agent”全局猜测。

`turn/started` 只有 `{threadId, turn}`。当 Remote resume 一个 Loom 未登记的旧 Thread 时，先创建
占位 Agent 保住后续 Item，再异步调用 `thread/read {includeTurns:false}` 回填 `cwd/name`。

## 方法表(0.142.5 实测完整枚举)

通过对未知方法的报错 `unknown variant ..., expected one of ...` 提取,共 120+ 方法。
CodexLoom 用到的核心子集：

| 方法 | 用途 | 关键参数 / 返回 |
|---|---|---|
| `initialize` | 握手 | 见上 |
| `thread/start` | 新建线程 | `{cwd, sandbox}` → `{thread:{id}}` |
| `thread/resume` | 恢复线程(跨进程续上下文) | `{threadId, sandbox, cwd}`;线程 rollout 不存在时报错含 `no rollout` / `not found` → 回退 `thread/start` |
| `thread/read` | 读元数据或历史(**运行中也安全**) | `{threadId, includeTurns:false}` 回填 adoption 元数据；`true` 返回 turns/items |
| `thread/goal/get` | 读取 Thread 当前原生 Goal | `{threadId}` → `{goal: ThreadGoal|null}` |
| `thread/goal/set` | 创建或更新 Goal | `{threadId, objective?, status?, tokenBudget?}` → `{goal}`；省略字段表示保持，`tokenBudget:null` 表示清除预算 |
| `thread/goal/clear` | 清除 Goal | `{threadId}` → `{cleared}` |
| `thread/inject_items` | 不启动 turn，向模型可见历史追加原始 Responses API item | `{threadId, items:[...]}`；需要 `experimentalApi:true` |
| `thread/archive` | 归档 Agent 时归档其线程 | `{threadId}` |
| `thread/unarchive` | 恢复误归档线程 | `{threadId}` → `{thread}`；0.144.1 实测 |
| `turn/start` | 派任务(异步,响应先回,事件走通知) | `{threadId, input:[{type:"text",text},{type:"localImage",path}], approvalPolicy, model?}` → `{turn:{id}}` |
| `turn/interrupt` | 中断当前 turn | `{threadId, turnId?}`(实测缺 `threadId` 报 `missing field \`threadId\``,即方法存在) |
| `turn/steer` | 将因果关联的 Agent reply 追加到仍在运行的原 Turn | `{threadId, input:[{type:"text",text}], expectedTurnId}` → `{turnId}`；只接受精确 active Turn，失败时回退 message queue |
| `thread/list` | 列全部线程 | `{}` → `{data:[{id,sessionId,preview,...}]}` |

其余可用方法(未用,备查):`thread/fork`、`thread/delete`、`thread/rollback`、
`thread/compact/start`、`thread/name/set`、`thread/turns/list`、`thread/turns/items/list`、
`model/list`、`account/read`、`getAuthStatus`、`fuzzyFileSearch`、`fs/*`、`process/*`、
`command/exec*`、`review/start`、`skills/list`、`mcpServer/*`、`config/*` 等。

错误形状:`{"code":-32600,"message":"Invalid request: missing field `threadId`"}`。

## 原生 Thread Goal

Goal 是 Codex 拥有的 Thread 级持久状态，不是 Loom Message，也不是一个超长 Turn。一个 Thread
同时只有一个当前 Goal；Codex 负责状态持久化、Token/时间统计、上下文压缩后的延续，以及 active
Goal 在 Turn 结束后的自动 continuation。Loom 不把 Goal 写进自己的 store，只维护来自原生 API
和通知的内存投影。

`ThreadGoal` 的稳定字段为：

```json
{
  "threadId": "019f...",
  "objective": "完成 Connector 回执审计并交付修复",
  "status": "active",
  "tokenBudget": 120000,
  "tokensUsed": 4300,
  "timeUsedSeconds": 92,
  "createdAt": 1784080000,
  "updatedAt": 1784080092
}
```

状态为 `active`、`paused`、`blocked`、`usageLimited`、`budgetLimited`、`complete`。通知为：

- `thread/goal/updated {threadId, turnId?, goal}`
- `thread/goal/cleared {threadId}`

关键运行语义：

1. `thread/goal/set` 创建 active Goal 后，已加载的 Thread 会由 Codex 自动开始或继续工作。
2. 共享 Host 启动时，Loom 对已登记 Thread 调 `thread/goal/get`；只对 active Goal执行
   `thread/resume`，把自动 continuation 交还 Codex。
3. active Goal 可能正处于两个 Turn 之间。Loom 的 `Agent.status=idle` 只表示当前没有 active Turn，
   不能据此把普通队列工作插进未结束 Goal。
4. Loom 的 UI、HTTP 与 CLI 直接映射 `thread/goal/*`。发送字符串 `/goal ...` 不是 Goal API。
5. Agent 可通过 Codex Goal tools 将 Goal 标记为 complete 或 blocked；用户控制创建、编辑、暂停、
   恢复、预算和清除。

## `thread/inject_items`

这个方法适合把系统拥有的长期上下文写入 thread，而不伪装成用户新发的一次任务。它会持久化
到 Codex rollout，并在后续 turn 中进入模型可见历史，但不会触发 `turn/start`，也不会产生
一条普通用户消息。

CodexLoom 用它注入 Agent Profile：

```json
{
  "id": 17,
  "method": "thread/inject_items",
  "params": {
    "threadId": "019f...",
    "items": [{
      "type": "message",
      "role": "developer",
      "content": [{
        "type": "input_text",
        "text": "<agent_profile version=\"2\" ...>...</agent_profile>"
      }]
    }]
  }
}
```

注意：

- 方法名确实是带下划线的 `thread/inject_items`，不是 camelCase。
- 必须先完成 `initialize`，并声明 `capabilities.experimentalApi:true`。
- 应在 Agent 空闲、runtime ready 且 `thread/resume` / 回退 `thread/start` 完成后调用。
- Profile 用 developer role，避免污染用户输入语义；`loom thread history` 的 Turn 投影不会把它显示为用户消息。
- hub 用 `profileVersionSeen` 保证一个 Profile 版本只在下一个安全 turn 前注入一次；同一 runtime
  的 ready、注入和 turn reservation 必须串行，防止并发派任务重复注入。

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

- 方法名包含 `approval`(如 `item/commandExecution/requestApproval`),
  带 `id`,**必须应答**:`{"id":<同 id>,"result":{"decision":"accept"}}`；当前实现将拒绝映射为
  `cancel`，并以 app-server 返回的 `availableDecisions` 为准。
- `turn/start` 传 `approvalPolicy:"never"` + `sandboxPolicy:{"type":"dangerFullAccess"}`
  可全程免审批(无人值守模式);`on-request` 则会收到审批请求。
- 不认识的 server→client 请求要回 error(`-32601`),不要挂着不答。

## 经验值(来自参考实现,生产验证过)

- 当前随 Loom 部署的 Codex `0.144.1` 在 `thread/start` / `thread/resume` 的 sandbox
  仍使用 `danger-full-access`；`turn/start` 使用
  `sandboxPolicy:{"type":"dangerFullAccess"}`，approvalPolicy 默认 `never`。
- 请求超时:普通请求 10~30s;turn 完成不靠请求返回,靠 `turn/completed` 通知。
- turn 看门狗:无活动 30min 或绝对 4h 上限 → interrupt。
- 命令输出截断 4000 字符;diff 截断展示。
- CodexHost 退出即所有 pending 请求置错，active Turn 标记 interrupted；Agent/Thread binding 和
  rollout 仍在，下一次共享 Host 启动后 `thread/resume` 可继续。这是 Agent 跨 Loom 重启存活的根基。
