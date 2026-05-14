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
  it('defaults to all tab with editor closed', () => {
    const { vm } = createPage();
    expect(vm.activeTab.value).toBe('all');
    expect(vm.editorOpen.value).toBe(false);
  });

  it('switchTab changes activeTab and clears notice', () => {
    const { vm } = createPage();
    vm.notice.message = 'old';
    vm.switchTab('coder');
    expect(vm.activeTab.value).toBe('coder');
    expect(vm.notice.message).toBe('');
  });

  it('switchTab is noop for same tab', () => {
    const { vm } = createPage();
    vm.notice.message = 'keep';
    vm.switchTab('all');
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
        { id: 'p1', name: 'Coder Prompt', content: 'a', agentType: 'coder' },
        { id: 'p2', name: 'PM Prompt', content: 'b', agentType: 'pm' },
      ],
    });

    const { vm } = createPage();
    await vm.loadPrompts();

    // default tab is 'all' — shows everything
    expect(vm.filteredCards.value).toHaveLength(2);

    vm.switchTab('coder');
    expect(vm.filteredCards.value).toHaveLength(1);
    expect(vm.filteredCards.value[0].name).toBe('Coder Prompt');

    vm.switchTab('pm');
    expect(vm.filteredCards.value).toHaveLength(1);
    expect(vm.filteredCards.value[0].name).toBe('PM Prompt');
  });

  it('TestSystemPromptPageContentTextareaDisabledInFallback', () => {
    expect(SystemPromptPage.template).toContain(':disabled="saving || fallbackMode || editingHasSections"');
    expect(SystemPromptPage.template).toContain("{{ fallbackMode ? '只读模式'");
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
    // Readonly editor is driven directly by fallbackMode.
    expect(vm.fallbackMode.value).toBe(true);
  });

  it('fallback hydrate should send projectStore.state.active when threadStore has no cwd', async () => {
    apiMock.callAPI
      .mockRejectedValueOnce(createStatusOnlyError(404, '404 prompts/list not found'))
      .mockResolvedValueOnce({ prompts: [] });

    const { vm } = createPage({
      projectStore: { state: { active: '/active-repo', cwd: '/legacy-cwd' } },
      threadStore: { state: {} },
    });
    await vm.loadPrompts();

    expect(apiMock.callAPI.mock.calls[1]).toEqual(['dashboard/prompts', { cwd: '/active-repo' }]);
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
    // createDisabled is true when activeTab==='all'; switch to a role first
    vm.switchTab('coder');
    expect(vm.createDisabled.value).toBe(false);
    expect(vm.saveDisabled.value).toBe(false);
    expect(vm.deleteDisabled.value).toBe(false);
    expect(vm.promptCards.value[0].name).toBe('Recovered Prompt');

    vm.openEdit(vm.promptCards.value[0]);
    expect(vm.fallbackMode.value).toBe(false);
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
      priority: 0,
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
    expect(vm.notice.message).toContain('已设为强制使用');
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
    expect(vm.notice.message).toContain('已取消强制使用');
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

  it('basic user flow: select role + add tags + save auto-generates match_when and sends tags', async () => {
    // 1. Load page, switch to coder role, open create
    apiMock.callAPI.mockResolvedValueOnce({ prompts: [] }); // initial load
    const { vm } = createPage();
    await vm.loadPrompts();
    vm.switchTab('coder');
    vm.openCreate();

    // 2. Fill basic fields (no advanced settings)
    vm.form.name = '代码审查专家';
    vm.form.description = '帮你审查代码质量';
    vm.form.agentKey = 'coder';
    vm.form.tags = ['代码审查', 'bug'];
    vm.form.content = '你是一位资深代码审查专家。';
    // user does NOT touch matchWhen or priority (stays default)
    expect(vm.form.matchWhen).toBe('');
    expect(vm.form.priority).toBe(0);

    // 3. Save
    apiMock.callAPI
      .mockResolvedValueOnce({ prompt: { id: 'new-1', name: '代码审查专家' } }) // prompts/write
      .mockResolvedValueOnce({ prompts: [{ id: 'new-1', name: '代码审查专家', content: '你是一位资深代码审查专家。', agentType: 'coder', tags: '["代码审查","bug"]' }] }); // reload
    await vm.savePrompt();

    // 4. Verify the write call
    const writeCalls = apiMock.callAPI.mock.calls.filter(c => c[0] === 'prompts/write');
    expect(writeCalls).toHaveLength(1);
    const payload = writeCalls[0][1];

    // tags sent
    expect(payload.tags).toEqual(['代码审查', 'bug']);
    // match_when auto-generated from tags (not manually set)
    expect(payload.match_when).toEqual({ tags_has: ['代码审查', 'bug'] });
    // agentType from role selection
    expect(payload.agentType).toBe('coder');
    // priority stays default
    expect(payload.priority).toBe(0);
    // editor closed after save
    expect(vm.editorOpen.value).toBe(false);
  });

  it('basic user flow: single tag generates string match_when instead of array', async () => {
    apiMock.callAPI.mockResolvedValueOnce({ prompts: [] });
    const { vm } = createPage();
    await vm.loadPrompts();
    vm.switchTab('pm');
    vm.openCreate();

    vm.form.name = '需求分析';
    vm.form.agentKey = 'pm';
    vm.form.tags = ['需求'];
    vm.form.content = '你是产品经理。';

    apiMock.callAPI
      .mockResolvedValueOnce({ prompt: {} })
      .mockResolvedValueOnce({ prompts: [] });
    await vm.savePrompt();

    const payload = apiMock.callAPI.mock.calls.find(c => c[0] === 'prompts/write')[1];
    // single tag => string, not array
    expect(payload.match_when).toEqual({ tags_has: '需求' });
    expect(payload.agentType).toBe('pm');
  });

  it('basic user flow: no tags means no match_when', async () => {
    apiMock.callAPI.mockResolvedValueOnce({ prompts: [] });
    const { vm } = createPage();
    await vm.loadPrompts();
    vm.switchTab('coder');
    vm.openCreate();

    vm.form.name = '简单提示词';
    vm.form.agentKey = 'coder';
    vm.form.content = '你好。';
    // no tags, no matchWhen

    apiMock.callAPI
      .mockResolvedValueOnce({ prompt: {} })
      .mockResolvedValueOnce({ prompts: [] });
    await vm.savePrompt();

    const payload = apiMock.callAPI.mock.calls.find(c => c[0] === 'prompts/write')[1];
    expect(payload.match_when).toBeUndefined();
    expect(payload.tags).toBeUndefined();
  });

  it('openEdit + tag change without touching matchWhen regenerates match_when from new tags', async () => {
    // Regression: 之前 openEdit 把现有 match_when 回填到 textarea 后，
    // 即使用户只改 tags，保存仍把"回填值"原样写回，导致 tags 与 match_when 脱节。
    const existing = {
      id: 'coder/prompt',
      name: '编码执行代理',
      content: '...',
      agentType: 'coder',
      tags: ['old-long-tag'],
      match_when: { tags_has: ['old-long-tag'] },
    };
    apiMock.callAPI.mockResolvedValueOnce({ prompts: [existing] });
    const { vm } = createPage();
    await vm.loadPrompts();
    vm.openEdit(existing);

    // sanity: matchWhen textarea 被回填，但 dirty flag 应为 false
    expect(vm.form.matchWhen).toContain('old-long-tag');
    expect(vm.matchWhenDirty.value).toBe(false);

    // 用户只改 tags，从不动 textarea
    vm.form.tags = ['代码', 'bug'];

    apiMock.callAPI
      .mockResolvedValueOnce({ prompt: {} })
      .mockResolvedValueOnce({ prompts: [existing] });
    await vm.savePrompt();

    const payload = apiMock.callAPI.mock.calls.find(c => c[0] === 'prompts/write')[1];
    // match_when 必须用"当前 tags"生成，不能是旧的回填值
    expect(payload.match_when).toEqual({ tags_has: ['代码', 'bug'] });
    expect(payload.tags).toEqual(['代码', 'bug']);
  });

  it('openEdit + user edits matchWhen textarea keeps user value (dirty=true path)', async () => {
    const existing = {
      id: 'coder/prompt',
      name: '编码执行代理',
      content: '...',
      agentType: 'coder',
      tags: ['代码'],
      match_when: { tags_has: ['代码'] },
    };
    apiMock.callAPI.mockResolvedValueOnce({ prompts: [existing] });
    const { vm } = createPage();
    await vm.loadPrompts();
    vm.openEdit(existing);

    // 模拟用户敲键盘改 matchWhen textarea
    vm.form.matchWhen = '{"tags_has":["手动定制"]}';
    vm.matchWhenDirty.value = true;

    apiMock.callAPI
      .mockResolvedValueOnce({ prompt: {} })
      .mockResolvedValueOnce({ prompts: [existing] });
    await vm.savePrompt();

    const payload = apiMock.callAPI.mock.calls.find(c => c[0] === 'prompts/write')[1];
    expect(payload.match_when).toEqual({ tags_has: ['手动定制'] });
  });
});
