// feed.ts turns the raw CodexLoom event stream into renderable blocks.
// Items are keyed by itemId so streamed deltas, item/updated and
// item/completed all land on the same block.
import type { LoomEvent } from "./types";

export interface ExternalAttachment {
  id?: string;
  name?: string;
  mimeType?: string;
  size?: string | number;
  url?: string;
  path?: string;
}

export interface ExternalThreadContext {
  rootMessageId: string;
  truncated: boolean;
  unavailableReason?: string;
  messages: Array<{
    id: string;
    role: "root" | "reply";
    senderId?: string;
    sender: string;
    occurredAt?: string;
    body: string;
    textTruncated: boolean;
    attachments: ExternalAttachment[];
  }>;
}

export type Block =
  | { kind: "user"; ts: string; text: string; attachments: ExternalAttachment[] }
  | {
      kind: "agentMessage";
      id: string;
      ts: string;
      variant: "req" | "res" | "notify";
      from: string;
      to: string;
      subject: string;
      body: string;
      raw: string;
      response: string;
      replyTo?: string;
      replyCommand?: string;
    }
  | {
      kind: "externalMessage";
      id: string;
      inboxItemId: string;
      ts: string;
      provider: string;
      addressId: string;
      senderId: string;
      sender: string;
      conversationId: string;
      conversationType?: string;
      threadId?: string;
      membershipId?: string;
      membershipName?: string;
      membershipVersion?: number;
      expectation: string;
      replyPolicy: string;
      replyInstruction?: string;
      replyCommand?: string;
      noReplyCommand?: string;
      body: string;
      raw: string;
      attachments: ExternalAttachment[];
      threadContext?: ExternalThreadContext;
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
  | { kind: "artifact"; id: string; ts: string; artifact: ExternalAttachment }
  | {
      kind: "usage";
      id: string;
      model?: string;
      inputTokens: number;
      cachedInputTokens: number;
      outputTokens: number;
      reasoningOutputTokens: number;
      totalTokens: number;
      calls: number;
    }
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

function eventMessage(data: any) {
  const value = data?.error?.message ?? data?.message ?? data?.error ?? "";
  return typeof value === "string" ? value : JSON.stringify(value);
}

function structuredText(value: unknown): string {
  if (typeof value === "string") return value;
  if (Array.isArray(value)) {
    return value.map(structuredText).filter(Boolean).join("\n\n");
  }
  if (value && typeof value === "object") {
    const record = value as Record<string, unknown>;
    for (const key of ["text", "content", "summary"]) {
      const text = structuredText(record[key]);
      if (text) return text;
    }
  }
  return "";
}

function deltaText(delta: unknown): string {
  return structuredText(delta);
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

function childElement(root: Element, name: string): Element | null {
  return root.getElementsByTagName(name)[0] || null;
}

function directChildElement(root: Element, name: string): Element | null {
  return Array.from(root.children).find((child) => child.nodeName === name) || null;
}

function directChildText(root: Element, name: string): string {
  return directChildElement(root, name)?.textContent?.trim() || "";
}

function externalAttachments(root: Element | null): ExternalAttachment[] {
  if (!root) return [];
  return Array.from(root.children)
    .filter((child) => child.nodeName === "attachment")
    .map((attachment) => ({
      id: attachment.getAttribute("id") || undefined,
      name: attachment.getAttribute("name") || undefined,
      mimeType: attachment.getAttribute("mime_type") || undefined,
      size: attachment.getAttribute("size") || undefined,
      url: attachment.getAttribute("url") || undefined,
      path: attachment.getAttribute("path") || undefined,
    }));
}

function externalThreadContext(root: Element): ExternalThreadContext | undefined {
  const context = directChildElement(root, "thread_context");
  if (!context) return undefined;
  const messages = Array.from(context.children)
    .filter((child) => child.nodeName === "message")
    .map((message) => {
      const sender = directChildElement(message, "sender");
      const role: "root" | "reply" = message.getAttribute("role") === "root" ? "root" : "reply";
      return {
        id: message.getAttribute("id") || "",
        role,
        senderId: sender?.getAttribute("id") || undefined,
        sender: sender?.textContent?.trim() || sender?.getAttribute("id") || "Unknown sender",
        occurredAt: message.getAttribute("occurred_at") || undefined,
        body: directChildText(message, "body"),
        textTruncated: message.getAttribute("text_truncated") === "true",
        attachments: externalAttachments(directChildElement(message, "attachments")),
      };
    });
  return {
    rootMessageId: context.getAttribute("root_message_id") || "",
    truncated: context.getAttribute("truncated") === "true",
    unavailableReason: directChildText(context, "unavailable_reason") || undefined,
    messages,
  };
}

function agentMessageBody(root: Element): string {
  const body = childText(root, "body");
  // Older chub callers commonly passed multiline text as literal `\\n` sequences.
  // Interpret that legacy shape for display only; raw keeps the exact envelope.
  if (body.includes("\n") || !body.includes("\\n")) return body;
  return body.replace(/\\r\\n/g, "\n").replace(/\\n/g, "\n");
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
      body: agentMessageBody(root),
      raw,
      response,
      replyTo: replyTo || undefined,
      replyCommand: childText(root, "reply_command") || undefined,
    };
  } catch {
    return null;
  }
}

