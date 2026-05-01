// @ts-nocheck
import { beforeEach, describe, expect, it, vi } from 'vitest';

const apiMock = vi.hoisted(() => ({
  callAPI: vi.fn(() => Promise.resolve(null)),
}));

vi.mock('./services/api.js', () => ({
  callAPI: apiMock.callAPI,
}));

vi.mock('./services/log.js', () => ({
  logDebug: vi.fn(),
  logInfo: vi.fn(),
  logWarn: vi.fn(),
}));

import { MemoryCenterPage } from './pages/MemoryCenterPage.js';

function setupPage(overview = {}) {
  const emit = vi.fn();
  const props = {
    model: {
      overview,
      private: { entries: [] },
      team: { entries: [] },
    },
  };
  const vm = MemoryCenterPage.setup(props, { emit });
  return { vm, emit };
}

beforeEach(() => {
  apiMock.callAPI.mockReset();
  apiMock.callAPI.mockResolvedValue(null);
});

describe('MemoryCenterPage auto-dream card', () => {
  it('runtime true with no intent → 已开启', () => {
    const { vm } = setupPage({ enabled: true, autoDreamEnabled: true });
    expect(vm.autoDreamEnabled.value).toBe(true);
    expect(vm.autoDreamStatusLabel.value).toBe('已开启');
    expect(vm.autoDreamPendingRestart.value).toBe(false);
  });

  it('runtime false with no intent → 已关闭', () => {
    const { vm } = setupPage({ enabled: true, autoDreamEnabled: false });
    expect(vm.autoDreamEnabled.value).toBe(false);
    expect(vm.autoDreamStatusLabel.value).toBe('已关闭');
    expect(vm.autoDreamPendingRestart.value).toBe(false);
  });

  it('intent=true overrides runtime=false and flags pending restart', () => {
    const { vm } = setupPage({ enabled: true, autoDreamEnabled: false, autoDreamIntent: true });
    expect(vm.autoDreamEnabled.value).toBe(true);
    expect(vm.autoDreamStatusLabel.value).toBe('已开启');
    expect(vm.autoDreamPendingRestart.value).toBe(true);
  });

  it('intent=false overrides runtime=true and flags pending restart', () => {
    const { vm } = setupPage({ enabled: true, autoDreamEnabled: true, autoDreamIntent: false });
    expect(vm.autoDreamEnabled.value).toBe(false);
    expect(vm.autoDreamStatusLabel.value).toBe('已关闭');
    expect(vm.autoDreamPendingRestart.value).toBe(true);
  });

  it('intent matching runtime → no pending restart', () => {
    const { vm } = setupPage({ enabled: true, autoDreamEnabled: true, autoDreamIntent: true });
    expect(vm.autoDreamPendingRestart.value).toBe(false);
  });

  it('toggleAutoDream calls RPC, sets warning notice, and emits refresh', async () => {
    const { vm, emit } = setupPage({ enabled: true, autoDreamEnabled: false });
    await vm.toggleAutoDream();
    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/memory/auto-dream/set-intent', { enabled: true });
    expect(vm.notice.level).toBe('warning');
    expect(vm.notice.message).toContain('开启');
    expect(vm.notice.message).toContain('重启 agent-terminal 后生效');
    expect(emit).toHaveBeenCalledWith('refresh');
    expect(vm.autoDreamToggling.value).toBe(false);
  });

  it('toggleAutoDream from enabled → disabled passes false', async () => {
    const { vm } = setupPage({ enabled: true, autoDreamEnabled: true, autoDreamIntent: true });
    await vm.toggleAutoDream();
    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/memory/auto-dream/set-intent', { enabled: false });
    expect(vm.notice.message).toContain('关闭');
  });

  it('toggleAutoDream surfaces RPC error as error notice', async () => {
    const { vm, emit } = setupPage({ enabled: true, autoDreamEnabled: false });
    apiMock.callAPI.mockRejectedValueOnce(new Error('boom'));
    await vm.toggleAutoDream();
    expect(vm.notice.level).toBe('error');
    expect(vm.notice.message).toContain('boom');
    expect(emit).not.toHaveBeenCalledWith('refresh');
    expect(vm.autoDreamToggling.value).toBe(false);
  });
});
