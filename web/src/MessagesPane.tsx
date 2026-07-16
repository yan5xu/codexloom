import { CheckCircle2, Clock3, MessageSquare, Reply, RotateCcw, Send, SkipForward, XCircle } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Popover, PopoverContent, PopoverTrigger } from "./components/ui/popover";
import { api, type AgentMessage, type Agent } from "./types";

const STALE_AFTER_MS = 24 * 60 * 60 * 1000;

function isStale(message: AgentMessage) {
  if (message.status !== "open" || message.deliveryStatus !== "delivered") return false;
  const createdAt = new Date(message.createdAt).getTime();
  return Number.isFinite(createdAt) && Date.now() - createdAt >= STALE_AFTER_MS;
}

function isWaiting(message: AgentMessage) {
  return message.status === "open" && message.deliveryStatus === "delivered" && !["interrupted", "failed"].includes(message.handlingStatus || "") && !isStale(message);
}

interface Props {
  agents: Agent[];
  onError: (msg: string) => void;
  initialTo?: string;
  participants?: [string, string] | null;
  onClearParticipants?: () => void;
}

export function MessagesPane({ agents, onError, initialTo, participants, onClearParticipants }: Props) {
  const [messages, setMessages] = useState<AgentMessage[]>([]);
  const [filter, setFilter] = useState<"all" | "waiting" | "queued" | "held" | "stale" | "failed">("all");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [subject, setSubject] = useState("");
  const [body, setBody] = useState("");
  const [response, setResponse] = useState<"required" | "none">("required");
  const [replyTo, setReplyTo] = useState<AgentMessage | null>(null);
  const [sending, setSending] = useState(false);
  const [resolveTarget, setResolveTarget] = useState<string | null>(null);
  const [resolveKind, setResolveKind] = useState<"completed_elsewhere" | "superseded">("completed_elsewhere");
  const [resolveReason, setResolveReason] = useState("");
  const [resolving, setResolving] = useState(false);

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
        if (evt.type === "loom/reconcile" || evt.type === "loom/comms-message") refresh().catch(() => {});
      } catch {
        /* ignore */
      }
    };
    return () => es.close();
  }, []);

  useEffect(() => {
    if (!from && agents.length > 0) setFrom(agents[0].name);
    if (!to && agents.length > 1) setTo(agents[1].name);
  }, [agents, from, to]);

  useEffect(() => {
    if (initialTo) setTo(initialTo);
  }, [initialTo]);

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
    let roots = messages.filter((m) => !m.replyTo);
    if (participants) {
      const expected = new Set(participants);
      roots = roots.filter((message) => expected.has(message.from) && expected.has(message.to));
    }
    if (filter === "waiting") return roots.filter((m) => isWaiting(m));
    if (filter === "queued") return roots.filter((m) => m.deliveryStatus === "queued" || m.deliveryStatus === "delivering");
    if (filter === "held") return roots.filter((m) => m.handlingStatus === "interrupted" || m.handlingStatus === "failed");
    if (filter === "stale") return roots.filter((m) => isStale(m));
    if (filter === "failed") return roots.filter((m) => m.deliveryStatus === "failed");
    return roots;
  }, [messages, filter, participants]);
  const rootMessages = useMemo(() => {
    const roots = messages.filter((m) => !m.replyTo);
    if (!participants) return roots;
    const expected = new Set(participants);
    return roots.filter((message) => expected.has(message.from) && expected.has(message.to));
  }, [messages, participants]);
  const waitingCount = rootMessages.filter(isWaiting).length;
  const queuedCount = rootMessages.filter((m) => m.deliveryStatus === "queued" || m.deliveryStatus === "delivering").length;
  const heldCount = rootMessages.filter((m) => m.handlingStatus === "interrupted" || m.handlingStatus === "failed").length;
  const staleCount = rootMessages.filter(isStale).length;
  const failedCount = rootMessages.filter((m) => m.deliveryStatus === "failed").length;

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

  const cancelMessage = async (msg: AgentMessage) => {
    try {
      await api("POST", `/api/comms/messages/${encodeURIComponent(msg.id)}/cancel`);
      await refresh();
    } catch (err: any) {
      onError(err.message);
    }
  };

  const closeWithoutReply = async (msg: AgentMessage) => {
    try {
      await api("POST", `/api/comms/messages/${encodeURIComponent(msg.id)}/no-reply`, { from: msg.to });
      await refresh();
    } catch (err: any) {
      onError(err.message);
    }
  };

  const continueMessage = async (msg: AgentMessage) => {
    try {
      await api("POST", `/api/comms/messages/${encodeURIComponent(msg.id)}/retry`, {});
      await refresh();
    } catch (err: any) {
      onError(err.message);
    }
  };

  const resolveMessage = async (msg: AgentMessage) => {
    if (!resolveReason.trim() || resolving) return;
    setResolving(true);
    try {
      await api("POST", `/api/comms/messages/${encodeURIComponent(msg.id)}/resolve`, {
        from: msg.fromAgentId || msg.from,
        resolution: resolveKind,
        reason: resolveReason.trim(),
      });
      setResolveTarget(null);
      setResolveReason("");
      setResolveKind("completed_elsewhere");
      await refresh();
    } catch (err: any) {
      onError(err.message);
    } finally {
      setResolving(false);
    }
  };

  const statusClass = (status: AgentMessage["status"]) => {
    if (status === "open") return "bg-warning/10 text-warning";
    if (status === "answered") return "bg-success/10 text-success";
    return "bg-muted text-muted-foreground";
  };

  const deliveryClass = (status: AgentMessage["deliveryStatus"]) => {
    if (status === "delivered") return "bg-success/10 text-success";
    if (status === "queued" || status === "delivering") return "bg-warning/10 text-warning";
    if (status === "failed") return "bg-destructive/10 text-destructive";
    return "bg-muted text-muted-foreground";
  };

  const deliveryModeLabel = (msg: AgentMessage) => {
    if (msg.deliveryMode === "turn_steer") return "active turn";
    if (msg.deliveryMode === "turn_start") return "new turn";
    return "";
  };

  const filterCount = (f: typeof filter) => {
    if (f === "waiting") return waitingCount;
    if (f === "queued") return queuedCount;
    if (f === "held") return heldCount;
    if (f === "stale") return staleCount;
    if (f === "failed") return failedCount;
    return rootMessages.length;
  };

  const fmt = (ts: string) => {
    const d = new Date(ts);
    if (Number.isNaN(d.getTime())) return ts;
    return d.toLocaleString();
  };

  return (
    <main className="flex min-w-0 flex-1 flex-col bg-background">
      <header className="flex h-14 shrink-0 items-center justify-between border-b border-border bg-card/80 pl-14 pr-3 backdrop-blur md:px-5">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <MessageSquare className="size-4 text-primary" />
            <h1 className="truncate font-serif text-xl tracking-tight">Team activity</h1>
          </div>
          <div className="mt-0.5 hidden font-mono text-[10px] uppercase tracking-[0.16em] text-muted-foreground md:block">
            agent communication history
          </div>
        </div>
        <div className="flex max-w-[70vw] overflow-x-auto rounded-lg border border-border bg-background p-0.5">
          {(["all", "waiting", "queued", "held", "stale", "failed"] as const).map((f) => (
            <button
              key={f}
              onClick={() => setFilter(f)}
              className={`h-7 whitespace-nowrap rounded-md px-2 text-[12px] font-medium capitalize sm:px-3 ${
                filter === f ? "bg-primary text-primary-foreground" : "text-muted-foreground hover:text-foreground"
              }`}
            >
              {f} <span className="hidden font-mono opacity-70 sm:inline">{filterCount(f)}</span>
            </button>
          ))}
        </div>
      </header>
      {participants ? (
        <div className="flex h-9 shrink-0 items-center gap-2 border-b border-border bg-muted/30 px-3 text-[10.5px] text-muted-foreground md:px-5">
          <span>Activity evidence:</span><strong className="truncate text-foreground">{participants[0]} and {participants[1]}</strong>
          <button type="button" onClick={onClearParticipants} className="ml-auto text-[10px] font-medium text-primary hover:underline">Show all messages</button>
        </div>
      ) : null}

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
                  className="h-9 w-full rounded-lg bg-background px-2 text-[13px] outline-none ring-1 ring-border focus:ring-ring/25"
                >
                  <option value="">select</option>
                  {agents.map((s) => (
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
                  className="h-9 w-full rounded-lg bg-background px-2 text-[13px] outline-none ring-1 ring-border focus:ring-ring/25 disabled:opacity-70"
                >
                  <option value="">select</option>
                  {agents.map((s) => (
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
                className="h-9 w-full rounded-lg bg-background px-2.5 text-[13px] outline-none ring-1 ring-border focus:ring-ring/25"
              />
            </label>
            <label className="block space-y-1">
              <span className="text-[11px] font-medium text-muted-foreground">Response</span>
              <select
                value={response}
                onChange={(e) => setResponse(e.target.value as "required" | "none")}
                disabled={!!replyTo}
                className="h-9 w-full rounded-lg bg-background px-2 text-[13px] outline-none ring-1 ring-border focus:ring-ring/25 disabled:opacity-70"
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
                className="min-h-40 w-full resize-none rounded-lg bg-background px-3 py-2 font-mono text-[12.5px] outline-none ring-1 ring-border focus:ring-ring/25"
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
                    {isStale(msg) && (
                      <span className="flex items-center gap-1 rounded-md bg-warning/10 px-2 py-1 text-[11px] font-medium text-warning">
                        <Clock3 className="size-3" /> stale
                      </span>
                    )}
                    <span className={`rounded-md px-2 py-1 text-[11px] font-medium ${statusClass(msg.status)}`}>
                      {msg.resolution || msg.status}
                    </span>
                    <span className={`rounded-md px-2 py-1 text-[11px] font-medium ${deliveryClass(msg.deliveryStatus)}`}>
                      {msg.deliveryStatus}
                    </span>
                    {msg.handlingStatus && msg.deliveryStatus === "delivered" ? <span className={`rounded-md px-2 py-1 text-[11px] font-medium ${msg.handlingStatus === "interrupted" ? "bg-warning/10 text-warning" : msg.handlingStatus === "failed" ? "bg-destructive/10 text-destructive" : "bg-muted text-muted-foreground"}`}>{msg.handlingStatus === "interrupted" ? "held" : msg.handlingStatus}</span> : null}
                    <span className="font-mono text-[10.5px] text-muted-foreground">{fmt(msg.createdAt)}</span>
                  </div>
                </div>
                <pre className="mt-3 max-h-72 overflow-auto whitespace-pre-wrap rounded-lg bg-muted/35 px-3 py-2 font-mono text-[12.5px] text-foreground/85">
                  {msg.body}
                </pre>
                <div className="mt-3 flex items-center justify-between">
                  <div className="font-mono text-[10.5px] text-muted-foreground">
                    response {msg.response}
                    {msg.sourceTurnId ? ` · source turn ${msg.sourceTurnId}` : ""}
                    {deliveryModeLabel(msg) ? ` · ${deliveryModeLabel(msg)}` : ""}
                    {msg.deliveredTurnId ? ` ${msg.deliveredTurnId}` : ""}
                    {msg.lastDeliveryError ? ` · ${msg.lastDeliveryError}` : ""}
                    {msg.lastHandlingError ? ` · ${msg.lastHandlingError}` : ""}
                  </div>
                  <div className="flex items-center gap-2">
                  {msg.deliveryStatus === "queued" && (
                    <button
                      onClick={() => cancelMessage(msg)}
                      className="flex h-8 items-center gap-1.5 rounded-lg border border-border bg-background px-2.5 text-[12px] font-medium text-muted-foreground hover:text-foreground"
                    >
                      <XCircle className="size-3.5" />
                      Cancel
                    </button>
                  )}
                  {msg.status === "open" && msg.deliveryStatus === "delivered" && (
                    <>
                      {(msg.handlingStatus === "interrupted" || msg.handlingStatus === "failed") && <button onClick={() => continueMessage(msg)} className="flex h-8 items-center gap-1.5 rounded-lg bg-primary px-2.5 text-[12px] font-medium text-primary-foreground"><RotateCcw className="size-3.5" />Continue</button>}
                      <button
                        onClick={() => closeWithoutReply(msg)}
                        className="flex h-8 items-center gap-1.5 rounded-lg border border-border bg-background px-2.5 text-[12px] font-medium text-muted-foreground hover:text-foreground"
                      >
                        <SkipForward className="size-3.5" />
                        No reply
                      </button>
                      <button
                        onClick={() => beginReply(msg)}
                        className="flex h-8 items-center gap-1.5 rounded-lg border border-border bg-background px-2.5 text-[12px] font-medium text-muted-foreground hover:text-foreground"
                      >
                        <Reply className="size-3.5" />
                        Reply
                      </button>
                    </>
                  )}
                  {msg.status === "open" && msg.response === "required" && msg.deliveryStatus !== "queued" && msg.deliveryStatus !== "delivering" && (
                    <Popover open={resolveTarget === msg.id} onOpenChange={(open) => {
                      setResolveTarget(open ? msg.id : null);
                      if (!open) {
                        setResolveReason("");
                        setResolveKind("completed_elsewhere");
                      }
                    }}>
                      <PopoverTrigger className="flex h-8 items-center gap-1.5 rounded-lg border border-border bg-background px-2.5 text-[12px] font-medium text-muted-foreground outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring/30">
                        <CheckCircle2 className="size-3.5" />
                        Resolve
                      </PopoverTrigger>
                      <PopoverContent side="bottom" align="end" className="w-[min(22rem,calc(100vw-1rem))] space-y-3" aria-label={`Resolve ${msg.subject}`}>
                        <div>
                          <div className="text-[12px] font-semibold">Resolve request</div>
                          <div className="mt-0.5 text-[11px] text-muted-foreground">Close this request without inventing a reply link.</div>
                        </div>
                        <label className="block space-y-1">
                          <span className="text-[10.5px] font-medium text-muted-foreground">Outcome</span>
                          <select value={resolveKind} onChange={(event) => setResolveKind(event.target.value as typeof resolveKind)} className="h-8 w-full rounded-md border border-border bg-background px-2 text-[12px] outline-none focus:ring-2 focus:ring-ring/25">
                            <option value="completed_elsewhere">Completed elsewhere</option>
                            <option value="superseded">Superseded</option>
                          </select>
                        </label>
                        <label className="block space-y-1">
                          <span className="text-[10.5px] font-medium text-muted-foreground">Reason</span>
                          <textarea value={resolveReason} onChange={(event) => setResolveReason(event.target.value)} placeholder="Where it completed, or what replaced it" className="min-h-20 w-full resize-none rounded-md border border-border bg-background px-2.5 py-2 text-[12px] outline-none focus:ring-2 focus:ring-ring/25" />
                        </label>
                        <button type="button" onClick={() => resolveMessage(msg)} disabled={resolving || !resolveReason.trim()} className="flex h-8 w-full items-center justify-center gap-1.5 rounded-md bg-primary px-3 text-[12px] font-medium text-primary-foreground disabled:cursor-not-allowed disabled:opacity-50">
                          <CheckCircle2 className="size-3.5" /> {resolving ? "Resolving" : "Resolve request"}
                        </button>
                      </PopoverContent>
                    </Popover>
                  )}
                  </div>
                </div>
                {msg.resolutionReason && (
                  <div className="mt-2 border-t border-border/70 pt-2 text-[11px] text-muted-foreground">
                    <span className="font-medium text-foreground/80">Resolution:</span> {msg.resolutionReason}
                    {msg.resolvedBy ? ` · ${msg.resolvedBy}` : ""}
                    {msg.resolvedAt ? ` · ${fmt(msg.resolvedAt)}` : ""}
                  </div>
                )}
                {(msg.handlingAttempts || []).length > 0 && (
                  <details className="mt-3 border-t border-border pt-3">
                    <summary className="cursor-pointer font-mono text-[10px] uppercase text-muted-foreground">Handling attempts · {msg.handlingAttempts!.length}</summary>
                    <div className="mt-2 divide-y divide-border/70">
                      {msg.handlingAttempts!.map((attempt) => (
                        <div key={attempt.id} className="grid gap-1 py-2 text-[10.5px] sm:grid-cols-[88px_1fr_auto]">
                          <span className="font-mono uppercase text-muted-foreground">{attempt.status}</span>
                          <span className="truncate font-mono" title={attempt.turnId}>{attempt.turnId || attempt.id}</span>
                          <span className="text-muted-foreground">{fmt(attempt.completedAt || attempt.startedAt)}</span>
                          {attempt.error ? <span className="text-destructive sm:col-span-3">{attempt.error}</span> : null}
                        </div>
                      ))}
                    </div>
                  </details>
                )}
                {(repliesByParent.get(msg.id) || []).length > 0 && (
                  <div className="mt-3 space-y-2 border-t border-border pt-3">
                    {(repliesByParent.get(msg.id) || []).map((reply) => (
                      <div key={reply.id} className="rounded-lg bg-muted/30 px-3 py-2">
                        <div className="flex flex-wrap items-center justify-between gap-2">
                          <div className="font-mono text-[11px] text-muted-foreground">
                            {reply.from} -&gt; {reply.to} · {reply.id}
                          </div>
                          <div className="flex items-center gap-2">
                            <span className={`rounded-md px-2 py-1 text-[10.5px] font-medium ${deliveryClass(reply.deliveryStatus)}`}>
                              {reply.deliveryStatus}
                            </span>
                            <div className="font-mono text-[10.5px] text-muted-foreground">{fmt(reply.createdAt)}</div>
                          </div>
                        </div>
                        <pre className="mt-2 whitespace-pre-wrap font-mono text-[12.5px] text-foreground/85">
                          {reply.body}
                        </pre>
                        <div className="mt-1 font-mono text-[10.5px] text-muted-foreground">
                          {reply.sourceTurnId ? `source turn ${reply.sourceTurnId}` : ""}
                          {reply.sourceTurnId && deliveryModeLabel(reply) ? " · " : ""}
                          {deliveryModeLabel(reply)}
                          {reply.deliveredTurnId ? ` ${reply.deliveredTurnId}` : ""}
                          {reply.lastDeliveryError ? ` · ${reply.lastDeliveryError}` : ""}
                        </div>
                      </div>
                    ))}
                  </div>
                )}
              </article>
            ))}
            {visible.length === 0 && (
              <div className="rounded-lg border border-dashed border-border bg-card/50 px-4 py-10 text-center text-sm text-muted-foreground">
                {filter === "waiting" ? "No messages waiting for a reply." : filter === "stale" ? "No stale requests." : "No messages."}
              </div>
            )}
          </div>
        </section>
      </div>
    </main>
  );
}
