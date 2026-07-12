# Agent Profile：长期协作身份

## 定义

在 CodexLoom 中，Agent 是有稳定 ID、并绑定一个主要 Codex Thread 的长生命周期主体。它不是执行完一次
task 就销毁的 worker，而是持续维护一个领域、积累上下文，并与用户和其他 Agent 反复互动
的长期主体。

Agent Profile 是这个主体对团队公开的长期领域说明：

> 我是谁，长期生活在哪个领域，以及我在其中负责到哪里。

Profile 的目的不是描述一次任务怎样调用，也不是枚举 Agent 当前会哪些工具。它让一个新
Agent 能理解团队成员的领域和边界，并据此建立长期协作关系。

## 三个字段

Profile 只有三个可选业务字段。

### Identity

说明这个长期主体是谁、在团队中以什么身份存在。

Identity 不是显示名称。名称可以修改；Profile 始终绑定稳定 `agentId`。

### Domain

说明 Agent 长期理解和维护的主题世界。Domain 应描述持续存在的业务领域，而不是列举临时
任务或技能。

例如“基于 Codex 的 Agent 治理，包括长期 Agent、共享 Host、Web/CLI、Team、通信与运行可靠性”，
比“会写 Go、会做 React”更接近 Domain。

### Scope

说明 Agent 在 Domain 中负责什么、拥有何种决策边界，以及明确不负责什么。Scope 可以
包含责任、资源边界、交接条件和需要用户确认的动作。

三个字段都允许为空。系统不会因为 Profile 不完整而阻止 Agent 创建、运行或收消息。
Profile 应随着长期领域逐步形成，而不是成为创建 Agent 时必须填完的大表单。

新 Agent 默认没有 Profile，版本为 `0`。提交一个全空表单不会创建 Profile、不会落盘，也
不会安排 developer message；只有通过 CLI 或 Web UI 显式写入至少一个字段后，Profile 才
开始版本化并在下一个安全 turn 前注入。

## 如何思考 Profile

不要从“这个 Agent 眼下要完成什么任务”开始写。先把它当成一个会长期存在、经历多次 turn、
持续积累上下文并反复与团队协作的主体，然后回答三个问题：

1. 它以什么长期身份存在？
2. 哪一类持续出现的问题应该路由给它？
3. 它负责到哪里，在哪里必须协作或交接？

### 第一步：寻找长期不变量

回看这个 Agent 最近做过的事情和 Messages，但不要直接复制任务历史。提取反复出现且未来
仍会存在的名词、系统边界和责任，例如 Agent lifecycle、CodexHost、运行可靠性，而不是
“修复昨天的重启问题”。

可以用四个测试筛选内容：

- **时间测试**：三个月后这句话大概率仍然成立吗？
- **路由测试**：新 Agent 看完后，知道什么问题应该来找它吗？
- **边界测试**：团队知道什么事情不应默认由它处理吗？
- **频率测试**：如果每个 turn 都可能变化，它通常不属于 Profile。

### 第二步：分别写 Identity、Domain、Scope

Identity 用一句话说明长期角色。它不重复 Agent 名称，不写模型版本，也不塑造与职责无关的
人格。例如“CodexLoom 的长期产品工程维护者”。

Domain 描述它长期生活的主题世界。优先写业务对象、系统边界和持续问题，使团队能够据此
路由工作；不要把“会 Go、React、搜索”这样的技能列表当成领域。

Scope 描述责任和边界，通常需要回答：

- 长期拥有和维护什么？
- 为其他 Agent 提供什么支持？
- 哪些决策可以自主完成？
- 哪些外部系统或高风险动作必须交给其他主体？

### 第三步：为协作可发现性检查

假设一个完全不了解团队的新 Agent 只看到 Profile。它应该能判断“遇到什么问题找谁”，但
不需要从 Profile 了解所有协作者和历史。具体协作对象写入 Team Relationship，实际请求与
回复留在 Messages；不要把两者复制进 Scope。

Profile 可以稀疏。领域尚未稳定时，只填写已经确定的部分，比用套话补满三个字段更准确。
后续只有长期身份、领域或责任边界发生实质变化时才更新版本。

### 多行写作模板

三个字段在存储中仍然是字符串，但 CLI、Web UI 和 developer context 都保留换行。较长的
Domain 或 Scope 应使用空行、小标题和列表组织：

