import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Search, Sparkles } from 'lucide-react';
import { FocusTrapDialog } from '../../shared/ui/FocusTrapDialog.jsx';
import { applySkillResolution, deleteSkill, getDashboardPage, importSkillDirectories, listSkillFiles, listSkillResolutions, previewSkillResolution, readSkill, selectProjectDirs, suggestSkillSummary, writeSkill } from '../../shared/api/backendApi.js';
import { cleanScalar, dashboardQueryKey, errorMessage, listToText, optionalSettingsCwd, SKILLS_REQUEST_TIMEOUT_MS, textValue, withTimeout, wordListFromText } from '../shared/pageShared.js';
import { PageHeader, RetryableSyncError } from '../shared/pageComponents.jsx';

function normalizeSettingsCwd(value) {
  const cwd = (value || '').toString().trim();
  if (!cwd || cwd === '.' || cwd === '未选择项目') {
    throw new Error('settings: cwd is required');
  }
  return cwd;
}

async function fetchSkillsDashboard(cwd) {
  const response = await withTimeout(
    getDashboardPage({ cwd, page: 'skills' }),
    SKILLS_REQUEST_TIMEOUT_MS,
    '技能列表加载超时，请检查技能目录或后端状态。',
  );
  return normalizeSkillsResponse(response);
}

async function fetchSkillResolutionsDashboard(cwd) {
  const response = await withTimeout(
    listSkillResolutions({ cwd }),
    SKILLS_REQUEST_TIMEOUT_MS,
    '技能冲突检查超时，请检查技能目录或后端状态。',
  );
  return normalizeResolutionResponse(response);
}

function scopeForSkill(raw) {
  const scope = (raw?.scope || '').toString().trim().toLowerCase();
  if (scope === 'project' || scope === 'personal') return scope;

  const trust = (raw?.trust || '').toString().trim().toLowerCase();
  if (trust === 'user' || trust === 'signed' || trust === 'system' || trust === 'personal') {
    return 'personal';
  }
  return 'project';
}

function scopeLabel(scope) {
  return scope === 'personal' ? '私人使用' : '项目共享';
}

function isInternalSkillReferenceWord(word) {
  const text = (word || '').toString().trim();
  return text.startsWith('@') || /^\[skill:[^\]]+\]$/i.test(text);
}

function normalizeWordList(...groups) {
  const seen = new Set();
  const words = [];
  groups.flat().forEach((word) => {
    const text = (word || '').toString().trim();
    const key = text.toLowerCase();
    if (!text || seen.has(key) || isInternalSkillReferenceWord(text)) return;
    seen.add(key);
    words.push(text);
  });
  return words;
}

function skillText(raw, keys) {
  for (const key of keys) {
    const text = textValue(raw?.[key]);
    if (text) return text;
  }
  return '';
}

function skillWordGroup(raw, snakeKey, camelKey) {
  const snakeValue = raw?.[snakeKey];
  if (Array.isArray(snakeValue)) return snakeValue;
  return raw?.[camelKey] || [];
}

function normalizeSkill(raw, index) {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) {
    throw new Error(`skills dashboard response item ${index} must be an object`);
  }
  const name = skillText(raw, ['name', 'key']);
  const displayName = skillText(raw, ['display_name', 'displayName', 'title']);
  const scope = scopeForSkill(raw);
  const dir = skillText(raw, ['dir', 'path']);
  const description = skillText(raw, ['description', 'summary']);
  const summary = skillText(raw, ['summary', 'description']);
  const title = displayName || name;
  return {
    id: [scope, skillText(raw, ['personal_type', 'personalType']), name, dir, index].join(':'),
    name,
    title: title || '未命名技能',
    dir,
    description,
    summary,
    scope,
    personalType: skillText(raw, ['personal_type', 'personalType']),
    tags: normalizeWordList(skillWordGroup(raw, 'trigger_words', 'triggerWords'), skillWordGroup(raw, 'force_words', 'forceWords')),
  };
}

function normalizeSkillsResponse(response) {
  if (!response || typeof response !== 'object' || Array.isArray(response)) {
    throw new Error('skills dashboard response must be an object');
  }
  if (!Array.isArray(response.skills)) {
    throw new Error('skills dashboard response skills must be an array');
  }
  return response.skills.map((item, index) => normalizeSkill(item, index));
}

function normalizeSummarySuggestion(value) {
  if (value && typeof value === 'object' && !Array.isArray(value)) {
    return textValue(value.description);
  }
  return textValue(value);
}

function parseWordsValue(value) {
  if (Array.isArray(value)) return wordListFromText(value);
  const raw = (value || '').toString().trim();
  if (!raw) return [];
  return wordListFromText(raw.startsWith('[') && raw.endsWith(']') ? raw.slice(1, -1) : raw);
}

function parseSkillMarkdown(content, fallbackName = '') {
  const text = (content || '').replace(/\r\n/g, '\n');
  if (!text.startsWith('---\n')) {
    return {
      name: fallbackName,
      displayName: '',
      description: '',
      triggerWords: [],
      body: text,
    };
  }
  const rest = text.slice(4);
  const end = rest.indexOf('\n---');
  if (end < 0) return { name: fallbackName, displayName: '', description: '', triggerWords: [], body: text };
  const attrs = {};
  for (const line of rest.slice(0, end).split('\n')) {
    const idx = line.indexOf(':');
    if (idx <= 0) continue;
    attrs[line.slice(0, idx).trim().toLowerCase().replace(/-/g, '_')] = line.slice(idx + 1).trim();
  }
  return {
    name: cleanScalar(attrs.name) || fallbackName,
    displayName: cleanScalar(attrs.display_name || attrs.displayname || attrs.title),
    description: cleanScalar(attrs.description || attrs.summary || attrs.digest),
    triggerWords: wordListFromText([
      ...parseWordsValue(attrs.trigger_words || attrs.triggerwords || attrs.keywords || attrs.tags),
      ...parseWordsValue(attrs.force_words || attrs.forcewords),
    ]),
    body: rest.slice(end + 4).replace(/^\n/, '').trim(),
  };
}

function quoteYAML(value) {
  return `"${(value || '').toString().replace(/"/g, '\\"')}"`;
}

function skillNameFromDisplayName(value) {
  const text = (value || '').toString().trim();
  let slug = '';
  let lastDash = false;
  for (const char of Array.from(text)) {
    if (/[\p{L}\p{N}_-]/u.test(char)) {
      slug += char;
      lastDash = false;
    } else if (!lastDash) {
      slug += '-';
      lastDash = true;
    }
  }
  return slug.replace(/^-+|-+$/g, '');
}

function buildSkillMarkdown(form) {
  const name = (form.name || '').trim();
  const displayName = (form.displayName || '').trim();
  const description = (form.description || '').trim();
  const words = wordListFromText(form.keywords);
  const body = (form.body || '').trim();
  const lines = ['---', `name: ${quoteYAML(name)}`];
  if (displayName) lines.push(`display_name: ${quoteYAML(displayName)}`);
  if (description) lines.push(`description: ${quoteYAML(description)}`);
  if (words.length > 0) lines.push(`trigger_words: [${words.map(quoteYAML).join(', ')}]`);
  lines.push('---', '', body || '## 说明\n\n请补充技能规则。');
  return lines.join('\n');
}

