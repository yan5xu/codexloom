import assert from 'node:assert/strict';
import test from 'node:test';

import { inboxDispatchAction, parallConversationCandidates, parallConversationType, parallDeliveryReceipts, parallIdempotencyKey, parallMessageHints, parallOutboxRequest, parallProviderReadRequest, parallThreadContext } from './parall-protocol.mjs';

test('maps native Parall chat reads to fixed provider paths', () => {
  assert.deepEqual(parallProviderReadRequest({
    resource: 'chats', action: 'list', arguments: { limit: '40', cursor: 'next page' },
  }, 'org_test'), {
    resource: '/api/v1/orgs/org_test/chats?limit=40&cursor=next+page', method: 'GET',
  });
  assert.deepEqual(parallProviderReadRequest({
    resource: 'chats', action: 'members-list', arguments: { chatId: 'prll://cht_team' },
  }, 'org_test'), {
    resource: '/api/v1/orgs/org_test/chats/cht_team/members', method: 'GET',
  });
});

test('maps native Parall message reads and strips prll references', () => {
  assert.deepEqual(parallProviderReadRequest({
    resource: 'messages', action: 'list', arguments: {
      chatId: 'prll://cht_daily', limit: '20', before: 'prll://msg_before',
      threadRootId: 'prll://msg_root', topLevel: true,
    },
  }, 'org_test'), {
    resource: '/api/v1/orgs/org_test/chats/cht_daily/messages?limit=20&before=msg_before&thread_root_id=msg_root&top_level=true',
    method: 'GET',
  });
  assert.deepEqual(parallProviderReadRequest({
    resource: 'messages', action: 'replies', arguments: { messageId: 'prll://msg_root' },
  }, 'org_test'), {
    resource: '/api/v1/messages/msg_root/replies?limit=20', method: 'GET',
  });
});

test('rejects writes, missing ids and unbounded native reads', () => {
  assert.throws(
    () => parallProviderReadRequest({ resource: 'messages', action: 'send' }, 'org_test'),
    /unsupported Parall provider operation/,
  );
  assert.throws(
    () => parallProviderReadRequest({ resource: 'messages', action: 'get', arguments: {} }, 'org_test'),
    /message id is required/,
  );
  assert.throws(
    () => parallProviderReadRequest({ resource: 'chats', action: 'list', arguments: { limit: 101 } }, 'org_test'),
    /limit must be an integer from 1 to 100/,
  );
});

test('namespaces Hub reply keys away from Parall dispatch effects', () => {
  assert.equal(
    parallIdempotencyKey({ idempotencyKey: 'reply:inb_123' }),
    'codex-hub:reply:inb_123',
  );
});

test('preserves ordinary proactive-send keys', () => {
  assert.equal(parallIdempotencyKey({ idempotencyKey: 'outbound:123' }), 'outbound:123');
});

test('falls back to the durable outbox id', () => {
  assert.equal(parallIdempotencyKey({ id: 'out_123' }), 'out_123');
});

test('rejects an item with no durable identity', () => {
  assert.throws(() => parallIdempotencyKey({}), /idempotency key is required/);
});

test('only explicit none expectation suppresses recipient agent dispatch', () => {
  assert.deepEqual(parallMessageHints({ responseExpectation: 'none' }), { no_reply: true });
  assert.equal(parallMessageHints({ responseExpectation: 'optional' }), undefined);
  assert.equal(parallMessageHints({ responseExpectation: 'required' }), undefined);
  assert.equal(parallMessageHints({}), undefined);
});

test('builds one Parall message with text, ordered attachments, thread and reply expectation', () => {
  assert.deepEqual(parallOutboxRequest({
    id: 'out_1', idempotencyKey: 'delivery:1', responseExpectation: 'none',
    content: { text: 'summary' }, conversation: { threadId: 'msg_root' },
  }, ['att_image', 'att_file']), {
    message_type: 'text',
    content: { text: 'summary' },
    idempotency_key: 'delivery:1',
    attachment_ids: ['att_image', 'att_file'],
    hints: { no_reply: true },
    thread_root_id: 'msg_root',
  });
});

test('reports both Parall message and provider attachment identities', () => {
  assert.deepEqual(parallDeliveryReceipts({
    content: { text: 'summary', attachments: [{ id: 'art_image' }, { id: 'art_file' }] },
  }, ['att_image', 'att_file'], 'msg_1'), [
    { kind: 'text', externalMessageId: 'msg_1' },
    { kind: 'attachment', artifactId: 'art_image', externalMessageId: 'msg_1', externalAttachmentId: 'att_image' },
    { kind: 'attachment', artifactId: 'art_file', externalMessageId: 'msg_1', externalAttachmentId: 'att_file' },
  ]);
});

