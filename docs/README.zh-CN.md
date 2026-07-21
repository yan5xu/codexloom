# CodexLoom 文档地图

**简体中文** · [English](README.md)

> **本文是文档地图的权威版本。** [README.md](README.md) 是它的英文译本。

CodexLoom 的文档服务于不同读者。请从与你当前决定相匹配的那份文档开始，而不是把
整个仓库当作一本连续的手册来读。

## Owner Guide

- [CodexLoom Owner Guide（简体中文，权威版本）](owner-guide.zh-CN.md) —— 建立、
  使用、协调、观察并调整一支长期在岗的 Codex Agent Team。
- [CodexLoom Owner Guide（English translation）](owner-guide.md) —— 上述中文
  权威文本的英文译本。
- [仓库 README（简体中文）](../README.zh-CN.md) —— 产品定位、安装入口与项目状态。
- [Repository README（English translation）](../README.md) —— 中文仓库
  README 的英文译本。

Owner Guide 是主要的用户旅程文档。它说明何时使用某个 Loom 概念，以及这些概念
如何组合在一起。下面的参考文档提供确切的对象、命令、集成与实现细节。

## Owner 与 Agent 参考文档

以下参考文档目前只有英文版本。

| 文档 | 用于 | 说明 |
|---|---|---|
| [Agent Profile](agent-profile.md) | 定义长期 Identity、Domain 与 Scope | 同时包含概念说明与 CLI／存储细节 |
| [Agent 通信与 CLI](loom-cli.md) | 使用 `loom` 命令面与 Agent Message | 主要命令参考，不是线性的 Owner 教程 |
| [Conversation Membership](conversation-membership.md) | 理解 Agent 在某个外部会话中的角色 | 详细的治理模型 |
| [Integrations](integrations.md) | 配置并诊断飞书、Slack 与 Parall | 面向高级 Owner 与运维者的参考 |
| [Skills](skills.md) | 理解内置 Skill 与 Codex 的发现机制 | 面向 Agent 与运维者的参考 |
| [Thread Artifacts](thread-artifacts.md) | 处理附加到 Thread 或由 Thread 产生的文件 | 用户与实现参考 |

## 产品方向与设计证据

这些文档解释当前产品为什么是现在的形态。它们不能替代当前的用户说明。

| 文档 | 角色 |
|---|---|
| [Product design](product-design.md) | 产品基线与信息架构 |
| [Product walkthrough](product-walkthrough.md) | 带截图的产品评估 |
| [Visual identity](visual-identity.md) | 品牌与视觉系统方向 |

产品设计文档可能早于当前行为。面向用户的陈述在发布前应对照当前构建核验。

## 开发与运维文档

| 文档 | 角色 |
|---|---|
| [Development handbook](handbook.md) | 架构、存储、API、迁移、构建与运维 |
| [Codex app-server protocol](codex-app-server-protocol.md) | 适配器与协议观察记录 |
| [Agent platform integration design](agent-platform-integration.md) | Connector 架构与设计依据 |
| [WebUI validation](webui-validation.md) | 浏览器与移动端验证实践 |
| [Technical debt audit](technical-debt-audit.md) | 工程审计与修复记录 |
| [Markdown rendering fixture](markdown-rendering-fixture.md) | 渲染器测试内容 |
| [chub compatibility](chub-communication.md) | 历史兼容说明 |

## 文档规则

1. 仓库中的 Markdown 及其经过评审的 Git 历史是权威文本。
2. Owner Guide 说明用户旅程；参考文档保留确切的命令、字段、协议与诊断信息。
3. 产品原则、当前行为、已验证实践、当前建议与假设不得被当作可以相互替换的陈述。
4. 仅存在于开发构建中的行为必须被标注，直到它在该文档所面向的分支上完成集成
   与验证。
5. 产品设计证据不能覆盖当前实现事实；当前实现也不得静默地重新定义一条经 Owner
   确认的产品边界。
6. 优先使用链接，而不是重复解释。当两份文档做出同一条当前行为陈述时，应指明其中
   一份为权威参考。
7. 中文是 Owner Guide 与本文档地图的权威语言。英文版本是译本；两者出现分歧时，
   以中文为准，并应回头修订英文译本。

## 内容归属

- `README.zh-CN.md` 拥有简洁的产品定位、安装入口、平台支持表和项目状态；
  `README.md` 是英文译本。
- Owner Guide 拥有端到端的 Owner 旅程，以及在各个 Loom 协调机制之间的选择。
- `loom-cli.md`、`integrations.md`、`conversation-membership.md`、`skills.md`
  和 `thread-artifacts.md` 分别拥有各自领域内确切的命令与对象行为。
- 产品设计与 walkthrough 文档保存决策与评估证据。它们不覆盖任何一份当前行为参考。
- 开发手册拥有实现架构与运维内容。
- 中文与英文成对存在的文档中，中文文件拥有内容；对应的英文文件是译本，不单独
  引入新的产品含义。
