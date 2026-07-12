import {
  ArrowLeft,
  Check,
  Clock3,
  Inbox,
  MessageSquareReply,
  Paperclip,
  RefreshCw,
  RotateCcw,
  Send,
  SkipForward,
} from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import {
  api,
  type AgentAddress,
  type InboxEntry,
  type InboxItem,
  type OutboxItem,
  type Agent,
} from "./types";

type InboxFilter = "all" | InboxItem["state"];
type Mode = "inbox" | "outbox";

export function InboxPane({ agents, onError }: { agents: Agent[]; onError: (message: string) => void }) {
  const [entries, setEntries] = useState<InboxEntry[]>([]);
  const [outbox, setOutbox] = useState<OutboxItem[]>([]);
  const [addresses, setAddresses] = useState<AgentAddress[]>([]);
  const [mode, setMode] = useState<Mode>("inbox");
  const [filter, setFilter] = useState<InboxFilter>("all");
  const [origin, setOrigin] = useState("all");
  const [selectedID, setSelectedID] = useState("");
  const [reply, setReply] = useState("");
  const [reason, setReason] = useState("");
  const [deferUntil, setDeferUntil] = useState("");
  const [working, setWorking] = useState(false);
  const [composeOpen, setComposeOpen] = useState(false);
  const [sendAgent, setSendAgent] = useState("");
  const [sendAddress, setSendAddress] = useState("");
  const [sendConversation, setSendConversation] = useState("");
  const [sendText, setSendText] = useState("");
  const stateRef = useRef<Record<string, unknown>>({});

  const refresh = async () => {
    const [inboxData, outboxData, addressData] = await Promise.all([
      api("GET", "/api/inbox"),
      api("GET", "/api/outbox"),
      api("GET", "/api/integrations/addresses"),
    ]);
    setEntries(inboxData.entries || []);
    setOutbox(outboxData.items || []);
    setAddresses(addressData.addresses || []);
  };

  useEffect(() => {
    refresh().catch((error: Error) => onError(error.message));
    const es = new EventSource("/api/events");
    es.onmessage = (event) => {
      try {
        const value = JSON.parse(event.data);
        if (["loom/inbox-message", "loom/inbox-item", "loom/outbox-item", "loom/comms-message"].includes(value.type)) {
          refresh().catch(() => {});
        }
      } catch {
        // Ignore malformed global events; EventSource will continue.
      }
    };
    return () => es.close();
  }, []);

  useEffect(() => {
    const query = window.location.hash.split("?")[1] || "";
    const id = new URLSearchParams(query).get("item");
    if (id) setSelectedID(id);
  }, []);

  const origins = useMemo(
    () => Array.from(new Set(entries.map((entry) => entry.message.origin))).sort(),
    [entries],
  );
  const visibleEntries = useMemo(
    () =>
      entries.filter(
        (entry) =>
          (filter === "all" || entry.item.state === filter) &&
          (origin === "all" || entry.message.origin === origin),
      ),
    [entries, filter, origin],
  );
  const selectedEntry = entries.find((entry) => entry.item.id === selectedID) || null;
  const selectedOutbox = outbox.find((item) => item.id === selectedID) || null;
  const selected = mode === "inbox" ? selectedEntry : selectedOutbox;

  const selectItem = (id: string) => {
    setSelectedID(id);
    setReply("");
    setReason("");
    window.history.replaceState(null, "", `#inbox?item=${encodeURIComponent(id)}`);
  };

  const run = async (task: () => Promise<unknown>) => {
    if (working) return;
    setWorking(true);
    try {
      await task();
      await refresh();
    } catch (error: any) {
      onError(error.message);
    } finally {
      setWorking(false);
    }
  };

  const respond = (kind: "reply" | "no-reply" | "defer" | "retry") => {
    if (!selectedEntry) return;
    const internal = selectedEntry.message.origin === "loom" || selectedEntry.message.origin === "chub";
    if (kind === "reply" && !reply.trim()) {
      onError("reply text is required");
      return;
    }
    if (kind === "defer" && !deferUntil) {
      onError("defer time is required");
      return;
    }
    run(async () => {
      if (internal) {
        const messageID = selectedEntry.internalMessage?.id || selectedEntry.item.messageId;
        if (kind === "reply") {
          await api("POST", "/api/comms/messages", {
            from: selectedEntry.agentName,
            replyTo: messageID,
            body: reply.trim(),
          });
          return;
        }
        if (kind === "no-reply") {
          await api("POST", `/api/comms/messages/${encodeURIComponent(messageID)}/no-reply`, {
            from: selectedEntry.agentName,
          });
          return;
        }
        throw new Error("This action is not available for internal messages");
      }
      const path = `/api/inbox/${encodeURIComponent(selectedEntry.item.id)}/${kind}`;
      const body =
        kind === "reply"
          ? { agent: selectedEntry.agentName, content: { text: reply.trim() } }
          : kind === "no-reply"
            ? { agent: selectedEntry.agentName, reason: reason.trim() }
            : kind === "defer"
              ? { agent: selectedEntry.agentName, reason: reason.trim(), until: new Date(deferUntil).toISOString() }
              : {};
      await api("POST", path, body);
    });
  };

  const sendOutbound = () => {
    if (!sendAgent || !sendAddress || !sendConversation.trim() || !sendText.trim()) {
      onError("agent, address, conversation and text are required");
      return;
    }
    const address = addresses.find((value) => value.id === sendAddress);
    if (!address) return;
    run(async () => {
      await api("POST", "/api/outbox", {
        agent: sendAgent,
        addressId: sendAddress,
        conversation: {
          provider: "external",
          connectionId: address.connectionId,
          conversationId: sendConversation.trim(),
        },
        content: { text: sendText.trim() },
        responseExpectation: "none",
        idempotencyKey: `web:${crypto.randomUUID()}`,
      });
      setSendText("");
      setComposeOpen(false);
      setMode("outbox");
    });
  };

  const counts = useMemo(() => {
    const result: Record<string, number> = { all: entries.length };
    for (const entry of entries) result[entry.item.state] = (result[entry.item.state] || 0) + 1;
    return result;
  }, [entries]);

  stateRef.current = {
    mode,
    entriesCount: entries.length,
    visibleCount: visibleEntries.length,
    outboxCount: outbox.length,
    selectedID,
    filter,
    origin,
  };
  useEffect(() => {
    const root = ((((window as any).codexLoom ||= (window as any).codexHub || {}) as Record<string, any>));
	(window as any).codexHub = root;
    root.inbox = {
      state: () => stateRef.current,
      select: async (id: string) => {
        selectItem(id);
        await new Promise((resolve) => setTimeout(resolve, 0));
        return { ...stateRef.current, selectedID: id };
      },
      setFilter: async (value: InboxFilter) => {
        setFilter(value);
        await new Promise((resolve) => setTimeout(resolve, 0));
        return { ...stateRef.current, filter: value };
      },
      refresh: async () => {
        await refresh();
        return stateRef.current;
      },
    };
    return () => {
      delete root.inbox;
    };
  }, []);

  return (
    <main className="flex w-full min-w-0 max-w-full flex-1 flex-col overflow-hidden bg-background">
      <header className="flex min-h-14 w-full max-w-full shrink-0 items-center gap-3 overflow-hidden border-b border-border bg-card/80 py-2 pl-14 pr-3 md:px-5">
        <Inbox className="size-4 shrink-0 text-primary" />
        <h1 className="min-w-0 truncate font-serif text-xl tracking-tight">Inbox</h1>
        <div className="ml-auto flex shrink-0 items-center gap-1">
          <div className="flex shrink-0 rounded-md border border-border bg-background p-0.5">
            {(["inbox", "outbox"] as const).map((value) => (
              <button
                key={value}
                onClick={() => {
                  setMode(value);
                  setSelectedID("");
                }}
                className={`h-7 rounded px-2.5 text-[11px] font-medium capitalize ${mode === value ? "bg-primary text-primary-foreground" : "text-muted-foreground"}`}
              >
                {value}
              </button>
            ))}
          </div>
          <button onClick={() => refresh().catch((error: Error) => onError(error.message))} title="Refresh" className="hidden size-8 items-center justify-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground sm:flex">
            <RefreshCw className="size-3.5" />
          </button>
          <button onClick={() => setComposeOpen((value) => !value)} title="New outbound message" className="hidden size-8 items-center justify-center rounded-md bg-primary text-primary-foreground hover:opacity-90 sm:flex">
            <Send className="size-3.5" />
          </button>
        </div>
      </header>

      {composeOpen && (
        <section className="grid shrink-0 gap-2 border-b border-border bg-card px-4 py-3 sm:grid-cols-2 xl:grid-cols-[160px_220px_1fr_2fr_auto]">
          <select value={sendAgent} onChange={(event) => { setSendAgent(event.target.value); setSendAddress(""); }} className={controlClass}>
            <option value="">Agent</option>
            {agents.map((agent) => <option key={agent.id} value={agent.name}>{agent.name}</option>)}
          </select>
          <select value={sendAddress} onChange={(event) => setSendAddress(event.target.value)} className={controlClass}>
            <option value="">Address</option>
            {addresses.filter((address) => address.enabled && agents.find((agent) => agent.name === sendAgent)?.id === address.agentId).map((address) => (
              <option key={address.id} value={address.id}>{address.displayName || address.externalIdentity}</option>
            ))}
          </select>
          <input value={sendConversation} onChange={(event) => setSendConversation(event.target.value)} placeholder="conversation id" className={controlClass} />
          <input value={sendText} onChange={(event) => setSendText(event.target.value)} placeholder="message" className={controlClass} />
          <button onClick={sendOutbound} disabled={working} className="h-8 rounded-md bg-primary px-3 text-[12px] font-medium text-primary-foreground disabled:opacity-50">Send</button>
        </section>
      )}

      {mode === "inbox" ? (
        <>
          <div className="flex shrink-0 items-center gap-2 overflow-x-auto border-b border-border px-3 py-2">
            {(["all", "queued", "handling", "deferred", "handled", "failed"] as const).map((value) => (
              <button key={value} onClick={() => setFilter(value)} className={`h-7 shrink-0 rounded-md px-2.5 text-[11px] font-medium capitalize ${filter === value ? "bg-foreground text-background" : "bg-muted/60 text-muted-foreground hover:text-foreground"}`}>
                {value} <span className="font-mono opacity-65">{counts[value] || 0}</span>
              </button>
            ))}
            <select value={origin} onChange={(event) => setOrigin(event.target.value)} className="ml-auto h-7 rounded-md border border-border bg-background px-2 text-[11px] outline-none">
              <option value="all">All sources</option>
              {origins.map((value) => <option key={value} value={value}>{value}</option>)}
            </select>
          </div>
          <div className="grid min-h-0 flex-1 grid-cols-1 overflow-hidden lg:grid-cols-[minmax(300px,380px)_1fr]">
            <section className={`${selectedEntry ? "hidden lg:block" : "block"} min-h-0 overflow-y-auto border-r border-border`}>
              {visibleEntries.map((entry) => (
                <InboxRow key={entry.item.id} entry={entry} selected={entry.item.id === selectedID} onClick={() => selectItem(entry.item.id)} />
              ))}
              {visibleEntries.length === 0 && <Empty label="No inbox items" />}
            </section>
            <section className={`${selectedEntry ? "block" : "hidden lg:block"} min-h-0 overflow-y-auto`}>
              {selectedEntry ? (
                <InboxInspector
                  entry={selectedEntry}
                  reply={reply}
                  reason={reason}
                  deferUntil={deferUntil}
                  working={working}
                  onReply={setReply}
                  onReason={setReason}
                  onDeferUntil={setDeferUntil}
                  onAction={respond}
                  onBack={() => setSelectedID("")}
                />
              ) : <Empty label="Select a message" />}
            </section>
          </div>
        </>
      ) : (
        <div className="grid min-h-0 flex-1 grid-cols-1 overflow-hidden lg:grid-cols-[minmax(300px,380px)_1fr]">
          <section className={`${selectedOutbox ? "hidden lg:block" : "block"} min-h-0 overflow-y-auto border-r border-border`}>
            {outbox.map((item) => (
              <button key={item.id} onClick={() => selectItem(item.id)} className={`block w-full border-b border-border px-4 py-3 text-left ${item.id === selectedID ? "bg-selection text-selection-foreground" : "hover:bg-muted/45"}`}>
                <div className="flex items-center gap-2"><StateDot state={item.state} /><span className="min-w-0 flex-1 truncate font-mono text-[11px]">{item.conversation.conversationId}</span><span className="text-[10px] text-muted-foreground">{formatTime(item.createdAt)}</span></div>
                <div className="mt-1 line-clamp-2 text-[12.5px] leading-5">{item.content.text || "Attachment"}</div>
              </button>
            ))}
            {outbox.length === 0 && <Empty label="No outbox items" />}
          </section>
          <section className={`${selectedOutbox ? "block" : "hidden lg:block"} min-h-0 overflow-y-auto`}>
            {selectedOutbox ? <OutboxInspector item={selectedOutbox} working={working} onBack={() => setSelectedID("")} onRetry={() => run(() => api("POST", `/api/outbox/${encodeURIComponent(selectedOutbox.id)}/retry`, {}))} /> : <Empty label="Select an outbound message" />}
          </section>
        </div>
      )}
    </main>
  );
}

