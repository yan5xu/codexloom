# CodexLoom Product Design

> Status: approved product baseline for the next WebUI iteration.

## Product Boundary

CodexLoom serves an advanced individual Owner operating a long-lived Codex Agent Team, especially in a One Person Company context. Codex provides Agent intelligence, Threads, Turns, tools, and Desktop/Mobile clients. Loom helps the Owner establish durable Agent identities, define responsibilities, coordinate the team, govern external roles, observe operation, and adjust the organization.

`Loom your Codex` describes the value: organize Codex Agents so one person can use them as a continuing team.

The Owner's business goals remain outside Loom. Loom is not a company operating system, project manager, CRM, ERP, generic workflow builder, enterprise administration suite, or automatic organization designer.

## Product Rhythm

Importance and frequency are different:

- **Continuous:** work with Agents and their Threads.
- **Urgent when present:** answer Needs You requests.
- **Periodic:** inspect status, capacity, and token usage.
- **Occasional:** adjust Team relationships and External roles.
- **Rare and technical:** operate Remote, backup, restart, and developer tools.

The default screen is therefore an Agent Thread, not a dashboard. Lower-frequency controls must remain available without permanently occupying the primary work surface.

## Causal And Return Continuity

Every secondary surface must preserve the chain `who started this work -> why it needs attention -> where the answer or result returns`. Needs You, Overview evidence, Team Activity, External failures, and Settings diagnostics must link back to the exact Agent and, where available, the originating Turn, Message, Conversation, or operation. The Owner must not become the layer that manually joins these records.

Result attention follows causality rather than output volume or semantic guesswork:

- an Owner-originated Turn returns the responsible Agent's first readable terminal or stage result to the Owner;
- an internal Message reply returns to the requesting Agent, which integrates it before returning to the Owner;
- an External Inbox result returns to the originating Conversation unless it needs an Owner decision or fails its governed delivery;
- schedule and monitoring work only returns on the boundary defined by its purpose, not on unchanged checks;
- an Owner-authorized real-world action returns its final success, failure, or safe stop;
- a long-running Goal returns at completion, blocked/paused boundaries that need input, or an Owner-defined milestone, not at every continuation Turn.

Therefore a new assistant output or a completed Turn is not automatically an Owner result. Internal collaboration, routine external replies, tool calls, retries, queue transitions, and `response=none` background notices remain ordinary Agent activity. When Loom cannot infer the causal boundary safely, the responsible Domain Agent decides whether to request the Owner through Needs You or include the finding in its next stage response.

## Global Information Architecture

The primary product areas are:

1. **Agents** - daily work with long-lived Agents and their Threads.
2. **Needs You** - durable questions, decisions, facts, and authorizations that require the Owner.
3. **Overview** - Status, Capacity, and Token Usage over a shared time range.
4. **Team** - Directory, Organization, Collaboration, and Activity.
5. **External** - Agent identities and roles in external Conversations.
6. **Settings** - Codex/Remote, Connectors, Data/Recovery, System, and Developer tools.

Existing technical workspaces move as follows:

- global Inbox becomes per-Agent Inbox near the composer;
- Messages becomes Team Activity and remains reachable by stable deep links;
- Capacity and Token Usage become separate views within Overview;
- Schedules are visible per Agent, with a global summary in Overview;
- Integrations becomes External;
- Remote, backup, restart, Design System, API, and canary tools move to Settings.

The backend Connection, Address, Membership, Inbox, Outbox, and Provider Operation objects remain authoritative. The WebUI presents Owner-facing projections without weakening their auditability.

## Global Shell

### Desktop

The sidebar contains Needs You, Overview, Team, External, an Agent directory, and a bottom Settings entry. Selecting an Agent opens a persistent tab. Closing a tab never stops, archives, or changes the Agent.

The Agent tab strip remains mounted while global workspaces cover the content area. Tabs receive live status and message events even when inactive. Keyboard shortcuts switch tabs without reloading them.

The top work surface exposes only two global signals: Needs You and the current executing/idle summary. Model, effort, IDs, workspace, and runtime controls stay in the Agent Inspector.

When the desktop sidebar is collapsed, the sidebar disappears except for a bottom restore control. Agent tabs continue to provide navigation. Needs You and current execution signals remain available from the work surface.

### Mobile

