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
  const activeCwdSource = ref('/repo');
  const vm = useSkillResolutions({
    activeCwdSource,
    emit,
    setNotice: (level, message) => {
      notice.level = level;
      notice.message = message;
    },
  });
  return { activeCwdSource, emit, notice, vm };
}

beforeEach(() => {
  apiMock.listSkillResolutions.mockReset().mockResolvedValue([]);
  apiMock.previewSkillResolution.mockReset().mockResolvedValue({});
  apiMock.applySkillResolution.mockReset().mockResolvedValue({});
  globalThis.window = { prompt: vi.fn(() => 'copy') };
});

describe('useSkillResolutions', () => {
  it('does not call backend while active cwd is missing', async () => {
    const { activeCwdSource, notice, vm } = createResolutions();
    activeCwdSource.value = '';
    vm.resolutionConflicts.value = [{ conflict_id: 'stale-conflict', name: 'DocsSkill' }];
    vm.resolutionPreview.value = { conflict_id: 'stale-conflict' };
    vm.resolutionNamePrompt.value = { conflict: { conflict_id: 'stale-conflict' } };
    vm.resolutionNameInput.value = 'stale-name';

    const result = await vm.refreshSkillResolutions({ notify: false, notifyOnError: true });

    expect(result).toEqual([]);
    expect(apiMock.listSkillResolutions).not.toHaveBeenCalled();
    expect(vm.resolutionConflicts.value).toEqual([]);
    expect(vm.resolutionPreview.value).toBe(null);
    expect(vm.resolutionNamePrompt.value).toBe(null);
    expect(vm.resolutionNameInput.value).toBe('');
    expect(vm.showResolutionCheckButton.value).toBe(false);
    expect(notice.message).toBe('');
  });

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

  it('keeps destructive same-name actions and filters legacy policy-only action', async () => {
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

    expect(vm.resolutionConflicts.value[0].available_actions).toEqual(['view_diff', 'rename_personal', 'keep_selected']);
    expect(vm.resolutionActionUnsupported('disable_personal_for_project')).toBe(true);
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
    expect(vm.resolutionActionLabel('rename_personal')).toBe('改名保存');
    expect(vm.resolutionActionLabel('keep_selected')).toBe('用选中的版本，删除其他版本');
    expect(vm.resolutionActionLabel('replace_provider_root_symlink')).toBe('接管外部技能目录');
    expect(vm.resolutionActionHelp('sync_back_to_canonical')).toBe('保留 Claude/Codex 里的修改，写回本项目管理的技能。');
    expect(vm.resolutionActionHelp('canonical_overwrite_mirror')).toBe('丢弃 Claude/Codex 里的修改，用本项目当前技能重新同步。');
    expect(vm.resolutionActionHelp('rename_personal')).toBe('把选中的版本改名保存，两个版本都会保留。');
    expect(vm.resolutionActionHelp('keep_selected')).toBe('保留这个版本，删除其他同名版本。');
    expect(vm.resolutionActionHelp('replace_provider_root_symlink')).toBe('移除旧连接，创建由本项目管理的技能目录，并重新同步技能。');
    expect(vm.resolutionActionSectionTitle({ kind: 'same_name' })).toBe('选择使用哪个版本');
    expect(vm.resolutionActionSectionTitle({ kind: 'mirror_drift' })).toBe('处理方式');
    expect(vm.resolutionActionFootnote({ kind: 'same_name' })).toBe('处理后同名冲突会立即消失。');
    expect(vm.resolutionActionFootnote({ kind: 'mirror_drift' })).toBe('');
    expect(vm.resolutionConflictGuide({ kind: 'same_name' })).toBe('发现多个同名技能。请选择保留哪一版，其他同名版本会被删除；也可以改名保存。');
    expect(vm.resolutionConflictGuide({
      kind: 'same_name',
      sources: [
        { scope: 'personal', personal_type: 'hub' },
        { scope: 'personal', personal_type: 'user' },
      ],
    })).toBe('发现多个同名的私人技能。请选择保留哪一版，其他同名版本会被删除；也可以改名保存。');
    expect(vm.resolutionConflictGuide({ kind: 'mirror_drift' })).toBe('外部应用里的技能和本项目管理的技能不一致。请选择下面一种处理方式。');
    expect(vm.resolutionConflictGuide({ kind: 'mirror_root_symlink' })).toBe('外部应用的技能目录还是旧连接。接管后会改成由本项目管理的技能目录，并重新同步技能。');
    expect(vm.resolutionManualSteps({
      kind: 'same_name',
      available_actions: ['view_diff', 'rename_personal', 'keep_selected'],
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

  it('supports provider root symlink takeover as a normal mutating resolution', async () => {
    const { vm } = createResolutions();
    const conflict = {
      conflict_id: 'root-1',
      name: 'Claude 项目技能目录',
      scope: 'project',
      kind: 'mirror_root_symlink',
      available_actions: ['view_unmanaged', 'replace_provider_root_symlink'],
      provider_entries: [{ provider: 'claude', source_path_id: 'provider:claude', source_path: '/repo/.claude/skills', target_path: '/repo/.claude/skills' }],
    };
    apiMock.previewSkillResolution.mockResolvedValueOnce({
      items: [{
        action: 'replace_provider_root_symlink',
        provider: 'claude',
        source_provider: 'claude',
        source_path_id: 'provider:claude',
        source_path: '/repo/.claude/skills',
        target_path: '/repo/.claude/skills',
        preview_id: 'root-preview',
        preview_hash: 'root-hash',
      }],
    });

    expect(vm.resolutionActionEntries(conflict).map((entry) => entry.action)).toEqual(['view_unmanaged', 'replace_provider_root_symlink']);
    await vm.onApplyResolution(conflict, 'replace_provider_root_symlink', conflict.provider_entries[0]);

    expect(apiMock.previewSkillResolution).toHaveBeenCalledWith(expect.objectContaining({
      conflictId: 'root-1',
      name: 'Claude 项目技能目录',
      provider: 'claude',
      sourceProvider: 'claude',
      sourcePathId: 'provider:claude',
      action: 'replace_provider_root_symlink',
    }));
    expect(vm.resolutionPreview.value?.requiresApply).toBe(true);
    expect(apiMock.applySkillResolution).not.toHaveBeenCalled();
    expect(vm.resolutionPreviewItemSummary(vm.resolutionPreview.value.items[0], 'replace_provider_root_symlink')).toBe('将接管 Claude 的技能目录，并重新同步本项目管理的技能。');
  });

  it('creates same-name destructive action entries with source-bound payload fields', () => {
    const { vm } = createResolutions();
    const conflict = {
      kind: 'same_name',
      available_actions: ['view_diff', 'rename_personal', 'keep_selected'],
      sources: [
        { scope: 'project', canonical_id: 'project/DocsSkill' },
        { scope: 'personal', personal_type: 'user', canonical_id: 'personal/user/DocsSkill' },
      ],
    };

    const entries = vm.resolutionActionEntries(conflict);
    expect(entries.map((entry) => vm.resolutionActionEntryLabel(entry))).toEqual([
      '用项目共享版本，删除其他版本',
      '用自己创建的私人版本，删除其他版本',
      '改名保存项目共享版本',
      '改名保存自己创建的私人版本',
    ]);
    expect(vm.resolutionApplyKey({ conflict_id: 'same-1' }, 'keep_selected', entries[1])).toContain('personal/user/DocsSkill');
    expect(vm.resolutionApplyKey({ conflict_id: 'same-1' }, 'rename_personal', entries[3])).toContain('personal/user/DocsSkill');
  });

  it('shows one-click choices for project-only same-name conflicts', () => {
    const { vm } = createResolutions();
    const conflict = {
      kind: 'same_name',
      available_actions: ['view_diff', 'rename_personal', 'keep_selected'],
      sources: [
        { scope: 'project', canonical_id: 'project/security-engineer', path: '/repo/.agent/skills/security-engineer' },
        { scope: 'project', canonical_id: 'project/security-standards', path: '/repo/.agent/skills/security-standards' },
      ],
    };

    const entries = vm.resolutionActionEntries(conflict);

    expect(entries.map((entry) => vm.resolutionActionEntryLabel(entry))).toEqual([
      '用项目共享版本：security-engineer，删除其他版本',
      '用项目共享版本：security-standards，删除其他版本',
      '改名保存项目共享版本：security-engineer',
      '改名保存项目共享版本：security-standards',
    ]);
    expect(vm.resolutionManualSteps(conflict)).toEqual([]);
    expect(vm.resolutionApplyKey({ conflict_id: 'same-project' }, 'keep_selected', entries[1])).toContain('project/security-standards');
  });

  it('labels multiple personal same-name choices by the version users recognize', () => {
    const { vm } = createResolutions();
    const conflict = {
      kind: 'same_name',
      available_actions: ['view_diff', 'rename_personal', 'keep_selected'],
      sources: [
        { scope: 'personal', personal_type: 'hub', canonical_id: 'personal/hub/DocsSkill' },
        { scope: 'personal', personal_type: 'user', canonical_id: 'personal/user/DocsSkill' },
      ],
    };

    const entries = vm.resolutionActionEntries(conflict);

    expect(entries.map((entry) => vm.resolutionActionEntryLabel(entry))).toEqual([
      '用市场下载的版本，删除其他版本',
      '用自己创建的版本，删除其他版本',
      '改名保存市场下载的私人版本',
      '改名保存自己创建的私人版本',
    ]);
    expect(entries.map((entry) => vm.resolutionActionEntryHelp(entry))).toEqual([
      '保留这个市场下载的版本，删除其他同名版本。',
      '保留这个自己创建的版本，删除其他同名版本。',
      '把这个版本改成新名称，原来的同名冲突会保留为不同技能。',
      '把这个版本改成新名称，原来的同名冲突会保留为不同技能。',
    ]);
    expect(new Set(entries.map((entry) => vm.resolutionActionEntryLabel(entry))).size).toBe(entries.length);
  });

  it('summarizes previews in user-facing language instead of raw hashes', () => {
    const { vm } = createResolutions();
    const viewItem = {
      provider: 'codex',
      source_path: '/repo/.agents/skills/deploy/SKILL.md',
      target_path: '/repo/.agent/skills/deploy/SKILL.md',
      source_hash: '8b022cc49401abd24425d711fe24aed33d49ddb7dff41bbd2a6bc69e4909af22c',
      target_hash: '854b60866d3b76b7c95ccbc4ec856459624dc622d34971865412b47b56fa840d',
      diff: 'source 8b022... /repo/.agents/skills/deploy\n target 854b... /repo/.agent/skills/deploy',
    };
    const overwriteItem = {
      provider: 'codex',
      source_path: '/repo/.agent/skills/deploy/SKILL.md',
      target_path: '/repo/.agents/skills/deploy/SKILL.md',
      source_hash: '854b60866d3b76b7c95ccbc4ec856459624dc622d34971865412b47b56fa840d',
      target_hash: '8b022cc49401abd24425d711fe24aed33d49ddb7dff41bbd2a6bc69e4909af22c',
    };

    expect(vm.resolutionPreviewIntro({ action: 'view_diff' })).toBe('下面只说明两个版本分别在哪里，不会修改文件。');
    expect(vm.resolutionPreviewItemSummary(viewItem, 'view_diff')).toBe('Codex 里的版本和本项目管理的版本不一致。');
    expect(vm.resolutionPreviewItemPaths(viewItem, 'view_diff')).toEqual([
      { label: '外部版本', value: '/repo/.agents/skills/deploy/SKILL.md' },
      { label: '本项目版本', value: '/repo/.agent/skills/deploy/SKILL.md' },
    ]);
    expect(vm.resolutionPreviewItemPaths(overwriteItem, 'canonical_overwrite_mirror')).toEqual([
      { label: '本项目版本', value: '/repo/.agent/skills/deploy/SKILL.md' },
      { label: '外部版本', value: '/repo/.agents/skills/deploy/SKILL.md' },
    ]);
    expect(vm.resolutionShortHash(viewItem.source_hash)).toBe('8b022cc4');
  });

  it('prompts for a new name before saving a same-name version under a new name', async () => {
    const { vm } = createResolutions();
    apiMock.previewSkillResolution.mockResolvedValueOnce({ items: [{ preview_id: 'p-same', preview_hash: 'h-same' }] });
    const conflict = {
      conflict_id: 'same-2',
      name: 'DocsSkill',
      kind: 'same_name',
      sources: [
        { scope: 'project', canonical_id: 'project/DocsSkill' },
        { scope: 'personal', personal_type: 'user', canonical_id: 'personal/user/DocsSkill' },
      ],
    };
    const entry = { action: 'rename_personal', source: conflict.sources[1], sourceID: 'personal/user/DocsSkill' };

    await vm.onApplyResolution(conflict, 'rename_personal', entry);

    expect(apiMock.previewSkillResolution).not.toHaveBeenCalled();
    expect(vm.resolutionNamePrompt.value?.action).toBe('rename_personal');
    vm.resolutionNameInput.value = 'DocsUserCopy';
    await vm.confirmResolutionNewName();

    expect(apiMock.previewSkillResolution).toHaveBeenCalledWith(expect.objectContaining({
      action: 'rename_personal',
      keepSourceID: 'personal/user/DocsSkill',
      newName: 'DocsUserCopy',
    }));
    expect(apiMock.applySkillResolution).toHaveBeenCalledWith(expect.objectContaining({
      action: 'rename_personal',
      keepSourceID: 'personal/user/DocsSkill',
      newName: 'DocsUserCopy',
      previewId: 'p-same',
      previewHash: 'h-same',
    }));
    expect(vm.resolutionNamePrompt.value).toBe(null);
    expect(vm.resolutionPreview.value).toBe(null);
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

  it('refreshes stale conflict cards when the backend no longer finds the conflict', async () => {
    const { notice, vm } = createResolutions();
    const conflict = {
      conflict_id: 'old-conflict',
      name: 'vue3',
      scope: 'project',
      kind: 'canonical_deleted_with_drift',
      provider_entries: [{ provider: 'codex', source_path_id: 'provider:codex' }],
    };
    vm.resolutionConflicts.value = [conflict];
    apiMock.previewSkillResolution.mockRejectedValueOnce(new Error('resolution conflict not found: old-conflict'));
    apiMock.listSkillResolutions.mockResolvedValueOnce([]);

    await vm.onApplyResolution(conflict, 'confirm_delete_drifted_mirror', conflict.provider_entries[0]);

    expect(apiMock.listSkillResolutions).toHaveBeenCalledWith('/repo');
    expect(vm.resolutionConflicts.value).toEqual([]);
    expect(vm.resolutionPreview.value).toBe(null);
    expect(apiMock.applySkillResolution).not.toHaveBeenCalled();
    expect(notice.level).toBe('info');
    expect(notice.message).toContain('已经处理或不存在');
  });

  it('applies same-name keep action after generating preview proof', async () => {
    const { emit, vm } = createResolutions();
    const conflict = {
      conflict_id: 'same-1',
      name: 'DocsSkill',
      kind: 'same_name',
      available_actions: ['keep_selected'],
      sources: [
        { scope: 'project', canonical_id: 'project/DocsSkill' },
        { scope: 'personal', personal_type: 'user', canonical_id: 'personal/user/DocsSkill' },
      ],
    };
    const actionEntry = vm.resolutionActionEntries(conflict).find((entry) => entry.sourceID === 'project/DocsSkill');
    apiMock.previewSkillResolution.mockResolvedValueOnce({
      items: [{ preview_id: 'same-preview', preview_hash: 'same-hash' }],
    });
    apiMock.listSkillResolutions.mockResolvedValueOnce([]);

    await vm.onApplyResolution(conflict, actionEntry.action, actionEntry);

    expect(apiMock.previewSkillResolution).toHaveBeenCalledWith(expect.objectContaining({
      action: 'keep_selected',
      keepSourceID: 'project/DocsSkill',
    }));
    expect(apiMock.applySkillResolution).toHaveBeenCalledWith(expect.objectContaining({
      action: 'keep_selected',
      previewId: 'same-preview',
      previewHash: 'same-hash',
      keepSourceID: 'project/DocsSkill',
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

  it('groups identical external provider entries into one user action target', async () => {
    const { vm } = createResolutions();
    const conflict = {
      conflict_id: 'c-provider',
      name: 'DocsSkill',
      kind: 'unmanaged_provider_skill',
      available_actions: ['view_unmanaged', 'import_to_personal_imported'],
      provider_entries: [
        { provider: 'claude', source_path_id: 'provider:claude', source_hash: 'same-hash', source_path: '/repo/.claude/skills/DocsSkill' },
        { provider: 'codex', source_path_id: 'provider:codex', source_hash: 'same-hash', source_path: '/repo/.agents/skills/DocsSkill' },
      ],
    };

    const entries = vm.resolutionProviderEntries(conflict);

    expect(entries).toHaveLength(1);
    expect(vm.resolutionProviderEntryLabel(entries[0])).toBe('Claude、Codex');
    expect(vm.resolutionActionEntries(conflict).map((entry) => vm.resolutionActionEntryLabel(entry))).toEqual([
      '查看外部位置',
      '导入到私人使用',
    ]);
    expect(vm.resolutionActionEntryTarget({ action: 'view_unmanaged' }, entries[0])).toEqual(expect.objectContaining({
      provider: '',
      source_path_id: '',
    }));

    apiMock.previewSkillResolution.mockResolvedValueOnce({
      items: [{ preview_id: 'p-import', preview_hash: 'h-import', provider: 'claude', source_provider: 'claude', source_path_id: 'provider:claude' }],
    });
    await vm.onApplyResolution(conflict, 'import_to_personal_imported', entries[0]);

    expect(apiMock.previewSkillResolution).toHaveBeenCalledWith(expect.objectContaining({
      action: 'import_to_personal_imported',
      provider: 'claude',
      sourceProvider: 'claude',
      sourcePathId: 'provider:claude',
    }));
  });

  it('keeps different external provider versions as separate choices', () => {
    const { vm } = createResolutions();
    const conflict = {
      conflict_id: 'c-provider',
      name: 'DocsSkill',
      kind: 'unmanaged_provider_skill',
      available_actions: ['view_unmanaged', 'import_to_personal_imported'],
      provider_entries: [
        { provider: 'claude', source_path_id: 'provider:claude', source_hash: 'claude-hash' },
        { provider: 'codex', source_path_id: 'provider:codex', source_hash: 'codex-hash' },
      ],
    };

    const entries = vm.resolutionProviderEntries(conflict);

    expect(entries.map((entry) => vm.resolutionProviderEntryLabel(entry))).toEqual(['Claude', 'Codex']);
    expect(vm.resolutionApplyKey(conflict, 'import_to_personal_imported', entries[0])).not.toBe(
      vm.resolutionApplyKey(conflict, 'import_to_personal_imported', entries[1]),
    );
  });

  it('treats external same-name provider conflicts as project version conflicts', () => {
    const { vm } = createResolutions();
    const conflict = {
      conflict_id: 'c-same-provider',
      name: 'DocsSkill',
      kind: 'unmanaged_same_name',
      scope: 'project',
      available_actions: ['view_diff', 'sync_back_to_canonical', 'canonical_overwrite_mirror', 'save_as_new_skill'],
      provider_entries: [
        { provider: 'claude', source_path_id: 'provider:claude', source_hash: 'claude-hash' },
      ],
    };

    expect(vm.resolutionConflictGuide(conflict)).toBe('项目里已有同名技能，外部版本和项目共享版本不一致。请选择保留哪一版，或另存为新技能。');
    expect(vm.resolutionActionEntries(conflict).map((entry) => vm.resolutionActionEntryLabel(entry))).toEqual([
      '查看两个版本',
      '用外部修改更新本项目',
      '用本项目内容覆盖外部版本',
      '另存为新技能',
    ]);
    expect(vm.resolutionActionEntries(conflict).map((entry) => entry.action)).not.toContain('import_to_personal_imported');
  });

  it('treats external personal skills with project names as project shared choices', () => {
    const { vm } = createResolutions();
    const conflict = {
      conflict_id: 'c-personal-project',
      name: 'DocsSkill',
      kind: 'external_personal_project_same_name',
      scope: 'personal',
      available_actions: ['view_diff', 'use_project_shared_skill', 'use_external_provider_skill', 'save_as_new_personal_skill', 'import_to_personal_imported'],
      provider_entries: [
        { provider: 'claude', source_path_id: 'provider:claude', source_hash: 'claude-hash' },
      ],
    };

    expect(vm.resolutionConflictGuide(conflict)).toBe('检测到同名技能同时存在于私人使用和项目共享。请选择使用项目共享版本、继续私人使用，或另存为新私人技能。');
    expect(vm.resolutionActionEntries(conflict).map((entry) => vm.resolutionActionEntryLabel(entry))).toEqual([
      '查看两个版本',
      '使用项目共享版本，删除旧私人版本',
      '继续私人使用，替换项目共享版本',
      '另存为新私人技能',
    ]);
    expect(vm.resolutionActionEntries(conflict).map((entry) => entry.action)).not.toContain('import_to_personal_imported');
  });

  it('explains old private provider copies without calling them abnormal external versions', () => {
    const { vm } = createResolutions();
    const conflict = {
      conflict_id: 'c-private-deleted',
      name: '测试驱动开发',
      kind: 'canonical_deleted_with_drift',
      scope: 'personal',
      personal_type: 'imported',
      available_actions: ['view_diff', 'save_as_new_personal_skill', 'sync_back_to_personal', 'confirm_delete_drifted_mirror'],
      provider_entries: [
        {
          provider: 'claude',
          source_path_id: 'provider:claude',
          source_path: '/Users/mac/.claude/skills/测试驱动开发',
          target_path: '/Users/mac/.super-dolphin/skills/personal/imported/测试驱动开发',
        },
      ],
    };
    const previewItem = {
      provider: 'claude',
      source_path: '/Users/mac/.claude/skills/测试驱动开发',
      target_path: '/Users/mac/.super-dolphin/skills/personal/imported/测试驱动开发',
    };

    expect(vm.resolutionTitle(conflict)).toBe('测试驱动开发 · 旧私人版本需要处理');
    expect(vm.resolutionConflictGuide(conflict)).toBe('私人使用里的同名技能已经删除或改成项目共享，但 Claude/Codex 里还保留旧私人版本。请选择继续私人使用、另存为新私人技能，或删除旧私人版本。');
    expect(vm.resolutionActionEntries(conflict).map((entry) => vm.resolutionActionEntryLabel(entry))).toEqual([
      '查看两个版本',
      '另存为新私人技能',
      '继续私人使用',
      '使用项目共享版本，删除旧私人版本',
    ]);
    expect(vm.resolutionPreviewItemSummary(previewItem, 'view_diff')).toBe('Claude 里还保留旧私人版本，需要选择继续私人使用、另存或删除。');
    expect(vm.resolutionPreviewItemPaths(previewItem, 'view_diff')).toEqual([
      { label: 'Claude 里的旧私人版本', value: '/Users/mac/.claude/skills/测试驱动开发' },
      { label: '私人使用版本', value: '/Users/mac/.super-dolphin/skills/personal/imported/测试驱动开发' },
    ]);
  });

  it('asks for save-as-new names inline before preview and apply', async () => {
    const { notice, vm } = createResolutions();
    apiMock.previewSkillResolution.mockResolvedValueOnce({ items: [{ preview_id: 'p2', preview_hash: 'h2' }] });

    await vm.onApplyResolution({
      conflict_id: 'c2',
      name: 'DocsSkill',
      scope: 'project',
      provider_entries: [{ provider: 'codex' }],
    }, 'save_as_new_skill', { provider: 'codex' });

    expect(apiMock.previewSkillResolution).not.toHaveBeenCalled();
    expect(vm.resolutionNamePrompt.value?.action).toBe('save_as_new_skill');
    expect(vm.resolutionNameInput.value).toBe('DocsSkill-copy');
    expect(notice.message).toContain('输入新技能名称');

    vm.resolutionNameInput.value = 'DocsCopy';
    await vm.confirmResolutionNewName();

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

  it('saves external personal project same-name as new personal skill immediately after naming', async () => {
    const { emit, vm } = createResolutions();
    globalThis.window = {};
    apiMock.previewSkillResolution.mockResolvedValueOnce({ items: [{ preview_id: 'p3', preview_hash: 'h3', provider: 'claude' }] });
    apiMock.listSkillResolutions.mockResolvedValueOnce([]);

    await vm.onApplyResolution({
      conflict_id: 'c3',
      name: 'DocsSkill',
      scope: 'personal',
      kind: 'external_personal_project_same_name',
      available_actions: ['save_as_new_personal_skill'],
      provider_entries: [{ provider: 'claude' }],
    }, 'save_as_new_personal_skill', { provider: 'claude' });

    expect(apiMock.previewSkillResolution).not.toHaveBeenCalled();
    expect(vm.resolutionNamePrompt.value?.action).toBe('save_as_new_personal_skill');
    expect(vm.resolutionNamePrompt.value?.autoApply).toBe(true);
    expect(vm.resolutionNameInput.value).toBe('DocsSkill-private');

    vm.resolutionNameInput.value = 'DocsPrivate';
    await vm.confirmResolutionNewName();

    expect(apiMock.previewSkillResolution).toHaveBeenCalledWith(expect.objectContaining({
      action: 'save_as_new_personal_skill',
      newName: 'DocsPrivate',
    }));
    expect(apiMock.applySkillResolution).toHaveBeenCalledWith(expect.objectContaining({
      action: 'save_as_new_personal_skill',
      newName: 'DocsPrivate',
      previewId: 'p3',
      previewHash: 'h3',
    }));
    expect(emit).toHaveBeenCalledWith('refresh-skills');
    expect(vm.resolutionNamePrompt.value).toBe(null);
  });
});
