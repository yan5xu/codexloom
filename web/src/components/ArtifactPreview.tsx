import { useEffect, useState } from "react";
import { Download, FileArchive, FileCode2, FileText, Image as ImageIcon, Loader2 } from "lucide-react";
import type { ExternalAttachment } from "../feed";
import { MarkdownContent } from "../pages/agent/markdown";
import { buttonVariants } from "./ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "./ui/dialog";
import { cn } from "../lib/utils";

export type ArtifactPreviewKind = "markdown" | "text" | "image" | "pdf" | "unsupported";

export const MAX_TEXT_PREVIEW_BYTES = 2 * 1024 * 1024;

const rasterImageTypes = new Set(["image/png", "image/jpeg", "image/gif", "image/webp"]);
const markdownExtensions = new Set(["md", "markdown", "mdown", "mkd"]);
const textExtensions = new Set([
  "txt", "text", "log",
  "json", "jsonl", "ndjson",
  "csv", "tsv",
  "xml", "yaml", "yml",
  "html", "htm", "css", "scss", "less",
  "js", "jsx", "ts", "tsx", "mjs", "cjs",
  "go", "rs", "py", "rb", "java", "kt", "kts", "swift",
  "c", "h", "cc", "cpp", "hpp", "cs", "php",
  "sh", "bash", "zsh", "fish", "sql", "toml", "ini", "conf",
  "graphql", "gql", "proto", "diff", "patch", "svg",
]);

function normalizedMimeType(value?: string) {
  return (value || "").split(";", 1)[0].trim().toLowerCase();
}

function fileExtension(name?: string) {
  const match = (name || "").toLowerCase().match(/\.([a-z0-9]+)$/);
  return match?.[1] || "";
}

export function artifactPreviewKind(artifact: ExternalAttachment): ArtifactPreviewKind {
  const mimeType = normalizedMimeType(artifact.mimeType);
  const extension = fileExtension(artifact.name);

  if (mimeType === "text/markdown" || mimeType === "text/x-markdown" || markdownExtensions.has(extension)) {
    return "markdown";
  }
  if (mimeType === "application/pdf" || extension === "pdf") return "pdf";
  if (rasterImageTypes.has(mimeType)) return "image";
  if (
    mimeType === "application/json"
    || mimeType.endsWith("+json")
    || mimeType === "application/xml"
    || mimeType.endsWith("+xml")
    || mimeType === "application/yaml"
    || mimeType === "application/x-yaml"
    || mimeType.startsWith("text/")
    || textExtensions.has(extension)
  ) {
    return "text";
  }
  return "unsupported";
}

export function artifactPreviewTooLarge(artifact: ExternalAttachment) {
  const size = Number(artifact.size);
  const kind = artifactPreviewKind(artifact);
  return (kind === "markdown" || kind === "text") && Number.isFinite(size) && size > MAX_TEXT_PREVIEW_BYTES;
}

export function artifactURL(url: string, disposition: "preview" | "download") {
  const [withoutHash, hash = ""] = url.split("#", 2);
  const separator = withoutHash.includes("?") ? "&" : "?";
  return `${withoutHash}${separator}${disposition}=1${hash ? `#${hash}` : ""}`;
}

