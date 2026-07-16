import {
  Background,
  Controls,
  Handle,
  MarkerType,
  Position,
  ReactFlow,
  type Edge,
  type Node,
  type NodeChange,
  type NodeProps,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import {
  Activity,
  CalendarClock,
  Edit3,
  GitFork,
  Link2,
  List,
  MessageSquare,
  Network,
  Plus,
  RefreshCw,
  Save,
  Search,
  Send,
  Trash2,
  X,
} from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import {
  api,
  type AgentProfile,
  type OrganizationRelationship,
  type TeamAgent,
  type TeamObservedLink,
  type TeamRelationship,
  type TeamView,
} from "./types";

declare global {
  interface Window {
    codexHub?: Record<string, any>;
    codexLoom?: Record<string, any>;
  }
}

interface Props {
  onError: (msg: string) => void;
  onMessageAgent: (name: string) => void;
  onScheduleAgent: (name: string) => void;
  onOpenMessages: (agentA: string, agentB: string) => void;
}

type TeamViewMode = "organization" | "collaboration" | "activity" | "directory";
type GraphViewMode = Exclude<TeamViewMode, "directory">;
type NodePositions = Record<string, { x: number; y: number }>;
const TEAM_POSITIONS_KEY = "codex-loom.team.positions.v3";
const DEFAULT_ACTIVITY_EDGE_LIMIT = 12;

type AgentNodeData = {
  agent: TeamAgent;
  selected: boolean;
  perspective: GraphViewMode;
  organizationRole?: "root" | "member";
  onMessage: (name: string) => void;
};

type ActivityPair = {
  id: string;
  agentAId: string;
  agentBId: string;
  agentA: string;
  agentB: string;
  messageCount: number;
  replyCount: number;
  openCount: number;
  queuedCount: number;
  failedCount: number;
  lastMessageAt?: string;
  subjects: string[];
  directions: TeamObservedLink[];
};

type TeamGraph = {
  nodes: Node<AgentNodeData>[];
  edges: Edge[];
  visibleAgentIds: string[];
  visibleEdgeIds: string[];
};

const EMPTY_TEAM: TeamView = { agents: [], organizationLinks: [], collaborationLinks: [], observedLinks: [], explicitLinks: [] };
const nodeTypes = { agentCard: AgentGraphNode };

export function TeamPane({ onError, onMessageAgent, onScheduleAgent, onOpenMessages }: Props) {
  const route = readTeamRouteState();
  const [team, setTeam] = useState<TeamView>(EMPTY_TEAM);
  const [activityLinks, setActivityLinks] = useState<TeamObservedLink[]>([]);
  const [activityDays, setActivityDays] = useState(route.days);
  const [query, setQuery] = useState(route.query);
  const [selectedAgentId, setSelectedAgentId] = useState(route.agent);
  const [selectedLinkId, setSelectedLinkId] = useState(route.link);
  const [viewMode, setViewMode] = useState<TeamViewMode>(route.view);
  const [loading, setLoading] = useState(false);
  const [flowInstance, setFlowInstance] = useState<any>(null);
  const [nodePositions, setNodePositions] = useState<NodePositions>(loadNodePositions);

  useEffect(() => {
    window.localStorage.setItem(TEAM_POSITIONS_KEY, JSON.stringify(nodePositions));
  }, [nodePositions]);

  const refresh = async (days = activityDays) => {
    setLoading(true);
    try {
      const [teamData, activityData] = await Promise.all([
        api("GET", "/api/team"),
        api("GET", `/api/team/activity?days=${days}`),
      ]);
      const next = teamData.team || EMPTY_TEAM;
      setTeam({
        ...EMPTY_TEAM,
        ...next,
        organizationLinks: next.organizationLinks || [],
        collaborationLinks: next.collaborationLinks || next.explicitLinks || [],
        observedLinks: next.observedLinks || [],
        explicitLinks: next.explicitLinks || next.collaborationLinks || [],
      });
      setActivityLinks(activityData.observedLinks || []);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    refresh(activityDays).catch((err: Error) => onError(err.message));
    const es = new EventSource("/api/events");
    es.onmessage = (event) => {
      try {
        const value = JSON.parse(event.data);
        if (
          value.type === "loom/reconcile" ||
          value.type === "loom/comms-message" ||
          value.type === "loom/agent-status" ||
          value.type === "loom/agents" ||
          value.type === "loom/profile-updated" ||
          value.type === "loom/team-link-updated" ||
          value.type === "loom/team-link-deleted" ||
          value.type === "loom/organization-link-updated" ||
          value.type === "loom/organization-link-deleted"
        ) {
          refresh(activityDays).catch(() => {});
        }
      } catch {
        // Ignore malformed SSE values; the next snapshot refresh repairs state.
      }
    };
    return () => es.close();
  }, [activityDays]);

  const resolveAgentId = (key: string) => team.agents.find((agent) => agent.id === key || agent.name === key)?.id || key;
  const selectedAgent = team.agents.find((agent) => agent.id === selectedAgentId) || null;
  const activityPairs = useMemo(() => aggregateActivityPairs(activityLinks), [activityLinks]);
  const selectedOrganization = team.organizationLinks.find((link) => organizationLinkId(link) === selectedLinkId) || null;
  const selectedCollaboration = team.collaborationLinks.find((link) => collaborationLinkId(link) === selectedLinkId) || null;
  const selectedActivity = activityPairs.find((link) => link.id === selectedLinkId) || null;
  const adjacentOrganization = selectedAgent
    ? team.organizationLinks.filter((link) => link.parentAgentId === selectedAgent.id || link.childAgentId === selectedAgent.id)
    : [];
  const adjacentCollaboration = selectedAgent
    ? team.collaborationLinks.filter((link) => link.fromAgentId === selectedAgent.id || link.toAgentId === selectedAgent.id)
    : [];
  const adjacentObserved = selectedAgent
    ? activityLinks.filter((link) => link.fromAgentId === selectedAgent.id || link.toAgentId === selectedAgent.id)
    : [];

  useEffect(() => {
    if (!selectedAgentId || team.agents.some((agent) => agent.id === selectedAgentId)) return;
    const byName = team.agents.find((agent) => agent.name === selectedAgentId);
    if (byName) setSelectedAgentId(byName.id);
  }, [selectedAgentId, team.agents]);

  const filteredAgents = useMemo(() => {
    const q = query.trim().toLowerCase();
    const visible = q ? team.agents.filter((agent) => agentSearchText(agent).includes(q)) : team.agents;
    return [...visible].sort((a, b) => a.name.localeCompare(b.name));
  }, [team.agents, query]);

  const graphMode: GraphViewMode = viewMode === "directory" ? "organization" : viewMode;
  const graph = useMemo(
    () => buildTeamGraph(team, activityPairs, graphMode, query, selectedAgentId, selectedLinkId, nodePositions, onMessageAgent),
    [team, activityPairs, graphMode, query, selectedAgentId, selectedLinkId, nodePositions, onMessageAgent],
  );
  const graphFitKey = graph.visibleAgentIds.join("\u0000");

  useEffect(() => {
    if (viewMode === "directory" || !flowInstance || graph.nodes.length === 0) return;
    const timer = window.setTimeout(() => flowInstance.fitView({ padding: 0.16, duration: 220 }), 120);
    return () => window.clearTimeout(timer);
  }, [flowInstance, graphFitKey, viewMode]);

  const activeAgents = team.agents.filter(teamAgentWorking).length;
  const profiledAgents = team.agents.filter((agent) => agent.profile.version > 0).length;
  const openRequests = team.agents.reduce((sum, agent) => sum + agent.openIn, 0);

  useEffect(() => {
    writeTeamRouteState({ agent: selectedAgentId, link: selectedLinkId, query, view: viewMode, days: activityDays });
  }, [activityDays, query, selectedAgentId, selectedLinkId, viewMode]);

  const selectAgent = (key: string) => {
    const id = resolveAgentId(key);
    setSelectedAgentId(id);
    setSelectedLinkId("");
  };

  const saveProfile = async (agent: TeamAgent, profile: Pick<AgentProfile, "identity" | "domain" | "scope">) => {
    await api("PUT", `/api/agents/${encodeURIComponent(agent.id)}/profile`, {
      ...profile,
      expectedVersion: agent.profile.version,
    });
    await refresh();
  };

  const createRelationship = async (from: string, to: string, description: string) => {
    const result = await api("POST", "/api/team/relationships", { from, to, description });
    await refresh();
    setSelectedAgentId("");
    setSelectedLinkId(collaborationLinkId(result.relationship));
  };

  const updateRelationship = async (relationship: TeamRelationship, description: string) => {
    await api("PATCH", `/api/team/relationships/${encodeURIComponent(relationship.id)}`, { description });
    await refresh();
  };

  const deleteRelationship = async (relationship: TeamRelationship) => {
    await api("DELETE", `/api/team/relationships/${encodeURIComponent(relationship.id)}`);
    setSelectedLinkId("");
    await refresh();
  };

  const createOrganizationRelationship = async (parent: string, child: string, description: string) => {
    const result = await api("POST", "/api/team/organization", { parent, child, description });
    await refresh();
    setViewMode("organization");
    setSelectedAgentId("");
    setSelectedLinkId(organizationLinkId(result.relationship));
  };

  const updateOrganizationRelationship = async (relationship: OrganizationRelationship, description: string) => {
    await api("PATCH", `/api/team/organization/${encodeURIComponent(relationship.id)}`, { description });
    await refresh();
  };

  const deleteOrganizationRelationship = async (relationship: OrganizationRelationship) => {
    await api("DELETE", `/api/team/organization/${encodeURIComponent(relationship.id)}`);
    setSelectedLinkId("");
    await refresh();
  };

  useEffect(() => {
    const state = () => ({
      view: "team",
      viewMode,
      filter: query,
      selectedAgentId,
      selectedAgent: selectedAgent?.name || "",
      selectedLinkId,
      loading,
      agentsCount: team.agents.length,
      profiledAgentsCount: profiledAgents,
      organizationLinksCount: team.organizationLinks.length,
      collaborationLinksCount: team.collaborationLinks.length,
      explicitLinksCount: team.collaborationLinks.length,
      observedLinksCount: team.observedLinks.length,
      activityLinksCount: activityPairs.length,
      activityDays,
      visibleAgentsCount: viewMode === "directory" ? filteredAgents.length : graph.visibleAgentIds.length,
      openRequests,
      graph: viewMode === "directory" ? graphState(EMPTY_GRAPH, "", "") : graphState(graph, selectedAgentId, selectedLinkId),
    });
    const root = window.codexLoom || window.codexHub || {};
    const teamAutomation = {
      state,
      open: () => {
        setViewMode("organization");
        return waitForAutomationState();
      },
      setView: (mode: TeamViewMode) => {
        setViewMode(mode);
        return waitForAutomationState();
      },
      selectAgent: (key: string) => {
        selectAgent(key);
        return waitForAutomationState();
      },
      setFilter: (value: string) => {
        setQuery(value);
        return waitForAutomationState();
      },
      saveProfile: async (key: string, profile: Pick<AgentProfile, "identity" | "domain" | "scope">) => {
        const agent = team.agents.find((item) => item.id === key || item.name === key);
        if (!agent) throw new Error(`agent not found: ${key}`);
        await saveProfile(agent, profile);
        return waitForAutomationState();
      },
      createRelationship: async (from: string, to: string, description: string) => {
        await createRelationship(from, to, description);
        return waitForAutomationState();
      },
      deleteRelationship: async (id: string) => {
        const relationship = team.collaborationLinks.find((item) => item.id === id);
        if (!relationship) throw new Error(`relationship not found: ${id}`);
        await deleteRelationship(relationship);
        return waitForAutomationState();
      },
      createOrganizationRelationship: async (parent: string, child: string, description: string) => {
        await createOrganizationRelationship(parent, child, description);
        return waitForAutomationState();
      },
      deleteOrganizationRelationship: async (id: string) => {
        const relationship = team.organizationLinks.find((item) => item.id === id);
        if (!relationship) throw new Error(`organization relationship not found: ${id}`);
        await deleteOrganizationRelationship(relationship);
        return waitForAutomationState();
      },
    };
    const graphAutomation = {
        state: () => graphState(graph, selectedAgentId, selectedLinkId),
        open: () => {
          if (viewMode === "directory") setViewMode("organization");
          return waitForGraphState();
        },
        selectNode: (key: string) => {
          if (viewMode === "directory") setViewMode("organization");
          selectAgent(key);
          return waitForGraphState();
        },
        selectEdge: (id: string) => {
          if (viewMode === "directory") setViewMode("organization");
          setSelectedAgentId("");
          setSelectedLinkId(id);
          return waitForGraphState();
        },
        search: (value: string) => {
          setQuery(value);
          return waitForGraphState();
        },
        focusAgent: (key: string) => {
          selectAgent(key);
          return waitForGraphState();
        },
        fitView: () => {
          flowInstance?.fitView?.({ padding: 0.2, duration: 220 });
          return waitForGraphState();
        },
        relayout: () => {
          setNodePositions((previous) => Object.fromEntries(Object.entries(previous).filter(([key]) => !key.startsWith(`${graphMode}:`))));
          window.setTimeout(() => flowInstance?.fitView?.({ padding: 0.2, duration: 220 }), 30);
          return waitForGraphState();
        },
        moveNode: (id: string, x: number, y: number) => {
          setNodePositions((previous) => ({ ...previous, [`${graphMode}:${id}`]: { x, y } }));
          return waitForGraphState();
        },
    };
    root.team = teamAutomation;
    root.graph = graphAutomation;
    window.codexLoom = root;
    window.codexHub = root;
  }, [activityDays, activityPairs, filteredAgents.length, flowInstance, graph, graphMode, loading, openRequests, profiledAgents, query, selectedAgent, selectedAgentId, selectedLinkId, team, viewMode]);

  const handleGraphNodesChange = (changes: NodeChange[]) => {
    setNodePositions((previous) => {
      let next = previous;
      for (const change of changes) {
        if (change.type === "position" && change.position) {
          if (next === previous) next = { ...previous };
          next[`${graphMode}:${change.id}`] = change.position;
        }
      }
      return next;
    });
  };

  const inspector = (
    <TeamInspector
      team={team}
      agent={selectedAgent}
      organization={selectedOrganization}
      collaboration={selectedCollaboration}
      activity={selectedActivity}
      adjacentOrganization={adjacentOrganization}
      adjacentCollaboration={adjacentCollaboration}
      adjacentObserved={adjacentObserved}
      onError={onError}
      onMessageAgent={onMessageAgent}
      onScheduleAgent={onScheduleAgent}
      onOpenMessages={onOpenMessages}
      onSelectAgent={selectAgent}
      onSelectOrganization={(relationship) => {
        setSelectedAgentId("");
        setSelectedLinkId(organizationLinkId(relationship));
      }}
      onSelectCollaboration={(relationship) => {
        setSelectedAgentId("");
        setSelectedLinkId(collaborationLinkId(relationship));
      }}
      onSaveProfile={saveProfile}
      onCreateRelationship={createRelationship}
      onUpdateRelationship={updateRelationship}
      onDeleteRelationship={deleteRelationship}
      onCreateOrganizationRelationship={createOrganizationRelationship}
      onUpdateOrganizationRelationship={updateOrganizationRelationship}
      onDeleteOrganizationRelationship={deleteOrganizationRelationship}
    />
  );
  const hasInspectorSelection = Boolean(selectedAgent || selectedOrganization || selectedCollaboration || selectedActivity);
  const organizationAgentIds = new Set(team.organizationLinks.flatMap((relationship) => [relationship.parentAgentId, relationship.childAgentId]));
  const unassignedAgents = filteredAgents.filter((agent) => !organizationAgentIds.has(agent.id) && isActiveAgent(agent));
  const graphLayoutClass = viewMode === "organization"
    ? hasInspectorSelection
      ? "lg:grid-cols-[230px_minmax(0,1fr)_360px]"
      : "lg:grid-cols-[230px_minmax(0,1fr)]"
    : hasInspectorSelection
      ? "lg:grid-cols-[minmax(0,1fr)_360px]"
      : "lg:grid-cols-1";

  return (
    <main className="flex min-w-0 flex-1 flex-col bg-background">
      <header className="flex h-14 shrink-0 items-center justify-between border-b border-border bg-card/80 pl-14 pr-3 backdrop-blur md:px-5">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <Network className="size-4 text-primary" />
            <h1 className="truncate font-serif text-xl">Team</h1>
          </div>
          <div className="mt-0.5 hidden font-mono text-[10px] uppercase text-muted-foreground sm:block">
            long-lived domains, relationships and observed work
          </div>
        </div>
        <div className="flex items-center gap-1.5 font-mono text-[10px] text-muted-foreground sm:gap-2 sm:text-[11px]">
          <Stat label="agents" value={team.agents.length} />
          <Stat label="profiled" value={profiledAgents} className="hidden sm:inline-flex" />
          <Stat label="active" value={activeAgents} tone="success" />
          <Stat label="open" value={openRequests} tone="warning" />
        </div>
      </header>

      <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
        <div className="border-b border-border bg-card/45 px-3 py-2.5 sm:px-4">
          <div className="mx-auto grid w-full max-w-[1500px] grid-cols-[minmax(0,1fr)_minmax(0,1fr)_2.25rem] items-center gap-2 sm:flex sm:flex-wrap">
            <div className="col-span-3 flex h-9 w-full max-w-full overflow-x-auto rounded-md bg-muted/60 p-1 text-[12px] font-medium sm:w-auto">
              <ModeButton active={viewMode === "organization"} icon={<GitFork className="size-3.5" />} label="Organization" shortLabel="Org" onClick={() => { setViewMode("organization"); setSelectedLinkId(""); }} />
              <ModeButton active={viewMode === "collaboration"} icon={<Link2 className="size-3.5" />} label="Collaboration" shortLabel="Collab" onClick={() => { setViewMode("collaboration"); setSelectedLinkId(""); }} />
              <ModeButton active={viewMode === "activity"} icon={<Activity className="size-3.5" />} label="Activity" onClick={() => { setViewMode("activity"); setSelectedLinkId(""); }} />
              <ModeButton active={viewMode === "directory"} icon={<List className="size-3.5" />} label="Directory" shortLabel="Agents" onClick={() => { setViewMode("directory"); setSelectedLinkId(""); }} />
            </div>
            <div className="relative col-span-3 min-w-0 sm:min-w-[190px] sm:flex-1">
              <Search className="pointer-events-none absolute left-3 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
              <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="search agents, domains and relationships" className="h-9 w-full rounded-md bg-background pl-9 pr-3 text-[13px] outline-none ring-1 ring-border focus:ring-ring/25" />
            </div>
            {viewMode === "activity" && (
              <select value={activityDays} onChange={(event) => setActivityDays(Number(event.target.value))} aria-label="Activity window" className="h-9 min-w-0 rounded-md bg-background px-2 text-[12px] outline-none ring-1 ring-border sm:w-auto">
                <option value={1}>24 hours</option><option value={7}>7 days</option><option value={30}>30 days</option><option value={0}>All time</option>
              </select>
            )}
            <select value={selectedAgentId} onChange={(event) => { setSelectedAgentId(event.target.value); setSelectedLinkId(""); }} aria-label="Focus agent" className={`h-9 min-w-0 rounded-md bg-background px-2 text-[12px] outline-none ring-1 ring-border focus:ring-ring/25 sm:min-w-[145px] sm:max-w-[210px] ${viewMode === "activity" ? "" : "col-span-2"}`}>
              <option value="">all agents</option>
              {team.agents.map((agent) => <option key={agent.id} value={agent.id}>{agent.name}</option>)}
            </select>
            <button onClick={() => refresh().catch((err: Error) => onError(err.message))} title="Refresh team" className="flex size-9 items-center justify-center rounded-md border border-border bg-background text-muted-foreground hover:text-foreground">
              <RefreshCw className={`size-3.5 ${loading ? "animate-spin" : ""}`} />
            </button>
          </div>
        </div>

        {viewMode !== "directory" ? (
          <div className={`grid min-h-0 flex-1 grid-cols-1 overflow-y-auto lg:overflow-hidden ${graphLayoutClass}`}>
            {viewMode === "organization" && (
              <aside className="min-h-0 border-b border-border bg-card/35 p-3 lg:overflow-y-auto lg:border-b-0 lg:border-r">
                <SectionTitle icon={<Network className="size-3.5" />} label="Unassigned" count={unassignedAgents.length} />
                <div className="mt-3 space-y-1">
                  {unassignedAgents.map((agent) => <UnassignedAgent key={agent.id} agent={agent} selected={selectedAgentId === agent.id} onSelect={() => selectAgent(agent.id)} />)}
                  {unassignedAgents.length === 0 && <p className="px-2 py-4 text-[11px] text-muted-foreground">Every Agent belongs to the organization map.</p>}
                </div>
              </aside>
            )}
            <section className="relative min-h-[460px] border-b border-border bg-background lg:min-h-0 lg:border-b-0 lg:border-r">
              <ReactFlow nodes={graph.nodes} edges={graph.edges} nodeTypes={nodeTypes} fitView fitViewOptions={{ padding: 0.22 }} minZoom={0.35} maxZoom={1.3} nodesDraggable nodesConnectable={false} elementsSelectable onInit={setFlowInstance} onNodesChange={handleGraphNodesChange} onNodeClick={(_, node) => selectAgent(node.id)} onEdgeClick={(_, edge) => { setSelectedAgentId(""); setSelectedLinkId(edge.id); }} proOptions={{ hideAttribution: true }} className="bg-background">
                <Background gap={22} size={1} color="rgba(120, 113, 108, 0.16)" />
                <Controls showInteractive={false} />
              </ReactFlow>
              {graph.nodes.length === 0 && <GraphEmptyState mode={viewMode} focused={Boolean(selectedAgentId)} />}
            </section>
            {hasInspectorSelection && inspector}
          </div>
        ) : (
          <div className="min-h-0 flex-1 overflow-x-hidden overflow-y-auto">
            <div className={`mx-auto grid max-w-[1500px] items-start gap-5 p-4 lg:p-5 ${hasInspectorSelection ? "lg:grid-cols-[minmax(0,1fr)_380px]" : "grid-cols-1"}`}>
              <section className="min-w-0 space-y-3">
                <SectionTitle icon={<Network className="size-3.5" />} label="Agent directory" count={filteredAgents.length} />
                <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
                  {filteredAgents.map((agent) => <AgentListCard key={agent.id} agent={agent} selected={selectedAgentId === agent.id} onSelect={() => selectAgent(agent.id)} onMessage={() => onMessageAgent(agent.name)} />)}
                </div>
                {filteredAgents.length === 0 && <EmptyState text="No agents match this view." />}
              </section>
              {hasInspectorSelection && <div className="min-w-0 lg:sticky lg:top-0 lg:max-h-[calc(100vh-9rem)] lg:overflow-y-auto">{inspector}</div>}
            </div>
          </div>
        )}
      </div>
    </main>
  );
}

function TeamInspector({
  team,
  agent,
  organization,
  collaboration,
  activity,
  adjacentOrganization,
  adjacentCollaboration,
  adjacentObserved,
  onError,
  onMessageAgent,
  onScheduleAgent,
  onOpenMessages,
  onSelectAgent,
  onSelectOrganization,
  onSelectCollaboration,
  onSaveProfile,
  onCreateRelationship,
  onUpdateRelationship,
  onDeleteRelationship,
  onCreateOrganizationRelationship,
  onUpdateOrganizationRelationship,
  onDeleteOrganizationRelationship,
}: {
  team: TeamView;
  agent: TeamAgent | null;
  organization: OrganizationRelationship | null;
  collaboration: TeamRelationship | null;
  activity: ActivityPair | null;
  adjacentOrganization: OrganizationRelationship[];
  adjacentCollaboration: TeamRelationship[];
  adjacentObserved: TeamObservedLink[];
  onError: (message: string) => void;
  onMessageAgent: (name: string) => void;
  onScheduleAgent: (name: string) => void;
  onOpenMessages: (agentA: string, agentB: string) => void;
  onSelectAgent: (id: string) => void;
  onSelectOrganization: (relationship: OrganizationRelationship) => void;
  onSelectCollaboration: (relationship: TeamRelationship) => void;
  onSaveProfile: (agent: TeamAgent, profile: Pick<AgentProfile, "identity" | "domain" | "scope">) => Promise<void>;
  onCreateRelationship: (from: string, to: string, description: string) => Promise<void>;
  onUpdateRelationship: (relationship: TeamRelationship, description: string) => Promise<void>;
  onDeleteRelationship: (relationship: TeamRelationship) => Promise<void>;
  onCreateOrganizationRelationship: (parent: string, child: string, description: string) => Promise<void>;
  onUpdateOrganizationRelationship: (relationship: OrganizationRelationship, description: string) => Promise<void>;
  onDeleteOrganizationRelationship: (relationship: OrganizationRelationship) => Promise<void>;
}) {
  return (
    <aside className="min-h-0 overflow-y-auto bg-card/35 p-4">
      <SectionTitle icon={<Network className="size-3.5" />} label="Inspector" />
      <div className="mt-3">
        {agent ? (
          <AgentInspector
            team={team}
            agent={agent}
            organization={adjacentOrganization}
            collaborations={adjacentCollaboration}
            observed={adjacentObserved}
            onError={onError}
            onMessage={() => onMessageAgent(agent.name)}
            onSchedule={() => onScheduleAgent(agent.name)}
            onSelectAgent={onSelectAgent}
            onSelectOrganization={onSelectOrganization}
            onSelectRelationship={onSelectCollaboration}
            onSaveProfile={onSaveProfile}
            onCreateRelationship={onCreateRelationship}
            onCreateOrganizationRelationship={onCreateOrganizationRelationship}
          />
        ) : organization ? (
          <OrganizationRelationshipInspector relationship={organization} onError={onError} onUpdate={onUpdateOrganizationRelationship} onDelete={onDeleteOrganizationRelationship} />
        ) : collaboration ? (
          <RelationshipInspector relationship={collaboration} onError={onError} onUpdate={onUpdateRelationship} onDelete={onDeleteRelationship} />
        ) : activity ? (
          <ActivityInspector pair={activity} onOpenMessages={onOpenMessages} />
        ) : (
          <EmptyState text="Select an agent card or relationship." />
        )}
      </div>
    </aside>
  );
}

function AgentInspector({
  team,
  agent,
  organization,
  collaborations,
  observed,
  onError,
  onMessage,
  onSchedule,
  onSelectAgent,
  onSelectOrganization,
  onSelectRelationship,
  onSaveProfile,
  onCreateRelationship,
  onCreateOrganizationRelationship,
}: {
  team: TeamView;
  agent: TeamAgent;
  organization: OrganizationRelationship[];
  collaborations: TeamRelationship[];
  observed: TeamObservedLink[];
  onError: (message: string) => void;
  onMessage: () => void;
  onSchedule: () => void;
  onSelectAgent: (id: string) => void;
  onSelectOrganization: (relationship: OrganizationRelationship) => void;
  onSelectRelationship: (relationship: TeamRelationship) => void;
  onSaveProfile: (agent: TeamAgent, profile: Pick<AgentProfile, "identity" | "domain" | "scope">) => Promise<void>;
  onCreateRelationship: (from: string, to: string, description: string) => Promise<void>;
  onCreateOrganizationRelationship: (parent: string, child: string, description: string) => Promise<void>;
}) {
  const [editing, setEditing] = useState(false);
  const [saving, setSaving] = useState(false);
  const [identity, setIdentity] = useState(agent.profile.identity || "");
  const [domain, setDomain] = useState(agent.profile.domain || "");
  const [scope, setScope] = useState(agent.profile.scope || "");
  const [addingLink, setAddingLink] = useState(false);
  const [target, setTarget] = useState("");
  const [description, setDescription] = useState("");
  const [addingOrganization, setAddingOrganization] = useState(false);
  const [organizationDirection, setOrganizationDirection] = useState<"reports-to" | "manages">("reports-to");
  const [organizationTarget, setOrganizationTarget] = useState("");
  const [organizationDescription, setOrganizationDescription] = useState("");

  useEffect(() => {
    setEditing(false);
    setIdentity(agent.profile.identity || "");
    setDomain(agent.profile.domain || "");
    setScope(agent.profile.scope || "");
    setAddingLink(false);
    setTarget("");
    setDescription("");
    setAddingOrganization(false);
    setOrganizationTarget("");
    setOrganizationDescription("");
  }, [agent.id, agent.profile.version]);

  const editable = isActiveAgent(agent);
  const targets = team.agents.filter((item) => item.id !== agent.id && isActiveAgent(item));
  const parentRelationship = organization.find((relationship) => relationship.childAgentId === agent.id) || null;
  const directReports = organization.filter((relationship) => relationship.parentAgentId === agent.id);

  const save = async () => {
    setSaving(true);
    try {
      await onSaveProfile(agent, { identity, domain, scope });
      setEditing(false);
    } catch (err: any) {
      onError(err.message);
    } finally {
      setSaving(false);
    }
  };

  const addRelationship = async () => {
    if (!target || !description.trim()) return;
    setSaving(true);
    try {
      await onCreateRelationship(agent.id, target, description.trim());
      setAddingLink(false);
    } catch (err: any) {
      onError(err.message);
    } finally {
      setSaving(false);
    }
  };

  const addOrganizationRelationship = async () => {
    if (!organizationTarget || !organizationDescription.trim()) return;
    setSaving(true);
    try {
      const parent = organizationDirection === "reports-to" ? organizationTarget : agent.id;
      const child = organizationDirection === "reports-to" ? agent.id : organizationTarget;
      await onCreateOrganizationRelationship(parent, child, organizationDescription.trim());
      setAddingOrganization(false);
    } catch (err: any) {
      onError(err.message);
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="flex flex-col gap-5">
      <section className="order-1 border-b border-border pb-4">
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <div className="truncate text-[16px] font-semibold text-foreground">{agent.name}</div>
            <div className="mt-1 truncate font-mono text-[10px] text-muted-foreground" title={agent.id}>{agent.id}</div>
          </div>
          <StatusBadge status={teamAgentStatus(agent)} />
        </div>
        <div className="mt-3 grid grid-cols-4 gap-2">
          <Metric label="in" value={agent.messageIn} />
          <Metric label="out" value={agent.messageOut} />
          <Metric label="open" value={agent.openIn} tone={agent.openIn > 0 ? "warning" : "muted"} />
          <Metric label="links" value={organization.length + collaborations.length} />
        </div>
        {editable && (
          <div className="mt-3 flex gap-2">
            <CommandButton icon={<Send className="size-3.5" />} label="Message" onClick={onMessage} />
            <CommandButton icon={<CalendarClock className="size-3.5" />} label="Schedule" onClick={onSchedule} />
          </div>
        )}
      </section>

      <section className="order-3 border-b border-border pb-4">
        <div className="mb-3 flex items-center justify-between gap-2">
          <div>
            <div className="text-[12px] font-semibold uppercase text-muted-foreground">Collaboration profile</div>
            <div className="mt-0.5 font-mono text-[10px] text-muted-foreground">version {agent.profile.version}</div>
          </div>
          {editable && !editing && (
            <button onClick={() => setEditing(true)} title="Edit profile" className="flex size-8 items-center justify-center rounded-md border border-border bg-background text-muted-foreground hover:text-foreground">
              <Edit3 className="size-3.5" />
            </button>
          )}
        </div>
        {editing ? (
          <div className="space-y-3">
            <ProfileField label="Identity" value={identity} onChange={setIdentity} rows={3} placeholder="Who is this long-lived agent?" />
            <ProfileField label="Domain" value={domain} onChange={setDomain} rows={4} placeholder="What enduring subject does this agent maintain?" />
            <ProfileField label="Scope" value={scope} onChange={setScope} rows={6} placeholder="What does it own, decide, and explicitly not own?" />
            <div className="flex justify-end gap-2">
              <button onClick={() => setEditing(false)} className="flex h-8 items-center gap-1.5 rounded-md border border-border px-3 text-[12px] text-muted-foreground hover:text-foreground"><X className="size-3.5" />Cancel</button>
              <button disabled={saving} onClick={save} className="flex h-8 items-center gap-1.5 rounded-md bg-primary px-3 text-[12px] font-medium text-primary-foreground disabled:opacity-50"><Save className="size-3.5" />Save</button>
            </div>
          </div>
        ) : agent.profile.version > 0 ? (
          <div className="space-y-3">
            <ProfileSection label="Identity" value={agent.profile.identity} />
            <ProfileSection label="Domain" value={agent.profile.domain} />
            <ProfileSection label="Scope" value={agent.profile.scope} />
          </div>
        ) : (
          <p className="text-[12px] leading-5 text-muted-foreground">No collaboration profile has been declared.</p>
        )}
      </section>

      <section className="order-2 border-b border-border pb-4">
        <div className="mb-3 flex items-center justify-between">
          <div className="text-[12px] font-semibold uppercase text-muted-foreground">Organization</div>
          {editable && targets.length > 0 && !addingOrganization && (
            <button onClick={() => { setOrganizationDirection(parentRelationship ? "manages" : "reports-to"); setAddingOrganization(true); }} title="Add organization relationship" className="flex size-8 items-center justify-center rounded-md border border-border bg-background text-muted-foreground hover:text-foreground"><Plus className="size-3.5" /></button>
          )}
        </div>
        {addingOrganization && (
          <div className="mb-3 space-y-2 rounded-md border border-border bg-background p-3">
            <select value={organizationDirection} onChange={(event) => setOrganizationDirection(event.target.value as "reports-to" | "manages")} className="h-9 w-full rounded-md bg-background px-2 text-[12px] outline-none ring-1 ring-border">
              {!parentRelationship && <option value="reports-to">Reports to</option>}
              <option value="manages">Manages</option>
            </select>
            <select value={organizationTarget} onChange={(event) => setOrganizationTarget(event.target.value)} className="h-9 w-full rounded-md bg-background px-2 text-[12px] outline-none ring-1 ring-border">
              <option value="">select agent</option>
              {targets.map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}
            </select>
            <textarea value={organizationDescription} onChange={(event) => setOrganizationDescription(event.target.value)} rows={3} placeholder="Describe the durable responsibility boundary" className="w-full resize-y rounded-md bg-background p-2 text-[12px] leading-5 outline-none ring-1 ring-border focus:ring-ring/25" />
            <div className="flex justify-end gap-2">
              <button onClick={() => setAddingOrganization(false)} className="h-8 rounded-md border border-border px-3 text-[12px] text-muted-foreground">Cancel</button>
              <button disabled={!organizationTarget || !organizationDescription.trim() || saving} onClick={addOrganizationRelationship} className="h-8 rounded-md bg-primary px-3 text-[12px] font-medium text-primary-foreground disabled:opacity-50">Add</button>
            </div>
          </div>
        )}
        <div className="space-y-2">
          {parentRelationship && (
            <button onClick={() => onSelectOrganization(parentRelationship)} className="block w-full border-l-2 border-foreground/45 py-1 pl-3 text-left hover:border-foreground">
              <div className="text-[10px] uppercase text-muted-foreground">Reports to</div>
              <div className="mt-0.5 text-[12px] font-semibold">{parentRelationship.parent}</div>
              <div className="mt-0.5 line-clamp-2 text-[11px] leading-4 text-muted-foreground">{parentRelationship.description}</div>
            </button>
          )}
          {directReports.map((relationship) => (
            <button key={relationship.id} onClick={() => onSelectOrganization(relationship)} className="block w-full border-l-2 border-foreground/25 py-1 pl-3 text-left hover:border-foreground">
              <div className="text-[10px] uppercase text-muted-foreground">Direct report</div>
              <div className="mt-0.5 text-[12px] font-semibold">{relationship.child}</div>
              <div className="mt-0.5 line-clamp-2 text-[11px] leading-4 text-muted-foreground">{relationship.description}</div>
            </button>
          ))}
          {!parentRelationship && directReports.length === 0 && <p className="text-[12px] text-muted-foreground">Unassigned in the organization.</p>}
        </div>
      </section>

      <section className="order-4 border-b border-border pb-4">
        <div className="mb-3 flex items-center justify-between">
          <div className="text-[12px] font-semibold uppercase text-muted-foreground">Collaboration</div>
          {editable && targets.length > 0 && !addingLink && (
            <button onClick={() => setAddingLink(true)} title="Add relationship" className="flex size-8 items-center justify-center rounded-md border border-border bg-background text-muted-foreground hover:text-foreground"><Plus className="size-3.5" /></button>
          )}
        </div>
        {addingLink && (
          <div className="mb-3 space-y-2 rounded-md border border-primary/20 bg-background p-3">
            <select value={target} onChange={(event) => setTarget(event.target.value)} className="h-9 w-full rounded-md bg-background px-2 text-[12px] outline-none ring-1 ring-border">
              <option value="">select related agent</option>
              {targets.map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}
            </select>
            <textarea value={description} onChange={(event) => setDescription(event.target.value)} rows={4} placeholder="Describe the long-term collaboration boundary" className="w-full resize-y rounded-md bg-background p-2 text-[12px] leading-5 outline-none ring-1 ring-border focus:ring-ring/25" />
            <div className="flex justify-end gap-2">
              <button onClick={() => setAddingLink(false)} className="h-8 rounded-md border border-border px-3 text-[12px] text-muted-foreground">Cancel</button>
              <button disabled={!target || !description.trim() || saving} onClick={addRelationship} className="h-8 rounded-md bg-primary px-3 text-[12px] font-medium text-primary-foreground disabled:opacity-50">Add</button>
            </div>
          </div>
        )}
        <div className="space-y-2">
          {collaborations.map((relationship) => {
            const otherId = relationship.fromAgentId === agent.id ? relationship.toAgentId : relationship.fromAgentId;
            const otherName = relationship.fromAgentId === agent.id ? relationship.to : relationship.from;
            return (
              <button key={relationship.id} onClick={() => onSelectRelationship(relationship)} className="block w-full border-l-2 border-primary/40 py-1 pl-3 text-left hover:border-primary">
                <div className="text-[12px] font-semibold text-foreground">{relationship.from} -&gt; {relationship.to}</div>
                <div className="mt-0.5 line-clamp-2 text-[11px] leading-4 text-muted-foreground">{relationship.description}</div>
                <span onClick={(event) => { event.stopPropagation(); onSelectAgent(otherId); }} className="mt-1 inline-block text-[10px] text-primary hover:underline">open {otherName}</span>
              </button>
            );
          })}
          {collaborations.length === 0 && <p className="text-[12px] text-muted-foreground">No declared collaboration.</p>}
        </div>
      </section>

      <section className="order-5">
        <div className="mb-3 text-[12px] font-semibold uppercase text-muted-foreground">Recent activity</div>
        <div className="space-y-2">
          {observed.slice(0, 6).map((link) => (
            <div key={observedLinkId(link)} className="border-l-2 border-border py-1 pl-3">
              <div className="text-[12px] font-medium">{link.from} -&gt; {link.to}</div>
              <div className="mt-0.5 text-[11px] text-muted-foreground">{link.messageCount} messages, {link.replyCount} replies, {link.openCount} open</div>
            </div>
          ))}
          {observed.length === 0 && <p className="text-[12px] text-muted-foreground">No message relationship observed yet.</p>}
        </div>
      </section>
    </div>
  );
}

function OrganizationRelationshipInspector({
  relationship,
  onError,
  onUpdate,
  onDelete,
}: {
  relationship: OrganizationRelationship;
  onError: (message: string) => void;
  onUpdate: (relationship: OrganizationRelationship, description: string) => Promise<void>;
  onDelete: (relationship: OrganizationRelationship) => Promise<void>;
}) {
  const [editing, setEditing] = useState(false);
  const [description, setDescription] = useState(relationship.description);
  const [saving, setSaving] = useState(false);
  useEffect(() => {
    setDescription(relationship.description);
    setEditing(false);
  }, [relationship.id, relationship.updatedAt]);
  const run = async (action: () => Promise<void>) => {
    setSaving(true);
    try { await action(); } catch (err: any) { onError(err.message); } finally { setSaving(false); }
  };
  return (
    <div className="space-y-4">
      <section className="border-b border-border pb-4">
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <div className="text-[15px] font-semibold">{relationship.parent} <span className="text-muted-foreground">manages</span> {relationship.child}</div>
            <div className="mt-1 truncate font-mono text-[10px] text-muted-foreground">{relationship.id}</div>
          </div>
          <span className="rounded bg-foreground px-2 py-1 text-[10px] font-medium text-background">organization</span>
        </div>
      </section>
      <section>
        <div className="mb-2 flex items-center justify-between">
          <div className="text-[12px] font-semibold uppercase text-muted-foreground">Responsibility boundary</div>
          {!editing && <button onClick={() => setEditing(true)} title="Edit organization relationship" className="flex size-8 items-center justify-center rounded-md border border-border bg-background text-muted-foreground"><Edit3 className="size-3.5" /></button>}
        </div>
        {editing ? (
          <div className="space-y-3">
            <textarea value={description} onChange={(event) => setDescription(event.target.value)} rows={8} className="w-full resize-y rounded-md bg-background p-2.5 text-[13px] leading-5 outline-none ring-1 ring-border focus:ring-ring/25" />
            <div className="flex items-center justify-between gap-2">
              <button disabled={saving} onClick={() => { if (window.confirm("Remove this organization relationship?")) run(() => onDelete(relationship)); }} className="flex h-8 items-center gap-1.5 rounded-md border border-destructive/30 px-3 text-[12px] text-destructive"><Trash2 className="size-3.5" />Remove</button>
              <div className="flex gap-2">
                <button onClick={() => setEditing(false)} className="h-8 rounded-md border border-border px-3 text-[12px] text-muted-foreground">Cancel</button>
                <button disabled={!description.trim() || saving} onClick={() => run(async () => { await onUpdate(relationship, description.trim()); setEditing(false); })} className="flex h-8 items-center gap-1.5 rounded-md bg-primary px-3 text-[12px] font-medium text-primary-foreground disabled:opacity-50"><Save className="size-3.5" />Save</button>
              </div>
            </div>
          </div>
        ) : <p className="whitespace-pre-wrap text-[13px] leading-6 text-foreground">{relationship.description}</p>}
      </section>
    </div>
  );
}

function RelationshipInspector({
  relationship,
  onError,
  onUpdate,
  onDelete,
}: {
  relationship: TeamRelationship;
  onError: (message: string) => void;
  onUpdate: (relationship: TeamRelationship, description: string) => Promise<void>;
  onDelete: (relationship: TeamRelationship) => Promise<void>;
}) {
  const [editing, setEditing] = useState(false);
  const [description, setDescription] = useState(relationship.description);
  const [saving, setSaving] = useState(false);
  useEffect(() => {
    setDescription(relationship.description);
    setEditing(false);
  }, [relationship.id, relationship.updatedAt]);

  const run = async (action: () => Promise<void>) => {
    setSaving(true);
    try {
      await action();
    } catch (err: any) {
      onError(err.message);
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="space-y-4">
      <section className="border-b border-border pb-4">
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <div className="text-[15px] font-semibold">{relationship.from} -&gt; {relationship.to}</div>
            <div className="mt-1 truncate font-mono text-[10px] text-muted-foreground">{relationship.id}</div>
          </div>
          <span className="rounded-md bg-primary/10 px-2 py-1 text-[10px] font-medium text-primary">declared</span>
        </div>
      </section>
      <section>
        <div className="mb-2 flex items-center justify-between">
          <div className="text-[12px] font-semibold uppercase text-muted-foreground">Relationship</div>
          {!editing && <button onClick={() => setEditing(true)} title="Edit relationship" className="flex size-8 items-center justify-center rounded-md border border-border bg-background text-muted-foreground"><Edit3 className="size-3.5" /></button>}
        </div>
        {editing ? (
          <div className="space-y-3">
            <textarea value={description} onChange={(event) => setDescription(event.target.value)} rows={8} className="w-full resize-y rounded-md bg-background p-2.5 text-[13px] leading-5 outline-none ring-1 ring-border focus:ring-ring/25" />
            <div className="flex items-center justify-between gap-2">
              <button disabled={saving} onClick={() => { if (window.confirm("Delete this declared relationship?")) run(() => onDelete(relationship)); }} className="flex h-8 items-center gap-1.5 rounded-md border border-destructive/30 px-3 text-[12px] text-destructive"><Trash2 className="size-3.5" />Delete</button>
              <div className="flex gap-2">
                <button onClick={() => setEditing(false)} className="h-8 rounded-md border border-border px-3 text-[12px] text-muted-foreground">Cancel</button>
                <button disabled={!description.trim() || saving} onClick={() => run(async () => { await onUpdate(relationship, description.trim()); setEditing(false); })} className="flex h-8 items-center gap-1.5 rounded-md bg-primary px-3 text-[12px] font-medium text-primary-foreground disabled:opacity-50"><Save className="size-3.5" />Save</button>
              </div>
            </div>
          </div>
        ) : (
          <p className="whitespace-pre-wrap text-[13px] leading-6 text-foreground">{relationship.description}</p>
        )}
      </section>
    </div>
  );
}

function ActivityInspector({ pair, onOpenMessages }: { pair: ActivityPair; onOpenMessages: (agentA: string, agentB: string) => void }) {
  return (
    <div className="space-y-4">
      <section className="border-b border-border pb-4">
        <div className="text-[15px] font-semibold">{pair.agentA} <span className="text-muted-foreground">and</span> {pair.agentB}</div>
        <div className="mt-1 text-[11px] text-muted-foreground">Activity evidence from Messages, not an organization relationship.</div>
        <div className="mt-3 grid grid-cols-4 gap-2">
          <Metric label="messages" value={pair.messageCount} />
          <Metric label="replies" value={pair.replyCount} />
          <Metric label="open" value={pair.openCount} tone={pair.openCount > 0 ? "warning" : "muted"} />
          <Metric label="failed" value={pair.failedCount} tone={pair.failedCount > 0 ? "danger" : "muted"} />
        </div>
      </section>
      <section>
        <div className="mb-2 text-[12px] font-semibold uppercase text-muted-foreground">Directions</div>
        <div className="space-y-1.5">
          {pair.directions.map((direction) => <div key={observedLinkId(direction)} className="flex items-center justify-between gap-2 border-l-2 border-border py-1 pl-3 text-[11px]"><span>{direction.from} -&gt; {direction.to}</span><span className="font-mono text-muted-foreground">{direction.messageCount} sent · {direction.replyCount} replies</span></div>)}
        </div>
      </section>
      <section>
        <div className="mb-2 text-[12px] font-semibold uppercase text-muted-foreground">Recent subjects</div>
        <div className="space-y-1.5">
          {pair.subjects.map((subject) => <div key={subject} className="rounded-md bg-muted px-2.5 py-2 text-[12px] text-muted-foreground">{subject}</div>)}
        </div>
      </section>
      <button type="button" onClick={() => onOpenMessages(pair.agentA, pair.agentB)} className="flex h-9 w-full items-center justify-center gap-2 rounded-md border border-border bg-background px-3 text-[12px] font-medium text-foreground hover:bg-muted">
        <MessageSquare className="size-3.5" />Open message history
      </button>
    </div>
  );
}

function buildTeamGraph(
  team: TeamView,
  activityPairs: ActivityPair[],
  mode: GraphViewMode,
  query: string,
  selectedAgentId: string,
  selectedLinkId: string,
  nodePositions: NodePositions,
  onMessage: (name: string) => void,
): TeamGraph {
  const q = query.trim().toLowerCase();
  const agentsById = new Map(team.agents.map((agent) => [agent.id, agent]));
  const visibleAgentIds = new Set<string>();
  let organizationLinks: OrganizationRelationship[] = [];
  let collaborationLinks: TeamRelationship[] = [];
  let visibleActivityPairs: ActivityPair[] = [];

  if (mode === "organization") {
    organizationLinks = team.organizationLinks.filter((link) => {
      if (selectedAgentId && link.parentAgentId !== selectedAgentId && link.childAgentId !== selectedAgentId) return false;
      if (!q) return true;
      return [link.parent, link.child, link.description].join(" ").toLowerCase().includes(q)
        || agentSearchText(agentsById.get(link.parentAgentId) || externalAgent(link.parentAgentId)).includes(q)
        || agentSearchText(agentsById.get(link.childAgentId) || externalAgent(link.childAgentId)).includes(q);
    });
    for (const link of organizationLinks) {
      visibleAgentIds.add(link.parentAgentId);
      visibleAgentIds.add(link.childAgentId);
    }
  } else if (mode === "collaboration") {
    collaborationLinks = team.collaborationLinks.filter((link) => {
      if (selectedAgentId && link.fromAgentId !== selectedAgentId && link.toAgentId !== selectedAgentId) return false;
      return !q || [link.from, link.to, link.description].join(" ").toLowerCase().includes(q);
    });
    for (const link of collaborationLinks) {
      visibleAgentIds.add(link.fromAgentId);
      visibleAgentIds.add(link.toAgentId);
    }
  } else {
    const candidates = activityPairs.filter((pair) => {
      if (selectedAgentId && pair.agentAId !== selectedAgentId && pair.agentBId !== selectedAgentId) return false;
      return !q || [pair.agentA, pair.agentB, ...pair.subjects].join(" ").toLowerCase().includes(q);
    });
    visibleActivityPairs = strongestActivityPairs(candidates, selectedLinkId, q || selectedAgentId ? candidates.length : DEFAULT_ACTIVITY_EDGE_LIMIT);
    for (const pair of visibleActivityPairs) {
      visibleAgentIds.add(pair.agentAId);
      visibleAgentIds.add(pair.agentBId);
    }
  }
  if (q) {
    for (const agent of team.agents) {
      if (agentSearchText(agent).includes(q)) visibleAgentIds.add(agent.id);
    }
  }

  const ids = Array.from(visibleAgentIds).sort((a, b) => {
    if (a === selectedAgentId) return -1;
    if (b === selectedAgentId) return 1;
    const degreeA = graphDegree(mode, a, organizationLinks, collaborationLinks, visibleActivityPairs);
    const degreeB = graphDegree(mode, b, organizationLinks, collaborationLinks, visibleActivityPairs);
    return degreeB - degreeA || (agentsById.get(a)?.name || a).localeCompare(agentsById.get(b)?.name || b);
  });
  const positions = mode === "organization" ? computeOrganizationPositions(ids, organizationLinks) : computePositions(ids);
  const organizationChildren = new Set(organizationLinks.map((link) => link.childAgentId));
  const nodes: Node<AgentNodeData>[] = ids.map((id) => {
    const agent = agentsById.get(id) || externalAgent(id);
    return {
      id,
      type: "agentCard",
      position: nodePositions[`${mode}:${id}`] || positions.get(id) || { x: 0, y: 0 },
      selected: selectedAgentId === id,
      zIndex: 2,
      data: {
        agent,
        selected: selectedAgentId === id,
        perspective: mode,
        organizationRole: mode === "organization" ? organizationChildren.has(id) ? "member" : "root" : undefined,
        onMessage,
      },
    };
  });

  const organizationEdges: Edge[] = organizationLinks.map((link) => ({
    id: organizationLinkId(link),
    source: link.parentAgentId,
    target: link.childAgentId,
    type: "smoothstep",
    selected: selectedLinkId === organizationLinkId(link),
    zIndex: selectedLinkId === organizationLinkId(link) ? 1 : 0,
    markerEnd: { type: MarkerType.ArrowClosed, width: 14, height: 14, color: "var(--foreground)" },
    style: { stroke: "var(--foreground)", strokeWidth: selectedLinkId === organizationLinkId(link) ? 3 : 1.6 },
  }));
  const collaborationEdges: Edge[] = collaborationLinks.map((link) => ({
    id: collaborationLinkId(link),
    source: link.fromAgentId,
    target: link.toAgentId,
    type: "smoothstep",
    selected: selectedLinkId === collaborationLinkId(link),
    zIndex: selectedLinkId === collaborationLinkId(link) ? 1 : 0,
    markerEnd: { type: MarkerType.ArrowClosed, width: 14, height: 14, color: "var(--loom-teal)" },
    style: { stroke: "var(--loom-teal)", strokeWidth: selectedLinkId === collaborationLinkId(link) ? 3 : 2, strokeDasharray: "7 4" },
  }));
  const activityEdges: Edge[] = visibleActivityPairs.map((pair) => {
    const warning = pair.failedCount > 0 || pair.openCount > 0 || pair.queuedCount > 0;
    const color = pair.failedCount > 0 ? "var(--destructive)" : warning ? "var(--warning)" : "#918f87";
    return {
      id: pair.id,
      source: pair.agentAId,
      target: pair.agentBId,
      selected: selectedLinkId === pair.id,
      zIndex: selectedLinkId === pair.id ? 1 : 0,
      label: `${pair.messageCount + pair.replyCount}`,
      style: { stroke: color, strokeWidth: selectedLinkId === pair.id ? 3 : Math.min(3, 1.1 + (pair.messageCount + pair.replyCount) * 0.12) },
      labelStyle: { fill: color, fontSize: 10, fontWeight: 700 },
      labelBgStyle: { fill: "#fafaf9", fillOpacity: 0.9 },
      animated: pair.queuedCount > 0,
    };
  });
  const edges = [...organizationEdges, ...collaborationEdges, ...activityEdges];
  return { nodes, edges, visibleAgentIds: ids, visibleEdgeIds: edges.map((edge) => edge.id) };
}

function AgentGraphNode({ data }: NodeProps<Node<AgentNodeData>>) {
  const { agent, selected, perspective, organizationRole, onMessage } = data;
  const domain = firstLine(agent.profile.domain) || firstLine(agent.profile.identity) || "No domain declared";
  return (
    <div className={`w-[240px] rounded-md border bg-card p-3 shadow-card ${selected ? "border-ring/50 ring-2 ring-ring/18" : "border-border"}`}>
      <Handle type="target" position={Position.Left} className="!size-2 !border-border !bg-muted-foreground" />
      <Handle type="source" position={Position.Right} className="!size-2 !border-border !bg-muted-foreground" />
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <span className={`size-2 shrink-0 rounded-full ${teamAgentWorking(agent) ? "bg-success" : "bg-muted-foreground/40"}`} />
            <div className="truncate text-[14px] font-semibold" title={agent.name}>{agent.name}</div>
          </div>
        </div>
        <StatusBadge status={teamAgentStatus(agent)} />
      </div>
      <div className="mt-2 h-9 overflow-hidden text-[11px] leading-[18px] text-foreground/85" title={agent.profile.domain || agent.profile.identity}>{domain}</div>
      <div className="mt-2 flex items-end justify-between gap-3 border-t border-border pt-2">
        <div className="font-mono text-[9px] uppercase text-muted-foreground">{perspective === "organization" ? organizationRole : perspective === "activity" ? `${agent.messageIn + agent.messageOut} messages` : agent.profile.version > 0 ? `profile v${agent.profile.version}` : "no profile"}</div>
        {isActiveAgent(agent) && selected && (
          <button onClick={(event) => { event.stopPropagation(); onMessage(agent.name); }} title="Message agent" className="flex size-7 items-center justify-center rounded-md border border-border bg-background text-muted-foreground hover:text-foreground"><Send className="size-3.5" /></button>
        )}
      </div>
    </div>
  );
}

function AgentListCard({ agent, selected, onSelect, onMessage }: { agent: TeamAgent; selected: boolean; onSelect: () => void; onMessage: () => void }) {
  return (
    <article className={`min-w-0 overflow-hidden rounded-md border bg-card p-4 shadow-card ${selected ? "border-ring/45 ring-1 ring-ring/15" : "border-border"}`}>
      <button onClick={onSelect} className="block w-full text-left">
        <div className="flex items-start justify-between gap-2">
          <div className="min-w-0">
            <div className="truncate text-[15px] font-semibold">{agent.name}</div>
            <div className="mt-1 truncate font-mono text-[10px] text-muted-foreground">{agent.id}</div>
          </div>
          <StatusBadge status={teamAgentStatus(agent)} />
        </div>
        <div className="mt-3 min-h-10 break-words text-[12px] leading-5 text-foreground/85">{firstLine(agent.profile.domain) || firstLine(agent.profile.identity) || "No domain declared"}</div>
        <div className="mt-3 grid grid-cols-3 gap-2">
          <Metric label="msgs" value={agent.messageIn + agent.messageOut} />
          <Metric label="open" value={agent.openIn} tone={agent.openIn > 0 ? "warning" : "muted"} />
          <Metric label="profile" value={agent.profile.version} />
        </div>
      </button>
      {isActiveAgent(agent) && (
        <div className="mt-3 flex justify-end"><CommandButton icon={<Send className="size-3.5" />} label="Message" onClick={onMessage} /></div>
      )}
    </article>
  );
}

function UnassignedAgent({ agent, selected, onSelect }: { agent: TeamAgent; selected: boolean; onSelect: () => void }) {
  return (
    <button onClick={onSelect} className={`flex w-full items-center gap-2 rounded px-2 py-2 text-left ${selected ? "bg-selection text-selection-foreground" : "hover:bg-muted/60"}`}>
      <span className={`size-2 shrink-0 rounded-full ${teamAgentWorking(agent) ? "bg-success" : "bg-muted-foreground/35"}`} />
      <span className="min-w-0 flex-1 truncate text-[12px] font-medium">{agent.name}</span>
      {agent.openIn > 0 && <span className="font-mono text-[9px] text-warning">{agent.openIn} open</span>}
    </button>
  );
}

function GraphEmptyState({ mode, focused }: { mode: GraphViewMode; focused: boolean }) {
  const copy = mode === "organization"
    ? focused
      ? "This Agent is unassigned. Add its parent or a direct report in the Inspector."
      : "No organization relationships yet. Select an Agent from Unassigned and add its parent or direct report in the Inspector."
    : mode === "collaboration"
      ? "No declared collaboration matches this view. Select an Agent from the focus menu to add one."
      : "No message activity exists in this time window.";
  return <div className="pointer-events-none absolute inset-0 flex items-center justify-center p-8"><div className="max-w-md border border-dashed border-border bg-background/90 px-6 py-8 text-center text-[12px] leading-5 text-muted-foreground">{copy}</div></div>;
}

function ProfileField({ label, value, onChange, rows, placeholder }: { label: string; value: string; onChange: (value: string) => void; rows: number; placeholder: string }) {
  return (
    <label className="block">
      <span className="mb-1.5 block text-[11px] font-semibold uppercase text-muted-foreground">{label}</span>
      <textarea value={value} onChange={(event) => onChange(event.target.value)} rows={rows} placeholder={placeholder} className="w-full resize-y rounded-md bg-background p-2.5 text-[12px] leading-5 outline-none ring-1 ring-border focus:ring-ring/25" />
    </label>
  );
}

function ProfileSection({ label, value }: { label: string; value?: string }) {
  if (!value) return null;
  return <div><div className="text-[10px] font-semibold uppercase text-muted-foreground">{label}</div><p className="mt-1 whitespace-pre-wrap text-[12px] leading-5 text-foreground/90">{value}</p></div>;
}

function SectionTitle({ icon, label, count }: { icon: React.ReactNode; label: string; count?: number }) {
  return <div className="flex items-center gap-2 text-[11px] font-semibold uppercase text-muted-foreground">{icon}{label}{count !== undefined && <span className="font-mono text-[10px]">{count}</span>}</div>;
}

function ModeButton({ active, icon, label, shortLabel, onClick }: { active: boolean; icon: React.ReactNode; label: string; shortLabel?: string; onClick: () => void }) {
  return <button onClick={onClick} title={label} className={`flex shrink-0 items-center gap-1.5 rounded px-2.5 ${active ? "bg-background text-foreground shadow-sm" : "text-muted-foreground hover:text-foreground"}`}>{icon}<span className={shortLabel ? "hidden sm:inline" : ""}>{label}</span>{shortLabel && <span className="sm:hidden">{shortLabel}</span>}</button>;
}

function CommandButton({ icon, label, onClick }: { icon: React.ReactNode; label: string; onClick: () => void }) {
  return <button onClick={onClick} className="flex h-8 flex-1 items-center justify-center gap-1.5 rounded-md border border-border bg-background px-2.5 text-[12px] font-medium text-muted-foreground hover:text-foreground">{icon}{label}</button>;
}

function StatusBadge({ status }: { status: string }) {
	const cls = status === "running" || status === "goal active" ? "bg-success/10 text-success" : status === "system" ? "bg-primary/10 text-primary" : "bg-muted text-muted-foreground";
  return <span className={`shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium ${cls}`}>{status}</span>;
}

function teamAgentWorking(agent: TeamAgent) {
  return agent.status === "running";
}

function teamAgentStatus(agent: TeamAgent) {
  if (agent.status === "running") return "running";
  return agent.status || "external";
}

function Stat({ label, value, tone, className = "" }: { label: string; value: number; tone?: "success" | "warning"; className?: string }) {
  const cls = tone === "success" ? "bg-success/10 text-success" : tone === "warning" ? "bg-warning/10 text-warning" : "bg-muted text-muted-foreground";
  return <span title={`${value} ${label}`} aria-label={`${value} ${label}`} className={`items-center gap-1 rounded px-1.5 py-1 ${cls} ${className || "inline-flex"}`}><strong>{value}</strong><span className="hidden sm:inline">{label}</span></span>;
}

function Metric({ label, value, tone = "muted" }: { label: string; value: number; tone?: "muted" | "warning" | "danger" }) {
  const color = tone === "warning" ? "text-warning" : tone === "danger" ? "text-destructive" : "text-foreground";
  return <div className="rounded-md bg-muted/35 px-2 py-1.5"><div className={`font-mono text-[11px] font-semibold ${color}`}>{value}</div><div className="mt-0.5 truncate text-[9px] uppercase text-muted-foreground">{label}</div></div>;
}

function EmptyState({ text }: { text: string }) {
  return <div className="rounded-md border border-dashed border-border px-4 py-8 text-center text-[12px] text-muted-foreground">{text}</div>;
}

function isActiveAgent(agent: TeamAgent) {
  return agent.status !== "external" && agent.status !== "system" && agent.status !== "archived";
}

function agentSearchText(agent: TeamAgent) {
  return [agent.name, agent.id, agent.cwd || "", agent.profile.identity || "", agent.profile.domain || "", agent.profile.scope || ""].join(" ").toLowerCase();
}

function aggregateActivityPairs(links: TeamObservedLink[]): ActivityPair[] {
  const pairs = new Map<string, ActivityPair>();
  for (const link of links) {
    const [agentAId, agentBId] = [link.fromAgentId, link.toAgentId].sort();
    const id = `activity:${agentAId}::${agentBId}`;
    let pair = pairs.get(id);
    if (!pair) {
      const forward = link.fromAgentId === agentAId;
      pair = {
        id, agentAId, agentBId,
        agentA: forward ? link.from : link.to,
        agentB: forward ? link.to : link.from,
        messageCount: 0, replyCount: 0, openCount: 0, queuedCount: 0, failedCount: 0,
        subjects: [], directions: [],
      };
      pairs.set(id, pair);
    }
    pair.messageCount += link.messageCount;
    pair.replyCount += link.replyCount;
    pair.openCount += link.openCount;
    pair.queuedCount += link.queuedCount;
    pair.failedCount += link.failedCount;
    if ((link.lastMessageAt || "") > (pair.lastMessageAt || "")) pair.lastMessageAt = link.lastMessageAt;
    pair.directions.push(link);
    for (const subject of link.subjects) {
      if (!pair.subjects.includes(subject) && pair.subjects.length < 8) pair.subjects.push(subject);
    }
  }
  return [...pairs.values()];
}

function graphDegree(mode: GraphViewMode, id: string, organization: OrganizationRelationship[], collaboration: TeamRelationship[], activity: ActivityPair[]) {
  if (mode === "organization") return organization.filter((link) => link.parentAgentId === id || link.childAgentId === id).length;
  if (mode === "collaboration") return collaboration.filter((link) => link.fromAgentId === id || link.toAgentId === id).length;
  return activity.reduce((sum, pair) => sum + (pair.agentAId === id || pair.agentBId === id ? pair.messageCount + pair.replyCount : 0), 0);
}

function loadNodePositions(): NodePositions {
  try {
    const raw = JSON.parse(window.localStorage.getItem(TEAM_POSITIONS_KEY) || "{}");
    const positions: NodePositions = {};
    for (const [id, value] of Object.entries(raw || {})) {
      const position = value as { x?: unknown; y?: unknown };
      if (typeof position.x === "number" && Number.isFinite(position.x) && typeof position.y === "number" && Number.isFinite(position.y)) {
        positions[id] = { x: position.x, y: position.y };
      }
    }
    return positions;
  } catch {
    return {};
  }
}

function computePositions(ids: string[]) {
  const positions = new Map<string, { x: number; y: number }>();
  const columns = Math.max(1, Math.ceil(Math.sqrt(ids.length)));
  const columnGap = 275;
  const rowGap = 145;
  ids.forEach((id, index) => {
    positions.set(id, {
      x: (index % columns) * columnGap,
      y: Math.floor(index / columns) * rowGap,
    });
  });
  return positions;
}

function computeOrganizationPositions(ids: string[], links: OrganizationRelationship[]) {
  const positions = new Map<string, { x: number; y: number }>();
  const visible = new Set(ids);
  const children = new Map<string, string[]>();
  const hasParent = new Set<string>();
  for (const link of links) {
    if (!visible.has(link.parentAgentId) || !visible.has(link.childAgentId)) continue;
    children.set(link.parentAgentId, [...(children.get(link.parentAgentId) || []), link.childAgentId]);
    hasParent.add(link.childAgentId);
  }
  const roots = ids.filter((id) => !hasParent.has(id));
  const levels: string[][] = [];
  let frontier = roots;
  const seen = new Set<string>();
  while (frontier.length > 0) {
    const level = frontier.filter((id) => !seen.has(id));
    if (level.length === 0) break;
    levels.push(level);
    for (const id of level) seen.add(id);
    frontier = level.flatMap((id) => children.get(id) || []);
  }
  const remainder = ids.filter((id) => !seen.has(id));
  if (remainder.length > 0) levels.push(remainder);
  levels.forEach((level, depth) => {
    level.sort();
    level.forEach((id, index) => positions.set(id, { x: depth * 310, y: index * 145 }));
  });
  return positions;
}

function strongestActivityPairs(pairs: ActivityPair[], selectedLinkId: string, limit: number) {
  const selected = pairs.find((pair) => pair.id === selectedLinkId);
  const ranked = [...pairs].sort((a, b) => {
    const riskA = a.failedCount * 1000 + a.openCount * 100 + a.queuedCount * 50;
    const riskB = b.failedCount * 1000 + b.openCount * 100 + b.queuedCount * 50;
    return riskB - riskA || (b.messageCount + b.replyCount) - (a.messageCount + a.replyCount) || a.id.localeCompare(b.id);
  });
  const result = ranked.slice(0, Math.max(0, limit));
  if (selected && !result.includes(selected)) result.push(selected);
  return result;
}

function graphState(graph: TeamGraph, selectedAgentId: string, selectedLinkId: string) {
  return {
    nodesCount: graph.nodes.length,
    edgesCount: graph.edges.length,
    selectedNodeId: selectedAgentId,
    selectedEdgeId: selectedLinkId,
    visibleNodeIds: graph.visibleAgentIds,
    visibleEdgeIds: graph.visibleEdgeIds,
    positions: Object.fromEntries(graph.nodes.map((node) => [node.id, node.position])),
  };
}

const EMPTY_GRAPH: TeamGraph = { nodes: [], edges: [], visibleAgentIds: [], visibleEdgeIds: [] };

function organizationLinkId(link: Pick<OrganizationRelationship, "id">) {
  return `organization:${link.id}`;
}

function collaborationLinkId(link: Pick<TeamRelationship, "id">) {
  return `collaboration:${link.id}`;
}

function observedLinkId(link: Pick<TeamObservedLink, "fromAgentId" | "toAgentId">) {
  return `observed:${link.fromAgentId}->${link.toAgentId}`;
}

function externalAgent(id: string): TeamAgent {
  return {
    id,
    name: id,
    status: "external",
    source: "relationship participant",
    profile: { agentId: id, version: 0 },
    messageIn: 0,
    messageOut: 0,
    openIn: 0,
    openOut: 0,
    scheduledIn: 0,
  };
}

function firstLine(value?: string) {
  return (value || "").split("\n")[0].trim();
}

function readTeamRouteState() {
  const hash = window.location.hash.slice(1);
  const queryStart = hash.indexOf("?");
  const defaultView: TeamViewMode = "directory";
  if (queryStart < 0) return { agent: "", link: "", query: "", view: defaultView, days: 7 };
  const params = new URLSearchParams(hash.slice(queryStart + 1));
  const rawView = params.get("view");
  const views: TeamViewMode[] = ["organization", "collaboration", "activity", "directory"];
  const view = views.includes(rawView as TeamViewMode) ? rawView as TeamViewMode : rawView === "list" ? "directory" : rawView === "graph" ? "organization" : defaultView;
  const rawDays = Number(params.get("days") || 7);
  return {
    agent: params.get("agent") || "",
    link: params.get("link") || "",
    query: params.get("q") || "",
    view,
    days: [0, 1, 7, 30].includes(rawDays) ? rawDays : 7,
  };
}

function writeTeamRouteState({ agent, link, query, view, days }: { agent: string; link: string; query: string; view: TeamViewMode; days: number }) {
  const params = new URLSearchParams();
  params.set("view", view);
  if (agent) params.set("agent", agent);
  if (link) params.set("link", link);
  if (query.trim()) params.set("q", query.trim());
  if (view === "activity") params.set("days", String(days));
  const value = params.toString();
  const next = `#team${value ? `?${value}` : ""}`;
  if (window.location.hash !== next) window.history.replaceState(null, "", next);
}

function waitForAutomationState() {
  return new Promise((resolve) => window.setTimeout(() => resolve(window.codexLoom?.team?.state?.() || window.codexHub?.team?.state?.() || null), 100));
}

function waitForGraphState() {
  return new Promise((resolve) => window.setTimeout(() => resolve(window.codexLoom?.graph?.state?.() || window.codexHub?.graph?.state?.() || null), 100));
}
