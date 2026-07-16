import { describe, expect, it } from "vitest";
import { executionState, isAgentExecuting, isOwnerResultEvent, oldestWaitingMs } from "./product-state";
import type { Agent, InboxEntry } from "./types";

function agent(status: string, goalStatus?: Agent["goal"] extends infer G ? any : never): Agent {
  return {
    id: "a1", name: "research", cwd: "/tmp", threadId: "t1", sandbox: "danger-full-access",
    approvalPolicy: "never", status, currentTask: "", currentTurnId: "", lastError: "",
    createdAt: "2026-07-17T00:00:00Z", updatedAt: "2026-07-17T00:00:00Z", processAlive: true,
    pendingApprovals: [], lastSeq: 0,
    goal: goalStatus ? { threadId: "t1", objective: "Long goal", status: goalStatus, tokenBudget: null, tokensUsed: 0, timeUsedSeconds: 0, createdAt: 0, updatedAt: 0 } : undefined,
  };
}

describe("product state", () => {
  it("does not treat an active Goal as execution", () => {
    const value = agent("idle", "active");
    expect(executionState(value)).toBe("idle");
    expect(isAgentExecuting(value)).toBe(false);
  });

  it("only returns Owner-facing terminal events", () => {
    expect(isOwnerResultEvent({ type: "loom/turn-completed", data: { source: "owner" } })).toBe(true);
    expect(isOwnerResultEvent({ type: "loom/turn-interrupted", data: { source: "remote" } })).toBe(true);
    expect(isOwnerResultEvent({ type: "loom/turn-completed", data: { source: "internal" } })).toBe(false);
    expect(isOwnerResultEvent({ type: "item/completed", data: {} })).toBe(false);
  });

  it("reports the oldest active queue age", () => {
    const entries = [{ item: { createdAt: "2026-07-17T00:05:00Z" }, message: { receivedAt: "2026-07-17T00:04:00Z" } }] as InboxEntry[];
    expect(oldestWaitingMs(entries, Date.parse("2026-07-17T00:10:00Z"))).toBe(6 * 60 * 1000);
  });
});
