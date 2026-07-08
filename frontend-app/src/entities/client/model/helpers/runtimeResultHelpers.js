import {
  firstOptionalPresent,
  normalizeOptionalTextField,
  systemClockMillis,
  currentIsoTimestamp,
  parseOptionalTimestamp,
  parseRequiredJsonObject } from '../contractStoreModel.js';
import { compactSafeDiagnosticPreview } from '../../../../shared/api/safeDiagnosticPreview.js';

const MAX_RUNTIME_RESULT_ENTRIES = 120;
const RUNTIME_RESULT_DETAIL_LIMIT = 1600;
const RUNTIME_TOOL_FAILED_STATUSES = new Set(['failed', 'error']);

export const RUNTIME_TOOL_TERMINAL_STATUSES = new Set([
  'completed',
  'complete',
  'done',
  'ok',
  'success',
  'succeeded',
  'failed',
  'error',
]);

const defaultNormalizeString = (value) => normalizeOptionalTextField(value);

function defaultNormalizeTimestamp(value) {
  if (typeof value === 'boolean' || value === null || value === undefined) return 0;
  if (typeof value === 'number') return Number.isFinite(value) && value > 0 ? value : 0;
  const text = defaultNormalizeString(value);
  if (!text) return 0;
  const asNumber = Number(text);
  if (Number.isFinite(asNumber) && asNumber > 0) return asNumber;
  const sanitized = text.replace(/(\.\d{3})\d+/g, '$1');
  const parsed = parseOptionalTimestamp(sanitized);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : 0;
}

function compactRuntimeResultTextValue(value, normalizeString) {
  if (value === null || value === undefined) return '';
  const text = typeof value === 'string' ? value : JSON.stringify(value);
  const normalized = normalizeString(text);
  if (!normalized) return '';
  if (normalized.length <= RUNTIME_RESULT_DETAIL_LIMIT) return normalized;
  return `${normalized.slice(0, RUNTIME_RESULT_DETAIL_LIMIT)}...`;
}

function compactRuntimeDiagnosticPreviewText(value) {
  return compactSafeDiagnosticPreview(value, RUNTIME_RESULT_DETAIL_LIMIT, { parseJsonStrings: true });
}

function compactRuntimeToolResultText(value) {
  return compactSafeDiagnosticPreview(value, RUNTIME_RESULT_DETAIL_LIMIT, { parseJsonStrings: true });
}

function safeRuntimeToolResultFieldObject(preview) {
  if (!preview) return {};
  try {
    const parsed = parseRequiredJsonObject(preview);
    if (parsed && typeof parsed === 'object') return parsed;
  } catch {
    // Keep a structured container for the runtime popover without exposing raw text.
  }
  return { preview };
}

function safeRuntimeToolResultFields(item = {}) {
  return safeRuntimeToolResultFieldObject(compactRuntimeToolResultText(item));
}

function safeRuntimeRPCFieldValue(value, normalizeString) {
  if (typeof value === 'string') {
    const normalized = normalizeString(value);
    return normalized || undefined;
  }
  if (typeof value === 'number') return Number.isFinite(value) ? value : undefined;
  if (typeof value === 'boolean') return value;
  return undefined;
}

const RUNTIME_RPC_FIELD_ALIASES = [
  ['method'],
  ['rpcMethod', 'method'],
  ['rpc_method', 'method'],
  ['threadId'],
  ['thread_id', 'threadId'],
  ['req_id'],
  ['reqId', 'req_id'],
  ['trace_id'],
  ['traceId', 'trace_id'],
  ['span_id'],
  ['spanId', 'span_id'],
  ['status'],
];

function safeRuntimeRPCResultFields(fields = {}, detail = '', normalizeString = defaultNormalizeString) {
  const out = {};
  for (const [source, target = source] of RUNTIME_RPC_FIELD_ALIASES) {
    const value = safeRuntimeRPCFieldValue(fields[source], normalizeString);
    if (value !== undefined && out[target] === undefined) out[target] = value;
  }
  const preview = safeRuntimeToolResultFieldObject(detail);
  if (Object.keys(preview).length > 0) out.preview = preview;
  return out;
}

function normalizeRuntimeToolNameValue(name, normalizeString) {
  const raw = normalizeString(name);
  if (!raw) return '';
  const lower = raw.toLowerCase();
  const mcpParts = lower.startsWith('mcp__') ? lower.split('__') : [];
  const withoutMCPServer = mcpParts.length >= 3 ? mcpParts.slice(2).join('__') : raw;
  return (
    withoutMCPServer
    .replace(/[./:-]+/g, '_')
    .replace(/^functions_+/, '')
    .replace(/^function_+/, '')
    .replace(/^tools_+/, '')
    .replace(/^tool_+/, '')
    .replace(/^lsp_+/, '')
    .replace(/_+/g, '_')
    .replace(/^_+|_+$/g, '')
  );
}

function runtimeToolResultDetail(item = {}) {
  for (const key of ['output', 'preview', 'result', 'error', 'message', 'text']) {
    const detail = compactRuntimeToolResultText(item[key]);
    if (detail) return detail;
  }
  return '';
}

