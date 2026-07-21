# CodexLoom Owner Guide

> Secondary English translation of the review draft for advanced individual
> Owners who use Codex agents as a long-lived team.

[**简体中文（权威版本）**](owner-guide.zh-CN.md) · **English translation**

The Simplified Chinese [Owner Guide](owner-guide.zh-CN.md) is the canonical
text and primary review surface. This file is its English translation. If the
two diverge, the Chinese guide governs and this translation should be updated;
this file must not introduce independent product meaning.

Current-behavior statements in this draft were checked against `origin/main`
at commit `81696e1`. Topics and Triggers are separately identified as
development-build behavior because they are not part of that baseline.

CodexLoom helps one person establish, use, coordinate, and adjust a team of
long-lived Codex agents. Codex remains the agent runtime. Loom adds stable
identity, continuing responsibility, communication, governed external roles,
and evidence about how the team is operating.

This guide is organized around the Owner's work rather than Loom's database
objects or infrastructure.

## How to Read This Guide

The guide distinguishes five kinds of statements:

- **Product principle:** the product boundary CodexLoom intends to preserve.
- **Current behavior:** behavior available from the current `main` branch.
- **Validated practice:** a way of working supported by repeated real use.
- **Current recommendation:** useful guidance that may change with more use.
- **Hypothesis:** an idea that still needs evidence before it becomes guidance.

Features that exist only in a development build are called out explicitly.
They should not be treated as a promise of the current `main` branch.

Choose a reading path based on the question you have now:

- First use: read **The Core Idea**, **The Owner's Working Rhythm**, and
  **Establish Your First Long-Lived Agent** in order.
- You already have a long-lived Agent: focus on **Work With an Agent Day to
  Day** and **Choose Process Management or Result Management**.
- You are starting cross-Agent collaboration: read **Choose the Right
  Coordination Mechanism** and **Grow From One Agent Into a Team**.
- You want colleagues to reuse your Agent capabilities: read **Give Agents
  Governed External Roles**.
- You are adjusting the organization: read **Observe and Adjust the Team** and
  **Product Boundary**.

## The Core Idea

### Start with continuing work, not an organization chart

The first question is not "How many agents should I create?" It is:

> What work needs a continuing owner, context, and professional judgment?

A one-off task can stay a task. A repeatable procedure may belong in a Skill.
A long-lived Agent becomes useful when related work repeatedly benefits from
the same identity, working history, responsibility, and judgment.

**Product principle:** an Agent is a durable working relationship, not a job
title added to a diagram.

### Let one Agent carry the work until a stable boundary appears

**Current recommendation:** begin with the smallest useful setup: one Agent
with a narrow Profile and one real assignment.

Its primary Thread preserves the working trajectory while its
Profile states the responsibility that should remain stable.

Do not split a new Agent merely because one task is large or a tool is missing.
Continue observing the work. Differentiation becomes useful when evidence shows
that a single Agent is carrying incompatible contexts, distinct professional
judgments, sustained load, or a responsibility that deserves its own owner.

### Organization follows differentiation

When responsibility is divided among long-lived Agents, communication and
organization become necessary. Loom then helps answer:

- Who remains responsible for the whole?
- Which Agent owns each stable domain?
- Which relationships are hierarchical responsibilities, and which are
  stable cross-domain interfaces?
- Where should a question, result, or escalation return?

**Current recommendation:** organization is more reliable when it records a real
division of responsibility than when it predicts an ideal future structure.

### Loom organizes Agents but does not own the Owner's goals

The Owner may want to reduce forwarding, expand personal capacity, improve
external collaboration, or operate a One Person Company. Those goals belong to
the Owner. Loom can help achieve them, but it should not absorb the goals into
its own product objects.

**Product principle:** Loom owns only what it can reliably govern: Agent
identity, continuing responsibility, relationships, communication, external
roles, and operating evidence. Business projects, customer commitments,
operating results, and the definition of success remain with the Owner and the
authoritative business systems.

