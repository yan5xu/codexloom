import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { HostFilesPane } from "./HostFilesPane";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

function jsonResponse(value: unknown, status = 200, headers: Record<string, string> = {}) {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "content-type": "application/json", ...headers },
  });
}

const rootDirectory = {
  path: "/tmp/issue69-file-fixture",
  name: "issue69-file-fixture",
  kind: "directory" as const,
  size: 0,
  modifiedAt: "2026-08-14T08:00:00Z",
  mode: "drwx------",
  readable: true,
  entries: [
    { path: "/tmp/issue69-file-fixture/.hidden", name: ".hidden", kind: "file" as const, size: 7, modifiedAt: "2026-08-14T08:01:00Z", mode: "-rw-------", readable: true, mimeType: "text/plain" },
    { path: "/tmp/issue69-file-fixture/nested", name: "nested", kind: "directory" as const, size: 0, modifiedAt: "2026-08-14T08:02:00Z", mode: "drwx------", readable: true },
    { path: "/tmp/issue69-file-fixture/probe.txt", name: "probe.txt", kind: "file" as const, size: 10, modifiedAt: "2026-08-14T08:03:00Z", mode: "-rw-------", readable: true, mimeType: "text/plain" },
    { path: "/tmp/issue69-file-fixture/unknown.data", name: "unknown.data", kind: "file" as const, size: 4, modifiedAt: "2026-08-14T08:04:00Z", mode: "-rw-------", readable: true, mimeType: "application/octet-stream" },
  ],
};

describe("HostFilesPane", () => {
  it("loads a directory only when the pane is selected, includes hidden files, navigates and refreshes", async () => {
    const fetchMock = vi.fn().mockImplementation((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("path=%2Ftmp%2Fissue69-file-fixture%2Fnested")) {
        return Promise.resolve(jsonResponse({ ...rootDirectory, path: "/tmp/issue69-file-fixture/nested", name: "nested", entries: [] }));
      }
      return Promise.resolve(jsonResponse(rootDirectory));
    });
    vi.stubGlobal("fetch", fetchMock);

    render(<HostFilesPane initialPath="/tmp/issue69-file-fixture" />);
    expect(await screen.findByText(".hidden")).toBeInTheDocument();
    expect(fetchMock.mock.calls.some(([input]) => String(input).includes("/api/files?path=%2Ftmp%2Fissue69-file-fixture"))).toBe(true);
    expect(screen.getByText(/hidden files included/)).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Open directory: nested" }));
    expect(await screen.findByText("0 entries · hidden files included")).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/files?path=%2Ftmp%2Fissue69-file-fixture%2Fnested",
      expect.objectContaining({ credentials: "same-origin" }),
    );

    const callsBeforeRefresh = fetchMock.mock.calls.length;
    fireEvent.click(screen.getByRole("button", { name: "Refresh host files" }));
    await waitFor(() => expect(fetchMock.mock.calls.length).toBeGreaterThan(callsBeforeRefresh));
  });

  it("does not read file content until a file is clicked, then shows a bounded preview and live download", async () => {
    const fetchMock = vi.fn().mockImplementation((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.startsWith("/api/files/preview?")) {
        return Promise.resolve(new Response("probe-success\n", {
          status: 200,
          headers: {
            "X-Codex-Loom-Preview-Limit": "1048576",
            "X-Codex-Loom-Preview-Truncated": "false",
          },
        }));
      }
      return Promise.resolve(jsonResponse(rootDirectory));
    });
    vi.stubGlobal("fetch", fetchMock);

    render(<HostFilesPane initialPath="/tmp/issue69-file-fixture" />);
    await screen.findByText("probe.txt");
    expect(fetchMock.mock.calls.some(([input]) => String(input).includes("/preview"))).toBe(false);

    fireEvent.click(screen.getAllByRole("button", { name: "Preview file: probe.txt" })[0]);
    expect(await screen.findByRole("dialog")).toBeInTheDocument();
    expect(await screen.findByText("probe-success")).toBeInTheDocument();
    expect(fetchMock.mock.calls.some(([input]) => String(input) === "/api/files/preview?path=%2Ftmp%2Fissue69-file-fixture%2Fprobe.txt&maxBytes=1048576")).toBe(true);
    expect(screen.getByRole("link", { name: "Download" })).toHaveAttribute("href", "/api/files/content?path=%2Ftmp%2Fissue69-file-fixture%2Fprobe.txt&download=1");

    fireEvent.click(screen.getByRole("button", { name: "Close" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("keeps unknown files metadata-only and exposes a download without trying to preview them", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ ...rootDirectory.entries[3], path: "/tmp/issue69-file-fixture/unknown.data" }));
    vi.stubGlobal("fetch", fetchMock);

    render(<HostFilesPane initialPath="/tmp/issue69-file-fixture/unknown.data" />);
    expect(await screen.findByRole("dialog")).toBeInTheDocument();
    expect(screen.getByText("metadata only")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Download" })).toHaveAttribute("href", "/api/files/content?path=%2Ftmp%2Fissue69-file-fixture%2Funknown.data&download=1");
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(fetchMock.mock.calls.some(([input]) => String(input).includes("/preview"))).toBe(false);
  });

  it("shows the API error without substituting an empty directory", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ error: { code: "not_found", message: "path does not exist" } }, 404));
    vi.stubGlobal("fetch", fetchMock);

    render(<HostFilesPane initialPath="/tmp/missing" />);
    expect(await screen.findByRole("alert")).toHaveTextContent("path does not exist (not_found)");
    expect(screen.queryByText("hidden files included")).not.toBeInTheDocument();
  });
});
