import { useEffect, useRef, useState } from "react";
import { api, type Session } from "./types";
import { SessionPane } from "./SessionPane";

export default function App() {
  const [sessions, setSessions] = useState<Session[]>([]);
  const [current, setCurrent] = useState<string | null>(null);
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
    <div className="flex h-full overflow-hidden text-[14px]">
      {/* sidebar */}
      <div className="flex w-[260px] shrink-0 flex-col border-r border-line bg-panel">
        <h1 className="px-4 pb-2.5 pt-3.5 text-[15px] font-semibold tracking-wide">
          <span className="text-accent">◉</span> codex-hub
        </h1>
        <div className="flex-1 overflow-y-auto">
          {sessions.map((s) => (
            <div
              key={s.id}
              onClick={() => setCurrent(s.id)}
              className={`cursor-pointer border-l-[3px] px-3.5 py-2.5 hover:bg-panel2 ${
                s.id === current ? "border-accent bg-panel2" : "border-transparent"
              }`}
            >
              <div className="flex items-center gap-2 font-semibold">
                <span
                  className={`h-2 w-2 shrink-0 rounded-full ${
                    s.status === "running" ? "pulse bg-warn" : s.status === "idle" ? "bg-ok" : "bg-dim"
                  }`}
                />
                {s.name}
              </div>
              <div className="mt-0.5 truncate font-mono text-[11.5px] text-dim">{s.cwd}</div>
              {s.currentTask && (
                <div className="mt-0.5 truncate text-[11.5px] text-warn">{s.currentTask}</div>
              )}
            </div>
          ))}
        </div>
        <div className="flex flex-col gap-2 border-t border-line p-3">
          <input
            value={newName}
            onChange={(e) => setNewName(e.target.value)}
            placeholder="session name"
            spellCheck={false}
            className="rounded-md border border-line bg-bg px-2.5 py-1.5 text-[13px] outline-none focus:border-accent"
          />
          <input
            value={newCwd}
            onChange={(e) => setNewCwd(e.target.value)}
            placeholder="working directory"
            spellCheck={false}
            className="rounded-md border border-line bg-bg px-2.5 py-1.5 font-mono text-[12.5px] outline-none focus:border-accent"
          />
          <button
            onClick={create}
            className="rounded-md border border-[#1f6feb] bg-[#1f6feb] px-3 py-1.5 text-[13px] font-medium"
          >
            new session
          </button>
        </div>
      </div>

      {/* main */}
      {selected ? (
        <SessionPane
          key={selected.id}
          session={selected}
          onKilled={() => setCurrent(null)}
          onError={showToast}
        />
      ) : (
        <div className="flex flex-1 flex-col items-center justify-center gap-2 text-dim">
          <div className="text-3xl">◉</div>
          <div>select or create a session</div>
        </div>
      )}

      {/* toast */}
      {toast && (
        <div className="fixed bottom-20 right-5 z-10 rounded-lg border border-err bg-[#3d1418] px-4 py-2.5 text-err">
          {toast}
        </div>
      )}
    </div>
  );
}
