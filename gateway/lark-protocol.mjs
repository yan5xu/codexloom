function addIdentity(target, value) {
  for (const item of String(value || '').split(',')) {
    const id = item.trim();
    if (id) target.add(id);
  }
}

export function larkBotIdentities(configured, whoami = {}) {
  const ids = new Set();
  addIdentity(ids, configured);
  addIdentity(ids, whoami.appId);
  addIdentity(ids, whoami.openId);
  addIdentity(ids, whoami.data?.app_id);
  addIdentity(ids, whoami.data?.appId);
  addIdentity(ids, whoami.data?.open_id);
  addIdentity(ids, whoami.data?.openId);
  return ids;
}

export function hasLarkBotMention(mentions, botIdentities) {
  return (mentions || []).some((mention) => botIdentities.has(String(mention?.id || '').trim()));
}

export function larkReactionAction(entry) {
  const state = entry?.item?.state;
  if (state === 'failed') return 'remove';
  if (state !== 'handled') return 'wait';
  if (entry.item.outcome === 'no_reply') return 'remove';
  if (entry.item.outcome === 'reply' && entry.outboxItem?.state === 'sent') return 'remove';
  return 'wait';
}