The Thread and composer own the screen without a persistent bottom navigation bar. A compact header menu opens the Agent drawer and the same Needs You, Overview, Team, External, and Settings destinations used on desktop. Agent selection uses that drawer or the tab strip; Inspectors become full-screen views.

## Agent Work Surface

### Tabs

Each tab shows:

- Agent name;
- execution indicator;
- non-zero Needs You or Agent Inbox signal;
- setup or last-turn failure when present.

The tab may also show a new-result marker for an unread result on an Owner-originated causal chain. It must not light up for every background assistant output. Unread is scoped to an observer and a causal return, not inferred from generic Thread activity.

Execution and attention are separate dimensions. A running Agent may also need the Owner, have queued work, or have a Connector issue.

### Thread Feed

The feed preserves one chronological trajectory while rendering different event classes distinctly:

1. Owner and Agent dialogue;
2. Internal Messages, External Inbox, and Schedules that introduced work;
3. Reasoning, Tool Use, Approval, Goal, and artifact events;
4. compaction, reconnect, interruption, and lifecycle events.

Agent responses and message envelopes render Markdown. Internal and external envelopes show their origin, timing, request/reply/notification semantics, and an expandable raw representation. Tool calls are compact by default; image results render an inspectable preview. Reasoning must never appear as an empty panel.

Sending is optimistic but truthful: the draft becomes a local `Sending` item immediately, then `Sent`, `Queued`, or `Failed`. The composer is protected against duplicate submission while the request is unresolved. A failed request restores the draft.

Virtual scrolling must preserve variable-height rows, upward reading position, per-tab scroll state, and a visible jump-to-latest control. New events only auto-follow when the reader is already at the bottom.

### Agent Inbox

Agent Inbox is the Agent's work queue, not the Owner's action list. It is hidden at zero and appears immediately above the composer when non-empty. The compact summary shows total items, source mix, oldest waiting time, and handling/failure signals. The detailed view exposes source, sender, subject, received time, wait duration, state, and stable evidence links.

The first version is primarily observational. The Owner may inspect an item, cancel eligible Owner-originated work, retry interrupted work, or explicitly close work that no longer requires handling. Arbitrary reordering and forced insertion are not part of the initial design.

### Goal

Goal is a slim, persistent Thread control above the composer. It does not redefine Agent execution state and cannot silently reserve the Agent after a blocked, paused, limited, or completed state.

### Agent Inspector

The information button provides a compact hover summary and opens a right-side Inspector on desktop or a full-screen view on mobile. Sections are:

- Profile: Identity, Domain, Scope, version, and explicit save;
- Team: parent, Internal Agents, and Collaboration relationships;
- External: identities and Conversation roles;
- Schedules;
- Runtime: model, reasoning effort, working directory, sandbox, approval, and Remote;
- Usage: a summary with a filtered link to Overview.

Profile changes create an explicit version and apply from the next safe Turn. Runtime changes do not mutate an active Turn. Rename preserves stable Agent and Thread identity. Stop remains in the composer; Archive remains in the Agent directory.

## Status And Attention

### Execution State

- Starting
- Running
- Idle
- Draining
- Unavailable

### Attention Signals

- Needs You
- Agent Inbox backlog
- setup issue
- last Turn failed
- Connector issue

Goal state and unread activity do not make an Agent `Running`. The global `active / idle` summary is derived only from execution state. Clicking it shows executing Agents, current Turn duration, work source, and drain state.

Ordinary completion goes to recent activity, not Needs You. Internal and external work goes to Agent Inbox. Connector failures appear in External and Overview unless an Agent creates a specific Owner request.

## Needs You

Needs You is the Owner's only durable action inbox. It contains explicit Agent requests for a fact, decision, or authorization; Codex approval requests; blocked decisions; and failed delivery of an Owner answer. It excludes ordinary activity, external Inbox, normal completion notifications, and generic Connector health.

Items are ordered by blocking required requests, approvals, optional requests, then time. Detail includes Agent, question, context, blocked work, options, related Thread/Message/Conversation, sent time, wait duration, and answer delivery state. Dismissal must state whether no answer is needed, the request is deferred, or the work is cancelled.

## Overview

Overview contains separate Status, Capacity, and Token Usage views with one shared local-time range selector. The user may choose any single day, any seven-day period, 30 days, or a custom range and move backward or forward by the selected period.

