import {
  AlertCircle,
  Download,
  File,
  FileAudio,
  FileCode2,
  FileImage,
  FileText,
  FileVideo,
  Folder,
  FolderOpen,
  Loader2,
  RefreshCw,
} from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import {
  fetchHostFileNode,
  fetchHostTextPreview,
  formatHostFileModifiedAt,
  formatHostFileSize,
  hostFileContentURL,
  hostFilePreviewKind,
  HostFileRequestError,
  MAX_HOST_PREVIEW_BYTES,
  type HostDirectory,
  type HostFileEntry,
  type HostFileNode,
  type HostFilePreviewKind,
} from "./host-files";
import type { ReactNode } from "react";
import { Button, buttonVariants } from "./components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "./components/ui/dialog";
import { MarkdownContent } from "./pages/agent/markdown";
import { cn } from "./lib/utils";

function parentDirectory(path: string) {
  const normalized = path.replace(/\/+$/, "") || "/";
  const slash = normalized.lastIndexOf("/");
  return slash <= 0 ? "/" : normalized.slice(0, slash);
}

function breadcrumbs(path: string) {
  const normalized = path.startsWith("/") ? path.replace(/\/+$/, "") || "/" : `/${path}`;
  if (normalized === "/") return [{ path: "/", label: "/" }];
  const parts = normalized.slice(1).split("/");
  return [
    { path: "/", label: "/" },
    ...parts.map((part, index) => ({
      path: `/${parts.slice(0, index + 1).join("/")}`,
      label: part,
    })),
  ];
}

function hostErrorMessage(error: unknown) {
  if (error instanceof HostFileRequestError) return `${error.message} (${error.code})`;
  if (error instanceof Error) return error.message;
  return "Host file request failed.";
}

function fileIcon(entry: Pick<HostFileEntry, "kind" | "mimeType" | "path">) {
  if (entry.kind === "directory") return Folder;
  switch (hostFilePreviewKind(entry)) {
    case "image":
      return FileImage;
    case "audio":
      return FileAudio;
    case "video":
      return FileVideo;
    case "text":
    case "markdown":
      return FileCode2;
    default:
      return File;
  }
}

function FileMetadata({ file }: { file: HostFileEntry }) {
  return (
    <div className="flex min-w-0 flex-wrap gap-x-3 gap-y-1 font-mono text-[10px] text-muted-foreground">
      <span>{file.kind}</span>
      <span>{formatHostFileSize(file.size)}</span>
      <span>{formatHostFileModifiedAt(file.modifiedAt)}</span>
      <span>{file.mode}</span>
      {file.mimeType ? <span className="break-all">{file.mimeType}</span> : null}
    </div>
  );
}

function PreviewBody({
  file,
  kind,
  text,
  loading,
  error,
  truncated,
  onOpenHostFile,
}: {
  file: HostFileEntry;
  kind: HostFilePreviewKind;
  text: string;
  loading: boolean;
  error: string;
  truncated: boolean;
  onOpenHostFile?: (path: string) => void;
}) {
  if (!file.readable) {
    return <PreviewMessage tone="error">This file is not readable by the Host process.</PreviewMessage>;
  }
  if (error) return <PreviewMessage tone="error">{error}</PreviewMessage>;
  if (loading) {
    return (
      <div className="flex min-h-40 items-center justify-center gap-2 text-sm text-muted-foreground" role="status">
        <Loader2 className="size-4 animate-spin" />
        Reading a bounded preview…
      </div>
    );
  }

  const contentURL = hostFileContentURL(file.path, "preview");
  switch (kind) {
    case "text":
      return (
        <div className="min-w-0">
          {truncated ? <PreviewMessage>Preview limited to {formatHostFileSize(MAX_HOST_PREVIEW_BYTES)}. Download the file for the complete content.</PreviewMessage> : null}
          <pre className="max-h-[min(62dvh,680px)] overflow-auto whitespace-pre-wrap break-words rounded-md border border-border bg-muted/25 p-3 font-mono text-[12px] leading-5 text-foreground">{text}</pre>
        </div>
      );
    case "markdown":
      return (
        <div className="min-w-0 overflow-auto rounded-md border border-border bg-card p-4 text-sm">
          {truncated ? <PreviewMessage>Preview limited to {formatHostFileSize(MAX_HOST_PREVIEW_BYTES)}. Download the file for the complete content.</PreviewMessage> : null}
          <MarkdownContent content={text} onOpenHostFile={onOpenHostFile} />
        </div>
      );
    case "image":
      return <img src={contentURL} alt={file.name} className="mx-auto max-h-[min(65dvh,720px)] max-w-full object-contain" />;
    case "pdf":
      return <iframe title={`Preview of ${file.name}`} src={contentURL} className="h-[min(68dvh,740px)] min-h-80 w-full rounded-md border border-border" />;
    case "audio":
      return <audio controls preload="metadata" src={contentURL} className="w-full" aria-label={`Preview of ${file.name}`} />;
    case "video":
      return <video controls preload="metadata" src={contentURL} className="mx-auto max-h-[min(65dvh,720px)] max-w-full" aria-label={`Preview of ${file.name}`} />;
    case "unknown":
      return <PreviewMessage>This format has no inline preview. Use Download to retrieve the live file.</PreviewMessage>;
  }
}

