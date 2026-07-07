import { UserBubble, AssistantBubble } from "./pages/agent/MessageBubbles";
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
    // User turn — rendered by the ported UserBubble (verbatim topic component).
    case "user":
      return (
        <UserBubble
          message={{ id: "u", topic_id: "", role: "user", content: block.text, created_at: block.ts }}
        />
      );

    // Agent reply — rendered by the ported AssistantBubble (verbatim topic
    // component). A minimal single-iteration group carries the text.
    case "agent": {
      const msg = { id: block.id, topic_id: "", role: "assistant" as const, content: block.text, created_at: "" };
      return (
        <AssistantBubble
          group={{
            type: "assistant",
            message: msg,
            steps: [],
            iterations: [{ thinking: null, text: block.text, tools: [] }],
            usage: null,
          }}
          streaming={block.streaming}
        />
      );
    }

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
        <div className="my-3 overflow-hidden rounded-xl border border-border/60 bg-card shadow-card">
          <img
            src={block.data}
            alt="generated"
            className="block h-auto max-h-[70vh] w-full object-contain"
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