The same boundary applies to organization methods. Long-lived Agents, Topics,
Leads, and Practice Coaches may help the Owner work, but Loom does not decide
the correct company goal or hard-code every working method as a product object.

### An advanced individual does not have to work alone

CodexLoom first serves one advanced individual Owner, but that person may work
inside a company or team. The Owner can bring a long-developed Agent into a
Feishu or Slack conversation through a governed external identity, then let
colleagues collaborate with it under an explicit Conversation Role.

This makes personal capability accumulated in the Agent's Profile, Thread, and
working methods reusable by the team. The Owner spends less time answering the
same questions, forwarding information, and reconstructing context, while
colleagues gain direct access to a professional capability. The Agent still
follows its existing domain, authorization, and escalation boundaries. Reusing
the capability does not expose all of the Owner's context, permissions, or
business judgment to external collaborators.

**Product principle:** Loom may help an individual bring Agent capabilities to
people around them without becoming an enterprise multi-tenant administration
system or changing the starting point: one explicit Owner remains responsible
for the Agent's identity and boundaries.

## The Owner's Working Rhythm

CodexLoom has several work surfaces because the Owner uses them at different
frequencies.

| Frequency | Surface | Main question |
|---|---|---|
| Most of the day | Agent workspace | Which long-lived Agent am I working with? |
| When asked | Needs You | What fact, choice, or authorization requires me? |
| Across days or Agents | Topics (development build) | Who owns this bounded matter now, and what is it waiting for? |
| Periodically | Overview | Is the team operating normally, and what deserves investigation? |
| When responsibilities change | Team | Who owns what, and do declared relationships still fit the work? |
| When external roles change | External | Which Agent may act as which identity in which conversation? |
| During maintenance | Settings | Is the local runtime, connector, backup, or remote access healthy? |

The Agent workspace is the default. Overview is an observation surface, not a
company dashboard. Settings is a maintenance surface, not the center of daily
work.

## Establish Your First Long-Lived Agent

### 1. Choose a continuing responsibility

Use work that already recurs. Examples include maintaining a product domain,
conducting ongoing research, operating a publication practice, or supporting a
long-running customer relationship.

Ask:

- What should this Agent remember across assignments?
- What judgment should improve with repeated work?
- What should naturally enter or leave its responsibility?
- Who decides whether its work is correct?

If the answers describe only today's deliverable, create a task rather than a
new long-lived Agent.

### 2. Create the smallest usable Profile

A starting Profile needs three parts:

- **Identity:** who the Agent is in this working relationship.
- **Domain:** the objects and professional judgment it owns over time.
- **Scope:** what it may do, what it should refuse, and when it should return to
  the Owner or another responsible Agent.

Treat the Profile as a testable hypothesis. It should be specific enough to
start safely but remain easy to revise after real work.

### 3. Give it a real first assignment

An Agent record is not proof that the role works. Use a real assignment to
verify that the Agent can access the necessary tools and Skills, produce a
useful result, preserve continuity, and stop at the intended boundary.

### 4. Keep using the same Agent for related work

Return related work to the same Agent and Thread. Corrections, vocabulary,
constraints, artifacts, and decisions then become part of its continuing
trajectory. Update the Profile only when a change should remain a durable
responsibility, not for every temporary instruction.

## Work With an Agent Day to Day

The normal loop is:

1. Give an Agent a goal, question, or concrete assignment in its workspace.
2. The Agent works in its own Thread, uses tools, and contacts other Agents when
   their judgment is required.
3. Ordinary internal replies and external interactions return to the directly
   responsible Agent rather than to the Owner.
4. A decision that genuinely requires a human appears in Needs You.
5. The Agent returns a readable result to the relationship that initiated the
   work.

The important distinction is causal:

> Do not ask whether every output looks important. Ask whose work it completes
> and where that result should return.

Agent Inbox belongs to the Agent. It can be useful evidence of workload,
routing, or a tooling problem, but it is not a second Owner task list. Needs You
contains the items that require the Owner to act.

