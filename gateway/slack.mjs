#!/usr/bin/env node

import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

import { slackDeliveryReceipts, slackEventToIngress, slackOutboxRequest, slackReactionAction } from './slack-protocol.mjs';

const args = parseArgs(process.argv.slice(2));
const socketEnabled = args.socket !== 'false';
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
  apiUrl: trimSlash(process.env.SLACK_API_URL || 'https://slack.com/api'),
  appToken: socketEnabled ? required(process.env.SLACK_APP_TOKEN, 'SLACK_APP_TOKEN') : '',
  botToken: required(process.env.SLACK_BOT_TOKEN, 'SLACK_BOT_TOKEN'),
  botUserId: args['bot-user-id'] || process.env.SLACK_BOT_USER_ID || '',
  teamId: args['team-id'] || process.env.SLACK_TEAM_ID || '',
  stateFile: args['state-file'] || path.join(defaultDataDir(), 'gateway', `slack-${args.connection}.json`),
  socketEnabled,
};

const controller = new AbortController();
process.on('SIGINT', () => controller.abort());
process.on('SIGTERM', () => controller.abort());

await fs.promises.mkdir(path.dirname(config.stateFile), { recursive: true });
const state = await readState(config.stateFile);
state.pending ||= {};
state.seen ||= {};
state.reactionMonitors ||= {};
state.outboxResults ||= {};
state.users ||= {};
let stateWrite = Promise.resolve();
let socketConnected = !config.socketEnabled;
let lastSocketError = '';
const activeReactionMonitors = new Set();

const auth = await slack('auth.test', { token: config.botToken });
config.botUserId ||= auth.user_id || '';
config.teamId ||= auth.team_id || '';
if (!config.botUserId) throw new Error('Slack auth.test returned no bot user id');

restoreReactionMonitors();
await Promise.all([
  config.socketEnabled ? runSocketLoop() : Promise.resolve(),
  runPendingLoop(),
  runCommandLoop(),
  runHeartbeatLoop(),
]);

async function runSocketLoop() {
  while (!controller.signal.aborted) {
    try {
      const ticket = await slack('apps.connections.open', { token: config.appToken });
      if (!ticket.url) throw new Error('apps.connections.open returned no WebSocket URL');
      await connectSocket(ticket.url);
    } catch (error) {
      socketConnected = false;
      lastSocketError = errorText(error);
      if (!controller.signal.aborted) console.error(`[slack] ${lastSocketError}; reconnecting Socket Mode`);
    }
    await delay(1000);
  }
}

async function connectSocket(url) {
  await new Promise((resolve, reject) => {
    const ws = new WebSocket(url);
    let settled = false;
    let socketError;
    const finish = (error) => {
      if (settled) return;
      settled = true;
      controller.signal.removeEventListener('abort', abort);
      socketConnected = false;
      if (error) reject(error);
      else resolve();
    };
    const abort = () => {
      try { ws.close(); } catch {}
      finish();
    };
    controller.signal.addEventListener('abort', abort, { once: true });
    ws.onopen = () => {
      socketConnected = true;
      lastSocketError = '';
      console.error(`[slack] Socket Mode connected for ${config.teamId || 'workspace'}`);
    };
    ws.onerror = (event) => {
      socketError = new Error(`WebSocket error${event?.message ? `: ${event.message}` : ''}`);
    };
    ws.onclose = (event) => {
      const detail = `close=${event.code} ${event.reason || ''}`.trim();
      finish(socketError || (controller.signal.aborted ? undefined : new Error(`Socket Mode disconnected ${detail}`)));
    };
    ws.onmessage = ({ data }) => {
      void handleSocketFrame(ws, data).catch((error) => {
        console.error(`[slack] frame: ${errorText(error)}`);
      });
    };
  });
}

async function handleSocketFrame(ws, raw) {
  const frame = JSON.parse(String(raw));
  if (frame.type === 'hello') return;
  if (frame.type === 'disconnect') {
    console.error(`[slack] disconnect requested: ${frame.reason || 'unknown'}`);
    ws.close();
    return;
  }
  if (!frame.envelope_id) return;

  const eventKey = frame.payload?.event_id || frame.envelope_id;
  if (!state.seen[eventKey] && !state.pending[eventKey]) {
    state.pending[eventKey] = frame;
    await persistState();
  }
  if (ws.readyState === WebSocket.OPEN) {
    ws.send(JSON.stringify({ envelope_id: frame.envelope_id }));
  }
}

async function runPendingLoop() {
  while (!controller.signal.aborted) {
    const entry = Object.entries(state.pending)[0];
    if (!entry) {
      await delay(300);
      continue;
    }
    const [eventKey, envelope] = entry;
    try {
      await processEnvelope(envelope);
      delete state.pending[eventKey];
      state.seen[eventKey] = new Date().toISOString();
      trimRecord(state.seen, 2000);
      await persistState();
    } catch (error) {
      console.error(`[slack] ingest ${eventKey}: ${errorText(error)}`);
      await delay(1000);
    }
  }
}

