import { Activity, AlertTriangle, BarChart3, CheckCircle2, CircleHelp, Inbox, RadioTower, Users } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import type { Agent, HumanRequest, InboxEntry, PlatformConnection, RemoteSnapshot } from "./types";
import { api } from "./types";
import { CapacityPane, UsagePane } from "./UsagePane";
import { summarizeTask } from "./feed";
import { attentionForAgent, executionDotClass, executionLabel, isAgentExecuting, oldestWaitingMs } from "./product-state";
import { Button } from "./components/ui/button";
import { readUsageLocation, type UsageDateRange } from "./usage";

export type OverviewSection = "status" | "capacity" | "usage";

export function OverviewPane({
  section,
  agents,
  requests,
  entries,
  remote,
  onSectionChange,
  onSelectAgent,
  onOpenNeedsYou,
  onOpenExternal,
}: {
  section: OverviewSection;
  agents: Agent[];
  requests: HumanRequest[];
  entries: InboxEntry[];
  remote: RemoteSnapshot | null;
  onSectionChange: (section: OverviewSection) => void;
  onSelectAgent: (id: string) => void;
  onOpenNeedsYou: () => void;
  onOpenExternal: () => void;
}) {
  const [range, setRange] = useState<UsageDateRange>(() => readUsageLocation(window.location.hash));
  return (
    <main className="flex min-h-0 min-w-0 flex-1 flex-col bg-background">
      <header className="flex min-h-14 shrink-0 items-center gap-3 border-b border-border bg-card/80 pl-14 pr-3 md:px-5">
        <Activity className="size-4 shrink-0 text-primary" />
        <div className="min-w-0 flex-1">
          <h1 className="truncate text-[15px] font-semibold">Overview</h1>
          <p className="hidden truncate text-[10.5px] text-muted-foreground sm:block">Inspect operation, capacity, and Codex usage when you need them.</p>
        </div>
        <nav className="flex shrink-0 items-center rounded-sm bg-muted p-0.5" aria-label="Overview views">
          <OverviewTab active={section === "status"} icon={CheckCircle2} label="Status" onClick={() => onSectionChange("status")} />
          <OverviewTab active={section === "capacity"} icon={Activity} label="Capacity" onClick={() => onSectionChange("capacity")} />
          <OverviewTab active={section === "usage"} icon={BarChart3} label="Token usage" onClick={() => onSectionChange("usage")} />
        </nav>
      </header>
      {section === "status" ? (
        <StatusOverview
          agents={agents}
          requests={requests}
          entries={entries}
          remote={remote}
          onSelectAgent={onSelectAgent}
          onOpenNeedsYou={onOpenNeedsYou}
          onOpenExternal={onOpenExternal}
        />
      ) : section === "capacity" ? (
        <CapacityPane onSelectAgent={onSelectAgent} embedded controlledRange={range} onControlledRangeChange={setRange} />
      ) : (
        <UsagePane onSelectAgent={onSelectAgent} embedded controlledRange={range} onControlledRangeChange={setRange} />
      )}
    </main>
  );
}

function OverviewTab({ active, icon: Icon, label, onClick }: { active: boolean; icon: typeof Activity; label: string; onClick: () => void }) {
  return (
    <Button type="button" variant="ghost" size="sm" onClick={onClick} className={`h-7 gap-1.5 px-2 text-[10.5px] ${active ? "bg-background text-foreground shadow-sm" : "text-muted-foreground"}`}>
      <Icon className="size-3" /><span className="hidden sm:inline">{label}</span>
    </Button>
  );
}

