# CodexLoom Skills

CodexLoom 使用 Skill 保存 Agent 可复用的工作方法。Profile 说明一个长期 Agent 是谁、负责什么、边界在哪里；Skill 说明它遇到某类情境时如何行动。Skill 不拥有领域，也不代替 Thread 中积累的工作轨迹。

## 内置 Skill

CodexLoom 当前内置八个 Skill：

| Skill | 使用者 | 作用 |
|---|---|---|
| `loom-communication` | 所有 CodexLoom Agent | 找到领域负责人，发送 request/notification，回复消息，理解普通排队与因果回复进入 active Turn 的边界；禁止手写 Agent Message XML，也禁止用 sleep/轮询等待回复 |
| `loom-needs-you` | 所有 CodexLoom Agent | 在确实需要用户决定、事实或授权时创建持久 Human Request，区分 required/optional；请求后结束 Turn，不用原生悬挂式提问，也不 sleep/轮询 |
| `domain-agent-coaching` | Loom Coach，也可供其他 Agent 自省 | 用证据和提问梳理长期 Identity、Domain、Scope、Organization 和 Collaboration，不替业务负责人做决定 |
| `loom-integrations` | 管理外部身份或排查外部消息的 Agent | 用 CLI 检查和配置 Connection、Address、群/DM Membership、触发和回复策略，并用真实 Inbox/Outbox 验证投递 |
| `loom-external-messaging` | 需要在飞书、Slack、Parall 等外部会话中回复或发布的 Agent | 根据 Inbox delivery contract 回复、no-reply 或 defer，向已授权 Membership 主动发送文字和附件，并通过 Outbox 验证结果；禁止绕过 Loom 调用平台 CLI |
| `loom-parall` | 需要补充读取 Parall 上下文的 Agent | 使用受管 Address 读取 Parall 原生 chats、messages、replies 和 members；Provider ID 与原生 JSON 对 Agent 可见，但 credential 保留在 Connector；不提供写操作 |
| `loom-feishu` | 需要补充读取飞书上下文的 Agent | 使用受管 Address 读取飞书原生 message、topic replies 和 chat history；Hub 校验启用的 Membership，Gateway 二次校验 chat 归属；不提供写操作 |
| `loom-artifacts` | 收到 Thread 附件或需要交付生成文件的 Agent | 读取 `loom_attachments` 中的受管文件；用 `loom artifact publish` 把最终图片、报告或数据文件快照回当前 Thread；外部平台附件仍走 `loom-external-messaging` |

源文件在 `skills/<name>/`。每个 Skill 只有 Codex 需要的 `SKILL.md` 和 UI 元数据 `agents/openai.yaml`，随 CodexLoom 二进制一起内嵌和版本管理。

## 四种上下文的边界

| 上下文 | 回答的问题 | 生命周期 |
|---|---|---|
| Profile | 我是谁，长期负责什么，什么不属于我 | 跨 Turn 的长期治理定义 |
| Developer context | 我当前以哪个 Agent 身份运行，有哪些应用级约束 | 由 CodexLoom 在需要时注入 Thread |
| Skill | 遇到某类问题时，我应该怎样做 | Codex 按描述发现，触发后加载正文 |
| Team projection | 目前有哪些 Agent，组织、协作和活动关系是什么 | 从当前治理数据实时投影 |

不要把 CLI 命令抄进 Profile，也不要把某个 Agent 的业务 Scope 固化进通用 Skill。Profile 是 `what/why`，Skill 是可复用的 `how`。

## CodexLoom Agent 如何获得 Skill

CodexLoom 启动共享 CodexHost 后，会把内置 Skill 解包到：

```text
$CODEX_LOOM_DATA/builtin-skills
```

默认数据目录下对应 `~/.codex-loom/builtin-skills`。Hub 随后调用 app-server 的 `skills/extraRoots/set` 注册这个根目录。因此：

- 所有由该 CodexHost 承载的 Agent 自动看到同一版本的 Skill。
- Agent 的 `cwd` 可以在任意仓库，不需要逐个复制 `.agents/skills`。
- 发布目录中的 `codex-loom` 会把同目录的 `loom` 加入 CodexHost 的 `PATH`，通信 Skill 不依赖开发机绝对路径。
- 重启 Hub 或重建 CodexHost 后会重新解包和注册，不依赖工作区残留文件。
- 长期 Thread 不需要重建或清空历史；后续 Turn 会使用当前 Skill inventory。

