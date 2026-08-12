import type { ELK as ElkApi, ElkNode } from "elkjs/lib/elk.bundled.js";

export const TEAM_GRAPH_NODE_WIDTH = 240;
export const TEAM_GRAPH_NODE_HEIGHT = 132;

export type TeamGraphLayoutMode = "organization" | "collaboration" | "activity";

export type TeamGraphLayoutEdge = {
  id: string;
  source: string;
  target: string;
};

export type TeamGraphPosition = { x: number; y: number };

export type TeamGraphComponent = {
  id: string;
  nodeIds: string[];
  x: number;
  y: number;
  width: number;
  height: number;
};

export type TeamGraphLayout = {
  positions: Record<string, TeamGraphPosition>;
  components: TeamGraphComponent[];
  width: number;
  height: number;
};

type LocalComponentLayout = {
  id: string;
  nodeIds: string[];
  positions: Record<string, TeamGraphPosition>;
  width: number;
  height: number;
};

const COMPONENT_GAP = 112;
let elkPromise: Promise<ElkApi> | null = null;

export async function layoutTeamGraph(
  nodeIds: string[],
  edges: TeamGraphLayoutEdge[],
  mode: TeamGraphLayoutMode,
): Promise<TeamGraphLayout> {
  const ids = [...new Set(nodeIds)].sort();
  if (ids.length === 0) return { positions: {}, components: [], width: 0, height: 0 };

  const visible = new Set(ids);
  const normalizedEdges = edges
    .filter((edge) => visible.has(edge.source) && visible.has(edge.target) && edge.source !== edge.target)
    .sort((a, b) => a.id.localeCompare(b.id));
  const components = connectedComponents(ids, normalizedEdges);
  const localLayouts = await Promise.all(components.map((component) => layoutComponent(component, normalizedEdges, mode)));
  const packed = packComponents(localLayouts, mode === "organization" ? 1.65 : mode === "activity" ? 1.6 : 1.75);
  const positions: Record<string, TeamGraphPosition> = {};

  for (const component of packed.components) {
    const local = localLayouts.find((item) => item.id === component.id);
    if (!local) continue;
    for (const nodeId of local.nodeIds) {
      const position = local.positions[nodeId];
      positions[nodeId] = {
        x: round(component.x + position.x),
        y: round(component.y + position.y),
      };
    }
  }

  return {
    positions,
    components: packed.components,
    width: packed.width,
    height: packed.height,
  };
}

function connectedComponents(nodeIds: string[], edges: TeamGraphLayoutEdge[]) {
  const adjacency = new Map(nodeIds.map((id) => [id, new Set<string>()]));
  for (const edge of edges) {
    adjacency.get(edge.source)?.add(edge.target);
    adjacency.get(edge.target)?.add(edge.source);
  }

  const seen = new Set<string>();
  const components: string[][] = [];
  for (const start of nodeIds) {
    if (seen.has(start)) continue;
    const pending = [start];
    const component: string[] = [];
    seen.add(start);
    while (pending.length > 0) {
      const current = pending.shift()!;
      component.push(current);
      for (const neighbor of [...(adjacency.get(current) || [])].sort()) {
        if (seen.has(neighbor)) continue;
        seen.add(neighbor);
        pending.push(neighbor);
      }
    }
    components.push(component.sort());
  }
  return components;
}

