import { describe, expect, it, vi } from 'vitest';

import { RECOVERY_METHOD_IDS, createRecoveryClient, normalizeRecoveryState } from './recoveryClient.js';

function recoveryPayload(overrides = {}) {
  return {
    mode: 'recovery',
    last_action: 'state',
    actions: { check: true, retry: true, restore: true },
    projection: {
      transaction_id: 'transaction-1',
      attempt_id: 'attempt-1',
      state: 'probation',
      lease_present: true,
      lease_owner: 'guard-1',
      lease_generation: 2,
      candidate_sha256: 'abc123',
      reason: 'health check failed',
    },
    ...overrides,
  };
}

describe('Recovery client', () => {
  it('rejects normal mode instead of reusing normal ready', () => {
    expect(() => normalizeRecoveryState(recoveryPayload({ mode: 'normal' })))
      .toThrow('Recovery mode is required');
  });

  it('fails fast on missing or unknown state fields', () => {
    const missing = recoveryPayload();
    delete missing.last_action;
    expect(() => normalizeRecoveryState(missing)).toThrow('Recovery state fields must exactly match');
    expect(() => normalizeRecoveryState(recoveryPayload({ future_field: true })))
      .toThrow('Recovery state fields must exactly match');
  });

  it('fails fast on missing or unknown action fields', () => {
    const missing = recoveryPayload();
    delete missing.actions.retry;
    expect(() => normalizeRecoveryState(missing)).toThrow('Recovery actions fields must exactly match');
    const unknown = recoveryPayload();
    unknown.actions.future_action = true;
    expect(() => normalizeRecoveryState(unknown)).toThrow('Recovery actions fields must exactly match');
  });

  it('fails fast on missing, unknown, or non-boolean projection fields', () => {
    const missing = recoveryPayload();
    delete missing.projection.lease_present;
    expect(() => normalizeRecoveryState(missing)).toThrow('fields must exactly match');
    const unknown = recoveryPayload();
    unknown.projection.future_field = 'unexpected';
    expect(() => normalizeRecoveryState(unknown)).toThrow('fields must exactly match');
    expect(() => normalizeRecoveryState(recoveryPayload({
      projection: { ...recoveryPayload().projection, lease_present: 'true' },
    }))).toThrow('projection.lease_present must be a boolean');
  });

  it('calls only the four exact Recovery action IDs', async () => {
    const byID = vi.fn().mockImplementation((methodID) => Promise.resolve(recoveryPayload({
      last_action: Object.entries(RECOVERY_METHOD_IDS).find(([, id]) => id === methodID)?.[0] ?? '',
    })));
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

  it('fails fast when the Recovery runtime bridge is unavailable', async () => {
    const client = createRecoveryClient(async () => ({}));
    await expect(client.state()).rejects.toThrow('Recovery Wails runtime is unavailable');
  });
});
