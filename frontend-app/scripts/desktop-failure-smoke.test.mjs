import { describe, expect, it } from 'vitest';

import {
  DESKTOP_FAILURE_CASES,
  desktopFailureSmokeConfig,
  packageScriptIncludesFailureSmoke,
  resolveChromiumExecutable,
  validateDesktopFailureCases,
  validateDesktopFailureReport,
} from './desktop-failure-smoke.mjs';
import {
  commandFailureMessage,
  DESKTOP_FAILURE_REPORT_REQUIREMENTS,
  DESKTOP_FAILURE_SMOKE_DEFAULT_TIMEOUT_MS,
  DESKTOP_FAILURE_SMOKE_COMMAND,
  DESKTOP_FAILURE_SOURCE_PATHS,
  mergeDebugNamespace,
  resolveDesktopFailureSmokeTimeout,
} from './desktop-failure-contract.mjs';

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
    const command = DESKTOP_FAILURE_SMOKE_COMMAND;
    const caseEvidence = (caseId, hops, domAssertions) => ({
      caseId, result: 'GREEN', command,
      hops,
      domAssertions,
      secretAssertions: ['dom-does-not-contain-raw-provider-secret', 'report-does-not-contain-raw-provider-secret'],
      execution: { status: 'passed', durationMs: 1 },
    });
    const valid = {
      schemaVersion: 2,
      caseIds: ['terminal-failed', 'prompt-history-reject'],
      testCount: 2,
      status: 'covered',
      blockedCases: [],
      sourceHashes: Object.fromEntries(DESKTOP_FAILURE_SOURCE_PATHS.map((sourcePath) => [sourcePath, 'a'.repeat(64)])),
      execution: {
        goBuild: { argv: ['go', 'build'], cwd: '.', exitCode: 0, signal: null, outputSha256: 'a'.repeat(64) },
        playwright: { argv: ['playwright', 'test'], cwd: 'frontend-app', exitCode: 0, signal: null, outputSha256: 'b'.repeat(64), testCount: 2 },
        wailsHost: { argv: ['failure-smoke-host'], cwd: '.', exitCode: null, signal: 'SIGTERM', outputSha256: 'c'.repeat(64) },
        vite: { argv: ['npm', 'run', 'dev'], cwd: 'frontend-app', exitCode: null, signal: 'SIGTERM', outputSha256: 'd'.repeat(64) },
      },
      cases: [
        caseEvidence('terminal-failed', DESKTOP_FAILURE_REPORT_REQUIREMENTS['terminal-failed'].hops, DESKTOP_FAILURE_REPORT_REQUIREMENTS['terminal-failed'].domAssertions),
        caseEvidence('prompt-history-reject', DESKTOP_FAILURE_REPORT_REQUIREMENTS['prompt-history-reject'].hops, DESKTOP_FAILURE_REPORT_REQUIREMENTS['prompt-history-reject'].domAssertions),
      ],
    };
    expect(validateDesktopFailureReport(valid)).toBe(valid);
    expect(() => validateDesktopFailureReport({ ...valid, caseIds: valid.caseIds.slice(1) })).toThrow(/exact diff/);
    expect(() => validateDesktopFailureReport({ ...valid, caseIds: ['terminal-failed', 'terminal-failed'] })).toThrow(/exact diff/);
    expect(() => validateDesktopFailureReport({ ...valid, caseIds: ['terminal-failed', 'stale'] })).toThrow(/exact diff/);
    expect(() => validateDesktopFailureReport({ ...valid, testCount: 0 })).toThrow(/testCount/);
    expect(() => validateDesktopFailureReport({ ...valid, status: 'partial', blockedCases: [{}] })).toThrow(/covered status/);
    const redCase = structuredClone(valid);
    redCase.cases[0].result = 'RED';
    expect(() => validateDesktopFailureReport(redCase)).toThrow(/case evidence/);
    const summaryOnly = structuredClone(valid);
    summaryOnly.cases[0].hops = ['summary'];
    expect(() => validateDesktopFailureReport(summaryOnly)).toThrow(/case evidence/);
    const noRecoveryAssertion = structuredClone(valid);
    noRecoveryAssertion.cases[1].domAssertions = ['draft-preserved', 'cursor-preserved', 'summary-only'];
    expect(() => validateDesktopFailureReport(noRecoveryAssertion)).toThrow(/case evidence/);
    const addedAssertion = structuredClone(valid);
    addedAssertion.cases[0].domAssertions.push('unfrozen-assertion');
    expect(() => validateDesktopFailureReport(addedAssertion)).toThrow(/case evidence/);
    const fakeExecution = structuredClone(valid);
    fakeExecution.execution.playwright.exitCode = 1;
    expect(() => validateDesktopFailureReport(fakeExecution)).toThrow(/command execution/);
    const partialExecution = structuredClone(valid);
    partialExecution.execution.playwright.testCount = 1;
    expect(() => validateDesktopFailureReport(partialExecution)).toThrow(/command execution/);
    const removedFrozenSource = structuredClone(valid);
    delete removedFrozenSource.sourceHashes['frontend-app/scripts/desktop-failure-contract.mjs'];
    expect(() => validateDesktopFailureReport(removedFrozenSource)).toThrow(/source-hashed/);
    expect(() => validateDesktopFailureReport({ ...valid, raw: 't03-raw-provider-secret-do-not-persist' })).toThrow(/leaked raw provider secret/);
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
      timeoutMs: DESKTOP_FAILURE_SMOKE_DEFAULT_TIMEOUT_MS,
      backendBinary: expect.stringMatching(/^\/repo\/app\/\.tmp\/desktop-failure-smoke\/failure-smoke-host-\d+$/u),
      reportPath: '/repo/app/.tmp/desktop-failure-smoke/report.json',
    }));
    expect(resolveDesktopFailureSmokeTimeout({ SUPER_DOLPHIN_FAILURE_SMOKE_TIMEOUT_MS: '240000' })).toBe(240_000);
    expect(() => resolveDesktopFailureSmokeTimeout({ SUPER_DOLPHIN_FAILURE_SMOKE_TIMEOUT_MS: 'invalid' })).toThrow(/positive integer/);
  });

  it('redacts bearer credentials from failed command diagnostics', () => {
    const message = commandFailureMessage({
      command: 'playwright', args: ['test'], code: 1, signal: null, stdout: '',
      stderr: 'Authorization: Bearer t03-raw-provider-secret-do-not-persist',
    });
    expect(message).toContain('exit=1');
    expect(message).toContain('Authorization: Bearer [redacted]');
    expect(message).not.toContain('t03-raw-provider-secret-do-not-persist');
  });

  it('keeps caller debug namespaces while enabling browser process diagnostics', () => {
    expect(mergeDebugNamespace('vite:*,pw:browser*', 'pw:browser*')).toBe('vite:*,pw:browser*');
    expect(mergeDebugNamespace('vite:*', 'pw:browser*')).toBe('vite:*,pw:browser*');
    expect(mergeDebugNamespace('', 'pw:browser*')).toBe('pw:browser*');
  });

  it('keeps both ends of an oversized redacted command failure', () => {
    const message = commandFailureMessage({
      command: 'playwright', args: ['test'], code: 1, signal: null,
      stdout: `first-failure ${'x'.repeat(2500)}`,
      stderr: `${'y'.repeat(2500)} t03-raw-provider-secret-do-not-persist final-result`,
    });
    expect(message).toContain('first-failure');
    expect(message).toContain('final-result');
    expect(message).toContain('...[truncated]...');
    expect(message).not.toContain('t03-raw-provider-secret-do-not-persist');
  });

  it('summarizes the first failed Playwright case from structured output', () => {
    const message = commandFailureMessage({ command: 'playwright', args: ['test'], code: 1, signal: null, stdout: JSON.stringify({
      suites: [{
        specs: [{
          title: 'terminal-failed crosses production Wails application',
          tests: [{ results: [{
            status: 'failed',
            errors: [{ message: 'safe error missing' }],
            stdout: [
              { text: '\u001b[30;1mpw:browser \u001b[0m<launching> /runtime/chrome\n' },
              { text: '\u001b[30;1mpw:browser \u001b[0m[pid=42] <process did exit: exitCode=null, signal=SIGTRAP>\n' },
            ],
          }] }],
        }],
      }],
    }), stderr: '' });
    expect(message).toContain('Playwright failure: terminal-failed crosses production Wails application: safe error missing');
    expect(message).toContain('Playwright browser process:');
    expect(message).toContain('pw:browser <launching> /runtime/chrome');
    expect(message).toContain('signal=SIGTRAP');
    expect(message).not.toContain('\u001b');
  });

  it('keeps fatal browser diagnostics when repetitive debug output is filtered', () => {
    const noisyLines = Array.from({ length: 24 }, (_, index) => (
      `pw:browser [pid=42][err] dbus/bus.cc noisy line ${index}`
    ));
    noisyLines.splice(12, 0, 'pw:browser [pid=42][err] FATAL: Check failed: browser runtime');
    const message = commandFailureMessage({ command: 'playwright', args: ['test'], code: 1, signal: null, stdout: JSON.stringify({
      suites: [{
        specs: [{
          title: 'browser runtime failure',
          tests: [{ results: [{ status: 'failed', errors: [], stderr: [{ text: noisyLines.join('\n') }] }] }],
        }],
      }],
    }), stderr: '' });
    expect(message).toContain('FATAL: Check failed: browser runtime');
    expect(message).toContain('...[browser log filtered]...');
  });
});
