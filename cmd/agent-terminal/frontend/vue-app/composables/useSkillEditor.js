import { computed, nextTick, reactive, ref, watch } from '../../lib/vue.esm-browser.prod.js';
import { callAPI, selectProjectDirs } from '../services/api.js';
import { logWarn } from '../services/log.js';
import { importSkills, writeSkill } from '../services/skills-api.js';
import { renderAssistantMarkdown } from '../utils/assistant-markdown.js';
import {
  applyParsedSkillState,
  isCurrentEditorTarget,
  isMissingSkillsCwdError,
  isSummarySuggestQualityError,
  isSummarySuggestUnavailableError,
  mainSkillSavePath,
  normalizeSkillScope,
  personalTypeForScope,
  personalTypeFromForm,
  rememberLoadedMainSkillTarget,
  requireSkillsCwd,
  requestSkillSummarySuggestion,
  resetLoadedMainSkillTarget,
  scopeFromTrust,
  updateNotice,
  withSkillsCwd,
} from './useSkillEditorHelpers.js';
import {
  createSkillImportSummaryActions,
  duplicateImportNotice,
  importSummaryDraftMessage,
  importSummaryDraftNotice,
  normalizeImportFailure,
} from './useSkillImportSummaryDrafts.js';
import {
  listToText, inferSkillNameFromPath, summarizeItems, normalizeWordList,
  normalizePathKey, fileNameFromPath, isSkillMainFilePath,
  parseSkillMarkdown, buildSkillMarkdown,
  skillDescriptionQualitySaveMessage, validateSkillNameText,
} from '../utils/skill-parser.js';

function createSkillFileReaders(props, state) {
  async function loadSkillFiles(dir, preferredPath = '') {
    const raw = await callAPI('skills/local/listFiles', withSkillsCwd(props, { dir }));
    const files = Array.isArray(raw?.files) ? raw.files : [];
    const normalized = files
      .map((item) => ({
        name: (item?.name || '').toString().trim(),
        path: (item?.path || '').toString().trim(),
        isMain: Boolean(item?.is_main),
      }))
      .filter((item) => item.name && item.path);
    state.skillFiles.value = normalized;
    if (normalized.length === 0) {
      state.activeSkillFilePath.value = '';
      return;
    }
    const preferredKey = normalizePathKey(preferredPath);
    const nextActive = normalized.find((item) => normalizePathKey(item.path) === preferredKey)
      || normalized.find((item) => item.isMain)
      || normalized[0];
    if (nextActive?.path) {
      state.activeSkillFilePath.value = nextActive.path;
    }
  }

  async function readSkillFile(path, fallbackName = '', fallbackSummary = '', fallbackSource = '') {
    const raw = await callAPI('skills/local/read', withSkillsCwd(props, { path }));
    const content = (raw?.skill?.content || '').toString();
    if (!content.trim()) {
      throw new Error('读取的技能文件为空');
    }
    const serverSummary = (raw?.skill?.summary || '').toString().trim();
    const serverSummarySource = (raw?.skill?.summary_source || '').toString().trim();
    const parsed = parseSkillMarkdown(content, fallbackName);
    const finalFallbackSummary = serverSummary || fallbackSummary;
    const finalFallbackSource = serverSummarySource || fallbackSource;
    applyParsedSkillState(state, parsed, content, path, finalFallbackSummary, finalFallbackSource);
  }

  return { loadSkillFiles, readSkillFile };
}

