import { describe, expect, it, vi } from "vitest";

import {
  RECOVERY_METHOD_IDS,
  createRecoveryClient,
  normalizeRecoveryState,
} from "./recoveryClient.js";

const RECOVERY_DIAGNOSTIC_ID =
  "2222222222222222222222222222222222222222222222222222222222222222";
const RECOVERY_STARTUP_REASON =
  `RECOVERY_STARTUP_FAILED|${RECOVERY_DIAGNOSTIC_ID}`;

function recoveryPayload(overrides = {}) {
  return {
    mode: "recovery",
    last_action: "state",
    actions: { check: true, retry: true, restore: true },
    failure: { code: "", retryable: false, action: "", transaction_id: "" },
    projection: {
      transaction_id: "transaction-1",
      attempt_id: "attempt-1",
      state: "probation",
      lease_present: true,
      lease_owner: "guard-1",
      lease_generation: 2,
      candidate_sha256: "abc123",
      reason: RECOVERY_STARTUP_REASON,
    },
    ...overrides,
  };
}

const INVALID_LEASE_GENERATIONS = [
  ["string", "2"],
  ["null", null],
  ["boolean", true],
  ["float", 2.5],
  ["unsafe integer", Number.MAX_SAFE_INTEGER + 1],
];

describe("Recovery client", () => {
  it("rejects normal mode instead of reusing normal ready", () => {
    expect(() =>
      normalizeRecoveryState(recoveryPayload({ mode: "normal" })),
    ).toThrow("Recovery mode is required");
  });

  it("fails fast on missing or unknown state fields", () => {
    const missing = recoveryPayload();
    delete missing.last_action;
    expect(() => normalizeRecoveryState(missing)).toThrow(
      "Recovery state fields must exactly match",
    );
    expect(() =>
      normalizeRecoveryState(recoveryPayload({ future_field: true })),
    ).toThrow("Recovery state fields must exactly match");
  });

  it("fails fast on missing or unknown action fields", () => {
    const missing = recoveryPayload();
    delete missing.actions.retry;
    expect(() => normalizeRecoveryState(missing)).toThrow(
      "Recovery actions fields must exactly match",
    );
    const unknown = recoveryPayload();
    unknown.actions.future_action = true;
    expect(() => normalizeRecoveryState(unknown)).toThrow(
      "Recovery actions fields must exactly match",
    );
  });

  it("normalizes only the exact four-field failure contract", () => {
    const state = normalizeRecoveryState(
      recoveryPayload({
        failure: {
          code: "UPDATE_TRANSACTION_AMBIGUOUS",
          retryable: false,
          action: "preserve_state_export_diagnostics",
          transaction_id: "transaction-1",
        },
      }),
    );
    expect(state.failure).toEqual({
      code: "UPDATE_TRANSACTION_AMBIGUOUS",
      retryable: false,
      action: "preserve_state_export_diagnostics",
      transactionId: "transaction-1",
    });
    const extra = recoveryPayload();
    extra.failure.raw_error = "postgres://secret";
    expect(() => normalizeRecoveryState(extra)).toThrow(
      "Recovery failure fields must exactly match",
    );
  });

  it("rejects unknown structured recovery actions", () => {
    const payload = recoveryPayload({
      failure: {
        code: "UNKNOWN",
        retryable: false,
        action: "automatic_rollback",
        transaction_id: "",
      },
    });
    expect(() => normalizeRecoveryState(payload)).toThrow(
      "Recovery failure action is unsupported",
    );
  });

  it("rejects inconsistent empty and retryable failure metadata", () => {
    expect(() =>
      normalizeRecoveryState(
        recoveryPayload({
          failure: {
            code: "",
            retryable: false,
            action: "",
            transaction_id: "stale-transaction",
          },
        }),
      ),
    ).toThrow("Recovery empty failure fields are inconsistent");
    expect(() =>
      normalizeRecoveryState(
        recoveryPayload({
          failure: {
            code: "CAPACITY_EXHAUSTED",
            retryable: false,
            action: "wait_then_retry",
            transaction_id: "",
          },
        }),
      ),
    ).toThrow("Recovery failure retryability is inconsistent");
  });

  it("fails fast on missing, unknown, or non-boolean projection fields", () => {
    const missing = recoveryPayload();
    delete missing.projection.lease_present;
    expect(() => normalizeRecoveryState(missing)).toThrow(
      "fields must exactly match",
    );
    const unknown = recoveryPayload();
    unknown.projection.future_field = "unexpected";
    expect(() => normalizeRecoveryState(unknown)).toThrow(
      "fields must exactly match",
    );
    expect(() =>
      normalizeRecoveryState(
        recoveryPayload({
          projection: {
            ...recoveryPayload().projection,
            lease_present: "true",
          },
        }),
      ),
    ).toThrow("projection.lease_present must be a boolean");
  });

  it.each(INVALID_LEASE_GENERATIONS)(
    "fails fast on a %s projection lease generation",
    (_label, leaseGeneration) => {
      const payload = recoveryPayload();
      payload.projection.lease_generation = leaseGeneration;

      expect(() => normalizeRecoveryState(payload)).toThrow(
        "projection.lease_generation must be a non-negative integer",
      );
    },
  );

  it.each(INVALID_LEASE_GENERATIONS)(
    "normalizes the runtime Call.ByID result and rejects a %s lease generation",
    async (_label, leaseGeneration) => {
      const payload = recoveryPayload();
      payload.projection.lease_generation = leaseGeneration;
      const byID = vi.fn().mockResolvedValue(payload);
      const client = createRecoveryClient(async () => ({
        Call: { ByID: byID },
      }));

      await expect(client.state()).rejects.toThrow(
        "projection.lease_generation must be a non-negative integer",
      );
      expect(byID).toHaveBeenCalledWith(RECOVERY_METHOD_IDS.state);
    },
  );

  it("calls only the four exact Recovery action IDs", async () => {
    const byID = vi.fn().mockImplementation((methodID) =>
      Promise.resolve(
        recoveryPayload({
          last_action:
            Object.entries(RECOVERY_METHOD_IDS).find(
              ([, id]) => id === methodID,
            )?.[0] ?? "",
        }),
      ),
    );
    const client = createRecoveryClient(async () => ({ Call: { ByID: byID } }));

    await client.state();
    await client.check();
    await client.retry();
    await client.restore();

    expect(byID.mock.calls.map(([methodID]) => methodID)).toEqual([
      RECOVERY_METHOD_IDS.state,
      RECOVERY_METHOD_IDS.check,
      RECOVERY_METHOD_IDS.retry,
      RECOVERY_METHOD_IDS.restore,
    ]);
  });

  it("fails fast when the Recovery runtime bridge is unavailable", async () => {
    const client = createRecoveryClient(async () => ({}));
    await expect(client.state()).rejects.toThrow(
      "Recovery action could not be completed safely",
    );
  });
});
