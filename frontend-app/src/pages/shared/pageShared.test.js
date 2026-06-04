import React from 'react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, cleanup, render } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { optionalSettingsCwd, textValue, useDashboardQueryFocusInvalidation, wordListFromText } from './pageShared.js';

function FocusInvalidationHarness({ queryKey }) {
  useDashboardQueryFocusInvalidation(queryKey);
  return null;
}

function renderFocusInvalidation(queryKey) {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  });
  const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries').mockResolvedValue();

  render(
    React.createElement(
      QueryClientProvider,
      { client: queryClient },
      React.createElement(FocusInvalidationHarness, { queryKey }),
    ),
  );

  return { invalidateSpy };
}

function setVisibilityState(value) {
  Object.defineProperty(document, 'visibilityState', {
    configurable: true,
    value,
  });
}

describe('pageShared utilities', () => {
  it('normalizes shared page text helpers', () => {
    expect(textValue(' value ')).toBe('value');
    expect(optionalSettingsCwd('selected-project')).toBe('selected-project');
    expect(wordListFromText('alpha, beta gamma')).toEqual(['alpha', 'beta gamma']);
  });
});

describe('useDashboardQueryFocusInvalidation', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    setVisibilityState('visible');
  });

  afterEach(() => {
    cleanup();
    vi.runOnlyPendingTimers();
    vi.useRealTimers();
    vi.restoreAllMocks();
    setVisibilityState('visible');
  });

  it('coalesces focus and visibility changes in the same short window into one invalidate', () => {
    const queryKey = ['dashboard', 'project', '/repo/app', 'dags'];
    const { invalidateSpy } = renderFocusInvalidation(queryKey);

    act(() => {
      window.dispatchEvent(new Event('focus'));
      document.dispatchEvent(new Event('visibilitychange'));
      vi.runOnlyPendingTimers();
    });

    expect(invalidateSpy).toHaveBeenCalledTimes(1);
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey });
  });

  it('skips invalidation while the document is hidden', () => {
    const queryKey = ['dashboard', 'project', '/repo/app', 'dags'];
    const { invalidateSpy } = renderFocusInvalidation(queryKey);

    setVisibilityState('hidden');
    act(() => {
      window.dispatchEvent(new Event('focus'));
      document.dispatchEvent(new Event('visibilitychange'));
      vi.runOnlyPendingTimers();
    });

    expect(invalidateSpy).not.toHaveBeenCalled();
  });

  it('does not register listeners or invalidate when the query key is invalid', () => {
    const windowAddSpy = vi.spyOn(window, 'addEventListener');
    const documentAddSpy = vi.spyOn(document, 'addEventListener');
    const { invalidateSpy } = renderFocusInvalidation([]);

    expect(windowAddSpy).not.toHaveBeenCalledWith('focus', expect.any(Function));
    expect(documentAddSpy).not.toHaveBeenCalledWith('visibilitychange', expect.any(Function));

    act(() => {
      window.dispatchEvent(new Event('focus'));
      document.dispatchEvent(new Event('visibilitychange'));
      vi.runOnlyPendingTimers();
    });

    expect(invalidateSpy).not.toHaveBeenCalled();
  });
});
