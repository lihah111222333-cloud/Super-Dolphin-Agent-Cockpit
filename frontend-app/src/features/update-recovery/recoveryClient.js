import {
  RECOVERY_FAILURE_FIELDS,
  normalizeRecoveryFailure,
} from "../../shared/recovery/recoveryFailure.js";
import { loadWailsRuntime } from "../../shared/api/wailsBridge.js";

const RECOVERY_MODE = "recovery";

const RECOVERY_WIRE_FAILURE_PATTERN = /^(RECOVERY_[A-Z_]+)\|([a-f0-9]{64})$/;
const RECOVERY_DIAGNOSTIC_ID_PATTERN = /^[a-f0-9]{64}$/;
const RECOVERY_FALLBACK_DIAGNOSTIC_ID = "0".repeat(64);

const RECOVERY_PUBLIC_ERRORS = Object.freeze({
  RECOVERY_STARTUP_FAILED: Object.freeze({
    code: "RECOVERY_STARTUP_FAILED",
    title: "Recovery mode started",
    message:
      "Recovery mode started because the previous startup did not complete.",
  }),
  RECOVERY_STATE_FAILED: Object.freeze({
    code: "RECOVERY_STATE_FAILED",
    title: "Recovery state unavailable",
    message: "Recovery state could not be loaded. Please restart Recovery.",
  }),
  RECOVERY_CHECK_FAILED: Object.freeze({
    code: "RECOVERY_CHECK_FAILED",
    title: "Recovery check failed",
    message:
      "Recovery check could not be completed. You can retry or restore the previous release.",
  }),
  RECOVERY_RETRY_FAILED: Object.freeze({
    code: "RECOVERY_RETRY_FAILED",
    title: "Recovery retry failed",
    message:
      "Recovery retry could not be completed. You can retry or restore the previous release.",
  }),
  RECOVERY_RESTORE_FAILED: Object.freeze({
    code: "RECOVERY_RESTORE_FAILED",
    title: "Recovery restore failed",
    message:
      "Recovery restore could not be completed. Review diagnostics before trying again.",
  }),
  RECOVERY_UNKNOWN_FAILURE: Object.freeze({
    code: "RECOVERY_UNKNOWN_FAILURE",
    title: "Recovery action failed",
    message:
      "Recovery action could not be completed safely. Review the diagnostic ID before trying again.",
  }),
});

const RECOVERY_UNKNOWN_FAILURE =
  RECOVERY_PUBLIC_ERRORS.RECOVERY_UNKNOWN_FAILURE;

class RecoveryPublicError extends Error {
  constructor(code, diagnosticId) {
    const value = typeof code === "string" ? code : "";
    const copy = Object.hasOwn(RECOVERY_PUBLIC_ERRORS, value)
      ? RECOVERY_PUBLIC_ERRORS[value]
      : RECOVERY_UNKNOWN_FAILURE;
    super(copy.message);
    this.name = "RecoveryPublicError";
    this.code = copy.code;
    this.title = copy.title;
    this.publicMessage = copy.message;
    this.diagnosticId = recoveryDiagnosticId(diagnosticId);
  }
}

function recoveryDiagnosticId(value) {
  return typeof value === "string" && RECOVERY_DIAGNOSTIC_ID_PATTERN.test(value)
    ? value
    : RECOVERY_FALLBACK_DIAGNOSTIC_ID;
}

function recoveryPublicErrorForCode(code, diagnosticId) {
  return new RecoveryPublicError(code, diagnosticId);
}

function recoveryPublicErrorForFailure(error) {
  if (error instanceof RecoveryPublicError) return error;
  const message =
    error instanceof Error && typeof error.message === "string"
      ? error.message
      : "";
  const match = RECOVERY_WIRE_FAILURE_PATTERN.exec(message);
  return recoveryPublicErrorForCode(
    match ? match[1] : RECOVERY_UNKNOWN_FAILURE.code,
    match ? match[2] : RECOVERY_FALLBACK_DIAGNOSTIC_ID,
  );
}

function recoveryPublicReasonForDisplay(reason) {
  if (reason == null) return null;
  return reason instanceof RecoveryPublicError
    ? reason
    : recoveryPublicErrorForCode(
        RECOVERY_UNKNOWN_FAILURE.code,
        RECOVERY_FALLBACK_DIAGNOSTIC_ID,
      );
}

const RECOVERY_METHOD_IDS = Object.freeze({
  state: 2428597088,
  check: 739820993,
  retry: 2511456687,
  restore: 91896983,
});

const RECOVERY_STATE_FIELDS = Object.freeze([
  "mode",
  "projection",
  "last_action",
  "actions",
  "failure",
]);

const RECOVERY_ACTION_FIELDS = Object.freeze(["check", "retry", "restore"]);

const RECOVERY_PROJECTION_FIELDS = Object.freeze([
  "transaction_id",
  "attempt_id",
  "state",
  "lease_present",
  "lease_owner",
  "lease_generation",
  "candidate_sha256",
  "reason",
]);

