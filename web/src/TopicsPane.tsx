import {
  ArrowLeft,
  ArrowUpRight,
  Check,
  ChevronRight,
  CircleHelp,
  FileText,
  GitBranch,
  Loader2,
  MessageSquare,
  Pause,
  Plus,
  Send,
  Square,
  X,
} from "lucide-react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { MarkdownContent } from "./pages/agent/markdown";
import { PublishedArtifactCard } from "./components/ArtifactPreview";
import { Button } from "./components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "./components/ui/dialog";
import { Input } from "./components/ui/input";
import { api, type Agent, type ThreadArtifact, type Topic, type TopicActiveTurn, type TopicLink, type TopicStatus, type TopicSummary } from "./types";
import { topicAuditSummary, topicBriefTimeline } from "./topics-view";
import { agentLabel } from "./agent-label";

type TopicFilter = "attention" | "active" | "waiting" | "resolved" | "all";
type TopicDetailView = "current" | "history";

type CreateRequest = {
  agentId?: string;
  nonce: number;
} | null;

type TopicParticipantDraft = { agent: string; responsibility: string };
type CreateTopicDraft = {
  title: string;
  responsibleAgent: string;
  purpose: string;
  completionBoundary: string;
  summary: string;
  participants: TopicParticipantDraft[];
};

function emptyCreateDraft(responsibleAgent = ""): CreateTopicDraft {
  return { title: "", responsibleAgent, purpose: "", completionBoundary: "", summary: "", participants: [] };
}

const textareaClass = "w-full resize-y rounded-md border border-input bg-card px-2.5 py-2 text-[12px] leading-5 shadow-[inset_0_1px_2px_rgb(32_33_31/0.05)] outline-none transition focus:border-ring focus:ring-2 focus:ring-ring/15";
const selectClass = "h-8 w-full rounded-md border border-input bg-card px-2.5 text-[11.5px] outline-none focus:border-ring focus:ring-2 focus:ring-ring/15";

