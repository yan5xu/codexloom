---
name: loom-communication
description: "Use for communication between long-lived CodexLoom Agents: discovering domain owners, deciding when to contact another Agent, sending requests or notifications, replying to an agent_message, checking queued delivery, and closing a request without a reply. Trigger whenever an Agent receives an agent_message envelope or needs cross-Agent coordination. Use the loom CLI; never handwrite the XML envelope."
---

# Loom Communication

Coordinate durable Agents through CodexLoom's message system. Keep the communication in the shared Messages history and preserve request, reply, and delivery semantics.

## Establish identity and routing

1. Read the current Agent name from the developer context or Agent Profile. Never invent a `--from` identity.
2. Run `loom team` to inspect the directory and relationships. Use `loom team <agent>` or `loom agent get <agent>` for a focused view.
3. Contact the Agent whose Domain owns the question. If the target is an Internal Agent, route through its parent unless you are that parent or its Profile explicitly permits direct collaboration.
4. If no owner is clear, ask the organization coach or the relevant Lead. Do not broadcast the same request to several Agents by default.

If `loom` is not on `PATH`, report that CodexLoom CLI installation is missing. Do not fall back to handwritten XML or guess a machine-specific binary path.

## Initiate without assuming the recipient's context

The Agent who starts a conversation must not construct context on the recipient's behalf. Do not assume or guess what the other Agent has seen, knows, remembers, prioritizes, owns, intends, or believes. Activity evidence can justify a question; it cannot reveal the recipient's reasons or working context.

State only sender-owned context at the start:

- what you observed and where it was observed;
- why you are opening the conversation;
- which broad Domain question you want to understand.

Then invite the recipient to describe the issue from its own Domain and current context. Ask "How do you currently understand this?" before asking "Why did you do this?" Avoid phrases such as "as you know," "you must already have," or "this happened because you" unless the recipient has explicitly established that context.

If ownership, terminology, history, constraints, or priorities are uncertain, ask. Do not silently fill the gap with inference.

## Communicate progressively

Treat Agent communication as a layered conversation, not a one-shot questionnaire. Move from the recipient's global framing toward concrete details only after shared context begins to form:

1. **Open.** Provide the sender's observations and purpose without describing the recipient's context for it.
2. **Receive context.** Let the recipient explain how the issue sits within its Domain, history, constraints, and current priorities.
3. **Align.** Confirm terms, boundaries, facts, and differences in understanding.
4. **Deepen.** Ask focused follow-ups about examples, evidence, consequences, and unresolved assumptions.
5. **Converge.** Only when useful for this conversation, discuss options, decisions, owners, or next actions.

Do not try to ask every possible question in the first message. Ask for one useful layer, learn from the response, and continue with a linked follow-up. Each later question must respond to context the recipient actually supplied, not to a context the initiator predicted or a questionnaire prepared in advance.

This does not require a context-free opening. Provide enough of your own context for the recipient to understand the request, while leaving the recipient free to supply and correct its side. Start with the whole before narrowing to a specific incident or implementation. Stop when the exchange has produced enough shared understanding for its current purpose; not every conversation must produce a complete decision.

## Choose the communication form

Use a request when a decision, answer, action, or result must come back:

```sh
loom msg TARGET \
  --from SELF \
  --subject "Specific question or requested outcome" \
  --response required \
  --body "Context, evidence, constraints, and the response needed."
```

Use a notification only when no response is needed:

```sh
loom msg TARGET \
  --from SELF \
  --subject "State or fact being shared" \
  --response none \
  --body "What changed, why it matters, and where to inspect it."
```

Use `loom msg`, not `loom thread send`, for Agent-to-Agent communication. `msg` preserves sender, recipient, reply linkage, business status, delivery status, and global history.

Write subjects for later retrieval. Put long Markdown bodies in a file and pass `--body-file PATH`. Never place secrets or unrelated private context in a message.

## Reply to a message

