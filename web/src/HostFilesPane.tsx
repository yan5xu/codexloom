import {
  AlertCircle,
  ArrowDownUp,
  Check,
  Copy,
  Download,
  File,
  FileAudio,
  FileCode2,
  FileImage,
  FileVideo,
  Folder,
  FolderOpen,
  Loader2,
  RefreshCw,
  Search,
  X,
} from "lucide-react";
import { useEffect, useMemo, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import {
  fetchHostFileNode,
  fetchHostHome,
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
import { Button, buttonVariants } from "./components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "./components/ui/dialog";
import { MarkdownContent } from "./pages/agent/markdown";
import { copyText } from "./lib/clipboard";
import { cn } from "./lib/utils";

type HostFileSort = "name" | "type" | "size" | "modified";

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

function entryType(entry: HostFileEntry) {
  return entry.kind === "directory" ? "folder" : hostFilePreviewKind(entry);
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

function CopyPathButton({ path, compact = false }: { path: string; compact?: boolean }) {
  const [status, setStatus] = useState<"idle" | "copied" | "failed">("idle");
  const label = status === "copied" ? "Path copied" : status === "failed" ? "Copy path failed" : "Copy path";

  const copyPath = async () => {
    const copied = await copyText(path);
    setStatus(copied ? "copied" : "failed");
    window.setTimeout(() => setStatus("idle"), 1600);
  };

  return (
    <Button
      type="button"
      variant="ghost"
      size={compact ? "icon-sm" : "sm"}
      onClick={copyPath}
      aria-label={label}
      title={label}
      className={cn(
        compact ? "size-7" : "h-7 px-2 text-[10px]",
        status === "copied" ? "text-success" : status === "failed" ? "text-destructive" : "text-muted-foreground",
      )}
    >
      {status === "copied" ? <Check /> : status === "failed" ? <AlertCircle /> : <Copy />}
      {!compact ? <span aria-live="polite">{label}</span> : null}
    </Button>
  );
}

function PreviewBody({
  file,
  kind,
  text,
  loading,
  error,
  truncated,
  onRetry,
  onOpenHostFile,
}: {
  file: HostFileEntry;
  kind: HostFilePreviewKind;
  text: string;
  loading: boolean;
  error: string;
  truncated: boolean;
  onRetry: () => void;
  onOpenHostFile?: (path: string) => void;
}) {
  if (!file.readable) {
    return <PreviewMessage tone="error">This file is not readable by the Host process.</PreviewMessage>;
  }
  if (error) {
    return (
      <PreviewMessage tone="error" action={<Button type="button" variant="outline" size="sm" onClick={onRetry}>Retry</Button>}>
        {error}
      </PreviewMessage>
    );
  }
  if (loading) {
    return (
      <div className="flex min-h-40 items-center justify-center gap-2 text-sm text-muted-foreground" role="status">
        <Loader2 className="size-4 animate-spin" />
        Reading file…
      </div>
    );
  }

  const contentURL = hostFileContentURL(file.path, "preview");
  switch (kind) {
    case "text":
      return (
        <div className="min-w-0">
          {truncated ? <PreviewMessage>Preview limited to {formatHostFileSize(MAX_HOST_PREVIEW_BYTES)}. Download the file for the complete content.</PreviewMessage> : null}
          <pre className="max-h-[min(58dvh,640px)] overflow-auto whitespace-pre-wrap break-words rounded-md border border-border bg-muted/25 p-3 font-mono text-[12px] leading-5 text-foreground">{text}</pre>
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
      return <img src={contentURL} alt={file.name} className="mx-auto max-h-[min(60dvh,680px)] max-w-full object-contain" />;
    case "pdf":
      return <iframe title={`Preview of ${file.name}`} src={contentURL} className="h-[min(64dvh,680px)] min-h-80 w-full rounded-md border border-border" />;
    case "audio":
      return <audio controls preload="metadata" src={contentURL} className="w-full" aria-label={`Preview of ${file.name}`} />;
    case "video":
      return <video controls preload="metadata" src={contentURL} className="mx-auto max-h-[min(60dvh,680px)] max-w-full" aria-label={`Preview of ${file.name}`} />;
    case "unknown":
      return <PreviewMessage>This format has no inline preview. Use Download to retrieve the live file.</PreviewMessage>;
  }
}

function PreviewMessage({ children, tone = "muted", action }: { children: ReactNode; tone?: "muted" | "error"; action?: ReactNode }) {
  return (
    <div className={cn("mb-3 rounded-md border px-3 py-2 text-[12px] leading-5", tone === "error" ? "border-destructive/35 bg-destructive/5 text-destructive" : "border-border bg-muted/25 text-muted-foreground")} role={tone === "error" ? "alert" : undefined}>
      <div className="min-w-0 break-words">{children}</div>
      {action ? <div className="mt-2">{action}</div> : null}
    </div>
  );
}

function PreviewHeader({ file, onClose }: { file: HostFileEntry; onClose?: () => void }) {
  const Icon = fileIcon(file);
  return (
    <div className="min-w-0 border-b border-border px-4 py-3">
      <div className="flex min-w-0 items-start gap-2">
        <Icon className="mt-0.5 size-4 shrink-0 text-primary" aria-hidden="true" />
        <div className="min-w-0 flex-1">
          <div className="break-all text-sm font-semibold text-foreground">{file.name}</div>
          <div className="mt-1 text-[11px] text-muted-foreground">{file.kind === "directory" ? "Folder" : hostFilePreviewKind(file)}</div>
        </div>
        {onClose ? <Button type="button" variant="ghost" size="icon-sm" onClick={onClose} aria-label="Close preview" title="Close preview"><X /></Button> : null}
      </div>
      <div className="mt-3 flex min-w-0 items-start gap-1 rounded-md bg-muted/35 px-2 py-1.5">
        <code className="min-w-0 flex-1 select-text break-all font-mono text-[10px] leading-4 text-muted-foreground" title={file.path}>{file.path}</code>
        <CopyPathButton path={file.path} compact />
      </div>
      <div className="mt-2"><FileMetadata file={file} /></div>
    </div>
  );
}

function PreviewFooter({ file, kind }: { file: HostFileEntry; kind: HostFilePreviewKind }) {
  return (
    <div className="flex flex-col gap-2 border-t border-border bg-muted/50 p-3 sm:flex-row sm:items-center sm:justify-between">
      <span className="min-w-0 break-all text-left font-mono text-[10px] text-muted-foreground">{kind === "unknown" ? "metadata only" : kind}</span>
      <a className={buttonVariants({ variant: "outline", size: "sm" })} href={hostFileContentURL(file.path, "download")} download>
        <Download />
        Download
      </a>
    </div>
  );
}

type PreviewState = {
  text: string;
  loading: boolean;
  error: string;
  truncated: boolean;
};

export function HostFilePreviewDialog({
  file,
  open,
  onOpenChange,
  preview,
  onRetry,
  onOpenHostFile,
}: {
  file: HostFileEntry | null;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  preview: PreviewState;
  onRetry: () => void;
  onOpenHostFile?: (path: string) => void;
}) {
  if (!file) return null;

  const kind = hostFilePreviewKind(file);
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="h-dvh w-screen max-w-none grid-rows-[auto_minmax(0,1fr)_auto] gap-0 overflow-hidden rounded-none p-0 sm:h-[min(92dvh,920px)] sm:w-[min(96vw,1240px)] sm:max-w-none sm:rounded-xl">
        <DialogHeader className="min-w-0 border-b border-border px-4 py-3 pr-14">
          <DialogTitle className="break-all pr-2">{file.name}</DialogTitle>
          <DialogDescription className="min-w-0 space-y-2">
            <div className="flex min-w-0 items-start gap-1 rounded-md bg-muted/35 px-2 py-1.5">
              <code className="min-w-0 flex-1 select-text break-all font-mono text-[10px] leading-4" title={file.path}>{file.path}</code>
              <CopyPathButton path={file.path} compact />
            </div>
            <FileMetadata file={file} />
          </DialogDescription>
        </DialogHeader>
        <div className="min-h-0 overflow-auto px-4 py-4">
          <PreviewBody file={file} kind={kind} {...preview} onRetry={onRetry} onOpenHostFile={onOpenHostFile} />
        </div>
        <PreviewFooter file={file} kind={kind} />
      </DialogContent>
    </Dialog>
  );
}

function PathDisplay({ path, onEdit }: { path: string; onEdit: () => void }) {
  const handleKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key === "Enter" || event.key === " ") {
      event.preventDefault();
      onEdit();
    }
  };

  return (
    <div className="flex min-w-0 flex-1 items-center gap-1 rounded-md border border-border bg-muted/35 px-2 py-1.5">
      <div
        role="button"
        tabIndex={0}
        aria-label="Edit absolute Host path"
        title={path}
        onClick={onEdit}
        onKeyDown={handleKeyDown}
        className="min-w-0 flex-1 cursor-text focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/20"
      >
        <Breadcrumbs path={path} />
      </div>
      <CopyPathButton path={path} compact />
    </div>
  );
}

function DirectoryToolbar({
  path,
  pathDraft,
  pathError,
  editingPath,
  loading,
  onPathDraftChange,
  onPathSubmit,
  onStartEdit,
  onCancelEdit,
  onRefresh,
}: {
  path: string;
  pathDraft: string;
  pathError: string;
  editingPath: boolean;
  loading: boolean;
  onPathDraftChange: (value: string) => void;
  onPathSubmit: () => void;
  onStartEdit: () => void;
  onCancelEdit: () => void;
  onRefresh: () => void;
}) {
  const preserveEditOnBlur = useRef(false);

  return (
    <div className="min-w-0 space-y-2">
      <div className="flex min-w-0 flex-wrap items-center gap-2">
        <FolderOpen className="size-4 shrink-0 text-primary" aria-hidden="true" />
        <div className="min-w-0 flex-1">
          <div className="text-[15px] font-semibold text-foreground">Host files</div>
          <div className="mt-0.5 text-[11px] text-muted-foreground">Browse files readable by the Host process. Read only.</div>
        </div>
        <Button type="button" variant="outline" size="sm" onClick={onRefresh} disabled={loading} aria-label="Refresh host files">
          <RefreshCw className={cn(loading && "animate-spin")} />
          Refresh
        </Button>
      </div>
      {editingPath ? (
        <form
          aria-label="Go to absolute Host path"
          className="flex min-w-0 flex-wrap items-center gap-1.5"
          onSubmit={(event) => {
            event.preventDefault();
            preserveEditOnBlur.current = false;
            onPathSubmit();
          }}
          onBlur={(event) => {
            if (preserveEditOnBlur.current) return;
            onCancelEdit();
          }}
        >
          <div className="flex min-w-0 flex-1 items-center gap-1 rounded-md border border-ring bg-muted/35 px-2 ring-2 ring-ring/20">
            <input
              autoFocus
              type="text"
              value={pathDraft}
              onChange={(event) => onPathDraftChange(event.target.value)}
              aria-label="Absolute Host path"
              placeholder="/absolute/path"
              spellCheck={false}
              className="min-w-0 flex-1 bg-transparent py-1.5 font-mono text-[11px] leading-4 outline-none placeholder:text-muted-foreground/65"
            />
            <CopyPathButton path={path} compact />
          </div>
          <Button
            type="submit"
            variant="outline"
            size="sm"
            disabled={loading}
            onMouseDown={() => {
              preserveEditOnBlur.current = true;
            }}
          >
            Go
          </Button>
          <Button type="button" variant="ghost" size="sm" onClick={onCancelEdit}>
            Cancel
          </Button>
        </form>
      ) : (
        <div className="flex min-w-0 items-center gap-1.5">
          <PathDisplay path={path} onEdit={onStartEdit} />
        </div>
      )}
      {pathError ? <div className="min-w-0 break-words text-[11px] text-destructive" role="alert">{pathError}</div> : null}
    </div>
  );
}

function Breadcrumbs({ path }: { path: string }) {
  const items = breadcrumbs(path);

  return (
    <nav aria-label="Host file path" className="flex min-w-0 flex-wrap font-mono text-[11px]">
      {items.map((crumb, index, allItems) => (
        <span key={crumb.path} className="min-w-0 break-all">
          {index > 1 ? <span className="text-muted-foreground/50" aria-hidden="true">/</span> : null}
          <span className={cn("min-w-0 break-all font-mono", index === allItems.length - 1 ? "text-foreground" : "text-primary")} aria-current={index === allItems.length - 1 ? "page" : undefined}>{crumb.label}</span>
        </span>
      ))}
    </nav>
  );
}

function DirectoryEntry({ entry, selected, onOpen }: { entry: HostFileEntry; selected: boolean; onOpen: (entry: HostFileEntry) => void }) {
  const Icon = fileIcon(entry);
  const action = entry.kind === "directory" ? "Open directory" : "Preview file";
  return (
    <li className="flex min-w-0 items-center gap-1">
      <button
        type="button"
        onClick={() => onOpen(entry)}
        className={cn(
          "flex min-w-0 flex-1 items-center gap-2 rounded-md border px-2 py-1.5 text-left outline-none transition-colors focus-visible:border-ring focus-visible:ring-2 focus-visible:ring-ring/40",
          selected ? "border-primary/40 bg-primary/5" : "border-transparent hover:border-border hover:bg-muted/45",
        )}
        aria-label={`${action}: ${entry.name}`}
        aria-pressed={entry.kind === "file" ? selected : undefined}
        title={entry.path}
      >
        <Icon className="size-4 shrink-0 text-primary" aria-hidden="true" />
        <span className="min-w-0 flex-1 truncate text-[12px] font-medium text-foreground">{entry.name}</span>
        <span className="shrink-0 text-[9px] text-muted-foreground sm:hidden">{formatHostFileSize(entry.size)}</span>
        <span className="hidden min-w-0 max-w-[52%] shrink items-center justify-end gap-2 text-[9.5px] text-muted-foreground sm:flex">
          <span className={cn("min-w-0 truncate", entry.errorCode && "text-destructive")} title={entry.errorCode || undefined}>
            {entry.errorCode || (entry.kind === "directory" ? "Folder" : hostFilePreviewKind(entry))}
          </span>
          <span className="shrink-0">{formatHostFileSize(entry.size)}</span>
          <span className="hidden shrink-0 lg:inline">{formatHostFileModifiedAt(entry.modifiedAt)}</span>
        </span>
        {entry.errorCode ? (
          <span className="sr-only">{entry.errorCode}</span>
        ) : null}
      </button>
      <CopyPathButton path={entry.path} compact />
    </li>
  );
}

function compareEntries(left: HostFileEntry, right: HostFileEntry, sort: HostFileSort) {
  if (left.kind !== right.kind) return left.kind === "directory" ? -1 : 1;
  let result = 0;
  if (sort === "type") result = entryType(left).localeCompare(entryType(right), undefined, { sensitivity: "base" });
  if (sort === "size") result = left.size - right.size;
  if (sort === "modified") result = new Date(left.modifiedAt).getTime() - new Date(right.modifiedAt).getTime();
  if (result === 0) result = left.name.localeCompare(right.name, undefined, { sensitivity: "base" });
  return result;
}

function isAbsoluteHostPath(value: string) {
  const trimmed = value.trim();
  return trimmed === "/" || (trimmed.startsWith("/") && !trimmed.startsWith("//"));
}

export function HostFilesPane({ initialPath = "/", onOpenHostFile }: { initialPath?: string; onOpenHostFile?: (path: string) => void }) {
  const [path, setPath] = useState(initialPath || "/");
  const pathRef = useRef(path);
  pathRef.current = path;
  const [pathDraft, setPathDraft] = useState(initialPath || "/");
  const [editingPath, setEditingPath] = useState(false);
  const [pathError, setPathError] = useState("");
  const [homeError, setHomeError] = useState("");
  const [homePending, setHomePending] = useState(() => (initialPath || "/") === "/");
  const [homeRevision, setHomeRevision] = useState(0);
  const homePathRequestedRef = useRef(false);
  const pendingDirectPathRef = useRef<string | null>(null);
  const directPathBeforeRef = useRef(initialPath || "/");
  const [node, setNode] = useState<HostFileNode | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [previewFile, setPreviewFile] = useState<HostFileEntry | null>(null);
  const [previewText, setPreviewText] = useState("");
  const [previewLoading, setPreviewLoading] = useState(false);
  const [previewError, setPreviewError] = useState("");
  const [previewTruncated, setPreviewTruncated] = useState(false);
  const [previewRevision, setPreviewRevision] = useState(0);
  const [search, setSearch] = useState("");
  const [sort, setSort] = useState<HostFileSort>("name");
  const [revision, setRevision] = useState(0);
  useEffect(() => {
    const nextPath = initialPath || "/";
    if (nextPath === "/") {
      if (!homePathRequestedRef.current) {
        homePathRequestedRef.current = true;
        setHomePending(true);
        setHomeError("");
        setHomeRevision((current) => current + 1);
      }
      setPath("/");
      setPreviewFile(null);
    } else {
      homePathRequestedRef.current = false;
      setHomePending(false);
      setHomeError("");
      if (pathRef.current !== nextPath) {
        setPath(nextPath);
        setPreviewFile(null);
      }
    }
    setPathDraft(nextPath);
    setEditingPath(false);
    setPathError("");
    setSearch("");
  }, [initialPath]);

  useEffect(() => {
    if (homeRevision === 0) return;
    const controller = new AbortController();
    let current = true;
    setHomePending(true);
    setHomeError("");
    setError("");
    setNode(null);
    setLoading(true);
    const start = window.setTimeout(() => {
      fetchHostHome(controller.signal)
        .then((home) => {
          if (!current) return;
          setHomePending(false);
          setPath(home.path);
          setPathDraft(home.path);
          setPathError("");
        })
        .catch((reason: unknown) => {
          if (reason instanceof DOMException && reason.name === "AbortError") return;
          if (!current) return;
          const message = hostErrorMessage(reason);
          setHomePending(false);
          setHomeError(message);
          setError(`Could not resolve Host home: ${message}`);
          setLoading(false);
        });
    }, 0);
    return () => {
      current = false;
      window.clearTimeout(start);
      controller.abort();
    };
  }, [homeRevision]);

  useEffect(() => {
    if (homePending || homeError) return;
    const controller = new AbortController();
    let current = true;
    setLoading(true);
    setError("");
    setNode(null);
    setPathDraft(path);
    const start = window.setTimeout(() => {
      fetchHostFileNode(path, controller.signal)
        .then((nextNode) => {
          if (pendingDirectPathRef.current === path) {
            pendingDirectPathRef.current = null;
            onOpenHostFile?.(path);
          }
          setNode(nextNode);
          if (nextNode.kind === "file" && path !== "/") setPreviewFile(nextNode);
        })
        .catch((reason: unknown) => {
          if (reason instanceof DOMException && reason.name === "AbortError") return;
          if (pendingDirectPathRef.current === path) {
            pendingDirectPathRef.current = null;
            const fallback = directPathBeforeRef.current;
            setPathError(`Could not open path: ${hostErrorMessage(reason)}`);
            setPath(fallback);
            setPathDraft(fallback);
            return;
          }
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
  }, [homeError, homePending, path, revision]);

  const previewKind = previewFile ? hostFilePreviewKind(previewFile) : "unknown";
  useEffect(() => {
    if (!previewFile) {
      setPreviewText("");
      setPreviewLoading(false);
      setPreviewError("");
      setPreviewTruncated(false);
      return;
    }
    setPreviewText("");
    setPreviewError("");
    setPreviewTruncated(false);
    if (!previewFile.readable || (previewKind !== "text" && previewKind !== "markdown")) {
      setPreviewLoading(false);
      return;
    }

    const controller = new AbortController();
    let current = true;
    setPreviewLoading(true);
    const start = window.setTimeout(() => {
      fetchHostTextPreview(previewFile.path, MAX_HOST_PREVIEW_BYTES, controller.signal)
        .then((preview) => {
          setPreviewText(preview.text);
          setPreviewTruncated(preview.truncated);
        })
        .catch((reason: unknown) => {
          if (reason instanceof DOMException && reason.name === "AbortError") return;
          setPreviewError(hostErrorMessage(reason));
        })
        .finally(() => { if (current) setPreviewLoading(false); });
    }, 0);
    return () => {
      current = false;
      window.clearTimeout(start);
      controller.abort();
    };
  }, [previewFile?.path, previewFile?.readable, previewKind, previewRevision]);

  const preview = { text: previewText, loading: previewLoading, error: previewError, truncated: previewTruncated };
  const directory = node?.kind === "directory" ? (node as HostDirectory) : null;
  const entries = useMemo(() => {
    if (!directory) return [];
    const query = search.trim().toLocaleLowerCase();
    return [...directory.entries]
      .filter((entry) => !query || entry.name.toLocaleLowerCase().includes(query))
      .sort((left, right) => compareEntries(left, right, sort));
  }, [directory, search, sort]);

  const refresh = () => {
    setPathError("");
    if (homeError) {
      setHomePending(true);
      setHomeRevision((current) => current + 1);
      return;
    }
    setRevision((current) => current + 1);
  };
  const retryPreview = () => setPreviewRevision((current) => current + 1);
  const goHome = () => {
    pendingDirectPathRef.current = null;
    setPreviewFile(null);
    setEditingPath(false);
    setSearch("");
    setPathDraft("/");
    setPathError("");
    setHomeError("");
    setHomePending(true);
    homePathRequestedRef.current = true;
    setHomeRevision((current) => current + 1);
    setPath("/");
    onOpenHostFile?.("/");
  };
  const openEntry = (entry: HostFileEntry) => {
    if (entry.kind === "directory") {
      pendingDirectPathRef.current = null;
      setPreviewFile(null);
      setEditingPath(false);
      setSearch("");
      setPathDraft(entry.path);
      setPathError("");
      setPath(entry.path);
    } else {
      setPreviewFile(entry);
    }
  };
  const openPath = (nextPath: string) => {
    pendingDirectPathRef.current = null;
    setPreviewFile(null);
    setEditingPath(false);
    setSearch("");
    setPathDraft(nextPath);
    setPathError("");
    setPath(nextPath);
    onOpenHostFile?.(nextPath);
  };
  const submitPath = () => {
    const nextPath = pathDraft.trim();
    if (!isAbsoluteHostPath(nextPath)) {
      setPathError("Enter an absolute Host path starting with /.");
      return;
    }
    setEditingPath(false);
    setPathError("");
    setPreviewFile(null);
    setSearch("");
    setPathDraft(nextPath);
    if (nextPath === path) {
      setRevision((current) => current + 1);
      return;
    }
    directPathBeforeRef.current = path;
    pendingDirectPathRef.current = nextPath;
    setPath(nextPath);
  };

  const totalEntries = directory?.entries.length || 0;
  const resultLabel = search.trim() ? `${entries.length} of ${totalEntries} items` : `${totalEntries} ${totalEntries === 1 ? "item" : "items"}`;

  return (
    <main className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden bg-background" aria-label="Host files">
      <div className="shrink-0 border-b border-border px-4 py-3 md:px-6">
        <div className="mx-auto w-full max-w-none space-y-2">
          <DirectoryToolbar
            path={path}
            pathDraft={pathDraft}
            pathError={pathError}
            editingPath={editingPath}
            loading={loading}
            onPathDraftChange={(value) => {
              setPathDraft(value);
              if (pathError) setPathError("");
            }}
            onPathSubmit={submitPath}
            onStartEdit={() => {
              setPathDraft(path);
              setPathError("");
              setEditingPath(true);
            }}
            onCancelEdit={() => {
              setPathDraft(path);
              setPathError("");
              setEditingPath(false);
            }}
            onRefresh={refresh}
          />
        </div>
      </div>
      <div className="min-h-0 flex-1 overflow-hidden">
        <div data-testid="host-files-content" className="mx-auto flex h-full min-h-0 w-full max-w-none min-w-0 px-4 py-4 md:px-6 md:py-6">
          <section className="flex min-h-0 min-w-0 flex-1 flex-col" aria-label="Host file list">
            {error ? (
              <div className="min-h-0 flex-1 overflow-y-auto">
                <div className="rounded-md border border-destructive/35 bg-destructive/5 px-3 py-3 text-[12px] leading-5 text-destructive" role="alert">
                  <div className="flex items-start gap-2">
                    <AlertCircle className="mt-0.5 size-4 shrink-0" />
                    <span className="min-w-0 flex-1 break-words">Failed to read directory: {error}</span>
                  </div>
                  <div className="mt-3 flex flex-wrap gap-2">
                    <Button type="button" variant="outline" size="sm" onClick={refresh}>Retry</Button>
                    <Button type="button" variant="ghost" size="sm" onClick={goHome}>Go to Home</Button>
                  </div>
                </div>
              </div>
            ) : directory ? (
              <>
                <div className="mb-3 flex min-w-0 shrink-0 flex-col gap-3">
                  <div className="flex min-w-0 flex-wrap items-center justify-between gap-2">
                    <div className="min-w-0 break-words text-[12px] text-muted-foreground">{resultLabel} · hidden files included</div>
                    {directory.readable ? null : <span className="text-[11px] text-destructive">not readable</span>}
                  </div>
                  <div className="flex min-w-0 flex-col gap-2 sm:flex-row">
                    <label className="flex min-w-0 flex-1 items-center gap-2 rounded-md border border-border bg-card px-2.5 focus-within:border-ring focus-within:ring-2 focus-within:ring-ring/20">
                      <Search className="size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
                      <span className="sr-only">Search current directory</span>
                      <input
                        type="search"
                        value={search}
                        onChange={(event) => setSearch(event.target.value)}
                        placeholder="Search current directory"
                        aria-label="Search current directory"
                        className="min-w-0 flex-1 bg-transparent py-1.5 text-[12px] outline-none placeholder:text-muted-foreground/65"
                      />
                    </label>
                    <label className="flex shrink-0 items-center gap-2 rounded-md border border-border bg-card px-2.5">
                      <ArrowDownUp className="size-3.5 text-muted-foreground" aria-hidden="true" />
                      <span className="sr-only">Sort files</span>
                      <select value={sort} onChange={(event) => setSort(event.target.value as HostFileSort)} aria-label="Sort files" className="bg-transparent py-1.5 text-[12px] outline-none">
                        <option value="name">Name</option>
                        <option value="type">Type</option>
                        <option value="size">Size</option>
                        <option value="modified">Modified</option>
                      </select>
                    </label>
                  </div>
                </div>
                <div data-testid="host-file-list-scroll" className="min-h-0 flex-1 overflow-y-auto pr-1">
                  {totalEntries === 0 ? (
                    <div className="rounded-xl border border-dashed border-border bg-card/60 px-4 py-12 text-center text-[12px] text-muted-foreground">
                      <FolderOpen className="mx-auto size-6 text-muted-foreground/60" aria-hidden="true" />
                      <div className="mt-3 font-medium text-foreground">This directory is empty</div>
                      <p className="mt-1">Files created on the Host will appear here after Refresh.</p>
                    </div>
                  ) : entries.length === 0 ? (
                    <div className="rounded-xl border border-dashed border-border bg-card/60 px-4 py-12 text-center text-[12px] text-muted-foreground">
                      <Search className="mx-auto size-6 text-muted-foreground/60" aria-hidden="true" />
                      <div className="mt-3 font-medium text-foreground">No items found</div>
                      <p className="mt-1">Try a different name in this directory.</p>
                    </div>
                  ) : (
                    <ul className="space-y-1" aria-label="Host file entries" data-density="compact">
                      {entries.map((entry) => <DirectoryEntry key={entry.path} entry={entry} selected={previewFile?.path === entry.path} onOpen={openEntry} />)}
                    </ul>
                  )}
                </div>
              </>
            ) : node?.kind === "file" ? (
              <div className="min-h-0 flex-1 overflow-y-auto">
                <div className="rounded-xl border border-border bg-card px-4 py-4">
                  <div className="break-all text-sm font-medium">{node.name}</div>
                  <div className="mt-2 flex items-start gap-1"><code className="min-w-0 flex-1 select-text break-all font-mono text-[10px] text-muted-foreground">{node.path}</code><CopyPathButton path={node.path} compact /></div>
                  <div className="mt-2"><FileMetadata file={node} /></div>
                  <p className="mt-3 text-[12px] text-muted-foreground">The file preview is open. Use Download for the live file.</p>
                </div>
              </div>
            ) : loading ? (
              <div className="flex min-h-0 flex-1 items-center justify-center gap-2 overflow-y-auto text-sm text-muted-foreground" role="status"><Loader2 className="size-4 animate-spin" />Loading files…</div>
            ) : null}
          </section>
        </div>
      </div>
      <HostFilePreviewDialog file={previewFile} open={Boolean(previewFile)} onOpenChange={(open) => { if (!open) setPreviewFile(null); }} preview={preview} onRetry={retryPreview} onOpenHostFile={openPath} />
    </main>
  );
}
