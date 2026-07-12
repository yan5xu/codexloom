import { Archive, Cable, CalendarClock, ChevronRight, Inbox as InboxIcon, Menu, MessageSquare, Network, PanelLeftClose, PanelLeftOpen, Plus, RadioTower, RotateCw, Settings2, SwatchBook, X } from "lucide-react";
import { lazy, Suspense, type ReactNode, useEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, type Agent, type RemoteSnapshot } from "./types";
import { summarizeTask } from "./feed";
import { BrandLockup, BrandMark } from "./components/BrandMark";
import { Button } from "./components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "./components/ui/dialog";
import { Separator } from "./components/ui/separator";
import { Input } from "./components/ui/input";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "./components/ui/collapsible";

const AgentPane = lazy(() => import("./AgentPane").then((module) => ({ default: module.AgentPane })));
const InboxPane = lazy(() => import("./InboxPane").then((module) => ({ default: module.InboxPane })));
const IntegrationsPane = lazy(() => import("./IntegrationsPane").then((module) => ({ default: module.IntegrationsPane })));
const MessagesPane = lazy(() => import("./MessagesPane").then((module) => ({ default: module.MessagesPane })));
const SchedulesPane = lazy(() => import("./SchedulesPane").then((module) => ({ default: module.SchedulesPane })));
const TeamPane = lazy(() => import("./TeamPane").then((module) => ({ default: module.TeamPane })));
const RemotePane = lazy(() => import("./RemotePane").then((module) => ({ default: module.RemotePane })));
const DesignPane = lazy(() => import("./DesignPane").then((module) => ({ default: module.DesignPane })));

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
};

