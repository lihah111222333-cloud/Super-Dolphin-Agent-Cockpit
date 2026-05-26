// @ts-nocheck
import { describe, expect, it } from 'vitest';

import { isThreadErrorStatus, normalizeStatus } from './services/status.js';

describe('status service', () => {
  it('normalizes known backend statuses', () => {
    expect(normalizeStatus(' RUNNING ')).toBe('running');
    expect(normalizeStatus('responding')).toBe('responding');
    expect(normalizeStatus('editing')).toBe('editing');
    expect(normalizeStatus('failed')).toBe('error');
    expect(normalizeStatus('rejected')).toBe('error');
  });

  it('falls back to idle for empty and unknown statuses', () => {
    expect(normalizeStatus('')).toBe('idle');
    expect(normalizeStatus(null)).toBe('idle');
    expect(normalizeStatus('made-up-status')).toBe('idle');
  });

  it('classifies terminal error statuses for send gating', () => {
    expect(isThreadErrorStatus('error')).toBe(true);
    expect(isThreadErrorStatus(' FAILED ')).toBe(true);
    expect(isThreadErrorStatus('rejected')).toBe(true);
    expect(isThreadErrorStatus('running')).toBe(false);
    expect(isThreadErrorStatus('')).toBe(false);
  });
});
