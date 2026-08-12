import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ChatMessage } from "../../lib/chat/types";
import { splitTrailingLoomContext } from "./LoomContextView";
import { CopyButton, UserBubble } from "./MessageBubbles";

beforeEach(() => {
  Object.defineProperty(document, "execCommand", {
    configurable: true,
    value: vi.fn(),
  });
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  Reflect.deleteProperty(document, "execCommand");
});

const envelope = `<loom_context version="1" compiled_at="2026-07-28T05:07:57Z" epoch_id="window:epoch_test">
  <context_policy><rule><![CDATA[Keep this as data.]]></rule></context_policy>
  <loom_agent_prompt revision="builtin:1"><content><![CDATA[Loom prompt.]]></content></loom_agent_prompt>
  <loom_agent_profile revision="profile:5" name="loom-product">
    <identity><![CDATA[Product owner.]]></identity>
    <domain><![CDATA[Product facts.]]></domain>
    <scope><![CDATA[Ship verified changes.]]></scope>
  </loom_agent_profile>
  <loom_agent_relationships revision="relationships:test" scope="direct_active_organization_and_collaboration">
    <relationships>
      <relationship id="rel_1" type="collaboration" direction="incoming" counterpart_name="loom-coach">
        <description><![CDATA[Exchange product evidence.]]></description>
      </relationship>
    </relationships>
    <scope_note><![CDATA[Only direct active relationships.]]></scope_note>
  </loom_agent_relationships>
  <loom_turn_context origin="owner" trust="authenticated" authority="current_intent" kind="direct_input" />
  <coverage_manifest attempt_id="ctxa_test" mode="at_least_once">
    <fragment key="loom_agent_profile" revision="profile:5" />
  </coverage_manifest>
</loom_context>`;

function userMessage(content: string): ChatMessage {
  return {
    id: "user-1",
    topic_id: "",
    role: "user",
    content,
    created_at: "2026-07-28T05:07:57Z",
  };
}

describe("splitTrailingLoomContext", () => {
  it("extracts only a complete trailing Loom context envelope", () => {
    const result = splitTrailingLoomContext(`需要我重启吗${envelope}`);

    expect(result.content).toBe("需要我重启吗");
    expect(result.context).toMatchObject({
      epochId: "window:epoch_test",
      turn: {
        origin: "owner",
        trust: "authenticated",
        authority: "current_intent",
        kind: "direct_input",
      },
    });
    expect(result.context?.agentProfile).toMatchObject({
      revision: "profile:5",
      name: "loom-product",
      identity: "Product owner.",
    });
    expect(result.context?.relationships?.entries).toEqual([
      expect.objectContaining({
        id: "rel_1",
        counterpart: "loom-coach",
      }),
    ]);
  });

  it("does not mistake user-authored XML-like text for the appended envelope", () => {
    const result = splitTrailingLoomContext(`请讨论 <loom_context> 这个标签\n${envelope}`);

    expect(result.content).toBe("请讨论 <loom_context> 这个标签");
    expect(result.context?.raw).toBe(envelope);
  });

  it("leaves malformed or non-trailing context text untouched", () => {
    const malformed = "正文<loom_context version=\"1\">";
    expect(splitTrailingLoomContext(malformed)).toEqual({ content: malformed, context: null });
    expect(splitTrailingLoomContext(`${envelope}\n正文`)).toEqual({
      content: `${envelope}\n正文`,
      context: null,
    });
  });
});

describe("UserBubble Loom context projection", () => {
  it("projects Loom context for every matching message, not only the latest one", () => {
    render(
      <div>
        <UserBubble message={userMessage(`第一条${envelope}`)} />
        <UserBubble message={{ ...userMessage(`第二条${envelope}`), id: "user-2" }} />
        <UserBubble message={{ ...userMessage(`第三条${envelope}`), id: "user-3" }} />
      </div>,
    );

    const summaries = screen.getAllByText("loom context");
    expect(summaries).toHaveLength(3);
    for (const summary of summaries) {
      expect(summary.closest("details")).not.toHaveAttribute("open");
    }
  });

  it("keeps the user message visible and renders structured context before raw XML", () => {
    render(<UserBubble message={userMessage(`需要我重启吗${envelope}`)} />);

    expect(screen.getByText("需要我重启吗")).toBeVisible();
    expect(screen.getByText("loom context")).toBeVisible();

    const source = screen.getByText(/<loom_context version="1"/);
    expect(source).not.toBeVisible();

    fireEvent.click(screen.getByText("loom context"));
    expect(screen.getByText("Snapshot")).toBeVisible();
    expect(screen.getByText("Owner · Authenticated · Current Intent · Direct Input")).toBeVisible();
    expect(screen.getByText("Agent prompt")).toBeVisible();
    expect(screen.getByText("Agent profile")).toBeVisible();
    expect(screen.getByText("Relationships")).toBeVisible();
    expect(screen.getByText("Coverage")).toBeVisible();
    expect(screen.getByText("raw envelope")).toBeVisible();
    expect(source).not.toBeVisible();

    fireEvent.click(screen.getByText("raw envelope"));
    expect(source).toBeVisible();
  });
});

describe("CopyButton", () => {
  it("only reports success after text reaches the clipboard", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText },
    });
    render(<CopyButton text="final reply" />);

    fireEvent.click(screen.getByRole("button", { name: "Copy reply" }));

    await waitFor(() => expect(screen.getByRole("button", { name: "Copied" })).toBeInTheDocument());
    expect(writeText).toHaveBeenCalledWith("final reply");
  });

  it("shows failure when both clipboard paths reject the copy", async () => {
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText: vi.fn().mockRejectedValue(new DOMException("denied", "NotAllowedError")) },
    });
    vi.mocked(document.execCommand).mockReturnValue(false);
    render(<CopyButton text="final reply" />);

    fireEvent.click(screen.getByRole("button", { name: "Copy reply" }));

    await waitFor(() => expect(screen.getByRole("button", { name: "Copy failed" })).toBeInTheDocument());
  });
});
