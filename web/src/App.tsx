import { Archive, Cable, CalendarClock, Inbox as InboxIcon, Menu, MessageSquare, Network, RadioTower, RotateCw } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { api, type Agent, type RemoteSnapshot } from "./types";
import { MessagesPane } from "./MessagesPane";
import { SchedulesPane } from "./SchedulesPane";
import { AgentPane } from "./AgentPane";
import { TeamPane } from "./TeamPane";
import { InboxPane } from "./InboxPane";
import { IntegrationsPane } from "./IntegrationsPane";
import { RemotePane } from "./RemotePane";
import { summarizeTask } from "./feed";

export default function App() {
  const [agents, setAgents] = useState<Agent[]>([]);
  const [current, setCurrent] = useState<string | null>(null);
  const [view, setView] = useState<"agents" | "inbox" | "integrations" | "messages" | "schedules" | "team" | "remote">("agents");
  const [remote, setRemote] = useState<RemoteSnapshot | null>(null);
  const [targetHint, setTargetHint] = useState("");
  const [sidebarOpen, setSidebarOpen] = useState(false); // mobile drawer
  const [newName, setNewName] = useState("");
  const [newCwd, setNewCwd] = useState("");
  const [toast, setToast] = useState<string | null>(null);
  const [restarting, setRestarting] = useState(false);
  const [restartStatus, setRestartStatus] = useState<any>({ state: "idle" });
  const [backingUp, setBackingUp] = useState(false);
  const [backupStatus, setBackupStatus] = useState<any>({ backups: [] });
  const toastTimer = useRef<ReturnType<typeof setTimeout>>(null);

  useEffect(() => {
    const automation = window.codexLoom || window.codexHub || {};
    window.codexLoom = automation;
    window.codexHub = automation;
  }, []);

  const showToast = (msg: string) => {
    setToast(msg);
    if (toastTimer.current) clearTimeout(toastTimer.current);
    toastTimer.current = setTimeout(() => setToast(null), 4000);
  };

  const refresh = async () => {
    try {
      const data = await api("GET", "/api/agents");
      setAgents(data.agents);
    } catch {
      /* Service unreachable; global SSE will retry. */
    }
  };

  const refreshRemote = async () => {
    try {
      const data = await api("GET", "/api/remote");
      setRemote(data.remote);
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
      const data = await api("GET", "/api/admin/backups");
      setBackupStatus(data);
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

  useEffect(() => {
    refreshBackups();
    refreshRemote();
  }, []);

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
  }, [agents, current, remote?.status.state, restartStatus?.state, view]);

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
      {/* sidebar — static column on md+, slide-in drawer on mobile */}
      <aside
        className={`fixed inset-y-0 left-0 z-40 flex w-[272px] shrink-0 transform flex-col bg-sidebar shadow-xl transition-transform duration-200 md:static md:z-auto md:translate-x-0 md:bg-sidebar/60 md:shadow-none md:transition-none ${
          sidebarOpen ? "translate-x-0" : "-translate-x-full"
        }`}
      >
        <div className="px-4 pb-2 pt-4">
          <p className="font-mono text-[9px] uppercase tracking-[0.2em] text-muted-foreground/70">codex · loom</p>
          <h2 className="mt-0.5 font-serif text-xl leading-tight tracking-tight">Agents</h2>
        </div>

        <div className="mx-3 h-px bg-border/40" />

        <div className="mx-3 mt-3 grid grid-cols-3 overflow-hidden rounded-lg border border-border/60 bg-background/65">
          <div className="px-2 py-1.5">
            <div className="font-mono text-[10px] font-semibold leading-none text-foreground">
              {agents.length}
            </div>
            <div className="mt-0.5 text-[9px] uppercase tracking-[0.12em] text-muted-foreground">total</div>
          </div>
          <div className="border-l border-border/50 px-2 py-1.5">
            <div className="font-mono text-[10px] font-semibold leading-none text-warning">
              {activeCount}
            </div>
            <div className="mt-0.5 text-[9px] uppercase tracking-[0.12em] text-muted-foreground">active</div>
          </div>
          <div className="border-l border-border/50 px-2 py-1.5">
            <div className="font-mono text-[10px] font-semibold leading-none text-success">
              {idleCount}
            </div>
            <div className="mt-0.5 text-[9px] uppercase tracking-[0.12em] text-muted-foreground">idle</div>
          </div>
        </div>

        <div className="flex items-center justify-between px-4 pb-1 pt-3">
          <span className="text-[9px] font-bold uppercase tracking-[0.15em] text-muted-foreground">
            Comms
          </span>
        </div>

        <div className="px-2 pb-2">
          <button
            onClick={selectInbox}
            className={`flex h-9 w-full items-center gap-2 rounded-xl px-2.5 text-left text-[13px] font-medium transition-colors ${
              view === "inbox" ? "bg-primary/[0.12] text-foreground ring-1 ring-primary/20" : "text-foreground/85 hover:bg-foreground/[0.04]"
            }`}
          >
            <InboxIcon className="size-3.5 text-primary" />
            Inbox
          </button>
          <button
            onClick={selectMessages}
            className={`mt-1 flex h-9 w-full items-center gap-2 rounded-xl px-2.5 text-left text-[13px] font-medium transition-colors ${
              view === "messages" ? "bg-primary/[0.12] text-foreground ring-1 ring-primary/20" : "text-foreground/85 hover:bg-foreground/[0.04]"
            }`}
          >
            <MessageSquare className="size-3.5 text-primary" />
            Messages
          </button>
          <button
            onClick={selectSchedules}
            className={`mt-1 flex h-9 w-full items-center gap-2 rounded-xl px-2.5 text-left text-[13px] font-medium transition-colors ${
              view === "schedules" ? "bg-primary/[0.12] text-foreground ring-1 ring-primary/20" : "text-foreground/85 hover:bg-foreground/[0.04]"
            }`}
          >
            <CalendarClock className="size-3.5 text-primary" />
            Schedules
          </button>
          <button
            onClick={selectTeam}
            className={`mt-1 flex h-9 w-full items-center gap-2 rounded-xl px-2.5 text-left text-[13px] font-medium transition-colors ${
              view === "team" ? "bg-primary/[0.12] text-foreground ring-1 ring-primary/20" : "text-foreground/85 hover:bg-foreground/[0.04]"
            }`}
          >
            <Network className="size-3.5 text-primary" />
            Team
          </button>
          <button
            onClick={selectIntegrations}
            className={`mt-1 flex h-9 w-full items-center gap-2 rounded-xl px-2.5 text-left text-[13px] font-medium transition-colors ${
              view === "integrations" ? "bg-primary/[0.12] text-foreground ring-1 ring-primary/20" : "text-foreground/85 hover:bg-foreground/[0.04]"
            }`}
          >
            <Cable className="size-3.5 text-primary" />
            Integrations
          </button>
          <button
            onClick={selectRemote}
            className={`mt-1 flex h-9 w-full items-center gap-2 rounded-xl px-2.5 text-left text-[13px] font-medium transition-colors ${
              view === "remote" ? "bg-primary/[0.12] text-foreground ring-1 ring-primary/20" : "text-foreground/85 hover:bg-foreground/[0.04]"
            }`}
          >
            <RadioTower className="size-3.5 text-primary" />
            <span className="min-w-0 flex-1 truncate">Remote</span>
            <span
              className={`size-2 shrink-0 rounded-full ${
                remote?.status.state === "connected"
                  ? "bg-success"
                  : remote?.status.state === "connecting" || remote?.status.state === "starting"
                    ? "animate-pulse bg-warning"
                    : remote?.status.state === "error"
                      ? "bg-destructive"
                      : "bg-muted-foreground/30"
              }`}
            />
          </button>
        </div>

        <div className="flex items-center justify-between px-4 pb-1 pt-1">
          <span className="text-[9px] font-bold uppercase tracking-[0.15em] text-muted-foreground">
            Threads
          </span>
          <span className="font-mono text-[10px] text-muted-foreground/50">{agents.length}</span>
        </div>

        <div className="flex-1 space-y-0.5 overflow-y-auto px-2 pb-2">
          {agents.map((s) => {
            const active = s.id === current;
            const detail = s.currentTask ? clipTask(s.currentTask) : midPath(s.cwd);
            const detailTitle = s.currentTask ? `${s.cwd}\n${summarizeTask(s.currentTask)}` : s.cwd;
            return (
              <button
                key={s.id}
                onClick={() => selectAgent(s.id)}
                title={detailTitle}
                className={`group relative block h-[50px] w-full overflow-hidden rounded-xl px-2.5 py-2 text-left transition-colors ${
                  active ? "bg-primary/[0.12] ring-1 ring-primary/20" : "hover:bg-foreground/[0.04]"
                }`}
              >
                {active && (
                  <span className="absolute inset-y-1.5 left-0 w-0.5 rounded-full bg-primary" />
                )}
                <div className="flex items-center gap-2">
                  <span
                    className={`h-2 w-2 shrink-0 rounded-full ${
                      s.status === "running"
                        ? "pulse bg-success ring-2 ring-success/20"
                        : "bg-muted-foreground/30"
                    }`}
                  />
                  <span
                    className={`truncate text-[13.5px] ${active ? "font-semibold text-foreground" : "font-medium text-foreground/90"}`}
                  >
                    {s.name}
                  </span>
                </div>
                <div
                  className={`mt-0.5 truncate pl-4 ${
                    s.currentTask
                      ? "text-[11px] font-medium text-warning/90"
                      : "font-mono text-[10.5px] text-muted-foreground"
                  }`}
                >
                  {detail}
                </div>
              </button>
            );
          })}
          {agents.length === 0 && (
            <div className="px-3 py-6 text-center text-[12px] text-muted-foreground/50">
              No agents yet.
            </div>
          )}
        </div>

        {/* new agent — floating panel separated from the scrolling list */}
        <div className="border-t border-border/50 bg-sidebar/80 px-3 pb-5 pt-3 shadow-[0_-4px_12px_-8px_rgba(0,0,0,0.08)]">
          <button
            onClick={backupNow}
            disabled={backingUp}
            title={backupStatus?.dir || "Create a local CodexLoom backup"}
            className="mb-2 flex h-8 w-full items-center justify-center gap-2 rounded-xl bg-background text-[12.5px] font-medium text-muted-foreground ring-1 ring-border transition hover:text-foreground hover:ring-primary/30 disabled:cursor-not-allowed disabled:opacity-60"
          >
            <Archive className={`size-3.5 ${backingUp ? "animate-pulse" : ""}`} />
            {backingUp ? "Backing Up" : "Backup Now"}
          </button>
          <div className="mb-3 truncate px-1 text-center font-mono text-[10px] text-muted-foreground/65">
            {backupLabel(latestBackup)}
          </div>
          <button
            onClick={restartLoom}
            disabled={restarting || restartPending}
            title="Restart CodexLoom to load the already built version"
            className="mb-3 flex h-8 w-full items-center justify-center gap-2 rounded-xl bg-background text-[12.5px] font-medium text-muted-foreground ring-1 ring-border transition hover:text-foreground hover:ring-primary/30 disabled:cursor-not-allowed disabled:opacity-60"
          >
            <RotateCw className={`size-3.5 ${restarting || restartPending ? "animate-spin" : ""}`} />
            {restartState === "waiting" ? "Restart Waiting" : restartState === "restarting" ? "Restarting" : "Restart Loom"}
          </button>
          {restartState !== "idle" && (
            <div
              className={`mb-3 rounded-xl px-2.5 py-2 text-[11.5px] ${
                restartState === "failed"
                  ? "bg-destructive/10 text-destructive"
                  : "bg-warning/10 text-warning"
              }`}
            >
              <div className="font-medium">{restartStatus.message || restartState}</div>
              {restartStatus.running?.length > 0 && (
                <div className="mt-1 space-y-0.5 font-mono text-[10.5px] opacity-90">
                  {restartStatus.running.slice(0, 3).map((s: any) => (
                    <div key={s.id} className="truncate">
                      {s.name}
                    </div>
                  ))}
                  {restartStatus.running.length > 3 && <div>+{restartStatus.running.length - 3} more</div>}
                </div>
              )}
            </div>
          )}
          <div className="flex flex-col gap-2">
          <input
            value={newName}
            onChange={(e) => setNewName(e.target.value)}
            placeholder="agent name"
            spellCheck={false}
            className="h-8 rounded-xl bg-background px-2.5 text-[13px] outline-none ring-1 ring-border transition placeholder:text-muted-foreground/70 focus:ring-primary/40"
          />
          <input
            value={newCwd}
            onChange={(e) => setNewCwd(e.target.value)}
            placeholder="working directory"
            spellCheck={false}
            className="h-8 rounded-xl bg-background px-2.5 font-mono text-[12px] outline-none ring-1 ring-border transition placeholder:text-muted-foreground/70 focus:ring-primary/40"
          />
          <button
            onClick={create}
            className="rounded-xl bg-primary px-3 py-2 text-[13px] font-medium text-primary-foreground transition-colors hover:opacity-90"
          >
            + New agent
          </button>
          </div>
        </div>
      </aside>

      {/* mobile drawer toggle — floats over the main content on small screens */}
      <button
        onClick={() => setSidebarOpen(true)}
        aria-label="open agents"
        className="fixed left-3 top-3 z-20 flex size-9 items-center justify-center rounded-xl bg-card/90 shadow-card ring-1 ring-border/50 backdrop-blur md:hidden"
      >
        <Menu className="size-4" />
      </button>

      {/* main */}
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
          <div className="text-4xl opacity-25">◐</div>
          <h2 className="font-serif text-2xl tracking-tight text-foreground/80">CodexLoom</h2>
          <div className="text-sm text-muted-foreground/70">Select or create an agent to begin.</div>
        </div>
      )}

      {/* toast */}
      {toast && (
        <div className="fixed bottom-6 right-6 z-10 rounded-xl border border-destructive/30 bg-destructive/10 px-4 py-2.5 text-sm text-destructive shadow-card backdrop-blur">
          {toast}
        </div>
      )}
    </div>
  );
}
