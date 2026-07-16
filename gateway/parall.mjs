#!/usr/bin/env node

import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

import { inboxDispatchAction, parallConversationCandidates, parallConversationType, parallDeliveryReceipts, parallOutboxRequest, parallProviderReadRequest, parallThreadContext } from './parall-protocol.mjs';

const args = parseArgs(process.argv.slice(2));
const config = {
  hub: trimSlash(
    args.service || args.hub || process.env.CODEX_LOOM_URL || process.env.CHUB_URL || 'http://127.0.0.1:4870',
  ),
  connectionId: required(
    args.connection || process.env.CODEX_LOOM_CONNECTION_ID || process.env.CHUB_CONNECTION_ID,
    'connection id',
  ),
  addressId: required(
    args.address || process.env.CODEX_LOOM_ADDRESS_ID || process.env.CHUB_ADDRESS_ID,
    'address id',
  ),
  connectorToken: process.env.CODEX_LOOM_CONNECTOR_TOKEN || process.env.CODEX_HUB_CONNECTOR_TOKEN || '',
  apiUrl: trimSlash(required(process.env.PRLL_API_URL, 'PRLL_API_URL')),
  wsUrl: process.env.PRLL_WS_URL || parallWebSocketURL(process.env.PRLL_API_URL),
  apiKey: required(process.env.PRLL_API_KEY, 'PRLL_API_KEY'),
  orgId: required(process.env.PRLL_ORG_ID, 'PRLL_ORG_ID'),
  stateFile:
    args['state-file'] || path.join(defaultDataDir(), 'gateway', `parall-${args.connection}.json`),
  pollOnly: args['poll-only'] === 'true',
  pollInbound: args['poll-inbound'] === 'true',
  includeReceived: args['include-received'] === 'true',
};

const controller = new AbortController();
process.on('SIGINT', () => controller.abort());
process.on('SIGTERM', () => controller.abort());

await fs.promises.mkdir(path.dirname(config.stateFile), { recursive: true });
let state = await readState(config.stateFile);
state.dispatchMonitors ||= {};
let stateWrite = Promise.resolve();
const activeDispatchMonitors = new Set();
const chatTypes = new Map();
let ownAgentID = args['agent-id'] || process.env.PRLL_AGENT_ID || '';
if (!ownAgentID) {
  try {
    const me = await parall(`/api/v1/orgs/${config.orgId}/agents/me`);
    ownAgentID = me?.id || me?.agent_id || me?.user?.id || '';
  } catch (error) {
    console.error(`[parall] cannot resolve own agent id: ${errorText(error)}`);
  }
}

restoreDispatchMonitors();

await Promise.all([
  config.pollInbound ? runDispatchPollLoop() : config.pollOnly ? Promise.resolve() : runParallLoop(),
  runCommandLoop(),
  runHeartbeatLoop(),
  runConversationDiscoveryLoop(),
]);

async function runDispatchPollLoop() {
  while (!controller.signal.aborted) {
    try {
      await catchUpDispatches();
    } catch (error) {
      console.error(`[parall] catch-up: ${errorText(error)}`);
    }
    await delay(2000, controller.signal);
  }
}

async function runParallLoop() {
  while (!controller.signal.aborted) {
    try {
      await catchUpDispatches();
      await connectWebSocket();
    } catch (error) {
      if (!controller.signal.aborted) console.error(`[parall] ${errorText(error)}; reconnecting`);
    }
    await delay(1000, controller.signal);
  }
}

async function connectWebSocket() {
  const ticket = await parall('/api/v1/ws/ticket', { method: 'POST' });
  const target = new URL(ticket.ws_url || config.wsUrl);
  target.searchParams.set('ticket', ticket.ticket);
  if (state.lastSeq > 0) target.searchParams.set('last_seq', String(state.lastSeq));

  await new Promise((resolve, reject) => {
    const ws = new WebSocket(target);
    let heartbeat;
    let socketError;
    let settled = false;
    const abort = () => ws.close();
    controller.signal.addEventListener('abort', abort, { once: true });
    ws.onopen = () => console.error(`[parall] connected ${target.origin}`);
    ws.onerror = (event) => {
      socketError = new Error(`WebSocket error${event?.message ? `: ${event.message}` : ''}`);
      try { ws.close(); } catch {}
    };
    ws.onclose = (event) => {
      clearInterval(heartbeat);
      controller.signal.removeEventListener('abort', abort);
      if (settled) return;
      settled = true;
      if (socketError) {
        reject(new Error(`${socketError.message}; close=${event.code} ${event.reason || ''}`.trim()));
      } else {
        console.error(`[parall] disconnected close=${event.code} ${event.reason || ''}`.trim());
        resolve();
      }
    };
    ws.onmessage = async ({ data }) => {
      try {
        const frame = JSON.parse(String(data));
        if (Number.isFinite(frame.seq) && frame.seq > state.lastSeq) {
          state.lastSeq = frame.seq;
          await persistState();
        }
        if (frame.type === 'hello') {
          const seconds = Number(frame.data?.heartbeat_interval || 30);
          clearInterval(heartbeat);
          heartbeat = setInterval(() => {
            if (ws.readyState === WebSocket.OPEN) {
              ws.send(JSON.stringify({ type: 'ping', data: { ts: Date.now() } }));
            }
          }, Math.max(5, seconds) * 1000);
          return;
        }
        if (frame.type === 'dispatch.new') await handleDispatch(frame.data);
      } catch (error) {
        console.error(`[parall] frame: ${errorText(error)}`);
      }
    };
  });
}

