// @ts-nocheck
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { reactive, ref } from '../lib/vue.esm-browser.prod.js';

const apiMock = vi.hoisted(() => ({
  callAPI: vi.fn(),
  selectProjectDirs: vi.fn(),
}));

vi.mock('./services/api.js', () => ({
  callAPI: apiMock.callAPI,
  selectProjectDirs: apiMock.selectProjectDirs,
}));

vi.mock('./services/log.js', () => ({
  logInfo: vi.fn(),
  logWarn: vi.fn(),
}));

vi.mock('./utils/assistant-markdown.js', () => ({
  renderAssistantMarkdown: (text) => `<p>${text}</p>`,
}));

import { useSkillEditor } from './composables/useSkillEditor.js';
import {
  importSummaryDraftPanelHint,
  importSummaryDraftPanelTitle,
} from './composables/useSkillImportSummaryDrafts.js';

function createEditor(emit = vi.fn(), overrides = {}) {
  const props = reactive({
    cwd: overrides.cwd ?? '',
    projectStore: overrides.projectStore ?? { state: { active: '/repo' } },
  });
  const vm = useSkillEditor(props, emit, { skillCards: ref(overrides.skillCards || []) });
  return { emit, vm };
}

beforeEach(() => {
  apiMock.callAPI.mockReset().mockResolvedValue({});
  apiMock.selectProjectDirs.mockReset().mockResolvedValue([]);
});

