import { MessageSquare, Reply, Send } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { api, type AgentMessage, type Session } from "./types";

interface Props {
  sessions: Session[];
  onError: (msg: string) => void;
}

export function MessagesPane({ sessions, onError }: Props) {
  const [messages, setMessages] = useState<AgentMessage[]>([]);
  const [filter, setFilter] = useState<"open" | "all">("all");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [subject, setSubject] = useState("");
  const [body, setBody] = useState("");
  const [response, setResponse] = useState<"required" | "none">("required");
  const [replyTo, setReplyTo] = useState<AgentMessage | null>(null);
  const [sending, setSending] = useState(false);

  const refresh = async () => {
    const data = await api("GET", "/api/comms");
    setMessages(data.messages || []);
  };

  useEffect(() => {
    refresh().catch((err: any) => onError(err.message));
    const es = new EventSource("/api/events");
    es.onmessage = (e) => {
      try {
        const evt = JSON.parse(e.data);
        if (evt.type === "hub/comms-message") refresh().catch(() => {});
      } catch {
        /* ignore */
      }
    };
    return () => es.close();
  }, []);

  useEffect(() => {
    if (!from && sessions.length > 0) setFrom(sessions[0].name);
    if (!to && sessions.length > 1) setTo(sessions[1].name);
  }, [sessions, from, to]);

  const repliesByParent = useMemo(() => {
    const m = new Map<string, AgentMessage[]>();
    for (const msg of messages) {
      if (!msg.replyTo) continue;
      const list = m.get(msg.replyTo) || [];
      list.push(msg);
      m.set(msg.replyTo, list);
    }
    for (const list of m.values()) {
      list.sort((a, b) => a.createdAt.localeCompare(b.createdAt));
    }
    return m;
  }, [messages]);

  const visible = useMemo(() => {
    const roots = messages.filter((m) => !m.replyTo);
    if (filter === "open") return roots.filter((m) => m.status === "open");
    return roots;
  }, [messages, filter]);
  const rootMessages = useMemo(() => messages.filter((m) => !m.replyTo), [messages]);
  const openCount = rootMessages.filter((m) => m.status === "open").length;

  const beginReply = (msg: AgentMessage) => {
    setReplyTo(msg);
    setFrom(msg.to);
    setTo(msg.from);
    setSubject(msg.subject.startsWith("Re:") ? msg.subject : `Re: ${msg.subject}`);
    setResponse("none");
    setBody("");
  };

  const clearReply = () => {
    setReplyTo(null);
    setSubject("");
    setBody("");
    setResponse("required");
  };

  const send = async () => {
    if (sending) return;
    if (!from.trim() || !body.trim()) {
      onError("from and body required");
      return;
    }
    if (!replyTo && (!to.trim() || !subject.trim())) {
      onError("to and subject required");
      return;
    }
    setSending(true);
    try {
      await api("POST", "/api/comms/messages", {
        from: from.trim(),
        to: replyTo ? "" : to.trim(),
        subject: subject.trim(),
        body,
        response,
        replyTo: replyTo?.id || "",
      });
      setBody("");
      if (replyTo) clearReply();
      await refresh();
    } catch (err: any) {
      onError(err.message);
    } finally {
      setSending(false);
    }
  };

  const statusClass = (status: AgentMessage["status"]) => {
    if (status === "open") return "bg-warning/10 text-warning";
    if (status === "answered") return "bg-success/10 text-success";
    return "bg-muted text-muted-foreground";
  };

  const fmt = (ts: string) => {
    const d = new Date(ts);
    if (Number.isNaN(d.getTime())) return ts;
    return d.toLocaleString();
  };

  return (
    <main className="flex min-w-0 flex-1 flex-col bg-background">
      <header className="flex h-14 shrink-0 items-center justify-between border-b border-border bg-card/80 px-5 backdrop-blur">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <MessageSquare className="size-4 text-primary" />
            <h1 className="truncate font-serif text-xl tracking-tight">Messages</h1>
          </div>
          <div className="mt-0.5 font-mono text-[10px] uppercase tracking-[0.16em] text-muted-foreground">
            agent communication history
          </div>
        </div>
        <div className="flex rounded-lg border border-border bg-background p-0.5">
          {(["open", "all"] as const).map((f) => (
            <button
              key={f}
              onClick={() => setFilter(f)}
              className={`h-7 rounded-md px-3 text-[12px] font-medium capitalize ${
                filter === f ? "bg-primary text-primary-foreground" : "text-muted-foreground hover:text-foreground"
              }`}
            >
              {f} <span className="font-mono opacity-70">{f === "open" ? openCount : rootMessages.length}</span>
            </button>
          ))}
        </div>
      </header>

      <div className="grid min-h-0 flex-1 grid-cols-1 overflow-hidden lg:grid-cols-[360px_1fr]">
        <section className="border-b border-border bg-card/45 p-4 lg:border-b-0 lg:border-r">
          <div className="space-y-3">
            {replyTo && (
              <div className="rounded-lg border border-primary/25 bg-primary/10 px-3 py-2 text-[12px]">
                <div className="font-medium text-foreground">Replying to {replyTo.id}</div>
                <div className="mt-0.5 truncate text-muted-foreground">{replyTo.subject}</div>
                <button onClick={clearReply} className="mt-2 text-primary hover:underline">
                  cancel reply
                </button>
              </div>
            )}
            <div className="grid grid-cols-2 gap-2">
              <label className="space-y-1">
                <span className="text-[11px] font-medium text-muted-foreground">From</span>
                <select
                  value={from}
                  onChange={(e) => setFrom(e.target.value)}
                  className="h-9 w-full rounded-lg bg-background px-2 text-[13px] outline-none ring-1 ring-border focus:ring-primary/40"
                >
                  <option value="">select</option>
                  {sessions.map((s) => (
                    <option key={s.id} value={s.name}>
                      {s.name}
                    </option>
                  ))}
                </select>
              </label>
              <label className="space-y-1">
                <span className="text-[11px] font-medium text-muted-foreground">To</span>
                <select
                  value={to}
                  onChange={(e) => setTo(e.target.value)}
                  disabled={!!replyTo}
                  className="h-9 w-full rounded-lg bg-background px-2 text-[13px] outline-none ring-1 ring-border focus:ring-primary/40 disabled:opacity-70"
                >
                  <option value="">select</option>
                  {sessions.map((s) => (
                    <option key={s.id} value={s.name}>
                      {s.name}
                    </option>
                  ))}
                </select>
              </label>
            </div>
            <label className="block space-y-1">
              <span className="text-[11px] font-medium text-muted-foreground">Subject</span>
              <input
                value={subject}
                onChange={(e) => setSubject(e.target.value)}
                className="h-9 w-full rounded-lg bg-background px-2.5 text-[13px] outline-none ring-1 ring-border focus:ring-primary/40"
              />
            </label>
            <label className="block space-y-1">
              <span className="text-[11px] font-medium text-muted-foreground">Response</span>
              <select
                value={response}
                onChange={(e) => setResponse(e.target.value as "required" | "none")}
                disabled={!!replyTo}
                className="h-9 w-full rounded-lg bg-background px-2 text-[13px] outline-none ring-1 ring-border focus:ring-primary/40 disabled:opacity-70"
              >
                <option value="required">required</option>
                <option value="none">none</option>
              </select>
            </label>
            <label className="block space-y-1">
              <span className="text-[11px] font-medium text-muted-foreground">Body</span>
              <textarea
                value={body}
                onChange={(e) => setBody(e.target.value)}
                className="min-h-40 w-full resize-none rounded-lg bg-background px-3 py-2 font-mono text-[12.5px] outline-none ring-1 ring-border focus:ring-primary/40"
              />
            </label>
            <button
              onClick={send}
              disabled={sending}
              className="flex h-9 w-full items-center justify-center gap-2 rounded-lg bg-primary px-3 text-[13px] font-medium text-primary-foreground transition hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-60"
            >
              <Send className="size-3.5" />
              {sending ? "Sending" : replyTo ? "Send reply" : "Send message"}
            </button>
          </div>
        </section>

        <section className="min-h-0 overflow-y-auto p-4">
          <div className="mx-auto max-w-4xl space-y-3">
            {visible.map((msg) => (
              <article key={msg.id} className="rounded-lg border border-border bg-card p-4 shadow-card">
                <div className="flex flex-wrap items-start justify-between gap-3">
                  <div className="min-w-0">
                    <div className="truncate text-[15px] font-semibold text-foreground">{msg.subject}</div>
                    <div className="mt-1 font-mono text-[11px] text-muted-foreground">
                      {msg.from} -&gt; {msg.to} · {msg.id}
                    </div>
                  </div>
                  <div className="flex items-center gap-2">
                    <span className={`rounded-md px-2 py-1 text-[11px] font-medium ${statusClass(msg.status)}`}>
                      {msg.status}
                    </span>
                    <span className="font-mono text-[10.5px] text-muted-foreground">{fmt(msg.createdAt)}</span>
                  </div>
                </div>
                <pre className="mt-3 max-h-72 overflow-auto whitespace-pre-wrap rounded-lg bg-muted/35 px-3 py-2 font-mono text-[12.5px] text-foreground/85">
                  {msg.body}
                </pre>
                <div className="mt-3 flex items-center justify-between">
                  <div className="font-mono text-[10.5px] text-muted-foreground">
                    response {msg.response}
                    {msg.deliveredTurnId ? ` · turn ${msg.deliveredTurnId}` : ""}
                  </div>
                  {msg.status === "open" && (
                    <button
                      onClick={() => beginReply(msg)}
                      className="flex h-8 items-center gap-1.5 rounded-lg border border-border bg-background px-2.5 text-[12px] font-medium text-muted-foreground hover:text-foreground"
                    >
                      <Reply className="size-3.5" />
                      Reply
                    </button>
                  )}
                </div>
                {(repliesByParent.get(msg.id) || []).length > 0 && (
                  <div className="mt-3 space-y-2 border-t border-border pt-3">
                    {(repliesByParent.get(msg.id) || []).map((reply) => (
                      <div key={reply.id} className="rounded-lg bg-muted/30 px-3 py-2">
                        <div className="flex flex-wrap items-center justify-between gap-2">
                          <div className="font-mono text-[11px] text-muted-foreground">
                            {reply.from} -&gt; {reply.to} · {reply.id}
                          </div>
                          <div className="font-mono text-[10.5px] text-muted-foreground">{fmt(reply.createdAt)}</div>
                        </div>
                        <pre className="mt-2 whitespace-pre-wrap font-mono text-[12.5px] text-foreground/85">
                          {reply.body}
                        </pre>
                      </div>
                    ))}
                  </div>
                )}
              </article>
            ))}
            {visible.length === 0 && (
              <div className="rounded-lg border border-dashed border-border bg-card/50 px-4 py-10 text-center text-sm text-muted-foreground">
                {filter === "open" ? "No open messages." : "No messages."}
              </div>
            )}
          </div>
        </section>
      </div>
    </main>
  );
}
