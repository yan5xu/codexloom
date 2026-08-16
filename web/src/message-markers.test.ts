import { describe, expect, it } from "vitest";
import { emptyFeed, reduceFeed, type Block, type FeedState, type TopicContextPayload } from "./feed";
import {
  blockStableID,
  messageMarkerClusters,
  messageMarkerLensRange,
  receivedMessageKindForBlock,
  receivedMessageMarkerForBlock,
  timelineMarkerPercent,
  type MessageMarkerRecipient,
} from "./message-markers";

const recipient: MessageMarkerRecipient = { id: "agent-current-id", name: "agent-current" };

function agentMessage(overrides: Partial<Extract<Block, { kind: "agentMessage" }>> = {}): Extract<Block, { kind: "agentMessage" }> {
  return {
    kind: "agentMessage",
    id: "msg-inbound",
    ts: "2026-08-16T01:00:01Z",
    variant: "req",
    from: "agent-sender",
    to: recipient.name,
    subject: "Inbound work",
    body: "body",
    raw: "",
    response: "required",
    ...overrides,
  };
}

function topicContext(payload?: TopicContextPayload): Extract<Block, { kind: "topicContext" }> {
  return {
    kind: "topicContext",
    id: `topic-${payload?.kind || "context"}`,
    ts: "2026-08-16T01:00:02Z",
    topicId: "topic-1",
    status: "active",
    briefVersion: 1,
    eventSeq: 1,
    title: "Topic",
    responsibleAgent: "lead",
    purpose: "purpose",
    completionBoundary: "done",
    yourResponsibility: "validate",
    briefSummary: "summary",
    currentState: "active",
    nextStep: "next",
    limitations: "none",
    instruction: "instruction",
    links: [],
    delta: [],
    payload,
    raw: "",
  };
}

function markerIDs(state: FeedState): string[] {
  return state.blocks.flatMap((block) => {
    const marker = receivedMessageMarkerForBlock(block, recipient);
    return marker ? [marker.id] : [];
  });
}

