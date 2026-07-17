/**
 * @typedef {{ onError?: (error: unknown) => void, logger?: (message: string, error: unknown) => void }} UIActionOptions
 * @typedef {{ catch?: unknown }} Catchable
 * @typedef {PromiseLike<unknown> & { catch: (handler: (error: unknown) => void) => unknown }} RejectablePromise
 */

/**
 * @param {unknown | (() => unknown)} action
 * @param {UIActionOptions} [options]
 */
export function runUIAction(action, options = {}) {
  const { onError, logger = console.error } = options;
  /** @param {unknown} error */
  const reportError = (error) => {
    if (typeof logger === 'function') logger('[frontend-app] UI action failed', error);
    if (typeof onError === 'function') onError(error);
  };

  try {
    const result = typeof action === 'function' ? action() : action;
    if (result && typeof /** @type {Catchable} */ (result).catch === 'function') {
      void /** @type {RejectablePromise} */ (result).catch(reportError);
    }
  }
  catch (error) {
    reportError(error);
  }
}