async function processEnvelope(envelope) {
  const event = envelope?.payload?.event || {};
  const context = {
    connectionId: config.connectionId,
    addressId: config.addressId,
    botUserId: config.botUserId,
    teamId: config.teamId,
    senderName: '',
  };
  const ingress = slackEventToIngress(envelope, context);
  if (!ingress) return;
  ingress.sender.displayName = event.user ? await resolveUserName(event.user) : ingress.sender.displayName;
  const result = await hub('/api/integrations/ingress', { method: 'POST', body: ingress });
  if (result.ignored) {
    console.error(`[slack] ignored ${ingress.externalMessageId}: ${result.reason}`);
    return;
  }
  if (!result.inboxItem?.id) throw new Error(`Hub accepted ${ingress.externalMessageId} without an inbox item`);
  const record = {
    channel: ingress.conversation.conversationId,
    timestamp: ingress.externalMessageId,
    inboxItemId: result.inboxItem.id,
    added: false,
  };
  state.reactionMonitors[record.timestamp] = record;
  await persistState();
  startReactionMonitor(record);
  console.error(`[slack] accepted ${record.timestamp} from ${ingress.sender.displayName}`);
}

async function resolveUserName(userId) {
  if (state.users[userId]) return state.users[userId];
  try {
    const result = await slack('users.info', { token: config.botToken, body: { user: userId } });
    const profile = result.user?.profile || {};
    const name = profile.display_name || profile.real_name || result.user?.real_name || result.user?.name || userId;
    state.users[userId] = name;
    trimRecord(state.users, 1000);
    await persistState();
    return name;
  } catch (error) {
    console.error(`[slack] users.info ${userId}: ${errorText(error)}`);
    return userId;
  }
}

function restoreReactionMonitors() {
  for (const record of Object.values(state.reactionMonitors)) startReactionMonitor(record);
}

function startReactionMonitor(record) {
  if (!record?.timestamp || activeReactionMonitors.has(record.timestamp)) return;
  activeReactionMonitors.add(record.timestamp);
  void monitorReaction(record)
    .catch((error) => console.error(`[slack] reaction monitor ${record.timestamp}: ${errorText(error)}`))
    .finally(() => activeReactionMonitors.delete(record.timestamp));
}

async function monitorReaction(record) {
  while (!controller.signal.aborted) {
    try {
      await ensureEyesReaction(record);
      const entry = await hub(`/api/inbox/${record.inboxItemId}`);
      if (slackReactionAction(entry) === 'remove') {
        await removeEyesReaction(record);
        delete state.reactionMonitors[record.timestamp];
        await persistState();
        console.error(`[slack] removed eyes from ${record.timestamp}`);
        return;
      }
    } catch (error) {
      console.error(`[slack] reaction ${record.timestamp}: ${errorText(error)}`);
    }
    await delay(1000);
  }
}

async function ensureEyesReaction(record) {
  if (record.added) return;
  await slack('reactions.add', {
    token: config.botToken,
    body: { channel: record.channel, timestamp: record.timestamp, name: 'eyes' },
    allowErrors: ['already_reacted'],
  });
  record.added = true;
  state.reactionMonitors[record.timestamp] = record;
  await persistState();
  console.error(`[slack] added eyes to ${record.timestamp}`);
}

async function removeEyesReaction(record) {
  await slack('reactions.remove', {
    token: config.botToken,
    body: { channel: record.channel, timestamp: record.timestamp, name: 'eyes' },
    allowErrors: ['no_reaction'],
  });
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
        if (envelope.type === 'connector/command') await sendOutbox(envelope.data);
      });
    } catch (error) {
      if (!controller.signal.aborted) console.error(`[hub] ${errorText(error)}; reconnecting commands`);
    }
    await delay(1000);
  }
}

