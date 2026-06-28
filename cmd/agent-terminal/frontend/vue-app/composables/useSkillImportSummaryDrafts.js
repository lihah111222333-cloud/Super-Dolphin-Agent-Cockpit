import { callAPI } from '../services/api.js';
import { logWarn } from '../services/log.js';
import { requireSkillsCwd } from './useSkillEditorHelpers.js';
import {
  fileNameFromPath, listToText, normalizePathKey, normalizeWordList,
  parseSkillMarkdown, skillDescriptionQualityIssue, summarizeItems,
} from '../utils/skill-parser.js';

function withSkillsCwd(cwd, payload = {}) {
  return { ...payload, cwd };
}

function normalizeSkillScope(scope) {
  const normalized = (scope || '').toString().trim().toLowerCase();
  if (normalized === 'personal' || normalized === 'user' || normalized === 'signed' || normalized === 'system') {
    return 'personal';
  }
  return 'project';
}

function personalTypeForScope(scope, personalType = 'user') {
  if (normalizeSkillScope(scope) !== 'personal') return '';
  const normalized = (personalType || '').toString().trim().toLowerCase();
  if (normalized === 'agent' || normalized === 'imported') return normalized;
  return 'user';
}

function rememberLoadedMainSkillTarget(state, scope, personalType) {
  state.loadedMainSkillScope.value = normalizeSkillScope(scope);
  state.loadedMainSkillPersonalType.value = personalTypeForScope(scope, personalType);
}

function importedSkillFilePath(item) {
  return (item?.skill_file || item?.skillFile || item?.path || '').toString().trim();
}

function importSummaryDraftID(skillFile, name, index) {
  const key = skillFile || name || `skill-${index}`;
  return `${index}:${key}`;
}

export function importSummaryDraftMessage(drafts) {
  const list = Array.isArray(drafts) ? drafts : [];
  const readyCount = list.filter((item) => item.status === 'ready').length;
  const conflictCount = list.filter((item) => item.status === 'conflict').length;
  const errorCount = list.filter((item) => item.status === 'error').length;
  const parts = [];
  if (readyCount > 0) parts.push(`已生成 ${readyCount} 条简介建议，采用后再保存。`);
  if (conflictCount > 0) parts.push(`${conflictCount} 个同名技能待处理。`);
  if (errorCount > 0) parts.push(`${errorCount} 个技能可手动补充简介。`);
  return parts.join('，');
}

export function importSummaryDraftNotice(importedCount, drafts) {
  const draftMessage = importSummaryDraftMessage(drafts);
  if (!draftMessage) return `已导入 ${importedCount} 个技能目录（含资源文件）`;
  return `已导入 ${importedCount} 个技能目录，${draftMessage}`;
}

function duplicateImportFailureMessage(message) {
  const raw = (message || '').toString().trim();
  const existsMatch = raw.match(/^skill already exists:\s*(.+)$/i);
  if (existsMatch) return `${(existsMatch[1] || '').toString().trim() || '该技能'} 已存在，未重复导入。`;
  if (/^source is inside skills root:/i.test(raw)) return '这个目录已经在技能管理中，未重复导入。';
  return '';
}

export function normalizeImportFailure(item) {
  const source = (item?.source || '').toString().trim();
  const rawMessage = (item?.error || '未知错误').toString().trim();
  const duplicateMessage = duplicateImportFailureMessage(rawMessage);
  return {
    source,
    message: duplicateMessage || rawMessage || '未知错误',
    duplicateName: rawMessage.match(/^skill already exists:\s*(.+)$/i)?.[1]?.toString().trim() || '',
    duplicate: Boolean(duplicateMessage),
    text: `${source || '-'}：${duplicateMessage || rawMessage || '未知错误'}`,
  };
}

export function duplicateImportNotice(scope, duplicateFailures) {
  const names = duplicateFailures.map((item) => item.duplicateName).filter(Boolean);
  const prefix = normalizeSkillScope(scope) === 'personal' ? '私人使用里已存在' : '项目共享里已存在';
  return names.length > 0
    ? `${prefix}：${summarizeItems(names)}，未重复导入。`
    : `${prefix} ${duplicateFailures.length} 个技能，未重复导入。`;
}

