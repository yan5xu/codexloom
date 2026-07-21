# CodexLoom Documentation

CodexLoom documentation serves different readers. Start with the guide that
matches the decision you are making instead of reading the repository as one
continuous manual.

## Owner Guide

- [Repository README](../README.md) - product entry, installation, and project
  status.
- [CodexLoom Owner Guide](owner-guide.md) - establish, use, coordinate, observe,
  and adjust a long-lived Codex Agent Team.

The Owner Guide is the primary user journey. It explains when to use a Loom
concept and how the concepts work together. The references below provide exact
object, command, integration, and implementation detail.

## Owner and Agent Reference

| Document | Use it for | Notes |
|---|---|---|
| [Agent Profile](agent-profile.md) | Define long-term identity, domain, and scope | Contains both conceptual and CLI/storage detail |
| [Agent communication and CLI](loom-cli.md) | Use the `loom` command surface and Agent Messages | Primary command reference; not a linear Owner tutorial |
| [Conversation Membership](conversation-membership.md) | Understand an Agent's role in one external conversation | Detailed governance model |
| [Integrations](integrations.md) | Configure and diagnose Feishu, Slack, and Parall | Advanced Owner and operator reference |
| [Skills](skills.md) | Understand built-in Skills and Codex discovery | Agent and operator reference |
| [Thread Artifacts](thread-artifacts.md) | Work with files attached to or generated from a Thread | User and implementation reference |

## Product Direction and Design Evidence

These documents explain why the current product is shaped as it is. They are
not substitutes for current user instructions.

| Document | Role |
|---|---|
| [Product design](product-design.md) | Product baseline and information architecture |
| [Product walkthrough](product-walkthrough.md) | Screenshot-backed product evaluation |
| [Visual identity](visual-identity.md) | Brand and visual-system direction |

Product design documents can predate current behavior. User-facing statements
should be verified against the current build before publication.

## Developer and Operator Documentation

| Document | Role |
|---|---|
| [Development handbook](handbook.md) | Architecture, storage, APIs, migration, build, and operations |
| [Codex app-server protocol](codex-app-server-protocol.md) | Adapter and protocol observations |
| [Agent platform integration design](agent-platform-integration.md) | Connector architecture and design rationale |
| [WebUI validation](webui-validation.md) | Browser and mobile verification practice |
| [Technical debt audit](technical-debt-audit.md) | Engineering audit and remediation record |
| [Markdown rendering fixture](markdown-rendering-fixture.md) | Renderer test content |
| [chub compatibility](chub-communication.md) | Legacy compatibility notes |

## Documentation Rules

1. The repository Markdown and its reviewed Git history are the canonical text.
2. The Owner Guide explains the user journey; reference documents retain exact
   commands, fields, protocols, and diagnostics.
3. Product principles, current behavior, validated practices,
   recommendations, and hypotheses must not be presented as interchangeable.
4. Development-only behavior must be labeled until it is integrated and
   verified on the branch the document targets.
5. Product design evidence does not override current implementation facts, and
   current implementation does not silently redefine an Owner-confirmed product
   boundary.
6. Prefer links to repeated explanations. If two documents make the same
   current-behavior claim, one should be identified as the owning reference.

## Content Ownership

- The repository README owns the concise product position, installation entry,
  platform support table, and project status.
- The Owner Guide owns the end-to-end Owner journey and the choice between Loom
  coordination mechanisms.
- `loom-cli.md`, `integrations.md`, `conversation-membership.md`, `skills.md`,
  and `thread-artifacts.md` own exact commands and object behavior in their
  respective areas.
- Product design and walkthrough documents preserve decisions and evaluation
  evidence. They do not override a current-behavior reference.
- The development handbook owns implementation architecture and operations.
