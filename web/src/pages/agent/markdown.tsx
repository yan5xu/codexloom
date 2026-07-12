import { Streamdown, defaultRehypePlugins } from "streamdown";
import { harden } from "rehype-harden";
import rehypeSanitize, { defaultSchema } from "rehype-sanitize";
import { code } from "@streamdown/code";
import { cjk } from "@streamdown/cjk";
import { createMathPlugin } from "@streamdown/math";
import { useEffect, useState } from "react";
import type { PluggableList } from "unified";
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

export function MarkdownContent({ content, streaming = false }: { content: string; streaming?: boolean }) {
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
    <div className="max-w-none break-words prose">
      <Streamdown
        plugins={plugins}
        rehypePlugins={customRehypePlugins}
        isAnimating={streaming}
      >
        {content}
      </Streamdown>
    </div>
  );
}
