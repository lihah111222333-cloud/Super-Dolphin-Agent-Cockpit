// @ts-check

import {
  compactTimelineText,
  dedupeAssistantTimelineItems,
  preferredAssistantTimelineItem,
  sameRuntimeAssistantContentLoose,
  sameTimelineContent,
  sameTimelineContentCompact,
  sameTimelineContentPrefix,
  sortTimelineChronologically,
} from './timelineRuntime.js';

function normalizeString(value) {
  return (value || '').toString().trim();
}

function extractText(value) {
  if (value === null || value === undefined) return '';
  if (typeof value === 'string' || typeof value === 'number' || typeof value === 'boolean') {
    return normalizeString(value);
  }
  if (Array.isArray(value)) {
    return value.map((item) => extractText(item)).filter(Boolean).join('\n');
  }
  if (typeof value === 'object') {
    return extractText(value.text || value.content || value.message || value.delta || value.output || value.result || value.answer || value.response);
  }
  return '';
}

export function runtimeTurnId(payload = {}) {
  return normalizeString(payload.turnId || payload.turn_id || payload.turn?.id);
}

export function runtimeAssistantStreamId(payload = {}) {
  const turnId = runtimeTurnId(payload);
  return turnId ? `assistant-stream-${turnId}` : '';
}

export function runtimeAssistantFallbackId(payload = {}, deps = {}) {
  const normalizeThreadId = deps.normalizeThreadId || normalizeString;
  const runtimeThreadIdentifier = deps.runtimeThreadIdentifier || (() => '');
  const nowMillis = deps.nowMillis || (() => Date.now());
  return (
    runtimeAssistantStreamId(payload) ||
    `assistant-stream-${normalizeThreadId(runtimeThreadIdentifier(payload)) || nowMillis()}`
  );
}

export function isRuntimeAssistantItem(item) {
  const type = normalizeString(item?.type || item?.kind || item?.role).toLowerCase();
  return (
    type.includes('agentmessage') ||
    type.includes('agent_message') ||
    type.includes('assistant') ||
    type === 'final_answer'
  );
}

export function runtimeAssistantCompletion(payload = {}, deps = {}) {
  const nowISO = deps.nowISO || (() => new Date().toISOString());
  const nowMillis = deps.nowMillis || (() => Date.now());
  const item = payload.item && typeof payload.item === 'object' ? payload.item : {};
  const hasItem = Object.keys(item).length > 0;
  if (hasItem && !isRuntimeAssistantItem(item)) return null;

  const text = extractText(item.text || item.content || payload.text || payload.content || payload.result);
  if (!text) return null;

  const explicitId = normalizeString(item.id || payload.messageId || payload.message_id);
  return {
    item: {
      id: explicitId || `assistant-final-${runtimeTurnId(payload) || nowMillis()}`,
      role: 'assistant',
      kind: 'assistant',
      text,
      time: normalizeString(payload.timestamp || item.ts || item.createdAt || item.created_at) || nowISO(),
      done: true,
      optimistic: false,
      runtime: true,
    },
    explicitId: Boolean(explicitId),
    streamId: runtimeAssistantStreamId(payload),
  };
}

export function isAssistantMessageDeltaEvent(eventName, payload = {}) {
  if (eventName === 'item/agentmessage/delta' || eventName === 'item/agent_message/delta') return true;
  if (eventName === 'message.delta' || eventName === 'agent_message_delta' || eventName === 'assistant:message_delta') return true;
  if (eventName !== 'turn/output/delta' && eventName !== 'turn/outputdelta') return false;
  const stream = normalizeString(payload.stream).toLowerCase();
  return !stream || stream === 'message' || stream === 'assistant' || stream === 'agentmessage' || stream === 'agent_message';
}

export function appendAssistantDeltaText(existingText, deltaText) {
  const base = (existingText || '').toString();
  const incoming = (deltaText || '').toString();
  if (!incoming) return base;
  if (!base) return incoming;
  if (base.endsWith(incoming)) return base;
  if (incoming.endsWith(base)) return incoming;
  const maxOverlap = Math.min(base.length, incoming.length, 32);
  for (let overlap = maxOverlap; overlap > 0; overlap -= 1) {
    if (base.slice(-overlap) === incoming.slice(0, overlap)) {
      return base + incoming.slice(overlap);
    }
  }
  return base + incoming;
}

export function assistantDeltaBufferKey(threadId, itemId) {
  return `${threadId}\u0000${itemId}`;
}

export function mergeRuntimeAssistantCompletion(existingItems = [], completion) {
  if (!completion?.item) return existingItems;
  const finalItem = completion.item;

  let lastUserIndex = -1;
  for (let index = existingItems.length - 1; index >= 0; index -= 1) {
    if (existingItems[index]?.role === 'user') {
      lastUserIndex = index;
      break;
    }
  }

  const turnAssistantItems = existingItems.slice(lastUserIndex + 1).filter(
    (item) => item?.role === 'assistant' && (item?.kind === 'assistant' || !item?.kind)
  );
  const accumulatedText = turnAssistantItems.map((item) => (item.text || '').toString()).join('');
  const compactAccumulated = compactTimelineText(accumulatedText);
  const compactFinal = compactTimelineText(finalItem.text);

  if (compactAccumulated && compactAccumulated === compactFinal) {
    if (turnAssistantItems.length === 1) {
      const singleItem = turnAssistantItems[0];
      const isStreamItem = singleItem.id === completion.streamId;
      const preferred = isStreamItem ? finalItem : preferredAssistantTimelineItem(singleItem, finalItem);
      return existingItems.map((item) => {
        if (item.id === singleItem.id) {
          return { ...preferred, done: true };
        }
        return item;
      });
    }
    return existingItems.map((item, index) => {
      if (index > lastUserIndex && item.role === 'assistant' && item.done === false) {
        return { ...item, done: true };
      }
      return item;
    });
  }

  const dropIds = new Set([finalItem.id, completion.streamId].filter(Boolean));
  const withoutReplaced = existingItems.filter((item) => !dropIds.has(item.id));
  lastUserIndex = -1;
  for (let index = withoutReplaced.length - 1; index >= 0; index -= 1) {
    if (withoutReplaced[index]?.role === 'user') {
      lastUserIndex = index;
      break;
    }
  }
  const duplicateIndex = withoutReplaced.findIndex((item, index) => (
    item.role === 'assistant' &&
    item.done !== false &&
    (
      sameTimelineContent(item, finalItem) ||
      (index > lastUserIndex && (
        sameTimelineContentCompact(item, finalItem) ||
        sameRuntimeAssistantContentLoose(item, finalItem) ||
        (item.runtime && finalItem.runtime && sameTimelineContentPrefix(item, finalItem))
      ))
    )
  ));
  if (duplicateIndex >= 0 && (
    !completion.explicitId ||
    withoutReplaced[duplicateIndex].runtime ||
    duplicateIndex > lastUserIndex
  )) {
    return dedupeAssistantTimelineItems(sortTimelineChronologically(withoutReplaced.map((item, index) => (
      index === duplicateIndex ? preferredAssistantTimelineItem(item, finalItem) : item
    ))));
  }
  return dedupeAssistantTimelineItems([...withoutReplaced, finalItem]);
}
