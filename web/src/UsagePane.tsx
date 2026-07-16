import { useQuery } from "@tanstack/react-query";
import { Tooltip } from "@base-ui/react/tooltip";
import { hierarchy, treemap } from "d3-hierarchy";
import { Activity, ArrowUpRight, BarChart3, CalendarDays, Check, ChevronLeft, ChevronRight, ChevronsUpDown, RefreshCw, X } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { UsageBarTooltip, usageDayLabel } from "./components/UsageBarTooltip";
import { Button } from "./components/ui/button";
import { Input } from "./components/ui/input";
import { Popover, PopoverContent, PopoverTrigger } from "./components/ui/popover";
import {
  api,
  type AgentTokenUsage,
  type AgentWorkload,
  type TokenUsage,
  type TokenUsageOverview,
  type TeamView,
  type UsageDay,
  type WorkloadDay,
  type WorkloadEvidence,
  type WorkloadOverview,
  type WorkloadSource,
} from "./types";
import {
  buildCapacityHash,
  buildUsageHash,
  canShiftUsageRangeForward,
  dateSpanDays,
  readCapacityLocation,
  readUsageLocation,
  reanchorUsageRange,
  shiftUsageRange,
  todayDate,
  usageAPIQuery,
  usageRangeEndingOn,
  USAGE_RANGE_MODES,
  type UsageDateRange,
  type UsageRangeMode,
} from "./usage";

type CalendarPaneProps = {
  onSelectAgent: (id: string) => void;
  embedded?: boolean;
  controlledRange?: UsageDateRange;
  onControlledRangeChange?: (range: UsageDateRange) => void;
};

export function UsagePane({ onSelectAgent, embedded = false, controlledRange, onControlledRangeChange }: CalendarPaneProps) {
  const initialLocation = readUsageLocation(window.location.hash);
  const [localRange, setLocalRange] = useState<UsageDateRange>(initialLocation);
  const range = controlledRange || localRange;
  const [selectedAgentId, setSelectedAgentId] = useState(initialLocation.agentId);
  const updateRange = (value: UsageDateRange) => {
    if (onControlledRangeChange) onControlledRangeChange(value);
    else setLocalRange(value);
  };
  const query = useQuery<TokenUsageOverview>({
    queryKey: ["token-usage", range.from, range.to, range.timezone],
    queryFn: async () => (await api("GET", `/api/usage?${usageAPIQuery(range)}`)).usage,
    refetchInterval: (current) => current.state.data?.live ? 15_000 : false,
  });
  const teamQuery = useQuery<TeamView>({
    queryKey: ["usage-team"],
    queryFn: async () => (await api("GET", "/api/team")).team,
    staleTime: 30_000,
  });
  const usage = query.data;
  const selectedAgent = useMemo(
    () => usage?.agents.find((agent) => agent.agentId === selectedAgentId) || null,
    [selectedAgentId, usage],
  );

  useEffect(() => {
    if (controlledRange) return;
    setUsageHash(range, selectedAgentId);
  }, [controlledRange, range, selectedAgentId]);

  useEffect(() => {
    const root = (((window as any).codexLoom ||= (window as any).codexHub || {}) as Record<string, any>);
    (window as any).codexHub = root;
    const automation = {
      state: () => ({
        view: "usage",
        mode: range.mode,
        from: range.from,
        to: range.to,
        timezone: range.timezone,
        days: usage?.days || dateSpanDays(range.from, range.to),
        live: usage?.live || false,
        loading: query.isLoading,
        trackedAgents: usage?.trackedAgents || 0,
        agentsCount: usage?.agents.length || 0,
        periodTokens: usage?.period.totalTokens || 0,
        todayTokens: usage?.today.totalTokens || 0,
        dailyPoints: usage?.daily.length || 0,
        topAgent: usage?.agents[0]?.agentName || null,
        selectedUsageAgent: selectedAgent?.agentName || null,
        selectedUsageAgentId: selectedAgent?.agentId || null,
        selectedDailyTokens: selectedAgent?.period.totalTokens || usage?.period.totalTokens || 0,
      }),
      setRange: async (value: Partial<UsageDateRange>) => {
        const mode = value.mode || range.mode;
        const next = mode === "custom"
          ? { ...range, ...value, mode }
          : usageRangeEndingOn(mode, value.to || range.to, value.timezone || range.timezone);
        updateRange(next);
        if (!controlledRange) setUsageHash(next, selectedAgentId);
        await new Promise((resolve) => window.setTimeout(resolve, 50));
        return root.usage?.state?.() || automation.state();
      },
      setDays: async (value: number) => {
        const mode = ({ 1: "day", 7: "7d", 30: "30d", 90: "90d" } as Record<number, UsageRangeMode>)[value];
        if (!mode) throw new Error(`unsupported usage range: ${value}`);
        const next = usageRangeEndingOn(mode, todayDate(), range.timezone);
        updateRange(next);
        if (!controlledRange) setUsageHash(next, selectedAgentId);
        await new Promise((resolve) => window.setTimeout(resolve, 50));
        return root.usage?.state?.() || automation.state();
      },
      shiftRange: async (direction: -1 | 1) => {
        const next = shiftUsageRange(range, direction);
        updateRange(next);
        if (!controlledRange) setUsageHash(next, selectedAgentId);
        await new Promise((resolve) => window.setTimeout(resolve, 50));
        return root.usage?.state?.() || automation.state();
      },
      selectUsageAgent: async (id?: string | null) => {
        if (!id || id === "all") {
          setSelectedAgentId(null);
          if (!controlledRange) setUsageHash(range, null);
        } else {
          const agent = usage?.agents.find((item) => item.agentId === id || item.agentName === id);
          if (!agent) throw new Error(`Agent not found in usage: ${id}`);
          setSelectedAgentId(agent.agentId);
          if (!controlledRange) setUsageHash(range, agent.agentId);
        }
        await new Promise((resolve) => window.setTimeout(resolve, 50));
        return root.usage?.state?.() || automation.state();
      },
      refresh: async () => {
        await query.refetch();
        await new Promise((resolve) => window.setTimeout(resolve, 0));
        return root.usage?.state?.() || automation.state();
      },
      selectAgent: async (id: string) => {
        const agent = usage?.agents.find((item) => item.agentId === id || item.agentName === id);
        if (!agent) throw new Error(`Agent not found in usage: ${id}`);
        onSelectAgent(agent.agentId);
        return agent;
      },
    };
    root.usage = automation;
    return () => {
      if (root.usage === automation) delete root.usage;
    };
  }, [controlledRange, onSelectAgent, query, range, selectedAgent, selectedAgentId, usage]);

  const applyRange = (value: UsageDateRange) => {
    updateRange(value);
    if (!controlledRange) setUsageHash(value, selectedAgentId);
  };

  const selectUsageAgent = (id: string | null, scroll = false) => {
    setSelectedAgentId(id);
    if (!controlledRange) setUsageHash(range, id);
    if (scroll) window.requestAnimationFrame(() => document.querySelector("[data-usage-inspector]")?.scrollIntoView({ behavior: "smooth", block: "nearest" }));
  };

  return (
    <main className="flex min-h-0 min-w-0 flex-1 flex-col bg-background">
      {!embedded ? <header className="flex min-h-14 shrink-0 flex-wrap items-center justify-end gap-3 border-b border-border py-2 pl-14 pr-4 sm:justify-start sm:px-4 md:px-6">
        <div className="hidden min-w-0 flex-1 items-center gap-3 sm:flex">
          <BarChart3 className="size-4 shrink-0 text-primary" />
          <div className="min-w-0">
            <h1 className="text-[15px] font-semibold">Token usage</h1>
            <p className="hidden truncate text-[10.5px] text-muted-foreground sm:block">Codex Thread token accounting and prompt cache</p>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="icon-sm" onClick={() => query.refetch()} disabled={query.isFetching} title="Refresh usage" aria-label="Refresh usage">
            <RefreshCw className={query.isFetching ? "animate-spin" : ""} />
          </Button>
        </div>
      </header> : null}

      <div className="min-h-0 flex-1 overflow-y-auto">
        {query.isLoading ? (
          <div className="flex h-full items-center justify-center gap-2 font-mono text-[11px] text-muted-foreground"><span className="spinner size-3" />Reading token usage</div>
        ) : query.isError ? (
          <div className="flex h-full items-center justify-center p-8 text-[12px] text-destructive">{query.error?.message}</div>
        ) : usage ? (
          <TokenUsageWorkspace
            usage={usage}
            range={range}
            team={teamQuery.data}
            selectedAgentId={selectedAgent?.agentId || null}
            onSelectUsageAgent={selectUsageAgent}
            onSelectAgent={onSelectAgent}
            onRangeChange={applyRange}
          />
        ) : null}
      </div>
    </main>
  );
}

