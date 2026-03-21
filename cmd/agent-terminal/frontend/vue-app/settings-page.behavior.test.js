// @ts-nocheck
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const apiMock = vi.hoisted(() => ({
  callAPI: vi.fn(),
}));

const logMock = vi.hoisted(() => ({
  logError: vi.fn(),
  logInfo: vi.fn(),
  logWarn: vi.fn(),
  readLogBuffer: vi.fn(() => []),
  readLogLevel: vi.fn(() => 'info'),
}));

const settingsScopeMock = vi.hoisted(() => ({
  activeProjectCwd: { value: '/repo' },
  withProjectCwd: vi.fn((payload) => ({ ...payload, cwd: '/repo' })),
}));
;

vi.mock('./services/api.js', () => ({
  callAPI: apiMock.callAPI,
}));

vi.mock('./services/log.js', () => ({
  logError: logMock.logError,
  logInfo: logMock.logInfo,
  logWarn: logMock.logWarn,
  readLogBuffer: logMock.readLogBuffer,
  readLogLevel: logMock.readLogLevel,
}));

vi.mock('./pages/settings/ProviderSettings.ts', () => ({
  ProviderSettings: { name: 'ProviderSettings' },
}));

vi.mock('./pages/settings/LspPromptSettings.ts', () => ({
  LspPromptSettings: { name: 'LspPromptSettings' },
}));

vi.mock('./pages/settings/useSettingsScope.ts', () => ({
  useSettingsScope: () => settingsScopeMock,
}));

import { SettingsPage } from './pages/SettingsPage.ts';

function createSettingsPage(overrides = {}, emit = vi.fn()) {
  const props = {
    buildInfo: {
      version: '1.0.0',
      runtime: 'runtime-x',
      buildTime: '2026-03-10 12:00:00',
      commit: 'abc123',
      ...overrides.buildInfo,
    },
    projectStore: overrides.projectStore ?? { state: { active: '/repo' } },
  };
  return { props, emit, vm: SettingsPage.setup(props, { emit }) };
}

beforeEach(() => {
  apiMock.callAPI.mockReset();
  logMock.logError.mockReset();
  logMock.logInfo.mockReset();
  logMock.logWarn.mockReset();
  logMock.readLogBuffer.mockReset().mockReturnValue([]);
  logMock.readLogLevel.mockReset().mockReturnValue('info');
  settingsScopeMock.withProjectCwd.mockClear();
  settingsScopeMock.activeProjectCwd.value = '/repo';
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('SettingsPage behavior', () => {
  it('exposes build metadata and refresh contract', () => {
    const emit = vi.fn();
    const { vm } = createSettingsPage({}, emit);

    expect(SettingsPage.name).toBe('SettingsPage');
    expect(vm.versionText.value).toBe('Agent Orchestrator 1.0.0');
    expect(vm.runtimeText.value).toContain('runtime-x');
    expect(vm.buildTimeText.value).toBe('2026-03-10 12:00:00');
    expect(vm.commitText.value).toBe('abc123');

    vm.refresh();
    expect(emit).toHaveBeenCalledWith('refresh');
  });

  it('refreshes log panel from buffered entries in reverse order', () => {
    logMock.readLogLevel.mockReturnValueOnce('warn');
    logMock.readLogBuffer.mockReturnValueOnce([
      { seq: 1, event: 'a' },
      { seq: 2, event: 'b' },
      { seq: 3, event: 'c' },
    ]);
    const { vm } = createSettingsPage();

    vm.refreshLogPanel();

    expect(vm.logLevel.value).toBe('warn');
    expect(vm.logEntries.value.map((item) => item.seq)).toEqual([3, 2, 1]);
  });

  it('loads valid stall threshold settings with project cwd scope', async () => {
    apiMock.callAPI.mockResolvedValueOnce(45);
    const { vm } = createSettingsPage();

    await vm.loadStallSettings();

    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/preferences/get', { key: 'stallThresholdSec', cwd: '/repo' });
    expect(vm.stallThreshold.value).toBe(45);
    expect(vm.stallNotice.message).toBe('');
  });

  it('keeps current stall threshold and reports invalid loaded values', async () => {
    apiMock.callAPI.mockResolvedValueOnce(10);
    const { vm } = createSettingsPage();

    await vm.loadStallSettings();

    expect(vm.stallThreshold.value).toBe(30);
    expect(vm.stallNotice.level).toBe('error');
    expect(vm.stallNotice.message).toContain('无效阈值');
  });

  it('saves a valid stall threshold and blocks values below the minimum', async () => {
    const { vm } = createSettingsPage();

    vm.stallThreshold.value = 20;
    await vm.saveStallThreshold();
    expect(apiMock.callAPI).not.toHaveBeenCalled();
    expect(vm.stallNotice.message).toContain('不能小于 30 秒');

    apiMock.callAPI.mockResolvedValueOnce({ ok: true });
    vm.stallThreshold.value = 90;
    await vm.saveStallThreshold();
    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/preferences/set', { key: 'stallThresholdSec', value: 90, cwd: '/repo' });
    expect(vm.stallNotice.message).toContain('已保存');
  });
});