function InboxRow({ entry, selected, onClick }: { entry: InboxEntry; selected: boolean; onClick: () => void }) {
  const subject = String(entry.message.providerMetadata?.subject || "");
  return (
    <button onClick={onClick} className={`block w-full overflow-hidden border-b border-border px-4 py-3 text-left ${selected ? "bg-selection text-selection-foreground" : "hover:bg-muted/45"}`}>
      <div className="flex min-w-0 items-center gap-2">
        <StateDot state={entry.item.state} />
        <span className="truncate text-[12px] font-semibold">{entry.message.sender.displayName || entry.message.sender.externalId}</span>
        <span className="rounded bg-muted px-1.5 py-0.5 font-mono text-[9px] uppercase text-muted-foreground">{entry.message.origin}</span>
        <span className="ml-auto shrink-0 text-[10px] text-muted-foreground">{formatTime(entry.item.createdAt)}</span>
      </div>
      {subject && <div className="mt-1 truncate text-[12px] font-medium">{subject}</div>}
      <div className="mt-0.5 line-clamp-2 break-words text-[12px] leading-5 text-muted-foreground">{entry.message.content.text || "Attachment"}</div>
      <div className="mt-1 flex items-center gap-2 font-mono text-[9px] uppercase text-muted-foreground/75">
        <span>{entry.agentName}</span>
        {entry.membership && <span className="min-w-0 truncate normal-case">{entry.membership.displayName || entry.membership.conversationId}</span>}
        <span>{entry.item.state}</span>{entry.item.outcome && <span>{entry.item.outcome}</span>}
      </div>
    </button>
  );
}

