# loom 使用与 Agent 通信

`loom` 是 CodexLoom 的规范命令行客户端，`chub` 只保留为迁移期兼容二进制。新文档、Agent
Profile、脚本和 Agent 间说明统一使用 `loom`。它面向两类动作：

- 管 Agent/Thread：创建 Agent、启动 Turn、观察、中断、看历史。
- 管 agent 通信：给另一个 agent 发结构化消息、回复消息、查看投递状态。

默认连接 `http://localhost:4870`。如果服务不在默认地址，设置：

```sh
export CODEX_LOOM_URL=http://127.0.0.1:4870
```

## 安装位置与 chub 迁移

当前规范二进制和完整文档位于：

```sh
/Users/cp/Developer/epiral/repos/codex-hub/bin/loom
/Users/cp/Developer/epiral/repos/codex-hub/docs/loom-cli.md
```

只有当前目录是 CodexLoom 仓库时才能使用 `./bin/loom`。其他 Agent 通常工作在自己的仓库，
应使用绝对路径，或把目录加入 `PATH`：

```sh
export PATH="/Users/cp/Developer/epiral/repos/codex-hub/bin:$PATH"
loom agent list
```

旧命令与规范命令的对应关系：

| 旧 chub 用法 | 规范 loom 用法 |
|---|---|
| `chub create NAME --cwd PATH` | `loom agent create NAME --cwd PATH` |
| `chub list` | `loom agent list` |
| `chub get AGENT` | `loom agent get AGENT` |
| `chub send AGENT TEXT` | `loom thread send AGENT TEXT` |
| `chub watch AGENT` | `loom thread watch AGENT` |
| `chub history AGENT` | `loom thread history AGENT` |
| `chub interrupt AGENT` | `loom thread interrupt AGENT` |
| `chub msg ...` | `loom msg ...` |
| `chub kill AGENT` | 已禁用；根据目的选择 `loom thread interrupt` 或 `loom agent archive` |

快速确认当前使用的是新 CLI：

```sh
loom help
```

## CodexLoom Skills

CodexLoom 的共享 CodexHost 会自动加载内置的 `loom-communication`、`loom-needs-you`、`domain-agent-coaching`、`loom-integrations`、`loom-external-messaging`、`loom-parall` 和 `loom-feishu`，不同 cwd 的 Agent 不需要各自复制文件。让用户自己的其他 Codex 工作区也能使用它们时，安装到 `$HOME/.agents/skills`：

```sh
loom skills list
loom skills status
loom skills install
loom skills reload
```

安装器不会覆盖有本地修改的同名 Skill；只有确认替换时才使用 `--force`。完整的作用域、生命周期和验证说明见 [skills.md](skills.md)。

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

Thread 可以同时接收图片或普通文件。Loom 会先把文件快照为 Agent 所有的 Artifact；图片作为 Codex 原生 `localImage` 输入，其他文件通过受管路径进入上下文：

```sh
./bin/loom thread send cici-research "总结这份报告" --attachment /absolute/path/report.pdf
./bin/loom thread send cici-research "看这张截图" --attachment /absolute/path/screenshot.png
./bin/loom thread send cici-research --attachment /absolute/path/data.csv
```

Agent 要把生成文件交付回当前 Thread 时使用：

```sh
./bin/loom artifact publish --from cici-research --file /absolute/path/result.pdf
```

完整生命周期、限制和 WebUI 行为见 [Thread Artifacts](thread-artifacts.md)。

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

查看 Agent directory、正式组织、声明协作和消息活动证据：

```sh
./bin/loom team
./bin/loom team cici-research
./bin/loom team organization add parall-lead parall-edge-dev \
  --description "负责 Parall Edge 领域的实现、验证与交付"
./bin/loom team collaboration add parall-lead pinix-lead \
  --description "遇到 Pinix 核心领域问题时向 pinix-lead 获取判断"
```