export function importSummaryDraftPanelTitle(drafts) {
  const list = Array.isArray(drafts) ? drafts : [];
  const readyCount = list.filter((item) => item.status === 'ready' || item.status === 'applied').length;
  const conflictCount = list.filter((item) => item.status === 'conflict').length;
  const errorCount = list.filter((item) => item.status === 'error').length;
  if (conflictCount > 0 && readyCount === 0) return '导入后需要处理';
  if (conflictCount > 0) return '导入后的简介建议和同名处理';
  if (errorCount > 0 && readyCount === 0) return '导入后可补充简介';
  return '导入后的简介建议';
}

export function importSummaryDraftPanelHint(drafts) {
  const list = Array.isArray(drafts) ? drafts : [];
  const readyCount = list.filter((item) => item.status === 'ready' || item.status === 'applied').length;
  const conflictCount = list.filter((item) => item.status === 'conflict').length;
  const errorCount = list.filter((item) => item.status === 'error').length;
  if (conflictCount > 0 && readyCount === 0) return '同名技能需要先选择使用哪个版本。';
  if (conflictCount > 0) return '简介建议采用并保存后生效；同名技能需要选择使用哪个版本。';
  if (errorCount > 0 && readyCount === 0) return '技能已正常导入，可以稍后手动补充简介。';
  return '还没有写入技能，采用并保存后生效。';
}

async function readImportedSkillForSummary(cwd, skillFile, fallbackName = '') {
  const raw = await callAPI('skills/local/read', withSkillsCwd(cwd, { path: skillFile }));
  const content = (raw?.skill?.content || '').toString();
  if (!content.trim()) {
    throw new Error('读取的技能文件为空');
  }
  return parseSkillMarkdown(content, fallbackName);
}

function isImportedSkillSameNameConflictError(error) {
  const message = (error?.message || error || '').toString().toLowerCase();
  return message.includes('skill same-name conflict')
    || message.includes('err_skill_same_name_conflict')
    || message.includes('skill path is not in effective skill set');
}

function importedSkillSameNameConflictMessage(draft) {
  return draft.scope === 'personal'
    ? '已导入，但和项目共享技能同名，暂未启用。请在冲突提示中选择使用哪个版本。'
    : '已导入，但与现有技能同名，暂未启用。请在冲突提示中选择使用哪个版本。';
}