export function CapacityPane({ onSelectAgent, embedded = false, controlledRange, onControlledRangeChange }: CalendarPaneProps) {
  const initialLocation = readCapacityLocation(window.location.hash);
  const [localRange, setLocalRange] = useState<UsageDateRange>(initialLocation);
  const range = controlledRange || localRange;
  const [selectedAgentId, setSelectedAgentId] = useState(initialLocation.agentId);
  const updateRange = (value: UsageDateRange) => {
    if (onControlledRangeChange) onControlledRangeChange(value);
    else setLocalRange(value);
  };
  const query = useQuery<WorkloadOverview>({
    queryKey: ["workload", range.from, range.to, range.timezone],
    queryFn: async () => (await api("GET", `/api/workload?${usageAPIQuery(range)}`)).workload,
    refetchInterval: 15_000,
  });
  const workload = query.data;
  const selectedAgent = useMemo(
    () => workload?.agents.find((agent) => agent.agentId === selectedAgentId) || null,
    [selectedAgentId, workload],
  );

  useEffect(() => {
    if (controlledRange) return;
    setCapacityHash(range, selectedAgentId);
  }, [controlledRange, range, selectedAgentId]);

  useEffect(() => {
    const root = (((window as any).codexLoom ||= (window as any).codexHub || {}) as Record<string, any>);
    (window as any).codexHub = root;
    const automation = {
      state: () => ({
        view: "capacity",
        mode: range.mode,
        from: range.from,
        to: range.to,
        timezone: range.timezone,
        days: workload?.days || dateSpanDays(range.from, range.to),
        live: workload?.live || false,
        loading: query.isLoading,
        trackedAgents: workload?.dataQuality.trackedActivityAgents || 0,
        agentsCount: workload?.agents.length || 0,
        executingPercent: selectedAgent?.executingPercent ?? workload?.executingPercent ?? 0,
        queueWaitP90Ms: selectedAgent?.wait.p90Ms ?? workload?.wait.p90Ms ?? 0,
        backlogCount: selectedAgent?.backlog.count ?? workload?.backlog.count ?? 0,
        selectedCapacityAgent: selectedAgent?.agentName || null,
        selectedCapacityAgentId: selectedAgent?.agentId || null,
        dailyPoints: selectedAgent?.daily.length || workload?.daily.length || 0,
      }),
      setRange: async (value: Partial<UsageDateRange>) => {
        const mode = value.mode || range.mode;
        const next = mode === "custom"
          ? { ...range, ...value, mode }
          : usageRangeEndingOn(mode, value.to || range.to, value.timezone || range.timezone);
        updateRange(next);
        if (!controlledRange) setCapacityHash(next, selectedAgentId);
        await new Promise((resolve) => window.setTimeout(resolve, 50));
        return root.capacity?.state?.() || automation.state();
      },
      setDays: async (value: number) => {
        const mode = ({ 1: "day", 7: "7d", 30: "30d", 90: "90d" } as Record<number, UsageRangeMode>)[value];
        if (!mode) throw new Error(`unsupported capacity range: ${value}`);
        const next = usageRangeEndingOn(mode, todayDate(), range.timezone);
        updateRange(next);
        if (!controlledRange) setCapacityHash(next, selectedAgentId);
        await new Promise((resolve) => window.setTimeout(resolve, 50));
        return root.capacity?.state?.() || automation.state();
      },
      shiftRange: async (direction: -1 | 1) => {
        const next = shiftUsageRange(range, direction);
        updateRange(next);
        if (!controlledRange) setCapacityHash(next, selectedAgentId);
        await new Promise((resolve) => window.setTimeout(resolve, 50));
        return root.capacity?.state?.() || automation.state();
      },
      selectCapacityAgent: async (id?: string | null) => {
        if (!id || id === "all") {
          setSelectedAgentId(null);
          if (!controlledRange) setCapacityHash(range, null);
        } else {
          const agent = workload?.agents.find((item) => item.agentId === id || item.agentName === id);
          if (!agent) throw new Error(`Agent not found in capacity: ${id}`);
          setSelectedAgentId(agent.agentId);
          if (!controlledRange) setCapacityHash(range, agent.agentId);
        }
        await new Promise((resolve) => window.setTimeout(resolve, 50));
        return root.capacity?.state?.() || automation.state();
      },
      refresh: async () => {
        await query.refetch();
        await new Promise((resolve) => window.setTimeout(resolve, 0));
        return root.capacity?.state?.() || automation.state();
      },
      selectAgent: async (id: string) => {
        const agent = workload?.agents.find((item) => item.agentId === id || item.agentName === id);
        if (!agent) throw new Error(`Agent not found in capacity: ${id}`);
        onSelectAgent(agent.agentId);
        return agent;
      },
    };
    root.capacity = automation;
    return () => {
      if (root.capacity === automation) delete root.capacity;
    };
  }, [controlledRange, onSelectAgent, query, range, selectedAgent, selectedAgentId, workload]);

  const applyRange = (value: UsageDateRange) => {
    updateRange(value);
    if (!controlledRange) setCapacityHash(value, selectedAgentId);
  };

  const selectCapacityAgent = (id: string | null) => {
    setSelectedAgentId(id);
    if (!controlledRange) setCapacityHash(range, id);
  };

  return (
    <main className="flex min-h-0 min-w-0 flex-1 flex-col bg-background">
      {!embedded ? <header className="flex min-h-14 shrink-0 flex-wrap items-center justify-end gap-3 border-b border-border py-2 pl-14 pr-4 sm:justify-start sm:px-4 md:px-6">
        <div className="hidden min-w-0 flex-1 items-center gap-3 sm:flex">
          <Activity className="size-4 shrink-0 text-primary" />
          <div className="min-w-0">
            <h1 className="text-[15px] font-semibold">Capacity</h1>
            <p className="hidden truncate text-[10.5px] text-muted-foreground sm:block">Execution and queue pressure across Agent Threads</p>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="icon-sm" onClick={() => query.refetch()} disabled={query.isFetching} title="Refresh capacity" aria-label="Refresh capacity">
            <RefreshCw className={query.isFetching ? "animate-spin" : ""} />
          </Button>
        </div>
      </header> : null}

      <div className="min-h-0 flex-1 overflow-y-auto">
        {query.isLoading ? (
          <div className="flex h-full items-center justify-center gap-2 font-mono text-[11px] text-muted-foreground"><span className="spinner size-3" />Reading capacity signals</div>
        ) : query.isError ? (
          <div className="flex h-full items-center justify-center p-8 text-[12px] text-destructive">{query.error?.message}</div>
        ) : workload ? (
          <div className="mx-auto w-full max-w-[1240px] px-4 py-5 md:px-8 md:py-7">
            <CapacityWorkspace
              workload={workload}
              range={range}
              selectedAgent={selectedAgent}
              scope={selectedAgent || workload}
              selectedAgentId={selectedAgent?.agentId || null}
              onSelectAgent={selectCapacityAgent}
              onOpenAgent={onSelectAgent}
              onRangeChange={applyRange}
            />
          </div>
        ) : null}
      </div>
    </main>
  );
}

