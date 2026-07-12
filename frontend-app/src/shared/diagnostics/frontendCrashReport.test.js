import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  createFrontendCrashReport,
  installGlobalCrashHandlers,
  reportFrontendCrash,
} from './frontendCrashReport.js';
import crashReportSource from './frontendCrashReport.js?raw';

const cleanups = [];

afterEach(() => {
  while (cleanups.length > 0) cleanups.pop()();
  vi.restoreAllMocks();
});

function privateCrashInput(options = {}) {
  const error = new TypeError(options.message ?? 'private message body at /Users/alice/private/project/secret.js');
  error.code = options.errorCode ?? 'APPROVAL_SUBMIT_TIMEOUT';
  error.stack = options.stack ?? 'RAW_STACK_SECRET sk-live-secret-token';
  return {
    actionCode: options.actionCode ?? 'app.render.crash',
    routeId: options.routeId ?? 'app',
    phase: options.phase ?? 'render',
    timestamp: options.timestamp ?? '2026-07-11T13:10:00.000Z',
    error,
    componentStack: options.componentStack ?? 'COMPONENT_STACK_SECRET',
    fields: {
      token: 'sk-live-secret-token',
      Authorization: 'Bearer authorization-secret',
      cwd: '/Users/alice/private/project',
      prompt: 'private prompt body',
      message: 'private message body',
      toolResult: 'private tool result',
      memory: 'private memory',
      skill: 'private skill',
    },
    breadcrumbs: options.breadcrumbs ?? [{
      actionCode: 'app.bootstrap',
      routeId: 'app',
      phase: 'start',
      timestamp: '2026-07-11T13:09:59.000Z',
    }],
  };
}

