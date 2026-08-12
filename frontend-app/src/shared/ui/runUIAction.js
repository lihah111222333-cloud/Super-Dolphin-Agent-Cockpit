// @ts-check

import {
  recordFrontendHealth,
  recordFrontendHealthFailureState,
} from '../diagnostics/frontendHealthStore.js';
import {
  clearVisibleActionFailureIfCurrent,
  publishVisibleActionFailure,
  visibleActionFailureSnapshot,
} from './actionFailureSink.js';
import { diagnosticIdFactoryForError, publicErrorForAction, publicErrorForSink } from './publicError.js';

/**
 * @typedef {ReturnType<typeof publicErrorForAction>} PublicError
 * @typedef {{ actionId: string, publicError: PublicError }} HealthFailure
 * @typedef {{ actionId: string, publicError: PublicError, retry?: () => unknown }} VisibleFailure
 * @typedef {{
 *   diagnosticIdFactory?: () => string,
 *   healthSink?: (failure: HealthFailure) => unknown,
 *   onError?: (error: PublicError) => unknown,
 *   rejectFalse?: boolean,
 *   retryable?: boolean,
 *   supersedesActionIds?: readonly string[],
 *   visibleFailureSink?: (failure: VisibleFailure) => unknown,
 * }} RunUIActionOptions
 */

let reportingDiagnosticSequence = 0;
const reportedSharedPromiseFailures = new WeakMap();

/** @returns {string} */
function reportingDiagnosticIdFactory() {
  reportingDiagnosticSequence += 1;
  return `ui-action-reporting-${reportingDiagnosticSequence}`;
}

/**
 * The reporting failure state is the only terminal path. It never calls the
 * failing custom sink or persistence again, so reporting is finite.
 * @param {string} actionId
 * @param {PublicError} publicError
 */
function recordReportingFailure(actionId, publicError) {
  recordFrontendHealthFailureState({ actionId, publicError });
}

/**
 * @param {{ actionId: string, code?: string, diagnosticIdFactory?: () => string, retryable?: boolean }} options
 * @returns {PublicError}
 */
function createSafePublicError({ actionId, code, diagnosticIdFactory, retryable = false }) {
  try {
    return /** @type {PublicError} */ (code
      ? publicErrorForSink(code, diagnosticIdFactory)
      : publicErrorForAction(actionId, { diagnosticIdFactory, retryable }));
  } catch {
    const publicError = /** @type {PublicError} */ (code
      ? publicErrorForSink(code, reportingDiagnosticIdFactory)
      : publicErrorForAction(actionId, { diagnosticIdFactory: reportingDiagnosticIdFactory, retryable }));
    const factoryFailure = /** @type {PublicError} */ (
      publicErrorForSink('DIAGNOSTIC_ID_FACTORY_FAILED', reportingDiagnosticIdFactory)
    );
    recordReportingFailure('ui-action.diagnostic-id', factoryFailure);
    return publicError;
  }
}

/**
 * @param {string} actionId
 * @param {PublicError} publicError
 * @param {RunUIActionOptions} options
 */
function writeHealth(actionId, publicError, options) {
  const healthSink = options.healthSink ?? recordFrontendHealth;
  try {
    healthSink({ actionId, publicError });
  } catch {
    recordReportingFailure(actionId, publicError);
    const healthFailure = createSafePublicError({
      actionId: 'frontend-health.record',
      code: 'HEALTH_SINK_FAILED',
      diagnosticIdFactory: options.diagnosticIdFactory,
    });
    recordReportingFailure('frontend-health.record', healthFailure);
  }
}

/**
 * @param {{ action: () => unknown, actionId: string, cause?: unknown, options: RunUIActionOptions }} args
 */
function reportFailure({ action, actionId, cause, options }) {
  const publicError = createSafePublicError({
    actionId,
    diagnosticIdFactory: diagnosticIdFactoryForError(cause, options.diagnosticIdFactory),
    retryable: options.retryable,
  });
  writeHealth(actionId, publicError, options);
  const visibleFailureSink = options.visibleFailureSink ?? publishVisibleActionFailure;
  let retryStarted = false;
  const retry = options.retryable ? () => {
    if (retryStarted) return undefined;
    retryStarted = true;
    return executeAction(
      actionId,
      action,
      options,
    );
  } : undefined;
  try {
    visibleFailureSink({ actionId, publicError, retry });
  } catch {
    const visibleFailure = createSafePublicError({
      actionId: 'visible-action-failure.publish',
      code: 'VISIBLE_FAILURE_SINK_FAILED',
      diagnosticIdFactory: options.diagnosticIdFactory,
    });
    writeHealth('visible-action-failure.publish', visibleFailure, options);
  }
  if (options.onError) {
    try {
      options.onError(publicError);
    } catch {
      const onErrorFailure = createSafePublicError({
        actionId: 'ui-action.on-error',
        code: 'ON_ERROR_CALLBACK_FAILED',
        diagnosticIdFactory: options.diagnosticIdFactory,
      });
      writeHealth('ui-action.on-error', onErrorFailure, options);
    }
  }
}

/** @param {object} promiseResult @param {{ action: () => unknown, actionId: string, cause?: unknown, options: RunUIActionOptions }} args */
function reportSharedPromiseFailure(promiseResult, args) {
  const reportedActionIds = reportedSharedPromiseFailures.get(promiseResult) ?? new Set();
  if (reportedActionIds.has(args.actionId)) return;
  reportedActionIds.add(args.actionId);
  reportedSharedPromiseFailures.set(promiseResult, reportedActionIds);
  reportFailure(args);
}