export function PublishedArtifactCard({
  artifact,
  publishedAt,
  displayName,
}: {
  artifact: ExternalAttachment;
  publishedAt?: string;
  displayName?: string;
}) {
  const [open, setOpen] = useState(false);
  const kind = artifactPreviewKind(artifact);
  const previewable = kind !== "unsupported" && !artifactPreviewTooLarge(artifact);
  const label = displayName || artifact.name || artifact.id || "Published artifact";

  return (
    <>
      <article className="my-2 flex min-w-0 items-center gap-2 rounded-md border border-border bg-card px-3 py-2.5 shadow-card">
        {artifact.url && previewable ? (
          <button
            type="button"
            className="flex min-w-0 flex-1 items-center gap-3 text-left outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
            onClick={() => setOpen(true)}
            aria-label={`Preview ${label}`}
          >
            <ArtifactIcon kind={kind} />
            <ArtifactSummary artifact={artifact} publishedAt={publishedAt} displayName={displayName} />
            <span className="shrink-0 text-[11px] font-medium text-primary">Preview</span>
          </button>
        ) : artifact.url ? (
          <a
            href={artifactURL(artifact.url, "download")}
            className="flex min-w-0 flex-1 items-center gap-3 outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
          >
            <ArtifactIcon kind={kind} />
            <ArtifactSummary artifact={artifact} publishedAt={publishedAt} displayName={displayName} />
            <span className="shrink-0 text-[11px] font-medium text-primary">Download</span>
          </a>
        ) : (
          <div className="flex min-w-0 flex-1 items-center gap-3">
            <ArtifactIcon kind={kind} />
            <ArtifactSummary artifact={artifact} publishedAt={publishedAt} displayName={displayName} />
          </div>
        )}
        {artifact.url && previewable && (
          <a
            href={artifactURL(artifact.url, "download")}
            className={cn(buttonVariants({ variant: "ghost", size: "icon-sm" }), "shrink-0")}
            title={`Download ${label}`}
            aria-label={`Download ${label}`}
          >
            <Download />
          </a>
        )}
      </article>
      {artifact.url && previewable && (
        <ArtifactPreviewDialog artifact={artifact} kind={kind} open={open} onOpenChange={setOpen} displayName={displayName} />
      )}
    </>
  );
}

function ArtifactSummary({
  artifact,
  publishedAt,
  displayName,
}: {
  artifact: ExternalAttachment;
  publishedAt?: string;
  displayName?: string;
}) {
  const label = displayName || artifact.name || artifact.id || "Published artifact";
  return (
    <>
      <div className="min-w-0 flex-1">
        <div className="font-mono text-[9px] uppercase text-muted-foreground">Published artifact</div>
        <div className="truncate text-[12px] font-medium text-foreground">{label}</div>
        <div className="mt-0.5 flex min-w-0 items-center gap-2 font-mono text-[9px] text-muted-foreground">
          {publishedAt && <time dateTime={publishedAt}>{formatArtifactTime(publishedAt)}</time>}
          {artifact.id && <span className="truncate">{artifact.id.slice(0, 12)}</span>}
        </div>
      </div>
      {artifact.size !== undefined && (
        <span className="shrink-0 font-mono text-[9.5px] text-muted-foreground">
          {formatFileSize(artifact.size)}
        </span>
      )}
    </>
  );
}

function ArtifactIcon({ kind }: { kind: ArtifactPreviewKind }) {
  const Icon = kind === "image"
    ? ImageIcon
    : kind === "text" || kind === "markdown"
      ? FileCode2
      : kind === "unsupported"
        ? FileArchive
        : FileText;
  return (
    <span className="flex size-8 shrink-0 items-center justify-center rounded-sm bg-secondary text-muted-foreground">
      <Icon className="size-4" />
    </span>
  );
}

