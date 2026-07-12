const DISPATCH_EFFECT_PREFIXES = ['reply:', 'task_update:'];

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

export function inboxDispatchAction(entry, received) {
  const state = entry?.item?.state;
  if (!received && (state === 'handling' || state === 'handled')) return 'mark_received';
  if (state !== 'handled') return 'wait';
  if (entry.item.outcome === 'no_reply') return 'ack';
  if (entry.item.outcome === 'reply' && entry.outboxItem?.state === 'sent') return 'ack';
  return 'wait';
}
