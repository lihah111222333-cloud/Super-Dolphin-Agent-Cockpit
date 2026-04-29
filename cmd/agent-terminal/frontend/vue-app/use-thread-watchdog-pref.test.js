// @ts-nocheck
import { beforeEach, describe, expect, it, vi } from 'vitest';

const apiMock = vi.hoisted(() => ({ callAPI: vi.fn() }));
vi.mock('./services/api.js', () => ({ callAPI: apiMock.callAPI }));
vi.mock('./services/log.js', () => ({
  logInfo: vi.fn(),
  logWarn: vi.fn(),
  logError: vi.fn(),
}));

import {
  useThreadWatchdogPref,
  useThreadWatchdogPrefReady,
  loadThreadWatchdogPref,
  saveThreadWatchdogPref,
  isValidThreadWatchdogPref,
  _resetThreadWatchdogPrefForTest,
} from './composables/useThreadWatchdogPref.js';

beforeEach(() => {
  _resetThreadWatchdogPrefForTest();
  apiMock.callAPI.mockReset();
});

describe('useThreadWatchdogPref', () => {
  it('默认值为 true', () => {
    apiMock.callAPI.mockResolvedValue(null);
    expect(useThreadWatchdogPref().value).toBe(true);
  });

  it('load 后采用后端值', async () => {
    apiMock.callAPI.mockResolvedValue(false);
    await loadThreadWatchdogPref();
    expect(useThreadWatchdogPref().value).toBe(false);
    expect(useThreadWatchdogPrefReady().value).toBe(true);
  });

  it('load 失败保留默认值且 ready=true', async () => {
    apiMock.callAPI.mockRejectedValue(new Error('boom'));
    await loadThreadWatchdogPref();
    expect(useThreadWatchdogPref().value).toBe(true);
    expect(useThreadWatchdogPrefReady().value).toBe(true);
  });

  it('save 立即更新模块 ref', async () => {
    apiMock.callAPI.mockResolvedValue(undefined);
    await saveThreadWatchdogPref(false);
    expect(useThreadWatchdogPref().value).toBe(false);
    // call 0 是 load(get)，call 1 是 save(set)
    const setCall = apiMock.callAPI.mock.calls.find((c) => c[0] === "ui/preferences/set");
    expect(setCall).toBeTruthy();
    expect(setCall[1]).toEqual({ key: "taskHandoff.threadWatchdog", value: false });
  });

  it('save 非 boolean 抛错', async () => {
    await expect(saveThreadWatchdogPref('yes')).rejects.toThrow('boolean');
  });

  it('isValidThreadWatchdogPref', () => {
    expect(isValidThreadWatchdogPref(true)).toBe(true);
    expect(isValidThreadWatchdogPref(false)).toBe(true);
    expect(isValidThreadWatchdogPref('true')).toBe(false);
    expect(isValidThreadWatchdogPref(null)).toBe(false);
    expect(isValidThreadWatchdogPref(undefined)).toBe(false);
  });

  it('PREF_KEY 锁定为 taskHandoff.threadWatchdog（不复用 autoContinueOnAlert）', async () => {
    apiMock.callAPI.mockResolvedValue(true);
    await loadThreadWatchdogPref();
    expect(apiMock.callAPI.mock.calls[0][1].key).toBe('taskHandoff.threadWatchdog');
  });
});
