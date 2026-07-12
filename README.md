![CodexLoom visual identity: long-lived threads woven into an agent organization](docs/assets/codexloom-vi-direction.png)

# CodexLoom

> **Turn Codex threads into an organization of long-lived domain agents.**

**Domain Agent Organization Platform for Codex**

**English** · [简体中文](README.zh-CN.md)

CodexLoom turns Codex threads into a team of domain agents that stay on the job.

It uses Profiles to define long-term domains and responsibilities, preserves working context in Codex threads, and connects agents through communication, division of responsibility, and explicit external boundaries.

[Why long-lived agents](#why-long-lived-domain-agents) · [Get started](#quick-start) · [Documentation](#documentation)

## What Is CodexLoom

CodexLoom is built on Codex. It does not reimplement the agent runtime or duplicate thread history. Instead, it adds durable agent identities, Profiles, organizational relationships, communication, external platform integrations, and governance on top of the threads, turns, tools, and clients provided by Codex.

In CodexLoom, an agent has a stable ID, name, Profile, and primary thread. A person can continue that thread through Codex Desktop, Mobile, or the CodexLoom WebUI. Other agents can find it and send messages through the `loom` CLI. Members of an organization can work with it from existing environments such as Feishu (Lark) and Parall.

When a domain grows, a Lead can delegate stable subdomains to long-lived Internal Agents. When the team needs to participate in an external organization, it can establish a dedicated Interface Agent and define a different role for each channel. CodexLoom therefore governs an ongoing agent organization rather than a collection of isolated conversations.

> **Codex provides the threads; CodexLoom weaves them into an agent organization.**

The name `Loom` describes this organizing process: every thread keeps its own history and direction while responsibilities and relationships weave it into a larger collaborative structure.

## What You Can Do Today

- **Long-lived agents:** Maintain a stable identity, editable name, primary thread, Profile, and model configuration for each agent.
- **One thread, multiple surfaces:** Work on the same thread from Codex Desktop, Mobile, the WebUI, and the CLI, with live message and status synchronization.
- **Agent-to-agent communication:** Send, queue, and reply to Messages while preserving delivery state, response relationships, and complete history.
- **Team structure:** Use the Team List and draggable Team Map to inspect declared relationships and collaboration observed in actual Messages.
- **External organization integration:** Manage external identities, Conversation Memberships, Inbox, and Outbox through Parall and Feishu (Lark).
- **Continuous operations:** Run Schedules, inspect global runtime state, create backups on demand, and restart gracefully after active turns finish.

Lead, Internal Agent, and Interface Agent are currently expressed through Profiles, declared relationships, Messages, and Conversation Memberships. Dedicated hierarchical messaging policies and organization templates are still being modeled.

## Who Is It For

### Advanced Individuals

- Maintain several specialized agents over the long term.
- Use the Codex app for daily work.
- Let agents responsible for different domains collaborate directly.
- Bring agents into work groups and communities they already use.

### Organizations

- Establish and continuously adapt an agent organization.
- Let employees use agents directly through their existing messaging platforms.
- Give platform maintainers control over responsibilities, access, and runtime health.
- Coach agents from their work trajectories and reorganize responsibilities when the current structure stops working.

## Quick Start

Prerequisite: install the `codex` CLI and sign in with a ChatGPT account.

```sh
make release
./bin/codex-loom
```

Open <http://localhost:4870>, then create your first agent:

```sh
./bin/loom agent create research --cwd /path/to/repo
./bin/loom profile set research \
  --identity "Long-term researcher for this domain" \
  --domain "Continuously research the relevant products, protocols, and implementations" \
  --scope "Answer domain questions, preserve conclusions, and advise related agents"
./bin/loom thread send research "Establish a baseline for the current state of this domain"
```

## Why Long-Lived Domain Agents

### A Task Agent Starts with a Task

Code agents are usually organized around tasks: create a thread, provide the objective and background, deliver the result, and end the collaboration. That thread may already contain project context, constraints, tool usage, and prior decisions that would be valuable to the next piece of work. Yet the next related task often starts in a new thread, forcing the user to restate the background, recover earlier decisions, and rebuild the working context. Every task pays the cold-start cost again.

CodexLoom makes a different bet: a thread should continue to carry work after one task ends. Later tasks in the same domain return to that thread and reuse what it has accumulated. A Profile gives the agent a durable domain, responsibility, and boundary. The thread stops being a record of one task and becomes the continuing workspace of a Domain Agent.

| | Task Agent | Domain Agent |
|---|---|---|
| Created around | One task | A long-lived domain |
| Thread lifecycle | Abandoned or replaced after delivery | Continues to receive work in the same domain |
| Next task | Reintroduces background and rebuilds context | Continues from existing context and trajectory |
| Responsibility | Complete the current task | Remain responsible for the domain |

### Rebuilding Context Also Has a Cost

A common concern about long-lived threads is that context windows are finite and longer histories may make each task slower or more expensive. That concern is real, but it counts only the cost of retaining context, not the cost of reconstructing it. A new thread still needs the relevant background, constraints, and previous decisions to reach the same working state. Reusing a thread also allows its stable history prefix to benefit from prompt caching instead of repeatedly processing the same context from scratch.

### Compaction Preserves Continuity

Compaction does not reset the context to zero. After older history is compressed, its summary and the recent trajectory still preserve substantial working information. A long-lived thread does not require every raw message to grow without bound; its context can evolve through caching and compaction. Compaction is a mechanism for maintaining continuity, not a reason to abandon the thread.

A long-lived thread also accumulates tacit context that is difficult to reconstruct in a single prompt. Corrections, preferences, terminology, judgment, and collaboration habits enter the thread through repeated work and are refined across successive compactions. They may not belong as individual rules in a Profile, but together they create a working understanding unique to that agent. Starting a new thread loses precisely this knowledge that is hardest to migrate explicitly.

> **A Task Agent starts with a task; a Domain Agent continues from prior work.**

Tasks still exist in the Domain Agent model, but they become units of work rather than definitions of identity or lifecycle. An agent can remain idle between tasks and still continue later from what it has already learned.

## Profile and Thread

CodexLoom treats an agent as a durable, long-lived subject. Its Profile defines who the agent is, what it remains responsible for, and where its boundaries lie. A domain can be a project, subsystem, professional capability, customer, or business area.

The thread carries interactions, decisions, tool calls, artifacts, and feedback as they happen, forming the agent's work trajectory over time. The Profile supplies stable direction; the thread preserves accumulated work so that future tasks can continue from existing context.

A Profile also exposes collaboration information to other agents. It states what the agent owns, which questions should be directed to it, and which work lies outside its boundary. It is both durable context for the agent and a discoverability and collaboration contract for the organization.

## From Domain Agents to an Organization

> **A workflow describes how work moves; CodexLoom organizes who remains responsible.**

When agents remain responsible for different domains, their relationships stop being one-off invocation chains and become ongoing collaboration. Work that crosses a domain boundary needs to find the responsible agent, ask for its judgment, request help, and report problems discovered while using what it owns.

The Team Map presents durable responsibilities and organizational relationships. Messages preserve the collaboration that actually occurs. Declared relationships help agents find the right owner; message history provides evidence of how those relationships operate in real work.

### Communication Across Domains

When one agent uses a tool maintained by another, it can ask the owner how to use it or report a failure directly. If the recipient is busy, the message waits until its current turn ends. Questions, judgments, and results remain connected through replies. The `loom` CLI provides discovery, delivery, queuing, replies, and status inspection, while Messages records the resulting cross-domain collaboration.

### Internal Agents

When a domain becomes too large, its owner can become a Lead and delegate stable subdomains to Internal Agents. Every Internal Agent has its own Profile and thread and remains responsible for one subdomain. The Lead coordinates the whole domain and acts as the public collaboration boundary for the group.

```text
Product Lead
├── Desktop Internal Agent
├── Web Internal Agent
├── Backend Internal Agent
└── Ops Internal Agent
```

Human maintainers can interact directly with any Internal Agent. Other agents collaborate through the Lead and do not need to understand every internal responsibility.

This resembles a human organization: members have stable responsibilities, growing domains are divided further, internal work happens through collaboration, and explicit roles carry responsibility across organizational boundaries.

## Designing the External Boundary

> **IM integration is not simply binding an internal agent to an external account. It lets the agent organization design its own external boundary.**

Advanced users may bring long-term agent collaborators into existing work environments. An agent organization can establish a dedicated Interface Agent for external communication. It maintains external relationships and conversational context, asks the Lead for domain judgment when needed, responds externally with the resulting decision, and brings important feedback back into the internal organization.

```text
Feishu (Lark) / Parall  <->  Interface Agent  <->  Lead  <->  Internal Agents
```

An agent can have identities on multiple platforms and participate in multiple group conversations. An external identity states where the agent is reachable. A Conversation Membership defines its role in a specific conversation: what to pay attention to, when to speak, what it must not disclose, and when it must consult an internal owner. The same agent can therefore perform different roles in different channels without being copied into separate, disconnected agents.

| Status | Platforms |
|---|---|
| Available | Feishu (Lark), Parall |
| TODO | Slack, Microsoft Teams |

## Work Where People Already Work

CodexLoom does not require every participant to move to a new chat interface. The same agent remains reachable through different surfaces while preserving its identity and thread continuity.

| Participant | Surface | Purpose |
|---|---|---|
| Daily user | Codex Desktop / Mobile | Continue working with an agent and its thread across devices |
| Agent maintainer | CodexLoom WebUI | Use agents and govern Profiles, relationships, external surfaces, and runtime state |
| Other agents | `loom` CLI | Discover domain owners, send and reply to messages, and inspect delivery state |
| Organization members | External IM | Use agents from the work environments they already inhabit |

## Govern the Agent Organization

CodexLoom governs an ongoing agent organization, not a collection of model parameters. The health of an agent must be judged from real work: how it interprets input, which tools it uses, what it produces, how it communicates with other agents, and when it fails, retries, or needs human intervention. These observable facts form its trajectory; they are not the model's hidden chain of thought.

Long-lived agents also make continuous coaching possible. A maintainer can talk directly with an agent, review a decision, correct its understanding of the domain, and observe whether later work improves. Changes that should persist can be applied to the Profile, relationships, and Conversation Memberships instead of remaining in a one-off prompt.

Governance includes the organization itself. Split out new Internal Agents when a responsibility becomes too broad, adjust relationships when collaboration paths become too long, and redesign Interface Agents or channel roles when external communication loses focus. CodexLoom governs both the performance of individual agents and the fitness of the organization around them.

For an individual, this is a set of agent partners that can collaborate over time and participate in existing work environments. For an enterprise, these agents are organizational assets that require ongoing operation. Employees use them through IM platforms, while maintainers use CodexLoom to observe, coach, and reshape the agent organization behind them.

## Product Boundary

CodexLoom is built on Codex. Codex continues to provide the agent runtime, thread history, and daily clients such as Desktop and Mobile. CodexLoom provides durable agent identity, long-term responsibility, organizational communication, external boundaries, and governance. Tasks and workflows can still run inside an agent, but they are not the primary objects governed by CodexLoom.

## Documentation

- [Agent Profiles: defining a long-term identity, domain, and scope](docs/agent-profile.md)
- [Agent communication and the `loom` CLI](docs/loom-cli.md)
- [External platform integration design](docs/agent-platform-integration.md)
- [Conversation Membership: an agent's role in a specific conversation](docs/conversation-membership.md)
- [Codex app-server protocol and adapter notes](docs/codex-app-server-protocol.md)
- [Architecture, data flow, APIs, migration, and development handbook](docs/handbook.md)

## Project Status

CodexLoom is a Codex-native, local-first, self-hosted project under active development. Features such as Remote depend on experimental Codex APIs whose interfaces and backend behavior may change between Codex releases. See the [development handbook](docs/handbook.md#已知限制) for current limitations and compatibility strategy.

CodexLoom is an independent project built on Codex. It is not affiliated with or endorsed by OpenAI.
