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
  CalendarClock,
  Edit3,
  Link2,
  List,
  Network,
  Plus,
  RefreshCw,
  Save,
  Search,
  Send,
  Share2,
  Trash2,
  X,
} from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import {
  api,
  type AgentProfile,
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
}

type TeamViewMode = "graph" | "list";
type NodePositions = Record<string, { x: number; y: number }>;
const TEAM_POSITIONS_KEY = "codex-loom.team.positions.v2";
const DEFAULT_OBSERVED_EDGE_LIMIT = 18;

type AgentNodeData = {
  agent: TeamAgent;
  selected: boolean;
  onMessage: (name: string) => void;
};

type TeamGraph = {
  nodes: Node<AgentNodeData>[];
  edges: Edge[];
  visibleAgentIds: string[];
  visibleEdgeIds: string[];
};

const EMPTY_TEAM: TeamView = { agents: [], observedLinks: [], explicitLinks: [] };
const nodeTypes = { agentCard: AgentGraphNode };

export function TeamPane({ onError, onMessageAgent, onScheduleAgent }: Props) {
  const route = readTeamRouteState();
  const [team, setTeam] = useState<TeamView>(EMPTY_TEAM);
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

  const refresh = async () => {
    setLoading(true);
    try {
      const data = await api("GET", "/api/team");
      setTeam(data.team || EMPTY_TEAM);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    refresh().catch((err: Error) => onError(err.message));
    const es = new EventSource("/api/events");
    es.onmessage = (event) => {
      try {
        const value = JSON.parse(event.data);
        if (
          value.type === "loom/comms-message" ||
          value.type === "loom/agent-status" ||
          value.type === "loom/agents" ||
          value.type === "loom/profile-updated" ||
          value.type === "loom/team-link-updated" ||
          value.type === "loom/team-link-deleted"
        ) {
          refresh().catch(() => {});
        }
      } catch {
        // Ignore malformed SSE values; the next snapshot refresh repairs state.
      }
    };
    return () => es.close();
  }, []);

  const resolveAgentId = (key: string) => team.agents.find((agent) => agent.id === key || agent.name === key)?.id || key;
  const selectedAgent = team.agents.find((agent) => agent.id === selectedAgentId) || null;
  const selectedExplicit = team.explicitLinks.find((link) => explicitLinkId(link) === selectedLinkId) || null;
  const selectedObserved = team.observedLinks.find((link) => observedLinkId(link) === selectedLinkId) || null;
  const adjacentExplicit = selectedAgent
    ? team.explicitLinks.filter((link) => link.fromAgentId === selectedAgent.id || link.toAgentId === selectedAgent.id)
    : [];
  const adjacentObserved = selectedAgent
    ? team.observedLinks.filter((link) => link.fromAgentId === selectedAgent.id || link.toAgentId === selectedAgent.id)
    : [];

  useEffect(() => {
    if (!selectedAgentId || team.agents.some((agent) => agent.id === selectedAgentId)) return;
    const byName = team.agents.find((agent) => agent.name === selectedAgentId);
    if (byName) setSelectedAgentId(byName.id);
  }, [selectedAgentId, team.agents]);

  const filteredAgents = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return team.agents;
    return team.agents.filter((agent) => agentSearchText(agent).includes(q));
  }, [team.agents, query]);

  const filteredExplicit = useMemo(() => {
    const q = query.trim().toLowerCase();
    return team.explicitLinks.filter((link) => {
      if (selectedAgentId && link.fromAgentId !== selectedAgentId && link.toAgentId !== selectedAgentId) return false;
      return !q || [link.from, link.to, link.description].join(" ").toLowerCase().includes(q);
    });
  }, [team.explicitLinks, selectedAgentId, query]);

  const filteredObserved = useMemo(() => {
    const q = query.trim().toLowerCase();
    return team.observedLinks.filter((link) => {
      if (selectedAgentId && link.fromAgentId !== selectedAgentId && link.toAgentId !== selectedAgentId) return false;
      return !q || [link.from, link.to, ...link.subjects].join(" ").toLowerCase().includes(q);
    });
  }, [team.observedLinks, selectedAgentId, query]);

  const graph = useMemo(
    () => buildTeamGraph(team, query, selectedAgentId, selectedLinkId, nodePositions, onMessageAgent),
    [team, query, selectedAgentId, selectedLinkId, nodePositions, onMessageAgent],
  );
  const graphFitKey = graph.visibleAgentIds.join("\u0000");

  useEffect(() => {
    if (viewMode !== "graph" || !flowInstance || graph.nodes.length === 0) return;
    const timer = window.setTimeout(() => flowInstance.fitView({ padding: 0.16, duration: 220 }), 120);
    return () => window.clearTimeout(timer);
  }, [flowInstance, graphFitKey, viewMode]);

  const activeAgents = team.agents.filter((agent) => agent.status === "running").length;
  const profiledAgents = team.agents.filter((agent) => agent.profile.version > 0).length;
  const openRequests = team.agents.reduce((sum, agent) => sum + agent.openIn, 0);

  useEffect(() => {
    writeTeamRouteState({ agent: selectedAgentId, link: selectedLinkId, query, view: viewMode });
  }, [query, selectedAgentId, selectedLinkId, viewMode]);

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
    setSelectedLinkId(explicitLinkId(result.relationship));
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
      explicitLinksCount: team.explicitLinks.length,
      observedLinksCount: team.observedLinks.length,
      visibleAgentsCount: graph.visibleAgentIds.length,
      openRequests,
      graph: graphState(graph, selectedAgentId, selectedLinkId),
    });
    const root = window.codexLoom || window.codexHub || {};
    const teamAutomation = {
      state,
      open: () => {
        setViewMode("graph");
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
        const relationship = team.explicitLinks.find((item) => item.id === id);
        if (!relationship) throw new Error(`relationship not found: ${id}`);
        await deleteRelationship(relationship);
        return waitForAutomationState();
      },
    };
    const graphAutomation = {
        state: () => graphState(graph, selectedAgentId, selectedLinkId),
        open: () => {
          setViewMode("graph");
          return waitForGraphState();
        },
        selectNode: (key: string) => {
          setViewMode("graph");
          selectAgent(key);
          return waitForGraphState();
        },
        selectEdge: (id: string) => {
          setViewMode("graph");
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
          setNodePositions({});
          window.setTimeout(() => flowInstance?.fitView?.({ padding: 0.2, duration: 220 }), 30);
          return waitForGraphState();
        },
        moveNode: (id: string, x: number, y: number) => {
          setNodePositions((previous) => ({ ...previous, [id]: { x, y } }));
          return waitForGraphState();
        },
    };
    root.team = teamAutomation;
    root.graph = graphAutomation;
    window.codexLoom = root;
    window.codexHub = root;
  }, [flowInstance, graph, loading, openRequests, profiledAgents, query, selectedAgent, selectedAgentId, selectedLinkId, team, viewMode]);

  const handleGraphNodesChange = (changes: NodeChange[]) => {
    setNodePositions((previous) => {
      let next = previous;
      for (const change of changes) {
        if (change.type === "position" && change.position) {
          if (next === previous) next = { ...previous };
          next[change.id] = change.position;
        }
      }
      return next;
    });
  };

  const inspector = (
    <TeamInspector
      team={team}
      agent={selectedAgent}
      explicit={selectedExplicit}
      observed={selectedObserved}
      adjacentExplicit={adjacentExplicit}
      adjacentObserved={adjacentObserved}
      onError={onError}
      onMessageAgent={onMessageAgent}
      onScheduleAgent={onScheduleAgent}
      onSelectAgent={selectAgent}
      onSelectExplicit={(relationship) => {
        setSelectedAgentId("");
        setSelectedLinkId(explicitLinkId(relationship));
      }}
      onSaveProfile={saveProfile}
      onCreateRelationship={createRelationship}
      onUpdateRelationship={updateRelationship}
      onDeleteRelationship={deleteRelationship}
    />
  );

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
          <div className="mx-auto flex max-w-7xl flex-wrap items-center gap-2">
            <div className="flex h-9 rounded-md bg-muted/60 p-1 text-[12px] font-medium">
              <ModeButton active={viewMode === "graph"} icon={<Share2 className="size-3.5" />} label="Graph" onClick={() => setViewMode("graph")} />
              <ModeButton active={viewMode === "list"} icon={<List className="size-3.5" />} label="List" onClick={() => setViewMode("list")} />
            </div>
            <div className="relative min-w-[190px] flex-1">
              <Search className="pointer-events-none absolute left-3 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
              <input
                value={query}
                onChange={(event) => setQuery(event.target.value)}
                placeholder="search agents, domains, scope, relationships"
                className="h-9 w-full rounded-md bg-background pl-9 pr-3 text-[13px] outline-none ring-1 ring-border focus:ring-primary/40"
              />
            </div>
            <select
              value={selectedAgentId}
              onChange={(event) => {
                setSelectedAgentId(event.target.value);
                setSelectedLinkId("");
              }}
              className="h-9 min-w-[150px] max-w-[220px] rounded-md bg-background px-2 text-[13px] outline-none ring-1 ring-border focus:ring-primary/40"
            >
              <option value="">all agents</option>
              {team.agents.map((agent) => <option key={agent.id} value={agent.id}>{agent.name}</option>)}
            </select>
            <button
              onClick={() => refresh().catch((err: Error) => onError(err.message))}
              title="Refresh team"
              className="flex size-9 items-center justify-center rounded-md border border-border bg-background text-muted-foreground hover:text-foreground"
            >
              <RefreshCw className={`size-3.5 ${loading ? "animate-spin" : ""}`} />
            </button>
          </div>
        </div>

        {viewMode === "graph" ? (
          <div className="grid min-h-0 flex-1 grid-cols-1 overflow-y-auto lg:grid-cols-[minmax(0,1fr)_380px] lg:overflow-hidden">
            <section className="min-h-[460px] border-b border-border bg-background lg:min-h-0 lg:border-b-0 lg:border-r">
              <ReactFlow
                nodes={graph.nodes}
                edges={graph.edges}
                nodeTypes={nodeTypes}
                fitView
                fitViewOptions={{ padding: 0.2 }}
                minZoom={0.3}
                maxZoom={1.3}
                nodesDraggable
                nodesConnectable={false}
                elementsSelectable
                onInit={setFlowInstance}
                onNodesChange={handleGraphNodesChange}
                onNodeClick={(_, node) => selectAgent(node.id)}
                onEdgeClick={(_, edge) => {
                  setSelectedAgentId("");
                  setSelectedLinkId(edge.id);
                }}
                proOptions={{ hideAttribution: true }}
                className="bg-background"
              >
                <Background gap={22} size={1} color="rgba(120, 113, 108, 0.18)" />
                <Controls showInteractive={false} />
              </ReactFlow>
            </section>
            {inspector}
          </div>
        ) : (
          <div className="min-h-0 flex-1 overflow-y-auto">
            <div className="mx-auto grid max-w-7xl items-start gap-5 p-4 lg:grid-cols-[minmax(0,1fr)_380px] lg:p-5">
              <section className="order-2 min-w-0 space-y-3 lg:order-1">
                <SectionTitle icon={<Network className="size-3.5" />} label="Agent directory" count={filteredAgents.length} />
                <div className="grid gap-3 md:grid-cols-2">
                  {filteredAgents.map((agent) => (
                    <AgentListCard
                      key={agent.id}
                      agent={agent}
                      selected={selectedAgentId === agent.id}
                      onSelect={() => selectAgent(agent.id)}
                      onMessage={() => onMessageAgent(agent.name)}
                    />
                  ))}
                </div>
                {filteredAgents.length === 0 && <EmptyState text="No agents match this view." />}

                <SectionTitle icon={<Link2 className="size-3.5" />} label="Observed collaboration" count={filteredObserved.length} />
                {filteredObserved.map((link) => (
                  <ObservedLinkCard
                    key={observedLinkId(link)}
                    link={link}
                    selected={selectedLinkId === observedLinkId(link)}
                    onSelect={() => {
                      setSelectedAgentId("");
                      setSelectedLinkId(observedLinkId(link));
                    }}
                  />
                ))}
                {filteredObserved.length === 0 && <EmptyState text="No observed messages match this view." />}
              </section>

              <div className="order-1 min-w-0 lg:order-2 lg:sticky lg:top-0 lg:max-h-[calc(100vh-9rem)] lg:overflow-y-auto">
                {selectedAgent || selectedExplicit || selectedObserved ? inspector : (
                  <aside className="min-w-0 space-y-3 bg-card/35 p-4">
                    <SectionTitle icon={<Link2 className="size-3.5" />} label="Declared relationships" count={filteredExplicit.length} />
                    {filteredExplicit.map((relationship) => (
                      <button
                        key={relationship.id}
                        onClick={() => {
                          setSelectedAgentId("");
                          setSelectedLinkId(explicitLinkId(relationship));
                        }}
                        className="block w-full rounded-md border border-primary/20 bg-card p-3 text-left shadow-card hover:border-primary/40"
                      >
                        <div className="truncate text-[13px] font-semibold">{relationship.from} -&gt; {relationship.to}</div>
                        <div className="mt-1 whitespace-pre-wrap text-[12px] leading-5 text-muted-foreground">{relationship.description}</div>
                      </button>
                    ))}
                    {filteredExplicit.length === 0 && <EmptyState text="No declared relationships yet." />}
                  </aside>
                )}
              </div>
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
  explicit,
  observed,
  adjacentExplicit,
  adjacentObserved,
  onError,
  onMessageAgent,
  onScheduleAgent,
  onSelectAgent,
  onSelectExplicit,
  onSaveProfile,
  onCreateRelationship,
  onUpdateRelationship,
  onDeleteRelationship,
}: {
  team: TeamView;
  agent: TeamAgent | null;
  explicit: TeamRelationship | null;
  observed: TeamObservedLink | null;
  adjacentExplicit: TeamRelationship[];
  adjacentObserved: TeamObservedLink[];
  onError: (message: string) => void;
  onMessageAgent: (name: string) => void;
  onScheduleAgent: (name: string) => void;
  onSelectAgent: (id: string) => void;
  onSelectExplicit: (relationship: TeamRelationship) => void;
  onSaveProfile: (agent: TeamAgent, profile: Pick<AgentProfile, "identity" | "domain" | "scope">) => Promise<void>;
  onCreateRelationship: (from: string, to: string, description: string) => Promise<void>;
  onUpdateRelationship: (relationship: TeamRelationship, description: string) => Promise<void>;
  onDeleteRelationship: (relationship: TeamRelationship) => Promise<void>;
}) {
  return (
    <aside className="min-h-0 overflow-y-auto bg-card/35 p-4">
      <SectionTitle icon={<Network className="size-3.5" />} label="Inspector" />
      <div className="mt-3">
        {agent ? (
          <AgentInspector
            team={team}
            agent={agent}
            relationships={adjacentExplicit}
            observed={adjacentObserved}
            onError={onError}
            onMessage={() => onMessageAgent(agent.name)}
            onSchedule={() => onScheduleAgent(agent.name)}
            onSelectAgent={onSelectAgent}
            onSelectRelationship={onSelectExplicit}
            onSaveProfile={onSaveProfile}
            onCreateRelationship={onCreateRelationship}
          />
        ) : explicit ? (
          <RelationshipInspector relationship={explicit} onError={onError} onUpdate={onUpdateRelationship} onDelete={onDeleteRelationship} />
        ) : observed ? (
          <ObservedInspector link={observed} />
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
  relationships,
  observed,
  onError,
  onMessage,
  onSchedule,
  onSelectAgent,
  onSelectRelationship,
  onSaveProfile,
  onCreateRelationship,
}: {
  team: TeamView;
  agent: TeamAgent;
  relationships: TeamRelationship[];
  observed: TeamObservedLink[];
  onError: (message: string) => void;
  onMessage: () => void;
  onSchedule: () => void;
  onSelectAgent: (id: string) => void;
  onSelectRelationship: (relationship: TeamRelationship) => void;
  onSaveProfile: (agent: TeamAgent, profile: Pick<AgentProfile, "identity" | "domain" | "scope">) => Promise<void>;
  onCreateRelationship: (from: string, to: string, description: string) => Promise<void>;
}) {
  const [editing, setEditing] = useState(false);
  const [saving, setSaving] = useState(false);
  const [identity, setIdentity] = useState(agent.profile.identity || "");
  const [domain, setDomain] = useState(agent.profile.domain || "");
  const [scope, setScope] = useState(agent.profile.scope || "");
  const [addingLink, setAddingLink] = useState(false);
  const [target, setTarget] = useState("");
  const [description, setDescription] = useState("");

  useEffect(() => {
    setEditing(false);
    setIdentity(agent.profile.identity || "");
    setDomain(agent.profile.domain || "");
    setScope(agent.profile.scope || "");
    setAddingLink(false);
    setTarget("");
    setDescription("");
  }, [agent.id, agent.profile.version]);

  const editable = isActiveAgent(agent);
  const targets = team.agents.filter((item) => item.id !== agent.id && isActiveAgent(item));

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

  return (
    <div className="space-y-5">
      <section className="border-b border-border pb-4">
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <div className="truncate text-[16px] font-semibold text-foreground">{agent.name}</div>
            <div className="mt-1 truncate font-mono text-[10px] text-muted-foreground" title={agent.id}>{agent.id}</div>
          </div>
          <StatusBadge status={agent.status || "external"} />
        </div>
        <div className="mt-3 grid grid-cols-4 gap-2">
          <Metric label="in" value={agent.messageIn} />
          <Metric label="out" value={agent.messageOut} />
          <Metric label="open" value={agent.openIn} tone={agent.openIn > 0 ? "warning" : "muted"} />
          <Metric label="links" value={relationships.length} />
        </div>
        {editable && (
          <div className="mt-3 flex gap-2">
            <CommandButton icon={<Send className="size-3.5" />} label="Message" onClick={onMessage} />
            <CommandButton icon={<CalendarClock className="size-3.5" />} label="Schedule" onClick={onSchedule} />
          </div>
        )}
      </section>

      <section className="border-b border-border pb-4">
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

      <section className="border-b border-border pb-4">
        <div className="mb-3 flex items-center justify-between">
          <div className="text-[12px] font-semibold uppercase text-muted-foreground">Declared relationships</div>
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
            <textarea value={description} onChange={(event) => setDescription(event.target.value)} rows={4} placeholder="Describe the long-term collaboration boundary" className="w-full resize-y rounded-md bg-background p-2 text-[12px] leading-5 outline-none ring-1 ring-border focus:ring-primary/40" />
            <div className="flex justify-end gap-2">
              <button onClick={() => setAddingLink(false)} className="h-8 rounded-md border border-border px-3 text-[12px] text-muted-foreground">Cancel</button>
              <button disabled={!target || !description.trim() || saving} onClick={addRelationship} className="h-8 rounded-md bg-primary px-3 text-[12px] font-medium text-primary-foreground disabled:opacity-50">Add</button>
            </div>
          </div>
        )}
        <div className="space-y-2">
          {relationships.map((relationship) => {
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
          {relationships.length === 0 && <p className="text-[12px] text-muted-foreground">No declared relationships.</p>}
        </div>
      </section>

      <section>
        <div className="mb-3 text-[12px] font-semibold uppercase text-muted-foreground">Observed collaboration</div>
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
            <textarea value={description} onChange={(event) => setDescription(event.target.value)} rows={8} className="w-full resize-y rounded-md bg-background p-2.5 text-[13px] leading-5 outline-none ring-1 ring-border focus:ring-primary/40" />
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

function ObservedInspector({ link }: { link: TeamObservedLink }) {
  return (
    <div className="space-y-4">
      <section className="border-b border-border pb-4">
        <div className="text-[15px] font-semibold">{link.from} -&gt; {link.to}</div>
        <div className="mt-1 text-[11px] text-muted-foreground">observed from Messages</div>
        <div className="mt-3 grid grid-cols-4 gap-2">
          <Metric label="messages" value={link.messageCount} />
          <Metric label="replies" value={link.replyCount} />
          <Metric label="open" value={link.openCount} tone={link.openCount > 0 ? "warning" : "muted"} />
          <Metric label="failed" value={link.failedCount} tone={link.failedCount > 0 ? "danger" : "muted"} />
        </div>
      </section>
      <section>
        <div className="mb-2 text-[12px] font-semibold uppercase text-muted-foreground">Recent subjects</div>
        <div className="space-y-1.5">
          {link.subjects.map((subject) => <div key={subject} className="rounded-md bg-muted px-2.5 py-2 text-[12px] text-muted-foreground">{subject}</div>)}
        </div>
      </section>
    </div>
  );
}

function buildTeamGraph(
  team: TeamView,
  query: string,
  selectedAgentId: string,
  selectedLinkId: string,
  nodePositions: NodePositions,
  onMessage: (name: string) => void,
): TeamGraph {
  const q = query.trim().toLowerCase();
  const agentsById = new Map(team.agents.map((agent) => [agent.id, agent]));
  const visibleAgentIds = new Set<string>();
  const collaborationAgentIds = new Set<string>();
  for (const link of team.explicitLinks) {
    collaborationAgentIds.add(link.fromAgentId);
    collaborationAgentIds.add(link.toAgentId);
  }
  for (const link of team.observedLinks) {
    collaborationAgentIds.add(link.fromAgentId);
    collaborationAgentIds.add(link.toAgentId);
  }
  for (const agent of team.agents) {
    if (agent.profile.version > 0) collaborationAgentIds.add(agent.id);
  }
  const hasCollaborationMap = collaborationAgentIds.size > 0;

  for (const agent of team.agents) {
    const matches = !q || agentSearchText(agent).includes(q);
    const belongsToDefaultMap = !q && !selectedAgentId ? !hasCollaborationMap || collaborationAgentIds.has(agent.id) : true;
    if (matches && belongsToDefaultMap && (!selectedAgentId || selectedAgentId === agent.id)) visibleAgentIds.add(agent.id);
  }

  const explicit = team.explicitLinks.filter((link) => {
    if (selectedAgentId && link.fromAgentId !== selectedAgentId && link.toAgentId !== selectedAgentId) return false;
    return !q || [link.from, link.to, link.description].join(" ").toLowerCase().includes(q);
  });
  const observedCandidates = team.observedLinks.filter((link) => {
    if (selectedAgentId && link.fromAgentId !== selectedAgentId && link.toAgentId !== selectedAgentId) return false;
    return !q || [link.from, link.to, ...link.subjects].join(" ").toLowerCase().includes(q);
  });
  const observed = !q && !selectedAgentId
    ? strongestObservedLinks(observedCandidates, selectedLinkId, DEFAULT_OBSERVED_EDGE_LIMIT)
    : observedCandidates;
  for (const link of explicit) {
    visibleAgentIds.add(link.fromAgentId);
    visibleAgentIds.add(link.toAgentId);
  }
  for (const link of observed) {
    visibleAgentIds.add(link.fromAgentId);
    visibleAgentIds.add(link.toAgentId);
  }
  if (selectedAgentId) visibleAgentIds.add(selectedAgentId);

  const ids = Array.from(visibleAgentIds).sort((a, b) => {
    if (a === selectedAgentId) return -1;
    if (b === selectedAgentId) return 1;
    return degree(team, b) - degree(team, a) || (agentsById.get(a)?.name || a).localeCompare(agentsById.get(b)?.name || b);
  });
  const positions = computePositions(ids);
  const nodes: Node<AgentNodeData>[] = ids.map((id) => {
    const agent = agentsById.get(id) || externalAgent(id);
    return {
      id,
      type: "agentCard",
      position: nodePositions[id] || positions.get(id) || { x: 0, y: 0 },
      selected: selectedAgentId === id,
      zIndex: 2,
      data: { agent, selected: selectedAgentId === id, onMessage },
    };
  });

  const explicitEdges: Edge[] = explicit.map((link) => ({
    id: explicitLinkId(link),
    source: link.fromAgentId,
    target: link.toAgentId,
    type: "smoothstep",
    selected: selectedLinkId === explicitLinkId(link),
    zIndex: selectedLinkId === explicitLinkId(link) ? 1 : 0,
    label: "declared",
    markerEnd: { type: MarkerType.ArrowClosed, width: 15, height: 15, color: "#2563eb" },
    style: { stroke: "#2563eb", strokeWidth: selectedLinkId === explicitLinkId(link) ? 3 : 2, strokeDasharray: "7 4" },
    labelStyle: { fill: "#2563eb", fontSize: 10, fontWeight: 700 },
    labelBgStyle: { fill: "#fafaf9", fillOpacity: 1 },
  }));
  const observedEdges: Edge[] = observed.map((link) => {
    const id = observedLinkId(link);
    const warning = link.failedCount > 0 || link.openCount > 0 || link.queuedCount > 0;
    const color = link.failedCount > 0 ? "#dc2626" : warning ? "#d97706" : "#a8a29e";
    return {
      id,
      source: link.fromAgentId,
      target: link.toAgentId,
      selected: selectedLinkId === id,
      zIndex: selectedLinkId === id ? 1 : 0,
      label: `${link.messageCount}`,
      markerEnd: { type: MarkerType.ArrowClosed, width: 14, height: 14, color },
      style: { stroke: color, strokeWidth: selectedLinkId === id ? 3 : Math.min(2.8, 1.1 + link.messageCount * 0.35) },
      labelStyle: { fill: color, fontSize: 10, fontWeight: 700 },
      labelBgStyle: { fill: "#fafaf9", fillOpacity: 0.9 },
      animated: warning,
    };
  });
  const edges = [...explicitEdges, ...observedEdges];
  return { nodes, edges, visibleAgentIds: ids, visibleEdgeIds: edges.map((edge) => edge.id) };
}

function AgentGraphNode({ data }: NodeProps<Node<AgentNodeData>>) {
  const { agent, selected, onMessage } = data;
  const domain = firstLine(agent.profile.domain) || firstLine(agent.profile.identity) || "No domain declared";
  return (
    <div className={`w-[260px] rounded-md border bg-card p-3.5 shadow-card ${selected ? "border-primary/50 ring-2 ring-primary/20" : "border-border"}`}>
      <Handle type="target" position={Position.Left} className="!size-2 !border-border !bg-muted-foreground" />
      <Handle type="source" position={Position.Right} className="!size-2 !border-border !bg-muted-foreground" />
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <span className={`size-2 shrink-0 rounded-full ${agent.status === "running" ? "bg-success" : "bg-muted-foreground/40"}`} />
            <div className="truncate text-[15px] font-semibold" title={agent.name}>{agent.name}</div>
          </div>
          <div className="mt-1 truncate font-mono text-[10px] text-muted-foreground" title={agent.id}>{agent.id}</div>
        </div>
        <StatusBadge status={agent.status || "external"} />
      </div>
      <div className="mt-3 h-10 overflow-hidden text-[12px] leading-5 text-foreground/85" title={agent.profile.domain || agent.profile.identity}>{domain}</div>
      <div className="mt-3 flex items-end justify-between gap-3 border-t border-border pt-2.5">
        <div className="font-mono text-[10px] text-muted-foreground">{agent.profile.version > 0 ? `profile v${agent.profile.version}` : "no profile"} | {agent.openIn} open</div>
        {isActiveAgent(agent) && selected && (
          <button onClick={(event) => { event.stopPropagation(); onMessage(agent.name); }} title="Message agent" className="flex size-7 items-center justify-center rounded-md border border-border bg-background text-muted-foreground hover:text-foreground"><Send className="size-3.5" /></button>
        )}
      </div>
    </div>
  );
}

function AgentListCard({ agent, selected, onSelect, onMessage }: { agent: TeamAgent; selected: boolean; onSelect: () => void; onMessage: () => void }) {
  return (
    <article className={`rounded-md border bg-card p-4 shadow-card ${selected ? "border-primary/40 ring-1 ring-primary/15" : "border-border"}`}>
      <button onClick={onSelect} className="block w-full text-left">
        <div className="flex items-start justify-between gap-2">
          <div className="min-w-0">
            <div className="truncate text-[15px] font-semibold">{agent.name}</div>
            <div className="mt-1 truncate font-mono text-[10px] text-muted-foreground">{agent.id}</div>
          </div>
          <StatusBadge status={agent.status || "external"} />
        </div>
        <div className="mt-3 min-h-10 text-[12px] leading-5 text-foreground/85">{firstLine(agent.profile.domain) || firstLine(agent.profile.identity) || "No domain declared"}</div>
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

function ObservedLinkCard({ link, selected, onSelect }: { link: TeamObservedLink; selected: boolean; onSelect: () => void }) {
  return (
    <button onClick={onSelect} className={`block w-full rounded-md border bg-card p-3 text-left shadow-card ${selected ? "border-primary/40 ring-1 ring-primary/15" : "border-border hover:border-primary/30"}`}>
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="truncate text-[13px] font-semibold">{link.from} -&gt; {link.to}</div>
        <div className="font-mono text-[10px] text-muted-foreground">{link.messageCount} messages | {link.replyCount} replies | {link.openCount} open</div>
      </div>
      {link.subjects.length > 0 && <div className="mt-2 truncate text-[11px] text-muted-foreground">{link.subjects.join(" | ")}</div>}
    </button>
  );
}

function ProfileField({ label, value, onChange, rows, placeholder }: { label: string; value: string; onChange: (value: string) => void; rows: number; placeholder: string }) {
  return (
    <label className="block">
      <span className="mb-1.5 block text-[11px] font-semibold uppercase text-muted-foreground">{label}</span>
      <textarea value={value} onChange={(event) => onChange(event.target.value)} rows={rows} placeholder={placeholder} className="w-full resize-y rounded-md bg-background p-2.5 text-[12px] leading-5 outline-none ring-1 ring-border focus:ring-primary/40" />
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

function ModeButton({ active, icon, label, onClick }: { active: boolean; icon: React.ReactNode; label: string; onClick: () => void }) {
  return <button onClick={onClick} className={`flex items-center gap-1.5 rounded px-2.5 ${active ? "bg-background text-foreground shadow-sm" : "text-muted-foreground hover:text-foreground"}`}>{icon}{label}</button>;
}

function CommandButton({ icon, label, onClick }: { icon: React.ReactNode; label: string; onClick: () => void }) {
  return <button onClick={onClick} className="flex h-8 flex-1 items-center justify-center gap-1.5 rounded-md border border-border bg-background px-2.5 text-[12px] font-medium text-muted-foreground hover:text-foreground">{icon}{label}</button>;
}

function StatusBadge({ status }: { status: string }) {
  const cls = status === "running" ? "bg-success/10 text-success" : status === "system" ? "bg-primary/10 text-primary" : "bg-muted text-muted-foreground";
  return <span className={`shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium ${cls}`}>{status}</span>;
}

function Stat({ label, value, tone, className = "" }: { label: string; value: number; tone?: "success" | "warning"; className?: string }) {
  const cls = tone === "success" ? "bg-success/10 text-success" : tone === "warning" ? "bg-warning/10 text-warning" : "bg-muted text-muted-foreground";
  return <span className={`items-center gap-1 rounded px-1.5 py-1 ${cls} ${className || "inline-flex"}`}><strong>{value}</strong><span className="hidden sm:inline">{label}</span></span>;
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

function degree(team: TeamView, id: string) {
  const observed = team.observedLinks.reduce((sum, link) => sum + (link.fromAgentId === id || link.toAgentId === id ? link.messageCount + link.replyCount : 0), 0);
  const explicit = team.explicitLinks.reduce((sum, link) => sum + (link.fromAgentId === id || link.toAgentId === id ? 2 : 0), 0);
  return observed + explicit;
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
  const columnGap = 290;
  const rowGap = 165;
  ids.forEach((id, index) => {
    positions.set(id, {
      x: (index % columns) * columnGap,
      y: Math.floor(index / columns) * rowGap,
    });
  });
  return positions;
}

function strongestObservedLinks(links: TeamObservedLink[], selectedLinkId: string, limit: number) {
  const selected = links.find((link) => observedLinkId(link) === selectedLinkId);
  const ranked = [...links].sort((a, b) => {
    const riskA = a.failedCount * 1000 + a.openCount * 100 + a.queuedCount * 50;
    const riskB = b.failedCount * 1000 + b.openCount * 100 + b.queuedCount * 50;
    const volumeA = a.messageCount + a.replyCount;
    const volumeB = b.messageCount + b.replyCount;
    return riskB - riskA || volumeB - volumeA || observedLinkId(a).localeCompare(observedLinkId(b));
  });
  const result = ranked.slice(0, limit);
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

function explicitLinkId(link: Pick<TeamRelationship, "id">) {
  return `explicit:${link.id}`;
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
  const responsiveDefault = window.innerWidth > 0 && window.innerWidth <= 1023 ? "list" : "graph";
  if (queryStart < 0) return { agent: "", link: "", query: "", view: responsiveDefault as TeamViewMode };
  const params = new URLSearchParams(hash.slice(queryStart + 1));
  return {
    agent: params.get("agent") || "",
    link: params.get("link") || "",
    query: params.get("q") || "",
    view: params.get("view") === "list" || params.get("view") === "graph"
      ? params.get("view") as TeamViewMode
      : responsiveDefault as TeamViewMode,
  };
}

function writeTeamRouteState({ agent, link, query, view }: { agent: string; link: string; query: string; view: TeamViewMode }) {
  const params = new URLSearchParams();
  params.set("view", view);
  if (agent) params.set("agent", agent);
  if (link) params.set("link", link);
  if (query.trim()) params.set("q", query.trim());
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
