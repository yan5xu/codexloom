# Markdown Rendering Fixture

这份内容用于验证 CodexLoom 的 Markdown 渲染质量，重点覆盖嵌套结构、混合块、长内容和 CJK/English 混排。

## 1. Executive Summary

目标是让复杂回答在 Agent Thread 中仍然清晰：

- 顶层列表要容易扫读。
- 二级、三级列表要能明显区分层级。
- 列表里的段落、代码块、引用和表格不能挤在一起。
- 表格宽时要横向滚动，而不是挤爆消息列。
- 行内元素如 `code`、**strong**、_emphasis_、~~deleted~~ 和 [link](https://example.com) 要有稳定视觉权重。

## 2. Nested Lists

1. Product surface
   1. Web console
      - Agent Thread feed
        - User turn
        - Assistant answer
        - Agent message cards:
          - `REQ`
          - `RES`
          - `NOTIFY`
      - Global Messages
        1. Open messages
        2. All messages
        3. Thread view
   2. CLI
      - `loom msg <to> --from <from> --response required`
      - `loom msg --reply-to <message-id> --from <from>`
2. Runtime surface
   - One stable Agent per long-lived subject.
   - A Codex Thread is the Agent's primary context binding.
   - Turns are short-lived work units inside a Thread.
   - Rollout remains the single source of truth for Thread history.

Nested paragraph inside list:

1. First decision

   This item has an explanatory paragraph. The paragraph should align under the list item text, not drift too far left or right.

   > A quote inside a list item should be visibly nested, but still compact.

   ```ts
   const envelope = {
     response: "required",
     replyTo: null,
   };
   ```

2. Second decision

   - Child point with `inline code`
   - Child point with **bold outcome**

## 3. Task Lists

- [x] Detect agent message envelope in live events.
- [x] Detect agent message envelope in rollout history.
- [ ] Add per-Agent communication summary.
- [ ] Add CLI message history command.

## 4. Blockquotes

> Top-level quote with enough text to wrap across lines. It should read as supporting context, not as a dominant card.
>
> - Quotes may contain lists.
> - Quotes may contain `inline code`.
>
> Nested quote:
>
> > This inner quote should remain legible and not collapse into the parent.

## 5. Code

Inline command example: `loom msg pinix-lead --from codex-loom-dev --response none`.

```xml
<agent_message version="1" id="msg_example" response="required" status="open">
  <from>codex-loom-dev</from>
  <to>pinix-lead</to>
  <subject>Rendering check</subject>
  <reply_command>loom msg --reply-to msg_example --from pinix-lead --body "..."</reply_command>
  <body><![CDATA[
Nested Markdown should render as a structured card in the Agent Thread feed.
  ]]></body>
</agent_message>
```

```go
type AgentMessage struct {
    ID       string
    From     string
    To       string
    Response string
}
```

## 6. Table

| Case | Input | Expected rendering | Risk |
| --- | --- | --- | --- |
| Notify | `response=none` | Blue-ish `NOTIFY` card | Low |
| Request | `response=required` | Warning `REQ` card with reply command | Medium |
| Reply | `reply_to=msg_xxx` | Success `RES` card with parent id | Medium |
| Very long cell | This cell intentionally contains a long sentence that should not destroy the message column layout or force the whole app to scroll sideways. | Table scrolls inside the prose block | High |

## 7. Mixed CJK Content

中文段落和 English identifiers 混排时，`source.item`、`touchpoint`、`response=required` 这些词应该保持清楚，不要因为换行或字距导致难读。

- 第一层：业务主体
  - 第二层：`company` / `person` / `batch`
    - 第三层：证据对象 `source.item`
      - 第四层：人工判断 `note`

## 8. Horizontal Rule

Above.

---

Below.
