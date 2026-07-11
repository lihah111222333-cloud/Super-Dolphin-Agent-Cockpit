import { safeLogFields } from './safeLogFields.js';

const installedGlobalHandlers = new WeakMap();
const SystemDate = globalThis.Date;

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

function stableBreadcrumbs(value) {
  if (value === undefined) return [];
  if (!Array.isArray(value)) throw new TypeError('frontend crash breadcrumbs must be an array');
  return value.map((entry) => {
    assertPlainObject(entry, 'frontend crash breadcrumb');
    return safeLogFields({
      actionCode: requiredText(entry.actionCode, 'frontend crash breadcrumb actionCode'),
      routeId: requiredText(entry.routeId, 'frontend crash breadcrumb routeId'),
      phase: requiredText(entry.phase, 'frontend crash breadcrumb phase'),
      timestamp: requiredText(entry.timestamp, 'frontend crash breadcrumb timestamp'),
    });
  });
}

export function createFrontendCrashReport(input) {
  assertPlainObject(input, 'frontend crash input');
  return safeLogFields({
    actionCode: requiredText(input.actionCode, 'frontend crash actionCode'),
    routeId: requiredText(input.routeId, 'frontend crash routeId'),
    phase: requiredText(input.phase, 'frontend crash phase'),
    timestamp: requiredText(input.timestamp, 'frontend crash timestamp'),
    breadcrumbs: stableBreadcrumbs(input.breadcrumbs),
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
