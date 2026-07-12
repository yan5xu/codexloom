# loom 使用与 Agent 通信

`loom` 是 CodexLoom 的规范命令行客户端，`chub` 是兼容二进制。它面向两类动作：

- 管 Agent/Thread：创建 Agent、启动 Turn、观察、中断、看历史。
- 管 agent 通信：给另一个 agent 发结构化消息、回复消息、查看投递状态。

默认连接 `http://localhost:4870`。如果服务不在默认地址，设置：

```sh
export CODEX_LOOM_URL=http://127.0.0.1:4870
```

常用二进制路径：

```sh
./bin/loom
```

## Codex Remote

Remote 是连接共享 CodexHost 的另一种入口，不会改变 Agent 治理机制。常用命令：

```sh
./bin/loom remote status
./bin/loom remote enable
./bin/loom remote pair
./bin/loom remote devices
./bin/loom remote revoke <client-id>
./bin/loom remote disable
```

`pair` 返回二维码 payload、手工配对码和过期时间；也可以在 Web 的 Remote 页面直接扫码。设备名称来自 Remote 客户端，CodexHost 名来自系统 hostname，只用于展示。

## Agent 与 Thread 基本命令

Agent 是稳定治理实体，Codex Thread 是它的主要上下文绑定。新建 Agent 使用领域化命令：

最小命令：

```sh
./bin/loom agent create cici-research --cwd /path/to/project
```

含义：

- `cici-research` 是 Agent 名称，也是之后 `thread send/watch`、`msg` 使用的目标名。
- `--cwd` 是这个 agent 工作时所在的本地目录。它会在这个目录里读代码、跑命令、改文件。
- 名称只能使用字母、数字、下划线和中划线，建议统一用短横线，例如 `codex-loom-dev`、`pinix-lead`、`cici-research`。

创建后检查：

```sh
./bin/loom agent list
./bin/loom agent get cici-research
```

创建时指定模型、思考强度和权限：

```sh
./bin/loom agent create cici-research \
  --cwd /path/to/project \
  --model gpt-5 \
  --effort high \
  --sandbox danger-full-access \
  --approval never
```

常用参数：

- `--model`：使用的模型。
- `--effort`：thinking effort，可用 `minimal` / `low` / `medium` / `high` / `xhigh`。
- `--sandbox`：沙箱策略，例如 `danger-full-access`。
- `--approval`：审批策略，例如 `never` 或 `on-request`。

创建出来的 agent 立刻可以收普通任务：

```sh
./bin/loom thread send cici-research "读 README，总结这个项目的核心"
```

也立刻可以收 agent 通信消息：

```sh
./bin/loom msg cici-research \
  --from codex-loom-dev \
  --subject "请协助调研" \
  --body "请调研 Claude Code 是否能作为 provider 接入 CodexLoom。"
```

查看所有 Agent：

```sh
./bin/loom agent list
```

查看 agent directory 和基于消息观察到的关系：

```sh
./bin/loom team
./bin/loom team links
./bin/loom team cici-research
```

`loom team` 是组合读模型：agent 列表来自 Agent registry 和消息参与者，Profile 描述长期
Identity / Domain / Scope，显式 Relationship 描述声明的长期协作，observed links 来自
Messages。`loom team <agent>` 聚焦指定 Agent 及相邻关系。

Profile 和显式关系的管理见 [agent-profile.md](agent-profile.md)。

查看单个 Agent 详情：

```sh
./bin/loom agent get codex-loom-dev
```

派一个普通任务：

```sh
./bin/loom thread send cici-research "检查 README 里启动说明是否过期"
```

观察 Agent 实时输出：

```sh
./bin/loom thread watch cici-research
```

`Ctrl-C` 只会退出观察，不会中断目标 agent 正在跑的任务。

中断当前任务：

```sh
./bin/loom thread interrupt cici-research
```

看历史：

```sh
./bin/loom thread history cici-research --count 5
```

改名：

```sh
./bin/loom agent rename old-name new-name
```

备份：

```sh
./bin/loom backup --reason before-risky-change
./bin/loom backups
```

## Agent 通信总原则

agent 之间不要手写 XML。统一使用：

```sh
./bin/loom msg ...
```

