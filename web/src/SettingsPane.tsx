import { Archive, Cable, Code2, DatabaseBackup, RadioTower, RotateCw, Settings2 } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { useEffect, useRef } from "react";
import { DesignPane } from "./DesignPane";
import { RemotePane } from "./RemotePane";
import { api, type BackupStatus, type PlatformConnection, type RemoteSnapshot } from "./types";
import { Button } from "./components/ui/button";

export type SettingsSection = "remote" | "connectors" | "recovery" | "system" | "developer";

const sections: Array<{ id: SettingsSection; label: string; icon: typeof Settings2 }> = [
  { id: "remote", label: "Codex & Remote", icon: RadioTower },
  { id: "connectors", label: "Connectors", icon: Cable },
  { id: "recovery", label: "Data & Recovery", icon: DatabaseBackup },
  { id: "system", label: "System", icon: Settings2 },
  { id: "developer", label: "Developer", icon: Code2 },
];

export function SettingsPane({ section, remote, backupStatus, backingUp, restarting, restartStatus, onSectionChange, onRemoteUpdated, onBackup, onRestart, onOpenExternal, onError }: {
  section: SettingsSection;
  remote: RemoteSnapshot | null;
  backupStatus: BackupStatus;
  backingUp: boolean;
  restarting: boolean;
  restartStatus: any;
  onSectionChange: (section: SettingsSection) => void;
  onRemoteUpdated: (remote: RemoteSnapshot) => void;
  onBackup: () => void;
  onRestart: () => void;
  onOpenExternal: () => void;
  onError: (message: string) => void;
}) {
  const sectionNavRef = useRef<HTMLElement>(null);

  useEffect(() => {
    const nav = sectionNavRef.current;
    const selected = nav?.querySelector<HTMLElement>(`[data-settings-section="${section}"]`);
    if (!nav || !selected || window.matchMedia("(min-width: 768px)").matches) return;
    nav.scrollTo({ left: selected.offsetLeft - (nav.clientWidth - selected.offsetWidth) / 2, behavior: "auto" });
  }, [section]);

  return (
    <main className="flex min-h-0 min-w-0 flex-1 flex-col bg-background">
      <header className="flex h-14 shrink-0 items-center gap-3 border-b border-border bg-card/80 pl-14 pr-3 md:px-5">
        <Settings2 className="size-4 text-primary" />
        <div className="min-w-0"><h1 className="text-[15px] font-semibold">Settings</h1><p className="hidden text-[10.5px] text-muted-foreground sm:block">Low-frequency CodexLoom operation and recovery.</p></div>
      </header>
      <div className="flex min-h-0 flex-1 flex-col md:flex-row">
        <nav ref={sectionNavRef} className="flex shrink-0 overflow-x-auto border-b border-border bg-sidebar/35 p-2 md:w-52 md:flex-col md:overflow-visible md:border-b-0 md:border-r" aria-label="Settings sections">
          {sections.map((item) => {
            const Icon = item.icon;
            return <Button key={item.id} data-settings-section={item.id} type="button" variant={section === item.id ? "secondary" : "ghost"} onClick={() => onSectionChange(item.id)} className={`h-8 shrink-0 justify-start gap-2 px-2.5 text-[11.5px] ${section === item.id ? "bg-selection text-selection-foreground" : "text-muted-foreground"}`}><Icon className="size-3.5" />{item.label}</Button>;
          })}
        </nav>
        <div className="flex min-h-0 min-w-0 flex-1 overflow-hidden">
          {section === "remote" ? <RemotePane remote={remote} onUpdated={onRemoteUpdated} onError={onError} embedded /> : null}
          {section === "connectors" ? <ConnectorSettings onOpenExternal={onOpenExternal} /> : null}
          {section === "recovery" ? <RecoverySettings status={backupStatus} backingUp={backingUp} onBackup={onBackup} /> : null}
          {section === "system" ? <SystemSettings restarting={restarting} restartStatus={restartStatus} onRestart={onRestart} /> : null}
          {section === "developer" ? <DesignPane embedded /> : null}
        </div>
      </div>
    </main>
  );
}