**Validated practice:** ordinary Agent-to-Agent replies should return to the
requesting Agent for integration. Sending every intermediate reply to the Owner
recreates the forwarding burden the team is intended to reduce.

## Choose Process Management or Result Management

Process management and result management are not two fixed Agent types. The
same Agent and the same kind of work should be managed differently as capability
matures and risk boundaries change.

| Situation | Better management mode | What the Owner or Lead watches |
|---|---|---|
| New domain, ambiguous objective, unproven capability, or strong cross-domain dependency | Process management | Clarify the problem together, inspect critical steps, and shorten feedback distance |
| Clear objective and acceptance, with an externalized process that is repeatedly followed | Result management | Objective, boundary, result, evidence, and exception escalation |
| Production writes, external commitments, sensitive information, or irreversible state changes | Increase process visibility and add gates | Authorization, stop conditions, rollback, and independent review |
| Ordinary work in a mature process | Result management with periodic process sampling | Whether results remain acceptable and the standard is still valid |

As a working method becomes clear, write the stable process into a Skill, SOP,
checklist, tool constraint, or automated gate. Once the Agent has shown across
several real assignments that it follows those rules, and failures are
detectable, reversible, or contained by permission boundaries, the Owner or
Lead no longer needs to inspect every step. They can instead define the local
result, acceptance, evidence, and exceptions that must be escalated.

**Validated practice:** moving to result management does not mean that process
observation stops. Watch results in ordinary work, descend into the process
when something is abnormal, and sample the process periodically. Return
temporarily to process management when the standard fails, risk rises, or the
Agent begins to drift.

A Lead should not become a larger all-purpose Agent. It owns the overall goal,
priority, cross-domain tradeoffs, result integration, and final closure.
Specialist Agents own their domain facts, methods, and local results. If the
Lead repeatedly redoes every specialist's implementation and validation, the
result interface, Skill, gate, or responsibility boundary is usually not mature
enough.

## Choose the Right Coordination Mechanism

Different Loom mechanisms solve different continuity problems.

| Need | Use | What it preserves |
|---|---|---|
| Continue normal work with one Agent | Agent Thread | The Agent's working trajectory |
| Continue long-running work for one Agent | Goal | Runtime continuation and completion state |
| Ask another Agent for domain judgment | Message | Request, reply, delivery, and causal ownership |
| Obtain a human fact, choice, or authorization | Needs You | A durable human decision and resumption path |
| Wake work at a known time | Schedule | Time-based recurrence |
| Share a bounded matter across days and Agents | Topic (development build) | A responsible Agent, scoped participants, brief, waiting state, and evidence links |
| Resume when an external fact changes | Trigger (development build) | A governed reason to re-check provider state |

**Current behavior:** Agent Threads, Goals, Messages, Needs You, and Schedules
are present on `main` at the base of this draft.

**Development build:** Topics and Triggers are running in the current local
development build but are not present on that `main` baseline. Their final user
instructions must be rechecked after their implementation is integrated.

### Messages are for direct responsibility boundaries

An Agent sends a Message when it needs another long-lived Agent's judgment,
work, or feedback. The reply returns to the requesting Agent. The Owner should
not need to forward ordinary Agent-to-Agent communication.

### Let information follow responsibility and decision authority

Communication is not meant to make every Agent know everything. It should put
the necessary information in the hands of the role that owns the matter and
has authority to decide it:

> **Information flows to responsibility. Decisions flow to authority. Results
> return to the initiator. Exceptions escalate upward.**

Different relationships need different information resolutions:

- The Owner gives the Responsible Agent or Lead the reason for the work,
  priority, success criteria, risk tradeoffs, and material authorization.
- The Responsible Agent gives a Participant the domain-relevant objective,
  confirmed facts, non-goals, stable objects or versions, acceptance criteria,
  and escalation boundary.
- Specialist Agents exchange high-resolution reproductions, Artifacts,
  contracts, versions, and local problems directly.
