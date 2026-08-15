import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
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

function entryButtons(container: HTMLElement) {
  return within(container).getAllByRole("button", { name: /^(Open directory|Preview file):/ });
}

function editPath() {
  fireEvent.click(screen.getByRole("button", { name: "Edit absolute Host path" }));
  return screen.getByRole("textbox", { name: "Absolute Host path" });
}

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
    expect(await screen.findByText("0 items · hidden files included")).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/files?path=%2Ftmp%2Fissue69-file-fixture%2Fnested",
      expect.objectContaining({ credentials: "same-origin" }),
    );

    const callsBeforeRefresh = fetchMock.mock.calls.length;
    fireEvent.click(screen.getByRole("button", { name: "Refresh host files" }));
    await waitFor(() => expect(fetchMock.mock.calls.length).toBeGreaterThan(callsBeforeRefresh));
  });

  it("resolves the Host home before reading the default directory", async () => {
    const homePath = "/Users/cp";
    const homeDirectory = { ...rootDirectory, path: homePath, name: "cp" };
    const fetchMock = vi.fn().mockImplementation((input: RequestInfo | URL) => {
      const url = String(input);
      if (url === "/api/files/home") return Promise.resolve(jsonResponse({ path: homePath, name: "cp", kind: "directory", readable: true }));
      if (url === `/api/files?path=${encodeURIComponent(homePath)}`) return Promise.resolve(jsonResponse(homeDirectory));
      return Promise.resolve(jsonResponse(rootDirectory));
    });
    vi.stubGlobal("fetch", fetchMock);

    render(<HostFilesPane initialPath="/" />);
    expect(await screen.findByText(".hidden")).toBeInTheDocument();
    expect(fetchMock.mock.calls[0][0]).toBe("/api/files/home");
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/files/home",
      expect.objectContaining({ credentials: "same-origin" }),
    );
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/files?path=%2FUsers%2Fcp",
      expect.objectContaining({ credentials: "same-origin" }),
    );
    expect(screen.getByRole("button", { name: "Edit absolute Host path" })).toHaveTextContent(homePath);
  });

  it("surfaces a home resolution error without guessing a fallback path", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ error: { code: "home_unavailable", message: "home unavailable" } }, 500));
    vi.stubGlobal("fetch", fetchMock);

    render(<HostFilesPane initialPath="/" />);
    expect(await screen.findByRole("alert")).toHaveTextContent("Could not resolve Host home: home unavailable (home_unavailable)");
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("does not duplicate the root path and shows breadcrumbs only for nested navigation", async () => {
    const fetchMock = vi.fn().mockImplementation((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("path=%2Ftmp%2Fissue69-file-fixture%2Fnested")) {
        return Promise.resolve(jsonResponse({ ...rootDirectory, path: "/tmp/issue69-file-fixture/nested", name: "nested", entries: [] }));
      }
      if (url === "/api/files/home") return Promise.resolve(jsonResponse({ path: "/", name: "/", kind: "directory", readable: true }));
      return Promise.resolve(jsonResponse({ ...rootDirectory, path: "/", name: "/", entries: [] }));
    });
    vi.stubGlobal("fetch", fetchMock);

    render(<HostFilesPane initialPath="/" />);
    expect(await screen.findByText("0 items · hidden files included")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Edit absolute Host path" })).toHaveTextContent("/");
    expect(screen.getByRole("navigation", { name: "Host file path" })).toHaveTextContent("/");

    const pathInput = editPath();
    fireEvent.change(pathInput, { target: { value: "/tmp/issue69-file-fixture/nested" } });
    fireEvent.submit(screen.getByRole("form", { name: "Go to absolute Host path" }));
    expect(await screen.findByText("0 items · hidden files included")).toBeInTheDocument();
    expect(screen.getByRole("navigation", { name: "Host file path" })).toBeInTheDocument();
  });

  it("switches the fused breadcrumb to an editor and restores it on cancel or blur", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(rootDirectory)));

    render(<HostFilesPane initialPath="/tmp/issue69-file-fixture" />);
    await screen.findByText(".hidden");

    const pathInput = editPath();
    expect(pathInput).toHaveValue("/tmp/issue69-file-fixture");
    fireEvent.change(pathInput, { target: { value: "/tmp/changed" } });
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("textbox", { name: "Absolute Host path" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Edit absolute Host path" })).toHaveTextContent("issue69-file-fixture");

    const blurredInput = editPath();
    fireEvent.change(blurredInput, { target: { value: "/tmp/blurred" } });
    fireEvent.blur(blurredInput);
    expect(screen.queryByRole("textbox", { name: "Absolute Host path" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Edit absolute Host path" })).toHaveTextContent("issue69-file-fixture");
  });

  it("keeps the workspace fixed and confines directory rows to the list scroll region", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(rootDirectory)));

    render(<HostFilesPane initialPath="/tmp/issue69-file-fixture" />);
    await screen.findByText(".hidden");

    expect(screen.getByRole("main", { name: "Host files" })).toHaveClass("overflow-hidden");
    expect(screen.getByTestId("host-files-content")).toHaveClass("h-full", "min-h-0", "w-full");
    expect(screen.getByTestId("host-file-list-scroll")).toHaveClass("min-h-0", "flex-1", "overflow-y-auto");
  });

  it("filters the current directory and sorts locally without another directory request", async () => {
    const searchableDirectory = {
      ...rootDirectory,
      entries: [
        rootDirectory.entries[1],
        { path: "/tmp/issue69-file-fixture/zeta.txt", name: "zeta.txt", kind: "file" as const, size: 100, modifiedAt: "2026-08-14T08:05:00Z", mode: "-rw-------", readable: true, mimeType: "text/plain" },
        { path: "/tmp/issue69-file-fixture/alpha.txt", name: "alpha.txt", kind: "file" as const, size: 2, modifiedAt: "2026-08-14T08:06:00Z", mode: "-rw-------", readable: true, mimeType: "text/plain" },
        rootDirectory.entries[2],
      ],
    };
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(searchableDirectory));
    vi.stubGlobal("fetch", fetchMock);

    render(<HostFilesPane initialPath="/tmp/issue69-file-fixture" />);
    const list = await screen.findByRole("list", { name: "Host file entries" });
    expect(list).toHaveAttribute("data-density", "compact");
    expect(entryButtons(list).map((button) => button.getAttribute("aria-label"))).toEqual([
      "Open directory: nested",
      "Preview file: alpha.txt",
      "Preview file: probe.txt",
      "Preview file: zeta.txt",
    ]);

    fireEvent.change(screen.getByRole("searchbox", { name: "Search current directory" }), { target: { value: "probe" } });
    expect(screen.getByText("1 of 4 items · hidden files included")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Preview file: probe.txt" })).toHaveAttribute("title", "/tmp/issue69-file-fixture/probe.txt");
    expect(screen.queryByRole("button", { name: "Preview file: zeta.txt" })).not.toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledTimes(1);

    fireEvent.change(screen.getByRole("searchbox", { name: "Search current directory" }), { target: { value: "does-not-exist" } });
    expect(screen.getByText("No items found")).toBeInTheDocument();
  });

  it("sorts by size while keeping directories first", async () => {
    const searchableDirectory = {
      ...rootDirectory,
      entries: [
        rootDirectory.entries[2],
        rootDirectory.entries[1],
        { path: "/tmp/issue69-file-fixture/alpha.txt", name: "alpha.txt", kind: "file" as const, size: 2, modifiedAt: "2026-08-14T08:06:00Z", mode: "-rw-------", readable: true, mimeType: "text/plain" },
        { path: "/tmp/issue69-file-fixture/zeta.txt", name: "zeta.txt", kind: "file" as const, size: 100, modifiedAt: "2026-08-14T08:05:00Z", mode: "-rw-------", readable: true, mimeType: "text/plain" },
      ],
    };
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(searchableDirectory)));

    render(<HostFilesPane initialPath="/tmp/issue69-file-fixture" />);
    const list = await screen.findByRole("list", { name: "Host file entries" });
    fireEvent.change(screen.getByRole("combobox", { name: "Sort files" }), { target: { value: "size" } });
    expect(entryButtons(list).map((button) => button.getAttribute("aria-label"))).toEqual([
      "Open directory: nested",
      "Preview file: alpha.txt",
      "Preview file: probe.txt",
      "Preview file: zeta.txt",
    ]);
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

    fireEvent.click(screen.getByRole("button", { name: "Preview file: probe.txt" }));
    expect(await screen.findByRole("dialog")).toBeInTheDocument();
    expect(await screen.findByText("probe-success")).toBeInTheDocument();
    expect(fetchMock.mock.calls.some(([input]) => String(input) === "/api/files/preview?path=%2Ftmp%2Fissue69-file-fixture%2Fprobe.txt&maxBytes=1048576")).toBe(true);
    expect(screen.getByRole("link", { name: "Download" })).toHaveAttribute("href", "/api/files/content?path=%2Ftmp%2Fissue69-file-fixture%2Fprobe.txt&download=1");

    fireEvent.click(screen.getByRole("button", { name: "Close" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("shows a preview error with Retry and recovers on the next bounded read", async () => {
    let previewAttempts = 0;
    const fetchMock = vi.fn().mockImplementation((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.startsWith("/api/files/preview?")) {
        previewAttempts += 1;
        return Promise.resolve(previewAttempts === 1
          ? jsonResponse({ error: { code: "read_failed", message: "temporary read failure" } }, 500)
          : new Response("recovered preview", { status: 200, headers: { "X-Codex-Loom-Preview-Truncated": "false" } }));
      }
      return Promise.resolve(jsonResponse(rootDirectory));
    });
    vi.stubGlobal("fetch", fetchMock);

    render(<HostFilesPane initialPath="/tmp/issue69-file-fixture" />);
    await screen.findByText("probe.txt");
    fireEvent.click(screen.getByRole("button", { name: "Preview file: probe.txt" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("temporary read failure (read_failed)");
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(await screen.findByText("recovered preview")).toBeInTheDocument();
    expect(previewAttempts).toBe(2);
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

  it("copies the exact absolute path and exposes copied feedback", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal("navigator", { clipboard: { writeText } });
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(rootDirectory)));

    render(<HostFilesPane initialPath="/tmp/issue69-file-fixture" />);
    const list = await screen.findByRole("list", { name: "Host file entries" });
    fireEvent.click(within(list).getAllByRole("button", { name: "Copy path" })[0]);
    expect(await screen.findByRole("button", { name: "Path copied" })).toBeInTheDocument();
    expect(writeText).toHaveBeenCalledWith("/tmp/issue69-file-fixture/nested");
  });

  it("shows the same modal preview on desktop and keeps the list as the underlying view", async () => {
    const fetchMock = vi.fn().mockImplementation((input: RequestInfo | URL) => {
      if (String(input).startsWith("/api/files/preview?")) return Promise.resolve(new Response("desktop preview", { status: 200 }));
      return Promise.resolve(jsonResponse(rootDirectory));
    });
    vi.stubGlobal("fetch", fetchMock);

    render(<HostFilesPane initialPath="/tmp/issue69-file-fixture" />);
    await screen.findByText("probe.txt");
    fireEvent.click(screen.getByRole("button", { name: "Preview file: probe.txt" }));
    expect(await screen.findByRole("dialog")).toBeInTheDocument();
    expect(await screen.findByText("desktop preview")).toBeInTheDocument();
    expect(screen.queryByRole("complementary", { name: "File preview" })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Close" }));
    expect(await screen.findByRole("list", { name: "Host file entries" })).toBeInTheDocument();
  });

  it("shows a clear empty-directory state", async () => {
    const emptyDirectory = { ...rootDirectory, entries: [] };
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(emptyDirectory)));

    render(<HostFilesPane initialPath="/tmp/issue69-file-fixture" />);
    expect(await screen.findByText("This directory is empty")).toBeInTheDocument();
    expect(screen.getByRole("searchbox", { name: "Search current directory" })).toBeInTheDocument();
  });

  it("shows the API error with Retry and Go to Home instead of substituting an empty directory", async () => {
    const fetchMock = vi.fn().mockImplementation((input: RequestInfo | URL) => {
      const url = String(input);
      if (url === "/api/files?path=%2Ftmp%2Fmissing") return Promise.resolve(jsonResponse({ error: { code: "not_found", message: "path does not exist" } }, 404));
      if (url === "/api/files/home") return Promise.resolve(jsonResponse({ path: "/tmp/issue69-file-fixture", name: "issue69-file-fixture", kind: "directory", readable: true }));
      return Promise.resolve(jsonResponse(rootDirectory));
    });
    vi.stubGlobal("fetch", fetchMock);

    render(<HostFilesPane initialPath="/tmp/missing" />);
    expect(await screen.findByRole("alert")).toHaveTextContent("Failed to read directory: path does not exist (not_found)");
    expect(screen.queryByText("hidden files included")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Go to Home" }));
    expect(await screen.findByText(".hidden")).toBeInTheDocument();
    expect(fetchMock.mock.calls.some(([input]) => String(input) === "/api/files/home")).toBe(true);
    expect(fetchMock.mock.calls.some(([input]) => String(input) === "/api/files?path=%2Ftmp%2Fissue69-file-fixture")).toBe(true);
  });

  it("navigates to an absolute directory path from the toolbar and updates the host-file route", async () => {
    const nestedDirectory = { ...rootDirectory, path: "/tmp/issue69-file-fixture/nested", name: "nested", entries: [] };
    const onOpenHostFile = vi.fn();
    const fetchMock = vi.fn().mockImplementation((input: RequestInfo | URL) => {
      const url = String(input);
      return Promise.resolve(jsonResponse(url.includes("nested") ? nestedDirectory : rootDirectory));
    });
    vi.stubGlobal("fetch", fetchMock);

    render(<HostFilesPane initialPath="/tmp/issue69-file-fixture" onOpenHostFile={onOpenHostFile} />);
    await screen.findByText(".hidden");
    const pathInput = editPath();
    fireEvent.change(pathInput, { target: { value: "  /tmp/issue69-file-fixture/nested  " } });
    fireEvent.click(screen.getByRole("button", { name: "Go" }));

    expect(await screen.findByText("0 items · hidden files included")).toBeInTheDocument();
    expect(screen.queryByRole("textbox", { name: "Absolute Host path" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Edit absolute Host path" })).toHaveTextContent("nested");
    expect(screen.getByRole("navigation", { name: "Host file path" })).toHaveTextContent("/tmp/issue69-file-fixture/nested");
    expect(onOpenHostFile).toHaveBeenCalledWith("/tmp/issue69-file-fixture/nested");
    expect(fetchMock.mock.calls.some(([input]) => String(input) === "/api/files?path=%2Ftmp%2Fissue69-file-fixture%2Fnested")).toBe(true);
  });

  it("opens a file when its absolute path is submitted", async () => {
    const filePath = "/tmp/issue69-file-fixture/probe.txt";
    const fileNode = { ...rootDirectory.entries[2], path: filePath };
    const onOpenHostFile = vi.fn();
    const fetchMock = vi.fn().mockImplementation((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.startsWith("/api/files/preview?")) return Promise.resolve(new Response("direct preview", { status: 200 }));
      if (url.includes(encodeURIComponent(filePath))) return Promise.resolve(jsonResponse(fileNode));
      return Promise.resolve(jsonResponse(rootDirectory));
    });
    vi.stubGlobal("fetch", fetchMock);

    render(<HostFilesPane initialPath="/tmp/issue69-file-fixture" onOpenHostFile={onOpenHostFile} />);
    await screen.findByText(".hidden");
    fireEvent.change(editPath(), { target: { value: filePath } });
    fireEvent.click(screen.getByRole("button", { name: "Go" }));

    expect(await screen.findByRole("dialog")).toBeInTheDocument();
    expect(await screen.findByText("direct preview")).toBeInTheDocument();
    expect(onOpenHostFile).toHaveBeenCalledWith(filePath);
    expect(fetchMock.mock.calls.some(([input]) => String(input).startsWith("/api/files/preview?"))).toBe(true);
  });

  it("rejects a relative path without changing the current directory", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(rootDirectory));
    vi.stubGlobal("fetch", fetchMock);

    render(<HostFilesPane initialPath="/tmp/issue69-file-fixture" />);
    await screen.findByText(".hidden");
    fireEvent.change(editPath(), { target: { value: "nested/probe.txt" } });
    fireEvent.click(screen.getByRole("button", { name: "Go" }));

    expect(screen.getByRole("alert")).toHaveTextContent("absolute Host path");
    expect(screen.getByText(".hidden")).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("keeps the current directory when an absolute target cannot be read", async () => {
    const missingPath = "/tmp/issue69-file-fixture/missing.txt";
    const fetchMock = vi.fn().mockImplementation((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes(encodeURIComponent(missingPath))) {
        return Promise.resolve(jsonResponse({ error: { code: "not_found", message: "path does not exist" } }, 404));
      }
      return Promise.resolve(jsonResponse(rootDirectory));
    });
    vi.stubGlobal("fetch", fetchMock);

    render(<HostFilesPane initialPath="/tmp/issue69-file-fixture" />);
    await screen.findByText(".hidden");
    const pathInput = editPath();
    fireEvent.change(pathInput, { target: { value: missingPath } });
    fireEvent.click(screen.getByRole("button", { name: "Go" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("Could not open path: path does not exist (not_found)");
    expect(await screen.findByText(".hidden")).toBeInTheDocument();
    expect(screen.queryByRole("textbox", { name: "Absolute Host path" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Edit absolute Host path" })).toHaveTextContent("issue69-file-fixture");
    expect(fetchMock.mock.calls.some(([input]) => String(input).includes(encodeURIComponent(missingPath)))).toBe(true);
  });
});
