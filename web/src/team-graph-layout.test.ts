import { describe, expect, it } from "vitest";
import {
  layoutTeamGraph,
  TEAM_GRAPH_NODE_HEIGHT,
  TEAM_GRAPH_NODE_WIDTH,
  type TeamGraphLayoutEdge,
} from "./team-graph-layout";

const organizationEdges: TeamGraphLayoutEdge[] = [
  { id: "loom-comms", source: "loom-coach", target: "agent-communication-coach" },
  { id: "loom-frontend", source: "loom-coach", target: "frontend-practice-coach" },
  { id: "parall-edge", source: "parall-dev-lead", target: "parall-edge-dev" },
  { id: "parall-platform", source: "parall-dev-lead", target: "parall-platform-dev" },
  { id: "pinix-api", source: "pinix-lead", target: "pinix-api" },
  { id: "pinix-edge", source: "pinix-lead", target: "pinix-edge" },
  { id: "pinix-ops", source: "pinix-lead", target: "pinix-ops" },
  { id: "pinix-web", source: "pinix-lead", target: "pinix-web" },
];

const collaborationEdges: TeamGraphLayoutEdge[] = [
  { id: "business-audio", source: "parall-business-assistant", target: "audio-transcriber" },
  { id: "platform-edge", source: "parall-platform-dev", target: "parall-edge-dev" },
  { id: "operator-community", source: "omac-operator", target: "ai-community" },
  { id: "operator-mmx", source: "omac-operator", target: "cici-mmx" },
  { id: "research-operator", source: "cici-research", target: "omac-operator" },
  { id: "parall-pinix", source: "parall-dev-lead", target: "pinix-lead" },
];

const activityEdges: TeamGraphLayoutEdge[] = [
  { id: "coach-product", source: "loom-coach", target: "loom-product" },
  { id: "product-web", source: "loom-product", target: "cici-web" },
  { id: "product-visual", source: "loom-product", target: "cici-visual" },
  { id: "web-visual", source: "cici-web", target: "cici-visual" },
  { id: "web-seo", source: "cici-web", target: "cici-seo" },
  { id: "web-community", source: "cici-web", target: "cici-community" },
];

describe("team graph clustered layout", () => {
  it("builds separate organization islands with parents left of their children", async () => {
    const nodeIds = [...new Set(organizationEdges.flatMap((edge) => [edge.source, edge.target]))];
    const layout = await layoutTeamGraph(nodeIds, organizationEdges, "organization");

    expect(layout.components).toHaveLength(3);
    expectNoNodeOverlap(layout.positions);
    expectNoComponentOverlap(layout.components);
    for (const edge of organizationEdges) {
      expect(layout.positions[edge.source].x).toBeLessThan(layout.positions[edge.target].x);
    }
  });

  it("packs the cleaned collaboration graph as four independent islands", async () => {
    const nodeIds = [...new Set(collaborationEdges.flatMap((edge) => [edge.source, edge.target]))];
    const layout = await layoutTeamGraph(nodeIds, collaborationEdges, "collaboration");

    expect(layout.components).toHaveLength(4);
    expectNoNodeOverlap(layout.positions);
    expectNoComponentOverlap(layout.components);
    expect(layout.width).toBeGreaterThan(0);
    expect(layout.height).toBeGreaterThan(0);
  });

  it("returns stable coordinates for the same topology", async () => {
    const nodeIds = [...new Set(collaborationEdges.flatMap((edge) => [edge.source, edge.target]))];
    const first = await layoutTeamGraph(nodeIds, collaborationEdges, "collaboration");
    const second = await layoutTeamGraph(nodeIds, collaborationEdges, "collaboration");
    expect(second).toEqual(first);
  });

  it("automatically arranges the observed activity network without treating it as a hierarchy", async () => {
    const nodeIds = [...new Set(activityEdges.flatMap((edge) => [edge.source, edge.target]))];
    const layout = await layoutTeamGraph(nodeIds, activityEdges, "activity");

    expect(layout.components).toHaveLength(1);
    expectNoNodeOverlap(layout.positions);
    expect(layout.width).toBeGreaterThan(TEAM_GRAPH_NODE_WIDTH);
    expect(layout.height).toBeGreaterThan(TEAM_GRAPH_NODE_HEIGHT);
  });
});

function expectNoNodeOverlap(positions: Record<string, { x: number; y: number }>) {
  const entries = Object.entries(positions);
  for (let i = 0; i < entries.length; i += 1) {
    for (let j = i + 1; j < entries.length; j += 1) {
      const [leftId, left] = entries[i];
      const [rightId, right] = entries[j];
      const separated = left.x + TEAM_GRAPH_NODE_WIDTH <= right.x
        || right.x + TEAM_GRAPH_NODE_WIDTH <= left.x
        || left.y + TEAM_GRAPH_NODE_HEIGHT <= right.y
        || right.y + TEAM_GRAPH_NODE_HEIGHT <= left.y;
      expect(separated, `${leftId} overlaps ${rightId}`).toBe(true);
    }
  }
}

function expectNoComponentOverlap(components: Array<{ id: string; x: number; y: number; width: number; height: number }>) {
  for (let i = 0; i < components.length; i += 1) {
    for (let j = i + 1; j < components.length; j += 1) {
      const left = components[i];
      const right = components[j];
      const separated = left.x + left.width <= right.x
        || right.x + right.width <= left.x
        || left.y + left.height <= right.y
        || right.y + right.height <= left.y;
      expect(separated, `${left.id} overlaps ${right.id}`).toBe(true);
    }
  }
}
