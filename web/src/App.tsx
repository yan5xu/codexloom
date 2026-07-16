import { Activity, Archive, BarChart3, Bot, Cable, CalendarClock, ChevronRight, CircleHelp, Inbox as InboxIcon, Info, Menu, MessageSquare, Network, PanelLeftClose, PanelLeftOpen, Plus, RadioTower, RotateCw, Settings2, SwatchBook, X } from "lucide-react";
import { lazy, Suspense, type ReactNode, useEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, type Agent, type BackupStatus, type HumanRequest, type InboxEntry, type RemoteSnapshot } from "./types";
import { summarizeTask } from "./feed";
import { BrandLockup, BrandMark } from "./components/BrandMark";
import { Button } from "./components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "./components/ui/dialog";
import { Separator } from "./components/ui/separator";
import { Input } from "./components/ui/input";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "./components/ui/collapsible";
import { Popover, PopoverContent, PopoverTrigger } from "./components/ui/popover";
import { publishThreadEvent, threadEventSubscriberCount } from "./thread-events";

const AgentPane = lazy(() => import("./AgentPane").then((module) => ({ default: module.AgentPane })));
const InboxPane = lazy(() => import("./InboxPane").then((module) => ({ default: module.InboxPane })));
const IntegrationsPane = lazy(() => import("./IntegrationsPane").then((module) => ({ default: module.IntegrationsPane })));
const MessagesPane = lazy(() => import("./MessagesPane").then((module) => ({ default: module.MessagesPane })));
const SchedulesPane = lazy(() => import("./SchedulesPane").then((module) => ({ default: module.SchedulesPane })));
const TeamPane = lazy(() => import("./TeamPane").then((module) => ({ default: module.TeamPane })));
const RemotePane = lazy(() => import("./RemotePane").then((module) => ({ default: module.RemotePane })));
const DesignPane = lazy(() => import("./DesignPane").then((module) => ({ default: module.DesignPane })));
const UsagePane = lazy(() => import("./UsagePane").then((module) => ({ default: module.UsagePane })));
const CapacityPane = lazy(() => import("./UsagePane").then((module) => ({ default: module.CapacityPane })));
const NeedsYouPane = lazy(() => import("./NeedsYouPane").then((module) => ({ default: module.NeedsYouPane })));

function WorkbenchFallback() {
  return (
    <main className="flex min-w-0 flex-1 items-center justify-center bg-background" aria-live="polite">
      <div className="flex items-center gap-2 font-mono text-[11px] text-muted-foreground">
        <span className="spinner size-3" />
        Loading workspace
      </div>
    </main>
  );
}

type SidebarNavItemProps = {
  label: string;
  icon: typeof InboxIcon;
  active: boolean;
  compact: boolean;
  onSelect: () => void;
  indicator?: "success" | "warning" | "destructive" | "muted";
  count?: number;
};

function SidebarNavItem({ label, icon: Icon, active, compact, onSelect, indicator, count = 0 }: SidebarNavItemProps) {
  return (
    <Button
      type="button"
      variant={active ? "secondary" : "ghost"}
      onClick={onSelect}
      title={compact ? label : undefined}
      aria-label={compact ? label : undefined}
      className={`relative h-8 w-full justify-start gap-2 px-2.5 text-[12.5px] ${active ? "bg-selection text-selection-foreground hover:bg-selection" : "text-foreground/80"} ${compact ? "md:justify-center md:px-0" : ""}`}
    >
      <Icon className="size-3.5 text-primary" />
      <span className={`min-w-0 flex-1 truncate text-left ${compact ? "md:hidden" : ""}`}>{label}</span>
      {count > 0 ? <span className={`${compact ? "md:absolute md:right-0.5 md:top-0.5" : ""} flex min-w-4 items-center justify-center rounded-sm bg-warning/15 px-1 font-mono text-[8.5px] font-semibold text-warning`}>{count}</span> : null}
      {indicator ? (
        <span
          className={`size-1.5 shrink-0 rounded-full ${
            indicator === "success"
              ? "bg-success"
              : indicator === "warning"
                ? "bg-warning"
                : indicator === "destructive"
                  ? "bg-destructive"
                  : "bg-muted-foreground/30"
          } ${compact ? "md:absolute md:right-1.5 md:top-1.5" : ""}`}
        />
      ) : null}
    </Button>
  );
}

function SidebarNavGroup({ label, open, onOpenChange, children }: { label: string; open: boolean; onOpenChange: (open: boolean) => void; children: ReactNode }) {
  return (
    <Collapsible open={open} onOpenChange={onOpenChange} className="group/nav-group">
      <CollapsibleTrigger
        render={<Button type="button" variant="ghost" className="h-7 w-full justify-start px-2 text-[9px] font-bold uppercase text-muted-foreground" />}
      >
        <ChevronRight className={`size-3 transition-transform ${open ? "rotate-90" : ""}`} />
        <span className="flex-1 text-left">{label}</span>
      </CollapsibleTrigger>
      <CollapsibleContent className="space-y-0.5 pb-1">
        {children}
      </CollapsibleContent>
    </Collapsible>
  );
}