`loom team` 是组合读模型：Agent 列表来自 registry 和消息参与者，Profile 描述长期 Identity / Domain / Scope，Organization 描述 parent/child 职责结构，Collaboration 描述声明的跨领域长期协作，Activity evidence 来自 Messages。`loom team <agent>` 聚焦指定 Agent 及相邻关系。

Organization 保证一个 Agent 最多一个直接上级并拒绝环路；Collaboration 不代表上下级。旧的 `loom team link ...` 仍是 Collaboration 的兼容别名。

Profile 和显式关系的管理见 [agent-profile.md](agent-profile.md)。

## Agent 负载观察

`loom workload` 提供组织诊断所需的事实入口。它用于已有负载、Scope 或协作问题的复盘，不是
Agent 排名、健康评分或自动拆分/合并建议。

先看组织，再按 Agent 下钻：

```sh
loom workload --days 7
loom workload loom-coach --days 1 --evidence
loom workload loom-coach --days 30
loom workload loom-coach --days 30 --evidence
loom workload loom-coach --days 90 --json
```

- `--days` 支持 `1`、`7`、`30`、`90`，默认 `7`。`1` 表示本地时区当天零点到当前时刻。
- `--evidence` 展开最多 8 条排队证据，只显示元数据、稳定 ID 和 Web href，不批量输出消息正文。
- `--json` 输出结构化报告，便于 Agent 进一步分析。
- 组织视图按 Agent 名称排列，不按执行占比或等待时间排名。
- 组织 executing 的分母以 observed Agent-days 显示，Turn time 明确标为跨 Agent 汇总值。
- Agent 行的 `current_status` 是当前瞬时状态，executing 和 wait 则属于所选历史窗口。

普通输出始终同时给出 executing、`calendar non-executing proxy`、new-work queue wait、当前
backlog、工作来源和数据质量。`proxy` 不是严格的在线空闲时间：历史在线区间尚未持久化，因此
其中包含机器和 Loom 服务停机时间。等待时间的 `p50/p90/max` 始终带样本数；`n=0` 表示没有
完成的新工作样本，不表示等待时间为零。

`continuation` 会出现在来源明细中，但不计入 headline 的 new-work queue percentiles。`--evidence`
显示的是“当前排队项优先、再按最长等待”的最多 8 条元数据，并明确标注已显示数量和总证据数；
通用页面导航会标为 `generic`，具体 Message / Inbox ID 才是稳定证据标识。

这些信号只用于形成可验证的问题。例如，高等待可能来自容量，也可能来自 active Goal、重启
drain、Connector 或调度策略。需要沿 evidence ID 查看必要上下文，并与相关 Owner / Agent
访谈后再形成组织判断。

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

这只会停止当前 Turn，Agent、Profile、Thread 和团队关系都会保留。归档整个长期 Agent 必须使用
显式命令：

```sh
./bin/loom agent archive cici-research
```

顶层 `loom kill` / `chub kill` 已禁用，因为“停止当前工作”和“归档 Agent”是两种完全不同的
操作，不能由一个含义模糊的命令同时表达。

看历史：

```sh
./bin/loom thread history cici-research --count 5
```

## Goal：跨多个 Turn 的当前成果

Goal 是 Codex Thread 的原生长期工作状态。它与 Agent、Profile、Turn 的关系是：

- Agent 是稳定的领域主体。
- Profile 是 Agent 的长期职责。
- Goal 是这个 Agent 当前要持续取得的阶段性成果。
- Turn 是 Codex 为完成 Goal 执行的一段工作；active Goal 可以自动产生多个 Turn。

创建或查看 Goal：

```sh
loom goal cici-research set "完成 Claude Provider 调研、原型与验收" --token-budget 120000
loom goal cici-research
```

控制生命周期：

```sh
loom goal cici-research pause
loom goal cici-research resume
loom goal cici-research clear
```

编辑目标或预算仍使用 `set`；清除已有预算使用 `--clear-token-budget`。这些命令直接调用 Codex
`thread/goal/get|set|clear`，不会向 Thread 发送一条字面 `/goal` 用户消息。