function PreviewMessage({ children, tone = "muted" }: { children: ReactNode; tone?: "muted" | "error" }) {
  return (
    <div className={cn("mb-3 rounded-md border px-3 py-2 text-[12px] leading-5", tone === "error" ? "border-destructive/35 bg-destructive/5 text-destructive" : "border-border bg-muted/25 text-muted-foreground")} role={tone === "error" ? "alert" : undefined}>
      {children}
    </div>
  );
}

export function HostFilePreviewDialog({
  file,
  open,
  onOpenChange,
  onOpenHostFile,
}: {
  file: HostFileEntry | null;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onOpenHostFile?: (path: string) => void;
}) {
  const kind = file ? hostFilePreviewKind(file) : "unknown";
  const [text, setText] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [truncated, setTruncated] = useState(false);

  useEffect(() => {
    if (!open || !file || (kind !== "text" && kind !== "markdown")) return;
    const controller = new AbortController();
    let current = true;
    setLoading(true);
    setText("");
    setError("");
    setTruncated(false);
    const start = window.setTimeout(() => {
      fetchHostTextPreview(file.path, MAX_HOST_PREVIEW_BYTES, controller.signal)
        .then((preview) => {
          setText(preview.text);
          setTruncated(preview.truncated);
        })
        .catch((reason: unknown) => {
          if (reason instanceof DOMException && reason.name === "AbortError") return;
          setError(hostErrorMessage(reason));
        })
        .finally(() => { if (current) setLoading(false); });
    }, 0);
    return () => {
      current = false;
      window.clearTimeout(start);
      controller.abort();
    };
  }, [file?.path, kind, open]);

  if (!file) return null;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="h-dvh w-screen max-w-none grid-rows-[auto_minmax(0,1fr)_auto] gap-0 overflow-hidden rounded-none p-0 sm:h-[min(92dvh,920px)] sm:w-[min(96vw,1240px)] sm:max-w-none sm:rounded-xl">
        <DialogHeader className="min-w-0 border-b border-border px-4 py-3 pr-14">
          <DialogTitle className="break-all pr-2">{file?.name || "Host file"}</DialogTitle>
          {file ? (
            <DialogDescription className="min-w-0 space-y-1">
              <span className="block break-all font-mono text-[10px]">{file.path}</span>
              <FileMetadata file={file} />
            </DialogDescription>
          ) : null}
        </DialogHeader>
        <div className="min-h-0 overflow-auto px-4 py-4">
          {file ? <PreviewBody file={file} kind={kind} text={text} loading={loading} error={error} truncated={truncated} onOpenHostFile={onOpenHostFile} /> : null}
        </div>
        <div className="-mx-4 -mb-4 flex flex-col gap-2 rounded-b-xl border-t border-border bg-muted/50 p-4 sm:flex-row sm:items-center sm:justify-between">
          {file ? <span className="min-w-0 break-all text-left font-mono text-[10px] text-muted-foreground">{kind === "unknown" ? "metadata only" : kind}</span> : <span />}
          {file ? (
            <a className={buttonVariants({ variant: "outline", size: "sm" })} href={hostFileContentURL(file.path, "download")} download>
              <Download />
              Download
            </a>
          ) : null}
        </div>
      </DialogContent>
    </Dialog>
  );
}