function externalMessageBlock(text: string, ts: string): Block | null {
  const raw = (text || "").trim();
  if (!raw.startsWith("<inbox_message")) return null;
  try {
    const doc = new DOMParser().parseFromString(raw, "application/xml");
    const root = doc.documentElement;
    if (!root || root.nodeName !== "inbox_message" || doc.getElementsByTagName("parsererror").length > 0) {
      return null;
    }
    const origin = directChildElement(root, "origin");
    const sender = directChildElement(root, "sender");
    const conversation = directChildElement(root, "conversation");
    const membership = directChildElement(root, "membership");
    const rawVersion = membership?.getAttribute("version") || "";
    const membershipVersion = Number.parseInt(rawVersion, 10);
    const attachments = externalAttachments(directChildElement(root, "attachments"));
    return {
      kind: "externalMessage",
      id: root.getAttribute("id") || `imsg-${Math.random().toString(16).slice(2)}`,
      inboxItemId: root.getAttribute("inbox_item_id") || "",
      ts,
      provider: origin?.getAttribute("provider") || "external",
      addressId: origin?.getAttribute("address_id") || "",
      senderId: sender?.getAttribute("id") || "",
      sender: sender?.textContent?.trim() || sender?.getAttribute("id") || "Unknown sender",
      conversationId: conversation?.getAttribute("id") || "",
      conversationType: conversation?.getAttribute("type") || undefined,
      threadId: conversation?.getAttribute("thread_id") || undefined,
      membershipId: membership?.getAttribute("id") || undefined,
      membershipName: membership?.getAttribute("name") || undefined,
      membershipVersion: Number.isFinite(membershipVersion) ? membershipVersion : undefined,
      expectation: root.getAttribute("expectation") || "optional",
      replyPolicy: directChildText(root, "reply_policy"),
      replyInstruction: directChildText(root, "reply_instruction") || undefined,
      replyCommand: directChildText(root, "reply_command") || undefined,
      noReplyCommand: directChildText(root, "no_reply_command") || undefined,
      body: directChildText(root, "body"),
      raw,
      attachments,
      threadContext: externalThreadContext(root),
    };
  } catch {
    return null;
  }
}

function humanInputResponseSummary(text: string): string {
  const raw = (text || "").trim();
  if (!raw.startsWith("<human_input_response")) return "";
  try {
    const doc = new DOMParser().parseFromString(raw, "application/xml");
    const root = doc.documentElement;
    if (!root || root.nodeName !== "human_input_response" || doc.getElementsByTagName("parsererror").length > 0) {
      return "";
    }
    const answer = directChildText(root, "answer");
    return answer ? `Owner answer · ${answer}` : "Owner answered a request";
  } catch {
    return "";
  }
}

function normalizeAttachment(value: any): ExternalAttachment {
  return {
    id: typeof value?.id === "string" ? value.id : undefined,
    name: typeof value?.name === "string" ? value.name : undefined,
    mimeType: typeof value?.mimeType === "string" ? value.mimeType : undefined,
    size: typeof value?.size === "string" || typeof value?.size === "number" ? value.size : undefined,
    url: typeof value?.url === "string" ? value.url : undefined,
    path: typeof value?.path === "string" ? value.path : undefined,
  };
}

function mergeAttachments(...groups: ExternalAttachment[][]): ExternalAttachment[] {
  const seen = new Set<string>();
  const merged: ExternalAttachment[] = [];
  for (const group of groups) {
    for (const attachment of group) {
      const keys = [attachment.id, attachment.path, attachment.url, attachment.name ? `${attachment.name}:${attachment.size}` : undefined].filter(Boolean) as string[];
      if (keys.length === 0 || keys.some((key) => seen.has(key))) continue;
      for (const key of keys) seen.add(key);
      merged.push(attachment);
    }
  }
  return merged;
}