function SkillMarkdownPreview({ content }) {
  const text = (content || '').toString().trim();
  if (!text) return <p>暂无内容，点击“编辑正文”开始编写。</p>;
  const blocks = [];
  let paragraph = [];
  let list = [];
  const flushParagraph = () => {
    if (paragraph.length === 0) return;
    blocks.push({ type: 'p', text: paragraph.join(' ') });
    paragraph = [];
  };
  const flushList = () => {
    if (list.length === 0) return;
    blocks.push({ type: 'ul', items: list });
    list = [];
  };
  for (const line of text.split('\n')) {
    const trimmed = line.trim();
    if (!trimmed) {
      flushParagraph();
      flushList();
      continue;
    }
    const heading = /^(#{1,6})\s+(.+)$/.exec(trimmed);
    if (heading) {
      flushParagraph();
      flushList();
      blocks.push({ type: 'heading', level: Math.min(heading[1].length, 3), text: heading[2] });
      continue;
    }
    const bullet = /^[-*]\s+(.+)$/.exec(trimmed);
    if (bullet) {
      flushParagraph();
      list.push(bullet[1]);
      continue;
    }
    paragraph.push(trimmed);
  }
  flushParagraph();
  flushList();
  return (
    <>
      {blocks.map((block, index) => {
        if (block.type === 'heading') {
          const Tag = block.level <= 1 ? 'h3' : 'h4';
          return <Tag key={`heading-${index}`}>{block.text}</Tag>;
        }
        if (block.type === 'ul') {
          return <ul key={`list-${index}`}>{block.items.map((item, itemIndex) => <li key={`${index}-${itemIndex}`}>{item}</li>)}</ul>;
        }
        return <p key={`p-${index}`}>{block.text}</p>;
      })}
    </>
  );
}

function emptySkillForm() {
  return {
    name: '',
    displayName: '',
    description: '',
    keywords: '',
    body: '',
    scope: 'project',
    personalType: '',
  };
}

function normalizeSkillFileList(response) {
  if (!response || typeof response !== 'object' || !Array.isArray(response.files)) return [];
  return (
    response.files
    .map((file) => ({
      name: (file?.name || '').toString().trim(),
      path: (file?.path || '').toString().trim(),
      isMain: Boolean(file?.is_main || file?.isMain),
    }))
    .filter((file) => file.name && file.path)
  );
}

function isMainSkillFile(path) {
  return /(^|[\\/])SKILL\.md$/i.test((path || '').toString().trim());
}

function normalizeResolutionResponse(response) {
  if (Array.isArray(response)) return response;
  if (!response || typeof response !== 'object') {
    throw new Error('skill resolutions response must be an object');
  }
  if (Array.isArray(response?.items)) return response.items;
  if (Array.isArray(response?.conflicts)) return response.conflicts;
  throw new Error('skill resolutions response items must be an array');
}

function resolutionKindLabel(kind) {
  return ({
    mirror_drift: '外部版本有改动',
    unmanaged_provider_skill: '发现外部技能',
    unmanaged: '发现外部技能',
    same_name: '同名技能',
    same_name_scope_conflict: '同名技能',
    canonical_deleted_with_drift: '旧版本需要处理',
    external_personal_project_same_name: '私人和项目同名',
  }[(kind || '').toString().trim().toLowerCase()] || '需要处理');
}

function resolutionActionLabel(action) {
  return ({
    view_diff: '查看两个版本',
    view_unmanaged: '查看外部位置',
    sync_back_to_canonical: '用外部修改更新本项目',
    canonical_overwrite_mirror: '用本项目内容覆盖外部版本',
    save_as_new_skill: '另存为新技能',
    confirm_delete_drifted_mirror: '删除旧版本',
    sync_back_to_personal: '继续私人使用',
    personal_overwrite_mirror: '用私人技能覆盖外部版本',
    save_as_new_personal_skill: '另存为新私人技能',
    import_to_personal_imported: '导入到私人使用',
    import_to_project: '导入到项目共享',
    takeover_provider_skill: '纳入管理',
    use_project_shared_skill: '使用项目共享版本，删除旧私人版本',
    use_external_provider_skill: '继续私人使用，替换项目共享版本',
    replace_provider_root_symlink: '接管外部技能目录',
    rename_personal: '改名保存',
    keep_selected: '用选中的版本，删除其他版本',
  }[(action || '').toString().trim()] || '处理');
}

function resolutionActionHelp(action) {
  return ({
    view_diff: '只查看差异，不写入文件。',
    view_unmanaged: '查看外部技能位置，不写入文件。',
    sync_back_to_canonical: '把外部修改同步回当前管理的技能。',
    canonical_overwrite_mirror: '用当前项目共享技能覆盖 Claude/Codex 中的外部版本。',
    save_as_new_skill: '保留两边内容，把外部版本保存成新的项目共享技能。',
    confirm_delete_drifted_mirror: '删除 Claude/Codex 里保留的旧版本。',
    sync_back_to_personal: '恢复为私人使用，外部运行时会继续读取这个私人版本。',
    personal_overwrite_mirror: '用当前私人技能覆盖 Claude/Codex 中的外部版本。',
    save_as_new_personal_skill: '保留两边内容，把外部版本保存成新的私人技能。',
    import_to_personal_imported: '把外部技能导入到私人使用。',
    import_to_project: '把外部技能导入到项目共享。',
    takeover_provider_skill: '把外部技能纳入当前技能管理。',
    use_project_shared_skill: '使用项目共享版本，并删除同名旧私人版本。',
    use_external_provider_skill: '继续私人使用，并替换项目共享版本。',
    replace_provider_root_symlink: '用当前技能根目录接管外部技能目录。',
    rename_personal: '把选中的版本改名保存，两个版本都会保留。',
    keep_selected: '保留选中的版本，删除其他同名版本。',
  }[(action || '').toString().trim()] || '');
}

function requiresResolutionNewName(action) {
  return (
    action === 'save_as_new_skill'
    || action === 'save_as_new_personal_skill'
    || action === 'rename_personal'
  );
}

function isResolutionViewAction(action) {
  return action === 'view_diff' || action === 'view_unmanaged';
}

function resolutionRequiresApply(action) {
  return !isResolutionViewAction(action);
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

function resolutionSourceID(source) {
  return (source?.canonical_id || source?.canonicalID || source?.source_id || source?.sourceID || '').toString().trim();
}

function resolutionSourceScope(source) {
  return (source?.scope || '').toString().trim().toLowerCase();
}

function resolutionSourcePersonalType(source) {
  return (source?.personal_type || source?.personalType || '').toString().trim().toLowerCase();
}

function resolutionSourcePathLeaf(source) {
  const path = (source?.path || source?.skill_file || source?.skillFile || '').toString().trim().replace(/\\/g, '/');
  if (!path) return '';
  const parts = path.split('/').filter(Boolean);
  const leaf = parts[parts.length - 1] || '';
  return leaf === 'SKILL.md' && parts.length > 1 ? parts[parts.length - 2] || '' : leaf;
}

function sameNameResolutionConflict(conflict) {
  const kind = (conflict?.kind || '').toString().trim().toLowerCase();
  return kind === 'same_name' || kind === 'same_name_scope_conflict';
}

function sameNameProjectSources(conflict) {
  const sources = Array.isArray(conflict?.sources) ? conflict.sources : [];
  return sources.filter((source) => resolutionSourceScope(source) === 'project');
}

function sameNamePersonalSources(conflict) {
  const sources = Array.isArray(conflict?.sources) ? conflict.sources : [];
  return sources.filter((source) => resolutionSourceScope(source) === 'personal');
}

function sameNameHasProjectSource(conflict) {
  const sources = Array.isArray(conflict?.sources) ? conflict.sources : [];
  return sources.some((source) => resolutionSourceScope(source) === 'project');
}

function firstResolutionSourceID(conflict) {
  const sources = Array.isArray(conflict?.sources) ? conflict.sources : [];
  return resolutionSourceID(sources[0]);
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

function sameNameSourceShortText(source, includeSourceLeaf = false) {
  if (resolutionSourceScope(source) === 'project') {
    const leaf = includeSourceLeaf ? resolutionSourcePathLeaf(source) || resolutionSourceID(source).replace(/^project\//, '') : '';
    return leaf ? `项目共享版本：${leaf}` : '项目共享版本';
  }
  return sameNamePersonalVersionText(source, true);
}

function sameNameProjectVersionEntry(source, multipleProjectSources = false) {
  const leaf = multipleProjectSources ? resolutionSourcePathLeaf(source) || resolutionSourceID(source).replace(/^project\//, '') : '';
  return {
    action: 'keep_selected',
    label: leaf ? `用项目共享版本：${leaf}，删除其他版本` : '用项目共享版本，删除其他版本',
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

function personalDeletedDriftResolutionConflict(conflict) {
  return (
    (conflict?.kind || '').toString().trim().toLowerCase() === 'canonical_deleted_with_drift'
    && (conflict?.scope || '').toString().trim().toLowerCase() === 'personal'
  );
}

function externalPersonalProjectResolutionConflict(conflict) {
  return (conflict?.kind || '').toString().trim().toLowerCase() === 'external_personal_project_same_name';
}

function resolutionProviderLabel(provider) {
  return ({
    codex: 'Codex',
    claude: 'Claude',
  }[(provider || '').toString().trim().toLowerCase()] || (provider || '').toString().trim());
}

function resolutionProviderEntryLabel(entry) {
  const label = (entry?.display_label || '').toString().trim();
  if (label) return label;
  const group = Array.isArray(entry?.provider_group) ? entry.provider_group.map(resolutionProviderLabel).filter(Boolean) : [];
  if (group.length > 0) return group.join('、');
  return resolutionProviderLabel(entry?.provider || entry?.source_provider) || '外部版本';
}

function resolutionProviderEntries(conflict) {
  const entries = Array.isArray(conflict?.provider_entries) ? conflict.provider_entries : [];
  if (entries.length > 0) return entries;
  const provider = (conflict?.provider || conflict?.source_provider || '').toString().trim();
  if (!provider) return [{}];
  return [{
    provider,
    source_path_id: conflict?.source_path_id || conflict?.sourcePathId || '',
  }];
}

function resolutionActionEntries(conflict) {
  const actions = (Array.isArray(conflict?.available_actions) ? conflict.available_actions : [])
    .filter((action) => !resolutionActionUnsupported(action));
  if (personalDeletedDriftResolutionConflict(conflict)) {
    return actions.map((action) => ({
      action,
      label: ({
        sync_back_to_personal: '继续私人使用',
        confirm_delete_drifted_mirror: '使用项目共享版本，删除旧私人版本',
      }[action] || resolutionActionLabel(action)),
      help: resolutionActionHelp(action),
    }));
  }
  if (externalPersonalProjectResolutionConflict(conflict)) {
    const allowed = new Set(['view_diff', 'use_project_shared_skill', 'use_external_provider_skill', 'save_as_new_personal_skill']);
    return (
      actions
      .filter((action) => allowed.has(action))
      .map((action) => ({
        action,
        label: ({
          use_project_shared_skill: '使用项目共享版本，删除旧私人版本',
          use_external_provider_skill: '继续私人使用，替换项目共享版本',
        }[action] || resolutionActionLabel(action)),
        help: resolutionActionHelp(action),
      }))
    );
  }
  if (!sameNameResolutionConflict(conflict)) {
    return actions.map((action) => ({ action, help: resolutionActionHelp(action) }));
  }
  const entries = [];
  const personalSources = sameNamePersonalSources(conflict);
  const projectSources = sameNameProjectSources(conflict);
  const hasProjectSource = sameNameHasProjectSource(conflict);
  if (actions.includes('keep_selected')) {
    projectSources.forEach((source) => entries.push(sameNameProjectVersionEntry(source, projectSources.length > 1)));
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
  return entries.length > 0 ? entries : actions.map((action) => ({ action, help: resolutionActionHelp(action) }));
}

function resolutionActionEntryLabel(entry) {
  return entry?.label || resolutionActionLabel(entry?.action || entry);
}

function resolutionActionEntryHelp(entry) {
  return entry?.help || resolutionActionHelp(entry?.action || entry);
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

function resolutionSameNamePayloadFields(conflict, action, entry = null) {
  switch (action) {
    case 'rename_personal':
    case 'keep_selected': {
      const sources = Array.isArray(conflict?.sources) ? conflict.sources : [];
      const selected = entry?.source
        || sources.find((source) => resolutionSourceScope(source) === 'personal')
        || sources.find((source) => resolutionSourceScope(source) === 'project');
      const keepSourceID = resolutionSourceID(selected) || firstResolutionSourceID(conflict);
      return keepSourceID ? { keep_source_id: keepSourceID } : {};
    }
    case 'merge_manually': {
      const mergeContentHash = (conflict?.merge_content_hash || conflict?.mergeContentHash || '').toString().trim();
      return {
        keep_source_id: firstResolutionSourceID(conflict),
        merge_content_hash: mergeContentHash,
      };
    }
    default:
      return {};
  }
}

function resolutionActionAutoApplies(action) {
  return action === 'keep_selected';
}

function resolutionActionAutoAppliesForConflict(action, conflict) {
  if (resolutionActionAutoApplies(action)) return true;
  if (action === 'rename_personal') return true;
  if (externalPersonalProjectResolutionConflict(conflict)) {
    return (
      action === 'use_project_shared_skill'
      || action === 'use_external_provider_skill'
      || action === 'save_as_new_personal_skill'
    );
  }
  return false;
}

function resolutionApplyKey(conflict, action, entry = null) {
  const source = (
    entry?.source_path_id
    || entry?.sourcePathId
    || entry?.provider
    || entry?.sourceID
    || resolutionSourceID(entry?.source)
    || ''
  ).toString().trim();
  return `${conflict?.conflict_id || conflict?.conflictId || ''}:${source}:${action || ''}`;
}

function previewItemPaths(item) {
  return [
    ['来源', item?.source_path || item?.sourcePath],
    ['目标', item?.target_path || item?.targetPath],
  ].map(([label, value]) => ({ label, value: (value || '').toString().trim() }))
    .filter((itemPath) => itemPath.value);
}

function SkillsPage({ projectPath, refreshKey = 0, resolveLaunchPreferences }) {
  const model = useSkillsPageModel({ projectPath, refreshKey, resolveLaunchPreferences });
  return <SkillsPageView model={model} />;
}

function useSkillsPageModel({ projectPath, refreshKey, resolveLaunchPreferences }) {
  const projectCwd = optionalSettingsCwd(projectPath);
  const [query, setQuery] = useState('');
  const [scopeFilter, setScopeFilter] = useState('all');
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const dashboard = useSkillsDashboard(projectCwd, refreshKey);
  const filters = useSkillsFilters(dashboard.items, query, scopeFilter);
  const editor = useSkillEditor({ projectPath, refreshSkillSurface: dashboard.refreshSkillSurface, resolveLaunchPreferences, setError, setNotice });
  const resolution = useSkillResolution({ projectPath, refreshSkillSurface: dashboard.refreshSkillSurface, resolutionConflicts: dashboard.resolutionConflicts, setError, setNotice });
  const resetResolution = resolution.reset;
  useEffect(() => { setError(''); setNotice(''); resetResolution(); }, [projectCwd, resetResolution]);
  return { dashboard, editor, error, filters, isProjectPending: !projectCwd, notice, query, resolution, scopeFilter, setQuery, setScopeFilter };
}

function useSkillsDashboard(projectCwd, refreshKey) {
  const queryClient = useQueryClient();
  const skillRefreshKey = Number(refreshKey || 0);
  const skillsQuery = useQuery({ queryKey: dashboardQueryKey(projectCwd, 'skills'), queryFn: () => fetchSkillsDashboard(projectCwd), enabled: Boolean(projectCwd) });
  const resolutionsQuery = useQuery({ queryKey: dashboardQueryKey(projectCwd, 'skill-resolutions'), queryFn: () => fetchSkillResolutionsDashboard(projectCwd), enabled: Boolean(projectCwd) });
  const items = useMemo(() => (Array.isArray(skillsQuery.data) ? skillsQuery.data : []), [skillsQuery.data]);
  const resolutionConflicts = useMemo(() => (Array.isArray(resolutionsQuery.data) ? resolutionsQuery.data : []), [resolutionsQuery.data]);
  const refreshSkillSurface = useCallback(async () => {
    if (!projectCwd) return;
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: dashboardQueryKey(projectCwd, 'skills') }),
      queryClient.invalidateQueries({ queryKey: dashboardQueryKey(projectCwd, 'skill-resolutions') }),
    ]);
  }, [projectCwd, queryClient]);
  const retrySkillSurface = useCallback(async () => {
    if (!projectCwd) return;
    await Promise.all([skillsQuery.refetch(), resolutionsQuery.refetch()]);
  }, [projectCwd, resolutionsQuery, skillsQuery]);
  useSkillSurfaceRefresh(projectCwd, refreshSkillSurface, skillRefreshKey);
  return skillsDashboardState({ items, projectCwd, resolutionConflicts, resolutionsQuery, retrySkillSurface, refreshSkillSurface, skillsQuery });
}

function skillsDashboardState({ items, projectCwd, resolutionConflicts, resolutionsQuery, retrySkillSurface, refreshSkillSurface, skillsQuery }) {
  const hasSnapshot = Array.isArray(skillsQuery.data);
  const syncErrorText = skillsSyncErrorText(skillsQuery, resolutionsQuery);
  return {
    items,
    isInitialLoading: Boolean(projectCwd) && skillsQuery.isPending && !hasSnapshot,
    refreshSkillSurface,
    resolutionConflicts,
    retrySkillSurface,
    showBlockingSyncError: Boolean(syncErrorText && !hasSnapshot),
    showCachedSyncError: Boolean(syncErrorText && hasSnapshot),
    syncErrorText,
  };
}

function skillsSyncErrorText(skillsQuery, resolutionsQuery) {
  if (skillsQuery.error) return errorMessage(skillsQuery.error);
  if (resolutionsQuery.error) return '读取技能冲突失败：' + errorMessage(resolutionsQuery.error);
  return '';
}

function useSkillSurfaceRefresh(projectCwd, refreshSkillSurface, skillRefreshKey) {
  useEffect(() => {
    if (skillRefreshKey > 0 && projectCwd) void refreshSkillSurface();
  }, [projectCwd, refreshSkillSurface, skillRefreshKey]);
  useEffect(() => skillSurfaceFocusHandler(projectCwd, refreshSkillSurface), [projectCwd, refreshSkillSurface]);
}

function skillSurfaceFocusHandler(projectCwd, refreshSkillSurface) {
  if (!projectCwd) return undefined;
  const refreshWhenVisible = () => {
    if (typeof document !== 'undefined' && document.visibilityState === 'hidden') return;
    void refreshSkillSurface();
  };
  const handleVisibilityChange = () => {
    if (typeof document === 'undefined' || document.visibilityState === 'visible') refreshWhenVisible();
  };
  window.addEventListener('focus', refreshWhenVisible);
  document.addEventListener('visibilitychange', handleVisibilityChange);
  return () => { window.removeEventListener('focus', refreshWhenVisible); document.removeEventListener('visibilitychange', handleVisibilityChange); };
}

function useSkillsFilters(items, query, scopeFilter) {
  const counts = useMemo(() => skillCounts(items), [items]);
  const filteredItems = useMemo(() => filterSkills(items, query, scopeFilter), [items, query, scopeFilter]);
  const scopeOptions = useMemo(() => ([['personal', '私人使用 ' + counts.personal], ['project', '项目共享 ' + counts.project], ['all', '全部 ' + counts.all]]), [counts]);
  return { counts, filteredItems, scopeOptions };
}

function skillCounts(items) {
  return items.reduce((acc, item) => {
    acc.all += 1;
    if (item.scope === 'personal') acc.personal += 1;
    else acc.project += 1;
    return acc;
  }, { all: 0, personal: 0, project: 0 });
}

function filterSkills(items, query, scopeFilter) {
  const keyword = query.trim().toLowerCase();
  return items.filter((item) => skillMatchesFilter(item, keyword, scopeFilter));
}

function skillMatchesFilter(item, keyword, scopeFilter) {
  if (scopeFilter !== 'all' && item.scope !== scopeFilter) return false;
  if (!keyword) return true;
  return [item.name, item.title, item.description, item.summary, item.dir, ...item.tags].join(' ').toLowerCase().includes(keyword);
}

function useSkillEditor({ projectPath, refreshSkillSurface, resolveLaunchPreferences, setError, setNotice }) {
  const [state, setState] = useState(defaultSkillEditorState);
  const setPatch = useCallback((patch) => setState((current) => ({ ...current, ...patch })), []);
  const setForm = useCallback((updater) => setState((current) => ({ ...current, editorForm: typeof updater === 'function' ? updater(current.editorForm) : updater })), []);
  const actions = useMemo(() => skillEditorActions({ projectPath, refreshSkillSurface, resolveLaunchPreferences, setError, setForm, setNotice, setPatch, state }), [projectPath, refreshSkillSurface, resolveLaunchPreferences, setError, setForm, setNotice, setPatch, state]);
  return { ...state, ...actions, setForm };
}

function defaultSkillEditorState() {
  return { activeSkillPath: '', deleteTarget: null, deleting: false, editorForm: emptySkillForm(), editorOpen: false, importScopeOpen: false, importing: false, saving: false, skillFiles: [], summarySuggestion: '', summarySuggesting: false };
}

function skillEditorActions(ctx) {
  return {
    applySummary: () => {
      ctx.setForm((form) => ({ ...form, description: ctx.state.summarySuggestion }));
      ctx.setPatch({ summarySuggestion: '' });
    },
    closeDelete: () => ctx.setPatch({ deleteTarget: null }),
    closeEditor: () => ctx.setPatch({ editorOpen: false }),
    closeImportScope: () => ctx.setPatch({ importScopeOpen: false }),
    confirmDeleteSkill: () => confirmDeleteSkill(ctx),
    confirmImportScope: (scope) => confirmImportScope(ctx, scope),
    onDeleteSkill: (skill) => ctx.setPatch({ deleteTarget: skill }),
    openCreateEditor: () => openCreateSkillEditor(ctx),
    openEditSkill: (skill) => openEditSkill(ctx, skill),
    openImportScope: () => ctx.setPatch({ importScopeOpen: true }),
    openSkillFile: (file) => openSkillFile(ctx, file),
    saveEditor: () => saveSkillEditor(ctx),
    suggestSummary: () => suggestSkillSummaryForEditor(ctx),
  };
}

function openCreateSkillEditor(ctx) {
  ctx.setPatch({ activeSkillPath: '', editorForm: emptySkillForm(), editorOpen: true, skillFiles: [], summarySuggestion: '' });
  ctx.setError('');
  ctx.setNotice('');
}

async function openEditSkill(ctx, skill) {
  if (!skill?.dir) { ctx.setError('skills/local/read: path is required'); return; }
  const skillPath = skill.dir.replace(/[\\/]+$/g, '') + '/SKILL.md';
  ctx.setError(''); ctx.setNotice(''); ctx.setPatch({ summarySuggestion: '' });
  try {
    const cwd = normalizeSettingsCwd(ctx.projectPath);
    const [rawSkill, rawFiles] = await Promise.all([readSkill({ cwd, path: skillPath }), listSkillFiles({ cwd, dir: skill.dir })]);
    ctx.setPatch({ activeSkillPath: skillPath, editorForm: skillFormFromRaw(rawSkill, skill), editorOpen: true, skillFiles: normalizeSkillFileList(rawFiles) });
  } catch (err) {
    ctx.setError('读取技能失败：' + (err.message || String(err)));
  }
}

function skillFormFromRaw(rawSkill, skill) {
  const content = (rawSkill?.skill?.content || '').toString();
  const parsed = parseSkillMarkdown(content, skill.name);
  return { name: parsed.name || skill.name, displayName: parsed.displayName || skill.title || '', description: parsed.description || skill.description || '', keywords: listToText(parsed.triggerWords.length > 0 ? parsed.triggerWords : skill.tags), body: parsed.body, scope: skill.scope, personalType: skill.personalType };
}

async function openSkillFile(ctx, file) {
  const path = (file?.path || '').toString().trim();
  if (!path) return;
  ctx.setError('');
  try {
    const raw = await readSkill({ cwd: normalizeSettingsCwd(ctx.projectPath), path });
    const content = (raw?.skill?.content || '').toString();
    ctx.setForm((form) => skillFormForOpenedFile(path, content, form));
    ctx.setPatch({ activeSkillPath: path });
  } catch (err) {
    ctx.setError('读取子文件失败：' + (err.message || String(err)));
  }
}

function skillFormForOpenedFile(path, content, form) {
  if (!isMainSkillFile(path)) return { ...form, body: content };
  const parsed = parseSkillMarkdown(content, form.name);
  return { ...form, name: parsed.name || form.name, displayName: parsed.displayName || parsed.name || form.displayName, description: parsed.description, keywords: listToText(parsed.triggerWords), body: parsed.body };
}

async function suggestSkillSummaryForEditor(ctx) {
  ctx.setPatch({ summarySuggesting: true, summarySuggestion: '' });
  ctx.setError('');
  try {
    const cwd = normalizeSettingsCwd(ctx.projectPath);
    const launchPreferences = typeof ctx.resolveLaunchPreferences === 'function' ? await ctx.resolveLaunchPreferences(cwd) : null;
    const description = await suggestSkillSummary(skillSummaryRequest(cwd, ctx.state.editorForm, launchPreferences));
    ctx.setPatch({ summarySuggestion: normalizeSummarySuggestion(description) });
  } catch (err) {
    ctx.setError('生成简介失败：' + (err.message || String(err)));
  } finally {
    ctx.setPatch({ summarySuggesting: false });
  }
}

function skillSummaryRequest(cwd, form, launchPreferences) {
  return { cwd, name: form.displayName || form.name, description: form.description, content: form.body, scenario_words: wordListFromText(form.keywords), scope: form.scope, provider: textValue(launchPreferences?.modelProvider || launchPreferences?.provider), model: textValue(launchPreferences?.model), codexModelProvider: textValue(launchPreferences?.config?.codexModelProvider) };
}

async function saveSkillEditor(ctx) {
  ctx.setPatch({ saving: true });
  ctx.setError(''); ctx.setNotice('');
  try {
    await writeSkill(skillSavePayload(normalizeSettingsCwd(ctx.projectPath), ctx.state));
    ctx.setPatch({ editorOpen: false });
    await ctx.refreshSkillSurface();
    ctx.setNotice('已保存');
  } catch (err) {
    ctx.setError('保存失败：' + (err.message || String(err)));
  } finally {
    ctx.setPatch({ saving: false });
  }
}

function skillSavePayload(cwd, state) {
  const isMain = !state.activeSkillPath || isMainSkillFile(state.activeSkillPath);
  const displayName = state.editorForm.displayName.trim();
  const name = state.editorForm.name.trim() || skillNameFromDisplayName(displayName);
  if (isMain && !displayName) throw new Error('请先填写技能名称');
  if (isMain && !name) throw new Error('技能名称必须包含中文、英文或数字');
  const normalizedForm = isMain ? { ...state.editorForm, name, displayName } : state.editorForm;
  return { cwd, path: isMain ? (state.activeSkillPath || name) : state.activeSkillPath, content: isMain ? buildSkillMarkdown(normalizedForm) : state.editorForm.body, scope: state.editorForm.scope, personal_type: state.editorForm.scope === 'personal' ? (state.editorForm.personalType || 'user') : '' };
}

async function confirmDeleteSkill(ctx) {
  const skill = ctx.state.deleteTarget;
  const skillName = (skill?.name || '').toString().trim();
  if (!skillName) { ctx.setError('skills/local/delete: name is required'); return; }
  ctx.setPatch({ deleting: true }); ctx.setError(''); ctx.setNotice('');
  try {
    await deleteSkill({ cwd: normalizeSettingsCwd(ctx.projectPath), name: skillName, scope: skill.scope, personal_type: skill.personalType });
    ctx.setPatch({ deleteTarget: null });
    await ctx.refreshSkillSurface();
    ctx.setNotice('已删除 ' + skill.title);
  } catch (err) {
    ctx.setError(err.message || String(err));
  } finally {
    ctx.setPatch({ deleting: false });
  }
}

async function confirmImportScope(ctx, scope) {
  ctx.setPatch({ importing: true }); ctx.setError(''); ctx.setNotice('');
  try {
    const paths = await selectProjectDirs();
    ctx.setPatch({ importScopeOpen: false });
    if (!Array.isArray(paths) || paths.length === 0) { ctx.setNotice('未选择目录'); return; }
    await importSkillDirectories({ cwd: normalizeSettingsCwd(ctx.projectPath), paths, scope, personal_type: scope === 'personal' ? 'imported' : '' });
    await ctx.refreshSkillSurface();
    ctx.setNotice('导入完成');
  } catch (err) {
    ctx.setError('导入目录失败：' + (err.message || String(err)));
  } finally {
    ctx.setPatch({ importing: false });
  }
}

function useSkillResolution({ projectPath, refreshSkillSurface, resolutionConflicts, setError, setNotice }) {
  const [preview, setPreview] = useState(null);
  const [namePrompt, setNamePrompt] = useState(null);
  const [nameInput, setNameInput] = useState('');
  const [actioning, setActioning] = useState('');
  const reset = useCallback(() => { setPreview(null); setNamePrompt(null); setNameInput(''); }, []);
  const runAction = useCallback((conflict, actionOrEntry, entry = null, newName = '') => runResolutionPipeline({ actionOrEntry, actioning, conflict, entry, newName, projectPath, refreshSkillSurface, setActioning, setError, setNameInput, setNamePrompt, setNotice, setPreview }), [actioning, projectPath, refreshSkillSurface, setError, setNotice]);
  const confirmName = useCallback(() => confirmResolutionName({ nameInput, namePrompt, runAction, setError, setNameInput, setNamePrompt }), [nameInput, namePrompt, runAction, setError]);
  const confirmPreview = useCallback(() => confirmResolutionPreview({ preview, refreshSkillSurface, setActioning, setError, setNameInput, setNamePrompt, setNotice, setPreview }), [preview, refreshSkillSurface, setError, setNotice]);
  useEffect(() => { if (resolutionConflicts.length === 0) reset(); }, [reset, resolutionConflicts.length]);
  return { actioning, confirmName, confirmPreview, nameInput, namePrompt, preview, reset, runAction, setNameInput, setNamePrompt, setPreview };
}

async function runResolutionPipeline(ctx) {
  const request = resolutionRequestFromAction(ctx);
  if (!request.ok) return request.value;
  if (request.prompt) { promptResolutionNewName(ctx, request.prompt); return false; }
  return previewAndMaybeApplyResolution(ctx, request.payload, request.action, request.conflict, request.applyKey);
}

function resolutionRequestFromAction({ actionOrEntry, conflict, entry, newName, projectPath }) {
  const conflictID = (conflict?.conflict_id || conflict?.conflictId || '').toString().trim();
  const actionEntry = typeof actionOrEntry === 'string' ? { action: actionOrEntry } : actionOrEntry || {};
  const action = (actionEntry.action || '').toString().trim();
  if (!conflictID || !action) return { ok: false, value: false };
  if (resolutionActionUnsupported(action)) return { ok: false, value: false, unsupported: action };
  const providerEntry = resolutionActionEntryTarget(actionEntry, entry || resolutionProviderEntries(conflict)[0] || {});
  const applyKey = resolutionApplyKey(conflict, action, providerEntry);
  const trimmedNewName = (newName || '').toString().trim();
  if (requiresResolutionNewName(action) && !trimmedNewName) return { ok: true, prompt: { action, applyKey, conflict, entry: providerEntry } };
  return { ok: true, action, applyKey, conflict, payload: resolutionPayload({ action, actionEntry, conflict, conflictID, projectPath, providerEntry, trimmedNewName }) };
}

function resolutionPayload({ action, actionEntry, conflict, conflictID, projectPath, providerEntry, trimmedNewName }) {
  const payload = { cwd: normalizeSettingsCwd(projectPath), conflict_id: conflictID, action, name: conflict?.name || conflict?.skill_name || '', scope: conflict?.scope || '', personal_type: conflict?.personal_type || conflict?.personalType || '', provider: providerEntry?.provider || conflict?.provider || '', source_provider: providerEntry?.provider || conflict?.source_provider || conflict?.provider || '', source_path_id: providerEntry?.source_path_id || providerEntry?.sourcePathId || conflict?.source_path_id || '', ...resolutionSameNamePayloadFields(conflict, action, actionEntry) };
  if (trimmedNewName) payload.new_name = trimmedNewName;
  return payload;
}

function promptResolutionNewName(ctx, prompt) {
  if (resolutionActionUnsupported(prompt.action)) {
    ctx.setNotice('暂不支持该技能冲突操作：' + resolutionActionLabel(prompt.action));
    return;
  }
  ctx.setPreview(null);
  ctx.setNamePrompt({ ...prompt, autoApply: resolutionActionAutoAppliesForConflict(prompt.action, prompt.conflict) });
  ctx.setNameInput(defaultResolutionNewName(prompt.conflict, prompt.action));
  ctx.setNotice('请输入新技能名称后继续。');
}

async function previewAndMaybeApplyResolution(ctx, payload, action, conflict, applyKey) {
  ctx.setActioning(applyKey); ctx.setError('');
  let result = false;
  try {
    const preview = await previewSkillResolution(payload);
    const items = Array.isArray(preview?.items) ? preview.items : [];
    if (resolutionActionAutoAppliesForConflict(action, conflict)) {
      result = await autoApplyResolutionPreview(ctx, payload, items);
    } else {
      ctx.setPreview({ ...preview, action, payload, items, requiresApply: resolutionRequiresApply(action) });
      ctx.setNotice(isResolutionViewAction(action) ? '已生成处理预览' : '已生成处理预览，请确认应用。');
      result = true;
    }
  } catch (err) {
    ctx.setError('处理技能冲突失败：' + (err.message || String(err)));
  } finally {
    ctx.setActioning('');
  }
  return result;
}

async function autoApplyResolutionPreview(ctx, payload, items) {
  const proof = items[0];
  if (!proof?.preview_id || !proof?.preview_hash) throw new Error('缺少处理预览凭据');
  await applySkillResolution(resolutionApplyPayload(payload, proof));
  ctx.setPreview(null); ctx.setNamePrompt(null); ctx.setNameInput('');
  await ctx.refreshSkillSurface();
  ctx.setNotice('已处理技能冲突');
  return true;
}

function resolutionApplyPayload(payload, proof) {
  return { ...payload, provider: proof.provider || payload.provider, source_provider: proof.source_provider || payload.source_provider, source_path_id: proof.source_path_id || payload.source_path_id, preview_id: proof.preview_id, preview_hash: proof.preview_hash };
}

async function confirmResolutionName({ nameInput, namePrompt, runAction, setError, setNameInput, setNamePrompt }) {
  if (!namePrompt) return;
  const newName = nameInput.trim();
  if (!newName) { setError('请输入新技能名称。'); return; }
  if (await runAction(namePrompt.conflict, namePrompt.action, namePrompt.entry, newName)) {
    setNamePrompt(null); setNameInput('');
  }
}

async function confirmResolutionPreview(ctx) {
  const proof = Array.isArray(ctx.preview?.items) ? ctx.preview.items[0] : null;
  if (!ctx.preview?.requiresApply || !proof?.preview_id || !proof?.preview_hash) return;
  ctx.setActioning('confirm');
  try {
    await applySkillResolution(resolutionApplyPayload(ctx.preview.payload, proof));
    ctx.setPreview(null); ctx.setNamePrompt(null); ctx.setNameInput('');
    await ctx.refreshSkillSurface();
    ctx.setNotice('已处理技能冲突');
  } catch (err) {
    ctx.setError('应用技能冲突处理失败：' + (err.message || String(err)));
  } finally {
    ctx.setActioning('');
  }
}

function SkillsPageView({ model }) {
  return (
    <section className="console-page">
      <PageHeader icon={Sparkles} title="技能管理" />
      <div className="subhead">技能列表</div>
      <SkillsToolbar model={model} />
      <SkillFilter filters={model.filters} scopeFilter={model.scopeFilter} setScopeFilter={model.setScopeFilter} />
      <SkillsStatus model={model} />
      <SkillResolutionPanel model={model} />
      <SkillGrid model={model} />
      <SkillModals model={model} />
    </section>
  );
}

function SkillsToolbar({ model }) {
  return (
    <div className="skills-toolbar">
      <button type="button" onClick={model.editor.openImportScope} disabled={model.editor.importing}>批量导入技能目录</button>
      <button type="button" className="ghost" onClick={model.editor.openCreateEditor}>新建技能</button>
      <label><Search size={18} /><input value={model.query} onChange={(event) => model.setQuery(event.target.value)} placeholder="搜索技能名称、简介、关键词..." /></label>
    </div>
  );
}

function SkillFilter({ filters, scopeFilter, setScopeFilter }) {
  return (
    <div className="skill-filter">
      {filters.scopeOptions.map(([value, label]) => <button key={value} type="button" className={scopeFilter === value ? 'active' : ''} onClick={() => setScopeFilter(value)}>{label}</button>)}
    </div>
  );
}

function SkillsStatus({ model }) {
  return (
    <>
      {model.isProjectPending ? <p className="console-message">正在连接本地项目...</p> : null}
      {model.dashboard.isInitialLoading ? <p className="console-message">加载技能中...</p> : null}
      {model.notice ? <p className="settings-status">{model.notice}</p> : null}
      {model.dashboard.showCachedSyncError ? <CachedSkillSyncError dashboard={model.dashboard} /> : null}
      {model.dashboard.showBlockingSyncError ? <RetryableSyncError className="danger-text skills-sync-alert" message={model.dashboard.syncErrorText} onRetry={model.dashboard.retrySkillSurface} /> : null}
      {model.error ? <p className="danger-text" role="alert">{model.error}</p> : null}
    </>
  );
}

function CachedSkillSyncError({ dashboard }) {
  return (
    <div className="danger-text skills-sync-alert" role="alert">
      <span>同步失败，显示的是上次成功的数据：{dashboard.syncErrorText}</span>
      <button type="button" className="ghost" onClick={() => { void dashboard.retrySkillSurface(); }}>重试同步</button>
    </div>
  );
}

function SkillResolutionPanel({ model }) {
  const conflicts = model.dashboard.resolutionConflicts;
  if (!conflicts.length) return null;
  return (
    <section className="skills-resolution-panel">
      <strong>发现 {conflicts.length} 个技能冲突，需要处理后再使用。</strong>
      {conflicts.map((conflict, index) => <SkillResolutionConflict conflict={conflict} index={index} key={(conflict.conflict_id || conflict.conflictId || index).toString()} resolution={model.resolution} />)}
      {model.resolution.preview ? <SkillResolutionPreview resolution={model.resolution} /> : null}
    </section>
  );
}

function SkillResolutionConflict({ conflict, index, resolution }) {
  const conflictID = (conflict.conflict_id || conflict.conflictId || index).toString();
  const promptConflictID = (resolution.namePrompt?.conflict?.conflict_id || resolution.namePrompt?.conflict?.conflictId || '').toString();
  const promptApplies = resolution.namePrompt && promptConflictID === (conflict.conflict_id || conflict.conflictId || '').toString();
  return (
    <article className="skills-resolution-item">
      <header><h3>{conflict.name || conflict.skill_name || '未命名技能'} · {resolutionKindLabel(conflict.kind)}</h3><span>{scopeLabel(scopeForSkill(conflict))}</span></header>
      {resolutionProviderEntries(conflict).map((entry, sourceIndex) => <SkillResolutionActionRow conflict={conflict} conflictID={conflictID} providerEntry={entry} resolution={resolution} sourceIndex={sourceIndex} key={conflictID + ':' + sourceIndex + ':' + resolutionProviderEntryLabel(entry)} />)}
      {promptApplies ? <SkillResolutionNamePrompt resolution={resolution} /> : null}
    </article>
  );
}

function SkillResolutionActionRow({ conflict, conflictID, providerEntry, resolution, sourceIndex }) {
  const providerEntries = resolutionProviderEntries(conflict);
  return (
    <div className="skills-resolution-actions">
      {providerEntries.length > 1 ? <span className="skills-resolution-source">{resolutionProviderEntryLabel(providerEntry)}</span> : null}
      {resolutionActionEntries(conflict).map((actionEntry, actionIndex) => <SkillResolutionActionButton actionEntry={actionEntry} actionIndex={actionIndex} conflict={conflict} providerEntry={providerEntry} resolution={resolution} key={conflictID + ':' + sourceIndex + ':' + actionIndex} />)}
    </div>
  );
}

function SkillResolutionActionButton({ actionEntry, actionIndex, conflict, providerEntry, resolution }) {
  const action = (actionEntry.action || actionEntry).toString();
  const targetEntry = resolutionActionEntryTarget(actionEntry, providerEntry);
  const applyKey = resolutionApplyKey(conflict, action, targetEntry);
  return (
    <button key={applyKey + ':' + actionIndex} type="button" title={resolutionActionEntryHelp(actionEntry)} onClick={() => { void resolution.runAction(conflict, actionEntry, providerEntry); }} disabled={resolution.actioning === applyKey}>
      {resolution.actioning === applyKey ? '处理中...' : resolutionActionEntryLabel(actionEntry)}
    </button>
  );
}

function SkillResolutionNamePrompt({ resolution }) {
  return (
    <div className="skills-resolution-name-field">
      <label>新技能名称<input value={resolution.nameInput} onChange={(event) => resolution.setNameInput(event.target.value)} aria-label="新技能名称" /></label>
      <button type="button" onClick={() => { void resolution.confirmName(); }} disabled={resolution.actioning === resolution.namePrompt.applyKey}>{resolutionPromptActionLabel(resolution)}</button>
      <button type="button" className="ghost" onClick={() => { resolution.setNamePrompt(null); resolution.setNameInput(''); }}>取消</button>
    </div>
  );
}

function resolutionPromptActionLabel(resolution) {
  if (resolution.actioning === resolution.namePrompt?.applyKey) return resolution.namePrompt?.autoApply ? '处理中...' : '生成中...';
  return resolution.namePrompt?.autoApply ? '确认处理' : '生成预览';
}

function SkillResolutionPreview({ resolution }) {
  return (
    <article className="skills-resolution-preview">
      <header><h3>{resolutionActionLabel(resolution.preview.action)}</h3>{resolution.preview.requiresApply ? <button type="button" onClick={() => { void resolution.confirmPreview(); }} disabled={resolution.actioning === 'confirm'}>{resolution.actioning === 'confirm' ? '应用中...' : '确认应用'}</button> : null}<button type="button" className="ghost" onClick={() => resolution.setPreview(null)}>取消</button></header>
      {(resolution.preview.items || []).map((item, index) => <SkillResolutionPreviewItem item={item} key={item.preview_id || index} />)}
    </article>
  );
}

function SkillResolutionPreviewItem({ item }) {
  return (
    <div className="skills-resolution-preview-item">
      {previewItemPaths(item).map((pathItem) => <p key={pathItem.label + ':' + pathItem.value}><span>{pathItem.label}</span><code>{pathItem.value}</code></p>)}
    </div>
  );
}

function SkillGrid({ model }) {
  if (!model.isProjectPending && !model.dashboard.isInitialLoading && !model.dashboard.showBlockingSyncError && model.filters.filteredItems.length === 0) return <p className="console-message">暂无技能</p>;
  if (!model.filters.filteredItems.length) return null;
  return <div className="skill-grid">{model.filters.filteredItems.map((skill) => <SkillCard key={skill.id} skill={skill} onEdit={model.editor.openEditSkill} onDelete={model.editor.onDeleteSkill} />)}</div>;
}

function SkillModals({ model }) {
  const editor = model.editor;
  return (
    <>
      {editor.editorOpen ? <SkillEditorDialog editor={editor} /> : null}
      {editor.deleteTarget ? <ConfirmSkillDeleteModal skill={editor.deleteTarget} deleting={editor.deleting} onClose={editor.closeDelete} onConfirm={editor.confirmDeleteSkill} /> : null}
      {editor.importScopeOpen ? <ImportScopeModal importing={editor.importing} onClose={editor.closeImportScope} onConfirm={editor.confirmImportScope} /> : null}
    </>
  );
}

function SkillEditorDialog({ editor }) {
  return (
    <SkillEditorModal
      form={editor.editorForm}
      setForm={editor.setForm}
      activeSkillPath={editor.activeSkillPath}
      files={editor.skillFiles}
      summarySuggestion={editor.summarySuggestion}
      summarySuggesting={editor.summarySuggesting}
      saving={editor.saving}
      onSuggestSummary={editor.suggestSummary}
      onApplySummary={editor.applySummary}
      onOpenFile={editor.openSkillFile}
      onClose={editor.closeEditor}
      onSave={editor.saveEditor}
    />
  );
}

function SkillCard({ skill, onEdit, onDelete }) {
  const tags = skill.tags.slice(0, 4);
  const extraTagCount = skill.tags.length - tags.length;
  const descriptionText = (skill.description || '').toString().trim();
  const summaryText = (skill.summary || '').toString().trim();
  const description = descriptionText || summaryText || '暂无描述';
  const shouldShowSummary = Boolean(summaryText && summaryText !== description);

  return (
    <article className="skill-card">
      <header><h3>{skill.title}</h3><span>{scopeLabel(skill.scope)}</span></header>
      <p className="path">{skill.dir || '未提供路径'}</p>
      <p>{description}</p>
      {shouldShowSummary ? <div className="quote">{summaryText}</div> : null}
      <small>关键词</small>
      <div className="tags">
        {tags.length > 0 ? tags.map((tag) => <span key={tag}>{tag}</span>) : <span>暂无关键词</span>}
        {extraTagCount > 0 ? <span>+{extraTagCount}</span> : null}
      </div>
      <footer>
        <button type="button" onClick={() => { void onEdit(skill); }} disabled={!skill.dir}>编辑详情</button>
        <button type="button" className="text-danger" onClick={() => { void onDelete(skill); }} disabled={!skill.name}>删除</button>
      </footer>
    </article>
  );
}

function SkillEditorModal({
  form,
  setForm,
  activeSkillPath,
  files,
  summarySuggestion,
  summarySuggesting,
  saving,
  onSuggestSummary,
  onApplySummary,
  onOpenFile,
  onClose,
  onSave,
}) {
  const isMain = !activeSkillPath || isMainSkillFile(activeSkillPath);
  const modalTitle = activeSkillPath ? '编辑技能' : '新建技能';
  const update = (key) => (event) => setForm((current) => ({ ...current, [key]: event.target.value }));
  const updateDisplayName = (event) => {
    const value = event.target.value;
    setForm((current) => ({
      ...current,
      displayName: value,
      name: activeSkillPath ? current.name : skillNameFromDisplayName(value),
    }));
  };
  const [bodyEditing, setBodyEditing] = useState(!activeSkillPath);
  useEffect(() => {
    setBodyEditing(!activeSkillPath);
  }, [activeSkillPath]);
  return (
    <FocusTrapDialog ariaLabel={modalTitle} className="modal-box skills-editor-modal" closeDisabled={saving} onClose={onClose}>
      <SkillEditorHeader modalTitle={modalTitle} saving={saving} onClose={onClose} />
      <SkillEditorFields
        form={form}
        isMain={isMain}
        summarySuggestion={summarySuggestion}
        summarySuggesting={summarySuggesting}
        update={update}
        updateDisplayName={updateDisplayName}
        setForm={setForm}
        onApplySummary={onApplySummary}
        onSuggestSummary={onSuggestSummary}
      />
      <SkillEditorSubfiles activeSkillPath={activeSkillPath} files={files} onOpenFile={onOpenFile} />
      <SkillEditorBody bodyEditing={bodyEditing} form={form} isMain={isMain} setBodyEditing={setBodyEditing} update={update} />
      <footer>
        <button type="button" className="ghost" onClick={onClose} disabled={saving}>取消</button>
        <button type="button" onClick={() => { void onSave(); }} disabled={saving}>{saving ? '保存中...' : '保存技能'}</button>
      </footer>
    </FocusTrapDialog>
  );
}

function SkillEditorHeader({ modalTitle, saving, onClose }) {
  return (
    <header className="skills-editor-modal-head">
      <div><h2>{modalTitle}</h2><p>你可以修改简介和技能内容。</p></div>
      <button type="button" className="ghost" onClick={onClose} disabled={saving}>关闭</button>
    </header>
  );
}

function SkillEditorFields({ form, isMain, summarySuggestion, summarySuggesting, update, updateDisplayName, setForm, onApplySummary, onSuggestSummary }) {
  return (
    <div className="form-grid">
      <label className="wide">技能名称<input value={form.displayName} onChange={updateDisplayName} disabled={!isMain} /></label>
      <SkillDescriptionField form={form} isMain={isMain} summarySuggestion={summarySuggestion} summarySuggesting={summarySuggesting} update={update} onApplySummary={onApplySummary} onSuggestSummary={onSuggestSummary} />
      <div className="skills-field">
        <span>使用范围</span>
        <div className="skills-scope-segmented" role="group" aria-label="使用范围">
          <button type="button" className={form.scope === 'project' ? 'active' : ''} disabled={!isMain} onClick={() => setForm((current) => ({ ...current, scope: 'project' }))}>项目共享</button>
          <button type="button" className={form.scope === 'personal' ? 'active' : ''} disabled={!isMain} onClick={() => setForm((current) => ({ ...current, scope: 'personal' }))}>私人使用</button>
        </div>
      </div>
      <label>关键词<input value={form.keywords} onChange={update('keywords')} disabled={!isMain} aria-label="关键词" /></label>
    </div>
  );
}

function SkillDescriptionField({ form, isMain, summarySuggestion, summarySuggesting, update, onApplySummary, onSuggestSummary }) {
  return (
    <div className="skills-field wide">
      <div className="skills-editor-label-row">
        <label htmlFor="skills-description-input">技能简介</label>
        <button type="button" className="ghost" onClick={() => { void onSuggestSummary(); }} disabled={!isMain || summarySuggesting || (!form.name.trim() && !form.body.trim())}>
          {summarySuggesting ? '生成中' : '帮我生成'}
        </button>
      </div>
      <input id="skills-description-input" value={form.description} onChange={update('description')} disabled={!isMain} aria-label="技能简介" />
      {summarySuggestion ? <div className="skills-inline-tip skills-summary-suggestion" data-testid="skills-summary-suggestion"><span>建议：{summarySuggestion}</span><button type="button" onClick={onApplySummary}>采用</button></div> : null}
      <div className="skills-inline-tip">建议写成“当你需要……时使用”。</div>
    </div>
  );
}

function SkillEditorSubfiles({ activeSkillPath, files, onOpenFile }) {
  if (!files.some((file) => !file.isMain)) return null;
  return (
    <div className="skills-subfiles-wrap">
      <span>附加内容</span>
      <div className="skills-subfiles">
        {files.map((file) => (
          <button key={file.path} type="button" className={file.path === activeSkillPath ? 'active' : ''} onClick={() => { void onOpenFile(file); }}>
            {file.name}{file.isMain ? ' · 主要文件' : ''}
          </button>
        ))}
      </div>
      <div className="skills-inline-tip">这里是这个技能附带的示例、模板或脚本。</div>
    </div>
  );
}

function SkillEditorBody({ bodyEditing, form, isMain, setBodyEditing, update }) {
  return (
    <div className="skills-body-field">
      <div className="skills-body-head">
        <span>{isMain ? '技能内容' : '关联文件内容'}</span>
        {bodyEditing ? <button type="button" className="ghost" onClick={() => setBodyEditing(false)}>预览正文</button> : <button type="button" onClick={() => setBodyEditing(true)}>编辑正文</button>}
      </div>
      {bodyEditing ? <textarea value={form.body} onChange={update('body')} aria-label={isMain ? '技能内容' : '关联文件内容'} /> : <div className="skills-body-preview" data-testid="skills-editor-body-preview"><SkillMarkdownPreview content={form.body} /></div>}
      <div className="skills-inline-tip">点击“编辑正文”展开编辑；切回“预览正文”查看效果。</div>
    </div>
  );
}

function ConfirmSkillDeleteModal({ skill, deleting, onClose, onConfirm }) {
  return (
    <FocusTrapDialog ariaLabel="删除技能" closeDisabled={deleting} onClose={onClose}>
        <header><h2>删除技能</h2><button type="button" className="ghost" onClick={onClose} disabled={deleting}>关闭</button></header>
        <p>确定删除技能 “{skill.name}” 吗？该操作会删除技能目录及其资源文件，无法恢复。</p>
        <p className="path">{skill.dir || '-'}</p>
        <footer>
          <button type="button" className="ghost" onClick={onClose} disabled={deleting}>取消</button>
          <button type="button" className="text-danger" onClick={() => { void onConfirm(); }} disabled={deleting}>{deleting ? '删除中...' : '确认删除'}</button>
        </footer>
    </FocusTrapDialog>
  );
}

function ImportScopeModal({ importing, onClose, onConfirm }) {
  return (
    <FocusTrapDialog ariaLabel="导入技能" closeDisabled={importing} onClose={onClose}>
        <header><h2>导入技能</h2><button type="button" className="ghost" onClick={onClose} disabled={importing}>关闭</button></header>
        <p>这些技能导入后给谁使用？</p>
        <footer>
          <button type="button" className="ghost" onClick={onClose} disabled={importing}>取消</button>
          <button type="button" onClick={() => { void onConfirm('personal'); }} disabled={importing}>私人使用</button>
          <button type="button" onClick={() => { void onConfirm('project'); }} disabled={importing}>项目共享</button>
        </footer>
    </FocusTrapDialog>
  );
}

export { SkillsPage };
