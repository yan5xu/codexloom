import { describe, expect, it } from "vitest";
import { agentLabel } from "./agent-label";

describe("agentLabel", () => {
  it("prefers a Unicode display name", () => {
    expect(agentLabel({ name: "duanpian-gaibian", displayName: "短篇改编" })).toBe("短篇改编");
  });

  it("falls back to the stable internal identifier", () => {
    expect(agentLabel({ name: "research" })).toBe("research");
    expect(agentLabel({ name: "research", displayName: "  " })).toBe("research");
  });
});
