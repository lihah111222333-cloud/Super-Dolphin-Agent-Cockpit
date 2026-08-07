import React, { useCallback, useEffect, useMemo, useState } from 'react'; import { useQuery, useQueryClient } from '@tanstack/react-query';
import { z } from 'zod';
import { Database, FileText, MousePointer2, RefreshCw, Search, Plus } from 'lucide-react';
import { FocusTrapDialog } from '../../shared/ui/FocusTrapDialog.jsx';
import { APP_COPY } from '../../shared/i18n/appI18n.js'; import { skillsPageService } from './services/skillsPageService.js';
import { cleanScalar, dashboardQueryKey, errorMessage, listToText, optionalSettingsCwd, textValue, withTimeout, wordListFromText } from '../shared/pageShared.js'; import { RetryableSyncError } from '../shared/pageComponents.jsx'; import './SkillsPage.css';
import { SkillMarkdownPreview } from './SkillMarkdownPreview.jsx';
import { resolveSkillPreviewFile, skillCitationFromLink, skillPreviewDir, stripLinkHash } from './SkillMarkdownPreviewModel.js';
import { SkillToolsState } from './SkillToolsTable.jsx';
import { MCPToolCard } from './MCPToolCard.jsx';
import { AddSkillToolDialog } from './AddSkillToolDialog.jsx';
import { useSkillToolRegistration } from './skillToolRegistrationModel.js';
import { DataSourceView } from './DataSourceView.jsx';
import { runBackgroundAction, runUIAction } from '../../shared/ui/runUIAction.js';
import { fetchSkillsDashboard, firstTextField, lowerTrimmedText, optionalArray, optionalObject, requiredText, scopeForSkill, SKILLS_DASHBOARD_TIMEOUT_MS, textFromValue, trimmedText } from './skillsDashboardModel.js';

const {
  applySkillResolution,
  createSkillTool,
  importSkillDirectories,
  listMCPServers,
  listSkillFiles,
  listSkillResolutions,
  listSkillTools,
  previewSkillResolution,
  readSkill,
  selectProjectDirs,
  startPlaywrightMCPServer,
  startSQLiteMCPServer,
  stopPlaywrightMCPServer,
  stopSQLiteMCPServer,
  suggestSkillSummary,
} = skillsPageService; function normalizeSettingsCwd(value) { const cwd = trimmedText(value); if (!cwd || cwd === '.' || cwd === '未选择项目') { throw new Error('settings: cwd is required'); } return cwd; }