async function catchUpDispatches() {
  let cursor = '';
  for (let page = 0; page < 40; page++) {
    const query = new URLSearchParams({ limit: '50' });
    if (cursor) query.set('cursor', cursor);
    const response = await parall(`/api/v1/orgs/${config.orgId}/dispatch?${query}`);
    for (const dispatch of response.data || []) await handleDispatch(dispatch);
    cursor = response.next_cursor || '';
    if (!response.has_more || !cursor) break;
  }
}

async function handleDispatch(dispatch) {
  if (!dispatch?.id || dispatch.event_type !== 'message' || !dispatch.source_id) return;
  if (dispatch.status && dispatch.status !== 'pending' && !(config.includeReceived && dispatch.status === 'received')) return;
  if (activeDispatchMonitors.has(dispatch.id) || state.dispatchMonitors[dispatch.id]) return;
  const message = await parall(`/api/v1/messages/${dispatch.source_id}`);
  if (ownAgentID && message.sender_id === ownAgentID) {
    await ackDispatch(dispatch.id);
    return;
  }
  const attachments = (message.attachments || []).map((item) => ({
    id: item.id,
    name: item.file_name || item.name,
    mimeType: item.mime_type || item.content_type,
    size: item.file_size || item.size,
    url: item.url || item.download_url || item.web_url,
  }));
  const text = message.content?.text || '';
  if (!text && attachments.length === 0) {
    await ackDispatch(dispatch.id);
    return;
  }
  const conversationType = await resolveConversationType(message.chat_id, message.thread_root_id);
  const threadContext = await readThreadContext(message);
  const ingress = {
    connectionId: config.connectionId,
    addressId: config.addressId,
    externalEventId: dispatch.id,
    externalMessageId: message.id,
    sender: {
      externalId: message.sender_id,
      displayName: message.sender?.display_name || message.sender_id,
      kind: message.sender?.type || 'unknown',
    },
    conversation: {
      conversationId: message.chat_id,
      threadId: message.thread_root_id || '',
      messageId: message.id,
      conversationType,
    },
    content: { text, attachments },
    threadContext,
    responseExpectation: message.hints?.no_reply ? 'none' : 'optional',
    occurredAt: message.created_at,
    trigger: { direct: false, mentioned: true, explicitDispatch: true },
    providerMetadata: {
      dispatchId: dispatch.id,
      sourceType: dispatch.source_type,
      sourceId: dispatch.source_id,
      deliveryReason: dispatch.delivery_reason,
    },
  };
  const result = await hub('/api/integrations/ingress', { method: 'POST', body: ingress });
  if (result.ignored) {
    await ackDispatch(dispatch.id);
    console.error(`[parall] ignored ${message.id}: ${result.reason}`);
  } else {
    if (!result.inboxItem?.id) throw new Error(`Hub accepted ${message.id} without an inbox item`);
    const record = {
      dispatchId: dispatch.id,
      sourceType: dispatch.source_type || 'message',
      sourceId: dispatch.source_id,
      inboxItemId: result.inboxItem.id,
      received: dispatch.status === 'received',
    };
    state.dispatchMonitors[dispatch.id] = record;
    await persistState();
    startDispatchMonitor(record);
    console.error(`[parall] accepted ${message.id} from ${ingress.sender.displayName}`);
  }
}

async function readThreadContext(message) {
  const rootID = String(message?.thread_root_id || '').trim();
  if (!rootID) return undefined;
  try {
    const [root, replies] = await Promise.all([
      parall(`/api/v1/messages/${encodeURIComponent(rootID)}`),
      parall(`/api/v1/messages/${encodeURIComponent(rootID)}/replies?limit=20`),
    ]);
    return parallThreadContext(root, replies, message);
  } catch (error) {
    const reason = errorText(error).slice(0, 500);
    console.error(`[parall] thread context ${rootID}: ${reason}`);
    return { rootExternalMessageId: rootID, unavailableReason: reason };
  }
}

