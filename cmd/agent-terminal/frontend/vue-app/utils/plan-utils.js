import { hasJsonRenderSpec, extractSpecBlocks } from '../services/json-render-engine.js';

function planText(item) {
  return (item?.text || '').toString().trim();
}

export function resolvePlanItemKey(item) {
  if (!item || typeof item !== 'object') return '';
  const id = (item.id || '').toString().trim();
  if (id) return `id:${id}`;
  const timestamp = (item.ts || '').toString().trim();
  const text = planText(item);
  if (!text) return '';
  // NOTE: `done` is intentionally excluded from the key so that dismiss state
  // survives plan status transitions (in-progress → complete).
  if (timestamp) return `ts:${timestamp}`;
  return text.length > 32 ? text.substring(0, 32) : text;
}

export function isPlanSuperseded(item, index, source) {
  if (item?.kind !== 'plan' || !Array.isArray(source)) return false;
  for (let cursor = index + 1; cursor < source.length; cursor += 1) {
    const next = source[cursor];
    const kind = (next?.kind || '').toString().trim();
    if (kind === 'plan' && planText(next)) return true;
    if (kind === 'user' && !next?.internal && planText(next)) return true;
  }
  return false;
}

export function pinnedPlanHasSpec(text) {
  return hasJsonRenderSpec(text);
}

export function splitPinnedPlanSpec(text) {
  return extractSpecBlocks(text);
}

export function pinnedPlanCardSpec(plan) {
  const statusText = (plan?.statusText || (plan?.done ? '完成' : '进行中')).toString();
  const metaChildren = [
    { type: 'Badge', text: statusText, variant: 'default' },
  ];
  const rawText = (plan?.text || '').toString();
  const bodyChildren = pinnedPlanHasSpec(rawText)
    ? splitPinnedPlanSpec(rawText).flatMap((part) => {
      if (part?.type === 'text' && (part.content || '').toString().trim()) {
        return [{ type: 'Markdown', text: part.content }];
      }
      if (part?.type === 'spec' && part.spec && typeof part.spec === 'object') {
        return [part.spec];
      }
      return [];
    })
    : rawText.trim()
      ? [{ type: 'Markdown', text: rawText }]
      : [{ type: 'Text', text: '(empty plan)' }];
  const children = [
    {
      type: 'Stack',
      direction: 'row',
      gap: 8,
      children: metaChildren,
    },
    {
      type: 'Separator',
    },
    ...bodyChildren,
  ];
  return {
    type: 'Card',
    title: 'PLAN',
    description: statusText,
    children,
  };
}
