import { Streamdown, defaultRehypePlugins } from "streamdown";
import type { LinkSafetyConfig, LinkSafetyModalProps } from "streamdown";
import { harden } from "rehype-harden";
import rehypeSanitize, { defaultSchema } from "rehype-sanitize";
import { code } from "@streamdown/code";
import { cjk } from "@streamdown/cjk";
import { createMathPlugin } from "@streamdown/math";
import { Check, Copy, ExternalLink, FileText, X } from "lucide-react";
import { useEffect, useState } from "react";
import type { PluggableList } from "unified";
import { cn } from "../../lib/utils";
import { api } from "../../types";
import "katex/dist/katex.min.css";

/* ================================================================
   Rehype plugins — sanitize + harden for safe HTML rendering
   ================================================================ */

const sanitizeSchema: typeof defaultSchema = {
  ...defaultSchema,
  protocols: {
    ...defaultSchema.protocols,
    src: [...(defaultSchema.protocols?.src || []), "pinix-data", "pinix-web"],
  },
  attributes: {
    ...defaultSchema.attributes,
    code: [...((defaultSchema.attributes?.code as string[]) || []), "metastring"],
  },
};

const math = createMathPlugin({ singleDollarTextMath: true });

const customRehypePlugins: PluggableList = [
  defaultRehypePlugins.raw,
  [rehypeSanitize, sanitizeSchema],
  [harden, {
    allowedImagePrefixes: ["*"],
    allowedLinkPrefixes: ["*"],
    allowedProtocols: ["*"],
    defaultOrigin: undefined,
    allowDataImages: true,
  }],
];

const localPathRoots = /^\/(?:Users|home|Volumes|private|tmp|var|opt|srv|mnt)\//;
const windowsAbsolutePath = /^[A-Za-z]:[\\/]/;

export function localPathFromHref(href: string): string | null {
  const value = href.trim();
  if (localPathRoots.test(value) || windowsAbsolutePath.test(value)) return value;
  if (!value.toLowerCase().startsWith("file://")) return null;
  try {
    const parsed = new URL(value);
    if (parsed.protocol !== "file:" || (parsed.hostname && parsed.hostname !== "localhost")) return null;
    return decodeURIComponent(parsed.pathname);
  } catch {
    return null;
  }
}

function MarkdownLinkSafetyModal({ url, isOpen, onClose, onConfirm }: LinkSafetyModalProps) {
  const localPath = localPathFromHref(url);
  const [copied, setCopied] = useState(false);
  const [opening, setOpening] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (!isOpen) return;
    setCopied(false);
    setOpening(false);
    setError("");
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [isOpen, onClose, url]);

  if (!isOpen) return null;

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(localPath || url);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 2000);
    } catch {
      setError(localPath ? "无法复制文件路径。" : "无法复制链接。")
    }
  };

  const open = async () => {
    if (!localPath) {
      onConfirm();
      onClose();
      return;
    }
    setOpening(true);
    setError("");
    try {
      await api("POST", "/api/files/open", { path: localPath });
      onClose();
    } catch (cause) {
      setError(`打开失败：${cause instanceof Error ? cause.message : "未知错误"}`);
      setOpening(false);
    }
  };

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-background/50 backdrop-blur-sm"
      onClick={onClose}
      role="presentation"
    >
      <div
        aria-modal="true"
        className="relative mx-4 flex w-full max-w-md flex-col gap-4 rounded-xl border bg-background p-6 shadow-lg"
        onClick={(event) => event.stopPropagation()}
        role="dialog"
      >
        <button
          aria-label="关闭"
          className="absolute top-4 right-4 rounded-md p-1 text-muted-foreground transition-all hover:bg-muted hover:text-foreground"
          onClick={onClose}
          type="button"
        >
          <X size={16} />
        </button>
        <div className="flex flex-col gap-2">
          <div className="flex items-center gap-2 pr-8 text-lg font-semibold">
            {localPath ? <FileText size={20} /> : <ExternalLink size={20} />}
            <span>{localPath ? "打开本地文件？" : "打开外部链接？"}</span>
          </div>
          <p className="text-sm text-muted-foreground">
            {localPath ? "将使用系统默认应用打开此文件。" : "即将访问外部网站。"}
          </p>
        </div>
        <div className="max-h-32 overflow-y-auto break-all rounded-md bg-muted p-3 font-mono text-sm">
          {localPath || url}
        </div>
        {error ? <p className="text-sm text-destructive" role="alert">{error}</p> : null}
        <div className="flex gap-2">
          <button
            className="flex flex-1 items-center justify-center gap-2 rounded-md border bg-background px-4 py-2 text-sm font-medium transition-all hover:bg-muted"
            onClick={copy}
            type="button"
          >
            {copied ? <Check size={14} /> : <Copy size={14} />}
            <span>{copied ? "已复制" : localPath ? "复制路径" : "复制链接"}</span>
          </button>
          <button
            className="flex flex-1 items-center justify-center gap-2 rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground transition-all hover:bg-primary/90 disabled:cursor-wait disabled:opacity-60"
            disabled={opening}
            onClick={open}
            type="button"
          >
            {localPath ? <FileText size={14} /> : <ExternalLink size={14} />}
            <span>{opening ? "正在打开…" : localPath ? "打开文件" : "打开链接"}</span>
          </button>
        </div>
      </div>
    </div>
  );
}

const markdownLinkSafety: LinkSafetyConfig = {
  enabled: true,
  renderModal: (props) => <MarkdownLinkSafetyModal {...props} />,
};

/* ================================================================
   MarkdownContent — Streamdown-based markdown renderer
   ================================================================ */

export function MarkdownContent({ content, streaming = false, className }: { content: string; streaming?: boolean; className?: string }) {
  const needsMermaid = /```mermaid\b/i.test(content);
  const [mermaidPlugin, setMermaidPlugin] = useState<typeof import("@streamdown/mermaid")["mermaid"] | null>(null);

  useEffect(() => {
    if (!needsMermaid || mermaidPlugin) return;
    let cancelled = false;
    import("@streamdown/mermaid").then((module) => {
      if (!cancelled) setMermaidPlugin(() => module.mermaid);
    });
    return () => {
      cancelled = true;
    };
  }, [needsMermaid, mermaidPlugin]);

  if (!content && !streaming) return null;

  const plugins = {
    code,
    cjk,
    math,
    ...(mermaidPlugin ? { mermaid: mermaidPlugin } : {}),
  };

  return (
    <div data-i18n-preserve className={cn("max-w-none break-words prose", className)}>
      <Streamdown
        plugins={plugins}
        rehypePlugins={customRehypePlugins}
        isAnimating={streaming}
        linkSafety={markdownLinkSafety}
      >
        {content}
      </Streamdown>
    </div>
  );
}