如果 `$HOME/.agents/skills` 已经存在同名 Skill，用户级版本优先，Hub 不会再把该名称放进 extra root，避免 Codex 出现重复的同名 Skill。

这是 CodexLoom 运行时能力，不会自动修改用户的全局 Codex 配置。

Loom 通过 app-server `skills/extraRoots/set` 注册内置 Skill。Codex 在每个 Turn 的 Available Skills catalog 中提供 Skill 的名称、描述和路径；Agent 根据描述匹配当前场景后，再按原生渐进加载机制读取完整 `SKILL.md`。Loom 不在 `turn/start` 中常驻附加完整 Skill，因此普通 Turn 不会反复增加 Skill 正文或把它累积进 Thread 历史。Codex Desktop、Mobile、WebUI、CLI、Schedule 和 Inbox 只要连接同一个 CodexHost，都会看到同一份 catalog。

## 安装到用户自己的 Codex

要让独立运行的 Codex CLI、Desktop 或其他 app-server 在任意仓库中也发现这些 Skill，显式安装到官方用户级目录：

```sh
loom skills list
loom skills status
loom skills install
loom skills reload
```

默认目标是：

```text
$HOME/.agents/skills
```

只安装或检查一个 Skill：

```sh
loom skills status loom-communication
loom skills install loom-communication
```

安装器比较内嵌版本与目标目录：

- `missing`：尚未安装。
- `installed`：文件与当前 CodexLoom 版本一致。
- `modified`：同名 Skill 存在，但包含本地修改。

安装器不会静默覆盖 `modified`。确认要丢弃本地修改时才执行：

```sh
loom skills install loom-communication --force
```

Codex app-server 会监听本地 Skill 文件变化并发出 `skills/changed`。Hub 收到后会为全部 Agent CWD 调用 `skills/list` 并设置 `forceReload: true`；也可以使用 `loom skills reload` 手动刷新并验证。

修改已有 Skill 文件和新增 Skill 名称不是同一种热更新。新增目录后，不要仅凭文件已经出现在一个运行中的 `extraRoots` 下就判断可用；必须执行 `loom skills reload` 或让新版 Hub 重启后重新 materialize 全部内置 Skill 并调用 `skills/extraRoots/set`。开发验收还要用一个全新 Turn 确认 Skill 自动触发；Agent 手工搜索仓库并读取 `skills/<name>/SKILL.md` 不算运行时发现成功。

## 新用户启用流程

新用户只要启动 CodexLoom 并首次打开或创建 Agent，CodexLoom 的共享 CodexHost 就会自动注册内置 Skill，不需要额外安装步骤。首次引导只需要解释两个作用域：

1. **Use in CodexLoom**：自动完成，状态来自共享 CodexHost。
2. **Use in all Codex workspaces**：由用户确认后执行 `loom skills install`。

未来 Web onboarding 的“Install for all Codex workspaces”按钮应调用与 CLI 相同的安装逻辑，不能在前端另写一套复制规则。

Codex 的 Skill 发现位置、符号链接和插件分发规则见 [OpenAI Codex Skills documentation](https://developers.openai.com/codex/skills)。当 CodexLoom 需要通过 marketplace 分发 Skills、connectors 和其他扩展时，应进一步打包为 Codex plugin；当前内嵌机制优先保证自托管 Hub 与 CLI 安装的一致性。

## 修改与验证

修改内置 Skill 后必须完成：

```sh
python3 "$CODEX_HOME/skills/.system/skill-creator/scripts/quick_validate.py" skills/loom-communication
python3 "$CODEX_HOME/skills/.system/skill-creator/scripts/quick_validate.py" skills/loom-needs-you
python3 "$CODEX_HOME/skills/.system/skill-creator/scripts/quick_validate.py" skills/domain-agent-coaching
python3 "$CODEX_HOME/skills/.system/skill-creator/scripts/quick_validate.py" skills/loom-integrations
python3 "$CODEX_HOME/skills/.system/skill-creator/scripts/quick_validate.py" skills/loom-external-messaging
python3 "$CODEX_HOME/skills/.system/skill-creator/scripts/quick_validate.py" skills/loom-parall
python3 "$CODEX_HOME/skills/.system/skill-creator/scripts/quick_validate.py" skills/loom-feishu
go test ./skills ./internal/hub ./cmd/loom
make build
```

再用临时 HOME 验证 `missing -> installed`，用真实 app-server `skills/list` 验证名称、作用域和 `enabled=true`，最后让目标 Agent 在真实消息或组织反思场景中触发 Skill。只验证文件存在或 Go 构建成功不算交付。
