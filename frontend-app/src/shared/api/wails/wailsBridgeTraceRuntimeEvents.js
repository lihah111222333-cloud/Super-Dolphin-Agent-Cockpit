import { isSafeNumber, parse as parseLosslessJSON } from "lossless-json";
import {
  bridgeEventParseFailureEnvelope,
  optionalDiagnosticString,
  waitRuntime,
  writeBridgeLog,
} from "./wailsBridgeLogRuntime.js";

/** @typedef {Record<string, unknown>} TraceRecord */
/** @typedef {(event: unknown) => void} RuntimeEventListener */
/**
 * @typedef {{
 *   On?: (eventName: string, listener: RuntimeEventListener) => unknown,
 *   Off?: (eventName: string) => unknown,
 * }} RuntimeEventSource
 */
/** @typedef {{Events?: RuntimeEventSource}} RuntimeBridge */
/**
 * @typedef {{
 *   beforeCallback?: (event: unknown) => void,
 *   escalateCallbackError?: boolean | ((error: unknown, event: unknown) => boolean),
 *   onCallbackError?: (error: unknown, event: unknown) => void,
 * }} RuntimeEventCallbackOptions
 */
/**
 * @typedef {{
 *   callbackFailedLog?: string,
 *   subscribeFailedLog?: string,
 *   subscribeReadyLog?: string,
 *   subscribeUnavailableLog?: string,
 *   unsubscribeDoneLog?: string,
 * }} RuntimeEventLogOptions
 */
/** @typedef {RuntimeEventCallbackOptions & RuntimeEventLogOptions} RuntimeEventOptions */

/** @param {string} value @returns {number | string} */
function parseRuntimeEventNumber(value) {
  return isSafeNumber(value) ? Number(value) : value;
}

/** @param {unknown} rawText @param {string} eventName @returns {unknown} */
function parseRuntimeEventJSON(rawText, eventName) {
  let parsed;
  try {
    parsed = parseLosslessJSON(String(rawText), null, {
      parseNumber: parseRuntimeEventNumber,
    });
  } catch (error) {
    return bridgeEventParseFailureEnvelope(rawText, error, eventName);
  }
  return parsed;
}

function noopBridgeUnsubscribe() {
  return undefined;
}

/** @param {unknown} evt @returns {unknown} */
function normalizeRuntimeEventEnvelope(evt) {
  if (!evt || typeof evt !== "object") return {};
  const envelope = /** @type {TraceRecord} */ (evt);
  const hasWailsEnvelope =
    Object.prototype.hasOwnProperty.call(envelope, "name") &&
    Object.prototype.hasOwnProperty.call(envelope, "data");
  if (!hasWailsEnvelope) return evt;
  const inner = envelope.data;
  if (inner == null || inner === "") return {};
  if (typeof inner === "object") return inner;
  if (typeof inner === "string")
    return parseRuntimeEventJSON(
      inner,
      optionalDiagnosticString(envelope.name),
    );
  return { data: inner };
}

/** @param {string} eventName @param {(event: unknown) => unknown} callback @param {RuntimeEventOptions} options */
function subscribeRuntimeEvent(eventName, callback, options = {}) {
  let off = noopBridgeUnsubscribe;
  let cancelled = false;
  let readySettled = false;
  /** @type {(value: boolean) => void} */
  let resolveReady;
  /** @type {Promise<boolean>} */
  const ready = new Promise((resolve) => {
    resolveReady = resolve;
  });
  /** @param {boolean} value @returns {void} */
  const settleReady = (value) => {
    if (!readySettled) {
      readySettled = true;
      resolveReady(value === true);
    }
  };
  /** @param {unknown} runtime @param {unknown} unbind @returns {boolean} */
  const teardown = (runtime, unbind) => {
    try {
      if (typeof unbind === "function") {
        unbind();
        return true;
      }
      const runtimeRecord =
        runtime && typeof runtime === "object"
          ? /** @type {RuntimeBridge} */ (runtime)
          : {};
      const events =
        runtimeRecord.Events && typeof runtimeRecord.Events === "object"
          ? /** @type {RuntimeEventSource} */ (runtimeRecord.Events)
          : {};
      if (typeof events.Off === "function") {
        events.Off(eventName);
        return true;
      }
    } catch {
      /* ignore */
    }
    return false;
  };
  const unsubscribe = () => {
    cancelled = true;
    off();
  };
  /** @param {unknown} error @param {unknown} normalized @returns {boolean} */
  const shouldEscalateCallbackError = (error, normalized) =>
    typeof options.escalateCallbackError === "function"
      ? options.escalateCallbackError(error, normalized) === true
      : options.escalateCallbackError === true;
  /** @param {unknown} evt @returns {void} */
  const wrapped = (evt) => {
    /** @type {unknown} */
    const normalized = normalizeRuntimeEventEnvelope(evt);
    if (typeof options.beforeCallback === "function")
      options.beforeCallback(normalized);
    try {
      callback(normalized);
    } catch (error) {
      writeBridgeLog(
        "error",
        options.callbackFailedLog || "runtime.callback.failed",
        { error },
      );
      if (typeof options.onCallbackError === "function")
        options.onCallbackError(error, normalized);
      if (shouldEscalateCallbackError(error, normalized)) throw error;
    }
  };
  void waitRuntime()
    .then(
      /** @param {unknown} runtime */ (runtime) => {
        const runtimeRecord =
          runtime && typeof runtime === "object"
            ? /** @type {RuntimeBridge} */ (runtime)
            : {};
        const events =
          runtimeRecord.Events && typeof runtimeRecord.Events === "object"
            ? /** @type {RuntimeEventSource} */ (runtimeRecord.Events)
            : {};
        if (typeof events.On !== "function") {
          writeBridgeLog(
            "warn",
            options.subscribeUnavailableLog || "runtime.subscribe.unavailable",
            { eventName },
          );
          settleReady(false);
          return;
        }
        const unbind = events.On(eventName, wrapped);
        if (cancelled) {
          teardown(runtime, unbind);
          settleReady(false);
          return;
        }
        off = () => {
          cancelled = true;
          teardown(runtime, unbind);
        };
        settleReady(true);
      },
    )
    .catch(
      /** @param {unknown} error */ (error) => {
        writeBridgeLog(
          "error",
          options.subscribeFailedLog || "runtime.subscribe.failed",
          { eventName, error },
        );
        settleReady(false);
      },
    );
  return { ready, unsubscribe };
}

export {
  parseRuntimeEventNumber,
  parseRuntimeEventJSON,
  normalizeRuntimeEventEnvelope,
  subscribeRuntimeEvent,
};
