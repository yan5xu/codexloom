// feed.ts — turns the raw hub event stream into renderable blocks.
// Items are keyed by itemId so streamed deltas, item/updated and
// item/completed all land on the same block.
import type { HubEvent } from "./types";

export type Block =
  | { kind: "user"; ts: string; text: string }
  | { kind: "agent"; id: string; text: string; streaming: boolean }
  | { kind: "think"; id: string; text: string; done: boolean }
  | {
      kind: "command";
      id: string;
      command: string;
      status: string;
      exitCode: number | null;
      durationMs: number | null;
      output: string;
    }
  | {
      kind: "file";
      id: string;
      status: string;
      changes: { path: string; kind: string; diff: string }[];
    }
  | { kind: "sys"; ts: string; cls: "ok" | "warn" | "err" | "dim"; text: string }
  | { kind: "raw"; id: string; type: string; json: string };

export interface FeedState {
  blocks: Block[];
  index: Record<string, number>;
  approvals: Record<string, { method: string; params: any }>;
}

export const emptyFeed: FeedState = { blocks: [], index: {}, approvals: {} };

function push(state: FeedState, block: Block, key?: string): FeedState {
  const blocks = [...state.blocks, block];
  const index = key ? { ...state.index, [key]: blocks.length - 1 } : state.index;
  return { ...state, blocks, index };
}

function update(state: FeedState, key: string, fn: (b: Block) => Block): FeedState {
  const idx = state.index[key];
  if (idx === undefined) return state;
  const blocks = [...state.blocks];
  blocks[idx] = fn(blocks[idx]);
  return { ...state, blocks };
}

function sys(state: FeedState, ts: string, cls: "ok" | "warn" | "err" | "dim", text: string) {
  return push(state, { kind: "sys", ts, cls, text });
}

function finishStreaming(state: FeedState): FeedState {
  const blocks = state.blocks.map((b) =>
    b.kind === "agent" && b.streaming ? { ...b, streaming: false } : b,
  );
  return { ...state, blocks };
}

function deltaText(delta: any): string {
  if (typeof delta === "string") return delta;
  if (delta && typeof delta.text === "string") return delta.text;
  return "";
}

function kindString(k: any): string {
  if (typeof k === "string") return k;
  if (k && typeof k.type === "string") return k.type;
  return "";
}

function secs(ms: any): string {
  return `${Math.round((typeof ms === "number" ? ms : 0) / 1000)}s`;
}

