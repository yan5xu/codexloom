import { MarkdownContent } from "./pages/agent/markdown";
import type { Block } from "./feed";

// Maps sys-line classes onto the warm cashmere semantic palette.
const sysColor: Record<string, string> = {
  ok: "text-success",
  warn: "text-warning",
  err: "text-destructive",
  dim: "text-muted-foreground",
};

function tsShort(ts: string) {
  return ts && ts.length >= 19 ? ts.slice(11, 19) : ts;
}

export function BlockView({ block }: { block: Block }) {
  switch (block.kind) {
    // User turn — plain text with a tracked uppercase label (mirrors UserBubble).
    case "user":
      return (
        <div className="py-3">
          <div className="mb-1 flex items-center gap-2">
            <span className="text-[9px] font-bold uppercase tracking-[0.15em] text-muted-foreground">
              you
            </span>
            <span className="font-mono text-[10px] text-muted-foreground/50">{tsShort(block.ts)}</span>
          </div>
          <div className="whitespace-pre-wrap break-words text-sm">{block.text}</div>
        </div>
      );

    // Agent reply — final answer carries a primary left-border accent.
    case "agent":
      return (
        <div className="py-3">
          <div className="mb-1.5 flex items-center gap-2">
            <span className="text-[9px] font-bold uppercase tracking-[0.15em] text-primary">agent</span>
            {block.streaming && (
              <span className="flex items-center gap-1.5 font-mono text-[10px] text-primary/70">
                <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-primary" />
                streaming
              </span>
            )}
          </div>
          <div
            className={`border-l-2 border-primary/30 pl-3 text-sm leading-relaxed ${
              block.streaming ? "cursor-blink" : ""
            }`}
          >
            <MarkdownContent content={block.text} streaming={block.streaming} />
          </div>
        </div>
      );

    // Reasoning — collapsible, sunken muted tone.
    case "think":
      return (
        <details className="group/reason py-1" open={!block.done}>
          <summary className="flex cursor-pointer list-none select-none items-center gap-2 py-1 text-[11px] text-muted-foreground/60 transition-colors hover:text-muted-foreground">
            <span className="text-[10px] transition-transform group-open/reason:rotate-90">▶</span>
            <span className="font-mono tracking-wide">{block.done ? "reasoning" : "reasoning…"}</span>
          </summary>
          <pre className="mt-1 whitespace-pre-wrap border-l-2 border-muted-foreground/15 pl-4 font-sans text-[12.5px] leading-relaxed text-muted-foreground/70">
            {block.text}
          </pre>
        </details>
      );

    // Command execution — bordered card with a status chip and recessed output tray.
    case "command": {
      const finished = block.exitCode !== null || block.status === "completed" || block.status === "failed";
      const ok = block.exitCode === 0;
      return (
        <details className="card my-2 overflow-hidden rounded-xl border border-border bg-card shadow-card">
          <summary className="flex cursor-pointer select-none items-center gap-2 px-3 py-2.5">
            <span className="flex-1 truncate font-mono text-[12.5px]">{block.command}</span>
            {finished ? (
              <span
                className={`shrink-0 rounded-lg px-2 py-0.5 text-[10.5px] font-medium ${
                  ok ? "bg-success/10 text-success" : "bg-destructive/10 text-destructive"
                }`}
              >
                exit {block.exitCode ?? "?"}
                {block.durationMs != null ? ` · ${block.durationMs}ms` : ""}
              </span>
            ) : (
              <span className="flex shrink-0 items-center gap-1.5 rounded-lg bg-warning/10 px-2 py-0.5 text-[10.5px] font-medium text-warning">
                <span className="spinner !h-2.5 !w-2.5" />
                running
              </span>
            )}
          </summary>
          <pre className="max-h-80 overflow-auto whitespace-pre-wrap border-t border-border bg-muted/40 px-3.5 py-2.5 font-mono text-[12px] text-muted-foreground">
            {block.output || "(no output)"}
          </pre>
        </details>
      );
    }

    // File change — file paths in primary, diff coloured add/del/context.
    case "file":
      return (
        <details className="card my-2 overflow-hidden rounded-xl border border-border bg-card shadow-card" open>
          <summary className="flex cursor-pointer select-none items-center gap-2 px-3 py-2.5">
            <span className="flex-1 truncate font-mono text-[12.5px] text-primary">
              {block.changes.map((c) => `${c.kind} ${c.path}`).join(", ") || "file change"}
            </span>
            <span className="shrink-0 rounded-lg bg-success/10 px-2 py-0.5 text-[10.5px] font-medium text-success">
              {block.status}
            </span>
          </summary>
          <pre className="max-h-80 overflow-auto border-t border-border bg-muted/40 px-3.5 py-2.5 font-mono text-[12px]">
            {block.changes.map((c, i) => (
              <span key={i}>
                {c.diff
                  ? c.diff.split("\n").map((line, j) => (
                      <span
                        key={j}
                        className={
                          line.startsWith("+")
                            ? "text-add"
                            : line.startsWith("-")
                              ? "text-del"
                              : "text-muted-foreground"
                        }
                      >
                        {line}
                        {"\n"}
                      </span>
                    ))
                  : "(no diff)\n"}
              </span>
            ))}
          </pre>
        </details>
      );

    // Generated image — rendered inline from its base64 data URI.
    case "image":
      return (
        <div className="my-2">
          <img
            src={block.data}
            alt="generated"
            className="max-w-md rounded-xl border border-border shadow-card"
          />
        </div>
      );

    // System line — quiet meta with a mono timestamp.
    case "sys":
      return (
        <div className={`py-0.5 text-[12px] ${sysColor[block.cls]}`}>
          <span className="mr-2 font-mono text-[10.5px] text-muted-foreground/50">{tsShort(block.ts)}</span>
          {block.text}
        </div>
      );

    // Unrecognised item — raw JSON, collapsed.
    case "raw":
      return (
        <details className="card my-2 overflow-hidden rounded-xl border border-border bg-card">
          <summary className="cursor-pointer select-none px-3 py-2 font-mono text-[12.5px] text-muted-foreground">
            {block.type}
          </summary>
          <pre className="max-h-80 overflow-auto whitespace-pre-wrap border-t border-border bg-muted/40 px-3.5 py-2.5 font-mono text-[12px] text-muted-foreground">
            {block.json}
          </pre>
        </details>
      );
  }
}
