// @ts-nocheck
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ref } from '../lib/vue.esm-browser.prod.js';

const apiMock = vi.hoisted(() => ({
  listSkillResolutions: vi.fn(),
  previewSkillResolution: vi.fn(),
  applySkillResolution: vi.fn(),
}));

vi.mock('./services/skills-api.js', () => ({
  listSkillResolutions: apiMock.listSkillResolutions,
  previewSkillResolution: apiMock.previewSkillResolution,
  applySkillResolution: apiMock.applySkillResolution,
}));

import { useSkillResolutions } from './composables/useSkillResolutions.js';

function createResolutions(emit = vi.fn()) {
  const notice = { level: '', message: '' };
  const vm = useSkillResolutions({
    activeCwdSource: ref('/repo'),
    emit,
    setNotice: (level, message) => {
      notice.level = level;
      notice.message = message;
    },
  });
  return { emit, notice, vm };
}

beforeEach(() => {
  apiMock.listSkillResolutions.mockReset().mockResolvedValue([]);
  apiMock.previewSkillResolution.mockReset().mockResolvedValue({});
  apiMock.applySkillResolution.mockReset().mockResolvedValue({});
  globalThis.window = { prompt: vi.fn(() => 'copy') };
});

describe('useSkillResolutions', () => {
  it('loads resolution conflicts from the active cwd', async () => {
    const { notice, vm } = createResolutions();
    apiMock.listSkillResolutions.mockResolvedValueOnce([
      { conflict_id: 'c1', name: 'DocsSkill', kind: 'mirror_drift', available_actions: ['sync_back_to_canonical'] },
    ]);

    await vm.refreshSkillResolutions();

    expect(apiMock.listSkillResolutions).toHaveBeenCalledWith('/repo');
    expect(vm.resolutionConflicts.value).toEqual([
      { conflict_id: 'c1', name: 'DocsSkill', kind: 'mirror_drift', available_actions: ['sync_back_to_canonical'] },
    ]);
    expect(notice.message).toContain('发现 1 个技能冲突');
  });

  it('keeps existing conflicts and can notify when a silent refresh fails', async () => {
    const { notice, vm } = createResolutions();
    vm.resolutionConflicts.value = [{ conflict_id: 'existing-conflict', name: 'DocsSkill' }];
    apiMock.listSkillResolutions.mockRejectedValueOnce(new Error('rpc offline'));

    await vm.refreshSkillResolutions({ notify: false, notifyOnError: true });

    expect(vm.resolutionConflicts.value.map((item) => item.conflict_id)).toEqual(['existing-conflict']);
    expect(notice.level).toBe('error');
    expect(notice.message).toContain('读取技能冲突失败');
  });

  it('filters unsupported same-name mutating actions and keeps view-only resolution', async () => {
    const { vm } = createResolutions();
    apiMock.listSkillResolutions.mockResolvedValueOnce([
      {
        conflict_id: 'same-1',
        name: 'DocsSkill',
        kind: 'same_name',
        available_actions: ['view_diff', 'rename_personal', 'disable_personal_for_project', 'keep_selected'],
        sources: [
          { scope: 'project', canonical_id: 'project/DocsSkill', canonical_hash: 'hp' },
          { scope: 'personal', personal_type: 'user', canonical_id: 'personal/user/DocsSkill', canonical_hash: 'hu' },
        ],
      },
      {
        conflict_id: 'drift-1',
        name: 'BuildSkill',
        kind: 'mirror_drift',
        available_actions: ['view_diff', 'sync_back_to_canonical'],
      },
    ]);

    await vm.refreshSkillResolutions();

    expect(vm.resolutionConflicts.value[0].available_actions).toEqual(['view_diff', 'disable_personal_for_project', 'keep_selected']);
    expect(vm.resolutionActionUnsupported('rename_personal')).toBe(true);
    expect(vm.resolutionActionEntries(vm.resolutionConflicts.value[1]).map((item) => item.action)).toEqual(['view_diff', 'sync_back_to_canonical']);
  });

  it('shows conflict count near the check button and can collapse the conflict panel', async () => {
    const { vm } = createResolutions();
    expect(vm.resolutionCheckButtonText.value).toBe('检查冲突');
    expect(vm.showResolutionCheckButton.value).toBe(false);
    expect(vm.showResolutionPanel.value).toBe(false);

    vm.resolutionConflicts.value = [{ conflict_id: 'c1' }, { conflict_id: 'c2' }];
    expect(vm.resolutionCheckButtonText.value).toBe('发现 2 个冲突');
    expect(vm.showResolutionCheckButton.value).toBe(true);
    expect(vm.resolutionPanelToggleText.value).toBe('收起冲突');
    expect(vm.showResolutionPanel.value).toBe(true);

    vm.toggleResolutionPanel();
    expect(vm.resolutionPanelCollapsed.value).toBe(true);
    expect(vm.resolutionPanelToggleText.value).toBe('展开冲突');
    expect(vm.showResolutionPanel.value).toBe(false);
  });

  it('uses user-facing labels for conflicts, actions and external apps', () => {
    const { vm } = createResolutions();

    expect(vm.resolutionTitle({ name: 'DocsSkill', kind: 'mirror_drift' })).toBe('DocsSkill · 外部版本有改动');
    expect(vm.resolutionConflictAlertText.value).toBe('');
    vm.resolutionConflicts.value = [{ conflict_id: 'c1' }, { conflict_id: 'c2' }];
    expect(vm.resolutionConflictAlertText.value).toBe('发现 2 个技能冲突，需要处理后再使用，避免 Claude 或 Codex 读到不同版本。');
    expect(vm.resolutionActionLabel('view_diff')).toBe('查看两个版本');
    expect(vm.resolutionActionLabel('sync_back_to_canonical')).toBe('用外部修改更新本项目');
    expect(vm.resolutionActionLabel('canonical_overwrite_mirror')).toBe('用本项目内容覆盖外部版本');
    expect(vm.resolutionActionLabel('save_as_new_skill')).toBe('另存为新技能');
    expect(vm.resolutionActionLabel('import_to_personal_imported')).toBe('导入到私人使用');
    expect(vm.resolutionActionLabel('disable_personal_for_project')).toBe('使用项目共享版本');
    expect(vm.resolutionActionLabel('keep_selected')).toBe('使用选中的版本');
    expect(vm.resolutionActionHelp('sync_back_to_canonical')).toBe('保留 Claude/Codex 里的修改，写回本项目管理的技能。');
    expect(vm.resolutionActionHelp('canonical_overwrite_mirror')).toBe('丢弃 Claude/Codex 里的修改，用本项目当前技能重新同步。');
    expect(vm.resolutionActionHelp('disable_personal_for_project')).toBe('当前项目使用项目共享版本，不删除私人技能。');
    expect(vm.resolutionActionHelp('keep_selected')).toBe('选择后优先使用这个版本，不删除其他同名技能。');
    expect(vm.resolutionActionSectionTitle({ kind: 'same_name' })).toBe('选择使用哪个版本');
    expect(vm.resolutionActionSectionTitle({ kind: 'mirror_drift' })).toBe('处理方式');
    expect(vm.resolutionActionFootnote({ kind: 'same_name' })).toBe('选择后只会设置优先使用的版本，不会删除其他技能。以后也可以通过改名或删除来彻底消除冲突。');
    expect(vm.resolutionActionFootnote({ kind: 'mirror_drift' })).toBe('');
    expect(vm.resolutionConflictGuide({ kind: 'same_name' })).toBe('发现多个同名技能。请选择要优先使用的版本，其他版本不会被删除。');
    expect(vm.resolutionConflictGuide({
      kind: 'same_name',
      sources: [
        { scope: 'personal', personal_type: 'hub' },
        { scope: 'personal', personal_type: 'user' },
      ],
    })).toBe('发现多个同名的私人技能。请选择要优先使用的版本，其他版本不会被删除。');
    expect(vm.resolutionConflictGuide({ kind: 'mirror_drift' })).toBe('外部应用里的技能和本项目管理的技能不一致。请选择下面一种处理方式。');
    expect(vm.resolutionManualSteps({
      kind: 'same_name',
      available_actions: ['view_diff', 'disable_personal_for_project', 'keep_selected'],
    })).toEqual([]);
    expect(vm.resolutionManualSteps({ kind: 'same_name' })).toEqual([
      '要保留项目共享：编辑或删除同名私人技能。',
      '要保留私人使用：编辑项目共享技能改名，或删除项目共享技能。',
      '两边都要保留：把其中一个改成更明确的名字。',
    ]);
    expect(vm.resolutionManualSteps({ kind: 'mirror_drift' })).toEqual([]);
    expect(vm.resolutionProviderLabel('claude')).toBe('Claude');
    expect(vm.resolutionProviderLabel('codex')).toBe('Codex');
    expect(vm.resolutionProviderLabel('')).toBe('外部应用');
  });

  it('creates same-name one-click action entries with source-bound payload fields', () => {
    const { vm } = createResolutions();
    const conflict = {
      kind: 'same_name',
      available_actions: ['view_diff', 'disable_personal_for_project', 'keep_selected'],
      sources: [
        { scope: 'project', canonical_id: 'project/DocsSkill' },
        { scope: 'personal', personal_type: 'user', canonical_id: 'personal/user/DocsSkill' },
      ],
    };

    const entries = vm.resolutionActionEntries(conflict);
    expect(entries.map((entry) => vm.resolutionActionEntryLabel(entry))).toEqual([
      '使用项目共享版本',
      '使用自己创建的私人版本',
    ]);
    expect(vm.resolutionApplyKey({ conflict_id: 'same-1' }, 'keep_selected', entries[1])).toContain('personal/user/DocsSkill');
  });

  it('labels multiple personal same-name choices by the version users recognize', () => {
    const { vm } = createResolutions();
    const conflict = {
      kind: 'same_name',
      available_actions: ['view_diff', 'keep_selected'],
      sources: [
        { scope: 'personal', personal_type: 'hub', canonical_id: 'personal/hub/DocsSkill' },
        { scope: 'personal', personal_type: 'user', canonical_id: 'personal/user/DocsSkill' },
      ],
    };

    const entries = vm.resolutionActionEntries(conflict);

    expect(entries.map((entry) => vm.resolutionActionEntryLabel(entry))).toEqual([
      '使用市场下载的版本',
      '使用自己创建的版本',
    ]);
    expect(entries.map((entry) => vm.resolutionActionEntryHelp(entry))).toEqual([
      '之后优先使用这个市场下载的版本，其他同名技能不会被删除。',
      '之后优先使用这个自己创建的版本，其他同名技能不会被删除。',
    ]);
    expect(new Set(entries.map((entry) => vm.resolutionActionEntryLabel(entry))).size).toBe(entries.length);
  });

  it('summarizes previews in user-facing language instead of raw hashes', () => {
    const { vm } = createResolutions();
    const viewItem = {
      provider: 'codex',
      source_path: '/repo/.codex/skills/deploy/SKILL.md',
      target_path: '/repo/.agent/skills/deploy/SKILL.md',
      source_hash: '8b022cc49401abd24425d711fe24aed33d49ddb7dff41bbd2a6bc69e4909af22c',
      target_hash: '854b60866d3b76b7c95ccbc4ec856459624dc622d34971865412b47b56fa840d',
      diff: 'source 8b022... /repo/.codex/skills/deploy\n target 854b... /repo/.agent/skills/deploy',
    };
    const overwriteItem = {
      provider: 'codex',
      source_path: '/repo/.agent/skills/deploy/SKILL.md',
      target_path: '/repo/.codex/skills/deploy/SKILL.md',
      source_hash: '854b60866d3b76b7c95ccbc4ec856459624dc622d34971865412b47b56fa840d',
      target_hash: '8b022cc49401abd24425d711fe24aed33d49ddb7dff41bbd2a6bc69e4909af22c',
    };

    expect(vm.resolutionPreviewIntro({ action: 'view_diff' })).toBe('下面只说明两个版本分别在哪里，不会修改文件。');
    expect(vm.resolutionPreviewItemSummary(viewItem, 'view_diff')).toBe('Codex 里的版本和本项目管理的版本不一致。');
    expect(vm.resolutionPreviewItemPaths(viewItem, 'view_diff')).toEqual([
      { label: '外部版本', value: '/repo/.codex/skills/deploy/SKILL.md' },
      { label: '本项目版本', value: '/repo/.agent/skills/deploy/SKILL.md' },
    ]);
    expect(vm.resolutionPreviewItemPaths(overwriteItem, 'canonical_overwrite_mirror')).toEqual([
      { label: '本项目版本', value: '/repo/.agent/skills/deploy/SKILL.md' },
      { label: '外部版本', value: '/repo/.codex/skills/deploy/SKILL.md' },
    ]);
    expect(vm.resolutionShortHash(viewItem.source_hash)).toBe('8b022cc4');
  });

  it('rejects legacy same-name rename without previewing', async () => {
    const { vm } = createResolutions();
    globalThis.window.prompt = vi.fn(() => 'DocsUserCopy');
    apiMock.previewSkillResolution.mockResolvedValueOnce({ items: [{ preview_id: 'p-same', preview_hash: 'h-same' }] });

    await vm.onApplyResolution({
      conflict_id: 'same-2',
      name: 'DocsSkill',
      kind: 'same_name',
      sources: [
        { scope: 'project', canonical_id: 'project/DocsSkill' },
        { scope: 'personal', personal_type: 'user', canonical_id: 'personal/user/DocsSkill' },
      ],
    }, 'rename_personal');

    expect(apiMock.previewSkillResolution).not.toHaveBeenCalled();
  });

  it('previews a selected mutating action and waits for explicit confirmation before apply', async () => {
    const { emit, vm } = createResolutions();
    const conflict = {
      conflict_id: 'c1',
      name: 'DocsSkill',
      scope: 'project',
      kind: 'mirror_drift',
      provider_entries: [{ provider: 'claude', source_path_id: 'provider:claude' }],
    };
    apiMock.previewSkillResolution.mockResolvedValueOnce({
      items: [{ preview_id: 'p1', preview_hash: 'h1', provider: 'claude', source_provider: 'claude', source_path_id: 'provider:claude' }],
    });
    apiMock.listSkillResolutions.mockResolvedValueOnce([]);

    await vm.onApplyResolution(conflict, 'sync_back_to_canonical', conflict.provider_entries[0]);

    expect(apiMock.previewSkillResolution).toHaveBeenCalledWith({
      cwd: '/repo',
      conflictId: 'c1',
      name: 'DocsSkill',
      scope: 'project',
      personalType: '',
      provider: 'claude',
      sourceProvider: 'claude',
      sourcePathId: 'provider:claude',
      action: 'sync_back_to_canonical',
      newName: '',
    });
    expect(vm.resolutionPreview.value?.items?.[0]).toEqual(expect.objectContaining({
      preview_id: 'p1',
      preview_hash: 'h1',
      source_provider: 'claude',
    }));
    expect(apiMock.applySkillResolution).not.toHaveBeenCalled();
    expect(emit).not.toHaveBeenCalledWith('refresh-skills');
  });

  it('applies same-name one-click policy actions after generating preview proof', async () => {
    const { emit, vm } = createResolutions();
    const conflict = {
      conflict_id: 'same-1',
      name: 'DocsSkill',
      kind: 'same_name',
      available_actions: ['disable_personal_for_project'],
      sources: [
        { scope: 'project', canonical_id: 'project/DocsSkill' },
        { scope: 'personal', personal_type: 'user', canonical_id: 'personal/user/DocsSkill' },
      ],
    };
    const actionEntry = vm.resolutionActionEntries(conflict).find((entry) => entry.action === 'disable_personal_for_project');
    apiMock.previewSkillResolution.mockResolvedValueOnce({
      items: [{ preview_id: 'same-preview', preview_hash: 'same-hash' }],
    });
    apiMock.listSkillResolutions.mockResolvedValueOnce([]);

    await vm.onApplyResolution(conflict, actionEntry.action, actionEntry);

    expect(apiMock.previewSkillResolution).toHaveBeenCalledWith(expect.objectContaining({
      action: 'disable_personal_for_project',
      disablePolicyTarget: 'personal/user/DocsSkill',
    }));
    expect(apiMock.applySkillResolution).toHaveBeenCalledWith(expect.objectContaining({
      action: 'disable_personal_for_project',
      previewId: 'same-preview',
      previewHash: 'same-hash',
      disablePolicyTarget: 'personal/user/DocsSkill',
    }));
    expect(emit).toHaveBeenCalledWith('refresh-skills');
  });

  it('applies the stored resolution preview only after user confirmation', async () => {
    const { emit, vm } = createResolutions();
    const conflict = {
      conflict_id: 'c1',
      name: 'DocsSkill',
      scope: 'project',
      kind: 'mirror_drift',
      provider_entries: [{ provider: 'claude', source_path_id: 'provider:claude' }],
    };
    apiMock.previewSkillResolution.mockResolvedValueOnce({
      items: [{ preview_id: 'p1', preview_hash: 'h1', provider: 'claude', source_provider: 'claude', source_path_id: 'provider:claude' }],
    });
    apiMock.listSkillResolutions.mockResolvedValueOnce([]);

    await vm.onApplyResolution(conflict, 'sync_back_to_canonical', conflict.provider_entries[0]);
    await vm.confirmResolutionPreview();

    expect(apiMock.applySkillResolution).toHaveBeenCalledWith(expect.objectContaining({
      action: 'sync_back_to_canonical',
      previewId: 'p1',
      previewHash: 'h1',
      sourceProvider: 'claude',
    }));
    expect(emit).toHaveBeenCalledWith('refresh-skills');
  });

  it('previews read-only actions without applying', async () => {
    const { emit, notice, vm } = createResolutions();
    apiMock.previewSkillResolution.mockResolvedValueOnce({
      items: [{ action: 'view_diff', provider: 'claude', diff: '--- old\n+++ new' }],
    });

    await vm.onApplyResolution({
      conflict_id: 'c-view',
      name: 'DocsSkill',
      scope: 'project',
      provider_entries: [{ provider: 'claude', source_path_id: 'provider:claude' }],
    }, 'view_diff', { provider: 'claude', source_path_id: 'provider:claude' });

    expect(apiMock.previewSkillResolution).toHaveBeenCalledWith(expect.objectContaining({
      conflictId: 'c-view',
      action: 'view_diff',
      sourceProvider: 'claude',
    }));
    expect(apiMock.applySkillResolution).not.toHaveBeenCalled();
    expect(vm.resolutionPreview.value?.items?.[0]).toEqual(expect.objectContaining({
      action: 'view_diff',
      diff: '--- old\n+++ new',
    }));
    expect(emit).not.toHaveBeenCalledWith('refresh-skills');
    expect(notice.level).toBe('info');
    expect(notice.message).toContain('已生成处理预览');
  });

  it('keeps each provider entry as a separate action target', () => {
    const { vm } = createResolutions();
    const conflict = {
      conflict_id: 'c-provider',
      name: 'DocsSkill',
      provider_entries: [
        { provider: 'claude', source_path_id: 'provider:claude' },
        { provider: 'codex', source_path_id: 'provider:codex' },
      ],
    };

    expect(vm.resolutionProviderEntries(conflict).map((entry) => entry.provider)).toEqual(['claude', 'codex']);
    expect(vm.resolutionApplyKey(conflict, 'sync_back_to_canonical', conflict.provider_entries[0])).not.toBe(
      vm.resolutionApplyKey(conflict, 'sync_back_to_canonical', conflict.provider_entries[1]),
    );
  });

  it('prompts for save-as-new names before preview and apply', async () => {
    const { vm } = createResolutions();
    globalThis.window.prompt = vi.fn(() => 'DocsCopy');
    apiMock.previewSkillResolution.mockResolvedValueOnce({ items: [{ preview_id: 'p2', preview_hash: 'h2' }] });

    await vm.onApplyResolution({
      conflict_id: 'c2',
      name: 'DocsSkill',
      scope: 'project',
      provider_entries: [{ provider: 'codex' }],
    }, 'save_as_new_skill', { provider: 'codex' });

    expect(apiMock.previewSkillResolution).toHaveBeenCalledWith(expect.objectContaining({
      action: 'save_as_new_skill',
      newName: 'DocsCopy',
    }));
    expect(apiMock.applySkillResolution).not.toHaveBeenCalled();
    await vm.confirmResolutionPreview();
    expect(apiMock.applySkillResolution).toHaveBeenCalledWith(expect.objectContaining({
      action: 'save_as_new_skill',
      newName: 'DocsCopy',
      previewId: 'p2',
      previewHash: 'h2',
    }));
  });
});
