import type { Block } from "./feed";

export type MessageMarkerKind =
  | "user"
  | "agent"
  | "external"
  | "trigger"
  | "topic"
  | "assistant"
  | "thinking"
  | "command"
  | "file"
  | "image"
  | "artifact"
  | "usage"
  | "system"
  | "raw";

export interface MessageMarker {
  id: string;
  kind: MessageMarkerKind;
  typeLabel: string;
  detail: string;
  label: string;
  colorClass: string;
}

const markerMeta: Record<MessageMarkerKind, { label: string; colorClass: string }> = {
  user: { label: "User task", colorClass: "bg-[var(--loom-teal)]" },
  agent: { label: "Internal Agent Message", colorClass: "bg-[var(--loom-blue)]" },
  external: { label: "External message", colorClass: "bg-[var(--loom-ink)]" },
  trigger: { label: "External trigger", colorClass: "bg-[var(--loom-green)]" },
  topic: { label: "Topic context", colorClass: "bg-[var(--loom-amber)]" },
  assistant: { label: "Agent response", colorClass: "bg-[var(--loom-blue)]" },
  thinking: { label: "Agent reasoning", colorClass: "bg-[var(--loom-vermilion)]" },
  command: { label: "Command", colorClass: "bg-[var(--loom-vermilion)]" },
  file: { label: "File change", colorClass: "bg-[var(--loom-green)]" },
  image: { label: "Image", colorClass: "bg-[var(--loom-teal)]" },
  artifact: { label: "Artifact", colorClass: "bg-[var(--loom-amber)]" },
  usage: { label: "Usage", colorClass: "bg-[var(--loom-blue)]" },
  system: { label: "System event", colorClass: "bg-muted-foreground" },
  raw: { label: "Event", colorClass: "bg-foreground/60" },
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

export function markerKindForBlock(block: Block): MessageMarkerKind {
  switch (block.kind) {
    case "user": return "user";
    case "agentMessage": return "agent";
    case "externalMessage": return "external";
    case "externalTrigger": return "trigger";
    case "topicContext": return "topic";
    case "agent": return "assistant";
    case "think": return "thinking";
    case "command": return "command";
    case "file": return "file";
    case "image": return "image";
    case "artifact": return "artifact";
    case "usage": return "usage";
    case "sys": return "system";
    case "raw": return "raw";
  }
}

function markerDetail(block: Block): string {
  switch (block.kind) {
    case "user": return block.text.split("\n", 1)[0].trim() || "User task";
    case "agentMessage": return block.subject || `${block.from} → ${block.to}`;
    case "externalMessage": return `${block.sender} · ${block.provider}`;
    case "externalTrigger": return `${block.provider} · ${block.event}`;
    case "topicContext": return block.title || block.topicId || "Topic context";
    case "agent": return "Agent response";
    case "think": return "Agent reasoning";
    case "command": return block.description || block.command || "Command";
    case "file": return "File change";
    case "image": return "Image";
    case "artifact": return block.artifact.name || "Artifact";
    case "usage": return block.model || "Usage";
    case "sys": return block.text || "System event";
    case "raw": return block.type || "Event";
  }
}

export function messageMarkerForBlock(block: Block): MessageMarker {
  const kind = markerKindForBlock(block);
  const meta = markerMeta[kind];
  const detail = markerDetail(block);
  return {
    id: blockStableID(block),
    kind,
    typeLabel: meta.label,
    detail,
    label: `${meta.label}: ${detail}`,
    colorClass: meta.colorClass,
  };
}

export function markerPercent(position: number, count: number): number {
  if (count <= 1) return 50;
  return Math.min(99.5, Math.max(0.5, (position / (count - 1)) * 100));
}
