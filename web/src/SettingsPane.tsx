import { Archive, Cable, Check, Code2, Copy, Cpu, DatabaseBackup, ExternalLink, GitBranch, KeyRound, Loader2, Plus, RadioTower, RotateCw, Save, Settings2, ShieldCheck, Trash2 } from "lucide-react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { DesignPane } from "./DesignPane";
import { RemotePane } from "./RemotePane";
import { api, type BackupStatus, type GitHubDeviceFlow, type ModelProvider, type PlatformConnection, type RemoteSnapshot } from "./types";
import { Button } from "./components/ui/button";
import { Input } from "./components/ui/input";

export type SettingsSection = "remote" | "providers" | "connectors" | "recovery" | "system" | "developer";

const sections: Array<{ id: SettingsSection; label: string; icon: typeof Settings2 }> = [
  { id: "remote", label: "Codex & Remote", icon: RadioTower },
  { id: "providers", label: "Model Providers", icon: Cpu },
  { id: "connectors", label: "Connectors", icon: Cable },
  { id: "recovery", label: "Data & Recovery", icon: DatabaseBackup },
  { id: "system", label: "System", icon: Settings2 },
  { id: "developer", label: "Developer", icon: Code2 },
];

const maxGitHubDevicePollFailures = 3;

export function githubDevicePollStateAfterFailure(consecutiveFailures: number): GitHubDeviceFlow["status"] {
  return consecutiveFailures >= maxGitHubDevicePollFailures ? "failed" : "pending";
}

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
          {section === "providers" ? <ProviderSettings onError={onError} /> : null}
          {section === "connectors" ? <ConnectorSettings onOpenExternal={onOpenExternal} onError={onError} /> : null}
          {section === "recovery" ? <RecoverySettings status={backupStatus} backingUp={backingUp} onBackup={onBackup} /> : null}
          {section === "system" ? <SystemSettings restarting={restarting} restartStatus={restartStatus} onRestart={onRestart} /> : null}
          {section === "developer" ? <DesignPane embedded /> : null}
        </div>
      </div>
    </main>
  );
}

