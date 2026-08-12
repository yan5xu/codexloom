import { Archive, Bot, CirclePause, LocateFixed, Search, X } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { filterAgentDirectory } from "./agent-directory";
import { agentLabel } from "./agent-label";
import { summarizeTask } from "./feed";
import { executionDotClass, executionLabel, isAgentExecuting } from "./product-state";
import { Button } from "./components/ui/button";
import type { Agent, HumanRequest, InboxEntry } from "./types";

type SidebarAgentDirectoryProps = {
  agents: Agent[];
  currentId: string | null;
  sidebarOpen: boolean;
  humanRequests: HumanRequest[];
  pendingWork: InboxEntry[];
  unseenIds: Set<string>;
  archivingIds: Set<string>;
  onSelectAgent: (id: string) => void;
  onSelectRequest: (id?: string) => void;
  onArchiveAgent: (agent: Agent) => void;
};

function interruptedAgentTitle(agent: Agent) {
  if (agent.status !== "interrupted") return "";
  const timestamp = agent.lastTurn?.completedAt ? new Date(agent.lastTurn.completedAt) : null;
  const when = timestamp && !Number.isNaN(timestamp.getTime()) ? ` · last active ${timestamp.toLocaleString()}` : "";
  return `Turn interrupted by restart${when}`;
}

function countsByAgent<T>(items: T[], agentID: (item: T) => string) {
  const counts = new Map<string, number>();
  for (const item of items) {
    const id = agentID(item);
    if (id) counts.set(id, (counts.get(id) || 0) + 1);
  }
  return counts;
}