- A specialist returns the result, key evidence, limitations, and any fact that
  changes overall scope, dependencies, risk, or completion conditions to the
  Responsible Agent.
- The Responsible Agent escalates only the human facts, choices,
  authorizations, and irreversible boundaries that truly require the Owner,
  rather than dumping the full specialist process.

This is not merely “send less information.” It is **minimum sufficient
resolution**: enough for the receiver to make its own decision without copying
the sender's entire professional context. High-resolution evidence that was not
actively sent upward stays with the domain owner and remains available for
stepwise inspection when something fails or needs retrospective review.

When a specialist Agent needs context that only the Owner knows from the
business situation, it should usually send the gap and its impact to the
Responsible Agent or domain owner that holds that business context. That owner
then creates one self-contained human question. This path puts context in the
hands of someone who must know it without requiring the upper layer to
understand model choice, debugging steps, or other internal process details.

### Use progressive communication for ambiguous questions

**Current recommendation:** for ambiguous, novel, cross-domain, or organization
questions, communicate from broad to narrow and shallow to deep, converging
across several rounds:

1. Open with a broad but real question and let the receiver explain the
   situation from its own long-lived domain context.
2. Align terminology, facts, boundaries, and unknowns.
3. Ask focused follow-ups about important examples, counterexamples, evidence,
   and responsibility conflicts.
4. Converge on options, a decision, an escalation, or the next experiment.

Each round should be understandable on its own and advance one clear layer of
judgment. For stable interfaces, factual lookups, fixed-format delivery, and
mature Skills, a one-shot structured request is usually more efficient.

**Hypothesis:** this method may work partly because an LLM generates each next
token from the current context. An overlong, over-specified one-shot prompt can
reinforce the sender's incorrect framing. Letting the receiver first generate
its own overall understanding creates a self-prompting scaffold for the next
round and exposes false premises earlier. This explanation still needs evidence
from quality, waiting, rework, and context loss; it is not a model guarantee.

### Needs You is for a human decision

Use Needs You only when work cannot responsibly continue without an Owner fact,
choice, or authorization. Include what is blocked, the exact question, and the
consequence of each available choice. After the Owner responds, the original
Agent should continue the same work rather than requiring the Owner to restate
the context.

### Topics are for bounded shared continuity

**Development build:** this mechanism is not part of the `main` baseline for
this draft.

A Topic is not a shared model Session or a project-management board. It is a
thin coordination record for a bounded matter that spans Turns, days, or
Agents. It has one Responsible Agent, scoped Participants, a versioned brief,
waiting conditions, and links to evidence. Each Participant keeps detailed
professional work in its own Agent Thread. The Topic context requires the
Participant to return scoped results, limitations, context gaps, and evidence
to the Responsible Agent. This is a working protocol: Loom requires both ends
of a topic-linked Message to belong to the Topic, but it does not force every
Participant Message to name the Responsible Agent as its recipient.

**Current recommendation:** create a Topic only when that shared continuity is
otherwise expensive to reconstruct. A small handoff or one meeting does not
automatically require one.

Ordinary Owner input in a Topic goes to the Responsible Agent. The Responsible
Agent routes work to Participants from the purpose, completion boundary, and
current brief. Participants work in their own Agent Threads and, under the
Topic working protocol, return results, limitations, and key evidence to the
Responsible Agent. Only a stage result integrated and published by the
Responsible Agent enters the Owner's Results Ready view.

If the current execution of a Participant Turn is clearly going wrong, the
Owner can open the active Turn currently associated with that Participant from
the Topic. Steer targets the captured explicit Turn. Interrupt begins from that
active Turn and aligns to the Agent's current authoritative Turn if the actual
active Turn has changed. The intervention leaves an audit event and notifies
the Responsible Agent. It does not automatically change Topic state, scope,
Participant responsibility, or any long-term organization relationship. If it
changes the overall plan, the Responsible Agent must update the brief and route
the work again.

### Trial a division of work in a Topic before making it durable

