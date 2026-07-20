import { RECOVERY_FAILURE_FIELDS, normalizeRecoveryFailure } from '../../shared/recovery/recoveryFailure.js';

const RECOVERY_MODE = "recovery";

const RECOVERY_METHOD_IDS = Object.freeze({
  state: 2428597088,
  check: 739820993,
  retry: 2511456687,
  restore: 91896983,
});

const RECOVERY_STATE_FIELDS = Object.freeze([
  'mode',
  'projection',
  'last_action',
  'actions',
  'failure',
]);

const RECOVERY_ACTION_FIELDS = Object.freeze([
  'check',
  'retry',
  'restore',
]);

const RECOVERY_PROJECTION_FIELDS = Object.freeze([
  'transaction_id',
  'attempt_id',
  'state',
  'lease_present',
  'lease_owner',
  'lease_generation',
  'candidate_sha256',
  'reason',
]);

function requireString(value, field) {
  if (typeof value !== "string")
    throw new TypeError(`Recovery field ${field} must be a string`);
  return value;
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
      reason: requireString(projection.reason, "projection.reason"),
    }),
  });
}

async function loadWailsRuntime() {
  const modulePath = "/wails/runtime.js";
  return import(/* @vite-ignore */ modulePath);
}

function createRecoveryClient(runtimeLoader = loadWailsRuntime) {
  async function invoke(action) {
    const methodID = RECOVERY_METHOD_IDS[action];
    if (!methodID)
      throw new Error(`Unsupported Recovery action ${String(action)}`);
    const runtime = await runtimeLoader();
    if (!runtime?.Call?.ByID)
      throw new Error("Recovery Wails runtime is unavailable");
    return normalizeRecoveryState(await runtime.Call.ByID(methodID));
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
  RECOVERY_FAILURE_FIELDS,
  RECOVERY_METHOD_IDS,
  RECOVERY_MODE,
  RECOVERY_PROJECTION_FIELDS,
  RECOVERY_STATE_FIELDS,
  createRecoveryClient,
  normalizeRecoveryState,
};
