import { reportFrontendReadiness } from './shared/api/wails/wailsBridgeRpc.js';

const MAIN_FRONTEND_READINESS_STARTUP_DEADLINE_MS = 25_000;
const MAIN_FRONTEND_READINESS_RETRY_DELAY_MS = 250;

/** @param {AbortSignal | undefined} signal */
function abortReason(signal) {
  if (signal?.reason instanceof Error) return signal.reason;
  const error = new Error('frontend readiness retry aborted');
  error.name = 'AbortError';
  return error;
}

/** @param {unknown} error */
function isFrontendReadinessProtocolError(error) {
  const message = error instanceof Error ? error.message : String(error);
  if (message === 'frontend page load is required before readiness') return true;
  if (/^frontend readiness (?:probe|commit) response /i.test(message)) return true;
  if (/^frontend readiness commit epoch /i.test(message)) return true;
  return /^wails frontend readiness: (?:probe must|.*epoch|phase |decode request|request must)/i.test(message);
}

/** @param {number} delayMs @param {AbortSignal | undefined} signal */
function abortableSleep(delayMs, signal) {
  if (signal?.aborted) return Promise.reject(abortReason(signal));
  return new Promise((resolve, reject) => {
    const timeoutID = setTimeout(() => {
      signal?.removeEventListener('abort', onAbort);
      resolve(undefined);
    }, delayMs);
    const onAbort = () => {
      clearTimeout(timeoutID);
      signal?.removeEventListener('abort', onAbort);
      reject(abortReason(signal));
    };
    signal?.addEventListener('abort', onAbort, { once: true });
  });
}

/**
 * @param {() => Promise<number>} reportReadiness
 * @param {number} remainingMs
 * @param {AbortSignal | undefined} signal
 * @param {unknown} latestError
 */
function reportReadinessBeforeDeadline(reportReadiness, remainingMs, signal, latestError) {
  if (signal?.aborted) return Promise.reject(abortReason(signal));
  return new Promise((resolve, reject) => {
    const timeoutError = latestError ?? new Error('frontend readiness startup deadline expired');
    let settled = false;
    const finish = (callback, value) => {
      if (settled) return;
      settled = true;
      clearTimeout(timeoutID);
      signal?.removeEventListener('abort', onAbort);
      callback(value);
    };
    const timeoutID = setTimeout(() => finish(reject, timeoutError), remainingMs);
    const onAbort = () => finish(reject, abortReason(signal));
    signal?.addEventListener('abort', onAbort, { once: true });
    Promise.resolve()
      .then(reportReadiness)
      .then(
        (epoch) => finish(resolve, epoch),
        (error) => finish(reject, error),
      );
  });
}

/**
 * @param {{
 *   reportReadiness?: () => Promise<number>,
 *   sleep?: (delayMs: number, signal?: AbortSignal) => Promise<unknown>,
 *   now?: () => number,
 *   signal?: AbortSignal,
 *   startupDeadlineMs?: number,
 *   retryDelayMs?: number,
 * }} [options]
 * @returns {Promise<number>}
 */
async function reportMainFrontendReadiness({
  reportReadiness = reportFrontendReadiness,
  sleep = abortableSleep,
  now = Date.now,
  signal,
  startupDeadlineMs = MAIN_FRONTEND_READINESS_STARTUP_DEADLINE_MS,
  retryDelayMs = MAIN_FRONTEND_READINESS_RETRY_DELAY_MS,
} = {}) {
  if (!Number.isFinite(startupDeadlineMs) || startupDeadlineMs <= 0) {
    throw new TypeError('frontend readiness startup deadline must be positive');
  }
  if (!Number.isFinite(retryDelayMs) || retryDelayMs <= 0) {
    throw new TypeError('frontend readiness retry delay must be positive');
  }
  const deadlineAt = now() + startupDeadlineMs;
  /** @type {unknown} */
  let latestError;

  while (now() < deadlineAt) {
    if (signal?.aborted) throw abortReason(signal);
    try {
      return await reportReadinessBeforeDeadline(
        reportReadiness,
        deadlineAt - now(),
        signal,
        latestError,
      );
    } catch (error) {
      if (isFrontendReadinessProtocolError(error)) throw error;
      latestError = error;
    }

    if (signal?.aborted) throw abortReason(signal);
    const remainingMs = deadlineAt - now();
    if (remainingMs <= 0) break;
    await sleep(Math.min(retryDelayMs, remainingMs), signal);
  }

  if (latestError !== undefined) throw latestError;
  throw new Error('frontend readiness startup deadline expired before the first attempt');
}

export { reportMainFrontendReadiness };
