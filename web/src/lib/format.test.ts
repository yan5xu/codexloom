import { describe, expect, it } from "vitest";
import { localTimeWithSeconds } from "./format";

describe("localTimeWithSeconds", () => {
  it("converts UTC timestamps into the requested local timezone", () => {
    expect(localTimeWithSeconds("2026-07-15T01:23:45Z", "Asia/Shanghai")).toBe("09:23:45");
  });

  it("keeps a useful fallback for malformed rollout timestamps", () => {
    expect(localTimeWithSeconds("2026-07-15 not-a-time")).toBe("not-a-ti");
  });
});
