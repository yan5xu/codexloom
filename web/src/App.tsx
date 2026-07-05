import { Menu } from "lucide-react";
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
                  ? { ...s, status: d.status, currentTask: d.currentTask || "", lastError: d.lastError || "" }
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

  const selected = sessions.find((s) => s.id === current) || null;

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

        <div className="flex items-center justify-between px-4 pb-1 pt-3">
          <span className="text-[9px] font-bold uppercase tracking-[0.15em] text-muted-foreground">
            Threads
          </span>
          <span className="font-mono text-[10px] text-muted-foreground/50">{sessions.length}</span>
        </div>

        <div className="flex-1 space-y-0.5 overflow-y-auto px-2 pb-2">
          {sessions.map((s) => {
            const active = s.id === current;
            return (
              <button
                key={s.id}
                onClick={() => {
                  setCurrent(s.id);
                  setSidebarOpen(false);
                }}
                className={`group relative block w-full overflow-hidden rounded-xl px-2.5 py-2 text-left transition-colors ${
                  active ? "bg-primary/[0.07]" : "hover:bg-foreground/[0.03]"
                }`}
              >
                {active && (
                  <span className="absolute inset-y-1.5 left-0 w-0.5 rounded-full bg-primary" />
                )}
                <div className="flex items-center gap-2">
                  <span
                    className={`h-2 w-2 shrink-0 rounded-full ${
                      s.status === "running"
                        ? "pulse bg-warning"
                        : s.status === "idle"
                          ? "bg-success"
                          : "bg-muted-foreground/40"
                    }`}
                  />
                  <span
                    className={`truncate text-[13.5px] ${active ? "font-semibold text-foreground" : "font-medium text-foreground/90"}`}
                  >
                    {s.name}
                  </span>
                </div>
                <div className="mt-0.5 truncate pl-4 font-mono text-[11px] text-muted-foreground/70">{s.cwd}</div>
                {s.currentTask && (
                  <div className="mt-0.5 truncate pl-4 text-[11px] text-warning/90">{s.currentTask}</div>
                )}
              </button>
            );
          })}
          {sessions.length === 0 && (
            <div className="px-3 py-6 text-center text-[12px] text-muted-foreground/50">
              No sessions yet.
            </div>
          )}
        </div>

        {/* new session */}
        <div className="mx-3 h-px bg-border/40" />
        <div className="flex flex-col gap-2 p-3">
          <input
            value={newName}
            onChange={(e) => setNewName(e.target.value)}
            placeholder="session name"
            spellCheck={false}
            className="h-8 rounded-xl bg-background px-2.5 text-[13px] outline-none ring-1 ring-border/60 transition focus:ring-primary/40"
          />
          <input
            value={newCwd}
            onChange={(e) => setNewCwd(e.target.value)}
            placeholder="working directory"
            spellCheck={false}
            className="h-8 rounded-xl bg-background px-2.5 font-mono text-[12px] outline-none ring-1 ring-border/60 transition focus:ring-primary/40"
          />
          <button
            onClick={create}
            className="rounded-xl bg-primary px-3 py-2 text-[13px] font-medium text-primary-foreground transition-colors hover:opacity-90"
          >
            + New session
          </button>
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
