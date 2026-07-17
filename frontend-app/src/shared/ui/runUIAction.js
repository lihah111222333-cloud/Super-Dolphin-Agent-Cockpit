// @ts-check

import {
  recordFrontendHealth,
  recordFrontendHealthFailureState,
  retainDiagnosticCause,
} from '../diagnostics/frontendHealthStore.js';
import { clearVisibleActionFailure, publishVisibleActionFailure } from './actionFailureSink.js';
import { publicErrorForAction, publicErrorForSink } from './publicError.js';

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
 *   visibleFailureSink?: (failure: VisibleFailure) => unknown,
 * }} RunUIActionOptions
 */

let reportingDiagnosticSequence = 0;

/** @returns {string} */
function reportingDiagnosticIdFactory() {
  reportingDiagnosticSequence += 1;
  return `ui-action-reporting-${reportingDiagnosticSequence}`;
}

/** @param {string} diagnosticId @param {unknown} cause */
function retainCause(diagnosticId, cause) {
  retainDiagnosticCause(diagnosticId, cause);
}

/**
 * The reporting failure state is the only terminal path. It never calls the
 * failing custom sink or persistence again, so reporting is finite.
 * @param {string} actionId
 * @param {unknown} cause
 * @param {PublicError} publicError
 */
function recordReportingFailure(actionId, cause, publicError) {
  retainCause(publicError.diagnosticId, cause);
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
  } catch (factoryCause) {
    const publicError = /** @type {PublicError} */ (code
      ? publicErrorForSink(code, reportingDiagnosticIdFactory)
      : publicErrorForAction(actionId, { diagnosticIdFactory: reportingDiagnosticIdFactory, retryable }));
    const factoryFailure = /** @type {PublicError} */ (
      publicErrorForSink('DIAGNOSTIC_ID_FACTORY_FAILED', reportingDiagnosticIdFactory)
    );
    recordReportingFailure('ui-action.diagnostic-id', factoryCause, factoryFailure);
    return publicError;
  }
}

/**
 * @param {string} actionId
 * @param {unknown} cause
 * @param {PublicError} publicError
 * @param {RunUIActionOptions} options
 */
function writeHealth(actionId, cause, publicError, options) {
  retainCause(publicError.diagnosticId, cause);
  const healthSink = options.healthSink ?? recordFrontendHealth;
  try {
    healthSink({ actionId, publicError });
  } catch (healthCause) {
    recordReportingFailure(actionId, cause, publicError);
    const healthFailure = createSafePublicError({
      actionId: 'frontend-health.record',
      code: 'HEALTH_SINK_FAILED',
      diagnosticIdFactory: options.diagnosticIdFactory,
    });
    recordReportingFailure('frontend-health.record', healthCause, healthFailure);
  }
}

/**
 * @param {{ action: () => unknown, actionId: string, cause: unknown, options: RunUIActionOptions }} args
 */
function reportFailure({ action, actionId, cause, options }) {
  const publicError = createSafePublicError({
    actionId,
    diagnosticIdFactory: options.diagnosticIdFactory,
    retryable: options.retryable,
  });
  writeHealth(actionId, cause, publicError, options);
  const visibleFailureSink = options.visibleFailureSink ?? publishVisibleActionFailure;
  let retryStarted = false;
  const retry = options.retryable ? () => {
    if (retryStarted) return undefined;
    retryStarted = true;
    return executeAction(
      actionId,
      action,
      options,
      visibleFailureSink === publishVisibleActionFailure ? clearVisibleActionFailure : undefined,
    );
  } : undefined;
  try {
    visibleFailureSink({ actionId, publicError, retry });
  } catch (visibleCause) {
    const visibleFailure = createSafePublicError({
      actionId: 'visible-action-failure.publish',
      code: 'VISIBLE_FAILURE_SINK_FAILED',
      diagnosticIdFactory: options.diagnosticIdFactory,
    });
    writeHealth('visible-action-failure.publish', visibleCause, visibleFailure, options);
  }
  if (options.onError) {
    try {
      options.onError(publicError);
    } catch (onErrorCause) {
      const onErrorFailure = createSafePublicError({
        actionId: 'ui-action.on-error',
        code: 'ON_ERROR_CALLBACK_FAILED',
        diagnosticIdFactory: options.diagnosticIdFactory,
      });
      writeHealth('ui-action.on-error', onErrorCause, onErrorFailure, options);
    }
  }
}

/** @param {unknown} value @param {{ action: () => unknown, actionId: string, options: RunUIActionOptions }} args @param {(() => void) | undefined} onSuccess */
function handleActionResolution(value, args, onSuccess) {
  if (args.options.rejectFalse && value === false) {
    reportFailure({ ...args, cause: new TypeError(`${args.actionId} reported unsuccessful result`) });
    return;
  }
  if (onSuccess) onSuccess();
}

/** @param {string} actionId @param {() => unknown} action @param {RunUIActionOptions} options @param {(() => void) | undefined} [onSuccess] */
function executeAction(actionId, action, options, onSuccess) {
  try {
    const result = action();
    if (result && typeof /** @type {{ then?: unknown }} */ (result).then === 'function') {
      const args = { action, actionId, options };
      void Promise.resolve(result).then(
        (value) => handleActionResolution(value, args, onSuccess),
        (cause) => reportFailure({ ...args, cause }),
      );
    } else if (options.rejectFalse && result === false) {
      reportFailure({ action, actionId, cause: new TypeError(`${actionId} reported unsuccessful result`), options });
    } else if (onSuccess) {
      onSuccess();
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
  const reportBackgroundFailure = (/** @type {unknown} */ cause) => {
    const publicError = createSafePublicError({
      actionId: normalizedActionId,
      diagnosticIdFactory: options.diagnosticIdFactory,
    });
    writeHealth(normalizedActionId, cause, publicError, options);
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
