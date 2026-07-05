import type { Block } from "./feed";

const sysColor: Record<string, string> = {
  ok: "text-ok",
  warn: "text-warn",
  err: "text-err",
  dim: "text-dim",
};

function tsShort(ts: string) {
  return ts && ts.length >= 19 ? ts.slice(11, 19) : ts;
}

export function BlockView({ block }: { block: Block }) {
  switch (block.kind) {
    case "user":
      return (
        <div className="mb-2.5 max-w-4xl rounded-lg border border-[#1f4267] bg-[#12233a] px-3.5 py-2.5">
          <div className="mb-1 text-[11px] font-semibold text-accent">
            USER <span className="ml-2 font-normal text-dim">{tsShort(block.ts)}</span>
          </div>
          <div className="whitespace-pre-wrap">{block.text}</div>
        </div>
      );

    case "agent":
      return (
        <div
          className={`mb-2.5 max-w-4xl whitespace-pre-wrap px-0.5 py-1 leading-relaxed ${
            block.streaming ? "cursor-blink" : ""
          }`}
        >
          {block.text}
        </div>
      );

    case "think":
      return (
        <details className="mb-2.5 max-w-4xl text-dim" open={!block.done}>
          <summary className="cursor-pointer select-none text-[11.5px]">
            {block.done ? "thinking (done)" : "thinking…"}
          </summary>
          <pre className="mt-1 whitespace-pre-wrap border-l-2 border-line pl-3 font-sans text-[12.5px]">
            {block.text}
          </pre>
        </details>
      );

    case "command": {
      const finished = block.exitCode !== null || block.status === "completed" || block.status === "failed";
      const ok = block.exitCode === 0;
      return (
        <details className="card mb-2.5 max-w-4xl overflow-hidden rounded-lg border border-line bg-panel">
          <summary className="flex cursor-pointer select-none items-center gap-2 px-3 py-2">
            <span className="flex-1 truncate font-mono text-[12.5px]">{block.command}</span>
            {finished ? (
              <span
                className={`shrink-0 rounded-lg px-2 py-px text-[10.5px] ${
                  ok ? "bg-[#12351c] text-ok" : "bg-[#3d1418] text-err"
                }`}
              >
                exit {block.exitCode ?? "?"}
                {block.durationMs != null ? ` · ${block.durationMs}ms` : ""}
              </span>
            ) : (
              <span className="shrink-0 rounded-lg bg-[#3a2c10] px-2 py-px text-[10.5px] text-warn">
                running
              </span>
            )}
          </summary>
          <pre className="max-h-80 overflow-auto whitespace-pre-wrap border-t border-line px-3.5 py-2.5 font-mono text-[12px] text-dim">
            {block.output || "(no output)"}
          </pre>
        </details>
      );
    }

    case "file":
      return (
        <details className="card mb-2.5 max-w-4xl overflow-hidden rounded-lg border border-line bg-panel" open>
          <summary className="flex cursor-pointer select-none items-center gap-2 px-3 py-2">
            <span className="flex-1 truncate font-mono text-[12.5px] text-grape">
              {block.changes.map((c) => `${c.kind} ${c.path}`).join(", ") || "file change"}
            </span>
            <span className="shrink-0 rounded-lg bg-[#12351c] px-2 py-px text-[10.5px] text-ok">
              {block.status}
            </span>
          </summary>
          <pre className="max-h-80 overflow-auto border-t border-line px-3.5 py-2.5 font-mono text-[12px]">
            {block.changes.map((c, i) => (
              <span key={i}>
                {c.diff
                  ? c.diff.split("\n").map((line, j) => (
                      <span
                        key={j}
                        className={
                          line.startsWith("+") ? "text-ok" : line.startsWith("-") ? "text-err" : "text-dim"
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

    case "sys":
      return (
        <div className={`mb-1 py-0.5 text-[12px] ${sysColor[block.cls]}`}>
          <span className="mr-2 text-[10.5px] text-dim">{tsShort(block.ts)}</span>
          {block.text}
        </div>
      );

    case "raw":
      return (
        <details className="card mb-2.5 max-w-4xl overflow-hidden rounded-lg border border-line bg-panel">
          <summary className="cursor-pointer select-none px-3 py-2 font-mono text-[12.5px] text-dim">
            {block.type}
          </summary>
          <pre className="max-h-80 overflow-auto whitespace-pre-wrap border-t border-line px-3.5 py-2.5 font-mono text-[12px] text-dim">
            {block.json}
          </pre>
        </details>
      );
  }
}
