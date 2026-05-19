import { computed, ref } from '../../lib/vue.esm-browser.prod.js';
import { applySkillResolution, listSkillResolutions, previewSkillResolution } from '../services/skills-api.js';
import {
  resolutionActionHelp,
  resolutionActionLabel,
  resolutionConflictGuide,
  resolutionConflictKindLabel,
  resolutionKindLabel,
  resolutionPreviewIntro,
  resolutionPreviewItemPaths,
  resolutionPreviewItemSummary,
  resolutionProviderEntryLabel,
  resolutionProviderLabel,
  resolutionShortHash,
} from './useSkillResolutionCopy.js';

function requiresResolutionNewName(action) {
  return action === 'save_as_new_skill'
    || action === 'save_as_new_personal_skill'
    || action === 'rename_personal';
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

function defaultResolutionNewName(conflict, action) {
  const base = (conflict?.name || conflict?.skill_name || 'skill').toString().trim() || 'skill';
  return `${base}${action === 'save_as_new_personal_skill' ? '-private' : '-copy'}`;
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
  'use_project_shared_skill',
  'use_external_provider_skill',
  'replace_provider_root_symlink',
  'rename_personal',
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

function sameNameProjectSources(conflict) {
  const sources = Array.isArray(conflict?.sources) ? conflict.sources : [];
  return sources.filter((source) => resolutionSourceScope(source) === 'project');
}

function firstResolutionSourceID(conflict) {
  const sources = Array.isArray(conflict?.sources) ? conflict.sources : [];
  return resolutionSourceID(sources[0]);
}

function resolutionSameNamePayloadFields(conflict, action, entry = null) {
  switch (action) {
    case 'rename_personal':
    case 'keep_selected': {
      const sources = Array.isArray(conflict?.sources) ? conflict.sources : [];
      const selected = entry?.source || sources.find((source) => resolutionSourceScope(source) === 'personal') || sources.find((source) => resolutionSourceScope(source) === 'project');
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

function resolutionManualSteps(conflict) {
  const kind = (conflict?.kind || '').toString().trim().toLowerCase();
  const actions = Array.isArray(conflict?.available_actions) ? conflict.available_actions : [];
  if (kind === 'same_name' || kind === 'same_name_scope_conflict') {
    if (actions.includes('keep_selected') || actions.includes('rename_personal')) {
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
    label: '用项目共享版本，删除其他版本',
    help: '保留项目共享版本，删除其他同名版本。',
    source,
    sourceID: resolutionSourceID(source),
  };
}

function sameNameProjectVersionEntryForSource(source, multipleProjectSources = false) {
  if (!multipleProjectSources) return sameNameProjectVersionEntry(source);
  const leaf = resolutionSourcePathLeaf(source) || resolutionSourceID(source).replace(/^project\//, '') || '项目共享版本';
  return {
    action: 'keep_selected',
    label: `用项目共享版本：${leaf}，删除其他版本`,
    help: '保留这个项目共享版本，删除其他同名版本。',
    source,
    sourceID: resolutionSourceID(source),
  };
}

function sameNameRenameEntry(source, includeSourceLeaf = false) {
  return {
    action: 'rename_personal',
    label: `改名保存${sameNameSourceShortText(source, includeSourceLeaf)}`,
    help: '把这个版本改成新名称，原来的同名冲突会保留为不同技能。',
    source,
    sourceID: resolutionSourceID(source),
  };
}

function sameNameSourceShortText(source, includeSourceLeaf = false) {
  if (resolutionSourceScope(source) === 'project') {
    const leaf = includeSourceLeaf ? resolutionSourcePathLeaf(source) || resolutionSourceID(source).replace(/^project\//, '') : '';
    return leaf ? `项目共享版本：${leaf}` : '项目共享版本';
  }
  return sameNamePersonalVersionText(source, true);
}

function resolutionSourcePathLeaf(source) {
  const path = (source?.path || source?.skill_file || source?.skillFile || '').toString().trim().replace(/\\/g, '/');
  if (!path) return '';
  const parts = path.split('/').filter(Boolean);
  const leaf = parts[parts.length - 1] || '';
  if (leaf === 'SKILL.md' && parts.length > 1) return parts[parts.length - 2] || '';
  return leaf;
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
  return '处理后同名冲突会立即消失。';
}

function resolutionActionEntryTarget(actionEntry, providerEntry) {
  if (providerEntry?.merged_provider_entry && actionEntry?.action === 'view_unmanaged') {
    return {
      ...providerEntry,
      provider: '',
      source_path_id: '',
      sourcePathId: '',
    };
  }
  return actionEntry?.source ? actionEntry : providerEntry;
}

function resolutionActionAutoApplies(action) {
  return action === 'keep_selected';
}

function resolutionActionAutoAppliesForConflict(action, conflict) {
  if (resolutionActionAutoApplies(action)) return true;
  if (action === 'rename_personal') return true;
  if (externalPersonalProjectResolutionConflict(conflict)) {
    return action === 'use_project_shared_skill'
      || action === 'use_external_provider_skill'
      || action === 'save_as_new_personal_skill';
  }
  return false;
}

function personalDeletedDriftResolutionConflict(conflict) {
  return (conflict?.kind || '').toString().trim().toLowerCase() === 'canonical_deleted_with_drift'
    && (conflict?.scope || '').toString().trim().toLowerCase() === 'personal';
}

function personalDeletedDriftActionEntry(action) {
  const labels = {
    sync_back_to_personal: '继续私人使用',
    confirm_delete_drifted_mirror: '使用项目共享版本，删除旧私人版本',
  };
  const helps = {
    sync_back_to_personal: '恢复为私人使用，Claude/Codex 会继续读取这个私人版本。',
    confirm_delete_drifted_mirror: '删除 Claude/Codex 里的旧私人版本；如果项目共享里已有同名技能，会继续使用项目共享版本。',
  };
  return { action, label: labels[action] || resolutionActionLabel(action), help: helps[action] || resolutionActionHelp(action) };
}

function externalPersonalProjectActionEntry(action) {
  const labels = {
    use_project_shared_skill: '使用项目共享版本，删除旧私人版本',
    use_external_provider_skill: '继续私人使用，替换项目共享版本',
  };
  return { action, label: labels[action] || resolutionActionLabel(action), help: resolutionActionHelp(action) };
}

function resolutionNamePromptHelpText(prompt) {
  if (!prompt) return '';
  return prompt.autoApply
    ? `${prompt.label}，会立刻处理这个冲突。`
    : `${prompt.label}，生成预览后再确认应用。`;
}

function resolutionNamePromptButtonText(prompt, actioning) {
  if (!prompt) return '生成预览';
  if (actioning === prompt.applyKey) {
    return prompt.autoApply ? '处理中...' : '生成中...';
  }
  return prompt.autoApply ? prompt.label : '生成预览';
}

function resolutionConflictNotFound(error) {
  return (error?.message || String(error || '')).includes('resolution conflict not found');
}

function resolutionConflictShapePart(value) {
  return (value || '').toString().trim().toLowerCase();
}

function sameResolutionConflictShape(left, right) {
  return resolutionConflictShapePart(left?.name || left?.skill_name) === resolutionConflictShapePart(right?.name || right?.skill_name)
    && resolutionConflictShapePart(left?.kind) === resolutionConflictShapePart(right?.kind)
    && resolutionConflictShapePart(left?.scope) === resolutionConflictShapePart(right?.scope)
    && resolutionConflictShapePart(left?.personal_type || left?.personalType) === resolutionConflictShapePart(right?.personal_type || right?.personalType);
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

function groupedResolutionProviderEntries(conflict, entries) {
  if (!externalProviderResolutionConflict(conflict) || entries.length < 2) {
    return entries;
  }
  const groups = [];
  const byHash = new Map();
  for (const entry of entries) {
    const hash = (entry?.source_hash || entry?.sourceHash || '').toString().trim();
    if (!hash) return entries;
    if (!byHash.has(hash)) {
      const group = [];
      byHash.set(hash, group);
      groups.push(group);
    }
    byHash.get(hash).push(entry);
  }
  return groups.flatMap((group) => {
    if (group.length === 1) return group;
    const providers = group.map((entry) => (entry?.provider || '').toString().trim()).filter(Boolean);
    return [{
      ...group[0],
      provider_entries: group,
      provider_group: providers,
      display_label: providers.map(resolutionProviderLabel).join('、'),
      merged_provider_entry: true,
    }];
  });
}

function externalProviderResolutionConflict(conflict) {
  const kind = (conflict?.kind || '').toString().trim().toLowerCase();
  return kind === 'unmanaged_provider_skill' || kind === 'unmanaged_same_name' || kind === 'unmanaged' || kind === 'external_personal_project_same_name';
}

function externalPersonalProjectResolutionConflict(conflict) {
  return (conflict?.kind || '').toString().trim().toLowerCase() === 'external_personal_project_same_name';
}

function createResolutionNamePromptHandlers({
  resolutionPreview,
  resolutionNamePrompt,
  resolutionNameInput,
  resolutionProviderEntry,
  resolutionApplyKey,
  setNotice,
  runResolutionAction,
}) {
  function requestResolutionNewName(conflict, selectedAction, entry = null) {
    const providerEntry = entry || resolutionProviderEntry(conflict);
    resolutionPreview.value = null;
    resolutionNamePrompt.value = {
      conflict,
      action: selectedAction,
      entry: providerEntry,
      applyKey: resolutionApplyKey(conflict, selectedAction, providerEntry),
      label: resolutionActionLabel(selectedAction),
      autoApply: resolutionActionAutoAppliesForConflict(selectedAction, conflict),
    };
    resolutionNameInput.value = defaultResolutionNewName(conflict, selectedAction);
    setNotice('info', '请输入新技能名称后继续。');
  }

  function clearResolutionNamePrompt() {
    resolutionNamePrompt.value = null;
    resolutionNameInput.value = '';
  }

  async function confirmResolutionNewName() {
    const prompt = resolutionNamePrompt.value;
    if (!prompt) return;
    const newName = resolutionNameInput.value.toString().trim();
    if (!newName) {
      setNotice('error', '请输入新技能名称。');
      return;
    }
    const ok = await runResolutionAction(prompt.conflict, prompt.action, prompt.entry, newName);
    if (ok) clearResolutionNamePrompt();
  }

  function resolutionNamePromptApplies(conflict, entry = null) {
    const prompt = resolutionNamePrompt.value;
    if (!prompt) return false;
    if (prompt.applyKey === resolutionApplyKey(conflict, prompt.action, entry || resolutionProviderEntry(conflict))) return true;
    const promptConflictID = (prompt.conflict?.conflict_id || '').toString().trim();
    const conflictID = (conflict?.conflict_id || '').toString().trim();
    return sameNameResolutionConflict(conflict)
      && promptConflictID !== ''
      && promptConflictID === conflictID
      && Boolean(prompt.entry?.source);
  }

  return {
    requestResolutionNewName,
    clearResolutionNamePrompt,
    confirmResolutionNewName,
    resolutionNamePromptApplies,
  };
}

function createResolutionPreviewApplies({ resolutionPreview, resolutionProviderEntry }) {
  return function resolutionPreviewApplies(conflict, entry = null) {
    const preview = resolutionPreview.value;
    if (!preview) return false;
    const payload = preview.payload || {};
    const conflictID = (conflict?.conflict_id || '').toString().trim();
    if (!conflictID || (payload.conflictId || '').toString().trim() !== conflictID) return false;
    const previewSource = (payload.sourcePathId || payload.provider || payload.sourceProvider || '').toString().trim();
    const targetEntry = entry || resolutionProviderEntry(conflict);
    const entrySource = (targetEntry?.source_path_id || targetEntry?.sourcePathId || targetEntry?.provider || targetEntry?.sourceID || '').toString().trim();
    return !previewSource || !entrySource || previewSource === entrySource;
  };
}

function createResolutionListHandlers({
  activeCwdSource,
  resolutionConflicts,
  resolutionLoading,
  resolutionPreview,
  resolutionNamePrompt,
  resolutionNameInput,
  resolutionPanelCollapsed,
  resolutionLoadError,
  setNotice,
  onNoConflicts,
}) {
  async function refreshSkillResolutions(options = {}) {
    const notify = options?.notify !== false;
    const notifyOnError = notify || options?.notifyOnError === true;
    const collapseOnConflict = options?.collapseOnConflict === true;
    resolutionLoading.value = true;
    try {
      const conflicts = await listSkillResolutions(activeCwdSource.value);
      const normalizedConflicts = conflicts.map(normalizeResolutionConflict);
      resolutionConflicts.value = normalizedConflicts;
      resolutionLoadError.value = '';
      resolutionPanelCollapsed.value = collapseOnConflict && conflicts.length > 0;
      if (normalizedConflicts.length === 0 && typeof onNoConflicts === 'function') onNoConflicts();
      if (notify) setNotice('info', conflicts.length > 0 ? `发现 ${conflicts.length} 个技能冲突待处理。` : '');
      return normalizedConflicts;
    } catch (error) {
      resolutionLoadError.value = error?.message || String(error || '');
      if (notifyOnError) setNotice('error', `读取技能冲突失败：${error?.message || error}`);
      return null;
    } finally {
      resolutionLoading.value = false;
    }
  }

  function resetSkillResolutions() {
    resolutionConflicts.value = [];
    resolutionPreview.value = null;
    resolutionNamePrompt.value = null;
    resolutionNameInput.value = '';
    resolutionPanelCollapsed.value = false;
    resolutionLoadError.value = '';
  }

  return { refreshSkillResolutions, resetSkillResolutions };
}

function createMissingResolutionConflictHandler({
  resolutionPreview,
  resolutionNamePrompt,
  resolutionNameInput,
  listHandlers,
  setNotice,
}) {
  return async function handleMissingResolutionConflict(error, conflict) {
    if (!resolutionConflictNotFound(error)) return false;
    resolutionPreview.value = null;
    resolutionNamePrompt.value = null;
    resolutionNameInput.value = '';
    const refreshed = await listHandlers.refreshSkillResolutions({ notify: false, notifyOnError: true });
    if (!Array.isArray(refreshed)) return true;
    const stillNeedsAttention = refreshed.some((item) => sameResolutionConflictShape(item, conflict));
    setNotice(
      'info',
      stillNeedsAttention
        ? '这个技能冲突已经变化，列表已刷新，请按新的处理方式操作。'
        : '这个技能冲突已经处理或不存在，列表已刷新。',
    );
    return true;
  };
}

export function useSkillResolutions({ activeCwdSource, emit, setNotice, onNoConflicts }) {
  const resolutionConflicts = ref([]);
  const resolutionLoading = ref(false);
  const resolutionActioning = ref('');
  const resolutionPreview = ref(null);
  const resolutionNamePrompt = ref(null);
  const resolutionNameInput = ref('');
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

  function resolutionTitle(conflict) {
    const name = (conflict?.name || conflict?.skill_name || '').toString().trim() || '(unnamed)';
    return `${name} · ${resolutionConflictKindLabel(conflict)}`;
  }

  function resolutionProviderEntry(conflict) {
    return resolutionProviderEntries(conflict)[0] || {};
  }

  function resolutionProviderEntries(conflict) {
    const entries = Array.isArray(conflict?.provider_entries) ? conflict.provider_entries : [];
    if (entries.length > 0) return groupedResolutionProviderEntries(conflict, entries);
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
    if (personalDeletedDriftResolutionConflict(conflict)) {
      return actions.map(personalDeletedDriftActionEntry);
    }
    if (externalPersonalProjectResolutionConflict(conflict)) {
      const allowed = new Set(['view_diff', 'use_project_shared_skill', 'use_external_provider_skill', 'save_as_new_personal_skill']);
      return actions.filter((action) => allowed.has(action)).map(externalPersonalProjectActionEntry);
    }
    if (!sameNameResolutionConflict(conflict)) {
      return actions.map((action) => ({ action }));
    }
    const entries = [];
    const personalSources = sameNamePersonalSources(conflict);
    const projectSources = sameNameProjectSources(conflict);
    const hasProjectSource = sameNameHasProjectSource(conflict);
    if (actions.includes('keep_selected') && projectSources.length > 0) {
      projectSources.forEach((source) => {
        entries.push(sameNameProjectVersionEntryForSource(source, projectSources.length > 1));
      });
    }
    if (actions.includes('keep_selected')) {
      personalSources.forEach((source) => {
        const versionText = sameNamePersonalVersionText(source, hasProjectSource);
        entries.push({
          action: 'keep_selected',
          label: `用${versionText}，删除其他版本`,
          help: `保留这个${versionText}，删除其他同名版本。`,
          source,
          sourceID: resolutionSourceID(source),
        });
      });
    }
    if (actions.includes('rename_personal')) {
      [...projectSources, ...personalSources].forEach((source) => {
        entries.push(sameNameRenameEntry(source, projectSources.length > 1));
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
    if (requiresResolutionNewName(selectedAction)) {
      namePromptHandlers.requestResolutionNewName(conflict, selectedAction, entry);
      return;
    }
    await runResolutionAction(conflict, selectedAction, entry, '');
  }

  async function runResolutionAction(conflict, selectedAction, entry = null, newName = '') {
    const conflictId = (conflict?.conflict_id || '').toString().trim();
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
      if (resolutionActionAutoAppliesForConflict(selectedAction, conflict)) {
        await applyResolutionPreviewNow(preview, payload);
        resolutionPreview.value = null;
        setNotice('success', `已处理技能冲突：${conflict?.name || conflictId}`);
        emit('refresh-skills');
        await listHandlers.refreshSkillResolutions();
        return true;
      }
      resolutionPreview.value = {
        ...preview,
        action: selectedAction,
        payload,
        requiresApply: resolutionRequiresApply(selectedAction),
      };
      if (isResolutionViewAction(selectedAction)) {
        setNotice('info', `已生成处理预览：${conflict?.name || conflictId}`);
        return true;
      }
      if (isResolutionPreviewOnlyAction(selectedAction)) {
        setNotice('info', `已生成处理预览：${conflict?.name || conflictId}。此操作当前仅预览，不会直接写入。`);
        return true;
      }
      const proof = Array.isArray(preview?.items) ? preview.items[0] : null;
      if (!proof?.preview_id || !proof?.preview_hash) {
        throw new Error('缺少处理预览凭据');
      }
      setNotice('info', `已生成处理预览：${conflict?.name || conflictId}`);
      return true;
    } catch (error) {
      if (await handleMissingResolutionConflict(error, conflict)) return false;
      setNotice('error', `处理技能冲突失败：${error?.message || error}`);
      return false;
    } finally {
      resolutionActioning.value = '';
    }
  }

  const namePromptHandlers = createResolutionNamePromptHandlers({
    resolutionPreview,
    resolutionNamePrompt,
    resolutionNameInput,
    resolutionProviderEntry,
    resolutionApplyKey,
    setNotice,
    runResolutionAction,
  });
  const listHandlers = createResolutionListHandlers({
    activeCwdSource,
    resolutionConflicts,
    resolutionLoading,
    resolutionPreview,
    resolutionNamePrompt,
    resolutionNameInput,
    resolutionPanelCollapsed,
    resolutionLoadError,
    setNotice,
    onNoConflicts,
  });
  const handleMissingResolutionConflict = createMissingResolutionConflictHandler({ resolutionPreview, resolutionNamePrompt, resolutionNameInput, listHandlers, setNotice });
  const resolutionPreviewApplies = createResolutionPreviewApplies({ resolutionPreview, resolutionProviderEntry });

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
      await listHandlers.refreshSkillResolutions();
    } catch (error) {
      if (await handleMissingResolutionConflict(error, preview.payload || {})) return;
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
    resolutionNamePrompt,
    resolutionNameInput,
    resolutionPanelCollapsed,
    resolutionCheckButtonText,
    showResolutionCheckButton,
    showResolutionPanel,
    resolutionPanelToggleText,
    toggleResolutionPanel,
    refreshSkillResolutions: listHandlers.refreshSkillResolutions,
    resetSkillResolutions: listHandlers.resetSkillResolutions,
    resolutionTitle,
    resolutionActionLabel,
    resolutionKindLabel,
    resolutionProviderLabel,
    resolutionProviderEntryLabel,
    resolutionProviderEntry,
    resolutionProviderEntries,
    resolutionActionEntries,
    resolutionActionEntryLabel,
    resolutionActionEntryHelp,
    resolutionPreviewApplies,
    resolutionNamePromptHelpText,
    resolutionNamePromptButtonText,
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
    confirmResolutionNewName: namePromptHandlers.confirmResolutionNewName,
    clearResolutionNamePrompt: namePromptHandlers.clearResolutionNamePrompt,
    resolutionNamePromptApplies: namePromptHandlers.resolutionNamePromptApplies,
    clearResolutionPreview,
    confirmResolutionPreview,
  };
}