```text
Identity
<产品或领域>的长期<角色>。

Domain
<这个 Agent 长期维护的主题世界>。

覆盖：
- <长期对象或子领域>
- <长期对象或子领域>

Scope
长期职责：
- <拥有并持续维护的能力或边界>
- <可自主完成的决策>

协作支持：
- <为其他 Agent 提供的持续支持>

协作边界：
- <不归自己所有、需要交接的领域>
- <需要用户或外部主体执行的动作>
```

常见反例：

| 写法 | 问题 | 应放在哪里 |
|---|---|---|
| “今天修复 Graph UI” | 一次性任务 | 当前 turn 或 Message |
| “会 Go、React、Playwright” | 技能清单不能说明工作路由 | Skill 或项目文档 |
| “负责一切开发问题” | 没有领域和责任边界 | 收窄 Domain 与 Scope |
| “当前 busy，使用 gpt-5.6” | 高频运行状态 | Agent config/runtime |
| 罗列每个协作 Agent | 关系会独立演进 | Team Relationship |
| 把所有职责挤成一段长句 | 可读性和边界识别差 | 使用多行模板 |

## 示例

```text
Identity
CodexLoom 的长期产品工程维护者。

Domain
基于 Codex 的长期 Agent 治理与组织协作。

覆盖：
- Agent lifecycle 与共享 CodexHost
- event、rollout、store、HTTP/SSE
- WebUI、`loom` CLI 与 Remote

Scope
长期职责：
- 维护 CodexLoom 的领域模型、架构和端到端产品能力
- 保障协议兼容、持久化安全、运行可靠性和真实环境验收

协作支持：
- 处理其他 Agent 遇到的 `loom` 使用问题
- 解释通信语义，收集需求并通知产品变化

协作边界：
- Pinix 基础设施交由对应 Agent 维护
- 当前 CodexLoom 的生产重启由用户或外部 supervisor 触发
```

系统另外维护稳定 `agentId`、`version` 和 `updatedAt`；这些元数据不需要写进三个业务字段。

## 不属于 Profile 的内容

| 内容 | 归属 |
|---|---|
| 名称、cwd、model、effort、sandbox | Agent metadata/config |
| busy、idle、当前 turn、待审批 | Runtime state |
| 一次任务的输入要求和输出格式 | 当前 Message 或 turn |
| 工具命令和操作步骤 | Skill、playbook 或项目文档 |
| 实际发生的请求、通知和回复 | Messages |
| 与其他 Agent 的长期协作关系 | Team Relationship |
| 未来投递时间 | Schedule |

如果一项信息会随着单次 turn 频繁变化，它通常不属于 Profile。只有长期领域身份或责任边界
发生变化时才应该更新 Profile。

## Profile、Relationship 与 Messages

协作模型由节点、声明边和观察边组成：

```text
Agent + Profile       长期主体及其领域
Team Relationship     团队声明的长期协作关系
Messages              实际发生的多次互动
Team Map              上述事实和 runtime state 的组合读模型
```

Relationship 是独立对象，不重复写进双方 Profile：

```json
{
  "id": "rel_c962f15daeff93c3",
  "fromAgentId": "59c2da81",
  "toAgentId": "a4a63156",
  "description": "长期协作维护 Team graph UI、交互设计和浏览器验收。"
}
```

第一版不定义 manager、peer、owner 等复杂枚举。方向和一段清晰的关系说明足以表达“谁与谁
长期协作，以及为什么”。

Messages 只证明实际发生过协作，不能自动修改 Profile 或升级为声明关系。Team Map 同时
展示：

- dashed blue edge：显式 Relationship。
- solid status edge：Messages 聚合出的 observed link。

## 稳定身份

Agent 名称可以修改，因此 Profile、Relationship 和 Messages 都使用稳定 Agent ID 关联。
Message 同时保存发送时的名称快照：

```json
{
  "fromAgentId": "59c2da81",
  "from": "codex-loom-dev",
  "toAgentId": "a4a63156",
  "to": "cici-mmx"
}
```

聚合和关系判断使用 ID；界面优先显示当前名称；Agent 已删除时回退到消息名称快照。
`scheduler` 使用保留身份 `system:scheduler`。

