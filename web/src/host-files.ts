export type ExplicitHostPathSource = "backtick" | "markdown" | "file-url";

export type HostFileKind = "directory" | "file";

export type HostFilePreviewKind = "text" | "markdown" | "image" | "pdf" | "audio" | "video" | "unknown";

export interface HostFileEntry {
  path: string;
  name: string;
  kind: HostFileKind;
  size: number;
  modifiedAt: string;
  mode: string;
  readable: boolean;
  mimeType?: string;
  errorCode?: string;
}

export interface HostDirectory extends HostFileEntry {
  kind: "directory";
  entries: HostFileEntry[];
}

export type HostFileNode = HostDirectory | HostFileEntry;

export interface HostFileErrorPayload {
  error?: {
    code?: string;
    message?: string;
  };
}

export class HostFileRequestError extends Error {
  readonly status: number;
  readonly code: string;

  constructor(message: string, status: number, code = "request_failed") {
    super(message);
    this.name = "HostFileRequestError";
    this.status = status;
    this.code = code;
  }
}

export interface HostTextPreview {
  text: string;
  truncated: boolean;
  limit: number;
}

export const DEFAULT_HOST_PREVIEW_BYTES = 256 * 1024;
export const MAX_HOST_PREVIEW_BYTES = 1024 * 1024;

export interface ExplicitHostPath {
  path: string;
  label: string;
  source: ExplicitHostPathSource;
  start: number;
  end: number;
}

export type HostPathToken =
  | { type: "text"; value: string }
  | { type: "path"; value: ExplicitHostPath };

function hostFileQuery(path: string, extra: Record<string, string> = {}) {
  const params = new URLSearchParams({ path, ...extra });
  return params.toString();
}

async function hostFileResponseError(response: Response): Promise<never> {
  let payload: HostFileErrorPayload = {};
  try {
    payload = (await response.json()) as HostFileErrorPayload;
  } catch {
    // Keep the status useful even when a proxy returns a non-JSON error.
  }
  const code = payload.error?.code || "request_failed";
  const message = payload.error?.message || `Host file request failed (${response.status})`;
  throw new HostFileRequestError(message, response.status, code);
}

async function hostFileFetch(path: string, init?: RequestInit) {
  const response = await fetch(`/api/files?${hostFileQuery(path)}`, {
    credentials: "same-origin",
    ...init,
  });
  if (!response.ok) await hostFileResponseError(response);
  return response;
}

export async function fetchHostFileNode(path: string, signal?: AbortSignal): Promise<HostFileNode> {
  const response = await hostFileFetch(path, { signal });
  return (await response.json()) as HostFileNode;
}

export async function fetchHostTextPreview(path: string, maxBytes = MAX_HOST_PREVIEW_BYTES, signal?: AbortSignal): Promise<HostTextPreview> {
  const requested = Number.isFinite(maxBytes) ? Math.floor(maxBytes) : DEFAULT_HOST_PREVIEW_BYTES;
  const limit = Math.min(Math.max(requested, 1), MAX_HOST_PREVIEW_BYTES);
  const response = await fetch(`/api/files/preview?${hostFileQuery(path, { maxBytes: String(limit) })}`, {
    credentials: "same-origin",
    signal,
  });
  if (!response.ok) await hostFileResponseError(response);
  const text = await response.text();
  return {
    text,
    truncated: response.headers.get("X-Codex-Loom-Preview-Truncated") === "true",
    limit: Number(response.headers.get("X-Codex-Loom-Preview-Limit")) || limit,
  };
}

export function hostFileContentURL(path: string, disposition: "preview" | "download") {
  return `/api/files/content?${hostFileQuery(path, disposition === "preview" ? { preview: "1" } : { download: "1" })}`;
}

function extensionOf(path: string) {
  const name = path.split("/").pop() || path;
  const dot = name.lastIndexOf(".");
  return dot >= 0 ? name.slice(dot + 1).toLowerCase() : "";
}

const markdownExtensions = new Set(["md", "markdown", "mdown", "mkd"]);
const audioExtensions = new Set(["aac", "flac", "m4a", "mp3", "oga", "ogg", "wav", "weba"]);
const videoExtensions = new Set(["avi", "m4v", "mkv", "mov", "mp4", "ogv", "webm"]);
const textExtensions = new Set([
  "c", "cc", "cpp", "css", "csv", "go", "h", "hpp", "html", "java", "js", "json", "jsx", "log", "mjs", "py", "rb", "rs", "sh", "sql", "svg", "toml", "ts", "tsx", "txt", "xml", "yaml", "yml",
]);

