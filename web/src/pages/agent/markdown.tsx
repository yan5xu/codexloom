import { Streamdown, defaultRehypePlugins } from "streamdown";
import { harden } from "rehype-harden";
import rehypeSanitize, { defaultSchema } from "rehype-sanitize";
import { code } from "@streamdown/code";
import { cjk } from "@streamdown/cjk";
import { createMathPlugin } from "@streamdown/math";
import { mermaid } from "@streamdown/mermaid";
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
  if (!content && !streaming) return null;

  return (
    <div className="max-w-none break-words prose">
      <Streamdown
        plugins={{ code, cjk, math, mermaid }}
        rehypePlugins={customRehypePlugins}
        isAnimating={streaming}
      >
        {content}
      </Streamdown>
    </div>
  );
}
