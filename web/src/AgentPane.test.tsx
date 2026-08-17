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

  it("renders only received message markers and jumps by stable message identity", async () => {
    const extraOwnerItems = Array.from({ length: 8 }, (_, index) => ({
      type: "user",
      id: `owner-extra-${index}`,
      timestamp: `2026-08-16T01:01:${String(index).padStart(2, "0")}Z`,
      text: `loaded owner message ${index + 1}`,
    }));
    const history = {
      total: 1,
      turns: [{
        id: "turn-markers",
        items: [
          { type: "user", id: "user-marker", timestamp: "2026-08-16T01:00:00Z", text: "loaded task" },
          {
            type: "user",
            id: "agent-message-item",
            timestamp: "2026-08-16T01:00:01Z",
            text: `<agent_message version="1" id="msg-inbound" response="required" status="open">
  <from>loom-product</from><to>agent-scroll</to><subject>Inbound work</subject><body>body</body>
</agent_message>`,
          },
          {
            type: "user",
            id: "schedule-item",
            timestamp: "2026-08-16T01:00:02Z",
            text: `<agent_message version="1" id="msg-schedule" response="none" status="closed">
  <from>scheduler</from><to>agent-scroll</to><subject>Daily review</subject><body>body</body>
</agent_message>`,
          },
          {
            type: "user",
            id: "external-item",
            timestamp: "2026-08-16T01:00:03Z",
            text: `<inbox_message version="1" id="imsg-inbound" inbox_item_id="inbox-1" expectation="optional">
  <origin provider="parall" address_id="address-1" />
  <sender id="sender-1">External sender</sender><conversation id="conversation-1" type="group" />
  <reply_policy>none</reply_policy><body>External body</body>
</inbox_message>`,
          },
          ...extraOwnerItems,
          {
            type: "user",
            id: "outbound-item",
            timestamp: "2026-08-16T01:00:04Z",
            text: `<agent_message version="1" id="msg-outbound" response="none" status="closed">
  <from>agent-scroll</from><to>agent-other</to><subject>Outbound work</subject><body>body</body>
</agent_message>`,
          },
          { type: "answer", id: "answer-marker", timestamp: "2026-08-16T01:00:05Z", text: "loaded answer" },
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

    virtualizerHarness.instance.getTotalSize.mockReturnValue(history.turns[0].items.length * 100);
    virtualizerHarness.instance.getVirtualItems.mockReturnValue(history.turns[0].items.map((item, index) => ({
      index,
      key: item.id,
      start: index * 100,
      size: 100,
      end: (index + 1) * 100,
      lane: 0,
    })));

    render(<AgentPane {...props} active />);
    const timeline = await screen.findByTestId("message-timeline");
    const markers = timeline.querySelectorAll("[data-message-marker-id]");
    expect(markers).toHaveLength(12);
    expect(Array.from(markers).map((marker) => marker.getAttribute("data-message-marker-kind"))).toEqual([
      "owner", "agent", "schedule", "external", ...Array.from({ length: 8 }, () => "owner"),
    ]);
    vi.spyOn(timeline, "getBoundingClientRect").mockReturnValue({
      top: 100,
      bottom: 500,
      left: 0,
      right: 32,
      width: 32,
      height: 400,
      x: 0,
      y: 100,
      toJSON: () => ({}),
    });
    fireEvent.mouseMove(timeline, { clientY: 300 });
    const lens = screen.getByTestId("message-timeline-lens");
    expect(lens).toHaveTextContent("Received messages");
    expect(lens).toHaveTextContent("Focused 7 of 12 · scroll for all loaded");
    const lensItems = Array.from(lens.querySelectorAll<HTMLElement>("[data-message-timeline-lens-id]"));
    expect(lensItems).toHaveLength(12);
    const lensIDs = lensItems.map((item) => item.dataset.messageTimelineLensId);
    const lensList = screen.getByTestId("message-timeline-lens-list");
    lensList.scrollTop = 120;
    fireEvent.scroll(lensList);
    fireEvent.mouseEnter(lensItems[10]);
    expect(Array.from(lens.querySelectorAll<HTMLElement>("[data-message-timeline-lens-id]")).map((item) => item.dataset.messageTimelineLensId)).toEqual(lensIDs);
    expect(lens).toHaveTextContent("Focused 7 of 12 · scroll for all loaded");
    expect(lensList.scrollTop).toBe(120);
    fireEvent.mouseLeave(timeline);
    expect(screen.queryByTestId("message-timeline-lens")).toBeNull();

    const timelineToggle = screen.getByRole("button", { name: /Open received message timeline, 12 messages/ });
    fireEvent.mouseEnter(timelineToggle);
    expect(screen.getByTestId("message-timeline-lens")).toBeInTheDocument();
    fireEvent.click(timelineToggle);
    expect(screen.getByTestId("message-timeline-lens")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Close received message timeline" }));
    expect(screen.queryByTestId("message-timeline-lens")).toBeNull();

    virtualizerHarness.instance.scrollToIndex.mockClear();
    fireEvent.mouseEnter(markers[0]);
    const farMessage = screen.getByTestId("message-timeline-lens").querySelectorAll<HTMLElement>("[data-message-timeline-lens-id]")[10];
    fireEvent.click(farMessage);
    expect(virtualizerHarness.instance.scrollToIndex).toHaveBeenCalledWith(10, { align: "center" });

    virtualizerHarness.instance.scrollToIndex.mockClear();
    fireEvent.click(markers[1]);
    expect(virtualizerHarness.instance.scrollToIndex).toHaveBeenCalledWith(1, { align: "center" });
    await waitFor(() => expect(screen.getByTestId("message-timeline").parentElement?.querySelector("[data-message-highlighted='true']")).toHaveAttribute("data-message-id", "agentMessage:msg-inbound"));
    expect(timeline.parentElement?.querySelector("[data-message-id='agentMessage:msg-outbound']")).not.toHaveAttribute("data-message-kind");
    expect(timeline.parentElement?.querySelector("[data-message-id='agent:answer-marker']")).not.toHaveAttribute("data-message-kind");
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