async function layoutComponent(
  nodeIds: string[],
  allEdges: TeamGraphLayoutEdge[],
  mode: TeamGraphLayoutMode,
): Promise<LocalComponentLayout> {
  const componentSet = new Set(nodeIds);
  const edges = allEdges.filter((edge) => componentSet.has(edge.source) && componentSet.has(edge.target));
  const id = nodeIds[0];
  if (nodeIds.length === 1) {
    return {
      id,
      nodeIds,
      positions: { [id]: { x: 0, y: 0 } },
      width: TEAM_GRAPH_NODE_WIDTH,
      height: TEAM_GRAPH_NODE_HEIGHT,
    };
  }

  const graph: ElkNode = {
    id: `component:${id}`,
    layoutOptions: {
      "elk.algorithm": "layered",
      "elk.direction": "RIGHT",
      "elk.edgeRouting": "SPLINES",
      "elk.padding": "[left=0, top=0, right=0, bottom=0]",
      "elk.spacing.nodeNode": mode === "organization" ? "48" : mode === "activity" ? "44" : "58",
      "elk.spacing.edgeNode": "36",
      "elk.layered.spacing.nodeNodeBetweenLayers": mode === "organization" ? "142" : mode === "activity" ? "112" : "132",
      "elk.layered.spacing.edgeEdgeBetweenLayers": "28",
      "elk.layered.crossingMinimization.strategy": "LAYER_SWEEP",
      "elk.layered.nodePlacement.strategy": "NETWORK_SIMPLEX",
      "elk.layered.considerModelOrder.strategy": "NODES_AND_EDGES",
      "elk.layered.compaction.postCompaction.strategy": "LEFT",
    },
    children: nodeIds.map((nodeId) => ({
      id: nodeId,
      width: TEAM_GRAPH_NODE_WIDTH,
      height: TEAM_GRAPH_NODE_HEIGHT,
    })),
    edges: edges.map((edge) => ({
      id: edge.id,
      sources: [edge.source],
      targets: [edge.target],
    })),
  };
  const result = await (await getElk()).layout(graph);
  const children = result.children || [];
  const minX = Math.min(...children.map((node) => node.x || 0));
  const minY = Math.min(...children.map((node) => node.y || 0));
  const positions: Record<string, TeamGraphPosition> = {};
  let width = 0;
  let height = 0;
  for (const child of children) {
    const x = (child.x || 0) - minX;
    const y = (child.y || 0) - minY;
    positions[child.id] = { x: round(x), y: round(y) };
    width = Math.max(width, x + TEAM_GRAPH_NODE_WIDTH);
    height = Math.max(height, y + TEAM_GRAPH_NODE_HEIGHT);
  }
  return { id, nodeIds, positions, width: round(width), height: round(height) };
}

function packComponents(components: LocalComponentLayout[], targetAspect: number) {
  if (components.length === 0) return { components: [] as TeamGraphComponent[], width: 0, height: 0 };
  const sorted = [...components].sort((a, b) => b.width * b.height - a.width * a.height || a.id.localeCompare(b.id));
  let best: { components: TeamGraphComponent[]; width: number; height: number; score: number } | null = null;

  for (let columnCount = 1; columnCount <= Math.min(4, sorted.length); columnCount += 1) {
    const columns = Array.from({ length: columnCount }, () => ({ width: 0, height: 0, items: [] as LocalComponentLayout[] }));
    for (const component of sorted) {
      const column = columns.reduce((shortest, candidate) => candidate.height < shortest.height ? candidate : shortest, columns[0]);
      column.items.push(component);
      column.width = Math.max(column.width, component.width);
      column.height += component.height + (column.items.length > 1 ? COMPONENT_GAP : 0);
    }
    const width = columns.reduce((sum, column) => sum + column.width, 0) + COMPONENT_GAP * (columns.length - 1);
    const height = Math.max(...columns.map((column) => column.height));
    const aspect = width / Math.max(1, height);
    const area = width * height;
    const score = area * (1 + Math.abs(Math.log(aspect / targetAspect)) * 0.42);
    if (best && best.score <= score) continue;

    const placed: TeamGraphComponent[] = [];
    let x = 0;
    for (const column of columns) {
      let y = 0;
      for (const component of column.items) {
        placed.push({
          id: component.id,
          nodeIds: component.nodeIds,
          x: round(x + (column.width - component.width) / 2),
          y: round(y),
          width: component.width,
          height: component.height,
        });
        y += component.height + COMPONENT_GAP;
      }
      x += column.width + COMPONENT_GAP;
    }
    best = { components: placed.sort((a, b) => a.id.localeCompare(b.id)), width: round(width), height: round(height), score };
  }

  return best!;
}

function round(value: number) {
  return Math.round(value * 100) / 100;
}

function getElk() {
  if (!elkPromise) {
    elkPromise = import("elkjs/lib/elk.bundled.js")
      .then(({ default: ELK }) => new ELK())
      .catch((error) => {
        elkPromise = null;
        throw error;
      });
  }
  return elkPromise;
}