export function SidebarAgentDirectory({
  agents,
  currentId,
  sidebarOpen,
  humanRequests,
  pendingWork,
  unseenIds,
  archivingIds,
  onSelectAgent,
  onSelectRequest,
  onArchiveAgent,
}: SidebarAgentDirectoryProps) {
  const [query, setQuery] = useState("");
  const [locateRequest, setLocateRequest] = useState(0);
  const rowElements = useRef(new Map<string, HTMLDivElement>());
  const visibleAgents = useMemo(() => filterAgentDirectory(agents, query), [agents, query]);
  const currentAgent = agents.find((agent) => agent.id === currentId) || null;
  const requestsByAgent = useMemo(() => countsByAgent(humanRequests, (request) => request.agentId), [humanRequests]);
  const inboxByAgent = useMemo(
    () => countsByAgent(pendingWork.filter((entry) => !["handled", "cancelled"].includes(entry.item.state)), (entry) => entry.item.agentId),
    [pendingWork],
  );

  useEffect(() => {
    if (!currentId) return;
    rowElements.current.get(currentId)?.scrollIntoView({ block: "nearest" });
  }, [currentId, locateRequest, sidebarOpen, visibleAgents.length]);

  const locateCurrent = () => {
    setQuery("");
    setLocateRequest((value) => value + 1);
  };

  return (
    <section className="mx-2 mt-1 flex min-h-0 flex-1 flex-col overflow-hidden rounded-t-md bg-background/45" aria-label="Agents">
      <div className="flex h-8 shrink-0 items-center gap-2 px-2.5 text-muted-foreground">
        <Bot className="size-3" />
        <span className="text-[9px] font-bold uppercase">Agents</span>
        <span className="ml-auto font-mono text-[9px] text-muted-foreground/60" aria-live="polite">
          {query.trim() ? `${visibleAgents.length}/${agents.length}` : agents.length}
        </span>
        {currentAgent ? (
          <Button
            type="button"
            variant="ghost"
            size="icon-xs"
            onClick={locateCurrent}
            title={`Locate current agent: ${agentLabel(currentAgent)}`}
            aria-label={`Locate current agent ${agentLabel(currentAgent)}`}
            className="-mr-1 text-muted-foreground"
          >
            <LocateFixed />
          </Button>
        ) : null}
      </div>

      <div className="shrink-0 px-2 pb-2">
        <div className="flex h-7 items-center gap-1.5 rounded-md border border-sidebar-border/80 bg-background/75 px-2 focus-within:border-ring focus-within:ring-2 focus-within:ring-ring/15">
          <Search className="size-3 shrink-0 text-muted-foreground" />
          <input
            type="search"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Find agent…"
            aria-label="Search agents by name"
            className="min-w-0 flex-1 bg-transparent text-[11.5px] text-foreground outline-none placeholder:text-muted-foreground/60 [&::-webkit-search-cancel-button]:hidden"
            spellCheck={false}
          />
          {query ? (
            <button
              type="button"
              onClick={() => setQuery("")}
              className="flex size-5 shrink-0 items-center justify-center rounded-sm text-muted-foreground outline-none hover:bg-muted hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring/30"
              title="Clear agent search"
              aria-label="Clear agent search"
            >
              <X className="size-3" />
            </button>
          ) : null}
        </div>
      </div>

      <div className="min-h-0 flex-1 space-y-0.5 overflow-y-auto px-1 pb-2" data-agent-directory-list>
        {visibleAgents.map((agent) => {
          const active = agent.id === currentId;
          const archiving = archivingIds.has(agent.id);
          const needsYou = requestsByAgent.get(agent.id) || 0;
          const inboxCount = inboxByAgent.get(agent.id) || 0;
          const activity = agent.currentTask || "";
          const detailTitle = [agent.cwd, activity ? summarizeTask(activity) : "", interruptedAgentTitle(agent)].filter(Boolean).join("\n");
          return (
            <div
              key={agent.id}
              ref={(node) => {
                if (node) rowElements.current.set(agent.id, node);
                else rowElements.current.delete(agent.id);
              }}
              className={`group/agent flex h-8 min-w-0 items-center rounded-md ${
                active ? "bg-selection text-selection-foreground" : "text-foreground/85 hover:bg-muted"
              }`}
              data-agent-directory-row={agent.id}
            >
              <Button
                type="button"
                variant="ghost"
                onClick={() => onSelectAgent(agent.id)}
                title={detailTitle}
                aria-current={active ? "page" : undefined}
                data-agent-directory-entry={agent.id}
                className="h-8 min-w-0 flex-1 justify-start overflow-hidden bg-transparent px-2.5 text-left hover:bg-transparent hover:text-inherit"
              >
                {agent.status === "interrupted" ? (
                  <CirclePause className="size-3.5 shrink-0 text-warning" aria-label="Interrupted by restart" />
                ) : (
                  <span
                    className={`size-2 shrink-0 rounded-full ${isAgentExecuting(agent) ? "pulse" : ""} ${executionDotClass(agent)}`}
                    title={executionLabel(agent)}
                    aria-hidden="true"
                  />
                )}
                <span className={`min-w-0 flex-1 truncate text-[12.5px] ${active ? "font-semibold" : "font-medium"}`}>{agentLabel(agent)}</span>
                {unseenIds.has(agent.id) ? (
                  <span className="size-1.5 shrink-0 rounded-full bg-ring" title="New result from Owner-started work">
                    <span className="sr-only">New result</span>
                  </span>
                ) : null}
                {inboxCount > 0 ? (
                  <span className="flex h-5 min-w-5 shrink-0 items-center justify-center rounded-sm bg-muted px-1 font-mono text-[8.5px] text-muted-foreground" title={`${inboxCount} Agent Inbox items`}>
                    <span className="sr-only">{inboxCount} Agent Inbox items</span>
                    <span aria-hidden="true">{inboxCount}</span>
                  </span>
                ) : null}
              </Button>
              {needsYou > 0 ? (
                <button
                  type="button"
                  onClick={() => onSelectRequest(humanRequests.find((request) => request.agentId === agent.id)?.id)}
                  className="flex h-5 min-w-5 shrink-0 items-center justify-center rounded-sm bg-warning/15 px-1 font-mono text-[8.5px] font-semibold text-warning outline-none hover:bg-warning/25 focus-visible:ring-2 focus-visible:ring-warning/40"
                  title={`${needsYou} request${needsYou === 1 ? "" : "s"} need your input`}
                  aria-label={`Open ${needsYou} human request${needsYou === 1 ? "" : "s"} from ${agentLabel(agent)}`}
                >
                  {needsYou}
                </button>
              ) : null}
              <button
                type="button"
                onClick={() => onArchiveAgent(agent)}
                disabled={archiving}
                tabIndex={active ? 0 : -1}
                className={`mr-1 flex size-6 shrink-0 items-center justify-center rounded-sm text-muted-foreground outline-none transition hover:bg-destructive/10 hover:text-destructive focus-visible:ring-2 focus-visible:ring-destructive/30 disabled:opacity-50 ${
                  active ? "visible opacity-70" : "invisible opacity-0 group-hover/agent:visible group-hover/agent:opacity-70 group-focus-within/agent:visible group-focus-within/agent:opacity-70"
                }`}
                title={`Archive ${agentLabel(agent)}`}
                aria-label={`Archive ${agentLabel(agent)}`}
              >
                <Archive className={`size-3 ${archiving ? "animate-pulse" : ""}`} />
              </button>
            </div>
          );
        })}
        {agents.length === 0 ? (
          <div className="px-3 py-6 text-center text-[12px] text-muted-foreground/50">No agents yet.</div>
        ) : visibleAgents.length === 0 ? (
          <div className="px-3 py-6 text-center text-[11.5px] text-muted-foreground">
            <div>No agents match “{query.trim()}”.</div>
            <button type="button" onClick={() => setQuery("")} className="mt-2 rounded-sm px-2 py-1 text-primary outline-none hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring/30">
              Clear search
            </button>
          </div>
        ) : null}
      </div>
    </section>
  );
}
