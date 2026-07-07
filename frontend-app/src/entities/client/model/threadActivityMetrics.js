// @ts-check

import {
  normalizeBackendThreadId,
  normalizeThreadId,
} from './threadIdentity.js';

const ACTIVITY_COUNT_FIELDS = Object.freeze({
  lspCalls: Object.freeze(['lspCalls', 'lsp_calls']),
  commands: Object.freeze(['commands']),
  fileEdits: Object.freeze(['fileEdits', 'file_edits']),
});

const TERMINAL_ACTIVE_TURN_STATUSES = new Set([
  'idle',
  'completed',
  'complete',
  'done',
  'ok',
  'success',
  'succeeded',
  'failed',
  'fail',
  'error',
  'interrupted',
  'canceled',
  'cancelled',
  'aborted',
  'stopped',
  'ended',
  'closed',
  'ready',
  'stalled',
  'archived',
  '空闲',
  '已完成',
  '失败',
  '错误',
  '已中断',
  '已停止',
]);

function normalizeString(value) {
  return (value || '').toString().trim();
}

function objectRecord(value) {
  return value && typeof value === 'object' && !Array.isArray(value) ? value : {};
}

function hasOwn(value, key) {
  return Boolean(value && typeof value === 'object' && Object.prototype.hasOwnProperty.call(value, key));
}

/**
 * @param {Record<string, unknown>} source
 * @param {readonly string[]} keys
 */
function firstFieldValue(source, keys = []) {
  const record = objectRecord(source);
  for (const key of keys) {
    const value = record[key];
    if (value !== undefined && value !== null && value !== '') return value;
  }
  return undefined;
}

/**
 * @param {Record<string, unknown>} source
 * @param {readonly string[]} keys
 */
function positiveNumberFromFields(source, keys = []) {
  const numeric = Number(firstFieldValue(source, keys));
  return Math.max(0, Number.isFinite(numeric) ? numeric : 0);
}

function tokenUsageObject(value) {
  return value && typeof value === 'object' && !Array.isArray(value) ? value : null;
}

function tokenUsageNumber(source, keys) {
  const object = tokenUsageObject(source);
  if (!object) return null;
  for (const key of keys) {
    if (!hasOwn(object, key)) continue;
    const number = Number(object[key]);
    if (Number.isFinite(number)) return number;
  }
  return null;
}

function tokenUsageIO(source) {
  const input = tokenUsageNumber(source, ['input', 'inputTokens', 'input_tokens', 'promptTokens', 'prompt_tokens']);
  const output = tokenUsageNumber(source, ['output', 'outputTokens', 'output_tokens', 'completionTokens', 'completion_tokens']);
  if (input === null && output === null) return null;
  return (input || 0) + (output || 0);
}

function firstTokenUsageNumber(...values) {
  return values.find((value) => Number.isFinite(value)) ?? null;
}

export function normalizeTurnSummary(value) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null;
  const id = normalizeString(value.id || value.turnId || value.turn_id);
  if (!id) return null;
  return {
    id,
    threadId: normalizeBackendThreadId(value.threadId || value.thread_id),
    agentId: normalizeThreadId(value.agentId || value.agent_id),
    status: normalizeString(value.status),
    startedAt: normalizeString(value.startedAt || value.started_at || value.createdAt || value.created_at || value.ts || value.time),
    updatedAt: normalizeString(value.updatedAt || value.updated_at),
    completedAt: normalizeString(value.completedAt || value.completed_at || value.finishedAt || value.finished_at),
  };
}

export function isTerminalActiveTurnStatus(status) {
  const normalized = normalizeString(status).toLowerCase();
  return Boolean(normalized && TERMINAL_ACTIVE_TURN_STATUSES.has(normalized));
}

export function isInterruptibleTurnSummary(turn) {
  return Boolean(turn?.id && !isTerminalActiveTurnStatus(turn.status));
}

export function activeTurnPayload(payload = {}) {
  if (hasOwn(payload, 'active_turn')) return payload.active_turn;
  if (hasOwn(payload, 'activeTurn')) return payload.activeTurn;
  return undefined;
}

export function shouldFloatThreadPatch(payload = {}) {
  if (normalizeString(payload.source || payload.event || payload.type) !== 'turn/completed') return false;
  const thread = payload.thread && typeof payload.thread === 'object' ? payload.thread : {};
  const status = normalizeString(payload.status || thread.state || thread.status).toLowerCase();
  return !status || ['idle', 'completed', 'success', 'succeeded'].includes(status);
}

