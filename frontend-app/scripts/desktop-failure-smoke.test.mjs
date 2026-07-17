import { describe, expect, it } from 'vitest';

import {
  DESKTOP_FAILURE_CASES,
  desktopFailureSmokeConfig,
  packageScriptIncludesFailureSmoke,
  resolveChromiumExecutable,
  validateDesktopFailureCases,
  validateDesktopFailureReport,
} from './desktop-failure-smoke.mjs';

describe('desktop failure smoke contract', () => {
  it('keeps both Go/Wails to real DOM failure cases runnable', () => {
    expect(validateDesktopFailureCases()).toBe(DESKTOP_FAILURE_CASES);
    expect(DESKTOP_FAILURE_CASES[1]).toEqual(expect.objectContaining({
      caseId: 'prompt-history-reject',
      status: 'runnable',
      fixtureContract: expect.objectContaining({
        preserve: ['draft', 'cursor'],
        visibleError: true,
        retryRecovery: true,
      }),
    }));
  });

  it('rejects missing, duplicate, stale, or non-executable case declarations', () => {
    expect(() => validateDesktopFailureCases(DESKTOP_FAILURE_CASES.slice(0, 1))).toThrow(/exact diff/);
    expect(() => validateDesktopFailureCases([DESKTOP_FAILURE_CASES[0], DESKTOP_FAILURE_CASES[0]])).toThrow(/exact diff/);
    expect(() => validateDesktopFailureCases([
      DESKTOP_FAILURE_CASES[0],
      { ...DESKTOP_FAILURE_CASES[1], caseId: 'stale-case' },
    ])).toThrow(/exact diff/);
    expect(() => validateDesktopFailureCases([
      DESKTOP_FAILURE_CASES[0],
      { ...DESKTOP_FAILURE_CASES[1], status: 'blocked' },
    ])).toThrow(/executable evidence/);
  });

  it('fails fast on missing, duplicate, stale, zero-count, or partial reports', () => {
    const valid = {
      caseIds: ['terminal-failed', 'prompt-history-reject'],
      testCount: 2,
      status: 'covered',
      blockedCases: [],
    };
    expect(validateDesktopFailureReport(valid)).toBe(valid);
    expect(() => validateDesktopFailureReport({ ...valid, caseIds: valid.caseIds.slice(1) })).toThrow(/exact diff/);
    expect(() => validateDesktopFailureReport({ ...valid, caseIds: ['terminal-failed', 'terminal-failed'] })).toThrow(/exact diff/);
    expect(() => validateDesktopFailureReport({ ...valid, caseIds: ['terminal-failed', 'stale'] })).toThrow(/exact diff/);
    expect(() => validateDesktopFailureReport({ ...valid, testCount: 0 })).toThrow(/testCount/);
    expect(() => validateDesktopFailureReport({ ...valid, status: 'partial', blockedCases: [{}] })).toThrow(/covered status/);
  });

  it('requires a real Chromium executable and the named package script', async () => {
    expect(resolveChromiumExecutable(
      { PLAYWRIGHT_CHROMIUM_EXECUTABLE: '/browser/chrome' },
      (candidate) => candidate === '/browser/chrome',
    )).toBe('/browser/chrome');
    expect(() => resolveChromiumExecutable({}, () => false)).toThrow(/required/);
    await expect(packageScriptIncludesFailureSmoke()).resolves.toBe(true);
  });

  it('builds isolated default ports and evidence path', () => {
    const config = desktopFailureSmokeConfig({
      PLAYWRIGHT_CHROMIUM_EXECUTABLE: '/browser/chrome',
    }, '/repo/app');
    expect(config).toEqual(expect.objectContaining({
      backendAddr: '127.0.0.1:4514',
      viteURL: 'http://127.0.0.1:5178',
      backendBinary: expect.stringMatching(/^\/repo\/app\/\.tmp\/desktop-failure-smoke\/failure-smoke-host-\d+$/u),
      reportPath: '/repo/app/.tmp/desktop-failure-smoke/report.json',
    }));
  });
});