async function resolveConversationType(chatID, threadRootID) {
  if (threadRootID) return parallConversationType('', threadRootID);
  let chatType = chatTypes.get(chatID);
  if (!chatType) {
    try {
      const chat = await parall(`/api/v1/orgs/${config.orgId}/chats/${chatID}`);
      chatType = chat?.type || 'group';
      chatTypes.set(chatID, chatType);
    } catch (error) {
      console.error(`[parall] read chat ${chatID}: ${errorText(error)}`);
      chatType = 'group';
    }
  }
  return parallConversationType(chatType, '');
}

function restoreDispatchMonitors() {
  for (const record of Object.values(state.dispatchMonitors)) startDispatchMonitor(record);
}

function startDispatchMonitor(record) {
  if (!record?.dispatchId || activeDispatchMonitors.has(record.dispatchId)) return;
  activeDispatchMonitors.add(record.dispatchId);
  void monitorDispatch(record)
    .catch((error) => console.error(`[parall] monitor ${record.dispatchId}: ${errorText(error)}`))
    .finally(() => activeDispatchMonitors.delete(record.dispatchId));
}

async function monitorDispatch(record) {
  while (!controller.signal.aborted) {
    try {
      const entry = await hub(`/api/inbox/${record.inboxItemId}`);
      const action = inboxDispatchAction(entry, record.received);
      if (action === 'mark_received') {
        await parall(`/api/v1/orgs/${config.orgId}/dispatch/received`, {
          method: 'POST',
          body: { source_type: record.sourceType, source_id: record.sourceId },
        });
        record.received = true;
        state.dispatchMonitors[record.dispatchId] = record;
        await persistState();
        console.error(`[parall] reading ${record.sourceId}`);
        continue;
      }
      if (action === 'ack') {
        await ackDispatch(record.dispatchId);
        delete state.dispatchMonitors[record.dispatchId];
        await persistState();
        console.error(`[parall] resolved ${record.sourceId}`);
        return;
      }
    } catch (error) {
      console.error(`[parall] monitor ${record.dispatchId}: ${errorText(error)}`);
    }
    await delay(500, controller.signal);
  }
}

async function ackDispatch(dispatchId) {
  await parall(`/api/v1/orgs/${config.orgId}/dispatch/${dispatchId}/ack`, { method: 'POST' });
}

async function runCommandLoop() {
  while (!controller.signal.aborted) {
    try {
      const response = await fetch(
        `${config.hub}/api/integrations/connections/${config.connectionId}/commands`,
        { headers: connectorHeaders(), signal: controller.signal },
      );
      if (!response.ok) throw new Error(`command stream HTTP ${response.status}`);
      await consumeSSE(response.body, async (envelope) => {
        if (envelope.type !== 'connector/command') return;
        if (envelope.data?.type === 'provider_operation') {
          await runProviderOperation(envelope.data);
        } else {
          await sendOutbox(envelope.data);
        }
      });
    } catch (error) {
      if (!controller.signal.aborted) console.error(`[hub] ${errorText(error)}; reconnecting commands`);
    }
    await delay(1000, controller.signal);
  }
}

async function runProviderOperation(command) {
  const operation = command?.providerOperation;
  if (!operation?.id) return;
  try {
    if (operation.provider !== 'parall') throw new Error(`unsupported provider ${operation.provider}`);
    const request = parallProviderReadRequest(operation, config.orgId);
    const result = await parall(request.resource, { method: request.method });
    const encoded = JSON.stringify(result);
    if (Buffer.byteLength(encoded) > 700 * 1024) {
      throw new Error('Parall result exceeds the 700 KiB Connector limit; request a smaller page');
    }
    await reportProviderOperation(operation.id, { attemptToken: operation.attemptToken, success: true, result });
    console.error(`[parall] read ${operation.resource} ${operation.action} (${operation.id})`);
  } catch (error) {
    await reportProviderOperation(operation.id, { attemptToken: operation.attemptToken, success: false, error: errorText(error) }).catch(() => {});
    console.error(`[parall] read ${operation.id}: ${errorText(error)}`);
  }
}

