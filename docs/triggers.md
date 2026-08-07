# CodexLoom Triggers

Trigger 是一条持久的外部条件：当可识别的外部事实发生变化时，恢复同一个长期 Agent 的既有工作。

外部事件只是一条“现在值得重新检查”的线索，不是最终业务结论。Agent 被唤醒后必须重新读取 provider 的当前权威状态，确认原目标、候选、依赖和完成条件仍然成立，再决定继续、重建等待、升级给 Owner 或停止。

## 与相邻能力的边界

| 能力 | 语义 |
| --- | --- |
| Agent Message / External Inbox | 人、Agent 或 Conversation 带来了新信息或新工作。 |
| Schedule | 因为一个时间条件到期而开始工作。 |
| Trigger | 因为一个外部事实变化而恢复既有工作。 |
| Goal | Codex Thread 上跨 Turn 持续推进的当前成果。 |

Trigger 不是 workflow builder。它不编排动作图、不执行任意表达式，也不把 provider 账号、仓库或 CI 建模成 Agent。它唯一自动执行的产品动作是：生成一条持久的 Trigger Message，并交给正常 Agent 队列。

## 当前支持范围

v1 使用 provider-neutral 的 Trigger 生命周期，首个 Adapter 是 GitHub：

| Resource | Event | 匹配方式 |
| --- | --- | --- |
| `pull-request` | `merged` | PR 当前已经 merged。 |
| `pull-request` | `closed` | PR 当前 closed 且未 merge。 |
| `pull-request` | `head-changed` | 当前 full HEAD SHA 与创建时提供的 `expectedHead` 不同。 |
| `workflow-run` | `completed` | Workflow run 当前已 completed。 |
| `workflow-run` | `success` | Workflow run 当前 conclusion 为 success。 |
| `workflow-run` | `failure` | Workflow run 当前 conclusion 为 failure。 |
| `workflow-run` | `cancelled` | Workflow run 当前 conclusion 为 cancelled。 |

每条 Trigger 是 one-shot。多个 `--on` 条件使用 ANY 语义。创建时立即读取当前状态，因此条件已经满足时会直接触发，不会等待一个永远不会再出现的边沿事件。

当前 GitHub Adapter 由 Loom 直接调用 GitHub REST API，不启动 `gh pr checks --watch`，也不要求机器安装或登录 `gh`。默认每 30 秒观察一次 active Trigger；可用 `CODEX_LOOM_TRIGGER_POLL_INTERVAL` 调整，最小 1 秒。

## 连接 GitHub

GitHub Connection 属于 Settings > Connectors，不需要 Agent Address 或 Conversation Membership。一个 Connection 表示“GitHub 登录账号 + 可访问的 Resource Owner + 独立凭据”；同一登录账号可以同时连接个人账号和多个 Organization，不会互相覆盖。fine-grained PAT 通常只覆盖创建 Token 时选中的一个 Resource Owner，因此添加 Token 时必须明确填写该 GitHub 用户或 Organization，例如 `parall-hq`。

### OAuth Device Flow

产品维护者先创建 GitHub OAuth App，开启 Device Flow，并把 Client ID 配给 Loom：

```sh
export CODEX_LOOM_GITHUB_CLIENT_ID=Iv1_your_client_id
loom integration connect github
```

CLI 或 WebUI 会显示 GitHub verification URL 和一次性 code。该流程不需要公网 callback、Loom 域名或客户端 secret。授权成功后，Loom 验证 `/user`，把 token 存入 Owner-only managed store，并只在 `integrations.json` 保存 Hub 签发的 `managed:` opaque reference。OAuth 授权被记录为可覆盖所有获准 Resource Owner 的 broad source；当存在更精确的 Owner source 时，Trigger 优先使用精确匹配。

### 用户自己的 token 文件

也可以导入 fine-grained PAT 或 classic PAT：

```sh
chmod 600 /absolute/path/github.token
loom integration connect github --token-file /absolute/path/github.token --resource-owner parall-hq
```

CLI 只接受当前用户所有的普通文件，不接受 symlink，权限必须是 `0600` 或 `0400`；token 不进入参数、日志或命令输出。Loom 验证账号后将 token 写入按“登录账号 + Resource Owner”绑定的 managed entry，源文件保持不变。WebUI 使用 Resource Owner 输入框和密码输入框接收一次性粘贴的 token；验证成功并写入 managed store 后立即清空输入框，Loom 业务数据文件仍只保存 `managed:` 引用。再次添加相同 Owner 会原地更新对应 Connection；添加不同 Owner 会保留现有 Connection 并创建新的 Source。

fine-grained PAT 至少需要目标仓库的 Metadata read；观察 PR 需要 Pull requests read，观察 workflow run 需要 Actions read。Classic PAT 访问私有仓库通常使用 `repo`，只访问公开仓库可使用 `public_repo`。

### 环境引用

无人值守环境可以让 Loom 进程持有环境变量，再保存引用：

