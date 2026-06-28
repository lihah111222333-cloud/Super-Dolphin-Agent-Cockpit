// @ts-nocheck
import { describe, expect, it } from 'vitest';

import { isThreadActiveStatus, isThreadErrorStatus, normalizeStatus } from './services/status.js';

describe('status service', () => {
  it('normalizes known backend statuses', () => {
    expect(normalizeStatus(' RUNNING ')).toBe('running');
    expect(normalizeStatus('responding')).toBe('responding');
    expect(normalizeStatus('editing')).toBe('editing');
    expect(normalizeStatus('archived')).toBe('archived');
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

  it('classifies archived as terminal rather than active', () => {
    expect(isThreadActiveStatus('running')).toBe(true);
    expect(isThreadActiveStatus('thinking')).toBe(true);
    expect(isThreadActiveStatus('archived')).toBe(false);
    expect(isThreadActiveStatus('idle')).toBe(false);
    expect(isThreadActiveStatus('error')).toBe(false);
  });
});
