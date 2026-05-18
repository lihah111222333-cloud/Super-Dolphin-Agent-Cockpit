import { computed, ref } from '../../lib/vue.esm-browser.prod.js';
import { applySkillResolution, listSkillResolutions, previewSkillResolution } from '../services/skills-api.js';

function requiresResolutionNewName(action) {
  return action === 'save_as_new_skill'
    || action === 'save_as_new_personal_skill';
}

function isResolutionViewAction(action) {
  return action === 'view_diff' || action === 'view_unmanaged';
}

function isResolutionPreviewOnlyAction(action) {
  return false;
}

function resolutionRequiresApply(action) {
  return !isResolutionViewAction(action) && !isResolutionPreviewOnlyAction(action);
}

const actionableResolutionActions = new Set([
  'view_diff',
  'view_unmanaged',
  'sync_back_to_canonical',
  'canonical_overwrite_mirror',
  'save_as_new_skill',
  'confirm_delete_drifted_mirror',
  'sync_back_to_personal',
  'personal_overwrite_mirror',
  'save_as_new_personal_skill',
  'import_to_personal_imported',
  'import_to_project',
  'takeover_provider_skill',
  'disable_personal_for_project',
  'keep_selected',
]);

function resolutionActionUnsupported(action) {
  return !actionableResolutionActions.has((action || '').toString().trim());
}

function normalizeResolutionConflict(conflict) {
  const actions = Array.isArray(conflict?.available_actions) ? conflict.available_actions : [];
  return {
    ...conflict,
    available_actions: actions.filter((action) => !resolutionActionUnsupported(action)),
  };
}

function resolutionSourceID(source) {
  return (source?.canonical_id || source?.canonicalID || '').toString().trim();
}

function resolutionSourceScope(source) {
  return (source?.scope || '').toString().trim().toLowerCase();
}

function resolutionSourcePersonalType(source) {
  return (source?.personal_type || source?.personalType || '').toString().trim().toLowerCase();
}

function sameNamePersonalSource(conflict) {
  const sources = Array.isArray(conflict?.sources) ? conflict.sources : [];
  return sources.find((source) => resolutionSourceScope(source) === 'personal') || null;
}

function sameNameProjectSource(conflict) {
  const sources = Array.isArray(conflict?.sources) ? conflict.sources : [];
  return sources.find((source) => resolutionSourceScope(source) === 'project') || null;
}

function firstResolutionSourceID(conflict) {
  const sources = Array.isArray(conflict?.sources) ? conflict.sources : [];
  return resolutionSourceID(sources[0]);
}

function resolutionSameNamePayloadFields(conflict, action, entry = null) {
  switch (action) {
    case 'disable_personal_for_project': {
      const source = entry?.source || sameNamePersonalSource(conflict);
      return {
        disablePolicyTarget: resolutionSourceID(source),
        personalType: resolutionSourcePersonalType(source) || conflict?.personal_type || '',
      };
    }
    case 'keep_selected': {
      const selected = entry?.source || sameNamePersonalSource(conflict) || sameNameProjectSource(conflict);
      return { keepSourceID: resolutionSourceID(selected) || firstResolutionSourceID(conflict) };
    }
    case 'merge_manually':
      return {
        keepSourceID: firstResolutionSourceID(conflict),
        mergeContentHash: (conflict?.merge_content_hash || conflict?.mergeContentHash || '').toString().trim(),
      };
    default:
      return {};
  }
}

function resolutionKindLabel(kind) {
  const value = (kind || '').toString().trim().toLowerCase();
  return ({
    mirror_drift: '外部版本有改动',
    unmanaged_provider_skill: '发现外部技能',
    unmanaged: '发现外部技能',
    same_name: '同名技能',
    same_name_scope_conflict: '同名技能',
  }[value] || '需要处理');
}

