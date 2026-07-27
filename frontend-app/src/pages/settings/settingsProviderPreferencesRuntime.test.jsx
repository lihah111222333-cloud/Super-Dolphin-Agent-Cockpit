import React from 'react';
import { act, renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { expect, it, vi } from 'vitest';

const runtime = vi.hoisted(() => {
  const preferences = new Map([
    ['settings.provider.codex.summary', 'detailed'],
    ['settings.provider.codex.approvalPolicy', 'on-request'],
  ]);
  return {
    preferences,
    service: {
      getPreference: vi.fn(({ key }) => Promise.resolve(preferences.get(key) ?? null)),
      setPreference: vi.fn(({ key, value }) => {
        preferences.set(key, value);
        return Promise.resolve();
      }),
    },
  };
});

vi.mock('./services/settingsPageService.js', () => ({ settingsPageService: runtime.service }));

import { useProviderPreferences } from './settingsProviderPreferencesRuntime.js';

function wrapper({ children }) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
}

it('refetches saved approval policy before clearing the dirty state', async () => {
  const copy = {
    provider: {
      loadPreferencesFailed: 'load failed',
      saveFailed: 'save failed',
      savedPrefix: 'saved: ',
    },
  };
  const { result } = renderHook(
    () => useProviderPreferences('/repo/app', 'codex', copy),
    { wrapper },
  );
  await waitFor(() => expect(result.current.approvalMode).toBe('on-request'));

  act(() => result.current.setApprovalMode('never'));
  await act(async () => { await result.current.save(); });

  expect(runtime.service.setPreference).toHaveBeenCalledWith({
    cwd: '/repo/app',
    key: 'settings.provider.codex.approvalPolicy',
    value: 'never',
  });
  await waitFor(() => expect(result.current.approvalMode).toBe('never'));
});
