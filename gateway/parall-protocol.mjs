const DISPATCH_EFFECT_PREFIXES = ['reply:', 'task_update:'];

export function parallProviderReadRequest(operation, orgId) {
  const resource = String(operation?.resource || '').trim().toLowerCase();
  const action = String(operation?.action || '').trim().toLowerCase();
  const args = operation?.arguments || {};
  const org = encodeURIComponent(requiredString(orgId, 'Parall organization id'));
  const query = new URLSearchParams();

  switch (`${resource}/${action}`) {
    case 'chats/list':
      query.set('limit', boundedLimit(args.limit));
      optionalQuery(query, 'cursor', args.cursor);
      return { resource: `/api/v1/orgs/${org}/chats?${query}`, method: 'GET' };
    case 'chats/get':
      return { resource: `/api/v1/orgs/${org}/chats/${providerID(args.chatId, 'chat id')}`, method: 'GET' };
    case 'chats/discoverable':
      query.set('limit', boundedLimit(args.limit));
      optionalQuery(query, 'q', args.query);
      return { resource: `/api/v1/orgs/${org}/chats/discoverable?${query}`, method: 'GET' };
    case 'chats/members-list':
      return { resource: `/api/v1/orgs/${org}/chats/${providerID(args.chatId, 'chat id')}/members`, method: 'GET' };
    case 'messages/list':
      query.set('limit', boundedLimit(args.limit));
      optionalQuery(query, 'before', stripParallRef(args.before));
      optionalQuery(query, 'after', stripParallRef(args.after));
      optionalQuery(query, 'since', args.since);
      optionalQuery(query, 'thread_root_id', stripParallRef(args.threadRootId));
      if (args.topLevel === true || args.topLevel === 'true') query.set('top_level', 'true');
      return { resource: `/api/v1/orgs/${org}/chats/${providerID(args.chatId, 'chat id')}/messages?${query}`, method: 'GET' };
    case 'messages/get':
      return { resource: `/api/v1/messages/${providerID(args.messageId, 'message id')}`, method: 'GET' };
    case 'messages/replies':
      query.set('limit', boundedLimit(args.limit));
      optionalQuery(query, 'before', stripParallRef(args.before));
      optionalQuery(query, 'after', stripParallRef(args.after));
      return { resource: `/api/v1/messages/${providerID(args.messageId, 'message id')}/replies?${query}`, method: 'GET' };
    default:
      throw new Error(`unsupported Parall provider operation: ${resource} ${action}`);
  }
}

function boundedLimit(value) {
  const parsed = value === undefined || value === '' ? 20 : Number(value);
  if (!Number.isInteger(parsed) || parsed < 1 || parsed > 100) {
    throw new Error('limit must be an integer from 1 to 100');
  }
  return String(parsed);
}

function providerID(value, name) {
  return encodeURIComponent(requiredString(stripParallRef(value), name));
}