describe('useSkillEditor personal target payloads', () => {
  it('uses explicit cwd when opening skill details and project store is not ready', async () => {
    const { vm } = createEditor(vi.fn(), {
      cwd: '/thread-repo',
      projectStore: { state: { active: '.' } },
    });
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'skills/local/read' && payload?.path === '/skills/thread/SKILL.md') {
        return { skill: { content: '---\nname: ThreadSkill\n---\n# body' } };
      }
      if (method === 'skills/local/listFiles' && payload?.dir === '/skills/thread') {
        return { files: [{ name: 'SKILL.md', path: '/skills/thread/SKILL.md', is_main: true }] };
      }
      return {};
    });

    await vm.onEditSkill({ name: 'ThreadSkill', dir: '/skills/thread', scope: 'project' });

    expect(apiMock.callAPI).toHaveBeenCalledWith('skills/local/read', {
      path: '/skills/thread/SKILL.md',
      cwd: '/thread-repo',
    });
    expect(apiMock.callAPI).toHaveBeenCalledWith('skills/local/listFiles', {
      dir: '/skills/thread',
      cwd: '/thread-repo',
    });
  });

  it('uses explicit cwd when saving and project store is not ready', async () => {
    const { vm } = createEditor(vi.fn(), {
      cwd: '/thread-repo',
      projectStore: { state: { active: '' } },
    });

    vm.form.name = 'ThreadSkill';
    vm.form.description = 'Use when editing the current project skill.';
    vm.form.body = '## body';

    await vm.onSaveSkill();

    expect(apiMock.callAPI).toHaveBeenCalledWith('skills/local/write', expect.objectContaining({
      cwd: '/thread-repo',
      path: 'ThreadSkill',
    }));
  });

  it('uses explicit cwd for import summary reads and suggestions', async () => {
    const { vm } = createEditor(vi.fn(), {
      cwd: '/thread-repo',
      projectStore: { state: { active: '.' } },
    });
    apiMock.selectProjectDirs.mockResolvedValue(['/imports/MissingDesc']);
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'skills/local/importDir') {
        return { imported: [{ name: 'MissingDesc', skill_file: '/skills/imported/MissingDesc/SKILL.md' }], failures: [] };
      }
      if (method === 'skills/local/read' && payload?.path === '/skills/imported/MissingDesc/SKILL.md') {
        return { skill: { content: '---\nname: MissingDesc\n---\n# MissingDesc\nNeeds docs.' } };
      }
      if (method === 'skills/summary/suggest') {
        return { description: 'Use when filling in missing skill details.' };
      }
      return {};
    });

    await vm.onUploadSkill();
    await vm.confirmImportScope('personal');

    expect(apiMock.callAPI).toHaveBeenCalledWith('skills/local/importDir', expect.objectContaining({
      cwd: '/thread-repo',
    }));
    expect(apiMock.callAPI).toHaveBeenCalledWith('skills/local/read', {
      path: '/skills/imported/MissingDesc/SKILL.md',
      cwd: '/thread-repo',
    });
    expect(apiMock.callAPI).toHaveBeenCalledWith('skills/summary/suggest', expect.objectContaining({
      cwd: '/thread-repo',
      name: 'MissingDesc',
    }));
  });

  it('blocks local skill RPCs when cwd is not ready', async () => {
    const { vm } = createEditor(vi.fn(), {
      cwd: '',
      projectStore: { state: { active: '.' } },
    });

    await vm.onEditSkill({ name: 'ThreadSkill', dir: '/skills/thread', scope: 'project' });

    expect(apiMock.callAPI).not.toHaveBeenCalled();
    expect(vm.notice.level).toBe('error');
    expect(vm.notice.message).toContain('项目路径未就绪');
  });

  it('blocks summary suggestions when cwd is not ready', async () => {
    const { vm } = createEditor(vi.fn(), {
      cwd: '',
      projectStore: { state: { active: '.' } },
    });
    vm.form.name = 'ThreadSkill';
    vm.form.body = '## body';

    await vm.onSuggestSkillSummary();

    expect(apiMock.callAPI).not.toHaveBeenCalled();
    expect(vm.notice.level).toBe('error');
    expect(vm.notice.message).toContain('项目路径未就绪');
  });

  it('saves personal skills with user personal type', async () => {
    const { vm } = createEditor();

    vm.form.name = 'PersonalDocs';
    vm.form.summary = 'personal docs';
    vm.form.body = '## personal';
    vm.form.scope = 'personal';

    await vm.onSaveSkill();

    expect(apiMock.callAPI).toHaveBeenCalledWith('skills/local/write', {
      cwd: '/repo',
      path: 'PersonalDocs',
      content: expect.stringContaining('## personal'),
      scope: 'personal',
      personal_type: 'user',
    });
    expect(vm.notice.message).toBe('已保存');
  });

  it('keeps save non-blocking while warning when summary is too short', async () => {
    const { vm } = createEditor();

    vm.form.name = 'ShortDesc';
    vm.form.description = '处理问题';
    vm.form.body = '## body';

    await vm.onSaveSkill();

    expect(apiMock.callAPI).toHaveBeenCalledWith('skills/local/write', expect.objectContaining({
      path: 'ShortDesc',
    }));
    expect(vm.notice.level).toBe('success');
    expect(vm.notice.message).toBe('已保存。简介偏短，建议写清楚“什么时候使用”。');
  });

  it('keeps save non-blocking while warning when summary is too generic', async () => {
    const { vm } = createEditor();

    vm.form.name = 'GenericDesc';
    vm.form.description = '帮你处理各种问题';
    vm.form.body = '## body';

    await vm.onSaveSkill();

    expect(apiMock.callAPI).toHaveBeenCalledWith('skills/local/write', expect.objectContaining({
      path: 'GenericDesc',
    }));
    expect(vm.notice.level).toBe('success');
    expect(vm.notice.message).toBe('已保存。简介比较宽泛，建议补充具体场景。');
  });

  it('keeps save non-blocking while warning when summary describes workflow', async () => {
    const { vm } = createEditor();

    vm.form.name = 'WorkflowDesc';
    vm.form.description = '先读取文件，再分析内容，然后输出报告';
    vm.form.body = '## body';

    await vm.onSaveSkill();

    expect(apiMock.callAPI).toHaveBeenCalledWith('skills/local/write', expect.objectContaining({
      path: 'WorkflowDesc',
    }));
    expect(vm.notice.level).toBe('success');
    expect(vm.notice.message).toBe('已保存。建议把简介写成一句话：什么时候该用这个技能。');
  });

  it('shows one scenario keyword field and clears legacy force words after editing', async () => {
    const { vm } = createEditor();
    vm.form.triggerWordsText = 'bug, 调试';
    vm.form.forceWordsText = '必须, 强制';

    expect(vm.scenarioKeywordsText.value).toBe('bug, 调试, 必须, 强制');

    vm.scenarioKeywordsText.value = '测试失败, 后端';

    expect(vm.form.triggerWordsText).toBe('测试失败, 后端');
    expect(vm.form.forceWordsText).toBe('');
  });

  it('loads legacy force words into the single scenario field', async () => {
    const { vm } = createEditor();
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'skills/local/read' && payload?.path === '/skills/legacy/SKILL.md') {
        return { skill: { content: '---\nname: LegacySkill\ntrigger_words: ["bug", "@LegacySkill"]\nforce_words: ["必须", "bug", "[skill:LegacySkill]"]\n---\n## body' } };
      }
      if (method === 'skills/local/listFiles') return { files: [] };
      return {};
    });

    await vm.onEditSkill({ name: 'LegacySkill', dir: '/skills/legacy', scope: 'project' });

    expect(vm.form.triggerWordsText).toBe('bug, 必须');
    expect(vm.form.forceWordsText).toBe('');
    expect(vm.form.internalScenarioWordsText).toBe('@LegacySkill, [skill:LegacySkill]');
    expect(vm.scenarioKeywordsText.value).toBe('bug, 必须');
  });

  it('generates a skill summary suggestion and only applies it after confirmation', async () => {
    const { vm } = createEditor();
    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'skills/summary/suggest') {
        return { description: '当你需要编写或验证技能文件时使用。' };
      }
      return {};
    });
    vm.form.name = '编写技能';
    vm.form.body = '# 编写技能\n创建或修改 SKILL.md。';
    vm.scenarioKeywordsText.value = 'skill, @skill';

    await vm.onSuggestSkillSummary();

    expect(apiMock.callAPI).toHaveBeenCalledWith('skills/summary/suggest', {
      cwd: '/repo',
      name: '编写技能',
      description: '',
      content: '# 编写技能\n创建或修改 SKILL.md。',
      scenario_words: ['skill', '@skill'],
      scope: 'project',
    });
    expect(vm.form.description).toBe('');
    expect(vm.summarySuggestion.value).toBe('当你需要编写或验证技能文件时使用。');

    vm.applySummarySuggestion();

    expect(vm.form.description).toBe('当你需要编写或验证技能文件时使用。');
    expect(vm.summarySuggestion.value).toBe('');
  });

  it('retries summary suggestion once when the first response has no usable description', async () => {
    const { vm } = createEditor();
    apiMock.callAPI
      .mockResolvedValueOnce({})
      .mockResolvedValueOnce({ description: '当你需要调度多个互不依赖的任务时使用。' });
    vm.form.name = '调度并行代理';
    vm.form.body = '# 调度并行代理\n并行处理多个独立任务。';

    await vm.onSuggestSkillSummary();

    expect(apiMock.callAPI).toHaveBeenCalledTimes(2);
    expect(apiMock.callAPI).toHaveBeenNthCalledWith(1, 'skills/summary/suggest', expect.objectContaining({
      cwd: '/repo',
      name: '调度并行代理',
    }));
    expect(apiMock.callAPI).toHaveBeenNthCalledWith(2, 'skills/summary/suggest', expect.objectContaining({
      cwd: '/repo',
      name: '调度并行代理',
    }));
    expect(vm.summarySuggestion.value).toBe('当你需要调度多个互不依赖的任务时使用。');
    expect(vm.notice.message).toBe('已生成简介建议，采用后再保存。');
  });

  it('shows immediate feedback while summary suggestion is in progress', async () => {
    const { vm } = createEditor();
    let resolveSuggest = () => {};
    apiMock.callAPI.mockImplementation(() => new Promise((resolve) => {
      resolveSuggest = resolve;
    }));
    vm.form.name = '调度并行代理';

    const task = vm.onSuggestSkillSummary();
    await Promise.resolve();

    expect(vm.summarySuggesting.value).toBe(true);
    expect(vm.notice.message).toBe('正在生成简介...');

    resolveSuggest({ description: '当你需要调度多个互不依赖的任务时使用。' });
    await task;
    expect(vm.summarySuggesting.value).toBe(false);
  });

  it('does not request a summary suggestion when name and body are empty', async () => {
    const { vm } = createEditor();

    await vm.onSuggestSkillSummary();

    expect(apiMock.callAPI).not.toHaveBeenCalled();
    expect(vm.notice.level).toBe('info');
    expect(vm.notice.message).toBe('先填写技能名称或内容，再生成简介。');
  });

  it('shows a friendly summary generation message when dream executor is unavailable', async () => {
    const { vm } = createEditor();
    apiMock.callAPI.mockRejectedValueOnce(new Error('dream executor is not configured'));
    vm.form.name = '编写技能';

    await vm.onSuggestSkillSummary();

    expect(vm.notice.level).toBe('error');
    expect(vm.notice.message).toBe('当前无法生成简介，请稍后再试或手动填写。');
  });

  it('shows a friendly summary generation message when generated summary is low quality', async () => {
    const { vm } = createEditor();
    apiMock.callAPI.mockRejectedValueOnce(new Error('skill summary suggestion quality: generic'));
    vm.form.name = '通用助手';

    await vm.onSuggestSkillSummary();

    expect(vm.notice.level).toBe('error');
    expect(vm.notice.message).toBe('生成的简介不够具体，请补充技能内容后重新生成，或手动填写。');
  });

  it('does not expose technical errors when manual summary generation fails unexpectedly', async () => {
    const { vm } = createEditor();
    apiMock.callAPI.mockRejectedValueOnce(new Error('[-32098] dreamexec: claude exited with error: exit status 1'));
    vm.form.name = '安全工程师';

    await vm.onSuggestSkillSummary();

    expect(vm.notice.level).toBe('error');
    expect(vm.notice.message).toBe('当前无法生成简介，请稍后再试或手动填写。');
    expect(vm.notice.message).not.toContain('-32098');
    expect(vm.notice.message).not.toContain('claude');
  });

  it('imports personal skills into the imported personal type', async () => {
	const { vm } = createEditor();
    vm.importScope.value = 'personal';
    apiMock.selectProjectDirs.mockResolvedValue(['/imports/DocsSkill']);
    apiMock.callAPI.mockResolvedValueOnce({ imported: [], failures: [] });

    await vm.onUploadSkill();
    expect(apiMock.selectProjectDirs).not.toHaveBeenCalled();
    await vm.confirmImportScope('personal');

    expect(apiMock.selectProjectDirs).toHaveBeenCalledTimes(1);
    expect(apiMock.callAPI).toHaveBeenCalledWith('skills/local/importDir', {
      cwd: '/repo',
      paths: ['/imports/DocsSkill'],
      scope: 'personal',
	  personal_type: 'imported',
	});
  });

  it('creates import summary drafts for imported skills that need descriptions without writing files', async () => {
    const { vm } = createEditor();
    apiMock.selectProjectDirs.mockResolvedValue(['/imports/MissingDesc', '/imports/GoodDesc']);
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'skills/local/importDir') {
        return {
          imported: [
            { name: 'MissingDesc', skill_file: '/skills/imported/MissingDesc/SKILL.md' },
            { name: 'GoodDesc', skill_file: '/skills/imported/GoodDesc/SKILL.md' },
          ],
          failures: [],
        };
      }
      if (method === 'skills/local/read' && payload?.path === '/skills/imported/MissingDesc/SKILL.md') {
        return { skill: { content: '---\nname: MissingDesc\ntrigger_words: ["review"]\n---\n# MissingDesc\n帮助审查代码。' } };
      }
      if (method === 'skills/local/read' && payload?.path === '/skills/imported/GoodDesc/SKILL.md') {
        return { skill: { content: '---\nname: GoodDesc\ndescription: 当你需要整理项目文档时使用。\n---\n# GoodDesc\n整理文档。' } };
      }
      if (method === 'skills/summary/suggest') {
        return { description: '当你需要审查代码改动时使用。' };
      }
      return {};
    });

    await vm.onUploadSkill();
    await vm.confirmImportScope('personal');

    expect(apiMock.callAPI).toHaveBeenCalledWith('skills/summary/suggest', {
      cwd: '/repo',
      name: 'MissingDesc',
      description: '',
      content: '# MissingDesc\n帮助审查代码。',
      scenario_words: ['review'],
      scope: 'personal',
    });
    expect(apiMock.callAPI).not.toHaveBeenCalledWith('skills/summary/suggest', expect.objectContaining({
      name: 'GoodDesc',
    }));
    expect(apiMock.callAPI.mock.calls.some(([method]) => method === 'skills/local/write')).toBe(false);
    expect(vm.importSummaryDrafts.value).toEqual([
      expect.objectContaining({
        name: 'MissingDesc',
        skillFile: '/skills/imported/MissingDesc/SKILL.md',
        scope: 'personal',
        personalType: 'imported',
        suggestion: '当你需要审查代码改动时使用。',
        status: 'ready',
      }),
    ]);
    expect(vm.notice.message).toContain('已生成 1 条简介建议');
  });

  it('applies an import summary draft to the editor and still requires saving', async () => {
    const { vm } = createEditor();
    apiMock.selectProjectDirs.mockResolvedValue(['/imports/MissingDesc']);
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'skills/local/importDir') {
        return { imported: [{ name: 'MissingDesc', skill_file: '/skills/imported/MissingDesc/SKILL.md' }], failures: [] };
      }
      if (method === 'skills/local/read' && payload?.path === '/skills/imported/MissingDesc/SKILL.md') {
        return { skill: { content: '---\nname: MissingDesc\n---\n# MissingDesc\n帮助审查代码。' } };
      }
      if (method === 'skills/summary/suggest') {
        return { description: '当你需要审查代码改动时使用。' };
      }
      return {};
    });

    await vm.onUploadSkill();
    await vm.confirmImportScope('personal');
    apiMock.callAPI.mockClear();

    await vm.applyImportSummaryDraft(0);

    expect(vm.form.description).toBe('当你需要审查代码改动时使用。');
    expect(vm.isEditorOpen.value).toBe(true);
    expect(vm.notice.message).toBe('已采用简介建议，保存技能后生效。');
    expect(apiMock.callAPI.mock.calls.some(([method]) => method === 'skills/local/write')).toBe(false);
  });

  it('offers a legacy summary as an import draft without calling the summary generator', async () => {
    const { vm } = createEditor();
    apiMock.selectProjectDirs.mockResolvedValue(['/imports/SummaryOnly']);
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'skills/local/importDir') {
        return { imported: [{ name: 'SummaryOnly', skill_file: '/skills/imported/SummaryOnly/SKILL.md' }], failures: [] };
      }
      if (method === 'skills/local/read' && payload?.path === '/skills/imported/SummaryOnly/SKILL.md') {
        return { skill: { content: '---\nname: SummaryOnly\nsummary: 当你需要整理项目文档时使用。\n---\n# SummaryOnly\n整理文档。' } };
      }
      return {};
    });

    await vm.onUploadSkill();
    await vm.confirmImportScope('personal');

    expect(apiMock.callAPI.mock.calls.some(([method]) => method === 'skills/summary/suggest')).toBe(false);
    expect(vm.importSummaryDrafts.value).toEqual([
      expect.objectContaining({
        name: 'SummaryOnly',
        source: 'summary',
        suggestion: '当你需要整理项目文档时使用。',
        status: 'ready',
      }),
    ]);
  });

  it('does not show a failed summary draft when an imported skill already has a description', async () => {
    const { vm } = createEditor();
    apiMock.selectProjectDirs.mockResolvedValue(['/imports/ExistingDesc']);
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'skills/local/importDir') {
        return { imported: [{ name: 'ExistingDesc', skill_file: '/skills/imported/ExistingDesc/SKILL.md' }], failures: [] };
      }
      if (method === 'skills/local/read' && payload?.path === '/skills/imported/ExistingDesc/SKILL.md') {
        return { skill: { content: '---\nname: ExistingDesc\ndescription: 处理问题\n---\n# ExistingDesc\n帮助排查项目问题。' } };
      }
      if (method === 'skills/summary/suggest') {
        throw new Error('[-32098] dreamexec: claude exited with error: exit status 1');
      }
      return {};
    });

    await vm.onUploadSkill();
    await vm.confirmImportScope('personal');

    expect(vm.importSummaryDrafts.value).toEqual([]);
    expect(vm.notice.message).toBe('已导入 1 个技能目录（含资源文件）');
    expect(vm.notice.message).not.toContain('生成失败');
    expect(vm.notice.message).not.toContain('-32098');
    expect(vm.notice.message).not.toContain('claude');
  });

  it('can offer an improved summary draft for an imported skill with a weak existing description', async () => {
    const { vm } = createEditor();
    apiMock.selectProjectDirs.mockResolvedValue(['/imports/WeakDesc']);
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'skills/local/importDir') {
        return { imported: [{ name: 'WeakDesc', skill_file: '/skills/imported/WeakDesc/SKILL.md' }], failures: [] };
      }
      if (method === 'skills/local/read' && payload?.path === '/skills/imported/WeakDesc/SKILL.md') {
        return { skill: { content: '---\nname: WeakDesc\ndescription: 处理问题\n---\n# WeakDesc\n帮助排查项目问题。' } };
      }
      if (method === 'skills/summary/suggest') {
        return { description: '当你需要排查项目安全风险或安全配置问题时使用。' };
      }
      return {};
    });

    await vm.onUploadSkill();
    await vm.confirmImportScope('personal');

    expect(vm.importSummaryDrafts.value).toEqual([
      expect.objectContaining({
        name: 'WeakDesc',
        status: 'ready',
        suggestion: '当你需要排查项目安全风险或安全配置问题时使用。',
      }),
    ]);
  });

  it('shows a non-technical editable draft when an imported skill has no description and summary generation fails', async () => {
    const { vm } = createEditor();
    apiMock.selectProjectDirs.mockResolvedValue(['/imports/MissingDesc']);
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'skills/local/importDir') {
        return { imported: [{ name: 'MissingDesc', skill_file: '/skills/imported/MissingDesc/SKILL.md' }], failures: [] };
      }
      if (method === 'skills/local/read' && payload?.path === '/skills/imported/MissingDesc/SKILL.md') {
        return { skill: { content: '---\nname: MissingDesc\n---\n# MissingDesc\n帮助审查代码。' } };
      }
      if (method === 'skills/summary/suggest') {
        throw new Error('[-32098] dreamexec: claude exited with error: exit status 1');
      }
      return {};
    });

    await vm.onUploadSkill();
    await vm.confirmImportScope('personal');

    expect(vm.importSummaryDrafts.value).toEqual([
      expect.objectContaining({
        name: 'MissingDesc',
        status: 'error',
        error: '技能已正常导入。可以稍后重试，或手动补充简介。',
      }),
    ]);
    expect(vm.notice.message).toBe('已导入 1 个技能目录，1 个技能可手动补充简介。');
    expect(vm.importSummaryDrafts.value[0].error).not.toContain('-32098');
    expect(vm.importSummaryDrafts.value[0].error).not.toContain('claude');
  });

  it('marks imported same-name conflicts as conflict drafts instead of summary failures', async () => {
    const { vm } = createEditor();
    apiMock.selectProjectDirs.mockResolvedValue(['/imports/ProjectSkill']);
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'skills/local/importDir') {
        return {
          imported: [{ name: 'ProjectSkill', skill_file: '/skills/imported/ProjectSkill/SKILL.md' }],
          failures: [],
        };
      }
      if (method === 'skills/local/read' && payload?.path === '/skills/imported/ProjectSkill/SKILL.md') {
        throw new Error('[-31003] skill same-name conflict: ProjectSkill');
      }
      return {};
    });

    await vm.onUploadSkill();
    await vm.confirmImportScope('personal');

    expect(apiMock.callAPI.mock.calls.some(([method]) => method === 'skills/summary/suggest')).toBe(false);
    expect(vm.importSummaryDrafts.value).toEqual([
      expect.objectContaining({
        name: 'ProjectSkill',
        status: 'conflict',
        error: '已导入，但和项目共享技能同名，暂未启用。请在冲突提示中选择使用哪个版本。',
      }),
    ]);
    expect(vm.notice.message).toContain('1 个同名技能待处理');
    expect(vm.notice.message).not.toContain('简介建议生成失败');
  });

  it('treats imported inactive private duplicates as same-name conflicts instead of summary failures', async () => {
    const { vm } = createEditor();
    const importedPath = '/Users/mac/.super-dolphin/skills/personal/imported/编写计划/SKILL.md';
    apiMock.selectProjectDirs.mockResolvedValue(['/imports/编写计划']);
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'skills/local/importDir') {
        return {
          imported: [{ name: '编写计划', skill_file: importedPath }],
          failures: [],
        };
      }
      if (method === 'skills/local/read' && payload?.path === importedPath) {
        throw new Error(`[-32098] skill path is not in effective skill set: ${importedPath}`);
      }
      return {};
    });

    await vm.onUploadSkill();
    await vm.confirmImportScope('personal');

    expect(apiMock.callAPI.mock.calls.some(([method]) => method === 'skills/summary/suggest')).toBe(false);
    expect(vm.importSummaryDrafts.value).toEqual([
      expect.objectContaining({
        name: '编写计划',
        status: 'conflict',
        error: '已导入，但和项目共享技能同名，暂未启用。请在冲突提示中选择使用哪个版本。',
      }),
    ]);
    expect(vm.importSummaryDrafts.value[0].error).not.toContain('-32098');
    expect(vm.importSummaryDrafts.value[0].error).not.toContain(importedPath);
    expect(vm.notice.message).toContain('1 个同名技能待处理');
    expect(vm.notice.message).not.toContain('简介建议生成失败');
  });

  it('labels import conflict drafts as conflict handling instead of summary suggestions', async () => {
    const drafts = [{ status: 'conflict' }];

    expect(importSummaryDraftPanelTitle(drafts)).toBe('导入后需要处理');
    expect(importSummaryDraftPanelHint(drafts)).toBe('同名技能需要先选择使用哪个版本。');
  });

  it('preserves imported personal type after opening an imported personal skill', async () => {
	const { vm } = createEditor();
	vm.importScope.value = 'personal';
	apiMock.selectProjectDirs.mockResolvedValue(['/imports/DocsSkill']);
	apiMock.callAPI.mockImplementation(async (method, payload) => {
	  if (method === 'skills/local/importDir') {
	    return { imported: [{ name: 'DocsSkill', skill_file: '/skills/imported/DocsSkill/SKILL.md' }], failures: [] };
	  }
	  if (method === 'skills/local/read' && payload?.path === '/skills/imported/DocsSkill/SKILL.md') {
	    return { skill: { content: '---\nname: DocsSkill\nsummary: imported docs\n---\n## body' } };
	  }
	  return {};
	});

		await vm.onUploadSkill();
		await vm.confirmImportScope('personal');
		vm.form.body = '## updated';
	await vm.onSaveSkill();

	expect(vm.form.scope).toBe('personal');
	expect(vm.form.personal_type).toBe('imported');
	expect(apiMock.callAPI).toHaveBeenCalledWith('skills/local/write', expect.objectContaining({
	  path: '/skills/imported/DocsSkill/SKILL.md',
	  scope: 'personal',
	  personal_type: 'imported',
	}));
  });

  it('saves generated descriptions back to the loaded main file when the folder and skill name differ', async () => {
    const { vm } = createEditor(vi.fn(), {
      skillCards: [{
        name: 'agentic-engineering',
        dir: '/repo/.agent/skills/Agent工程学',
        scope: 'project',
      }],
    });
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'skills/local/read' && payload?.path === '/repo/.agent/skills/Agent工程学/SKILL.md') {
        return { skill: { content: '---\nname: agentic-engineering\n---\n# Agent 工程学\n拆分任务。' } };
      }
      if (method === 'skills/local/listFiles') {
        return { files: [{ name: 'SKILL.md', path: '/repo/.agent/skills/Agent工程学/SKILL.md', is_main: true }] };
      }
      if (method === 'skills/summary/suggest') {
        return { description: '当你需要拆分或管理多步骤 Agent 工程任务时使用。' };
      }
      return {};
    });

    await vm.onEditSkill({
      name: 'agentic-engineering',
      dir: '/repo/.agent/skills/Agent工程学',
      scope: 'project',
    });
    await vm.onSuggestSkillSummary();
    vm.applySummarySuggestion();
    await vm.onSaveSkill();

    expect(apiMock.callAPI).toHaveBeenCalledWith('skills/local/write', expect.objectContaining({
      path: '/repo/.agent/skills/Agent工程学/SKILL.md',
      scope: 'project',
    }));
  });

  it('rejects invalid skill names before saving a main skill file', async () => {
    const { vm } = createEditor();
    vm.form.name = 'Agent/工程学';
    vm.form.description = '当你需要拆分或管理多步骤 Agent 工程任务时使用。';
    vm.form.body = '# Agent/工程学\n拆分任务。';

    await vm.onSaveSkill();

    expect(apiMock.callAPI).not.toHaveBeenCalled();
    expect(vm.notice.level).toBe('error');
    expect(vm.notice.message).toBe('技能名称不能包含非法字符，请使用中文、英文、数字、- 或 _；带空格的展示文本请填写显示名称。');
  });

  it('warns about same-name conflicts without claiming imports overwrite other scopes', async () => {
	const { vm } = createEditor(vi.fn(), { skillCards: [{ name: 'DocsSkill', scope: 'project' }] });
	vm.importScope.value = 'personal';
	apiMock.selectProjectDirs.mockResolvedValue(['/imports/DocsSkill']);
	let resolveImport = () => {};
	apiMock.callAPI.mockImplementation((method) => {
	  if (method === 'skills/local/importDir') {
	    return new Promise((resolve) => {
	      resolveImport = resolve;
	    });
	  }
	  return Promise.resolve({});
	});

		await vm.onUploadSkill();
		const importTask = vm.confirmImportScope('personal');
		await Promise.resolve();
	await Promise.resolve();

	expect(vm.notice.message).toContain('同名技能');
	expect(vm.notice.message).not.toContain('覆盖');

	resolveImport({ imported: [], failures: [] });
	await importTask;
  });

  it('reports duplicate imports as already existing instead of a generic failure', async () => {
	const { vm, emit } = createEditor();
	apiMock.selectProjectDirs.mockResolvedValue(['/imports/DocsSkill']);
	apiMock.callAPI.mockImplementation(async (method) => {
	  if (method === 'skills/local/importDir') {
	    return {
	      imported: [],
	      failures: [{ source: '/imports/DocsSkill', error: 'skill already exists: DocsSkill' }],
	    };
	  }
	  return {};
	});

	await vm.onUploadSkill();
	await vm.confirmImportScope('project');

	expect(emit).toHaveBeenCalledWith('refresh-skills');
	expect(vm.notice.level).toBe('info');
	expect(vm.notice.message).toContain('项目共享里已存在：DocsSkill');
	expect(vm.notice.message).toContain('未重复导入');
	expect(vm.notice.message).not.toContain('失败');
	expect(vm.notice.message).not.toContain('成功 0');
	expect(vm.importFailures.value).toEqual(['/imports/DocsSkill：DocsSkill 已存在，未重复导入。']);
  });

  it('keeps same-name conflict warning in successful save notice', async () => {
	const { vm } = createEditor(vi.fn(), { skillCards: [{ name: 'DocsSkill', scope: 'project' }] });
	vm.form.name = 'DocsSkill';
	vm.form.summary = 'docs';
	vm.form.body = '## body';
	vm.form.scope = 'personal';
	vm.form.personal_type = 'imported';

	await vm.onSaveSkill();

	expect(vm.notice.level).toBe('success');
	expect(vm.notice.message).toContain('已经有同名技能');
	expect(apiMock.callAPI).toHaveBeenCalledWith('skills/local/write', expect.objectContaining({
	  path: 'DocsSkill',
	  scope: 'personal',
	  personal_type: 'imported',
	}));
  });

  it('deletes personal skills with their personal type', async () => {
    const { vm } = createEditor();

    vm.onDeleteSkill({ name: 'DocsSkill', scope: 'personal', personal_type: 'agent' });
    await vm.confirmSkillDelete();

    expect(apiMock.callAPI).toHaveBeenCalledWith('skills/local/delete', {
      name: 'DocsSkill',
      cwd: '/repo',
      scope: 'personal',
      personal_type: 'agent',
    });
  });

  it('does not clear a project editor when deleting a same-name personal skill', async () => {
    const { vm } = createEditor(vi.fn(), {
      skillCards: [
        { name: 'DocsSkill', scope: 'project', dir: '/repo/.agent/skills/docs' },
        { name: 'DocsSkill', scope: 'personal', personal_type: 'agent', dir: '/home/skills/agent/docs' },
      ],
    });
    vm.selectedSkillName.value = 'DocsSkill';
    vm.sourcePath.value = '/repo/.agent/skills/docs/SKILL.md';
    vm.activeSkillFilePath.value = '/repo/.agent/skills/docs/SKILL.md';
    vm.form.name = 'DocsSkill';
    vm.form.scope = 'project';
    vm.form.body = 'project body';
    vm.isEditorOpen.value = true;

    vm.onDeleteSkill({ name: 'DocsSkill', scope: 'personal', personal_type: 'agent', dir: '/home/skills/agent/docs' });
    await vm.confirmSkillDelete();

    expect(vm.selectedSkillName.value).toBe('DocsSkill');
    expect(vm.form.name).toBe('DocsSkill');
    expect(vm.form.scope).toBe('project');
    expect(vm.form.body).toBe('project body');
    expect(vm.sourcePath.value).toBe('/repo/.agent/skills/docs/SKILL.md');
    expect(vm.isEditorOpen.value).toBe(true);
  });

  it('preserves existing personal type when saving a loaded main skill', async () => {
    const { vm } = createEditor();
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'skills/local/read' && payload?.path === '/skills/agent-docs/SKILL.md') {
        return { skill: { content: '---\nname: AgentDocs\nsummary: agent docs\n---\n## body' } };
      }
      if (method === 'skills/local/listFiles') return { files: [] };
      return {};
    });

    await vm.onEditSkill({
      name: 'AgentDocs',
      dir: '/skills/agent-docs',
      scope: 'personal',
      personal_type: 'agent',
      summary: 'agent docs',
    });
    vm.form.body = '## updated';
    await vm.onSaveSkill();

    expect(vm.form.personal_type).toBe('agent');
    expect(apiMock.callAPI).toHaveBeenCalledWith('skills/local/write', expect.objectContaining({
      path: '/skills/agent-docs/SKILL.md',
      scope: 'personal',
      personal_type: 'agent',
    }));
  });

  it('preserves existing personal type when saving a loaded subfile', async () => {
    const { vm } = createEditor();
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'skills/local/read' && payload?.path === '/skills/hub-docs/SKILL.md') {
        return { skill: { content: '---\nname: HubDocs\nsummary: hub docs\n---\n## body' } };
      }
      if (method === 'skills/local/listFiles') {
        return { files: [{ name: 'SKILL.md', path: '/skills/hub-docs/SKILL.md', is_main: true }, { name: 'guide.md', path: '/skills/hub-docs/references/guide.md', is_main: false }] };
      }
      return {};
    });

    await vm.onEditSkill({
      name: 'HubDocs',
      dir: '/skills/hub-docs',
      scope: 'personal',
      personal_type: 'hub',
      summary: 'hub docs',
    });
    vm.activeSkillFilePath.value = '/skills/hub-docs/references/guide.md';
    vm.form.body = 'guide update';
    await vm.onSaveSkill();

    expect(vm.form.personal_type).toBe('hub');
    expect(apiMock.callAPI).toHaveBeenCalledWith('skills/local/write', expect.objectContaining({
      path: '/skills/hub-docs/references/guide.md',
      scope: 'personal',
      personal_type: 'hub',
    }));
  });
});