function ProviderSettings({ onError }: { onError: (message: string) => void }) {
  const queryClient = useQueryClient();
  const query = useQuery<{ providers: ModelProvider[] }>({ queryKey: ["model-providers"], queryFn: () => api("GET", "/api/model-providers") });
  const providers = query.data?.providers || [];
  const [editing, setEditing] = useState(false);
  const [providerId, setProviderId] = useState("deepseek");
  const [name, setName] = useState("DeepSeek");
  const [baseUrl, setBaseUrl] = useState("https://api.deepseek.com");
  const [wireApi, setWireApi] = useState("responses");
  const [apiKey, setApiKey] = useState("");
  const [envKey, setEnvKey] = useState("");
  const [working, setWorking] = useState(false);
  const [verification, setVerification] = useState<Record<string, string> | null>(null);

  const editProvider = (provider?: ModelProvider) => {
    if (provider) {
      setProviderId(provider.id);
      setName(provider.name);
      setBaseUrl(provider.baseUrl || "");
      setWireApi(provider.wireApi || "responses");
    } else {
      setProviderId("deepseek");
      setName("DeepSeek");
      setBaseUrl("https://api.deepseek.com");
      setWireApi("responses");
    }
    setApiKey("");
    setEnvKey("");
    setVerification(null);
    setEditing(true);
  };

  const save = async () => {
    if (!providerId.trim() || !name.trim() || !baseUrl.trim() || !wireApi.trim()) return;
    setWorking(true);
    try {
      await api("PUT", `/api/model-providers/${encodeURIComponent(providerId.trim())}`, {
        name: name.trim(), baseUrl: baseUrl.trim(), wireApi: wireApi.trim(),
        apiKey: apiKey.trim(), envKey: envKey.trim(),
      });
      setApiKey("");
      setEnvKey("");
      setEditing(false);
      await queryClient.invalidateQueries({ queryKey: ["model-providers"] });
    } catch (error) {
      onError(error instanceof Error ? error.message : String(error));
    } finally {
      setWorking(false);
    }
  };

  const verify = async (id: string) => {
    setWorking(true);
    try {
      const data = await api("POST", `/api/model-providers/${encodeURIComponent(id)}/verify`, {}) as { verification: Record<string, string> };
      setVerification(data.verification);
      setProviderId(id);
    } catch (error) {
      onError(error instanceof Error ? error.message : String(error));
    } finally {
      setWorking(false);
    }
  };

  const disable = async (id: string) => {
    setWorking(true);
    try {
      await api("DELETE", `/api/model-providers/${encodeURIComponent(id)}`);
      await queryClient.invalidateQueries({ queryKey: ["model-providers"] });
    } catch (error) {
      onError(error instanceof Error ? error.message : String(error));
    } finally {
      setWorking(false);
    }
  };

  return <SettingsBody title="Model Providers" description="Provider definitions and credentials are written to the active Codex configuration.">
    <div className="flex items-center justify-between border-y border-border py-3">
      <div className="flex items-center gap-2"><Cpu className="size-4 text-primary" /><span className="text-[12px] font-semibold">Configured providers</span></div>
      <Button size="sm" onClick={() => editProvider()}><Plus />Add provider</Button>
    </div>
    <div className="divide-y divide-border border-b border-border">
      {providers.map((provider) => <div key={provider.id} className="flex min-w-0 items-center gap-3 py-3">
        <span className={`size-2 shrink-0 rounded-full ${provider.credentialConfigured ? "bg-success" : "bg-warning"}`} />
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2"><span className="truncate text-[11.5px] font-semibold">{provider.name}</span>{provider.publicBeta ? <span className="rounded-sm bg-warning/10 px-1.5 py-0.5 font-mono text-[8px] uppercase text-warning">beta</span> : null}</div>
          <div className="mt-0.5 truncate font-mono text-[9.5px] text-muted-foreground">{provider.id} · {provider.wireApi || "Codex default"} · {provider.credentialSource}</div>
        </div>
        {provider.boundAgentCount > 0 ? <span className="font-mono text-[9px] text-muted-foreground">{provider.boundAgentCount} Agent{provider.boundAgentCount === 1 ? "" : "s"}</span> : null}
        <Button variant="outline" size="icon" onClick={() => void verify(provider.id)} disabled={working} title={`Verify ${provider.name}`} aria-label={`Verify ${provider.name}`}><ShieldCheck /></Button>
        {provider.source === "custom" ? <Button variant="outline" size="icon" onClick={() => editProvider(provider)} disabled={working} title={`Edit ${provider.name}`} aria-label={`Edit ${provider.name}`}><Settings2 /></Button> : null}
        {provider.source === "custom" ? <Button variant="outline" size="icon" onClick={() => void disable(provider.id)} disabled={working || provider.boundAgentCount > 0} title={`Disable ${provider.name}`} aria-label={`Disable ${provider.name}`}><Trash2 /></Button> : null}
      </div>)}
      {!query.isLoading && providers.length === 0 ? <div className="py-7 text-center text-[11px] text-muted-foreground">No Provider configuration available.</div> : null}
    </div>
    {verification && verification.providerId === providerId ? <dl className="mt-4 grid gap-3 border-l-2 border-primary bg-muted/20 px-3 py-2 text-[10px] sm:grid-cols-3"><SettingFact label="Config" value={verification.config} /><SettingFact label="Credential" value={verification.authentication} /><SettingFact label="Request" value={verification.minimalRequest} /></dl> : null}
    {editing ? <form className="mt-6 border-t border-border pt-5" onSubmit={(event) => { event.preventDefault(); void save(); }}>
      <div className="grid gap-3 sm:grid-cols-2">
        <label className="text-[10.5px] font-medium text-muted-foreground">Provider ID<Input value={providerId} onChange={(event) => setProviderId(event.target.value)} disabled={providers.some((provider) => provider.id === providerId)} spellCheck={false} className="mt-1 font-mono text-[11px]" /></label>
        <label className="text-[10.5px] font-medium text-muted-foreground">Name<Input value={name} onChange={(event) => setName(event.target.value)} spellCheck={false} className="mt-1" /></label>
        <label className="text-[10.5px] font-medium text-muted-foreground">Base URL<Input value={baseUrl} onChange={(event) => setBaseUrl(event.target.value)} spellCheck={false} className="mt-1 font-mono text-[11px]" /></label>
        <label className="text-[10.5px] font-medium text-muted-foreground">Wire API<Input value={wireApi} onChange={(event) => setWireApi(event.target.value)} spellCheck={false} className="mt-1 font-mono text-[11px]" /></label>
        <label className="text-[10.5px] font-medium text-muted-foreground">API key<Input value={apiKey} onChange={(event) => { setApiKey(event.target.value); if (event.target.value) setEnvKey(""); }} type="password" autoComplete="new-password" spellCheck={false} placeholder="Stored in Codex TOML" className="mt-1 font-mono text-[11px]" /></label>
        <label className="text-[10.5px] font-medium text-muted-foreground">Environment key<Input value={envKey} onChange={(event) => { setEnvKey(event.target.value); if (event.target.value) setApiKey(""); }} spellCheck={false} placeholder="DEEPSEEK_API_KEY" className="mt-1 font-mono text-[11px]" /></label>
      </div>
      <div className="mt-4 flex justify-end gap-2"><Button type="button" variant="outline" onClick={() => setEditing(false)}>Cancel</Button><Button type="submit" disabled={working || !providerId.trim() || !name.trim() || !baseUrl.trim() || !wireApi.trim()}>{working ? <Loader2 className="animate-spin" /> : <Save />}Save Provider</Button></div>
    </form> : null}
  </SettingsBody>;
}

