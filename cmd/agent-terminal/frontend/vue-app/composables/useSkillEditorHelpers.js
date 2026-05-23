import { logInfo } from '../services/log.js';
import { suggestSkillSummary } from '../services/skills-api.js';
import {
  isInternalSkillReferenceWord,
  isSkillMainFilePath,
  listToText,
  normalizePathKey,
  normalizeWordList,
} from '../utils/skill-parser.js';

export function updateNotice(notice, level, message) {
  notice.level = level || 'info';
  notice.message = (message || '').toString();
}

export function resolveSkillsCwd(props) {
  const activeProject = (props?.projectStore?.state?.active || '').toString().trim();
  return activeProject && activeProject !== '.' ? activeProject : '';
}

export function withSkillsCwd(props, payload = {}) {
  const cwd = resolveSkillsCwd(props);
  return cwd ? { ...payload, cwd } : payload;
}

export function isSummarySuggestUnavailableError(error) {
  const message = (error?.message || error || '').toString();
  return /dream executor is not configured|dreamexec|claude exited|codex exited|\[-\d+\]|exit status/i.test(message);
}

export function isSummarySuggestQualityError(error) {
  const message = (error?.message || error || '').toString();
  return /skill summary suggestion quality/i.test(message);
}

function isSummarySuggestRetryableError(error) {
  const message = (error?.message || error || '').toString();
  return /server overloaded|retry later|parse skill summary suggestion|skill summary suggestion is empty/i.test(message);
}

export async function requestSkillSummarySuggestion(cwd, payload) {
  let lastError = null;
  for (let attempt = 0; attempt < 2; attempt += 1) {
    try {
      const description = await suggestSkillSummary(cwd, payload);
      if (description) return description;
      lastError = new Error('skill summary suggestion is empty');
    } catch (error) {
      lastError = error;
      if (!isSummarySuggestRetryableError(error) || attempt > 0) {
        throw error;
      }
    }
  }
  throw lastError || new Error('skill summary suggestion is empty');
}

export function normalizeSkillScope(scope) {
  const normalized = (scope || '').toString().trim().toLowerCase();
  if (normalized === 'personal' || normalized === 'user' || normalized === 'signed' || normalized === 'system') {
    return 'personal';
  }
  return 'project';
}

export function scopeFromTrust(trust) {
  const normalized = (trust || '').toString().trim().toLowerCase();
  if (!normalized) return '';
  return normalized === 'project' ? 'project' : 'personal';
}

export function personalTypeFromSkill(item, fallback = '') {
  const direct = (item?.personal_type || item?.personalType || item?.type || '').toString().trim().toLowerCase();
  if (direct === 'user' || direct === 'agent' || direct === 'imported' || direct === 'hub') return direct;
  const trust = (item?.trust || fallback || '').toString().trim().toLowerCase();
  if (trust === 'imported' || trust === 'hub' || trust === 'agent') return trust;
  return 'user';
}

export function personalTypeForScope(scope, item = null, fallback = 'user') {
  return normalizeSkillScope(scope) === 'personal' ? personalTypeFromSkill(item, fallback) : '';
}

export function personalTypeFromForm(form, fallback = 'user') {
  return personalTypeForScope(form?.scope, { personal_type: form?.personal_type }, fallback);
}

function sameSkillTarget(scope, personalType, nextScope, nextPersonalType) {
  const currentScope = normalizeSkillScope(scope);
  const targetScope = normalizeSkillScope(nextScope);
  if (currentScope !== targetScope) return false;
  return personalTypeForScope(currentScope, { personal_type: personalType }, 'user') === personalTypeForScope(targetScope, { personal_type: nextPersonalType }, 'user');
}

function skillItemMainPath(item) {
  const dir = (item?.dir || '').toString().trim();
  return dir ? `${dir}/SKILL.md` : '';
}

export function isCurrentEditorTarget(state, item, scope, personalType) {
  const currentPath = normalizePathKey(state.sourcePath.value || state.activeSkillFilePath.value || '');
  const deletedPath = normalizePathKey(skillItemMainPath(item));
  if (currentPath && deletedPath) {
    return currentPath === deletedPath || currentPath.startsWith(`${normalizePathKey(item?.dir || '')}/`);
  }
  const currentName = (state.form.name || state.selectedSkillName.value || '').toString().trim().toLowerCase();
  const deletedName = (item?.name || '').toString().trim().toLowerCase();
  if (!currentName || currentName !== deletedName) return false;
  const currentScope = normalizeSkillScope(state.form.scope);
  const currentPersonalType = personalTypeFromForm(state.form);
  return currentScope === scope && currentPersonalType === personalType;
}

export function applyParsedSkillState(state, parsed, rawContent, path = '', fallbackSummary = '', fallbackSource = '') {
  const explicitSummary = parsed.summary || '';
  state.form.name = parsed.name || state.form.name || '';
  state.form.displayName = parsed.displayName || '';
  state.form.description = parsed.description || explicitSummary || '';
  state.form.summary = explicitSummary;
  state.generatedSummaryPreview.value = (!parsed.description && !explicitSummary && fallbackSource === 'generated')
    ? (fallbackSummary || '')
    : '';
  state.summarySuggestion.value = '';
  if (explicitSummary) {
    state.summarySource.value = 'frontmatter';
  } else if (fallbackSource) {
    state.summarySource.value = fallbackSource;
  } else if (fallbackSummary) {
    state.summarySource.value = 'generated';
  } else if (parsed.description) {
    state.summarySource.value = 'description';
  } else {
    state.summarySource.value = '';
  }
  const scenarioWords = normalizeWordList(`${listToText(parsed.triggerWords)},${listToText(parsed.forceWords)}`);
  const skillName = parsed.name || state.form.name || '';
  state.form.triggerWordsText = listToText(scenarioWords.filter((word) => !isInternalSkillReferenceWord(word, skillName)));
  state.form.forceWordsText = '';
  state.form.internalScenarioWordsText = listToText(scenarioWords.filter((word) => isInternalSkillReferenceWord(word, skillName)));
  state.form.body = (parsed.body || '').trim();
  state.sourcePath.value = path;
  state.activeSkillFilePath.value = path;
  state.selectedSkillName.value = state.form.name || state.selectedSkillName.value;
  logInfo('skills', 'editor.skill.loaded', {
    name: state.form.name,
    source_path: path,
    body_len: rawContent.length,
  });
}

export function resetLoadedMainSkillTarget(state) {
  state.loadedMainSkillScope.value = '';
  state.loadedMainSkillPersonalType.value = '';
}

export function rememberLoadedMainSkillTarget(state, scope, personalType) {
  state.loadedMainSkillScope.value = normalizeSkillScope(scope);
  state.loadedMainSkillPersonalType.value = personalTypeForScope(state.loadedMainSkillScope.value, { personal_type: personalType }, 'user');
}

export function mainSkillSavePath(state, fallbackName) {
  const path = (state.sourcePath.value || state.activeSkillFilePath.value || '').toString().trim();
  if (!path || !isSkillMainFilePath(path)) return fallbackName;
  if (!state.loadedMainSkillScope.value) return path;
  if (!sameSkillTarget(state.loadedMainSkillScope.value, state.loadedMainSkillPersonalType.value, state.form.scope, personalTypeFromForm(state.form))) {
    return fallbackName;
  }
  return path;
}