function runtimeToolResultEntry(item, threadId, index, helpers) {
  const { normalizeString, nowISO, nowMillis } = helpers;
  const kind = normalizeString(firstOptionalPresent(item?.kind, item?.type)).toLowerCase();
  if (kind !== 'tool') return null;
  const toolName = normalizeRuntimeToolNameValue(item.tool || item.toolName || item.name, normalizeString) || 'tool';
  const status = normalizeString(item.status).toLowerCase();
  const failed = RUNTIME_TOOL_FAILED_STATUSES.has(status) || item.success === false || Boolean(normalizeString(item.error));
  const detail = runtimeToolResultDetail(item);
  const terminal = RUNTIME_TOOL_TERMINAL_STATUSES.has(status);
  if (!detail && !terminal) return null;
  const summary = detail ? detail.replace(/\s+/g, ' ').slice(0, 180) : '';
  return {
    id: normalizeString(item.id) || `tool-result-${threadId}-${index}-${nowMillis()}`,
    timestamp: normalizeString(item.ts || item.time || item.createdAt || item.created_at) || nowISO(),
    level: failed ? 'error' : 'info',
    event: 'tool.result',
    threadId,
    message: `${toolName} ${failed ? '失败' : '返回'}${summary ? ` · ${summary}` : ''}`,
    detail,
    fields: safeRuntimeToolResultFields(item),
    signature: `tool.result|${threadId}|${normalizeString(item.id) || toolName}|${detail}`,
  };
}

function runtimeResultEntriesFromTimelineItemsValue(items, threadId, helpers) {
  if (!Array.isArray(items) || !threadId) return [];
  return items.map((item, index) => runtimeToolResultEntry(item, threadId, index, helpers)).filter(Boolean);
}

function runtimeResultEntryFromRPCDoneValue(event, fields = {}, helpers) {
  if (event !== 'api.rpc.done') return null;
  const { normalizeString, normalizeThreadId, runtimeThreadIdentifier, nowISO, nowMillis, randomHex } = helpers;
  const method = normalizeString(fields.method || fields.rpcMethod || fields.rpc_method);
  const detail = compactRuntimeDiagnosticPreviewText(fields.result_preview || fields.result);
  if (!method || !detail) return null;
  const threadId = normalizeThreadId(runtimeThreadIdentifier(fields));
  const summary = detail.replace(/\s+/g, ' ').slice(0, 180);
  return {
    id: `${event}-${fields.req_id || nowMillis()}-${randomHex()}`,
    timestamp: nowISO(),
    level: 'info',
    event,
    threadId,
    message: `${method} 返回 · ${summary}`,
    detail,
    fields: safeRuntimeRPCResultFields(fields, detail, normalizeString),
    signature: `${event}|${threadId}|${method}|${detail}`,
  };
}

function mergeRuntimeResultEntriesValue(existingEntries = [], incomingEntries = [], normalizeTimestamp = defaultNormalizeTimestamp) {
  const nextById = new Map();
  for (const entry of [...incomingEntries, ...existingEntries]) {
    const key = firstOptionalPresent(entry?.signature, entry?.id);
    if (!key) continue;
    const existing = nextById.get(key);
    if (!existing) {
      nextById.set(key, entry);
      continue;
    }
    nextById.set(key, {
      ...existing,
      occurrenceCount: (Number(existing.occurrenceCount) || 1) + (Number(entry.occurrenceCount) || 1),
    });
  }
  return [...nextById.values()]
    .sort((left, right) => normalizeTimestamp(right.timestamp) - normalizeTimestamp(left.timestamp))
    .slice(0, MAX_RUNTIME_RESULT_ENTRIES);
}

export function createRuntimeResultHelperSet(deps = {}) {
  const helpers = {
    normalizeString: deps.normalizeString || defaultNormalizeString,
    normalizeTimestamp: deps.normalizeTimestamp || defaultNormalizeTimestamp,
    normalizeThreadId: deps.normalizeThreadId || deps.normalizeString || defaultNormalizeString,
    runtimeThreadIdentifier: deps.runtimeThreadIdentifier || (() => ''),
    nowISO: deps.nowISO || (() => currentIsoTimestamp()),
    nowMillis: deps.nowMillis || (() => systemClockMillis()),
    randomHex: deps.randomHex || (() => Math.random().toString(16).slice(2)),
  };

  return {
    compactRuntimeResultText: (value) => compactRuntimeResultTextValue(value, helpers.normalizeString),
    mergeRuntimeResultEntries: (existingEntries, incomingEntries) => (
      mergeRuntimeResultEntriesValue(existingEntries, incomingEntries, helpers.normalizeTimestamp)
    ),
    normalizeRuntimeToolName: (name) => normalizeRuntimeToolNameValue(name, helpers.normalizeString),
    runtimeResultEntriesFromTimelineItems: (items, threadId) => (
      runtimeResultEntriesFromTimelineItemsValue(items, threadId, helpers)
    ),
    runtimeResultEntryFromRPCDone: (event, fields) => runtimeResultEntryFromRPCDoneValue(event, fields, helpers),
  };
}