export function hostFilePreviewKind(file: Pick<HostFileEntry, "path" | "mimeType">): HostFilePreviewKind {
  const mime = (file.mimeType || "").toLowerCase().split(";", 1)[0].trim();
  const extension = extensionOf(file.path);
  if (mime === "application/pdf" || extension === "pdf") return "pdf";
  if (mime.startsWith("image/")) return "image";
  if (mime.startsWith("audio/")) return "audio";
  if (mime.startsWith("video/")) return "video";
  if (audioExtensions.has(extension)) return "audio";
  if (videoExtensions.has(extension)) return "video";
  if (mime === "text/markdown" || markdownExtensions.has(extension)) return "markdown";
  if (mime.startsWith("text/") || mime === "application/json" || mime === "application/javascript" || mime === "application/xml" || textExtensions.has(extension)) return "text";
  return "unknown";
}

export function formatHostFileSize(bytes: number) {
  if (!Number.isFinite(bytes) || bytes < 0) return "—";
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 ** 2) return `${(bytes / 1024).toFixed(1)} KB`;
  if (bytes < 1024 ** 3) return `${(bytes / 1024 ** 2).toFixed(1)} MB`;
  return `${(bytes / 1024 ** 3).toFixed(1)} GB`;
}

export function formatHostFileModifiedAt(value: string) {
  if (!value) return "—";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
}

function trimFileUrlPunctuation(value: string) {
  return value.replace(/[.,;!?]+$/g, "");
}

function markdownDestination(value: string) {
  const trimmed = value.trim();
  if (trimmed.startsWith("<")) {
    const closing = trimmed.indexOf(">");
    return closing > 0 ? trimmed.slice(1, closing) : trimmed;
  }
  return trimmed.split(/\s+/, 1)[0] || "";
}

/**
 * Converts only an absolute local path marker into a Host path.
 * Relative paths, bare text and non-file URLs intentionally return null.
 */
export function absoluteHostPathFromHref(raw: string): string | null {
  const value = raw.trim();
  if (value.startsWith("/") && !value.startsWith("//")) return value;
  if (!value.toLowerCase().startsWith("file://")) return null;

  try {
    const url = new URL(value);
    if (url.protocol !== "file:") return null;
    if (url.hostname && url.hostname !== "localhost") return null;
    const path = decodeURIComponent(url.pathname);
    return path.startsWith("/") ? path : null;
  } catch {
    return null;
  }
}

function addMatch(
  matches: ExplicitHostPath[],
  start: number,
  end: number,
  label: string,
  href: string,
  source: ExplicitHostPathSource,
) {
  const normalizedHref = source === "file-url"
    ? trimFileUrlPunctuation(href)
    : source === "markdown"
      ? markdownDestination(href)
      : href.trim();
  const path = absoluteHostPathFromHref(normalizedHref);
  if (!path) return;
  const normalizedLabel = source === "file-url" ? label.slice(0, normalizedHref.length) : label.trim();
  const normalizedEnd = source === "file-url" ? Math.min(end, start + normalizedHref.length) : end;
  matches.push({ path, label: normalizedLabel || path, source, start, end: normalizedEnd });
}

export function findExplicitHostPaths(text: string): ExplicitHostPath[] {
  const matches: ExplicitHostPath[] = [];

  const markdown = /\[([^\]\r\n]+)\]\(([^)\r\n]+)\)/g;
  for (const match of text.matchAll(markdown)) {
    const start = match.index ?? 0;
    addMatch(matches, start, start + match[0].length, match[1], match[2], "markdown");
  }

  const backticks = /`([^`\r\n]+)`/g;
  for (const match of text.matchAll(backticks)) {
    const start = match.index ?? 0;
    addMatch(matches, start, start + match[0].length, match[1], match[1], "backtick");
  }

  const fileUrls = /file:\/\/[^\s<>"')\]]+/gi;
  for (const match of text.matchAll(fileUrls)) {
    const start = match.index ?? 0;
    addMatch(matches, start, start + match[0].length, match[0], match[0], "file-url");
  }

  return matches
    .sort((left, right) => left.start - right.start || left.end - right.end)
    .filter((match, index, all) => index === 0 || match.start >= all[index - 1].end);
}

export function tokenizeExplicitHostPaths(text: string): HostPathToken[] {
  const matches = findExplicitHostPaths(text);
  if (matches.length === 0) return [{ type: "text", value: text }];

  const tokens: HostPathToken[] = [];
  let cursor = 0;
  for (const match of matches) {
    if (match.start > cursor) tokens.push({ type: "text", value: text.slice(cursor, match.start) });
    tokens.push({ type: "path", value: match });
    cursor = match.end;
  }
  if (cursor < text.length) tokens.push({ type: "text", value: text.slice(cursor) });
  return tokens;
}
