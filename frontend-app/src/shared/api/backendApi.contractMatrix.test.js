import { describe, expect, it } from 'vitest';
import { RPC_METHODS } from './backendApi.js';
import {
  RPC_CONTRACT_MATRIX,
  RPC_RESPONSE_BEHAVIORS,
  RPC_RISK_LEVELS,
} from './backendApi.contractMatrix.js';

describe('backend API contract matrix', () => {
  it('classifies every RPC method with risk and response behavior', () => {
    const methodValues = Object.values(RPC_METHODS);

    expect(RPC_CONTRACT_MATRIX).toHaveLength(methodValues.length);
    expect(new Set(RPC_CONTRACT_MATRIX.map((entry) => entry.method))).toEqual(new Set(methodValues));

    for (const entry of RPC_CONTRACT_MATRIX) {
      expect(Object.values(RPC_RISK_LEVELS)).toContain(entry.risk);
      expect(Object.values(RPC_RESPONSE_BEHAVIORS)).toContain(entry.responseBehavior);
      expect(entry.key).toBeTruthy();
      expect(entry.method).toBe(RPC_METHODS[entry.key]);
      expect(Array.isArray(entry.contractNotes)).toBe(true);
    }
  });

  it('defaults mutating and credential-affecting methods away from P2', () => {
    const mutating = RPC_CONTRACT_MATRIX.filter((entry) => (
      /(^|_)(WRITE|DELETE|SAVE|APPLY|DISPATCH|START|RUN_ONCE|SET_ACTIVE|SET|UPSERT|MERGE|IGNORE|CONSOLIDATE)(_|$)/.test(entry.key)
      || /(^|\/)(write|delete|save|apply|dispatch|start|runOnce|setActive|set|upsert|merge|ignore|consolidate)(\/|$|[A-Z])/.test(entry.method)
    ));

    expect(mutating.length).toBeGreaterThan(0);
    for (const entry of mutating) {
      expect(entry.risk).not.toBe(RPC_RISK_LEVELS.P2);
    }
  });

  it('marks known contract exceptions explicitly', () => {
    const byMethod = new Map(RPC_CONTRACT_MATRIX.map((entry) => [entry.method, entry]));

    expect(byMethod.get(RPC_METHODS.DASHBOARD_SHARED_FILES).contractNotes).toContain('params:{}-only');
    expect(byMethod.get(RPC_METHODS.THREAD_START).contractNotes).toContain('custom-decoder');
    expect(byMethod.get(RPC_METHODS.TURN_START).contractNotes).toContain('custom-decoder');
    expect(byMethod.get(RPC_METHODS.TURN_INTERRUPT).contractNotes).toContain('custom-decoder');
  });
});
