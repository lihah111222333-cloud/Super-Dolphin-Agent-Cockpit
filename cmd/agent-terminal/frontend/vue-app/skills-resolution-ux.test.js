// @ts-nocheck
import { describe, expect, it, vi } from 'vitest';

const lifecycleMock = vi.hoisted(() => ({ mounted: [] }));
const apiMock = vi.hoisted(() => ({ callAPI: vi.fn(), selectProjectDirs: vi.fn() }));

vi.mock('../lib/vue.esm-browser.prod.js', async () => {
  const actual = await vi.importActual('../lib/vue.esm-browser.prod.js');
  return { ...actual, onMounted: (cb) => lifecycleMock.mounted.push(cb) };
});

vi.mock('./services/api.js', () => ({
  callAPI: apiMock.callAPI,
  selectProjectDirs: apiMock.selectProjectDirs,
}));

vi.mock('./services/log.js', () => ({
  logDebug: vi.fn(),
  logInfo: vi.fn(),
  logWarn: vi.fn(),
}));

vi.mock('./utils/assistant-markdown.js', () => ({
  renderAssistantMarkdown: (text) => `<p>${text}</p>`,
  injectSentenceBreaks: vi.fn((text) => text),
}));

import { SkillsPage } from './pages/SkillsPage.js';
import { reactive } from '../lib/vue.esm-browser.prod.js';

