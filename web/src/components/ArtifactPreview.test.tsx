import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  artifactPreviewKind,
  artifactPreviewTooLarge,
  artifactURL,
  MAX_TEXT_PREVIEW_BYTES,
  PublishedArtifactCard,
} from "./ArtifactPreview";

describe("artifactPreviewKind", () => {
  it.each([
    ["guide.md", "text/plain", "markdown"],
    ["data.json", "application/json", "text"],
    ["page.html", "text/html", "text"],
    ["icon.svg", "image/svg+xml", "text"],
    ["image.png", "image/png", "image"],
    ["paper.pdf", "application/pdf", "pdf"],
    ["archive.zip", "application/zip", "unsupported"],
    ["document.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", "unsupported"],
  ])("classifies %s as %s", (name, mimeType, kind) => {
    expect(artifactPreviewKind({ name, mimeType })).toBe(kind);
  });

  it("limits text previews without blocking streamed media", () => {
    expect(artifactPreviewTooLarge({ name: "large.md", size: MAX_TEXT_PREVIEW_BYTES + 1 })).toBe(true);
    expect(artifactPreviewTooLarge({ name: "large.pdf", size: MAX_TEXT_PREVIEW_BYTES + 1 })).toBe(false);
  });

  it("adds explicit disposition parameters", () => {
    expect(artifactURL("/api/artifacts/art_1", "preview")).toBe("/api/artifacts/art_1?preview=1");
    expect(artifactURL("/api/artifacts/art_1?token=x#page=2", "download")).toBe("/api/artifacts/art_1?token=x&download=1#page=2");
  });
});

describe("PublishedArtifactCard", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("opens and renders a Markdown artifact instead of navigating to a download", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response("# Preview title\n\nArtifact body", {
      status: 200,
      headers: { "content-type": "text/markdown", "content-length": "31" },
    }));
    vi.stubGlobal("fetch", fetchMock);

    render(
      <PublishedArtifactCard
        artifact={{ id: "art_markdown", name: "guide.md", mimeType: "text/markdown", size: 31, url: "/api/agents/a/artifacts/art_markdown" }}
        publishedAt="2026-07-27T09:00:00Z"
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "Preview guide.md" }));
    const dialog = await screen.findByRole("dialog");
    expect(dialog).toHaveClass("h-dvh", "w-screen", "rounded-none");
    expect(await screen.findByText("Preview title")).toBeInTheDocument();
    expect(screen.getByRole("group", { name: "Markdown preview mode" }).closest("[data-artifact-preview-body]")).toHaveClass("min-w-0");
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/agents/a/artifacts/art_markdown",
      expect.objectContaining({ credentials: "same-origin" }),
    );

    fireEvent.click(screen.getByRole("button", { name: "Source" }));
    expect(screen.getByText(/# Preview title/)).toBeInTheDocument();
  });

  it("keeps file-based preview classification when a context label is displayed", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response("# Labeled Markdown", {
      status: 200,
      headers: { "content-type": "text/plain", "content-length": "18" },
    }));
    vi.stubGlobal("fetch", fetchMock);

    render(
      <PublishedArtifactCard
        artifact={{ id: "art_labeled", name: "guide.md", mimeType: "text/plain", size: 18, url: "/api/agents/a/artifacts/art_labeled" }}
        displayName="Product guide"
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "Preview Product guide" }));
    expect(await screen.findByText("Labeled Markdown")).toBeInTheDocument();
    expect(screen.getByRole("group", { name: "Markdown preview mode" })).toBeInTheDocument();
  });

  it("keeps unsupported artifacts as downloads", () => {
    render(
      <PublishedArtifactCard
        artifact={{ id: "art_zip", name: "bundle.zip", mimeType: "application/zip", size: 1024, url: "/api/agents/a/artifacts/art_zip" }}
      />,
    );
    const download = screen.getByRole("link", { name: /bundle.zip/i });
    expect(download).toHaveAttribute("href", "/api/agents/a/artifacts/art_zip?download=1");
    expect(screen.queryByRole("button", { name: /Preview bundle.zip/i })).not.toBeInTheDocument();
  });

  it("uses explicit inline URLs for raster images and PDFs", () => {
    const { unmount } = render(
      <PublishedArtifactCard
        artifact={{ id: "art_image", name: "screen.png", mimeType: "image/png", url: "/api/agents/a/artifacts/art_image" }}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Preview screen.png" }));
    expect(screen.getByRole("img", { name: "screen.png" })).toHaveAttribute(
      "src",
      "/api/agents/a/artifacts/art_image?preview=1",
    );
    unmount();

    render(
      <PublishedArtifactCard
        artifact={{ id: "art_pdf", name: "report.pdf", mimeType: "application/pdf", url: "/api/agents/a/artifacts/art_pdf" }}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Preview report.pdf" }));
    expect(screen.getByTitle("Preview report.pdf")).toHaveAttribute(
      "src",
      "/api/agents/a/artifacts/art_pdf?preview=1#view=FitH",
    );
  });

  it("does not fetch a text artifact beyond the preview limit", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    render(
      <PublishedArtifactCard
        artifact={{ id: "art_large", name: "large.txt", mimeType: "text/plain", size: MAX_TEXT_PREVIEW_BYTES + 1, url: "/api/agents/a/artifacts/art_large" }}
      />,
    );
    expect(screen.getByRole("link", { name: /large.txt/i })).toHaveAttribute("href", "/api/agents/a/artifacts/art_large?download=1");
    await waitFor(() => expect(fetchMock).not.toHaveBeenCalled());
  });
});
