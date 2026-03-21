// @ts-nocheck
import { describe, expect, it } from 'vitest';

import { normalizeStatus } from './services/status.js';

describe('status service', () => {
  it('normalizes known backend statuses', () => {
    expect(normalizeStatus(' RUNNING ')).toBe('running');
    expect(normalizeStatus('responding')).toBe('responding');
    expect(normalizeStatus('editing')).toBe('editing');
  });

  it('falls back to idle for empty and unknown statuses', () => {
    expect(normalizeStatus('')).toBe('idle');
    expect(normalizeStatus(null)).toBe('idle');
    expect(normalizeStatus('made-up-status')).toBe('idle');
  });
});
