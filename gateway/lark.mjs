#!/usr/bin/env node

// Legacy migration adapter. New Feishu connections use bin/loom-feishu-gateway
// and the official Go SDK; keep this file only until existing deployments move.
import { spawn } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import readline from 'node:readline';

import {
  hasLarkBotMention,
  larkBotIdentities,
  larkLocalImageAttachment,
  larkOutboundContentArgs,
  larkReactionAction,
} from './lark-protocol.mjs';

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
  larkCli: process.env.LARK_CLI || 'lark-cli',
  botId: args['bot-id'] || process.env.LARK_BOT_ID || '',
  stateFile:
    args['state-file'] || path.join(defaultDataDir(), 'gateway', `lark-${args.connection}.json`),
};

const controller = new AbortController();
process.on('SIGINT', () => controller.abort());
process.on('SIGTERM', () => controller.abort());

await fs.promises.mkdir(path.dirname(config.stateFile), { recursive: true });
const state = await readState(config.stateFile);
state.reactionMonitors ||= {};
let stateWrite = Promise.resolve();
const activeReactionMonitors = new Set();
let eventConsumerRunning = false;
let lastEventError = '';
const botIdentities = await resolveBotIdentities();
restoreReactionMonitors();
await Promise.all([runEventLoop(), runCommandLoop(), runHeartbeatLoop()]);

async function runEventLoop() {
  while (!controller.signal.aborted) {
    try {
      await consumeLarkEvents();
    } catch (error) {
      if (!controller.signal.aborted) {
        lastEventError = errorText(error);
        console.error(`[lark] ${lastEventError}; restarting consumer`);
      }
    }
    await delay(1000);
  }
}

async function consumeLarkEvents() {
  const child = spawn(config.larkCli, ['event', 'consume', 'im.message.receive_v1', '--as', 'bot'], {
    stdio: ['pipe', 'pipe', 'pipe'],
  });
  eventConsumerRunning = true;
  lastEventError = '';
  const closed = new Promise((resolve, reject) => {
    child.once('close', resolve);
    child.once('error', reject);
  });
  const abort = () => child.kill('SIGTERM');
  controller.signal.addEventListener('abort', abort, { once: true });
  child.stderr.setEncoding('utf8');
  child.stderr.on('data', (chunk) => {
    for (const line of String(chunk).trim().split('\n')) {
      if (line) console.error(`[lark] ${line}`);
    }
  });
  const lines = readline.createInterface({ input: child.stdout });
  try {
    for await (const line of lines) {
      if (!String(line).trim()) continue;
      try {
        await ingestLarkEvent(JSON.parse(line));
      } catch (error) {
        console.error(`[lark] event: ${errorText(error)}`);
      }
    }
    const code = await closed;
    if (!controller.signal.aborted) throw new Error(`event consumer exited ${code}`);
  } finally {
    controller.signal.removeEventListener('abort', abort);
    eventConsumerRunning = false;
  }
}

async function ingestLarkEvent(event) {
  if (event.type !== 'im.message.receive_v1' || !event.event_id || !event.message_id) return;
  if (event.message_type === 'interactive') return;
  const detail = await fetchLarkMessage(event.message_id);
  const sender = detail?.sender || {};
  const senderID = sender.id || event.sender_id || '';
  if (botIdentities.has(senderID) || ['app', 'bot'].includes(String(sender.sender_type || '').toLowerCase())) {
    console.error(`[lark] ignored own message ${event.message_id}`);
    return;
  }
  const mentionIDs = (detail?.mentions || []).map((mention) => mention.id).filter(Boolean);
  const direct = event.chat_type === 'p2p';
  const mentioned = hasLarkBotMention(detail?.mentions, botIdentities);
  const content = normalizeLarkContent(detail?.content ?? event.content);
  const attachments = larkAttachmentRefs(event, detail, content);
  if (!content && attachments.length === 0) return;
  const occurredAt = millisToISO(event.create_time || event.timestamp);
  const result = await hub('/api/integrations/ingress', {
    method: 'POST',
    body: {
      connectionId: config.connectionId,
      addressId: config.addressId,
      externalEventId: event.event_id,
      externalMessageId: event.message_id,
      sender: {
        externalId: senderID,
        displayName: sender.name || senderID,
        kind: sender.sender_type || 'human',
      },
      conversation: {
        conversationId: event.chat_id,
        threadId: detail?.thread_id || detail?.root_id || '',
        messageId: event.message_id,
        conversationType: event.chat_type === 'p2p' ? 'dm' : 'group',
      },
      content: { text: content, attachments },
      responseExpectation: 'optional',
      occurredAt,
      trigger: { direct, mentioned, explicitDispatch: false },
      providerMetadata: {
        eventType: event.type,
        messageType: event.message_type,
        chatType: event.chat_type,
        mentionIds: mentionIDs,
        messageAppLink: detail?.message_app_link || '',
      },
    },
  });
  if (result.ignored) {
    console.error(`[lark] ignored ${event.message_id}: ${result.reason}`);
  } else {
    if (!result.inboxItem?.id) throw new Error(`Hub accepted ${event.message_id} without an inbox item`);
    const record = {
      messageId: event.message_id,
      inboxItemId: result.inboxItem.id,
      reactionId: '',
    };
    state.reactionMonitors[event.message_id] = record;
    await persistState();
    startReactionMonitor(record);
    console.error(`[lark] accepted ${event.message_id} from ${senderID}`);
  }
}