function TokenUsageWorkspace({
  usage,
  range,
  team,
  selectedAgentId,
  onSelectUsageAgent,
  onSelectAgent,
  onRangeChange,
}: {
  usage: TokenUsageOverview;
  range: UsageDateRange;
  team?: TeamView;
  selectedAgentId: string | null;
  onSelectUsageAgent: (id: string | null, scroll?: boolean) => void;
  onSelectAgent: (id: string) => void;
  onRangeChange: (range: UsageDateRange) => void;
}) {
  const selectedAgent = usage.agents.find((agent) => agent.agentId === selectedAgentId) || null;
  const previousDelta = usageDelta(usage.period.totalTokens, usage.previous.totalTokens);
  return (
    <div className="mx-auto w-full min-w-0 max-w-[1240px] overflow-x-hidden px-4 py-5 md:px-8 md:py-7">
      <UsageRangeToolbar subject="Token usage" range={range} live={usage.live} generatedAt={usage.generatedAt} onChange={onRangeChange} />

      <SectionHeading title="Overview" detail="Measured from Codex rollout token_count events" />
      <section className="grid grid-cols-2 border-y border-border lg:grid-cols-6" aria-label="Usage summary">
        <Metric label="Total tokens" value={formatTokens(usage.period.totalTokens)} detail={`${previousDelta.label} previous period`} />
        <Metric label="Model calls" value={formatTokens(usage.period.calls)} detail={`${formatTokens(averagePerCall(usage.period))} average / call`} />
        <Metric
          label={usage.days === 1 ? "Active agents" : "Average / day"}
          value={usage.days === 1 ? `${usage.agents.filter((agent) => agent.period.totalTokens > 0).length}/${usage.agents.length}` : formatTokens(usage.period.totalTokens / usage.days)}
          detail={usage.days === 1 ? "with token activity" : `${usage.days} calendar days`}
        />
        <Metric label="Output" value={formatTokens(usage.period.outputTokens)} detail={`${formatTokens(usage.period.reasoningOutputTokens)} reasoning`} />
        <Metric label="Cache hit" value={formatPercent(cachePercent(usage.period))} detail={`${formatTokens(usage.period.inputTokens - usage.period.cachedInputTokens)} uncached`} />
        <Metric label="Tracked" value={`${usage.trackedAgents}/${usage.agents.length}`} detail={`lifetime ${formatTokens(usage.lifetime.totalTokens)}`} />
      </section>

      {usage.days > 1 && <section data-usage-chart-section className="scroll-mt-14 border-b border-border py-6">
        <div className="mb-4 flex min-w-0 flex-wrap items-center justify-between gap-3">
          <div className="min-w-0">
            <h2 className="text-[12px] font-semibold uppercase text-foreground">Activity over time</h2>
            <p className="mt-0.5 truncate font-mono text-[9.5px] text-muted-foreground">Daily token volume · click a day to inspect it</p>
          </div>
          <span className="font-mono text-[9.5px] text-muted-foreground">{usage.since} to {usage.through} · {usage.timezone}</span>
        </div>
        <DailyBars days={usage.daily} chartId="overview" onSelectDay={(date) => onRangeChange(usageRangeEndingOn("day", date, range.timezone))} />
      </section>}

      <section className="border-b border-border py-6">
        <div className="mb-4 flex min-w-0 flex-wrap items-center justify-between gap-3">
          <div>
            <h2 className="text-[12px] font-semibold uppercase text-foreground">Agent allocation</h2>
            <p className="mt-0.5 text-[10px] text-muted-foreground">Rectangle area is proportional to tokens in the selected period.</p>
          </div>
          <UsageScopePicker agents={usage.agents} selectedAgentId={selectedAgent?.agentId || null} onSelect={onSelectUsageAgent} />
        </div>
        <TokenTreemap
          agents={usage.agents}
          organizationLinks={team?.organizationLinks || []}
          total={usage.period.totalTokens}
          selectedAgentId={selectedAgent?.agentId || null}
          onSelect={onSelectUsageAgent}
        />
      </section>

      {selectedAgent && <AgentUsageInspector agent={selectedAgent} total={usage.period.totalTokens} onClose={() => onSelectUsageAgent(null)} onOpenAgent={onSelectAgent} />}

      <section className="border-b border-border py-6">
        <SectionHeading title="Agent comparison" detail="Exact values for the selected calendar range" />
        <AgentUsageTable
          agents={usage.agents}
          total={usage.period.totalTokens}
          selectedAgentId={selectedAgent?.agentId || null}
          onInspectAgent={(id) => onSelectUsageAgent(id, true)}
          onSelectAgent={onSelectAgent}
        />
      </section>

      <section className="grid gap-8 py-6 lg:grid-cols-2">
        <div className="min-w-0">
          <SectionHeading title="Token composition" detail="Selected period" />
          <TokenComposition usage={usage.period} />
        </div>
        <div className="min-w-0">
          <SectionHeading title="Models" detail="Selected period" />
          <ModelBreakdown models={usage.models} total={usage.period.totalTokens} />
        </div>
      </section>
    </div>
  );
}

function UsageRangeToolbar({
  subject,
  range,
  live,
  generatedAt,
  onChange,
}: {
  subject: string;
  range: UsageDateRange;
  live: boolean;
  generatedAt: string;
  onChange: (range: UsageDateRange) => void;
}) {
  const setMode = (mode: UsageRangeMode) => {
    if (mode === "custom") onChange({ ...range, mode });
    else onChange(usageRangeEndingOn(mode, range.to, range.timezone));
  };
  const setAnchor = (to: string) => {
    if (!to) return;
    if (range.mode === "custom") onChange({ ...range, to, from: range.from > to ? to : range.from });
    else onChange(usageRangeEndingOn(range.mode, to, range.timezone));
  };
  const setCustomFrom = (from: string) => {
    if (!from) return;
    onChange({ ...range, from, to: from > range.to ? from : range.to });
  };
  const generated = new Date(generatedAt);
  const liveLabel = Number.isNaN(generated.getTime()) ? "Live" : `Live · through ${generated.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}`;
  return (
    <section className="mb-7 border-y border-border bg-muted/20 px-3 py-3" aria-label={`${subject} calendar range`}>
      <div className="flex min-w-0 flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
        <div className="overflow-x-auto">
          <div className="flex w-max items-center bg-muted/70 p-0.5" role="group" aria-label={`${subject} range mode`}>
            {USAGE_RANGE_MODES.map((mode) => (
              <button
                key={mode}
                type="button"
                onClick={() => setMode(mode)}
                className={`h-8 min-w-14 rounded px-2 font-mono text-[10px] ${range.mode === mode ? "bg-background font-semibold text-foreground shadow-sm" : "text-muted-foreground hover:text-foreground"}`}
              >
                {usageModeLabel(mode)}
              </button>
            ))}
          </div>
        </div>
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          <Button variant="outline" size="icon-sm" onClick={() => onChange(shiftUsageRange(range, -1))} title="Previous period" aria-label="Previous period">
            <ChevronLeft />
          </Button>
          {range.mode === "custom" ? (
            <div className="order-last flex w-full min-w-0 items-center gap-1.5 sm:order-none sm:w-auto sm:flex-none">
              <DateInput value={range.from} max={range.to} onChange={setCustomFrom} label="From date" />
              <span className="font-mono text-[9px] text-muted-foreground">to</span>
              <DateInput value={range.to} min={range.from} max={todayDate()} onChange={setAnchor} label="Through date" />
            </div>
          ) : (
            <DateInput value={range.to} max={todayDate()} onChange={setAnchor} label={range.mode === "day" ? `${subject} date` : "Period end date"} />
          )}
          <Button variant="outline" size="icon-sm" onClick={() => onChange(shiftUsageRange(range, 1))} disabled={!canShiftUsageRangeForward(range)} title="Next period" aria-label="Next period">
            <ChevronRight />
          </Button>
          <Button variant="outline" size="sm" onClick={() => onChange(reanchorUsageRange(range, todayDate()))}>
            <CalendarDays /> Today
          </Button>
        </div>
      </div>
      <div className="mt-2 flex min-w-0 flex-col gap-1 font-mono text-[9.5px] text-muted-foreground sm:flex-row sm:flex-wrap sm:items-center sm:justify-between sm:gap-2">
        <span>{range.from}{range.from !== range.to ? ` to ${range.to}` : ""} · {range.timezone}</span>
        <span className={live ? "text-success" : ""}>{live ? liveLabel : `${dateSpanDays(range.from, range.to)} complete calendar day${dateSpanDays(range.from, range.to) === 1 ? "" : "s"}`}</span>
      </div>
    </section>
  );
}

function DateInput({ value, min, max, onChange, label }: { value: string; min?: string; max?: string; onChange: (value: string) => void; label: string }) {
  return (
    <input
      type="date"
      value={value}
      min={min}
      max={max}
      onChange={(event) => onChange(event.target.value)}
      aria-label={label}
      className="h-8 w-0 min-w-0 flex-1 rounded-md border border-input bg-background px-2 font-mono text-[10px] text-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring/30 sm:w-[9.5rem] sm:flex-none"
    />
  );
}

type TreemapDatum = {
  name: string;
  color: string;
  agent?: AgentTokenUsage;
  children?: TreemapDatum[];
};

const TREEMAP_COLORS = ["var(--loom-teal)", "var(--loom-green)", "var(--loom-amber)", "var(--loom-vermilion)", "var(--loom-blue)"];

