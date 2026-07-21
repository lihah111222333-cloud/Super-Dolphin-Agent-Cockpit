/**
 * @typedef {Record<string, unknown> | unknown[] | null} DiagnosticPreviewJSON
 */

/** @param {unknown} value @returns {value is Record<string, unknown>} */
function isPlainPreviewObject(value) {
  if (!value || typeof value !== "object" || Array.isArray(value)) return false;
  const proto = Object.getPrototypeOf(value);
  return proto === Object.prototype || proto === null;
}

/** @param {unknown} value @returns {DiagnosticPreviewJSON} */
function assertDiagnosticPreviewJSONShape(value) {
  if (value === null || Array.isArray(value) || isPlainPreviewObject(value))
    return value;
  const error = new TypeError(
    "safe diagnostic preview JSON must decode to an object, array, or null",
  );
  error.name = "SafeDiagnosticPreviewJSONShapeError";
  throw error;
}

/**
 * @param {unknown} value
 * @param {string} label
 * @returns {DiagnosticPreviewJSON}
 */
export function parseStrictDiagnosticPreviewJSON(value, label) {
  if (typeof value !== "string") {
    throw new TypeError(`${label} JSON source must be a string`);
  }
  try {
    return assertDiagnosticPreviewJSONShape(JSON.parse(value));
  } catch (error) {
    if (
      error instanceof Error &&
      error.name === "SafeDiagnosticPreviewJSONShapeError"
    )
      throw error;
    const parseError = new Error(`${label} JSON parse failed`);
    parseError.name = "SafeDiagnosticPreviewJSONParseError";
    parseError.cause = error;
    throw parseError;
  }
}
