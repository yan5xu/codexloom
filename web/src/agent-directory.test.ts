import { describe, expect, it } from "vitest";
import { filterAgentDirectory, sortAgentDirectory } from "./agent-directory";
import type { Agent } from "./types";

function agent(id: string, name: string, cwd = `/workspace/${name}`): Agent {
  return {
    id,
    name,
    cwd,
    threadId: `thread-${id}`,
    sandbox: "workspace-write",
    approvalPolicy: "on-request",
    status: "idle",
    currentTask: "",
    currentTurnId: "",
    lastError: "",
    createdAt: "2026-08-01T00:00:00Z",
    updatedAt: "2026-08-01T00:00:00Z",
    processAlive: true,
    pendingApprovals: [],
    lastSeq: 0,
  };
}

describe("agent directory", () => {
  it("sorts names case-insensitively with numeric order and an ID tie-breaker", () => {
    const input = [
      agent("z", "zulu"),
      agent("alpha-b", "Alpha"),
      agent("ten", "agent-10"),
      agent("two", "agent-2"),
      agent("alpha-a", "alpha"),
    ];

    expect(sortAgentDirectory(input).map((item) => item.id)).toEqual(["two", "ten", "alpha-a", "alpha-b", "z"]);
    expect(input.map((item) => item.id)).toEqual(["z", "alpha-b", "ten", "two", "alpha-a"]);
  });

  it("filters the stable directory by Agent name without broad workspace matches", () => {
    const input = [
      agent("writer", "cici-writer", "/workspace/content"),
      agent("backend", "parall-server-dev", "/workspace/backend/gamma"),
      agent("frontend", "loom-frontend", "/workspace/frontend"),
    ];

    expect(filterAgentDirectory(input, "LOOM").map((item) => item.id)).toEqual(["frontend"]);
    expect(filterAgentDirectory(input, "server dev").map((item) => item.id)).toEqual(["backend"]);
    expect(filterAgentDirectory(input, "backend gamma")).toEqual([]);
    expect(filterAgentDirectory(input, "  ").map((item) => item.name)).toEqual(["cici-writer", "loom-frontend", "parall-server-dev"]);
  });

  it("keeps existing Agents in the same relative order when a new Agent is added", () => {
    const existing = [agent("z", "zulu"), agent("a", "alpha"), agent("m", "middle")];
    const before = sortAgentDirectory(existing).map((item) => item.id);
    const after = sortAgentDirectory([...existing, agent("b", "beta")])
      .filter((item) => item.id !== "b")
      .map((item) => item.id);

    expect(after).toEqual(before);
  });
});
