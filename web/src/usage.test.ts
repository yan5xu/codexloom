import { describe, expect, it } from "vitest";
import type { TokenUsage, TokenUsageOverview } from "./types";
import {
  buildCapacityHash,
  buildUsageHash,
  dateSpanDays,
  readCapacityLocation,
  reanchorUsageRange,
  readUsageLocation,
  resolveUsageScope,
  shiftUsageRange,
  usageAPIQuery,
  usageRangeEndingOn,
} from "./usage";

const tokens = (totalTokens: number): TokenUsage => ({
  inputTokens: totalTokens,
  cachedInputTokens: 0,
  outputTokens: 0,
  reasoningOutputTokens: 0,
  totalTokens,
  calls: totalTokens ? 1 : 0,
});

describe("Usage location and scope", () => {
  it("round-trips an explicit calendar range and Agent through the URL", () => {
    const range = usageRangeEndingOn("7d", "2026-07-15", "Asia/Shanghai");
    const hash = buildUsageHash(range, "agent one");
    expect(hash).toBe("#usage?mode=7d&from=2026-07-09&to=2026-07-15&tz=Asia%2FShanghai&agent=agent+one");
    expect(readUsageLocation(hash)).toEqual({ ...range, agentId: "agent one" });
    expect(usageAPIQuery(range)).toBe("from=2026-07-09&to=2026-07-15&tz=Asia%2FShanghai");
  });

  it("moves preset and custom windows without changing their calendar length", () => {
    const preset = usageRangeEndingOn("7d", "2026-07-15", "Asia/Shanghai");
    expect(shiftUsageRange(preset, -1)).toEqual(usageRangeEndingOn("7d", "2026-07-08", "Asia/Shanghai"));
    const custom = { mode: "custom" as const, from: "2026-07-02", to: "2026-07-05", timezone: "Asia/Shanghai" };
    expect(shiftUsageRange(custom, -1)).toEqual({ ...custom, from: "2026-06-28", to: "2026-07-01" });
    expect(reanchorUsageRange(custom, "2026-07-15")).toEqual({ ...custom, from: "2026-07-12", to: "2026-07-15" });
    expect(dateSpanDays(custom.from, custom.to)).toBe(4);
  });

  it("round-trips an independent Capacity calendar range", () => {
    const range = usageRangeEndingOn("day", "2026-07-12", "Asia/Shanghai");
    expect(buildCapacityHash(range, "agent one")).toBe("#capacity?mode=day&from=2026-07-12&to=2026-07-12&tz=Asia%2FShanghai&agent=agent+one");
    expect(readCapacityLocation("#capacity?mode=day&from=2026-07-12&to=2026-07-12&tz=Asia%2FShanghai")).toEqual({ ...range, agentId: null });
    expect(readCapacityLocation("#capacity?days=30").mode).toBe("30d");
  });

  it("uses the selected Agent series and previous-period usage", () => {
    const overview = {
      days: 7,
      since: "2026-07-09",
      through: "2026-07-15",
      timezone: "Asia/Shanghai",
      generatedAt: "",
      live: false,
      trackedAgents: 1,
      lifetime: tokens(900),
      period: tokens(700),
      previous: tokens(600),
      today: tokens(100),
      daily: [{ date: "2026-07-15", usage: tokens(100) }],
      models: [],
      agents: [{
        agentId: "a1",
        agentName: "alpha",
        status: "idle",
        available: true,
        lifetime: tokens(400),
        period: tokens(300),
        previous: tokens(250),
        today: tokens(40),
        latestCall: tokens(20),
        cacheHitPercent: 0,
        context: { inputTokens: 20, windowTokens: 100, usedPercent: 20 },
        daily: [{ date: "2026-07-15", usage: tokens(40) }],
        models: [],
      }],
    } satisfies TokenUsageOverview;
    const scope = resolveUsageScope(overview, "a1");
    expect(scope.agent?.agentName).toBe("alpha");
    expect(scope.period.totalTokens).toBe(300);
    expect(scope.previous.totalTokens).toBe(250);
    expect(scope.daily[0].usage.totalTokens).toBe(40);
  });
});
