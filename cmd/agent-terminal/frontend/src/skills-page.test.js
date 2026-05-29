// @ts-nocheck
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

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

import { nextTick, reactive } from '../lib/vue.esm-browser.prod.js';
import { SkillsPage } from './pages/SkillsPage.js';

function createSkillsPage(overrides = {}, emit = vi.fn()) {
  const props = reactive({
    skills: overrides.skills ?? [
      {
        name: 'DeploySkill',
        dir: '/skills/deploy',
        description: 'Deploy the current project',
        summary: 'Helps shipping releases',
        trust: 'project',
        trigger_words: ['ship'],
        force_words: ['@DeploySkill', '@deploy', '[skill:DeploySkill]'],
      },
      {
        name: 'DocsSkill',
        dir: '/skills/docs',
        description: 'Write docs',
        summary: 'Documentation helper',
        trust: 'user',
        personal_type: 'agent',
        trigger_words: ['docs'],
        force_words: [],
      },
    ],
    projectStore: overrides.projectStore ?? { state: { active: '/repo' } },
  });
  const vm = SkillsPage.setup(props, { emit });
  return { props, emit, vm };
}

async function flushUi() {
  await nextTick();
  await Promise.resolve();
}

beforeEach(() => {
  apiMock.callAPI.mockReset().mockResolvedValue({});
  apiMock.selectProjectDirs.mockReset().mockResolvedValue([]);
  globalThis.window = {
    confirm: vi.fn(() => true),
  };
});

afterEach(() => {
  delete globalThis.window;
});

