// @ts-check

const TRACE_DIAGNOSTIC_ID_PATTERN = /^[0-9a-f]{32}$/;
const correlations = new WeakMap();

/** @param {unknown} value @returns {value is object | Function} */
function correlatableError(value) {
  return Boolean(value) && (typeof value === 'object' || typeof value === 'function');
}

/**
 * Only the Wails bridge may register a correlation after it has created and
 * logged the trace context for this exact error object.
 * @param {unknown} error
 * @param {unknown} diagnosticId
 */
export function registerFrontendDiagnosticCorrelation(error, diagnosticId) {
  if (!correlatableError(error)) throw new TypeError('frontend diagnostic correlation error is required');
  if (typeof diagnosticId !== 'string' || !TRACE_DIAGNOSTIC_ID_PATTERN.test(diagnosticId)) {
    throw new TypeError('frontend diagnostic correlation trace ID is invalid');
  }
  correlations.set(error, diagnosticId);
}

/** @param {unknown} error @returns {string | undefined} */
export function frontendDiagnosticCorrelationForError(error) {
  return correlatableError(error) ? correlations.get(error) : undefined;
}