Today runs from local midnight to the current time. Historical days use complete local calendar days.

### Status

Status shows currently executing Agents, Agents waiting for the Owner, current backlog, recent failures, Connector issues, and recently completed background work. Every aggregate is inspectable.

### Capacity

Capacity reports executing time, calendar non-executing proxy, new-work wait p50/p90/max with sample count, current backlog with oldest age, work source, wait reason, data quality, and stable evidence. It never ranks Agents or automatically recommends split/merge actions. `Calendar non-executing proxy` must never be shortened to `idle`.

### Token Usage

A single-day view uses a treemap where each Agent's area represents token use. Multi-day views use daily stacked bars and Agent trends. Hover and selection show exact input, output, cached input, reasoning output, model, calls, and period comparison. Tooltips render outside clipping containers.

## Team

Team opens in Directory, not Graph. Directory provides stable ordering and accurate scanning of current status, Agent name, Domain, organization position, Needs You, Agent Inbox, and External presence.

The four views have distinct semantics:

- **Directory:** authoritative Agent inventory;
- **Organization:** declared parent/Internal-Agent responsibility structure;
- **Collaboration:** declared stable cross-domain working relationships;
- **Activity:** time-scoped communication and handoff evidence.

Observed Activity never becomes a declared relationship automatically. External roles are not Team relationships. Graph nodes are draggable and persist positions, but graph gestures do not modify relationship semantics. Relationship changes use explicit forms.

## External

External opens from an Agent-oriented directory. The default projection is:

`Loom Agent -> external identity -> Conversation role`

An Agent may have multiple identities across or within providers. One identity may participate in multiple groups, channels, or DMs. A newly discovered place appears as `Joined, not configured` and remains inactive until the Owner defines its role.

For each Conversation, the Owner defines:

- purpose;
- Agent role;
- guidance and explicit prohibitions;
- when it responds;
- how it replies;
- whether proactive sending is allowed.

DM participants receive the same explicit role and policy treatment as groups. The default experience never asks the Owner to reason in Connection, Address, Gateway, credential, cursor, or raw ID terms. Those remain in Advanced and are available for operation and audit.

Normal external work starts from an Agent Thread. External is used for onboarding, authorization, role adjustment, evidence, and failure recovery. It is not a second chat client, CRM, or external business system.

## Settings

Settings is a low-frequency bottom entry with these sections:

- Codex & Remote;
- Connectors;
- Data & Recovery;
- System;
- Developer.

Restart exposes a truthful lifecycle: ready, draining with the list of active work, restarting/reconnecting, complete, or failed with a recovery command. An active Goal stops automatic continuation at the current Turn boundary and is resumed after the new process starts; the current Turn is never interrupted. Developer contains Design System, API diagnostics, canary tools, and raw events and stays out of normal navigation.

## Agent Creation

The current release keeps creation simple: Agent name, absolute working directory, and optional Domain, with advanced runtime configuration collapsed. Creation opens the resulting Agent Thread immediately.

A future coach-assisted path may help the Owner decide whether a long-lived Agent should exist, produce a reversible organization hypothesis, validate overlap and boundaries, and run a real setup check. That path is deliberately deferred until the organization-coaching practice is stable enough to productize.

## Cross-Device Continuity

Web, Codex Desktop, Codex Mobile, and CLI share Agent identity, Thread history, streaming events, Tool Use, artifacts, Turn state, Goal, Agent Inbox, Needs You, Profile, and runtime configuration. A message accepted on one surface appears on the others without refresh.

Open tabs, tab order, scroll position, Inspector state, and unsent drafts remain device-local. When another client submits work while an Agent is running, every client sees the same queued request and delivery state. Stable request IDs prevent network retries from creating duplicate Turns.

Reconnect resumes from an event cursor and reconciles the authoritative snapshot. Unconfirmed messages are never displayed as sent.

## Delivery Order

1. Unify execution and attention projections and real-time reconciliation.
2. Replace the global shell and finish the Agent work surface.
3. Consolidate Needs You and Overview.
4. Restructure Team and External projections.
5. Move technical controls into Settings and preserve old deep-link compatibility.
6. Verify the production build with real Agents, Remote synchronization, desktop viewports, and mobile viewports.

The UI migration does not rename or weaken backend governance objects. Each new projection ships only after its real user path is verified against the current service.
