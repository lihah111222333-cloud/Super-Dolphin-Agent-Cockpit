import { describe, expect, it, vi } from 'vitest';

import { reportMainFrontendReadiness } from './mainReadiness.js';

describe('main frontend readiness retry', () => {
  it('retries one transient runtime failure and stops after success', async () => {
    const transientError = new Error('Wails runtime bridge not ready');
    const reportReadiness = vi.fn()
      .mockRejectedValueOnce(transientError)
      .mockResolvedValueOnce(7);
    const sleep = vi.fn().mockResolvedValue(undefined);

    await expect(reportMainFrontendReadiness({
      reportReadiness,
      sleep,
      now: () => 0,
      startupDeadlineMs: 1_000,
      retryDelayMs: 100,
    })).resolves.toBe(7);

    expect(reportReadiness).toHaveBeenCalledTimes(2);
    expect(sleep).toHaveBeenCalledOnce();
    expect(sleep).toHaveBeenCalledWith(100, undefined);
  });

  it('stops at the startup deadline and preserves the latest transport failure', async () => {
    let nowMs = 0;
    const latestError = new Error('transport disconnected');
    const reportReadiness = vi.fn().mockRejectedValue(latestError);
    const sleep = vi.fn(async (delayMs) => {
      nowMs += delayMs;
    });

    await expect(reportMainFrontendReadiness({
      reportReadiness,
      sleep,
      now: () => nowMs,
      startupDeadlineMs: 1_000,
      retryDelayMs: 400,
    })).rejects.toBe(latestError);

    expect(reportReadiness).toHaveBeenCalledTimes(3);
    expect(sleep.mock.calls.map(([delayMs]) => delayMs)).toEqual([400, 400, 200]);
  });

  it('bounds a hung transport attempt by the startup deadline', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(0);
    try {
      const reportReadiness = vi.fn(() => new Promise(() => {}));
      const readiness = reportMainFrontendReadiness({
        reportReadiness,
        now: Date.now,
        startupDeadlineMs: 1_000,
      });
      const rejected = expect(readiness).rejects.toThrow('frontend readiness startup deadline expired');

      await vi.advanceTimersByTimeAsync(1_000);
      await rejected;
      expect(reportReadiness).toHaveBeenCalledOnce();
    } finally {
      vi.useRealTimers();
    }
  });

  it('cancels an active retry sequence without scheduling another attempt', async () => {
    const controller = new AbortController();
    const abortError = new Error('desktop startup cancelled');
    const reportReadiness = vi.fn(() => new Promise(() => {}));
    const sleep = vi.fn();

    const readiness = reportMainFrontendReadiness({
      reportReadiness,
      sleep,
      signal: controller.signal,
      startupDeadlineMs: 1_000,
    });
    controller.abort(abortError);

    await expect(readiness).rejects.toBe(abortError);
    expect(reportReadiness).toHaveBeenCalledOnce();
    expect(sleep).not.toHaveBeenCalled();
  });

  it.each([
    new Error('frontend readiness probe response must contain only epoch'),
    new Error('frontend readiness commit epoch does not match probe epoch'),
    new Error('wails frontend readiness: epoch does not match current activation'),
  ])('fails fast without retry for schema or epoch error: %s', async (protocolError) => {
    const reportReadiness = vi.fn().mockRejectedValue(protocolError);
    const sleep = vi.fn();

    await expect(reportMainFrontendReadiness({
      reportReadiness,
      sleep,
      now: () => 0,
      startupDeadlineMs: 1_000,
      retryDelayMs: 100,
    })).rejects.toBe(protocolError);

    expect(reportReadiness).toHaveBeenCalledOnce();
    expect(sleep).not.toHaveBeenCalled();
  });
});