function TokenTreemap({
  agents,
  organizationLinks,
  total,
  selectedAgentId,
  onSelect,
}: {
  agents: AgentTokenUsage[];
  organizationLinks: TeamView["organizationLinks"];
  total: number;
  selectedAgentId: string | null;
  onSelect: (id: string | null) => void;
}) {
  const layout = useMemo(() => buildTokenTreemap(agents, organizationLinks), [agents, organizationLinks]);
  if (total <= 0 || layout.leaves.length === 0) {
    return <div className="flex h-64 items-center justify-center border-y border-border text-[11px] text-muted-foreground">No token usage in this period.</div>;
  }
  return (
    <Tooltip.Provider delay={120} closeDelay={40}>
      <div data-usage-treemap className="relative h-[420px] w-full min-w-0 max-w-full overflow-hidden border border-border bg-muted/20 sm:h-[480px]">
        {layout.groups.map((group) => (
          <div
            key={`group:${group.data.name}`}
            className="pointer-events-none absolute overflow-hidden border border-border/80"
            style={treemapBox(group)}
          >
            {(group.x1 - group.x0) >= 60 && <div className="truncate px-2 pt-1 font-mono text-[8.5px] font-semibold uppercase text-muted-foreground">{group.data.name}</div>}
          </div>
        ))}
        {layout.leaves.map((leaf) => {
          const agent = leaf.data.agent!;
          const share = total > 0 ? (agent.period.totalTokens / total) * 100 : 0;
          const width = leaf.x1 - leaf.x0;
          const height = leaf.y1 - leaf.y0;
          const detailed = width >= 105 && height >= 65;
          const named = width >= 55 && height >= 30;
          const selected = agent.agentId === selectedAgentId;
          return (
            <Tooltip.Root key={agent.agentId}>
              <Tooltip.Trigger
                type="button"
                onClick={() => onSelect(selected ? null : agent.agentId)}
                className={`absolute overflow-hidden border text-left outline-none transition-[filter,border-color] hover:brightness-[0.97] focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring/45 ${selected ? "z-10 border-foreground shadow-[inset_0_0_0_1px_var(--foreground)]" : "border-background"}`}
                style={{
                  ...treemapBox(leaf),
                  background: `color-mix(in oklch, ${leaf.data.color} ${selected ? "38%" : "24%"}, var(--background))`,
                }}
                aria-label={`${agent.agentName}: ${formatTokens(agent.period.totalTokens)}, ${formatPercent(share)} of selected period`}
              >
                {named && <div className="flex h-full min-w-0 flex-col p-2">
                  <span className="truncate text-[10.5px] font-semibold text-foreground">{agent.agentName}</span>
                  {detailed && <>
                    <span className="mt-auto font-mono text-[13px] font-semibold text-foreground">{formatTokens(agent.period.totalTokens)}</span>
                    <span className="font-mono text-[8.5px] text-muted-foreground">{formatPercent(share)} · {formatTokens(agent.period.calls)} calls</span>
                  </>}
                </div>}
              </Tooltip.Trigger>
              <Tooltip.Portal>
                <Tooltip.Positioner side="top" sideOffset={8} className="z-50 outline-none">
                  <Tooltip.Popup className="w-56 border border-border bg-popover px-3 py-2.5 text-popover-foreground shadow-float outline-none data-closed:hidden">
                    <div className="truncate text-[11px] font-semibold">{agent.agentName}</div>
                    <div className="mt-1 font-mono text-[10px] font-semibold">{formatTokens(agent.period.totalTokens)} · {formatPercent(share)}</div>
                    <div className="mt-2 grid grid-cols-2 gap-x-3 gap-y-1 font-mono text-[8.5px] text-muted-foreground">
                      <span>Calls</span><span className="text-right text-foreground">{formatTokens(agent.period.calls)}</span>
                      <span>Input</span><span className="text-right text-foreground">{formatTokens(agent.period.inputTokens)}</span>
                      <span>Cached</span><span className="text-right text-foreground">{formatPercent(cachePercent(agent.period))}</span>
                      <span>Output</span><span className="text-right text-foreground">{formatTokens(agent.period.outputTokens)}</span>
                      <span>Previous</span><span className="text-right text-foreground">{usageDelta(agent.period.totalTokens, agent.previous.totalTokens).label}</span>
                    </div>
                  </Tooltip.Popup>
                </Tooltip.Positioner>
              </Tooltip.Portal>
            </Tooltip.Root>
          );
        })}
      </div>
    </Tooltip.Provider>
  );
}

function buildTokenTreemap(agents: AgentTokenUsage[], links: TeamView["organizationLinks"]) {
  const byID = new Map(agents.map((agent) => [agent.agentId, agent]));
  const parentByChild = new Map(links.map((link) => [link.childAgentId, link.parentAgentId]));
  const linked = new Set(links.flatMap((link) => [link.parentAgentId, link.childAgentId]));
  const groups = new Map<string, AgentTokenUsage[]>();
  for (const agent of agents.filter((item) => item.period.totalTokens > 0)) {
    let rootID = agent.agentId;
    const visited = new Set<string>();
    while (parentByChild.has(rootID) && !visited.has(rootID)) {
      visited.add(rootID);
      rootID = parentByChild.get(rootID)!;
    }
    const groupName = linked.has(agent.agentId) ? (byID.get(rootID)?.agentName || "Organization") : "Independent";
    const current = groups.get(groupName) || [];
    current.push(agent);
    groups.set(groupName, current);
  }
  const children = [...groups.entries()]
    .map(([name, members], index): TreemapDatum => ({
      name,
      color: TREEMAP_COLORS[index % TREEMAP_COLORS.length],
      children: members.map((agent) => ({ name: agent.agentName, color: TREEMAP_COLORS[index % TREEMAP_COLORS.length], agent })),
    }))
    .sort((a, b) => sumGroup(b) - sumGroup(a));
  const root = hierarchy<TreemapDatum>({ name: "Agents", color: "var(--muted)", children })
    .sum((datum) => datum.agent?.period.totalTokens || 0)
    .sort((a, b) => (b.value || 0) - (a.value || 0));
  const laidOut = treemap<TreemapDatum>()
    .size([1000, 500])
    .paddingOuter(2)
    .paddingInner(3)
    .paddingTop((node) => node.depth === 1 ? 22 : 0)
    .round(true)(root);
  return {
    groups: laidOut.descendants().filter((node) => node.depth === 1),
    leaves: laidOut.leaves(),
  };
}

function treemapBox(node: { x0: number; y0: number; x1: number; y1: number }) {
  return {
    left: `${node.x0 / 10}%`,
    top: `${node.y0 / 5}%`,
    width: `${(node.x1 - node.x0) / 10}%`,
    height: `${(node.y1 - node.y0) / 5}%`,
  };
}

function sumGroup(group: TreemapDatum) {
  return (group.children || []).reduce((sum, child) => sum + (child.agent?.period.totalTokens || 0), 0);
}

function AgentUsageInspector({ agent, total, onClose, onOpenAgent }: { agent: AgentTokenUsage; total: number; onClose: () => void; onOpenAgent: (id: string) => void }) {
  const delta = usageDelta(agent.period.totalTokens, agent.previous.totalTokens);
  const contextAvailable = agent.context.windowTokens > 0;
  return (
    <section data-usage-inspector className="scroll-mt-14 border-b border-border py-6">
      <div className="mb-4 flex min-w-0 items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex min-w-0 items-center gap-2">
            <span className={`size-1.5 shrink-0 rounded-full ${agent.status === "running" ? "bg-success" : "bg-muted-foreground/35"}`} />
            <h2 className="truncate text-[15px] font-semibold">{agent.agentName}</h2>
          </div>
          <p className="mt-1 font-mono text-[9.5px] text-muted-foreground">Selected Agent · current Thread context is a live snapshot</p>
        </div>
        <div className="flex items-center gap-1">
          <Button variant="outline" size="sm" onClick={() => onOpenAgent(agent.agentId)}><ArrowUpRight /> Open Thread</Button>
          <Button variant="ghost" size="icon-sm" onClick={onClose} title="Close inspector" aria-label="Close inspector"><X /></Button>
        </div>
      </div>
      <div className="grid grid-cols-2 border-y border-border lg:grid-cols-6">
        <Metric label="Tokens" value={formatTokens(agent.period.totalTokens)} detail={`${formatPercent(total ? agent.period.totalTokens * 100 / total : 0)} of period`} />
        <Metric label="Previous" value={formatTokens(agent.previous.totalTokens)} detail={`${delta.label} previous period`} />
        <Metric label="Calls" value={formatTokens(agent.period.calls)} detail={`${formatTokens(averagePerCall(agent.period))} average / call`} />
        <Metric label="Cache hit" value={formatPercent(cachePercent(agent.period))} detail={`${formatTokens(agent.period.cachedInputTokens)} cached`} />
        <Metric label="Output" value={formatTokens(agent.period.outputTokens)} detail={`${formatTokens(agent.period.reasoningOutputTokens)} reasoning`} />
        <Metric label="Current context" value={contextAvailable ? formatPercent(agent.context.usedPercent) : "—"} detail={contextAvailable ? `${formatTokens(agent.context.inputTokens)} / ${formatTokens(agent.context.windowTokens)}` : "live snapshot unavailable"} />
      </div>
    </section>
  );
}