export function createSkillImportSummaryActions(props, state, readers, options = {}) {
  const requestSkillSummarySuggestion = options.requestSkillSummarySuggestion;

  async function createImportSummaryDraft(item, scope, personalType, index) {
    const cwd = requireSkillsCwd(props);
    const skillFile = importedSkillFilePath(item);
    const fallbackName = (item?.name || '').toString().trim();
    const dirName = fileNameFromPath(skillFile.replace(/[\\/]SKILL\.md$/i, ''));
    const baseDraft = {
      id: importSummaryDraftID(skillFile, fallbackName, index),
      name: fallbackName || dirName,
      skillFile,
      scope: normalizeSkillScope(scope),
      personalType: personalTypeForScope(scope, personalType || 'imported'),
      currentDescription: '',
      currentSummary: '',
      suggestion: '',
      status: 'ready',
      source: 'llm',
      error: '',
    };
    if (!skillFile) return null;

    try {
      const parsed = await readImportedSkillForSummary(cwd, skillFile, fallbackName);
      const name = parsed.name || fallbackName || baseDraft.name;
      const currentDescription = (parsed.description || '').toString().trim();
      const currentSummary = (parsed.summary || '').toString().trim();
      const issue = skillDescriptionQualityIssue(currentDescription);
      if (!issue) return null;

      if (!currentDescription && currentSummary && !skillDescriptionQualityIssue(currentSummary)) {
        return {
          ...baseDraft,
          name,
          currentSummary,
          suggestion: currentSummary,
          source: 'summary',
          issue,
        };
      }

      if (typeof requestSkillSummarySuggestion !== 'function') {
        throw new Error('缺少简介生成能力');
      }
      const suggestion = await requestSkillSummarySuggestion(cwd, {
        name,
        description: currentDescription || currentSummary,
        content: parsed.body,
        scenarioWords: normalizeWordList(`${listToText(parsed.triggerWords)},${listToText(parsed.forceWords)}`),
        scope: normalizeSkillScope(scope),
      });
      if (!suggestion) return null;
      return {
        ...baseDraft,
        name,
        currentDescription,
        currentSummary,
        suggestion,
        issue,
      };
    } catch (error) {
      logWarn('skills', 'upload.summary_suggest_failed', { error, path: skillFile });
      if (isImportedSkillSameNameConflictError(error)) {
        return {
          ...baseDraft,
          status: 'conflict',
          error: importedSkillSameNameConflictMessage(baseDraft),
        };
      }
      try {
        const parsed = await readImportedSkillForSummary(cwd, skillFile, fallbackName);
        if ((parsed.description || '').toString().trim()) return null;
      } catch (readError) {
        logWarn('skills', 'upload.summary_suggest_read_after_failure_failed', { error: readError, path: skillFile });
      }
      return {
        ...baseDraft,
        status: 'error',
        error: '技能已正常导入。可以稍后重试，或手动补充简介。',
      };
    }
  }

  async function refreshImportSummaryDrafts(importedSkills, scope, personalType) {
    state.importSummaryDrafts.value = [];
    if (!Array.isArray(importedSkills) || importedSkills.length === 0) return [];
    const drafts = [];
    state.importSummaryGenerating.value = true;
    try {
      for (let index = 0; index < importedSkills.length; index += 1) {
        const draft = await createImportSummaryDraft(importedSkills[index], scope, personalType, index);
        if (draft) drafts.push(draft);
      }
      state.importSummaryDrafts.value = drafts;
      return drafts;
    } finally {
      state.importSummaryGenerating.value = false;
    }
  }

  async function openImportSummaryDraft(indexOrDraft) {
    const draft = typeof indexOrDraft === 'number'
      ? state.importSummaryDrafts.value[indexOrDraft]
      : indexOrDraft;
    if (!draft) return false;
    const skillFile = (draft.skillFile || '').toString().trim();
    try {
      const currentPath = normalizePathKey(state.sourcePath.value || state.activeSkillFilePath.value || '');
      if (skillFile && normalizePathKey(skillFile) !== currentPath) {
        await readers.readSkillFile(skillFile, draft.name || '');
      }
      state.form.scope = normalizeSkillScope(draft.scope);
      state.form.personal_type = personalTypeForScope(state.form.scope, draft.personalType || 'imported');
      rememberLoadedMainSkillTarget(state, state.form.scope, state.form.personal_type);
      state.summarySuggestion.value = '';
      state.isEditorOpen.value = true;
      state.isBodyEditing.value = false;
      return true;
    } catch (error) {
      logWarn('skills', 'upload.summary_open_failed', { error, path: skillFile });
      state.setNotice('error', '打开技能失败，请稍后重试。');
      return false;
    }
  }

  async function applyImportSummaryDraft(indexOrDraft) {
    const draft = typeof indexOrDraft === 'number'
      ? state.importSummaryDrafts.value[indexOrDraft]
      : indexOrDraft;
    if (!draft || draft.status !== 'ready') return;
    const suggestion = (draft.suggestion || '').toString().trim();
    if (!suggestion) return;
    const opened = await openImportSummaryDraft(draft);
    if (!opened) return;
    try {
      state.form.description = suggestion;
      state.summarySource.value = 'description';
      state.summarySuggestion.value = '';
      draft.status = 'applied';
      state.setNotice('success', '已采用简介建议，保存技能后生效。');
    } catch (error) {
      logWarn('skills', 'upload.summary_apply_failed', { error, path: skillFile });
      state.setNotice('error', `采用简介失败：${error?.message || error}`);
    }
  }

  function dismissImportSummaryDraft(indexOrDraft) {
    const index = typeof indexOrDraft === 'number'
      ? indexOrDraft
      : state.importSummaryDrafts.value.indexOf(indexOrDraft);
    if (index < 0) return;
    state.importSummaryDrafts.value.splice(index, 1);
  }

  function clearImportSummaryDrafts() {
    state.importSummaryDrafts.value = [];
  }

  function clearImportConflictDrafts() {
    state.importSummaryDrafts.value = state.importSummaryDrafts.value.filter((draft) => draft?.status !== 'conflict');
  }

  return {
    refreshImportSummaryDrafts,
    applyImportSummaryDraft,
    openImportSummaryDraft,
    dismissImportSummaryDraft,
    clearImportSummaryDrafts,
    clearImportConflictDrafts,
  };
}