const skillResolutionItemsSchema = z.array(z.unknown());
const skillResolutionResponseSchema = z.union([
  skillResolutionItemsSchema,
  z.object({ items: skillResolutionItemsSchema }).passthrough(),
  z.object({ conflicts: skillResolutionItemsSchema }).passthrough(),
]);
async function fetchSkillResolutionsDashboard(cwd) { const response = await withTimeout( listSkillResolutions({ cwd }), SKILLS_DASHBOARD_TIMEOUT_MS, '技能冲突检查超时，请检查技能目录或后端状态。', ); return normalizeResolutionResponse(response); }
function scopeLabel(scope) { return scope === 'personal' ? '私人使用' : '项目共享'; }
function normalizeSummarySuggestion(value) { if (value && typeof value === 'object' && !Array.isArray(value)) { return textValue(value.description); } return textValue(value); }
function parseWordsValue(value) { if (Array.isArray(value)) return wordListFromText(value); const raw = trimmedText(value); if (!raw) return []; return wordListFromText(raw.startsWith('[') && raw.endsWith(']') ? raw.slice(1, -1) : raw); }
function parseSkillMarkdown(content, fallbackName = '') { const text = textFromValue(content).replace(/\r\n/g, '\n'); if (!text.startsWith('---\n')) { return { name: fallbackName, displayName: '', description: '', triggerWords: [], body: text, };
} const rest = text.slice(4); const end = rest.indexOf('\n---'); if (end < 0) return { name: fallbackName, displayName: '', description: '', triggerWords: [], body: text }; const attrs = {}; for (const line of rest.slice(0, end).split('\n')) {
const idx = line.indexOf(':'); if (idx <= 0) continue; attrs[line.slice(0, idx).trim().toLowerCase().replace(/-/g, '_')] = line.slice(idx + 1).trim(); } return { name: cleanScalar(attrs.name) || fallbackName,
displayName: cleanScalar(attrs.display_name || attrs.displayname || attrs.title), description: cleanScalar(attrs.description || attrs.summary || attrs.digest), triggerWords: wordListFromText([
...parseWordsValue(attrs.trigger_words || attrs.triggerwords || attrs.keywords || attrs.tags), ...parseWordsValue(attrs.force_words || attrs.forcewords), ]), body: rest.slice(end + 4).replace(/^\n/, '').trim(), }; }
function quoteYAML(value) { return `"${textFromValue(value).replace(/"/g, '\\"')}"`; }
function skillNameFromDisplayName(value) { const text = trimmedText(value); let slug = ''; let lastDash = false; for (const char of Array.from(text)) { if (/[\p{L}\p{N}_-]/u.test(char)) { slug += char; lastDash = false; } else if (!lastDash) { slug += '-'; lastDash = true; }
} return slug.replace(/^-+|-+$/g, ''); } function buildSkillMarkdown(form) {
const name = trimmedText(form.name); const displayName = trimmedText(form.displayName); const description = trimmedText(form.description); const words = wordListFromText(form.keywords); const body = trimmedText(form.body); const lines = ['---', `name: ${quoteYAML(name)}`];
if (displayName) lines.push(`display_name: ${quoteYAML(displayName)}`); if (description) lines.push(`description: ${quoteYAML(description)}`); if (words.length > 0) lines.push(`trigger_words: [${words.map(quoteYAML).join(', ')}]`);
lines.push('---', '', body ? body : '## 说明\n\n请补充技能规则。'); return lines.join('\n'); }
function normalizeSkillCitationPath(path) { return stripLinkHash(path).replace(/\\/g, '/').replace(/\/+/g, '/').replace(/\/+$/g, '').toLowerCase(); } function compactSkillLookupText(value) { return Array.from(lowerTrimmedText(value)) .filter((char) => /[\p{L}\p{N}]/u.test(char)) .join(''); }
function skillFileForItem(skill) { const explicit = trimmedText(skill?.skillFile); if (explicit) return explicit; const dir = trimmedText(skill?.dir).replace(/[\\/]+$/g, ''); return dir ? `${dir}/SKILL.md` : ''; }
function skillMatchesCitationPath(skill, path) { const citationPath = normalizeSkillCitationPath(path); if (!citationPath) return false;
const skillFile = normalizeSkillCitationPath(skillFileForItem(skill)); const skillDir = normalizeSkillCitationPath(skill?.dir); return citationPath === skillFile || (skillDir && citationPath === `${skillDir}/skill.md`); }
function skillMatchesCitationName(skill, citation) { const needles = [citation.skillName, citation.raw, citation.skillId].map(compactSkillLookupText).filter(Boolean); if (needles.length === 0) return false;
const haystack = [skill?.name, skill?.title, skill?.displayName].map(compactSkillLookupText).filter(Boolean); return needles.some((needle) => haystack.includes(needle)); }
function findSkillForCitation(skills, citation) { const items = Array.isArray(skills) ? skills : []; if (citation.path) { const byPath = items.find((skill) => skillMatchesCitationPath(skill, citation.path)); if (byPath) return byPath;
} return items.find((skill) => skillMatchesCitationName(skill, citation)) || null; } function emptySkillForm() { return { name: '', displayName: '', description: '', keywords: '', body: '', scope: 'project', personalType: '', }; }
function normalizeSkillFileList(response) { if (!response || typeof response !== 'object' || !Array.isArray(response.files)) { throw new Error('skills/local/files response.files must be an array'); } const files = []; for (const file of response.files) { const normalized = {
name: requiredText(file?.name, 'name', 'skills/local/files item'), path: requiredText(file?.path, 'path', 'skills/local/files item'), isMain: Boolean(file?.is_main), }; files.push(normalized); } return files; }
function isMainSkillFile(path) { return /(^|[\\/])SKILL\.md$/i.test(trimmedText(path)); }
function normalizeResolutionResponse(response) {
  const result = skillResolutionResponseSchema.safeParse(response);
  if (!result.success) {
    if (!response || typeof response !== 'object') throw new Error('skill resolutions response must be an object');
    throw new Error('skill resolutions response items must be an array');
  }
  const parsed = result.data;
  if (Array.isArray(parsed)) return parsed;
  if (Array.isArray(parsed.items)) return parsed.items;
  return parsed.conflicts;
}
function resolutionKindLabel(kind) { return ({ mirror_drift: '外部版本有改动', unmanaged_provider_skill: '发现外部技能', unmanaged: '发现外部技能', same_name: '同名技能', same_name_scope_conflict: '同名技能', canonical_deleted_with_drift: '旧版本需要处理', external_personal_project_same_name: '外部 Provider 与项目同名',
}[lowerTrimmedText(kind)] || '需要处理'); } function resolutionActionLabel(action) { return ({ view_diff: '查看两个版本', view_unmanaged: '查看外部位置', sync_back_to_canonical: '用外部修改更新本项目', canonical_overwrite_mirror: '用本项目内容覆盖外部版本', save_as_new_skill: '另存为新技能', confirm_delete_drifted_mirror: '删除旧版本',
sync_back_to_personal: '继续私人使用', personal_overwrite_mirror: '用私人技能覆盖外部版本', save_as_new_personal_skill: '另存为新私人技能', import_to_personal_imported: '导入到私人使用', import_to_project: '导入到项目共享', takeover_provider_skill: '纳入管理', use_project_shared_skill: '使用项目共享版本，删除旧私人版本',
use_external_provider_skill: '继续私人使用，替换项目共享版本', replace_provider_root_symlink: '接管外部技能目录', rename_personal: '改名保存', keep_selected: '用选中的版本，删除其他版本', }[trimmedText(action)] || '处理'); }
function resolutionActionHelp(action) { const help = { view_diff: '只查看两个版本分别在哪里，不会修改文件。', view_unmanaged: '查看外部技能位置，不写入文件。', sync_back_to_canonical: '把外部修改同步回当前管理的技能。', canonical_overwrite_mirror: '用当前项目共享技能覆盖 Claude/Codex 中的外部版本。', save_as_new_skill: '保留两边内容，把外部版本保存成新的项目共享技能。',
confirm_delete_drifted_mirror: '删除 Claude/Codex 里保留的旧版本。', sync_back_to_personal: '恢复为私人使用，外部运行时会继续读取这个私人版本。', personal_overwrite_mirror: '用当前私人技能覆盖 Claude/Codex 中的外部版本。', save_as_new_personal_skill: '保留两边内容，把外部版本保存成新的私人技能。', import_to_personal_imported: '把外部技能导入到私人使用。',
import_to_project: '把外部技能导入到项目共享。', takeover_provider_skill: '把外部技能纳入当前技能管理。',
use_project_shared_skill: '使用项目共享版本，并删除 Claude/Codex 外部目录中的同名旧版本。',
use_external_provider_skill: '将 Claude/Codex 外部目录中的版本转为私人使用，并替换项目共享版本。',
replace_provider_root_symlink: '用当前技能根目录接管外部技能目录。', rename_personal: '把选中的版本改名保存，两个版本都会保留。',
keep_selected: '保留选中的版本，删除其他同名版本。', }[trimmedText(action)]; return textFromValue(help); }
function resolutionConflictGuide(conflict) { const kind = lowerTrimmedText(conflict?.kind); if (sameNameResolutionConflict(conflict)) { if (!sameNameHasProjectSource(conflict) && sameNamePersonalSources(conflict).length > 1) { return '发现多个同名的私人技能。请选择保留哪一版，其他同名版本会被删除；也可以改名保存。';
} return '发现多个同名技能。请选择保留哪一版，其他同名版本会被删除；也可以改名保存。'; } if (kind === 'external_personal_project_same_name') { return 'Claude/Codex 外部目录中的同名版本与项目共享版本发生冲突。请选择使用项目共享版本、将外部版本转为私人使用，或另存为新私人技能。'; } if (kind === 'unmanaged_provider_skill' || kind === 'unmanaged_same_name' || kind === 'unmanaged') {
return '外部应用里有一个还没纳入管理的技能。可以导入后统一管理，或只保留在外部应用里。'; } if (kind === 'canonical_deleted_with_drift') { if (lowerTrimmedText(conflict?.scope) === 'personal') { return '私人使用里的同名技能已经删除或改成项目共享，但 Claude/Codex 里还保留旧私人版本。请选择继续私人使用、另存为新私人技能，或删除旧私人版本。';
} return '本项目里的技能已不存在，但外部应用里还有改过的版本。请选择恢复、另存或删除外部版本。'; } if (kind === 'mirror_root_symlink') { return '外部应用的技能目录还是旧连接。接管后会改成由本项目管理的技能目录，并重新同步技能。'; } return '外部应用里的技能和本项目管理的技能不一致。请选择下面一种处理方式。'; }
function resolutionPreviewIntro(preview) { const action = trimmedText(preview?.action); if (isResolutionViewAction(action)) return '下面只说明两个版本分别在哪里，不会修改文件。'; return '请先确认将要写入的位置，确认应用后才会修改文件。'; }
function requiresResolutionNewName(action) { return ( action === 'save_as_new_skill' || action === 'save_as_new_personal_skill' || action === 'rename_personal' ); } function isResolutionViewAction(action) { return action === 'view_diff' || action === 'view_unmanaged'; }
function resolutionRequiresApply(action) { return !isResolutionViewAction(action); }
function defaultResolutionNewName(conflict, action) { const base = firstTextField(conflict, ['name', 'skill_name'], 'skill resolution conflict') || 'skill'; return `${base}${action === 'save_as_new_personal_skill' ? '-private' : '-copy'}`; }
const actionableResolutionActions = new Set([ 'view_diff', 'view_unmanaged', 'sync_back_to_canonical', 'canonical_overwrite_mirror', 'save_as_new_skill', 'confirm_delete_drifted_mirror', 'sync_back_to_personal', 'personal_overwrite_mirror', 'save_as_new_personal_skill',
'import_to_personal_imported', 'import_to_project', 'takeover_provider_skill', 'use_project_shared_skill', 'use_external_provider_skill', 'replace_provider_root_symlink', 'rename_personal', 'keep_selected', ]);
function resolutionActionUnsupported(action) { return !actionableResolutionActions.has(trimmedText(action)); } function resolutionSourceID(source) { return firstTextField(source, ['canonical_id', 'source_id'], 'skill resolution source'); }
function resolutionSourceScope(source) { return lowerTrimmedText(source?.scope); } function resolutionSourcePersonalType(source) { return lowerTrimmedText(source?.personal_type); }
function resolutionSourcePathLeaf(source) { const path = firstTextField(source, ['path', 'skill_file'], 'skill resolution source').replace(/\\/g, '/'); if (!path) return '';
const parts = path.split('/').filter(Boolean); const leaf = parts[parts.length - 1]; return leaf === 'SKILL.md' && parts.length > 1 ? textFromValue(parts[parts.length - 2]) : textFromValue(leaf); }
function sameNameResolutionConflict(conflict) { const kind = lowerTrimmedText(conflict?.kind); return kind === 'same_name' || kind === 'same_name_scope_conflict'; }
function sameNameProjectSources(conflict) { const sources = optionalArray(conflict?.sources); return sources.filter((source) => resolutionSourceScope(source) === 'project'); }
function sameNamePersonalSources(conflict) { const sources = optionalArray(conflict?.sources); return sources.filter((source) => resolutionSourceScope(source) === 'personal'); }
function sameNameHasProjectSource(conflict) { const sources = optionalArray(conflict?.sources); return sources.some((source) => resolutionSourceScope(source) === 'project'); }
function firstResolutionSourceID(conflict) { const sources = optionalArray(conflict?.sources); return resolutionSourceID(sources[0]); }
function sameNamePersonalVersionText(source, hasProjectSource = false) { const suffix = hasProjectSource ? '私人版本' : '版本'; const value = resolutionSourcePersonalType(source); return ({ user: `自己创建的${suffix}`, agent: `自动生成的${suffix}`, imported: `导入的${suffix}`,
hub: `市场下载的${suffix}`, }[value] || `私人${suffix}`); } function sameNameSourceShortText(source, includeSourceLeaf = false) { if (resolutionSourceScope(source) === 'project') {
const leaf = includeSourceLeaf ? resolutionSourcePathLeaf(source) || resolutionSourceID(source).replace(/^project\//, '') : ''; return leaf ? `项目共享版本：${leaf}` : '项目共享版本'; } return sameNamePersonalVersionText(source, true); }
function sameNameProjectVersionEntry(source, multipleProjectSources = false) { const leaf = multipleProjectSources ? resolutionSourcePathLeaf(source) || resolutionSourceID(source).replace(/^project\//, '') : ''; return { action: 'keep_selected',
label: leaf ? `用项目共享版本：${leaf}，删除其他版本` : '用项目共享版本，删除其他版本', help: '保留这个项目共享版本，删除其他同名版本。', source, sourceID: resolutionSourceID(source), }; }
function sameNameRenameEntry(source, includeSourceLeaf = false) { return { action: 'rename_personal', label: `改名保存${sameNameSourceShortText(source, includeSourceLeaf)}`, help: '把这个版本改成新名称，原来的同名冲突会保留为不同技能。', source, sourceID: resolutionSourceID(source), }; }
function personalDeletedDriftResolutionConflict(conflict) { return ( lowerTrimmedText(conflict?.kind) === 'canonical_deleted_with_drift' && lowerTrimmedText(conflict?.scope) === 'personal' ); }
function externalPersonalProjectResolutionConflict(conflict) { return lowerTrimmedText(conflict?.kind) === 'external_personal_project_same_name'; }
function resolutionConflictScopeLabel(conflict) { return externalPersonalProjectResolutionConflict(conflict) ? '外部 Provider 版本' : scopeLabel(scopeForSkill(conflict)); }
function resolutionProviderLabel(provider) { return ({ codex: 'Codex', claude: 'Claude', }[lowerTrimmedText(provider)] || trimmedText(provider)); }
function resolutionProviderEntryLabel(entry) { const label = trimmedText(entry?.display_label); if (label) return label; const group = []; for (const provider of optionalArray(entry?.provider_group)) {
const providerLabel = resolutionProviderLabel(provider); if (providerLabel) group.push(providerLabel); } if (group.length > 0) return group.join('、');
const provider = firstTextField(entry, ['provider', 'source_provider'], 'skill resolution provider entry'); return resolutionProviderLabel(provider) || '外部版本'; }
function resolutionProviderEntries(conflict) { const entries = optionalArray(conflict?.provider_entries); if (entries.length > 0) return entries; const provider = firstTextField(conflict, ['provider', 'source_provider'], 'skill resolution conflict'); if (!provider) return [{}];
return [{ provider, source_path_id: trimmedText(conflict?.source_path_id), }]; }
function resolutionActionEntries(conflict) { const actions = optionalArray(conflict?.available_actions) .filter((action) => !resolutionActionUnsupported(action)); if (personalDeletedDriftResolutionConflict(conflict)) { return actions.map((action) => ({ action, label: ({
sync_back_to_personal: '继续私人使用', confirm_delete_drifted_mirror: '使用项目共享版本，删除旧私人版本', }[action] || resolutionActionLabel(action)), help: resolutionActionHelp(action), })); } if (externalPersonalProjectResolutionConflict(conflict)) {
const allowed = externalPersonalProjectAllowedActions(); const entries = []; for (const action of actions) { if (!allowed.has(action)) continue; entries.push(externalPersonalProjectActionEntry(action)); } return entries; } if (!sameNameResolutionConflict(conflict)) {
return actions.map((action) => ({ action, help: resolutionActionHelp(action) }));
} const entries = []; const personalSources = sameNamePersonalSources(conflict); const projectSources = sameNameProjectSources(conflict); const hasProjectSource = sameNameHasProjectSource(conflict); if (actions.includes('keep_selected')) {
projectSources.forEach((source) => entries.push(sameNameProjectVersionEntry(source, projectSources.length > 1))); personalSources.forEach((source) => { const versionText = sameNamePersonalVersionText(source, hasProjectSource); entries.push({ action: 'keep_selected',
label: `用${versionText}，删除其他版本`, help: `保留这个${versionText}，删除其他同名版本。`, source, sourceID: resolutionSourceID(source), }); }); } if (actions.includes('rename_personal')) { [...projectSources, ...personalSources].forEach((source) => {
entries.push(sameNameRenameEntry(source, projectSources.length > 1)); }); } return entries.length > 0 ? entries : actions.map((action) => ({ action, help: resolutionActionHelp(action) })); }
function externalPersonalProjectActionEntry(action) { return { action, label: externalPersonalProjectActionLabel(action), help: resolutionActionHelp(action) }; }
function externalPersonalProjectAllowedActions() { return new Set(['view_diff', 'use_project_shared_skill', 'use_external_provider_skill', 'save_as_new_personal_skill']); }
function externalPersonalProjectActionLabel(action) { return { use_project_shared_skill: '使用项目共享版本，删除外部旧版本', use_external_provider_skill: '将外部版本转为私人使用，替换项目共享版本' }[action] || resolutionActionLabel(action); }
function resolutionActionEntryLabel(entry) { return entry?.label || resolutionActionLabel(entry?.action || entry); } function resolutionActionEntryHelp(entry) { return entry?.help || resolutionActionHelp(entry?.action || entry); }
function resolutionActionEntryTarget(actionEntry, providerEntry) { if (providerEntry?.merged_provider_entry && actionEntry?.action === 'view_unmanaged') { return { ...providerEntry, provider: '', source_path_id: '', sourcePathId: '', }; } return actionEntry?.source ? actionEntry : providerEntry; }
function resolutionSameNamePayloadFields(conflict, action, entry = null) { switch (action) { case 'rename_personal': case 'keep_selected': {
const sources = optionalArray(conflict?.sources); const selected = entry?.source || sources.find((source) => resolutionSourceScope(source) === 'personal') || sources.find((source) => resolutionSourceScope(source) === 'project');
const keepSourceID = resolutionSourceID(selected) || firstResolutionSourceID(conflict); return keepSourceID ? { keep_source_id: keepSourceID } : {}; } case 'merge_manually': { const mergeContentHash = trimmedText(conflict?.merge_content_hash); return {
keep_source_id: firstResolutionSourceID(conflict), merge_content_hash: mergeContentHash, }; } default: return {}; } } function resolutionActionAutoApplies(action) { return action === 'keep_selected'; }
function resolutionActionAutoAppliesForConflict(action, conflict) { if (resolutionActionAutoApplies(action)) return true; if (action === 'rename_personal') return true; if (externalPersonalProjectResolutionConflict(conflict)) { return (
action === 'use_project_shared_skill' || action === 'use_external_provider_skill' || action === 'save_as_new_personal_skill' ); } return false; } function resolutionApplyKey(conflict, action, entry = null) {
const source = firstTextField(entry, ['source_path_id', 'provider', 'sourceID'], 'skill resolution entry') || resolutionSourceID(entry?.source); return `${trimmedText(conflict?.conflict_id)}:${trimmedText(source)}:${trimmedText(action)}`; }
function previewItemPaths(item, action = '') { const normalizedAction = trimmedText(action || item?.action); const overwrite = normalizedAction === 'canonical_overwrite_mirror' || normalizedAction === 'personal_overwrite_mirror';
const importAction = normalizedAction === 'import_to_personal_imported' || normalizedAction === 'import_to_project' || normalizedAction === 'takeover_provider_skill'; const useProjectShared = normalizedAction === 'use_project_shared_skill';
const useExternal = normalizedAction === 'use_external_provider_skill'; const sourceLabel = overwrite || useProjectShared ? '本项目版本' : '外部版本'; let targetLabel = overwrite ? '外部版本' : '本项目版本'; if (importAction) targetLabel = '保存位置'; if (useProjectShared) targetLabel = '外部版本';
if (useExternal) targetLabel = '项目共享版本'; return [ [sourceLabel, item?.source_path], [targetLabel, item?.target_path], ].map(([label, value]) => ({ label, value: trimmedText(value) })) .filter((itemPath) => itemPath.value); }
function resolutionShortHash(value) { return trimmedText(value).slice(0, 8); } function resolutionManualSteps(conflict) {
const kind = lowerTrimmedText(conflict?.kind); const actions = optionalArray(conflict?.available_actions); if ((kind === 'same_name' || kind === 'same_name_scope_conflict') && !actions.includes('keep_selected') && !actions.includes('rename_personal')) { return [
'要保留项目共享：编辑或删除同名私人技能。', '要保留私人使用：编辑项目共享技能改名，或删除项目共享技能。', '两边都要保留：把其中一个改成更明确的名字。', ]; } return []; } function importedSkillFilePath(item) { return firstTextField(item, ['skill_file', 'path'], 'imported skill item'); }
function isImportedSkillSameNameConflictError(error) { const message = textFromValue(error?.message || error).toLowerCase(); return message.includes('skill same-name conflict') || message.includes('err_skill_same_name_conflict') || message.includes('skill path is not in effective skill set'); }
function importedSkillSameNameConflictMessage(draft) { return draft.scope === 'personal' ? '已导入，但和项目共享技能同名，暂未启用。请在冲突提示中选择使用哪个版本。' : '已导入，但与现有技能同名，暂未启用。请在冲突提示中选择使用哪个版本。'; }
function skillFileBaseName(path) { const clean = trimmedText(path).replace(/[\\/]+SKILL\.md$/i, '').replace(/\\/g, '/'); const parts = clean.split('/').filter(Boolean); return textFromValue(parts[parts.length - 1]); }
function fileNameFromPath(path) { const clean = trimmedText(path).replace(/[\\/]+$/g, '').replace(/\\/g, '/'); const parts = clean.split('/').filter(Boolean); return textFromValue(parts[parts.length - 1]); }
function normalizeImportSummaryDraftScope(scope) { return scope === 'personal' ? 'personal' : 'project'; } function importSummaryDraftStatusCount(drafts, status) { return drafts.filter((draft) => draft.status === status).length; } function importSummaryPanelTitle(drafts) {
const conflictCount = importSummaryDraftStatusCount(drafts, 'conflict'); const errorCount = importSummaryDraftStatusCount(drafts, 'error'); const readyCount = drafts.filter((draft) => draft.status === 'ready' || draft.status === 'applied').length;
if (conflictCount > 0 && readyCount === 0) return '导入后需要处理'; if (conflictCount > 0) return '导入后的简介建议和同名处理'; if (errorCount > 0 && readyCount === 0) return '导入后可补充简介'; return '导入后的简介建议'; } function importSummaryPanelHint(drafts) {
const conflictCount = importSummaryDraftStatusCount(drafts, 'conflict'); const errorCount = importSummaryDraftStatusCount(drafts, 'error'); const readyCount = drafts.filter((draft) => draft.status === 'ready' || draft.status === 'applied').length;
if (conflictCount > 0 && readyCount === 0) return '同名技能需要先选择使用哪个版本。'; if (conflictCount > 0) return '简介建议采用并保存后生效；同名技能需要选择使用哪个版本。'; if (errorCount > 0 && readyCount === 0) return '技能已正常导入，可以稍后手动补充简介。'; return '还没有写入技能，采用并保存后生效。'; }
function importSummaryDraftMessage(drafts) { const readyCount = importSummaryDraftStatusCount(drafts, 'ready'); const conflictCount = importSummaryDraftStatusCount(drafts, 'conflict'); const errorCount = importSummaryDraftStatusCount(drafts, 'error'); const parts = [];
if (readyCount > 0) parts.push(`已生成 ${readyCount} 条简介建议，采用后再保存。`); if (conflictCount > 0) parts.push(`${conflictCount} 个同名技能待处理。`); if (errorCount > 0) parts.push(`${errorCount} 个技能可手动补充简介。`); return parts.join('，'); }
function duplicateImportFailureMessage(message) { const raw = trimmedText(message); const existsMatch = raw.match(/^skill already exists:\s*(.+)$/i); if (existsMatch) return `${trimmedText(existsMatch[1]) || '该技能'} 已存在，未重复导入。`;
if (/^source is inside skills root:/i.test(raw)) return '这个目录已经在技能管理中，未重复导入。'; return ''; }
function normalizeImportFailure(item) { const source = trimmedText(item?.source); const rawMessage = trimmedText(item?.error) || '未知错误'; const duplicateMessage = duplicateImportFailureMessage(rawMessage); return { duplicate: Boolean(duplicateMessage),
duplicateName: trimmedText(rawMessage.match(/^skill already exists:\s*(.+)$/i)?.[1]), message: duplicateMessage || rawMessage, source, }; } function summarizeImportFailureNames(names) { if (names.length <= 3) return names.join('、'); return `${names.slice(0, 3).join('、')} 等 ${names.length} 个`; }
function duplicateImportNotice(scope, duplicateFailures) { const names = []; for (const item of duplicateFailures) { if (item.duplicateName) names.push(item.duplicateName);
} const prefix = normalizeImportSummaryDraftScope(scope) === 'personal' ? '私人使用里已存在' : '项目共享里已存在'; return names.length > 0 ? `${prefix}：${summarizeImportFailureNames(names)}，未重复导入。` : `${prefix} ${duplicateFailures.length} 个技能，未重复导入。`; }
function importNotice(importedCount, drafts, failures, scope) {
const parts = []; if (importedCount > 0) parts.push(`已导入 ${importedCount} 个技能目录`); const draftMessage = importSummaryDraftMessage(drafts); if (draftMessage) parts.push(draftMessage); const duplicateFailures = failures.filter((failure) => failure.duplicate);
if (duplicateFailures.length > 0) parts.push(duplicateImportNotice(scope, duplicateFailures)); const otherFailures = failures.filter((failure) => !failure.duplicate);
if (otherFailures.length > 0) parts.push(`${otherFailures.length} 个目录导入失败：${otherFailures[0].source || otherFailures[0].message}`); return parts.length > 0 ? parts.join('，') : '未导入任何技能目录'; }
const SKILL_TOOLS_LIST_LIMIT = 200;
const SKILL_TOOLS_UI = Object.freeze({ actions: '\u64cd\u4f5c', create: '\u65b0\u589e\u5de5\u5177', description: '\u63cf\u8ff0', disabled: '\u5df2\u5173\u95ed', enabled: '\u5df2\u542f\u7528', empty: '\u6682\u65e0 Skill \u5de5\u5177',
errorPrefix: '\u8bfb\u53d6 Skill \u5de5\u5177\u5931\u8d25\uff1a', loading: '\u8bfb\u53d6\u4e2d...', methodName: '\u65b9\u6cd5\u540d', refresh: '\u5237\u65b0', sectionTitle: '\u63d2\u4ef6\u4e0e\u6280\u80fd', status: '\u72b6\u6001', title: 'Skill\u5de5\u5177',
waitingProject: '\u6b63\u5728\u8fde\u63a5\u672c\u5730\u9879\u76ee...', });
 function SkillsPage({ copy = APP_COPY.zh.skills, projectPath, refreshKey = 0, resolveLaunchPreferences }) { const [subTab, setSubTab] = useState('plugins'); return (
<div className="skills-tabbed-container"> <div className="skills-subtabs-header"> <button type="button" className={subTab === 'plugins' ? 'active' : ''} onClick={() => setSubTab('plugins')} > {copy.tabs.plugins} </button> <button type="button"
className={subTab === 'library' ? 'active' : ''} onClick={() => setSubTab('library')} > {copy.tabs.library} </button> <button type="button" className={subTab === 'datasource' ? 'active' : ''} onClick={() => setSubTab('datasource')} > {copy.tabs.datasource} </button>
</div> <div className="skills-tab-content"> {subTab === 'plugins' ? ( <PluginsSquareView copy={copy} projectPath={projectPath} /> ) : subTab === 'datasource' ? ( <DataSourceView copy={copy} /> ) : subTab === 'skills' ? (
<SkillToolsView projectPath={projectPath} /> ) : (
<SkillsLibraryTab copy={copy} projectPath={projectPath} refreshKey={refreshKey} resolveLaunchPreferences={resolveLaunchPreferences} /> )} </div> </div> ); }
function SkillsLibraryTab({ copy, projectPath, refreshKey, resolveLaunchPreferences }) { const model = useSkillsPageModel({ projectPath, refreshKey, resolveLaunchPreferences }); return <SkillsPageView copy={copy} model={model} />; } function skillToolsQueryKey(cwd) { return ['skillTools', cwd]; }
function normalizeSkillTool(raw, index = 0) { if (!raw || typeof raw !== 'object' || Array.isArray(raw)) { throw new Error(`skill tool ${index} must be an object`); } const id = Number(raw.id); if (!Number.isInteger(id) || id <= 0) {
throw new Error(`skill tool ${index} is missing id`); } const methodName = cleanScalar(raw.methodName ?? raw.method_name ?? raw.name); if (!methodName) { throw new Error(`skill tool ${index} is missing methodName`); } return { id, methodName,
name: cleanScalar(raw.name) || methodName, description: cleanScalar(raw.description), command: cleanScalar(raw.command), args: Array.isArray(raw.args) ? raw.args.flatMap((arg) => { const value = cleanScalar(arg); return value ? [value] : []; }) : [], enabled: raw.enabled !== false, }; }
function normalizeSkillToolsResponse(response) { if (!response || typeof response !== 'object' || Array.isArray(response)) { throw new Error('skills/tools/list response must be an object'); } if (!Array.isArray(response.tools)) {
throw new Error('skills/tools/list response.tools must be an array'); } return response.tools.map(normalizeSkillTool); }
function SkillToolsView({ projectPath }) { const queryClient = useQueryClient(); const cwd = useMemo(() => { try { return normalizeSettingsCwd(projectPath); } catch { return ''; } }, [projectPath]); const { data: tools = [], error, isError, isFetching, isLoading, } = useQuery({
queryKey: skillToolsQueryKey(cwd), enabled: Boolean(cwd), queryFn: () => runBackgroundAction('skill.tools.load', async () => normalizeSkillToolsResponse(await listSkillTools({ cwd, keyword: '', limit: SKILL_TOOLS_LIST_LIMIT }))), }); const refreshTools = () => {
if (cwd) void queryClient.invalidateQueries({ queryKey: skillToolsQueryKey(cwd) }); };
return ( <section className="skill-tools-panel" aria-label={SKILL_TOOLS_UI.title}> <div className="skill-tools-header"> <div> <span className="skill-tools-kicker">{SKILL_TOOLS_UI.sectionTitle}</span> <h2>{SKILL_TOOLS_UI.title}</h2> </div> <div className="skill-tools-actions">
<button type="button">{SKILL_TOOLS_UI.create}</button> <button type="button" className="ghost" onClick={refreshTools} disabled={!cwd || isFetching}> <RefreshCw size={16} aria-hidden="true" /> <span>{SKILL_TOOLS_UI.refresh}</span> </button> </div> </div>
{!cwd ? <p className="skill-tools-notice">{SKILL_TOOLS_UI.waitingProject}</p> : null} {cwd && isLoading ? <p className="skill-tools-notice">{SKILL_TOOLS_UI.loading}</p> : null}
<SkillToolsState cwd={cwd} error={error} errorMessage={errorMessage} isError={isError} isLoading={isLoading} tools={tools} /> </section> ); }


function mcpServersListQueryKey(projectPath) { return ['mcpServer', 'list', optionalSettingsCwd(projectPath) || 'pending']; }
const MCP_TOOL_DEFINITIONS = [ { id: 'sqlite', title: 'SQLite MCP', description: '使用 @bytebase/dbhub 暴露本地 Super-Dolphin SQLite 数据库。', Icon: Database, testId: 'sqlite-mcp-status', start: startSQLiteMCPServer, stop: stopSQLiteMCPServer, }, { id: 'playwright',
title: 'Playwright MCP', description: '使用 @playwright/mcp@latest 提供浏览器自动化 MCP 工具。', Icon: MousePointer2, testId: 'playwright-mcp-status', start: startPlaywrightMCPServer, stop: stopPlaywrightMCPServer, }, ];
function mcpServerMap(response) { if (!response || typeof response !== 'object' || Array.isArray(response)) { throw new Error('mcp/list response must be an object'); } if (!response.mcpServers || typeof response.mcpServers !== 'object' || Array.isArray(response.mcpServers)) {
throw new Error('mcp/list response.mcpServers must be an object'); } return response.mcpServers; }
function mcpServerConfig(response, serverName) { const servers = mcpServerMap(response); const config = servers[serverName]; return config && typeof config === 'object' && !Array.isArray(config) ? config : null; }
function mcpServerStatus(projectReady, query, serverName) { if (!projectReady) return { label: '未选择项目', tone: 'missing' }; if (query.isLoading || (query.isFetching && !query.data)) return { label: '读取中', tone: 'loading' };
if (query.isError) return { label: '读取失败', tone: 'error' }; const config = mcpServerConfig(query.data, serverName); if (!config) return { label: '未配置', tone: 'missing' }; return config.enabled === false ? { label: '已关闭', tone: 'disabled' } : { label: '已开启', tone: 'enabled' }; }
function mergeMCPServerEnabled(response, result, serverName, enabled) { const current = optionalObject(response); if (!current) throw new Error('mcp/list cached response must be an object');
const servers = mcpServerMap(current); const resultName = trimmedText(result?.serverName || serverName); if (!resultName) return current;
const existingConfig = servers[resultName]; const existing = existingConfig && typeof existingConfig === 'object' && !Array.isArray(existingConfig) ? existingConfig : {}; const nextConfig = { ...existing, enabled, }; return { ...current, mcpServers: { ...servers, [resultName]: nextConfig, }, }; }
function PluginsSquareView({ copy, projectPath }) {
  const projectReady = Boolean(optionalSettingsCwd(projectPath));
  const queryClient = useQueryClient();
  const [mcpActions, setMCPActions] = useState({});
  const [mcpNotices, setMCPNotices] = useState({});
  const [mcpErrors, setMCPErrors] = useState({});
  const [panelNotice, setPanelNotice] = useState('');
  const toolRegistration = useSkillToolRegistration({ createTool: createSkillTool, listTools: listSkillTools, projectPath, queryClient, setPanelNotice });
  const {
    data: mcpServersData,
    error: mcpServersError,
    isError: mcpServersIsError,
    isFetching: mcpServersIsFetching,
    isLoading: mcpServersIsLoading,
  } = useQuery({
    queryKey: mcpServersListQueryKey(projectPath),
    queryFn: () => runBackgroundAction('mcp.servers.load', () => listMCPServers()),
    enabled: projectReady,
  });
  const mcpStatusQuery = useMemo(() => ({
    data: mcpServersData,
    isError: mcpServersIsError,
    isFetching: mcpServersIsFetching,
    isLoading: mcpServersIsLoading,
  }), [mcpServersData, mcpServersIsError, mcpServersIsFetching, mcpServersIsLoading]);

  const executeMCPAction = useCallback(async (tool, action) => {
    const label = action === 'start' ? '开启' : '关闭';
    const enabled = action === 'start';
    setMCPActions((current) => ({ ...current, [tool.id]: action }));
    setMCPNotices((current) => ({ ...current, [tool.id]: '' }));
    setMCPErrors((current) => ({ ...current, [tool.id]: '' }));
    try {
      normalizeSettingsCwd(projectPath);
      const queryKey = mcpServersListQueryKey(projectPath);
      const result = action === 'start' ? await tool.start() : await tool.stop();
      queryClient.setQueryData(queryKey, (current) => mergeMCPServerEnabled(current, result, tool.id, enabled));
      setMCPNotices((current) => ({ ...current, [tool.id]: `${tool.title} 已${label}` }));
    } catch (error) {
      setMCPErrors((current) => ({ ...current, [tool.id]: `${tool.title} ${label}失败，请重试。` }));
      throw error;
    } finally {
      setMCPActions((current) => ({ ...current, [tool.id]: '' }));
    }
  }, [projectPath, queryClient]);
  const runMCPAction = useCallback((tool, action) => {
    if (tool.id === 'sqlite' && action === 'start') return runUIAction('mcp.sqlite.start', () => executeMCPAction(tool, action));
    if (tool.id === 'sqlite' && action === 'stop') return runUIAction('mcp.sqlite.stop', () => executeMCPAction(tool, action));
    if (tool.id === 'playwright' && action === 'start') return runUIAction('mcp.playwright.start', () => executeMCPAction(tool, action));
    if (tool.id === 'playwright' && action === 'stop') return runUIAction('mcp.playwright.stop', () => executeMCPAction(tool, action));
    throw new Error(`unsupported MCP action: ${tool.id}.${action}`);
  }, [executeMCPAction]);

  // 未选择项目时，MCP 开关与注册技能工具入口不静默失效：给出明确的项目引导提示。
  const requireProjectNotice = useCallback((actionLabel) => {
    setPanelNotice(`请先在聊天页选择项目，再${actionLabel}。`);
  }, []);
  const handleAddNewSkill = useCallback(() => {
    if (!projectReady) {
      setPanelNotice(copy.registerTool.projectRequired);
      return;
    }
    setPanelNotice('');
    toolRegistration.openEditor();
  }, [projectReady, copy.registerTool.projectRequired, toolRegistration]);

  return (
    <div className="plugins-square-container">
      <div className="plugins-square-header">
        <h1>{copy.pluginsTitle}</h1>
        <p className="plugins-square-subtitle">{copy.pluginsSubtitle}</p>
      </div>

      {panelNotice ? <p className="plugins-square-panel-notice" role="status">{panelNotice}</p> : null}

      <div className="mcp-tool-panel">
        {MCP_TOOL_DEFINITIONS.map((tool) => (
          <MCPToolCard
            errorMessage={errorMessage}
            key={tool.id}
            mcpServerStatus={mcpServerStatus}
            tool={tool}
            state={mcpToolState({
              mcpActions,
              mcpErrors,
              mcpNotices,
              mcpServersError,
              mcpServersIsError,
              mcpStatusQuery,
              onProjectRequired: () => requireProjectNotice(`管理 ${tool.title}`),
              projectReady,
              runMCPAction,
            })}
          />
        ))}

        {/* 注册技能工具卡片：打开“注册技能工具”对话框（从现有技能选择并注册，skills/tools/create） */}
        <button type="button" className="mcp-tool-card add-new-card" aria-label={copy.registerTool.card} onClick={handleAddNewSkill}>
          <span className="mcp-tool-icon add-new" aria-hidden="true">
            <Plus size={20} />
          </span>
          <span className="mcp-tool-main">
            <span className="mcp-tool-title-line">
              <span className="add-new-title">{copy.registerTool.card}</span>
            </span>
            <span className="mcp-tool-notice">{copy.registerTool.cardHint}</span>
          </span>
        </button>
      </div>

      {toolRegistration.toolEditorOpen ? (
        <AddSkillToolDialog
          availableSkills={toolRegistration.availableSkills}
          copy={copy.registerTool}
          description={toolRegistration.description}
          enabled={toolRegistration.enabled}
          loadState={toolRegistration.loadState}
          onChangeDescription={toolRegistration.setDescription}
          onChangeEnabled={toolRegistration.setEnabled}
          onClose={toolRegistration.closeEditor}
          onSave={() => runUIAction('skill.tool.register', toolRegistration.saveTool)}
          onSelectSkill={toolRegistration.selectSkill}
          registeredCount={toolRegistration.registeredCount}
          saveError={toolRegistration.saveError}
          saving={toolRegistration.toolSaving}
          selection={toolRegistration.selection}
        />
      ) : null}
    </div>
  );
}
function mcpToolState(state) { return state; }
function useSkillsPageModel({ projectPath, refreshKey, resolveLaunchPreferences }) {
const projectCwd = optionalSettingsCwd(projectPath); const [query, setQuery] = useState(''); const [scopeFilter, setScopeFilter] = useState('all'); const [status, setStatus] = useState({ projectCwd, error: '', notice: '' }); if (status.projectCwd !== projectCwd) {
setStatus({ projectCwd, error: '', notice: '' }); } const setError = useCallback((value) => { setStatus((current) => ({ ...current, error: typeof value === 'function' ? value(current.error) : value })); }, []); const setNotice = useCallback((value) => {
setStatus((current) => ({ ...current, notice: typeof value === 'function' ? value(current.notice) : value })); }, []); const dashboard = useSkillsDashboard(projectCwd, refreshKey); const filters = useSkillsFilters(dashboard.items, query, scopeFilter);
const editor = useSkillEditor({ projectPath, refreshSkillSurface: dashboard.refreshSkillSurface, resolveLaunchPreferences, setError, setNotice, skills: dashboard.items });
const resolution = useSkillResolution({ projectPath, refreshSkillSurface: dashboard.refreshSkillSurface, resetKey: projectCwd, resolutionConflicts: dashboard.resolutionConflicts, setError, setNotice }); const error = status.projectCwd === projectCwd ? status.error : '';
const notice = status.projectCwd === projectCwd ? status.notice : ''; return { dashboard, editor, error, filters, isProjectPending: !projectCwd, notice, query, resolution, scopeFilter, setQuery, setScopeFilter }; }
function useSkillsDashboard(projectCwd, refreshKey) { const queryClient = useQueryClient(); const skillRefreshKey = Number(refreshKey || 0); const { data: skillsData, error: skillsError, isPending: skillsPending, refetch: refetchSkills, } = useQuery({
queryKey: dashboardQueryKey(projectCwd, 'skills', `revision:${skillRefreshKey}`),
queryFn: () => runBackgroundAction('skill.dashboard.load', async () => {
const data = await fetchSkillsDashboard(projectCwd);
queryClient.setQueryData(dashboardQueryKey(projectCwd, 'skills'), data);
return data;
}), enabled: Boolean(projectCwd),
initialData: () => queryClient.getQueryData(dashboardQueryKey(projectCwd, 'skills')), initialDataUpdatedAt: 0, placeholderData: (previousData) => previousData, }); const { data: resolutionsData, error: resolutionsError, isPending: resolutionsPending, refetch: refetchResolutions,
} = useQuery({ queryKey: dashboardQueryKey(projectCwd, 'skill-resolutions', `revision:${skillRefreshKey}`), queryFn: () => runBackgroundAction('skill.resolutions.load', async () => {
const data = await fetchSkillResolutionsDashboard(projectCwd); queryClient.setQueryData(dashboardQueryKey(projectCwd, 'skill-resolutions'), data); return data; }), enabled: Boolean(projectCwd),
initialData: () => queryClient.getQueryData(dashboardQueryKey(projectCwd, 'skill-resolutions')), initialDataUpdatedAt: 0, placeholderData: (previousData) => previousData,
}); const skillsQuery = { data: skillsData, error: skillsError, isPending: skillsPending }; const resolutionsQuery = { data: resolutionsData, error: resolutionsError, isPending: resolutionsPending };
const items = useMemo(() => (Array.isArray(skillsData) ? skillsData : []), [skillsData]); const resolutionConflicts = useMemo(() => (Array.isArray(resolutionsData) ? resolutionsData : []), [resolutionsData]); const refreshSkillSurface = useCallback(async () => {
if (!projectCwd) return; await Promise.all([ queryClient.invalidateQueries({ queryKey: dashboardQueryKey(projectCwd, 'skills') }), queryClient.invalidateQueries({ queryKey: dashboardQueryKey(projectCwd, 'skill-resolutions') }), ]);
}, [projectCwd, queryClient]); const retrySkillSurface = useCallback(async () => { if (!projectCwd) return; await Promise.all([refetchSkills(), refetchResolutions()]);
}, [projectCwd, refetchResolutions, refetchSkills]); useSkillSurfaceRefresh(projectCwd, refreshSkillSurface); return skillsDashboardState({ items, projectCwd, resolutionConflicts, resolutionsQuery, retrySkillSurface, refreshSkillSurface, skillsQuery }); } function skillsDashboardState(options) {
const { items, projectCwd, resolutionConflicts, resolutionsQuery, retrySkillSurface, refreshSkillSurface, skillsQuery } = options; const hasSnapshot = Array.isArray(skillsQuery.data); const hasResolutionSnapshot = Array.isArray(resolutionsQuery.data);
const resolutionSyncErrorText = resolutionsQuery.error ? '读取技能冲突失败，请重试。' : ''; const syncErrorText = skillsSyncErrorText(skillsQuery, resolutionsQuery); return { items,
isInitialLoading: Boolean(projectCwd) && skillsQuery.isPending && !hasSnapshot && !syncErrorText, isResolutionPending: Boolean(projectCwd) && resolutionsQuery.isPending && !hasResolutionSnapshot && !resolutionSyncErrorText, refreshSkillSurface, resolutionConflicts,
resolutionSyncErrorText, retrySkillSurface, showBlockingSyncError: Boolean(syncErrorText && !hasSnapshot), showCachedSyncError: Boolean(syncErrorText && hasSnapshot), syncErrorText, }; }
function skillsSyncErrorText(skillsQuery, resolutionsQuery) { if (skillsQuery.error) return '读取技能失败，请重试。'; if (resolutionsQuery.error) return '读取技能冲突失败，请重试。'; return ''; }
function useSkillSurfaceRefresh(projectCwd, refreshSkillSurface) { useEffect(() => skillSurfaceFocusHandler(projectCwd, refreshSkillSurface), [projectCwd, refreshSkillSurface]); }
function skillSurfaceFocusHandler(projectCwd, refreshSkillSurface) { if (!projectCwd) return undefined; const refreshWhenVisible = () => { if (typeof document !== 'undefined' && document.visibilityState === 'hidden') return; void refreshSkillSurface();
}; const handleVisibilityChange = () => { if (typeof document === 'undefined' || document.visibilityState === 'visible') refreshWhenVisible();
}; window.addEventListener('focus', refreshWhenVisible); document.addEventListener('visibilitychange', handleVisibilityChange); return () => { window.removeEventListener('focus', refreshWhenVisible); document.removeEventListener('visibilitychange', handleVisibilityChange); }; }
function useSkillsFilters(items, query, scopeFilter) { const counts = useMemo(() => skillCounts(items), [items]); const filteredItems = useMemo(() => filterSkills(items, query, scopeFilter), [items, query, scopeFilter]);
const scopeOptions = useMemo(() => ([['personal', '私人使用 ' + counts.personal], ['project', '项目共享 ' + counts.project], ['all', '全部 ' + counts.all]]), [counts]); const countText = skillCountText({ counts, filteredCount: filteredItems.length, query, scopeFilter });
return { counts, countText, filteredItems, scopeOptions }; }
function skillCountText({ counts, filteredCount, query, scopeFilter }) { if (counts.all === 0) return ''; if (scopeFilter === 'all' && !query.trim()) return `共 ${counts.all} 个技能`; if (filteredCount === 0) return `当前没有匹配技能，共 ${counts.all} 个`; return `显示 ${filteredCount} 个，共 ${counts.all} 个技能`; }
function skillCounts(items) { return items.reduce((acc, item) => { acc.all += 1; if (item.scope === 'personal') acc.personal += 1; else acc.project += 1; return acc; }, { all: 0, personal: 0, project: 0 }); }
function filterSkills(items, query, scopeFilter) { const keyword = query.trim().toLowerCase(); return items.filter((item) => skillMatchesFilter(item, keyword, scopeFilter)); }
function skillMatchesFilter(item, keyword, scopeFilter) { if (scopeFilter !== 'all' && item.scope !== scopeFilter) return false; if (!keyword) return true; return [item.name, item.title, item.description, item.summary, item.dir, ...item.tags].join(' ').toLowerCase().includes(keyword); }
function useSkillEditor(options) {
const { projectPath, refreshSkillSurface, resolveLaunchPreferences, setError, setNotice, skills } = options; const [state, setState] = useState(defaultSkillEditorState); const setPatch = useCallback((patch) => setState((current) => ({ ...current, ...patch })), []);
const setForm = useCallback((updater) => setState((current) => ({ ...current, editorForm: typeof updater === 'function' ? updater(current.editorForm) : updater })), []);
const actions = useMemo(
() => skillEditorActions({ facade: skillsPageService, projectPath, refreshSkillSurface, resolveLaunchPreferences, setError, setForm, setNotice, setPatch, skills, state }),
[projectPath, refreshSkillSurface, resolveLaunchPreferences, setError, setForm, setNotice, setPatch, skills, state],
);
return { ...state, ...actions, setForm }; } function defaultSkillEditorState() {
return { activeSkillPath: '', deleteTarget: null, deleting: false, editorForm: emptySkillForm(), editorOpen: false, importScopeOpen: false, importSummaryDrafts: [], importing: false, saving: false, skillFiles: [], summarySuggestion: '', summarySuggesting: false }; }
function skillEditorActions(ctx) { return { applySummary: () => { ctx.setForm((form) => ({ ...form, description: ctx.state.summarySuggestion })); ctx.setPatch({ summarySuggestion: '' }); }, closeDelete: () => ctx.setPatch({ deleteTarget: null }),
closeEditor: () => ctx.setPatch({ editorOpen: false }), closeImportScope: () => ctx.setPatch({ importScopeOpen: false }), clearImportSummaryDrafts: () => ctx.setPatch({ importSummaryDrafts: [] }), confirmDeleteSkill: () => runUIAction('skill.delete', () => confirmDeleteSkill(ctx)),
confirmImportScope: (scope) => runUIAction('skill.import', () => confirmImportScope(ctx, scope)), applyImportSummaryDraft: (draft) => applyImportSummaryDraft(ctx, draft), dismissImportSummaryDraft: (draft) => dismissImportSummaryDraft(ctx, draft),
openImportSummaryDraft: (draft) => runUIAction('skill.import-summary.open', () => openImportSummaryDraft(ctx, draft), { retryable: true }),
onDeleteSkill: (skill) => ctx.setPatch({ deleteTarget: skill }), openCreateEditor: () => openCreateSkillEditor(ctx),
openEditSkill: (skill) => runUIAction('skill.open', () => openEditSkill(ctx, skill), { retryable: true }),
openImportScope: () => ctx.setPatch({ importScopeOpen: true }),
openSkillFile: (file) => runUIAction('skill.file.open', () => openSkillFile(ctx, file), { retryable: true }),
openSkillCitation: (target, label) => openSkillCitation(ctx, target, label),
saveEditor: () => runUIAction('skill.save', () => saveSkillEditor(ctx)),
suggestSummary: () => runUIAction('skill.summary.suggest', () => suggestSkillSummaryForEditor(ctx), { retryable: true }), }; }
function openCreateSkillEditor(ctx) { ctx.setPatch({ activeSkillPath: '', editorForm: emptySkillForm(), editorOpen: true, skillFiles: [], summarySuggestion: '' }); ctx.setError(''); ctx.setNotice(''); }
async function openEditSkill(ctx, skill) { const skillPath = skillFileForItem(skill); const skillDir = (skill?.dir || skillPreviewDir(skillPath)).toString().trim(); if (!skillPath || !skillDir) { ctx.setError('skills/local/read: path is required'); return; }
ctx.setError(''); ctx.setNotice(''); ctx.setPatch({ summarySuggestion: '' }); try { const cwd = normalizeSettingsCwd(ctx.projectPath); const [rawSkill, rawFiles] = await Promise.all([readSkill({ cwd, path: skillPath }), listSkillFiles({ cwd, dir: skillDir })]);
ctx.setPatch({ activeSkillPath: skillPath, editorForm: skillFormFromRaw(rawSkill, skill), editorOpen: true, skillFiles: normalizeSkillFileList(rawFiles) }); } catch (err) { ctx.setError('读取技能失败，请重试。'); throw err; } }
function skillFormFromRaw(rawSkill, skill) { const raw = optionalObject(rawSkill?.skill); if (!raw) throw new Error('skills/local/read response.skill must be an object'); const content = textFromValue(raw.content); const parsed = parseSkillMarkdown(content, skill.name); return {
name: parsed.name ? parsed.name : skill.name, displayName: parsed.displayName ? parsed.displayName : textFromValue(skill.title), description: parsed.description ? parsed.description : textFromValue(skill.description),
keywords: listToText(parsed.triggerWords.length > 0 ? parsed.triggerWords : skill.tags), body: parsed.body, scope: skill.scope, personalType: skill.personalType, }; } async function openSkillFile(ctx, file) { const path = trimmedText(file?.path); if (!path) return; ctx.setError(''); try {
const raw = await readSkill({ cwd: normalizeSettingsCwd(ctx.projectPath), path }); const skill = optionalObject(raw?.skill); if (!skill) throw new Error('skills/local/read response.skill must be an object');
const content = textFromValue(skill.content); ctx.setForm((form) => skillFormForOpenedFile(path, content, form)); ctx.setPatch({ activeSkillPath: path }); } catch (err) { ctx.setError('读取子文件失败，请重试。'); throw err; } }
async function openSkillCitation(ctx, target, label = '') { const citation = skillCitationFromLink(target, label); if (!citation) return false; ctx.setError(''); if (citation.kind === 'conversation') {
ctx.setNotice('暂不支持会话跳转：' + (citation.conversationId || citation.raw || '未命名会话')); return false; } const skill = findSkillForCitation(ctx.skills, citation); if (!skill) {
ctx.setNotice('未找到引用的技能：' + (citation.skillName || citation.path || citation.skillId || citation.raw || '未命名技能')); return false; } await openEditSkill(ctx, skill); return true; } function skillFormForOpenedFile(path, content, form) { if (!isMainSkillFile(path)) return { ...form, body: content };
const parsed = parseSkillMarkdown(content, form.name); return { ...form, name: parsed.name || form.name, displayName: parsed.displayName || parsed.name || form.displayName, description: parsed.description, keywords: listToText(parsed.triggerWords), body: parsed.body }; }
async function suggestSkillSummaryForEditor(ctx) { ctx.setPatch({ summarySuggesting: true, summarySuggestion: '' }); ctx.setError(''); try {
const cwd = normalizeSettingsCwd(ctx.projectPath); const launchPreferences = typeof ctx.resolveLaunchPreferences === 'function' ? await ctx.resolveLaunchPreferences(cwd) : null;
const description = await suggestSkillSummary(skillSummaryRequest(cwd, ctx.state.editorForm, launchPreferences)); ctx.setPatch({ summarySuggestion: normalizeSummarySuggestion(description) }); } catch (err) { ctx.setError('生成简介失败，请重试。'); throw err; } finally {
ctx.setPatch({ summarySuggesting: false }); } } function skillSummaryRequest(cwd, form, launchPreferences) { return { cwd, name: form.displayName || form.name, description: form.description, content: form.body, scenario_words: wordListFromText(form.keywords), scope: form.scope,
provider: firstTextField(launchPreferences, ['modelProvider', 'provider'], 'launch preferences'), model: textValue(launchPreferences?.model), codexModelProvider: textValue(launchPreferences?.config?.codexModelProvider), }; }
// eslint-disable-next-line react-refresh/only-export-components
export async function saveSkillEditor(ctx) { ctx.setPatch({ saving: true }); ctx.setError(''); ctx.setNotice(''); try { const payload = skillSavePayload(normalizeSettingsCwd(ctx.projectPath), ctx.state); if (shouldCreateProjectSkill(ctx.state)) {
await ctx.facade.createSkill({ cwd: payload.cwd, name: payload.path, content: payload.content }); } else { await ctx.facade.writeSkill(payload); } ctx.setPatch({ editorOpen: false }); await ctx.refreshSkillSurface(); ctx.setNotice(skillSaveNotice(ctx.state, payload)); } catch (err) {
ctx.setError('保存失败，请重试。'); throw err; } finally { ctx.setPatch({ saving: false }); } } function shouldCreateProjectSkill(state) { return !state.activeSkillPath && state.editorForm.scope === 'project'; }
function skillSaveNotice(state, payload) { if (state.activeSkillPath && !isMainSkillFile(state.activeSkillPath)) { return '文件已保存：' + (fileNameFromPath(payload.path) || payload.path); } return '已保存'; } function skillSavePayload(cwd, state) {
const isMain = !state.activeSkillPath || isMainSkillFile(state.activeSkillPath); const displayName = state.editorForm.displayName.trim(); const name = state.editorForm.name.trim() || skillNameFromDisplayName(displayName); if (isMain && !displayName) throw new Error('请先填写技能名称');
if (isMain && !name) throw new Error('技能名称必须包含中文、英文或数字'); const normalizedForm = isMain ? { ...state.editorForm, name, displayName } : state.editorForm; return { cwd, path: isMain ? (state.activeSkillPath || name) : state.activeSkillPath,
content: isMain ? buildSkillMarkdown(normalizedForm) : state.editorForm.body, scope: state.editorForm.scope, personal_type: state.editorForm.scope === 'personal' ? (state.editorForm.personalType || 'user') : '', }; }
// eslint-disable-next-line react-refresh/only-export-components
export async function confirmDeleteSkill(ctx) { const skill = ctx.state.deleteTarget; const skillName = trimmedText(skill?.name); if (!skillName) { ctx.setError('skills/local/delete: name is required'); return; } ctx.setPatch({ deleting: true }); ctx.setError(''); ctx.setNotice(''); try {
await ctx.facade.deleteSkill({ cwd: normalizeSettingsCwd(ctx.projectPath), name: skillName, scope: skill.scope, personal_type: skill.personalType }); ctx.setPatch({ deleteTarget: null }); await ctx.refreshSkillSurface(); ctx.setNotice('已删除 ' + skill.title); } catch (err) {
ctx.setError('删除技能失败，请重试。'); throw err; } finally { ctx.setPatch({ deleting: false }); } } async function confirmImportScope(ctx, scope) { ctx.setPatch({ importing: true }); ctx.setError(''); ctx.setNotice(''); try {
const paths = await selectProjectDirs(); ctx.setPatch({ importScopeOpen: false }); if (!Array.isArray(paths) || paths.length === 0) { ctx.setNotice('未选择目录'); return; }
const cwd = normalizeSettingsCwd(ctx.projectPath); const personalType = scope === 'personal' ? 'imported' : ''; const result = await importSkillDirectories({ cwd, paths, scope, personal_type: personalType });
const failures = Array.isArray(result?.failures) ? result.failures.map(normalizeImportFailure) : []; const importSummaryDrafts = await createImportSummaryDrafts(ctx, result?.imported, scope, personalType); ctx.setPatch({ importSummaryDrafts });
await ctx.refreshSkillSurface(); ctx.setNotice(importNotice(Array.isArray(result?.imported) ? result.imported.length : 0, importSummaryDrafts, failures, scope)); } catch (err) { ctx.setError('导入目录失败，请重试。'); throw err; } finally { ctx.setPatch({ importing: false }); } }
async function createImportSummaryDrafts(ctx, importedSkills, scope, personalType) { if (!Array.isArray(importedSkills) || importedSkills.length === 0) return []; const cwd = normalizeSettingsCwd(ctx.projectPath); const draftResults = await Promise.all(
importedSkills.map((item, index) => createImportSummaryDraft({ ctx, cwd, item, scope, personalType, index, })), ); const drafts = []; for (const draft of draftResults) { if (draft) drafts.push(draft); } return drafts; }
async function createImportSummaryDraft(options) { const { cwd, item, scope, personalType, index } = options; const skillFile = importedSkillFilePath(item); if (!skillFile) return null;
const fallbackName = trimmedText(item?.name) || skillFileBaseName(skillFile); const baseDraft = { id: `${index}:${skillFile || fallbackName}`, name: fallbackName, skillFile, scope: normalizeImportSummaryDraftScope(scope), personalType, suggestion: '', status: 'ready', error: '',
}; try { const raw = await readSkill({ cwd, path: skillFile }); const skill = optionalObject(raw?.skill); if (!skill) throw new Error('skills/local/read response.skill must be an object');
const parsed = parseSkillMarkdown(textFromValue(skill.content), fallbackName); const currentDescription = trimmedText(parsed.description); if (currentDescription) return null; const suggestion = await suggestSkillSummary({ cwd, name: parsed.name || fallbackName,
description: currentDescription, content: parsed.body, scenario_words: parsed.triggerWords, scope: normalizeImportSummaryDraftScope(scope), }); if (!suggestion) return null; return { ...baseDraft, name: parsed.name || fallbackName, suggestion }; } catch (err) {
if (isImportedSkillSameNameConflictError(err)) { return { ...baseDraft, status: 'conflict', error: importedSkillSameNameConflictMessage(baseDraft) }; } return { ...baseDraft, status: 'error', error: '技能已正常导入。可以稍后重试，或手动补充简介。' }; } }
function importSummaryEditorPatch(raw, draft) { const skillBase = { name: draft.name, title: draft.name, description: '', tags: [], scope: draft.scope, personalType: draft.personalType };
const editorForm = { ...skillFormFromRaw(raw, skillBase), scope: draft.scope, personalType: draft.personalType };
return { activeSkillPath: draft.skillFile, editorForm, editorOpen: true, skillFiles: [{ name: 'SKILL.md', path: draft.skillFile, isMain: true }], summarySuggestion: '' }; }
async function openImportSummaryDraft(ctx, draft) { if (!draft?.skillFile) return false; ctx.setError('');
try { const cwd = normalizeSettingsCwd(ctx.projectPath); const raw = await readSkill({ cwd, path: draft.skillFile }); ctx.setPatch(importSummaryEditorPatch(raw, draft)); return true; } catch (err) { ctx.setError('打开技能失败，请重试。'); throw err; } }
async function applyImportSummaryDraft(ctx, draft) { if (!draft || draft.status !== 'ready') return; const suggestion = trimmedText(draft.suggestion); if (!suggestion) return; const opened = await openImportSummaryDraft(ctx, draft); if (!opened) return;
ctx.setForm((form) => ({ ...form, description: suggestion })); ctx.setPatch({ importSummaryDrafts: ctx.state.importSummaryDrafts.map((item) => (item.id === draft.id ? { ...item, status: 'applied' } : item)), }); ctx.setNotice('已采用简介建议，保存技能后生效。'); }
function dismissImportSummaryDraft(ctx, draft) { ctx.setPatch({ importSummaryDrafts: ctx.state.importSummaryDrafts.filter((item) => item.id !== draft?.id) }); } function useSkillResolution(options) {
const { projectPath, refreshSkillSurface, resetKey, resolutionConflicts, setError, setNotice } = options; const [resolutionState, setResolutionState] = useState({ resetKey, preview: null, namePrompt: null, nameInput: '' }); const [actioning, setActioning] = useState('');
const stateShouldReset = ( resolutionState.resetKey !== resetKey || (resolutionConflicts.length === 0 && (resolutionState.preview || resolutionState.namePrompt || resolutionState.nameInput)) ); if (stateShouldReset) { setResolutionState({ resetKey, preview: null, namePrompt: null, nameInput: '' });
} const preview = stateShouldReset ? null : resolutionState.preview; const namePrompt = stateShouldReset ? null : resolutionState.namePrompt; const nameInput = stateShouldReset ? '' : resolutionState.nameInput; const setPreview = useCallback((value) => {
setResolutionState((current) => ({ ...current, preview: typeof value === 'function' ? value(current.preview) : value })); }, []); const setNamePrompt = useCallback((value) => {
setResolutionState((current) => ({ ...current, namePrompt: typeof value === 'function' ? value(current.namePrompt) : value })); }, []); const setNameInput = useCallback((value) => {
setResolutionState((current) => ({ ...current, nameInput: typeof value === 'function' ? value(current.nameInput) : value }));
}, []); const reset = useCallback(() => setResolutionState((current) => ({ ...current, preview: null, namePrompt: null, nameInput: '' })), []); const runAction = useCallback(
(conflict, actionOrEntry, entry = null, newName = '') => runUIAction('skill.resolution.preview', () => runResolutionPipeline({
actionOrEntry, actioning, conflict, entry, newName, projectPath, refreshSkillSurface,
setActioning, setError, setNameInput, setNamePrompt, setNotice, setPreview,
}), { retryable: true }), [actioning, projectPath, refreshSkillSurface, setError, setNameInput, setNamePrompt, setNotice, setPreview],
); const confirmName = useCallback(() => confirmResolutionName({ nameInput, namePrompt, runAction, setError, setNameInput, setNamePrompt }), [nameInput, namePrompt, runAction, setError, setNameInput, setNamePrompt]);
const confirmPreview = useCallback(() => runUIAction(
'skill.resolution.apply',
() => confirmResolutionPreview({ preview, refreshSkillSurface, setActioning, setError, setNameInput, setNamePrompt, setNotice, setPreview }),
), [preview, refreshSkillSurface, setError, setNameInput, setNamePrompt, setNotice, setPreview]);
return { actioning, confirmName, confirmPreview, nameInput, namePrompt, preview, reset, runAction, setNameInput, setNamePrompt, setPreview }; }
async function runResolutionPipeline(ctx) { const request = resolutionRequestFromAction(ctx); if (!request.ok) return request.value; if (request.prompt) { promptResolutionNewName(ctx, request.prompt); return false; }
return previewAndMaybeApplyResolution(ctx, request.payload, request.action, request.conflict, request.applyKey); } function resolutionRequestFromAction({ actionOrEntry, conflict, entry, newName, projectPath }) {
const conflictID = trimmedText(conflict?.conflict_id); const actionEntry = typeof actionOrEntry === 'string' ? { action: actionOrEntry } : actionOrEntry; if (!actionEntry || typeof actionEntry !== 'object' || Array.isArray(actionEntry)) return { ok: false, value: false };
const action = trimmedText(actionEntry.action); if (!conflictID || !action) return { ok: false, value: false }; if (resolutionActionUnsupported(action)) return { ok: false, value: false, unsupported: action };
const providerEntry = resolutionActionEntryTarget(actionEntry, entry || resolutionProviderEntries(conflict)[0]); const applyKey = resolutionApplyKey(conflict, action, providerEntry); const trimmedNewName = trimmedText(newName);
if (requiresResolutionNewName(action) && !trimmedNewName) return { ok: true, prompt: { action, applyKey, conflict, entry: providerEntry } };
return { ok: true, action, applyKey, conflict, payload: resolutionPayload({ action, actionEntry, conflict, conflictID, projectPath, providerEntry, trimmedNewName }) }; }
function resolutionPayload(options) { const { action, actionEntry, conflict, conflictID, projectPath, providerEntry, trimmedNewName } = options; const provider = trimmedText(providerEntry?.provider); const fallbackProvider = trimmedText(conflict?.provider); const payload = {
cwd: normalizeSettingsCwd(projectPath), conflict_id: conflictID, action, name: firstTextField(conflict, ['name', 'skill_name'], 'skill resolution conflict'), scope: trimmedText(conflict?.scope), personal_type: trimmedText(conflict?.personal_type),
provider: provider || fallbackProvider, source_provider: provider || trimmedText(conflict?.source_provider) || fallbackProvider, source_path_id: trimmedText(providerEntry?.source_path_id) || trimmedText(conflict?.source_path_id),
...resolutionSameNamePayloadFields(conflict, action, actionEntry), }; if (trimmedNewName) payload.new_name = trimmedNewName; return payload; }
function promptResolutionNewName(ctx, prompt) { if (resolutionActionUnsupported(prompt.action)) { ctx.setNotice('暂不支持该技能冲突操作：' + resolutionActionLabel(prompt.action)); return;
} ctx.setPreview(null); ctx.setNamePrompt({ ...prompt, autoApply: resolutionActionAutoAppliesForConflict(prompt.action, prompt.conflict) }); ctx.setNameInput(defaultResolutionNewName(prompt.conflict, prompt.action)); ctx.setNotice('请输入新技能名称后继续。'); }
async function previewAndMaybeApplyResolution(ctx, payload, action, conflict, applyKey) { ctx.setActioning(applyKey); ctx.setError(''); let result; try {
const preview = await previewSkillResolution(payload); const items = Array.isArray(preview?.items) ? preview.items : []; if (resolutionActionAutoAppliesForConflict(action, conflict)) { result = await autoApplyResolutionPreview(ctx, payload, items); } else {
ctx.setPreview({ ...preview, action, payload, items, requiresApply: resolutionRequiresApply(action) }); ctx.setNotice(isResolutionViewAction(action) ? '已生成处理预览' : '已生成处理预览，请确认应用。'); result = true; } } catch (err) { ctx.setError('处理技能冲突失败，请重试。'); throw err;
} finally { ctx.setActioning(''); } return result; } async function autoApplyResolutionPreview(ctx, payload, items) { const proof = items[0]; if (!proof?.preview_id || !proof?.preview_hash) throw new Error('缺少处理预览凭据');
const report = await applySkillResolution(resolutionApplyPayload(payload, proof)); ctx.setPreview(null); ctx.setNamePrompt(null); ctx.setNameInput(''); await ctx.refreshSkillSurface(); applyResolutionReportFeedback(ctx, report); return true; } function resolutionApplyPayload(payload, proof) {
return { ...payload, provider: proof.provider || payload.provider, source_provider: proof.source_provider || payload.source_provider, source_path_id: proof.source_path_id || payload.source_path_id, preview_id: proof.preview_id, preview_hash: proof.preview_hash }; }
function resolutionApplyPartialFailure(report) { return Boolean(report?.partialFailure); } function resolutionApplyFollowUpAction(report) { return trimmedText(report?.followUpAction); } function resolutionApplyReportMessage(report) { if (!resolutionApplyPartialFailure(report)) return '已处理技能冲突';
const followUpAction = resolutionApplyFollowUpAction(report); const followUp = followUpAction ? `，后续需要重试：${resolutionActionLabel(followUpAction)}` : '，请查看技能冲突列表并重试'; return `技能冲突已部分处理${followUp}`; }
function applyResolutionReportFeedback(ctx, report) { const message = resolutionApplyReportMessage(report); if (resolutionApplyPartialFailure(report)) { ctx.setNotice(''); ctx.setError(message); return; } ctx.setError(''); ctx.setNotice(message); }
async function confirmResolutionName(options) { const { nameInput, namePrompt, runAction, setError, setNameInput, setNamePrompt } = options; if (!namePrompt) return; const newName = nameInput.trim(); if (!newName) { setError('请输入新技能名称。'); return; }
if (await runAction(namePrompt.conflict, namePrompt.action, namePrompt.entry, newName)) { setNamePrompt(null); setNameInput(''); } }
async function confirmResolutionPreview(ctx) { const proof = Array.isArray(ctx.preview?.items) ? ctx.preview.items[0] : null; if (!ctx.preview?.requiresApply || !proof?.preview_id || !proof?.preview_hash) return; ctx.setActioning('confirm'); try {
const report = await applySkillResolution(resolutionApplyPayload(ctx.preview.payload, proof)); ctx.setPreview(null); ctx.setNamePrompt(null); ctx.setNameInput(''); await ctx.refreshSkillSurface(); applyResolutionReportFeedback(ctx, report); } catch (err) {
ctx.setError('应用技能冲突处理失败，请重试。'); throw err; } finally { ctx.setActioning(''); } }
function SkillsPageView({ copy, model }) {
  return (
    <div className="plugins-square-container">
      <div className="plugins-square-header">
        <h1>{copy.title}</h1>
        <p className="plugins-square-subtitle">{copy.subtitle}</p>
      </div>
      <div className="subhead">{copy.localLibrary}</div>
      <SkillsOverview copy={copy} model={model} />
      <SkillsToolbar copy={copy} model={model} />
      <SkillsStatus copy={copy} model={model} />
      <SkillImportSummaryPanel editor={model.editor} />
      <SkillResolutionPanel model={model} />
      <SkillGrid copy={copy} model={model} />
      <SkillModals model={model} />
    </div>
  );
}
function SkillsOverview({ copy, model }) { const counts = model.filters.counts; const conflictValue = model.isProjectPending || model.dashboard.isResolutionPending || model.dashboard.resolutionSyncErrorText ? copy.pending : model.dashboard.resolutionConflicts.length; return (
<section className="skills-overview-compact" aria-label={copy.overviewAria}> <dl className="overview-stats-row"> <div><dt>{copy.localSkills}</dt><dd>{counts.all}</dd></div>
<div><dt>{copy.projectShared}</dt><dd>{counts.project}</dd></div> <div><dt>{copy.personalUse}</dt><dd>{counts.personal}</dd></div> <div><dt>{copy.pendingConflicts}</dt><dd>{conflictValue}</dd></div> </dl> </section> ); }
function SkillImportSummaryPanel({ editor }) { const drafts = Array.isArray(editor.importSummaryDrafts) ? editor.importSummaryDrafts : []; if (drafts.length === 0) return null; return (
<section className="skills-import-summary-panel" data-testid="skills-import-summary-panel"> <div className="skills-import-summary-head"> <div><strong>{importSummaryPanelTitle(drafts)}</strong><span>{importSummaryPanelHint(drafts)}</span></div>
<button type="button" className="ghost" data-testid="skills-import-summary-clear" onClick={editor.clearImportSummaryDrafts}>收起</button> </div> {drafts.map((draft, index) => <SkillImportSummaryItem draft={draft} editor={editor} index={index} key={draft.id || index} />)} </section> ); }
function SkillImportSummaryItem({ draft, editor, index }) { return (
<article className={'skills-import-summary-item is-' + draft.status} data-testid={'skills-import-summary-item-' + index}> <div className="skills-import-summary-main"><strong>{draft.name || '未命名技能'}</strong><span>{scopeLabel(draft.scope)}</span></div>
<p className="skills-import-summary-text">{draft.status === 'ready' || draft.status === 'applied' ? draft.suggestion : (draft.error || '技能已正常导入。可以稍后手动补充简介。')}</p> <div className="skills-import-summary-actions">
{draft.status === 'ready' ? <button type="button" data-testid={'skills-import-summary-apply-' + index} onClick={() => { void editor.applyImportSummaryDraft(draft); }}>采用并编辑</button> : null} {draft.status === 'applied' ? <span className="skills-inline-tip">已采用，保存后生效</span> : null}
{draft.status === 'error' ? <button type="button" data-testid={'skills-import-summary-edit-' + index} onClick={() => { void editor.openImportSummaryDraft(draft); }}>编辑简介</button> : null}
<button type="button" className="ghost" data-testid={'skills-import-summary-dismiss-' + index} onClick={() => editor.dismissImportSummaryDraft(draft)}>跳过</button> </div> </article> ); }

function SkillsToolbar({ copy, model }) { return (
<div className="skills-toolbar skills-toolbar-unified">
  <div className="skill-filter segment">
    {model.filters.scopeOptions.map(([value]) => (
      <button
        key={value}
        type="button"
        className={model.scopeFilter === value ? 'active' : ''}
        onClick={() => model.setScopeFilter(value)}
      >
        {copy.scopeLabels?.[value] || (value === 'personal' ? copy.personalUse : value === 'project' ? copy.projectShared : copy.scopeAll)} {model.filters.counts[value]}
      </button>
    ))}
  </div>
  <label>
    <Search size={18} />
    <input value={model.query} onChange={(event) => model.setQuery(event.target.value)} placeholder={copy.searchSkillsPlaceholder} aria-label={copy.searchSkills} />
  </label>
  <div className="skills-toolbar-actions">
    <button type="button" className="btn-secondary" onClick={model.editor.openImportScope} disabled={model.editor.importing}>{copy.importDirs}</button>
    <button type="button" className="btn-primary" onClick={model.editor.openCreateEditor}>{copy.newSkill}</button>
  </div>
</div>
); }
function SkillsStatus({ copy, model }) {
  return (
    <>
      {model.isProjectPending ? (
        <div className="status-surface-line info-status">{copy.connecting}</div>
      ) : null}
      {model.dashboard.isInitialLoading ? (
        <div className="status-surface-line loading-status">{copy.loading}</div>
      ) : null}
      {model.notice ? (
        <div className="status-surface-line success-status">{model.notice}</div>
      ) : null}
      {model.dashboard.showCachedSyncError ? (
        <CachedSkillSyncError copy={copy} dashboard={model.dashboard} />
      ) : null}
      {model.dashboard.showBlockingSyncError ? (
        <RetryableSyncError
          className="danger-text skills-sync-alert"
          message={model.dashboard.syncErrorText}
          onRetry={model.dashboard.retrySkillSurface}
        />
      ) : null}
      {model.error ? (
        <div className="status-surface-line error-status" role="alert">{model.error}</div>
      ) : null}
    </>
  );
}
function CachedSkillSyncError({ copy, dashboard }) { return (
<div className="danger-text skills-sync-alert" role="alert"> <span>同步失败，显示的是上次成功的数据：{dashboard.syncErrorText}</span> <button type="button" className="ghost" onClick={() => { void dashboard.retrySkillSurface(); }}>{copy.retrySync}</button> </div> ); }
function SkillResolutionPanel({ model }) { const conflicts = model.dashboard.resolutionConflicts; if (!conflicts.length) return null; return (
<section className="skills-resolution-panel">
  <header className="skills-resolution-header fusion-surface">
    <strong>发现 {conflicts.length} 个技能冲突，需要处理后再使用。</strong>
  </header>
  <div className="skills-resolution-list">
    {conflicts.map((conflict, index) => <SkillResolutionConflict conflict={conflict} index={index} key={textFromValue(conflict.conflict_id) || String(index)} resolution={model.resolution} />)}
    {model.resolution.preview ? <SkillResolutionPreview resolution={model.resolution} /> : null}
  </div>
</section> ); }
function SkillResolutionConflict({ conflict, index, resolution }) {
const conflictID = textFromValue(conflict.conflict_id) || String(index); const promptConflictID = textFromValue(resolution.namePrompt?.conflict?.conflict_id); const promptApplies = resolution.namePrompt && promptConflictID === textFromValue(conflict.conflict_id);
const manualSteps = resolutionManualSteps(conflict); return (
<article className="skills-resolution-item"> <header><h3>{firstTextField(conflict, ['name', 'skill_name'], 'skill resolution conflict') || '未命名技能'} · {resolutionKindLabel(conflict.kind)}</h3><span>{resolutionConflictScopeLabel(conflict)}</span></header>
<p className="skills-resolution-guide">{resolutionConflictGuide(conflict)}</p>
{resolutionProviderEntries(conflict).map((entry, sourceIndex) => <SkillResolutionActionRow conflict={conflict} conflictID={conflictID} providerEntry={entry} resolution={resolution} sourceIndex={sourceIndex} key={conflictID + ':' + sourceIndex + ':' + resolutionProviderEntryLabel(entry)} />)}
{manualSteps.length > 0 ? <ul className="skills-resolution-manual-steps">{manualSteps.map((step) => <li key={step}>{step}</li>)}</ul> : null} {promptApplies ? <SkillResolutionNamePrompt resolution={resolution} /> : null} </article> ); }
function SkillResolutionActionRow({ conflict, conflictID, providerEntry, resolution, sourceIndex }) { const providerEntries = resolutionProviderEntries(conflict); return (
<div className="skills-resolution-actions"> {providerEntries.length > 1 ? <span className="skills-resolution-source">{resolutionProviderEntryLabel(providerEntry)}</span> : null}
{resolutionActionEntries(conflict).map((actionEntry, actionIndex) => <SkillResolutionActionButton actionEntry={actionEntry} actionIndex={actionIndex} conflict={conflict} providerEntry={providerEntry} resolution={resolution} key={conflictID + ':' + sourceIndex + ':' + actionIndex} />)} </div> ); }
function resolutionActionVisualKind(actionEntry) {
  const action = (actionEntry.action || actionEntry).toString();
  if (action === 'view_diff' || action === 'view_unmanaged') return 'ghost resolution-btn-secondary';
  if (action === 'delete') return 'danger-button';
  if (actionEntry.recommended || actionEntry.preferred) return 'super-dolphin-agent-btn-fusion resolution-btn-primary';
  return 'super-dolphin-agent-btn-fusion-ghost resolution-btn-secondary';
}

function SkillResolutionActionButton({ actionEntry, actionIndex, conflict, providerEntry, resolution }) {
  const action = (actionEntry.action || actionEntry).toString();
  const targetEntry = resolutionActionEntryTarget(actionEntry, providerEntry);
  const applyKey = resolutionApplyKey(conflict, action, targetEntry);
  const buttonClass = resolutionActionVisualKind(actionEntry);
  return (
    <button
      key={applyKey + ':' + actionIndex}
      type="button"
      className={buttonClass}
      title={resolutionActionEntryHelp(actionEntry)}
      onClick={() => { void resolution.runAction(conflict, actionEntry, providerEntry); }}
      disabled={resolution.actioning === applyKey}
    >
      {resolution.actioning === applyKey ? '处理中...' : resolutionActionEntryLabel(actionEntry)}
    </button>
  );
}
function SkillResolutionNamePrompt({ resolution }) { return ( <div className="skills-resolution-name-field"> <label>新技能名称<input value={resolution.nameInput} onChange={(event) => resolution.setNameInput(event.target.value)} aria-label="新技能名称" /></label>
<button type="button" onClick={() => { void resolution.confirmName(); }} disabled={resolution.actioning === resolution.namePrompt.applyKey}>{resolutionPromptActionLabel(resolution)}</button>
<button type="button" className="ghost" onClick={() => { resolution.setNamePrompt(null); resolution.setNameInput(''); }}>取消</button> </div> ); }
function resolutionPromptActionLabel(resolution) { if (resolution.actioning === resolution.namePrompt?.applyKey) return resolution.namePrompt?.autoApply ? '处理中...' : '生成中...'; return resolution.namePrompt?.autoApply ? '确认处理' : '生成预览'; }
function SkillResolutionPreview({ resolution }) { return ( <article className="skills-resolution-preview"> <header> <h3>{resolutionActionLabel(resolution.preview.action)}</h3> {resolution.preview.requiresApply ? (
<button type="button" onClick={() => { void resolution.confirmPreview(); }} disabled={resolution.actioning === 'confirm'} > {resolution.actioning === 'confirm' ? '应用中...' : '确认应用'} </button>
) : null} <button type="button" className="ghost" onClick={() => resolution.setPreview(null)}>取消</button> </header> <p className="skills-resolution-guide">{resolutionPreviewIntro(resolution.preview)}</p>
{optionalArray(resolution.preview.items).map((item, index) => <SkillResolutionPreviewItem action={resolution.preview.action} item={item} key={textFromValue(item.preview_id) || String(index)} />)} </article> ); }
function SkillResolutionPreviewItem({ action, item }) { const sourceHash = resolutionShortHash(item.source_hash || item.sourceHash); const targetHash = resolutionShortHash(item.target_hash || item.targetHash); const diff = textFromValue(item.diff); return (
<div className="skills-resolution-preview-item"> {previewItemPaths(item, action).map((pathItem) => <p key={pathItem.label + ':' + pathItem.value}><span>{pathItem.label}</span><code>{pathItem.value}</code></p>)} {sourceHash || targetHash || diff ? (
<details className="skills-resolution-technical" open> <summary>技术信息</summary> {sourceHash ? <div className="skills-resolution-preview-path">外部版本号：{sourceHash}</div> : null} {targetHash ? <div className="skills-resolution-preview-path">管理版本号：{targetHash}</div> : null}
{diff ? <pre className="skills-resolution-diff">{diff}</pre> : null} </details> ) : null} </div> ); }
function SkillGrid({ copy, model }) { const showReadyEmpty = !model.isProjectPending && !model.dashboard.isInitialLoading && !model.dashboard.showBlockingSyncError && model.filters.filteredItems.length === 0; return (
<> {showReadyEmpty ? <SkillsEmptyState copy={copy} hasSkills={model.filters.counts.all > 0} /> : null}
{model.filters.filteredItems.length > 0 ? <div className="skill-grid">{model.filters.filteredItems.map((skill) => <SkillCard copy={copy} key={skill.id} skill={skill} onEdit={model.editor.openEditSkill} onDelete={model.editor.onDeleteSkill} />)}</div> : null}
{model.filters.countText ? <p className="skills-inline-tip">{model.filters.countText}</p> : null} </> ); }
function SkillsEmptyState({ copy, hasSkills }) { if (hasSkills) { return ( <div className="empty-state"> <h3>{copy.noMatchesTitle}</h3> <p>{copy.noMatchesText}</p> </div> ); } return <div className="status-surface-line empty-status">{copy.empty}</div>; }
function SkillModals({ model }) { const editor = model.editor; return (
<> {editor.editorOpen ? <SkillEditorDialog editor={editor} /> : null} {editor.deleteTarget ? <ConfirmSkillDeleteModal skill={editor.deleteTarget} deleting={editor.deleting} onClose={editor.closeDelete} onConfirm={editor.confirmDeleteSkill} /> : null}
{editor.importScopeOpen ? <ImportScopeModal importing={editor.importing} onClose={editor.closeImportScope} onConfirm={editor.confirmImportScope} /> : null} </> ); } function SkillEditorDialog({ editor }) { return (
<SkillEditorModal key={editor.activeSkillPath || 'new'} form={editor.editorForm} setForm={editor.setForm} activeSkillPath={editor.activeSkillPath} files={editor.skillFiles} summarySuggestion={editor.summarySuggestion} summarySuggesting={editor.summarySuggesting}
saving={editor.saving} onSuggestSummary={editor.suggestSummary} onApplySummary={editor.applySummary} onOpenCitation={editor.openSkillCitation} onOpenFile={editor.openSkillFile} onClose={editor.closeEditor} onSave={editor.saveEditor} /> ); } function SkillCard({ copy, skill, onEdit, onDelete }) {
  const tags = skill.tags.slice(0, 4); const extraTagCount = skill.tags.length - tags.length; const descriptionText = trimmedText(skill.description); const summaryText = trimmedText(skill.summary); const description = descriptionText || summaryText || copy.noDescription;
  const shouldShowSummary = Boolean(summaryText && summaryText !== description);
  return (
    <article className="skill-card skill-card-redesign">
      <div className="skill-card-icon" aria-hidden="true">
        <FileText size={20} />
      </div>
      <div className="mcp-tool-main">
        <header className="mcp-tool-title-line">
          <h3>{skill.title}</h3>
        </header>
        <p className="path-text">{skill.dir || copy.noPath}</p>
        <p className="description-text">{description}</p>
        {shouldShowSummary ? <div className="summary-quote">{summaryText}</div> : null}
        <div className="card-tags">
          {tags.length > 0 ? tags.map((tag) => <span key={tag} className="card-tag">{tag}</span>) : <span className="card-tag">{copy.noKeywords}</span>}
          {extraTagCount > 0 ? <span className="card-tag">+{extraTagCount}</span> : null}
        </div>
      </div>
      <span className="mcp-tool-status is-enabled">{scopeLabel(skill.scope)}</span>
      <div className="card-actions-redesign">
        <button type="button" onClick={() => { void onEdit(skill); }} disabled={!skill.dir}>{copy.editDetails}</button>
        <button type="button" className="danger-btn" onClick={() => { void onDelete(skill); }} disabled={!skill.name}>{copy.delete}</button>
      </div>
    </article>
  );
}
function SkillEditorModal(props) { const { form, setForm, activeSkillPath, files, summarySuggestion, summarySuggesting, saving, onSuggestSummary, onApplySummary, onOpenCitation, onOpenFile, onClose, onSave } = props;
const isMain = !activeSkillPath || isMainSkillFile(activeSkillPath); const modalTitle = activeSkillPath ? '编辑技能' : '新建技能'; const saveLabel = isMain ? '保存技能' : '保存文件'; const update = (key) => (event) => setForm((current) => ({ ...current, [key]: event.target.value }));
const updateDisplayName = (event) => { const value = event.target.value; setForm((current) => ({ ...current, displayName: value, name: activeSkillPath ? current.name : skillNameFromDisplayName(value), })); }; const [bodyEditing, setBodyEditing] = useState(!activeSkillPath); return (
<FocusTrapDialog ariaLabel={modalTitle} className="modal-box skills-editor-modal" closeDisabled={saving} onClose={onClose}> <SkillEditorHeader modalTitle={modalTitle} /> <SkillEditorFields form={form} isMain={isMain} summarySuggestion={summarySuggestion}
summarySuggesting={summarySuggesting} update={update} updateDisplayName={updateDisplayName} setForm={setForm} onApplySummary={onApplySummary} onSuggestSummary={onSuggestSummary} />
<SkillEditorSubfiles activeSkillPath={activeSkillPath} files={files} onOpenFile={onOpenFile} /> <SkillEditorBody activeSkillPath={activeSkillPath} bodyEditing={bodyEditing} files={files} form={form} isMain={isMain} onOpenCitation={onOpenCitation} onOpenFile={onOpenFile}
setBodyEditing={setBodyEditing} update={update} /> <footer> <button type="button" className="ghost" onClick={onClose} disabled={saving}>取消</button> <button type="button" onClick={() => { void onSave(); }} disabled={saving}>{saving ? '保存中...' : saveLabel}</button> </footer> </FocusTrapDialog> ); }
function SkillEditorHeader({ modalTitle }) { return ( <header className="skills-editor-modal-head"> <div><h2>{modalTitle}</h2><p>你可以修改简介和技能内容。</p></div> </header> ); }
function SkillEditorFields(props) { const { form, isMain, summarySuggestion, summarySuggesting, update, updateDisplayName, setForm, onApplySummary, onSuggestSummary, } = props; return (
<div className="form-grid"> <label className="wide">技能名称<input value={form.displayName} onChange={updateDisplayName} disabled={!isMain} /></label> <SkillDescriptionField form={form} isMain={isMain} summarySuggestion={summarySuggestion} summarySuggesting={summarySuggesting}
update={update} onApplySummary={onApplySummary} onSuggestSummary={onSuggestSummary} /> <div className="skills-field"> <span>使用范围</span> <fieldset className="skills-scope-segmented"> <legend className="sr-only">使用范围</legend>
<button type="button" className={form.scope === 'project' ? 'active' : ''} disabled={!isMain} onClick={() => setForm((current) => ({ ...current, scope: 'project' }))}>项目共享</button>
<button type="button" className={form.scope === 'personal' ? 'active' : ''} disabled={!isMain} onClick={() => setForm((current) => ({ ...current, scope: 'personal' }))}>私人使用</button> </fieldset> </div>
<label>关键词<input value={form.keywords} onChange={update('keywords')} disabled={!isMain} aria-label="关键词" /></label> </div> ); } function SkillDescriptionField(props) { const { form, isMain, summarySuggestion, summarySuggesting, update, onApplySummary, onSuggestSummary } = props; return (
<div className="skills-field wide"> <div className="skills-editor-label-row"> <label htmlFor="skills-description-input">技能简介</label> <button type="button" className="ghost" onClick={() => { void onSuggestSummary(); }}
disabled={!isMain || summarySuggesting || (!form.name.trim() && !form.body.trim())} > {summarySuggesting ? '生成中' : '帮我生成'} </button> </div> <input id="skills-description-input" value={form.description} onChange={update('description')} disabled={!isMain} aria-label="技能简介" />
{summarySuggestion ? <div className="skills-inline-tip skills-summary-suggestion" data-testid="skills-summary-suggestion"><span>建议：{summarySuggestion}</span><button type="button" onClick={onApplySummary}>采用</button></div> : null} <div className="skills-inline-tip">建议写成“当你需要……时使用”。</div> </div> ); }
function SkillEditorSubfiles({ activeSkillPath, files, onOpenFile }) { if (!files.some((file) => !file.isMain)) return null; return ( <div className="skills-subfiles-wrap"> <span>附加内容</span> <div className="skills-subfiles"> {files.map((file) => (
<button key={file.path} type="button" className={file.path === activeSkillPath ? 'active' : ''} onClick={() => { void onOpenFile(file); }}> {file.name}{file.isMain ? ' · 主要文件' : ''} </button> ))} </div> <div className="skills-inline-tip">这里是这个技能附带的示例、模板或脚本。</div> </div> ); }
function SkillEditorBody(props) { const { activeSkillPath, bodyEditing, files, form, isMain, onOpenCitation, onOpenFile, setBodyEditing, update } = props; const openPreviewPath = (path, label) => { if (skillCitationFromLink(path, label)) {
void onOpenCitation(path, label); return; } const file = resolveSkillPreviewFile(path, files, activeSkillPath); if (file) void onOpenFile(file); }; return ( <div className="skills-body-field"> <div className="skills-body-head"> <span>{isMain ? '技能内容' : '关联文件内容'}</span>
{bodyEditing ? <button type="button" className="ghost" onClick={() => setBodyEditing(false)}>预览正文</button> : <button type="button" onClick={() => setBodyEditing(true)}>编辑正文</button>} </div>
{bodyEditing ? <textarea value={form.body} onChange={update('body')} aria-label={isMain ? '技能内容' : '关联文件内容'} /> : <div className="skills-body-preview" data-testid="skills-editor-body-preview"><SkillMarkdownPreview content={form.body} onOpenPath={openPreviewPath} /></div>}
<div className="skills-inline-tip">点击“编辑正文”展开编辑；切回“预览正文”查看效果。</div> </div> ); } function ConfirmSkillDeleteModal({ skill, deleting, onClose, onConfirm }) { return (
<FocusTrapDialog ariaLabel="删除技能" closeDisabled={deleting} onClose={onClose}> <header><h2>删除技能</h2><button type="button" className="ghost" onClick={onClose} disabled={deleting}>关闭</button></header> <p>确定删除技能 “{skill.name}” 吗？该操作会删除技能目录及其资源文件，无法恢复。</p>
<p className="path">{skill.dir || '-'}</p> <footer> <button type="button" className="ghost" onClick={onClose} disabled={deleting}>取消</button>
<button type="button" className="text-danger" onClick={() => { void onConfirm(); }} disabled={deleting}>{deleting ? '删除中...' : '确认删除'}</button> </footer> </FocusTrapDialog> ); } function ImportScopeModal({ importing, onClose, onConfirm }) { return (
<FocusTrapDialog ariaLabel="导入技能" closeDisabled={importing} onClose={onClose}> <header><h2>导入技能</h2><button type="button" className="ghost" onClick={onClose} disabled={importing}>关闭</button></header> <p>这些技能导入后给谁使用？</p> <footer>
<button type="button" className="ghost" onClick={onClose} disabled={importing}>取消</button> <button type="button" onClick={() => { void onConfirm('personal'); }} disabled={importing}>私人使用</button>
<button type="button" onClick={() => { void onConfirm('project'); }} disabled={importing}>项目共享</button> </footer> </FocusTrapDialog> ); } export { SkillsPage };