`active` Goal 在自动 continuation 之间保留 Agent Thread 的下一次执行权：普通 Schedule、外部
Inbox 和无关 Agent Message 继续排队。Agent 在 Goal 工作中发出的 required 请求，其因果回复可以
进入后续 Turn；用户在 Needs You 中的回答也可以继续该 Goal。`paused`、`blocked`、
`usageLimited`、`budgetLimited` 和 `complete` Goal 仍然保留并显示，但不阻挡普通队列；新消息
可以正常进入 Thread，并可能提供解除阻塞所需的信息。

## Agent 向用户请求输入

Inbox 是发给 Agent 的工作队列；`Needs You` 是 Agent 发给用户的请求队列，两者不会混在一起。Agent 只有在确实需要用户的决定、事实、偏好、审阅或授权时才创建 Human Request：

```sh
./bin/loom ask-user \
  --from codex-loom-dev \
  --question "生产迁移使用哪个时间窗口？" \
  --context "今晚更快；明早有完整的运维回滚窗口。" \
  --blocks "安排并发布生产迁移通知" \
  --option "明早（推荐）::风险更低，有完整值守。" \
  --option "今晚::更快，但支持窗口较短。"
```

默认是 `required`；不阻塞当前工作流的反馈使用 `--optional`。命令成功后，请求已经由 Loom 持久化，Agent 应正常结束当前 Turn，不要 sleep 或轮询。用户稍后在 Web 的 `Needs You` 中回答，Loom 会在该 Agent 空闲时，用同一个 Agent Thread 开启关联的新 Turn；即使 Loom 重启或 Agent 当时正忙，回答也会留在队列中。

Human Request 阻塞的是 `--blocks` 指明的工作流，不是整个长期 Agent。能够在 Scope 内低风险独立判断的事情不应上抛给用户。

改名：

```sh
./bin/loom agent rename old-name new-name
```

备份：

```sh
./bin/loom backup --reason before-risky-change
./bin/loom backups
./bin/loom backups prune
```

`backups` 会显示当前压缩快照、总占用和保留策略；`backups prune` 立即按安装策略删除过期快照，
但始终保留配置的恢复底线。默认至少保留最新 2 份，并同时限制为最多 5 份、总计 2 GiB、最长
30 天；SSE event cache 是可重建数据，不进入快照。

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
- 在 envelope 中同时提供消息创建时间 `sent_at` 与本次投递参考时间 `current_time`，让接收方判断排队时长和事件顺序。

## 渐进式 Agent 沟通

主动发起沟通的一方不能替接收方构造上下文。不要假设或猜测对方已经看过什么、知道什么、记得什么、当前优先处理什么、为什么采取某个动作，也不能仅根据 Activity 推断对方的动机和工作语境。

第一轮只陈述属于发起方自己的上下文：在哪里观察到了什么、为什么要开启对话、希望理解哪个全局领域问题。然后先邀请对方从自己的 Domain、历史、约束与当前优先级说明它如何理解这件事。所有权、术语、历史或限制不确定时直接问，不要用推断补齐。

从对方真实提供的上下文出发，后续再逐层对齐理解，深入证据和假设，必要时才收敛到方案、决策人与行动。不要使用“如你所知”“你应该已经……”“这是因为你……”这类替对方预设上下文的表达。

推荐节奏是：

```text
Orient -> Explore -> Clarify -> Deepen -> Converge
```

“不对对方的上下文做预设”不等于发起方不提供自己的上下文。应给出足够背景让对方理解来意，但把解释其处境和补充其上下文的权利留给对方。每条消息只推进一个有价值的层次，通过 `--reply-to` 延续同一讨论；后续问题必须建立在对方实际说过的内容上，而不是预先准备好的完整问卷。交流达到本次目的所需的共同理解后即可停止，并非每次都必须形成完整决策。

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
  --body "现在 Restart Loom 会被 stale running 卡住，我建议改成外部 reloader 接管。"
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
./bin/loom integration send --from codex-loom-dev --reply-to inb_xxx \
  --body-file /absolute/path/to/reply.md \
  --file /absolute/path/to/image.png \
  --file /absolute/path/to/report.pdf