```sh
loom integration connect github --credential-ref env:GITHUB_TOKEN --resource-owner parall-hq
```

服务端会先验证 token 和 GitHub login，再按 login + Resource Owner 幂等创建或复用 Connection；明文仍不写入 Loom 数据文件。升级前只有 login、没有 Resource Owner 的旧 GitHub Connection 会保留为 legacy source，不会因为添加 Organization Token 被替换；导入与 login 同名的个人 Owner Token 时才会把该 legacy source 原地明确为个人范围。旧 Trigger 在修复凭据后执行 `resume`，会优先改绑到与其仓库 Owner 精确匹配的新 Source。

检查来源：

```sh
loom trigger source list
```

创建 Trigger 时省略 `--connection`，Loom 会使用 `OWNER/REPO` 中的 Owner 自动选择精确 Source，再回退到唯一 broad OAuth Source。只有多个 Source 同时覆盖同一 Owner 时才需要显式传 `--connection`。

## Agent 使用方式

等待 PR merge：

```sh
loom trigger add github pull-request OWNER/REPO#1970 \
  --from parall-dev-lead \
  --on merged \
  --on closed \
  --resume "Fetch main, verify the expected contract is present, then continue the original delivery flow if the dependency is truly satisfied." \
  --expires 14d
```

监视冻结候选是否漂移：

```sh
loom trigger add github pull-request OWNER/REPO#1971 \
  --from parall-dev-lead \
  --on head-changed \
  --expect-head FULL_SHA \
  --resume "Re-read the PR HEAD and invalidate evidence tied to the previous candidate." \
  --expires 14d
```

等待一次 workflow run 到终态：

```sh
loom trigger add github workflow-run OWNER/REPO/RUN_ID \
  --from parall-dev-lead \
  --on success \
  --on failure \
  --on cancelled \
  --resume "Read the current run and required checks for the candidate SHA, then decide whether the original work can proceed." \
  --expires 2d
```

创建后确认 initial observation：

```sh
loom trigger wait trg_xxx --timeout 30
loom trigger get trg_xxx
```

看到 `armed` 后应正常结束当前 Turn，不要继续 sleep 或轮询。Agent busy 时，触发产生的 Message 与普通新工作一样排队，在 Turn 边界一次投递一条，不会中途 steer 一个无关 Turn。

管理条件：

```sh
loom trigger list parall-dev-lead
loom trigger pause trg_xxx
loom trigger resume trg_xxx
loom trigger cancel trg_xxx
```

## 生命周期与持久化

- `pending`：已保存，正在做初次读取。
- `armed`：初次读取成功，条件尚未满足。
- `paused`：保留定义但停止观察。
- `triggered`：已经创建唯一的 durable Agent Message。
- `cancelled`：由用户或 Agent 显式停止。
- `expired`：等待期限结束。
- `failed`：定义或目标永久无效；临时 provider 错误保留 active 状态并显示 `lastError`。修复凭据或 provider 目标后可显式 `resume` 重试同一定义。

定义和当前投影保存在 `triggers.json`；触发产生的工作记录保存在现有 `comms.ndjson`。Message 先提交，Trigger 再进入 `triggered`；如果进程在两次提交之间退出，启动恢复会从 `triggerId` 修复 Trigger，不会复制消息。

Thread 输入使用专用 `<external_trigger>` envelope，包含 provider event time、Loom observation time、Connection、provider-native subject/event、原 Turn/Goal anchor、bounded snapshot 和恢复指令。Web Feed 将它渲染为 Trigger 卡片，同时允许展开原始 XML 与 snapshot。

Trigger sender 是 `system:trigger`，不是 Team 中的伪 Agent。Capacity 将它标为 `trigger` work source；Team Organization/Collaboration/Activity 不生成一条虚假 Agent 关系。

## UI

- Settings > Connectors：按 Resource Owner 连接一个或多个 GitHub Source、导入 token，并查看每个 Source 的账号、范围和 Connection health。
- Agent > Inspector > Triggers：查看 active/history、创建、暂停、恢复或取消外部条件；Schedule 同页显示为时间触发。
- Agent Thread：触发后显示结构化卡片，区分 occurred / observed 时间，并突出恢复后的第一项动作。

## 当前限制

- v1 只有 GitHub polling Adapter；webhook、deployment、approval 和通用 provider SDK 尚未实现。
- 只支持上述 PR 和 workflow run 事件；required-check aggregate、deployment/environment 和 review 事件尚未实现。
- Trigger 保存 Goal/Turn 因果 anchor，但不会自动改变 Codex Goal 状态，也尚未因 Goal 被替换而自动标记 `superseded`。
- `expired` 与临时 Connection 故障目前只进入 Trigger/Connection 状态，不额外启动 Agent Turn。
- OAuth Device Flow 需要部署者提供自己的 GitHub OAuth App Client ID；没有 Client ID 时使用 token file 或 `env:` 引用。
