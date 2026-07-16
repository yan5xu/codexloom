import assert from 'node:assert/strict';
import test from 'node:test';

import {
  hasLarkBotMention,
  larkBotIdentities,
  larkLocalImageAttachment,
  larkOutboundContentArgs,
  larkReactionAction,
} from './lark-protocol.mjs';

test('collects configured Open ID and whoami app ID as bot aliases', () => {
  const ids = larkBotIdentities('ou_bot', { appId: 'cli_app' });
  assert.deepEqual([...ids], ['ou_bot', 'cli_app']);
});

test('matches the app ID returned by bot-view message normalization', () => {
  const ids = larkBotIdentities('ou_bot', { appId: 'cli_app' });
  assert.equal(hasLarkBotMention([{ id: 'cli_app' }], ids), true);
});

test('matches the Open ID returned by user-view message normalization', () => {
  const ids = larkBotIdentities('ou_bot', { appId: 'cli_app' });
  assert.equal(hasLarkBotMention([{ id: 'ou_bot' }], ids), true);
});

test('rejects mentions of another bot', () => {
  const ids = larkBotIdentities('ou_bot', { appId: 'cli_app' });
  assert.equal(hasLarkBotMention([{ id: 'cli_other' }], ids), false);
});

test('keeps the reaction while a message is queued or handling', () => {
  assert.equal(larkReactionAction({ item: { state: 'queued' } }), 'wait');
  assert.equal(larkReactionAction({ item: { state: 'handling' } }), 'wait');
});

test('keeps the reaction until a reply is sent', () => {
  const entry = { item: { state: 'handled', outcome: 'reply' }, outboxItem: { state: 'pending' } };
  assert.equal(larkReactionAction(entry), 'wait');
  entry.outboxItem.state = 'sent';
  assert.equal(larkReactionAction(entry), 'remove');
});

test('removes the reaction for no-reply and failed terminal states', () => {
  assert.equal(larkReactionAction({ item: { state: 'handled', outcome: 'no_reply' } }), 'remove');
  assert.equal(larkReactionAction({ item: { state: 'failed' } }), 'remove');
});

test('selects a local image attachment and ignores non-images', () => {
  const item = { content: { attachments: [
    { path: '/tmp/report.pdf', mimeType: 'application/pdf' },
    { path: '/tmp/photo.png', mimeType: 'image/png' },
  ] } };
  assert.equal(larkLocalImageAttachment(item)?.path, '/tmp/photo.png');
  assert.equal(larkLocalImageAttachment({ content: { attachments: [{ path: '/tmp/report.pdf' }] } }), null);
});

test('sends text replies through the lark-cli markdown post path', () => {
  const item = { content: { text: '**Result**\n\n- first' } };
  assert.deepEqual(larkOutboundContentArgs(item), ['--markdown', item.content.text]);
  assert.deepEqual(larkOutboundContentArgs(item, './photo.png'), ['--image', './photo.png']);
});
