import { Streamdown, defaultRehypePlugins } from "streamdown";
import { harden } from "rehype-harden";
import rehypeSanitize, { defaultSchema } from "rehype-sanitize";
import { code } from "@streamdown/code";
import { cjk } from "@streamdown/cjk";
import { createMathPlugin } from "@streamdown/math";
import { useEffect, useState } from "react";
import type { PluggableList } from "unified";
import type { ComponentProps, ReactNode } from "react";
import { cn } from "../../lib/utils";
import { absoluteHostPathFromHref } from "../../host-files";
import { plantumlRenderer } from "./plantuml";
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

/* ================================================================
   MarkdownContent — Streamdown-based markdown renderer
   ================================================================ */

export function MarkdownContent({ content, streaming = false, className, onOpenHostFile }: { content: string; streaming?: boolean; className?: string; onOpenHostFile?: (path: string) => void }) {
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
    renderers: [plantumlRenderer],
    ...(mermaidPlugin ? { mermaid: mermaidPlugin } : {}),
  };

  const components = onOpenHostFile
    ? {
        a: ({ href, children, ...props }: ComponentProps<"a"> & { node?: unknown }) => {
          const path = href ? absoluteHostPathFromHref(href) : null;
          if (!path) return <a href={href} {...props}>{children}</a>;
          return (
            <button
              type="button"
              className="cursor-pointer break-all text-primary underline decoration-dotted underline-offset-2 hover:text-primary/80 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
              title={path}
              aria-label={`Open host file ${path}`}
              onClick={() => onOpenHostFile(path)}
            >
              {children as ReactNode}
            </button>
          );
        },
      }
    : undefined;

  return (
    <div className={cn("max-w-none break-words prose", className)}>
      <Streamdown
        plugins={plugins}
        rehypePlugins={customRehypePlugins}
        components={components}
        isAnimating={streaming}
      >
        {content}
      </Streamdown>
    </div>
  );
}
