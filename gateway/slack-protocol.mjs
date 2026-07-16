function conversationType(channelType) {
  if (channelType === 'im') return 'dm';
  if (channelType === 'mpim') return 'group_dm';
  return 'channel';
}

function stripBotMention(text, botUserId) {
  const value = String(text || '');
  if (!botUserId) return value.trim();
  return value.replaceAll(`<@${botUserId}>`, '').trim();
}

function slackAttachments(files) {
  return (files || []).map((file) => ({
    id: file.id || '',
    name: file.name || file.title || 'Slack attachment',
    mimeType: file.mimetype || file.filetype || '',
    size: Number(file.size) || 0,
    url: file.permalink || file.url_private || '',
  }));
}

export function slackEventToIngress(envelope, context) {
  if (envelope?.type !== 'events_api' || envelope.payload?.type !== 'event_callback') return null;
  const event = envelope.payload.event || {};
  if (!['app_mention', 'message'].includes(event.type)) return null;
  if (['message_changed', 'message_deleted', 'channel_join', 'channel_leave'].includes(event.subtype)) {
    return null;
  }
  if (event.bot_id || event.user === context.botUserId || event.subtype === 'bot_message') return null;

  const direct = event.channel_type === 'im';
  const mentioned = event.type === 'app_mention' ||
    Boolean(context.botUserId && String(event.text || '').includes(`<@${context.botUserId}>`));
  const text = stripBotMention(event.text, context.botUserId);
  const attachments = slackAttachments(event.files);
  if (!text && attachments.length === 0) return null;

  return {
    connectionId: context.connectionId,
    addressId: context.addressId,
    externalEventId: envelope.payload.event_id || envelope.envelope_id,
    externalMessageId: event.ts || envelope.payload.event_id || envelope.envelope_id,
    sender: {
      externalId: event.user || event.username || 'unknown',
      displayName: context.senderName || event.user || event.username || 'Unknown sender',
      kind: 'human',
    },
    conversation: {
      conversationId: event.channel || '',
      threadId: event.thread_ts || '',
      messageId: event.ts || '',
      conversationType: conversationType(event.channel_type),
    },
    content: { text, attachments },
    replyTo: event.thread_ts || '',
    responseExpectation: 'optional',
    occurredAt: slackTimestampToISO(event.event_ts || event.ts),
    trigger: { direct, mentioned, explicitDispatch: false },
    providerMetadata: {
      teamId: envelope.payload.team_id || context.teamId || '',
      apiAppId: envelope.payload.api_app_id || '',
      eventType: event.type,
      subtype: event.subtype || '',
      channelType: event.channel_type || '',
      envelopeId: envelope.envelope_id || '',
    },
  };
}

export function slackOutboxRequest(item) {
  const conversation = item?.conversation || {};
  const request = {
    channel: conversation.conversationId || '',
    text: item?.content?.text || '',
    unfurl_links: false,
    unfurl_media: false,
  };
  if (conversation.threadId) {
    request.thread_ts = conversation.threadId;
  } else if (conversation.messageId && conversation.conversationType !== 'dm') {
    request.thread_ts = conversation.messageId;
  }
  return request;
}

export function slackDeliveryReceipts(item, externalIds = []) {
  const ids = externalIds.map((value) => String(value || '').trim()).filter(Boolean);
  const receipts = [];
  let offset = 0;
  if (String(item?.content?.text || '').trim() && ids[0]) {
    receipts.push({ kind: 'text', externalMessageId: ids[0] });
    offset = 1;
  }
  const attachments = item?.content?.attachments || [];
  for (let index = 0; index < attachments.length && index + offset < ids.length; index++) {
    receipts.push({
      kind: 'attachment',
      artifactId: String(attachments[index]?.id || '').trim(),
      externalAttachmentId: ids[index + offset],
    });
  }
  return receipts;
}

export function slackReactionAction(entry) {
  const state = entry?.item?.state;
  if (state === 'failed') return 'remove';
  if (state !== 'handled') return 'wait';
  if (entry.item.outcome === 'no_reply') return 'remove';
  if (entry.item.outcome === 'reply' && entry.outboxItem?.state === 'sent') return 'remove';
  return 'wait';
}

export function slackTimestampToISO(value) {
  const seconds = Number.parseFloat(String(value || ''));
  return Number.isFinite(seconds) && seconds > 0 ? new Date(seconds * 1000).toISOString() : '';
}
