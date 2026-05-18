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

function createEditor(emit = vi.fn(), overrides = {}) {
  const props = reactive({ projectStore: { state: { active: '/repo' } } });
  const vm = useSkillEditor(props, emit, { skillCards: ref(overrides.skillCards || []) });
  return { emit, vm };
}

beforeEach(() => {
  apiMock.callAPI.mockReset().mockResolvedValue({});
  apiMock.selectProjectDirs.mockReset().mockResolvedValue([]);
});

describe('useSkillEditor personal target payloads', () => {
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
	  path: 'DocsSkill',
	  scope: 'personal',
	  personal_type: 'imported',
	}));
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
      path: 'AgentDocs',
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