function InboxInspector({ entry, reply, reason, deferUntil, working, onReply, onReason, onDeferUntil, onAction, onBack }: {
  entry: InboxEntry;
  reply: string;
  reason: string;
  deferUntil: string;
  working: boolean;
  onReply: (value: string) => void;
  onReason: (value: string) => void;
  onDeferUntil: (value: string) => void;
  onAction: (kind: "reply" | "no-reply" | "defer" | "retry") => void;
  onBack: () => void;
}) {
  const actionable = entry.item.state !== "handled" && entry.item.state !== "cancelled";
  const internal = entry.message.origin === "loom" || entry.message.origin === "chub";
  const replyPolicy = entry.attempt?.effectiveReplyPolicy || entry.membership?.replyPolicy || entry.address.replyPolicy;
  const replyAllowed = entry.message.responseExpectation !== "none" && replyPolicy !== "none";
  const subject = String(entry.message.providerMetadata?.subject || "");
  return (
    <div className="mx-auto max-w-3xl p-4 md:p-6">
      <button onClick={onBack} className="mb-4 flex items-center gap-1 text-[12px] text-muted-foreground lg:hidden"><ArrowLeft className="size-3.5" /> Inbox</button>
      <div className="flex flex-wrap items-center gap-2 border-b border-border pb-3">
        <span className="font-mono text-[10px] uppercase text-muted-foreground">{entry.message.origin}</span>
        <span className="font-mono text-[10px] uppercase text-muted-foreground">{entry.item.state}</span>
        {entry.membership && <span className="max-w-52 truncate text-[10px] font-medium">{entry.membership.displayName || entry.membership.conversationId}</span>}
        <span className="ml-auto text-[11px] text-muted-foreground">{formatDate(entry.item.createdAt)}</span>
      </div>
      <h2 className="mt-5 text-lg font-semibold leading-6">{subject || entry.message.sender.displayName || entry.message.sender.externalId}</h2>
      {subject && <div className="mt-1 text-[12px] text-muted-foreground">From {entry.message.sender.displayName || entry.message.sender.externalId}</div>}
      <div className="mt-5 whitespace-pre-wrap break-words text-[14px] leading-6">{entry.message.content.text}</div>
      <AttachmentList attachments={entry.message.content.attachments || []} />
      <dl className="mt-6 grid gap-x-5 gap-y-2 border-y border-border py-3 text-[11px] sm:grid-cols-2">
        <Meta label="Agent" value={entry.agentName} />
        <Meta label="Expectation" value={entry.message.responseExpectation} />
        <Meta label="Conversation" value={entry.message.conversation.conversationId} />
        <Meta label="Message" value={entry.message.externalMessageId || entry.message.id} />
        <Meta label="Attempts" value={String(entry.item.attemptCount || 0)} />
        <Meta label="Reply policy" value={replyPolicy} />
        {entry.membership && <Meta label="Membership" value={`${entry.membership.id} · current v${entry.membership.version}`} />}
        {entry.attempt?.membershipVersion && <Meta label="Context used" value={`v${entry.attempt.membershipVersion}`} />}
      </dl>
      {entry.membership && (entry.membership.purpose || entry.membership.role || entry.membership.guidance) && (
        <section className="mt-5 border-y border-border py-4">
          <div className="mb-3 flex items-center justify-between">
            <h3 className="text-[11px] font-semibold uppercase text-muted-foreground">Conversation context</h3>
            <span className="font-mono text-[9px] text-muted-foreground">{entry.membership.trustDomain}</span>
          </div>
          <div className="space-y-3 text-[12px] leading-5">
            {entry.membership.purpose && <ContextField label="Purpose" value={entry.membership.purpose} />}
            {entry.membership.role && <ContextField label="Role" value={entry.membership.role} />}
            {entry.membership.guidance && <ContextField label="Guidance" value={entry.membership.guidance} />}
          </div>
        </section>
      )}
      {entry.item.lastError && <div className="mt-4 border-l-2 border-destructive bg-destructive/5 px-3 py-2 text-[12px] text-destructive">{entry.item.lastError}</div>}
      {entry.item.note && <div className="mt-4 border-l-2 border-primary bg-primary/5 px-3 py-2 text-[12px] text-muted-foreground">{entry.item.note}</div>}
      {entry.outboxItem && <div className="mt-4 flex items-center gap-2 border-l-2 border-success bg-success/5 px-3 py-2 text-[12px]"><Check className="size-3.5 text-success" /> Reply {entry.outboxItem.state}</div>}
      {actionable && (
        <div className="mt-6 border-t border-border pt-4">
          {replyAllowed && <textarea value={reply} onChange={(event) => onReply(event.target.value)} rows={5} placeholder="Reply" className="w-full resize-y rounded-md bg-card p-3 text-[13px] leading-5 outline-none ring-1 ring-border focus:ring-ring/25" />}
          <div className="mt-2 flex flex-wrap items-center gap-2">
            {replyAllowed && <button onClick={() => onAction("reply")} disabled={working || !reply.trim()} className="flex h-8 items-center gap-1.5 rounded-md bg-primary px-3 text-[12px] font-medium text-primary-foreground disabled:opacity-50"><MessageSquareReply className="size-3.5" /> Reply</button>}
            <button onClick={() => onAction("no-reply")} disabled={working} className="flex h-8 items-center gap-1.5 rounded-md border border-border px-3 text-[12px] text-muted-foreground hover:text-foreground disabled:opacity-50"><SkipForward className="size-3.5" /> No reply</button>
            {!internal && (entry.item.state === "failed" || entry.item.state === "deferred") && <button onClick={() => onAction("retry")} disabled={working} className="flex h-8 items-center gap-1.5 rounded-md border border-border px-3 text-[12px]"><RotateCcw className="size-3.5" /> Retry</button>}
          </div>
          {!internal && (
            <div className="mt-3 grid gap-2 sm:grid-cols-[1fr_190px_auto]">
              <input value={reason} onChange={(event) => onReason(event.target.value)} placeholder="reason" className={controlClass} />
              <input type="datetime-local" value={deferUntil} onChange={(event) => onDeferUntil(event.target.value)} className={controlClass} />
              <button onClick={() => onAction("defer")} disabled={working || !deferUntil} className="flex h-8 items-center justify-center gap-1.5 rounded-md border border-border px-3 text-[12px] disabled:opacity-50"><Clock3 className="size-3.5" /> Defer</button>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

function ContextField({ label, value }: { label: string; value: string }) {
  return (
    <div className="grid min-w-0 gap-1 sm:grid-cols-[72px_1fr]">
      <div className="font-mono text-[9px] uppercase text-muted-foreground">{label}</div>
      <div className="min-w-0 whitespace-pre-wrap break-words">{value}</div>
    </div>
  );
}

function OutboxInspector({ item, working, onBack, onRetry }: { item: OutboxItem; working: boolean; onBack: () => void; onRetry: () => void }) {
  return <div className="mx-auto max-w-3xl p-4 md:p-6">
    <button onClick={onBack} className="mb-4 flex items-center gap-1 text-[12px] text-muted-foreground lg:hidden"><ArrowLeft className="size-3.5" /> Outbox</button>
    <div className="flex items-center gap-2 border-b border-border pb-3"><StateDot state={item.state} /><span className="font-mono text-[11px] uppercase">{item.state}</span><span className="ml-auto text-[11px] text-muted-foreground">{formatDate(item.createdAt)}</span></div>
    <div className="mt-5 whitespace-pre-wrap break-words text-[14px] leading-6">{item.content.text}</div>
    <AttachmentList attachments={item.content.attachments || []} />
    <dl className="mt-6 grid gap-x-5 gap-y-2 border-y border-border py-3 text-[11px] sm:grid-cols-2">
      <Meta label="Conversation" value={item.conversation.conversationId} /><Meta label="Address" value={item.addressId} /><Meta label="Attempts" value={String(item.attemptCount)} /><Meta label="External message" value={item.externalMessageId || "-"} /><Meta label="Idempotency key" value={item.idempotencyKey} />
    </dl>
    {item.lastError && <div className="mt-4 border-l-2 border-destructive bg-destructive/5 px-3 py-2 text-[12px] text-destructive">{item.lastError}</div>}
    {item.state === "failed" && <button onClick={onRetry} disabled={working} className="mt-5 flex h-8 items-center gap-1.5 rounded-md bg-primary px-3 text-[12px] font-medium text-primary-foreground disabled:opacity-50"><RotateCcw className="size-3.5" /> Retry</button>}
  </div>;
}

function Meta({ label, value }: { label: string; value: string }) {
  return <div className="min-w-0"><dt className="uppercase text-muted-foreground">{label}</dt><dd className="mt-0.5 break-all font-mono text-foreground">{value || "-"}</dd></div>;
}

function AttachmentList({ attachments }: { attachments: NonNullable<InboxEntry["message"]["content"]["attachments"]> }) {
  if (attachments.length === 0) return null;
  return (
    <div className="mt-5 divide-y divide-border border-y border-border">
      {attachments.map((attachment, index) => {
        const label = attachment.name || attachment.id || `Attachment ${index + 1}`;
        const detail = [attachment.mimeType, attachment.size ? formatBytes(attachment.size) : "", attachment.path || attachment.id || ""].filter(Boolean).join(" · ");
        const body = <><Paperclip className="size-3.5 shrink-0 text-muted-foreground" /><span className="min-w-0 flex-1"><span className="block truncate text-[12px] font-medium">{label}</span>{detail && <span className="block truncate font-mono text-[9px] text-muted-foreground">{detail}</span>}</span></>;
        return attachment.url ? (
          <a key={`${attachment.id || label}-${index}`} href={attachment.url} target="_blank" rel="noreferrer" className="flex min-w-0 items-center gap-2 py-2.5 hover:text-primary">{body}</a>
        ) : (
          <div key={`${attachment.id || label}-${index}`} className="flex min-w-0 items-center gap-2 py-2.5">{body}</div>
        );
      })}
    </div>
  );
}

function formatBytes(value: number) {
  if (value < 1024) return `${value} B`;
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KB`;
  return `${(value / 1024 / 1024).toFixed(1)} MB`;
}

function StateDot({ state }: { state: string }) {
  const color = state === "sent" || state === "handled" || state === "connected" ? "bg-success" : state === "failed" || state === "degraded" ? "bg-destructive" : state === "queued" || state === "handling" || state === "sending" || state === "pending" ? "bg-warning" : "bg-muted-foreground/45";
  return <span className={`size-2 shrink-0 rounded-full ${color}`} />;
}

function Empty({ label }: { label: string }) {
  return <div className="flex h-full min-h-40 items-center justify-center text-[12px] text-muted-foreground">{label}</div>;
}

function formatTime(value: string) {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

function formatDate(value: string) {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
}

const controlClass = "h-8 min-w-0 rounded-md border border-border bg-background px-2.5 text-[12px] outline-none focus:border-ring";