describe("received message marker metadata", () => {
  it("marks only messages received by the current Agent with source-specific colors", () => {
    const owner: Block = { kind: "user", id: "owner-1", ts: "2026-08-16T01:00:00Z", text: "Owner task", attachments: [] };
    const inbound = agentMessage();
    const inboundByID = agentMessage({ id: "msg-by-id", to: recipient.id });
    const schedule = agentMessage({ id: "msg-schedule", from: "scheduler", subject: "Daily review" });
    const external: Block = {
      kind: "externalMessage",
      id: "inbox-1",
      inboxItemId: "item-1",
      ts: "2026-08-16T01:00:03Z",
      provider: "parall",
      addressId: "address-1",
      senderId: "sender-1",
      sender: "External sender",
      conversationId: "conversation-1",
      expectation: "optional",
      replyPolicy: "none",
      body: "body",
      raw: "",
      attachments: [],
    };
    const topicAgent = topicContext({
      kind: "agentMessage",
      label: "REQ",
      body: "Topic-scoped request",
      subject: "Topic request",
      from: "loom-product",
      to: recipient.name,
      variant: "req",
    });
    const topicOwner = topicContext({ kind: "ownerInput", label: "OWNER INPUT", body: "Owner topic input" });
    const marked = [owner, inbound, inboundByID, schedule, external, topicAgent, topicOwner];

    expect(marked.map((block) => receivedMessageKindForBlock(block, recipient))).toEqual([
      "owner", "agent", "agent", "schedule", "external", "agent", "owner",
    ]);
    const markers = marked.map((block) => receivedMessageMarkerForBlock(block, recipient));
    expect(new Set(markers.map((marker) => marker?.colorClass)).size).toBe(4);
    expect(markers[0]).toMatchObject({ id: blockStableID(owner), label: "Owner message: Owner task" });
    expect(markers[3]).toMatchObject({ label: "Scheduled task: Daily review" });
    expect(markers[5]).toMatchObject({ label: "Internal Agent Message: Topic request" });
  });

  it("does not mark outbound, other-recipient, trigger, context-only, or trajectory blocks", () => {
    const excluded: Block[] = [
      agentMessage({ id: "msg-outbound", from: recipient.name, to: "agent-other" }),
      agentMessage({ id: "msg-other", from: "agent-sender", to: "agent-other" }),
      {
        kind: "externalTrigger",
        id: "trigger-1",
        triggerId: "trigger-1",
        ts: "2026-08-16T01:00:03Z",
        provider: "github",
        connectionId: "connection-1",
        resourceKind: "pull-request",
        subjectKey: "owner/repo#1",
        event: "merged",
        eventKey: "event-1",
        summary: "summary",
        resumeInstruction: "resume",
        instruction: "instruction",
        raw: "",
      },
      topicContext({ kind: "text", label: "TOPIC WORK", body: "Context only" }),
      topicContext({ kind: "agentMessage", label: "REQ", body: "For someone else", from: "lead", to: "agent-other", variant: "req" }),
      { kind: "agent", id: "answer-1", ts: "2026-08-16T01:00:04Z", text: "answer", streaming: false },
      { kind: "think", id: "think-1", ts: "2026-08-16T01:00:05Z", text: "thinking", done: true },
      { kind: "command", id: "command-1", ts: "2026-08-16T01:00:06Z", command: "pwd", status: "completed", exitCode: 0, durationMs: 1, output: "" },
      { kind: "file", id: "file-1", ts: "2026-08-16T01:00:07Z", status: "completed", changes: [] },
      { kind: "image", id: "image-1", ts: "2026-08-16T01:00:08Z", data: "data:image/png;base64,AA==" },
      { kind: "artifact", id: "artifact-1", ts: "2026-08-16T01:00:09Z", artifact: { name: "report.txt" } },
      { kind: "usage", id: "usage-1", ts: "2026-08-16T01:00:10Z", inputTokens: 1, cachedInputTokens: 0, outputTokens: 1, reasoningOutputTokens: 0, totalTokens: 2, calls: 1 },
      { kind: "sys", ts: "2026-08-16T01:00:11Z", cls: "dim", text: "system" },
      { kind: "raw", id: "raw-1", ts: "2026-08-16T01:00:12Z", type: "unknown", json: "{}" },
    ];

    expect(excluded.map((block) => receivedMessageMarkerForBlock(block, recipient))).toEqual(
      excluded.map(() => null),
    );
  });

  it("keeps received message IDs stable across reconcile and prepend windows", () => {
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
    const seedIDs = markerIDs(seed);
    const reconciled = reduceFeed(seed, { seq: 2, ts: "", type: "__history_reconcile__", data: { turns: [currentTurn] } });
    const prepended = reduceFeed(reconciled, {
      seq: 3,
      ts: "",
      type: "__history_prepend__",
      data: {
        offset: 1,
        turns: [{
          id: "turn-older",
          items: [
            { type: "user", id: "user-older", timestamp: "2026-08-16T00:58:00Z", text: "older task" },
            { type: "answer", id: "answer-older", timestamp: "2026-08-16T00:59:00Z", text: "older answer" },
          ],
        }],
      },
    });

    expect(seedIDs).toEqual(["user:user-current"]);
    expect(markerIDs(reconciled)).toEqual(seedIDs);
    expect(markerIDs(prepended)).toEqual(["user:user-older", ...seedIDs]);
    expect(seed.blocks.every((block) => !blockStableID(block).includes("h-"))).toBe(true);
  });

  it("keeps repeated live Owner messages distinct without losing replay stability", () => {
    const firstEvent = { seq: 1, ts: "2026-08-16T01:00:00Z", type: "loom/user-message", data: { text: "same task" } };
    const secondEvent = { seq: 2, ts: "2026-08-16T01:00:01Z", type: "loom/user-message", data: { text: "same task" } };
    const first = reduceFeed(emptyFeed, firstEvent);
    const both = reduceFeed(first, secondEvent);
    const replay = reduceFeed(emptyFeed, firstEvent);

    expect(markerIDs(both)[0]).not.toBe(markerIDs(both)[1]);
    expect(markerIDs(replay)[0]).toBe(markerIDs(first)[0]);
  });

  it("lays out a semantic timeline independently from scroll pixels", () => {
    expect(timelineMarkerPercent(0, 1)).toBe(50);
    expect(timelineMarkerPercent(0, 10)).toBe(6);
    expect(timelineMarkerPercent(9, 10)).toBe(94);

    const clusters = messageMarkerClusters(100, 28);
    expect(clusters).toHaveLength(25);
    expect(clusters[0]).toMatchObject({ startIndex: 0, endIndex: 3, count: 4 });
    expect(clusters.at(-1)).toMatchObject({ startIndex: 96, endIndex: 99, count: 4 });
    expect(clusters.reduce((total, cluster) => total + cluster.count, 0)).toBe(100);
    expect(clusters.every((cluster) => cluster.position >= 6 && cluster.position <= 94)).toBe(true);
  });

  it("opens a bounded seven-message lens around the selected timeline area", () => {
    expect(messageMarkerLensRange(0, 0)).toEqual({ start: 0, end: 0 });
    expect(messageMarkerLensRange(100, 0)).toEqual({ start: 0, end: 7 });
    expect(messageMarkerLensRange(100, 50)).toEqual({ start: 47, end: 54 });
    expect(messageMarkerLensRange(100, 99)).toEqual({ start: 93, end: 100 });
  });
});