CodexLoom 会负责：

- 生成标准 `<agent_message>` envelope。
- 把消息记录到全局 `comms.ndjson`。
- 在 Web 的 Messages 页面展示通信历史。
- 如果目标 agent 正忙，先排队，等目标空闲后投递。
- 目标 Agent 收到消息后，在 Agent Thread feed 里以 `REQ` / `RES` / `NOTIFY` 卡片渲染。
- 把 `from/to` 解析为稳定 Agent ID；名称仅作为发送时快照，改名不会破坏历史关系。

## 三种通信语义

### 1. 需要对方回复

用于提问、请求决策、需要对方处理后回报结果的场景。

```sh
./bin/loom msg pinix-lead \
  --from codex-loom-dev \
  --subject "pinixc edge 闪退" \
  --response required \
  --body "我这里观察到 pinixc edge 偶发闪退，请帮忙看一下日志和重启策略。"
```

`--response required` 表示这条消息需要业务回复。消息业务状态会从 `open` 开始。

简写：如果不写 `--response`，默认就是 `required`。

```sh
./bin/loom msg pinix-lead \
  --from codex-loom-dev \
  --subject "请确认重启方案" \
  --body "现在 Restart Hub 会被 stale running 卡住，我建议改成外部 reloader 接管。"
```

### 2. 不需要回复的通知

用于广播状态、同步事实、提醒别人某个用法已经变更。

```sh
./bin/loom msg cici-research \
  --from codex-loom-dev \
  --subject "loom 通信新用法" \
  --response none \
  --body "agent 之间请统一用 loom msg，不要再手写 XML。"
```

`--response none` 表示不需要回复。消息业务状态直接是 `closed`。

### 3. 回复某条消息

回复必须带原消息 id，用 `--reply-to`。

```sh
./bin/loom msg \
  --reply-to msg_abc123 \
  --from codex-loom-dev \
  --body "已修复，验证命令是 go test ./...，线上需要重启后生效。"
```

回复时不要手填 `to`。CodexLoom 会从原消息里反推接收方。

回复投递成功后，原消息会从 `open` 变为 `answered`。

### 4. 明确无需回复

接收方已经处理消息、但没有需要回传的内容时，显式关闭 required 消息：

```sh
./bin/loom msg --no-reply msg_abc123 --from codex-loom-dev
```

结果是 `status=closed`、`resolution=no_reply`。重复执行幂等；一旦 no-reply，就不能再回复，
一旦已有回复，也不能再 no-reply。

## 统一 Inbox 与外部平台

`loom msg` 管 Agent 内部通信，`loom inbox` 是内部 root messages 与外部平台消息的统一读模型。

```sh
./bin/loom inbox
./bin/loom inbox codex-loom-dev --state queued
./bin/loom inbox codex-loom-dev --origin lark
./bin/loom inbox get inb_xxx
./bin/loom inbox reply inb_xxx --agent codex-loom-dev --body "已处理"
./bin/loom inbox no-reply inb_xxx --agent codex-loom-dev
./bin/loom inbox defer inb_xxx --agent codex-loom-dev --until 2026-07-12T09:00:00+08:00
./bin/loom inbox retry inb_xxx
./bin/loom outbox inbox-canary --state sent
./bin/loom outbox send inbox-canary addr_xxx conversation_xxx "主动消息" \
  --expectation none --idempotency-key stable-operation-id
./bin/loom outbox retry out_xxx
```

内部消息在 Inbox 中使用 `loom:<message-id>` 虚拟 Item ID，物理记录仍在 `comms.ndjson`；
旧 `chub:<message-id>` 深链仍会被兼容解析。
外部消息的 reply/no-reply/defer 会更新 durable Inbox/Outbox；不要用 `loom msg --reply-to` 回复
外部 item。

连接和地址：

```sh
./bin/loom integration connect parall --account <account-ref> --credential-ref env:PRLL_API_KEY
./bin/loom integration bind codex-loom-dev <connection-id> --identity <external-id> \
  --trigger mention --reply-policy final_answer --trust-domain pinix \
  --allow-actors <actor-id-1,actor-id-2> --allow-conversations <chat-id> \
  --block-actors <actor-id> --block-conversations <chat-id>
./bin/loom integration list
./bin/loom integration status <connection-id>
./bin/loom integration update-address <address-id> --trigger mention \
  --allow-actors <actor-id> --allow-conversations <chat-id>
./bin/loom integration disable <connection-id-or-address-id>
./bin/loom integration enable <connection-id-or-address-id>
```