async function sendOutbox(command) {
  const item = command?.outboxItem;
  if (!item?.id) return;
  let deliveryReceipts = [];
  try {
    const attachmentIds = [];
    for (const attachment of item.content?.attachments || []) {
      const attachmentId = await uploadOutboxAttachment(attachment);
      attachmentIds.push(attachmentId);
      deliveryReceipts = parallDeliveryReceipts({ content: { attachments: (item.content?.attachments || []).slice(0, attachmentIds.length) } }, attachmentIds);
    }
    const request = parallOutboxRequest(item, attachmentIds);
    const message = await parall(
      `/api/v1/orgs/${config.orgId}/chats/${item.conversation.conversationId}/messages`,
      { method: 'POST', body: request },
    );
    deliveryReceipts = parallDeliveryReceipts(item, attachmentIds, message.id);
    await reportOutbox(item.id, {
      attemptToken: item.attemptToken,
      success: true,
      externalMessageId: message.id,
      externalMessageIds: [message.id],
      deliveryReceipts,
    });
    console.error(`[parall] sent ${item.id} as ${message.id}`);
  } catch (error) {
    await reportOutbox(item.id, { attemptToken: item.attemptToken, success: false, deliveryReceipts, error: errorText(error) }).catch(() => {});
    console.error(`[parall] send ${item.id}: ${errorText(error)}`);
  }
}

async function uploadOutboxAttachment(attachment) {
  const localPath = path.resolve(String(attachment?.path || '').trim());
  const stat = await fs.promises.stat(localPath);
  if (!stat.isFile() || stat.size <= 0 || stat.size > 25 * 1024 * 1024) {
    throw new Error(`invalid outbound attachment: ${localPath}`);
  }
  const fileName = path.basename(String(attachment?.name || '').trim() || localPath);
  const mimeType = String(attachment?.mimeType || '').trim() || 'application/octet-stream';
  const presign = await parall(`/api/v1/orgs/${config.orgId}/upload/presign`, {
    method: 'POST',
    body: { file_name: fileName, file_size: stat.size, mime_type: mimeType },
  });
  const attachmentId = String(presign?.attachment_id || '').trim();
  const uploadUrl = String(presign?.upload_url || '').trim();
  if (!attachmentId || !uploadUrl) throw new Error('Parall upload presign returned no attachment id or URL');
  const upload = await fetch(uploadUrl, {
    method: 'PUT',
    headers: { 'Content-Type': mimeType },
    body: await fs.promises.readFile(localPath),
    signal: controller.signal,
  });
  if (!upload.ok) throw new Error(`Parall attachment upload failed: HTTP ${upload.status}`);
  await parall(`/api/v1/orgs/${config.orgId}/upload/complete`, {
    method: 'POST', body: { attachment_id: attachmentId },
  });
  return attachmentId;
}

async function reportOutbox(id, result) {
  return hub(
    `/api/integrations/connections/${config.connectionId}/outbox/${id}/result`,
    { method: 'POST', body: result },
  );
}

async function reportProviderOperation(id, result) {
  return hub(
    `/api/integrations/connections/${config.connectionId}/provider-operations/${id}/result`,
    { method: 'POST', body: result },
  );
}

async function runHeartbeatLoop() {
  while (!controller.signal.aborted) {
    await hub(`/api/integrations/connections/${config.connectionId}/heartbeat`, {
      method: 'POST',
      body: {
        status: 'connected',
        cursor: String(state.lastSeq || ''),
        capabilities: [
          'receive_events',
          'explicit_dispatch',
          'threads',
          'thread_context',
          'attachments',
          'reading',
          'ack',
          'proactive_send',
          'provider_native_read',
        ],
      },
    }).catch((error) => console.error(`[hub] heartbeat: ${errorText(error)}`));
    await delay(10000, controller.signal);
  }
}

async function runConversationDiscoveryLoop() {
  let lastFingerprint = '';
  let lastReportedAt = 0;
  while (!controller.signal.aborted) {
    try {
      if (!ownAgentID) throw new Error('external Agent id is unavailable');
      const chats = await readJoinedConversations();
      const conversations = parallConversationCandidates(chats);
      const fingerprint = JSON.stringify(conversations);
      if (fingerprint !== lastFingerprint || Date.now() - lastReportedAt >= 300_000) {
        await hub(`/api/integrations/addresses/${encodeURIComponent(config.addressId)}/conversation-candidates`, {
          method: 'PUT',
          body: { conversations },
        });
        lastFingerprint = fingerprint;
        lastReportedAt = Date.now();
        console.error(`[parall] discovered ${conversations.length} joined group conversation(s)`);
      }
    } catch (error) {
      if (!controller.signal.aborted) console.error(`[parall] discover conversations: ${errorText(error)}`);
    }
    await delay(10000, controller.signal);
  }
}