function stripParallRef(value) {
  return String(value || '').trim().replace(/^prll:\/\//, '');
}

function requiredString(value, name) {
  const result = String(value || '').trim();
  if (!result) throw new Error(`${name} is required`);
  return result;
}

function optionalQuery(query, name, value) {
  const normalized = String(value || '').trim();
  if (normalized) query.set(name, normalized);
}

// Hub idempotency keys are provider-neutral. Parall reserves some prefixes
// for dispatch effects, so namespace ordinary connector sends before they
// cross the provider boundary.
export function parallIdempotencyKey(item) {
  const key = String(item?.idempotencyKey || item?.id || '').trim();
  if (!key) throw new Error('outbox item idempotency key is required');
  return DISPATCH_EFFECT_PREFIXES.some((prefix) => key.startsWith(prefix))
    ? `codex-hub:${key}`
    : key;
}

export function parallMessageHints(item) {
  return item?.responseExpectation === 'none' ? { no_reply: true } : undefined;
}

export function parallOutboxRequest(item, attachmentIds = []) {
  const request = {
    message_type: 'text',
    content: { text: item?.content?.text || '' },
    idempotency_key: parallIdempotencyKey(item),
  };
  const ids = attachmentIds.map((id) => String(id || '').trim()).filter(Boolean);
  if (ids.length) request.attachment_ids = ids;
  const hints = parallMessageHints(item);
  if (hints) request.hints = hints;
  if (item?.conversation?.threadId) request.thread_root_id = item.conversation.threadId;
  return request;
}

export function parallDeliveryReceipts(item, attachmentIds = [], externalMessageId = '') {
  const messageId = String(externalMessageId || '').trim();
  const receipts = [];
  if (String(item?.content?.text || '').trim()) {
    receipts.push({ kind: 'text', externalMessageId: messageId });
  }
  const attachments = item?.content?.attachments || [];
  for (let index = 0; index < attachmentIds.length; index++) {
    receipts.push({
      kind: 'attachment',
      artifactId: String(attachments[index]?.id || '').trim(),
      externalMessageId: messageId,
      externalAttachmentId: String(attachmentIds[index] || '').trim(),
    });
  }
  return receipts;
}

export function parallConversationType(chatType, threadRootID) {
  if (String(threadRootID || '').trim()) return 'thread';
  return String(chatType || '').toLowerCase() === 'direct' ? 'dm' : 'group';
}

export function parallConversationCandidates(chats = []) {
  const candidates = new Map();
  for (const chat of chats) {
    const conversationId = String(chat?.id || '').trim();
    const conversationType = parallConversationType(chat?.type || '', '');
    if (!conversationId || conversationType !== 'group') continue;
    const previous = candidates.get(conversationId);
    candidates.set(conversationId, {
      conversationId,
      conversationType,
      displayName: String(chat?.name || '').trim() || previous?.displayName || conversationId,
      description: String(chat?.description || '').trim() || previous?.description || '',
    });
  }
  return Array.from(candidates.values()).sort((left, right) =>
    left.displayName.localeCompare(right.displayName) || left.conversationId.localeCompare(right.conversationId),
  );
}

export function parallThreadContext(rootMessage, repliesResponse, currentMessage, limits = {}) {
  const rootExternalMessageId = String(currentMessage?.thread_root_id || rootMessage?.id || '').trim();
  if (!rootExternalMessageId) return undefined;
  const maxMessages = Math.max(1, Number(limits.maxMessages) || 12);
  const maxMessageChars = Math.max(1, Number(limits.maxMessageChars) || 6000);
  let remainingChars = Math.max(maxMessageChars, Number(limits.maxTotalChars) || 24000);
  const currentID = String(currentMessage?.id || '').trim();
  const currentAt = Date.parse(currentMessage?.created_at || '');
  const replies = Array.isArray(repliesResponse?.data) ? repliesResponse.data : [];
  const eligibleReplies = replies
    .filter((message) => {
      if (!message?.id || String(message.id) === currentID || String(message.id) === rootExternalMessageId) return false;
      const occurredAt = Date.parse(message.created_at || '');
      return !Number.isFinite(currentAt) || !Number.isFinite(occurredAt) || occurredAt <= currentAt;
    })
    .sort((left, right) => {
      const leftAt = Date.parse(left?.created_at || '') || 0;
      const rightAt = Date.parse(right?.created_at || '') || 0;
      return leftAt - rightAt || String(left?.id || '').localeCompare(String(right?.id || ''));
    });
  const selected = [rootMessage, ...eligibleReplies.slice(-(maxMessages - 1))].filter(Boolean);
  let contentTruncated = false;
  const messages = [];
  for (const message of selected) {
    const rawText = String(message?.content?.text || '');
    const allowed = Math.max(0, Math.min(maxMessageChars, remainingChars));
    const text = rawText.slice(0, allowed);
    const textTruncated = text.length < rawText.length;
    contentTruncated ||= textTruncated;
    remainingChars -= text.length;
    const attachments = (message?.attachments || []).map((item) => ({
      id: item?.id,
      name: item?.file_name || item?.name,
      mimeType: item?.mime_type || item?.content_type,
      size: item?.file_size || item?.size,
      url: item?.url || item?.download_url || item?.web_url,
    }));
    if (!text && attachments.length === 0) continue;
    messages.push({
      externalMessageId: String(message.id || '').trim(),
      role: String(message.id || '').trim() === rootExternalMessageId ? 'root' : 'reply',
      sender: {
        externalId: String(message.sender_id || '').trim(),
        displayName: String(message.sender?.display_name || message.sender_id || '').trim(),
        kind: String(message.sender?.type || 'unknown').trim(),
      },
      content: { text, attachments },
      occurredAt: String(message.created_at || '').trim(),
      textTruncated,
    });
  }
  return {
    rootExternalMessageId,
    messages,
    truncated: Boolean(repliesResponse?.has_more) || eligibleReplies.length > maxMessages - 1 || contentTruncated,
  };
}

export function inboxDispatchAction(entry, received) {
  const state = entry?.item?.state;
  if (!received && (state === 'handling' || state === 'handled')) return 'mark_received';
  if (state !== 'handled') return 'wait';
  if (entry.item.outcome === 'no_reply') return 'ack';
  if (entry.item.outcome === 'reply' && entry.outboxItem?.state === 'sent') return 'ack';
  return 'wait';
}
