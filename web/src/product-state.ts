import type { Agent, InboxEntry } from "./types";

export type ExecutionState = "starting" | "running" | "idle" | "draining" | "unavailable";

export function executionState(agent: Agent): ExecutionState {
  const status = String(agent.status || "").toLowerCase();
  if (status === "running") return "running";
  if (status === "starting") return "starting";
  if (status === "draining") return "draining";
  if (status === "idle") return "idle";
  return "unavailable";
}

export function isAgentExecuting(agent: Agent) {
  return executionState(agent) === "running";
}

export function executionLabel(agent: Agent) {
  const state = executionState(agent);
  return state.charAt(0).toUpperCase() + state.slice(1);
}

export function executionDotClass(agent: Agent) {
  switch (executionState(agent)) {
    case "running": return "bg-success ring-2 ring-success/20";
    case "starting": return "bg-warning ring-2 ring-warning/15";
    case "draining": return "bg-warning";
    case "idle": return "bg-muted-foreground/35";
    default: return "bg-destructive";
  }
}

export function isOwnerResultEvent(event: any) {
  if (!["loom/turn-completed", "loom/turn-failed", "loom/turn-interrupted"].includes(event?.type)) return false;
  const source = String(event?.data?.source || "");
  return source === "owner" || source === "remote";
}

export function oldestWaitingMs(entries: InboxEntry[], now = Date.now()) {
  let oldest = 0;
  for (const entry of entries) {
    const value = Date.parse(entry.message.receivedAt || entry.item.createdAt || "");
    if (Number.isFinite(value)) oldest = Math.max(oldest, Math.max(0, now - value));
  }
  return oldest;
}