./bin/loom inbox no-reply inb_xxx --agent codex-loom-dev
./bin/loom inbox defer inb_xxx --agent codex-loom-dev --until 2026-07-12T09:00:00+08:00
./bin/loom inbox retry inb_xxx
./bin/loom outbox inbox-canary --state sent
./bin/loom integration send --from inbox-canary --to mem_xxx \
  --body "主动消息" \
  --file /absolute/path/to/image.png \
  --expect-reply none \
  --idempotency-key stable-operation-id
./bin/loom integration send --from ai-community --to mem_xxx \
  --message-id om_thread_root --thread-id omt_topic \
  --body-file /absolute/path/to/correction.md \
  --expect-reply none \
  --idempotency-key stable-thread-followup-id
./bin/loom outbox retry out_xxx
```

内部消息在 Inbox 中使用 `loom:<message-id>` 虚拟 Item ID，物理记录仍在 `comms.ndjson`；
旧 `chub:<message-id>` 深链仍会被兼容解析。
外部消息的 reply/no-reply/defer 会更新 durable Inbox/Outbox；不要用 `loom msg --reply-to` 回复
外部 item，也不要直接调用平台 CLI。`integration send` 是 Agent 对外发送的统一入口：`--reply-to`
从 Inbox 继承 Connection、Address、Conversation 和 thread；`--to` 只接受已启用的 Membership，不能
传原始平台 Conversation ID。需要向这个 Membership 内已经存在的 provider thread 追加新消息时，
可在 `--to` 后提供 `--message-id` / `--thread-id`；它们只指定 Conversation 内的回复位置，每次仍创建
新的 Outbox 并要求新的稳定业务幂等键。飞书 thread 需要 root/target message ID 和 thread ID，Slack
使用 thread ID/thread_ts，Parall 使用 thread root ID。CLI 会先把每个 `--file` 快照复制到 Loom 的受控附件区，再由当前 Connector 上传。
一次最多 8 个附件、单个不超过 25 MB；飞书图片上限为 10 MB。默认等待平台确认，`--async` 可只入队。
Connector 领取 Outbox 或 provider read 时会得到一个持久 attempt token 和 2 分钟 lease；结果必须回传
当前 token。Connector 重连不会立即重放未过期操作，lease 过期后才回收，旧 attempt 的迟到结果会被拒绝。

Parall 上下文读取保留 provider 原语，但凭据仍由 Loom 托管。一个 Agent 可能有多个外部身份，因此每次
读取都必须显式选择 Address：

```sh
./bin/loom prll chats list --address addr_xxx [--limit 20] [--cursor CURSOR]
./bin/loom prll chats get cht_xxx --address addr_xxx
./bin/loom prll chats discoverable --address addr_xxx [--query TEXT]
./bin/loom prll chats members list cht_xxx --address addr_xxx
./bin/loom prll messages list cht_xxx --address addr_xxx [--before msg_xxx] [--after msg_xxx] [--since RFC3339]
./bin/loom prll messages list cht_xxx --address addr_xxx --thread-root-id msg_root
./bin/loom prll messages get msg_xxx --address addr_xxx
./bin/loom prll messages replies msg_root --address addr_xxx [--limit 20]
```

ID 可以写成裸 ID 或 `prll://` reference；输出为 Parall 原生 JSON。命令只开放 chats/messages 的只读操作，
最多读取 100 条。Hub 持久化 operation 审计，managed Gateway 使用对应 Address 所属 Connection 的 key。
发送仍必须使用 `integration send`，不能使用 `loom prll messages send`。