function ConnectorSettings({ onOpenExternal, onError }: { onOpenExternal: () => void; onError: (message: string) => void }) {
  const query = useQuery<{ connections: PlatformConnection[] }>({ queryKey: ["settings-connections"], queryFn: () => api("GET", "/api/integrations/connections"), refetchInterval: 30_000 });
  const connections = query.data?.connections?.filter((connection) => !connection.archivedAt) || [];
  const githubConnections = connections.filter((connection) => connection.provider === "github");
  const externalConnections = connections.filter((connection) => connection.provider !== "github");
  return <SettingsBody title="Connector operation" description="Transport health and credentials support External roles; they are not the everyday identity model.">
    <GitHubConnectionSettings connections={githubConnections} onError={onError} />
    <div className="mb-2 mt-7 text-[10px] font-semibold uppercase text-muted-foreground">External messaging</div>
    <div className="divide-y divide-border border-y border-border">
      {externalConnections.map((connection) => <div key={connection.id} className="flex min-w-0 items-center gap-3 py-3"><span className={`size-2 rounded-full ${connection.status === "connected" ? "bg-success" : connection.status === "degraded" ? "bg-warning" : "bg-destructive"}`} /><span className="min-w-0 flex-1"><span className="block text-[12px] font-semibold capitalize">{connection.provider}</span><span className="block truncate font-mono text-[9.5px] text-muted-foreground">{connection.accountRef || connection.id}</span></span><span className="font-mono text-[9px] uppercase text-muted-foreground">{connection.status}</span></div>)}
      {!query.isLoading && externalConnections.length === 0 ? <div className="py-7 text-center text-[11px] text-muted-foreground">No external messaging connectors.</div> : null}
    </div>
    <div className="mt-4 flex justify-end"><Button variant="outline" onClick={onOpenExternal}><Cable />Manage external roles</Button></div>
  </SettingsBody>;
}

