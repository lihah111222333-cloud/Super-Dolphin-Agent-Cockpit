import { compactSafeDiagnosticPreview } from '../../../shared/api/safeDiagnosticPreview.js';

const RUNTIME_LOG_DETAIL_LIMIT = 1600;

function safeRuntimeLogDetail(value) {
  return compactSafeDiagnosticPreview(value, RUNTIME_LOG_DETAIL_LIMIT, { parseJsonStrings: true });
}

function warningDetailText(entry) {
  if (entry?.runtimeKind === 'result' && entry?.fields && typeof entry.fields === 'object') {
    return safeRuntimeLogDetail(entry.fields);
  }
  return entry?.detail || JSON.stringify(entry?.fields ?? {}, null, 2);
}

function runtimeLogTimestamp(entry) {
  return entry?.timestamp || entry?.time || entry?.ts || '';
}

function runtimeLogLabel(entry) {
  return entry?.message || entry?.event || entry?.method || '';
}

function parseSafeLogTimestamp(entry) {
  const ts = runtimeLogTimestamp(entry);
  if (!ts) return 0;
  const text = ts.toString().trim();
  const asNumber = Number(text);
  if (Number.isFinite(asNumber) && asNumber > 0) return asNumber;
  const parsed = Date.parse(text.replace(/(\.\d{3})\d+/g, '$1'));
  return Number.isFinite(parsed) && parsed > 0 ? parsed : 0;
}

function runtimeLogInlineLabel(entry) {
  const label = runtimeLogLabel(entry);
  if (entry?.runtimeKind === 'result') return label.split(' · ', 1)[0] || label;
  return label;
}

function runtimeLogEntries(warnings = [], results = []) {
  return [
    ...(warnings || []).map((entry) => ({ ...entry, runtimeKind: 'warning' })),
    ...(results || []).map((entry) => ({ ...entry, runtimeKind: 'result' })),
  ].sort((left, right) => parseSafeLogTimestamp(right) - parseSafeLogTimestamp(left));
}

export {
  runtimeLogEntries,
  runtimeLogInlineLabel,
  runtimeLogTimestamp,
  warningDetailText,
};
