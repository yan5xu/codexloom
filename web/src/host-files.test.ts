import { describe, expect, it } from "vitest";
import {
  absoluteHostPathFromHref,
  fetchHostTextPreview,
  findExplicitHostPaths,
  hostFileContentURL,
  hostFilePreviewKind,
  tokenizeExplicitHostPaths,
} from "./host-files";
import { afterEach, vi } from "vitest";

afterEach(() => vi.unstubAllGlobals());

describe("absoluteHostPathFromHref", () => {
  it.each([
    ["/tmp/notes.md", "/tmp/notes.md"],
    ["//other-host/notes.md", null],
    ["file:///tmp/notes%20copy.md", "/tmp/notes copy.md"],
    ["file://localhost/tmp/notes.md", "/tmp/notes.md"],
  ])("accepts %s", (href, expected) => {
    expect(absoluteHostPathFromHref(href)).toBe(expected);
  });

  it.each(["notes.md", "./notes.md", "../notes.md", "https://example.com/a.txt", "file://remote/a.txt"])(
    "rejects unqualified path or URL %s",
    (href) => expect(absoluteHostPathFromHref(href)).toBeNull(),
  );
});

describe("findExplicitHostPaths", () => {
  it("recognizes only explicitly marked absolute paths", () => {
    expect(findExplicitHostPaths("Open /tmp/plain.txt or `./relative.txt` first.")).toEqual([]);
    expect(findExplicitHostPaths("Open `/tmp/notes.md`.")).toMatchObject([
      { path: "/tmp/notes.md", label: "/tmp/notes.md", source: "backtick" },
    ]);
    expect(findExplicitHostPaths("[notes](/tmp/notes.md)")).toMatchObject([
      { path: "/tmp/notes.md", label: "notes", source: "markdown" },
    ]);
    expect(findExplicitHostPaths("[notes](/tmp/notes.md \"read me\")")).toMatchObject([
      { path: "/tmp/notes.md", label: "notes", source: "markdown" },
    ]);
    expect(findExplicitHostPaths("file:///tmp/notes.md")).toMatchObject([
      { path: "/tmp/notes.md", source: "file-url" },
    ]);
    expect(tokenizeExplicitHostPaths("Download file:///tmp/notes.md.")).toEqual([
      { type: "text", value: "Download " },
      {
        type: "path",
        value: { path: "/tmp/notes.md", label: "file:///tmp/notes.md", source: "file-url", start: 9, end: 29 },
      },
      { type: "text", value: "." },
    ]);
  });

  it("does not emit a duplicate file URL for a Markdown link", () => {
    expect(findExplicitHostPaths("[notes](file:///tmp/notes.md)")).toHaveLength(1);
  });
});

describe("tokenizeExplicitHostPaths", () => {
  it("keeps surrounding text and makes the explicit marker addressable", () => {
    expect(tokenizeExplicitHostPaths("See `/tmp/a.txt` now.")).toEqual([
      { type: "text", value: "See " },
      {
        type: "path",
        value: { path: "/tmp/a.txt", label: "/tmp/a.txt", source: "backtick", start: 4, end: 16 },
      },
      { type: "text", value: " now." },
    ]);
  });
});

describe("Host file API helpers", () => {
  it("requests a bounded preview and preserves the truncation headers", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response("0123", {
      status: 200,
      headers: {
        "X-Codex-Loom-Preview-Limit": "4",
        "X-Codex-Loom-Preview-Truncated": "true",
      },
    }));
    vi.stubGlobal("fetch", fetchMock);

    await expect(fetchHostTextPreview("/tmp/large.log", 4)).resolves.toEqual({ text: "0123", limit: 4, truncated: true });
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/files/preview?path=%2Ftmp%2Flarge.log&maxBytes=4",
      expect.objectContaining({ credentials: "same-origin" }),
    );
  });

  it("uses the live content endpoint for preview and download without inventing a token", () => {
    expect(hostFileContentURL("/tmp/a file.bin", "preview")).toBe("/api/files/content?path=%2Ftmp%2Fa+file.bin&preview=1");
    expect(hostFileContentURL("/tmp/a file.bin", "download")).toBe("/api/files/content?path=%2Ftmp%2Fa+file.bin&download=1");
  });

  it.each([
    ["README.md", "", "markdown"],
    ["trace.log", "text/plain", "text"],
    ["photo.png", "image/png", "image"],
    ["clip.mp4", "", "video"],
    ["archive.bin", "application/octet-stream", "unknown"],
  ])("classifies %s as %s", (path, mimeType, expected) => {
    expect(hostFilePreviewKind({ path, mimeType })).toBe(expected);
  });
});
