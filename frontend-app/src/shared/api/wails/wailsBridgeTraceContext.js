/** @typedef {Record<string, unknown>} TraceRecord */

function resolveClientMeta() {
  const clientKind =
    typeof window !== "undefined" &&
    /** @type {Window & { __WAILS_SHIM_DEBUG__?: boolean }} */ (window)
      .__WAILS_SHIM_DEBUG__ === true
      ? "web-debug-shim"
      : "desktop-wails";
  const clientRoute =
    typeof window !== "undefined" && window.location
      ? (window.location.pathname || "/").toString()
      : "";
  return { clientKind, clientRoute };
}

/** @param {number} byteLength @returns {string} */
function randomHex(byteLength) {
  const cryptoSource = globalThis.crypto;
  if (!cryptoSource || typeof cryptoSource.getRandomValues !== "function") {
    throw new Error(
      "secure random source is required for Wails RPC trace context",
    );
  }
  const bytes = new Uint8Array(byteLength);
  while (true) {
    cryptoSource.getRandomValues(bytes);
    const value = Array.from(bytes, (byte) =>
      byte.toString(16).padStart(2, "0"),
    ).join("");
    if (!/^0+$/.test(value)) return value;
  }
}

function createTraceContext() {
  const traceId = randomHex(16);
  const spanId = randomHex(8);
  return {
    traceId,
    spanId,
    traceparent: `00-${traceId}-${spanId}-01`,
  };
}

function currentMonotonicMS() {
  if (
    typeof performance === "undefined" ||
    typeof performance.now !== "function"
  ) {
    const error = new Error("bridge monotonic clock is unavailable");
    error.name = "BridgeClockUnavailableError";
    throw error;
  }
  const value = performance.now();
  if (!Number.isFinite(value)) {
    const error = new Error("bridge clock returned an invalid timestamp");
    error.name = "BridgeClockUnavailableError";
    throw error;
  }
  return value;
}

/** @param {number} start @returns {number} */
function elapsedMS(start) {
  if (!Number.isFinite(start)) {
    const error = new Error("bridge start timestamp is invalid");
    error.name = "BridgeClockUnavailableError";
    throw error;
  }
  return Math.max(0, Math.round(currentMonotonicMS() - start));
}

/** @param {DateConstructor} clock @returns {string} */
function createFrontendTraceTimestamp(clock = Date) {
  if (!clock || typeof clock !== "function") {
    const error = new Error("frontend trace wall clock is unavailable");
    error.name = "BridgeClockUnavailableError";
    throw error;
  }
  return new clock().toISOString();
}

export {
  resolveClientMeta,
  createTraceContext,
  currentMonotonicMS,
  elapsedMS,
  createFrontendTraceTimestamp,
};
