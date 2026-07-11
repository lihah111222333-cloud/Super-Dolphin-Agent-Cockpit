import { safeLogFields } from './safeLogFields.js';
import {
  normalizeFrontendBreadcrumbRouteId,
  normalizeFrontendBreadcrumbTrail,
} from './frontendBreadcrumbs.js';

const installedGlobalHandlers = new WeakMap();
const SystemDate = globalThis.Date;
const CRASH_CONTRACTS = new Map([
  ['app.render.crash', { contextCode: 'react.root', phase: 'render' }],
  ['app.window.error', { contextCode: 'window.error', phase: 'global' }],
  ['app.unhandled.rejection', { contextCode: 'promise.unhandled', phase: 'global' }],
]);
const ERROR_NAMES = new Set([
  'Error',
  'TypeError',
  'RangeError',
  'ReferenceError',
  'SyntaxError',
  'AggregateError',
  'DOMException',
]);
const ERROR_CODE_RE = /^[A-Z][A-Z0-9_.-]{0,63}$/;
const FNV_OFFSET_BASIS_64 = 0xcbf29ce484222325n;
const FNV_PRIME_64 = 0x100000001b3n;
const UINT64_MASK = 0xffffffffffffffffn;

function currentTimestampISO() {
  return new SystemDate().toISOString();
}

function assertPlainObject(value, label) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    throw new TypeError(`${label} must be an object`);
  }
}

function requiredText(value, label) {
  if (typeof value !== 'string' || !value.trim()) throw new TypeError(`${label} must be a non-empty string`);
  return value.trim();
}

function crashContext(actionCode, phase) {
  const normalized = requiredText(actionCode, 'frontend crash actionCode');
  const contract = CRASH_CONTRACTS.get(normalized);
  if (!contract) throw new TypeError('frontend crash actionCode is not allowed');
  if (requiredText(phase, 'frontend crash phase') !== contract.phase) {
    throw new TypeError('frontend crash phase does not match actionCode');
  }
  return { actionCode: normalized, contextCode: contract.contextCode, phase: contract.phase };
}

function normalizedError(error) {
  const errorName = ERROR_NAMES.has(error?.name) ? error.name : 'UnknownError';
  const errorCode = typeof error?.code === 'string' && ERROR_CODE_RE.test(error.code)
    ? error.code
    : 'UNCLASSIFIED';
  return { errorName, errorCode };
}

function fnv1a64(value) {
  let hash = FNV_OFFSET_BASIS_64;
  for (let index = 0; index < value.length; index += 1) {
    const byte = value.charCodeAt(index);
    if (byte > 0x7f) throw new TypeError('frontend crash fingerprint input must be ASCII');
    hash ^= BigInt(byte);
    hash = (hash * FNV_PRIME_64) & UINT64_MASK;
  }
  return hash.toString(16).padStart(16, '0');
}

export function createFrontendCrashReport(input) {
  assertPlainObject(input, 'frontend crash input');
  const { actionCode, contextCode, phase } = crashContext(input.actionCode, input.phase);
  const routeId = normalizeFrontendBreadcrumbRouteId(input.routeId);
  const timestamp = requiredText(input.timestamp, 'frontend crash timestamp');
  const { errorName, errorCode } = normalizedError(input.error);
  let breadcrumbs = input.breadcrumbs;
  if (breadcrumbs === undefined) breadcrumbs = [];
  const breadcrumbTrail = normalizeFrontendBreadcrumbTrail(breadcrumbs);
  const fingerprintTuple = JSON.stringify([
    actionCode,
    routeId,
    phase,
    errorName,
    errorCode,
    contextCode,
    breadcrumbTrail,
  ]);
  return safeLogFields({
    actionCode,
    routeId,
    phase,
    timestamp,
    errorName,
    errorCode,
    contextCode,
    fingerprint: `crash-v1-${fnv1a64(fingerprintTuple)}`,
    breadcrumbTrail,
  });
}

export async function reportFrontendCrash({ input, reporter, consoleRef = console }) {
  if (typeof reporter !== 'function') throw new TypeError('frontend crash reporter must be a function');
  if (!consoleRef || typeof consoleRef.error !== 'function') {
    throw new TypeError('frontend crash console.error must be a function');
  }
  const report = createFrontendCrashReport(input);
  try {
    await reporter(report);
    return true;
  }
  catch {
    consoleRef.error('[frontend-crash] reporter failed');
    return false;
  }
}

function routeIdValue(routeId) {
  return requiredText(typeof routeId === 'function' ? routeId() : routeId, 'frontend crash routeId');
}

function breadcrumbSnapshot(breadcrumbs) {
  if (breadcrumbs === undefined) return [];
  if (Array.isArray(breadcrumbs)) return breadcrumbs;
  if (breadcrumbs && typeof breadcrumbs.snapshot === 'function') return breadcrumbs.snapshot();
  throw new TypeError('frontend crash breadcrumbs must be an array or buffer');
}

function globalCrashInput(options, actionCode, phase, error) {
  return {
    actionCode,
    routeId: routeIdValue(options.routeId),
    phase,
    timestamp: currentTimestampISO(),
    breadcrumbs: breadcrumbSnapshot(options.breadcrumbs),
    error,
  };
}

export function installGlobalCrashHandlers(options) {
  assertPlainObject(options, 'frontend global crash options');
  const { windowRef, reporter, consoleRef = console } = options;
  if (!windowRef || typeof windowRef.addEventListener !== 'function' || typeof windowRef.removeEventListener !== 'function') {
    throw new TypeError('frontend global crash window must support event listeners');
  }
  if (typeof reporter !== 'function') throw new TypeError('frontend crash reporter must be a function');
  const existing = installedGlobalHandlers.get(windowRef);
  if (existing) return existing.cleanup;

  const onError = (event) => {
    if (event.defaultPrevented) return;
    void reportFrontendCrash({
      input: globalCrashInput(options, 'app.window.error', 'global', event.error),
      reporter,
      consoleRef,
    });
  };
  const onUnhandledRejection = (event) => {
    if (event.defaultPrevented) return;
    void reportFrontendCrash({
      input: globalCrashInput(options, 'app.unhandled.rejection', 'global', event.reason),
      reporter,
      consoleRef,
    });
  };
  let active = true;
  const cleanup = () => {
    if (!active) return;
    active = false;
    windowRef.removeEventListener('error', onError);
    windowRef.removeEventListener('unhandledrejection', onUnhandledRejection);
    installedGlobalHandlers.delete(windowRef);
  };

  windowRef.addEventListener('error', onError);
  windowRef.addEventListener('unhandledrejection', onUnhandledRejection);
  installedGlobalHandlers.set(windowRef, { cleanup });
  return cleanup;
}
