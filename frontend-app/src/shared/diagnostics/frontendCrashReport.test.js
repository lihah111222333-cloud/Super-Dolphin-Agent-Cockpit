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

function privateCrashInput() {
  const error = new Error('private message body at /Users/alice/private/project/secret.js');
  error.stack = 'RAW_STACK_SECRET sk-live-secret-token';
  return {
    actionCode: 'app.render.crash',
    routeId: 'chat',
    phase: 'render',
    timestamp: '2026-07-11T13:10:00.000Z',
    error,
    componentStack: 'COMPONENT_STACK_SECRET',
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
    breadcrumbs: [{
      actionCode: 'chat.send',
      routeId: 'chat',
      phase: 'start',
      timestamp: '2026-07-11T13:09:59.000Z',
      token: 'breadcrumb-secret-token',
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
      routeId: 'chat',
      phase: 'render',
      timestamp: '2026-07-11T13:10:00.000Z',
    }));
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
