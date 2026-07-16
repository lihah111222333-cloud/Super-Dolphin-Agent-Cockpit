import { firstOptionalPresent, normalizeOptionalTextField, optionalTextField, systemClockMillis, currentIsoTimestamp } from './contractStoreModel.js';
import { validateTurnTerminalV2 } from '../../../shared/contracts/turnContractValidators.js';
// @ts-check

export { mergeRuntimeAssistantCompletionImpl as mergeRuntimeAssistantCompletion } from './helpers/runtimeAssistantTimelineMerge.js';

function normalizeString(value) {
  return normalizeOptionalTextField(value);
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

export function parseRuntimeTurnTerminal(payload) {
  try {
    return { value: validateTurnTerminalV2(payload) };
  } catch {
    return { error: 'canonical_terminal_contract' };
  }
}

export function runtimeTurnRefKey(threadId, turnId) {
  return `${threadId}\u0000${turnId}`;
}

function stableJSONValue(value) {
  if (Array.isArray(value)) return value.map(stableJSONValue);
  if (!value || typeof value !== 'object') return value;
  return Object.fromEntries(Object.keys(value)
    .sort((left, right) => (left < right ? -1 : left > right ? 1 : 0))
    .map((key) => [key, stableJSONValue(value[key])]));
}

export function runtimeTerminalFingerprint(terminal) {
  const content = { ...terminal };
  delete content.eventId;
  return JSON.stringify(stableJSONValue(content));
}

export function runtimeAssistantStreamId(payload = {}) {
  const turnId = runtimeTurnId(payload);
  return turnId ? `assistant-stream-${turnId}` : '';
}

export function runtimeAssistantFallbackId(payload = {}, deps = {}) {
  const normalizeThreadId = deps.normalizeThreadId || normalizeString;
  const runtimeThreadIdentifier = deps.runtimeThreadIdentifier || (() => '');
  const nowMillis = deps.nowMillis || (() => systemClockMillis());
  return (
    runtimeAssistantStreamId(payload) ||
    `assistant-stream-${normalizeThreadId(runtimeThreadIdentifier(payload)) || nowMillis()}`
  );
}

export function isRuntimeAssistantItem(item) {
  const type = normalizeString(firstOptionalPresent(item?.type, item?.kind, item?.role)).toLowerCase();
  return (
    type.includes('agentmessage') ||
    type.includes('agent_message') ||
    type.includes('assistant') ||
    type === 'final_answer'
  );
}

export function runtimeAssistantCompletion(payload = {}, deps = {}) {
  const nowISO = deps.nowISO || (() => currentIsoTimestamp());
  const nowMillis = deps.nowMillis || (() => systemClockMillis());
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
  const base = optionalTextField(existingText);
  const incoming = optionalTextField(deltaText);
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

export function assistantDeltaBufferKey(threadId, itemId, turnId = '') {
  return `${threadId}\u0000${turnId}\u0000${itemId}`;
}