function restoreReactionMonitors() {
  for (const record of Object.values(state.reactionMonitors)) startReactionMonitor(record);
}

function startReactionMonitor(record) {
  if (!record?.messageId || activeReactionMonitors.has(record.messageId)) return;
  activeReactionMonitors.add(record.messageId);
  void monitorReaction(record)
    .catch((error) => console.error(`[lark] reaction monitor ${record.messageId}: ${errorText(error)}`))
    .finally(() => activeReactionMonitors.delete(record.messageId));
}

async function monitorReaction(record) {
  while (!controller.signal.aborted) {
    try {
      await ensureOnItReaction(record);
      const entry = await hub(`/api/inbox/${record.inboxItemId}`);
      if (larkReactionAction(entry) === 'remove') {
        await removeOnItReaction(record);
        delete state.reactionMonitors[record.messageId];
        await persistState();
        console.error(`[lark] removed 👀 from ${record.messageId}`);
        return;
      }
    } catch (error) {
      console.error(`[lark] reaction monitor ${record.messageId}: ${errorText(error)}`);
    }
    await delay(500);
  }
}

async function ensureOnItReaction(record) {
  if (record.reactionId) return;
  const existing = await findOwnOnItReaction(record.messageId);
  if (existing) {
    record.reactionId = existing;
  } else {
    const result = await runJSONCommand(config.larkCli, [
      'im', 'reactions', 'create',
      '--message-id', record.messageId,
      '--data', JSON.stringify({ reaction_type: { emoji_type: 'OnIt' } }),
      '--as', 'bot', '--json',
    ]);
    record.reactionId = result?.data?.reaction_id || result?.reaction_id || '';
    if (!record.reactionId) throw new Error(`lark-cli returned no reaction_id: ${JSON.stringify(result)}`);
  }
  state.reactionMonitors[record.messageId] = record;
  await persistState();
  console.error(`[lark] added 👀 to ${record.messageId}`);
}

async function removeOnItReaction(record) {
  if (!record.reactionId) return;
  try {
    await runJSONCommand(config.larkCli, [
      'im', 'reactions', 'delete',
      '--message-id', record.messageId,
      '--reaction-id', record.reactionId,
      '--as', 'bot', '--json',
    ]);
  } catch (error) {
    const existing = await findOwnOnItReaction(record.messageId);
    if (!existing) return;
    record.reactionId = existing;
    throw error;
  }
}

async function findOwnOnItReaction(messageId) {
  const result = await runJSONCommand(config.larkCli, [
    'im', 'reactions', 'list',
    '--message-id', messageId,
    '--reaction-type', 'OnIt',
    '--page-size', '50',
    '--as', 'bot', '--json',
  ]);
  const items = result?.data?.items || result?.items || [];
  const own = items.find((item) =>
    item?.reaction_type?.emoji_type === 'OnIt' && botIdentities.has(item?.operator?.operator_id),
  );
  return own?.reaction_id || '';
}

async function resolveBotIdentities() {
  const result = await runJSONCommand(config.larkCli, ['whoami', '--as', 'bot', '--json']);
  const ids = larkBotIdentities(config.botId, result);
  if (ids.size === 0) throw new Error('lark-cli whoami returned no bot identity; set LARK_BOT_ID explicitly');
  return ids;
}

async function fetchLarkMessage(messageID) {
  let lastError;
  for (let attempt = 0; attempt < 3; attempt++) {
    try {
      const result = await runJSONCommand(config.larkCli, [
        'im', '+messages-mget', '--message-ids', messageID, '--as', 'bot', '--no-reactions', '--json',
      ]);
      const message = result?.data?.messages?.[0] || result?.messages?.[0];
      if (message) return message;
      throw new Error(`message ${messageID} was not returned`);
    } catch (error) {
      lastError = error;
      if (attempt < 2) await delay(250 * (attempt + 1));
    }
  }
  throw lastError;
}