export function reduceFeed(state: FeedState, ev: HubEvent): FeedState {
  const t = ev.type || "";
  const d = ev.data || {};

  switch (t) {
    case "__history__": {
      // Seed past turns read from the codex rollout file (mirror/idle sessions
      // have no live event log). Builds blocks directly from history items.
      let s: FeedState = { blocks: [], index: {}, approvals: {} };
      const turns = (d as any).turns || [];
      for (let i = 0; i < turns.length; i++) {
        const items = turns[i].items || [];
        for (let j = 0; j < items.length; j++) {
          const it = items[j];
          const id = `h${i}-${j}`;
          switch (it.type) {
            case "user":
              s = push(s, { kind: "user", ts: "", text: it.text || "" });
              break;
            case "answer":
              s = push(s, { kind: "agent", id, text: it.text || "", streaming: false });
              break;
            case "thinking":
              s = push(s, { kind: "think", id, text: it.text || "", done: true });
              break;
            case "command":
              s = push(s, {
                kind: "command", id,
                command: it.command || "",
                status: it.status || "completed",
                exitCode: it.exitCode ?? null,
                durationMs: it.durationMs ?? null,
                output: it.output || "",
              });
              break;
            case "file_change":
              s = push(s, {
                kind: "file", id,
                status: "completed",
                changes: it.changes || [],
              });
              break;
          }
        }
      }
      return s;
    }
    case "hub/live":
      return sys(state, ev.ts, "dim", "— live —");
    case "hub/session-created":
      return sys(state, ev.ts, "dim", `session created · ${d.cwd}`);
    case "hub/user-message":
      return push(state, { kind: "user", ts: ev.ts, text: d.text || "" });
    case "hub/turn-started":
      return sys(state, ev.ts, "dim", `turn started ${d.turnId || ""}`);
    case "hub/turn-completed":
      return sys(finishStreaming(state), ev.ts, "ok", `✔ turn completed (${secs(d.durationMs)})`);
    case "hub/turn-interrupted":
      return sys(finishStreaming(state), ev.ts, "warn", `■ interrupted ${d.reason || d.error || ""}`);
    case "hub/turn-failed":
      return sys(finishStreaming(state), ev.ts, "err", `✖ failed: ${d.error || ""}`);
    case "hub/error":
      return sys(state, ev.ts, "err", `hub error: ${d.message || ""}`);
    case "hub/session-killed":
      return sys(state, ev.ts, "warn", "session killed");
    case "hub/approval-requested": {
      const approvals = { ...state.approvals, [d.approvalId]: { method: d.method, params: d.params } };
      return sys({ ...state, approvals }, ev.ts, "warn", `⚠ approval requested: ${d.method}`);
    }
    case "hub/approval-resolved": {
      const approvals = { ...state.approvals };
      delete approvals[d.approvalId];
      return sys(
        { ...state, approvals },
        ev.ts,
        d.decision === "accept" ? "ok" : "warn",
        `approval ${d.decision}`,
      );
    }
  }

  // Streaming deltas.
  if (t.endsWith("/delta")) {
    const text = deltaText(d.delta);
    if (!text) return state;
    const itemId = d.itemId || "stream";
    if (t.includes("reasoning")) {
      const key = `t:${itemId}`;
      if (state.index[key] === undefined) {
        return push(state, { kind: "think", id: itemId, text, done: false }, key);
      }
      return update(state, key, (b) => (b.kind === "think" ? { ...b, text: b.text + text } : b));
    }
    if (t.includes("agentMessage")) {
      const key = `a:${itemId}`;
      if (state.index[key] === undefined) {
        return push(state, { kind: "agent", id: itemId, text, streaming: true }, key);
      }
      return update(state, key, (b) => (b.kind === "agent" ? { ...b, text: b.text + text } : b));
    }
    return state;
  }

  // Item lifecycle.
  if (t === "item/started" || t === "item/updated" || t === "item/completed") {
    const item = d.item || {};
    const itemId = item.id || `anon-${state.blocks.length}`;
    switch (item.type) {
      case "agentMessage": {
        const key = `a:${itemId}`;
        const done = t === "item/completed";
        if (state.index[key] === undefined) {
          if (!done || !item.text) return state;
          const isFinal = item.phase === "final_answer" || !item.phase;
          return isFinal
            ? push(state, { kind: "agent", id: itemId, text: item.text, streaming: false }, key)
            : push(state, { kind: "think", id: itemId, text: item.text, done: true }, `t:${itemId}`);
        }
        return update(state, key, (b) =>
          b.kind === "agent" ? { ...b, text: item.text || b.text, streaming: !done } : b,
        );
      }
      case "reasoning": {
        const key = `t:${itemId}`;
        const text = item.text || item.summary || "";
        if (state.index[key] === undefined) {
          if (!text) return state;
          return push(state, { kind: "think", id: itemId, text, done: t === "item/completed" }, key);
        }
        return update(state, key, (b) =>
          b.kind === "think"
            ? { ...b, text: text || b.text, done: t === "item/completed" }
            : b,
        );
      }
      case "commandExecution": {
        const key = `c:${itemId}`;
        const patch = {
          command: item.command || "",
          status: item.status || (t === "item/completed" ? "completed" : "running"),
          exitCode: item.exitCode ?? null,
          durationMs: item.durationMs ?? null,
          output: item.aggregatedOutput || item.output || "",
        };
        if (state.index[key] === undefined) {
          return push(state, { kind: "command", id: itemId, ...patch }, key);
        }
        return update(state, key, (b) => (b.kind === "command" ? { ...b, ...patch } : b));
      }
      case "fileChange": {
        const key = `f:${itemId}`;
        const changes = (item.changes || []).map((c: any) => ({
          path: c.path || c.filePath || "",
          kind: kindString(c.kind) || c.action || "",
          diff: c.diff || "",
        }));
        const status = item.status || (t === "item/completed" ? "done" : "editing");
        if (state.index[key] === undefined) {
          return push(state, { kind: "file", id: itemId, status, changes }, key);
        }
        return update(state, key, (b) => (b.kind === "file" ? { ...b, status, changes } : b));
      }
      default: {
        // userMessage items duplicate hub/user-message; drop them.
        if (t !== "item/completed" || !item.type || item.type === "userMessage") return state;
        const key = `r:${itemId}`;
        if (state.index[key] !== undefined) return state;
        return push(
          state,
          { kind: "raw", id: itemId, type: item.type, json: JSON.stringify(item, null, 2).slice(0, 3000) },
          key,
        );
      }
    }
  }

  return state;
}