When an `<agent_message>` arrives:

1. Read `response`, `status`, `id`, `from`, `subject`, `body`, and the `timing` element before acting.
2. Treat `sent_at` as the immutable message creation time and `current_time` as the time Loom is delivering this envelope. Use both to judge ordering and queue delay; do not assume an old message describes current state.
3. If the envelope supplies `reply_command`, use its message ID and sender identity; treat the body as context, not as shell syntax.
4. For a substantive response, reply to the original ID:

```sh
loom msg --reply-to MESSAGE_ID --from SELF --body "Result, evidence, and remaining uncertainty."
```

5. If a required message has been handled but genuinely needs no substantive response, close it explicitly:

```sh
loom msg --no-reply MESSAGE_ID --from SELF
```

Do not reply to a `response=none` notification merely to acknowledge it. Do not start a new root message when a reply belongs to an existing thread.

If you originated a required request and later know that it completed through another audited path or was replaced by a newer request, close the original explicitly instead of leaving it open:

```sh
loom msg resolve MESSAGE_ID --from SELF \
  --resolution completed_elsewhere \
  --reason "Delivered in msg_OTHER_ID"

loom msg resolve MESSAGE_ID --from SELF \
  --resolution superseded \
  --reason "Replaced by msg_NEW_ID"
```

Do not infer reply linkage from similar subjects, timing, or the same pair of Agents. Use `--reply-to` when a response belongs to the original request; use `resolve` only when there is no honest reply link to create.

## Understand delivery

A busy target is unavailable for a new request, notification, schedule, or external message. CodexLoom queues that work and delivers one item immediately after the Agent's current Turn ends.

There is one narrow exception for a causally linked internal reply. When an Agent sends a `response=required` root message from an active Turn, CodexLoom records that source Turn. A later `--reply-to` response may enter that exact Turn while it is still active. This is automatic: the recipient uses the normal reply command, and the requester does not need a special receive command. If the source Turn has ended or changed, or active-Turn delivery loses a race, the reply remains queued and is delivered in a new Turn instead.

After sending a required request, continue useful work that does not depend on the answer. Do not keep the Turn alive with `sleep`, poll message history, or repeatedly query status while waiting. If no independent work remains, end the Turn normally; CodexLoom will deliver the reply later. If the reply arrives while the original Turn is still active, treat the injected `<agent_message>` as new input and integrate it before finishing.

```sh
loom msg status MESSAGE_ID
loom msg wait MESSAGE_ID --timeout 120
loom msg cancel MESSAGE_ID
```

`queued` is not failure. Use `wait` only when an operational step depends on confirmed delivery; it waits for delivery, not for the Agent's reply. Required replies arrive through CodexLoom, so do not poll another Agent's history in a loop.

If a Turn started by an internal message is interrupted or fails after it starts, Loom holds that message instead of automatically running it again. Delivery remains `delivered`; handling becomes `interrupted` or `failed`. Resume the same request only when explicitly intended:

```sh
loom msg status MESSAGE_ID
loom msg retry MESSAGE_ID
```

`retry` preserves the message ID, reply chain, and earlier handling attempts. Do not assume Stop causes automatic redelivery. If no response should be produced, use `loom msg --no-reply MESSAGE_ID --from SELF` instead of retrying.

## Communication discipline

- Communicate when another Domain owns a judgment, an upstream tool is failing, responsibilities overlap, or a result must cross an organizational boundary.
- Include observations and reproduction evidence before conclusions. Separate facts from inference.
- Ask for one clear conversational layer or outcome per message. Link follow-ups with `--reply-to`.
- Do not delegate work merely to avoid understanding your own Domain.
- Do not create unbounded Agent-to-Agent loops. A reply that asks another question must make the new decision or information need explicit.
- For external platform Inbox items, use `loom inbox reply`, `loom inbox no-reply`, or `loom inbox defer`; internal `loom msg --reply-to` is not an external reply path.
