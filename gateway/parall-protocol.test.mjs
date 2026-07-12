import assert from 'node:assert/strict';
import test from 'node:test';

import { inboxDispatchAction, parallIdempotencyKey, parallMessageHints } from './parall-protocol.mjs';

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