function DirectoryToolbar({ path, loading, onRefresh }: { path: string; loading: boolean; onRefresh: () => void }) {
  return (
    <div className="flex min-w-0 flex-wrap items-center gap-2">
      <FolderOpen className="size-4 shrink-0 text-primary" aria-hidden="true" />
      <span className="min-w-0 flex-1 break-all font-mono text-[12px] text-foreground" title={path}>{path}</span>
      <Button type="button" variant="outline" size="sm" onClick={onRefresh} disabled={loading} aria-label="Refresh host files">
        <RefreshCw className={cn(loading && "animate-spin")} />
        Refresh
      </Button>
    </div>
  );
}

function Breadcrumbs({ path, onSelect }: { path: string; onSelect: (path: string) => void }) {
  return (
    <nav aria-label="Host file path" className="flex min-w-0 flex-wrap items-center gap-1 text-[11px]">
      {breadcrumbs(path).map((crumb, index, items) => (
        <span key={crumb.path} className="flex min-w-0 items-center gap-1">
          {index > 0 ? <span className="text-muted-foreground/50" aria-hidden="true">/</span> : null}
          {index === items.length - 1 ? (
            <span className="min-w-0 break-all font-mono text-foreground" aria-current="page">{crumb.label}</span>
          ) : (
            <button type="button" className="max-w-[14rem] break-all font-mono text-primary underline-offset-2 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40" onClick={() => onSelect(crumb.path)}>
              {crumb.label}
            </button>
          )}
        </span>
      ))}
    </nav>
  );
}

function DirectoryEntry({ entry, onOpen }: { entry: HostFileEntry; onOpen: (entry: HostFileEntry) => void }) {
  const Icon = fileIcon(entry);
  const action = entry.kind === "directory" ? "Open directory" : "Preview file";
  return (
    <li>
      <button type="button" onClick={() => onOpen(entry)} className="flex w-full min-w-0 items-start gap-3 rounded-md border border-transparent px-3 py-2.5 text-left outline-none transition-colors hover:border-border hover:bg-muted/45 focus-visible:border-ring focus-visible:ring-2 focus-visible:ring-ring/40" aria-label={`${action}: ${entry.name}`}>
        <Icon className="mt-0.5 size-4 shrink-0 text-primary" aria-hidden="true" />
        <span className="min-w-0 flex-1">
          <span className="block break-all text-[12.5px] font-medium text-foreground">{entry.name}</span>
          <span className="mt-0.5 block break-all font-mono text-[10px] text-muted-foreground">{entry.path}</span>
          {entry.errorCode ? <span className="mt-1 block text-[10px] text-destructive">{entry.errorCode}</span> : null}
        </span>
        <span className="flex max-w-[8rem] shrink-0 flex-col items-end break-words text-right font-mono text-[9.5px] text-muted-foreground sm:max-w-none sm:text-[10px]">
          <span className="block uppercase">{entry.kind}</span>
          <span className="mt-0.5 block">{formatHostFileSize(entry.size)}</span>
          <span className="mt-0.5 block">{formatHostFileModifiedAt(entry.modifiedAt)}</span>
        </span>
      </button>
    </li>
  );
}