function resolutionProviderLabel(provider) {
  const value = (provider || '').toString().trim();
  const normalized = value.toLowerCase();
  if (normalized === 'claude') return 'Claude';
  if (normalized === 'codex') return 'Codex';
  return value || '外部应用';
}

function resolutionConflictGuide(conflict) {
  const kind = (conflict?.kind || '').toString().trim().toLowerCase();
  if (kind === 'same_name' || kind === 'same_name_scope_conflict') {
    const personalCount = sameNamePersonalSources(conflict).length;
    if (!sameNameHasProjectSource(conflict) && personalCount > 1) {
      return '发现多个同名的私人技能。请选择要优先使用的版本，其他版本不会被删除。';
    }
    return '发现多个同名技能。请选择要优先使用的版本，其他版本不会被删除。';
  }
  if (kind === 'unmanaged_provider_skill' || kind === 'unmanaged_same_name' || kind === 'unmanaged') {
    return '外部应用里有一个还没纳入管理的技能。可以导入后统一管理，或只保留在外部应用里。';
  }
  if (kind === 'canonical_deleted_with_drift') {
    return '本项目里的技能已不存在，但外部应用里还有改过的版本。请选择恢复、另存或删除外部版本。';
  }
  return '外部应用里的技能和本项目管理的技能不一致。请选择下面一种处理方式。';
}

function resolutionActionHelp(action) {
  return ({
    view_diff: '只查看两个版本分别在哪里，不会修改文件。',
    view_unmanaged: '只查看外部技能位置，不会修改文件。',
    sync_back_to_canonical: '保留 Claude/Codex 里的修改，写回本项目管理的技能。',
    canonical_overwrite_mirror: '丢弃 Claude/Codex 里的修改，用本项目当前技能重新同步。',
    save_as_new_skill: '保留两边内容，把外部版本存成一个新的项目共享技能。',
    confirm_delete_drifted_mirror: '删除外部异常版本，下次会按本项目管理的技能重新生成。',
    sync_back_to_personal: '保留 Claude/Codex 里的修改，写回私人技能。',
    personal_overwrite_mirror: '丢弃 Claude/Codex 里的修改，用私人技能重新同步。',
    save_as_new_personal_skill: '保留两边内容，把外部版本存成一个新的私人技能。',
    import_to_personal_imported: '把外部技能导入为私人使用，之后由本项目管理。',
    import_to_project: '把外部技能导入为项目共享，项目成员都能使用。',
    takeover_provider_skill: '把外部技能纳入当前作用域管理，后续统一同步。',
    disable_personal_for_project: '当前项目使用项目共享版本，不删除私人技能。',
    keep_selected: '选择后优先使用这个版本，不删除其他同名技能。',
  }[action] || '执行前会先预览，不会直接写入。');
}

function resolutionManualSteps(conflict) {
  const kind = (conflict?.kind || '').toString().trim().toLowerCase();
  const actions = Array.isArray(conflict?.available_actions) ? conflict.available_actions : [];
  if (kind === 'same_name' || kind === 'same_name_scope_conflict') {
    if (actions.includes('disable_personal_for_project') || actions.includes('keep_selected')) {
      return [];
    }
    return [
      '要保留项目共享：编辑或删除同名私人技能。',
      '要保留私人使用：编辑项目共享技能改名，或删除项目共享技能。',
      '两边都要保留：把其中一个改成更明确的名字。',
    ];
  }
  return [];
}

function resolutionPreviewIntro(preview) {
  const action = (preview?.action || '').toString().trim();
  if (action === 'view_diff' || action === 'view_unmanaged') {
    return '下面只说明两个版本分别在哪里，不会修改文件。';
  }
  return '请先确认将要写入的位置，确认应用后才会修改文件。';
}

function resolutionShortHash(hash) {
  return (hash || '').toString().trim().slice(0, 8);
}