function SidebarNavItem({ label, icon: Icon, active, compact, onSelect, indicator }: SidebarNavItemProps) {
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
  const backupQuery = useQuery<any>({
    queryKey: ["backups"],
    queryFn: () => api("GET", "/api/admin/backups"),
    retry: false,
  });
  const agents = agentsQuery.data?.agents || [];
  const remote = remoteQuery.data || null;
  const backupStatus = backupQuery.data || { backups: [] };
  const setAgents = (next: Agent[] | ((previous: Agent[]) => Agent[])) => {
    queryClient.setQueryData<{ agents: Agent[] }>(["agents"], (current) => {
      const previous = current?.agents || [];
      return { agents: typeof next === "function" ? next(previous) : next };
    });
  };
  const setRemote = (next: RemoteSnapshot | null) => queryClient.setQueryData(["remote"], next);
  const setBackupStatus = (next: any | ((previous: any) => any)) => {
    queryClient.setQueryData(["backups"], (current: any) =>
      typeof next === "function" ? next(current || { backups: [] }) : next,
    );
  };
  const [current, setCurrent] = useState<string | null>(null);
  const [view, setView] = useState<"agents" | "inbox" | "integrations" | "messages" | "schedules" | "team" | "remote" | "design">("agents");
  const [targetHint, setTargetHint] = useState("");
  const [sidebarOpen, setSidebarOpen] = useState(false); // mobile drawer
  const [sidebarCollapsed, setSidebarCollapsed] = useState(() => localStorage.getItem("codexloom-sidebar") === "compact");
  const [newAgentOpen, setNewAgentOpen] = useState(false);
  const [adminOpen, setAdminOpen] = useState(false);
  const [communicationOpen, setCommunicationOpen] = useState(() => localStorage.getItem("codexloom-nav-communication") === "open");
  const [organizationOpen, setOrganizationOpen] = useState(() => localStorage.getItem("codexloom-nav-organization") === "open");
  const [newName, setNewName] = useState("");
  const [newCwd, setNewCwd] = useState("");
  const [toast, setToast] = useState<string | null>(null);
  const [restarting, setRestarting] = useState(false);
  const [restartStatus, setRestartStatus] = useState<any>({ state: "idle" });
  const [backingUp, setBackingUp] = useState(false);
  const toastTimer = useRef<ReturnType<typeof setTimeout>>(null);

  useEffect(() => {
    const automation = window.codexLoom || window.codexHub || {};
    window.codexLoom = automation;
    window.codexHub = automation;
  }, []);

  useEffect(() => {
    localStorage.setItem("codexloom-sidebar", sidebarCollapsed ? "compact" : "expanded");
  }, [sidebarCollapsed]);

  useEffect(() => {
    localStorage.setItem("codexloom-nav-communication", communicationOpen ? "open" : "closed");
  }, [communicationOpen]);

  useEffect(() => {
    localStorage.setItem("codexloom-nav-organization", organizationOpen ? "open" : "closed");
  }, [organizationOpen]);

  useEffect(() => {
    if (view === "inbox" || view === "messages") setCommunicationOpen(true);
    if (view === "team" || view === "schedules" || view === "integrations" || view === "remote") setOrganizationOpen(true);
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
    es.onmessage = (e) => {
      try {
        const evt = JSON.parse(e.data);
        if (evt.type === "loom/agents") {
          setAgents(evt.data.agents);
        } else if (evt.type === "loom/restart-status") {
          setRestartStatus(evt.data.restart || { state: "idle" });
        } else if (evt.type === "loom/remote-status") {
          setRemote(evt.data.remote || null);
        } else if (evt.type === "loom/agent-status") {
          const d = evt.data;
          if (d.status === "killed") {
            setAgents((prev) => prev.filter((s) => s.id !== d.id));
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
      setBackupStatus((prev: any) => ({
        ...prev,
        dir: data.dir || prev.dir,
        backups: [data.backup, ...(prev.backups || [])],
      }));
      showToast(`backup created: ${data.backup?.name || "snapshot"}`);
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
      if (s) setCurrent(s.id);
    }
    hashApplied.current = true;
  }, [agents]);

  const selectAgent = (id: string) => {
    setCurrent(id);
    setView("agents");
    setSidebarOpen(false);
    const s = agents.find((x) => x.id === id);
    if (s) window.location.hash = encodeURIComponent(s.name);
  };

  const selectMessages = () => {
    setTargetHint("");
    setView("messages");
    setCurrent(null);
    setSidebarOpen(false);
    window.location.hash = "messages";
  };

  const selectInbox = () => {
    setView("inbox");
    setCurrent(null);
    setSidebarOpen(false);
    window.location.hash = "inbox";
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
      activeCount: agents.filter((agent) => agent.status === "running").length,
      idleCount: agents.filter((agent) => agent.status === "idle").length,
      selectedAgent: agents.find((agent) => agent.id === current)?.name || null,
      selectedAgentId: current,
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
    root.openTeam = async () => {
      selectTeam();
      await new Promise((resolve) => window.setTimeout(resolve, 50));
      return window.codexLoom?.state?.();
    };
    root.openInbox = async () => {
      selectInbox();
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
  }, [agents, communicationOpen, current, organizationOpen, remote?.status.state, restartStatus?.state, sidebarCollapsed, view]);

  const updateAgent = (updated: Agent) => {
    setAgents((prev) => prev.map((s) => (s.id === updated.id ? updated : s)));
    if (updated.id === current) {
      window.location.hash = encodeURIComponent(updated.name);
    }
  };

  const selected = view === "agents" ? agents.find((s) => s.id === current) || null : null;
  const restartState = restartStatus?.state || "idle";
  const restartPending = restartState === "waiting" || restartState === "restarting";
  const activeCount = agents.filter((s) => s.status === "running").length;
  const idleCount = agents.filter((s) => s.status === "idle").length;
  const latestBackup = backupStatus?.backups?.[0];

  useEffect(() => {
    if (view !== "agents" || !selected) return;
    const nextHash = "#" + encodeURIComponent(selected.name);
    if (window.location.hash !== nextHash) {
      window.history.replaceState(null, "", nextHash);
    }
  }, [selected?.id, selected?.name, view]);

  useEffect(() => {
    if (restartState === "waiting") {
      document.title = "Restart waiting · CodexLoom";
    } else if (restartState === "restarting") {
      document.title = "Restarting · CodexLoom";
    } else if (view === "messages") {
      document.title = "Messages · CodexLoom";
    } else if (view === "inbox") {
      document.title = "Inbox · CodexLoom";
    } else if (view === "integrations") {
      document.title = "Integrations · CodexLoom";
    } else if (view === "schedules") {
      document.title = "Schedules · CodexLoom";
    } else if (view === "team") {
      document.title = "Team · CodexLoom";
    } else if (view === "remote") {
      document.title = `${remote?.status.state === "connected" ? "● " : ""}Remote · CodexLoom`;
    } else if (view === "design") {
      document.title = "Design System · CodexLoom";
    } else if (selected) {
      const marker = selected.status === "running" ? "● " : selected.lastError ? "! " : "";
      document.title = `${marker}${selected.name} · CodexLoom`;
    } else if (activeCount > 0) {
      document.title = `(${activeCount}) CodexLoom`;
    } else {
      document.title = "CodexLoom";
    }
  }, [activeCount, remote?.status.state, restartState, selected, view]);

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

        <div className={`mx-3 my-2 flex h-7 items-center justify-between rounded-md bg-background/65 px-2 font-mono text-[9.5px] text-muted-foreground ${sidebarCollapsed ? "md:hidden" : ""}`}>
          <span><strong className="text-foreground">{agents.length}</strong> agents</span>
          <span><strong className="text-success">{activeCount}</strong> active</span>
          <span><strong className="text-foreground/65">{idleCount}</strong> idle</span>
        </div>
        <div className={`hidden h-9 items-center justify-center font-mono text-[10px] text-success ${sidebarCollapsed ? "md:flex" : ""}`} title={`${activeCount} active of ${agents.length} agents`}>
          {activeCount}<span className="ml-1 size-1.5 rounded-full bg-success" />
        </div>

        <nav className="px-2 pb-2" aria-label="Workspace">
          <div className={`hidden space-y-0.5 ${sidebarCollapsed ? "md:block" : ""}`}>
            <SidebarNavItem label="Inbox" icon={InboxIcon} active={view === "inbox"} compact onSelect={selectInbox} />
            <SidebarNavItem label="Messages" icon={MessageSquare} active={view === "messages"} compact onSelect={selectMessages} />
            <SidebarNavItem label="Team" icon={Network} active={view === "team"} compact onSelect={selectTeam} />
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
            <SidebarNavGroup label="Communication" open={communicationOpen} onOpenChange={setCommunicationOpen}>
              <SidebarNavItem label="Inbox" icon={InboxIcon} active={view === "inbox"} compact={false} onSelect={selectInbox} />
              <SidebarNavItem label="Messages" icon={MessageSquare} active={view === "messages"} compact={false} onSelect={selectMessages} />
            </SidebarNavGroup>
            <SidebarNavGroup label="Organization" open={organizationOpen} onOpenChange={setOrganizationOpen}>
              <SidebarNavItem label="Team" icon={Network} active={view === "team"} compact={false} onSelect={selectTeam} />
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

        <Separator className={`mx-3 w-auto ${sidebarCollapsed ? "md:hidden" : ""}`} />
        <div className={`flex items-center justify-between px-4 pb-1 pt-2 ${sidebarCollapsed ? "md:hidden" : ""}`}>
          <span className="text-[9px] font-bold uppercase text-muted-foreground">Threads</span>
          <span className="font-mono text-[9px] text-muted-foreground/60">{agents.length}</span>
        </div>

        <div className={`min-h-0 flex-1 space-y-0.5 overflow-y-auto px-2 pb-2 ${sidebarCollapsed ? "md:hidden" : ""}`}>
          {agents.map((s) => {
            const active = s.id === current;
            const detailTitle = s.currentTask ? `${s.cwd}\n${summarizeTask(s.currentTask)}` : s.cwd;
            return (
              <Button
                key={s.id}
                type="button"
                variant="ghost"
                onClick={() => selectAgent(s.id)}
                title={detailTitle}
                className={`group relative h-8 w-full justify-start overflow-hidden px-2.5 text-left ${
                  active ? "bg-selection text-selection-foreground hover:bg-selection" : "text-foreground/85"
                }`}
              >
                <span className={`size-2 shrink-0 rounded-full ${s.status === "running" ? "pulse bg-success ring-2 ring-success/20" : "bg-muted-foreground/30"}`} />
                <span className={`min-w-0 flex-1 truncate text-[12.5px] ${active ? "font-semibold" : "font-medium"}`}>{s.name}</span>
                {s.currentTask ? <span className="size-1.5 shrink-0 rounded-full bg-warning" title={clipTask(s.currentTask)} /> : null}
              </Button>
            );
          })}
          {agents.length === 0 && (
            <div className="px-3 py-6 text-center text-[12px] text-muted-foreground/50">
              No agents yet.
            </div>
          )}
        </div>

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
              <div className="min-w-0 flex-1"><div className="text-[12px] font-medium">Local backup</div><div className="truncate font-mono text-[9.5px] text-muted-foreground" title={backupStatus?.dir}>{backupLabel(latestBackup)}</div></div>
              <Button variant="outline" size="sm" onClick={backupNow} disabled={backingUp}>{backingUp ? "Backing up" : "Back up"}</Button>
            </div>
            <div className="flex items-center gap-3 p-3">
              <RotateCw className={`size-4 text-primary ${restartPending ? "animate-spin" : ""}`} />
              <div className="min-w-0 flex-1"><div className="text-[12px] font-medium">Restart Loom</div><div className="truncate text-[10px] text-muted-foreground">{restartStatus.message || "Load the already built release safely."}</div></div>
              <Button variant="outline" size="sm" onClick={restartLoom} disabled={restarting || restartPending}>{restartState === "waiting" ? "Waiting" : restartState === "restarting" ? "Restarting" : "Restart"}</Button>
            </div>
          </div>
          {restartStatus.running?.length > 0 ? <div className="rounded-lg bg-warning/10 px-3 py-2 text-[11px] text-warning">Waiting for {restartStatus.running.map((agent: any) => agent.name).join(", ")}</div> : null}
          <DialogFooter showCloseButton />
        </DialogContent>
      </Dialog>

      {/* mobile drawer toggle — floats over the main content on small screens */}
      <button
        onClick={() => setSidebarOpen(true)}
        aria-label="open agents"
        className="fixed left-3 top-3 z-20 flex size-9 items-center justify-center rounded-md bg-card/90 shadow-card ring-1 ring-border/50 backdrop-blur md:hidden"
      >
        <Menu className="size-4" />
      </button>

      {/* main */}
      <Suspense fallback={<WorkbenchFallback />}>
        {view === "inbox" ? (
          <InboxPane agents={agents} onError={showToast} />
        ) : view === "integrations" ? (
          <IntegrationsPane agents={agents} onError={showToast} />
        ) : view === "messages" ? (
          <MessagesPane agents={agents} onError={showToast} initialTo={targetHint} />
        ) : view === "schedules" ? (
          <SchedulesPane agents={agents} onError={showToast} initialTo={targetHint} />
        ) : view === "team" ? (
          <TeamPane onError={showToast} onMessageAgent={messageAgent} onScheduleAgent={scheduleAgent} />
        ) : view === "remote" ? (
          <RemotePane remote={remote} onUpdated={setRemote} onError={showToast} />
        ) : view === "design" ? (
          <DesignPane />
        ) : selected ? (
          <AgentPane
            key={selected.id}
            agent={selected}
            onAgentUpdated={updateAgent}
            onKilled={() => setCurrent(null)}
            onError={showToast}
          />
        ) : (
          <div className="flex flex-1 flex-col items-center justify-center gap-3 bg-background text-muted-foreground">
            <BrandMark className="size-14 opacity-70" title="CodexLoom" />
            <h2 className="font-serif text-2xl text-foreground/80">CodexLoom</h2>
            <div className="text-sm text-muted-foreground/70">Select or create an agent to begin.</div>
          </div>
        )}
      </Suspense>

      {/* toast */}
      {toast && (
        <div className="fixed bottom-6 right-6 z-10 rounded-md border border-destructive/30 bg-destructive/10 px-4 py-2.5 text-sm text-destructive shadow-card backdrop-blur">
          {toast}
        </div>
      )}
    </div>
  );
}
