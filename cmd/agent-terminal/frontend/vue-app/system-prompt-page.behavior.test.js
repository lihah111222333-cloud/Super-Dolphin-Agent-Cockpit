// @ts-nocheck
import { beforeEach, describe, expect, it, vi } from 'vitest';

const apiMock = vi.hoisted(() => ({
  callAPI: vi.fn(),
  copyTextToClipboard: vi.fn(),
  onFilesDropped: vi.fn(() => () => {}),
  readDroppedTextFiles: vi.fn(),
}));

vi.mock('./services/api.js', () => ({
  callAPI: apiMock.callAPI,
  copyTextToClipboard: apiMock.copyTextToClipboard,
  onFilesDropped: apiMock.onFilesDropped,
  readDroppedTextFiles: apiMock.readDroppedTextFiles,
}));

vi.mock('./services/log.js', () => ({
  logDebug: vi.fn(),
  logInfo: vi.fn(),
  logWarn: vi.fn(),
}));

import { SystemPromptPage, isReadonlyFallbackListError, PREF_KEY_ACTIVE_PROMPT } from './pages/SystemPromptPage.js';

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
  apiMock.onFilesDropped.mockReset().mockReturnValue(() => {});
  apiMock.readDroppedTextFiles.mockReset();
});

describe('SystemPromptPage behavior', () => {
  it('registers PromptIntentWizard and keeps SectionsEditor behind hidden advanced debug', () => {
    expect(SystemPromptPage.components.PromptIntentWizard).toBeTruthy();
    expect(SystemPromptPage.components.SectionsEditor).toBeTruthy();
    expect(SystemPromptPage.template).toContain('AI 能力与资料');
    expect(SystemPromptPage.template).not.toContain('内置提示词');
    expect(SystemPromptPage.template).not.toContain('系统内置');
    expect(SystemPromptPage.template).not.toContain('builtin');
    expect(SystemPromptPage.template).toContain('<prompt-intent-wizard');
    expect(SystemPromptPage.template).toContain('data-testid="sp-advanced-debug"');
    expect(SystemPromptPage.template).toContain('<sections-editor');
    expect(SystemPromptPage.template).toContain(':prompt-id="form.id"');
    expect(SystemPromptPage.template).toContain('v-if="advancedDebugAvailable"');
    expect(SystemPromptPage.template).toContain('v-if="advancedDebugOpen"');
    expect(SystemPromptPage.template).toContain(':visible="advancedDebugOpen"');
    expect(SystemPromptPage.template).not.toContain('data-testid="sp-content-input"');
    expect(SystemPromptPage.template).not.toContain('data-testid="sp-editor-sections-banner"');
    expect(SystemPromptPage.template).toContain(':fallback-mode="fallbackMode || !currentProjectCwd"');
  });

  it('allows ordinary semantic edits while keeping advanced debug hidden by default', () => {
    const { vm } = createPage();

    expect(vm.advancedDebugAvailable.value).toBe(false);
    expect(vm.editorReadonly.value).toBe(false);
    expect(vm.editButtonCopy({}, false, vm.advancedDebugAvailable.value)).toBe('编辑');
    expect(vm.editorTitleCopy(false, 'edit', vm.advancedDebugAvailable.value)).toBe('编辑提示词');
    expect(SystemPromptPage.template).toContain('data-testid="sp-save-btn"');
    expect(SystemPromptPage.template).not.toContain('data-testid="sp-save-btn" v-if="advancedDebugAvailable"');
    expect(SystemPromptPage.template).toContain(':disabled="saving || fallbackMode"');
    expect(SystemPromptPage.template).toContain('data-testid="sp-execution-input"');
    expect(SystemPromptPage.template).toContain('AI 使用时怎么做');

    vm.advancedDebugAvailable.value = true;
    expect(vm.editorReadonly.value).toBe(false);
    expect(vm.editButtonCopy({}, false, vm.advancedDebugAvailable.value)).toBe('编辑');
    expect(vm.editorTitleCopy(false, 'edit', vm.advancedDebugAvailable.value)).toBe('编辑提示词');
  });

  it('openEdit maps when_to_use into the metadata form', async () => {
    apiMock.callAPI.mockResolvedValueOnce({
      prompts: [
        {
          id: 'coder/prompt',
          name: 'Coder',
          content: 'assembled preview',
          agentType: 'coder',
          when_to_use: 'Use for coding implementation tasks.',
        },
      ],
    });

    const { vm } = createPage();
    await vm.loadPrompts();
    vm.openEdit(vm.promptCards.value[0]);

    expect(vm.form.whenToUse).toBe('Use for coding implementation tasks.');
  });

  it('savePrompt writes metadata with when_to_use and changed execution content', async () => {
    apiMock.callAPI
      .mockResolvedValueOnce({ prompt: { id: 'new-prompt', name: 'New Prompt' } })
      .mockResolvedValueOnce({ prompts: [] });
    const { vm } = createPage();

    vm.editorMode.value = 'create';
    vm.editorOpen.value = true;
    vm.form.name = 'New Prompt';
    vm.form.originalContent = 'previous execution description';
    vm.form.content = 'When delegated, inspect the diff and report blocking findings first.';
    vm.form.whenToUse = 'Use when a coding task needs delegation.';
    await vm.savePrompt();

    expect(apiMock.callAPI).toHaveBeenCalledWith('prompts/write', {
      id: '',
      name: 'New Prompt',
      description: '',
      agentType: 'main',
      priority: 0,
      when_to_use: 'Use when a coding task needs delegation.',
      content: 'When delegated, inspect the diff and report blocking findings first.',
      tags: [],
      enabled: true,
      scope: 'project',
      cwd: '/test-repo',
    });
    expect(vm.form.id).toBe('new-prompt');
    expect(vm.editorMode.value).toBe('edit');
    expect(vm.editorOpen.value).toBe(true);
  });

  it('defaults to all tab with editor closed', () => {
    const { vm } = createPage();
    expect(vm.activeTab.value).toBe('all');
    expect(vm.editorOpen.value).toBe(false);
  });

  it('switchTab changes activeTab and clears notice', () => {
    const { vm } = createPage();
    vm.notice.message = 'old';
    vm.switchTab('expert');
    expect(vm.activeTab.value).toBe('expert');
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
    expect(apiMock.callAPI).toHaveBeenCalledWith('prompt-assets/list', { cwd: '/test-repo' });
  });

  it('loadPrompts uses user asset list and marks ready drafts as pending confirmation', async () => {
    apiMock.callAPI.mockResolvedValueOnce({
      prompts: [
        {
          id: 'intent/recall/ready',
          draft_key: 'intent/recall/ready',
          name: '价格表资料',
          description: '从 Excel 价格表整理出的资料',
          content: '',
          agentType: 'main',
          tags: '["intent:recall","pricing"]',
          state: 'pending_confirm',
          draft_status: 'ready_to_save',
        },
      ],
    });

    const { vm } = createPage();
    await vm.loadPrompts();

    expect(apiMock.callAPI).toHaveBeenCalledWith('prompt-assets/list', { cwd: '/test-repo' });
    expect(vm.promptCards.value[0]).toMatchObject({
      id: 'intent/recall/ready',
      draftKey: 'intent/recall/ready',
      assetType: 'recall',
      state: 'pending_confirm',
      draftStatus: 'ready_to_save',
      isPendingDraft: true,
    });
    expect(SystemPromptPage.template).toContain('待确认');
    expect(SystemPromptPage.template).toContain('item.isPendingDraft');
  });

  it('uses window cwd for prompt APIs and intent wizard when active project is dot', async () => {
    apiMock.callAPI
      .mockResolvedValueOnce({ prompts: [] })
      .mockResolvedValueOnce({ prompt: { id: 'new-prompt', name: 'New Prompt' } })
      .mockResolvedValueOnce({ prompts: [] });
    const { vm } = createPage({
      projectStore: { state: { active: '.' } },
      windowCwd: '/repo-root',
    });

    expect(vm.currentProjectCwd.value).toBe('/repo-root');
    await vm.loadPrompts();
    expect(apiMock.callAPI).toHaveBeenCalledWith('prompt-assets/list', { cwd: '/repo-root' });

    vm.editorMode.value = 'create';
    vm.editorOpen.value = true;
    vm.form.name = 'New Prompt';
    await vm.savePrompt();

    expect(apiMock.callAPI).toHaveBeenCalledWith('prompts/write', expect.objectContaining({
      name: 'New Prompt',
      cwd: '/repo-root',
    }));
    expect(apiMock.callAPI).toHaveBeenLastCalledWith('prompt-assets/list', { cwd: '/repo-root' });
  });

  it('does not send dot cwd or allow mutation while cwd is unresolved', async () => {
    const { vm } = createPage({
      projectStore: { state: { active: '.' } },
      windowCwd: '.',
    });
    vm.promptCards.value = [{ id: 'stale', name: 'Stale Prompt' }];

    expect(vm.currentProjectCwd.value).toBe('');
    expect(vm.cwdDisplay.value).toBe('未知');
    expect(vm.createDisabled.value).toBe(true);
    expect(vm.saveDisabled.value).toBe(true);
    expect(vm.deleteDisabled.value).toBe(true);

    await vm.loadPrompts();
    expect(vm.promptCards.value).toEqual([]);
    expect(vm.fallbackMode.value).toBe(true);
    expect(vm.readonlyReason.value).toBe('cwd unresolved');
    expect(apiMock.callAPI).not.toHaveBeenCalled();

    vm.openCreate();
    expect(vm.intentWizardOpen.value).toBe(false);
    expect(vm.notice.message).toContain('只读降级');

    vm.editorMode.value = 'create';
    vm.editorOpen.value = true;
    vm.form.name = 'New Prompt';
    await vm.savePrompt();

    await vm.copyPromptContent({ id: 'stale', content: 'stale body' });

    expect(apiMock.callAPI).not.toHaveBeenCalled();
    expect(vm.notice.message).toContain('暂无可复制内容');
  });

  it('disabled prompt cards render as disabled and do not use discoverable copy', async () => {
    apiMock.callAPI.mockResolvedValueOnce({
      prompts: [
        { id: 'p1', name: 'Disabled Prompt', content: 'preview', agentType: 'coder', enabled: false },
      ],
    });
    const { vm } = createPage();
    await vm.loadPrompts();

    expect(vm.promptCards.value[0].enabled).toBe(false);
    expect(vm.promptCards.value[0].preview).toBe('preview');
    expect(SystemPromptPage.template).toContain("'is-disabled': item.enabled === false && !item.isPendingDraft");
    expect(SystemPromptPage.template).toContain('已停用');
    expect(SystemPromptPage.template).not.toContain('禁用后仍会被 AI 发现');
  });

  it('renders recall asset cards with description instead of empty prompt content', async () => {
    apiMock.callAPI.mockResolvedValueOnce({
      prompts: [
        {
          id: 'main/knowledge/sqlc',
          name: 'SQLC 资料',
          content: '',
          description: 'SQLC migration 资料',
          agentType: 'main',
          tags: '["scope.cwd:/repo","intent:recall","sqlc"]',
        },
      ],
    });
    const { vm } = createPage();
    await vm.loadPrompts();

    expect(vm.promptCards.value[0].assetType).toBe('recall');
    expect(vm.promptCards.value[0].tags).toEqual(['sqlc']);
    expect(vm.promptCards.value[0].preview).toBe('SQLC migration 资料');
  });

  it('renders default rule cards as project rules and does not show blank preview', async () => {
    apiMock.callAPI.mockResolvedValueOnce({
      prompts: [
        {
          id: 'main/default-rule/db',
          name: '数据库规则',
          content: '',
          description: '',
          when_to_use: '',
          agent_key: 'default_rule',
          tags: '["scope.cwd:/repo","intent:default_rule","database"]',
        },
      ],
    });
    const { vm } = createPage();
    await vm.loadPrompts();

    expect(vm.promptCards.value[0].assetType).toBe('default_rule');
    expect(vm.promptCards.value[0].tags).toEqual(['database']);
    expect(vm.promptCards.value[0].preview).toBe('已保存，AI 会在相关场景中使用');
    expect(SystemPromptPage.template).toContain('默认规则');
  });

  it('prompt-assets/list 404 enters readonly fallback, disables mutations, and hydrates with cwd', async () => {
    apiMock.callAPI
      .mockRejectedValueOnce(createStatusOnlyError(404, '404 prompt-assets/list not found'))
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
    expect(vm.readonlyReason.value).toContain('404 prompt-assets/list not found');
    expect(vm.fallbackSource.value).toBe('dashboard/prompts');
    expect(vm.readonlyBannerMessage.value).toContain('只读模式');
    expect(vm.createDisabled.value).toBe(true);
    expect(vm.saveDisabled.value).toBe(true);
    expect(vm.deleteDisabled.value).toBe(true);
    expect(vm.promptCards.value[0].name).toBe('Readonly Prompt');
    expect(apiMock.callAPI.mock.calls[1]).toEqual(['dashboard/prompts', { cwd: '/test-repo' }]);

    vm.openCreate();
    expect(vm.intentWizardOpen.value).toBe(false);
    expect(vm.editorOpen.value).toBe(false);

    vm.openEdit(vm.promptCards.value[0]);
    // Readonly editor is driven directly by fallbackMode.
    expect(vm.fallbackMode.value).toBe(true);
  });

  it('fallback hydrate should send projectStore.state.active when threadStore has no cwd', async () => {
    apiMock.callAPI
      .mockRejectedValueOnce(createStatusOnlyError(404, '404 prompt-assets/list not found'))
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

  it('successful prompt-assets/list clears fallback state after recovery', async () => {
    apiMock.callAPI
      .mockRejectedValueOnce(createStatusOnlyError(404, '404 prompt-assets/list not found'))
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
    expect(vm.fallbackMode.value).toBe(false);
  });

  it('create button is enabled on all tab outside fallback mode', () => {
    const { vm } = createPage();
    expect(vm.activeTab.value).toBe('all');
    expect(vm.createDisabled.value).toBe(false);
  });

  it('create opens intent wizard instead of raw editor', () => {
    const { vm } = createPage();
    vm.form.name = 'old';
    vm.openCreate();
    expect(vm.intentWizardOpen.value).toBe(true);
    expect(vm.editorOpen.value).toBe(false);
    expect(vm.editorMode.value).toBe('edit');
    expect(vm.form.name).toBe('old');
  });

  it('intent saved closes wizard, reloads prompts, and shows notice', async () => {
    apiMock.callAPI.mockResolvedValueOnce({ prompts: [] });
    const { vm } = createPage();
    vm.intentWizardOpen.value = true;

    await vm.handleIntentSaved();

    expect(vm.intentWizardOpen.value).toBe(false);
    expect(apiMock.callAPI).toHaveBeenCalledWith('prompt-assets/list', { cwd: '/test-repo' });
    expect(vm.notice.message).toContain('已保存');
  });

  it('openEdit populates form from item', () => {
    const { vm } = createPage();
    vm.openEdit({ id: 'x1', name: 'Test', content: 'Body', description: 'Desc', enabled: false });
    expect(vm.editorOpen.value).toBe(true);
    expect(vm.editorMode.value).toBe('edit');
    expect(vm.form.id).toBe('x1');
    expect(vm.form.name).toBe('Test');
    expect(vm.form.content).toBe('Body');
    expect(vm.form.enabled).toBe(false);
  });

  it('ordinary edit toggles enabled and sends enabled in prompts/write payload', async () => {
    const existing = {
      id: 'coder/prompt',
      name: '编码执行代理',
      content: 'preview',
      agentType: 'coder',
      enabled: false,
    };
    apiMock.callAPI.mockResolvedValueOnce({ prompts: [existing] });
    const { vm } = createPage();
    await vm.loadPrompts();
    vm.openEdit(vm.promptCards.value[0]);
    vm.form.enabled = true;

    apiMock.callAPI
      .mockResolvedValueOnce({ prompt: {} })
      .mockResolvedValueOnce({ prompts: [existing] });
    await vm.savePrompt();

    const payload = apiMock.callAPI.mock.calls.find(c => c[0] === 'prompts/write')[1];
    expect(payload.enabled).toBe(true);
    expect(payload).not.toHaveProperty('content');
  });

  it('closeEditor hides modal', () => {
    const { vm } = createPage();
    vm.openEdit({ id: 'x1', name: 'Test' });
    vm.closeEditor();
    expect(vm.editorOpen.value).toBe(false);
  });

  it('savePrompt create keeps the editor open with returned prompt id for section editing', async () => {
    apiMock.callAPI
      .mockResolvedValueOnce({ prompt: { id: 'created-1', name: 'New Prompt' } }) // write
      .mockResolvedValueOnce({ prompts: [] }); // reload

    const { vm } = createPage();
    vm.editorMode.value = 'create';
    vm.editorOpen.value = true;
    vm.form.name = 'New Prompt';
    vm.form.whenToUse = 'Use when writing code.';
    await vm.savePrompt();

    expect(apiMock.callAPI).toHaveBeenCalledWith('prompts/write', {
      id: '',
      name: 'New Prompt',
      description: '',
      agentType: 'main',
      priority: 0,
      when_to_use: 'Use when writing code.',
      tags: [],
      enabled: true,
      scope: 'project',
      cwd: '/test-repo',
    });
    expect(apiMock.callAPI.mock.calls[0][1]).not.toHaveProperty('content');
    expect(vm.form.id).toBe('created-1');
    expect(vm.editorMode.value).toBe('edit');
    expect(vm.editorOpen.value).toBe(true);
  });

  it('savePrompt rejects empty name', async () => {
    const { vm } = createPage();
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

    expect(apiMock.callAPI).toHaveBeenCalledWith('prompts/delete', { id: 'd1', scope: 'project', cwd: '/test-repo' });
    expect(vm.notice.message).toContain('已删除');
  });

  it('copyPromptContent copies and shows notice', async () => {
    apiMock.callAPI.mockResolvedValueOnce({ prompt: { content: 'copy me full body' } });
    apiMock.copyTextToClipboard.mockResolvedValueOnce(true);

    const { vm } = createPage();
    await vm.copyPromptContent({ id: 'main/sectioned', content: 'copy me preview' });

    expect(apiMock.callAPI).toHaveBeenCalledWith('prompts/get', { id: 'main/sectioned', cwd: '/test-repo' });
    expect(apiMock.copyTextToClipboard).toHaveBeenCalledWith('copy me full body');
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

  it('setLaunchPrompt only allows enabled expert assets', async () => {
    const { vm } = createPage();

    await vm.setLaunchPrompt({ id: 'main/knowledge/sqlc', name: 'SQLC 资料', assetType: 'recall', enabled: true });
    expect(apiMock.callAPI).not.toHaveBeenCalled();
    expect(vm.notice.message).toContain('只有启用中的专家能力');

    await vm.setLaunchPrompt({ id: 'main/expert/disabled', name: '停用专家', assetType: 'expert', enabled: false });
    expect(apiMock.callAPI).not.toHaveBeenCalled();
    expect(vm.notice.message).toContain('只有启用中的专家能力');
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

  it('loadActivePromptId clears stale 0105 legacy active prompt preference', async () => {
    apiMock.callAPI
      .mockResolvedValueOnce({
        prompts: [
          { id: 'main/knowledge/sqlc', name: 'SQLC 资料', tags: '["intent:recall"]', enabled: true },
        ],
      })
      .mockResolvedValueOnce('main/general-en')
      .mockResolvedValueOnce({ ok: true });

    const { vm } = createPage();
    await vm.loadPrompts();
    const got = await vm.loadActivePromptId();

    expect(got).toBe('');
    expect(vm.activePromptId.value).toBe('');
    expect(apiMock.callAPI).toHaveBeenLastCalledWith('ui/preferences/set', {
      key: PREF_KEY_ACTIVE_PROMPT,
      value: '',
      cwd: '/test-repo',
    });
  });

  it('setLaunchPrompt is a no-op in readonly fallback', async () => {
    const { vm } = createPage();
    vm.fallbackMode.value = true;
    await vm.setLaunchPrompt({ id: 'main/launch-fav' });
    expect(apiMock.callAPI).not.toHaveBeenCalled();
    expect(vm.notice.message).toContain('只读降级');
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

  it('basic user flow: semantic category selection does not become agentType', async () => {
    // 1. Load page, switch to expert asset tab, open manual editor path used by tests.
    apiMock.callAPI.mockResolvedValueOnce({ prompts: [] }); // initial load
    const { vm } = createPage();
    await vm.loadPrompts();
    vm.switchTab('expert');
    vm.editorMode.value = 'create';
    vm.editorOpen.value = true;

    // 2. Fill basic fields (no advanced settings)
    vm.form.name = '代码审查专家';
    vm.form.description = '帮你审查代码质量';
    vm.form.whenToUse = 'Use for code review tasks.';
    vm.form.agentKey = '';
    vm.form.tags = ['代码审查', 'bug'];
    // user does NOT touch matchWhen or priority (stays default)
    expect(vm.form.matchWhen).toBe('');
    expect(vm.form.priority).toBe(0);

    // 3. Save
    apiMock.callAPI
      .mockResolvedValueOnce({ prompt: { id: 'new-1', name: '代码审查专家' } }) // prompts/write
      .mockResolvedValueOnce({ prompts: [{ id: 'new-1', name: '代码审查专家', content: 'sections preview', agentType: 'coder', tags: '["代码审查","bug"]' }] }); // reload
    await vm.savePrompt();

    // 4. Verify the write call
    const writeCalls = apiMock.callAPI.mock.calls.filter(c => c[0] === 'prompts/write');
    expect(writeCalls).toHaveLength(1);
    const payload = writeCalls[0][1];

    // tags sent
    expect(payload.tags).toEqual(['代码审查', 'bug']);
    // tags are only UI/search metadata; template-level tags_has routing is retired.
    expect(payload.match_when).toBeUndefined();
    // Asset tabs are not agent keys; ordinary create still defaults to main.
    expect(payload.agentType).toBe('main');
    expect(payload.when_to_use).toBe('Use for code review tasks.');
    expect(payload).not.toHaveProperty('content');
    // priority stays default
    expect(payload.priority).toBe(0);
    // create save keeps editor open so sections can be added with the returned ID
    expect(vm.editorOpen.value).toBe(true);
    expect(vm.form.id).toBe('new-1');
  });

  it('basic user flow: explicit debug agent key keeps agentType', async () => {
    apiMock.callAPI.mockResolvedValueOnce({ prompts: [] });
    const { vm } = createPage();
    await vm.loadPrompts();
    vm.switchTab('recall');
    vm.editorMode.value = 'create';
    vm.editorOpen.value = true;

    vm.form.name = '需求分析';
    vm.form.agentKey = 'pm';
    vm.form.tags = ['需求'];
    vm.form.whenToUse = 'Use for product requirement analysis.';

    apiMock.callAPI
      .mockResolvedValueOnce({ prompt: { id: 'new-pm' } })
      .mockResolvedValueOnce({ prompts: [] });
    await vm.savePrompt();

    const payload = apiMock.callAPI.mock.calls.find(c => c[0] === 'prompts/write')[1];
    expect(payload.match_when).toBeUndefined();
    expect(payload.agentType).toBe('pm');
    expect(payload.when_to_use).toBe('Use for product requirement analysis.');
    expect(payload).not.toHaveProperty('content');
    expect(vm.form.id).toBe('new-pm');
  });

  it('basic user flow: empty tags sends an explicit empty tags array without match_when', async () => {
    apiMock.callAPI.mockResolvedValueOnce({ prompts: [] });
    const { vm } = createPage();
    await vm.loadPrompts();
    vm.switchTab('expert');
    vm.editorMode.value = 'create';
    vm.editorOpen.value = true;

    vm.form.name = '简单提示词';
    vm.form.agentKey = 'coder';
    // no tags, no matchWhen

    apiMock.callAPI
      .mockResolvedValueOnce({ prompt: { id: 'new-simple' } })
      .mockResolvedValueOnce({ prompts: [] });
    await vm.savePrompt();

    const payload = apiMock.callAPI.mock.calls.find(c => c[0] === 'prompts/write')[1];
    expect(payload.match_when).toBeUndefined();
    expect(payload.tags).toEqual([]);
    expect(payload).not.toHaveProperty('content');
    expect(vm.form.id).toBe('new-simple');
  });

  it('openEdit + clearing all tags sends an empty tags array', async () => {
    const existing = {
      id: 'coder/prompt',
      name: '编码执行代理',
      content: '...',
      agentType: 'coder',
      tags: ['旧标签'],
    };
    apiMock.callAPI.mockResolvedValueOnce({ prompts: [existing] });
    const { vm } = createPage();
    await vm.loadPrompts();
    vm.openEdit(existing);

    vm.form.tags = [];

    apiMock.callAPI
      .mockResolvedValueOnce({ prompt: {} })
      .mockResolvedValueOnce({ prompts: [existing] });
    await vm.savePrompt();

    const payload = apiMock.callAPI.mock.calls.find(c => c[0] === 'prompts/write')[1];
    expect(payload.tags).toEqual([]);
  });

  it('openEdit + tag change without touching matchWhen does not write retired tags_has', async () => {
    // Regression: template-level tags_has routing is retired; tag-only edits
    // must not reintroduce tags_has via the old auto-generation path.
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
    // tags_has routing is retired; saving tags must not reintroduce it.
    expect(payload.match_when).toBeUndefined();
    expect(payload.tags).toEqual(['代码', 'bug']);
  });

  it('openEdit + user edits matchWhen textarea strips tags_has only payload to null', async () => {
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
    expect(payload.match_when).toBeNull();
  });

  it('openEdit + user edits matchWhen textarea strips tags_has but keeps supported keys', async () => {
    const existing = {
      id: 'coder/prompt',
      name: '编码执行代理',
      content: '...',
      agentType: 'coder',
      tags: ['代码'],
      match_when: { cwd_prefix: '/old' },
    };
    apiMock.callAPI.mockResolvedValueOnce({ prompts: [existing] });
    const { vm } = createPage();
    await vm.loadPrompts();
    vm.openEdit(existing);

    vm.form.matchWhen = '{"tags_has":["手动定制"],"cwd_prefix":"/repo"}';
    vm.matchWhenDirty.value = true;

    apiMock.callAPI
      .mockResolvedValueOnce({ prompt: {} })
      .mockResolvedValueOnce({ prompts: [existing] });
    await vm.savePrompt();

    const payload = apiMock.callAPI.mock.calls.find(c => c[0] === 'prompts/write')[1];
    expect(payload.match_when).toEqual({ cwd_prefix: '/repo' });
  });

  it('openEdit + user clears matchWhen textarea sends null to clear routing', async () => {
    const existing = {
      id: 'coder/prompt',
      name: '编码执行代理',
      content: '...',
      agentType: 'coder',
      tags: ['代码'],
      match_when: { cwd_prefix: '/repo' },
    };
    apiMock.callAPI.mockResolvedValueOnce({ prompts: [existing] });
    const { vm } = createPage();
    await vm.loadPrompts();
    vm.openEdit(existing);

    vm.form.matchWhen = '';
    vm.matchWhenDirty.value = true;

    apiMock.callAPI
      .mockResolvedValueOnce({ prompt: {} })
      .mockResolvedValueOnce({ prompts: [existing] });
    await vm.savePrompt();

    const payload = apiMock.callAPI.mock.calls.find(c => c[0] === 'prompts/write')[1];
    expect(payload.match_when).toBeNull();
  });
});