function createImportActions(props, emit, deps, state, readers, importSummaryActions) {
  async function importSelectedSkillDirs(folderPaths, scope) {
    state.uploading.value = true;
    state.importSummaryDrafts.value = [];
    try {
      const selectedNames = folderPaths
        .map((entryPath) => inferSkillNameFromPath(entryPath))
        .filter(Boolean);
      const existingNameSet = new Set(
        deps.skillCards.value.map((item) => (item?.name || '').toString().toLowerCase()).filter(Boolean),
      );
      const overwriteNames = selectedNames.filter((name) => existingNameSet.has(name.toLowerCase()));
      if (overwriteNames.length > 0) {
        state.setNotice('info', `检测到同名技能：${summarizeItems(overwriteNames)}，导入可能触发同名冲突，继续导入中...`);
      }

      const resolvedImportScope = normalizeSkillScope(scope);
      const resolvedImportPersonalType = personalTypeForScope(resolvedImportScope, null, 'imported');
      const imported = await importSkills(
        requireSkillsCwd(props),
        folderPaths,
        resolvedImportScope,
        resolvedImportPersonalType,
      );
      const importedSkills = Array.isArray(imported?.imported)
        ? imported.imported
        : (Array.isArray(imported?.skills) ? imported.skills : []);
      const failures = Array.isArray(imported?.failures) ? imported.failures : [];
      const normalizedFailures = failures.map(normalizeImportFailure);
      const duplicateFailures = normalizedFailures.filter((item) => item.duplicate);
      const blockingFailures = normalizedFailures.filter((item) => !item.duplicate);
      state.importFailures.value = normalizedFailures.map((item) => item.text);
	      const firstSkill = importedSkills[0] || null;
	      let openImportedError = null;

	      emit('refresh-skills');
	      if (firstSkill?.skill_file) {
	        try {
	          await readers.readSkillFile(firstSkill.skill_file, firstSkill.name || '');
	          state.form.scope = resolvedImportScope;
	          state.form.personal_type = resolvedImportPersonalType;
	          rememberLoadedMainSkillTarget(state, resolvedImportScope, resolvedImportPersonalType);
	        } catch (error) {
	          openImportedError = error;
	          logWarn('skills', 'upload.open_imported_failed', { error, path: firstSkill.skill_file });
	        }
	      }
      const importSummaryDrafts = await importSummaryActions.refreshImportSummaryDrafts(
        importedSkills,
        resolvedImportScope,
        resolvedImportPersonalType,
      );
	      if (failures.length > 0) {
	        const draftMessage = importSummaryDraftMessage(importSummaryDrafts);
	        if (blockingFailures.length === 0) {
	          state.setNotice(
	            importedSkills.length > 0 ? 'success' : 'info',
	            importedSkills.length > 0
	              ? `导入完成：成功 ${importedSkills.length}，${duplicateImportNotice(resolvedImportScope, duplicateFailures)}${draftMessage ? `，${draftMessage}` : ''}`
	              : duplicateImportNotice(resolvedImportScope, duplicateFailures),
	          );
	          return true;
	        }
	        const duplicateText = duplicateFailures.length > 0 ? `，已存在 ${duplicateFailures.length} 个未重复导入` : '';
	        state.setNotice('error', `导入完成：成功 ${importedSkills.length}，失败 ${blockingFailures.length}${duplicateText}${draftMessage ? `，${draftMessage}` : ''}`);
	        return true;
	      }
      if (importedSkills.length === 0) {
	        state.setNotice('info', '未导入任何技能目录');
	        return true;
	      }
	      if (openImportedError) {
	        const draftMessage = importSummaryDraftMessage(importSummaryDrafts);
	        state.setNotice('success', `已导入 ${importedSkills.length} 个技能目录，列表已刷新${draftMessage ? `，${draftMessage}` : '。'}`);
	        return true;
	      }
	      state.setNotice('success', importSummaryDraftNotice(importedSkills.length, importSummaryDrafts));
	      return true;
    } catch (error) {
      logWarn('skills', 'upload.failed', { error });
      state.setNotice('error', `导入目录失败：${error?.message || error}`);
      return false;
    } finally {
      state.uploading.value = false;
      state.pendingImportDirs.value = [];
      state.importScopePromptOpen.value = false;
    }
  }

  async function onUploadSkill() {
    if (state.uploading.value || state.importScopePromptOpen.value) return;
    state.importFailures.value = [];
    state.pendingImportDirs.value = [];
    state.importScopePromptOpen.value = true;
    state.setNotice('info', '请选择导入后的使用范围');
  }

  async function selectDirsForImport() {
    try {
      const folderPaths = await selectProjectDirs();
      if (!Array.isArray(folderPaths) || folderPaths.length === 0) {
        state.setNotice('info', '未选择目录');
        return [];
      }
      return folderPaths;
    } catch (error) {
      logWarn('skills', 'upload.select.failed', { error });
      state.setNotice('error', `选择目录失败：${error?.message || error}`);
      return [];
    }
  }

  function duplicatedSkillNames(folderPaths) {
    const selectedNames = folderPaths
      .map((entryPath) => inferSkillNameFromPath(entryPath))
      .filter(Boolean);
    const selectedNameSeen = new Set();
    const duplicatedNameSet = new Set();
    for (const name of selectedNames) {
      const key = name.toLowerCase();
      if (selectedNameSeen.has(key)) {
        duplicatedNameSet.add(name);
        continue;
      }
      selectedNameSeen.add(key);
    }
    return Array.from(duplicatedNameSet);
  }

  function cancelImportScopePrompt() {
    state.pendingImportDirs.value = [];
    state.importScopePromptOpen.value = false;
    state.setNotice('info', '已取消导入');
  }

  async function confirmImportScope(scope = state.importScope.value) {
    if (state.uploading.value) return false;
    state.importScope.value = normalizeSkillScope(scope);
    state.importScopePromptOpen.value = false;
    state.uploading.value = true;
    try {
      const folderPaths = await selectDirsForImport();
      if (folderPaths.length === 0) return false;
      const duplicatedNames = duplicatedSkillNames(folderPaths);
      if (duplicatedNames.length > 0) {
        state.setNotice('error', `选择目录中存在重复技能名：${summarizeItems(duplicatedNames)}`);
        return false;
      }
      state.uploading.value = false;
      return await importSelectedSkillDirs(folderPaths, state.importScope.value);
    } finally {
      state.uploading.value = false;
    }
  }

  return { onUploadSkill, cancelImportScopePrompt, confirmImportScope };
}

