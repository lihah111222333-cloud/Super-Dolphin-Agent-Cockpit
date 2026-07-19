import { describe, expect, it } from 'vitest';

import {
  commandFailureMessage,
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
    const command = ['node', 'scripts/desktop-failure-smoke.mjs'];
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
      sourceHashes: Object.fromEntries([
        'frontend-app/scripts/desktop-failure-smoke.mjs', 'frontend-app/tests/e2e/desktop-failure.spec.js',
        'frontend-app/playwright.failure.config.js', 'frontend-app/package.json', 'frontend-app/src/shared/api/wailsBridge.js',
        'internal/ui/wails/testdata/failure_smoke_host/main.go', 'internal/provider/claudecli/event_map.go',
        'internal/provider/codexapp/event_map.go', 'internal/provider/unified/event_map.go', 'internal/ui/wails/bridge.go',
      ].map((sourcePath) => [sourcePath, 'a'.repeat(64)])),
      execution: {
        goBuild: { argv: ['go', 'build'], cwd: '.', exitCode: 0, signal: null, outputSha256: 'a'.repeat(64) },
        playwright: { argv: ['playwright', 'test'], cwd: 'frontend-app', exitCode: 0, signal: null, outputSha256: 'b'.repeat(64), testCount: 2 },
        wailsHost: { argv: ['failure-smoke-host'], cwd: '.', exitCode: null, signal: 'SIGTERM', outputSha256: 'c'.repeat(64) },
        vite: { argv: ['npm', 'run', 'dev'], cwd: 'frontend-app', exitCode: null, signal: 'SIGTERM', outputSha256: 'd'.repeat(64) },
      },
      cases: [
        caseEvidence('terminal-failed', ['claudecli.raw', 'claudecli.adapter', 'turndto.TurnOutputDelta', 'wails.EventBridge', 'chromium.DOM', 'codexapp.raw', 'codexapp.adapter', 'turndto.TurnCompleted', 'turn/terminal', 'chromium.DOM'], ['partial-response-visible', 'safe-terminal-visible', 'raw-secret-absent']),
        caseEvidence('prompt-history-reject', ['wails.rpc', 'thread/promptHistory', 'frontend.action', 'chromium.DOM', 'retry.control', 'wails.rpc', 'chromium.DOM'], ['draft-preserved', 'cursor-preserved', 'retry-click-recovers']),
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
    const fakeExecution = structuredClone(valid);
    fakeExecution.execution.playwright.exitCode = 1;
    expect(() => validateDesktopFailureReport(fakeExecution)).toThrow(/command execution/);
    const partialExecution = structuredClone(valid);
    partialExecution.execution.playwright.testCount = 1;
    expect(() => validateDesktopFailureReport(partialExecution)).toThrow(/command execution/);
    const removedFrozenSource = structuredClone(valid);
    delete removedFrozenSource.sourceHashes['internal/provider/unified/event_map.go'];
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
      backendBinary: expect.stringMatching(/^\/repo\/app\/\.tmp\/desktop-failure-smoke\/failure-smoke-host-\d+$/u),
      reportPath: '/repo/app/.tmp/desktop-failure-smoke/report.json',
    }));
  });

  it('redacts bearer credentials from failed command diagnostics', () => {
    const message = commandFailureMessage(
      'playwright',
      ['test'],
      1,
      null,
      '',
      'Authorization: Bearer t03-raw-provider-secret-do-not-persist',
    );
    expect(message).toContain('exit=1');
    expect(message).toContain('Authorization: Bearer [redacted]');
    expect(message).not.toContain('t03-raw-provider-secret-do-not-persist');
  });

  it('keeps both ends of an oversized redacted command failure', () => {
    const message = commandFailureMessage(
      'playwright',
      ['test'],
      1,
      null,
      `first-failure ${'x'.repeat(2500)}`,
      `${'y'.repeat(2500)} t03-raw-provider-secret-do-not-persist final-result`,
    );
    expect(message).toContain('first-failure');
    expect(message).toContain('final-result');
    expect(message).toContain('...[truncated]...');
    expect(message).not.toContain('t03-raw-provider-secret-do-not-persist');
  });

  it('summarizes the first failed Playwright case from structured output', () => {
    const message = commandFailureMessage('playwright', ['test'], 1, null, JSON.stringify({
      suites: [{
        specs: [{
          title: 'terminal-failed crosses production Wails application',
          tests: [{ results: [{ status: 'failed', errors: [{ message: 'safe error missing' }] }] }],
        }],
      }],
    }), '');
    expect(message).toContain('Playwright failure: terminal-failed crosses production Wails application: safe error missing');
  });
});
