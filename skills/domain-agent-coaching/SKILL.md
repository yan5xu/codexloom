---
name: domain-agent-coaching
description: "Coach the design and ongoing reflection of long-lived domain Agents and Agent organizations. Use when defining or revising Identity, Domain, Scope, Organization or Collaboration relationships; deciding whether recurring work should become an Agent, Internal Agent, Skill, or Task; examining overlap, gaps, overloaded domains, unclear external roles, or unhealthy collaboration paths. Guide with evidence and questions without assuming the business context or making the owner's decisions."
---

# Domain Agent Coaching

Help people and Agents reason about durable Agent design. Act as a coach: reveal assumptions, sharpen boundaries, gather perspectives, and leave consequential business decisions with their owner.

## Hold the durable-Agent frame

A Domain Agent is a long-lived working subject, not a task wrapper. Its Profile defines a stable Identity, Domain, and Scope; its Codex Thread retains trajectory and working context across many interactions.

Keep these concepts separate:

- **Agent**: a durable domain with its own continuity, responsibilities, and relationships.
- **Internal Agent**: a durable subdomain that primarily serves one parent Agent and is normally reached through that parent.
- **Skill**: reusable procedural knowledge or a capability an Agent can invoke. It does not own a domain.
- **Task**: a bounded outcome performed by an Agent. It does not justify a durable identity by itself.

Do not recommend a new Agent merely because work is large. Look for recurring responsibility, independent context, stable boundaries, and a continuing need to coordinate.

## Build an evidence base

Inspect the current state before coaching:

```sh
loom team
loom team AGENT
loom agent get AGENT
loom profile get AGENT
```

Use Profiles as declared intent, Organization and Collaboration as declared structure, and message Activity as behavioral evidence. None is complete alone.

Label what you learn:

- **Fact**: explicitly declared or directly observed.
- **Signal**: repeated behavior that may indicate a design issue.
- **Inference**: a plausible interpretation that needs validation.
- **Decision**: a choice confirmed by the responsible human or Agent owner.

Do not turn message volume into authority, or a temporary project spike into a permanent scope boundary.

## Use workload evidence on demand

Read workload signals only after a user, Owner, incident, repeated delay, reported capacity problem, or planned
organization review has created a concrete coaching question. Do not scan all Agents routinely or treat the output
as a ranking.

Start with the organization and then inspect the affected Agent, unless a specific Message, Inbox item, or Turn is
already known:

```sh
loom workload --days 7
loom workload AGENT --days 1 --evidence
loom workload AGENT --days 30
loom workload AGENT --days 30 --evidence
loom workload AGENT --days 90 --json
```

Interpret the report as a question-forming evidence source:

- `calendar non-executing proxy` is not online idle time; it includes machine and service downtime.
- Queue percentiles are meaningful only with their sample count. `n=0` means no completed sample, not zero wait.
- Read executing, queue wait, current backlog, work source, wait reason, and data quality together.
- Use stable evidence IDs to inspect only the context needed for the active coaching question.
- Do not infer overloaded, underused, split, or merge from a percentage alone.

High wait may come from capacity, an active Goal reservation, restart drain, Connector behavior, or scheduling. Low
execution may be healthy for a sparse high-risk Domain. Form alternative explanations, then interview the relevant
Owner and Agent before proposing an organization change.

## Run the coaching loop

1. **Frame the tension.** State the observed design question without prescribing an answer.
2. **Inspect the structure.** Read the affected Profiles, relationships, and relevant communication evidence.
3. **Interview perspectives.** Ask the owner and affected Agents what they believe they own, depend on, reject, and repeatedly reconstruct.
4. **Test the boundaries.** Look for overlap, orphaned decisions, hidden routing, overloaded context, duplicated judgment, or an external role conflicting with internal responsibility.
5. **Offer alternatives.** Present a small number of viable designs and their tradeoffs.
6. **Name the decision owner.** Make clear who understands the business well enough to decide.
7. **Record only confirmed changes.** Do not mutate Profiles or relationships without explicit confirmation.
8. **Revisit after evidence accumulates.** Organization design is continuous; one conversation is not proof that a structure works.

Use `$loom-communication` when gathering another Agent's perspective. Ask about that Agent's own experience; do not ask it to decide the whole organization.

## Questions that expose boundaries

Use only the questions relevant to the tension:

- What durable judgment should this Agent become better at over months?
- What enters this Domain, and what must leave it?
- Which decisions can this Agent make without rebuilding context or consulting another owner?
- Which requests repeatedly arrive but do not belong here?
- What knowledge is reusable procedure, and what responsibility requires a continuing subject?
- If this Domain is split, what context and decisions remain with the parent?
- Who may contact an Internal Agent directly, and why?
- Does an external channel role express this Agent's Domain, or require a dedicated Interface Agent?
- What evidence would show that the proposed structure is wrong?

## Produce a decision artifact

Keep the output concise and reviewable:

```markdown
## Signal
Observed facts and repeated behavior.

## Design Question
The boundary or organizational choice that needs examination.

## Perspectives
What affected people and Agents believe, with disagreements preserved.

## Unresolved Assumptions
What is still inferred rather than known.

## Possible Designs
Two or three options with consequences and reversibility.

## Decision Owner
Who has enough business context and authority to choose.

## Decision
Confirmed choice, or `open` when no choice has been made.
```

Never manufacture consensus. If evidence is insufficient, leave the decision open and identify the next observation or conversation that would reduce uncertainty.
