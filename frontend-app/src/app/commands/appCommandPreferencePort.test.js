import { beforeEach, describe, expect, it, vi } from 'vitest';

import { getPreference, setPreference } from '../../shared/api/backendApi.js';
import { SHORTCUT_PREFERENCE_KEY } from '../../features/shortcut-settings/model/shortcutSettingsModel.js';
import { appCommandPreferencePort } from './appCommandPreferencePort.js';

vi.mock('../../shared/api/backendApi.js', () => ({
  getPreference: vi.fn(),
  setPreference: vi.fn(),
}));

describe('app command preference port', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('forwards the exact shortcut preference read', () => {
    const params = { cwd: '/repo/app', key: SHORTCUT_PREFERENCE_KEY };
    const response = Promise.resolve({ 'chat.new': { key: 'm' } });
    getPreference.mockReturnValue(response);

    expect(appCommandPreferencePort.getPreference(params)).toBe(response);
    expect(getPreference).toHaveBeenCalledExactlyOnceWith(params);
  });

  it('forwards the exact shortcut preference write', () => {
    const params = { cwd: '/repo/app', key: SHORTCUT_PREFERENCE_KEY, value: {} };
    const response = Promise.resolve({ ok: true });
    setPreference.mockReturnValue(response);

    expect(appCommandPreferencePort.setPreference(params)).toBe(response);
    expect(setPreference).toHaveBeenCalledExactlyOnceWith(params);
  });

  it.each(['other.preference', '', undefined])('rejects unsupported preference key %s', (key) => {
    const params = { cwd: '/repo/app', key };

    expect(() => appCommandPreferencePort.getPreference(params)).toThrow('unsupported app command preference key');
    expect(() => appCommandPreferencePort.setPreference({ ...params, value: {} })).toThrow('unsupported app command preference key');
    expect(getPreference).not.toHaveBeenCalled();
    expect(setPreference).not.toHaveBeenCalled();
  });
});
