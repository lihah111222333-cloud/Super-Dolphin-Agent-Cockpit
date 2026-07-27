import { expect, it, vi } from 'vitest';

const runtime = vi.hoisted(() => ({
  service: {
    getPreference: vi.fn(),
    listDashboardLogs: vi.fn(),
    setPreference: vi.fn().mockResolvedValue(undefined),
  },
}));

vi.mock('./services/settingsPageService.js', () => ({ settingsPageService: runtime.service }));

import { defaultSettingsForm, saveProviderRuntimePreferences } from './settingsPageRuntime.js';

it('shows readOnly when no sandbox preference has been persisted', () => {
  expect(defaultSettingsForm().sandboxPolicy).toBe('readOnly');
});

it('allows workspaceWrite to rely on the workspace when writable roots are empty', async () => {
  const form = { ...defaultSettingsForm(), sandboxPolicy: 'workspaceWrite', writableRoots: '' };

  await saveProviderRuntimePreferences({
    copy: { provider: { absolutePathRequired: 'absolute: ', settingsSaved: 'saved' } },
    cwd: '/repo/app',
    form,
    setError: vi.fn(),
    setStatus: vi.fn(),
  });

  expect(runtime.service.setPreference).toHaveBeenCalledWith({
    cwd: '/repo/app',
    key: 'settings.provider.codex.sandbox',
    value: { type: 'workspaceWrite', writableRoots: [], networkAccess: false },
  });
});
