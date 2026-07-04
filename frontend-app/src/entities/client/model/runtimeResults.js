import { compactSafeDiagnosticPreview } from '../../../shared/api/safeDiagnosticPreview.js';

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

const defaultNormalizeString = (value) => (value || '').toString().trim();

function defaultNormalizeTimestamp(value) {
  if (typeof value === 'boolean' || value === null || value === undefined) return 0;
  if (typeof value === 'number') return Number.isFinite(value) && value > 0 ? value : 0;
  const text = defaultNormalizeString(value);
  if (!text) return 0;
  const asNumber = Number(text);
  if (Number.isFinite(asNumber) && asNumber > 0) return asNumber;
  const sanitized = text.replace(/(\.\d{3})\d+/g, '$1');
  const parsed = Date.parse(sanitized);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : 0;
}

export function createRuntimeResultHelpers(deps = {}) {
  const normalizeString = deps.normalizeString || defaultNormalizeString;
  const normalizeTimestamp = deps.normalizeTimestamp || defaultNormalizeTimestamp;
  const normalizeThreadId = deps.normalizeThreadId || normalizeString;
  const runtimeThreadIdentifier = deps.runtimeThreadIdentifier || (() => '');
  const nowISO = deps.nowISO || (() => new Date().toISOString());
  const nowMillis = deps.nowMillis || (() => Date.now());
  const randomHex = deps.randomHex || (() => Math.random().toString(16).slice(2));

  const compactRuntimeResultText = (value) => {
    if (value === null || value === undefined) return '';
    const text = typeof value === 'string' ? value : JSON.stringify(value);
    const normalized = normalizeString(text);
    if (!normalized) return '';
    if (normalized.length <= RUNTIME_RESULT_DETAIL_LIMIT) return normalized;
    return `${normalized.slice(0, RUNTIME_RESULT_DETAIL_LIMIT)}...`;
  };

  const compactRuntimeDiagnosticPreviewText = (value) => (
    compactSafeDiagnosticPreview(value, RUNTIME_RESULT_DETAIL_LIMIT, { parseJsonStrings: true })
  );

  const normalizeRuntimeToolName = (name) => {
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
  };

  const runtimeToolResultDetail = (item = {}) => {
    for (const key of ['output', 'preview', 'result', 'error', 'message', 'text']) {
      const detail = compactRuntimeResultText(item[key]);
      if (detail) return detail;
    }
    return '';
  };

  const runtimeToolResultEntry = (item, threadId, index = 0) => {
    const kind = normalizeString(item?.kind || item?.type).toLowerCase();
    if (kind !== 'tool') return null;
    const toolName = normalizeRuntimeToolName(item.tool || item.toolName || item.name) || 'tool';
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
      fields: item,
      signature: `tool.result|${threadId}|${normalizeString(item.id) || toolName}|${detail}`,
    };
  };

  const runtimeResultEntriesFromTimelineItems = (items, threadId) => {
    if (!Array.isArray(items) || !threadId) return [];
    return (
      items
      .map((item, index) => runtimeToolResultEntry(item, threadId, index))
      .filter(Boolean)
    );
  };

  const runtimeResultEntryFromRPCDone = (event, fields = {}) => {
    if (event !== 'api.rpc.done') return null;
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
      fields,
      signature: `${event}|${threadId}|${method}|${detail}`,
    };
  };

  const mergeRuntimeResultEntries = (existingEntries = [], incomingEntries = []) => {
    const nextById = new Map();
    for (const entry of [...incomingEntries, ...existingEntries]) {
      const key = entry?.signature || entry?.id;
      if (!key) continue;
      const existing = nextById.get(key);
      if (existing) {
        nextById.set(key, {
          ...existing,
          occurrenceCount: (Number(existing.occurrenceCount) || 1) + (Number(entry.occurrenceCount) || 1),
        });
        continue;
      }
      nextById.set(key, entry);
    }
    return (
      [...nextById.values()]
      .sort((left, right) => {
        const leftTime = normalizeTimestamp(left.timestamp);
        const rightTime = normalizeTimestamp(right.timestamp);
        return rightTime - leftTime;
      })
      .slice(0, MAX_RUNTIME_RESULT_ENTRIES)
    );
  };

  return {
    compactRuntimeResultText,
    mergeRuntimeResultEntries,
    normalizeRuntimeToolName,
    runtimeResultEntriesFromTimelineItems,
    runtimeResultEntryFromRPCDone,
  };
}
