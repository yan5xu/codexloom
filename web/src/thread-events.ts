import type { LoomEvent } from "./types";

type ThreadEventListener = (event: LoomEvent) => void;

const listeners = new Map<string, Set<ThreadEventListener>>();

export function publishThreadEvent(agentId: string, event: LoomEvent) {
  if (!agentId || !event) return;
  for (const listener of listeners.get(agentId) || []) listener(event);
}

export function subscribeThreadEvents(agentId: string, listener: ThreadEventListener) {
  let agentListeners = listeners.get(agentId);
  if (!agentListeners) {
    agentListeners = new Set();
    listeners.set(agentId, agentListeners);
  }
  agentListeners.add(listener);
  return () => {
    agentListeners.delete(listener);
    if (agentListeners.size === 0) listeners.delete(agentId);
  };
}

export function threadEventSubscriberCount(agentId?: string) {
  if (agentId) return listeners.get(agentId)?.size || 0;
  let count = 0;
  for (const agentListeners of listeners.values()) count += agentListeners.size;
  return count;
}