describe('SkillsPage', () => {
  it('exports the page component and a setup function', () => {
    expect(SkillsPage.name).toBe('SkillsPage');
    expect(typeof SkillsPage.setup).toBe('function');
  });

  it('setup returns the core state and action surface', () => {
    const { vm } = createSkillsPage();

    expect(vm).toEqual(expect.objectContaining({
      selectedSkillName: expect.anything(),
      searchQuery: expect.anything(),
      filteredSkillCards: expect.anything(), showSkillCount: expect.anything(), skillCountText: expect.anything(),
      form: expect.anything(),
      scenarioKeywordsText: expect.anything(),
      scopeLabel: expect.any(Function),
      onUploadSkill: expect.any(Function),
      onSaveSkill: expect.any(Function),
      onOpenSkillSubfile: expect.any(Function),
      onSkillPreviewClick: expect.any(Function),
      isEditingMainSkillFile: expect.anything(),
      showRelatedSkillFiles: expect.anything(),
      skillBodyMarkdownHtml: expect.anything(),
      onDeleteSkill: expect.any(Function),
      skillCardKey: expect.any(Function),
      isSkillCardActive: expect.any(Function),
      confirmSkillDelete: expect.any(Function),
      cancelSkillDelete: expect.any(Function),
      confirmDeleteTarget: expect.anything(),
      onCreateSkill: expect.any(Function),
      importScope: expect.anything(),
      resolutionConflicts: expect.anything(),
      refreshSkillResolutions: expect.any(Function),
    }));
    expect(vm.scopeLabel('project')).toBe('项目共享');
    expect(vm.scopeLabel('personal')).toBe('私人使用');
    expect(vm.saveButtonLabel.value).toBe('保存技能');
    expect(vm.skillCountText.value).toBe('共 2 个技能'); vm.scopeFilter.value = 'personal'; expect(vm.showSkillCount.value).toBe(true);
  });

  it('preserves template contract for save and import entry points', () => {
    expect(SkillsPage.template).toContain('data-testid="skills-save-button"');
    expect(SkillsPage.template).toContain('data-testid="skills-import-button"');
    expect(SkillsPage.template).toContain('data-testid="skills-editor-scope-project"');
    expect(SkillsPage.template).toContain('data-testid="skills-editor-scope-personal"');
    expect(SkillsPage.template).toContain('项目共享');
    expect(SkillsPage.template).toContain('私人使用');
    expect(SkillsPage.template).toContain('技能列表');
    expect(SkillsPage.template).toContain('新建技能');
    ['技能简介', '你可以修改简介和技能内容。'].forEach((text) => expect(SkillsPage.template).toContain(text));
    ['搜索技能名称、简介、关键词', '支持按名称、简介、关键词搜索', '暂无简介，点击编辑补充。'].forEach((text) => expect(SkillsPage.template).toContain(text));
    ['帮我生成', '建议写成“当你需要……时使用”', 'data-testid="skills-summary-suggest-button"', '编辑简介'].forEach((text) => expect(SkillsPage.template).toContain(text));
    expect(SkillsPage.template).toContain('关键词');
    expect(SkillsPage.template).toContain('可选填入，用于辅助匹配使用技能');
    expect(SkillsPage.template).toContain('外部版本');
    expect(SkillsPage.template).toContain('管理版本号');
    expect(['personal', 'project', 'all'].map((scope) => SkillsPage.template.indexOf(`skills-scope-filter-${scope}`))).toEqual([...['personal', 'project', 'all'].map((scope) => SkillsPage.template.indexOf(`skills-scope-filter-${scope}`))].sort((a, b) => a - b)); ['data-testid="skills-scope-filter-pending"', 'data-testid="candidates-panel"', 'data-testid="candidates-list"', 'skills-segmented-count">{{ pendingCandidates.length }}</span>'].forEach((text) => expect(SkillsPage.template).not.toContain(text)); expect(SkillsPage.template).not.toMatch(/candidate-(approve|reject|preview)-/);
    expect(SkillsPage.template).not.toMatch(/强制词|重点关键词|skill-word-chip-force/);
    ['project（当前 cwd）', 'personal（个人技能）', '摘要（注入内容）', '搜索技能名称、摘要、适用场景', '搜索技能名称、简介、适用场景', '支持按名称、简介、适用场景搜索', '支持按名称、描述、摘要、适用场景搜索', '也可以填写 @xxx，对话里输入它时会自动使用这个技能', '暂无摘要，点击编辑补充。', '暂无描述', '运行时注入', 'frontmatter', '&lt;cwd&gt;', '新建 Skill', '保存 Skill', 'SKILL 列表', '{{ item.scope }} scope', "'provider'", 'source {{ item.source_hash }}', 'target {{ item.target_hash }}', '摘要来源', '你可以修改摘要和技能内容。', '简介生成失败'].forEach((text) => expect(SkillsPage.template).not.toContain(text));
    expect(SkillsPage.template).not.toContain('导入位置');
    expect(SkillsPage.template).toContain('data-testid="skills-import-scope-modal"');
    expect(SkillsPage.template).toContain('这些技能导入后给谁使用');
    expect(SkillsPage.template).not.toContain('data-testid="skills-scope-filter-system"');
    expect(SkillsPage.template).not.toContain('data-testid="skills-editor-scope-system"');
    expect(SkillsPage.template).not.toContain('data-testid="skills-import-scope-system"');
    expect(SkillsPage.template).toContain('data-testid="skills-resolution-refresh"');
    expect(SkillsPage.template).toContain('data-testid="skills-resolution-list"');
    expect(SkillsPage.template).toContain('v-if="showRelatedSkillFiles"');
    expect(SkillsPage.template).toContain('附加内容');
    expect(SkillsPage.template).toContain(':data-testid="\'skills-resolution-action-\' + conflictIdx + \'-\' + sourceIdx + \'-\' + actionIdx"');
  });

  it('hides related file navigation when a skill only has the main file', () => {
    const { vm } = createSkillsPage();
    vm.skillFiles.value = [{ name: 'SKILL.md', path: '/skills/deploy/SKILL.md', isMain: true }];
    expect(vm.showRelatedSkillFiles.value).toBe(false);
    vm.skillFiles.value = [{ name: 'SKILL.md', path: '/skills/deploy/SKILL.md', isMain: true }, { name: 'prompt.md', path: '/skills/deploy/references/prompt.md', isMain: false }];
    expect(vm.showRelatedSkillFiles.value).toBe(true);
  });

  it('keeps same-name skill cards distinct by scope and source path', () => {
    const { vm } = createSkillsPage({
      skills: [
        { name: 'DocsSkill', dir: '/repo/.agent/skills/docs', scope: 'project', summary: 'project docs' },
        { name: 'DocsSkill', dir: '/home/skills/personal/user/docs', scope: 'personal', personal_type: 'user', summary: 'user docs' },
      ],
    });

    const [projectSkill, personalSkill] = vm.filteredSkillCards.value;
    expect(vm.skillCardKey(projectSkill)).not.toBe(vm.skillCardKey(personalSkill));

    vm.selectedSkillName.value = 'DocsSkill'; vm.isEditorOpen.value = true; vm.sourcePath.value = '/home/skills/personal/user/docs/SKILL.md';

    expect(vm.isSkillCardActive(projectSkill)).toBe(false); expect(vm.isSkillCardActive(personalSkill)).toBe(true);
    vm.closeEditor(); expect(vm.isSkillCardActive(personalSkill)).toBe(false);
  });

  it('preserves personal type from list items for editor actions', () => {
    const { vm } = createSkillsPage();

    expect(vm.skillCards.value.find((item) => item.name === 'DocsSkill').personal_type).toBe('agent');
  });

  it('prefers backend scope over trust when classifying skill cards and deleting', async () => {
    const { vm } = createSkillsPage({
      skills: [{
        name: 'ProjectSigned',
        dir: '/skills/project-signed',
        description: 'Project signed skill',
        summary: 'Project helper',
        scope: 'project',
        trust: 'signed',
      }],
    });

    const card = vm.skillCards.value[0];
    expect(card.scope).toBe('project');
    expect(vm.scopeCounts.value.project).toBe(1);
    expect(vm.scopeCounts.value.personal).toBe(0);

    vm.onDeleteSkill(card);
    await vm.confirmSkillDelete();

    expect(apiMock.callAPI).toHaveBeenCalledWith('skills/local/delete', {
      name: 'ProjectSigned',
      cwd: '/repo',
      scope: 'project',
    });
  });

  it('filters skills by name, summary and trigger words', () => {
    const { vm } = createSkillsPage();

    vm.searchQuery.value = 'ship';
    expect(vm.filteredSkillCards.value.map((item) => item.name)).toEqual(['DeploySkill']);

    vm.searchQuery.value = 'documentation';
    expect(vm.filteredSkillCards.value.map((item) => item.name)).toEqual(['DocsSkill']);
    expect(vm.skillCards.value[0].displayScenarioWords).toEqual(['ship']);
  });

  it('maps summary source labels and markdown preview fallbacks without drifting helper output', () => {
    const { vm } = createSkillsPage();

    expect(vm.summarySourceLabel.value).toBe('系统生成');
    expect(vm.skillBodyMarkdownHtml.value).toContain('暂无内容');

    vm.summarySource.value = 'frontmatter';
    expect(vm.summarySourceLabel.value).toBe('用户摘要');

    vm.summarySource.value = 'description';
    expect(vm.summarySourceLabel.value).toBe('系统生成（基于描述）');

    vm.summarySource.value = 'generated';
    expect(vm.summarySourceLabel.value).toBe('系统生成（基于正文）');

    vm.summarySource.value = 'unknown';
    expect(vm.summarySourceLabel.value).toBe('系统生成');

    vm.form.body = '  **Skill body**  ';
    expect(vm.skillBodyMarkdownHtml.value).toBe('<p>**Skill body**</p>');
  });

  it('opens a fresh editor when creating a skill and focuses the body input on the next tick', async () => {
    const { vm } = createSkillsPage();
    const focus = vi.fn();
    vm.bodyInputRef.value = { focus };
    vm.selectedSkillName.value = 'DeploySkill';
    vm.summarySource.value = 'generated';
    vm.sourcePath.value = '/skills/deploy/SKILL.md';
    vm.skillFiles.value = [{ name: 'SKILL.md', path: '/skills/deploy/SKILL.md', isMain: true }];
    vm.activeSkillFilePath.value = '/skills/deploy/SKILL.md';
    vm.form.name = 'DeploySkill';
    vm.form.description = 'Deploy the current project';
    vm.form.summary = 'Helps shipping releases';
    vm.form.triggerWordsText = 'ship';
    vm.form.forceWordsText = '@deploy';
    vm.form.body = 'body';
    vm.isBodyEditing.value = false;
    vm.bodyEditorFocused.value = true;
    vm.isEditorOpen.value = false;

    vm.onCreateSkill();
    await flushUi();

    expect(vm.selectedSkillName.value).toBe('');
    expect(vm.summarySource.value).toBe('');
    expect(vm.sourcePath.value).toBe('');
    expect(vm.skillFiles.value).toEqual([]);
    expect(vm.activeSkillFilePath.value).toBe('');
    expect(vm.form).toEqual({
      name: '', displayName: '',
      description: '',
      summary: '',
      triggerWordsText: '',
      forceWordsText: '',
      internalScenarioWordsText: '',
      body: '',
      scope: 'project',
      personal_type: '',
    });
    expect(vm.isBodyEditing.value).toBe(true);
    expect(vm.bodyEditorFocused.value).toBe(false);
    expect(vm.isEditorOpen.value).toBe(true);
    expect(vm.notice.level).toBe('info');
    expect(vm.notice.message).toContain('已打开新建表单');
    expect(focus).toHaveBeenCalledTimes(1);
  });

  it('drives body editing and focus helpers without leaving stale editor state behind', async () => {
    const { vm } = createSkillsPage();
    const focus = vi.fn();
    vm.bodyInputRef.value = { focus };

    vm.startBodyEdit();
    await flushUi();
    expect(vm.isBodyEditing.value).toBe(true);
    expect(focus).toHaveBeenCalledTimes(1);

    vm.onBodyFocus();
    expect(vm.bodyEditorFocused.value).toBe(true);

    vm.onBodyBlur();
    expect(vm.bodyEditorFocused.value).toBe(false);

    vm.onBodyFocus();
    vm.finishBodyEdit();
    expect(vm.isBodyEditing.value).toBe(false);
    expect(vm.bodyEditorFocused.value).toBe(false);
  });

  it('matches deleting skill state case-insensitively', () => {
    const { vm } = createSkillsPage();

    vm.deletingSkillName.value = 'DeploySkill';

    expect(vm.isDeletingSkill('deployskill')).toBe(true);
    expect(vm.isDeletingSkill('DOCSSKILL')).toBe(false);
  });

  it('clears a stale selected skill when the watched skill list no longer contains it', async () => {
    const { vm, props } = createSkillsPage();

    vm.selectedSkillName.value = 'DeploySkill';
    props.skills = props.skills.filter((item) => item.name !== 'DeploySkill');
    await flushUi();

    expect(vm.selectedSkillName.value).toBe('');
  });

  it('closes the editor and clears file navigation state when the watched skill path disappears', async () => {
    const { vm, props } = createSkillsPage();

    vm.selectedSkillName.value = 'DeploySkill';
    vm.sourcePath.value = '/skills/deploy/references/prompt.md';
    vm.skillFiles.value = [
      { name: 'SKILL.md', path: '/skills/deploy/SKILL.md', isMain: true },
      { name: 'prompt.md', path: '/skills/deploy/references/prompt.md', isMain: false },
    ];
    vm.activeSkillFilePath.value = '/skills/deploy/references/prompt.md';
    vm.isEditorOpen.value = true;

    props.skills = props.skills.filter((item) => item.name !== 'DeploySkill');
    await flushUi();

    expect(vm.selectedSkillName.value).toBe('');
    expect(vm.isEditorOpen.value).toBe(false);
    expect(vm.skillFiles.value).toEqual([]);
    expect(vm.activeSkillFilePath.value).toBe('');
  });

  it('saves main skill files through skills/config/write and refreshes skills', async () => {
    const emit = vi.fn();
    const { vm } = createSkillsPage({}, emit);

    vm.form.name = 'DocsSkill';
    vm.form.description = 'Write docs';
    vm.form.summary = 'Documentation helper';
    vm.form.triggerWordsText = 'docs';
    vm.form.forceWordsText = '@docs';
    vm.form.body = '## docs body'; vm.isEditorOpen.value = true;

    await vm.onSaveSkill();

    const saveCall = apiMock.callAPI.mock.calls.find(([method]) => method === 'skills/local/write');
    expect(saveCall).toBeTruthy();
    expect(saveCall[1].path).toBe('DocsSkill');
    expect(saveCall[1].content).toContain('DocsSkill');
    expect(saveCall[1].content).toContain('## docs body');
    expect(saveCall[1].scope).toBe('project');
    expect(emit).toHaveBeenCalledWith('refresh-skills');
    expect(vm.summarySource.value).toBe('frontmatter'); expect(vm.isEditorOpen.value).toBe(false);
    expect(vm.notice.message).toContain('已经有同名技能');
  });

  it('normalizes legacy system save targets to personal scope', async () => {
    const { vm } = createSkillsPage();

    vm.form.name = 'SharedDocs';
    vm.form.summary = 'share me';
    vm.form.body = '## shared';
    vm.form.scope = 'system';

    await vm.onSaveSkill();

    expect(apiMock.callAPI).toHaveBeenCalledWith('skills/local/write', {
      cwd: '/repo',
      path: 'SharedDocs',
      content: expect.stringContaining('## shared'),
      scope: 'personal',
      personal_type: 'user',
    });
    expect(vm.notice.message).toBe('');
  });

  it('surfaces main skill save failures and clears saving state', async () => {
    const { vm } = createSkillsPage();
    apiMock.callAPI.mockRejectedValueOnce(new Error('write failed'));

    vm.form.name = 'DocsSkill';
    vm.form.body = '## docs body';

    await vm.onSaveSkill();

    expect(vm.saving.value).toBe(false);
    expect(vm.notice.level).toBe('error');
    expect(vm.notice.message).toContain('保存失败：write failed');
  });

  it('rejects saving a main skill when the name is empty', async () => {
    const { vm } = createSkillsPage();
    vm.form.name = '';
    vm.form.body = '## unnamed body';

    await vm.onSaveSkill();

    expect(apiMock.callAPI).not.toHaveBeenCalled();
    expect(vm.notice.level).toBe('error');
    expect(vm.notice.message).toContain('请先填写技能名称');
  });

  it('saves subfiles through skills/local/write when editing a non-main file', async () => {
    const emit = vi.fn();
    const { vm } = createSkillsPage({}, emit);

    vm.form.name = 'DeploySkill';
    vm.form.body = '# prompt body';
    vm.sourcePath.value = '/skills/deploy/SKILL.md';
    vm.activeSkillFilePath.value = '/skills/deploy/references/prompt.md'; vm.isEditorOpen.value = true;

    await vm.onSaveSkill();

    expect(apiMock.callAPI).toHaveBeenCalledWith('skills/local/write', {
      cwd: '/repo',
      path: '/skills/deploy/references/prompt.md',
      content: '# prompt body',
      scope: 'project',
    });
    expect(emit).not.toHaveBeenCalledWith('refresh-skills'); expect(vm.isEditorOpen.value).toBe(false);
    expect(vm.notice.message).toBe('');
  });

  it('surfaces subfile save failures and keeps the editor in subfile mode', async () => {
    const { vm } = createSkillsPage();
    apiMock.callAPI.mockRejectedValueOnce(new Error('subfile failed'));

    vm.form.name = 'DeploySkill';
    vm.form.body = '# prompt body';
    vm.sourcePath.value = '/skills/deploy/SKILL.md';
    vm.activeSkillFilePath.value = '/skills/deploy/references/prompt.md';

    await vm.onSaveSkill();

    expect(vm.isEditingMainSkillFile.value).toBe(false);
    expect(vm.saving.value).toBe(false);
    expect(vm.notice.level).toBe('error');
    expect(vm.notice.message).toContain('保存失败：subfile failed');
  });

  it('uploads imported skills using current skillCards context and ignores click-event payload shape', async () => {
    const emit = vi.fn();
    const { vm } = createSkillsPage({}, emit);

    apiMock.selectProjectDirs.mockResolvedValue(['/imports/ImportedSkill']);
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'skills/local/importDir') {
        return {
          imported: [{ name: 'ImportedSkill', skill_file: '/imports/ImportedSkill/SKILL.md' }],
          failures: [],
        };
      }
      if (method === 'skills/local/read' && payload?.path === '/imports/ImportedSkill/SKILL.md') {
        return {
          skill: {
            content: '---\nname: ImportedSkill\nsummary: Imported summary\n---\n# Imported body',
            summary: 'Imported summary',
            summary_source: 'frontmatter',
          },
        };
      }
      return {};
    });

    await vm.onUploadSkill({ type: 'click', target: {} });

    expect(apiMock.selectProjectDirs).not.toHaveBeenCalled();
    expect(apiMock.callAPI).not.toHaveBeenCalledWith('skills/local/importDir', expect.anything());
    expect(vm.importScopePromptOpen.value).toBe(true);

    await vm.confirmImportScope('project');

    expect(apiMock.selectProjectDirs).toHaveBeenCalledTimes(1);
    expect(apiMock.callAPI).toHaveBeenCalledWith('skills/local/importDir', {
      paths: ['/imports/ImportedSkill'],
      cwd: '/repo',
      scope: 'project',
    });
    expect(emit).toHaveBeenCalledWith('refresh-skills');
    expect(vm.selectedSkillName.value).toBe('ImportedSkill');
    expect(vm.sourcePath.value).toBe('/imports/ImportedSkill/SKILL.md');
    expect(vm.notice.message).toContain('已导入 1 个技能目录');
  });

  it('returns early when no directories are selected for import', async () => {
    const { vm } = createSkillsPage();
    apiMock.selectProjectDirs.mockResolvedValue([]);

    await vm.onUploadSkill({ type: 'click' });
    await vm.confirmImportScope('project');

    expect(apiMock.callAPI).not.toHaveBeenCalled();
    expect(vm.uploading.value).toBe(false);
    expect(vm.notice.level).toBe('info');
    expect(vm.notice.message).toContain('未选择目录');
  });

  it('shows a same-name conflict notice before import completes when selected names already exist', async () => {
    const { vm } = createSkillsPage();
    let resolveImport = () => {};
    apiMock.selectProjectDirs.mockResolvedValue(['/imports/DeploySkill']);
    apiMock.callAPI.mockImplementation((method) => {
      if (method === 'skills/local/importDir') {
        return new Promise((resolve) => {
          resolveImport = resolve;
        });
      }
      return Promise.resolve({});
    });

    await vm.onUploadSkill({ type: 'click' });
    const importTask = vm.confirmImportScope('project');
    await Promise.resolve();
    await Promise.resolve();

    expect(vm.notice.level).toBe('info');
    expect(vm.notice.message).toContain('同名冲突');
    expect(vm.notice.message).not.toContain('覆盖');

    resolveImport({ imported: [], failures: [] });
    await importTask;
    expect(vm.notice.message).toContain('未导入任何技能目录');
  });

  it('blocks uploads when selected directories contain duplicate inferred skill names', async () => {
    const { vm } = createSkillsPage();
    apiMock.selectProjectDirs.mockResolvedValue(['/imports/Alpha', '/tmp/Alpha/']);

    await vm.onUploadSkill({ type: 'click' });
    await vm.confirmImportScope('project');

    expect(apiMock.callAPI).not.toHaveBeenCalled();
    expect(vm.notice.level).toBe('error');
    expect(vm.notice.message).toContain('重复技能名');
  });

  it('records import failures while still refreshing and opening the first imported skill', async () => {
    const emit = vi.fn();
    const { vm } = createSkillsPage({}, emit);
    apiMock.selectProjectDirs.mockResolvedValue(['/imports/ImportedSkill']);
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'skills/local/importDir') {
        return {
          imported: [{ name: 'ImportedSkill', skill_file: '/imports/ImportedSkill/SKILL.md' }],
          failures: [{ source: '/imports/BadSkill', error: 'broken archive' }],
        };
      }
      if (method === 'skills/local/read' && payload?.path === '/imports/ImportedSkill/SKILL.md') {
        return {
          skill: {
            content: '---\nname: ImportedSkill\nsummary: Imported summary\n---\n# Imported body',
            summary: 'Imported summary',
            summary_source: 'generated',
          },
        };
      }
      return {};
    });

    await vm.onUploadSkill({ type: 'click' });
    await vm.confirmImportScope('project');

    expect(emit).toHaveBeenCalledWith('refresh-skills');
    expect(vm.selectedSkillName.value).toBe('ImportedSkill');
    expect(vm.importFailures.value).toEqual(['/imports/BadSkill：broken archive']);
    expect(vm.notice.level).toBe('error');
    expect(vm.notice.message).toContain('成功 1，失败 1');
  });

  it('shows an info notice when import finishes without any skills', async () => {
    const { vm } = createSkillsPage();
    apiMock.selectProjectDirs.mockResolvedValue(['/imports/EmptySkill']);
    apiMock.callAPI.mockResolvedValueOnce({ imported: [], failures: [] });

    await vm.onUploadSkill({ type: 'click' });
    await vm.confirmImportScope('project');

    expect(vm.importFailures.value).toEqual([]);
    expect(vm.notice.level).toBe('info');
    expect(vm.notice.message).toContain('未导入任何技能目录');
  });

  it('imports selected directories as personal when confirmed', async () => {
    const { vm } = createSkillsPage();
    apiMock.selectProjectDirs.mockResolvedValue(['/imports/SystemSkill']);
    apiMock.callAPI.mockResolvedValueOnce({ imported: [], failures: [] });

    await vm.onUploadSkill({ type: 'click' });
    await vm.confirmImportScope('personal');

    expect(apiMock.callAPI).toHaveBeenCalledWith('skills/local/importDir', {
      paths: ['/imports/SystemSkill'],
      cwd: '/repo',
      scope: 'personal',
      personal_type: 'imported',
    });
  });

  it('loads user and signed skills into personal editor scope without system mapping', async () => {
    const { vm } = createSkillsPage();
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'skills/local/read' && payload?.path === '/skills/docs/SKILL.md') {
        return {
          skill: {
            content: '---\nname: DocsSkill\ndescription: Documentation helper\n---\n# body',
            summary: 'Documentation helper',
            summary_source: 'description',
          },
        };
      }
      if (method === 'skills/local/listFiles') return { files: [] };
      return {};
    });

    await vm.onEditSkill({ name: 'DocsSkill', dir: '/skills/docs', summary: 'Documentation helper', trust: 'user' });

    expect(vm.form.scope).toBe('personal'); expect(vm.form.description).toBe('Documentation helper'); expect(vm.notice.message).toBe('');

    await vm.onEditSkill({ name: 'DocsSkill', dir: '/skills/docs', summary: 'Documentation helper', trust: 'signed' });

    expect(vm.form.scope).toBe('personal');
  });

  it('deletes the selected skill, clears editor state and emits refresh', async () => {
    const emit = vi.fn();
    const { vm } = createSkillsPage({}, emit);

    vm.selectedSkillName.value = 'DeploySkill';
    vm.form.name = 'DeploySkill';
    vm.form.description = 'Deploy the current project';
    vm.form.summary = 'Helps shipping releases';
    vm.form.body = 'body';
    vm.sourcePath.value = '/skills/deploy/SKILL.md';
    vm.skillFiles.value = [{ name: 'SKILL.md', path: '/skills/deploy/SKILL.md', isMain: true }];
    vm.activeSkillFilePath.value = '/skills/deploy/SKILL.md';
    vm.isEditorOpen.value = true;

    vm.onDeleteSkill({ name: 'DeploySkill' });
    expect(vm.confirmDeleteTarget.value).toEqual({ name: 'DeploySkill' });

    await vm.confirmSkillDelete();

    expect(apiMock.callAPI).toHaveBeenCalledWith('skills/local/delete', { name: 'DeploySkill', cwd: '/repo', scope: 'project' });
    expect(emit).toHaveBeenCalledWith('refresh-skills');
    expect(vm.isEditorOpen.value).toBe(false);
    expect(vm.form.name).toBe('');
    expect(vm.sourcePath.value).toBe('');
    expect(vm.skillFiles.value).toEqual([]);
    expect(vm.notice.message).toBe('');
  });

  it('aborts deletion when the user cancels confirmation', () => {
    const { vm } = createSkillsPage();

    vm.onDeleteSkill({ name: 'DeploySkill' });
    expect(vm.confirmDeleteTarget.value).toEqual({ name: 'DeploySkill' });

    vm.cancelSkillDelete();

    expect(vm.confirmDeleteTarget.value).toBe(null);
    expect(apiMock.callAPI).not.toHaveBeenCalled();
    expect(vm.deletingSkillName.value).toBe('');
  });

  it('surfaces delete failures and clears deleting state', async () => {
    const emit = vi.fn();
    const { vm } = createSkillsPage({}, emit);
    apiMock.callAPI.mockRejectedValueOnce(new Error('delete failed'));

    vm.onDeleteSkill({ name: 'DeploySkill' });
    await vm.confirmSkillDelete();

    expect(vm.deletingSkillName.value).toBe('');
    expect(vm.notice.level).toBe('error');
    expect(vm.notice.message).toContain('删除技能失败：delete failed');
    expect(emit).not.toHaveBeenCalled();
  });

  it('keeps editor open and surfaces subfile list load failures during edit', async () => {
    const { vm } = createSkillsPage();
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'skills/local/read' && payload?.path === '/skills/deploy/SKILL.md') {
        return {
          skill: {
            content: '---\nname: DeploySkill\nsummary: Helps shipping releases\n---\n# deploy body',
            summary: 'Helps shipping releases',
            summary_source: 'generated',
          },
        };
      }
      if (method === 'skills/local/listFiles' && payload?.dir === '/skills/deploy') {
        throw new Error('list failed');
      }
      return {};
    });

    await vm.onEditSkill({ name: 'DeploySkill', dir: '/skills/deploy', summary: 'Helps shipping releases' });

    expect(vm.isEditorOpen.value).toBe(true);
    expect(vm.selectedSkillName.value).toBe('DeploySkill');
    expect(vm.activeSkillFilePath.value).toBe('/skills/deploy/SKILL.md');
    expect(vm.skillFiles.value).toEqual([]);
    expect(vm.notice.level).toBe('error');
    expect(vm.notice.message).toContain('附加内容读取失败');
  });

  it('surfaces read failures when opening an existing skill', async () => {
    const { vm } = createSkillsPage();
    apiMock.callAPI.mockRejectedValueOnce(new Error('read failed'));

    await vm.onEditSkill({ name: 'DeploySkill', dir: '/skills/deploy' });

    expect(vm.isEditorOpen.value).toBe(false);
    expect(vm.selectedSkillName.value).toBe('');
    expect(vm.notice.level).toBe('error');
    expect(vm.notice.message).toContain('读取技能失败：read failed');
  });

  it('opens linked skill subfiles from markdown preview clicks', async () => {
    const { vm } = createSkillsPage();
    vm.skillFiles.value = [
      { name: 'SKILL.md', path: '/skills/deploy/SKILL.md', isMain: true },
      { name: 'prompt.md', path: '/skills/deploy/references/prompt.md', isMain: false },
    ];
    vm.sourcePath.value = '/skills/deploy/SKILL.md';
    vm.activeSkillFilePath.value = '/skills/deploy/SKILL.md';
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'skills/local/read' && payload?.path === '/skills/deploy/references/prompt.md') {
        return { skill: { content: '# prompt body' } };
      }
      return {};
    });

    await vm.onSkillPreviewClick({
      target: {
        closest: vi.fn((selector) => (selector.includes('chat-md-file-ref') ? {
          getAttribute: (name) => ({ 'data-file-path': './references/prompt.md', 'data-file-line': '1', 'data-file-column': '0' }[name] || ''),
          textContent: './references/prompt.md',
        } : null)),
      },
      preventDefault: vi.fn(),
      stopPropagation: vi.fn(),
    });

    expect(apiMock.callAPI).toHaveBeenCalledWith('skills/local/read', { path: '/skills/deploy/references/prompt.md', cwd: '/repo' });
    expect(vm.activeSkillFilePath.value).toBe('/skills/deploy/references/prompt.md');
    expect(vm.form.body).toBe('# prompt body');
  });

  it('opens the main skill file when markdown preview points back to SKILL.md', async () => {
    const { vm } = createSkillsPage();
    vm.form.name = 'DeploySkill';
    vm.skillFiles.value = [
      { name: 'SKILL.md', path: '/skills/deploy/SKILL.md', isMain: true },
      { name: 'prompt.md', path: '/skills/deploy/references/prompt.md', isMain: false },
    ];
    vm.sourcePath.value = '/skills/deploy/references/prompt.md';
    vm.activeSkillFilePath.value = '/skills/deploy/references/prompt.md';
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'skills/local/read' && payload?.path === '/skills/deploy/SKILL.md') {
        return {
          skill: {
            content: '---\nname: DeploySkill\nsummary: Helps shipping releases\n---\n# deploy body',
            summary: 'Helps shipping releases',
            summary_source: 'generated',
          },
        };
      }
      return {};
    });

    await vm.onSkillPreviewClick({
      target: {
        closest: vi.fn((selector) => (selector.includes('chat-md-file-ref') ? {
          getAttribute: (name) => ({ 'data-file-path': './SKILL.md', 'data-file-line': '1', 'data-file-column': '0' }[name] || ''),
          textContent: './SKILL.md',
        } : null)),
      },
      preventDefault: vi.fn(),
      stopPropagation: vi.fn(),
    });

    expect(apiMock.callAPI).toHaveBeenCalledWith('skills/local/read', { path: '/skills/deploy/SKILL.md', cwd: '/repo' });
    expect(vm.activeSkillFilePath.value).toBe('/skills/deploy/SKILL.md');
    expect(vm.notice.message).toContain('已切换到主文件 SKILL.md');
  });

  it('opens referenced skills from markdown citation chips', async () => {
    const { vm } = createSkillsPage();

    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'skills/local/read' && payload?.path === '/skills/docs/SKILL.md') {
        return { skill: { content: '---\nname: DocsSkill\n---\n# docs body', summary: 'Documentation helper', summary_source: 'generated' } };
      }
      if (method === 'skills/local/listFiles' && payload?.dir === '/skills/docs') {
        return { files: [{ name: 'SKILL.md', path: '/skills/docs/SKILL.md', is_main: true }] };
      }
      return {};
    });

    await vm.onSkillPreviewClick({
      target: {
        closest: vi.fn((selector) => (selector.includes('chat-md-citation') ? {
          getAttribute: (name) => ({ 'data-citation-kind': 'skill', 'data-skill-id': 'docs-skill', 'data-skill-name': 'DocsSkill', 'data-skill-path': '/skills/docs/SKILL.md' }[name] || ''),
          textContent: 'DocsSkill',
        } : null)),
      },
      preventDefault: vi.fn(),
      stopPropagation: vi.fn(),
    });

    expect(apiMock.callAPI).toHaveBeenCalledWith('skills/local/read', { path: '/skills/docs/SKILL.md', cwd: '/repo' });
    expect(apiMock.callAPI).toHaveBeenCalledWith('skills/local/listFiles', { dir: '/skills/docs', cwd: '/repo' });
    expect(vm.selectedSkillName.value).toBe('DocsSkill');
    expect(vm.sourcePath.value).toBe('/skills/docs/SKILL.md');
  });

  it('opens same-name skill citations by path before falling back to name', async () => {
    const { vm } = createSkillsPage({
      skills: [
        { name: 'DocsSkill', dir: '/repo/.agent/skills/docs', summary: 'Project docs', trust: 'project' },
        { name: 'DocsSkill', dir: '/home/.super-dolphin/skills/personal/user/docs', summary: 'Personal docs', trust: 'user', personal_type: 'user' },
      ],
    });

    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'skills/local/read' && payload?.path === '/home/.super-dolphin/skills/personal/user/docs/SKILL.md') {
        return { skill: { content: '---\nname: DocsSkill\n---\n# personal docs', summary: 'Personal docs', summary_source: 'generated' } };
      }
      if (method === 'skills/local/listFiles' && payload?.dir === '/home/.super-dolphin/skills/personal/user/docs') {
        return { files: [{ name: 'SKILL.md', path: '/home/.super-dolphin/skills/personal/user/docs/SKILL.md', is_main: true }] };
      }
      return {};
    });

    await vm.onSkillPreviewClick({
      target: {
        closest: vi.fn((selector) => (selector.includes('chat-md-citation') ? {
          getAttribute: (name) => ({
            'data-citation-kind': 'skill',
            'data-skill-name': 'DocsSkill',
            'data-skill-path': '/home/.super-dolphin/skills/personal/user/docs/SKILL.md',
          }[name] || ''),
          textContent: 'DocsSkill',
        } : null)),
      },
      preventDefault: vi.fn(),
      stopPropagation: vi.fn(),
    });

    expect(apiMock.callAPI).toHaveBeenCalledWith('skills/local/read', {
      path: '/home/.super-dolphin/skills/personal/user/docs/SKILL.md',
      cwd: '/repo',
    });
    expect(vm.sourcePath.value).toBe('/home/.super-dolphin/skills/personal/user/docs/SKILL.md');
  });

  it('shows an info notice for unsupported conversation citations', async () => {
    const { vm } = createSkillsPage();

    await vm.onSkillPreviewClick({
      target: {
        closest: vi.fn((selector) => (selector.includes('chat-md-citation') ? {
          getAttribute: (name) => ({ 'data-citation-kind': 'conversation', 'data-conversation-id': 'thread-1' }[name] || ''),
          textContent: 'conversation',
        } : null)),
      },
      preventDefault: vi.fn(),
      stopPropagation: vi.fn(),
    });

    expect(apiMock.callAPI).not.toHaveBeenCalled();
    expect(vm.notice.level).toBe('info');
    expect(vm.notice.message).toContain('暂不支持会话跳转');
  });

  it('shows an info notice when a cited skill cannot be resolved', async () => {
    const { vm } = createSkillsPage();

    await vm.onSkillPreviewClick({
      target: {
        closest: vi.fn((selector) => (selector.includes('chat-md-citation') ? {
          getAttribute: (name) => ({ 'data-citation-kind': 'skill', 'data-skill-name': 'MissingSkill', 'data-skill-path': '/skills/missing/SKILL.md' }[name] || ''),
          textContent: 'MissingSkill',
        } : null)),
      },
      preventDefault: vi.fn(),
      stopPropagation: vi.fn(),
    });

    expect(apiMock.callAPI).not.toHaveBeenCalled();
    expect(vm.notice.level).toBe('info');
    expect(vm.notice.message).toContain('未找到引用的技能');
  });

  it('shows an info notice for recognized but unsupported citation kinds', async () => {
    const { vm } = createSkillsPage();

    await vm.onSkillPreviewClick({
      target: {
        closest: vi.fn((selector) => (selector.includes('chat-md-citation') ? {
          getAttribute: (name) => ({ 'data-citation-kind': 'image', 'data-asset-pointer': 'asset-1' }[name] || ''),
          textContent: 'image-asset',
        } : null)),
      },
      preventDefault: vi.fn(),
      stopPropagation: vi.fn(),
    });

    expect(apiMock.callAPI).not.toHaveBeenCalled();
    expect(vm.notice.level).toBe('info');
    expect(vm.notice.message).toContain('该引用已识别');
  });

  it('shows an info notice when a preview file reference cannot be resolved inside the skill directory', async () => {
    const { vm } = createSkillsPage();
    vm.skillFiles.value = [
      { name: 'SKILL.md', path: '/skills/deploy/SKILL.md', isMain: true },
    ];
    vm.sourcePath.value = '/skills/deploy/SKILL.md';
    vm.activeSkillFilePath.value = '/skills/deploy/SKILL.md';

    await vm.onSkillPreviewClick({
      target: {
        closest: vi.fn((selector) => (selector.includes('chat-md-file-ref') ? {
          getAttribute: (name) => ({ 'data-file-path': './missing.md', 'data-file-line': '1', 'data-file-column': '0' }[name] || ''),
          textContent: './missing.md',
        } : null)),
      },
      preventDefault: vi.fn(),
      stopPropagation: vi.fn(),
    });

    expect(apiMock.callAPI).not.toHaveBeenCalled();
    expect(vm.notice.level).toBe('info');
    expect(vm.notice.message).toContain('当前预览仅支持打开本技能目录内的引用文件');
  });
});
