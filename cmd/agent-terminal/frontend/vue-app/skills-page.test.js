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
        force_words: ['@deploy'],
      },
      {
        name: 'DocsSkill',
        dir: '/skills/docs',
        description: 'Write docs',
        summary: 'Documentation helper',
        trust: 'user',
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
      filteredSkillCards: expect.anything(),
      form: expect.anything(),
      onUploadSkill: expect.any(Function),
      onSaveSkill: expect.any(Function),
      onOpenSkillSubfile: expect.any(Function),
      onSkillPreviewClick: expect.any(Function),
      isEditingMainSkillFile: expect.anything(),
      skillBodyMarkdownHtml: expect.anything(),
      onDeleteSkill: expect.any(Function),
      onCreateSkill: expect.any(Function),
      importScope: expect.anything(),
    }));
  });

  it('preserves template contract for save and import entry points', () => {
    expect(SkillsPage.template).toContain('data-testid="skills-save-button"');
    expect(SkillsPage.template).toContain('data-testid="skills-import-button"');
    expect(SkillsPage.template).toContain('data-testid="skills-editor-scope-project"');
    expect(SkillsPage.template).toContain('data-testid="skills-import-scope-system"');
  });

  it('filters skills by name, summary and trigger words', () => {
    const { vm } = createSkillsPage();

    vm.searchQuery.value = 'ship';
    expect(vm.filteredSkillCards.value.map((item) => item.name)).toEqual(['DeploySkill']);

    vm.searchQuery.value = 'documentation';
    expect(vm.filteredSkillCards.value.map((item) => item.name)).toEqual(['DocsSkill']);
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
      name: '',
      description: '',
      summary: '',
      triggerWordsText: '',
      forceWordsText: '',
      body: '',
      scope: 'project',
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
    vm.form.body = '## docs body';

    await vm.onSaveSkill();

    const saveCall = apiMock.callAPI.mock.calls.find(([method]) => method === 'skills/local/write');
    expect(saveCall).toBeTruthy();
    expect(saveCall[1].path).toBe('DocsSkill');
    expect(saveCall[1].content).toContain('DocsSkill');
    expect(saveCall[1].content).toContain('## docs body');
    expect(saveCall[1].scope).toBe('project');
    expect(emit).toHaveBeenCalledWith('refresh-skills');
    expect(vm.summarySource.value).toBe('frontmatter');
    expect(vm.notice.message).toContain('技能已保存：DocsSkill');
  });

  it('saves main skill files to system scope when selected', async () => {
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
      scope: 'system',
    });
    expect(vm.notice.message).toContain('system');
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
    vm.activeSkillFilePath.value = '/skills/deploy/references/prompt.md';

    await vm.onSaveSkill();

    expect(apiMock.callAPI).toHaveBeenCalledWith('skills/local/write', {
      cwd: '/repo',
      path: '/skills/deploy/references/prompt.md',
      content: '# prompt body',
      scope: 'project',
    });
    expect(emit).not.toHaveBeenCalledWith('refresh-skills');
    expect(vm.notice.message).toContain('子文件已保存');
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
          skills: [{ name: 'ImportedSkill', skill_file: '/imports/ImportedSkill/SKILL.md' }],
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

    expect(apiMock.callAPI).not.toHaveBeenCalled();
    expect(vm.uploading.value).toBe(false);
    expect(vm.notice.level).toBe('info');
    expect(vm.notice.message).toContain('未选择目录');
  });

  it('shows an overwrite notice before import completes when selected names already exist', async () => {
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

    const importTask = vm.onUploadSkill({ type: 'click' });
    await Promise.resolve();
    await Promise.resolve();

    expect(vm.notice.level).toBe('info');
    expect(vm.notice.message).toContain('将覆盖已有技能');

    resolveImport({ skills: [], failures: [] });
    await importTask;
    expect(vm.notice.message).toContain('未导入任何技能目录');
  });

  it('blocks uploads when selected directories contain duplicate inferred skill names', async () => {
    const { vm } = createSkillsPage();
    apiMock.selectProjectDirs.mockResolvedValue(['/imports/Alpha', '/tmp/Alpha/']);

    await vm.onUploadSkill({ type: 'click' });

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
          skills: [{ name: 'ImportedSkill', skill_file: '/imports/ImportedSkill/SKILL.md' }],
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

    expect(emit).toHaveBeenCalledWith('refresh-skills');
    expect(vm.selectedSkillName.value).toBe('ImportedSkill');
    expect(vm.importFailures.value).toEqual(['/imports/BadSkill：broken archive']);
    expect(vm.notice.level).toBe('error');
    expect(vm.notice.message).toContain('成功 1，失败 1');
  });

  it('shows an info notice when import finishes without any skills', async () => {
    const { vm } = createSkillsPage();
    apiMock.selectProjectDirs.mockResolvedValue(['/imports/EmptySkill']);
    apiMock.callAPI.mockResolvedValueOnce({ skills: [], failures: [] });

    await vm.onUploadSkill({ type: 'click' });

    expect(vm.importFailures.value).toEqual([]);
    expect(vm.notice.level).toBe('info');
    expect(vm.notice.message).toContain('未导入任何技能目录');
  });

  it('uploads directories to system scope when selected', async () => {
    const { vm } = createSkillsPage();
    vm.importScope.value = 'system';
    apiMock.selectProjectDirs.mockResolvedValue(['/imports/SystemSkill']);
    apiMock.callAPI.mockResolvedValueOnce({ skills: [], failures: [] });

    await vm.onUploadSkill({ type: 'click' });

    expect(apiMock.callAPI).toHaveBeenCalledWith('skills/local/importDir', {
      paths: ['/imports/SystemSkill'],
      cwd: '/repo',
      scope: 'system',
    });
  });

  it('loads saved skills into matching editor scopes from trust', async () => {
    const { vm } = createSkillsPage();
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'skills/local/read' && payload?.path === '/skills/docs/SKILL.md') {
        return {
          skill: {
            content: '---\nname: DocsSkill\nsummary: Documentation helper\n---\n# body',
            summary: 'Documentation helper',
            summary_source: 'frontmatter',
          },
        };
      }
      if (method === 'skills/local/listFiles') return { files: [] };
      return {};
    });

    await vm.onEditSkill({ name: 'DocsSkill', dir: '/skills/docs', summary: 'Documentation helper', trust: 'user' });

    expect(vm.form.scope).toBe('system');
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

    await vm.onDeleteSkill({ name: 'DeploySkill' });

    expect(apiMock.callAPI).toHaveBeenCalledWith('skills/local/delete', { name: 'DeploySkill', cwd: '/repo' });
    expect(emit).toHaveBeenCalledWith('refresh-skills');
    expect(vm.isEditorOpen.value).toBe(false);
    expect(vm.form.name).toBe('');
    expect(vm.sourcePath.value).toBe('');
    expect(vm.skillFiles.value).toEqual([]);
    expect(vm.notice.message).toContain('技能已删除');
  });

  it('aborts deletion when the user cancels confirmation', async () => {
    const { vm } = createSkillsPage();
    globalThis.window.confirm = vi.fn(() => false);

    await vm.onDeleteSkill({ name: 'DeploySkill' });

    expect(apiMock.callAPI).not.toHaveBeenCalled();
    expect(vm.deletingSkillName.value).toBe('');
  });

  it('surfaces delete failures and clears deleting state', async () => {
    const emit = vi.fn();
    const { vm } = createSkillsPage({}, emit);
    apiMock.callAPI.mockRejectedValueOnce(new Error('delete failed'));

    await vm.onDeleteSkill({ name: 'DeploySkill' });

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
    expect(vm.notice.message).toContain('子文件列表读取失败');
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