function TokenComposition({ usage }: { usage: TokenUsage }) {
  const segments = [
    { label: "Cached input", value: usage.cachedInputTokens, color: "var(--loom-teal)" },
    { label: "Uncached input", value: Math.max(0, usage.inputTokens - usage.cachedInputTokens), color: "var(--loom-blue)" },
    { label: "Reasoning output", value: usage.reasoningOutputTokens, color: "var(--loom-vermilion)" },
    { label: "Other output", value: Math.max(0, usage.outputTokens - usage.reasoningOutputTokens), color: "var(--loom-amber)" },
  ];
  const denominator = Math.max(1, usage.inputTokens + usage.outputTokens);
  return (
    <div className="border-y border-border py-4">
      <div className="flex h-3 w-full overflow-hidden bg-muted">
        {segments.map((segment) => <div key={segment.label} style={{ width: `${segment.value * 100 / denominator}%`, background: segment.color }} />)}
      </div>
      <div className="mt-4 space-y-2">
        {segments.map((segment) => (
          <div key={segment.label} className="grid grid-cols-[12px_1fr_auto_auto] items-center gap-2 text-[10px]">
            <span className="size-2" style={{ background: segment.color }} />
            <span className="text-muted-foreground">{segment.label}</span>
            <span className="font-mono text-foreground">{formatTokens(segment.value)}</span>
            <span className="w-12 text-right font-mono text-[9px] text-muted-foreground">{formatPercent(segment.value * 100 / denominator)}</span>
          </div>
        ))}
      </div>
    </div>
  );
}

function usageModeLabel(mode: UsageRangeMode) {
  if (mode === "day") return "Day";
  if (mode === "custom") return "Custom";
  return mode.toUpperCase();
}

function usageDelta(current: number, previous: number) {
  if (previous <= 0) return { value: current > 0 ? null : 0, label: current > 0 ? "New vs" : "No change vs" };
  const value = (current - previous) * 100 / previous;
  return { value, label: `${value >= 0 ? "+" : ""}${formatPercent(value)} vs` };
}

function averagePerCall(usage: TokenUsage) {
  return usage.calls > 0 ? usage.totalTokens / usage.calls : 0;
}

type WorkloadScope = Pick<
  WorkloadOverview,
  "observedSeconds" | "executingSeconds" | "executingPercent" | "idleProxyPercent" | "wait" | "backlog" | "daily" | "sources" | "evidence"
>;

function CapacityWorkspace({
  workload,
  range,
  selectedAgent,
  scope,
  selectedAgentId,
  onSelectAgent,
  onOpenAgent,
  onRangeChange,
}: {
  workload: WorkloadOverview;
  range: UsageDateRange;
  selectedAgent: AgentWorkload | null;
  scope: WorkloadScope;
  selectedAgentId: string | null;
  onSelectAgent: (id: string | null, scroll?: boolean) => void;
  onOpenAgent: (id: string) => void;
  onRangeChange: (range: UsageDateRange) => void;
}) {
  return (
    <div data-workload-section>
      <UsageRangeToolbar subject="Capacity" range={range} live={workload.live} generatedAt={workload.generatedAt} onChange={onRangeChange} />

      <section className="border-b border-border pb-7">
      <div className="mb-4 flex min-w-0 flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <h2 className="text-[12px] font-semibold uppercase text-foreground">Capacity signals</h2>
          <p className="mt-0.5 hidden text-[10px] text-muted-foreground sm:block">Diagnostic evidence for organization design, not an automatic split or merge decision.</p>
        </div>
        <div className="flex w-full min-w-0 flex-col items-stretch gap-2 sm:w-auto sm:flex-row sm:flex-wrap sm:items-center sm:justify-end">
          <span className="truncate text-left font-mono text-[9.5px] text-muted-foreground sm:text-right">{workload.since} to {workload.through} · {workload.timezone}</span>
          <CapacityScopePicker agents={workload.agents} selectedAgentId={selectedAgentId} onSelect={onSelectAgent} />
        </div>
      </div>

      <div className="grid grid-cols-2 border-y border-border [&>*:last-child:nth-child(odd)]:col-span-2 lg:grid-cols-5 lg:[&>*:last-child:nth-child(odd)]:col-span-1" aria-label="Capacity summary">
        <Metric
          label="Executing"
          value={scope.observedSeconds ? formatPercent(scope.executingPercent) : "—"}
          detail={`${formatDurationSeconds(scope.executingSeconds)} Turn time · ${formatDurationSeconds(scope.observedSeconds)} Agent-time${workload.live ? " through now" : ""}`}
        />
        <Metric label="Calendar non-executing proxy" value={scope.observedSeconds ? formatPercent(scope.idleProxyPercent) : "—"} detail={`recorded window${workload.live ? " through now" : ""} · includes offline`} />
        <Metric label="New-work wait" value={formatWait(scope.wait.p50Ms, scope.wait.samples)} detail={`p90 ${formatWait(scope.wait.p90Ms, scope.wait.samples)} · ${scope.wait.samples} samples`} />
        <Metric label="Current backlog" value={String(scope.backlog.count)} detail={scope.backlog.count ? `live snapshot · oldest ${formatDurationMs(scope.backlog.oldestMs)}` : "live snapshot · no queued work"} />
        <Metric
          label="Coverage"
          value={`${workload.dataQuality.trackedActivityAgents}/${workload.dataQuality.totalAgents}`}
          detail="Agents with readable rollout activity"
        />
      </div>

      <div className={`grid gap-6 py-6 ${workload.days > 1 ? "lg:grid-cols-[minmax(0,1.45fr)_minmax(280px,0.55fr)]" : ""}`}>
        {workload.days > 1 && <div className="min-w-0">
          <div className="mb-3 flex min-w-0 items-baseline justify-between gap-3">
            <span className="truncate text-[11px] font-semibold">{selectedAgent?.agentName || "All agents"}</span>
            <span className="shrink-0 font-mono text-[9px] text-muted-foreground">Recorded execution · click a day to inspect</span>
          </div>
          <WorkloadDailyBars days={scope.daily} onSelectDay={(date) => onRangeChange(usageRangeEndingOn("day", date, range.timezone))} />
        </div>}
        <SourceBreakdown sources={scope.sources} />
      </div>

      <CapacityAgentTable
        agents={workload.agents}
        selectedAgentId={selectedAgentId}
        onInspect={(id) => onSelectAgent(id)}
        onOpenAgent={onOpenAgent}
      />

      <div className="grid gap-6 pt-6 lg:grid-cols-[minmax(0,1fr)_320px]">
        <QueueEvidence evidence={scope.evidence} />
        <details className="border-y border-border py-3 text-[10px] text-muted-foreground">
          <summary className="cursor-pointer select-none text-[10px] font-semibold uppercase text-foreground">Data quality</summary>
          <div className="mt-3 space-y-2 leading-relaxed">
            {workload.dataQuality.limitations.map((limitation) => <p key={limitation}>{limitation}</p>)}
          </div>
        </details>
      </div>
      </section>
    </div>
  );
}