function GitHubConnectionSettings({ connections, onError }: { connections: PlatformConnection[]; onError: (message: string) => void }) {
  const queryClient = useQueryClient();
  const [device, setDevice] = useState<GitHubDeviceFlow | null>(null);
  const [working, setWorking] = useState(false);
  const [token, setToken] = useState("");
  const [resourceOwner, setResourceOwner] = useState("");
  const devicePollFailures = useRef(0);

  useEffect(() => {
    if (!device || device.status !== "pending") return;
    const timeout = window.setTimeout(async () => {
      try {
        const data = await api("GET", `/api/integrations/providers/github/device/${encodeURIComponent(device.id)}`) as { device: GitHubDeviceFlow };
        devicePollFailures.current = 0;
        setDevice(data.device);
        if (data.device.status === "connected") await queryClient.invalidateQueries({ queryKey: ["settings-connections"] });
      } catch (error) {
        const message = error instanceof Error ? error.message : String(error);
        devicePollFailures.current += 1;
        const status = githubDevicePollStateAfterFailure(devicePollFailures.current);
        setDevice((current) => current?.id === device.id ? { ...current, status, error: message } : current);
        if (status === "failed") onError(message);
      }
    }, Math.max(1, device.pollAfterSeconds || 5) * 1000);
    return () => window.clearTimeout(timeout);
  }, [device, onError, queryClient]);

  const startDevice = async () => {
    setWorking(true);
    try {
      const data = await api("POST", "/api/integrations/providers/github/device", { publicOnly: false }) as { device: GitHubDeviceFlow };
      devicePollFailures.current = 0;
      setDevice(data.device);
      if (data.device.verificationUri) window.open(data.device.verificationUri, "_blank", "noopener,noreferrer");
    } catch (error) {
      onError(error instanceof Error ? error.message : String(error));
    } finally {
      setWorking(false);
    }
  };

  const connectToken = async () => {
    const value = token.trim();
    const owner = resourceOwner.trim();
    if (!value || !owner) return;
    setWorking(true);
    try {
      await api("POST", "/api/integrations/providers/github/token", { token: value, resourceOwner: owner });
      await queryClient.invalidateQueries({ queryKey: ["settings-connections"] });
      setDevice(null);
      setToken("");
      setResourceOwner("");
    } catch (error) {
      onError(error instanceof Error ? error.message : String(error));
    } finally {
      setWorking(false);
    }
  };

  return <section>
    <div className="flex min-w-0 items-start gap-3 border-y border-border py-4">
      <div className="flex size-8 shrink-0 items-center justify-center border border-border bg-muted/35"><GitBranch className="size-4" /></div>
      <div className="min-w-0 flex-1">
        <div className="text-[12px] font-semibold">GitHub triggers</div>
        <div className="mt-0.5 max-w-xl text-[10.5px] leading-4 text-muted-foreground">Observe pull requests and workflow runs without keeping a Turn or terminal watcher alive.</div>
      </div>
      {connections.length > 0 ? <span className="font-mono text-[9px] uppercase text-success">{connections.length} connected</span> : null}
    </div>

    {connections.length > 0 ? <div className="divide-y divide-border border-b border-border">
      {connections.map((connection) => <div key={connection.id} className="flex min-w-0 items-center gap-3 py-3 pl-11">
        <span className={`size-2 shrink-0 rounded-full ${connection.status === "connected" ? "bg-success" : connection.status === "degraded" ? "bg-warning" : "bg-destructive"}`} />
        <div className="min-w-0 flex-1"><div className="truncate text-[11.5px] font-medium">{connection.accountRef} <span className="font-normal text-muted-foreground">/ {connection.scopeRef === "*" ? "all accessible owners" : connection.scopeRef || "legacy scope"}</span></div><div className="truncate font-mono text-[9px] text-muted-foreground">{connection.id} · {connection.capabilities.filter((value) => value.startsWith("trigger:")).join(" · ")}</div></div>
        <span className="font-mono text-[9px] uppercase text-muted-foreground">{connection.status}</span>
      </div>)}
    </div> : null}

    {device?.status === "pending" ? <div className="border-b border-border bg-muted/20 px-3 py-3">
      <div className="flex items-center gap-2 text-[11px] font-medium"><Loader2 className="size-3 animate-spin" />Waiting for GitHub authorization</div>
      <div className="mt-2 flex flex-wrap items-center gap-2">
        <code className="border border-border bg-background px-2.5 py-1.5 text-[15px] font-semibold">{device.userCode}</code>
        <Button variant="outline" size="sm" onClick={() => navigator.clipboard.writeText(device.userCode || "")}><Copy />Copy code</Button>
        <Button variant="outline" size="sm" onClick={() => device.verificationUri && window.open(device.verificationUri, "_blank", "noopener,noreferrer")}><ExternalLink />Open GitHub</Button>
      </div>
      {device.error ? <div className="mt-2 text-[10px] text-warning">Temporary polling failure. Retrying automatically.</div> : null}
    </div> : device?.status === "connected" ? <div className="flex items-center gap-2 border-b border-border bg-success/5 px-3 py-2.5 text-[11px] text-success"><Check className="size-3.5" />Connected as {device.login}</div> : device?.error ? <div className="border-b border-border bg-destructive/5 px-3 py-2.5 text-[11px] text-destructive">{device.error}</div> : null}

    <form className="mt-3 flex min-w-0 flex-col gap-2 lg:flex-row lg:items-end" onSubmit={(event) => { event.preventDefault(); void connectToken(); }}>
      <div className="grid min-w-0 flex-1 gap-2 sm:grid-cols-[minmax(10rem,0.36fr)_minmax(0,1fr)]">
        <label className="min-w-0 text-[10.5px] font-medium text-muted-foreground">Resource owner
          <Input value={resourceOwner} onChange={(event) => setResourceOwner(event.target.value)} autoCapitalize="none" autoCorrect="off" spellCheck={false} placeholder="parall-hq" className="mt-1 font-mono text-[11px]" />
        </label>
        <label className="min-w-0 text-[10.5px] font-medium text-muted-foreground">Personal access token
          <Input value={token} onChange={(event) => setToken(event.target.value)} type="password" autoComplete="new-password" spellCheck={false} placeholder="github_pat_… or ghp_…" className="mt-1 font-mono text-[11px]" />
        </label>
      </div>
      <div className="flex shrink-0 flex-wrap justify-end gap-2">
        <Button type="submit" disabled={working || !token.trim() || !resourceOwner.trim()}>{working ? <Loader2 className="animate-spin" /> : <KeyRound />}Connect owner</Button>
        <Button type="button" variant="outline" onClick={startDevice} disabled={working || device?.status === "pending"}>{working ? <Loader2 className="animate-spin" /> : <GitBranch />}{connections.length ? "Add broad authorization" : "Use GitHub authorization"}</Button>
      </div>
    </form>
    <p className="mt-2 text-[9.5px] leading-4 text-muted-foreground">Fine-grained tokens cover one GitHub user or organization. Add another Resource Owner here without replacing existing sources.</p>
  </section>;
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