飞书上下文读取使用同一套 durable provider operation，但保留飞书的 `chat_id`、`message_id`、`thread_id`
和分页模型。每次读取必须选择 Address；Hub 只允许读取该 Address 已启用 Membership 的 chat，Gateway
还会核对返回消息真实所属的 `chat_id`：

```sh
./bin/loom lark messages get om_xxx --chat-id oc_xxx --address addr_xxx
./bin/loom lark messages replies om_xxx --chat-id oc_xxx --address addr_xxx [--limit 20]
./bin/loom lark messages list oc_xxx --address addr_xxx [--limit 20] [--page-token TOKEN]
./bin/loom lark messages list oc_xxx --address addr_xxx --thread-id omt_xxx [--limit 20]
./bin/loom lark messages list oc_xxx --address addr_xxx --thread-root-only
./bin/loom lark messages list oc_xxx --address addr_xxx \
  [--start-time UNIX_SECONDS] [--end-time UNIX_SECONDS] [--sort asc|desc]
```

`loom feishu` 是 `loom lark` 的别名。普通 list 使用飞书原生 chat container；thread list 与 replies 使用
飞书原生 thread container，结果中的 `loom_scan` 会说明读取页数与
是否截断。飞书消息读取的 `--limit` 上限为 50。读取群历史需要飞书应用拥有“获取群组中所有消息”权限。该入口只读；回复和主动发送仍走
`loom integration send`。

连接和地址：

```sh
# 安全导入一个已经由 Parall 创建的外部 Agent。key 文件必须为当前用户所有，且权限为 0600 或 0400。
./bin/loom integration import parall \
  --agent ai-community \
  --org-id org_YOUR_ORG \
  --external-agent-id usr_YOUR_AGENT \
  --agent-key-file /absolute/path/to/parall-agent.key

./bin/loom integration connect parall --account <account-ref> --credential-ref env:PRLL_API_KEY
./bin/loom integration connect lark --account cli_xxx \
  --credential-ref keychain:com.codexloom.feishu.cli_xxx
./bin/loom integration connect slack --account T_WORKSPACE --credential-ref env:SLACK_BOT_TOKEN
./bin/loom integration bind codex-loom-dev <connection-id> --identity <external-id> \
  --trigger mention --reply-policy final_answer --dm-policy managed --trust-domain pinix \
  --enabled false \
  --allow-actors <actor-id-1,actor-id-2> --allow-conversations <chat-id> \
  --block-actors <actor-id> --block-conversations <chat-id>
./bin/loom integration list
./bin/loom integration status <connection-id>
./bin/loom integration update-address <address-id> --trigger mention \
  --dm-policy managed --allow-actors <actor-id> --allow-conversations <chat-id>
./bin/loom integration disable <connection-id-or-address-id>
./bin/loom integration enable <connection-id-or-address-id>
```

`integration import parall` 会先验证 Agent key 与 `--external-agent-id` 是否匹配，并确认 WebSocket ticket
可用；随后把 key 写入系统 Keychain、创建或复用 Connection/Address，并安装 managed Gateway。它不要求
Owner key，也不会创建或改名 Parall Agent。重复执行会复用同一稳定身份；失败时会恢复原 credential，
并删除本次新建的 Connection/Address。源 key 文件不会被 Loom 自动删除，确认 Gateway connected 后应由
操作者安全移除。为避免明文传输，CLI 只允许向本机 loopback HTTP 或 HTTPS Loom 地址发送 credential。
不要把 Agent key 放进参数、环境变量、普通 JSON 或消息正文。

第一次成功导入后，后续修复或幂等重跑可以省略 `--agent-key-file`，Loom 会复用 Keychain 中的凭据并再次
验证外部身份。导入器按 provider、外部稳定身份和 Loom Agent 查找已有记录：只有一套 legacy
`org-agent:<external-id>` 记录时原地升级并保留 Connection/Address ID；已经出现新旧重复时，选择 managed
记录作为 canonical，把旧 Connection、Address 和 Membership 标为 `archived` 并写入 `supersededBy`。
历史 Inbox/Outbox 仍可解析旧 ID，但归档记录不能重新启用；旧 Membership 中更完整的角色和策略会投影到
canonical Membership。`integration list` 会明确显示归档关系。