export function threadActivityTimestamp() {
  return Date.now();
}

export function normalizeTokenUsage(value) {
  if (!value || typeof value !== 'object') return null;
  const usage = tokenUsageObject(value.usage);
  const info = tokenUsageObject(value.info);
  const tokenUsage = tokenUsageObject(value.tokenUsage);
  const currentUsage = tokenUsageObject(tokenUsage?.last) || tokenUsageObject(info?.last_token_usage);
  const cumulativeUsage = tokenUsageObject(tokenUsage?.total) || tokenUsageObject(info?.total_token_usage);
  const inputTokens = firstTokenUsageNumber(
    tokenUsageNumber(currentUsage, ['input', 'inputTokens', 'input_tokens', 'promptTokens', 'prompt_tokens']),
    tokenUsageNumber(usage, ['input', 'inputTokens', 'input_tokens', 'promptTokens', 'prompt_tokens']),
    tokenUsageNumber(value, ['input', 'inputTokens', 'input_tokens', 'promptTokens', 'prompt_tokens']),
    tokenUsageNumber(cumulativeUsage, ['input', 'inputTokens', 'input_tokens', 'promptTokens', 'prompt_tokens']),
    0,
  );
  const outputTokens = firstTokenUsageNumber(
    tokenUsageNumber(currentUsage, ['output', 'outputTokens', 'output_tokens', 'completionTokens', 'completion_tokens']),
    tokenUsageNumber(usage, ['output', 'outputTokens', 'output_tokens', 'completionTokens', 'completion_tokens']),
    tokenUsageNumber(value, ['output', 'outputTokens', 'output_tokens', 'completionTokens', 'completion_tokens']),
    tokenUsageNumber(cumulativeUsage, ['output', 'outputTokens', 'output_tokens', 'completionTokens', 'completion_tokens']),
    0,
  );
  const usedTokens = firstTokenUsageNumber(
    tokenUsageNumber(value, ['usedTokens', 'used_tokens']),
    tokenUsageNumber(currentUsage, ['totalTokens', 'total_tokens']),
    tokenUsageIO(currentUsage),
    tokenUsageNumber(usage, ['totalTokens', 'total_tokens']),
    tokenUsageNumber(value, ['totalTokens', 'total_tokens']),
    tokenUsageNumber(cumulativeUsage, ['totalTokens', 'total_tokens']),
    tokenUsageIO(cumulativeUsage),
    inputTokens + outputTokens,
    0,
  );
  const contextWindowTokens = firstTokenUsageNumber(
    tokenUsageNumber(value, ['contextWindowTokens', 'context_window_tokens', 'contextWindow', 'context_window', 'modelContextWindow', 'model_context_window']),
    tokenUsageNumber(tokenUsage, ['contextWindowTokens', 'context_window_tokens', 'contextWindow', 'context_window', 'modelContextWindow', 'model_context_window']),
    tokenUsageNumber(usage, ['contextWindowTokens', 'context_window_tokens', 'contextWindow', 'context_window', 'modelContextWindow', 'model_context_window']),
    tokenUsageNumber(info, ['contextWindowTokens', 'context_window_tokens', 'contextWindow', 'context_window', 'modelContextWindow', 'model_context_window']),
    0,
  );
  const rawPercent = firstTokenUsageNumber(
    tokenUsageNumber(value, ['usedPercent', 'used_percent']),
    contextWindowTokens > 0 ? (usedTokens / contextWindowTokens) * 100 : 0,
  ) || 0;
  const usedPercent = Math.min(100, Math.max(0, rawPercent));
  return { usedTokens, contextWindowTokens, usedPercent };
}

export function normalizeActivityStats(value) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null;
  const rawToolCalls = value.toolCalls || value.tool_calls || {};
  const toolCalls = {};
  for (const [name, count] of Object.entries(objectRecord(rawToolCalls))) {
    const key = normalizeString(name);
    const numeric = Number(count);
    if (key && Number.isFinite(numeric) && numeric > 0) toolCalls[key] = numeric;
  }
  return {
    lspCalls: positiveNumberFromFields(value, ACTIVITY_COUNT_FIELDS.lspCalls),
    commands: positiveNumberFromFields(value, ACTIVITY_COUNT_FIELDS.commands),
    fileEdits: positiveNumberFromFields(value, ACTIVITY_COUNT_FIELDS.fileEdits),
    toolCalls,
  };
}
