import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { BlockView } from "./Blocks";
import type { Block } from "./feed";

afterEach(cleanup);

function commandBlock(overrides: Partial<Extract<Block, { kind: "command" }>> = {}): Extract<Block, { kind: "command" }> {
  return {
    kind: "command",
    id: "cmd-test",
    command: "printf probe-ok && printf a-very-long-token-that-must-wrap",
    status: "completed",
    exitCode: 0,
    durationMs: 12,
    output: "probe-ok",
    ...overrides,
  };
}

describe("command block presentation", () => {
  it("makes description the complete summary and keeps raw command in request details", () => {
    const description = "Run the isolated command probe and preserve the natural-language explanation.";
    const command = "printf probe-ok && printf a-very-long-token-that-must-wrap";
    const { container } = render(<BlockView block={commandBlock({ description, command })} />);
    const details = container.querySelector("details") as HTMLDetailsElement;
    const summary = details.querySelector("summary") as HTMLElement;

    expect(summary.textContent).toContain(description);
    expect(summary.textContent).not.toContain(command);
    expect(container.querySelectorAll("summary")).toHaveLength(1);
    expect(details.open).toBe(false);

    fireEvent.click(summary);

    expect(details.open).toBe(true);
    expect(details.querySelector("pre")?.textContent).toBe(command);
    expect(screen.getAllByText(description, { exact: true })).toHaveLength(1);
  });

  it("uses the raw command as the legacy summary when description is absent", () => {
    const command = "printf legacy";
    const { container } = render(<BlockView block={commandBlock({ command, description: undefined })} />);
    const summary = container.querySelector("summary") as HTMLElement;

    expect(summary.textContent).toContain(command);
    expect(summary.querySelector(".font-mono")).toBeTruthy();
  });

  it("wraps long description and continuous tokens without hiding the summary text", () => {
    const description = `Explain the command safely ${"x".repeat(180)}`;
    const { container } = render(<BlockView block={commandBlock({ description })} />);
    const summaryText = container.querySelector("summary > span") as HTMLElement;

    expect(summaryText.textContent).toBe(description);
    expect(summaryText).toHaveClass("break-words", "whitespace-pre-wrap");
  });
});