function ConnectorSettings({ onOpenExternal }: { onOpenExternal: () => void }) {
  const query = useQuery<{ connections: PlatformConnection[] }>({ queryKey: ["settings-connections"], queryFn: () => api("GET", "/api/integrations/connections"), refetchInterval: 30_000 });
  const connections = query.data?.connections?.filter((connection) => !connection.archivedAt) || [];
  return <SettingsBody title="Connector operation" description="Transport health and credentials support External roles; they are not the everyday identity model.">
    <div className="divide-y divide-border border-y border-border">
      {connections.map((connection) => <div key={connection.id} className="flex min-w-0 items-center gap-3 py-3"><span className={`size-2 rounded-full ${connection.status === "connected" ? "bg-success" : connection.status === "degraded" ? "bg-warning" : "bg-destructive"}`} /><span className="min-w-0 flex-1"><span className="block text-[12px] font-semibold capitalize">{connection.provider}</span><span className="block truncate font-mono text-[9.5px] text-muted-foreground">{connection.accountRef || connection.id}</span></span><span className="font-mono text-[9px] uppercase text-muted-foreground">{connection.status}</span></div>)}
      {!query.isLoading && connections.length === 0 ? <div className="py-8 text-center text-[11px] text-muted-foreground">No active connectors.</div> : null}
    </div>
    <div className="mt-4 flex justify-end"><Button variant="outline" onClick={onOpenExternal}><Cable />Manage external roles</Button></div>
  </SettingsBody>;
}

function RecoverySettings({ status, backingUp, onBackup }: { status: BackupStatus; backingUp: boolean; onBackup: () => void }) {
  const latest = status.backups?.[0];
  return <SettingsBody title="Data & Recovery" description="Create a durable local snapshot before risky changes and inspect the active retention policy.">
    <div className="flex items-center gap-3 border-y border-border py-4"><Archive className="size-4 text-primary" /><div className="min-w-0 flex-1"><div className="text-[12px] font-semibold">Local backup</div><div className="mt-0.5 truncate font-mono text-[9.5px] text-muted-foreground" title={status.dir}>{latest?.createdAt ? `Last ${new Date(latest.createdAt).toLocaleString()}` : "No backups"} · {status.count} snapshots · {formatStorage(status.totalBytes)}</div></div><Button variant="outline" onClick={onBackup} disabled={backingUp}>{backingUp ? "Backing up" : "Back up now"}</Button></div>
    <dl className="mt-5 grid gap-4 text-[10.5px] sm:grid-cols-3"><SettingFact label="Location" value={status.dir || "Unavailable"} /><SettingFact label="Retention" value={`${status.retention.minCount}-${status.retention.maxCount} snapshots`} /><SettingFact label="Maximum age" value={`${status.retention.maxAgeDays} days`} /></dl>
  </SettingsBody>;
}

function SystemSettings({ restarting, restartStatus, onRestart }: { restarting: boolean; restartStatus: any; onRestart: () => void }) {
  const state = restartStatus?.state || "ready";
  const pending = restarting || state === "waiting" || state === "restarting";
  return <SettingsBody title="System" description="Restart the built release through the supervised Loom reloader. Running work drains before the process changes.">
    <div className="flex items-center gap-3 border-y border-border py-4"><RotateCw className={`size-4 text-primary ${pending ? "animate-spin" : ""}`} /><div className="min-w-0 flex-1"><div className="text-[12px] font-semibold">Restart Loom</div><div className="mt-0.5 text-[10.5px] text-muted-foreground">{restartStatus?.message || "Ready. Existing active Turns will drain first."}</div></div><Button variant="outline" onClick={onRestart} disabled={pending}>{state === "waiting" ? "Waiting" : state === "restarting" ? "Restarting" : "Restart"}</Button></div>
    {restartStatus?.running?.length ? <div className="mt-4 border-l-2 border-warning bg-warning/5 px-3 py-2 text-[11px] text-warning">Waiting for {restartStatus.running.map((agent: any) => agent.name).join(", ")}</div> : null}
    {restartStatus?.operations?.length ? <div className="mt-3 border-l-2 border-warning bg-warning/5 px-3 py-2 text-[11px] text-warning">Waiting for {restartStatus.operations.map((operation: any) => `${operation.provider || "connector"} ${operation.kind}`).join(", ")}</div> : null}
    {restartStatus?.error ? <pre className="mt-4 overflow-auto border-l-2 border-destructive bg-destructive/5 px-3 py-2 font-mono text-[10px] text-destructive">{restartStatus.error}</pre> : null}
  </SettingsBody>;
}

function SettingsBody({ title, description, children }: { title: string; description: string; children: React.ReactNode }) {
  return <div className="min-h-0 flex-1 overflow-y-auto"><div className="mx-auto max-w-4xl px-4 py-6 md:px-8 md:py-8"><h2 className="text-[16px] font-semibold">{title}</h2><p className="mt-1 max-w-2xl text-[11.5px] leading-5 text-muted-foreground">{description}</p><div className="mt-6">{children}</div></div></div>;
}

function SettingFact({ label, value }: { label: string; value: string }) { return <div className="min-w-0"><dt className="uppercase text-muted-foreground">{label}</dt><dd className="mt-1 break-words font-mono text-[10px] text-foreground">{value}</dd></div>; }

function formatStorage(bytes: number) {
  if (!Number.isFinite(bytes) || bytes <= 0) return "0 B";
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  const power = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
  return `${(bytes / 1024 ** power).toFixed(power === 0 ? 0 : 1)} ${units[power]}`;
}
