import { CalendarClock, Pause, Play, Plus, RotateCw, Trash2, Zap } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { api, type Schedule, type Agent } from "./types";

interface Props {
  agents: Agent[];
  onError: (msg: string) => void;
  initialTo?: string;
}

type Mode = "cron" | "at";

export function SchedulesPane({ agents, onError, initialTo }: Props) {
  const [schedules, setSchedules] = useState<Schedule[]>([]);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [name, setName] = useState("");
  const [to, setTo] = useState("");
  const [subject, setSubject] = useState("");
  const [body, setBody] = useState("");
  const [response, setResponse] = useState<"required" | "none">("required");
  const [mode, setMode] = useState<Mode>("cron");
  const [cron, setCron] = useState("0 9 * * *");
  const [at, setAt] = useState("");
  const [timezone, setTimezone] = useState("Asia/Shanghai");

  const refresh = async () => {
    setLoading(true);
    try {
      const data = await api("GET", "/api/schedules");
      setSchedules(data.schedules || []);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    refresh().catch((err: any) => onError(err.message));
    const es = new EventSource("/api/events");
    es.onmessage = (e) => {
      try {
        const evt = JSON.parse(e.data);
        if (evt.type === "loom/schedule" || evt.type === "loom/schedule-deleted") {
          refresh().catch(() => {});
        }
      } catch {
        /* ignore */
      }
    };
    return () => es.close();
  }, []);

  useEffect(() => {
    if (!to && agents.length > 0) setTo(agents[0].name);
  }, [agents, to]);

  useEffect(() => {
    if (initialTo) setTo(initialTo);
  }, [initialTo]);

  const enabledCount = schedules.filter((s) => s.enabled).length;
  const dueSoon = useMemo(() => {
    const now = Date.now();
    return schedules.filter((s) => {
      if (!s.enabled || !s.nextRunAt) return false;
      const t = new Date(s.nextRunAt).getTime();
      return Number.isFinite(t) && t >= now && t - now <= 24 * 60 * 60 * 1000;
    }).length;
  }, [schedules]);

  const resetForm = () => {
    setName("");
    setSubject("");
    setBody("");
    setResponse("required");
    setMode("cron");
    setCron("0 9 * * *");
    setAt("");
    setTimezone("Asia/Shanghai");
  };

  const create = async () => {
    if (saving) return;
    if (!name.trim() || !to.trim() || !subject.trim() || !body.trim()) {
      onError("name, target, subject and body required");
      return;
    }
    if (mode === "cron" && !cron.trim()) {
      onError("cron is required");
      return;
    }
    if (mode === "at" && !at.trim()) {
      onError("run time is required");
      return;
    }
    setSaving(true);
    try {
      await api("POST", "/api/schedules", {
        name: name.trim(),
        to: to.trim(),
        subject: subject.trim(),
        body,
        response,
        cron: mode === "cron" ? cron.trim() : "",
        at: mode === "at" ? normalizeLocalAt(at) : "",
        timezone: timezone.trim() || "Asia/Shanghai",
      });
      resetForm();
      await refresh();
    } catch (err: any) {
      onError(err.message);
    } finally {
      setSaving(false);
    }
  };

  const action = async (schedule: Schedule, kind: "run" | "enable" | "disable" | "delete") => {
    try {
      if (kind === "delete") {
        await api("DELETE", `/api/schedules/${encodeURIComponent(schedule.id)}`);
      } else {
        await api("POST", `/api/schedules/${encodeURIComponent(schedule.id)}/${kind}`);
      }
      await refresh();
    } catch (err: any) {
      onError(err.message);
    }
  };

  const fmt = (ts?: string) => {
    if (!ts) return "-";
    const d = new Date(ts);
    if (Number.isNaN(d.getTime())) return ts;
    return d.toLocaleString([], {
      month: "short",
      day: "2-digit",
      hour: "2-digit",
      minute: "2-digit",
    });
  };

  const triggerLabel = (s: Schedule) => s.cron || s.at || "-";

  return (
    <main className="flex min-w-0 flex-1 flex-col bg-background">
      <header className="flex h-14 shrink-0 items-center justify-between border-b border-border bg-card/80 pl-14 pr-3 backdrop-blur md:px-5">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <CalendarClock className="size-4 text-primary" />
            <h1 className="truncate font-serif text-xl tracking-tight">Schedules</h1>
          </div>
          <div className="mt-0.5 font-mono text-[10px] uppercase tracking-[0.16em] text-muted-foreground">
            scheduler messages
          </div>
        </div>
        <div className="hidden items-center gap-2 font-mono text-[11px] text-muted-foreground sm:flex">
          <span className="rounded-lg bg-muted px-2 py-1">{schedules.length} total</span>
          <span className="rounded-lg bg-success/10 px-2 py-1 text-success">{enabledCount} enabled</span>
          <span className="rounded-lg bg-warning/10 px-2 py-1 text-warning">{dueSoon} next 24h</span>
        </div>
      </header>

      <div className="grid min-h-0 flex-1 grid-cols-1 overflow-hidden lg:grid-cols-[380px_1fr]">
        <section className="border-b border-border bg-card/45 p-4 lg:border-b-0 lg:border-r">
          <div className="space-y-3">
            <div className="grid grid-cols-2 gap-2">
              <label className="space-y-1">
                <span className="text-[11px] font-medium text-muted-foreground">Name</span>
                <input
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder="daily-check"
                  spellCheck={false}
                  className="h-9 w-full rounded-lg bg-background px-2.5 font-mono text-[12.5px] outline-none ring-1 ring-border focus:ring-ring/25"
                />
              </label>
              <label className="space-y-1">
                <span className="text-[11px] font-medium text-muted-foreground">Target</span>
                <select
                  value={to}
                  onChange={(e) => setTo(e.target.value)}
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
            </div>

            <label className="block space-y-1">
              <span className="text-[11px] font-medium text-muted-foreground">Subject</span>
              <input
                value={subject}
                onChange={(e) => setSubject(e.target.value)}
                className="h-9 w-full rounded-lg bg-background px-2.5 text-[13px] outline-none ring-1 ring-border focus:ring-ring/25"
              />
            </label>

            <div className="grid grid-cols-2 gap-2">
              <label className="space-y-1">
                <span className="text-[11px] font-medium text-muted-foreground">Mode</span>
                <select
                  value={mode}
                  onChange={(e) => setMode(e.target.value as Mode)}
                  className="h-9 w-full rounded-lg bg-background px-2 text-[13px] outline-none ring-1 ring-border focus:ring-ring/25"
                >
                  <option value="cron">cron</option>
                  <option value="at">once</option>
                </select>
              </label>
              <label className="space-y-1">
                <span className="text-[11px] font-medium text-muted-foreground">Response</span>
                <select
                  value={response}
                  onChange={(e) => setResponse(e.target.value as "required" | "none")}
                  className="h-9 w-full rounded-lg bg-background px-2 text-[13px] outline-none ring-1 ring-border focus:ring-ring/25"
                >
                  <option value="required">required</option>
                  <option value="none">none</option>
                </select>
              </label>
            </div>

            {mode === "cron" ? (
              <div className="grid grid-cols-[1fr_130px] gap-2">
                <label className="space-y-1">
                  <span className="text-[11px] font-medium text-muted-foreground">Cron</span>
                  <input
                    value={cron}
                    onChange={(e) => setCron(e.target.value)}
                    spellCheck={false}
                    className="h-9 w-full rounded-lg bg-background px-2.5 font-mono text-[12.5px] outline-none ring-1 ring-border focus:ring-ring/25"
                  />
                </label>
                <label className="space-y-1">
                  <span className="text-[11px] font-medium text-muted-foreground">Timezone</span>
                  <input
                    value={timezone}
                    onChange={(e) => setTimezone(e.target.value)}
                    spellCheck={false}
                    className="h-9 w-full rounded-lg bg-background px-2.5 font-mono text-[12.5px] outline-none ring-1 ring-border focus:ring-ring/25"
                  />
                </label>
              </div>
            ) : (
              <label className="block space-y-1">
                <span className="text-[11px] font-medium text-muted-foreground">Run once at</span>
                <input
                  type="datetime-local"
                  value={at}
                  onChange={(e) => setAt(e.target.value)}
                  className="h-9 w-full rounded-lg bg-background px-2.5 font-mono text-[12.5px] outline-none ring-1 ring-border focus:ring-ring/25"
                />
              </label>
            )}

            <label className="block space-y-1">
              <span className="text-[11px] font-medium text-muted-foreground">Body</span>
              <textarea
                value={body}
                onChange={(e) => setBody(e.target.value)}
                className="min-h-44 w-full resize-none rounded-lg bg-background px-3 py-2 font-mono text-[12.5px] outline-none ring-1 ring-border focus:ring-ring/25"
              />
            </label>

            <button
              onClick={create}
              disabled={saving}
              className="flex h-9 w-full items-center justify-center gap-2 rounded-lg bg-primary px-3 text-[13px] font-medium text-primary-foreground transition hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-60"
            >
              <Plus className="size-3.5" />
              {saving ? "Creating" : "Create schedule"}
            </button>
          </div>
        </section>

        <section className="min-h-0 overflow-y-auto p-4">
          <div className="mx-auto max-w-5xl space-y-3">
            <div className="flex items-center justify-between">
              <div className="text-[12px] text-muted-foreground">
                {loading ? "Loading schedules..." : `${schedules.length} schedule${schedules.length === 1 ? "" : "s"}`}
              </div>
              <button
                onClick={() => refresh().catch((err: any) => onError(err.message))}
                className="flex h-8 items-center gap-1.5 rounded-lg border border-border bg-background px-2.5 text-[12px] font-medium text-muted-foreground hover:text-foreground"
              >
                <RotateCw className="size-3.5" />
                Refresh
              </button>
            </div>

            {schedules.map((s) => (
              <article key={s.id} className="rounded-lg border border-border bg-card p-4 shadow-card">
                <div className="flex flex-wrap items-start justify-between gap-3">
                  <div className="min-w-0">
                    <div className="flex items-center gap-2">
                      <span className={`h-2 w-2 rounded-full ${s.enabled ? "bg-success" : "bg-muted-foreground/40"}`} />
                      <div className="truncate text-[15px] font-semibold text-foreground">{s.name}</div>
                    </div>
                    <div className="mt-1 font-mono text-[11px] text-muted-foreground">
                      scheduler -&gt; {s.to} · {s.id}
                    </div>
                  </div>
                  <div className="flex flex-wrap items-center gap-2">
                    <span className={`rounded-md px-2 py-1 text-[11px] font-medium ${s.enabled ? "bg-success/10 text-success" : "bg-muted text-muted-foreground"}`}>
                      {s.enabled ? "enabled" : "disabled"}
                    </span>
                    <span className="rounded-md bg-muted px-2 py-1 font-mono text-[11px] text-muted-foreground">
                      {s.response}
                    </span>
                  </div>
                </div>

                <div className="mt-3 grid gap-2 text-[12px] sm:grid-cols-3">
                  <div className="rounded-lg bg-muted/35 px-3 py-2">
                    <div className="text-muted-foreground">Trigger</div>
                    <div className="mt-1 truncate font-mono text-foreground" title={triggerLabel(s)}>
                      {triggerLabel(s)}
                    </div>
                  </div>
                  <div className="rounded-lg bg-muted/35 px-3 py-2">
                    <div className="text-muted-foreground">Next</div>
                    <div className="mt-1 font-mono text-foreground">{fmt(s.nextRunAt)}</div>
                  </div>
                  <div className="rounded-lg bg-muted/35 px-3 py-2">
                    <div className="text-muted-foreground">Last</div>
                    <div className="mt-1 truncate font-mono text-foreground">
                      {s.lastMessageId || fmt(s.lastRunAt)}
                    </div>
                  </div>
                </div>

                <div className="mt-3 text-[13px] font-medium text-foreground">{s.subject}</div>
                <pre className="mt-2 max-h-52 overflow-auto whitespace-pre-wrap rounded-lg bg-muted/25 px-3 py-2 font-mono text-[12.5px] text-foreground/85">
                  {s.body}
                </pre>
                {s.lastError && (
                  <div className="mt-2 rounded-lg bg-destructive/10 px-3 py-2 font-mono text-[11px] text-destructive">
                    {s.lastError}
                  </div>
                )}

                <div className="mt-3 flex flex-wrap items-center justify-between gap-2">
                  <div className="font-mono text-[10.5px] text-muted-foreground">
                    timezone {s.timezone || "-"} · updated {fmt(s.updatedAt)}
                  </div>
                  <div className="flex flex-wrap items-center gap-2">
                    <button
                      onClick={() => action(s, "run")}
                      className="flex h-8 items-center gap-1.5 rounded-lg border border-border bg-background px-2.5 text-[12px] font-medium text-muted-foreground hover:text-foreground"
                    >
                      <Zap className="size-3.5" />
                      Run
                    </button>
                    <button
                      onClick={() => action(s, s.enabled ? "disable" : "enable")}
                      className="flex h-8 items-center gap-1.5 rounded-lg border border-border bg-background px-2.5 text-[12px] font-medium text-muted-foreground hover:text-foreground"
                    >
                      {s.enabled ? <Pause className="size-3.5" /> : <Play className="size-3.5" />}
                      {s.enabled ? "Disable" : "Enable"}
                    </button>
                    <button
                      onClick={() => action(s, "delete")}
                      className="flex h-8 items-center gap-1.5 rounded-lg border border-border bg-background px-2.5 text-[12px] font-medium text-destructive hover:bg-destructive/10"
                    >
                      <Trash2 className="size-3.5" />
                      Delete
                    </button>
                  </div>
                </div>
              </article>
            ))}

            {schedules.length === 0 && (
              <div className="rounded-lg border border-dashed border-border bg-card/50 px-4 py-10 text-center text-sm text-muted-foreground">
                No schedules yet.
              </div>
            )}
          </div>
        </section>
      </div>
    </main>
  );
}

function normalizeLocalAt(value: string) {
  if (!value) return "";
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return value;
  return d.toISOString();
}
