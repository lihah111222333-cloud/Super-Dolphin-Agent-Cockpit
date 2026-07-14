import { describe, expect, it, vi } from 'vitest';
import {
  confirmDeleteSkill,
  importDatasourceSelection,
  saveSkillEditor,
} from './SkillsPage.jsx';

const skillMarkdown = [
  '---',
  'name: "deploy"',
  'display_name: "Deploy"',
  'description: "Deploy services"',
  'trigger_words: ["release", "service"]',
  '---',
  '',
  'Run deploy safely.',
].join('\n');

function skillEditorContext(overrides = {}) {
  const state = {
    activeSkillPath: '',
    deleteTarget: null,
    editorForm: {
      body: 'Run deploy safely.',
      description: 'Deploy services',
      displayName: 'Deploy',
      keywords: 'release, service',
      name: 'deploy',
      personalType: '',
      scope: 'project',
    },
  };
  return {
    facade: {},
    projectPath: '/repo/app',
    refreshSkillSurface: vi.fn().mockResolvedValue(undefined),
    setError: vi.fn(),
    setNotice: vi.fn(),
    setPatch: vi.fn(),
    state,
    ...overrides,
  };
}

describe('skills ignored-result actions', () => {
  it('ignores malformed datasource import body and publishes import success', async () => {
    const facade = { importDatasourceLocalFile: vi.fn().mockResolvedValue({ malformed: 'datasource-import-sentinel' }) };
    const ctx = {
      facade,
      invalidateDocuments: vi.fn().mockResolvedValue(undefined),
      pickerToken: 'picker-token',
      setNotice: vi.fn(),
      setSourcePath: vi.fn(),
      sourcePath: 'C:\\data\\source.pdf',
      successText: '资料已导入',
    };

    const result = await importDatasourceSelection(ctx);

    expect(result).toBeUndefined();
    expect(facade.importDatasourceLocalFile).toHaveBeenCalledWith({
      pickerToken: 'picker-token',
      sourcePath: 'C:\\data\\source.pdf',
    });
    expect(ctx.setSourcePath).toHaveBeenCalledWith('');
    expect(ctx.setNotice).toHaveBeenLastCalledWith('资料已导入');
    expect(ctx.invalidateDocuments).toHaveBeenCalledWith();
    expect(ctx.setSourcePath.mock.invocationCallOrder[0]).toBeLessThan(ctx.setNotice.mock.invocationCallOrder[0]);
    expect(ctx.setNotice.mock.invocationCallOrder[0]).toBeLessThan(ctx.invalidateDocuments.mock.invocationCallOrder[0]);
  });

  it('ignores malformed create-skill body and publishes save success', async () => {
    const facade = { createSkill: vi.fn().mockResolvedValue({ malformed: 'create-skill-sentinel' }), writeSkill: vi.fn() };
    const ctx = skillEditorContext({ facade });

    const result = await saveSkillEditor(ctx);

    expect(result).toBeUndefined();
    expect(facade.createSkill).toHaveBeenCalledWith({
      content: skillMarkdown,
      cwd: '/repo/app',
      name: 'deploy',
    });
    expect(facade.writeSkill).not.toHaveBeenCalled();
    expect(ctx.setPatch).toHaveBeenCalledWith({ editorOpen: false });
    expect(ctx.refreshSkillSurface).toHaveBeenCalledWith();
    expect(ctx.setNotice).toHaveBeenLastCalledWith('已保存');
    expect(ctx.refreshSkillSurface.mock.invocationCallOrder[0]).toBeLessThan(ctx.setNotice.mock.invocationCallOrder.at(-1));
    expect(ctx.setPatch).toHaveBeenLastCalledWith({ saving: false });
  });

  it('ignores malformed write-skill body and publishes save success', async () => {
    const facade = { createSkill: vi.fn(), writeSkill: vi.fn().mockResolvedValue(['write-skill-malformed-sentinel']) };
    const activeSkillPath = '/repo/app/.agents/skills/deploy/SKILL.md';
    const base = skillEditorContext({ facade });
    const ctx = { ...base, state: { ...base.state, activeSkillPath } };

    const result = await saveSkillEditor(ctx);

    expect(result).toBeUndefined();
    expect(facade.writeSkill).toHaveBeenCalledWith({
      content: skillMarkdown,
      cwd: '/repo/app',
      path: activeSkillPath,
      personal_type: '',
      scope: 'project',
    });
    expect(facade.createSkill).not.toHaveBeenCalled();
    expect(ctx.setPatch).toHaveBeenCalledWith({ editorOpen: false });
    expect(ctx.refreshSkillSurface).toHaveBeenCalledWith();
    expect(ctx.setNotice).toHaveBeenLastCalledWith('已保存');
    expect(ctx.refreshSkillSurface.mock.invocationCallOrder[0]).toBeLessThan(ctx.setNotice.mock.invocationCallOrder.at(-1));
    expect(ctx.setPatch).toHaveBeenLastCalledWith({ saving: false });
  });

  it('ignores malformed delete-skill body and publishes delete success', async () => {
    const facade = { deleteSkill: vi.fn().mockResolvedValue({ malformed: 'delete-skill-sentinel' }) };
    const base = skillEditorContext({ facade });
    const ctx = {
      ...base,
      state: {
        ...base.state,
        deleteTarget: { name: 'deploy', personalType: '', scope: 'project', title: 'Deploy' },
      },
    };

    const result = await confirmDeleteSkill(ctx);

    expect(result).toBeUndefined();
    expect(facade.deleteSkill).toHaveBeenCalledWith({
      cwd: '/repo/app',
      name: 'deploy',
      personal_type: '',
      scope: 'project',
    });
    expect(ctx.setPatch).toHaveBeenCalledWith({ deleteTarget: null });
    expect(ctx.refreshSkillSurface).toHaveBeenCalledWith();
    expect(ctx.setNotice).toHaveBeenLastCalledWith('已删除 Deploy');
    expect(ctx.refreshSkillSurface.mock.invocationCallOrder[0]).toBeLessThan(ctx.setNotice.mock.invocationCallOrder.at(-1));
    expect(ctx.setPatch).toHaveBeenLastCalledWith({ deleting: false });
  });
});