**Validated practice:** a new division of work or cross-domain collaboration
can first run as a topic-scoped responsibility. The Topic supplies a clear
purpose, completion boundary, Responsible Agent, Participants, and evidence
history while Profile, Organization, and Collaboration remain unchanged. This
makes the experiment reversible and avoids turning one project collaboration
into permanent organization fact too early.

Observe several rounds of real work. Does the division consistently reduce
duplicated judgment, incorrect routing, and context reconstruction? Do the
participants form a stable direct interface? Do exceptions and results return
to the right place? Only after the evidence is stable and the Owner confirms it
should a durable identity and responsibility enter a Profile, a hierarchical
responsibility enter Organization, or a stable horizontal interface enter
Collaboration. A repeated method should become a Skill or SOP rather than
remaining a temporary Topic instruction.

### Triggers are reasons to re-check, not conclusions

**Development build:** this mechanism is not part of the `main` baseline for
this draft. The current adapter observes supported GitHub pull-request and
workflow-run changes. Deployment, approval, webhook, and other providers remain
future scope.

A supported pull-request or workflow-run change can wake the responsible Agent.
The event is a reason to read the current authoritative provider state. It
should not be accepted as proof that the larger work is complete.

## Grow From One Agent Into a Team

### Evidence that a responsibility may need to split

**Hypothesis:** the following repeated signals can justify an organization
investigation, but no single signal proves that a new Agent is needed.

Consider a new Agent when several of these signals repeat:

- one Agent must retain unrelated high-resolution contexts;
- distinct professional judgments require different owners;
- sustained execution and queue wait show a stable capacity boundary;
- a Lead repeatedly descends into the same specialist implementation;
- the same responsibility is handed off repeatedly and loses context;
- a separate permission or external identity boundary must remain durable.

These signals start an investigation. They do not automatically prescribe a
split.

### Evidence against creating a new Agent

Do not create an Agent merely because:

- one task is unusually large;
- a reusable procedure could be a Skill;
- the existing Agent lacks a tool;
- a temporary project needs one participant;
- a name would make the organization chart look complete;
- the proposed role only forwards messages or status.

### Keep four kinds of team evidence separate

- **Profile:** what an Agent currently declares it owns.
- **Organization:** durable parent-child responsibility boundaries.
- **Collaboration:** stable horizontal interfaces between independent domains.
- **Activity:** time-scoped evidence of work that actually occurred.

A Topic can assemble a temporary set of Participants without declaring a
permanent relationship. A Message can record one collaboration without turning
it into an Organization or Collaboration relationship.

Organization and Collaboration are both declared structures. They do not grant
repository, deployment, credential, external-send, or production-write
permission, and they do not force every Message route. Permissions, external
roles, and concrete operations still require their own governance objects and
current authorization.

### Use matrix organization to separate result ownership from professional method

**Current recommendation:** as the Agent Team grows, separate three kinds of
relationships instead of drawing every adviser as a manager:

- **Business Home:** an Agent's stable business home. It owns business objects,
  context, priorities across Topics, and continuing responsibility after a
  Topic ends.
- **Topic Team:** a dynamic combination around one bounded matter. The
  Responsible Agent owns the Topic brief, routing, waiting state, result
  integration, and closure, but does not thereby become the Participants'
  long-term manager.
- **Practice Coach Network:** professional methods across Business Homes. A
  Coach gathers real successes, failures, and exceptions, develops candidate
  practices, calibrates them with the affected parties, and—after Owner
  confirmation—turns them into Skills, SOPs, tools, templates, or quality
  standards.

One Agent may belong to one Business Home, participate in several Topics, and
receive method support from several Practice Coaches. A communication Coach may
serve most Agents, while a frontend Coach serves only Agents working with Web,
UI, and browser automation. The Practice Coach owns “how we usually work.” The
business Lead or Topic Responsible owns “what we are doing now, why, its
priority, and acceptance.” The executing Agent retains local professional
judgment.

