import { compactSafeDiagnosticPreview, safeDiagnosticPreviewValue } from '../../../shared/api/support/safeDiagnosticPreview.js';
import { parseStrictJsonValue, requireTimestampMillis } from '../../shared/pageShared.js';

const RUNTIME_LOG_DETAIL_LIMIT = 1600;
const SAFE_WARNING_FIELD_ALIASES = [
  ['method'],
  ['rpcMethod'],
  ['rpc_method'],
  ['action'],
  ['code'],
  ['status'],
  ['provider'],
  ['req_id'],
  ['reqId', 'req_id'],
];

function runtimeLogTextValue(value) {
  if (value === null || value === undefined) return '';
  return value.toString();
}

function firstRuntimeLogText(values) {
  for (const value of values) {
    const text = runtimeLogTextValue(value);
    if (text) return text;
  }
  return '';
}

function runtimeLogTimestampCandidates(entry) {
  return [entry?.timestamp, entry?.time, entry?.ts];
}

function runtimeLogLabelCandidates(entry) {
  return [entry?.message, entry?.event, entry?.method];
}

function safeRuntimeLogDetail(value) {
  return compactSafeDiagnosticPreview(value, RUNTIME_LOG_DETAIL_LIMIT, { parseJsonStrings: true });
}

function parseRuntimeLogJSONText(value) {
  if (typeof value !== 'string') return value;
  const text = value.trim();
  if (!text) return value;
  try {
    return parseStrictJsonValue(text, 'runtime log detail');
  } catch {
    return value;
  }
}

function compactRuntimeLogText(value) {
  const text = typeof value === 'string' ? value : JSON.stringify(value);
  if (!text) return '';
  if (text.length <= RUNTIME_LOG_DETAIL_LIMIT) return text;
  return `${text.slice(0, RUNTIME_LOG_DETAIL_LIMIT)}...`;
}

function safeWarningCorrelationValue(value) {
  if (value === null || value === undefined) return undefined;
  if (typeof value === 'number') return Number.isFinite(value) ? value : undefined;
  if (typeof value === 'boolean') return value;
  const text = value.toString().trim();
  if (!text || text.length > 160) return undefined;
  if (text.startsWith('/') || text.includes('\\') || /[A-Za-z]:[\\/]/.test(text)) return undefined;
  if (!/^[A-Za-z0-9_.:/-]+$/.test(text)) return undefined;
  return text;
}

function safeRuntimeWarningFields(fields = {}) {
  const preview = safeDiagnosticPreviewValue(fields);
  const out = preview && typeof preview === 'object' && !Array.isArray(preview) ? { ...preview } : {};
  for (const [source, target = source] of SAFE_WARNING_FIELD_ALIASES) {
    const value = safeWarningCorrelationValue(fields?.[source]);
    if (value !== undefined) out[target] = value;
  }
  return out;
}

function safeRuntimeWarningDetail(value) {
  const source = parseRuntimeLogJSONText(value);
  if (source && typeof source === 'object' && !Array.isArray(source)) {
    return compactRuntimeLogText(safeRuntimeWarningFields(source));
  }
  return safeRuntimeLogDetail(source);
}

function warningDetailText(entry) {
  if (entry?.runtimeKind === 'result' && entry?.fields && typeof entry.fields === 'object') {
    return safeRuntimeLogDetail(entry.fields);
  }
  if (entry?.fields && typeof entry.fields === 'object' && Object.keys(entry.fields).length > 0) {
    return safeRuntimeWarningDetail(entry.fields);
  }
  if (entry?.detail !== undefined && entry?.detail !== null && entry.detail !== '') {
    return safeRuntimeWarningDetail(entry.detail);
  }
  return safeRuntimeWarningDetail(entry?.fields);
}

function runtimeLogTimestamp(entry) {
  return firstRuntimeLogText(runtimeLogTimestampCandidates(entry));
}

function runtimeLogLabel(entry) {
  return firstRuntimeLogText(runtimeLogLabelCandidates(entry));
}

function parseSafeLogTimestamp(entry) {
  const ts = runtimeLogTimestamp(entry);
  if (!ts) return 0;
  const text = runtimeLogTextValue(ts).trim();
  const asNumber = Number(text);
  if (Number.isFinite(asNumber) && asNumber > 0) return asNumber;
  try {
    return requireTimestampMillis(text.replace(/(\.\d{3})\d+/g, '$1'), 'runtime log timestamp');
  } catch {
    return 0;
  }
}

function runtimeLogInlineLabel(entry) {
  const label = runtimeLogLabel(entry);
  if (entry?.runtimeKind === 'result') return label.split(' · ', 1)[0] || label;
  return label;
}

function runtimeLogArray(value) {
  return Array.isArray(value) ? value : [];
}

function runtimeLogEntries(warnings = [], results = []) {
  return [
    ...runtimeLogArray(warnings).map((entry) => ({ ...entry, runtimeKind: 'warning' })),
    ...runtimeLogArray(results).map((entry) => ({ ...entry, runtimeKind: 'result' })),
  ].sort((left, right) => parseSafeLogTimestamp(right) - parseSafeLogTimestamp(left));
}

export {
  runtimeLogEntries,
  runtimeLogInlineLabel,
  runtimeLogTimestamp,
  warningDetailText,
};
