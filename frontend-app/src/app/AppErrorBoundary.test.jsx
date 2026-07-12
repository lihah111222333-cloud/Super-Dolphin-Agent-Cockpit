import React from 'react';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { AppErrorBoundary } from './AppErrorBoundary.jsx';
import mainSource from '../main.jsx?raw';

afterEach(() => {
  vi.restoreAllMocks();
});

describe('AppErrorBoundary', () => {
  it('wraps the retained Profiler in StrictMode before rendering App', () => {
    expect(mainSource).toMatch(
      /createElement\(\s*StrictMode[\s\S]*createElement\(\s*AppErrorBoundary[\s\S]*createElement\(\s*Profiler[\s\S]*createElement\(App,/,
    );
    expect(mainSource).toContain('error: `${report.errorName}:${report.errorCode}`');
    expect(mainSource).toContain('component: report.contextCode');
    expect(mainSource).toContain('crash_fingerprint: report.fingerprint');
    expect(mainSource).toContain('breadcrumb_trail: report.breadcrumbTrail');
    expect(mainSource).not.toContain('component: report.actionCode');
    expect(mainSource).not.toContain("component: 'app-error-boundary'");
    expect(mainSource).toContain('recordFrontendBootstrapBreadcrumb()');
    expect(mainSource).toContain('breadcrumbs: frontendBreadcrumbSnapshotSource');
    expect(mainSource.match(/breadcrumbs: frontendBreadcrumbSnapshotSource/g)).toHaveLength(2);
    expect(mainSource).not.toContain('createFrontendBreadcrumbBuffer()');
    expect(mainSource).not.toContain('frontendBreadcrumbs.record(');
    expect(mainSource.match(/startFrontendPerformancePressure\(/g)).toHaveLength(1);
    expect(mainSource).toContain('cleanupGlobalCrashHandlers()');
    expect(mainSource).toContain('frontendPerformancePressure.stop()');
    expect(mainSource).toContain('import.meta.hot.dispose(cleanupFrontendDiagnostics)');
    expect(mainSource).toContain("console.error('frontend.performance.reporter_contract_failed')");
    expect(mainSource).not.toContain('console.error(error)');
  });

  it('contains render crashes and retries the child tree from an accessible fallback', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {});
    const reporter = vi.fn().mockResolvedValue(undefined);
    const renderError = new TypeError('boundary private message at /Users/alice/private.jsx');
    renderError.code = 'APPROVAL_SUBMIT_TIMEOUT';
    renderError.stack = 'PRIVATE_BOUNDARY_STACK bearer secret-token';
    const breadcrumbs = [{
      actionCode: 'app.bootstrap',
      routeId: 'app',
      phase: 'start',
      timestamp: '2026-07-11T13:09:59.000Z',
    }];
    let crashing = true;
    function CrashyChild() {
      if (crashing) throw renderError;
      return <p>界面已恢复</p>;
    }

    render(
      <AppErrorBoundary
        reporter={reporter}
        routeId="chat"
        breadcrumbs={breadcrumbs}
        reload={vi.fn()}
      >
        <CrashyChild />
      </AppErrorBoundary>,
    );

    expect(screen.getByRole('alert')).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: '界面发生错误' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '重试界面' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '重新加载' })).toBeInTheDocument();
    await waitFor(() => expect(reporter).toHaveBeenCalledTimes(1));
    expect(reporter.mock.calls[0][0]).toEqual(expect.objectContaining({
      actionCode: 'app.render.crash',
      routeId: 'chat',
      phase: 'render',
      errorName: 'TypeError',
      errorCode: 'APPROVAL_SUBMIT_TIMEOUT',
      contextCode: 'react.root',
      fingerprint: expect.stringMatching(/^crash-v1-[0-9a-f]{16}$/),
      breadcrumbTrail: 'app.bootstrap:app:start',
    }));
    const serialized = JSON.stringify(reporter.mock.calls[0][0]);
    expect(serialized).not.toContain('boundary private message');
    expect(serialized).not.toContain('PRIVATE_BOUNDARY_STACK');
    expect(serialized).not.toContain('/Users/alice');

    crashing = false;
    fireEvent.click(screen.getByRole('button', { name: '重试界面' }));
    expect(screen.getByText('界面已恢复')).toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('offers an injected full reload action', () => {
    vi.spyOn(console, 'error').mockImplementation(() => {});
    const reload = vi.fn();
    function CrashyChild() {
      throw new Error('reload private message');
    }

    render(
      <AppErrorBoundary reporter={vi.fn()} routeId="chat" reload={reload}>
        <CrashyChild />
      </AppErrorBoundary>,
    );

    fireEvent.click(screen.getByRole('button', { name: '重新加载' }));
    expect(reload).toHaveBeenCalledTimes(1);
  });
});
