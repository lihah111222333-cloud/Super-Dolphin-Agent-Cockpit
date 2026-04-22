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

import { SystemPromptPage, isReadonlyFallbackListError, PREF_KEY_ACTIVE_PROMPT, PREF_KEY_CLASSIFIER_ENABLED } from './pages/SystemPromptPage.js';

function createPage(overrides = {}) {
  const props = {
    projectStore: overrides.projectStore ?? { state: { active: '/test-repo' } },
    threadStore: overrides.threadStore ?? null,
    windowCwd: overrides.windowCwd ?? '/fallback-cwd',
  };
  return { props, vm: SystemPromptPage.setup(props) };
}

function createStatusOnlyError(status, message = `status ${status}`) {
  const error = new Error(message);
  error.status = status;
  return error;
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

  it('message-only user not found does not trigger readonly fallback detector', () => {
    expect(isReadonlyFallbackListError(new Error('user not found'))).toBe(false);
  });

  it('code-only -32601 triggers readonly fallback detector', () => {
    expect(isReadonlyFallbackListError({ code: -32601 })).toBe(true);
  });

  it('status-only 404 triggers readonly fallback detector', () => {
    expect(isReadonlyFallbackListError({ status: 404 })).toBe(true);
  });

  it('name-only NotFoundError triggers readonly fallback detector', () => {
    expect(isReadonlyFallbackListError({ name: 'NotFoundError' })).toBe(true);
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

  it('prompts/list 404 enters readonly fallback, disables mutations, and hydrates with cwd', async () => {
    apiMock.callAPI
      .mockRejectedValueOnce(createStatusOnlyError(404, '404 prompts/list not found'))
      .mockResolvedValueOnce({
        prompts: [
          {
            prompt_key: 'readonly-main',
            title: 'Readonly Prompt',
            prompt_text: 'dashboard copy',
            agent_key: 'main',
          },
        ],
      });

    const { vm } = createPage({
      projectStore: { state: { active: '/test-repo', cwd: '/project-cwd' } },
      threadStore: { state: { cwd: '/thread-cwd' } },
    });
    await vm.loadPrompts();

    expect(vm.fallbackMode.value).toBe(true);
    expect(vm.readonlyReason.value).toContain('404 prompts/list not found');
    expect(vm.fallbackSource.value).toBe('dashboard/prompts');
    expect(vm.readonlyBannerMessage.value).toContain('只读模式');
    expect(vm.createDisabled.value).toBe(true);
    expect(vm.saveDisabled.value).toBe(true);
    expect(vm.deleteDisabled.value).toBe(true);
    expect(vm.promptCards.value[0].name).toBe('Readonly Prompt');
    expect(apiMock.callAPI.mock.calls[1]).toEqual(['dashboard/prompts', { cwd: '/thread-cwd' }]);

    vm.openCreate();
    expect(vm.editorOpen.value).toBe(false);

    vm.openEdit(vm.promptCards.value[0]);
    expect(vm.editorViewOnly.value).toBe(true);
  });

  it('loadPrompts rethrows message-only user not found errors without readonly fallback', async () => {
    apiMock.callAPI.mockRejectedValueOnce(new Error('user not found'));

    const { vm } = createPage();

    await expect(vm.loadPrompts()).rejects.toThrow('user not found');
    expect(vm.fallbackMode.value).toBe(false);
    expect(vm.readonlyReason.value).toBe('');
    expect(vm.notice.message).toContain('加载失败');
    expect(apiMock.callAPI).toHaveBeenCalledTimes(1);
  });

  it('loadPrompts rethrows non-404 errors', async () => {
    apiMock.callAPI.mockRejectedValueOnce(new Error('boom'));

    const { vm } = createPage();

    await expect(vm.loadPrompts()).rejects.toThrow('boom');
    expect(vm.fallbackMode.value).toBe(false);
    expect(vm.readonlyReason.value).toBe('');
    expect(vm.notice.message).toContain('加载失败');
    expect(apiMock.callAPI).toHaveBeenCalledTimes(1);
  });

  it('successful prompts/list clears fallback state after recovery', async () => {
    apiMock.callAPI
      .mockRejectedValueOnce(createStatusOnlyError(404, '404 prompts/list not found'))
      .mockResolvedValueOnce({
        prompts: [
          {
            prompt_key: 'readonly-main',
            title: 'Readonly Prompt',
            prompt_text: 'dashboard copy',
            agent_key: 'main',
          },
        ],
      })
      .mockResolvedValueOnce({
        prompts: [
          { id: 'live-main', name: 'Recovered Prompt', content: 'live content', agentType: 'main' },
        ],
      });

    const { vm } = createPage();
    await vm.loadPrompts();
    expect(vm.fallbackMode.value).toBe(true);

    await vm.loadPrompts();

    expect(vm.fallbackMode.value).toBe(false);
    expect(vm.readonlyReason.value).toBe('');
    expect(vm.fallbackSource.value).toBe('');
    expect(vm.createDisabled.value).toBe(false);
    expect(vm.saveDisabled.value).toBe(false);
    expect(vm.deleteDisabled.value).toBe(false);
    expect(vm.promptCards.value[0].name).toBe('Recovered Prompt');

    vm.openEdit(vm.promptCards.value[0]);
    expect(vm.editorViewOnly.value).toBe(false);
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

  it('setLaunchPrompt persists prompt id under cwd-scoped preference', async () => {
    apiMock.callAPI.mockResolvedValueOnce({ ok: true });

    const { vm } = createPage();
    await vm.setLaunchPrompt({ id: 'main/launch-fav', name: 'Launch Fav' });

    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/preferences/set', {
      key: PREF_KEY_ACTIVE_PROMPT,
      value: 'main/launch-fav',
      cwd: '/test-repo',
    });
    expect(vm.activePromptId.value).toBe('main/launch-fav');
    expect(vm.notice.message).toContain('已设为启动提示词');
  });

  it('clearLaunchPrompt writes empty value and resets active id', async () => {
    apiMock.callAPI
      .mockResolvedValueOnce({ ok: true }) // initial set
      .mockResolvedValueOnce({ ok: true }); // clear

    const { vm } = createPage();
    await vm.setLaunchPrompt({ id: 'main/launch-fav', name: 'Launch Fav' });
    await vm.clearLaunchPrompt();

    expect(apiMock.callAPI).toHaveBeenLastCalledWith('ui/preferences/set', {
      key: PREF_KEY_ACTIVE_PROMPT,
      value: '',
      cwd: '/test-repo',
    });
    expect(vm.activePromptId.value).toBe('');
    expect(vm.notice.message).toContain('已取消启动');
  });

  it('loadActivePromptId hydrates from preference get', async () => {
    apiMock.callAPI.mockResolvedValueOnce('main/launch-fav');

    const { vm } = createPage();
    const got = await vm.loadActivePromptId();

    expect(got).toBe('main/launch-fav');
    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/preferences/get', {
      key: PREF_KEY_ACTIVE_PROMPT,
      cwd: '/test-repo',
    });
    expect(vm.activePromptId.value).toBe('main/launch-fav');
  });

  it('setLaunchPrompt is a no-op in readonly fallback', async () => {
    const { vm } = createPage();
    vm.fallbackMode.value = true;
    await vm.setLaunchPrompt({ id: 'main/launch-fav' });
    expect(apiMock.callAPI).not.toHaveBeenCalled();
    expect(vm.notice.message).toContain('只读降级');
  });

  it('toggleClassifier persists enabled=true under cwd-scoped preference', async () => {
    apiMock.callAPI.mockResolvedValueOnce({ ok: true });

    const { vm } = createPage();
    await vm.toggleClassifier(true);

    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/preferences/set', {
      key: PREF_KEY_CLASSIFIER_ENABLED,
      value: true,
      cwd: '/test-repo',
    });
    expect(vm.classifierEnabled.value).toBe(true);
    expect(vm.notice.message).toContain('已开启智能启动');
  });

  it('toggleClassifier persists enabled=false and updates notice', async () => {
    apiMock.callAPI
      .mockResolvedValueOnce({ ok: true }) // enable
      .mockResolvedValueOnce({ ok: true }); // disable

    const { vm } = createPage();
    await vm.toggleClassifier(true);
    await vm.toggleClassifier(false);

    expect(apiMock.callAPI).toHaveBeenLastCalledWith('ui/preferences/set', {
      key: PREF_KEY_CLASSIFIER_ENABLED,
      value: false,
      cwd: '/test-repo',
    });
    expect(vm.classifierEnabled.value).toBe(false);
    expect(vm.notice.message).toContain('已关闭智能启动');
  });

  it('loadClassifierEnabled hydrates true from preference get', async () => {
    apiMock.callAPI.mockResolvedValueOnce(true);

    const { vm } = createPage();
    const got = await vm.loadClassifierEnabled();

    expect(got).toBe(true);
    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/preferences/get', {
      key: PREF_KEY_CLASSIFIER_ENABLED,
      cwd: '/test-repo',
    });
    expect(vm.classifierEnabled.value).toBe(true);
  });

  it('loadClassifierEnabled defaults to false when preference missing', async () => {
    apiMock.callAPI.mockResolvedValueOnce(null);

    const { vm } = createPage();
    const got = await vm.loadClassifierEnabled();

    expect(got).toBe(false);
    expect(vm.classifierEnabled.value).toBe(false);
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