function normalizeLarkContent(value) {
  if (typeof value === 'string') {
    try {
      const parsed = JSON.parse(value);
      if (typeof parsed?.text === 'string') return parsed.text;
    } catch {}
    return value;
  }
  if (value && typeof value.text === 'string') return value.text;
  return '';
}

function larkAttachmentRefs(event, detail, content) {
  const type = String(detail?.msg_type || event.message_type || '').toLowerCase();
  if (['text', 'post'].includes(type)) return [];
  const raw = typeof detail?.content === 'string' ? detail.content : JSON.stringify(detail?.content || {});
  const key = raw.match(/(?:file_key|image_key|key)["'=:\s]+([\w-]+)/i)?.[1] || event.message_id;
  const label = type ? `Lark ${type}` : 'Lark attachment';
  return [{
    id: key,
    name: content && content.length < 120 ? content : label,
    mimeType: larkMimeType(type),
    url: detail?.message_app_link || '',
  }];
}

function larkMimeType(type) {
  if (type === 'image') return 'image/*';
  if (type === 'audio') return 'audio/*';
  if (type === 'media') return 'video/*';
  return 'application/octet-stream';
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
  try {
    const attachments = item.content?.attachments || [];
    const image = larkLocalImageAttachment(item);
    if (attachments.length > 1 || (attachments.length === 1 && !image)) {
      throw new Error('legacy Lark gateway currently supports one local image attachment');
    }
    const imageInput = image ? `./${path.basename(image.path)}` : '';
    const cliArgs = ['im'];
    if (item.conversation?.messageId) {
      cliArgs.push(
        '+messages-reply',
        '--message-id', item.conversation.messageId,
        ...larkOutboundContentArgs(item, imageInput),
        '--idempotency-key', item.idempotencyKey,
        '--as', 'bot',
      );
      if (item.conversation.threadId) cliArgs.push('--reply-in-thread');
    } else {
      cliArgs.push(
        '+messages-send',
        '--chat-id', item.conversation?.conversationId || '',
        ...larkOutboundContentArgs(item, imageInput),
        '--idempotency-key', item.idempotencyKey,
        '--as', 'bot',
      );
    }
    const result = await runJSONCommand(config.larkCli, cliArgs, image ? { cwd: path.dirname(image.path) } : undefined);
    const externalID = result?.data?.message_id || result?.message_id || result?.data?.message?.message_id;
    if (!externalID) throw new Error(`lark-cli returned no message_id: ${JSON.stringify(result)}`);
    await reportOutbox(item.id, { attemptToken: item.attemptToken, success: true, externalMessageId: externalID });
    console.error(`[lark] sent ${item.id} as ${externalID}`);
  } catch (error) {
    await reportOutbox(item.id, { attemptToken: item.attemptToken, success: false, error: errorText(error) }).catch(() => {});
    console.error(`[lark] send ${item.id}: ${errorText(error)}`);
  }
}

async function reportOutbox(id, result) {
  return hub(`/api/integrations/connections/${config.connectionId}/outbox/${id}/result`, {
    method: 'POST',
    body: result,
  });
}

async function runHeartbeatLoop() {
  while (!controller.signal.aborted) {
    await hub(`/api/integrations/connections/${config.connectionId}/heartbeat`, {
      method: 'POST',
      body: {
        status: eventConsumerRunning ? 'connected' : lastEventError ? 'degraded' : 'connecting',
        capabilities: ['receive_events', 'threads', 'mentions', 'attachments', 'reactions', 'proactive_send'],
        error: eventConsumerRunning ? '' : lastEventError,
      },
    }).catch((error) => console.error(`[hub] heartbeat: ${errorText(error)}`));
    await delay(10000);
  }
}

function runJSONCommand(command, commandArgs, options = {}) {
  return new Promise((resolve, reject) => {
    const child = spawn(command, commandArgs, { stdio: ['ignore', 'pipe', 'pipe'], ...options });
    let stdout = '';
    let stderr = '';
    child.stdout.setEncoding('utf8');
    child.stderr.setEncoding('utf8');
    child.stdout.on('data', (chunk) => { stdout += chunk; });
    child.stderr.on('data', (chunk) => { stderr += chunk; });
    child.on('error', reject);
    child.on('close', (code) => {
      let parsed;
      try { parsed = JSON.parse(stdout); } catch {}
      if (code !== 0 || parsed?.ok === false) {
        reject(new Error(parsed?.error?.message || stderr.trim() || `lark-cli exited ${code}`));
        return;
      }
      resolve(parsed || {});
    });
  });
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

function millisToISO(value) {
  const number = Number(value);
  return Number.isFinite(number) && number > 0 ? new Date(number).toISOString() : '';
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