describe('SkillsPage resolution UX', () => {
  it('checks conflicts automatically after entering the skills page and shows the conflict reminder immediately', async () => {
    lifecycleMock.mounted.length = 0;
    apiMock.callAPI.mockReset().mockResolvedValue({
      items: [{ conflict_id: 'same-1', name: 'DocsSkill', kind: 'same_name', available_actions: ['view_diff'] }],
    });

    const vm = SkillsPage.setup({
      skills: [],
      pendingCandidates: [],
      projectStore: { state: { active: '/repo' } },
    }, { emit: vi.fn() });

    expect(apiMock.callAPI).not.toHaveBeenCalled();
    expect(lifecycleMock.mounted.length).toBeGreaterThan(0);

    await lifecycleMock.mounted[0]();

    expect(apiMock.callAPI).toHaveBeenCalledWith('skills/resolution_list', { cwd: '/repo' });
    expect(vm.resolutionCheckButtonText.value).toBe('发现 1 个冲突');
    expect(vm.resolutionPanelToggleText.value).toBe('收起冲突');
    expect(vm.showResolutionPanel.value).toBe(true);
    expect(vm.resolutionConflictAlertText.value).toContain('发现 1 个技能冲突');
    expect(vm.notice.message).toBe('');
  });

  it('shows a retry entry when initial conflict scan fails', async () => {
    lifecycleMock.mounted.length = 0;
    apiMock.callAPI.mockReset().mockRejectedValueOnce(new Error('rpc offline'));

    const vm = SkillsPage.setup({
      skills: [],
      pendingCandidates: [],
      projectStore: { state: { active: '/repo' } },
    }, { emit: vi.fn() });

    await lifecycleMock.mounted[0]();

    expect(vm.showResolutionCheckButton.value).toBe(true);
    expect(vm.resolutionCheckButtonText.value).toBe('检查冲突');
    expect(vm.notice.level).toBe('error');
    expect(vm.notice.message).toContain('读取技能冲突失败');
  });

  it('does not keep a persistent no-conflict notice after checking conflicts', async () => {
    lifecycleMock.mounted.length = 0;
    apiMock.callAPI.mockReset().mockResolvedValue({ items: [] });

    const vm = SkillsPage.setup({
      skills: [],
      pendingCandidates: [],
      projectStore: { state: { active: '/repo' } },
    }, { emit: vi.fn() });
    vm.notice.message = '旧提示';

    await vm.refreshSkillResolutions();

    expect(vm.notice.message).toBe('');
    expect(vm.resolutionConflictAlertText.value).toBe('');
    expect(vm.showResolutionCheckButton.value).toBe(false);
  });

  it('clears stale import conflict drafts when the real conflict list is empty', async () => {
    lifecycleMock.mounted.length = 0;
    apiMock.callAPI.mockReset().mockResolvedValue({ items: [] });

    const vm = SkillsPage.setup({
      skills: [],
      pendingCandidates: [],
      projectStore: { state: { active: '/repo' } },
    }, { emit: vi.fn() });
    vm.importSummaryDrafts.value = [
      { id: 'conflict', name: '编写计划', status: 'conflict', error: '同名技能待处理' },
      { id: 'summary', name: '后端', status: 'ready', suggestion: '当你需要编写后端代码时使用。' },
    ];

    await vm.refreshSkillResolutions({ notify: false });

    expect(vm.importSummaryDrafts.value).toEqual([
      { id: 'summary', name: '后端', status: 'ready', suggestion: '当你需要编写后端代码时使用。' },
    ]);
  });

  it('keeps same-name import conflicts out of the bottom import panel', () => {
    const vm = SkillsPage.setup({
      skills: [],
      pendingCandidates: [],
      projectStore: { state: { active: '/repo' } },
    }, { emit: vi.fn() });

    vm.importSummaryDrafts.value = [
      { id: 'conflict', name: '编写计划', status: 'conflict', error: '同名技能待处理' },
    ];

    expect(vm.visibleImportSummaryDrafts.value).toEqual([]);
    expect(SkillsPage.template).toContain('visibleImportSummaryDrafts.length > 0');
    expect(SkillsPage.template).toContain('in visibleImportSummaryDrafts');
  });

  it('uses keep-selected project action for same-name conflicts with multiple personal versions', () => {
    const vm = SkillsPage.setup({
      skills: [],
      pendingCandidates: [],
      projectStore: { state: { active: '/repo' } },
    }, { emit: vi.fn() });
    const conflict = {
      kind: 'same_name',
      name: 'Docs',
      available_actions: ['disable_personal_for_project', 'keep_selected'],
      sources: [
        { scope: 'project', canonical_id: 'project/Docs', path: '/repo/.agent/skills/Docs' },
        { scope: 'personal', personal_type: 'user', canonical_id: 'personal/user/Docs', path: '/home/user/Docs' },
        { scope: 'personal', personal_type: 'imported', canonical_id: 'personal/imported/Docs', path: '/home/imported/Docs' },
      ],
    };

    const entries = vm.resolutionActionEntries(conflict);

    expect(entries.map((entry) => ({
      action: entry.action,
      label: entry.label,
      sourceID: entry.sourceID,
    }))).toEqual([
      { action: 'keep_selected', label: '用项目共享版本，删除其他版本', sourceID: 'project/Docs' },
      { action: 'keep_selected', label: '用自己创建的私人版本，删除其他版本', sourceID: 'personal/user/Docs' },
      { action: 'keep_selected', label: '用导入的私人版本，删除其他版本', sourceID: 'personal/imported/Docs' },
    ]);
    expect(entries.some((entry) => entry.action === 'disable_personal_for_project')).toBe(false);
  });

  it('refreshes conflict reminder when active project changes', async () => {
    lifecycleMock.mounted.length = 0;
    const projectStore = reactive({ state: { active: '/repo-a' } });
    apiMock.callAPI.mockReset()
      .mockResolvedValueOnce({ items: [] })
      .mockResolvedValueOnce({ items: [{ conflict_id: 'same-b', name: 'DocsSkill', kind: 'same_name', available_actions: ['view_diff'] }] });

    const vm = SkillsPage.setup({
      skills: [],
      pendingCandidates: [],
      projectStore,
    }, { emit: vi.fn() });

    await lifecycleMock.mounted[0]();
    projectStore.state.active = '/repo-b';
    await Promise.resolve();
    await Promise.resolve();

    expect(apiMock.callAPI).toHaveBeenLastCalledWith('skills/resolution_list', { cwd: '/repo-b' });
    expect(vm.resolutionConflictAlertText.value).toContain('发现 1 个技能冲突');
  });

  it('waits for a concrete cwd before automatic conflict scanning', async () => {
    lifecycleMock.mounted.length = 0;
    const projectStore = reactive({ state: { active: '.' } });
    apiMock.callAPI.mockReset().mockResolvedValue({ items: [] });

    const vm = SkillsPage.setup({
      skills: [],
      pendingCandidates: [],
      projectStore,
    }, { emit: vi.fn() });

    await lifecycleMock.mounted[0]();
    expect(apiMock.callAPI).not.toHaveBeenCalled();
    expect(vm.showResolutionCheckButton.value).toBe(false);
    expect(vm.notice.message).toBe('');

    projectStore.state.active = '/repo';
    await Promise.resolve();
    await Promise.resolve();

    expect(apiMock.callAPI).toHaveBeenCalledWith('skills/resolution_list', { cwd: '/repo' });
  });

  it('uses the explicit cwd prop before projectStore active project for conflict scanning', async () => {
    lifecycleMock.mounted.length = 0;
    apiMock.callAPI.mockReset().mockResolvedValue({ items: [] });

    SkillsPage.setup({
      skills: [],
      pendingCandidates: [],
      cwd: '/window-repo',
      projectStore: { state: { active: '.' } },
    }, { emit: vi.fn() });

    await lifecycleMock.mounted[0]();

    expect(apiMock.callAPI).toHaveBeenCalledTimes(1);
    expect(apiMock.callAPI).toHaveBeenCalledWith('skills/resolution_list', { cwd: '/window-repo' });
  });

  it('shows conflict entry, user-facing preview and action explanations with a direct reminder', () => {
    expect(SkillsPage.template).toContain('data-testid="skills-resolution-alert"');
    expect(SkillsPage.template).toContain('resolutionConflictAlertText');
    expect(SkillsPage.template).toContain('showResolutionCheckButton');
    expect(SkillsPage.template).toContain('resolutionCheckButtonText');
    expect(SkillsPage.template).toContain('data-testid="skills-resolution-panel-toggle"');
    expect(SkillsPage.template).toContain('showResolutionPanel');
    expect(SkillsPage.template).toContain('resolutionConflictGuide(conflict)');
    expect(SkillsPage.template).toContain('class="skills-resolution-actions-title"');
    expect(SkillsPage.template).toContain('resolutionActionSectionTitle(conflict)');
    expect(SkillsPage.template).toContain('resolutionActionEntryLabel(actionEntry)');
    expect(SkillsPage.template).toContain('resolutionActionFootnote(conflict)');
    expect(SkillsPage.template).toContain('v-else-if="resolutionActionEntries(conflict).length > 0"');
    expect(SkillsPage.template).toContain('resolutionManualSteps(conflict)');
    expect(SkillsPage.template).toContain('resolutionPreviewItemSummary(item, resolutionPreview.action)');
    expect(SkillsPage.template).toContain('resolutionPreviewItemPaths(item, resolutionPreview.action)');
    expect(SkillsPage.template).toContain('resolutionPreviewApplies(conflict, entry)');
    expect(SkillsPage.template.indexOf('data-testid="skills-resolution-preview"')).toBeGreaterThan(
      SkillsPage.template.indexOf('data-testid="skills-resolution-list"'),
    );
    expect(SkillsPage.template).toContain('data-testid="skills-resolution-name-prompt"');
    expect(SkillsPage.template).toContain('v-model="resolutionNameInput"');
    expect(SkillsPage.template).toContain('confirmResolutionNewName');
    expect(SkillsPage.template).toContain('resolutionNamePromptHelpText(resolutionNamePrompt)');
    expect(SkillsPage.template).toContain('resolutionNamePromptButtonText(resolutionNamePrompt, resolutionActioning)');
    expect(SkillsPage.template).toContain('技术信息');
  });
});
