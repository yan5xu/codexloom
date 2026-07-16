import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { UsageBarTooltip } from "./UsageBarTooltip";

describe("UsageBarTooltip", () => {
  it("shows exact token and call values", () => {
    render(<UsageBarTooltip day={{
      date: "2026-07-15",
      usage: {
        inputTokens: 10_000,
        cachedInputTokens: 9_000,
        outputTokens: 2_345,
        reasoningOutputTokens: 345,
        totalTokens: 12_345,
        calls: 3,
      },
    }} />);
    expect(screen.getByRole("tooltip")).toHaveTextContent("2026-07-15");
    expect(screen.getByText("12,345")).toBeInTheDocument();
    expect(screen.getByText("10,000")).toBeInTheDocument();
    expect(screen.getByText("9,000")).toBeInTheDocument();
    expect(screen.getByText("2,345")).toBeInTheDocument();
    expect(screen.getByText("3")).toBeInTheDocument();
  });
});
