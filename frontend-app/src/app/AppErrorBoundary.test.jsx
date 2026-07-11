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
    expect(mainSource).toContain('component: report.actionCode');
    expect(mainSource).not.toContain("component: 'app-error-boundary'");
  });

  it('contains render crashes and retries the child tree from an accessible fallback', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {});
    const reporter = vi.fn().mockResolvedValue(undefined);
    let crashing = true;
    function CrashyChild() {
      if (crashing) throw new Error('boundary private message');
      return <p>界面已恢复</p>;
    }

    render(
      <AppErrorBoundary reporter={reporter} routeId="chat" reload={vi.fn()}>
        <CrashyChild />
      </AppErrorBoundary>,
    );

    expect(screen.getByRole('alert')).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: '界面发生错误' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '重试界面' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '重新加载' })).toBeInTheDocument();
    await waitFor(() => expect(reporter).toHaveBeenCalledTimes(1));
    expect(JSON.stringify(reporter.mock.calls[0][0])).not.toContain('boundary private message');

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