describe('frontend crash report', () => {
  it('uses the shared sanitizer without importing the client store', () => {
    expect(crashReportSource).toContain("from './safeLogFields.js'");
    expect(crashReportSource).toContain('safeLogFields(');
    expect(crashReportSource).not.toMatch(/useClientStore|entities\/client/);
  });

  it('serializes stable diagnostics without private content, paths, or stacks', () => {
    const report = createFrontendCrashReport(privateCrashInput());
    const serialized = JSON.stringify(report);

    expect(report).toEqual(expect.objectContaining({
      actionCode: 'app.render.crash',
      routeId: 'app',
      phase: 'render',
      timestamp: '2026-07-11T13:10:00.000Z',
      errorName: 'TypeError',
      errorCode: 'APPROVAL_SUBMIT_TIMEOUT',
      contextCode: 'react.root',
      fingerprint: 'crash-v1-1483443a51ffbe45',
      breadcrumbTrail: 'app.bootstrap:app:start',
    }));
    expect(report).not.toHaveProperty('breadcrumbs');
    expect(report).not.toHaveProperty('error');
    expect(report).not.toHaveProperty('componentStack');
    for (const forbidden of [
      'sk-live-secret-token',
      'authorization-secret',
      '/Users/alice/private/project',
      'private prompt body',
      'private message body',
      'private tool result',
      'private memory',
      'private skill',
      'RAW_STACK_SECRET',
      'COMPONENT_STACK_SECRET',
      'breadcrumb-secret-token',
    ]) {
      expect(serialized).not.toContain(forbidden);
    }
  });

  it('fingerprints only validated crash classification, context, and the emitted breadcrumb trail', () => {
    const first = createFrontendCrashReport(privateCrashInput());
    const privateVariant = createFrontendCrashReport(privateCrashInput({
      message: 'different private body at C:\\Users\\alice\\secret.txt',
      stack: 'DIFFERENT_PRIVATE_STACK bearer secret-token',
      componentStack: 'DIFFERENT_COMPONENT_STACK /home/alice/private.jsx',
      timestamp: '2026-07-11T14:10:00.000Z',
    }));
    const changedCode = createFrontendCrashReport(privateCrashInput({ errorCode: 'ANOTHER_STABLE_CODE' }));
    const changedContext = createFrontendCrashReport(privateCrashInput({
      actionCode: 'app.window.error',
      phase: 'global',
    }));
    const changedTrail = createFrontendCrashReport(privateCrashInput({
      breadcrumbs: [{
        actionCode: 'approval.submit',
        routeId: 'chat',
        phase: 'timeout',
        timestamp: '2026-07-11T13:09:59.000Z',
      }],
    }));

    expect(privateVariant.fingerprint).toBe(first.fingerprint);
    expect(changedCode.fingerprint).not.toBe(first.fingerprint);
    expect(changedContext).toEqual(expect.objectContaining({
      contextCode: 'window.error',
      fingerprint: expect.stringMatching(/^crash-v1-[0-9a-f]{16}$/),
    }));
    expect(changedContext.fingerprint).not.toBe(first.fingerprint);
    expect(changedTrail.fingerprint).not.toBe(first.fingerprint);
  });

  it('normalizes custom error names and non-machine codes to closed diagnostic values', () => {
    const input = privateCrashInput({ errorCode: 'private code /Users/alice' });
    input.error.name = 'PrivateUserError';

    expect(createFrontendCrashReport(input)).toEqual(expect.objectContaining({
      errorName: 'UnknownError',
      errorCode: 'UNCLASSIFIED',
      contextCode: 'react.root',
    }));
  });

  it('rejects crash actions outside the closed crash source contract', () => {
    expect(() => createFrontendCrashReport(privateCrashInput({
      actionCode: 'app.navigation',
      phase: 'complete',
    }))).toThrow('frontend crash actionCode is not allowed');
  });

  it('rejects private crash routes and phases that do not match the crash action', () => {
    expect(() => createFrontendCrashReport(privateCrashInput({
      routeId: '/Users/alice/private',
    }))).toThrow('frontend breadcrumb routeId is not allowed');
    expect(() => createFrontendCrashReport(privateCrashInput({
      actionCode: 'app.window.error',
      phase: 'render',
    }))).toThrow('frontend crash phase does not match actionCode');
    expect(() => createFrontendCrashReport({
      ...privateCrashInput(),
      breadcrumbs: null,
    })).toThrow('frontend crash breadcrumbs must be an array');
  });

  it('keeps only the newest complete breadcrumb entries within the 160 character wire limit', () => {
    const breadcrumbs = Array.from({ length: 12 }, (_, index) => ({
      actionCode: 'app.navigation',
      routeId: index === 11 ? 'settings' : 'observability',
      phase: 'complete',
      timestamp: `2026-07-11T13:09:${String(index).padStart(2, '0')}.000Z`,
    }));
    const changedDroppedOldest = breadcrumbs.map((entry, index) => (
      index === 0 ? { ...entry, routeId: 'files' } : entry
    ));
    const first = createFrontendCrashReport(privateCrashInput({ breadcrumbs }));
    const second = createFrontendCrashReport(privateCrashInput({ breadcrumbs: changedDroppedOldest }));

    expect(typeof first.breadcrumbTrail).toBe('string');
    expect(first.breadcrumbTrail.length).toBeLessThanOrEqual(160);
    expect(first.breadcrumbTrail).toMatch(/app\.navigation:settings:complete$/);
    expect(first.breadcrumbTrail).not.toContain('2026-07-11');
    expect(first.breadcrumbTrail).toBe(second.breadcrumbTrail);
    expect(first.fingerprint).toBe(second.fingerprint);
  });

  it('contains reporter failure without recursively reporting private errors', async () => {
    const reporter = vi.fn().mockRejectedValue(new Error('reporter secret failure'));
    const consoleRef = { error: vi.fn() };

    await expect(reportFrontendCrash({
      input: privateCrashInput(),
      reporter,
      consoleRef,
    })).resolves.toBe(false);

    expect(reporter).toHaveBeenCalledTimes(1);
    expect(consoleRef.error).toHaveBeenCalledExactlyOnceWith('[frontend-crash] reporter failed');
    expect(JSON.stringify(consoleRef.error.mock.calls)).not.toContain('reporter secret failure');
  });

  it('installs global handlers once, ignores prevented events, and cleans up', async () => {
    const devCollector = vi.fn();
    window.addEventListener('error', devCollector);
    window.addEventListener('unhandledrejection', devCollector);
    cleanups.push(() => {
      window.removeEventListener('error', devCollector);
      window.removeEventListener('unhandledrejection', devCollector);
    });
    const addSpy = vi.spyOn(window, 'addEventListener');
    const removeSpy = vi.spyOn(window, 'removeEventListener');
    const reporter = vi.fn().mockResolvedValue(undefined);
    const options = {
      windowRef: window,
      reporter,
      routeId: 'chat',
      consoleRef: { error: vi.fn() },
    };

    const cleanupFirst = installGlobalCrashHandlers(options);
    const cleanupSecond = installGlobalCrashHandlers(options);
    cleanups.push(cleanupFirst, cleanupSecond);

    expect(addSpy.mock.calls.filter(([name]) => name === 'error' || name === 'unhandledrejection')).toHaveLength(2);

    const prevented = new ErrorEvent('error', {
      cancelable: true,
      error: new Error('prevented private error'),
      message: 'prevented private error',
    });
    prevented.preventDefault();
    window.dispatchEvent(prevented);
    await Promise.resolve();
    expect(reporter).not.toHaveBeenCalled();
    expect(devCollector).toHaveBeenCalledTimes(1);

    window.dispatchEvent(new ErrorEvent('error', {
      error: new Error('window private error'),
      message: 'window private error',
    }));
    const rejection = new Event('unhandledrejection');
    Object.defineProperty(rejection, 'reason', { value: new Error('promise private error') });
    window.dispatchEvent(rejection);
    await Promise.resolve();
    expect(reporter).toHaveBeenCalledTimes(2);
    expect(reporter.mock.calls.map(([report]) => ({
      actionCode: report.actionCode,
      contextCode: report.contextCode,
      errorName: report.errorName,
      errorCode: report.errorCode,
    }))).toEqual([
      {
        actionCode: 'app.window.error',
        contextCode: 'window.error',
        errorName: 'Error',
        errorCode: 'UNCLASSIFIED',
      },
      {
        actionCode: 'app.unhandled.rejection',
        contextCode: 'promise.unhandled',
        errorName: 'Error',
        errorCode: 'UNCLASSIFIED',
      },
    ]);
    expect(devCollector).toHaveBeenCalledTimes(3);

    cleanupFirst();
    cleanupSecond();
    window.dispatchEvent(new ErrorEvent('error', { message: 'after cleanup' }));
    await Promise.resolve();

    expect(reporter).toHaveBeenCalledTimes(2);
    expect(devCollector).toHaveBeenCalledTimes(4);
    expect(removeSpy.mock.calls.filter(([name]) => name === 'error' || name === 'unhandledrejection')).toHaveLength(2);
  });
});