test('maps Parall chats into Loom conversation types', () => {
  assert.equal(parallConversationType('direct', ''), 'dm');
  assert.equal(parallConversationType('group', ''), 'group');
  assert.equal(parallConversationType('', ''), 'group');
  assert.equal(parallConversationType('direct', 'msg_root'), 'thread');
});

test('projects joined Parall groups as stable conversation candidates', () => {
  assert.deepEqual(parallConversationCandidates([
    { id: 'cht_dm', type: 'direct', name: 'CP' },
    { id: 'cht_group', type: 'group', name: 'MS合作', description: '合作群' },
    { id: 'cht_group', type: 'group', name: 'MS合作' },
    { id: '', type: 'group', name: 'invalid' },
  ]), [{
    conversationId: 'cht_group',
    conversationType: 'group',
    displayName: 'MS合作',
    description: '合作群',
  }]);
});

test('builds bounded thread context from the root and replies preceding the dispatch', () => {
  const root = {
    id: 'msg_root', sender_id: 'usr_root', sender: { display_name: 'Root user', type: 'human' },
    content: { text: 'Original question' }, created_at: '2026-07-14T10:00:00Z',
  };
  const current = { id: 'msg_current', thread_root_id: 'msg_root', created_at: '2026-07-14T10:03:00Z' };
  const result = parallThreadContext(root, { data: [
    root,
    { id: 'msg_future', sender_id: 'usr_future', content: { text: 'future' }, created_at: '2026-07-14T10:04:00Z' },
    { id: 'msg_current', sender_id: 'usr_current', content: { text: 'dispatch' }, created_at: '2026-07-14T10:03:00Z' },
    { id: 'msg_2', sender_id: 'usr_2', content: { text: 'second reply' }, created_at: '2026-07-14T10:02:00Z' },
    { id: 'msg_1', sender_id: 'usr_1', content: { text: 'first reply' }, created_at: '2026-07-14T10:01:00Z' },
  ] }, current);
  assert.equal(result.rootExternalMessageId, 'msg_root');
  assert.deepEqual(result.messages.map((message) => [message.externalMessageId, message.role]), [
    ['msg_root', 'root'], ['msg_1', 'reply'], ['msg_2', 'reply'],
  ]);
  assert.equal(result.truncated, false);
});

test('marks thread context truncated when count or text budgets are exceeded', () => {
  const root = { id: 'msg_root', content: { text: '123456' }, created_at: '2026-07-14T10:00:00Z' };
  const replies = { has_more: true, data: [
    { id: 'msg_1', content: { text: 'abcdef' }, created_at: '2026-07-14T10:01:00Z' },
    { id: 'msg_2', content: { text: 'uvwxyz' }, created_at: '2026-07-14T10:02:00Z' },
  ] };
  const result = parallThreadContext(root, replies, {
    id: 'msg_current', thread_root_id: 'msg_root', created_at: '2026-07-14T10:03:00Z',
  }, { maxMessages: 2, maxMessageChars: 4, maxTotalChars: 8 });
  assert.deepEqual(result.messages.map((message) => message.externalMessageId), ['msg_root', 'msg_2']);
  assert.equal(result.messages[0].content.text, '1234');
  assert.equal(result.messages[0].textTruncated, true);
  assert.equal(result.truncated, true);
});

test('does not mark a queued message as being read', () => {
  assert.equal(inboxDispatchAction({ item: { state: 'queued' } }, false), 'wait');
});

test('marks received when Hub actually starts handling the message', () => {
  assert.equal(inboxDispatchAction({ item: { state: 'handling' } }, false), 'mark_received');
  assert.equal(inboxDispatchAction({ item: { state: 'handling' } }, true), 'wait');
});

test('acks a reply only after the provider send succeeds', () => {
  const entry = { item: { state: 'handled', outcome: 'reply' }, outboxItem: { state: 'pending' } };
  assert.equal(inboxDispatchAction(entry, true), 'wait');
  entry.outboxItem.state = 'sent';
  assert.equal(inboxDispatchAction(entry, true), 'ack');
});

test('acks an explicit no-reply decision', () => {
  assert.equal(
    inboxDispatchAction({ item: { state: 'handled', outcome: 'no_reply' } }, true),
    'ack',
  );
});
