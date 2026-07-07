import { Menu, RotateCw } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { api, type Session } from "./types";
import { SessionPane } from "./SessionPane";

export default function App() {
  const [sessions, setSessions] = useState<Session[]>([]);
  const [current, setCurrent] = useState<string | null>(null);
  const [sidebarOpen, setSidebarOpen] = useState(false); // mobile drawer
  const [newName, setNewName] = useState("");
  const [newCwd, setNewCwd] = useState("");
  const [toast, setToast] = useState<string | null>(null);
  const [restarting, setRestarting] = useState(false);
  const [restartStatus, setRestartStatus] = useState<any>({ state: "idle" });
  const toastTimer = useRef<ReturnType<typeof setTimeout>>(null);

  const showToast = (msg: string) => {
    setToast(msg);
    if (toastTimer.current) clearTimeout(toastTimer.current);
    toastTimer.current = setTimeout(() => setToast(null), 4000);
  };

  const refresh = async () => {
    try {
      const data = await api("GET", "/api/sessions");
      setSessions(data.sessions);
    } catch {
      /* hub unreachable; global SSE will retry */
    }
  };

  // Hub-level live status stream (also delivers the initial snapshot).
  useEffect(() => {
    const es = new EventSource("/api/events");
    es.onmessage = (e) => {
      try {
        const evt = JSON.parse(e.data);
        if (evt.type === "hub/sessions") {
          setSessions(evt.data.sessions);
        } else if (evt.type === "hub/restart-status") {
          setRestartStatus(evt.data.restart || { state: "idle" });
        } else if (evt.type === "hub/session-status") {
          const d = evt.data;
          if (d.status === "killed") {
            setSessions((prev) => prev.filter((s) => s.id !== d.id));
            setCurrent((cur) => (cur === d.id ? null : cur));
          } else {
            setSessions((prev) => {
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
      const data = await api("POST", "/api/sessions", { name: newName.trim(), cwd: newCwd.trim() });
      setNewName("");
      setNewCwd("");
      await refresh();
      setCurrent(data.session.id);
    } catch (err: any) {
      showToast(err.message);
    }
  };

  const restartHub = async () => {
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

  // Deep-link: on first session load, honor #<id|name> in the URL so a session
  // view is directly linkable (and headless-screenshot-able).
  const hashApplied = useRef(false);
  useEffect(() => {
    if (hashApplied.current || sessions.length === 0) return;
    const h = decodeURIComponent(window.location.hash.slice(1));
    if (h) {
      const s = sessions.find((x) => x.id === h || x.name === h);
      if (s) setCurrent(s.id);
    }
    hashApplied.current = true;
  }, [sessions]);

  const selectSession = (id: string) => {
    setCurrent(id);
    setSidebarOpen(false);
    const s = sessions.find((x) => x.id === id);
    if (s) window.location.hash = encodeURIComponent(s.name);
  };

  const updateSession = (updated: Session) => {
    setSessions((prev) => prev.map((s) => (s.id === updated.id ? updated : s)));
    if (updated.id === current) {
      window.location.hash = encodeURIComponent(updated.name);
    }
  };

  const selected = sessions.find((s) => s.id === current) || null;
  const restartState = restartStatus?.state || "idle";
  const restartPending = restartState === "waiting" || restartState === "restarting";
  const activeCount = sessions.filter((s) => s.status === "running").length;
  const idleCount = sessions.filter((s) => s.status === "idle").length;

  useEffect(() => {
    if (!selected) return;
    const nextHash = "#" + encodeURIComponent(selected.name);
    if (window.location.hash !== nextHash) {
      window.history.replaceState(null, "", nextHash);
    }
  }, [selected?.id, selected?.name]);

  useEffect(() => {
    if (restartState === "waiting") {
      document.title = "Restart waiting · codex-hub";
    } else if (restartState === "restarting") {
      document.title = "Restarting · codex-hub";
    } else if (selected) {
      const marker = selected.status === "running" ? "● " : selected.lastError ? "! " : "";
      document.title = `${marker}${selected.name} · codex-hub`;
    } else if (activeCount > 0) {
      document.title = `(${activeCount}) codex-hub`;
    } else {
      document.title = "codex-hub";
    }
  }, [activeCount, restartState, selected]);

  // Middle-truncate long paths so the trailing folder (what distinguishes
  // same-named projects) stays visible.
  const midPath = (p: string) => {
    if (p.length <= 34) return p;
    return p.slice(0, 14) + "…" + p.slice(-18);
  };

  const clipTask = (task: string) => {
    if (task.length <= 46) return task;
    return task.slice(0, 43) + "…";
  };

  return (
    <div className="flex h-screen overflow-hidden">
      {/* backdrop — only on mobile when the drawer is open */}
      {sidebarOpen && (
        <div
          className="fixed inset-0 z-30 bg-black/40 md:hidden"
          onClick={() => setSidebarOpen(false)}
        />
      )}
      {/* sidebar — static column on md+, slide-in drawer on mobile */}
      <aside
        className={`fixed inset-y-0 left-0 z-40 flex w-[272px] shrink-0 transform flex-col bg-sidebar shadow-xl transition-transform duration-200 md:static md:z-auto md:translate-x-0 md:bg-sidebar/60 md:shadow-none ${
          sidebarOpen ? "translate-x-0" : "-translate-x-full"
        }`}
      >
        <div className="px-4 pb-2 pt-4">
          <p className="font-mono text-[9px] uppercase tracking-[0.2em] text-muted-foreground/70">codex · hub</p>
          <h2 className="mt-0.5 font-serif text-xl leading-tight tracking-tight">Sessions</h2>
        </div>

        <div className="mx-3 h-px bg-border/40" />

        <div className="mx-3 mt-3 grid grid-cols-3 overflow-hidden rounded-lg border border-border/60 bg-background/65">
          <div className="px-2 py-1.5">
            <div className="font-mono text-[10px] font-semibold leading-none text-foreground">
              {sessions.length}
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
            Threads
          </span>
          <span className="font-mono text-[10px] text-muted-foreground/50">{sessions.length}</span>
        </div>

        <div className="flex-1 space-y-0.5 overflow-y-auto px-2 pb-2">
          {sessions.map((s) => {
            const active = s.id === current;
            const detail = s.currentTask ? clipTask(s.currentTask) : midPath(s.cwd);
            const detailTitle = s.currentTask ? `${s.cwd}\n${s.currentTask}` : s.cwd;
            return (
              <button
                key={s.id}
                onClick={() => selectSession(s.id)}
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
          {sessions.length === 0 && (
            <div className="px-3 py-6 text-center text-[12px] text-muted-foreground/50">
              No sessions yet.
            </div>
          )}
        </div>

        {/* new session — floating panel separated from the scrolling list */}
        <div className="border-t border-border/50 bg-sidebar/80 px-3 pb-5 pt-3 shadow-[0_-4px_12px_-8px_rgba(0,0,0,0.08)]">
          <button
            onClick={restartHub}
            disabled={restarting || restartPending}
            title="Restart Hub to load the already built version"
            className="mb-3 flex h-8 w-full items-center justify-center gap-2 rounded-xl bg-background text-[12.5px] font-medium text-muted-foreground ring-1 ring-border transition hover:text-foreground hover:ring-primary/30 disabled:cursor-not-allowed disabled:opacity-60"
          >
            <RotateCw className={`size-3.5 ${restarting || restartPending ? "animate-spin" : ""}`} />
            {restartState === "waiting" ? "Restart Waiting" : restartState === "restarting" ? "Restarting" : "Restart Hub"}
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
            placeholder="session name"
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
            + New session
          </button>
          </div>
        </div>
      </aside>

      {/* mobile drawer toggle — floats over the main content on small screens */}
      <button
        onClick={() => setSidebarOpen(true)}
        aria-label="open sessions"
        className="fixed left-3 top-3 z-20 flex size-9 items-center justify-center rounded-xl bg-card/90 shadow-card ring-1 ring-border/50 backdrop-blur md:hidden"
      >
        <Menu className="size-4" />
      </button>

      {/* main */}
      {selected ? (
        <SessionPane
          key={selected.id}
          session={selected}
          onSessionUpdated={updateSession}
          onKilled={() => setCurrent(null)}
          onError={showToast}
        />
      ) : (
        <div className="flex flex-1 flex-col items-center justify-center gap-3 bg-background text-muted-foreground">
          <div className="text-4xl opacity-25">◐</div>
          <h2 className="font-serif text-2xl tracking-tight text-foreground/80">codex-hub</h2>
          <div className="text-sm text-muted-foreground/70">Select or create a session to begin.</div>
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