function resolutionPreviewItemPaths(item, action = '') {
  const paths = [];
  const sourcePath = (item?.source_path || item?.sourcePath || '').toString().trim();
  const targetPath = (item?.target_path || item?.targetPath || '').toString().trim();
  const normalizedAction = (action || item?.action || '').toString().trim();
  const overwrite = normalizedAction === 'canonical_overwrite_mirror' || normalizedAction === 'personal_overwrite_mirror';
  const importAction = normalizedAction === 'import_to_personal_imported' || normalizedAction === 'import_to_project' || normalizedAction === 'takeover_provider_skill';
  const sourceLabel = overwrite ? '本项目版本' : '外部版本';
  let targetLabel = overwrite ? '外部版本' : '本项目版本';
  if (importAction) targetLabel = '保存位置';
  if (sourcePath) paths.push({ label: sourceLabel, value: sourcePath });
  if (targetPath && targetPath !== sourcePath) paths.push({ label: targetLabel, value: targetPath });
  return paths;
}

function resolutionPreviewItemSummary(item, action = '') {
  const provider = resolutionProviderLabel(item?.provider || item?.source_provider);
  switch ((action || item?.action || '').toString().trim()) {
    case 'sync_back_to_canonical':
    case 'sync_back_to_personal':
      return `将把 ${provider} 里的版本写回本项目管理的技能。`;
    case 'canonical_overwrite_mirror':
    case 'personal_overwrite_mirror':
      return `将用本项目管理的技能覆盖 ${provider} 里的版本。`;
    case 'save_as_new_skill':
    case 'save_as_new_personal_skill':
      return `将把 ${provider} 里的版本另存为新技能。`;
    case 'confirm_delete_drifted_mirror':
      return `将删除 ${provider} 里的异常版本。`;
    case 'import_to_personal_imported':
      return `将把 ${provider} 里的技能导入为私人使用。`;
    case 'import_to_project':
      return `将把 ${provider} 里的技能导入为项目共享。`;
    case 'takeover_provider_skill':
      return `将把 ${provider} 里的技能纳入本项目管理。`;
    default:
      return `${provider} 里的版本和本项目管理的版本不一致。`;
  }
}

function resolutionActionLabel(action) {
  return ({
    view_diff: '查看两个版本',
    view_unmanaged: '查看外部版本',
    sync_back_to_canonical: '用外部修改更新本项目',
    canonical_overwrite_mirror: '用本项目内容覆盖外部版本',
    save_as_new_skill: '另存为新技能',
    confirm_delete_drifted_mirror: '删除外部异常版本',
    sync_back_to_personal: '用外部修改更新私人技能',
    personal_overwrite_mirror: '用私人技能覆盖外部版本',
    save_as_new_personal_skill: '另存为新私人技能',
    import_to_personal_imported: '导入到私人使用',
    import_to_project: '导入到项目共享',
    takeover_provider_skill: '纳入管理',
    rename_personal: '重命名私人技能',
    disable_personal_for_project: '使用项目共享版本',
    rename_personal_type: '更改私人技能类型',
    merge_manually: '手动合并',
    keep_selected: '使用选中的版本',
  }[action] || '处理');
}

function sameNameResolutionConflict(conflict) {
  const kind = (conflict?.kind || '').toString().trim().toLowerCase();
  return kind === 'same_name' || kind === 'same_name_scope_conflict';
}

function sameNamePersonalSources(conflict) {
  const sources = Array.isArray(conflict?.sources) ? conflict.sources : [];
  return sources.filter((source) => resolutionSourceScope(source) === 'personal');
}

function sameNameHasProjectSource(conflict) {
  const sources = Array.isArray(conflict?.sources) ? conflict.sources : [];
  return sources.some((source) => resolutionSourceScope(source) === 'project');
}

function sameNamePersonalVersionText(source, hasProjectSource = false) {
  const suffix = hasProjectSource ? '私人版本' : '版本';
  const value = resolutionSourcePersonalType(source);
  return ({
    user: `自己创建的${suffix}`,
    agent: `自动生成的${suffix}`,
    imported: `导入的${suffix}`,
    hub: `市场下载的${suffix}`,
  }[value] || `私人${suffix}`);
}

