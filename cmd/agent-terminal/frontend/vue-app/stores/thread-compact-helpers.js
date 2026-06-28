// @ts-nocheck
import { normalizeThreadID } from './bridge-event-parser.js';

function waitMs(ms) {
  return new Promise((resolve) => {
    globalThis.setTimeout(resolve, Math.max(0, Number(ms) || 0));
  });
}

export function tokenUsageSignature(state, threadId) {
  const usage = state.tokenUsageByThread?.[threadId];
  if (!usage || typeof usage !== 'object') return '';
  const used = Number(usage.usedTokens);
  const limit = Number(usage.contextWindowTokens);
  const percent = Number(usage.usedPercent);
  const roundedUsed = Number.isFinite(used) ? Math.round(used) : '';
  const roundedLimit = Number.isFinite(limit) ? Math.round(limit) : '';
  const roundedPercent = Number.isFinite(percent) ? percent.toFixed(3) : '';
  return [roundedUsed, roundedLimit, roundedPercent].join('|');
}

export function dialogTimelineSignature(state, threadId) {
  const items = Array.isArray(state?.timelinesByThread?.[threadId]) ? state.timelinesByThread[threadId] : [];
  for (let index = items.length - 1; index >= 0; index -= 1) {
    const item = items[index];
    const kind = (item?.kind || '').toString().trim();
    if (kind !== 'assistant' && kind !== 'user') continue;
    return [items.length, index, kind, (item?.id || '').toString().trim(), (item?.ts || '').toString().trim(), (item?.text || '').toString().trim().slice(0, 160)].join('|');
  }
  return `${items.length}|`;
}

export async function waitForCompactResponse(ctx, threadId, baselineTimelineSignature) {
  const id = normalizeThreadID(threadId);
  if (!id || typeof ctx.loadMessages !== 'function') return { attempts: 0, changed: false, signature: dialogTimelineSignature(ctx.state, id) };
  let signature = dialogTimelineSignature(ctx.state, id);
  let attempts = 0;
  while (!(signature && signature !== baselineTimelineSignature) && attempts < 3) {
    attempts += 1;
    await ctx.loadMessages(id, 300, { syncRuntime: false });
    signature = dialogTimelineSignature(ctx.state, id);
    if (!(signature && signature !== baselineTimelineSignature) && attempts < 3) await waitMs(120);
  }
  return { attempts, changed: Boolean(signature && signature !== baselineTimelineSignature), signature };
}

export function compactFailureResult(isTimeout) {
  if (isTimeout) return { message: '压缩超时：未收到完成信号，请重试。', code: 'compact_timeout' };
  return { message: '压缩失败，请重试。', code: 'compact_failed' };
}