四个 allow/block 参数都可省略。block 优先；同时配置 actor 与 conversation allowlist 时必须同时
匹配。`mention` 只接受 DM、平台结构化 @ 当前 bot，或明确 dispatch；正文中手写 `@名字` 不算。
主动发送必须使用 `integration send --to <membership-id>`，并传稳定 `--idempotency-key`；重试同一
业务动作时复用该 key。Membership 的 `outboundPolicy` 必须为 `proactive`。回复 Inbox 使用
`integration send --reply-to <inbox-item-id>`，不需要重建路由。`outbox send` 和 `inbox reply` 仅保留
为低层兼容与排障入口，不应写入新的 Agent 工作流。

群和私聊的长期行为通过 Conversation Membership 配置。新 Membership 应先禁用、检查，再启用：

```sh
./bin/loom conversation discover <agent> [--address <address-id>]

./bin/loom conversation set <address-id> <conversation-id> \
  --type group --name "群名称" \
  --purpose "长期讨论主题" --role "Agent 在群里的职责" \
  --guidance "该说什么、不说什么、何时交接" \
  --trigger mention --reply-policy final_answer \
  --outbound-policy reply_only --enabled false

./bin/loom conversation set <address-id> <dm-conversation-id> \
  --type dm --actor <stable-actor-id> --name "联系人" \
  --purpose "这段关系的用途" --role "Agent 对此人的角色" \
  --guidance "可讨论内容、隐私边界和升级规则" \
  --trigger direct --reply-policy final_answer \
  --outbound-policy reply_only --enabled false

./bin/loom conversation get <membership-id>
./bin/loom conversation enable <membership-id>
```

`conversation discover` 显示 Connector 观察到该外部身份已经加入、但不一定已经配置的会话。它是只读目录，不会授权群消息；`conversation list` 才是已建立的 Membership。需要排查外部身份已离开的历史会话时追加 `--all`。

更新已有 Membership 时先读取 `version`，再传 `--expected-version <version>`，遇到冲突重新读取并人工合并。完整操作纪律由内置 `loom-integrations` Skill 提供。

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
- `closed`：不需要回复，或请求已被显式取消/收口。

收口原因 `resolution`：

- `reply`：收到正式关联回复。
- `no_reply`：接收方确认无需回复。
- `cancelled`：发起方撤销请求。
- `completed_elsewhere`：结果已经通过其他有审计记录的路径交付。
- `superseded`：请求已经被更新的请求或决定替代。

投递状态 `deliveryStatus`：

- `queued`：目标 agent 正忙，消息在队列里。
- `delivering`：CodexLoom 正在投递。
- `delivered`：已经送进目标 Agent，形成一个 Turn。
- `failed`：投递失败。
- `cancelled`：排队时被取消。

处理状态 `handlingStatus` 与投递状态独立：

- `pending`：尚未形成处理 Turn。
- `running`：某个 Turn 正在处理这条已投递消息。
- `completed`：该次处理正常结束。
- `interrupted`：处理被中断，消息保持 delivered 并进入 held，不会自动重投。
- `failed`：处理 Turn 失败，消息保持 delivered 并等待显式处理。

每次处理都会写入 `handlingAttempts`，保留 attempt id、Turn id、开始/结束时间和结果。

发送后 CLI 会打印 message id 和投递状态，例如：

```text
queued msg_abc123 to pinix-lead - target busy
check: loom msg status msg_abc123
watch: loom msg wait msg_abc123
```

## 查状态、等待、重试、取消

查看消息状态：

```sh
./bin/loom msg status msg_abc123
```

等待投递完成：

```sh
./bin/loom msg wait msg_abc123 --timeout 120
```

原消息对应的 Agent Turn 失败，或历史版本留下了悬空的 `handling` 状态时，原地重试同一 message id：