function createEditorActions(props, emit, deps, state, readers) {
  function onCreateSkill() {
    state.selectedSkillName.value = '';
    state.summarySource.value = '';
    state.sourcePath.value = '';
    state.skillFiles.value = [];
    state.activeSkillFilePath.value = '';
    state.form.name = '';
    state.form.displayName = '';
    state.form.description = '';
    state.form.summary = '';
    state.generatedSummaryPreview.value = '';
    state.summarySuggestion.value = '';
    state.form.triggerWordsText = '';
    state.form.forceWordsText = '';
    state.form.internalScenarioWordsText = '';
    state.form.body = '';
    state.form.scope = 'project';
    state.form.personal_type = '';
    resetLoadedMainSkillTarget(state);
    state.isBodyEditing.value = true;
    state.bodyEditorFocused.value = false;
    state.isEditorOpen.value = true;
    state.setNotice('info', '已打开新建表单，填写后点击保存。');
    nextTick(() => {
      const node = state.bodyInputRef.value;
      if (node && typeof node.focus === 'function') node.focus();
    });
  }

  async function onEditSkill(item) {
    if (!item?.dir) return;
    const skillPath = `${item.dir}/SKILL.md`;
    try {
      await readers.readSkillFile(skillPath, item.name || '', item.summary || '', item.summary ? 'generated' : '');
      let filesLoadErrorMessage = '';
      try {
        await readers.loadSkillFiles(item.dir, skillPath);
      } catch (error) {
        filesLoadErrorMessage = (error?.message || error || '').toString();
        state.skillFiles.value = [];
        state.activeSkillFilePath.value = skillPath;
        logWarn('skills', 'load.subfiles.failed', { error, dir: item.dir, path: skillPath });
      }
      state.selectedSkillName.value = item.name || '';
      state.form.scope = normalizeSkillScope(item?.scope || scopeFromTrust(item?.trust));
      state.form.personal_type = personalTypeForScope(state.form.scope, item, 'user');
      if (!state.form.displayName) {
        state.form.displayName = (item?.displayName || item?.display_name || item?.title || '').toString().trim();
      }
      rememberLoadedMainSkillTarget(state, state.form.scope, state.form.personal_type);
      state.isBodyEditing.value = false;
      state.bodyEditorFocused.value = false;
      state.isEditorOpen.value = true;
      if (filesLoadErrorMessage) {
        state.setNotice('error', `技能已加载，但附加内容读取失败：${filesLoadErrorMessage}`);
      } else {
        state.setNotice('info', '');
      }
    } catch (error) {
      logWarn('skills', 'load.savedSkill.failed', { error, path: skillPath });
      state.setNotice('error', `读取技能失败：${error?.message || error}`);
    }
  }

  function isDeletingSkill(name) {
    return (state.deletingSkillName.value || '').toLowerCase() === (name || '').toString().toLowerCase();
  }

  function onDeleteSkill(item) {
    const skillName = (item?.name || '').toString().trim();
    if (!skillName || state.deletingSkillName.value) return;
    state.confirmDeleteTarget.value = item;
  }

  function cancelSkillDelete() {
    state.confirmDeleteTarget.value = null;
  }

  async function confirmSkillDelete() {
    const item = state.confirmDeleteTarget.value;
    const skillName = (item?.name || '').toString().trim();
    if (!skillName) return;
    state.confirmDeleteTarget.value = null;
    state.deletingSkillName.value = skillName;
    try {
      const scope = normalizeSkillScope(item?.scope || scopeFromTrust(item?.trust) || state.form.scope);
      const personalType = personalTypeForScope(scope, item, 'user');
      const payload = {
        name: skillName,
        scope,
      };
      if (personalType) payload.personal_type = personalType;
      await callAPI('skills/local/delete', withSkillsCwd(props, payload));
      const deletesCurrentEditorTarget = isCurrentEditorTarget(state, item, scope, personalType);
      if (deletesCurrentEditorTarget) {
        state.selectedSkillName.value = '';
        state.form.name = '';
        state.form.displayName = '';
        state.form.description = '';
        state.form.summary = '';
        state.generatedSummaryPreview.value = '';
        state.summarySuggestion.value = '';
        state.form.triggerWordsText = '';
        state.form.forceWordsText = '';
        state.form.internalScenarioWordsText = '';
        state.form.body = '';
        state.form.scope = 'project';
        state.form.personal_type = '';
        resetLoadedMainSkillTarget(state);
        state.summarySource.value = '';
        state.sourcePath.value = '';
        state.skillFiles.value = [];
        state.activeSkillFilePath.value = '';
        state.isEditorOpen.value = false;
      }
      emit('refresh-skills');
      state.setNotice('info', '');
    } catch (error) {
      logWarn('skills', 'delete.failed', { error, skill: skillName });
      state.setNotice('error', `删除技能失败：${error?.message || error}`);
    } finally {
      state.deletingSkillName.value = '';
    }
  }

  async function onSaveSkill() {
    const targetPath = (state.activeSkillFilePath.value || state.sourcePath.value || '').toString().trim();
    state.saving.value = true;
    try {
      if (!state.resolvedIsEditingMainSkillFile.value) {
        if (!targetPath) {
          throw new Error('缺少文件路径，无法保存');
        }
        const scope = normalizeSkillScope(state.form.scope);
        await writeSkill(requireSkillsCwd(props), targetPath, (state.form.body || '').toString(), scope, personalTypeFromForm(state.form));
        state.setNotice('success', `文件已保存：${fileNameFromPath(targetPath) || targetPath}`);
        closeEditor();
        return;
      }

      const name = (state.form.name || '').trim();
      const nameError = validateSkillNameText(name);
      if (nameError) {
        state.setNotice('error', nameError);
        return;
      }
      const hasDescription = Boolean(((state.form.description || '').trim() || (state.form.summary || '').trim()));
      const content = buildSkillMarkdown(state.form);
      state.form.scope = normalizeSkillScope(state.form.scope);
      const writePath = mainSkillSavePath(state, name);
      const nameKey = name.toLowerCase();
      const sameNameOtherScope = deps.skillCards.value.some((item) => {
        const itemName = (item?.name || '').toString().trim().toLowerCase();
        if (itemName !== nameKey) return false;
        const itemScope = normalizeSkillScope(item?.scope || scopeFromTrust(item?.trust));
        const itemPersonalType = personalTypeForScope(itemScope, item, 'user');
        const nextPersonalType = personalTypeFromForm(state.form);
        return itemScope !== state.form.scope || itemPersonalType !== nextPersonalType;
      });
      const saved = await writeSkill(requireSkillsCwd(props), writePath, content, state.form.scope, personalTypeFromForm(state.form));
      state.selectedSkillName.value = name;
      state.summarySource.value = 'frontmatter';
      if (saved?.path) {
        state.sourcePath.value = saved.path;
        state.activeSkillFilePath.value = saved.path;
      }
      rememberLoadedMainSkillTarget(state, state.form.scope, personalTypeFromForm(state.form));
      emit('refresh-skills');
      const explicitDescription = (state.form.description || '').toString().trim();
      let saveMessage = '已保存。建议填写简介，更好使用技能。';
      if (sameNameOtherScope) {
        saveMessage = '已保存；已经有同名技能，请选择处理方式。';
      } else if (explicitDescription) {
        saveMessage = skillDescriptionQualitySaveMessage(explicitDescription);
      } else if (hasDescription) {
        saveMessage = '已保存';
      }
      state.setNotice('success', saveMessage);
      closeEditor();
    } catch (error) {
      logWarn('skills', 'save.failed', {
        error,
        name: (state.form.name || '').trim(),
        path: targetPath,
        main_file: state.resolvedIsEditingMainSkillFile.value,
      });
      state.setNotice('error', `保存失败：${error?.message || error}`);
    } finally {
      state.saving.value = false;
    }
  }

  function closeEditor() {
    state.isEditorOpen.value = false;
    state.isBodyEditing.value = false;
    state.bodyEditorFocused.value = false;
  }

  function startBodyEdit() {
    state.isBodyEditing.value = true;
    nextTick(() => {
      const node = state.bodyInputRef.value;
      if (node && typeof node.focus === 'function') node.focus();
    });
  }

  function finishBodyEdit() {
    state.isBodyEditing.value = false;
    state.bodyEditorFocused.value = false;
  }

  function onBodyFocus() {
    state.bodyEditorFocused.value = true;
  }

  function onBodyBlur() {
    state.bodyEditorFocused.value = false;
  }

  return {
    onCreateSkill,
    onEditSkill,
    isDeletingSkill,
    onDeleteSkill,
    confirmSkillDelete,
    cancelSkillDelete,
    onSaveSkill,
    closeEditor,
    startBodyEdit,
    finishBodyEdit,
    onBodyFocus,
    onBodyBlur,
  };
}