/** @param {{ actionId: string, failureAtStart: VisibleFailure | null, options: RunUIActionOptions }} args */
function handleActionSuccess(args) {
  const resolvedActionIds = [args.actionId];
  if (args.options.supersedesActionIds) resolvedActionIds.push(...args.options.supersedesActionIds);
  clearVisibleActionFailureIfCurrent(
    args.failureAtStart,
    resolvedActionIds,
  );
}

/** @param {unknown} value @param {{ action: () => unknown, actionId: string, failureAtStart: VisibleFailure | null, options: RunUIActionOptions, promiseResult: object }} args */
function handleActionResolution(value, args) {
  if (args.options.rejectFalse && value === false) {
    reportSharedPromiseFailure(args.promiseResult, args);
    return;
  }
  handleActionSuccess(args);
}

/** @param {string} actionId @param {() => unknown} action @param {RunUIActionOptions} options */
function executeAction(actionId, action, options) {
  const failureAtStart = visibleActionFailureSnapshot();
  try {
    const result = action();
    if (result && typeof /** @type {{ then?: unknown }} */ (result).then === 'function') {
      const promiseResult = /** @type {object} */ (result);
      const args = { action, actionId, failureAtStart, options, promiseResult };
      void Promise.resolve(result).then(
        (value) => handleActionResolution(value, args),
        (cause) => reportSharedPromiseFailure(promiseResult, { ...args, cause }),
      );
    } else if (options.rejectFalse && result === false) {
      reportFailure({ action, actionId, options });
    } else {
      handleActionSuccess({ actionId, failureAtStart, options });
    }
    return result;
  } catch (cause) {
    reportFailure({ action, actionId, cause, options });
    return undefined;
  }
}

/**
 * @param {string} actionId
 * @param {() => unknown} action
 * @param {RunUIActionOptions} [options]
 */
export function runUIAction(actionId, action, options = {}) {
  if (typeof actionId !== 'string' || !actionId.trim()) throw new TypeError('runUIAction actionId is required');
  if (typeof action !== 'function') throw new TypeError('runUIAction action must be a function');
  if (!options || typeof options !== 'object' || Array.isArray(options)) {
    throw new TypeError('runUIAction options must be an object');
  }
  for (const name of ['diagnosticIdFactory', 'healthSink', 'onError', 'visibleFailureSink']) {
    if (options[/** @type {keyof RunUIActionOptions} */ (name)] !== undefined
      && typeof options[/** @type {keyof RunUIActionOptions} */ (name)] !== 'function') {
      throw new TypeError(`runUIAction ${name} must be a function`);
    }
  }
  for (const name of ['rejectFalse', 'retryable']) {
    if (options[/** @type {'rejectFalse' | 'retryable'} */ (name)] !== undefined
      && typeof options[/** @type {'rejectFalse' | 'retryable'} */ (name)] !== 'boolean') {
      throw new TypeError(`runUIAction ${name} must be a boolean`);
    }
  }
  if (options.supersedesActionIds !== undefined && (!Array.isArray(options.supersedesActionIds)
    || options.supersedesActionIds.some((id) => typeof id !== 'string' || !id.trim()))) {
    throw new TypeError('runUIAction supersedesActionIds must contain non-empty action ids');
  }
  return executeAction(actionId.trim(), action, options);
}

/**
 * Background work has no direct interaction to attach a visible notice to,
 * but it uses the same action identity, safe error, diagnostics and Health
 * path. The returned Promise remains observable to explicit callers.
 * @param {string} actionId
 * @param {() => unknown} action
 * @param {{ diagnosticIdFactory?: () => string, healthSink?: (failure: HealthFailure) => unknown }} [options]
 */
export function runBackgroundAction(actionId, action, options = {}) {
  if (typeof actionId !== 'string' || !actionId.trim()) throw new TypeError('runBackgroundAction actionId is required');
  if (typeof action !== 'function') throw new TypeError('runBackgroundAction action must be a function');
  if (!options || typeof options !== 'object' || Array.isArray(options)) throw new TypeError('runBackgroundAction options must be an object');
  for (const name of ['diagnosticIdFactory', 'healthSink']) {
    if (options[/** @type {'diagnosticIdFactory' | 'healthSink'} */ (name)] !== undefined
      && typeof options[/** @type {'diagnosticIdFactory' | 'healthSink'} */ (name)] !== 'function') {
      throw new TypeError(`runBackgroundAction ${name} must be a function`);
    }
  }
  const normalizedActionId = actionId.trim();
  /** @param {unknown} cause */
  const reportBackgroundFailure = (cause) => {
    const publicError = createSafePublicError({
      actionId: normalizedActionId,
      diagnosticIdFactory: diagnosticIdFactoryForError(cause, options.diagnosticIdFactory),
    });
    writeHealth(normalizedActionId, publicError, options);
  };
  try {
    const result = action();
    if (result && typeof /** @type {{ then?: unknown }} */ (result).then === 'function') {
      void Promise.resolve(result).catch(reportBackgroundFailure);
    }
    return result;
  } catch (cause) {
    reportBackgroundFailure(cause);
    return undefined;
  }
}
