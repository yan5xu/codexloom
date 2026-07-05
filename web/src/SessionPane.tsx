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

  // Live event stream: replay last 500 then follow.
  useEffect(() => {
    const es = new EventSource(`/api/sessions/${session.id}/events?tail=500`);
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

  return (
    <div className="flex min-w-0 flex-1 flex-col">
      {/* header */}
      <div className="flex items-center gap-3 border-b border-line bg-panel px-4 py-2.5">
        <span className="text-[15px] font-bold">{session.name}</span>
        <span
          className={`rounded-full border px-2.5 py-px text-[11px] ${
            session.status === "running"
              ? "border-warn text-warn"
              : session.status === "idle"
                ? "border-ok text-ok"
                : "border-line text-dim"
          }`}
        >
          {session.status}
          {session.currentTask ? ` — ${session.currentTask.slice(0, 50)}` : ""}
        </span>
        <span className="flex-1 truncate font-mono text-[12px] text-dim">
          {session.cwd} · {session.threadId}
        </span>
        {session.status === "running" && (
          <button
            onClick={interrupt}
            className="rounded-md border border-line bg-panel2 px-3 py-1.5 text-[13px] hover:border-warn hover:text-warn"
          >
            interrupt
          </button>
        )}
        <button
          onClick={kill}
          className="rounded-md border border-line bg-panel2 px-3 py-1.5 text-[13px] text-err hover:border-err"
        >
          kill
        </button>
      </div>

      {/* pending approvals */}
      {approvalEntries.length > 0 && (
        <div className="px-5 pt-2">
          {approvalEntries.map(([id, ap]) => (
            <div key={id} className="mb-2 max-w-4xl rounded-lg border border-warn bg-[#2d1a06] px-3.5 py-3">
              <div className="mb-2">
                ⚠ <b>codex requests approval</b> — <span className="font-mono text-[12px]">{ap.method}</span>
              </div>
              <pre className="mb-2 max-h-40 overflow-auto whitespace-pre-wrap font-mono text-[12px] text-dim">
                {JSON.stringify(ap.params, null, 2)?.slice(0, 1500)}
              </pre>
              <button
                onClick={() => resolveApproval(id, "accept")}
                className="mr-2 rounded-md border border-[#238636] bg-[#238636] px-3 py-1.5 text-[13px]"
              >
                approve
              </button>
              <button
                onClick={() => resolveApproval(id, "reject")}
                className="rounded-md border border-line bg-panel2 px-3 py-1.5 text-[13px] text-err hover:border-err"
              >
                reject
              </button>
            </div>
          ))}
        </div>
      )}

      {/* event feed */}
      <div ref={feedRef} onScroll={onScroll} className="flex-1 overflow-y-auto px-5 pb-8 pt-4">
        {feed.blocks.map((b, i) => (
          <BlockView key={i} block={b} />
        ))}
      </div>

      {/* composer */}
      <div className="flex items-end gap-2.5 border-t border-line bg-panel px-4 py-3">
        <textarea
          value={input}
          onChange={(e) => setInput(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.shiftKey) {
              e.preventDefault();
              send();
            }
          }}
          placeholder="send a task to this session… (Enter to send, Shift+Enter for newline)"
          className="max-h-40 min-h-[42px] flex-1 resize-none rounded-lg border border-line bg-bg px-3 py-2.5 text-[13.5px] outline-none focus:border-accent"
        />
        <button
          onClick={send}
          className="rounded-md border border-[#1f6feb] bg-[#1f6feb] px-4 py-2 text-[13px] font-medium"
        >
          send
        </button>
      </div>
    </div>
  );
}