function sameNameProjectVersionEntry(source) {
  return {
    action: 'keep_selected',
    label: '使用项目共享版本',
    help: '之后优先使用项目共享版本，其他同名技能不会被删除。',
    source,
    sourceID: resolutionSourceID(source),
  };
}

function resolutionActionEntryLabel(entry) {
  return entry?.label || resolutionActionLabel(entry?.action || entry);
}

function resolutionActionEntryHelp(entry) {
  return entry?.help || resolutionActionHelp(entry?.action || entry);
}

function resolutionActionSectionTitle(conflict) {
  return sameNameResolutionConflict(conflict) ? '选择使用哪个版本' : '处理方式';
}

function resolutionActionFootnote(conflict) {
  if (!sameNameResolutionConflict(conflict)) return '';
  return '选择后只会设置优先使用的版本，不会删除其他技能。以后也可以通过改名或删除来彻底消除冲突。';
}

function resolutionActionEntryTarget(actionEntry, providerEntry) {
  return actionEntry?.source ? actionEntry : providerEntry;
}

function resolutionActionAutoApplies(action) {
  return action === 'disable_personal_for_project' || action === 'keep_selected';
}

async function applyResolutionPreviewNow(preview, payload) {
  const proof = Array.isArray(preview?.items) ? preview.items[0] : null;
  if (!proof?.preview_id || !proof?.preview_hash) {
    throw new Error('缺少处理预览凭据');
  }
  await applySkillResolution({
    ...payload,
    provider: proof.provider || payload.provider,
    sourceProvider: proof.source_provider || payload.sourceProvider,
    sourcePathId: proof.source_path_id || payload.sourcePathId,
    previewId: proof.preview_id,
    previewHash: proof.preview_hash,
  });
}