function AgentActivityPopover({
  agents,
  humanRequests,
  compact = false,
  onSelect,
  onSelectRequest,
}: {
  agents: Agent[];
  humanRequests: HumanRequest[];
  compact?: boolean;
  onSelect: (id: string) => void;
  onSelectRequest: (id: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const activeAgents = agents.filter(isAgentWorking);
  const idleCount = agents.length - activeAgents.length;
  const taskSummary = (agent: Agent) => summarizeTask(agent.currentTask || agent.goal?.objective || "") || "Running turn";

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger
        openOnHover
        delay={180}
        closeDelay={180}
        aria-label="Show active agents"
        className={
          compact
            ? "flex h-9 w-full items-center justify-center font-mono text-[10px] text-success outline-none hover:bg-muted focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring/40"
            : "mx-3 my-2 flex h-7 w-[calc(100%-1.5rem)] items-center justify-between rounded-md bg-background/65 px-2 font-mono text-[9.5px] text-muted-foreground outline-none transition-colors hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring/40"
        }
      >
        {compact ? (
          <>
            {activeAgents.length}
            <span className={`ml-1 size-1.5 rounded-full ${activeAgents.length > 0 ? "bg-success" : "bg-muted-foreground/30"}`} />
            {humanRequests.length > 0 ? <span className="ml-1.5 text-warning">{humanRequests.length}?</span> : null}
          </>
        ) : (
          <>
            <span><strong className="text-foreground">{agents.length}</strong> agents</span>
            <span><strong className="text-success">{activeAgents.length}</strong> active</span>
            <span><strong className="text-foreground/65">{idleCount}</strong> idle</span>
            {humanRequests.length > 0 ? <span><strong className="text-warning">{humanRequests.length}</strong> need you</span> : null}
          </>
        )}
      </PopoverTrigger>
      <PopoverContent
        side={compact ? "right" : "bottom"}
        align="start"
        className="w-[min(20rem,calc(100vw-1rem))] p-2"
        aria-label="Active agents"
      >
        {humanRequests.length > 0 ? (
          <>
            <div className="flex items-center justify-between px-1 py-1">
              <span className="text-[11.5px] font-semibold">Needs your input</span>
              <span className="font-mono text-[9.5px] text-warning">{humanRequests.length} open</span>
            </div>
            <div className="mt-1 max-h-44 space-y-0.5 overflow-y-auto border-b border-border pb-2">
              {humanRequests.map((request) => (
                <button
                  key={request.id}
                  type="button"
                  onClick={() => {
                    setOpen(false);
                    onSelectRequest(request.id);
                  }}
                  className="flex w-full min-w-0 items-start gap-2 rounded-sm px-2 py-2 text-left outline-none hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring/40"
                >
                  <CircleHelp className="mt-0.5 size-3.5 shrink-0 text-warning" />
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-[11.5px] font-semibold">{request.agentName}</span>
                    <span className="mt-0.5 block line-clamp-2 text-[10px] leading-4 text-muted-foreground">{request.question}</span>
                  </span>
                  <ChevronRight className="mt-0.5 size-3.5 shrink-0 text-muted-foreground/60" />
                </button>
              ))}
            </div>
          </>
        ) : null}
        <div className="flex items-center justify-between px-1 py-1">
          <span className="text-[11.5px] font-semibold">Working now</span>
          <span className="font-mono text-[9.5px] text-muted-foreground">{activeAgents.length} active</span>
        </div>
        <div className="mt-1 max-h-72 space-y-0.5 overflow-y-auto">
          {activeAgents.map((agent) => {
            const task = taskSummary(agent);
            return (
              <button
                key={agent.id}
                type="button"
                data-agent-id={agent.id}
                data-agent-status={agent.status}
                onClick={() => {
                  setOpen(false);
                  onSelect(agent.id);
                }}
                className="flex w-full min-w-0 items-start gap-2 rounded-sm px-2 py-2 text-left outline-none hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring/40"
              >
                <span className="mt-1 size-2 shrink-0 rounded-full bg-success ring-2 ring-success/15" />
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-[11.5px] font-semibold">{agent.name}</span>
                  <span className="mt-0.5 block truncate text-[10px] text-muted-foreground" title={task}>{task}</span>
                </span>
                <ChevronRight className="mt-0.5 size-3.5 shrink-0 text-muted-foreground/60" />
              </button>
            );
          })}
          {activeAgents.length === 0 ? (
            <div className="px-2 py-5 text-center text-[11px] text-muted-foreground">No agents are working right now.</div>
          ) : null}
        </div>
      </PopoverContent>
    </Popover>
  );
}

function readAgentTabs() {
  try {
    const value = JSON.parse(sessionStorage.getItem("codexloom-agent-tabs") || "[]");
    return Array.isArray(value) ? [...new Set(value.filter((id): id is string => typeof id === "string" && Boolean(id)))] : [];
  } catch {
    return [];
  }
}

function agentUpdatedAt(agent: Agent) {
  const value = Date.parse(agent.updatedAt || "");
  return Number.isFinite(value) ? value : 0;
}

function mergeAgentSnapshot(previous: Agent[], incoming: Agent[]) {
  const previousByID = new Map(previous.map((agent) => [agent.id, agent]));
  return incoming.map((agent) => {
    const current = previousByID.get(agent.id);
    return current && agentUpdatedAt(current) > agentUpdatedAt(agent) ? current : agent;
  });
}

function threadEventError(data: any) {
  const value = data?.turn?.error?.message ?? data?.error?.message ?? data?.error ?? "";
  return typeof value === "string" ? value : "";
}

function applyThreadStatus(agent: Agent, event: any): Agent {
  const eventType = event?.type;
  const data = event?.data || {};
  const updatedAt = event?.ts || agent.updatedAt;
  if (eventType === "turn/started") {
    return {
      ...agent,
      status: "running",
      currentTurnId: data.turn?.id || data.turnId || agent.currentTurnId,
      lastError: "",
      updatedAt,
    };
  }
  if (eventType === "thread/goal/updated" && data.goal) {
    return { ...agent, goal: data.goal, updatedAt };
  }
  if (eventType === "thread/goal/cleared") {
    return { ...agent, goal: undefined, updatedAt };
  }
  if (["turn/completed", "turn/failed", "turn/aborted", "loom/turn-completed", "loom/turn-failed", "loom/turn-interrupted"].includes(eventType)) {
    const turnStatus = data.turn?.status;
    const failed = eventType === "turn/failed" || eventType === "loom/turn-failed" || turnStatus === "failed";
    return {
      ...agent,
      status: "idle",
      currentTask: "",
      currentTurnId: "",
      lastError: failed ? threadEventError(data) || "turn failed" : "",
      updatedAt,
    };
  }
  return agent;
}

function isAgentWorking(agent: Agent) {
  return agent.status === "running" || agent.goal?.status === "active";
}

function agentRuntimeLabel(agent: Agent) {
  if (agent.status === "running") return "running";
  if (agent.goal?.status === "active") return "goal active";
  return agent.status;
}

function AgentTabs({
  agents,
  humanRequests,
  activeId,
  unseenIds,
  onSelect,
  onClose,
  onEdit,
}: {
  agents: Agent[];
  humanRequests: HumanRequest[];
  activeId: string | null;
  unseenIds: Set<string>;
  onSelect: (id: string) => void;
  onClose: (id: string) => void;
  onEdit: (id: string) => void;
}) {
  const [openInfoId, setOpenInfoId] = useState<string | null>(null);
  if (agents.length === 0) return null;
  return (
    <div className="flex h-9 shrink-0 items-stretch overflow-hidden border-b border-border bg-sidebar/45 pl-12 md:pl-0" aria-label="Open agents">
      <div className="flex min-w-0 flex-1 overflow-x-auto [scrollbar-width:none] [&::-webkit-scrollbar]:hidden" role="tablist">
        {agents.map((agent, index) => {
          const active = agent.id === activeId;
          const unseen = unseenIds.has(agent.id);
          const needsYou = humanRequests.filter((request) => request.agentId === agent.id).length;
          const shortcutNumber = index < 8 ? index + 1 : index === agents.length - 1 ? 9 : null;
          return (
            <div
              key={agent.id}
              className={`group relative flex min-w-[156px] max-w-[240px] shrink-0 items-center border-r border-border/70 ${active ? "bg-background text-foreground" : "text-muted-foreground hover:bg-background/60 hover:text-foreground"}`}
            >
              {active ? <span className="absolute inset-x-0 bottom-[-1px] h-px bg-background" /> : null}
              <button
                type="button"
                role="tab"
                aria-selected={active}
                data-agent-id={agent.id}
                data-agent-status={agent.status}
                className="flex h-full min-w-0 flex-1 items-center gap-2 pl-3 text-left outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring/40"
                onClick={() => onSelect(agent.id)}
                title={`${agent.name}\n${agent.cwd}${shortcutNumber ? `\nSwitch: Ctrl/Option+${shortcutNumber}` : ""}`}
              >
                <span className={`size-1.5 shrink-0 rounded-full ${isAgentWorking(agent) ? "animate-pulse bg-warning" : unseen ? "bg-ring" : "bg-muted-foreground/35"}`} />
                <span className={`truncate text-[11.5px] ${active ? "font-semibold" : "font-medium"}`}>{agent.name}</span>
                {needsYou > 0 ? <span className="flex min-w-4 shrink-0 items-center justify-center rounded-sm bg-warning/15 px-1 font-mono text-[8px] font-semibold text-warning" title={`${needsYou} request${needsYou === 1 ? "" : "s"} need your input`}>{needsYou}</span> : null}
              </button>
              <Popover open={openInfoId === agent.id} onOpenChange={(open) => setOpenInfoId(open ? agent.id : null)}>
                <PopoverTrigger
                  openOnHover
                  delay={250}
                  closeDelay={180}
                  className={`flex size-6 shrink-0 items-center justify-center rounded-sm outline-none hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring/40 ${active ? "text-muted-foreground" : "text-muted-foreground/0 group-hover:text-muted-foreground group-focus-within:text-muted-foreground"}`}
                  title={`About ${agent.name}`}
                  aria-label={`About ${agent.name}`}
                >
                  <Info className="size-3" />
                </PopoverTrigger>
                <PopoverContent align="start" className="w-[min(22rem,calc(100vw-1rem))]">
                  <div className="flex items-center gap-2">
                    <span className={`size-2 shrink-0 rounded-full ${isAgentWorking(agent) ? "bg-warning" : agent.status === "idle" ? "bg-success" : "bg-muted-foreground/40"}`} />
                    <div className="min-w-0 flex-1 truncate text-[12px] font-semibold">{agent.name}</div>
                    <span className="font-mono text-[9px] uppercase text-muted-foreground">{agentRuntimeLabel(agent)}</span>
                  </div>
                  <dl className="mt-3 grid grid-cols-[58px_minmax(0,1fr)] gap-x-2 gap-y-2 text-[10.5px]">
                    <dt className="text-muted-foreground">Workspace</dt><dd className="min-w-0 truncate font-mono" title={agent.cwd}>{agent.cwd}</dd>
                    <dt className="text-muted-foreground">Thread</dt><dd className="min-w-0 truncate font-mono" title={agent.threadId || undefined}>{agent.threadId || "Not started"}</dd>
                    <dt className="text-muted-foreground">Model</dt><dd className="font-mono">{agent.model || "Default"}</dd>
                    <dt className="text-muted-foreground">Reasoning</dt><dd className="font-mono">{agent.effort === "xhigh" ? "Extra high" : agent.effort || "Default"}</dd>
                    <dt className="text-muted-foreground">Sandbox</dt><dd className="font-mono">{agent.sandbox || "danger-full-access"}</dd>
                    <dt className="text-muted-foreground">Approval</dt><dd className="font-mono">{agent.approvalPolicy || "never"}</dd>
                  </dl>
                  <div className="mt-3 flex justify-end border-t border-border pt-2">
                    <Button variant="outline" size="sm" onClick={() => { setOpenInfoId(null); onEdit(agent.id); }}>
                      Edit settings
                    </Button>
                  </div>
                </PopoverContent>
              </Popover>
              <button
                type="button"
                onClick={() => onClose(agent.id)}
                className={`mr-1 flex size-6 shrink-0 items-center justify-center rounded-sm outline-none hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring/40 ${active || unseen ? "text-muted-foreground" : "text-muted-foreground/0 group-hover:text-muted-foreground"}`}
                title={`Close ${agent.name} tab`}
                aria-label={`Close ${agent.name} tab`}
              >
                <X className="size-3" />
              </button>
            </div>
          );
        })}
      </div>
    </div>
  );
}

export default function App() {
  const queryClient = useQueryClient();
  const agentsQuery = useQuery<{ agents: Agent[] }>({
    queryKey: ["agents"],
    queryFn: () => api("GET", "/api/agents"),
  });
  const remoteQuery = useQuery<RemoteSnapshot | null>({
    queryKey: ["remote"],
    queryFn: async () => (await api("GET", "/api/remote")).remote,
    retry: false,
  });
  const backupQuery = useQuery<BackupStatus>({
    queryKey: ["backups"],
    queryFn: () => api("GET", "/api/admin/backups"),
    retry: false,
  });
  const pendingWorkQuery = useQuery<{ entries: InboxEntry[] }>({
    queryKey: ["pending-work"],
    queryFn: () => api("GET", "/api/inbox?active=true"),
    refetchInterval: 30_000,
  });
  const humanRequestsQuery = useQuery<{ requests: HumanRequest[] }>({
    queryKey: ["human-requests"],
    queryFn: () => api("GET", "/api/human-requests"),
    refetchInterval: 30_000,
  });
  const agents = agentsQuery.data?.agents || [];
  const remote = remoteQuery.data || null;
  const backupStatus = backupQuery.data || { backups: [], dir: "", count: 0, totalBytes: 0, retention: { minCount: 2, maxCount: 5, maxBytes: 2 * 1024 ** 3, maxAgeDays: 30 } };
  const setAgents = (next: Agent[] | ((previous: Agent[]) => Agent[])) => {
    queryClient.setQueryData<{ agents: Agent[] }>(["agents"], (current) => {
      const previous = current?.agents || [];
      return { agents: typeof next === "function" ? next(previous) : next };
    });
  };
  const setRemote = (next: RemoteSnapshot | null) => queryClient.setQueryData(["remote"], next);
  const [current, setCurrent] = useState<string | null>(() => sessionStorage.getItem("codexloom-active-agent"));
  const [openAgentIds, setOpenAgentIds] = useState<string[]>(readAgentTabs);
  const [unseenAgentIds, setUnseenAgentIds] = useState<Set<string>>(() => new Set());
  const [view, setView] = useState<"agents" | "needs-you" | "inbox" | "integrations" | "messages" | "schedules" | "team" | "capacity" | "usage" | "remote" | "design">("agents");
  const [targetHint, setTargetHint] = useState("");
  const [sidebarOpen, setSidebarOpen] = useState(false); // mobile drawer
  const [sidebarCollapsed, setSidebarCollapsed] = useState(() => localStorage.getItem("codexloom-sidebar") === "compact");
  const [newAgentOpen, setNewAgentOpen] = useState(false);
  const [adminOpen, setAdminOpen] = useState(false);
  const [configRequest, setConfigRequest] = useState<{ agentId: string; nonce: number } | null>(null);
  const [archivingAgentIds, setArchivingAgentIds] = useState<Set<string>>(() => new Set());
  const [communicationOpen, setCommunicationOpen] = useState(() => localStorage.getItem("codexloom-nav-communication") === "open");
  const [organizationOpen, setOrganizationOpen] = useState(() => localStorage.getItem("codexloom-nav-organization") === "open");
  const [newName, setNewName] = useState("");
  const [newCwd, setNewCwd] = useState("");
  const [toast, setToast] = useState<string | null>(null);
  const [restarting, setRestarting] = useState(false);
  const [restartStatus, setRestartStatus] = useState<any>({ state: "idle" });
  const [backingUp, setBackingUp] = useState(false);
  const toastTimer = useRef<ReturnType<typeof setTimeout>>(null);
  const activeAgentRef = useRef<{ id: string | null; view: typeof view }>({ id: current, view });
  const openAgentIdsRef = useRef(new Set(openAgentIds));

  useEffect(() => {
    const automation = window.codexLoom || window.codexHub || {};
    window.codexLoom = automation;
    window.codexHub = automation;
  }, []);

  useEffect(() => {
    localStorage.setItem("codexloom-sidebar", sidebarCollapsed ? "compact" : "expanded");
  }, [sidebarCollapsed]);

  useEffect(() => {
    sessionStorage.setItem("codexloom-agent-tabs", JSON.stringify(openAgentIds));
    openAgentIdsRef.current = new Set(openAgentIds);
  }, [openAgentIds]);

  useEffect(() => {
    if (current) sessionStorage.setItem("codexloom-active-agent", current);
    else sessionStorage.removeItem("codexloom-active-agent");
    activeAgentRef.current = { id: current, view };
  }, [current, view]);

  useEffect(() => {
    if (!agentsQuery.isSuccess) return;
    const available = new Set(agents.map((agent) => agent.id));
    setOpenAgentIds((ids) => ids.filter((id) => available.has(id)));
    setUnseenAgentIds((ids) => new Set([...ids].filter((id) => available.has(id))));
    setCurrent((id) => (id && available.has(id) ? id : null));
  }, [agents, agentsQuery.isSuccess]);

  useEffect(() => {
    localStorage.setItem("codexloom-nav-communication", communicationOpen ? "open" : "closed");
  }, [communicationOpen]);

  useEffect(() => {
    localStorage.setItem("codexloom-nav-organization", organizationOpen ? "open" : "closed");
  }, [organizationOpen]);

  useEffect(() => {
    if (view === "inbox" || view === "messages") setCommunicationOpen(true);
    if (view === "team" || view === "capacity" || view === "usage" || view === "schedules" || view === "integrations" || view === "remote") setOrganizationOpen(true);
  }, [view]);

  const showToast = (msg: string) => {
    setToast(msg);
    if (toastTimer.current) clearTimeout(toastTimer.current);
    toastTimer.current = setTimeout(() => setToast(null), 4000);
  };

  const refresh = async () => {
    try {
      await queryClient.invalidateQueries({ queryKey: ["agents"] });
    } catch {
      /* Service unreachable; global SSE will retry. */
    }
  };

  const reconcileAgents = async () => {
    try {
      const snapshot = await api("GET", "/api/agents") as { agents: Agent[] };
      setAgents((previous) => mergeAgentSnapshot(previous, snapshot.agents || []));
    } catch {
      /* The live stream reconnects independently; the next reconciliation retries. */
    }
  };

  const refreshRemote = async () => {
    try {
      await queryClient.invalidateQueries({ queryKey: ["remote"] });
    } catch {
      /* Remote is optional while an older compatibility binary is running. */
    }
  };

  // CodexLoom-level live status stream (also delivers the initial snapshot).
  useEffect(() => {
    const es = new EventSource("/api/events");
    es.onopen = () => {
      void reconcileAgents();
    };
    es.onmessage = (e) => {
      try {
        const evt = JSON.parse(e.data);
        if (evt.type === "loom/reconcile") {
          void reconcileAgents();
          void queryClient.invalidateQueries({ queryKey: ["pending-work"] });
          void queryClient.invalidateQueries({ queryKey: ["human-requests"] });
          void queryClient.invalidateQueries({ queryKey: ["remote"] });
          for (const agentId of openAgentIdsRef.current) publishThreadEvent(agentId, evt);
          return;
        }
        if (["loom/inbox-message", "loom/inbox-item", "loom/outbox-item", "loom/comms-message"].includes(evt.type)) {
          void queryClient.invalidateQueries({ queryKey: ["pending-work"] });
        }
        if (evt.type === "loom/human-request") {
          void queryClient.invalidateQueries({ queryKey: ["human-requests"] });
        }
        if (evt.type === "loom/agents") {
          setAgents((previous) => mergeAgentSnapshot(previous, evt.data.agents || []));
        } else if (evt.type === "loom/thread-event") {
          const agentId = evt.data?.agentId;
          const threadEvent = evt.data?.event;
          if (agentId && threadEvent) {
            setAgents((previous) => previous.map((agent) => agent.id === agentId ? applyThreadStatus(agent, threadEvent) : agent));
            publishThreadEvent(agentId, threadEvent);
            const active = activeAgentRef.current;
            if (openAgentIdsRef.current.has(agentId) && (active.view !== "agents" || active.id !== agentId)) {
              setUnseenAgentIds((ids) => new Set(ids).add(agentId));
            }
          }
        } else if (evt.type === "loom/restart-status") {
          setRestartStatus(evt.data.restart || { state: "idle" });
        } else if (evt.type === "loom/remote-status") {
          setRemote(evt.data.remote || null);
        } else if (evt.type === "loom/agent-status") {
          const d = evt.data;
          if (d.status === "killed") {
            setAgents((prev) => prev.filter((s) => s.id !== d.id));
            setOpenAgentIds((ids) => ids.filter((id) => id !== d.id));
            setUnseenAgentIds((ids) => {
              const next = new Set(ids);
              next.delete(d.id);
              return next;
            });
            setCurrent((cur) => (cur === d.id ? null : cur));
          } else {
            setAgents((prev) => {
              const found = prev.some((s) => s.id === d.id);
              if (!found) {
                refresh();
                return prev;
              }
              return prev.map((s) =>
                s.id === d.id
                  ? {
                      ...s,
                      name: d.name ?? s.name,
                      cwd: d.cwd ?? s.cwd,
                      threadId: d.threadId ?? s.threadId,
                      status: d.status,
                      currentTask: d.currentTask || "",
                      lastError: d.lastError || "",
                      model: d.model ?? s.model,
                      effort: d.effort ?? s.effort,
                      sandbox: d.sandbox ?? s.sandbox,
                      approvalPolicy: d.approvalPolicy ?? s.approvalPolicy,
                      goal: Object.prototype.hasOwnProperty.call(d, "goal") ? d.goal || undefined : s.goal,
                      updatedAt: d.updatedAt ?? s.updatedAt,
                    }
                  : s,
              );
            });
          }
        }
      } catch {
        /* ignore */
      }
    };
    return () => es.close();
  }, []);

  useEffect(() => {
    const reconcile = () => void reconcileAgents();
    const timer = window.setInterval(reconcile, 10_000);
    const onVisibilityChange = () => {
      if (document.visibilityState === "visible") reconcile();
    };
    document.addEventListener("visibilitychange", onVisibilityChange);
    return () => {
      window.clearInterval(timer);
      document.removeEventListener("visibilitychange", onVisibilityChange);
    };
  }, []);

  const create = async () => {
    if (!newName.trim() || !newCwd.trim()) {
      showToast("name and cwd required");
      return;
    }
    try {
      const data = await api("POST", "/api/agents", { name: newName.trim(), cwd: newCwd.trim() });
      setNewName("");
      setNewCwd("");
      setNewAgentOpen(false);
      await refresh();
      setOpenAgentIds((ids) => (ids.includes(data.agent.id) ? ids : [...ids, data.agent.id]));
      setUnseenAgentIds((ids) => {
        const next = new Set(ids);
        next.delete(data.agent.id);
        return next;
      });
      setCurrent(data.agent.id);
	  setView("agents");
    } catch (err: any) {
      showToast(err.message);
    }
  };

  const restartLoom = async () => {
    if (restarting) return;
    setRestarting(true);
    try {
      const data = await api("POST", "/api/admin/restart");
      setRestartStatus(data.restart || { state: "restarting" });
      showToast(data.restart?.message || "restart requested");
    } catch (err: any) {
      showToast(err.message);
    } finally {
      setRestarting(false);
    }
  };

  const refreshBackups = async () => {
    try {
      await queryClient.invalidateQueries({ queryKey: ["backups"] });
    } catch {
      /* backup status is admin-local only; ignore when unavailable */
    }
  };

  const backupNow = async () => {
    if (backingUp) return;
    setBackingUp(true);
    try {
      const data = await api("POST", "/api/admin/backup", { reason: "manual" });
      await queryClient.invalidateQueries({ queryKey: ["backups"] });
      const removed = data.backup?.prune?.removedCount || 0;
      showToast(`backup created${removed > 0 ? ` · ${removed} old removed` : ""}`);
    } catch (err: any) {
      showToast(err.message);
    } finally {
      setBackingUp(false);
    }
  };

  // Deep-link: on first Agent load, honor #<id|name> in the URL so an Agent
  // view is directly linkable (and headless-screenshot-able).
  const hashApplied = useRef(false);
  useEffect(() => {
    if (hashApplied.current) return;
    const h = decodeURIComponent(window.location.hash.slice(1));
    const route = h.split("?")[0];
    if (route === "messages") {
      setView("messages");
      hashApplied.current = true;
      return;
    }
    if (route === "needs-you") {
      setView("needs-you");
      hashApplied.current = true;
      return;
    }
    if (route === "inbox") {
      setView("inbox");
      hashApplied.current = true;
      return;
    }
    if (route === "integrations") {
      setView("integrations");
      hashApplied.current = true;
      return;
    }
    if (route === "schedules") {
      setView("schedules");
      hashApplied.current = true;
      return;
    }
    if (route === "team") {
      setView("team");
      hashApplied.current = true;
      return;
    }
    if (route === "usage") {
      setView("usage");
      hashApplied.current = true;
      return;
    }
    if (route === "capacity") {
      setView("capacity");
      hashApplied.current = true;
      return;
    }
    if (route === "remote") {
      setView("remote");
      hashApplied.current = true;
      return;
    }
    if (route === "design") {
      setView("design");
      hashApplied.current = true;
      return;
    }
    if (agents.length === 0) return;
    if (h) {
      const s = agents.find((x) => x.id === h || x.name === h);
      if (s) {
        setOpenAgentIds((ids) => (ids.includes(s.id) ? ids : [...ids, s.id]));
        setCurrent(s.id);
      }
    }
    hashApplied.current = true;
  }, [agents]);

  const selectAgent = (id: string) => {
    setOpenAgentIds((ids) => (ids.includes(id) ? ids : [...ids, id]));
    setUnseenAgentIds((ids) => {
      if (!ids.has(id)) return ids;
      const next = new Set(ids);
      next.delete(id);
      return next;
    });
    setCurrent(id);
    setView("agents");
    setSidebarOpen(false);
    const s = agents.find((x) => x.id === id);
    if (s) window.location.hash = encodeURIComponent(s.name);
  };

  const closeAgent = (id: string) => {
    setUnseenAgentIds((ids) => {
      if (!ids.has(id)) return ids;
      const next = new Set(ids);
      next.delete(id);
      return next;
    });
    setOpenAgentIds((ids) => {
      const index = ids.indexOf(id);
      if (index < 0) return ids;
      const next = ids.filter((candidate) => candidate !== id);
      if (current === id) {
        const nextId = next[Math.min(index, next.length - 1)] || null;
        setCurrent(nextId);
        if (nextId) {
          setView("agents");
          const nextAgent = agents.find((agent) => agent.id === nextId);
          if (nextAgent) window.location.hash = encodeURIComponent(nextAgent.name);
        } else if (view === "agents") {
          window.history.replaceState(null, "", window.location.pathname + window.location.search);
        }
      }
      return next;
    });
  };

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.defaultPrevented || event.isComposing || openAgentIds.length === 0) return;

      let nextId: string | undefined;
      if (!event.metaKey && (event.ctrlKey || event.altKey) && !event.shiftKey && /^Digit[1-9]$/.test(event.code)) {
        const requested = Number(event.code.slice(-1));
        const index = requested === 9 ? openAgentIds.length - 1 : requested - 1;
        nextId = openAgentIds[index];
      } else if (event.ctrlKey && !event.altKey && !event.metaKey && event.code === "Tab") {
        const currentIndex = openAgentIds.indexOf(current || "");
        const offset = event.shiftKey ? -1 : 1;
        const start = currentIndex < 0 ? (offset > 0 ? -1 : 0) : currentIndex;
        nextId = openAgentIds[(start + offset + openAgentIds.length) % openAgentIds.length];
      }

      if (!nextId) return;
      event.preventDefault();
      selectAgent(nextId);
    };

    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [agents, current, openAgentIds]);

  const editAgent = (id: string) => {
    selectAgent(id);
    setConfigRequest({ agentId: id, nonce: Date.now() });
  };

  const archiveAgent = async (agent: Agent) => {
    if (archivingAgentIds.has(agent.id)) return;
    if (!confirm(`archive agent "${agent.name}" and its Codex thread?`)) return;
    setArchivingAgentIds((ids) => new Set(ids).add(agent.id));
    try {
      await api("DELETE", `/api/agents/${agent.id}`);
      closeAgent(agent.id);
    } catch (err: any) {
      showToast(err.message);
    } finally {
      setArchivingAgentIds((ids) => {
        const next = new Set(ids);
        next.delete(agent.id);
        return next;
      });
    }
  };

  const selectMessages = () => {
    setTargetHint("");
    setView("messages");
    setCurrent(null);
    setSidebarOpen(false);
    window.location.hash = "messages";
  };

  const selectNeedsYou = (requestID?: string) => {
    setView("needs-you");
    setCurrent(null);
    setSidebarOpen(false);
    window.location.hash = requestID ? `needs-you?request=${encodeURIComponent(requestID)}` : "needs-you";
  };

  const selectInbox = (itemID?: string) => {
    setView("inbox");
    setCurrent(null);
    setSidebarOpen(false);
    window.location.hash = itemID ? `inbox?item=${encodeURIComponent(itemID)}` : "inbox";
  };

  const selectIntegrations = () => {
    setView("integrations");
    setCurrent(null);
    setSidebarOpen(false);
    window.location.hash = "integrations";
  };

  const selectSchedules = () => {
    setTargetHint("");
    setView("schedules");
    setCurrent(null);
    setSidebarOpen(false);
    window.location.hash = "schedules";
  };

  const selectTeam = () => {
    setView("team");
    setCurrent(null);
    setSidebarOpen(false);
    window.location.hash = "team";
  };

  const selectUsage = () => {
    setView("usage");
    setCurrent(null);
    setSidebarOpen(false);
    window.location.hash = "usage";
  };

  const selectCapacity = () => {
    setView("capacity");
    setCurrent(null);
    setSidebarOpen(false);
    window.location.hash = "capacity";
  };

  const selectRemote = () => {
    setView("remote");
    setCurrent(null);
    setSidebarOpen(false);
    window.location.hash = "remote";
  };

  const selectDesign = () => {
    setView("design");
    setCurrent(null);
    setSidebarOpen(false);
    window.location.hash = "design";
  };

  const messageAgent = (name: string) => {
    setTargetHint(name);
    setView("messages");
    setCurrent(null);
    setSidebarOpen(false);
    window.location.hash = "messages";
  };

  const scheduleAgent = (name: string) => {
    setTargetHint(name);
    setView("schedules");
    setCurrent(null);
    setSidebarOpen(false);
    window.location.hash = "schedules";
  };

  useEffect(() => {
    const root = (window.codexLoom ||= window.codexHub || {});
    window.codexHub = root;
    root.state = () => ({
      product: "CodexLoom",
      view,
      agentsCount: agents.length,
      activeCount: agents.filter(isAgentWorking).length,
      idleCount: agents.filter((agent) => !isAgentWorking(agent)).length,
      activeAgents: agents
        .filter(isAgentWorking)
        .map((agent) => ({
          id: agent.id,
          name: agent.name,
          task: summarizeTask(agent.currentTask || agent.goal?.objective || "") || "Running turn",
          goalStatus: agent.goal?.status || null,
        })),
      selectedAgent: agents.find((agent) => agent.id === current)?.name || null,
      selectedAgentId: current,
      openAgents: openAgentIds.map((id) => agents.find((agent) => agent.id === id)?.name).filter(Boolean),
      openAgentIds,
      openAgentStatuses: Object.fromEntries(openAgentIds.map((id) => {
        const agent = agents.find((candidate) => candidate.id === id);
        return [id, agent ? agentRuntimeLabel(agent) : "missing"];
      })),
      unseenAgentIds: [...unseenAgentIds],
      pendingWorkByAgent: Object.fromEntries(agents.map((agent) => [
        agent.id,
        (pendingWorkQuery.data?.entries || []).filter((entry) => entry.item.agentId === agent.id && !["handled", "cancelled"].includes(entry.item.state)).length,
      ])),
      needsYouCount: (humanRequestsQuery.data?.requests || []).filter((request) => request.state === "open").length,
      needsYouByAgent: Object.fromEntries(agents.map((agent) => [
        agent.id,
        (humanRequestsQuery.data?.requests || []).filter((request) => request.agentId === agent.id && request.state === "open").length,
      ])),
      threadSubscribers: threadEventSubscriberCount(),
      restartState: restartStatus?.state || "idle",
      remoteState: remote?.status.state || "unknown",
      sidebar: sidebarCollapsed ? "compact" : "expanded",
      navGroups: { communication: communicationOpen, organization: organizationOpen },
    });
    root.selectAgent = async (key: string) => {
      const agent = agents.find((candidate) => candidate.id === key || candidate.name === key);
      if (!agent) throw new Error(`Agent not found: ${key}`);
      selectAgent(agent.id);
      await new Promise((resolve) => window.setTimeout(resolve, 50));
      return window.codexLoom?.state?.();
    };
    root.closeAgent = async (key: string) => {
      const agent = agents.find((candidate) => candidate.id === key || candidate.name === key);
      if (!agent) throw new Error(`Agent not found: ${key}`);
      closeAgent(agent.id);
      await new Promise((resolve) => window.setTimeout(resolve, 50));
      return window.codexLoom?.state?.();
    };
    root.openTeam = async () => {
      selectTeam();
      await new Promise((resolve) => window.setTimeout(resolve, 50));
      return window.codexLoom?.state?.();
    };
    root.openUsage = async () => {
      selectUsage();
      await new Promise((resolve) => window.setTimeout(resolve, 50));
      return window.codexLoom?.state?.();
    };
    root.openCapacity = async () => {
      selectCapacity();
      await new Promise((resolve) => window.setTimeout(resolve, 50));
      return window.codexLoom?.state?.();
    };
    root.openInbox = async () => {
      selectInbox();
      await new Promise((resolve) => window.setTimeout(resolve, 50));
      return window.codexLoom?.state?.();
    };
    root.openNeedsYou = async (requestID?: string) => {
      selectNeedsYou(requestID);
      await new Promise((resolve) => window.setTimeout(resolve, 50));
      return window.codexLoom?.state?.();
    };
    root.openMessages = async () => {
      selectMessages();
      await new Promise((resolve) => window.setTimeout(resolve, 50));
      return window.codexLoom?.state?.();
    };
    root.openRemote = async () => {
      selectRemote();
      await new Promise((resolve) => window.setTimeout(resolve, 50));
      return window.codexLoom?.state?.();
    };
    root.openDesign = async () => {
      selectDesign();
      await new Promise((resolve) => window.setTimeout(resolve, 50));
      return window.codexLoom?.state?.();
    };
    root.setSidebar = async (mode: "compact" | "expanded") => {
      if (mode !== "compact" && mode !== "expanded") throw new Error(`Unknown sidebar mode: ${mode}`);
      setSidebarCollapsed(mode === "compact");
      await new Promise((resolve) => window.setTimeout(resolve, 220));
      return window.codexLoom?.state?.();
    };
    root.setNavGroup = async (group: "communication" | "organization", open: boolean) => {
      if (group === "communication") setCommunicationOpen(open);
      else if (group === "organization") setOrganizationOpen(open);
      else throw new Error(`Unknown navigation group: ${group}`);
      await new Promise((resolve) => window.setTimeout(resolve, 120));
      return window.codexLoom?.state?.();
    };
  }, [agents, communicationOpen, current, humanRequestsQuery.data?.requests, openAgentIds, organizationOpen, pendingWorkQuery.data?.entries, remote?.status.state, restartStatus?.state, sidebarCollapsed, unseenAgentIds, view]);

  const updateAgent = (updated: Agent) => {
    setAgents((prev) => prev.map((s) => (s.id === updated.id ? updated : s)));
    if (updated.id === current) {
      window.location.hash = encodeURIComponent(updated.name);
    }
  };

  const selected = view === "agents" ? agents.find((s) => s.id === current) || null : null;
  const openAgents = openAgentIds.map((id) => agents.find((agent) => agent.id === id)).filter((agent): agent is Agent => Boolean(agent));
  const humanRequests = humanRequestsQuery.data?.requests || [];
  const openHumanRequests = humanRequests.filter((request) => request.state === "open");
  const restartState = restartStatus?.state || "idle";
  const restartPending = restartState === "waiting" || restartState === "restarting";
  const activeCount = agents.filter(isAgentWorking).length;
  const latestBackup = backupStatus?.backups?.[0];

  useEffect(() => {
    if (view !== "agents" || !selected) return;
    const nextHash = "#" + encodeURIComponent(selected.name);
    if (window.location.hash !== nextHash) {
      window.history.replaceState(null, "", nextHash);
    }
  }, [selected?.id, selected?.name, view]);

  useEffect(() => {
    const attention = openHumanRequests.length > 0 ? `(${openHumanRequests.length}) ` : "";
    if (restartState === "waiting") {
      document.title = `${attention}Restart waiting · CodexLoom`;
    } else if (restartState === "restarting") {
      document.title = `${attention}Restarting · CodexLoom`;
    } else if (view === "needs-you") {
      document.title = `${attention}Needs you · CodexLoom`;
    } else if (view === "messages") {
      document.title = `${attention}Messages · CodexLoom`;
    } else if (view === "inbox") {
      document.title = `${attention}Inbox · CodexLoom`;
    } else if (view === "integrations") {
      document.title = `${attention}Integrations · CodexLoom`;
    } else if (view === "schedules") {
      document.title = `${attention}Schedules · CodexLoom`;
    } else if (view === "team") {
      document.title = `${attention}Team · CodexLoom`;
    } else if (view === "usage") {
      document.title = `${attention}Token usage · CodexLoom`;
    } else if (view === "capacity") {
      document.title = `${attention}Capacity · CodexLoom`;
    } else if (view === "remote") {
      document.title = `${attention}${remote?.status.state === "connected" ? "● " : ""}Remote · CodexLoom`;
    } else if (view === "design") {
      document.title = `${attention}Design System · CodexLoom`;
    } else if (selected) {
      const marker = isAgentWorking(selected) ? "● " : selected.lastError ? "! " : "";
      document.title = `${attention}${marker}${selected.name} · CodexLoom`;
    } else if (activeCount > 0) {
      document.title = `(${activeCount}) CodexLoom`;
    } else {
      document.title = "CodexLoom";
    }
  }, [activeCount, openHumanRequests.length, remote?.status.state, restartState, selected, view]);

  // Middle-truncate long paths so the trailing folder (what distinguishes
  // same-named projects) stays visible.
  const midPath = (p: string) => {
    if (p.length <= 34) return p;
    return p.slice(0, 14) + "…" + p.slice(-18);
  };

  const clipTask = (task: string) => {
    const summary = summarizeTask(task);
    if (summary.length <= 46) return summary;
    return summary.slice(0, 43) + "…";
  };

  const backupLabel = (backup: any) => {
    if (!backup?.createdAt) return "No backups";
    const d = new Date(backup.createdAt);
    if (Number.isNaN(d.getTime())) return backup.name || "Backup ready";
    return `Last backup ${d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}`;
  };

  const formatStorage = (bytes: number) => {
    if (!Number.isFinite(bytes) || bytes <= 0) return "0 B";
    const units = ["B", "KiB", "MiB", "GiB", "TiB"];
    const power = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
    return `${(bytes / 1024 ** power).toFixed(power === 0 ? 0 : 1)} ${units[power]}`;
  };

  return (
    <div className="flex h-screen w-screen max-w-full overflow-hidden">
      {/* backdrop — only on mobile when the drawer is open */}
      {sidebarOpen && (
        <div
          className="fixed inset-0 z-30 bg-black/40 md:hidden"
          onClick={() => setSidebarOpen(false)}
        />
      )}
      {/* sidebar — full drawer on mobile, compact or expanded rail on desktop */}
      <aside
        className={`fixed inset-y-0 left-0 z-40 flex w-[272px] shrink-0 transform flex-col bg-sidebar shadow-xl transition-[width,transform,translate] duration-200 md:static md:z-auto md:translate-x-0 md:bg-sidebar/60 md:shadow-none ${sidebarCollapsed ? "md:w-16" : "md:w-[272px]"} ${
          sidebarOpen ? "translate-x-0" : "-translate-x-full"
        }`}
      >
        <div className="relative flex h-14 shrink-0 items-center px-3">
          <div className={`min-w-0 ${sidebarCollapsed ? "md:hidden" : ""}`}><BrandLockup compact /></div>
          <div className={`hidden w-full items-center justify-center ${sidebarCollapsed ? "md:flex" : ""}`}><BrandMark className="size-8" title="CodexLoom" /></div>
          <Button
            variant="ghost"
            size="icon-sm"
            onClick={() => setSidebarOpen(false)}
            title="Close sidebar"
            aria-label="Close sidebar"
            className="ml-auto md:hidden"
          >
            <X />
          </Button>
        </div>

        <Separator className="mx-3 w-auto" />

        <div className={sidebarCollapsed ? "md:hidden" : ""}>
          <AgentActivityPopover agents={agents} humanRequests={openHumanRequests} onSelect={selectAgent} onSelectRequest={selectNeedsYou} />
        </div>
        <div className={`hidden ${sidebarCollapsed ? "md:block" : ""}`}>
          <AgentActivityPopover agents={agents} humanRequests={openHumanRequests} compact onSelect={selectAgent} onSelectRequest={selectNeedsYou} />
        </div>

        <nav className="px-2 pb-2" aria-label="Workspace">
          <div className={`hidden space-y-0.5 ${sidebarCollapsed ? "md:block" : ""}`}>
            <SidebarNavItem label="Needs You" icon={CircleHelp} active={view === "needs-you"} compact onSelect={() => selectNeedsYou()} count={openHumanRequests.length} />
            <SidebarNavItem label="Inbox" icon={InboxIcon} active={view === "inbox"} compact onSelect={selectInbox} />
            <SidebarNavItem label="Messages" icon={MessageSquare} active={view === "messages"} compact onSelect={selectMessages} />
            <SidebarNavItem label="Team" icon={Network} active={view === "team"} compact onSelect={selectTeam} />
            <SidebarNavItem label="Capacity" icon={Activity} active={view === "capacity"} compact onSelect={selectCapacity} />
            <SidebarNavItem label="Token usage" icon={BarChart3} active={view === "usage"} compact onSelect={selectUsage} />
            <SidebarNavItem label="Schedules" icon={CalendarClock} active={view === "schedules"} compact onSelect={selectSchedules} />
            <SidebarNavItem label="Integrations" icon={Cable} active={view === "integrations"} compact onSelect={selectIntegrations} />
            <SidebarNavItem
              label="Remote"
              icon={RadioTower}
              active={view === "remote"}
              compact
              onSelect={selectRemote}
              indicator={remote?.status.state === "connected" ? "success" : remote?.status.state === "error" ? "destructive" : remote?.status.state === "connecting" || remote?.status.state === "starting" ? "warning" : "muted"}
            />
          </div>
          <div className={sidebarCollapsed ? "md:hidden" : ""}>
            <div className="mb-1 border-b border-sidebar-border pb-1">
              <SidebarNavItem label="Needs You" icon={CircleHelp} active={view === "needs-you"} compact={false} onSelect={() => selectNeedsYou()} count={openHumanRequests.length} />
            </div>
            <SidebarNavGroup label="Communication" open={communicationOpen} onOpenChange={setCommunicationOpen}>
              <SidebarNavItem label="Inbox" icon={InboxIcon} active={view === "inbox"} compact={false} onSelect={selectInbox} />
              <SidebarNavItem label="Messages" icon={MessageSquare} active={view === "messages"} compact={false} onSelect={selectMessages} />
            </SidebarNavGroup>
            <SidebarNavGroup label="Organization" open={organizationOpen} onOpenChange={setOrganizationOpen}>
              <SidebarNavItem label="Team" icon={Network} active={view === "team"} compact={false} onSelect={selectTeam} />
              <SidebarNavItem label="Capacity" icon={Activity} active={view === "capacity"} compact={false} onSelect={selectCapacity} />
              <SidebarNavItem label="Token usage" icon={BarChart3} active={view === "usage"} compact={false} onSelect={selectUsage} />
              <SidebarNavItem label="Schedules" icon={CalendarClock} active={view === "schedules"} compact={false} onSelect={selectSchedules} />
              <SidebarNavItem label="Integrations" icon={Cable} active={view === "integrations"} compact={false} onSelect={selectIntegrations} />
              <SidebarNavItem
                label="Remote"
                icon={RadioTower}
                active={view === "remote"}
                compact={false}
                onSelect={selectRemote}
                indicator={remote?.status.state === "connected" ? "success" : remote?.status.state === "error" ? "destructive" : remote?.status.state === "connecting" || remote?.status.state === "starting" ? "warning" : "muted"}
              />
            </SidebarNavGroup>
          </div>
        </nav>

        <section className={`mx-2 mt-1 flex min-h-0 flex-1 flex-col overflow-hidden rounded-t-md bg-background/45 ${sidebarCollapsed ? "md:hidden" : ""}`} aria-label="Agents">
          <div className="flex h-8 shrink-0 items-center gap-2 px-2.5 text-muted-foreground">
            <Bot className="size-3" />
            <span className="text-[9px] font-bold uppercase">Agents</span>
            <span className="ml-auto font-mono text-[9px] text-muted-foreground/60">{agents.length}</span>
          </div>

          <div className="min-h-0 flex-1 space-y-0.5 overflow-y-auto px-1 pb-2">
            {agents.map((s) => {
              const active = s.id === current;
              const archiving = archivingAgentIds.has(s.id);
              const needsYou = openHumanRequests.filter((request) => request.agentId === s.id).length;
              const activity = s.currentTask || s.goal?.objective || "";
              const detailTitle = activity ? `${s.cwd}\n${summarizeTask(activity)}` : s.cwd;
              return (
                <div key={s.id} className="group/agent relative">
                  <Button
                    type="button"
                    variant="ghost"
                    onClick={() => selectAgent(s.id)}
                    title={detailTitle}
                    className={`relative h-8 w-full justify-start overflow-hidden px-2.5 pr-8 text-left ${
                      active ? "bg-selection text-selection-foreground hover:bg-selection" : "text-foreground/85"
                    }`}
                  >
                    <span className={`size-2 shrink-0 rounded-full ${isAgentWorking(s) ? "pulse bg-success ring-2 ring-success/20" : "bg-muted-foreground/30"}`} />
                    <span className={`min-w-0 flex-1 truncate text-[12.5px] ${active ? "font-semibold" : "font-medium"}`}>{s.name}</span>
                    {activity ? <span className="size-1.5 shrink-0 rounded-full bg-warning" title={clipTask(activity)} /> : null}
                  </Button>
                  {needsYou > 0 ? <button type="button" onClick={() => selectNeedsYou(openHumanRequests.find((request) => request.agentId === s.id)?.id)} className="absolute right-8 top-1.5 flex min-w-5 items-center justify-center rounded-sm bg-warning/15 px-1 font-mono text-[8.5px] font-semibold text-warning" title={`${needsYou} request${needsYou === 1 ? "" : "s"} need your input`} aria-label={`Open ${needsYou} human request${needsYou === 1 ? "" : "s"} from ${s.name}`}>{needsYou}</button> : null}
                  <button
                    type="button"
                    onClick={() => archiveAgent(s)}
                    disabled={archiving}
                    tabIndex={active ? 0 : -1}
                    className={`absolute right-1 top-1 flex size-6 items-center justify-center rounded-sm text-muted-foreground outline-none transition hover:bg-destructive/10 hover:text-destructive focus-visible:ring-2 focus-visible:ring-destructive/30 disabled:opacity-50 ${active ? "visible opacity-70" : "invisible opacity-0 group-hover/agent:visible group-hover/agent:opacity-70 group-focus-within/agent:visible group-focus-within/agent:opacity-70"}`}
                    title={`Archive ${s.name}`}
                    aria-label={`Archive ${s.name}`}
                  >
                    <Archive className={`size-3 ${archiving ? "animate-pulse" : ""}`} />
                  </button>
                </div>
              );
            })}
            {agents.length === 0 && (
              <div className="px-3 py-6 text-center text-[12px] text-muted-foreground/50">
                No agents yet.
              </div>
            )}
          </div>
        </section>

        <div className={`hidden flex-1 ${sidebarCollapsed ? "md:block" : ""}`} />

        <div className={`shrink-0 border-t border-border/60 bg-sidebar/85 p-2 ${sidebarCollapsed ? "md:flex md:flex-col md:items-center md:gap-1" : "grid grid-cols-[1fr_auto_auto_auto] gap-1"}`}>
          <Button onClick={() => setNewAgentOpen(true)} title="Create agent" className={sidebarCollapsed ? "md:hidden" : ""}>
            <Plus />
            <span>New agent</span>
          </Button>
          <Button variant="outline" size="icon" onClick={selectDesign} title="Design system" aria-label="Design system" className={sidebarCollapsed ? "md:hidden" : ""}><SwatchBook /></Button>
          <Button variant="outline" size="icon" onClick={() => setAdminOpen(true)} title="Loom administration" aria-label="Loom administration" className={sidebarCollapsed ? "md:hidden" : ""}>
            {restartPending ? <RotateCw className="animate-spin text-warning" /> : <Settings2 />}
          </Button>
          <Button
            variant="outline"
            size="icon"
            onClick={() => setSidebarCollapsed((value) => !value)}
            title={sidebarCollapsed ? "Expand sidebar" : "Collapse sidebar"}
            aria-label={sidebarCollapsed ? "Expand sidebar" : "Collapse sidebar"}
            className="hidden md:inline-flex"
          >
            {sidebarCollapsed ? <PanelLeftOpen /> : <PanelLeftClose />}
          </Button>
        </div>
      </aside>

      <Dialog open={newAgentOpen} onOpenChange={setNewAgentOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Create agent</DialogTitle>
            <DialogDescription>Create a long-lived domain agent backed by a Codex Thread.</DialogDescription>
          </DialogHeader>
          <div className="space-y-3">
            <label className="block space-y-1.5 text-[11px] font-medium text-muted-foreground">
              Agent name
              <Input value={newName} onChange={(event) => setNewName(event.target.value)} placeholder="codex-research" spellCheck={false} />
            </label>
            <label className="block space-y-1.5 text-[11px] font-medium text-muted-foreground">
              Working directory
              <Input value={newCwd} onChange={(event) => setNewCwd(event.target.value)} placeholder="/absolute/path/to/workspace" spellCheck={false} className="font-mono text-[12px]" />
            </label>
          </div>
          <DialogFooter showCloseButton>
            <Button onClick={create}><Plus />Create agent</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={adminOpen} onOpenChange={setAdminOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Loom administration</DialogTitle>
            <DialogDescription>Durable operations are kept out of the everyday navigation surface.</DialogDescription>
          </DialogHeader>
          <div className="divide-y divide-border rounded-lg border border-border">
            <div className="flex items-center gap-3 p-3">
              <Archive className="size-4 text-primary" />
              <div className="min-w-0 flex-1"><div className="text-[12px] font-medium">Local backup</div><div className="truncate font-mono text-[9.5px] text-muted-foreground" title={`${backupStatus.dir} · retains ${backupStatus.retention.minCount}-${backupStatus.retention.maxCount} snapshots`}>{backupLabel(latestBackup)} · {backupStatus.count} snapshots · {formatStorage(backupStatus.totalBytes)}</div></div>
              <Button variant="outline" size="sm" onClick={backupNow} disabled={backingUp}>{backingUp ? "Backing up" : "Back up"}</Button>
            </div>
            <div className="flex items-center gap-3 p-3">
              <RotateCw className={`size-4 text-primary ${restartPending ? "animate-spin" : ""}`} />
              <div className="min-w-0 flex-1"><div className="text-[12px] font-medium">Restart Loom</div><div className="truncate text-[10px] text-muted-foreground">{restartStatus.message || "Load the already built release safely."}</div></div>
              <Button variant="outline" size="sm" onClick={restartLoom} disabled={restarting || restartPending}>{restartState === "waiting" ? "Waiting" : restartState === "restarting" ? "Restarting" : "Restart"}</Button>
            </div>
          </div>
          {restartStatus.running?.length > 0 ? <div className="rounded-lg bg-warning/10 px-3 py-2 text-[11px] text-warning">Waiting for {restartStatus.running.map((agent: any) => agent.name).join(", ")}</div> : null}
          {restartStatus.operations?.length > 0 ? <div className="rounded-lg bg-warning/10 px-3 py-2 text-[11px] text-warning">Waiting for {restartStatus.operations.map((operation: any) => `${operation.provider || "connector"} ${operation.kind}`).join(", ")}</div> : null}
          <DialogFooter showCloseButton />
        </DialogContent>
      </Dialog>

      {/* mobile drawer toggle — floats over the main content on small screens */}
      <button
        onClick={() => setSidebarOpen(true)}
        aria-label="open agents"
        className={`fixed z-20 flex items-center justify-center rounded-md bg-card/90 shadow-card ring-1 ring-border/50 backdrop-blur md:hidden ${openAgents.length > 0 ? "left-2 top-0.5 size-8" : "left-3 top-3 size-9"}`}
      >
        <Menu className="size-4" />
      </button>

      {/* Agent tabs stay mounted while global workspaces temporarily cover them. */}
      <div className="flex min-w-0 flex-1 flex-col overflow-hidden">
        <AgentTabs
          agents={openAgents}
          humanRequests={openHumanRequests}
          activeId={view === "agents" ? current : null}
          unseenIds={unseenAgentIds}
          onSelect={selectAgent}
          onClose={closeAgent}
          onEdit={editAgent}
        />
        <Suspense fallback={<WorkbenchFallback />}>
          <div className="flex min-h-0 min-w-0 flex-1 overflow-hidden">
            {view === "needs-you" ? (
              <NeedsYouPane
                requests={humanRequests}
                onChanged={() => queryClient.invalidateQueries({ queryKey: ["human-requests"] })}
                onOpenAgent={selectAgent}
                onError={showToast}
              />
            ) : view === "inbox" ? (
              <InboxPane agents={agents} onError={showToast} />
            ) : view === "integrations" ? (
              <IntegrationsPane agents={agents} onError={showToast} />
            ) : view === "messages" ? (
              <MessagesPane agents={agents} onError={showToast} initialTo={targetHint} />
            ) : view === "schedules" ? (
              <SchedulesPane agents={agents} onError={showToast} initialTo={targetHint} />
            ) : view === "team" ? (
              <TeamPane onError={showToast} onMessageAgent={messageAgent} onScheduleAgent={scheduleAgent} />
            ) : view === "capacity" ? (
              <CapacityPane onSelectAgent={selectAgent} />
            ) : view === "usage" ? (
              <UsagePane onSelectAgent={selectAgent} />
            ) : view === "remote" ? (
              <RemotePane remote={remote} onUpdated={setRemote} onError={showToast} />
            ) : view === "design" ? (
              <DesignPane />
            ) : null}

            {openAgents.map((agent) => {
              const active = view === "agents" && agent.id === current;
              return (
                <div key={agent.id} className={active ? "flex min-h-0 min-w-0 flex-1" : "hidden"} aria-hidden={!active}>
                  <AgentPane
                    agent={agent}
                    active={active}
                    configRequestNonce={configRequest?.agentId === agent.id ? configRequest.nonce : 0}
                    pendingWork={(pendingWorkQuery.data?.entries || []).filter((entry) => entry.item.agentId === agent.id && !["handled", "cancelled"].includes(entry.item.state))}
                    humanRequests={openHumanRequests.filter((request) => request.agentId === agent.id)}
                    onOpenPendingWork={selectInbox}
                    onOpenHumanRequest={selectNeedsYou}
                    onHumanRequestChanged={() => queryClient.invalidateQueries({ queryKey: ["human-requests"] })}
                    onPendingWorkChanged={() => queryClient.invalidateQueries({ queryKey: ["pending-work"] })}
                    onAgentUpdated={updateAgent}
                    onError={showToast}
                  />
                </div>
              );
            })}

            {view === "agents" && !selected ? (
              <div className="flex flex-1 flex-col items-center justify-center gap-3 bg-background text-muted-foreground">
                <BrandMark className="size-14 opacity-70" title="CodexLoom" />
                <h2 className="font-serif text-2xl text-foreground/80">CodexLoom</h2>
                <div className="text-sm text-muted-foreground/70">Select or create an agent to begin.</div>
              </div>
            ) : null}
          </div>
        </Suspense>
      </div>

      {/* toast */}
      {toast && (
        <div className="fixed bottom-6 right-6 z-10 rounded-md border border-destructive/30 bg-destructive/10 px-4 py-2.5 text-sm text-destructive shadow-card backdrop-blur">
          {toast}
        </div>
      )}
    </div>
  );
}