async function sendOutbox(command) {
  const item = command?.outboxItem;
  if (!item?.id) return;
  let externalMessageIds = [];
  try {
    const prior = state.outboxResults[item.id];
    externalMessageIds = Array.isArray(prior) ? [...prior] : prior ? [prior] : [];
    const text = String(item.content?.text || '').trim();
    let completedParts = externalMessageIds.length;
    if (text && completedParts === 0) {
      const result = await slack('chat.postMessage', {
        token: config.botToken,
        body: slackOutboxRequest(item),
      });
      const externalMessageId = result.ts || result.message?.ts || '';
      if (!externalMessageId) throw new Error(`chat.postMessage returned no ts: ${JSON.stringify(result)}`);
      externalMessageIds.push(externalMessageId);
      completedParts++;
      state.outboxResults[item.id] = externalMessageIds;
      trimRecord(state.outboxResults, 2000);
      await persistState();
    }
    const attachmentOffset = text ? 1 : 0;
    const attachments = item.content?.attachments || [];
    for (let index = Math.max(0, completedParts - attachmentOffset); index < attachments.length; index++) {
      externalMessageIds.push(await uploadOutboxAttachment(item, attachments[index]));
      completedParts++;
      state.outboxResults[item.id] = externalMessageIds;
      trimRecord(state.outboxResults, 2000);
      await persistState();
    }
    if (externalMessageIds.length === 0) throw new Error('Slack outbox item has no text or attachments');
    await reportOutbox(item.id, {
      attemptToken: item.attemptToken,
      success: true,
      externalMessageId: externalMessageIds[0],
      externalMessageIds,
      deliveryReceipts: slackDeliveryReceipts(item, externalMessageIds),
    });
    console.error(`[slack] sent ${item.id} as ${externalMessageIds.join(', ')}`);
  } catch (error) {
    await reportOutbox(item.id, {
      attemptToken: item.attemptToken,
      success: false,
      externalMessageIds,
      deliveryReceipts: slackDeliveryReceipts(item, externalMessageIds),
      error: errorText(error),
    }).catch(() => {});
    console.error(`[slack] send ${item.id}: ${errorText(error)}`);
  }
}

async function uploadOutboxAttachment(item, attachment) {
  const localPath = path.resolve(String(attachment?.path || '').trim());
  const stat = await fs.promises.lstat(localPath);
  if (!stat.isFile() || stat.isSymbolicLink() || stat.size <= 0 || stat.size > 25 * 1024 * 1024) {
    throw new Error(`invalid outbound attachment: ${localPath}`);
  }
  const fileName = path.basename(String(attachment?.name || '').trim() || localPath);
  const prepared = await slack('files.getUploadURLExternal', {
    token: config.botToken,
    body: { filename: fileName, length: stat.size },
  });
  const uploadUrl = String(prepared.upload_url || '').trim();
  const fileId = String(prepared.file_id || '').trim();
  if (!uploadUrl || !fileId) throw new Error('files.getUploadURLExternal returned no upload URL or file ID');
  const upload = await fetch(uploadUrl, {
    method: 'POST',
    headers: { 'Content-Type': String(attachment?.mimeType || '').trim() || 'application/octet-stream' },
    body: await fs.promises.readFile(localPath),
    signal: controller.signal,
  });
  if (!upload.ok) throw new Error(`Slack file upload failed: HTTP ${upload.status}`);
  const request = slackOutboxRequest(item);
  const completed = await slack('files.completeUploadExternal', {
    token: config.botToken,
    body: {
      files: [{ id: fileId, title: fileName }],
      channel_id: request.channel,
      ...(request.thread_ts ? { thread_ts: request.thread_ts } : {}),
    },
  });
  return String(completed.files?.[0]?.id || fileId);
}

async function reportOutbox(id, result) {
  return hub(`/api/integrations/connections/${config.connectionId}/outbox/${id}/result`, {
    method: 'POST',
    body: result,
  });
}

async function runHeartbeatLoop() {
  while (!controller.signal.aborted) {
    const healthy = socketConnected;
    await hub(`/api/integrations/connections/${config.connectionId}/heartbeat`, {
      method: 'POST',
      body: {
        status: healthy ? 'connected' : lastSocketError ? 'degraded' : 'connecting',
        cursor: Object.keys(state.seen).at(-1) || '',
        capabilities: [
          'receive_events',
          'threads',
          'mentions',
          'attachments',
          'reactions',
          'proactive_send',
        ],
        error: healthy ? '' : lastSocketError,
      },
    }).catch((error) => console.error(`[hub] heartbeat: ${errorText(error)}`));
    await delay(10000);
  }
}

async function slack(method, options = {}) {
  const response = await fetch(`${config.apiUrl}/${method}`, {
    method: 'POST',
    headers: {
      Authorization: `Bearer ${options.token}`,
      'Content-Type': 'application/json; charset=utf-8',
    },
    body: options.body === undefined ? undefined : JSON.stringify(options.body),
    signal: controller.signal,
  });
  const data = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(`Slack HTTP ${response.status}: ${data.error || response.statusText}`);
  if (data.ok === false && !(options.allowErrors || []).includes(data.error)) {
    throw new Error(`Slack API ${method}: ${data.error || 'unknown_error'}`);
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
      if (data) await handler(JSON.parse(data));
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
    return {};
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

function trimRecord(record, limit) {
  const keys = Object.keys(record);
  for (const key of keys.slice(0, Math.max(0, keys.length - limit))) delete record[key];
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

function errorText(error) {
  return error instanceof Error ? error.message : String(error);
}

function delay(ms) {
  if (controller.signal.aborted) return Promise.resolve();
  return new Promise((resolve) => {
    let settled = false;
    const finish = () => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      controller.signal.removeEventListener('abort', finish);
      resolve();
    };
    const timer = setTimeout(finish, ms);
    controller.signal.addEventListener('abort', finish, { once: true });
  });
}