**Hypothesis:** whether a Practice Coach deserves to become a long-lived Agent
must be tested through real cycles across several Business Homes. Potential
applicability should not be predeclared as a large number of Collaboration
relationships. Begin with Topics, Messages, and explicit authorization. Make
relationships durable one by one only when method input, adoption feedback,
and a stable handoff recur.

Business Home, Topic Team, and Practice Coach Network are organization methods,
not new hard-coded Agent types or domain objects in Loom. They are expressed by
combining Profile and Organization, Topic, Collaboration, and shared Skills.
Only if real use proves that existing objects cannot carry a necessary
responsibility should a new product object be considered.

## Give Agents Governed External Roles

Think from the Agent outward:

1. Which long-lived Agent is responsible?
2. Which external identity represents it on a provider?
3. What role and permissions does it have in this specific conversation?

A Conversation Membership should make the audience, purpose, role, trigger,
reply behavior, and proactive-send boundary understandable to the Owner.
Connection, credential, gateway, provider IDs, and delivery diagnostics belong
to the advanced integration layer.

A dedicated Interface Agent is an optional organization pattern, not a required
Agent type. Use one only when external relationship context and judgment deserve
their own long-lived responsibility. Otherwise, a Domain Agent can hold a
governed external role directly.

## Observe and Adjust the Team

Use Overview and Team views to form questions, not performance rankings.

- **Execution state** reports what is running, waiting, stopped, or unavailable.
- **Inbox and queue wait** can reveal pressure, routing errors, reservations,
  connector delays, or missing tools.
- **Capacity** shows execution and waiting evidence; calendar non-executing time
  is a proxy, not proof that an Agent was available or unnecessary.
- **Token usage** reports consumption, not business value.
- **Activity** shows communication evidence, not authority or organizational
  importance.

Interpret signals together. High wait with low execution may indicate a Goal,
restart, permission, or connector problem rather than lack of capacity. Low
execution does not justify merging a sparse but high-risk responsibility.

When a repeated signal appears:

**Current recommendation:** use the following sequence to preserve evidence and
decision ownership.

1. Identify the affected work and stable evidence IDs.
2. Ask the responsible Agent how it understood the work and boundary.
3. Check adjacent owners only for their own experience.
4. Separate task, tool, scheduling, and connector failures from organization
   design.
5. Change a Profile or relationship only after the Owner confirms the durable
   responsibility change.
6. Observe later real work to see whether the change helped.

## Product Boundary

CodexLoom is for an advanced individual Owner, whether independent, operating a
One Person Company, or working inside a larger company, who wants to use a
long-lived Codex Agent Team. It helps organize that team and lets governed Agent
capabilities be reused by collaborators; it does not operate the Owner's entire
company.

CodexLoom is not intended to become:

- a CRM, ERP, finance, contract, or company operating system;
- a general project-management or workflow-building platform;
- an enterprise multi-tenant administration console;
- an automatic organization designer or Agent performance ranking system;
- a replacement for Codex runtime, model intelligence, or Thread history.

The Owner's goals remain the Owner's. Loom should reduce unnecessary setup,
searching, forwarding, tracking, and state reconstruction while the Agents do
the domain work.

## Current Limitations

- CodexLoom is local-first, self-hosted, and under active development.
- Some behavior depends on experimental Codex interfaces and may change across
  Codex releases.
- Topics and Triggers described above require the development build until their
  implementation is integrated into `main`.
- Runtime metrics are diagnostic evidence and contain known data-quality limits;
  they cannot determine organization changes automatically.
- Profiles and declared relationships express current responsibility but do not
  enforce every communication route.
- External provider capability and message shape differ; Loom governs identity,
  permissions, credentials, delivery, and audit without erasing provider-native
  concepts.

## Continue Reading

- [Documentation map](README.md)
- [Agent Profile](agent-profile.md)
- [Agent communication and CLI reference](loom-cli.md)
- [Conversation Membership](conversation-membership.md)
- [Integrations](integrations.md)
- [Skills](skills.md)
- [Product design baseline](product-design.md) - forward-looking design evidence,
  not a substitute for current user instructions.
