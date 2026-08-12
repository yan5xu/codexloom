import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { localPathFromHref, MarkdownContent } from "./markdown";

describe("MarkdownContent links", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("recognizes local absolute paths and file URLs", () => {
    expect(localPathFromHref("/Users/james/project/plan.md:12")).toBe("/Users/james/project/plan.md:12");
    expect(localPathFromHref("file:///Users/james/My%20Project/plan.md")).toBe("/Users/james/My Project/plan.md");
    expect(localPathFromHref("https://example.com/plan.md")).toBeNull();
    expect(localPathFromHref("/api/agents/a/artifacts/1")).toBeNull();
  });

  it("opens a local Markdown link through the localhost file endpoint", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ opened: true }), {
      status: 200,
      headers: { "content-type": "application/json" },
    }));
    vi.stubGlobal("fetch", fetchMock);

    render(<MarkdownContent content="[全季规划](/Users/james/project/season-plan.md:1)" />);
    fireEvent.click(screen.getByRole("button", { name: "全季规划" }));
    expect(screen.getByRole("dialog")).toHaveTextContent("打开本地文件？");
    fireEvent.click(screen.getByRole("button", { name: "打开文件" }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
      "/api/files/open",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify({ path: "/Users/james/project/season-plan.md:1" }),
      }),
    ));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("keeps the safety confirmation for external websites", () => {
    const open = vi.spyOn(window, "open").mockImplementation(() => null);
    render(<MarkdownContent content="[官网](https://example.com/docs)" />);
    fireEvent.click(screen.getByRole("button", { name: "官网" }));
    expect(screen.getByRole("dialog")).toHaveTextContent("打开外部链接？");
    fireEvent.click(screen.getByRole("button", { name: "打开链接" }));
    expect(open).toHaveBeenCalledWith("https://example.com/docs", "_blank", "noreferrer");
  });
});
