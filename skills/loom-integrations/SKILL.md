---
name: loom-integrations
description: "Configure and verify CodexLoom external platform integrations with the loom CLI. Use when inspecting or changing Connections, Agent Addresses, DM access, group/channel Conversation Memberships, trigger and reply policies, allow/block rules, trust domains, or Inbox/Outbox delivery for Feishu/Lark, Slack, Parall, and compatible connectors. Also use when an external message is not reaching an Agent or a reply is not returning to its platform."
---

# Loom Integrations

Manage external communication as four separate layers: a platform `Connection`, an Agent's external `Address`, a group or DM `ConversationMembership`, and the resulting `Inbox`/`Outbox` delivery. Do not treat a platform account as the Agent itself.

## Establish the target and authority

1. Use `loom`; if it is not on `PATH`, report that the CodexLoom CLI is unavailable. Do not guess a machine-specific binary path.
2. Identify the Loom Agent, provider, external identity, and exact group/channel/contact being changed.
3. Inspect before mutating. Do not rebind an identity, broaden DM access, alter a trust domain, or enable a conversation unless the user requested that effect.
4. Never place provider tokens or app secrets in a command, JSON file, Agent message, transcript, or repository. CLI configuration accepts credential references, not secret values.

## Inspect the current model

```sh
loom agent get AGENT
loom integration list
loom integration status CONNECTION_ID
loom conversation list AGENT
loom conversation list AGENT --address ADDRESS_ID
loom conversation get MEMBERSHIP_ID
```

Resolve IDs from these commands instead of copying an ID from a different provider identity. Preserve stable Agent, Address, conversation, and actor IDs when display names change.

Interpret the layers correctly:

- **Connection**: one platform app, bot, account, or tenant connection plus gateway health.
- **Address**: one external identity bound to one long-lived Loom Agent.
- **Conversation Membership**: that Address's purpose, role, guidance, and policy in one group, channel, or DM.
- **Inbox/Outbox**: durable evidence that real messages entered and left Loom.

## Provision Connections carefully

`integration connect` registers a Connection record. It does not verify a provider secret, save it to Keychain, or install a managed gateway:

```sh
loom integration connect PROVIDER \
  --account ACCOUNT_REF \
  --credential-ref keychain:SERVICE_NAME
```

Use it only when the credential and gateway lifecycle have already been provisioned by an operator. `env:NAME` is valid for legacy or manually managed gateways, but never print or inspect the variable's value.

For first-time native Feishu or Slack onboarding, use CodexLoom's Integrations setup flow to validate credentials, store them in Keychain, discover platform identities, and install the managed gateway.

For an existing Parall Agent whose one-time Agent key is already in an owner-only regular file, use the atomic CLI import instead of configuring an Owner key:

```sh
loom integration import parall \
  --agent AGENT \
  --org-id ORG_ID \
  --external-agent-id USER_ID \
  --agent-key-file /absolute/path/to/agent.key
```

The file must belong to the current user, must not be a symlink, and must use `0600` or `0400` permissions. The command only sends credentials to loopback HTTP or HTTPS. It verifies that the key belongs to the specified active external Agent and can open a WebSocket, stores it in Keychain, creates or reuses the Connection and Address, and installs the managed gateway. It is idempotent and rolls back newly created state on failure. After a successful migration of the same Connection, Loom unloads and removes its legacy launchd gateway plist. The command does not require an Owner key, change the external display name, configure a group, or delete the source key file. After `integration status` reports connected, securely remove the source file. Never put the key in a command argument, environment variable, JSON config, message, or transcript.

After the first successful import, omit `--agent-key-file` to reuse and revalidate the credential already in Keychain. The importer matches provider, stable external identity, and Loom Agent. It upgrades one legacy `org-agent:USER_ID` Connection in place. If duplicate legacy and managed records already exist, it keeps the managed identity canonical, projects the preferred Membership policy onto it, and archives the old Connection, Address, and Membership with `supersededBy` links. Historical IDs remain readable for Inbox/Outbox audit, but archived records must never be re-enabled or selected for new delivery.

## Bind and govern an Address

Bind an already provisioned Connection to an Agent:

```sh
loom integration bind AGENT CONNECTION_ID \
  --identity EXTERNAL_ID \
  --display-name "External display name" \
  --trigger mention \
  --reply-policy final_answer \
  --dm-policy managed \
  --trust-domain TRUST_DOMAIN \
  --enabled false
```

Inspect the result, then enable the Address:

```sh
loom integration enable ADDRESS_ID
```

Update only intended fields:

```sh
loom integration update-address ADDRESS_ID \
  --dm-policy managed \
  --trigger mention \
  --reply-policy final_answer \
  --enabled true
```

Address policy meanings:

- `dm-policy=open`: any sender may enter; use only for an intentionally public Agent.
- `dm-policy=managed`: unknown senders remain pending until a DM Membership is approved.
- `dm-policy=closed`: reject all DMs.
- `trigger=mention`: groups require a structured platform mention; DMs remain direct.
- `trigger=explicit_dispatch`: only provider-confirmed dispatches enter the Agent queue.
- `reply-policy=final_answer`: send the Agent's final answer through Outbox.
- `reply-policy=explicit`: require `loom integration send --reply-to` or `loom inbox no-reply`.
- `reply-policy=none`: receive without replying.

Use `--allow-actors`, `--allow-conversations`, `--block-actors`, and `--block-conversations` only for stable provider IDs. Block rules take precedence. If actor and conversation allowlists are both set, both must match.

Do not use a trust domain as a substitute for isolation. All Addresses on one Agent must have compatible trust domains because they share the same long-lived Thread.

## Configure a group or channel

Create new Memberships disabled, inspect their context, then enable them:

```sh
loom conversation set ADDRESS_ID CONVERSATION_ID \
  --type group \
  --name "Conversation name" \
  --purpose "What this conversation exists to discuss" \
  --role "What this Agent contributes here" \
  --guidance "What to answer, avoid, escalate, or keep private" \
  --trigger mention \
  --reply-policy final_answer \
  --outbound-policy reply_only \
  --enabled false

loom conversation get MEMBERSHIP_ID
loom conversation enable MEMBERSHIP_ID
```

Use platform-stable IDs, not display names, for `CONVERSATION_ID`. Ensure the bot is already a member of a Slack private channel or a provider conversation when the provider requires it.

Membership outbound policy is independent from inbound trigger and reply policy:

- `reply_only` is the default: the Agent may reply to an Inbox item from this Membership but cannot initiate a message.
- `proactive` allows replies and governed proactive sends to this Membership.
- `none` prevents all outbound delivery for this Membership.

## Configure a managed DM

A DM Membership must bind both the stable conversation ID and the other person's stable actor ID:

```sh
loom conversation set ADDRESS_ID CONVERSATION_ID \
  --type dm \
  --actor ACTOR_ID \
  --name "Person display name" \
  --purpose "Why this communication relationship exists" \
  --role "How the Agent should support this person" \
  --guidance "Allowed topics, privacy boundary, and escalation rules" \
  --trigger direct \
  --reply-policy final_answer \
  --enabled false

loom conversation get MEMBERSHIP_ID
loom conversation enable MEMBERSHIP_ID
```

Choose `trigger=direct` when every DM from that approved person should start a Turn. Choose `trigger=explicit_dispatch` when the provider distinguishes ordinary messages from requests for Agent work. Do not approve a sender using only a mutable display name.

For multiline context, use `--file` with a temporary non-secret JSON document:

```json
{
  "conversationType": "dm",
  "actorId": "provider-stable-user-id",
  "displayName": "Person display name",
  "purpose": "Long-term purpose",
  "role": "Agent role for this person",
  "guidance": "Behavior and privacy boundary",
  "triggerPolicy": "direct",
  "replyPolicy": "final_answer",
  "outboundPolicy": "reply_only",
  "enabled": false
}
```

Then run `loom conversation set ADDRESS_ID CONVERSATION_ID --file FILE` and remove the temporary file after verification.

## Edit without losing concurrent changes

Read the current Membership version and include it when changing an existing Membership:

```sh
loom conversation get MEMBERSHIP_ID
loom conversation set ADDRESS_ID CONVERSATION_ID \
  --expected-version CURRENT_VERSION \
  --guidance "Revised guidance"
```

A version conflict means another operator changed the Membership. Read it again and reconcile; do not retry blindly. Omitted fields remain unchanged. `conversationType` and a non-empty DM `actorId` cannot be reassigned in place; create the correct Membership or investigate the identity mismatch.

## Verify the real delivery path

Configuration output is not delivery proof. Complete these checks:

1. Confirm `loom integration status CONNECTION_ID` reports a connected gateway without `lastError`.
2. Confirm the intended Address and Membership are enabled with `loom integration list` and `loom conversation get`.
3. Send a real DM or structured mention from the external platform.
4. Inspect the resulting path:

```sh
loom inbox AGENT --origin PROVIDER
loom outbox AGENT
```

5. Confirm the message appeared in the Agent Thread and the final reply appeared on the external platform.

## Send external messages through Loom

Do not call provider CLIs or APIs directly. Reply to an external Inbox item through the governed delivery API so Loom inherits its Connection, Address, Conversation, thread, identity, and audit context:

```sh
loom integration send --from AGENT --reply-to INBOX_ITEM_ID \
  --body-file /absolute/path/to/reply.md \
  --file /absolute/path/to/image.png \
  --file /absolute/path/to/report.pdf
```

Send proactively only when the user authorized the exact Membership and its outbound policy is `proactive`:

```sh
loom integration send --from AGENT --to MEMBERSHIP_ID \
  --body "Connection verification" \
  --expect-reply none \
  --idempotency-key STABLE_OPERATION_ID
```

`--reply-to` and `--to` are mutually exclusive. Never use raw provider conversation IDs for proactive delivery. Reuse the same idempotency key when retrying the same business action. The CLI waits for Connector confirmation by default; use `--async` only when the workflow intentionally needs queue-only behavior. Text is sent first and repeated `--file` values retain their order. Loom stages local files in its managed artifact store, limits a delivery to 8 files and each file to 25 MB, and records every external message/file ID in Outbox.

## Diagnose failures by layer

- **Connection disconnected/degraded**: inspect `integration status`; fix gateway process, credentials, provider scopes, or rate limits before changing Membership policy.
- **Message absent from Inbox**: verify gateway health, Address enabled state, provider bot membership, stable IDs, allow/block rules, DM policy, and structured mention/dispatch evidence.
- **Inbox pending access**: create and enable a DM Membership for the exact conversation and actor only after authorization.
- **Inbox queued**: the Agent is busy; this is not an integration failure.
- **Agent replied but platform did not**: inspect Outbox state and provider error. Do not mark the Inbox handled before provider send succeeds.
- **Wrong behavior in one conversation**: edit its Membership purpose, role, guidance, trigger, or reply policy; do not rewrite the global Agent Profile for a local channel rule.
- **Different organization or privacy boundary**: create a separate Agent rather than relying only on allowlists or guidance.

Use `loom integration send --reply-to` for external replies and `loom inbox no-reply|defer|retry` for the remaining Inbox actions. Never use `loom msg --reply-to` for an external platform reply; `loom msg` is reserved for internal Agent-to-Agent communication.