function StatusOverview({ agents, requests, entries, remote, onSelectAgent, onOpenNeedsYou, onOpenExternal }: {
  agents: Agent[];
  requests: HumanRequest[];
  entries: InboxEntry[];
  remote: RemoteSnapshot | null;
  onSelectAgent: (id: string) => void;
  onOpenNeedsYou: () => void;
  onOpenExternal: () => void;
}) {
  const connectionsQuery = useQuery<{ connections: PlatformConnection[] }>({
    queryKey: ["overview-connections"],
    queryFn: () => api("GET", "/api/integrations/connections"),
    refetchInterval: 30_000,
  });
  const connections = connectionsQuery.data?.connections?.filter((connection) => !connection.archivedAt) || [];
  const enabledConnections = connections.filter((connection) => connection.enabled);
  const executing = agents.filter(isAgentExecuting);
  const openRequests = requests.filter((request) => request.state === "open");
  const activeEntries = entries.filter((entry) => !["handled", "cancelled"].includes(entry.item.state));
  const failedAgents = agents.filter((agent) => Boolean(agent.lastError));
  const connectorIssues = enabledConnections.filter((connection) => connection.status !== "connected");
  const oldest = oldestWaitingMs(activeEntries);
  const externalStatus = connectionsQuery.isPending
    ? { value: "–", detail: "Checking connections", tone: "text-muted-foreground" }
    : connectionsQuery.isError
      ? { value: "!", detail: "Connection status unavailable", tone: "text-destructive" }
      : connectorIssues.length > 0
        ? { value: String(connectorIssues.length), detail: `${connectorIssues.length} of ${enabledConnections.length} need attention`, tone: "text-destructive" }
        : { value: "0", detail: enabledConnections.length ? `${enabledConnections.length} enabled connections connected` : "No enabled connections", tone: "text-muted-foreground" };

  return (
    <div className="min-h-0 flex-1 overflow-y-auto">
      <div className="mx-auto w-full max-w-[1180px] px-4 py-5 md:px-8 md:py-7">
        <section className="grid border-y border-border sm:grid-cols-2 lg:grid-cols-4">
          <StatusMetric icon={Users} label="Executing now" value={String(executing.length)} detail={`${agents.length - executing.length} ready or unavailable`} tone="text-success" />
          <StatusMetric icon={CircleHelp} label="Needs You" value={String(openRequests.length)} detail={openRequests.length ? "Owner decisions waiting" : "No Owner decision waiting"} tone={openRequests.length ? "text-warning" : "text-muted-foreground"} onClick={onOpenNeedsYou} />
          <StatusMetric icon={Inbox} label="Agent Inbox" value={String(activeEntries.length)} detail={oldest ? `Oldest ${formatDuration(oldest)}` : "No queued Agent work"} tone={activeEntries.length ? "text-warning" : "text-muted-foreground"} />
          <StatusMetric icon={RadioTower} label="External" value={externalStatus.value} detail={externalStatus.detail} tone={externalStatus.tone} onClick={onOpenExternal} />
        </section>

        <div className="mt-7 grid gap-7 lg:grid-cols-[minmax(0,1.25fr)_minmax(300px,.75fr)]">
          <section className="min-w-0">
            <div className="mb-2 flex items-center justify-between"><h2 className="text-[12px] font-semibold uppercase text-muted-foreground">Executing Agents</h2><span className="font-mono text-[9.5px] text-muted-foreground">current state</span></div>
            <div className="divide-y divide-border border-y border-border">
              {executing.map((agent) => (
                <button key={agent.id} type="button" onClick={() => onSelectAgent(agent.id)} className="flex w-full min-w-0 items-center gap-3 px-2 py-3 text-left hover:bg-muted/40">
                  <span className={`size-2 shrink-0 rounded-full ${executionDotClass(agent)}`} />
                  <span className="min-w-0 flex-1"><span className="block truncate text-[12px] font-semibold">{agent.name}</span><span className="mt-0.5 block truncate text-[10.5px] text-muted-foreground">{summarizeTask(agent.currentTask || "") || "Turn in progress"}</span></span>
                  <span className="font-mono text-[9px] uppercase text-success">{executionLabel(agent)}</span>
                </button>
              ))}
              {executing.length === 0 ? <div className="px-2 py-8 text-center text-[11px] text-muted-foreground">No Agent is executing a Turn right now.</div> : null}
            </div>
          </section>

          <section className="min-w-0">
            <div className="mb-2 flex items-center justify-between"><h2 className="text-[12px] font-semibold uppercase text-muted-foreground">Attention signals</h2><span className="font-mono text-[9.5px] text-muted-foreground">not a task list</span></div>
            <div className="divide-y divide-border border-y border-border">
              {agents.map((agent) => ({ agent, attention: attentionForAgent(agent, openRequests, activeEntries) })).filter(({ attention }) => attention.total > 0).map(({ agent, attention }) => (
                <button key={agent.id} type="button" onClick={() => onSelectAgent(agent.id)} className="flex w-full min-w-0 items-center gap-3 px-2 py-3 text-left hover:bg-muted/40">
                  {attention.failures > 0 ? <AlertTriangle className="size-3.5 shrink-0 text-destructive" /> : <Inbox className="size-3.5 shrink-0 text-warning" />}
                  <span className="min-w-0 flex-1"><span className="block truncate text-[12px] font-semibold">{agent.name}</span><span className="mt-0.5 block truncate text-[10.5px] text-muted-foreground">{[attention.needsYou ? `${attention.needsYou} need you` : "", attention.inbox ? `${attention.inbox} inbox` : "", attention.failures ? `${attention.failures} issue` : ""].filter(Boolean).join(" · ")}</span></span>
                </button>
              ))}
              {connectionsQuery.isError || connectorIssues.length > 0 ? (
                <button type="button" onClick={onOpenExternal} className="flex w-full min-w-0 items-center gap-3 px-2 py-3 text-left hover:bg-muted/40">
                  <AlertTriangle className="size-3.5 shrink-0 text-destructive" />
                  <span className="min-w-0 flex-1"><span className="block truncate text-[12px] font-semibold">External connections</span><span className="mt-0.5 block truncate text-[10.5px] text-muted-foreground">{connectionsQuery.isError ? "Status unavailable" : `${connectorIssues.length} need attention`}</span></span>
                </button>
              ) : null}
              {failedAgents.length === 0 && openRequests.length === 0 && activeEntries.length === 0 && connectorIssues.length === 0 && !connectionsQuery.isError ? <div className="px-2 py-8 text-center text-[11px] text-muted-foreground">No current attention signals.</div> : null}
            </div>
          </section>
        </div>
        {remote?.config.enabled ? <div className="mt-7 border-l-2 border-border bg-muted/25 px-3 py-2 text-[10.5px] text-muted-foreground">Codex Remote is <span className="font-medium text-foreground">{remote.status.state}</span>. Remote host state is operational context, not Agent execution.</div> : null}
      </div>
    </div>
  );
}

function StatusMetric({ icon: Icon, label, value, detail, tone, onClick }: { icon: typeof Activity; label: string; value: string; detail: string; tone: string; onClick?: () => void }) {
  const content = <><div className="flex items-center gap-2 text-[10px] uppercase text-muted-foreground"><Icon className="size-3" />{label}</div><div className={`mt-2 font-mono text-2xl font-semibold ${tone}`}>{value}</div><div className="mt-1 text-[10.5px] text-muted-foreground">{detail}</div></>;
  return onClick ? <button type="button" onClick={onClick} className="min-w-0 border-b border-border p-4 text-left hover:bg-muted/35 sm:border-r lg:border-b-0">{content}</button> : <div className="min-w-0 border-b border-border p-4 sm:border-r lg:border-b-0">{content}</div>;
}

function formatDuration(milliseconds: number) {
  const minutes = Math.max(1, Math.floor(milliseconds / 60_000));
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h`;
  return `${Math.floor(hours / 24)}d`;
}
