// @ts-nocheck
import { beforeEach, describe, expect, it, vi } from 'vitest';

const apiMock = vi.hoisted(() => ({
  callAPI: vi.fn(),
  copyTextToClipboard: vi.fn(),
}));

vi.mock('./services/api.js', () => ({
  callAPI: apiMock.callAPI,
  copyTextToClipboard: apiMock.copyTextToClipboard,
}));

import { LspPromptSettings } from './pages/settings/LspPromptSettings.ts';

function createPromptSettings(overrides = {}) {
  const props = {
    projectStore: overrides.projectStore ?? { state: { active: '/repo' } },
  };
  return { props, vm: LspPromptSettings.setup(props) };
}

beforeEach(() => {
  apiMock.callAPI.mockReset();
  apiMock.copyTextToClipboard.mockReset();
});

describe('LspPromptSettings behavior', () => {
  it('loads prompt hint state and updates derived display counts', async () => {
    apiMock.callAPI.mockResolvedValueOnce({
      hint: 'effective\nprompt',
      defaultHint: 'default prompt',
      overrideHint: 'custom prompt',
      usingDefault: false,
    });

    const { vm } = createPromptSettings();
    await vm.loadLSPPromptHint();

    expect(vm.lspPromptHint.value).toBe('custom prompt');
    expect(vm.lspPromptEffectiveHint.value).toBe('effective\nprompt');
    expect(vm.lspPromptDisplayHint.value).toBe('effective\nprompt');
    expect(vm.lspPromptLineCount.value).toBe(2);
    expect(vm.lspPromptCharCount.value).toBe('effective\nprompt'.length);
  });

  it('saves and resets prompt hints through scoped config calls', async () => {
    const { vm } = createPromptSettings();

    apiMock.callAPI.mockResolvedValueOnce({
      hint: 'saved effective',
      defaultHint: 'default prompt',
      overrideHint: 'saved override',
      usingDefault: false,
    });
    vm.lspPromptHint.value = 'saved override';
    await vm.saveLSPPromptHint();
    expect(apiMock.callAPI).toHaveBeenCalledWith('config/lspPromptHint/write', {
      hint: 'saved override',
      cwd: '/repo',
    });
    expect(vm.lspPromptNotice.message).toContain('提示词已保存');

    apiMock.callAPI.mockResolvedValueOnce({
      hint: 'default prompt',
      defaultHint: 'default prompt',
      overrideHint: '',
      usingDefault: true,
    });
    await vm.resetLSPPromptHint();
    expect(vm.lspPromptHint.value).toBe('');
    expect(vm.lspPromptUsingDefault.value).toBe(true);
    expect(vm.lspPromptNotice.message).toContain('已恢复默认提示词');
  });

  it('copies effective prompt text and reports empty content', async () => {
    const { vm } = createPromptSettings();

    await vm.copyEffectivePromptHint();
    expect(vm.lspPromptNotice.message).toContain('暂无可复制内容');

    vm.lspPromptEffectiveHint.value = 'copy me';
    apiMock.copyTextToClipboard.mockResolvedValueOnce(true);
    await vm.copyEffectivePromptHint();
    expect(apiMock.copyTextToClipboard).toHaveBeenCalledWith('copy me');
    expect(vm.lspPromptNotice.message).toContain('已复制生效提示词');
  });

  it('loads and saves injected-prompt visibility using bool preference parsing', async () => {
    const { vm } = createPromptSettings();

    apiMock.callAPI.mockResolvedValueOnce('on');
    await vm.loadLSPPromptHint();

    apiMock.callAPI.mockResolvedValueOnce('true');
    await vm.saveInjectedPromptVisibility();

    vm.showInjectedPromptInChat.value = true;
    apiMock.callAPI.mockResolvedValueOnce({ ok: true });
    await vm.saveInjectedPromptVisibility();
    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/preferences/set', {
      key: 'settings.showInjectedPromptInChat',
      value: true,
      cwd: '/repo',
    });
  });
});
