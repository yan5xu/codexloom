import { Send, Square } from "lucide-react";
import { cn } from "@/lib/utils";
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
  const loadedRef = useRef(0); // turns loaded so far
  const totalRef = useRef(0); // total turns in rollout
  const loadingRef = useRef(false);
  const keepScrollRef = useRef<number | null>(null); // scrollHeight before a prepend

  const PAGE = 25;

  // Seed the newest page of past turns from the rollout (single source of
  // history; works for mirror/idle sessions with no live event log).
  useEffect(() => {
    let cancelled = false;
    loadedRef.current = 0;
    totalRef.current = 0;
    api("GET", `/api/sessions/${session.id}/history?count=${PAGE}&offset=0`)
      .then((h) => {
        if (cancelled) return;
        totalRef.current = h.total || 0;
        loadedRef.current = (h.turns || []).length;
        dispatch({ type: "__history__", ts: "", data: h } as any);
      })
      .catch(() => {
        /* ignore — live stream still works */
      });
    return () => {
      cancelled = true;
    };
  }, [session.id]);

  // Scroll-up lazy load: fetch the next older page and prepend it.
  const loadOlder = () => {
    if (loadingRef.current || loadedRef.current >= totalRef.current) return;
    loadingRef.current = true;
    const el = feedRef.current;
    keepScrollRef.current = el ? el.scrollHeight : null;
    const offset = loadedRef.current;
    api("GET", `/api/sessions/${session.id}/history?count=${PAGE}&offset=${offset}`)
      .then((h) => {
        const older = h.turns || [];
        loadedRef.current += older.length;
        totalRef.current = h.total || totalRef.current;
        dispatch({ type: "__history_prepend__", ts: "", data: { turns: older, offset } } as any);
      })
      .finally(() => {
        loadingRef.current = false;
      });
  };

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

  // After blocks change: if a prepend just happened, preserve the scroll
  // position (keep the same turn under the viewport); otherwise autoscroll to
  // bottom while pinned there.
  useEffect(() => {
    const el = feedRef.current;
    if (!el) return;
    if (keepScrollRef.current !== null) {
      el.scrollTop += el.scrollHeight - keepScrollRef.current;
      keepScrollRef.current = null;
      return;
    }
    if (stickRef.current) el.scrollTop = el.scrollHeight;
  }, [feed.blocks]);

  const onScroll = () => {
    const el = feedRef.current;
    if (!el) return;
    stickRef.current = el.scrollHeight - el.scrollTop - el.clientHeight < 60;
    if (el.scrollTop < 120) loadOlder(); // near top → load older page
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
      <div ref={feedRef} onScroll={onScroll} className="min-h-0 flex-1 overflow-y-auto">
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

      {/* composer — faithful AgentComposer layout: shimmer divider, rounded-2xl
          ring container, icon send/stop, centered hint. */}
      <div className="relative shrink-0 bg-background px-3 py-2 pb-[max(0.5rem,env(safe-area-inset-bottom))] md:px-6 md:py-3 md:pb-3">
        <div
          className="absolute inset-x-0 top-0 h-px"
          style={{
            background:
              "linear-gradient(90deg, transparent, oklch(0.88 0.01 78 / 0.4) 20%, oklch(0.88 0.01 78 / 0.4) 80%, transparent)",
          }}
        />
        <div className="mx-auto max-w-3xl">
          <div className="flex flex-col gap-2 rounded-2xl bg-card p-2 shadow-[0_4px_20px_rgba(0,0,0,0.02)] ring-1 ring-black/[0.04] transition-all duration-150 focus-within:ring-black/[0.08]">
            <textarea
              value={input}
              onChange={(e) => setInput(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
                  e.preventDefault();
                  send();
                }
              }}
              placeholder={`Send a task to ${session.name}…`}
              className="max-h-[200px] min-h-11 resize-none overflow-y-auto bg-transparent px-3 py-2 text-sm outline-none placeholder:text-muted-foreground/50"
            />
            <div className="flex items-center justify-end px-1">
              {running ? (
                <button
                  onClick={interrupt}
                  className="flex size-9 shrink-0 cursor-pointer items-center justify-center rounded-xl bg-primary text-primary-foreground shadow-[0_4px_12px_rgba(139,92,246,0.2)] transition-all duration-150 hover:bg-primary/90"
                >
                  <Square className="size-4" />
                </button>
              ) : (
                <button
                  onClick={send}
                  disabled={!input.trim()}
                  className={cn(
                    "flex size-9 shrink-0 items-center justify-center rounded-xl transition-all duration-150",
                    input.trim()
                      ? "cursor-pointer bg-primary text-primary-foreground shadow-[0_4px_12px_rgba(139,92,246,0.2)] hover:bg-primary/90"
                      : "cursor-not-allowed bg-muted text-muted-foreground/40",
                  )}
                >
                  <Send className="size-4" />
                </button>
              )}
            </div>
          </div>
        </div>
        <p className="mt-1.5 text-center font-mono text-[10px] text-muted-foreground/50">
          Enter to send · Shift+Enter for new line
        </p>
      </div>
    </main>
  );
}