export function HostFilesPane({ initialPath = "/", onOpenHostFile }: { initialPath?: string; onOpenHostFile?: (path: string) => void }) {
  const [path, setPath] = useState(initialPath || "/");
  const [node, setNode] = useState<HostFileNode | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [previewFile, setPreviewFile] = useState<HostFileEntry | null>(null);
  const [revision, setRevision] = useState(0);

  useEffect(() => {
    setPath(initialPath || "/");
    setPreviewFile(null);
  }, [initialPath]);

  useEffect(() => {
    const controller = new AbortController();
    let current = true;
    setLoading(true);
    setError("");
    setNode(null);
    const start = window.setTimeout(() => {
      fetchHostFileNode(path, controller.signal)
        .then((nextNode) => {
          setNode(nextNode);
          if (nextNode.kind === "file" && path !== "/") setPreviewFile(nextNode);
        })
        .catch((reason: unknown) => {
          if (reason instanceof DOMException && reason.name === "AbortError") return;
          setNode(null);
          setError(hostErrorMessage(reason));
        })
        .finally(() => { if (current) setLoading(false); });
    }, 0);
    return () => {
      current = false;
      window.clearTimeout(start);
      controller.abort();
    };
  }, [path, revision]);

  const directory = node?.kind === "directory" ? (node as HostDirectory) : null;
  const entries = useMemo(() => {
    if (!directory) return [];
    return [...directory.entries].sort((left, right) => left.kind === right.kind ? left.name.localeCompare(right.name) : left.kind === "directory" ? -1 : 1);
  }, [directory]);

  const refresh = () => {
    setRevision((current) => current + 1);
  };

  const openEntry = (entry: HostFileEntry) => {
    if (entry.kind === "directory") {
      setPreviewFile(null);
      setPath(entry.path);
    } else {
      setPreviewFile(entry);
    }
  };

  const openPath = (nextPath: string) => {
    setPreviewFile(null);
    setPath(nextPath);
    onOpenHostFile?.(nextPath);
  };

  return (
    <main className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden bg-background" aria-label="Host files">
      <div className="shrink-0 border-b border-border px-4 py-3 md:px-6">
        <div className="mx-auto max-w-[1080px] space-y-2">
          <DirectoryToolbar path={path} loading={loading} onRefresh={refresh} />
          <Breadcrumbs path={path} onSelect={openPath} />
        </div>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto max-w-[1080px] px-4 py-4 md:px-6 md:py-6">
          {error ? (
            <div className="flex items-start gap-2 rounded-md border border-destructive/35 bg-destructive/5 px-3 py-3 text-[12px] leading-5 text-destructive" role="alert">
              <AlertCircle className="mt-0.5 size-4 shrink-0" />
              <span className="min-w-0 break-words">{error}</span>
            </div>
          ) : directory ? (
            <>
              <div className="mb-3 flex min-w-0 items-center justify-between gap-3">
                <div className="min-w-0 break-all text-[12px] text-muted-foreground">{entries.length} {entries.length === 1 ? "entry" : "entries"} · hidden files included</div>
                {directory.readable ? null : <span className="text-[11px] text-destructive">not readable</span>}
              </div>
              {entries.length > 0 ? <ul className="space-y-1">{entries.map((entry) => <DirectoryEntry key={entry.path} entry={entry} onOpen={openEntry} />)}</ul> : <div className="rounded-md border border-dashed border-border px-4 py-10 text-center text-[12px] text-muted-foreground">This directory is empty.</div>}
            </>
          ) : node?.kind === "file" ? (
            <div className="rounded-md border border-border bg-card px-4 py-4">
              <div className="break-all text-sm font-medium">{node.name}</div>
              <FileMetadata file={node} />
              <p className="mt-3 text-[12px] text-muted-foreground">The file preview is open. Use Download for the live file.</p>
            </div>
          ) : loading ? (
            <div className="flex min-h-40 items-center justify-center gap-2 text-sm text-muted-foreground" role="status"><Loader2 className="size-4 animate-spin" />Loading Host files…</div>
          ) : null}
        </div>
      </div>
      <HostFilePreviewDialog file={previewFile} open={Boolean(previewFile)} onOpenChange={(open) => { if (!open) setPreviewFile(null); }} onOpenHostFile={(nextPath) => openPath(nextPath)} />
    </main>
  );
}
