import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { copyText } from "./clipboard";

beforeEach(() => {
  Object.defineProperty(document, "execCommand", {
    configurable: true,
    value: vi.fn(),
  });
});

afterEach(() => {
  vi.restoreAllMocks();
  Reflect.deleteProperty(document, "execCommand");
});

describe("copyText", () => {
  it("uses the Clipboard API when it succeeds", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText },
    });
    const execCommand = vi.mocked(document.execCommand).mockReturnValue(true);

    await expect(copyText("reply")).resolves.toBe(true);
    expect(writeText).toHaveBeenCalledWith("reply");
    expect(execCommand).not.toHaveBeenCalled();
  });

  it("falls back to a selected textarea when Clipboard API rejects", async () => {
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText: vi.fn().mockRejectedValue(new DOMException("Document is not focused", "NotAllowedError")) },
    });
    const execCommand = vi.mocked(document.execCommand).mockImplementation((command) => {
      expect(command).toBe("copy");
      expect(document.activeElement).toBeInstanceOf(HTMLTextAreaElement);
      expect((document.activeElement as HTMLTextAreaElement).value).toBe("reply");
      return true;
    });

    await expect(copyText("reply")).resolves.toBe(true);
    expect(execCommand).toHaveBeenCalledOnce();
    expect(document.querySelector("textarea[aria-hidden='true']")).not.toBeInTheDocument();
  });

  it("reports failure instead of claiming the text was copied", async () => {
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: undefined,
    });
    vi.mocked(document.execCommand).mockReturnValue(false);

    await expect(copyText("reply")).resolves.toBe(false);
  });
});