function extractLoomAttachments(text: string): { text: string; attachments: ExternalAttachment[] } {
  const match = text.match(/(?:\n\n)?<loom_attachments\b[\s\S]*?<\/loom_attachments>\s*$/i);
  if (!match) return { text, attachments: [] };
  const attachments: ExternalAttachment[] = [];
  try {
    const root = new DOMParser().parseFromString(match[0].trim(), "application/xml").documentElement;
    for (const node of Array.from(root.getElementsByTagName("attachment"))) {
      attachments.push({
        id: node.getAttribute("id") || undefined,
        name: node.getAttribute("name") || undefined,
        mimeType: node.getAttribute("mime_type") || undefined,
        size: node.getAttribute("size") || undefined,
        path: node.getAttribute("path") || undefined,
        url: node.getAttribute("url") || undefined,
      });
    }
  } catch {
    return { text, attachments: [] };
  }
  return { text: text.slice(0, match.index).trimEnd(), attachments };
}

function userBlock(ts: string, rawText: string, rawAttachments: any[] = []): Block {
  const extracted = extractLoomAttachments(rawText);
  const special = agentMessageBlock(extracted.text, ts) || externalMessageBlock(extracted.text, ts);
  if (special) return special;
  const attachments = mergeAttachments(extracted.attachments, rawAttachments.map(normalizeAttachment));
  return { kind: "user", ts, text: extracted.text, attachments };
}

export function summarizeTask(text: string): string {
  text = extractLoomAttachments(text).text;
  const humanInput = humanInputResponseSummary(text);
  if (humanInput) return humanInput;
  const external = externalMessageBlock(text, "");
  if (external?.kind === "externalMessage") {
    const destination = external.membershipName || external.conversationId;
    return [external.provider.toUpperCase(), external.sender, destination].filter(Boolean).join(" · ");
  }
  const agentMessage = agentMessageBlock(text, "");
  if (agentMessage?.kind === "agentMessage") {
    const variant = agentMessage.variant.toUpperCase();
    const route = [agentMessage.from, agentMessage.to].filter(Boolean).join(" → ");
    return [variant, route, agentMessage.subject].filter(Boolean).join(" · ");
  }
  return text;
}