export function TopicsPane({
  agents,
  createRequest,
  onCreateRequestHandled,
  onOpenAgent,
  onError,
}: {
  agents: Agent[];
  createRequest: CreateRequest;
  onCreateRequestHandled: () => void;
  onOpenAgent: (agentID: string) => void;
  onError: (message: string) => void;
}) {
  const queryClient = useQueryClient();
  const topicsQuery = useQuery<{ topics: TopicSummary[] }>({
    queryKey: ["topics"],
    queryFn: () => api("GET", "/api/topics?view=summary"),
    refetchInterval: 30_000,
  });
  const topics = topicsQuery.data?.topics || [];
  const [filter, setFilter] = useState<TopicFilter>("attention");
  const [selectedID, setSelectedID] = useState("");
  const [detailView, setDetailView] = useState<TopicDetailView>("current");
  const [createOpen, setCreateOpen] = useState(false);
  const [working, setWorking] = useState(false);
  const [message, setMessage] = useState("");
  const [evidenceOpen, setEvidenceOpen] = useState(false);
  const [intervention, setIntervention] = useState<{ turn: TopicActiveTurn; action: "steer" | "interrupt" } | null>(null);
  const automationRef = useRef<Record<string, unknown>>({});

  const [createDraft, setCreateDraft] = useState<CreateTopicDraft>(() => emptyCreateDraft());
  const [evidenceDraft, setEvidenceDraft] = useState({ type: "message", id: "", label: "" });
  const [interventionDraft, setInterventionDraft] = useState({ text: "", reason: "" });

  const selectedSummary = topics.find((topic) => topic.id === selectedID) || null;
  const selectedQuery = useQuery<{ topic: Topic }>({
    queryKey: ["topic", selectedID],
    queryFn: () => api("GET", `/api/topics/${encodeURIComponent(selectedID)}`),
    enabled: Boolean(selectedID),
  });
  const selected = selectedQuery.data?.topic || null;
  const selectedParticipants = selected?.participants || [];
  const selectedActiveTurns = selected?.activeTurns || [];
  const agentLabelsByID = useMemo(() => new Map(agents.map((agent) => [agent.id, agentLabel(agent)])), [agents]);
  const selectedResponsibleLabel = selected ? agentLabelsByID.get(selected.responsibleAgentId) || selected.responsibleAgent : "";
  const evidenceLinks = useMemo(() => (selected?.links || []).filter((link) => link.relation !== "activity"), [selected?.links]);
  const topicArtifactsQuery = useQuery<{ artifacts: ThreadArtifact[] }>({
    queryKey: ["topic-artifacts", selectedID],
    queryFn: () => api("GET", `/api/topics/${encodeURIComponent(selectedID)}/artifacts`),
    enabled: Boolean(selectedID),
  });
  const topicArtifacts = useMemo(
    () => new Map((topicArtifactsQuery.data?.artifacts || []).map((artifact) => [artifact.id, artifact])),
    [topicArtifactsQuery.data?.artifacts],
  );
  const briefTimeline = useMemo(() => selected ? topicBriefTimeline(selected) : [], [selected]);
  const counts = useMemo(() => ({
    attention: topics.filter((topic) => topic.needsMeCount > 0 || topic.resultsReady).length,
    active: topics.filter((topic) => topic.status === "active").length,
    waiting: topics.filter((topic) => topic.status === "waiting").length,
    resolved: topics.filter((topic) => topic.status === "resolved").length,
    all: topics.length,
  }), [topics]);
  const visible = useMemo(() => {
    const filtered = topics.filter((topic) => {
      if (filter === "attention") return topic.needsMeCount > 0 || topic.resultsReady;
      if (filter === "all") return topic.status !== "archived";
      return topic.status === filter;
    });
    return [...filtered].sort((a, b) => {
      const attentionA = (a.needsMeCount > 0 ? 2 : 0) + (a.resultsReady ? 1 : 0);
      const attentionB = (b.needsMeCount > 0 ? 2 : 0) + (b.resultsReady ? 1 : 0);
      return attentionB - attentionA || b.updatedAt.localeCompare(a.updatedAt);
    });
  }, [filter, topics]);

  const refresh = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ["topics"] }),
      selectedID ? Promise.all([
        queryClient.invalidateQueries({ queryKey: ["topic", selectedID] }),
        queryClient.invalidateQueries({ queryKey: ["topic-artifacts", selectedID] }),
      ]) : Promise.resolve(),
    ]);
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

  useEffect(() => {
    const syncHash = () => {
      const [route, query = ""] = window.location.hash.slice(1).split("?");
      if (route !== "topics") return;
      const params = new URLSearchParams(query);
      setSelectedID(params.get("topic") || "");
      setDetailView(params.get("view") === "history" ? "history" : "current");
    };
    syncHash();
    window.addEventListener("hashchange", syncHash);
    return () => window.removeEventListener("hashchange", syncHash);
  }, []);

  useEffect(() => {
    if (!topicsQuery.isFetched || !selectedID || topics.some((topic) => topic.id === selectedID)) return;
    setSelectedID("");
  }, [selectedID, topics, topicsQuery.isFetched]);

  useEffect(() => {
    if (!selectedSummary || topicMatchesFilter(selectedSummary, filter)) return;
    setFilter(selectedSummary.status === "archived" ? "all" : selectedSummary.status);
  }, [selectedSummary?.id]);

  useEffect(() => {
    if (!createRequest) return;
    const agent = agents.find((candidate) => candidate.id === createRequest.agentId);
    setCreateDraft(emptyCreateDraft(agent?.id));
    setCreateOpen(true);
    onCreateRequestHandled();
  }, [createRequest?.nonce]);

  useEffect(() => {
    if (!selectedSummary?.resultsReady) return;
    let active = true;
    let requested = false;
    const markRead = () => {
      if (!active || requested || document.visibilityState !== "visible") return;
      requested = true;
      void api("POST", `/api/topics/${encodeURIComponent(selectedSummary.id)}/read`, {})
        .then(refresh)
        .catch(() => { requested = false; });
    };
    markRead();
    document.addEventListener("visibilitychange", markRead);
    return () => {
      active = false;
      document.removeEventListener("visibilitychange", markRead);
    };
  }, [selectedSummary?.id, selectedSummary?.resultsReady]);

  useEffect(() => {
    const root = (((window as any).codexLoom ||= (window as any).codexHub || {}) as Record<string, any>);
    const state = () => ({
      count: topics.length,
      selectedTopicId: selectedID || null,
      filter,
      detailView,
      needsMe: topics.filter((topic) => topic.needsMeCount > 0).map((topic) => topic.id),
      resultsReady: topics.filter((topic) => topic.resultsReady).map((topic) => topic.id),
      visibleTopicIds: visible.map((topic) => topic.id),
      activeTurns: selected?.activeTurns || selectedSummary?.activeTurns || [],
    });
    automationRef.current = { state };
    root.topics = {
      state,
      select: async (id: string) => {
        selectTopic(id);
        await delay();
        return state();
      },
      setFilter: async (next: TopicFilter) => {
        if (!["attention", "active", "waiting", "resolved", "all"].includes(next)) throw new Error(`Unknown Topic filter: ${next}`);
        setFilter(next);
        await delay();
        return automationRef.current.state instanceof Function ? automationRef.current.state() : state();
      },
      setView: async (next: TopicDetailView) => {
        if (!selectedID || !["current", "history"].includes(next)) throw new Error(`Unknown Topic detail view: ${next}`);
        changeDetailView(next);
        await delay();
        return automationRef.current.state instanceof Function ? automationRef.current.state() : state();
      },
      openCreate: async (agent?: string) => {
        const resolved = agents.find((candidate) => candidate.id === agent || candidate.name === agent);
        setCreateDraft(emptyCreateDraft(resolved?.id));
        setCreateOpen(true);
        await delay();
        return state();
      },
    };
  }, [agents, detailView, filter, selected, selectedID, selectedSummary, topics, visible]);

  const selectTopic = (id: string) => {
    setSelectedID(id);
    setDetailView("current");
    window.history.replaceState(null, "", `#topics?topic=${encodeURIComponent(id)}`);
  };

  const changeDetailView = (next: TopicDetailView) => {
    setDetailView(next);
    if (!selectedID) return;
    const suffix = next === "history" ? "&view=history" : "";
    window.history.replaceState(null, "", `#topics?topic=${encodeURIComponent(selectedID)}${suffix}`);
  };

  const createTopic = () => run(async () => {
    const response = await api("POST", "/api/topics", {
      ...createDraft,
      createdBy: "owner",
      initialBrief: { summary: createDraft.summary },
    }) as { topic: Topic };
    setCreateOpen(false);
    setCreateDraft(emptyCreateDraft());
    selectTopic(response.topic.id);
  });

  const linkEvidence = () => selected && run(async () => {
    await api("POST", `/api/topics/${encodeURIComponent(selected.id)}/links`, { actor: "owner", relation: "evidence", ...evidenceDraft });
    setEvidenceOpen(false);
    setEvidenceDraft({ type: "message", id: "", label: "" });
  });

  const sendInput = () => {
    if (working || !selected || !message.trim()) return;
    const text = message.trim();
    setMessage("");
    run(async () => {
      try {
        await api("POST", `/api/topics/${encodeURIComponent(selected.id)}/send`, { text });
      } catch (error) {
        setMessage(text);
        throw error;
      }
    });
  };

  const intervene = () => intervention && selected && run(async () => {
    await api("POST", `/api/topics/${encodeURIComponent(selected.id)}/interventions`, {
      agent: intervention.turn.agentId,
      action: intervention.action,
      text: interventionDraft.text,
      reason: interventionDraft.reason,
    });
    setIntervention(null);
    setInterventionDraft({ text: "", reason: "" });
  });

  return (
    <main className="flex min-h-0 min-w-0 flex-1 bg-background" aria-label="Topics workspace">
      <div className="grid min-h-0 min-w-0 flex-1 grid-cols-1 lg:grid-cols-[300px_minmax(0,1fr)]">
        <section className={`${selectedID ? "hidden lg:flex" : "flex"} min-h-0 flex-col border-r border-border bg-sidebar/25`} aria-label="Topics list">
          <header className="flex h-12 shrink-0 items-center gap-2 border-b border-border px-3">
            <GitBranch className="size-4 text-primary" />
            <div className="min-w-0 flex-1">
              <h1 className="text-[13px] font-semibold">Topics</h1>
              <p className="text-[9.5px] text-muted-foreground">Shared coordination across Agent work</p>
            </div>
            <Button size="icon-sm" onClick={() => { setCreateDraft(emptyCreateDraft()); setCreateOpen(true); }} title="Track a new Topic" aria-label="Track a new Topic"><Plus /></Button>
          </header>
          <div className="flex shrink-0 gap-1 overflow-x-auto border-b border-border px-2 py-2 [scrollbar-width:none]">
            {(["attention", "active", "waiting", "resolved", "all"] as TopicFilter[]).map((value) => (
              <button key={value} type="button" onClick={() => setFilter(value)} className={`h-6 shrink-0 rounded-sm px-2 font-mono text-[9px] capitalize ${filter === value ? "bg-selection font-semibold text-selection-foreground" : "text-muted-foreground hover:bg-muted"}`}>
                {value === "attention" ? "For you" : value} {counts[value]}
              </button>
            ))}
          </div>
          <div className="min-h-0 flex-1 overflow-y-auto">
            {visible.map((topic) => <TopicListItem key={topic.id} topic={topic} responsibleLabel={agentLabelsByID.get(topic.responsibleAgentId) || topic.responsibleAgent} selected={topic.id === selectedID} onSelect={() => selectTopic(topic.id)} />)}
            {visible.length === 0 ? (
              <div className="flex h-full min-h-64 flex-col items-center justify-center px-6 text-center text-muted-foreground">
                <GitBranch className="size-5 opacity-45" />
                <div className="mt-2 text-[12px] font-medium text-foreground/75">No Topics in this view</div>
                <p className="mt-1 max-w-56 text-[10.5px] leading-4">Track work that must survive multiple Turns, waiting periods, or Agent handoffs.</p>
              </div>
            ) : null}
          </div>
        </section>

        <section className={`${selectedID ? "flex" : "hidden lg:flex"} min-h-0 min-w-0 flex-col`} aria-label="Topic detail">
          {selected ? (
            <>
              <header className="flex min-h-12 shrink-0 items-center gap-2 border-b border-border px-3 md:px-5">
                <Button variant="ghost" size="icon-sm" onClick={() => { setSelectedID(""); window.history.replaceState(null, "", "#topics"); }} className="lg:hidden" aria-label="Back to Topics"><ArrowLeft /></Button>
                <StatusMark status={selected.status} />
                <div className="min-w-0 flex-1">
                  <h2 className="truncate text-[13px] font-semibold" title={selected.title}>{selected.title}</h2>
                  <p className="truncate font-mono text-[9px] text-muted-foreground">{selectedResponsibleLabel} · brief v{selected.currentBrief.version} · Topic v{selected.version}</p>
                </div>
                {selected.needsMeCount > 0 ? <span className="rounded-sm bg-warning/15 px-1.5 py-1 font-mono text-[9px] font-semibold text-warning"><CircleHelp className="mr-1 inline size-3" />{selected.needsMeCount} need you</span> : null}
                {selected.resultsReady ? <span className="rounded-sm bg-success/10 px-1.5 py-1 font-mono text-[9px] font-semibold text-success"><Check className="mr-1 inline size-3" />result ready</span> : null}
                <span className="rounded-sm border border-border bg-card px-2 py-1 font-mono text-[9px] uppercase text-muted-foreground">{selected.status}</span>
              </header>

              <nav className="flex h-9 shrink-0 items-end gap-5 border-b border-border px-4 md:px-7" aria-label="Topic detail views">
                {(["current", "history"] as TopicDetailView[]).map((view) => (
                  <button key={view} type="button" onClick={() => changeDetailView(view)} className={`h-9 border-b-2 px-0.5 text-[10.5px] font-semibold capitalize transition ${detailView === view ? "border-foreground text-foreground" : "border-transparent text-muted-foreground hover:text-foreground"}`}>
                    {view}{view === "history" ? ` ${briefTimeline.length}` : ""}
                  </button>
                ))}
              </nav>

              <div className="min-h-0 flex-1 overflow-y-auto">
                {detailView === "current" ? (
                  <div className="mx-auto max-w-[1080px] px-4 py-5 md:px-7 md:py-7">
                    <TopicScope purpose={selected.purpose} completionBoundary={selected.completionBoundary} />

                    <section className="mt-6 border-b border-border pb-6">
                      <div className="flex flex-wrap items-center gap-2">
                        <SectionTitle>Now</SectionTitle>
                        <span className="font-mono text-[9px] text-muted-foreground">brief v{selected.currentBrief.version} · {formatDate(selected.currentBrief.updatedAt)}</span>
                      </div>
                      {selected.currentBrief.currentState ? <div className="mt-3 font-medium text-foreground"><MarkdownContent content={selected.currentBrief.currentState} className="!text-[12.5px] !leading-5" /></div> : null}
                      <div className={`${selected.currentBrief.currentState ? "mt-4 border-t border-border/70 pt-4" : "mt-3"} text-foreground/85`}>
                        <div className="mb-1 font-mono text-[8.5px] uppercase text-muted-foreground">Latest update</div>
                        <MarkdownContent content={selected.currentBrief.summary || "No shared brief yet."} className="!text-[12px] !leading-5" />
                      </div>
                    </section>

                    <section className="grid border-b border-border md:grid-cols-[minmax(0,0.9fr)_minmax(0,1.1fr)]">
                      {selected.waitingOn ? (
                        <div className="border-b border-border bg-warning/[0.04] px-4 py-4 md:border-b-0 md:border-r">
                          <div className="flex items-center gap-2 font-mono text-[9px] uppercase text-warning"><Pause className="size-3.5" />Waiting for {selected.waitingOn.kind}{selected.waitingOn.refId ? ` · ${selected.waitingOn.refId}` : ""}</div>
                          <p className="mt-2 text-[11.5px] leading-5">{selected.waitingOn.summary}</p>
                          <span className="mt-2 block font-mono text-[8.5px] text-muted-foreground">since {formatDate(selected.waitingOn.since)}</span>
                          {selected.waitingOn.resumeAction ? <details className="mt-2 border-t border-border/70 pt-2"><summary className="cursor-pointer font-mono text-[8.5px] uppercase text-muted-foreground">Resume check</summary><p className="mt-1.5 text-[10px] leading-4 text-muted-foreground">{selected.waitingOn.resumeAction}</p></details> : null}
                        </div>
                      ) : (
                        <div className="border-b border-border px-4 py-4 md:border-b-0 md:border-r"><SectionTitle>Status</SectionTitle><p className="mt-2 text-[11.5px] leading-5 text-muted-foreground">No external waiting condition.</p></div>
                      )}
                      <div className="px-4 py-4">
                        <SectionTitle>Next</SectionTitle>
                        <p className="mt-2 whitespace-pre-wrap text-[11.5px] leading-5">{selected.currentBrief.nextStep || selected.waitingOn?.resumeAction || "The Responsible Agent has not recorded a next step."}</p>
                      </div>
                    </section>

                    {selected.currentBrief.limitations ? (
                      <section className="grid gap-2 border-b border-border px-4 py-4 md:grid-cols-[180px_minmax(0,1fr)] md:gap-5">
                        <SectionTitle>Limits and uncertainty</SectionTitle>
                        <p className="whitespace-pre-wrap text-[11px] leading-5 text-foreground/80">{selected.currentBrief.limitations}</p>
                      </section>
                    ) : null}

                    <section className="mt-6 border-b border-border pb-6">
                      <div className="flex flex-wrap items-center gap-2"><SectionTitle>People and current work</SectionTitle><span className="font-mono text-[9px] text-muted-foreground">1 responsible · {selectedParticipants.length} participants</span></div>
                      <div className="mt-3 divide-y divide-border border-y border-border">
                        <AgentRoleRow agentId={selected.responsibleAgentId} name={selectedResponsibleLabel} role="Responsible" responsibility="Owns the shared brief, routing, waiting conditions, and final close." activeTurn={selectedActiveTurns.find((turn) => turn.agentId === selected.responsibleAgentId)} onOpenAgent={onOpenAgent} onIntervene={(turn, action) => { setIntervention({ turn, action }); setInterventionDraft({ text: "", reason: "" }); }} />
                        {selectedParticipants.map((participant) => <AgentRoleRow key={participant.agentId} agentId={participant.agentId} name={agentLabelsByID.get(participant.agentId) || participant.agent} role="Participant" responsibility={participant.responsibility} activeTurn={selectedActiveTurns.find((turn) => turn.agentId === participant.agentId)} onOpenAgent={onOpenAgent} onIntervene={(turn, action) => { setIntervention({ turn, action }); setInterventionDraft({ text: "", reason: "" }); }} />)}
                      </div>
                    </section>

                    <TopicCurrentArtifacts
                      links={evidenceLinks}
                      artifacts={topicArtifacts}
                      loading={topicArtifactsQuery.isLoading}
                      onLink={() => setEvidenceOpen(true)}
                      onViewAll={() => changeDetailView("history")}
                    />
                  </div>
                ) : (
                  <div className="mx-auto max-w-[980px] px-4 py-5 md:px-7 md:py-7">
                    <section>
                      <div className="flex flex-wrap items-baseline gap-2"><SectionTitle>Brief history</SectionTitle><span className="font-mono text-[9px] text-muted-foreground">{briefTimeline.length} versions · newest first</span></div>
                      <div className="mt-3 border-y border-border">
                        {briefTimeline.map((brief, index) => (
                          <details key={brief.version} className="group border-b border-border last:border-b-0">
                            <summary className="flex cursor-pointer list-none items-start gap-3 px-1 py-3 hover:bg-muted/35">
                              <span className={`mt-1.5 size-2 shrink-0 rounded-full ${index === 0 ? "bg-success" : "bg-muted-foreground/30"}`} />
                              <span className="min-w-0 flex-1">
                                <span className="flex flex-wrap items-center gap-x-2 gap-y-1"><span className="font-mono text-[9px] font-semibold text-foreground">v{brief.version}</span>{index === 0 ? <span className="font-mono text-[8px] uppercase text-success">current</span> : null}<span className="font-mono text-[8.5px] text-muted-foreground">{formatDate(brief.updatedAt)} · {brief.updatedBy}</span></span>
                                <span className="mt-1.5 line-clamp-2 break-words text-[11.5px] leading-5 text-foreground/85">{brief.summary || "No summary recorded."}</span>
                              </span>
                              <ChevronRight className="mt-1 size-3.5 shrink-0 text-muted-foreground transition group-open:rotate-90" />
                            </summary>
                            {(brief.currentState || brief.nextStep || brief.limitations || (brief.evidence || []).length > 0) ? (
                              <dl className="grid gap-x-6 gap-y-3 border-t border-border/60 bg-muted/20 px-6 py-4 text-[11px] sm:grid-cols-2">
                                {brief.currentState ? <BriefField label="State at this version" value={brief.currentState} /> : null}
                                {brief.nextStep ? <BriefField label="Next at this version" value={brief.nextStep} /> : null}
                                {brief.limitations ? <BriefField label="Limits at this version" value={brief.limitations} /> : null}
                                {(brief.evidence || []).length > 0 ? <BriefField label="Evidence" value={(brief.evidence || []).map((ref) => `${ref.type}:${ref.id}${ref.label ? ` · ${ref.label}` : ""}`).join("\n")} /> : null}
                              </dl>
                            ) : null}
                          </details>
                        ))}
                      </div>
                    </section>

                    <section className="mt-7">
                      <div className="flex items-center"><SectionTitle>Evidence anchors</SectionTitle><span className="ml-2 font-mono text-[9px] text-muted-foreground">{evidenceLinks.length}</span><Button variant="ghost" size="icon-xs" className="ml-auto" onClick={() => setEvidenceOpen(true)} title="Link evidence"><Plus /></Button></div>
                      <div className="mt-3 grid gap-2 border-y border-border py-2 sm:grid-cols-2">
                        {evidenceLinks.map((link) => <TopicEvidenceAnchor key={`${link.type}:${link.id}:${link.relation}`} link={link} artifact={topicArtifacts.get(link.id)} />)}
                        {evidenceLinks.length === 0 ? <div className="py-6 text-center text-[10.5px] text-muted-foreground sm:col-span-2">No evidence anchors linked.</div> : null}
                      </div>
                    </section>

                    <details className="group mt-7 border-y border-border py-3">
                      <summary className="flex cursor-pointer list-none items-center gap-2 font-mono text-[9px] font-semibold uppercase text-muted-foreground hover:text-foreground"><ChevronRight className="size-3.5 transition group-open:rotate-90" />Audit log · {(selected.events || []).length} events</summary>
                      <div className="mt-3 divide-y divide-border border-t border-border">
                        {[...(selected.events || [])].reverse().map((event) => (
                          <div key={event.seq} className="flex min-w-0 gap-3 py-2.5">
                            <span className="mt-1.5 size-1.5 shrink-0 rounded-full bg-muted-foreground/40" />
                            <div className="min-w-0 flex-1">
                              <div className="flex min-w-0 items-center gap-2"><span className="font-mono text-[9px] uppercase text-muted-foreground">{eventLabel(event.type)}</span>{event.agent ? <span className="truncate text-[10px] font-medium">{event.agent}</span> : null}<span className="ml-auto shrink-0 font-mono text-[8.5px] text-muted-foreground">{formatDate(event.createdAt)}</span></div>
                              <p className="mt-1 break-words text-[11px] leading-4 text-foreground/80">{topicAuditSummary(event)}</p>
                              {event.ref ? <span className="mt-1 block truncate font-mono text-[9px] text-muted-foreground">{event.ref.type}:{event.ref.id}</span> : null}
                            </div>
                          </div>
                        ))}
                      </div>
                    </details>
                  </div>
                )}
              </div>

              <footer className="shrink-0 border-t border-border bg-background px-3 py-2 md:px-6 md:py-3">
                <div className="mx-auto flex max-w-[880px] items-end gap-2 rounded-md border border-input bg-card p-1.5 shadow-card focus-within:border-ring focus-within:ring-2 focus-within:ring-ring/20">
                  <textarea value={message} disabled={working || selected.status === "archived"} onChange={(event) => setMessage(event.target.value)} onKeyDown={(event) => { if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing) { event.preventDefault(); sendInput(); } }} rows={2} placeholder={`Continue this Topic with ${selectedResponsibleLabel}…`} className="min-h-12 flex-1 resize-none bg-transparent px-2 py-1.5 text-[12px] leading-5 outline-none placeholder:text-muted-foreground/60" />
                  <Button size="icon" onClick={sendInput} disabled={working || !message.trim()} aria-label="Send Topic input to responsible Agent">{working ? <Loader2 className="animate-spin" /> : <Send />}</Button>
                </div>
              </footer>
            </>
          ) : selectedID ? (
            <div className="flex flex-1 items-center justify-center text-muted-foreground"><Loader2 className="size-5 animate-spin" /><span className="ml-2 text-[11px]">Loading Topic detail…</span></div>
          ) : (
            <div className="flex flex-1 flex-col items-center justify-center text-center text-muted-foreground"><GitBranch className="size-7 opacity-35" /><div className="mt-2 text-[12px] font-medium text-foreground/70">Select a Topic</div><p className="mt-1 text-[10.5px]">Recover the shared state without joining every Agent Thread.</p></div>
          )}
        </section>
      </div>

      <CreateTopicDialog open={createOpen} agents={agents} draft={createDraft} working={working} onOpenChange={setCreateOpen} onChange={setCreateDraft} onCreate={createTopic} />
      <Dialog open={evidenceOpen} onOpenChange={setEvidenceOpen}><DialogContent><DialogHeader><DialogTitle>Link an evidence anchor</DialogTitle><DialogDescription>Use a stable object ID. Do not copy an entire Agent Thread into the Topic.</DialogDescription></DialogHeader><div className="space-y-3"><Field label="Object type"><Input value={evidenceDraft.type} onChange={(e) => setEvidenceDraft((draft) => ({ ...draft, type: e.target.value }))} placeholder="message, turn, trigger, artifact, pr" /></Field><Field label="Stable ID"><Input value={evidenceDraft.id} onChange={(e) => setEvidenceDraft((draft) => ({ ...draft, id: e.target.value }))} className="font-mono" /></Field><Field label="Label"><Input value={evidenceDraft.label} onChange={(e) => setEvidenceDraft((draft) => ({ ...draft, label: e.target.value }))} /></Field></div><DialogFooter showCloseButton><Button onClick={linkEvidence} disabled={working || !evidenceDraft.type.trim() || !evidenceDraft.id.trim()}><FileText />Link evidence</Button></DialogFooter></DialogContent></Dialog>
      <Dialog
        open={Boolean(intervention)}
        onOpenChange={(open) => {
          if (!open) setIntervention(null);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{intervention?.action === "steer" ? "Guide active Turn" : "Interrupt active Turn"}</DialogTitle>
            <DialogDescription>
              This acts on {intervention?.turn.agent}&apos;s exact Turn {intervention?.turn.turnId}. The Topic itself remains unchanged and the Responsible Agent is informed.
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-3">
            {intervention?.action === "steer" ? (
              <Field label="Guidance">
                <textarea rows={4} value={interventionDraft.text} onChange={(event) => setInterventionDraft((draft) => ({ ...draft, text: event.target.value }))} className={textareaClass} />
              </Field>
            ) : null}
            <Field label={intervention?.action === "steer" ? "Reason / expected impact" : "Reason"}>
              <textarea rows={3} value={interventionDraft.reason} onChange={(event) => setInterventionDraft((draft) => ({ ...draft, reason: event.target.value }))} className={textareaClass} />
            </Field>
          </div>
          <DialogFooter showCloseButton>
            <Button
              variant={intervention?.action === "interrupt" ? "destructive" : "default"}
              onClick={intervene}
              disabled={working || (intervention?.action === "steer" && !interventionDraft.text.trim())}
            >
              {intervention?.action === "interrupt" ? <Square /> : <MessageSquare />}
              {intervention?.action === "interrupt" ? "Interrupt Turn" : "Send guidance"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </main>
  );
}

export function TopicCurrentArtifacts({
  links,
  artifacts,
  loading = false,
  onLink,
  onViewAll,
}: {
  links: TopicLink[];
  artifacts: Map<string, ThreadArtifact>;
  loading?: boolean;
  onLink: () => void;
  onViewAll: () => void;
}) {
  const artifactLinks = links
    .filter((link) => link.type.toLowerCase() === "artifact")
    .sort((a, b) => b.createdAt.localeCompare(a.createdAt));
  if (artifactLinks.length === 0) return null;

  const resolved = artifactLinks
    .map((link) => ({ link, artifact: artifacts.get(link.id) }))
    .filter((entry): entry is { link: TopicLink; artifact: ThreadArtifact } => Boolean(entry.artifact));
  const visible = resolved.slice(0, 6);

  return (
    <section className="mt-6 border-b border-border pb-6" aria-label="Topic artifacts">
      <div className="flex flex-wrap items-center gap-2">
        <SectionTitle>Artifacts</SectionTitle>
        <span className="font-mono text-[9px] text-muted-foreground">{artifactLinks.length} linked · newest first</span>
        <Button variant="ghost" size="icon-xs" className="ml-auto" onClick={onLink} title="Link evidence"><Plus /></Button>
      </div>
      <div className="mt-3 grid gap-2 sm:grid-cols-2">
        {visible.map(({ link, artifact }) => (
          <TopicEvidenceAnchor
            key={`${link.type}:${link.id}:${link.relation}`}
            link={link}
            artifact={artifact}
          />
        ))}
        {loading && visible.length === 0 ? (
          <div className="py-6 text-center text-[10.5px] text-muted-foreground sm:col-span-2">Loading published artifacts…</div>
        ) : null}
        {!loading && visible.length === 0 ? (
          <div className="py-6 text-center text-[10.5px] text-muted-foreground sm:col-span-2">Linked artifacts are not published or are no longer available.</div>
        ) : null}
      </div>
      {artifactLinks.length > visible.length ? (
        <button type="button" onClick={onViewAll} className="mt-3 font-mono text-[9px] font-semibold text-muted-foreground hover:text-foreground">
          View all {artifactLinks.length} in History
        </button>
      ) : null}
    </section>
  );
}

export function TopicScope({ purpose, completionBoundary }: { purpose: string; completionBoundary: string }) {
  return (
    <section className="border-y border-border bg-muted/15 px-4 py-5 md:px-5" aria-label="Topic scope">
      <div className="flex flex-wrap items-baseline gap-2">
        <SectionTitle>Scope</SectionTitle>
        <span className="font-mono text-[9px] text-muted-foreground">durable Topic contract</span>
      </div>
      <div className="mt-4 grid gap-5 md:grid-cols-[minmax(0,0.9fr)_minmax(0,1.1fr)] md:gap-0">
        <div className="min-w-0 md:pr-6">
          <TextSection label="Purpose" text={purpose} />
        </div>
        <div className="min-w-0 border-t border-border pt-5 md:border-l md:border-t-0 md:pl-6 md:pt-0">
          <TextSection label="Complete when" text={completionBoundary} />
        </div>
      </div>
    </section>
  );
}

export function TopicEvidenceAnchor({ link, artifact }: { link: TopicLink; artifact?: ThreadArtifact }) {
  if (link.type.toLowerCase() === "artifact" && artifact) {
    return (
      <div data-topic-artifact={artifact.id} className="min-w-0">
        <PublishedArtifactCard
          artifact={artifact}
          publishedAt={artifact.publishedAt}
          displayName={link.label}
        />
      </div>
    );
  }
  return (
    <div className="min-w-0 border border-border px-3 py-2.5">
      <div className="flex items-center gap-2">
        <FileText className="size-3 text-muted-foreground" />
        <span className="font-mono text-[8.5px] uppercase text-muted-foreground">{link.type} · {link.relation}</span>
      </div>
      <div className="mt-1 truncate font-mono text-[10px]" title={link.id}>{link.label || link.id}</div>
    </div>
  );
}

function CreateTopicDialog({ open, agents, draft, working, onOpenChange, onChange, onCreate }: { open: boolean; agents: Agent[]; draft: CreateTopicDraft; working: boolean; onOpenChange: (open: boolean) => void; onChange: (draft: CreateTopicDraft) => void; onCreate: () => void }) {
  const selectedParticipants = new Set(draft.participants.map((participant) => participant.agent));
  const updateParticipant = (index: number, patch: Partial<TopicParticipantDraft>) => {
    const participants = draft.participants.map((participant, candidate) => candidate === index ? { ...participant, ...patch } : participant);
    onChange({ ...draft, participants });
  };
  return <Dialog open={open} onOpenChange={onOpenChange}><DialogContent className="max-h-[min(88dvh,760px)] overflow-y-auto sm:max-w-xl"><DialogHeader><DialogTitle>Track this as a Topic</DialogTitle><DialogDescription>Create the smallest shared record needed to recover work across Turns, waiting periods, and Agent handoffs.</DialogDescription></DialogHeader><div className="space-y-3"><Field label="Title"><Input value={draft.title} onChange={(e) => onChange({ ...draft, title: e.target.value })} placeholder="A bounded stage with a clear finish" /></Field><Field label="Responsible Agent"><select value={draft.responsibleAgent} onChange={(e) => onChange({ ...draft, responsibleAgent: e.target.value, participants: draft.participants.filter((participant) => participant.agent !== e.target.value) })} className={selectClass}><option value="">Select the Agent accountable for the whole</option>{agents.map((agent) => <option key={agent.id} value={agent.id}>{agentLabel(agent)}</option>)}</select></Field><Field label="Purpose"><textarea rows={3} value={draft.purpose} onChange={(e) => onChange({ ...draft, purpose: e.target.value })} placeholder="Why this work needs continuity" className={textareaClass} /></Field><Field label="Complete when"><textarea rows={3} value={draft.completionBoundary} onChange={(e) => onChange({ ...draft, completionBoundary: e.target.value })} placeholder="The observable boundary that closes this Topic" className={textareaClass} /></Field><Field label="Current brief (optional)"><textarea rows={3} value={draft.summary} onChange={(e) => onChange({ ...draft, summary: e.target.value })} placeholder="What is already known now; not a full history import" className={textareaClass} /></Field><div className="space-y-2"><div className="flex items-center"><span className="text-[10px] font-medium uppercase text-muted-foreground">Participants (optional)</span><Button type="button" variant="outline" size="sm" className="ml-auto" onClick={() => onChange({ ...draft, participants: [...draft.participants, { agent: "", responsibility: "" }] })}><Plus />Participant</Button></div>{draft.participants.map((participant, index) => <div key={index} className="grid grid-cols-[minmax(0,0.8fr)_minmax(0,1.2fr)_auto] gap-2"><select value={participant.agent} onChange={(event) => updateParticipant(index, { agent: event.target.value })} className={selectClass}><option value="">Select an Agent</option>{agents.filter((agent) => agent.id !== draft.responsibleAgent && (!selectedParticipants.has(agent.id) || agent.id === participant.agent)).map((agent) => <option key={agent.id} value={agent.id}>{agentLabel(agent)}</option>)}</select><Input value={participant.responsibility} onChange={(event) => updateParticipant(index, { responsibility: event.target.value })} placeholder="Topic-scoped responsibility" /><Button type="button" variant="ghost" size="icon-sm" onClick={() => onChange({ ...draft, participants: draft.participants.filter((_, candidate) => candidate !== index) })} aria-label="Remove participant"><X /></Button></div>)}</div></div><DialogFooter showCloseButton><Button onClick={onCreate} disabled={working || !draft.title.trim() || !draft.responsibleAgent || !draft.purpose.trim() || !draft.completionBoundary.trim() || draft.participants.some((participant) => !participant.agent || !participant.responsibility.trim())}>{working ? <Loader2 className="animate-spin" /> : <Plus />}Create Topic</Button></DialogFooter></DialogContent></Dialog>;
}

function TopicListItem({ topic, responsibleLabel, selected, onSelect }: { topic: TopicSummary; responsibleLabel: string; selected: boolean; onSelect: () => void }) {
  const activeTurns = topic.activeTurns || [];
  return <button type="button" onClick={onSelect} className={`group flex w-full min-w-0 items-start gap-3 border-b border-border/70 px-3 py-3 text-left outline-none transition hover:bg-muted/55 focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring/30 ${selected ? "bg-selection" : ""}`}><StatusMark status={topic.status} /><span className="min-w-0 flex-1"><span className="flex min-w-0 items-center gap-2"><span className="truncate text-[12px] font-semibold">{topic.title}</span>{topic.needsMeCount > 0 ? <span className="shrink-0 rounded-sm bg-warning/15 px-1 font-mono text-[8px] font-semibold text-warning">{topic.needsMeCount} need you</span> : null}{topic.resultsReady ? <span className="size-1.5 shrink-0 rounded-full bg-success" title="Result ready" /> : null}</span><span className="mt-1 block truncate text-[10px] text-muted-foreground">{responsibleLabel} · {topic.currentBrief.nextStep || topic.currentBrief.summary || topic.purpose}</span><span className="mt-1.5 block font-mono text-[8.5px] uppercase text-muted-foreground">{topic.status}{activeTurns.length ? ` · ${activeTurns.length} active` : ""} · {relativeTime(topic.updatedAt)}</span></span><ChevronRight className="mt-1 size-3.5 shrink-0 text-muted-foreground/45 group-hover:text-muted-foreground" /></button>;
}

function AgentRoleRow({ agentId, name, role, responsibility, activeTurn, onOpenAgent, onIntervene }: { agentId: string; name: string; role: string; responsibility: string; activeTurn?: TopicActiveTurn; onOpenAgent: (id: string) => void; onIntervene: (turn: TopicActiveTurn, action: "steer" | "interrupt") => void }) {
  return <div className="flex min-w-0 items-start gap-3 py-3"><span className={`mt-1.5 size-2 shrink-0 rounded-full ${activeTurn ? "animate-pulse bg-success ring-2 ring-success/15" : "bg-muted-foreground/30"}`} /><div className="min-w-0 flex-1"><div className="flex min-w-0 items-center gap-2"><button type="button" onClick={() => onOpenAgent(agentId)} className="truncate text-[11.5px] font-semibold hover:underline">{name}</button><span className="font-mono text-[8.5px] uppercase text-muted-foreground">{role}</span></div><p className="mt-1 text-[10.5px] leading-4 text-muted-foreground">{responsibility}</p>{activeTurn ? <div className="mt-2 flex min-w-0 flex-wrap items-center gap-1.5 border-l-2 border-success/45 pl-2"><span className="min-w-0 flex-1 truncate text-[10px]" title={activeTurn.task}>{activeTurn.task || "Active Topic Turn"}</span><Button variant="ghost" size="sm" onClick={() => onOpenAgent(agentId)}><ArrowUpRight />Open</Button><Button variant="outline" size="sm" onClick={() => onIntervene(activeTurn, "steer")}><MessageSquare />Guide</Button><Button variant="destructive" size="sm" onClick={() => onIntervene(activeTurn, "interrupt")}><Square />Interrupt</Button></div> : null}</div></div>;
}

function StatusMark({ status }: { status: TopicStatus }) {
  return <span className={`mt-1 size-2 shrink-0 rounded-full ${status === "active" ? "bg-success" : status === "waiting" ? "bg-warning" : status === "resolved" ? "bg-ring/65" : "bg-muted-foreground/30"}`} title={status} />;
}

function TextSection({ label, text }: { label: string; text: string }) { return <div className="min-w-0"><SectionTitle>{label}</SectionTitle><div className="mt-2 text-foreground/85"><MarkdownContent content={text} className="!text-[12px] !leading-5" /></div></div>; }
function SectionTitle({ children }: { children: ReactNode }) { return <h3 className="font-mono text-[9px] font-semibold uppercase text-muted-foreground">{children}</h3>; }
function BriefField({ label, value }: { label: string; value: string }) { return <div><dt className="font-mono text-[8.5px] uppercase text-muted-foreground">{label}</dt><dd className="mt-1 whitespace-pre-wrap leading-5 text-foreground/85">{value}</dd></div>; }
function Field({ label, children }: { label: string; children: ReactNode }) { return <label className="block space-y-1.5 text-[10px] font-medium uppercase text-muted-foreground">{label}{children}</label>; }
function topicMatchesFilter(topic: TopicSummary, filter: TopicFilter) { if (filter === "attention") return topic.needsMeCount > 0 || topic.resultsReady; if (filter === "all") return topic.status !== "archived"; return topic.status === filter; }
function eventLabel(value: string) { return value.replaceAll("_", " "); }
function formatDate(value: string) { const date = new Date(value); return Number.isNaN(date.getTime()) ? value : date.toLocaleString(); }
function relativeTime(value: string) { const ms = Date.now() - Date.parse(value); if (!Number.isFinite(ms)) return value; const minutes = Math.max(0, Math.floor(ms / 60_000)); if (minutes < 1) return "now"; if (minutes < 60) return `${minutes}m`; const hours = Math.floor(minutes / 60); if (hours < 24) return `${hours}h`; return `${Math.floor(hours / 24)}d`; }
function delay() { return new Promise((resolve) => window.setTimeout(resolve, 60)); }