四个 allow/block 参数都可省略。block 优先；同时配置 actor 与 conversation allowlist 时必须同时
匹配。`mention` 只接受 DM、平台结构化 @ 当前 bot，或明确 dispatch；正文中手写 `@名字` 不算。
`outbox send` 默认 `expectation=none`；调用方应传稳定 `--idempotency-key`，重试同一业务动作时复用
该 key。回复 Inbox 必须继续用 `inbox reply`，不要把原消息路由手工重建成主动发送。

## 正文输入方式

短消息可以直接放在位置参数：

```sh
./bin/loom msg pinix-lead "帮我看一下 4870 的 launchd 状态" \
  --from codex-loom-dev \
  --subject "launchd 重启失败"
```

也可以用 `--body`：

```sh
./bin/loom msg pinix-lead \
  --from codex-loom-dev \
  --subject "工具缺口" \
  --body "需要一个结构化 tool-gap report 命令。"
```

长消息建议写到文件，再用 `--body-file`：

```sh
./bin/loom msg pinix-lead \
  --from codex-loom-dev \
  --subject "tool-gap: pinixc browser snapshot" \
  --body-file /tmp/tool-gap.md
```

## 投递状态

每条消息有两层状态，不能混用。

业务状态 `status`：

- `open`：需要回复，且还没被回复。
- `answered`：需要回复，且已经收到 reply。
- `closed`：不需要回复。

投递状态 `deliveryStatus`：

- `queued`：目标 agent 正忙，消息在队列里。
- `delivering`：CodexLoom 正在投递。
- `delivered`：已经送进目标 Agent，形成一个 Turn。
- `failed`：投递失败。
- `cancelled`：排队时被取消。

发送后 CLI 会打印 message id 和投递状态，例如：

```text
queued msg_abc123 to pinix-lead - target busy
check: loom msg status msg_abc123
watch: loom msg wait msg_abc123
```

## 查状态、等待、取消

查看消息状态：

```sh
./bin/loom msg status msg_abc123
```

等待投递完成：

```sh
./bin/loom msg wait msg_abc123 --timeout 120
```

取消还在排队的消息：

```sh
./bin/loom msg cancel msg_abc123
```

只有 `deliveryStatus=queued` 的消息可以取消。已经 `delivered` 的消息不能撤回，因为它已经进入目标 Agent 的真实历史。

## 忙碌目标的投递规则

如果目标 Agent 正在跑 Turn，CodexLoom 不会强行塞消息。

规则：

- 每个目标 agent 独立排队。
- 一个目标一次只投递一条。
- 目标 Turn 结束后，CodexLoom 立刻尝试投递下一条。
- CodexLoom 重启时，如果消息停在 `delivering`，会恢复为 `queued`，避免丢信。
- 目标 Agent 不存在或被删除，消息会变成 `failed`。

这意味着发消息后看到 `queued` 是正常的，不是失败。

## 推荐工作流

问一个需要结果的问题：

```sh
./bin/loom msg cici-research \
  --from codex-loom-dev \
  --subject "调研 Claude Code Agent 接入" \
  --response required \
  --body "请调研 Claude Code 是否有类似 app-server 的长生命周期 Agent 接口，重点看能否被 CodexLoom 托管。"
```

等投递完成：

```sh
./bin/loom msg wait msg_abc123 --timeout 300
```

观察对方实际执行：

```sh
./bin/loom thread watch cici-research
```

回复别人：

```sh
./bin/loom msg \
  --reply-to msg_def456 \
  --from codex-loom-dev \
  --body "结论：可以做，但需要先抽象 provider runtime，不能把 Claude 塞进 codex runtime。"
```

发一个无需回复的全局通知：

```sh
./bin/loom msg pinix-lead \
  --from codex-loom-dev \
  --subject "状态同步" \
  --response none \
  --body "codex-research stale running 问题已定位，原因是 rollout 最后一段未闭合但无存活进程。"
```