function WorkloadDailyBars({ days, onSelectDay }: { days: WorkloadDay[]; onSelectDay: (date: string) => void }) {
  const labelEvery = days.length <= 7 ? 1 : days.length <= 30 ? 5 : 15;
  return (
    <Tooltip.Provider delay={120} closeDelay={40}>
      <div className="overflow-x-auto pb-1">
        <div data-workload-chart className="flex h-28 min-w-[620px] items-end gap-1 border-b border-border px-1 pt-3">
          {days.map((day, index) => {
            const height = day.executingPercent > 0 ? Math.max(3, day.executingPercent) : 0;
            const showLabel = index % labelEvery === 0 || index === days.length - 1;
            const align = index === 0 ? "start" : index === days.length - 1 ? "end" : "center";
            return (
              <Tooltip.Root key={day.date}>
                <Tooltip.Trigger
                  type="button"
                  data-workload-date={day.date}
                  onClick={() => onSelectDay(day.date)}
                  className="relative flex h-full min-w-0 flex-1 cursor-pointer items-end outline-none hover:brightness-[0.97] focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring/35"
                  aria-label={`${day.date}: ${formatPercent(day.executingPercent)} executing, ${day.turnCount} Turns. Open this day.`}
                >
                  <div className="relative h-full w-full bg-muted">
                    {day.observedSeconds > 0 && day.executingPercent > 0 && <div className="absolute inset-x-0 bottom-0 bg-[var(--loom-vermilion)]/70" style={{ height: `${height}%` }} />}
                    {day.observedSeconds === 0 && <div className="absolute inset-0 bg-background opacity-80" />}
                  </div>
                  {showLabel && <span className="pointer-events-none absolute -bottom-5 left-0 whitespace-nowrap font-mono text-[8.5px] text-muted-foreground">{shortDate(day.date)}</span>}
                </Tooltip.Trigger>
                <Tooltip.Portal>
                  <Tooltip.Positioner side="top" align={align} sideOffset={8} className="z-50 outline-none">
                    <Tooltip.Popup className="w-44 border border-border bg-popover px-2.5 py-2 text-left text-popover-foreground shadow-float outline-none data-closed:hidden">
                      <div className="text-[10px] font-semibold text-foreground">{day.date}</div>
                      <div className="mt-1 font-mono text-[9px] text-muted-foreground">{formatPercent(day.executingPercent)} · {formatDurationSeconds(day.executingSeconds)} · {day.turnCount} Turns</div>
                    </Tooltip.Popup>
                  </Tooltip.Positioner>
                </Tooltip.Portal>
              </Tooltip.Root>
            );
          })}
        </div>
        <div className="mt-7 flex items-center gap-4 font-mono text-[9px] text-muted-foreground">
          <Legend color="bg-[var(--loom-vermilion)]/70" label="executing" />
          <Legend color="bg-muted" label="calendar non-executing proxy" />
        </div>
      </div>
    </Tooltip.Provider>
  );
}

function SourceBreakdown({ sources }: { sources: WorkloadSource[] }) {
  return (
    <div className="min-w-0 border-y border-border">
      <div className="border-b border-border bg-muted/25 px-3 py-2 text-[9px] font-semibold uppercase text-muted-foreground">Queue by source</div>
      {sources.length ? sources.map((source) => (
        <div key={source.source} className="grid grid-cols-[1fr_auto_auto] items-center gap-3 border-b border-border px-3 py-2.5 last:border-b-0">
          <span className="truncate text-[10.5px] font-medium">{sourceLabel(source.source)}</span>
          <span className="font-mono text-[9px] text-muted-foreground">p90 {formatWait(source.wait.p90Ms, source.wait.samples)}</span>
          <span className={`font-mono text-[9px] ${source.backlog.count ? "text-warning" : "text-muted-foreground"}`}>{source.backlog.count} queued</span>
        </div>
      )) : <div className="px-3 py-8 text-center text-[10px] text-muted-foreground">No queue samples in this range.</div>}
    </div>
  );
}

