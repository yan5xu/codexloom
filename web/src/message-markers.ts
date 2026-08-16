import type { Block } from "./feed";

export type MessageMarkerKind =
  | "owner"
  | "agent"
  | "schedule"
  | "external";

export interface MessageMarkerRecipient {
  id: string;
  name: string;
}

export interface MessageMarker {
  id: string;
  kind: MessageMarkerKind;
  typeLabel: string;
  detail: string;
  label: string;
  colorClass: string;
  timestamp?: string;
}

export interface MessageMarkerCluster {
  startIndex: number;
  endIndex: number;
  count: number;
  position: number;
}

const markerMeta: Record<MessageMarkerKind, { label: string; colorClass: string }> = {
  owner: { label: "Owner message", colorClass: "bg-[var(--loom-teal)]" },
  agent: { label: "Internal Agent Message", colorClass: "bg-[var(--loom-blue)]" },
  schedule: { label: "Scheduled task", colorClass: "bg-[var(--loom-amber)]" },
  external: { label: "External message", colorClass: "bg-[var(--loom-green)]" },
};

function stableHash(value: string): string {
  let hash = 2166136261;
  for (let index = 0; index < value.length; index += 1) {
    hash ^= value.charCodeAt(index);
    hash = Math.imul(hash, 16777619);
  }
  return (hash >>> 0).toString(36);
}

export function blockStableID(block: Block): string {
  if ("id" in block && block.id) return `${block.kind}:${block.id}`;
  if (block.kind === "sys") return `sys:${block.ts}:${block.cls}:${stableHash(block.text)}`;
  return `${block.kind}:${stableHash(JSON.stringify(block))}`;
}

function isCurrentRecipient(to: string | undefined, recipient: MessageMarkerRecipient): boolean {
  const target = to?.trim();
  if (!target) return false;
  return [recipient.id, recipient.name].some((value) => value.trim() === target);
}

export function receivedMessageKindForBlock(
  block: Block,
  recipient: MessageMarkerRecipient,
): MessageMarkerKind | null {
  switch (block.kind) {
    case "user": return "owner";
    case "agentMessage":
      if (!isCurrentRecipient(block.to, recipient)) return null;
      return block.from.trim() === "scheduler" ? "schedule" : "agent";
    case "externalMessage": return "external";
    case "topicContext": {
      const payload = block.payload;
      if (payload?.kind === "ownerInput" || payload?.kind === "intervention") return "owner";
      if (payload?.kind !== "agentMessage" || !isCurrentRecipient(payload.to, recipient)) return null;
      return payload.from?.trim() === "scheduler" ? "schedule" : "agent";
    }
    default: return null;
  }
}

function markerDetail(block: Block, kind: MessageMarkerKind): string {
  switch (block.kind) {
    case "user": {
      const text = block.text.split("<loom_context", 1)[0].split("<loom_attachments", 1)[0];
      return text.split("\n", 1)[0].trim() || "Owner message";
    }
    case "agentMessage": return block.subject || `${block.from} → ${block.to}`;
    case "externalMessage": return `${block.sender} · ${block.provider}`;
    case "topicContext":
      return block.payload?.subject || block.payload?.body.split("\n", 1)[0].trim() || block.title || markerMeta[kind].label;
    default: return markerMeta[kind].label;
  }
}

export function receivedMessageMarkerForBlock(
  block: Block,
  recipient: MessageMarkerRecipient,
): MessageMarker | null {
  const kind = receivedMessageKindForBlock(block, recipient);
  if (!kind) return null;
  const meta = markerMeta[kind];
  const detail = markerDetail(block, kind);
  return {
    id: blockStableID(block),
    kind,
    typeLabel: meta.label,
    detail,
    label: `${meta.label}: ${detail}`,
    colorClass: meta.colorClass,
    timestamp: "ts" in block && block.ts ? block.ts : undefined,
  };
}

export function timelineMarkerPercent(position: number, count: number): number {
  if (count <= 1) return 50;
  return Math.min(94, Math.max(6, 6 + (position / (count - 1)) * 88));
}

export function timelineIndexAtClientY(clientY: number, top: number, height: number, count: number): number {
  if (count <= 1 || height <= 0) return 0;
  const percent = ((clientY - top) / height) * 100;
  const normalized = (Math.min(94, Math.max(6, percent)) - 6) / 88;
  return Math.min(count - 1, Math.max(0, Math.round(normalized * (count - 1))));
}

export function messageMarkerClusters(count: number, maxClusters = 28): MessageMarkerCluster[] {
  if (count <= 0 || maxClusters <= 0) return [];
  const size = Math.max(1, Math.ceil(count / maxClusters));
  const clusters: MessageMarkerCluster[] = [];
  for (let startIndex = 0; startIndex < count; startIndex += size) {
    const endIndex = Math.min(count - 1, startIndex + size - 1);
    clusters.push({
      startIndex,
      endIndex,
      count: endIndex - startIndex + 1,
      position: timelineMarkerPercent((startIndex + endIndex) / 2, count),
    });
  }
  return clusters;
}
