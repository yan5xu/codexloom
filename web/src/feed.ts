// feed.ts — turns the raw hub event stream into renderable blocks.
// Items are keyed by itemId so streamed deltas, item/updated and
// item/completed all land on the same block.
import type { HubEvent } from "./types";

export type Block =
  | { kind: "user"; ts: string; text: string }
  | {
      kind: "agentMessage";
      id: string;
      ts: string;
      variant: "req" | "res" | "notify";
      from: string;
      to: string;
      subject: string;
      body: string;
      response: string;
      replyTo?: string;
      replyCommand?: string;
    }
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
  | { kind: "image"; id: string; data: string; path?: string }
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

function imageData(value: any): string {
  if (typeof value !== "string" || !value) return "";
  if (value.startsWith("data:image/")) return value;
  return `data:image/png;base64,${value}`;
}

function imagePath(value: any): string {
  if (typeof value !== "string" || !value) return "";
  return `/api/images?path=${encodeURIComponent(value)}`;
}

function imageSrc(item: any): { data: string; path?: string } | null {
  const data = imageData(item.data || item.result);
  if (data) return { data };
  const path = item.path || item.filePath;
  const src = imagePath(path);
  if (!src) return null;
  return { data: src, path };
}

function secs(ms: any): string {
  return `${Math.round((typeof ms === "number" ? ms : 0) / 1000)}s`;
}

function childText(root: Element, name: string): string {
  return root.getElementsByTagName(name)[0]?.textContent?.trim() || "";
}

function agentMessageBlock(text: string, ts: string): Block | null {
  const raw = (text || "").trim();
  if (!raw.startsWith("<agent_message")) return null;
  try {
    const doc = new DOMParser().parseFromString(raw, "application/xml");
    const root = doc.documentElement;
    if (!root || root.nodeName !== "agent_message" || doc.getElementsByTagName("parsererror").length > 0) {
      return null;
    }
    const response = root.getAttribute("response") || "";
    const replyTo = childText(root, "reply_to");
    const variant = replyTo ? "res" : response === "required" ? "req" : "notify";
    return {
      kind: "agentMessage",
      id: root.getAttribute("id") || `msg-${Math.random().toString(16).slice(2)}`,
      ts,
      variant,
      from: childText(root, "from"),
      to: childText(root, "to"),
      subject: childText(root, "subject"),
      body: childText(root, "body"),
      response,
      replyTo: replyTo || undefined,
      replyCommand: childText(root, "reply_command") || undefined,
    };
  } catch {
    return null;
  }
}

function userBlock(ts: string, text: string): Block {
  return agentMessageBlock(text, ts) || { kind: "user", ts, text };
}

// buildHistoryBlocks converts rollout history turns into renderable Blocks.
// Shared by the initial seed (__history__) and scroll-up prepend.
function buildHistoryBlocks(turns: any[], keyPrefix: string): Block[] {
  const blocks: Block[] = [];
  for (let i = 0; i < turns.length; i++) {
    const items = turns[i].items || [];
    for (let j = 0; j < items.length; j++) {
      const it = items[j];
      const id = `${keyPrefix}-${i}-${j}`;
      switch (it.type) {
        case "user":
          blocks.push(userBlock("", it.text || ""));
          break;
        case "answer":
          blocks.push({ kind: "agent", id, text: it.text || "", streaming: false });
          break;
        case "thinking":
          blocks.push({ kind: "think", id, text: it.text || "", done: true });
          break;
        case "command":
          blocks.push({
            kind: "command", id,
            command: it.command || "",
            status: it.status || "completed",
            exitCode: it.exitCode ?? null,
            durationMs: it.durationMs ?? null,
            output: it.output || "",
          });
          break;
        case "file_change":
          blocks.push({ kind: "file", id, status: "completed", changes: it.changes || [] });
          break;
        case "image":
          {
            const image = imageSrc(it);
            if (image) blocks.push({ kind: "image", id, ...image });
          }
          break;
      }
    }
  }
  return blocks;
}

export function reduceFeed(state: FeedState, ev: HubEvent): FeedState {
  const t = ev.type || "";
  const d = ev.data || {};

  switch (t) {
    case "__history__": {
      // Seed past turns from the rollout. Guard: if real live content already
      // arrived (in-progress turn streamed in before this async seed resolved),
      // do NOT wipe it. Only "sys" markers like "— live —" don't count.
      if (state.blocks.some((b) => b.kind !== "sys")) return state;
      const blocks = buildHistoryBlocks((d as any).turns || [], "h");
      return { blocks, index: {}, approvals: {} };
    }
    case "__history_prepend__": {
      // Scroll-up lazy load: older turns prepended before the current feed.
      const older = buildHistoryBlocks((d as any).turns || [], `p${(d as any).offset || 0}`);
      return { ...state, blocks: [...older, ...state.blocks] };
    }
    case "hub/live":
      return sys(state, ev.ts, "dim", "— live —");
    case "hub/session-created":
      return sys(state, ev.ts, "dim", `session created · ${d.cwd}`);
    case "hub/user-message":
      return push(state, userBlock(ev.ts, d.text || ""));
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
      case "imageGeneration":
      case "image_generation_call":
      case "imageView": {
        const key = `i:${itemId}`;
        const image = imageSrc(item);
        if (!image) return state;
        if (state.index[key] === undefined) {
          return push(state, { kind: "image", id: itemId, ...image }, key);
        }
        return update(state, key, (b) => (b.kind === "image" ? { ...b, ...image } : b));
      }
      default: {
        // userMessage items duplicate hub/user-message; drop them.
        if (t !== "item/completed" || !item.type || item.type === "userMessage") return state;
        const key = `r:${itemId}`;
        if (state.index[key] !== undefined) return state;
        return push(
          state,
          { kind: "raw", id: itemId, type: item.type, json: JSON.stringify(item, null, 2) },
          key,
        );
      }
    }
  }

  return state;
}
