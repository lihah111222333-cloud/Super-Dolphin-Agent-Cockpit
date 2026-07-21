import { optionalTextField, systemClockMillis, currentIsoTimestamp } from '../contractStoreModel.js';
import { safeDiagnosticPreviewValue } from '../../../../shared/api/support/safeDiagnosticPreview.js';

const MAX_WARNING_ENTRIES = 300;

const SAFE_WARNING_FIELD_ALIASES = [
  ['method'],
  ['rpcMethod'],
  ['rpc_method'],
  ['action'],
  ['code'],
  ['status'],
  ['provider'],
  ['requestedProvider'],
  ['requested_provider', 'requestedProvider'],
  ['reason'],
  ['eventName'],
  ['event_name', 'eventName'],
  ['payloadKeys'],
  ['payload_keys', 'payloadKeys'],
  ['eventKeys'],
  ['event_keys', 'eventKeys'],
  ['rawLen'],
  ['raw_len', 'rawLen'],
  ['threadId'],
  ['thread_id', 'threadId'],
  ['req_id'],
  ['reqId', 'req_id'],
  ['trace_id'],
  ['traceId', 'trace_id'],
  ['span_id'],
  ['spanId', 'span_id'],
  ['parent_span_id'],
  ['parentSpanId', 'parent_span_id'],
  ['agent_id'],
  ['agentId', 'agent_id'],
  ['turn_id'],
  ['turnId', 'turn_id'],
  ['call_id'],
  ['callId', 'call_id'],
];

const WARNING_CORRELATION_SECRET_PATTERNS = [
  /\b(?:api[_\s-]?key|auth[_\s-]?token|access[_\s-]?token|refresh[_\s-]?token|id[_\s-]?token|authorization|credential(?:s)?|password|secret|token)\b\s*[:=]\s*["']?[^"',\s}]+/i,
  /\b(?:bearer|basic)\s+[a-z0-9._~+/=-]{8,}\b/i,
  /\b(?:sk|pk|rk)-[a-z0-9][a-z0-9_-]{6,}\b/i,
  /\b(?:ghp|gho|ghu|ghs|glpat|xoxb|xoxp|xoxa|xoxr)[_-][a-z0-9_-]{6,}\b/i,
  /\bgithub_pat_[a-z0-9_]{12,}\b/i,
  /\bAKIA[0-9A-Z]{16}\b/,
];

function safeWarningCorrelationScalar(value) {
  if (value === null || value === undefined) return undefined;
  if (typeof value === 'number') return Number.isFinite(value) ? value : undefined;
  if (typeof value === 'boolean') return value;
  const text = value.toString().trim();
  if (!text || text.length > 160) return undefined;
  if (WARNING_CORRELATION_SECRET_PATTERNS.some((pattern) => pattern.test(text))) return undefined;
  if (text.startsWith('/') || text.includes('\\') || /[A-Za-z]:[\\/]/.test(text)) return undefined;
  if (!/^[A-Za-z0-9_.:/-]+$/.test(text)) return undefined;
  return text;
}

function safeWarningCorrelationValue(value) {
  if (Array.isArray(value)) {
    const items = value
      .map((item) => safeWarningCorrelationScalar(item))
      .filter((item) => item !== undefined);
    return items.length > 0 ? items : undefined;
  }
  return safeWarningCorrelationScalar(value);
}

export function safeWarningFields(fields = {}) {
  const preview = safeDiagnosticPreviewValue(fields);
  const out = preview && typeof preview === 'object' && !Array.isArray(preview) ? { ...preview } : {};
  for (const [source, target = source] of SAFE_WARNING_FIELD_ALIASES) {
    const value = safeWarningCorrelationValue(fields?.[source]);
    if (value !== undefined) out[target] = value;
  }
  return out;
}

export function attachWarningRuntime(runtime, deps) {
  const {
    cleanObject,
    emitFrontendTraceEvent,
    normalizeString,
    normalizeThreadId,
    runtimeThreadIdentifier,
  } = deps;
  const { set } = runtime;

  const warningErrorKey = (fields = {}) => {
    const error = fields?.error;
    if (typeof error === 'string') return error;
    if (error && typeof error === 'object') {
      return normalizeString(error.message || error.code || error.data || JSON.stringify(error));
    }
    return '';
  };

  const warningSignature = (level, event, threadId, fields = {}) => [
    level,
    event,
    threadId,
    normalizeString(fields.method || fields.action || fields.rpcMethod || fields.rpc_method),
    warningErrorKey(fields),
  ].join('|');

  const emitWarningTrace = (level, event, threadId, fields = {}) => {
    const method = normalizeString(event);
    if (!method) return;
    const metadata = cleanObject({
      component: warningTraceComponent(method),
      req_id: fields.req_id ?? fields.reqId,
    });
    emitFrontendTraceEvent(cleanObject({
      phase: 'frontend.warning',
      method,
      trace_id: normalizeString(fields.trace_id || fields.traceId),
      span_id: normalizeString(fields.span_id || fields.spanId),
      parent_span_id: normalizeString(fields.parent_span_id || fields.parentSpanId),
      thread_id: threadId,
      agent_id: normalizeString(fields.agent_id || fields.agentId),
      turn_id: normalizeString(fields.turn_id || fields.turnId),
      call_id: normalizeString(fields.call_id || fields.callId),
      status: warningTraceStatus(level, method),
      error: warningErrorKey(fields),
      metadata: Object.keys(metadata).length > 0 ? metadata : undefined,
    }));
  };

  const addWarning = (level, event, fields = {}) => {
    if (level !== 'warn' && level !== 'error') return;
    const threadId = normalizeThreadId(runtimeThreadIdentifier(fields));
    const safeFields = safeWarningFields(fields);
    const signature = warningSignature(level, event, threadId, safeFields);
    const entry = {
      id: `${event}-${systemClockMillis()}-${Math.random().toString(16).slice(2)}`,
      timestamp: currentIsoTimestamp(),
      level,
      event,
      threadId,
      fields: safeFields,
      occurrenceCount: 1,
      signature,
    };
    set((state) => ({
      warningEntries: mergeWarningEntries(state.warningEntries, entry, safeFields),
    }));
    emitWarningTrace(level, event, threadId, safeFields);
  };

  Object.assign(runtime, { addWarning });
}

export function warningTraceComponent(event) {
  return optionalTextField(event).trim().split(/[./]/).filter(Boolean)[0] || optionalTextField();
}

export function warningTraceStatus(level, event) {
  const method = optionalTextField(event).trim().toLowerCase();
  if (level === 'error' || method.endsWith('.failed') || method.endsWith('/failed')) return 'error';
  return 'ok';
}

export function mergeWarningEntries(warningEntries, entry, fields, maxEntries = MAX_WARNING_ENTRIES) {
  const existingIndex = warningEntries.findIndex((item) => item.signature === entry.signature);
  if (existingIndex < 0) return [entry, ...warningEntries].slice(0, maxEntries);
  const existing = warningEntries[existingIndex];
  const updated = {
    ...existing,
    id: entry.id,
    timestamp: entry.timestamp,
    fields,
    occurrenceCount: (Number(existing.occurrenceCount) || 1) + 1,
  };
  return [
    updated,
    ...warningEntries.slice(0, existingIndex),
    ...warningEntries.slice(existingIndex + 1),
  ].slice(0, maxEntries);
}
