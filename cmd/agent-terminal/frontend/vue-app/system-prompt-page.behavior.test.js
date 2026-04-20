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

vi.mock('./services/log.js', () => ({
  logDebug: vi.fn(),
  logInfo: vi.fn(),
  logWarn: vi.fn(),
}));

import { SystemPromptPage } from './pages/SystemPromptPage.js';

function createPage(overrides = {}) {
  const props = {
    projectStore: overrides.projectStore ?? { state: { active: '/test-repo' } },
    windowCwd: overrides.windowCwd ?? '/fallback-cwd',
  };
  return { props, vm: SystemPromptPage.setup(props) };
}

beforeEach(() => {
  apiMock.callAPI.mockReset();
  apiMock.copyTextToClipboard.mockReset();
});

describe('SystemPromptPage behavior', () => {
  it('defaults to main tab with editor closed', () => {
    const { vm } = createPage();
    expect(vm.activeTab.value).toBe('main');
    expect(vm.editorOpen.value).toBe(false);
  });

  it('switchTab changes activeTab and clears notice', () => {
    const { vm } = createPage();
    vm.notice.message = 'old';
    vm.switchTab('sub');
    expect(vm.activeTab.value).toBe('sub');
    expect(vm.notice.message).toBe('');
  });

  it('switchTab is noop for same tab', () => {
    const { vm } = createPage();
    vm.notice.message = 'keep';
    vm.switchTab('main');
    expect(vm.notice.message).toBe('keep');
  });

  it('loadPrompts populates promptCards from API', async () => {
    apiMock.callAPI.mockResolvedValueOnce({
      prompts: [
        { id: 'p1', name: 'Prompt A', content: 'hello', agentType: 'main' },
        { id: 'p2', name: 'Prompt B', content: 'world', agentType: 'sub' },
      ],
    });

    const { vm } = createPage();
    await vm.loadPrompts();

    expect(vm.promptCards.value).toHaveLength(2);
    expect(vm.promptCards.value[0].name).toBe('Prompt A');
    expect(apiMock.callAPI).toHaveBeenCalledWith('prompts/list', { cwd: '/test-repo' });
  });

  it('prompts/list 404 enters readonly fallback and hydrates dashboard prompts', async () => {
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {});
    const infoSpy = vi.spyOn(console, 'info').mockImplementation(() => {});
    apiMock.callAPI
      .mockRejectedValueOnce(new Error('404 method not found'))
      .mockResolvedValueOnce({
        commands: [
          { prompt_key: 'dash-1', title: 'Dash Prompt', prompt_text: 'readonly body', agent_key: 'main' },
        ],
      });

    try {
      const { vm } = createPage();
      await vm.loadPrompts();

      expect(vm.fallbackMode.value).toBe(true);
      expect(vm.readonlyReason.value).toContain('已切换为只读模式');
      expect(vm.fallbackSource.value).toBe('dashboard/prompts');
      expect(vm.readonlyBannerText.value).toContain('dashboard/prompts');
      expect(vm.promptCards.value).toEqual([
        expect.objectContaining({
          id: 'dash-1',
          name: 'Dash Prompt',
          content: 'readonly body',
          agentType: 'main',
        }),
      ]);
      expect(SystemPromptPage.template).toContain('data-testid="sp-readonly-banner"');
      expect(warnSpy).toHaveBeenCalledTimes(1);
      expect(infoSpy).not.toHaveBeenCalledWith('[SystemPromptPage] prompts/list recovered, exiting fallback');
    } finally {
      warnSpy.mockRestore();
      infoSpy.mockRestore();
    }
  });

  it('fallback mode disables save/delete write path', async () => {
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {});
    apiMock.callAPI
      .mockRejectedValueOnce(new Error('404 not found'))
      .mockResolvedValueOnce({
        commands: [
          { prompt_key: 'dash-1', title: 'Dash Prompt', prompt_text: 'readonly body', agent_key: 'main' },
        ],
      });

    try {
      const { vm } = createPage();
      await vm.loadPrompts();
      vm.form.name = 'Readonly Prompt';
      vm.form.content = 'No writes';

      await vm.savePrompt();
      await vm.deletePrompt({ id: 'dash-1', name: 'Dash Prompt' });

      const methods = apiMock.callAPI.mock.calls.map(([method]) => method);
      expect(methods).toEqual(['prompts/list', 'dashboard/prompts']);
      expect(SystemPromptPage.template).toContain(':disabled="fallbackMode || loading"');
      expect(SystemPromptPage.template).toContain(':disabled="fallbackMode || Boolean(deletingId)"');
      expect(SystemPromptPage.template).toContain('v-if="!fallbackMode" class="btn btn-primary sp-save-btn"');
    } finally {
      warnSpy.mockRestore();
    }
  });

  it('refresh recovery clears fallback after prompts/list succeeds', async () => {
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {});
    const infoSpy = vi.spyOn(console, 'info').mockImplementation(() => {});
    apiMock.callAPI
      .mockRejectedValueOnce(new Error('404 not found'))
      .mockResolvedValueOnce({
        commands: [
          { prompt_key: 'dash-1', title: 'Dash Prompt', prompt_text: 'readonly body', agent_key: 'main' },
        ],
      })
      .mockResolvedValueOnce({
        prompts: [
          { id: 'p3', name: 'Recovered Prompt', content: 'host body', agentType: 'main' },
        ],
      });

    try {
      const { vm } = createPage();
      await vm.loadPrompts();
      await vm.loadPrompts();

      expect(vm.fallbackMode.value).toBe(false);
      expect(vm.readonlyReason.value).toBe('');
      expect(vm.fallbackSource.value).toBe('');
      expect(vm.promptCards.value[0].name).toBe('Recovered Prompt');
      expect(warnSpy).toHaveBeenCalledTimes(1);
      expect(infoSpy).toHaveBeenCalledWith('[SystemPromptPage] prompts/list recovered, exiting fallback');
    } finally {
      warnSpy.mockRestore();
      infoSpy.mockRestore();
    }
  });

  it('non-404 load errors stay visible without entering fallback', async () => {
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {});
    apiMock.callAPI.mockRejectedValueOnce(new Error('500 internal error'));

    try {
      const { vm } = createPage();
      await vm.loadPrompts();

      expect(vm.fallbackMode.value).toBe(false);
      expect(vm.readonlyReason.value).toBe('');
      expect(vm.fallbackSource.value).toBe('');
      expect(vm.notice.message).toContain('500 internal error');
      expect(warnSpy).not.toHaveBeenCalled();
    } finally {
      warnSpy.mockRestore();
    }
  });

  it('filteredCards filters by activeTab', async () => {
    apiMock.callAPI.mockResolvedValueOnce({
      prompts: [
        { id: 'p1', name: 'Main', content: 'a', agentType: 'main' },
        { id: 'p2', name: 'Sub', content: 'b', agentType: 'sub' },
      ],
    });

    const { vm } = createPage();
    await vm.loadPrompts();

    expect(vm.filteredCards.value).toHaveLength(1);
    expect(vm.filteredCards.value[0].name).toBe('Main');

    vm.switchTab('sub');
    expect(vm.filteredCards.value).toHaveLength(1);
    expect(vm.filteredCards.value[0].name).toBe('Sub');
  });

  it('openCreate clears form and sets create mode', () => {
    const { vm } = createPage();
    vm.form.name = 'old';
    vm.openCreate();
    expect(vm.editorOpen.value).toBe(true);
    expect(vm.editorMode.value).toBe('create');
    expect(vm.form.name).toBe('');
    expect(vm.form.id).toBe('');
  });

  it('openEdit populates form from item', () => {
    const { vm } = createPage();
    vm.openEdit({ id: 'x1', name: 'Test', content: 'Body', description: 'Desc' });
    expect(vm.editorOpen.value).toBe(true);
    expect(vm.editorMode.value).toBe('edit');
    expect(vm.form.id).toBe('x1');
    expect(vm.form.name).toBe('Test');
    expect(vm.form.content).toBe('Body');
  });

  it('closeEditor hides modal', () => {
    const { vm } = createPage();
    vm.openCreate();
    vm.closeEditor();
    expect(vm.editorOpen.value).toBe(false);
  });

  it('savePrompt calls write API with form data', async () => {
    apiMock.callAPI
      .mockResolvedValueOnce({ ok: true }) // write
      .mockResolvedValueOnce({ prompts: [] }); // reload

    const { vm } = createPage();
    vm.openCreate();
    vm.form.name = 'New Prompt';
    vm.form.content = 'System content';
    await vm.savePrompt();

    expect(apiMock.callAPI).toHaveBeenCalledWith('prompts/write', {
      id: '',
      name: 'New Prompt',
      content: 'System content',
      description: '',
      agentType: 'main',
      cwd: '/test-repo',
    });
    expect(vm.editorOpen.value).toBe(false);
  });

  it('savePrompt rejects empty name', async () => {
    const { vm } = createPage();
    vm.openCreate();
    vm.form.name = '';
    await vm.savePrompt();
    expect(vm.notice.message).toContain('请填写提示词名称');
    expect(apiMock.callAPI).not.toHaveBeenCalled();
  });

  it('savePrompt is noop while saving', async () => {
    const { vm } = createPage();
    vm.saving.value = true;
    vm.form.name = 'Test';
    await vm.savePrompt();
    expect(apiMock.callAPI).not.toHaveBeenCalled();
  });

  it('deletePrompt calls delete API and reloads', async () => {
    apiMock.callAPI
      .mockResolvedValueOnce({ ok: true }) // delete
      .mockResolvedValueOnce({ prompts: [] }); // reload

    const { vm } = createPage();
    await vm.deletePrompt({ id: 'd1', name: 'Del' });

    expect(apiMock.callAPI).toHaveBeenCalledWith('prompts/delete', { id: 'd1', cwd: '/test-repo' });
    expect(vm.notice.message).toContain('已删除');
  });

  it('copyPromptContent copies and shows notice', async () => {
    apiMock.copyTextToClipboard.mockResolvedValueOnce(true);

    const { vm } = createPage();
    await vm.copyPromptContent({ content: 'copy me' });

    expect(apiMock.copyTextToClipboard).toHaveBeenCalledWith('copy me');
    expect(vm.notice.message).toContain('已复制');
  });

  it('copyPromptContent reports empty', async () => {
    const { vm } = createPage();
    await vm.copyPromptContent({ content: '' });
    expect(vm.notice.message).toContain('暂无可复制内容');
  });

  it('cwdDisplay falls back to windowCwd', () => {
    const { vm } = createPage({ projectStore: { state: { active: '' } } });
    expect(vm.cwdDisplay.value).toBe('/fallback-cwd');
  });

  it('truncate helper truncates long text', () => {
    const { vm } = createPage();
    expect(vm.truncate('A'.repeat(100))).toHaveLength(81); // 80 + '…'
    expect(vm.truncate('short')).toBe('short');
    expect(vm.truncate('')).toBe('暂无内容');
  });

  it('countStats returns correct line and char counts', () => {
    const { vm } = createPage();
    expect(vm.countStats('a\nb\nc')).toEqual({ lines: 3, chars: 5 });
    expect(vm.countStats('')).toEqual({ lines: 0, chars: 0 });
  });
});
