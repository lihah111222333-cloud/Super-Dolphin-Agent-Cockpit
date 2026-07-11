import { safeLogFields } from './safeLogFields.js';

const BREADCRUMB_KEYS = new Set(['actionCode', 'routeId', 'phase', 'timestamp']);
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

function normalizeBreadcrumb(input, now) {
  assertPlainObject(input, 'frontend breadcrumb');
  for (const key of Object.keys(input)) {
    if (!BREADCRUMB_KEYS.has(key)) throw new TypeError(`frontend breadcrumb must not include ${key}`);
  }
  return safeLogFields({
    actionCode: assertNonEmptyString(input.actionCode, 'frontend breadcrumb actionCode'),
    routeId: assertNonEmptyString(input.routeId, 'frontend breadcrumb routeId'),
    phase: assertNonEmptyString(input.phase, 'frontend breadcrumb phase'),
    timestamp: assertNonEmptyString(input.timestamp ?? now(), 'frontend breadcrumb timestamp'),
  });
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
