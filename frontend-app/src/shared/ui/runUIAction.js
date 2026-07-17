import {
  recordEmergencyFrontendHealth,
  recordFrontendHealth,
  recordLastResortFrontendHealth,
  retainDiagnosticCause,
} from '../diagnostics/frontendHealthStore.js';
import { publishVisibleActionFailure } from './actionFailureSink.js';
import { publicErrorForAction, publicErrorForSink } from './publicError.js';

let emergencyDiagnosticSequence = 0;

function emergencyDiagnosticIdFactory() {
  emergencyDiagnosticSequence += 1;
  return `ui-action-emergency-${emergencyDiagnosticSequence}`;
}

/** @param {string} diagnosticId @param {unknown} cause */
function retainCause(diagnosticId, cause) {
  try {
    retainDiagnosticCause(diagnosticId, cause);
  } catch {
    // A Map write can only fail catastrophically; reporting must still terminate without exposing the cause.
  }
}

function lastResortRecord(failure) {
  try {
    recordLastResortFrontendHealth(failure);
  } catch {
    // The last-resort recorder is intentionally the terminal boundary.
  }
}

function emergencyPublicError(code) {
  return publicErrorForSink(code, emergencyDiagnosticIdFactory);
}

function recordEmergency({ actionId, cause, emergencyHealthSink, publicError }) {
  retainCause(publicError.diagnosticId, cause);
  const failure = { actionId, publicError };
  try {
    emergencyHealthSink(failure);
  } catch (emergencyCause) {
    lastResortRecord(failure);
    const emergencyFailure = emergencyPublicError('EMERGENCY_HEALTH_SINK_FAILED');
    retainCause(emergencyFailure.diagnosticId, emergencyCause);
    lastResortRecord({ actionId: 'frontend-health.emergency', publicError: emergencyFailure });
  }
}

function createPublicError({ actionId, code, diagnosticIdFactory, emergencyHealthSink, retryable = false }) {
  try {
    return code
      ? publicErrorForSink(code, diagnosticIdFactory)
      : publicErrorForAction(actionId, { diagnosticIdFactory, retryable });
  } catch (factoryCause) {
    const publicError = code
      ? publicErrorForSink(code, emergencyDiagnosticIdFactory)
      : publicErrorForAction(actionId, { diagnosticIdFactory: emergencyDiagnosticIdFactory, retryable });
    const factoryFailure = emergencyPublicError('DIAGNOSTIC_ID_FACTORY_FAILED');
    recordEmergency({
      actionId: 'ui-action.diagnostic-id',
      cause: factoryCause,
      emergencyHealthSink,
      publicError: factoryFailure,
    });
    return publicError;
  }
}

function writeHealth({ actionId, cause, context, publicError }) {
  retainCause(publicError.diagnosticId, cause);
  const failure = { actionId, publicError };
  try {
    context.healthSink(failure);
  } catch (healthCause) {
    recordEmergency({ ...failure, cause, emergencyHealthSink: context.emergencyHealthSink });
    const healthFailure = createPublicError({
      code: 'HEALTH_SINK_FAILED',
      diagnosticIdFactory: context.diagnosticIdFactory,
      emergencyHealthSink: context.emergencyHealthSink,
    });
    recordEmergency({
      actionId: 'frontend-health.record',
      cause: healthCause,
      emergencyHealthSink: context.emergencyHealthSink,
      publicError: healthFailure,
    });
  }
}

function reportFailureInternal({ action, actionId, cause, options }) {
  const context = {
    diagnosticIdFactory: options.diagnosticIdFactory,
    emergencyHealthSink: options.emergencyHealthSink === undefined ? recordEmergencyFrontendHealth : options.emergencyHealthSink,
    healthSink: options.healthSink === undefined ? recordFrontendHealth : options.healthSink,
  };
  const publicError = createPublicError({
    actionId,
    diagnosticIdFactory: context.diagnosticIdFactory,
    emergencyHealthSink: context.emergencyHealthSink,
    retryable: options.retryable,
  });
  writeHealth({ actionId, cause, context, publicError });
  const retry = options.retryable ? () => executeAction(actionId, action, options) : undefined;
  const visibleFailureSink = options.visibleFailureSink === undefined ? publishVisibleActionFailure : options.visibleFailureSink;
  try {
    visibleFailureSink({ actionId, publicError, retry });
  } catch (visibleCause) {
    const visibleFailure = createPublicError({
      code: 'VISIBLE_FAILURE_SINK_FAILED',
      diagnosticIdFactory: context.diagnosticIdFactory,
      emergencyHealthSink: context.emergencyHealthSink,
    });
    writeHealth({
      actionId: 'visible-action-failure.publish',
      cause: visibleCause,
      context,
      publicError: visibleFailure,
    });
  }
  if (typeof options.onError === 'function') {
    try {
      options.onError(publicError);
    } catch (onErrorCause) {
      const onErrorFailure = createPublicError({
        code: 'ON_ERROR_CALLBACK_FAILED',
        diagnosticIdFactory: context.diagnosticIdFactory,
        emergencyHealthSink: context.emergencyHealthSink,
      });
      writeHealth({ actionId: 'ui-action.on-error', cause: onErrorCause, context, publicError: onErrorFailure });
    }
  }
}

function reportFailure(args) {
  try {
    reportFailureInternal(args);
  } catch (reportingCause) {
    const reportingFailure = emergencyPublicError('UI_ACTION_REPORTING_FAILED');
    retainCause(reportingFailure.diagnosticId, reportingCause);
    lastResortRecord({ actionId: 'ui-action.reporting', publicError: reportingFailure });
  }
}

function executeAction(actionId, action, options) {
  try {
    const result = action();
    if (result && typeof result.then === 'function') {
      void Promise.resolve(result).catch((cause) => reportFailure({ action, actionId, cause, options }));
    }
    return result;
  } catch (cause) {
    reportFailure({ action, actionId, cause, options });
    return undefined;
  }
}

export function runUIAction(actionId, action, options = {}) {
  if (typeof actionId !== 'string' || !actionId.trim()) throw new TypeError('runUIAction actionId is required');
  if (typeof action !== 'function') throw new TypeError('runUIAction action must be a function');
  if (!options || typeof options !== 'object' || Array.isArray(options)) {
    throw new TypeError('runUIAction options must be an object');
  }
  for (const name of ['diagnosticIdFactory', 'emergencyHealthSink', 'healthSink', 'onError', 'visibleFailureSink']) {
    if (options[name] !== undefined && typeof options[name] !== 'function') {
      throw new TypeError(`runUIAction ${name} must be a function`);
    }
  }
  if (options.retryable !== undefined && typeof options.retryable !== 'boolean') {
    throw new TypeError('runUIAction retryable must be a boolean');
  }
  return executeAction(actionId.trim(), action, options);
}