// buildHistoryBlocks converts rollout history turns into renderable Blocks.
// Shared by the initial seed (__history__) and scroll-up prepend.
function buildHistoryBlocks(turns: any[], keyPrefix: string): Block[] {
  const blocks: Block[] = [];
  for (let i = 0; i < turns.length; i++) {
    const turn = turns[i];
    const items = turn.items || [];
    for (let j = 0; j < items.length; j++) {
      const it = items[j];
      const id = `${keyPrefix}-${i}-${j}`;
      const timestamp = typeof it.timestamp === "string" ? it.timestamp : "";
      switch (it.type) {
        case "user":
          blocks.push(userBlock(timestamp, structuredText(it.text), Array.isArray(it.attachments) ? it.attachments : []));
          break;
        case "answer":
          blocks.push({ kind: "agent", id, text: structuredText(it.text), streaming: false });
          break;
        case "thinking": {
          const text = structuredText(it.text) || structuredText(it.summary) || structuredText(it.content);
          if (text.trim()) {
            blocks.push({ kind: "think", id, text, done: true });
          }
          break;
        }
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
    if (turn.usage?.totalTokens) {
      blocks.push({
        kind: "usage",
        id: turn.id || `${keyPrefix}-turn-${i}`,
        model: turn.model,
        inputTokens: turn.usage.inputTokens || 0,
        cachedInputTokens: turn.usage.cachedInputTokens || 0,
        outputTokens: turn.usage.outputTokens || 0,
        reasoningOutputTokens: turn.usage.reasoningOutputTokens || 0,
        totalTokens: turn.usage.totalTokens || 0,
        calls: turn.usage.calls || 0,
      });
    }
  }
  return blocks;
}

export function reduceFeed(state: FeedState, ev: LoomEvent): FeedState {
	const rawType = ev.type || "";
	const t = rawType === "hub/session-created"
		? "loom/agent-created"
		: rawType === "hub/session-killed"
			? "loom/agent-archived"
			: rawType.startsWith("hub/")
				? `loom/${rawType.slice("hub/".length)}`
				: rawType;
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
    case "__history_reconcile__": {
      const blocks = buildHistoryBlocks((d as any).turns || [], "r");
	  const artifacts = state.blocks.filter((block) => block.kind === "artifact");
	  return { blocks: [...blocks, ...artifacts], index: {}, approvals: {} };
    }
	case "__published_artifacts__": {
	  let next = state;
	  for (const raw of Array.isArray((d as any).artifacts) ? (d as any).artifacts : []) {
		const artifact = normalizeAttachment(raw);
		const id = artifact.id || artifact.path || artifact.url;
		if (!id || next.blocks.some((block) => block.kind === "artifact" && block.id === id)) continue;
		next = push(next, { kind: "artifact", id, ts: raw.publishedAt || raw.createdAt || ev.ts, artifact });
	  }
	  return next;
	}
    case "__history_prepend__": {
      // Scroll-up lazy load: older turns prepended before the current feed.
      const older = buildHistoryBlocks((d as any).turns || [], `p${(d as any).offset || 0}`);
      return { ...state, blocks: [...older, ...state.blocks] };
    }
    case "__turn_usage__": {
      const turn = (d as any).turn;
      if (!turn?.usage?.totalTokens || state.blocks.some((block) => block.kind === "usage" && block.id === turn.id)) return state;
      return push(state, {
        kind: "usage",
        id: turn.id,
        model: turn.model,
        inputTokens: turn.usage.inputTokens || 0,
        cachedInputTokens: turn.usage.cachedInputTokens || 0,
        outputTokens: turn.usage.outputTokens || 0,
        reasoningOutputTokens: turn.usage.reasoningOutputTokens || 0,
        totalTokens: turn.usage.totalTokens || 0,
        calls: turn.usage.calls || 0,
      });
    }
    case "loom/live":
      return sys(state, ev.ts, "dim", "— live —");
    case "loom/agent-created":
      return sys(state, ev.ts, "dim", `agent created · ${d.cwd}`);
    case "loom/user-message":
      return push(state, userBlock(ev.ts, d.text || "", Array.isArray(d.attachments) ? d.attachments : []));
    case "loom/artifact-published": {
      const artifact = normalizeAttachment(d.artifact);
      if (!artifact.id && !artifact.path && !artifact.url) return state;
	  const id = artifact.id || `${ev.seq}`;
	  if (state.blocks.some((block) => block.kind === "artifact" && block.id === id)) return state;
	  return push(state, { kind: "artifact", id, ts: ev.ts, artifact });
    }
    case "loom/turn-started":
      return sys(state, ev.ts, "dim", `turn started ${d.turnId || ""}`);
    case "loom/turn-completed":
      return sys(finishStreaming(state), ev.ts, "ok", `✔ turn completed (${secs(d.durationMs)})`);
    case "loom/turn-interrupted":
      return sys(finishStreaming(state), ev.ts, "warn", `■ interrupted ${d.reason || d.error || ""}`);
    case "loom/turn-failed":
      return sys(finishStreaming(state), ev.ts, "err", `✖ failed: ${d.error || ""}`);
    case "loom/error":
    case "loom/host-error":
      return sys(state, ev.ts, "err", `CodexLoom error: ${d.message || ""}`);
    case "warning":
      return sys(state, ev.ts, "warn", eventMessage(d) || "Codex warning");
    case "error":
      return sys(finishStreaming(state), ev.ts, "err", eventMessage(d) || "Codex turn failed");
    case "loom/agent-archived":
      return sys(state, ev.ts, "warn", "agent archived");
    case "loom/approval-requested": {
      const approvals = { ...state.approvals, [d.approvalId]: { method: d.method, params: d.params } };
      return sys({ ...state, approvals }, ev.ts, "warn", `⚠ approval requested: ${d.method}`);
    }
    case "loom/approval-resolved": {
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
        if (!text.trim()) return state;
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
        const text = structuredText(item.text) || structuredText(item.content);
        if (state.index[key] === undefined) {
          if (!done || !text) return state;
          const isFinal = item.phase === "final_answer" || !item.phase;
          return isFinal
            ? push(state, { kind: "agent", id: itemId, text, streaming: false }, key)
            : push(state, { kind: "think", id: itemId, text, done: true }, `t:${itemId}`);
        }
        return update(state, key, (b) =>
          b.kind === "agent" ? { ...b, text: text || b.text, streaming: !done } : b,
        );
      }
      case "reasoning": {
        const key = `t:${itemId}`;
        const text =
          structuredText(item.text) || structuredText(item.summary) || structuredText(item.content);
        if (state.index[key] === undefined) {
          if (!text.trim()) return state;
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
        // userMessage items duplicate the CodexLoom user-message event.
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
