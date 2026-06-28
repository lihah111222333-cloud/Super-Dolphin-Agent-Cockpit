// @ts-nocheck
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { reactive } from '../lib/vue.esm-browser.prod.js';

const apiMock = vi.hoisted(() => ({
  callAPI: vi.fn(),
  selectProjectDirs: vi.fn(),
}));

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

function createSkillsPage(overrides = {}, emit = vi.fn()) {
  const props = reactive({
    skills: overrides.skills ?? [{
      name: 'DeploySkill',
      dir: '/skills/deploy',
      description: 'Deploy the current project',
      summary: 'Helps shipping releases',
      scope: 'project',
      trigger_words: ['ship'],
      force_words: [],
    }],
    projectStore: overrides.projectStore ?? { state: { active: '/repo' } },
  });
  return { emit, vm: SkillsPage.setup(props, { emit }) };
}

beforeEach(() => {
  apiMock.callAPI.mockReset().mockResolvedValue({});
  apiMock.selectProjectDirs.mockReset().mockResolvedValue([]);
});

afterEach(() => {
  vi.useRealTimers();
});

describe('SkillsPage save feedback', () => {
  it('keeps saved feedback inside skill cards', () => {
    expect(SkillsPage.template).toContain('已保存');
    expect(SkillsPage.template).toContain('skills-card-saved-');
  });

  it('renders editor failures inside the open editor instead of behind the modal', () => {
    expect(SkillsPage.template).toContain('data-testid="skills-editor-notice"');
    expect(SkillsPage.template).toContain('v-if="notice.message && !isEditorOpen"');
  });

  it('does not match hidden internal skill references in search', () => {
    const { vm } = createSkillsPage({
      skills: [{
        name: 'DocsSkill',
        dir: '/skills/docs',
        description: 'Write documentation',
        summary: 'Write documentation',
        scope: 'project',
        trigger_words: ['@secret-ref'],
        force_words: [],
      }],
    });

    vm.searchQuery.value = '@secret-ref';

    expect(vm.filteredSkillCards.value).toEqual([]);
    expect(vm.skillCards.value[0].displayScenarioWords).toEqual([]);
  });

  it('shows delete confirmation scope and path context', () => {
    expect(SkillsPage.template).toContain('scopeLabel(confirmDeleteTarget.scope)');
    expect(SkillsPage.template).toContain('confirmDeleteTarget.dir');
  });

  it('shows saved feedback on the saved card instead of a persistent global success notice', async () => {
    vi.useFakeTimers();
    const { vm } = createSkillsPage();
    const card = vm.skillCards.value[0];

    vm.form.name = 'DeploySkill';
    vm.form.description = '当你需要发布当前项目时使用';
    vm.form.body = '## deploy body';
    vm.form.scope = 'project';
    vm.sourcePath.value = '/skills/deploy/SKILL.md';
    vm.activeSkillFilePath.value = '/skills/deploy/SKILL.md';
    vm.isEditorOpen.value = true;

    await vm.onSaveSkill();

    expect(vm.isEditorOpen.value).toBe(false);
    expect(vm.notice.message).toBe('');
    expect(vm.isSkillCardRecentlySaved(card)).toBe(true);

    vi.advanceTimersByTime(2600);
    expect(vm.isSkillCardRecentlySaved(card)).toBe(false);
  });

  it('keeps saved feedback on the card while showing description guidance', async () => {
    const { vm } = createSkillsPage();
    const card = vm.skillCards.value[0];

    vm.form.name = 'DeploySkill';
    vm.form.description = '处理问题';
    vm.form.body = '## deploy body';
    vm.form.scope = 'project';
    vm.sourcePath.value = '/skills/deploy/SKILL.md';
    vm.activeSkillFilePath.value = '/skills/deploy/SKILL.md';
    vm.isEditorOpen.value = true;

    await vm.onSaveSkill();

    expect(vm.isSkillCardRecentlySaved(card)).toBe(true);
    expect(vm.notice.message).toBe('简介偏短，建议写清楚“什么时候使用”。');
  });

  it('keeps same-name conflict guidance but moves saved confirmation to the card', async () => {
    const { vm } = createSkillsPage({
      skills: [
        {
          name: 'DeploySkill',
          dir: '/skills/deploy',
          description: 'Deploy the current project',
          summary: 'Helps shipping releases',
          scope: 'project',
          trigger_words: ['ship'],
          force_words: [],
        },
        {
          name: 'DeploySkill',
          dir: '/personal/skills/deploy',
          description: 'Personal deploy helper',
          summary: 'Personal helper',
          scope: 'personal',
          personal_type: 'user',
          trigger_words: [],
          force_words: [],
        },
      ],
    });
    const card = vm.skillCards.value[0];

    vm.form.name = 'DeploySkill';
    vm.form.description = 'Deploy the current project';
    vm.form.body = '## deploy body';
    vm.form.scope = 'project';
    vm.sourcePath.value = '/skills/deploy/SKILL.md';
    vm.activeSkillFilePath.value = '/skills/deploy/SKILL.md';
    vm.isEditorOpen.value = true;

    await vm.onSaveSkill();

    expect(vm.isSkillCardRecentlySaved(card)).toBe(true);
    expect(vm.notice.message).toContain('已经有同名技能');
    expect(vm.notice.message).not.toContain('已保存');
  });

  it('refreshes conflict handling after saving a skill', async () => {
    const { vm } = createSkillsPage();
    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'skills/resolution_list') {
        return { items: [{ conflict_id: 'same-deploy', name: 'DeploySkill', kind: 'same_name', available_actions: ['keep_selected'] }] };
      }
      return {};
    });

    vm.form.name = 'DeploySkill';
    vm.form.description = 'Deploy the current project';
    vm.form.body = '## deploy body';
    vm.form.scope = 'project';
    vm.isEditorOpen.value = true;

    await vm.onSaveSkill();

    expect(apiMock.callAPI).toHaveBeenCalledWith('skills/resolution_list', { cwd: '/repo' });
    expect(vm.resolutionConflicts.value.map((item) => item.conflict_id)).toEqual(['same-deploy']);
    expect(vm.showResolutionPanel.value).toBe(true);
  });

  it('refreshes conflict handling after importing skills', async () => {
    const { vm } = createSkillsPage();
    apiMock.selectProjectDirs.mockResolvedValue(['/imports/DeploySkill']);
    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'skills/local/importDir') return { imported: [] };
      if (method === 'skills/resolution_list') return { items: [{ conflict_id: 'import-conflict' }] };
      return {};
    });

    await vm.onUploadSkill();
    await vm.confirmImportScope('project');

    expect(apiMock.callAPI).toHaveBeenCalledWith('skills/resolution_list', { cwd: '/repo' });
    expect(vm.resolutionConflicts.value.map((item) => item.conflict_id)).toEqual(['import-conflict']);
  });

  it('still refreshes conflicts when opening the imported skill fails', async () => {
    const { vm } = createSkillsPage();
    apiMock.selectProjectDirs.mockResolvedValue(['/imports/DeploySkill']);
    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'skills/local/importDir') {
        return { imported: [{ name: 'DeploySkill', skill_file: '/missing/DeploySkill/SKILL.md' }], failures: [] };
      }
      if (method === 'skills/local/read') throw new Error('file disappeared');
      if (method === 'skills/resolution_list') return { items: [{ conflict_id: 'import-conflict' }] };
      return {};
    });

    await vm.onUploadSkill();
    await vm.confirmImportScope('project');

    expect(apiMock.callAPI).toHaveBeenCalledWith('skills/resolution_list', { cwd: '/repo' });
    expect(vm.resolutionConflicts.value.map((item) => item.conflict_id)).toEqual(['import-conflict']);
    expect(vm.notice.message).not.toContain('导入目录失败');
  });
});
