import { describe, expect, it } from 'vitest';

import {
  validatePublicErrorV1,
  validateTurnRefV1,
  validateTurnTerminalV2,
} from './turnContractValidators.js';

const publicError = {
  code: 'PROVIDER_FAILED', title: 'Provider failed', message: 'Try again.',
  diagnosticId: 'diag-1', retryable: false, recoveryActions: ['copy_diagnostics'],
};

function terminal(outcome) {
  return {
    schemaVersion: 2, eventId: 'event-1', threadId: 'thread-1', turnId: 'turn-1',
    outcome, occurredAt: '2026-07-16T00:00:00Z',
  };
}

describe('turn contract validators', () => {
  it('rejects missing and unknown TurnRefV1 fields', () => {
    expect(() => validateTurnRefV1({ threadId: 'thread-1' })).toThrow('turnId is required');
    expect(() => validateTurnRefV1({ threadId: 'thread-1', turnId: 'turn-1', legacy: true })).toThrow('legacy is unknown');
  });

  it('rejects raw PublicError fields and unimplemented recovery actions', () => {
    expect(() => validatePublicErrorV1({ ...publicError, rawCause: 'provider stack' })).toThrow('rawCause is unknown');
    for (const action of ['retry', 'reconnect', 'restart_provider', 'reopen_thread', 'invented']) {
      expect(() => validatePublicErrorV1({ ...publicError, recoveryActions: [action] })).toThrow(`unsupported value ${action}`);
    }
  });

  it('enforces terminal outcome-dependent fields', () => {
    expect(() => validateTurnTerminalV2(terminal('success'))).not.toThrow();
    expect(() => validateTurnTerminalV2(terminal('failed'))).toThrow('publicError is required');
    expect(() => validateTurnTerminalV2({ ...terminal('failed'), publicError })).not.toThrow();
    expect(() => validateTurnTerminalV2({ ...terminal('interrupted'), terminationCause: 'user_request' })).toThrow('terminationRequestId is required');
    expect(() => validateTurnTerminalV2({ ...terminal('interrupted'), terminationCause: 'user_request', terminationRequestId: 'request-1' })).not.toThrow();
    expect(() => validateTurnTerminalV2({ ...terminal('cancelled'), terminationCause: 'provider', publicError, terminationRequestId: 'request-1' })).toThrow('forbidden contract shape');
    expect(() => validateTurnTerminalV2({ ...terminal('unknown') })).toThrow('unsupported value');
  });
});