/**
 * 管理 SkillsPage 的编辑器状态、CRUD 与目录导入。
 *
 * @param {object} props
 * @param {(event: string, ...args: any[]) => void} emit
 * @param {{ skillCards: any, isEditingMainSkillFile?: any, sourcePath?: any, activeSkillFilePath?: any }} deps
 */
export function useSkillEditor(props, emit, deps) {
  const selectedSkillName = ref('');
  const summarySource = ref('');
  const sourcePath = deps.sourcePath || ref('');
  const skillFiles = ref([]);
  const activeSkillFilePath = deps.activeSkillFilePath || ref('');
  const importFailures = ref([]);
  const importSummaryDrafts = ref([]);
  const importSummaryGenerating = ref(false);
  const notice = reactive({ level: 'info', message: '' });
  const saving = ref(false);
  const uploading = ref(false);
  const deletingSkillName = ref('');
  const confirmDeleteTarget = ref(null);
  const pendingImportDirs = ref([]);
  const importScopePromptOpen = ref(false);
  const isEditorOpen = ref(false);
  const isBodyEditing = ref(false);
  const bodyEditorFocused = ref(false);
  const bodyInputRef = ref(null);
  const importScope = ref('project');
  const generatedSummaryPreview = ref('');
  const summarySuggestion = ref('');
  const summarySuggesting = ref(false);
  const loadedMainSkillScope = ref('');
  const loadedMainSkillPersonalType = ref('');
  const form = reactive({
    name: '',
    displayName: '',
    description: '',
    summary: '',
    triggerWordsText: '',
    forceWordsText: '',
    internalScenarioWordsText: '',
    body: '',
    scope: 'project',
    personal_type: '',
  });

  const summarySourceLabel = computed(() => {
    const source = (summarySource.value || '').toLowerCase();
    if (source === 'frontmatter') return '用户摘要';
    if (source === 'description') return '系统生成（基于描述）';
    if (source === 'generated') return '系统生成（基于正文）';
    return '系统生成';
  });

  const skillBodyMarkdownHtml = computed(() => {
    const text = (form.body || '').toString().trim();
    if (!text) return '<p>暂无内容，点击“编辑正文”开始编写。</p>';
    return renderAssistantMarkdown(text);
  });
  const scenarioKeywordsText = computed({
    get() {
      return listToText(normalizeWordList(`${form.triggerWordsText || ''},${form.forceWordsText || ''}`));
    },
    set(value) {
      form.triggerWordsText = listToText(normalizeWordList(value));
      form.forceWordsText = '';
    },
  });

  const isEditingMainSkillFile = computed(() => {
    const candidate = (activeSkillFilePath.value || sourcePath.value || '').toString().trim();
    if (!candidate) return true;
    return isSkillMainFilePath(candidate);
  });
  const showRelatedSkillFiles = computed(() => skillFiles.value.some((file) => !file?.isMain));
  const resolvedIsEditingMainSkillFile = deps.isEditingMainSkillFile || isEditingMainSkillFile;

  const state = {
    selectedSkillName,
    summarySource,
    sourcePath,
    skillFiles,
    activeSkillFilePath,
    importFailures,
    importSummaryDrafts,
    importSummaryGenerating,
    notice,
    saving,
    uploading,
    deletingSkillName,
    confirmDeleteTarget,
    pendingImportDirs,
    importScopePromptOpen,
    isEditorOpen,
    isBodyEditing,
    bodyEditorFocused,
    bodyInputRef,
    importScope,
    generatedSummaryPreview,
    summarySuggestion,
    loadedMainSkillScope,
    loadedMainSkillPersonalType,
    form,
    resolvedIsEditingMainSkillFile,
    setNotice: (level, message) => updateNotice(notice, level, message),
  };

  async function onSuggestSkillSummary() {
    if (summarySuggesting.value) return;
    if (!(form.name || '').trim() && !(form.body || '').trim()) {
      updateNotice(notice, 'info', '先填写技能名称或内容，再生成简介。');
      return;
    }
    summarySuggesting.value = true;
    try {
      summarySuggestion.value = '';
      updateNotice(notice, 'info', '正在生成简介...');
      const description = await requestSkillSummarySuggestion(requireSkillsCwd(props), {
        name: form.name,
        description: form.description,
        content: form.body,
        scenarioWords: normalizeWordList(scenarioKeywordsText.value),
        scope: form.scope,
      });
      if (!description) {
        updateNotice(notice, 'error', '生成失败：没有生成可用简介');
        return;
      }
      summarySuggestion.value = description;
      updateNotice(notice, 'info', '已生成简介建议，采用后再保存。');
    } catch (error) {
      logWarn('skills', 'summary.suggest.failed', { error, name: (form.name || '').trim() });
      if (isMissingSkillsCwdError(error)) {
        updateNotice(notice, 'error', error.message);
        return;
      }
      if (isSummarySuggestUnavailableError(error)) {
        updateNotice(notice, 'error', '当前无法生成简介，请稍后再试或手动填写。');
        return;
      }
      if (isSummarySuggestQualityError(error)) {
        updateNotice(notice, 'error', '生成的简介不够具体，请补充技能内容后重新生成，或手动填写。');
        return;
      }
      updateNotice(notice, 'error', '当前无法生成简介，请稍后再试或手动填写。');
    } finally {
      summarySuggesting.value = false;
    }
  }

  function applySummarySuggestion() {
    const value = (summarySuggestion.value || '').toString().trim();
    if (!value) return;
    form.description = value;
    summarySuggestion.value = '';
    summarySource.value = 'description';
  }

  const readers = createSkillFileReaders(props, state);
  const importSummaryActions = createSkillImportSummaryActions(props, state, readers, {
    requestSkillSummarySuggestion,
  });
  const importActions = createImportActions(props, emit, deps, state, readers, importSummaryActions);
  const editorActions = createEditorActions(props, emit, deps, state, readers);

  watch(deps.skillCards, (nextCards) => {
    const current = (selectedSkillName.value || '').toString().trim().toLowerCase();
    if (!current) return;
    const exists = nextCards.some((item) => (item?.name || '').toString().trim().toLowerCase() === current);
    if (!exists) {
      selectedSkillName.value = '';
    }
  });

  watch(deps.skillCards, (nextCards) => {
    const currentPathKey = normalizePathKey(sourcePath.value || '');
    if (!currentPathKey) return;
    const exists = nextCards.some((item) => {
      const dirKey = normalizePathKey(item?.dir || '');
      if (!dirKey) return false;
      return currentPathKey === `${dirKey}/skill.md` || currentPathKey.startsWith(`${dirKey}/`);
    });
    if (!exists) {
      selectedSkillName.value = '';
      isEditorOpen.value = false;
      skillFiles.value = [];
      activeSkillFilePath.value = '';
    }
  });

  watch(() => form.scope, (next) => {
    const normalized = normalizeSkillScope(next);
    if (normalized !== next) form.scope = normalized;
    if (normalized !== 'personal') form.personal_type = '';
    if (normalized === 'personal' && !form.personal_type) form.personal_type = 'user';
  }, { immediate: true });

  watch(importScope, (next) => {
    const normalized = normalizeSkillScope(next);
    if (normalized !== next) importScope.value = normalized;
  }, { immediate: true });

  return {
    selectedSkillName,
    summarySource,
    summarySourceLabel,
    scenarioKeywordsText,
    sourcePath,
    skillFiles,
    activeSkillFilePath,
    isEditingMainSkillFile: resolvedIsEditingMainSkillFile,
    showRelatedSkillFiles,
    importFailures,
    importSummaryDrafts,
    importSummaryGenerating,
    notice,
    saving,
    uploading,
    deletingSkillName,
    confirmDeleteTarget,
    pendingImportDirs,
    importScopePromptOpen,
    isEditorOpen,
    isBodyEditing,
    bodyEditorFocused,
    bodyInputRef,
    importScope,
    generatedSummaryPreview,
    summarySuggestion,
    summarySuggesting,
    form,
    skillBodyMarkdownHtml,
    setNotice: state.setNotice,
    onSuggestSkillSummary,
    applySummarySuggestion,
    ...readers,
    ...importSummaryActions,
    ...importActions,
    ...editorActions,
  };
}
