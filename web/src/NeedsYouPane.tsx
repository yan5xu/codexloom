import { ArrowLeft, Check, ChevronRight, CircleHelp, Clock3, Loader2, RotateCcw, Send, X } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { MarkdownContent } from "./pages/agent/markdown";
import { api, type HumanRequest } from "./types";

type Filter = "open" | "answered" | "all";

export function NeedsYouPane({
  requests,
  onChanged,
  onOpenAgent,
  onError,
}: {
  requests: HumanRequest[];
  onChanged: () => Promise<unknown> | void;
  onOpenAgent: (agentID: string) => void;
  onError: (message: string) => void;
}) {
  const [filter, setFilter] = useState<Filter>("open");
  const [selectedID, setSelectedID] = useState("");
  const [answer, setAnswer] = useState("");
  const [working, setWorking] = useState(false);
  const stateRef = useRef<Record<string, unknown>>({});

  const counts = useMemo(() => ({
    open: requests.filter((request) => request.state === "open").length,
    answered: requests.filter((request) => request.state === "answered").length,
    all: requests.length,
  }), [requests]);

  const visible = useMemo(() => {
    const filtered = filter === "all" ? requests : requests.filter((request) => request.state === filter);
    return [...filtered].sort((a, b) => {
      if (filter === "open" && a.expectation !== b.expectation) return a.expectation === "required" ? -1 : 1;
      return b.createdAt.localeCompare(a.createdAt);
    });
  }, [filter, requests]);
  const selected = requests.find((request) => request.id === selectedID) || null;

  useEffect(() => {
    const syncFromHash = () => {
      const [route, query = ""] = window.location.hash.slice(1).split("?");
      if (route !== "needs-you") return;
      setSelectedID(new URLSearchParams(query).get("request") || "");
    };
    syncFromHash();
    window.addEventListener("hashchange", syncFromHash);
    return () => window.removeEventListener("hashchange", syncFromHash);
  }, []);

  useEffect(() => {
    if (selectedID && !requests.some((request) => request.id === selectedID)) setSelectedID("");
  }, [requests, selectedID]);

  const selectRequest = (id: string) => {
    setSelectedID(id);
    setAnswer("");
    window.history.replaceState(null, "", `#needs-you?request=${encodeURIComponent(id)}`);
  };

  const run = async (task: () => Promise<unknown>) => {
    if (working) return;
    setWorking(true);
    try {
      await task();
      await onChanged();
    } catch (error: any) {
      onError(error.message);
    } finally {
      setWorking(false);
    }
  };

  const submitAnswer = () => {
    if (!selected || !answer.trim()) return;
    const text = answer.trim();
    setAnswer("");
    run(async () => {
      try {
        await api("POST", `/api/human-requests/${encodeURIComponent(selected.id)}/answer`, { answer: text });
      } catch (error) {
        setAnswer(text);
        throw error;
      }
    });
  };

  const cancel = () => {
    if (!selected) return;
    run(() => api("POST", `/api/human-requests/${encodeURIComponent(selected.id)}/cancel`));
  };

  const retry = () => {
    if (!selected) return;
    run(() => api("POST", `/api/human-requests/${encodeURIComponent(selected.id)}/retry`));
  };

  stateRef.current = {
    requestsCount: requests.length,
    openCount: counts.open,
    answeredCount: counts.answered,
    visibleCount: visible.length,
    selectedRequestId: selectedID || null,
    filter,
  };
  useEffect(() => {
    const root = (((window as any).codexLoom ||= (window as any).codexHub || {}) as Record<string, any>);
    (window as any).codexHub = root;
    root.needsYou = {
      state: () => stateRef.current,
      select: async (id: string) => {
        selectRequest(id);
        await new Promise((resolve) => setTimeout(resolve, 50));
        return { ...stateRef.current, selectedRequestId: id };
      },
      setFilter: async (value: Filter) => {
        setFilter(value);
        await new Promise((resolve) => setTimeout(resolve, 50));
        return { ...stateRef.current, filter: value };
      },
    };
    return () => {
      if (root.needsYou) delete root.needsYou;
    };
  }, [requests, selectedID, filter, visible.length]);

  return (
    <main className="flex min-w-0 flex-1 flex-col bg-background">
      <header className="flex h-14 shrink-0 items-center justify-between border-b border-border bg-card/80 pl-14 pr-3 backdrop-blur md:px-5">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <CircleHelp className="size-4 text-warning" />
            <h1 className="truncate font-serif text-xl">Needs You</h1>
          </div>
          <div className="mt-0.5 hidden font-mono text-[10px] uppercase text-muted-foreground md:block">requests from your agents</div>
        </div>
        <div className="flex rounded-sm border border-border bg-background p-0.5">
          {(["open", "answered", "all"] as const).map((value) => (
            <button
              key={value}
              type="button"
              onClick={() => setFilter(value)}
              className={`h-7 whitespace-nowrap rounded-sm px-2 text-[11.5px] font-medium capitalize sm:px-3 ${filter === value ? "bg-primary text-primary-foreground" : "text-muted-foreground hover:text-foreground"}`}
            >
              {value}{counts[value] > 0 ? <span className="ml-1 font-mono opacity-70">{counts[value]}</span> : null}
            </button>
          ))}
        </div>
      </header>

      <div className="grid min-h-0 flex-1 grid-cols-1 overflow-hidden lg:grid-cols-[360px_minmax(0,1fr)]">
        <section className={`${selected ? "hidden lg:flex" : "flex"} min-h-0 flex-col border-r border-border bg-card/35`} aria-label="Human requests">
          <div className="flex h-10 shrink-0 items-center justify-between border-b border-border px-3">
            <span className="text-[11px] font-semibold">{filter === "open" ? "Waiting for you" : filter === "answered" ? "Answered" : "Request history"}</span>
            <span className="font-mono text-[9.5px] text-muted-foreground">{visible.length}</span>
          </div>
          <div className="min-h-0 flex-1 overflow-y-auto">
            {visible.map((request) => (
              <button
                key={request.id}
                type="button"
                onClick={() => selectRequest(request.id)}
                className={`group flex w-full min-w-0 items-start gap-3 border-b border-border/70 px-3 py-3 text-left outline-none transition hover:bg-muted/55 focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring/30 ${selectedID === request.id ? "bg-selection" : ""}`}
              >
                <span className={`mt-1 size-2 shrink-0 rounded-full ${request.state === "open" ? request.expectation === "required" ? "bg-warning ring-2 ring-warning/15" : "bg-ring/70" : request.deliveryStatus === "failed" ? "bg-destructive" : "bg-success/70"}`} />
                <span className="min-w-0 flex-1">
                  <span className="flex min-w-0 items-center gap-2">
                    <span className="truncate text-[12px] font-semibold">{request.agentName}</span>
                    {request.state === "open" ? <span className="shrink-0 font-mono text-[8.5px] uppercase text-muted-foreground">{request.expectation}</span> : null}
                  </span>
                  <span className="mt-1 block line-clamp-2 text-[11.5px] leading-4 text-foreground/85">{request.question}</span>
                  <span className="mt-1.5 block font-mono text-[9px] text-muted-foreground">{relativeTime(request.createdAt)}</span>
                </span>
                <ChevronRight className="mt-1 size-3.5 shrink-0 text-muted-foreground/45 group-hover:text-muted-foreground" />
              </button>
            ))}
            {visible.length === 0 ? (
              <div className="flex h-full min-h-64 flex-col items-center justify-center px-6 text-center">
                <Check className="size-5 text-success/70" />
                <div className="mt-2 text-[12px] font-medium">{filter === "open" ? "Nothing needs your input" : "No requests in this view"}</div>
                <div className="mt-1 max-w-60 text-[10.5px] leading-4 text-muted-foreground">Agents can keep working until they explicitly need a decision, fact, or authorization.</div>
              </div>
            ) : null}
          </div>
        </section>

        <section className={`${selected ? "flex" : "hidden lg:flex"} min-h-0 min-w-0 flex-col bg-background`} aria-label="Human request detail">
          {selected ? (
            <>
              <div className="flex h-11 shrink-0 items-center gap-2 border-b border-border px-3 md:px-5">
                <button type="button" onClick={() => { setSelectedID(""); window.history.replaceState(null, "", "#needs-you"); }} className="flex size-7 items-center justify-center rounded-sm text-muted-foreground hover:bg-muted lg:hidden" aria-label="Back to requests"><ArrowLeft className="size-4" /></button>
                <button type="button" onClick={() => onOpenAgent(selected.agentId)} className="min-w-0 truncate text-[11.5px] font-semibold hover:underline">{selected.agentName}</button>
                <span className={`rounded-sm px-1.5 py-0.5 font-mono text-[8.5px] uppercase ${selected.expectation === "required" ? "bg-warning/10 text-warning" : "bg-muted text-muted-foreground"}`}>{selected.expectation}</span>
                <span className="ml-auto flex items-center gap-1 font-mono text-[9px] text-muted-foreground"><Clock3 className="size-3" />{formatTime(selected.createdAt)}</span>
              </div>
              <div className="min-h-0 flex-1 overflow-y-auto">
                <div className="mx-auto max-w-[760px] px-4 py-5 md:px-8 md:py-8">
                  <h2 className="font-serif text-[26px] leading-tight text-foreground md:text-[30px]">{selected.question}</h2>
                  {selected.context ? <div className="mt-5 border-l-2 border-border pl-4 text-[13px] leading-6 text-foreground/80"><MarkdownContent content={selected.context} /></div> : null}
                  {selected.blockedWork ? (
                    <section className="mt-6 border-y border-border bg-card/40 px-3 py-3">
                      <div className="font-mono text-[9px] uppercase text-muted-foreground">Waiting on this answer</div>
                      <div className="mt-1.5 text-[12.5px] leading-5">{selected.blockedWork}</div>
                    </section>
                  ) : null}

                  {selected.state === "open" ? (
                    <div className="mt-6">
                      {selected.options?.length ? (
                        <div className="mb-3 grid gap-2 sm:grid-cols-2">
                          {selected.options.map((option) => (
                            <button key={option.label} type="button" onClick={() => setAnswer(option.label)} className={`min-w-0 rounded-sm border px-3 py-2.5 text-left transition hover:border-ring/50 hover:bg-selection/60 ${answer === option.label ? "border-ring bg-selection" : "border-border bg-card"}`}>
                              <span className="block text-[12px] font-semibold">{option.label}</span>
                              {option.description ? <span className="mt-1 block text-[10.5px] leading-4 text-muted-foreground">{option.description}</span> : null}
                            </button>
                          ))}
                        </div>
                      ) : null}
                      <div className="rounded-sm border border-input bg-card p-1.5 shadow-card focus-within:border-ring focus-within:ring-2 focus-within:ring-ring/20">
                        <textarea
                          rows={4}
                          value={answer}
                          disabled={working}
                          onChange={(event) => setAnswer(event.target.value)}
                          onKeyDown={(event) => {
                            if ((event.metaKey || event.ctrlKey) && event.key === "Enter" && !working) submitAnswer();
                          }}
                          placeholder={`Answer ${selected.agentName}…`}
                          className="max-h-64 min-h-28 w-full resize-y bg-transparent px-2.5 py-2 text-[13px] leading-6 outline-none placeholder:text-muted-foreground/55"
                        />
                        <div className="flex items-center justify-between gap-2 px-1 pb-0.5">
                          <button type="button" onClick={cancel} disabled={working} className="flex h-8 items-center gap-1.5 rounded-sm px-2 text-[11px] text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-50"><X className="size-3.5" />Dismiss</button>
                          <button type="button" onClick={submitAnswer} disabled={working || !answer.trim()} className="shadow-action flex h-8 items-center gap-2 rounded-sm bg-primary px-3 text-[11.5px] font-medium text-primary-foreground disabled:cursor-not-allowed disabled:opacity-40">{working ? <Loader2 className="size-3.5 animate-spin" /> : <Send className="size-3.5" />}Answer</button>
                        </div>
                      </div>
                      <div className="mt-2 text-[9.5px] text-muted-foreground">Your answer is queued safely if the Agent is busy, then resumes this same Thread.</div>
                    </div>
                  ) : selected.state === "answered" ? (
                    <div className="mt-6">
                      <div className="font-mono text-[9px] uppercase text-muted-foreground">Your answer</div>
                      <div className="mt-2 border-l-2 border-success/40 pl-4 text-[13px] leading-6"><MarkdownContent content={selected.answer || ""} /></div>
                      <div className="mt-4 flex items-center gap-2 border-t border-border pt-3 font-mono text-[9.5px] text-muted-foreground">
                        <span className={`size-1.5 rounded-full ${selected.deliveryStatus === "failed" ? "bg-destructive" : selected.deliveryStatus === "delivered" ? "bg-success" : "bg-warning"}`} />
                        {deliveryLabel(selected)}
                        {selected.deliveryStatus === "failed" ? <button type="button" onClick={retry} disabled={working} className="ml-auto flex items-center gap-1 rounded-sm px-2 py-1 text-destructive hover:bg-destructive/10"><RotateCcw className="size-3" />Retry</button> : null}
                      </div>
                      {selected.lastError ? <div className="mt-2 break-words font-mono text-[9.5px] text-destructive">{selected.lastError}</div> : null}
                    </div>
                  ) : (
                    <div className="mt-6 text-[12px] text-muted-foreground">This request was dismissed without an answer.</div>
                  )}
                </div>
              </div>
            </>
          ) : (
            <div className="flex flex-1 flex-col items-center justify-center text-center text-muted-foreground">
              <CircleHelp className="size-6 opacity-45" />
              <div className="mt-2 text-[12px] font-medium text-foreground/70">Select a request</div>
              <div className="mt-1 text-[10.5px]">Review its context before answering.</div>
            </div>
          )}
        </section>
      </div>
    </main>
  );
}

function deliveryLabel(request: HumanRequest) {
  if (request.deliveryStatus === "delivered") return "Delivered to a new Turn";
  if (request.deliveryStatus === "delivering") return "Starting the follow-up Turn";
  if (request.deliveryStatus === "queued") return "Answer queued until the Agent is idle";
  if (request.deliveryStatus === "failed") return "Delivery failed";
  return request.deliveryStatus;
}

function formatTime(value: string) {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
}

function relativeTime(value: string) {
  const timestamp = Date.parse(value);
  if (!Number.isFinite(timestamp)) return value;
  const seconds = Math.max(0, Math.floor((Date.now() - timestamp) / 1000));
  if (seconds < 60) return "just now";
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  return `${Math.floor(hours / 24)}d ago`;
}
