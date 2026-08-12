---
name: loom-needs-you
description: Ask the human operator for a durable decision, missing fact, or authorization through CodexLoom. Use when work genuinely needs human input and should resume later in the same long-lived Agent Thread.
---

# Needs You

Use a Human Request only when the current work genuinely needs a decision, fact, preference, review, or authorization that the Agent should not infer safely.

Do not create a request merely to report progress, seek reassurance, or delegate a low-risk judgment you can make within your Scope. A required request blocks the named workstream, not your entire Domain.

## Create a request

Use `question` as a short, single-line title. Put all multi-line background and
explanation in `context`, which supports Markdown. `--blocks` is stored as
`blockedWork`; keep it short and plain text. Do not put multi-line content in
`question` or `--blocks`. Pass real newline characters in `context`; do not
write the literal two-character sequence `\n`.

```bash
loom ask-user --from <your-agent-name> --question "应选择哪个发布窗口？" \
  --context "有两个发布窗口可选。

**明早（推荐）：** 风险较低，有完整的支持窗口。
**今晚：** 更快，但支持窗口较短。" \
  --blocks "安排并发布迁移通知" \
  --option "明早（推荐）::风险较低，有完整的支持窗口。" \
  --option "今晚::更快，但支持窗口较短。"
```

Use `--optional` when the answer would improve the work but does not block it. Required is the default.

Keep each request at one decision layer. Offer two or three mutually exclusive options when that makes the decision easier, put the recommended option first, and explain the tradeoff without assuming the human already has your local context. Free-form questions are valid when options would be artificial.

After the command succeeds, end the current Turn normally. Do not sleep, poll, repeatedly inspect request state, or keep the Turn alive. CodexLoom persists the request and will resume this same Agent Thread in a new Turn with a linked `<human_input_response>` when the human answers.

Do not use Codex's native `request_user_input` tool for this workflow. That tool suspends one active Turn; a CodexLoom Human Request is durable across long waits and service restarts.

When the answer arrives, use it to continue the related work if it is still relevant. Do not ask the same question again unless the answer is materially ambiguous.
