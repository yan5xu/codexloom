import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Agent } from "./types";

const virtualizerHarness = vi.hoisted(() => ({
  options: [] as Array<Record<string, unknown>>,
  instance: {
    getTotalSize: vi.fn(() => 0),
    getVirtualItems: vi.fn((): Array<{ index: number; key: string | number; start: number; size: number; end: number; lane: number }> => []),
    measureElement: vi.fn(),
    resizeItem: vi.fn(),
    scrollToIndex: vi.fn(),
  },
}));

vi.mock("@tanstack/react-virtual", () => ({
  useVirtualizer: (options: Record<string, unknown>) => {
    virtualizerHarness.options.push(options);
    return virtualizerHarness.instance;
  },
}));

import { AgentPane } from "./AgentPane";

const testAgent: Agent = {
  id: "agent-scroll",
  name: "agent-scroll",
  cwd: "/workspace/agent-scroll",
  threadId: "thread-scroll",
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

const noOp = () => {};
const props = {
  agent: testAgent,
  modelProviders: [],
  configRequestNonce: 0,
  pendingWork: [],
  humanRequests: [],
  onOpenPendingWork: noOp,
  onOpenHumanRequest: noOp,
  onHumanRequestChanged: noOp,
  onPendingWorkChanged: noOp,
  onOpenUsage: noOp,
  onTrackTopic: noOp,
  onError: noOp,
  onAgentUpdated: noOp,
};

describe("AgentPane", () => {
  beforeEach(() => {
    virtualizerHarness.options.length = 0;
    virtualizerHarness.instance.getTotalSize.mockReset().mockReturnValue(0);
    virtualizerHarness.instance.getVirtualItems.mockReset().mockReturnValue([]);
    virtualizerHarness.instance.scrollToIndex.mockReset();
    vi.stubGlobal("requestAnimationFrame", (callback: FrameRequestCallback) => window.setTimeout(() => callback(performance.now()), 0));
    vi.stubGlobal("cancelAnimationFrame", (id: number) => window.clearTimeout(id));
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof input === "string" ? input : input instanceof URL ? input.pathname : input.url;
      const body = url.includes("/thread/history")
        ? { total: 0, turns: [] }
        : url.endsWith("/profile")
          ? { profile: { identity: "", domain: "", scope: "", version: 1 } }
          : url.endsWith("/addresses")
            ? { addresses: [] }
            : url === "/api/integrations/connections"
              ? { connections: [] }
              : url.startsWith("/api/integrations/conversations")
                ? { memberships: [] }
                : init?.method === "PATCH" && url.endsWith("/config")
                  ? { agent: testAgent }
                  : { artifacts: [] };
      return new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });
    }));
    Object.defineProperty(document, "execCommand", {
      configurable: true,
      value: vi.fn().mockReturnValue(false),
    });
  });

  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
    Reflect.deleteProperty(navigator, "clipboard");
    Reflect.deleteProperty(document, "execCommand");
    delete window.codexLoom;
    delete window.codexHub;
  });

  it("supplies the pane's last scrollTop when its virtualizer is re-enabled", () => {
    const view = render(<AgentPane {...props} active />);
    const feed = view.container.querySelector("main .overflow-y-auto") as HTMLDivElement;
    Object.defineProperties(feed, {
      clientHeight: { configurable: true, value: 600 },
      scrollHeight: { configurable: true, value: 2_000 },
    });
    feed.scrollTop = 900;
    fireEvent.scroll(feed);

    view.rerender(<AgentPane {...props} active={false} />);
    view.rerender(<AgentPane {...props} active />);

    const latestOptions = virtualizerHarness.options.at(-1);
    expect(latestOptions?.enabled).toBe(true);
    expect(latestOptions?.initialOffset).toBeTypeOf("function");
    expect((latestOptions?.initialOffset as () => number)()).toBe(900);
  });

  it("renders loaded message markers and jumps by stable message identity", async () => {
    const history = {
      total: 1,
      turns: [{
        id: "turn-markers",
        items: [
          { type: "user", id: "user-marker", timestamp: "2026-08-16T01:00:00Z", text: "loaded task" },
          { type: "answer", id: "answer-marker", timestamp: "2026-08-16T01:00:01Z", text: "loaded answer" },
        ],
      }],
    };
    vi.mocked(fetch).mockImplementation(async (input, init) => {
      const url = typeof input === "string" ? input : input instanceof URL ? input.pathname : input.url;
      const body = url.includes("/thread/history")
        ? history
        : url.endsWith("/artifacts")
          ? { artifacts: [] }
          : url.endsWith("/profile")
            ? { profile: { identity: "", domain: "", scope: "", version: 1 } }
            : url.endsWith("/addresses")
              ? { addresses: [] }
              : url === "/api/integrations/connections"
                ? { connections: [] }
                : url.startsWith("/api/integrations/conversations")
                  ? { memberships: [] }
                  : init?.method === "PATCH" && url.endsWith("/config")
                    ? { agent: testAgent }
                    : {};
      return new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });
    });

    virtualizerHarness.instance.getTotalSize.mockReturnValue(200);
    virtualizerHarness.instance.getVirtualItems.mockReturnValue([
      { index: 0, key: "user:user-marker", start: 0, size: 100, end: 100, lane: 0 },
      { index: 1, key: "agent:answer-marker", start: 100, size: 100, end: 200, lane: 0 },
    ]);

    render(<AgentPane {...props} active />);
    const rail = await screen.findByTestId("message-marker-rail");
    const markers = rail.querySelectorAll("[data-message-marker-id]");
    expect(markers).toHaveLength(2);
    expect(markers[0]).toHaveAttribute("data-message-marker-kind", "user");
    expect(markers[1]).toHaveAttribute("data-message-marker-kind", "assistant");
    fireEvent.mouseEnter(markers[0]);
    const hoverCard = screen.getByTestId("message-marker-hover-card");
    expect(hoverCard).toHaveTextContent("User task");
    expect(hoverCard).toHaveTextContent("loaded task");
    expect(hoverCard).toHaveTextContent("Click to jump");
    fireEvent.mouseLeave(markers[0]);
    expect(screen.queryByTestId("message-marker-hover-card")).toBeNull();

    virtualizerHarness.instance.scrollToIndex.mockClear();
    fireEvent.click(screen.getByRole("button", { name: /Jump to Agent response/ }));
    expect(virtualizerHarness.instance.scrollToIndex).toHaveBeenCalledWith(1, { align: "center" });
    await waitFor(() => expect(screen.getByTestId("message-marker-rail").parentElement?.querySelector("[data-message-highlighted='true']")).toHaveAttribute("data-message-id", "agent:answer-marker"));
  });

  it("shows the configured working directory as read-only selectable text between Name and Provider", async () => {
    render(<AgentPane {...props} configRequestNonce={1} active />);

    const path = await screen.findByTestId("agent-working-directory");
    const name = screen.getByRole("textbox", { name: "Name" });
    const provider = screen.getByRole("combobox", { name: "Provider" });

    expect(path).toHaveTextContent(testAgent.cwd);
    expect(path.tagName).toBe("CODE");
    expect(path).toHaveClass("select-text", "break-all", "whitespace-pre-wrap");
    expect(path.closest("input, textarea, [contenteditable='true']")).toBeNull();
    expect(name.compareDocumentPosition(path) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(path.compareDocumentPosition(provider) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it("copies the exact working directory and reports success", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText },
    });
    render(<AgentPane {...props} configRequestNonce={1} active />);

    fireEvent.click(await screen.findByRole("button", { name: "Copy working directory" }));

    await waitFor(() => expect(screen.getByRole("button", { name: "Copied working directory" })).toBeInTheDocument());
    expect(writeText).toHaveBeenCalledWith(testAgent.cwd);
  });

  it("reports copy failure instead of claiming success", async () => {
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText: vi.fn().mockRejectedValue(new DOMException("denied", "NotAllowedError")) },
    });
    vi.mocked(document.execCommand).mockReturnValue(false);
    render(<AgentPane {...props} configRequestNonce={1} active />);

    fireEvent.click(await screen.findByRole("button", { name: "Copy working directory" }));

    await waitFor(() => expect(screen.getByRole("button", { name: "Copy working directory failed" })).toBeInTheDocument());
    expect(screen.queryByRole("button", { name: "Copied working directory" })).not.toBeInTheDocument();
  });

  it("updates the path and resets copy feedback when the Agent changes", async () => {
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText: vi.fn().mockResolvedValue(undefined) },
    });
    const view = render(<AgentPane {...props} configRequestNonce={1} active />);
    fireEvent.click(await screen.findByRole("button", { name: "Copy working directory" }));
    await screen.findByRole("button", { name: "Copied working directory" });

    const nextAgent = { ...testAgent, id: "agent-next", name: "agent-next", cwd: "/projects/next agent/with/a/very/long/project/path" };
    view.rerender(<AgentPane {...props} agent={nextAgent} configRequestNonce={1} active />);

    expect(await screen.findByTestId("agent-working-directory")).toHaveTextContent(nextAgent.cwd);
    expect(screen.queryByText(testAgent.cwd)).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Copy working directory" })).toBeInTheDocument();
  });

  it("keeps cwd out of the config save request", async () => {
    const fetchMock = vi.mocked(fetch);
    render(<AgentPane {...props} configRequestNonce={1} active />);

    fireEvent.click(await screen.findByRole("button", { name: "Save" }));

    await waitFor(() => {
      expect(fetchMock.mock.calls.some(([input, init]) => {
        const url = typeof input === "string" ? input : input instanceof URL ? input.pathname : input.url;
        return url.endsWith("/config") && init?.method === "PATCH";
      })).toBe(true);
    });
    const [, init] = fetchMock.mock.calls.find(([input, request]) => {
      const url = typeof input === "string" ? input : input instanceof URL ? input.pathname : input.url;
      return url.endsWith("/config") && request?.method === "PATCH";
    })!;
    const body = JSON.parse(String(init?.body));
    expect(body).toEqual({
      name: testAgent.name,
      model: "",
      effort: "",
      sandbox: testAgent.sandbox,
      approvalPolicy: testAgent.approvalPolicy,
    });
    expect(body).not.toHaveProperty("cwd");
  });
});
