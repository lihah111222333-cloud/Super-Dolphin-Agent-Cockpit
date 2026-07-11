import { safeLogFields } from './safeLogFields.js';

const BREADCRUMB_KEYS = new Set(['actionCode', 'routeId', 'phase', 'timestamp']);
const BREADCRUMB_ACTION_CODES = new Set(['app.bootstrap', 'app.navigation', 'approval.submit']);
const BREADCRUMB_ROUTE_IDS = new Set([
  'app', 'chat', 'prompts', 'workflows', 'skills', 'memory', 'observability', 'files', 'settings',
]);
const BREADCRUMB_PHASES = new Set(['start', 'complete', 'success', 'timeout', 'failure']);
const BREADCRUMB_TRAIL_LIMIT = 160;
const SystemDate = globalThis.Date;

function currentTimestampISO() {
  return new SystemDate().toISOString();
}

function assertPlainObject(value, label) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    throw new TypeError(`${label} must be an object`);
  }
}

function assertNonEmptyString(value, label) {
  if (typeof value !== 'string' || !value.trim()) {
    throw new TypeError(`${label} must be a non-empty string`);
  }
  return value.trim();
}

function assertAllowedValue(value, allowed, label) {
  const normalized = assertNonEmptyString(value, label);
  if (!allowed.has(normalized)) throw new TypeError(`${label} is not allowed`);
  return normalized;
}

export function normalizeFrontendBreadcrumbRouteId(value) {
  return assertAllowedValue(value, BREADCRUMB_ROUTE_IDS, 'frontend breadcrumb routeId');
}

function normalizeBreadcrumb(input, now) {
  assertPlainObject(input, 'frontend breadcrumb');
  for (const key of Object.keys(input)) {
    if (!BREADCRUMB_KEYS.has(key)) throw new TypeError(`frontend breadcrumb must not include ${key}`);
  }
  return safeLogFields({
    actionCode: assertAllowedValue(input.actionCode, BREADCRUMB_ACTION_CODES, 'frontend breadcrumb actionCode'),
    routeId: normalizeFrontendBreadcrumbRouteId(input.routeId),
    phase: assertAllowedValue(input.phase, BREADCRUMB_PHASES, 'frontend breadcrumb phase'),
    timestamp: assertNonEmptyString(input.timestamp ?? now(), 'frontend breadcrumb timestamp'),
  });
}

export function normalizeFrontendBreadcrumbTrail(value, limit = BREADCRUMB_TRAIL_LIMIT) {
  if (!Array.isArray(value)) throw new TypeError('frontend crash breadcrumbs must be an array');
  if (!Number.isInteger(limit) || limit <= 0) {
    throw new TypeError('frontend breadcrumb trail limit must be a positive integer');
  }
  const entries = value.map((input) => {
    const breadcrumb = normalizeBreadcrumb(input, () => {
      throw new TypeError('frontend crash breadcrumb timestamp must be a non-empty string');
    });
    const entry = `${breadcrumb.actionCode}:${breadcrumb.routeId}:${breadcrumb.phase}`;
    if (entry.length > limit) throw new TypeError('frontend crash breadcrumb entry exceeds trail limit');
    return entry;
  });
  const selected = [];
  let length = 0;
  for (let index = entries.length - 1; index >= 0; index -= 1) {
    const separatorLength = selected.length === 0 ? 0 : 1;
    if (length + separatorLength + entries[index].length > limit) break;
    selected.push(entries[index]);
    length += separatorLength + entries[index].length;
  }
  return selected.reverse().join('>');
}

export function createFrontendBreadcrumbBuffer(options = {}) {
  assertPlainObject(options, 'frontend breadcrumb options');
  const capacity = options.capacity ?? 20;
  const now = options.now === undefined ? currentTimestampISO : options.now;
  if (!Number.isInteger(capacity) || capacity <= 0) {
    throw new TypeError('frontend breadcrumb capacity must be a positive integer');
  }
  if (typeof now !== 'function') throw new TypeError('frontend breadcrumb now must be a function');

  const entries = [];
  return Object.freeze({
    record(input) {
      const entry = Object.freeze(normalizeBreadcrumb(input, now));
      entries.push(entry);
      if (entries.length > capacity) entries.splice(0, entries.length - capacity);
      return entry;
    },
    snapshot() {
      return entries.map((entry) => Object.freeze(safeLogFields(entry)));
    },
  });
}
