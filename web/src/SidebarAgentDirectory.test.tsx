import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { SidebarAgentDirectory } from "./SidebarAgentDirectory";
import type { Agent, HumanRequest, InboxEntry } from "./types";

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

function humanRequest(agentId: string, agentName: string): HumanRequest {
  return {
    id: `request-${agentId}`,
    agentId,
    agentName,
    expectation: "required",
    question: "Choose a path",
    state: "open",
    deliveryStatus: "waiting",
    createdAt: "2026-08-01T00:00:00Z",
    updatedAt: "2026-08-01T00:00:00Z",
  };
}

function inboxEntry(agentId: string, id: string, state: InboxEntry["item"]["state"] = "queued") {
  return {
    item: { id, agentId, state, messageId: `message-${id}`, addressId: "address", attemptCount: 0, createdAt: "2026-08-01T00:00:00Z", updatedAt: "2026-08-01T00:00:00Z" },
    agentName: agentId,
  } as InboxEntry;
}

const noOp = () => {};

function directoryProps(agents: Agent[], overrides: Partial<Parameters<typeof SidebarAgentDirectory>[0]> = {}) {
  return {
    agents,
    currentId: null,
    sidebarOpen: false,
    humanRequests: [],
    pendingWork: [],
    unseenIds: new Set<string>(),
    archivingIds: new Set<string>(),
    onSelectAgent: noOp,
    onSelectRequest: noOp,
    onArchiveAgent: noOp,
    ...overrides,
  };
}

describe("SidebarAgentDirectory", () => {
  const scrollIntoView = vi.fn();

  beforeEach(() => {
    Object.defineProperty(HTMLElement.prototype, "scrollIntoView", { configurable: true, value: scrollIntoView });
    scrollIntoView.mockClear();
  });

  afterEach(cleanup);

  it("renders All Agents in a stable alphabetical order", () => {
    const { container } = render(<SidebarAgentDirectory {...directoryProps([
      agent("z", "zulu"),
      agent("a", "Alpha"),
      agent("m", "middle"),
    ])} />);

    const entries = [...container.querySelectorAll("[data-agent-directory-entry]")];
    expect(entries.map((entry) => entry.getAttribute("data-agent-directory-entry"))).toEqual(["a", "m", "z"]);
  });

  it("filters by name and restores the directory when cleared", () => {
    render(<SidebarAgentDirectory {...directoryProps([
      agent("writer", "cici-writer", "/workspace/content"),
      agent("backend", "parall-server-dev", "/workspace/backend/gamma"),
      agent("frontend", "loom-frontend", "/workspace/frontend"),
    ])} />);

    const search = screen.getByRole("searchbox", { name: "Search agents by name" });
    fireEvent.change(search, { target: { value: "server dev" } });
    expect(screen.getByText("1/3")).toBeInTheDocument();
    expect(document.querySelector("[data-agent-directory-entry='backend']")).toBeInTheDocument();
    expect(document.querySelector("[data-agent-directory-entry='writer']")).not.toBeInTheDocument();

    fireEvent.change(search, { target: { value: "missing" } });
    expect(screen.getByText("No agents match “missing”.")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Clear search" }));
    expect(document.querySelectorAll("[data-agent-directory-entry]")).toHaveLength(3);
  });

  it("keeps Human Request and Agent Inbox counts visually and accessibly distinct", () => {
    render(<SidebarAgentDirectory {...directoryProps(
      [agent("alpha", "alpha")],
      {
        humanRequests: [humanRequest("alpha", "alpha")],
        pendingWork: [inboxEntry("alpha", "one"), inboxEntry("alpha", "two"), inboxEntry("alpha", "done", "handled")],
      },
    )} />);

    expect(screen.getByTitle("2 Agent Inbox items")).toHaveClass("bg-muted");
    expect(screen.getByText("2 Agent Inbox items")).toHaveClass("sr-only");
    expect(screen.getByRole("button", { name: "Open 1 human request from alpha" })).toHaveClass("bg-warning/15");
  });

  it("clears filtering and scrolls the current Agent into view on request", async () => {
    render(<SidebarAgentDirectory {...directoryProps(
      [agent("alpha", "alpha"), agent("zeta", "zeta")],
      { currentId: "alpha" },
    )} />);

    const search = screen.getByRole("searchbox", { name: "Search agents by name" });
    fireEvent.change(search, { target: { value: "zeta" } });
    expect(document.querySelector("[data-agent-directory-entry='alpha']")).not.toBeInTheDocument();
    scrollIntoView.mockClear();

    fireEvent.click(screen.getByRole("button", { name: "Locate current agent alpha" }));
    expect(search).toHaveValue("");
    expect(document.querySelector("[data-agent-directory-entry='alpha']")).toBeInTheDocument();
    await waitFor(() => expect(scrollIntoView).toHaveBeenCalledWith({ block: "nearest" }));
  });

  it("repositions the current Agent when the mobile sidebar reopens", () => {
    const agents = [agent("alpha", "alpha"), agent("zeta", "zeta")];
    const { rerender } = render(<SidebarAgentDirectory {...directoryProps(agents, { currentId: "zeta", sidebarOpen: false })} />);
    scrollIntoView.mockClear();

    rerender(<SidebarAgentDirectory {...directoryProps(agents, { currentId: "zeta", sidebarOpen: true })} />);
    expect(scrollIntoView).toHaveBeenCalledWith({ block: "nearest" });
  });
});