function requireString(value, field) {
  if (typeof value !== "string")
    throw new TypeError(`Recovery field ${field} must be a string`);
  return value;
}

function normalizeRecoveryReason(value) {
  const wireValue = requireString(value, "projection.reason");
  if (wireValue === "") return null;
  const match = RECOVERY_WIRE_FAILURE_PATTERN.exec(wireValue);
  if (!match || match[1] !== "RECOVERY_STARTUP_FAILED") {
    throw recoveryPublicErrorForCode(
      RECOVERY_UNKNOWN_FAILURE.code,
      RECOVERY_FALLBACK_DIAGNOSTIC_ID,
    );
  }
  return recoveryPublicErrorForCode(match[1], match[2]);
}

function requireBoolean(value, field) {
  if (typeof value !== "boolean")
    throw new TypeError(`Recovery field ${field} must be a boolean`);
  return value;
}

function requireExactFields(value, fields, owner) {
  const compareFields = (left, right) => left.localeCompare(right);
  const actual = Object.keys(value).sort(compareFields);
  const expected = [...fields].sort(compareFields);
  if (
    actual.length !== expected.length ||
    actual.some((field, index) => field !== expected[index])
  ) {
    throw new TypeError(
      `Recovery ${owner} fields must exactly match ${expected.join(",")}`,
    );
  }
}

function normalizeRecoveryState(value) {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    throw new TypeError("Recovery state must be an object");
  }
  requireExactFields(value, RECOVERY_STATE_FIELDS, "state");
  if (value.mode !== RECOVERY_MODE) {
    throw new Error(
      `Recovery mode is required; received ${String(value.mode)}`,
    );
  }
  const projection = value.projection;
  if (
    !projection ||
    typeof projection !== "object" ||
    Array.isArray(projection)
  ) {
    throw new TypeError("Recovery projection must be an object");
  }
  requireExactFields(projection, RECOVERY_PROJECTION_FIELDS, "projection");
  const leaseGeneration = projection.lease_generation;
  if (
    typeof leaseGeneration !== "number" ||
    !Number.isSafeInteger(leaseGeneration) ||
    leaseGeneration < 0
  ) {
    throw new TypeError(
      "Recovery field projection.lease_generation must be a non-negative integer",
    );
  }
  const actions = value.actions;
  if (!actions || typeof actions !== "object" || Array.isArray(actions)) {
    throw new TypeError("Recovery actions must be an object");
  }
  requireExactFields(actions, RECOVERY_ACTION_FIELDS, "actions");
  return Object.freeze({
    mode: RECOVERY_MODE,
    lastAction: requireString(value.last_action, "last_action"),
    failure: normalizeRecoveryFailure(value.failure),
    actions: Object.freeze({
      check: requireBoolean(actions.check, "actions.check"),
      retry: requireBoolean(actions.retry, "actions.retry"),
      restore: requireBoolean(actions.restore, "actions.restore"),
    }),
    projection: Object.freeze({
      transactionId: requireString(
        projection.transaction_id,
        "projection.transaction_id",
      ),
      attemptId: requireString(projection.attempt_id, "projection.attempt_id"),
      state: requireString(projection.state, "projection.state"),
      leasePresent: requireBoolean(
        projection.lease_present,
        "projection.lease_present",
      ),
      leaseOwner: requireString(
        projection.lease_owner,
        "projection.lease_owner",
      ),
      leaseGeneration,
      candidateSHA256: requireString(
        projection.candidate_sha256,
        "projection.candidate_sha256",
      ),
      reason: normalizeRecoveryReason(projection.reason),
    }),
  });
}

function createRecoveryClient(runtimeLoader = loadWailsRuntime) {
  async function invoke(action) {
    const methodID = RECOVERY_METHOD_IDS[action];
    if (!methodID)
      throw new Error(`Unsupported Recovery action ${String(action)}`);
    let result;
    try {
      const runtime = await runtimeLoader();
      if (!runtime?.Call?.ByID)
        throw new Error("Recovery Wails runtime is unavailable");
      result = await runtime.Call.ByID(methodID);
    } catch (error) {
      throw recoveryPublicErrorForFailure(error);
    }
    return normalizeRecoveryState(result);
  }
  return Object.freeze({
    state: () => invoke("state"),
    check: () => invoke("check"),
    retry: () => invoke("retry"),
    restore: () => invoke("restore"),
  });
}

export {
  RECOVERY_ACTION_FIELDS,
  RECOVERY_FALLBACK_DIAGNOSTIC_ID,
  RECOVERY_FAILURE_FIELDS,
  RECOVERY_METHOD_IDS,
  RECOVERY_MODE,
  RECOVERY_PROJECTION_FIELDS,
  RECOVERY_STATE_FIELDS,
  RecoveryPublicError,
  createRecoveryClient,
  normalizeRecoveryReason,
  normalizeRecoveryState,
  recoveryPublicErrorForFailure,
  recoveryPublicReasonForDisplay,
};