function CapacityAgentTable({
  agents,
  selectedAgentId,
  onInspect,
  onOpenAgent,
}: {
  agents: AgentWorkload[];
  selectedAgentId: string | null;
  onInspect: (id: string) => void;
  onOpenAgent: (id: string) => void;
}) {
  return (
    <div className="min-w-0 max-w-full overflow-x-auto border-y border-border">
      <table className="w-full min-w-[920px] table-fixed text-left">
        <thead className="bg-muted/35 font-mono text-[9px] uppercase text-muted-foreground">
          <tr>
            <th className="w-[22%] px-3 py-2 font-medium">Agent</th>
            <th className="w-[11%] px-3 py-2 text-right font-medium">Execute</th>
            <th className="w-[11%] px-3 py-2 text-right font-medium">Calendar non-executing proxy</th>
            <th className="w-[9%] px-3 py-2 text-right font-medium">Turns</th>
            <th className="w-[11%] px-3 py-2 text-right font-medium">P50 wait</th>
            <th className="w-[11%] px-3 py-2 text-right font-medium">P90 wait</th>
            <th className="w-[9%] px-3 py-2 text-right font-medium">Queued</th>
            <th className="w-[10%] px-3 py-2 text-right font-medium">Oldest</th>
            <th className="w-[6%] px-3 py-2 text-right font-medium">Thread</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-border">
          {agents.map((agent) => (
            <tr
              key={agent.agentId}
              className={`cursor-pointer outline-none hover:bg-muted/30 focus-visible:bg-muted/40 ${agent.agentId === selectedAgentId ? "bg-primary/[0.07]" : ""}`}
              onClick={() => onInspect(agent.agentId)}
              onKeyDown={(event) => {
                if (event.key === "Enter" || event.key === " ") {
                  event.preventDefault();
                  onInspect(agent.agentId);
                }
              }}
              tabIndex={0}
              aria-selected={agent.agentId === selectedAgentId}
            >
              <td className="min-w-0 px-3 py-2.5">
                <div className="flex min-w-0 items-center gap-2">
                  <span className={`size-1.5 shrink-0 rounded-full ${agent.status === "running" ? "bg-success" : "bg-muted-foreground/35"}`} />
                  <span className="truncate text-[12px] font-medium">{agent.agentName}</span>
                  {!agent.activityAvailable && <span className="shrink-0 font-mono text-[8px] text-muted-foreground">no rollout</span>}
                </div>
              </td>
              <NumberCell value={agent.activityAvailable ? formatPercent(agent.executingPercent) : "—"} />
              <NumberCell value={agent.activityAvailable ? formatPercent(agent.idleProxyPercent) : "—"} />
              <NumberCell value={agent.activityAvailable ? String(agent.turnCount) : "—"} />
              <NumberCell value={formatWait(agent.wait.p50Ms, agent.wait.samples)} />
              <NumberCell value={formatWait(agent.wait.p90Ms, agent.wait.samples)} />
              <NumberCell value={String(agent.backlog.count)} strong={agent.backlog.count > 0} />
              <NumberCell value={agent.backlog.count ? formatDurationMs(agent.backlog.oldestMs) : "—"} />
              <td className="px-3 py-1.5 text-right">
                <button
                  type="button"
                  onClick={(event) => {
                    event.stopPropagation();
                    onOpenAgent(agent.agentId);
                  }}
                  className="inline-flex size-7 items-center justify-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/30"
                  title={`Open ${agent.agentName}`}
                  aria-label={`Open ${agent.agentName}`}
                >
                  <ArrowUpRight className="size-3.5" />
                </button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function QueueEvidence({ evidence }: { evidence: WorkloadEvidence[] }) {
  return (
    <div className="min-w-0">
      <div className="mb-3 flex items-baseline justify-between gap-3">
        <h3 className="text-[10px] font-semibold uppercase text-foreground">Queue evidence</h3>
        <span className="font-mono text-[9px] text-muted-foreground">queued first · longest waits</span>
      </div>
      <div className="overflow-x-auto border-y border-border">
        {evidence.length ? evidence.map((item) => (
          <a key={`${item.source}:${item.id}`} href={item.evidenceHref} className="grid min-w-[620px] grid-cols-[150px_1fr_90px_100px] items-center gap-3 border-b border-border px-3 py-2.5 last:border-b-0 hover:bg-muted/30">
            <div className="min-w-0">
              <div className="truncate text-[10.5px] font-medium">{item.agentName}</div>
              <div className="truncate font-mono text-[8.5px] text-muted-foreground">{item.id}</div>
            </div>
            <div className="min-w-0">
              <div className="truncate text-[10px] text-foreground">{sourceLabel(item.source)}{item.provider ? ` · ${item.provider}` : ""}</div>
              <div className="truncate font-mono text-[8.5px] text-muted-foreground">{waitReasonLabel(item.waitReason)}</div>
            </div>
            <span className={`font-mono text-[9px] ${item.startedAt ? "text-muted-foreground" : "text-warning"}`}>{item.startedAt ? "started" : item.state}</span>
            <span className="text-right font-mono text-[10px] font-semibold">{formatDurationMs(item.waitMs)}</span>
          </a>
        )) : <div className="px-3 py-8 text-center text-[10px] text-muted-foreground">No queue evidence in this range.</div>}
      </div>
    </div>
  );
}

function Metric({ label, value, detail }: { label: string; value: string; detail: string }) {
  return (
    <div className="min-w-0 border-b border-border px-3 py-4 [&:nth-child(even)]:border-l lg:border-b-0 lg:border-l lg:first:border-l-0">
      <div className="text-[10px] font-semibold uppercase text-muted-foreground">{label}</div>
      <div className="mt-1 font-mono text-[22px] font-semibold text-foreground">{value}</div>
      <div className="mt-0.5 truncate font-mono text-[9.5px] text-muted-foreground" title={detail}>{detail}</div>
    </div>
  );
}

function SectionHeading({ title, detail }: { title: string; detail: string }) {
  return (
    <div className="mb-4 flex min-w-0 items-baseline justify-between gap-4">
      <h2 className="text-[12px] font-semibold uppercase text-foreground">{title}</h2>
      <span className="hidden truncate font-mono text-[9.5px] text-muted-foreground sm:block">{detail}</span>
    </div>
  );
}

function DailyBars({ days, chartId = "overview", onSelectDay }: { days: UsageDay[]; chartId?: string; onSelectDay: (date: string) => void }) {
  const max = Math.max(1, ...days.map((day) => day.usage.totalTokens));
  const labelEvery = days.length <= 7 ? 1 : days.length <= 30 ? 5 : 15;
  return (
    <div className="overflow-x-auto pb-1">
      <div data-usage-chart={chartId} className="flex h-48 min-w-[620px] items-end gap-1 border-b border-border px-1 pt-4">
        {days.map((day, index) => {
          const height = day.usage.totalTokens > 0 ? Math.max(3, (day.usage.totalTokens / max) * 100) : 1;
          const cached = cachePercent(day.usage);
          const showLabel = index % labelEvery === 0 || index === days.length - 1;
          const tooltipAlign = index === 0 ? "start" : index === days.length - 1 ? "end" : "center";
          return (
            <button key={day.date} type="button" data-usage-date={day.date} onClick={() => onSelectDay(day.date)} className="group relative flex h-full min-w-0 flex-1 items-end outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring/35" aria-label={`${usageDayLabel(day)}. Open this day.`}>
              <UsageBarTooltip day={day} align={tooltipAlign} />
              <div className="relative w-full bg-muted" style={{ height: `${height}%` }}>
                <div className="absolute inset-x-0 bottom-0 bg-[var(--loom-teal)]/65" style={{ height: `${cached}%` }} />
                {day.usage.outputTokens > 0 && <div className="absolute inset-x-0 top-0 h-0.5 bg-[var(--loom-vermilion)]" />}
              </div>
              {showLabel && <span className="absolute -bottom-5 left-0 whitespace-nowrap font-mono text-[8.5px] text-muted-foreground">{shortDate(day.date)}</span>}
            </button>
          );
        })}
      </div>
      <div className="mt-7 flex items-center gap-4 font-mono text-[9px] text-muted-foreground">
        <Legend color="bg-muted" label="uncached input" />
        <Legend color="bg-[var(--loom-teal)]/65" label="cached input" />
        <Legend color="bg-[var(--loom-vermilion)]" label="output present" />
      </div>
    </div>
  );
}

function CapacityScopePicker({ agents, selectedAgentId, onSelect }: { agents: AgentWorkload[]; selectedAgentId: string | null; onSelect: (id: string | null) => void }) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const selected = agents.find((agent) => agent.agentId === selectedAgentId) || null;
  const filtered = useMemo(() => {
    const needle = query.trim().toLowerCase();
    if (!needle) return agents;
    return agents.filter((agent) => agent.agentName.toLowerCase().includes(needle));
  }, [agents, query]);

  const choose = (id: string | null) => {
    onSelect(id);
    setOpen(false);
    setQuery("");
  };

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger
        className="flex h-8 w-full min-w-0 shrink-0 items-center gap-2 rounded-md border border-border bg-background px-2.5 text-[11px] font-medium outline-none hover:bg-muted/35 focus-visible:ring-2 focus-visible:ring-ring/30 sm:w-auto sm:max-w-[min(18rem,calc(100vw-2rem))] sm:min-w-40"
        aria-label="Select capacity scope"
      >
        <Activity className="size-3.5 shrink-0 text-muted-foreground" />
        <span className="min-w-0 flex-1 truncate text-left">{selected?.agentName || "All agents"}</span>
        <ChevronsUpDown className="size-3.5 shrink-0 text-muted-foreground" />
      </PopoverTrigger>
      <PopoverContent align="end" className="w-[min(22rem,calc(100vw-1rem))] p-2" aria-label="Capacity scope">
        <Input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Find agent" className="mb-2 font-mono text-[11px]" />
        <div className="max-h-64 overflow-y-auto border-y border-border">
          <UsageScopeOption label="All agents" selected={!selected} detail={`${agents.filter((agent) => agent.activityAvailable).length} tracked`} onClick={() => choose(null)} />
          {filtered.map((agent) => (
            <UsageScopeOption
              key={agent.agentId}
              label={agent.agentName}
              selected={agent.agentId === selected?.agentId}
              detail={agent.activityAvailable ? `${formatPercent(agent.executingPercent)} executing` : "No activity"}
              onClick={() => choose(agent.agentId)}
            />
          ))}
          {filtered.length === 0 && <div className="px-3 py-6 text-center text-[10px] text-muted-foreground">No matching agents</div>}
        </div>
      </PopoverContent>
    </Popover>
  );
}

function UsageScopePicker({ agents, selectedAgentId, onSelect }: { agents: AgentTokenUsage[]; selectedAgentId: string | null; onSelect: (id: string | null) => void }) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const selected = agents.find((agent) => agent.agentId === selectedAgentId) || null;
  const filtered = useMemo(() => {
    const needle = query.trim().toLowerCase();
    if (!needle) return agents;
    return agents.filter((agent) => agent.agentName.toLowerCase().includes(needle));
  }, [agents, query]);

  const choose = (id: string | null) => {
    onSelect(id);
    setOpen(false);
    setQuery("");
  };

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger
        className="flex h-8 w-full min-w-0 shrink-0 items-center gap-2 rounded-md border border-border bg-background px-2.5 text-[11px] font-medium outline-none hover:bg-muted/35 focus-visible:ring-2 focus-visible:ring-ring/30 sm:w-auto sm:max-w-[min(18rem,calc(100vw-2rem))] sm:min-w-40"
        aria-label="Select usage scope"
      >
        <BarChart3 className="size-3.5 shrink-0 text-muted-foreground" />
        <span className="min-w-0 flex-1 truncate text-left">{selected?.agentName || "All agents"}</span>
        <ChevronsUpDown className="size-3.5 shrink-0 text-muted-foreground" />
      </PopoverTrigger>
      <PopoverContent align="end" className="w-[min(22rem,calc(100vw-1rem))] p-2" aria-label="Usage scope">
        <Input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Find agent" className="mb-2 font-mono text-[11px]" />
        <div className="max-h-64 overflow-y-auto border-y border-border">
          <UsageScopeOption label="All agents" selected={!selected} detail={`${agents.filter((agent) => agent.available).length} tracked`} onClick={() => choose(null)} />
          {filtered.map((agent) => (
            <UsageScopeOption
              key={agent.agentId}
              label={agent.agentName}
              selected={agent.agentId === selected?.agentId}
              detail={agent.available ? formatTokens(agent.period.totalTokens) : "No usage"}
              onClick={() => choose(agent.agentId)}
            />
          ))}
          {filtered.length === 0 && <div className="px-3 py-6 text-center text-[10px] text-muted-foreground">No matching agents</div>}
        </div>
      </PopoverContent>
    </Popover>
  );
}

function UsageScopeOption({ label, detail, selected, onClick }: { label: string; detail: string; selected: boolean; onClick: () => void }) {
  return (
    <button type="button" onClick={onClick} className={`flex min-h-9 w-full items-center gap-2 border-b border-border px-2.5 py-1.5 text-left last:border-b-0 ${selected ? "bg-primary text-primary-foreground" : "hover:bg-muted/45"}`}>
      <Check className={`size-3.5 shrink-0 ${selected ? "opacity-100" : "opacity-0"}`} />
      <span className="min-w-0 flex-1 truncate text-[11px] font-medium">{label}</span>
      <span className={`shrink-0 font-mono text-[9px] ${selected ? "text-primary-foreground/75" : "text-muted-foreground"}`}>{detail}</span>
    </button>
  );
}

function Legend({ color, label }: { color: string; label: string }) {
  return <span className="inline-flex items-center gap-1.5"><span className={`h-1.5 w-3 ${color}`} />{label}</span>;
}

function AgentUsageTable({
  agents,
  total,
  selectedAgentId,
  onInspectAgent,
  onSelectAgent,
}: {
  agents: AgentTokenUsage[];
  total: number;
  selectedAgentId: string | null;
  onInspectAgent: (id: string) => void;
  onSelectAgent: (id: string) => void;
}) {
  return (
    <div className="min-w-0 max-w-full overflow-x-auto border-y border-border">
      <table className="w-full min-w-[980px] table-fixed text-left">
        <thead className="bg-muted/35 font-mono text-[9px] uppercase text-muted-foreground">
          <tr>
            <th className="w-[22%] px-3 py-2 font-medium">Agent</th>
            <th className="w-[12%] px-3 py-2 text-right font-medium">Tokens</th>
            <th className="w-[10%] px-3 py-2 text-right font-medium">Share</th>
            <th className="w-[10%] px-3 py-2 text-right font-medium">Calls</th>
            <th className="w-[12%] px-3 py-2 text-right font-medium">Avg / call</th>
            <th className="w-[10%] px-3 py-2 text-right font-medium">Cache</th>
            <th className="w-[10%] px-3 py-2 text-right font-medium">Output</th>
            <th className="w-[9%] px-3 py-2 text-right font-medium">Previous</th>
            <th className="w-[5%] px-3 py-2 text-right font-medium">Thread</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-border">
          {agents.map((agent) => (
            <tr
              key={agent.agentId}
              className={`cursor-pointer outline-none hover:bg-muted/30 focus-visible:bg-muted/40 ${agent.agentId === selectedAgentId ? "bg-primary/[0.07]" : ""}`}
              onClick={() => onInspectAgent(agent.agentId)}
              onKeyDown={(event) => {
                if (event.key === "Enter" || event.key === " ") {
                  event.preventDefault();
                  onInspectAgent(agent.agentId);
                }
              }}
              tabIndex={0}
              aria-selected={agent.agentId === selectedAgentId}
            >
              <td className="min-w-0 px-3 py-2.5">
                <div className="flex min-w-0 items-center gap-2">
                  <span className={`size-1.5 shrink-0 rounded-full ${agent.status === "running" ? "bg-success" : "bg-muted-foreground/35"}`} />
                  <span className="truncate text-[12px] font-medium">{agent.agentName}</span>
                  {agent.latestModel && <span className="truncate font-mono text-[8.5px] text-muted-foreground">{agent.latestModel}</span>}
                </div>
              </td>
              {agent.available ? (
                <>
                  <NumberCell value={formatTokens(agent.period.totalTokens)} />
                  <NumberCell value={formatPercent(total ? agent.period.totalTokens * 100 / total : 0)} />
                  <NumberCell value={formatTokens(agent.period.calls)} />
                  <NumberCell value={formatTokens(averagePerCall(agent.period))} />
                  <NumberCell value={formatPercent(cachePercent(agent.period))} />
                  <NumberCell value={formatTokens(agent.period.outputTokens)} />
                  <NumberCell value={usageDelta(agent.period.totalTokens, agent.previous.totalTokens).label.replace(" vs", "")} strong />
                </>
              ) : (
                <td colSpan={7} className="px-3 py-2.5 text-right font-mono text-[10px] text-muted-foreground">No Thread usage yet</td>
              )}
              <td className="px-3 py-1.5 text-right">
                <button
                  type="button"
                  onClick={(event) => {
                    event.stopPropagation();
                    onSelectAgent(agent.agentId);
                  }}
                  className="inline-flex size-7 items-center justify-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/30"
                  title={`Open ${agent.agentName}`}
                  aria-label={`Open ${agent.agentName}`}
                >
                  <ArrowUpRight className="size-3.5" />
                </button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function NumberCell({ value, strong = false }: { value: string; strong?: boolean }) {
  return <td className={`px-3 py-2.5 text-right font-mono text-[10.5px] ${strong ? "font-semibold text-foreground" : "text-muted-foreground"}`}>{value}</td>;
}

function ModelBreakdown({ models, total }: { models: TokenUsageOverview["models"]; total: number }) {
  if (models.length === 0) return <div className="py-8 text-center text-[11px] text-muted-foreground">No model usage in this period.</div>;
  return (
    <div className="divide-y divide-border border-y border-border">
      {models.map((model) => {
        const share = total > 0 ? (model.usage.totalTokens / total) * 100 : 0;
        return (
          <div key={model.model} className="grid items-center gap-3 py-3 sm:grid-cols-[180px_1fr_100px_80px]">
            <span className="truncate font-mono text-[11px] font-medium">{model.model}</span>
            <div className="h-1.5 bg-muted"><div className="h-full bg-[var(--loom-blue)]/70" style={{ width: `${share}%` }} /></div>
            <span className="text-right font-mono text-[10px] text-muted-foreground">{formatTokens(model.usage.totalTokens)}</span>
            <span className="text-right font-mono text-[9px] text-muted-foreground">{formatPercent(share)}</span>
          </div>
        );
      })}
    </div>
  );
}

function formatTokens(value: number) {
  const amount = Number.isFinite(value) ? Math.max(0, value) : 0;
  if (amount >= 1_000_000_000) return `${trimNumber(amount / 1_000_000_000)}B`;
  if (amount >= 1_000_000) return `${trimNumber(amount / 1_000_000)}M`;
  if (amount >= 1_000) return `${trimNumber(amount / 1_000)}K`;
  return Math.round(amount).toLocaleString();
}

function trimNumber(value: number) {
  return value >= 100 ? value.toFixed(0) : value >= 10 ? value.toFixed(1) : value.toFixed(2);
}

function formatPercent(value: number) {
  if (!Number.isFinite(value)) return "0%";
  return `${value >= 10 ? value.toFixed(0) : value.toFixed(1)}%`;
}

function cachePercent(usage: TokenUsage) {
  return usage.inputTokens > 0 ? (usage.cachedInputTokens / usage.inputTokens) * 100 : 0;
}

function shortDate(value: string) {
  return value.slice(5).replace("-", "/");
}

function formatWait(value: number, samples: number) {
  return samples > 0 ? formatDurationMs(value) : "—";
}

function formatDurationMs(value: number) {
  const milliseconds = Number.isFinite(value) ? Math.max(0, value) : 0;
  if (milliseconds < 1_000) return milliseconds > 0 ? "<1s" : "0s";
  return formatDurationSeconds(milliseconds / 1_000);
}

function formatDurationSeconds(value: number) {
  const seconds = Number.isFinite(value) ? Math.max(0, value) : 0;
  if (seconds < 60) return `${Math.round(seconds)}s`;
  if (seconds < 3_600) return `${trimNumber(seconds / 60)}m`;
  if (seconds < 86_400) return `${trimNumber(seconds / 3_600)}h`;
  return `${trimNumber(seconds / 86_400)}d`;
}

function sourceLabel(value: string) {
  switch (value) {
    case "internal": return "Agent message";
    case "continuation": return "Causal reply";
    case "external": return "External Inbox";
    case "schedule": return "Schedule";
    case "human_answer": return "Needs You answer";
    default: return value.replaceAll("_", " ");
  }
}

function waitReasonLabel(value: string) {
  switch (value) {
    case "agent_busy": return "Agent is executing another Turn";
    case "active_goal": return "Active Goal owns the next continuation";
    case "restart_drain": return "Restart drain is holding durable work";
    case "deferred_until": return "Deferred until its configured time";
    case "ready_pending_dispatch": return "Ready, awaiting dispatcher claim";
    case "unrecorded": return "Historical wait reason was not recorded";
    default: return value.replaceAll("_", " ");
  }
}

function setUsageHash(range: UsageDateRange, agentId: string | null = null) {
  window.history.replaceState(null, "", buildUsageHash(range, agentId));
}

function setCapacityHash(range: UsageDateRange, agentId: string | null = null) {
  window.history.replaceState(null, "", buildCapacityHash(range, agentId));
}