## Web 侧怎么看

Web 控制台左侧有 `Messages` 入口：

- 查看全局 agent 通信历史。
- 按 All / Open / Queued / Failed 过滤。
- 查看一条消息的 replies thread。
- 直接发消息、回复消息、取消 queued 消息。

Agent Thread feed 中的 agent 通信会特殊渲染：

- `REQ`：需要回复的请求。
- `RES`：回复。
- `NOTIFY`：不需要回复的通知。

## 常见错误

### 忘记 `--from`

错误：

```sh
./bin/loom msg pinix-lead --subject "..." --body "..."
```

正确：

```sh
./bin/loom msg pinix-lead --from codex-loom-dev --subject "..." --body "..."
```

### 需要回复但没留 subject

非 reply 消息必须有 `--subject`，方便 Web Messages 和历史检索。

```sh
./bin/loom msg pinix-lead --from codex-loom-dev --subject "重启失败" --body "..."
```

### reply 时又手填目标

错误：

```sh
./bin/loom msg pinix-lead --reply-to msg_abc123 --from codex-loom-dev --body "..."
```

正确：

```sh
./bin/loom msg --reply-to msg_abc123 --from codex-loom-dev --body "..."
```

### 把 `queued` 当失败

`queued` 只是目标忙。用：

```sh
./bin/loom msg wait msg_abc123 --timeout 300
```

或者到 Web Messages 看投递状态。

## 与普通 `thread send` 的区别

`loom thread send` 是给某个 Agent 启动普通 Turn：

```sh
./bin/loom thread send cici-research "修一下 README"
```

`loom msg` 是 agent 间通信：

```sh
./bin/loom msg cici-research --from codex-loom-dev --subject "请协助" --body "修一下 README"
```

区别：

- `thread send` 不记录全局通信索引，只是普通 Agent Turn。
- `msg` 会进入 `comms.ndjson`，Web Messages 可检索。
- `msg` 有业务状态和投递状态。
- `msg` 会生成标准 agent envelope，目标 Agent 里能看出这是 agent 通信。

经验规则：人直接派活用 `thread send`；Agent 找 Agent 协作用 `msg`。

## 定时任务

定时任务使用独立系统身份 `scheduler`。它不会伪装成人或某个 agent；到点后会向目标 agent 发送标准 agent message。

创建一个每天 09:00 触发、需要回复的 schedule：

```sh
./bin/loom schedule add daily-check \
  --to cici-research \
  --subject "每日仓库健康检查" \
  --cron "0 9 * * *" \
  --tz Asia/Shanghai \
  --body "请检查昨天的改动、测试状态和风险，并用 loom msg --reply-to 回复。"
```

创建一次性 schedule：

```sh
./bin/loom schedule add oneoff-check \
  --to codex-loom-dev \
  --subject "备份恢复检查" \
  --at "2026-07-10T10:00:00+08:00" \
  --body "请检查最近一次备份是否包含 schedules.json 和 rollout 文件。"
```

创建无需回复的通知型 schedule：

```sh
./bin/loom schedule add weekly-notify \
  --to codex-loom-dev \
  --subject "每周提醒" \
  --cron "0 10 * * 1" \
  --tz Asia/Shanghai \
  --response none \
  --body "记得检查备份快照是否能恢复。"
```

管理 schedule：

```sh
./bin/loom schedule list
./bin/loom schedule get sched_xxx
./bin/loom schedule run sched_xxx
./bin/loom schedule disable sched_xxx
./bin/loom schedule enable sched_xxx
./bin/loom schedule delete sched_xxx
```

投递和回复规则：

- schedule 到点后生成一条 `from=scheduler` 的 message，进入全局 Messages 历史。
- 目标 agent 忙时沿用 message queue，先显示 `deliveryStatus=queued`，空闲后再投递。
- 默认 `--response required`，目标 agent 必须用 `loom msg --reply-to <message-id> --from <agent> --body "..."` 回复。
- 回复 scheduler 不会再投递给某个真实 Agent；它会记录到 Messages，并把原 schedule message 标为 `answered`。
- `--response none` 适合通知型 schedule，业务状态直接是 `closed`。