function ArtifactPreviewDialog({
  artifact,
  kind,
  open,
  onOpenChange,
  displayName,
}: {
  artifact: ExternalAttachment;
  kind: ArtifactPreviewKind;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  displayName?: string;
}) {
  const [text, setText] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [markdownMode, setMarkdownMode] = useState<"rendered" | "source">("rendered");
  const url = artifact.url || "";
  const label = displayName || artifact.name || artifact.id || "Published artifact";

  useEffect(() => {
    if (!open || (kind !== "markdown" && kind !== "text") || !url) return;
    const controller = new AbortController();
    setLoading(true);
    setError("");
    setText("");
    fetch(url, { credentials: "same-origin", signal: controller.signal })
      .then(async (response) => {
        if (!response.ok) throw new Error(`Preview failed (${response.status})`);
        const contentLength = Number(response.headers.get("content-length"));
        if (Number.isFinite(contentLength) && contentLength > MAX_TEXT_PREVIEW_BYTES) {
          throw new Error("This file is too large to preview.");
        }
        const content = await response.text();
        if (new TextEncoder().encode(content).byteLength > MAX_TEXT_PREVIEW_BYTES) {
          throw new Error("This file is too large to preview.");
        }
        setText(content);
      })
      .catch((cause) => {
        if (cause instanceof DOMException && cause.name === "AbortError") return;
        setError(cause instanceof Error ? cause.message : "Preview failed.");
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, [kind, open, url]);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="h-dvh w-screen max-w-none grid-rows-[auto_minmax(0,1fr)] gap-0 overflow-hidden rounded-none p-0 sm:h-[min(92dvh,920px)] sm:w-[min(96vw,1240px)] sm:max-w-none sm:rounded-xl">
        <DialogHeader className="border-b border-border px-4 py-3 pr-24">
          <DialogTitle className="truncate pr-2">{label}</DialogTitle>
          <DialogDescription className="flex min-w-0 flex-wrap gap-x-3 gap-y-1 font-mono text-[10px]">
            {artifact.mimeType && <span>{artifact.mimeType}</span>}
            {artifact.size !== undefined && <span>{formatFileSize(artifact.size)}</span>}
            {artifact.id && <span className="truncate">{artifact.id}</span>}
          </DialogDescription>
          <div className="absolute right-11 top-2">
            <a
              href={artifactURL(url, "download")}
              className={buttonVariants({ variant: "ghost", size: "icon-sm" })}
              title={`Download ${label}`}
              aria-label={`Download ${label}`}
            >
              <Download />
            </a>
          </div>
        </DialogHeader>
        <div data-artifact-preview-body className="relative min-h-0 min-w-0 bg-muted/15">
          {loading && (
            <div className="flex h-full items-center justify-center gap-2 text-sm text-muted-foreground">
              <Loader2 className="size-4 animate-spin" />
              Loading preview
            </div>
          )}
          {error && (
            <div className="flex h-full flex-col items-center justify-center gap-3 px-6 text-center">
              <FileText className="size-8 text-muted-foreground" />
              <div>
                <div className="text-sm font-medium">Preview unavailable</div>
                <div className="mt-1 text-xs text-muted-foreground">{error}</div>
              </div>
              <a href={artifactURL(url, "download")} className={buttonVariants({ variant: "outline", size: "sm" })}>
                <Download />
                Download
              </a>
            </div>
          )}
          {!loading && !error && kind === "markdown" && (
            <div className="flex h-full min-w-0 flex-col">
              <div className="flex shrink-0 justify-end border-b border-border bg-background/80 px-3 py-2">
                <div className="flex rounded-md border border-border bg-card p-0.5 shadow-card" role="group" aria-label="Markdown preview mode">
                  <button
                    type="button"
                    className={previewModeClass(markdownMode === "rendered")}
                    onClick={() => setMarkdownMode("rendered")}
                  >
                    Rendered
                  </button>
                  <button
                    type="button"
                    className={previewModeClass(markdownMode === "source")}
                    onClick={() => setMarkdownMode("source")}
                  >
                    Source
                  </button>
                </div>
              </div>
              {markdownMode === "rendered" ? (
                <div className="min-h-0 flex-1 overflow-auto px-5 py-6 sm:px-8">
                  <MarkdownContent content={text} className="mx-auto max-w-4xl" />
                </div>
              ) : (
                <pre className="min-h-0 flex-1 overflow-auto whitespace-pre-wrap break-words px-5 py-6 font-mono text-[12px] leading-5 text-foreground sm:px-8">
                  {text}
                </pre>
              )}
            </div>
          )}
          {!loading && !error && kind === "text" && (
            <pre className="h-full overflow-auto whitespace-pre-wrap break-words px-5 py-6 font-mono text-[12px] leading-5 text-foreground sm:px-8">
              {text}
            </pre>
          )}
          {kind === "image" && (
            <div className="flex h-full items-center justify-center overflow-auto p-4">
              <img src={artifactURL(url, "preview")} alt={label} className="max-h-full max-w-full object-contain" />
            </div>
          )}
          {kind === "pdf" && (
            <iframe
              src={`${artifactURL(url, "preview")}#view=FitH`}
              title={`Preview ${label}`}
              className="h-full w-full border-0 bg-background"
            />
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}

function previewModeClass(selected: boolean) {
  return cn(
    "h-6 rounded-sm px-2 text-[10px] font-medium outline-none focus-visible:ring-2 focus-visible:ring-ring/40",
    selected ? "bg-foreground text-background" : "text-muted-foreground hover:text-foreground",
  );
}

function formatArtifactTime(value: string) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString([], { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });
}

function formatFileSize(raw: string | number) {
  const size = Number(raw);
  if (!Number.isFinite(size) || size < 0) return String(raw);
  if (size < 1024) return `${size} B`;
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`;
  return `${(size / (1024 * 1024)).toFixed(1)} MB`;
}
