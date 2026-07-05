import { useEffect, useReducer, useRef, useState } from "react";
import { api, type Session } from "./types";
import { emptyFeed, reduceFeed } from "./feed";
import type { HubEvent } from "./types";
import { BlockView } from "./Blocks";

export function SessionPane({
  session,
  onKilled,
  onError,
}: {
  session: Session;
  onKilled: () => void;
  onError: (msg: string) => void;
}) {
  const [feed, dispatch] = useReducer(reduceFeed, emptyFeed);
  const [input, setInput] = useState("");
  const feedRef = useRef<HTMLDivElement>(null);
  const stickRef = useRef(true);

  // Seed past turns from the codex rollout file (single source of history —
  // works for mirror/idle sessions with no live event log), then follow live.
  useEffect(() => {
    let cancelled = false;
    api("GET", `/api/sessions/${session.id}/history?count=100`)
      .then((h) => {
        if (!cancelled) dispatch({ type: "__history__", ts: "", data: h } as any);
      })
      .catch(() => {
        /* ignore — live stream still works */
      });
    return () => {
      cancelled = true;
    };
  }, [session.id]);

  // Live event stream: replay=0 → history is the single source of the past,
  // events carry only new activity after open (no duplication with history).
  useEffect(() => {
    const es = new EventSource(`/api/sessions/${session.id}/events?replay=0`);
    es.onmessage = (e) => {
      try {
        dispatch(JSON.parse(e.data) as HubEvent);
      } catch {
        /* ignore */
      }
    };
    return () => es.close();
  }, [session.id]);

  // Autoscroll while pinned to bottom.
  useEffect(() => {
    const el = feedRef.current;
    if (el && stickRef.current) el.scrollTop = el.scrollHeight;
  }, [feed.blocks]);

  const onScroll = () => {
    const el = feedRef.current;
    if (el) stickRef.current = el.scrollHeight - el.scrollTop - el.clientHeight < 60;
  };

  const send = async () => {
    const text = input.trim();
    if (!text) return;
    try {
      await api("POST", `/api/sessions/${session.id}/messages`, { text });
      setInput("");
    } catch (err: any) {
      onError(err.message);
    }
  };

  const interrupt = async () => {
    try {
      await api("POST", `/api/sessions/${session.id}/interrupt`);
    } catch (err: any) {
      onError(err.message);
    }
  };

  const kill = async () => {
    if (!confirm(`kill session "${session.name}"? The thread is archived and removed from the hub.`))
      return;
    try {
      await api("DELETE", `/api/sessions/${session.id}`);
      onKilled();
    } catch (err: any) {
      onError(err.message);
    }
  };

  const resolveApproval = async (approvalId: string, decision: string) => {
    try {
      await api("POST", `/api/sessions/${session.id}/approvals/${approvalId}`, { decision });
    } catch (err: any) {
      onError(err.message);
    }
  };

  const approvalEntries = Object.entries(feed.approvals);
  const running = session.status === "running";

  return (
    <main className="flex min-w-0 flex-1 flex-col bg-background">
      {/* header — serif title, status pill, mono meta; soft warm shadow, no hard border */}
      <header
        className="flex shrink-0 items-center gap-3 px-4 py-2.5 md:px-6"
        style={{ boxShadow: "0 4px 12px -6px oklch(0.2 0.03 78 / 0.12)" }}
      >
        <div className="min-w-0">
          <h1 className="truncate font-serif text-xl leading-none tracking-tight">{session.name}</h1>
          <div className="mt-1 truncate font-mono text-[10px] uppercase tracking-widest text-muted-foreground/80">
            {session.cwd} · {session.threadId}
          </div>
        </div>
        <span
          className={`ml-1 inline-flex shrink-0 items-center gap-1.5 rounded-full px-2.5 py-0.5 text-[10px] font-medium ${
            running
              ? "bg-warning/10 text-warning"
              : session.status === "idle"
                ? "bg-success/10 text-success"
                : "bg-muted text-muted-foreground"
          }`}
        >
          <span
            className={`h-1.5 w-1.5 rounded-full ${
              running ? "animate-pulse bg-warning" : session.status === "idle" ? "bg-success" : "bg-muted-foreground/50"
            }`}
          />
          {session.status}
          {session.currentTask ? ` — ${session.currentTask.slice(0, 42)}` : ""}
        </span>
        <div className="flex-1" />
        {running && (
          <button
            onClick={interrupt}
            className="shrink-0 rounded-xl px-3 py-1.5 text-[13px] text-muted-foreground transition-colors hover:bg-warning/10 hover:text-warning"
          >
            interrupt
          </button>
        )}
        <button
          onClick={kill}
          className="shrink-0 rounded-xl px-3 py-1.5 text-[13px] text-muted-foreground transition-colors hover:bg-destructive/10 hover:text-destructive"
        >
          kill
        </button>
      </header>

      {/* event feed + pending approvals */}
      <div ref={feedRef} onScroll={onScroll} className="flex-1 overflow-y-auto">
        <div className="mx-auto max-w-3xl px-4 pb-8 pt-4 md:px-6">
          {/* pending approvals */}
          {approvalEntries.map(([id, ap]) => (
            <div key={id} className="mb-3 rounded-2xl border border-warning/30 bg-warning/5 px-4 py-3.5 shadow-card">
              <div className="mb-2 text-sm">
                <span className="text-warning">⚠</span> <b>codex requests approval</b> —{" "}
                <span className="font-mono text-[12px]">{ap.method}</span>
              </div>
              <pre className="mb-3 max-h-40 overflow-auto whitespace-pre-wrap rounded-xl bg-muted/50 px-3 py-2 font-mono text-[12px] text-muted-foreground">
                {JSON.stringify(ap.params, null, 2)?.slice(0, 1500)}
              </pre>
              <button
                onClick={() => resolveApproval(id, "accept")}
                className="mr-2 rounded-xl bg-primary px-3.5 py-1.5 text-[13px] font-medium text-primary-foreground transition-colors hover:opacity-90"
              >
                approve
              </button>
              <button
                onClick={() => resolveApproval(id, "reject")}
                className="rounded-xl px-3.5 py-1.5 text-[13px] text-muted-foreground transition-colors hover:bg-destructive/10 hover:text-destructive"
              >
                reject
              </button>
            </div>
          ))}

          {feed.blocks.map((b, i) => (
            <BlockView key={i} block={b} />
          ))}
        </div>
      </div>

      {/* composer — mirrors AgentComposer: shimmer divider + rounded-2xl ring container */}
      <div className="relative shrink-0 bg-background px-4 pb-3 pt-2 md:px-6">
        <div className="shimmer-divider absolute inset-x-0 top-0 h-px" />
        <div className="mx-auto flex max-w-3xl items-end gap-2.5">
          <div className="flex flex-1 items-end rounded-2xl bg-card p-1.5 shadow-card ring-1 ring-border/60 transition focus-within:ring-primary/40">
            <textarea
              value={input}
              onChange={(e) => setInput(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
                  e.preventDefault();
                  send();
                }
              }}
              placeholder="Send a task to this session…  (Enter to send · Shift+Enter for newline)"
              className="max-h-40 min-h-[40px] flex-1 resize-none bg-transparent px-2.5 py-2 text-[13.5px] outline-none placeholder:text-muted-foreground/50"
            />
            <button
              onClick={send}
              disabled={!input.trim()}
              className="mb-0.5 mr-0.5 shrink-0 rounded-xl bg-primary px-4 py-2 text-[13px] font-medium text-primary-foreground transition enabled:hover:opacity-90 disabled:cursor-not-allowed disabled:bg-muted disabled:text-muted-foreground/40"
            >
              send
            </button>
          </div>
        </div>
      </div>
    </main>
  );
}
