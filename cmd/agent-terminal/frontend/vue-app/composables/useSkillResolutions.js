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
import {
  defaultResolutionNewName,
  isResolutionPreviewOnlyAction,
  isResolutionViewAction,
  normalizeResolutionConflict,
  requiresResolutionNewName,
  resolutionActionAutoAppliesForConflict,
  resolutionActionEntries,
  resolutionActionEntryHelp,
  resolutionActionEntryLabel,
  resolutionActionEntryTarget,
  resolutionActionFootnote,
  resolutionActionSectionTitle,
  resolutionActionUnsupported,
  resolutionConflictNotFound,
  resolutionManualSteps,
  resolutionNamePromptButtonText,
  resolutionNamePromptHelpText,
  resolutionProviderEntries,
  resolutionRequiresApply,
  resolutionSameNamePayloadFields,
  resolutionSourceID,
  sameNameResolutionConflict,
  sameResolutionConflictShape,
} from './useSkillResolutionActions.js';

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
    const activeCwd = (activeCwdSource.value || '').toString().trim();
    if (!activeCwd || activeCwd === '.') {
      resolutionConflicts.value = [];
      resolutionPreview.value = null;
      resolutionNamePrompt.value = null;
      resolutionNameInput.value = '';
      resolutionPanelCollapsed.value = false;
      resolutionLoadError.value = '';
      resolutionLoading.value = false;
      return [];
    }
    resolutionLoading.value = true;
    try {
      const conflicts = await listSkillResolutions(activeCwd);
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

  function resolutionApplyKey(conflict, action, entry = null) {
    const source = (entry?.source_path_id || entry?.provider || entry?.sourceID || resolutionSourceID(entry?.source) || '').toString().trim();
    return `${conflict?.conflict_id || ''}:${source}:${action || ''}`;
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
