import { describe, expect, it } from "vitest";
import { emptyFeed, reduceFeed, type Block } from "./feed";
import { blockStableID, markerKindForBlock, markerPercent, messageMarkerForBlock } from "./message-markers";

describe("message marker metadata", () => {
  it("classifies every rendered message kind with a distinct marker family", () => {
    const blocks: Block[] = [
      { kind: "user", id: "user-1", ts: "2026-08-16T01:00:00Z", text: "task", attachments: [] },
      { kind: "agentMessage", id: "msg-1", ts: "2026-08-16T01:00:01Z", variant: "req", from: "a", to: "b", subject: "subject", body: "body", raw: "", response: "required" },
      { kind: "externalMessage", id: "inbox-1", inboxItemId: "item-1", ts: "2026-08-16T01:00:02Z", provider: "parall", addressId: "address-1", senderId: "sender-1", sender: "sender", conversationId: "conversation-1", expectation: "optional", replyPolicy: "none", body: "body", raw: "", attachments: [] },
      { kind: "externalTrigger", id: "trigger-1", triggerId: "trigger-1", ts: "2026-08-16T01:00:03Z", provider: "github", connectionId: "connection-1", resourceKind: "pull-request", subjectKey: "owner/repo#1", event: "merged", eventKey: "event-1", summary: "summary", resumeInstruction: "resume", instruction: "instruction", raw: "" },
      { kind: "topicContext", id: "topic-1", ts: "2026-08-16T01:00:04Z", topicId: "topic-1", status: "active", briefVersion: 1, eventSeq: 1, title: "Topic", responsibleAgent: "lead", purpose: "purpose", completionBoundary: "done", yourResponsibility: "validate", briefSummary: "summary", currentState: "active", nextStep: "next", limitations: "none", instruction: "instruction", links: [], delta: [], raw: "" },
      { kind: "agent", id: "answer-1", ts: "2026-08-16T01:00:05Z", text: "answer", streaming: false },
      { kind: "think", id: "think-1", ts: "2026-08-16T01:00:06Z", text: "thinking", done: true },
      { kind: "command", id: "command-1", ts: "2026-08-16T01:00:07Z", command: "pwd", status: "completed", exitCode: 0, durationMs: 1, output: "" },
      { kind: "file", id: "file-1", ts: "2026-08-16T01:00:08Z", status: "completed", changes: [] },
      { kind: "image", id: "image-1", ts: "2026-08-16T01:00:09Z", data: "data:image/png;base64,AA==" },
      { kind: "artifact", id: "artifact-1", ts: "2026-08-16T01:00:10Z", artifact: { name: "report.txt" } },
      { kind: "usage", id: "usage-1", ts: "2026-08-16T01:00:11Z", inputTokens: 1, cachedInputTokens: 0, outputTokens: 1, reasoningOutputTokens: 0, totalTokens: 2, calls: 1 },
      { kind: "sys", ts: "2026-08-16T01:00:12Z", cls: "dim", text: "system" },
      { kind: "raw", id: "raw-1", ts: "2026-08-16T01:00:13Z", type: "unknown", json: "{}" },
    ];

    expect(blocks.map(markerKindForBlock)).toEqual([
      "user", "agent", "external", "trigger", "topic", "assistant", "thinking", "command", "file", "image", "artifact", "usage", "system", "raw",
    ]);
    expect(new Set(blocks.map((block) => messageMarkerForBlock(block).colorClass)).size).toBeGreaterThan(1);
    expect(messageMarkerForBlock(blocks[0])).toMatchObject({ id: blockStableID(blocks[0]), label: "User task: task" });
  });

  it("keeps history block IDs stable across reconcile and prepend windows", () => {
    const currentTurn = {
      id: "turn-current",
      items: [
        { type: "user", id: "user-current", timestamp: "2026-08-16T01:00:00Z", text: "current task" },
        { type: "answer", id: "answer-current", timestamp: "2026-08-16T01:00:01Z", text: "current answer" },
        { type: "commandExecution", id: "command-current", timestamp: "2026-08-16T01:00:02Z", command: "pwd", status: "completed" },
        { type: "thinking", timestamp: "2026-08-16T01:00:03Z", text: "fallback identity" },
      ],
    };
    const seed = reduceFeed(emptyFeed, { seq: 1, ts: "", type: "__history__", data: { turns: [currentTurn] } });
    const seedIDs = seed.blocks.map(blockStableID);
    const reconciled = reduceFeed(seed, { seq: 2, ts: "", type: "__history_reconcile__", data: { turns: [currentTurn] } });
    const prepended = reduceFeed(reconciled, {
      seq: 3,
      ts: "",
      type: "__history_prepend__",
      data: { offset: 1, turns: [{ id: "turn-older", items: [{ type: "answer", id: "answer-older", timestamp: "2026-08-16T00:59:00Z", text: "older answer" }] }] },
    });

    expect(reconciled.blocks.map(blockStableID)).toEqual(seedIDs);
    expect(prepended.blocks.slice(-seedIDs.length).map(blockStableID)).toEqual(seedIDs);
    expect(prepended.blocks.map(blockStableID)).toContain("agent:answer-older");
    expect(seed.blocks.every((block) => !blockStableID(block).includes("h-"))).toBe(true);
  });

  it("keeps repeated live user messages distinct without losing replay stability", () => {
    const firstEvent = { seq: 1, ts: "2026-08-16T01:00:00Z", type: "loom/user-message", data: { text: "same task" } };
    const secondEvent = { seq: 2, ts: "2026-08-16T01:00:01Z", type: "loom/user-message", data: { text: "same task" } };
    const first = reduceFeed(emptyFeed, firstEvent);
    const both = reduceFeed(first, secondEvent);
    const replay = reduceFeed(emptyFeed, firstEvent);

    expect(blockStableID(both.blocks[0])).not.toBe(blockStableID(both.blocks[1]));
    expect(blockStableID(replay.blocks[0])).toBe(blockStableID(first.blocks[0]));
  });

  it("keeps marker positions inside the rail and centers a single message", () => {
    expect(markerPercent(0, 1)).toBe(50);
    expect(markerPercent(0, 10)).toBe(0.5);
    expect(markerPercent(9, 10)).toBe(99.5);
  });
});