async function readJoinedConversations() {
  const chats = [];
  let cursor = '';
  for (let page = 0; page < 50; page++) {
    const query = new URLSearchParams({ limit: '100' });
    if (cursor) query.set('cursor', cursor);
    const response = await parall(
      `/api/v1/orgs/${encodeURIComponent(config.orgId)}/members/${encodeURIComponent(ownAgentID)}/chats?${query}`,
    );
    chats.push(...(response.data || []));
    const nextCursor = String(response.next_cursor || '').trim();
    if (!response.has_more || !nextCursor || nextCursor === cursor) break;
    cursor = nextCursor;
  }
  return chats;
}

async function parall(resource, options = {}) {
  const headers = {
    Authorization: `Bearer ${config.apiKey}`,
    'Content-Type': 'application/json',
    ...(options.headers || {}),
  };
  const response = await fetch(`${config.apiUrl}${resource}`, {
    ...options,
    headers,
    body: options.body === undefined ? undefined : JSON.stringify(options.body),
    signal: options.signal || controller.signal,
  });
  const data = await response.json().catch(() => ({}));
  if (!response.ok) {
    const detail = data.error || data.message || response.statusText;
    throw new Error(`Parall HTTP ${response.status}: ${typeof detail === 'string' ? detail : JSON.stringify(detail)}`);
  }
  return data;
}

async function hub(resource, options = {}) {
  const response = await fetch(`${config.hub}${resource}`, {
    ...options,
    headers: { 'Content-Type': 'application/json', ...connectorHeaders(), ...(options.headers || {}) },
    body: options.body === undefined ? undefined : JSON.stringify(options.body),
    signal: options.signal || controller.signal,
  });
  const data = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(`hub HTTP ${response.status}: ${data.error || response.statusText}`);
  return data;
}

async function consumeSSE(stream, handler) {
  const reader = stream.getReader();
  const decoder = new TextDecoder();
  let buffer = '';
  while (!controller.signal.aborted) {
    const { value, done } = await reader.read();
    if (done) return;
    buffer += decoder.decode(value, { stream: true });
    let boundary;
    while ((boundary = buffer.indexOf('\n\n')) >= 0) {
      const block = buffer.slice(0, boundary);
      buffer = buffer.slice(boundary + 2);
      const data = block
        .split('\n')
        .filter((line) => line.startsWith('data: '))
        .map((line) => line.slice(6))
        .join('\n');
      if (!data) continue;
      const envelope = JSON.parse(data);
      await handler(envelope);
    }
  }
}

function connectorHeaders() {
  return config.connectorToken ? { 'X-Codex-Loom-Connector-Token': config.connectorToken } : {};
}

function defaultDataDir() {
  if (process.env.CODEX_LOOM_DATA) return process.env.CODEX_LOOM_DATA;
  if (process.env.CODEX_HUB_DATA) return process.env.CODEX_HUB_DATA;
  const current = path.join(os.homedir(), '.codex-loom');
  return fs.existsSync(current) ? current : path.join(os.homedir(), '.codex-hub');
}

async function readState(file) {
  try {
    return JSON.parse(await fs.promises.readFile(file, 'utf8'));
  } catch {
    return { lastSeq: 0 };
  }
}

async function writeState(file, value) {
  const tmp = `${file}.tmp`;
  await fs.promises.writeFile(tmp, JSON.stringify(value, null, 2));
  await fs.promises.rename(tmp, file);
}

function persistState() {
  stateWrite = stateWrite.catch(() => {}).then(() => writeState(config.stateFile, state));
  return stateWrite;
}

function parseArgs(values) {
  const result = {};
  for (let i = 0; i < values.length; i++) {
    if (!values[i].startsWith('--')) continue;
    const key = values[i].slice(2);
    result[key] = values[i + 1] && !values[i + 1].startsWith('--') ? values[++i] : 'true';
  }
  return result;
}

function required(value, name) {
  if (!value) throw new Error(`${name} is required`);
  return value;
}

function trimSlash(value) {
  return String(value).replace(/\/+$/, '');
}

function parallWebSocketURL(apiURL) {
  const target = new URL(apiURL);
  target.protocol = target.protocol === 'http:' ? 'ws:' : 'wss:';
  target.pathname = '/ws';
  target.search = '';
  target.hash = '';
  return target.toString();
}

function errorText(error) {
  return error instanceof Error ? error.message : String(error);
}

function delay(ms, signal) {
  signal ||= controller.signal;
  if (signal.aborted) return Promise.resolve();
  return new Promise((resolve) => {
    let settled = false;
    const finish = () => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      signal.removeEventListener('abort', finish);
      resolve();
    };
    const timer = setTimeout(finish, ms);
    signal.addEventListener('abort', finish, { once: true });
  });
}
