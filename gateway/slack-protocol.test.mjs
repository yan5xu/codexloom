import assert from 'node:assert/strict';
import test from 'node:test';

import {
  slackDeliveryReceipts,
  slackEventToIngress,
  slackOutboxRequest,
  slackReactionAction,
  slackTimestampToISO,
} from './slack-protocol.mjs';

const context = {
  connectionId: 'conn_slack',
  addressId: 'addr_slack',
  botUserId: 'U_BOT',
  teamId: 'T_TEAM',
  senderName: 'Ada',
};

test('normalizes an app mention and removes the bot token from agent-visible text', () => {
  const ingress = slackEventToIngress({
    type: 'events_api', envelope_id: 'env-1',
    payload: {
      type: 'event_callback', event_id: 'Ev-1', team_id: 'T_TEAM',
      event: { type: 'app_mention', user: 'U_ADA', channel: 'C_DEV', channel_type: 'channel', ts: '1710000000.100', text: '<@U_BOT> inspect this' },
    },
  }, context);
  assert.equal(ingress.content.text, 'inspect this');
  assert.deepEqual(ingress.trigger, { direct: false, mentioned: true, explicitDispatch: false });
  assert.equal(ingress.conversation.conversationType, 'channel');
});

test('treats Slack IM events as direct without requiring a mention', () => {
  const ingress = slackEventToIngress({
    type: 'events_api', envelope_id: 'env-2',
    payload: {
      type: 'event_callback', event_id: 'Ev-2',
      event: { type: 'message', user: 'U_ADA', channel: 'D_DM', channel_type: 'im', ts: '1710000001.100', text: 'hello' },
    },
  }, context);
  assert.equal(ingress.trigger.direct, true);
  assert.equal(ingress.conversation.conversationType, 'dm');
});

test('ignores bot messages and message mutation events', () => {
  const base = { type: 'events_api', payload: { type: 'event_callback', event_id: 'Ev', event: { type: 'message', channel: 'C', ts: '1', text: 'x' } } };
  assert.equal(slackEventToIngress({ ...base, payload: { ...base.payload, event: { ...base.payload.event, bot_id: 'B1' } } }, context), null);
  assert.equal(slackEventToIngress({ ...base, payload: { ...base.payload, event: { ...base.payload.event, subtype: 'message_changed' } } }, context), null);
});

test('preserves Slack thread roots and attachment metadata', () => {
  const ingress = slackEventToIngress({
    type: 'events_api', envelope_id: 'env-3',
    payload: {
      type: 'event_callback', event_id: 'Ev-3',
      event: {
        type: 'message', user: 'U_ADA', channel: 'C_DEV', channel_type: 'channel',
        ts: '1710000002.100', thread_ts: '1710000000.100', text: '<@U_BOT>',
        files: [{ id: 'F1', name: 'report.pdf', mimetype: 'application/pdf', size: 42, permalink: 'https://slack.example/F1' }],
      },
    },
  }, context);
  assert.equal(ingress.conversation.threadId, '1710000000.100');
  assert.deepEqual(ingress.content.attachments[0], {
    id: 'F1', name: 'report.pdf', mimeType: 'application/pdf', size: 42, url: 'https://slack.example/F1',
  });
});

test('replies to channel messages in a thread but keeps DMs unthreaded', () => {
  const channel = slackOutboxRequest({ content: { text: 'done' }, conversation: { conversationId: 'C1', messageId: '10.2', conversationType: 'channel' } });
  assert.equal(channel.thread_ts, '10.2');
  const dm = slackOutboxRequest({ content: { text: 'done' }, conversation: { conversationId: 'D1', messageId: '10.2', conversationType: 'dm' } });
  assert.equal(dm.thread_ts, undefined);
});

test('maps Slack message and file IDs to structured delivery receipts', () => {
  assert.deepEqual(slackDeliveryReceipts({
    content: { text: 'done', attachments: [{ id: 'art_report' }] },
  }, ['1710000000.100', 'F_REPORT']), [
    { kind: 'text', externalMessageId: '1710000000.100' },
    { kind: 'attachment', artifactId: 'art_report', externalAttachmentId: 'F_REPORT' },
  ]);
});

test('keeps eyes until handling reaches a terminal delivery state', () => {
  assert.equal(slackReactionAction({ item: { state: 'handling' } }), 'wait');
  assert.equal(slackReactionAction({ item: { state: 'handled', outcome: 'reply' }, outboxItem: { state: 'pending' } }), 'wait');
  assert.equal(slackReactionAction({ item: { state: 'handled', outcome: 'reply' }, outboxItem: { state: 'sent' } }), 'remove');
  assert.equal(slackReactionAction({ item: { state: 'handled', outcome: 'no_reply' } }), 'remove');
});

test('converts Slack decimal timestamps to ISO', () => {
  assert.equal(slackTimestampToISO('1710000000.500'), '2024-03-09T16:00:00.500Z');
});