CodexLoom 第一次读取旧 `comms.ndjson` 时会执行幂等迁移：优先使用规范
`deliveredAgentId`，兼容读取 `deliveredSessionId`，再通过
reply 的反向投递关系和当前 name-to-ID 映射推断；无法解析的历史参与者获得确定性的
`legacy_<hash>` ID。迁移只重写 Loom 通信索引，不修改任何 Codex rollout。原文件保留为
`comms.v1-name-addressed.ndjson`，新索引通过临时文件原子替换。

## 版本与上下文注入

每次 Profile 更新都会递增 `version`。Agent 的 `profileVersionSeen` 记录该 Agent 已经
读取的版本，它是运行元数据，不属于 Profile。

当 `profile.version > profileVersionSeen` 时，CodexLoom 在下一个安全 Turn 前使用 Codex
app-server `thread/inject_items` 写入一条 developer context：

- 不打断正在运行的 turn。
- 不伪装成用户消息。
- 不出现在 `loom history` 的用户对话中。
- 同一版本只注入一次。
- 从未配置过的空 Profile 不产生版本或注入。清空一个已经配置过的 Profile 才产生新版本，
  使 Agent 知道旧边界已撤销。

`profileVersionSeen` 只能证明 Loom 曾注入过该版本，不能证明 compact 后的每次模型请求仍包含
它。Codex app-server 尚未公开“本次实际发给模型的完整 history”时，不要基于 rollout 猜测；
未来重注入策略必须建立在官方上下文可见性信号上。

## 持久化

```text
~/.codex-loom/
  agents.json
  sessions.json                    # 兼容镜像
  profiles.json
  team-links.json
  comms.ndjson
  comms.v1-name-addressed.ndjson  # 仅发生旧消息迁移时
```

`profiles.json` 和 `team-links.json` 都使用 Agent ID。它们与整个 Loom data directory 一起进入
手动备份和重启前备份。

## CLI

```sh
loom profile get <agent>
loom profile set <agent> --identity "..." --domain "..." --scope "..."
loom profile set <agent> --file profile.json
loom profile clear <agent>

loom team                    # 列出 Agent、Profile 摘要和全部关系
loom team <agent>            # 查看一个 Agent 及相邻关系
loom team links [agent]      # 查看全部或指定 Agent 的关系
loom team link add <from> <to> --description "..."
loom team link update <id> --description "..."
loom team link delete <id>
```

`loom agent list` 在 Agent 状态后显示 Domain 首行；`loom agent get <agent>` 返回 Agent、Profile 和
Relationships。第一版不提供模糊 `team find`，查询保持为确定性的 list/get/links。

## Web UI

Team 页面有两种互补视图：

- Graph：默认只显示已有 Profile 或关系证据的 Agent，节点可拖拽；完整目录不被塞进一张图。
- List：显示全部 Agent、Domain 摘要、Profile 版本和消息指标，并在右侧复用同一个 Inspector。

选择 Agent 后，Inspector 展示和编辑 Identity、Domain、Scope，并管理相邻 Relationship；
选择显式边可以编辑或删除关系；选择观察边显示消息数量、回复、open/failed 和最近 subject。
关键选择状态使用稳定 Agent ID 写入 URL。

单个 Agent 的对话页面也在 `Agent Config > Profile` 中显示和编辑同一份 Identity、Domain、
Scope。这里的 Profile 和 Team Inspector 共用版本与 API；保存后 Team 页面通过事件流立即更新。
Agent 页签仍只管理 model、reasoning effort 等运行配置，二者不混为同一种配置。

浏览器自动化入口包括：

```js
window.codexLoom.state()
window.codexLoom.selectAgent(nameOrId)
window.codexLoom.saveProfile(nameOrId, { identity, domain, scope })
window.codexLoom.createRelationship(from, to, description)
window.codexLoom.deleteRelationship(id)
window.codexLoom.graph.selectNode(nameOrId)
window.codexLoom.graph.selectEdge(edgeId)
window.codexLoom.graph.moveNode(id, x, y)
```

## 领域约束

- Profile 绑定稳定 Agent ID，改名不能破坏它。
- Profile 字段可稀疏填写，不用套话补空字段。
- Profile 不保存 secret、运行状态、历史消息或一次性任务协议。
- 新 Agent Message 的 `from` 和 `to` 必须解析为真实 Agent；系统身份只能由 Loom 内部使用。
- Observed link 不能自动变成 Relationship。
- Profile 更新使用 `expectedVersion` 乐观并发控制；版本冲突返回 `409`。
- Profile 更新不打断当前 Turn，只在下一个安全上下文边界生效。