export function useSkillResolutions({ activeCwdSource, emit, setNotice }) {
  const resolutionConflicts = ref([]);
  const resolutionLoading = ref(false);
  const resolutionActioning = ref('');
  const resolutionPreview = ref(null);
  const resolutionPanelCollapsed = ref(false);
  const resolutionLoadError = ref('');
  const resolutionConflictAlertText = computed(() => {
    const count = resolutionConflicts.value.length;
    return count > 0 ? `发现 ${count} 个技能冲突，需要处理后再使用，避免 Claude 或 Codex 读到不同版本。` : '';
  });
  const resolutionCheckButtonText = computed(() => {
    if (resolutionLoading.value) return '检查中...';
    const count = resolutionConflicts.value.length;
    return count > 0 ? `发现 ${count} 个冲突` : '检查冲突';
  });
  const showResolutionCheckButton = computed(() => resolutionConflicts.value.length > 0 || Boolean(resolutionLoadError.value));
  const showResolutionPanel = computed(() => resolutionConflicts.value.length > 0 && !resolutionPanelCollapsed.value);
  const resolutionPanelToggleText = computed(() => (resolutionPanelCollapsed.value ? '展开冲突' : '收起冲突'));

  function toggleResolutionPanel() {
    resolutionPanelCollapsed.value = !resolutionPanelCollapsed.value;
  }

  async function refreshSkillResolutions(options = {}) {
    const notify = options?.notify !== false;
    const notifyOnError = notify || options?.notifyOnError === true;
    const collapseOnConflict = options?.collapseOnConflict === true;
    resolutionLoading.value = true;
    try {
      const conflicts = await listSkillResolutions(activeCwdSource.value);
      resolutionConflicts.value = conflicts.map(normalizeResolutionConflict);
      resolutionLoadError.value = '';
      resolutionPanelCollapsed.value = collapseOnConflict && conflicts.length > 0;
      if (notify) {
        setNotice(
          conflicts.length > 0 ? 'info' : 'success',
          conflicts.length > 0 ? `发现 ${conflicts.length} 个技能冲突待处理。` : '暂无技能冲突。',
        );
      }
    } catch (error) {
      resolutionLoadError.value = error?.message || String(error || '');
      if (notifyOnError) setNotice('error', `读取技能冲突失败：${error?.message || error}`);
    } finally {
      resolutionLoading.value = false;
    }
  }

  function resetSkillResolutions() {
    resolutionConflicts.value = [];
    resolutionPreview.value = null;
    resolutionPanelCollapsed.value = false;
    resolutionLoadError.value = '';
  }

  function resolutionTitle(conflict) {
    const name = (conflict?.name || conflict?.skill_name || '').toString().trim() || '(unnamed)';
    return `${name} · ${resolutionKindLabel(conflict?.kind)}`;
  }

  function resolutionProviderEntry(conflict) {
    return resolutionProviderEntries(conflict)[0] || {};
  }

  function resolutionProviderEntries(conflict) {
    const entries = Array.isArray(conflict?.provider_entries) ? conflict.provider_entries : [];
    if (entries.length > 0) return entries;
    const provider = (conflict?.provider || conflict?.source_provider || '').toString().trim();
    if (!provider) return [{}];
    return [{
      provider,
      source_path_id: conflict?.source_path_id || '',
    }];
  }

  function resolutionApplyKey(conflict, action, entry = null) {
    const source = (entry?.source_path_id || entry?.provider || entry?.sourceID || resolutionSourceID(entry?.source) || '').toString().trim();
    return `${conflict?.conflict_id || ''}:${source}:${action || ''}`;
  }

  function resolutionActionEntries(conflict) {
    const actions = (Array.isArray(conflict?.available_actions) ? conflict.available_actions : [])
      .filter((action) => !resolutionActionUnsupported(action));
    if (!sameNameResolutionConflict(conflict)) {
      return actions.map((action) => ({ action }));
    }
    const entries = [];
    const personalSources = sameNamePersonalSources(conflict);
    const hasProjectSource = sameNameHasProjectSource(conflict);
    const projectSource = sameNameProjectSource(conflict);
    if (actions.includes('keep_selected') && projectSource) {
      entries.push(sameNameProjectVersionEntry(projectSource));
    } else if (actions.includes('disable_personal_for_project')) {
      const source = sameNamePersonalSource(conflict);
      if (source) {
        entries.push({
          action: 'disable_personal_for_project',
          label: '使用项目共享版本',
          help: '之后优先使用项目共享版本，私人技能不会被删除。',
          source,
          sourceID: resolutionSourceID(source),
        });
      }
    }
    if (actions.includes('keep_selected')) {
      personalSources.forEach((source) => {
        const versionText = sameNamePersonalVersionText(source, hasProjectSource);
        entries.push({
          action: 'keep_selected',
          label: `使用${versionText}`,
          help: `之后优先使用这个${versionText}，其他同名技能不会被删除。`,
          source,
          sourceID: resolutionSourceID(source),
        });
      });
    }
    return entries;
  }

  async function onApplyResolution(conflict, action, entry = null) {
    const conflictId = (conflict?.conflict_id || '').toString().trim();
    const selectedAction = (action || '').toString().trim();
    if (!conflictId || !selectedAction) return;
    if (resolutionActionUnsupported(selectedAction)) {
      setNotice('info', `暂不支持该技能冲突操作：${resolutionActionLabel(selectedAction)}`);
      return;
    }
    let newName = '';
    if (requiresResolutionNewName(selectedAction)) {
      newName = (globalThis.window?.prompt?.('新技能名称', `${conflict?.name || 'skill'}-copy`) || '').toString().trim();
      if (!newName) {
        setNotice('info', '已取消处理。');
        return;
      }
    }
    const providerEntry = entry || resolutionProviderEntry(conflict);
    const sameNameFields = resolutionSameNamePayloadFields(conflict, selectedAction, entry);
    const payload = {
      cwd: activeCwdSource.value,
      conflictId,
      name: conflict?.name || conflict?.skill_name || '',
      scope: conflict?.scope || '',
      personalType: conflict?.personal_type || '',
      provider: providerEntry?.provider || conflict?.provider || '',
      sourceProvider: providerEntry?.provider || conflict?.source_provider || '',
      sourcePathId: providerEntry?.source_path_id || conflict?.source_path_id || '',
      action: selectedAction,
      newName,
      ...sameNameFields,
    };
    resolutionActioning.value = resolutionApplyKey(conflict, selectedAction, providerEntry);
    try {
      const preview = await previewSkillResolution(payload);
      if (resolutionActionAutoApplies(selectedAction)) {
        await applyResolutionPreviewNow(preview, payload);
        resolutionPreview.value = null;
        setNotice('success', `已处理技能冲突：${conflict?.name || conflictId}`);
        emit('refresh-skills');
        await refreshSkillResolutions();
        return;
      }
      resolutionPreview.value = {
        ...preview,
        action: selectedAction,
        payload,
        requiresApply: resolutionRequiresApply(selectedAction),
      };
      if (isResolutionViewAction(selectedAction)) {
        setNotice('info', `已生成处理预览：${conflict?.name || conflictId}`);
        return;
      }
      if (isResolutionPreviewOnlyAction(selectedAction)) {
        setNotice('info', `已生成处理预览：${conflict?.name || conflictId}。此操作当前仅预览，不会直接写入。`);
        return;
      }
      const proof = Array.isArray(preview?.items) ? preview.items[0] : null;
      if (!proof?.preview_id || !proof?.preview_hash) {
        throw new Error('缺少处理预览凭据');
      }
      setNotice('info', `已生成处理预览：${conflict?.name || conflictId}`);
    } catch (error) {
      setNotice('error', `处理技能冲突失败：${error?.message || error}`);
    } finally {
      resolutionActioning.value = '';
    }
  }

  function clearResolutionPreview() {
    resolutionPreview.value = null;
  }

  async function confirmResolutionPreview() {
    const preview = resolutionPreview.value;
    const proof = Array.isArray(preview?.items) ? preview.items[0] : null;
    if (!preview?.requiresApply || !proof?.preview_id || !proof?.preview_hash) return;
    const payload = preview.payload || {};
    resolutionActioning.value = 'confirm';
    try {
      await applyResolutionPreviewNow(preview, payload);
      setNotice('success', `已处理技能冲突：${payload.name || payload.conflictId || ''}`);
      clearResolutionPreview();
      emit('refresh-skills');
      await refreshSkillResolutions();
    } catch (error) {
      setNotice('error', `应用技能冲突处理失败：${error?.message || error}`);
    } finally {
      resolutionActioning.value = '';
    }
  }

  return {
    resolutionConflicts,
    resolutionLoading,
    resolutionActioning,
    resolutionPreview,
    resolutionPanelCollapsed,
    resolutionCheckButtonText,
    showResolutionCheckButton,
    showResolutionPanel,
    resolutionPanelToggleText,
    toggleResolutionPanel,
    refreshSkillResolutions,
    resetSkillResolutions,
    resolutionTitle,
    resolutionActionLabel,
    resolutionKindLabel,
    resolutionProviderLabel,
    resolutionProviderEntry,
    resolutionProviderEntries,
    resolutionActionEntries,
    resolutionActionEntryLabel,
    resolutionActionEntryHelp,
    resolutionActionSectionTitle,
    resolutionActionFootnote,
    resolutionActionEntryTarget,
    resolutionActionUnsupported,
    resolutionApplyKey,
    resolutionConflictAlertText,
    resolutionConflictGuide,
    resolutionActionHelp,
    resolutionManualSteps,
    resolutionPreviewIntro,
    resolutionPreviewItemSummary,
    resolutionPreviewItemPaths,
    resolutionShortHash,
    onApplyResolution,
    clearResolutionPreview,
    confirmResolutionPreview,
  };
}