```sh
./bin/loom msg retry msg_abc123
```

重试会保留原消息、回复链和 handling attempt 历史，不会复制出第二个请求。已形成 Turn 的消息在中断后会保持 `delivered + interrupted`；只有显式执行 `loom msg retry` 或在 UI 点击 Continue 才会开始新的处理 attempt。

撤销请求：

```sh
./bin/loom msg cancel msg_abc123
```

排队中的请求会同时停止投递并关闭；已经 `delivered` 的请求无法从目标 Agent 历史撤回，但可以停止继续等待回复。

如果工作已经通过另一条有审计记录的消息完成，或者请求已被替代，原发起方应显式收口并写明原因：

```sh
./bin/loom msg resolve msg_abc123 \
  --from pinix-lead \
  --resolution completed_elsewhere \
  --reason "结果已在 msg_def456 中交付"

./bin/loom msg resolve msg_abc123 \
  --from pinix-lead \
  --resolution superseded \
  --reason "由新的跨仓任务 msg_def456 替代"
```

Loom 不会根据时间、标题或通信双方自动猜测 reply 关系。

## 忙碌目标的投递规则

如果目标 Agent 正在跑 Turn，CodexLoom 不会强行塞消息。

规则：

- 每个目标 agent 独立排队。
- 一个目标一次只投递一条。
- 目标 Turn 结束后，CodexLoom 立刻尝试投递下一条。
- CodexLoom 重启时，如果消息停在 `delivering`，会恢复为 `queued`，避免丢信。
- 由内部消息启动的 Turn 被中断时，未解决的 `required` 消息保持 `delivered`，handling 状态变为 `interrupted`。它仍在目标 Agent 的 Inbox，但不会自动启动 Turn。
- 由内部消息启动的 Turn 失败时，Delivery 仍为 `delivered`，handling 状态变为 `failed`，使用 `loom msg retry` 显式继续。
- 自动重试只允许发生在 `turn/start` 尚未成功的投递边界之前；一旦 Codex 接受 Turn，后续中断和失败都属于 handling attempt。
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
- 按 All / Waiting / Queued / Stale / Failed 过滤；已投递 24 小时仍未收口的请求进入 Stale。
- 查看一条消息的 replies thread。
- 直接发消息、回复消息、撤销请求，或将已在别处完成/已被替代的请求显式 Resolve。

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
- 每个到点 occurrence 由 `scheduleId + scheduledAt` 唯一标识；Message 提交成功后才推进 Schedule，
  重启恢复会复用同一 message id。
- 目标 agent 忙时沿用 message queue，先显示 `deliveryStatus=queued`，空闲后再投递。
- Agent 在运行中的 Turn 内发出 `response=required` 请求时，message 会记录 `sourceTurnId`。与该请求关联的回复到达后，如果原 Turn 仍是同一个 active Turn，Loom 会通过 `turn/steer` 将回复追加给它；历史中显示 `deliveryMode=turn_steer`。
- 中途注入仅适用于回复，不适用于新请求、通知、schedule 或外部平台消息。回复可以越过普通排队消息，因为它属于当前工作的因果链，但不会改变其他消息的队列顺序。
- 如果原 Turn 已结束、已更换，或 `turn/steer` 因竞态失败，回复保持 `queued`。没有 active Goal 时等 Agent 空闲后用普通 `turn/start` 投递；active Goal 自动 continuation 期间，只有回复该 Agent 先前 required 请求的因果消息可以优先形成后续 Turn，无关消息继续等待。历史中显示 `deliveryMode=turn_start`。
- 默认 `--response required`，目标 agent 必须用 `loom msg --reply-to <message-id> --from <agent> --body "..."` 回复。
- 回复 scheduler 不会再投递给某个真实 Agent；它会记录到 Messages，并把原 schedule message 标为 `answered`。
- `--response none` 适合通知型 schedule，业务状态直接是 `closed`。
