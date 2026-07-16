import { describe, expect, it } from "vitest";
import { emptyFeed, reduceFeed, summarizeTask } from "./feed";

describe("rollout history projection", () => {
  it("summarizes a Human Input response without exposing its XML envelope", () => {
    const text = `<human_input_response version="1" request_id="hrq_test" expectation="required">
  <question><![CDATA[May I restart?]]></question>
  <answer><![CDATA[Proceed at the safe boundary]]></answer>
  <blocked_work><![CDATA[Production verification]]></blocked_work>
</human_input_response>`;
    expect(summarizeTask(text)).toBe("Owner answer · Proceed at the safe boundary");
  });

  it("keeps the item timestamp and restores legacy Markdown newlines", () => {
    const text = `<agent_message version="1" id="msg_test" response="required" status="open">
  <from>alpha</from><to>beta</to><subject>Review</subject>
  <body>**First**\\n- second</body>
</agent_message>`;
    const state = reduceFeed(emptyFeed, {
      seq: 0,
      ts: "",
      type: "__history__",
      data: { turns: [{ items: [{ type: "user", timestamp: "2026-07-15T01:23:45Z", text }] }] },
    });
    expect(state.blocks).toHaveLength(1);
    expect(state.blocks[0]).toMatchObject({
      kind: "agentMessage",
      id: "msg_test",
      ts: "2026-07-15T01:23:45Z",
      variant: "req",
      body: "**First**\n- second",
    });
  });

  it("renders Codex turn errors instead of leaving an empty completed turn", () => {
    const state = reduceFeed(emptyFeed, {
      seq: 9,
      ts: "2026-07-16T04:22:45Z",
      type: "error",
      data: {
        error: {
          message: "The selected model is not supported with this account.",
        },
      },
    });

    expect(state.blocks).toEqual([
      {
        kind: "sys",
        ts: "2026-07-16T04:22:45Z",
        cls: "err",
        text: "The selected model is not supported with this account.",
      },
    ]);
  });

  it("renders managed attachments without exposing the transport manifest as message text", () => {
    const text = `Please review this\n\n<loom_attachments version="1" agent_id="agent-1">
  <attachment id="art_image" name="screen.png" mime_type="image/png" size="2048" path="/tmp/screen.png" url="/api/agents/agent-1/artifacts/art_image" />
  <attachment id="art_doc" name="brief.pdf" mime_type="application/pdf" size="4096" path="/tmp/brief.pdf" url="/api/agents/agent-1/artifacts/art_doc" />
</loom_attachments>`;
    const state = reduceFeed(emptyFeed, {
      seq: 0,
      ts: "",
      type: "__history__",
      data: { turns: [{ items: [{ type: "user", timestamp: "2026-07-16T05:00:00Z", text, attachments: [{ path: "/tmp/screen.png", mimeType: "image/png" }] }] }] },
    });

    expect(state.blocks).toHaveLength(1);
    expect(state.blocks[0]).toMatchObject({
      kind: "user",
      text: "Please review this",
      attachments: [
        { id: "art_image", name: "screen.png", mimeType: "image/png" },
        { id: "art_doc", name: "brief.pdf", mimeType: "application/pdf" },
      ],
    });
  });

  it("projects a published Agent artifact into the live trajectory", () => {
    const state = reduceFeed(emptyFeed, {
      seq: 18,
      ts: "2026-07-16T05:10:00Z",
      type: "loom/artifact-published",
      data: { artifact: { id: "art_report", name: "report.pdf", size: 8192, url: "/api/agents/agent-1/artifacts/art_report" } },
    });
    expect(state.blocks[0]).toMatchObject({ kind: "artifact", id: "art_report", artifact: { name: "report.pdf" } });

	const restored = reduceFeed(emptyFeed, {
	  seq: 0,
	  ts: "",
	  type: "__published_artifacts__",
	  data: { artifacts: [{ id: "art_report", name: "report.pdf", publishedAt: "2026-07-16T05:10:00Z" }] },
	});
	expect(restored.blocks[0]).toMatchObject({ kind: "artifact", id: "art_report", ts: "2026-07-16T05:10:00Z" });
	const reconciled = reduceFeed(restored, { seq: 0, ts: "", type: "__history_reconcile__", data: { turns: [] } });
	expect(reconciled.blocks).toEqual(restored.blocks);
  });
});
