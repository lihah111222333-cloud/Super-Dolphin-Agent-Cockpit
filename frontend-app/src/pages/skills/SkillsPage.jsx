import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Database, Eye, FileText, MousePointer2, Pencil, Power, PowerOff, RefreshCw, Search, Sparkles, Trash2, Upload } from 'lucide-react';
import { FocusTrapDialog } from '../../shared/ui/FocusTrapDialog.jsx';
import { APP_COPY } from '../../shared/i18n/appI18n.js';
import { applySkillResolution, createSkill, deleteDatasourceDocument, deleteSkill, getDashboardPage, getDatasourceDocument, importDatasourceLocalFile, importSkillDirectories, listDatasourceDocuments, listMCPServers, listSkillFiles, listSkillResolutions, listSkillTools, previewSkillResolution, readSkill, selectFiles, selectProjectDirs, startPlaywrightMCPServer, startSQLiteMCPServer, stopPlaywrightMCPServer, stopSQLiteMCPServer, suggestSkillSummary, updateDatasourceDocument, writeSkill } from '../../shared/api/backendApi.js';
import { cleanScalar, dashboardQueryKey, errorMessage, listToText, optionalSettingsCwd, SKILLS_REQUEST_TIMEOUT_MS, textValue, withTimeout, wordListFromText } from '../shared/pageShared.js';
import { PageHeader, RetryableSyncError } from '../shared/pageComponents.jsx';
import './SkillsPage.css';

const SKILLS_DASHBOARD_TIMEOUT_MS = Math.max(1, SKILLS_REQUEST_TIMEOUT_MS - 250);

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
    SKILLS_DASHBOARD_TIMEOUT_MS,
    '技能列表加载超时，请检查技能目录或后端状态。',
  );
  return normalizeSkillsResponse(response);
}

async function fetchSkillResolutionsDashboard(cwd) {
  const response = await withTimeout(
    listSkillResolutions({ cwd }),
    SKILLS_DASHBOARD_TIMEOUT_MS,
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
  const skillFile = skillText(raw, ['skill_file', 'skillFile']) || (dir ? `${dir.replace(/[\\/]+$/g, '')}/SKILL.md` : '');
  const description = skillText(raw, ['description', 'summary']);
  const summary = skillText(raw, ['summary', 'description']);
  const title = displayName || name;
  return {
    id: [scope, skillText(raw, ['personal_type', 'personalType']), name, dir, index].join(':'),
    name,
    title: title || '未命名技能',
    dir,
    skillFile,
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

function SkillMarkdownInline({ text, onOpenPath, keyPrefix }) {
  const source = (text || '').toString();
  const parts = [];
  const linkPattern = /\[([^\]]+)\]\(([^)]+)\)/g;
  let lastIndex = 0;
  let match;
  while ((match = linkPattern.exec(source)) !== null) {
    const [raw, label, target] = match;
    if (match.index > lastIndex) parts.push(source.slice(lastIndex, match.index));
    const cleanTarget = (target || '').trim();
    parts.push(
      cleanTarget && typeof onOpenPath === 'function'
        ? (
          <button
            className={skillPreviewLinkClass(cleanTarget)}
            key={`${keyPrefix}-link-${match.index}`}
            type="button"
            onClick={() => onOpenPath(cleanTarget, label)}
          >
            {label}
          </button>
        )
        : raw,
    );
    lastIndex = match.index + raw.length;
  }
  if (lastIndex < source.length) parts.push(source.slice(lastIndex));
  return <>{parts.length > 0 ? parts : source}</>;
}

function skillPreviewLinkClass(target) {
  const meta = skillCitationFromLink(target);
  if (!meta) return 'skills-preview-link';
  const suffix = meta.kind === 'conversation' ? ' chat-md-conversation-chip' : ' chat-md-skill-chip';
  return `skills-preview-link chat-md-citation${suffix}`;
}

function SkillMarkdownPreview({ content, onOpenPath }) {
  const text = (content || '').toString().trim();
  if (!text) return <p>暂无内容，点击“编辑正文”开始编写。</p>;
  const blocks = [];
  let paragraph = [];
  let paragraphStartLine = 0;
  let list = [];
  let listStartLine = 0;
  const flushParagraph = () => {
    if (paragraph.length === 0) return;
    const paragraphText = paragraph.join(' ');
    blocks.push({ type: 'p', key: `p-${paragraphStartLine}-${paragraphText}`, text: paragraphText });
    paragraph = [];
    paragraphStartLine = 0;
  };
  const flushList = () => {
    if (list.length === 0) return;
    blocks.push({ type: 'ul', key: `list-${listStartLine}-${list.map((item) => item.text).join('|')}`, items: list });
    list = [];
    listStartLine = 0;
  };
  for (const [lineIndex, line] of text.split('\n').entries()) {
    const lineNumber = lineIndex + 1;
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
      blocks.push({ type: 'heading', key: `heading-${lineNumber}-${heading[2]}`, level: Math.min(heading[1].length, 3), text: heading[2] });
      continue;
    }
    const bullet = /^[-*]\s+(.+)$/.exec(trimmed);
    if (bullet) {
      flushParagraph();
      if (list.length === 0) listStartLine = lineNumber;
      list.push({ key: `item-${lineNumber}-${bullet[1]}`, text: bullet[1] });
      continue;
    }
    if (paragraph.length === 0) paragraphStartLine = lineNumber;
    paragraph.push(trimmed);
  }
  flushParagraph();
  flushList();
  return (
    <>
      {blocks.map((block) => {
        if (block.type === 'heading') {
          const Tag = block.level <= 1 ? 'h3' : 'h4';
          return <Tag key={block.key}><SkillMarkdownInline text={block.text} onOpenPath={onOpenPath} keyPrefix={block.key} /></Tag>;
        }
        if (block.type === 'ul') {
          return (
            <ul key={block.key}>
              {block.items.map((item) => (
                <li key={item.key}>
                  <SkillMarkdownInline text={item.text} onOpenPath={onOpenPath} keyPrefix={item.key} />
                </li>
              ))}
            </ul>
          );
        }
        return <p key={block.key}><SkillMarkdownInline text={block.text} onOpenPath={onOpenPath} keyPrefix={block.key} /></p>;
      })}
    </>
  );
}

function normalizeSkillPreviewPathKey(path) {
  return (path || '').toString().trim().replace(/\\/g, '/').replace(/\/+/g, '/').toLowerCase();
}

function skillPreviewDir(path) {
  const clean = (path || '').toString().trim().replace(/\\/g, '/').replace(/\/+$/g, '');
  const index = clean.lastIndexOf('/');
  return index > 0 ? clean.slice(0, index) : '';
}

function stripLinkHash(path) {
  return (path || '').toString().trim().replace(/[#?].*$/, '');
}

function skillCitationFromLink(target, label = '') {
  const rawTarget = (target || '').toString().trim();
  if (!rawTarget) return null;
  const appMatch = /^app:\/\/([^/?#]+)$/i.exec(rawTarget);
  if (appMatch) return { kind: 'skill', skillId: appMatch[1], skillName: label, path: '', raw: label || rawTarget };
  const conversationMatch = /^agent:\/\/([^/?#]+)$/i.exec(rawTarget);
  if (conversationMatch) return { kind: 'conversation', conversationId: conversationMatch[1], raw: label || rawTarget };
  const cleanPath = stripLinkHash(rawTarget);
  if (/(^|[\\/])SKILL\.md$/i.test(cleanPath)) {
    return { kind: 'skill', skillId: '', skillName: label, path: cleanPath, raw: label || rawTarget };
  }
  return null;
}

function resolveSkillPreviewFile(path, files, activeSkillPath) {
  const target = (path || '').toString().trim();
  if (!target) return null;
  const candidates = new Set([normalizeSkillPreviewPathKey(target), normalizeSkillPreviewPathKey(target.replace(/^\.\//, ''))]);
  if (!target.startsWith('/')) {
    const activeDir = skillPreviewDir(activeSkillPath);
    if (activeDir) {
      candidates.add(normalizeSkillPreviewPathKey(`${activeDir}/${target.replace(/^\.\//, '')}`));
    }
  }
  return (Array.isArray(files) ? files : []).find((file) => candidates.has(normalizeSkillPreviewPathKey(file?.path))) || null;
}

function normalizeSkillCitationPath(path) {
  return stripLinkHash(path).replace(/\\/g, '/').replace(/\/+/g, '/').replace(/\/+$/g, '').toLowerCase();
}

function compactSkillLookupText(value) {
  return Array.from((value || '').toString().trim().toLowerCase())
    .filter((char) => /[\p{L}\p{N}]/u.test(char))
    .join('');
}

function skillFileForItem(skill) {
  const explicit = (skill?.skillFile || skill?.skill_file || '').toString().trim();
  if (explicit) return explicit;
  const dir = (skill?.dir || '').toString().trim().replace(/[\\/]+$/g, '');
  return dir ? `${dir}/SKILL.md` : '';
}

function skillMatchesCitationPath(skill, path) {
  const citationPath = normalizeSkillCitationPath(path);
  if (!citationPath) return false;
  const skillFile = normalizeSkillCitationPath(skillFileForItem(skill));
  const skillDir = normalizeSkillCitationPath(skill?.dir || '');
  return citationPath === skillFile || (skillDir && citationPath === `${skillDir}/skill.md`);
}

function skillMatchesCitationName(skill, citation) {
  const needles = [citation.skillName, citation.raw, citation.skillId].map(compactSkillLookupText).filter(Boolean);
  if (needles.length === 0) return false;
  const haystack = [skill?.name, skill?.title, skill?.displayName].map(compactSkillLookupText).filter(Boolean);
  return needles.some((needle) => haystack.includes(needle));
}

function findSkillForCitation(skills, citation) {
  const items = Array.isArray(skills) ? skills : [];
  if (citation.path) {
    const byPath = items.find((skill) => skillMatchesCitationPath(skill, citation.path));
    if (byPath) return byPath;
  }
  return items.find((skill) => skillMatchesCitationName(skill, citation)) || null;
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
  const files = [];
  for (const file of response.files) {
    const normalized = {
      name: (file?.name || '').toString().trim(),
      path: (file?.path || '').toString().trim(),
      isMain: Boolean(file?.is_main || file?.isMain),
    };
    if (normalized.name && normalized.path) files.push(normalized);
  }
  return files;
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
    view_diff: '只查看两个版本分别在哪里，不会修改文件。',
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

function resolutionConflictGuide(conflict) {
  const kind = (conflict?.kind || '').toString().trim().toLowerCase();
  if (sameNameResolutionConflict(conflict)) {
    if (!sameNameHasProjectSource(conflict) && sameNamePersonalSources(conflict).length > 1) {
      return '发现多个同名的私人技能。请选择保留哪一版，其他同名版本会被删除；也可以改名保存。';
    }
    return '发现多个同名技能。请选择保留哪一版，其他同名版本会被删除；也可以改名保存。';
  }
  if (kind === 'external_personal_project_same_name') {
    return '检测到同名技能同时存在于私人使用和项目共享。请选择使用项目共享版本、继续私人使用，或另存为新私人技能。';
  }
  if (kind === 'unmanaged_provider_skill' || kind === 'unmanaged_same_name' || kind === 'unmanaged') {
    return '外部应用里有一个还没纳入管理的技能。可以导入后统一管理，或只保留在外部应用里。';
  }
  if (kind === 'canonical_deleted_with_drift') {
    if ((conflict?.scope || '').toString().trim().toLowerCase() === 'personal') {
      return '私人使用里的同名技能已经删除或改成项目共享，但 Claude/Codex 里还保留旧私人版本。请选择继续私人使用、另存为新私人技能，或删除旧私人版本。';
    }
    return '本项目里的技能已不存在，但外部应用里还有改过的版本。请选择恢复、另存或删除外部版本。';
  }
  if (kind === 'mirror_root_symlink') {
    return '外部应用的技能目录还是旧连接。接管后会改成由本项目管理的技能目录，并重新同步技能。';
  }
  return '外部应用里的技能和本项目管理的技能不一致。请选择下面一种处理方式。';
}

function resolutionPreviewIntro(preview) {
  const action = (preview?.action || '').toString().trim();
  if (isResolutionViewAction(action)) return '下面只说明两个版本分别在哪里，不会修改文件。';
  return '请先确认将要写入的位置，确认应用后才会修改文件。';
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
  const group = [];
  for (const provider of Array.isArray(entry?.provider_group) ? entry.provider_group : []) {
    const providerLabel = resolutionProviderLabel(provider);
    if (providerLabel) group.push(providerLabel);
  }
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
    const entries = [];
    for (const action of actions) {
      if (!allowed.has(action)) continue;
      entries.push({
        action,
        label: ({
          use_project_shared_skill: '使用项目共享版本，删除旧私人版本',
          use_external_provider_skill: '继续私人使用，替换项目共享版本',
        }[action] || resolutionActionLabel(action)),
        help: resolutionActionHelp(action),
      });
    }
    return entries;
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

function previewItemPaths(item, action = '') {
  const normalizedAction = (action || item?.action || '').toString().trim();
  const overwrite = normalizedAction === 'canonical_overwrite_mirror' || normalizedAction === 'personal_overwrite_mirror';
  const importAction = normalizedAction === 'import_to_personal_imported' || normalizedAction === 'import_to_project' || normalizedAction === 'takeover_provider_skill';
  const useProjectShared = normalizedAction === 'use_project_shared_skill';
  const useExternal = normalizedAction === 'use_external_provider_skill';
  const sourceLabel = overwrite || useProjectShared ? '本项目版本' : '外部版本';
  let targetLabel = overwrite ? '外部版本' : '本项目版本';
  if (importAction) targetLabel = '保存位置';
  if (useProjectShared) targetLabel = '外部版本';
  if (useExternal) targetLabel = '项目共享版本';
  return [
    [sourceLabel, item?.source_path || item?.sourcePath],
    [targetLabel, item?.target_path || item?.targetPath],
  ].map(([label, value]) => ({ label, value: (value || '').toString().trim() }))
    .filter((itemPath) => itemPath.value);
}

function resolutionShortHash(value) {
  return (value || '').toString().trim().slice(0, 8);
}

function resolutionManualSteps(conflict) {
  const kind = (conflict?.kind || '').toString().trim().toLowerCase();
  const actions = Array.isArray(conflict?.available_actions) ? conflict.available_actions : [];
  if ((kind === 'same_name' || kind === 'same_name_scope_conflict') && !actions.includes('keep_selected') && !actions.includes('rename_personal')) {
    return [
      '要保留项目共享：编辑或删除同名私人技能。',
      '要保留私人使用：编辑项目共享技能改名，或删除项目共享技能。',
      '两边都要保留：把其中一个改成更明确的名字。',
    ];
  }
  return [];
}

function importedSkillFilePath(item) {
  return (item?.skill_file || item?.skillFile || item?.path || '').toString().trim();
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

function skillFileBaseName(path) {
  const clean = (path || '').toString().trim().replace(/[\\/]+SKILL\.md$/i, '').replace(/\\/g, '/');
  const parts = clean.split('/').filter(Boolean);
  return parts[parts.length - 1] || '';
}

function fileNameFromPath(path) {
  const clean = (path || '').toString().trim().replace(/[\\/]+$/g, '').replace(/\\/g, '/');
  const parts = clean.split('/').filter(Boolean);
  return parts[parts.length - 1] || '';
}

function normalizeImportSummaryDraftScope(scope) {
  return scope === 'personal' ? 'personal' : 'project';
}

function importSummaryDraftStatusCount(drafts, status) {
  return drafts.filter((draft) => draft.status === status).length;
}

function importSummaryPanelTitle(drafts) {
  const conflictCount = importSummaryDraftStatusCount(drafts, 'conflict');
  const errorCount = importSummaryDraftStatusCount(drafts, 'error');
  const readyCount = drafts.filter((draft) => draft.status === 'ready' || draft.status === 'applied').length;
  if (conflictCount > 0 && readyCount === 0) return '导入后需要处理';
  if (conflictCount > 0) return '导入后的简介建议和同名处理';
  if (errorCount > 0 && readyCount === 0) return '导入后可补充简介';
  return '导入后的简介建议';
}

function importSummaryPanelHint(drafts) {
  const conflictCount = importSummaryDraftStatusCount(drafts, 'conflict');
  const errorCount = importSummaryDraftStatusCount(drafts, 'error');
  const readyCount = drafts.filter((draft) => draft.status === 'ready' || draft.status === 'applied').length;
  if (conflictCount > 0 && readyCount === 0) return '同名技能需要先选择使用哪个版本。';
  if (conflictCount > 0) return '简介建议采用并保存后生效；同名技能需要选择使用哪个版本。';
  if (errorCount > 0 && readyCount === 0) return '技能已正常导入，可以稍后手动补充简介。';
  return '还没有写入技能，采用并保存后生效。';
}

function importSummaryDraftMessage(drafts) {
  const readyCount = importSummaryDraftStatusCount(drafts, 'ready');
  const conflictCount = importSummaryDraftStatusCount(drafts, 'conflict');
  const errorCount = importSummaryDraftStatusCount(drafts, 'error');
  const parts = [];
  if (readyCount > 0) parts.push(`已生成 ${readyCount} 条简介建议，采用后再保存。`);
  if (conflictCount > 0) parts.push(`${conflictCount} 个同名技能待处理。`);
  if (errorCount > 0) parts.push(`${errorCount} 个技能可手动补充简介。`);
  return parts.join('，');
}

function duplicateImportFailureMessage(message) {
  const raw = (message || '').toString().trim();
  const existsMatch = raw.match(/^skill already exists:\s*(.+)$/i);
  if (existsMatch) return `${(existsMatch[1] || '').toString().trim() || '该技能'} 已存在，未重复导入。`;
  if (/^source is inside skills root:/i.test(raw)) return '这个目录已经在技能管理中，未重复导入。';
  return '';
}

function normalizeImportFailure(item) {
  const source = (item?.source || '').toString().trim();
  const rawMessage = (item?.error || '未知错误').toString().trim();
  const duplicateMessage = duplicateImportFailureMessage(rawMessage);
  return {
    duplicate: Boolean(duplicateMessage),
    duplicateName: rawMessage.match(/^skill already exists:\s*(.+)$/i)?.[1]?.toString().trim() || '',
    message: duplicateMessage || rawMessage || '未知错误',
    source,
  };
}

function summarizeImportFailureNames(names) {
  if (names.length <= 3) return names.join('、');
  return `${names.slice(0, 3).join('、')} 等 ${names.length} 个`;
}

function duplicateImportNotice(scope, duplicateFailures) {
  const names = [];
  for (const item of duplicateFailures) {
    if (item.duplicateName) names.push(item.duplicateName);
  }
  const prefix = normalizeImportSummaryDraftScope(scope) === 'personal' ? '私人使用里已存在' : '项目共享里已存在';
  return names.length > 0
    ? `${prefix}：${summarizeImportFailureNames(names)}，未重复导入。`
    : `${prefix} ${duplicateFailures.length} 个技能，未重复导入。`;
}

function importNotice(importedCount, drafts, failures, scope) {
  const parts = [];
  if (importedCount > 0) parts.push(`已导入 ${importedCount} 个技能目录`);
  const draftMessage = importSummaryDraftMessage(drafts);
  if (draftMessage) parts.push(draftMessage);
  const duplicateFailures = failures.filter((failure) => failure.duplicate);
  if (duplicateFailures.length > 0) parts.push(duplicateImportNotice(scope, duplicateFailures));
  const otherFailures = failures.filter((failure) => !failure.duplicate);
  if (otherFailures.length > 0) parts.push(`${otherFailures.length} 个目录导入失败：${otherFailures[0].source || otherFailures[0].message}`);
  return parts.length > 0 ? parts.join('，') : '未导入任何技能目录';
}

const DATASOURCE_LIST_LIMIT = 200;
const DATASOURCE_IMPORT_FILTERS = Object.freeze([
  Object.freeze({ displayName: 'PDF/TXT/TEXT', pattern: '*.pdf;*.txt;*.text' }),
]);
const SKILL_TOOLS_LIST_LIMIT = 200;

const SKILL_TOOLS_UI = Object.freeze({
  actions: '\u64cd\u4f5c',
  create: '\u65b0\u589e\u5de5\u5177',
  description: '\u63cf\u8ff0',
  disabled: '\u5df2\u5173\u95ed',
  enabled: '\u5df2\u542f\u7528',
  empty: '\u6682\u65e0 Skill \u5de5\u5177',
  errorPrefix: '\u8bfb\u53d6 Skill \u5de5\u5177\u5931\u8d25\uff1a',
  loading: '\u8bfb\u53d6\u4e2d...',
  methodName: '\u65b9\u6cd5\u540d',
  refresh: '\u5237\u65b0',
  sectionTitle: '\u63d2\u4ef6\u4e0e\u6280\u80fd',
  status: '\u72b6\u6001',
  title: 'Skill\u5de5\u5177',
  waitingProject: '\u6b63\u5728\u8fde\u63a5\u672c\u5730\u9879\u76ee...',
});

const DATASOURCE_UI = Object.freeze({
  actions: '\u64cd\u4f5c',
  cancel: '\u53d6\u6d88',
  chunks: '\u5206\u5757',
  close: '\u5173\u95ed',
  confirmDelete: '\u786e\u8ba4\u5220\u9664',
  content: '\u5206\u5757\u5185\u5bb9',
  delete: '\u5220\u9664',
  deletePrompt: '\u5220\u9664\u540e\u4f1a\u79fb\u9664\u8be5\u6570\u636e\u6e90\u548c\u5df2\u5bfc\u5165\u7684\u6587\u672c\u5206\u5757\u3002',
  deleteSuccess: '\u5df2\u5220\u9664\u6570\u636e\u6e90\u3002',
  deleteTitle: '\u5220\u9664\u6570\u636e\u6e90',
  detailTitle: '\u6570\u636e\u6e90\u8be6\u60c5',
  edit: '\u7f16\u8f91',
  editTitle: '\u7f16\u8f91\u6570\u636e\u6e90',
  empty: '\u6682\u65e0\u6570\u636e\u6e90',
  errorPrefix: '\u64cd\u4f5c\u5931\u8d25\uff1a',
  extension: '\u6269\u5c55\u540d',
  fileName: '\u6587\u4ef6\u540d',
  id: 'ID',
  import: '\u5bfc\u5165',
  importPlaceholder: '\u652f\u6301 PDF\u3001TXT \u548c TEXT \u6587\u4ef6',
  importSuccess: '\u5df2\u5bfc\u5165\u6570\u636e\u6e90\u3002',
  loading: '\u8bfb\u53d6\u4e2d...',
  noChunks: '\u6682\u65e0\u5206\u5757\u3002',
  path: '\u8def\u5f84',
  refresh: '\u5237\u65b0',
  save: '\u4fdd\u5b58',
  size: '\u5927\u5c0f',
  sourcePath: '\u672c\u5730\u6587\u4ef6\u8def\u5f84',
  status: '\u72b6\u6001',
  totalChars: '\u5b57\u7b26',
  updateSuccess: '\u5df2\u66f4\u65b0\u6570\u636e\u6e90\u3002',
  view: '\u67e5\u770b',
});

function SkillsPage({ copy = APP_COPY.zh.skills, projectPath, refreshKey = 0, resolveLaunchPreferences }) {
  const [subTab, setSubTab] = useState('plugins');
  return (
    <div className="skills-tabbed-container">
      <div className="skills-subtabs-header">
        <button
          type="button"
          className={subTab === 'plugins' ? 'active' : ''}
          onClick={() => setSubTab('plugins')}
        >
          {copy.tabs.plugins}
        </button>
        <button
          type="button"
          className={subTab === 'skills' ? 'active' : ''}
          onClick={() => setSubTab('skills')}
        >
          {copy.tabs.skills}
        </button>
        <button
          type="button"
          className={subTab === 'library' ? 'active icon-only' : 'icon-only'}
          aria-label={copy.localLibrary}
          title={copy.localLibrary}
          onClick={() => setSubTab('library')}
        >
          <Sparkles size={16} aria-hidden="true" />
        </button>
        <button
          type="button"
          className={subTab === 'datasource' ? 'active' : ''}
          onClick={() => setSubTab('datasource')}
        >
          {copy.tabs.datasource}
        </button>
      </div>
      <div className="skills-tab-content">
        {subTab === 'plugins' ? (
          <PluginsSquareView copy={copy} projectPath={projectPath} />
        ) : subTab === 'datasource' ? (
          <DataSourceView copy={copy} />
        ) : subTab === 'skills' ? (
          <SkillToolsView projectPath={projectPath} />
        ) : (
          <SkillsLibraryTab copy={copy} projectPath={projectPath} refreshKey={refreshKey} resolveLaunchPreferences={resolveLaunchPreferences} />
        )}
      </div>
    </div>
  );
}

function SkillsLibraryTab({ copy, projectPath, refreshKey, resolveLaunchPreferences }) {
  const model = useSkillsPageModel({ projectPath, refreshKey, resolveLaunchPreferences });
  return <SkillsPageView copy={copy} model={model} />;
}

function skillToolsQueryKey(cwd) {
  return ['skillTools', cwd];
}

function normalizeSkillTool(raw, index = 0) {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) {
    throw new Error(`skill tool ${index} must be an object`);
  }
  const id = Number(raw.id);
  if (!Number.isInteger(id) || id <= 0) {
    throw new Error(`skill tool ${index} is missing id`);
  }
  const methodName = cleanScalar(raw.methodName ?? raw.method_name ?? raw.name);
  if (!methodName) {
    throw new Error(`skill tool ${index} is missing methodName`);
  }
  return {
    id,
    methodName,
    name: cleanScalar(raw.name) || methodName,
    description: cleanScalar(raw.description),
    command: cleanScalar(raw.command),
    args: Array.isArray(raw.args) ? raw.args.flatMap((arg) => {
      const value = cleanScalar(arg);
      return value ? [value] : [];
    }) : [],
    enabled: raw.enabled !== false,
  };
}

function normalizeSkillToolsResponse(response) {
  if (!response || typeof response !== 'object' || Array.isArray(response)) {
    throw new Error('skills/tools/list response must be an object');
  }
  if (!Array.isArray(response.tools)) {
    throw new Error('skills/tools/list response.tools must be an array');
  }
  return response.tools.map(normalizeSkillTool);
}

function SkillToolsView({ projectPath }) {
  const queryClient = useQueryClient();
  const cwd = useMemo(() => {
    try {
      return normalizeSettingsCwd(projectPath);
    } catch {
      return '';
    }
  }, [projectPath]);
  const {
    data: tools = [],
    error,
    isError,
    isFetching,
    isLoading,
  } = useQuery({
    queryKey: skillToolsQueryKey(cwd),
    enabled: Boolean(cwd),
    queryFn: async () => normalizeSkillToolsResponse(await listSkillTools({ cwd, keyword: '', limit: SKILL_TOOLS_LIST_LIMIT })),
  });
  const refreshTools = () => {
    if (cwd) void queryClient.invalidateQueries({ queryKey: skillToolsQueryKey(cwd) });
  };

  return (
    <section className="skill-tools-panel" aria-label={SKILL_TOOLS_UI.title}>
      <div className="skill-tools-header">
        <div>
          <span className="skill-tools-kicker">{SKILL_TOOLS_UI.sectionTitle}</span>
          <h2>{SKILL_TOOLS_UI.title}</h2>
        </div>
        <div className="skill-tools-actions">
          <button type="button">{SKILL_TOOLS_UI.create}</button>
          <button type="button" className="ghost" onClick={refreshTools} disabled={!cwd || isFetching}>
            <RefreshCw size={16} aria-hidden="true" />
            <span>{SKILL_TOOLS_UI.refresh}</span>
          </button>
        </div>
      </div>
      {!cwd ? <p className="skill-tools-notice">{SKILL_TOOLS_UI.waitingProject}</p> : null}
      {cwd && isLoading ? <p className="skill-tools-notice">{SKILL_TOOLS_UI.loading}</p> : null}
      {isError ? <p className="skill-tools-error" role="alert">{SKILL_TOOLS_UI.errorPrefix}{errorMessage(error)}</p> : null}
      {cwd && !isLoading && !isError && tools.length === 0 ? <p className="skill-tools-empty">{SKILL_TOOLS_UI.empty}</p> : null}
      {tools.length > 0 ? (
        <div className="skill-tools-table-wrap">
          <table className="skill-tools-table">
            <thead>
              <tr>
                <th>{SKILL_TOOLS_UI.methodName}</th>
                <th>{SKILL_TOOLS_UI.description}</th>
                <th>{SKILL_TOOLS_UI.status}</th>
                <th>{SKILL_TOOLS_UI.actions}</th>
              </tr>
            </thead>
            <tbody>
              {tools.map((tool) => (
                <tr key={tool.id}>
                  <td className="skill-tool-name-cell">
                    <strong>{tool.name}</strong>
                    {tool.methodName !== tool.name ? <span>{tool.methodName}</span> : null}
                  </td>
                  <td>{tool.description || '-'}</td>
                  <td><span className={`skill-tool-status ${tool.enabled ? 'is-enabled' : 'is-disabled'}`}>{tool.enabled ? SKILL_TOOLS_UI.enabled : SKILL_TOOLS_UI.disabled}</span></td>
                  <td><code>{[tool.command, ...tool.args].filter(Boolean).join(' ') || '-'}</code></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : null}
    </section>
  );
}

function datasourceDocumentsQueryKey() {
  return ['datasourceV2', 'documents'];
}

function normalizeDatasourceDocument(raw, index = 0) {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) {
    throw new Error(`datasource document ${index} must be an object`);
  }
  const documentId = Number(raw.documentId ?? raw.document_id ?? raw.id);
  if (!Number.isInteger(documentId) || documentId <= 0) {
    throw new Error(`datasource document ${index} is missing documentId`);
  }
  return {
    documentId,
    sourcePath: cleanScalar(raw.sourcePath ?? raw.source_path),
    fileName: cleanScalar(raw.fileName ?? raw.file_name),
    extension: cleanScalar(raw.extension),
    sizeBytes: Number(raw.sizeBytes ?? raw.size_bytes ?? 0),
    contentHash: cleanScalar(raw.contentHash ?? raw.content_hash),
    chunkCount: Number(raw.chunkCount ?? raw.chunk_count ?? 0),
    totalChars: Number(raw.totalChars ?? raw.total_chars ?? 0),
    status: cleanScalar(raw.status),
    errorMessage: cleanScalar(raw.errorMessage ?? raw.error_message),
    createdAt: cleanScalar(raw.createdAt ?? raw.created_at),
    updatedAt: cleanScalar(raw.updatedAt ?? raw.updated_at),
  };
}

function normalizeDatasourceDocuments(response) {
  if (!response || typeof response !== 'object' || Array.isArray(response)) {
    throw new Error('datasourceV2/list response must be an object');
  }
  if (!Array.isArray(response.documents)) {
    throw new Error('datasourceV2/list response.documents must be an array');
  }
  return response.documents.map(normalizeDatasourceDocument);
}

function normalizeDatasourceDetail(response) {
  if (!response || typeof response !== 'object' || Array.isArray(response)) {
    throw new Error('datasourceV2/get response must be an object');
  }
  const document = normalizeDatasourceDocument(response.document || {}, 0);
  if (!Array.isArray(response.chunks)) {
    throw new Error('datasourceV2/get response.chunks must be an array');
  }
  const chunks = response.chunks.map((raw, index) => ({
    id: Number(raw?.id ?? index + 1),
    documentId: Number(raw?.documentId ?? raw?.document_id ?? document.documentId),
    chunkIndex: Number(raw?.chunkIndex ?? raw?.chunk_index ?? index),
    content: (raw?.content || '').toString(),
    charCount: Number(raw?.charCount ?? raw?.char_count ?? 0),
    byteCount: Number(raw?.byteCount ?? raw?.byte_count ?? 0),
  }));
  return { document, chunks };
}

function datasourceMatches(doc, search) {
  const keyword = search.trim().toLowerCase();
  if (!keyword) return true;
  return [doc.fileName, doc.sourcePath, doc.extension, doc.status]
    .some((value) => value.toLowerCase().includes(keyword));
}

function formatDatasourceBytes(value) {
  const bytes = Number(value);
  if (!Number.isFinite(bytes) || bytes < 0) return '-';
  if (bytes < 1024) return `${bytes} B`;
  const units = ['KB', 'MB', 'GB', 'TB'];
  let size = bytes / 1024;
  let unitIndex = 0;
  while (size >= 1024 && unitIndex < units.length - 1) {
    size /= 1024;
    unitIndex += 1;
  }
  return `${size >= 10 ? size.toFixed(0) : size.toFixed(1)} ${units[unitIndex]}`;
}

function datasourceStatusTone(status) {
  const value = status.toLowerCase();
  if (value === 'ready') return 'ready';
  if (value === 'failed') return 'failed';
  return 'pending';
}

function datasourceEditForm(doc) {
  return {
    sourcePath: doc.sourcePath,
    fileName: doc.fileName,
    extension: doc.extension,
    sizeBytes: String(Number.isFinite(doc.sizeBytes) ? doc.sizeBytes : 0),
  };
}

function DataSourceView({ copy }) {
  const queryClient = useQueryClient();
  const [search, setSearch] = useState('');
  const [sourcePath, setSourcePath] = useState('');
  const [busyAction, setBusyAction] = useState('');
  const [notice, setNotice] = useState('');
  const [actionError, setActionError] = useState('');
  const [detailID, setDetailID] = useState(0);
  const [editingDoc, setEditingDoc] = useState(null);
  const [deletingDoc, setDeletingDoc] = useState(null);

  const {
    data: documents = [],
    error: documentsError,
    isError: documentsIsError,
    isFetching: documentsIsFetching,
    isLoading: documentsIsLoading,
    refetch: refetchDocuments,
  } = useQuery({
    queryKey: datasourceDocumentsQueryKey(),
    queryFn: async () => normalizeDatasourceDocuments(await listDatasourceDocuments({ limit: DATASOURCE_LIST_LIMIT })),
  });
  const {
    data: detailData,
    error: detailError,
    isError: detailIsError,
    isLoading: detailIsLoading,
  } = useQuery({
    queryKey: ['datasourceV2', 'document', detailID],
    enabled: detailID > 0,
    queryFn: async () => normalizeDatasourceDetail(await getDatasourceDocument({ documentId: detailID })),
  });
  const filtered = documents.filter((doc) => datasourceMatches(doc, search));

  const invalidateDocuments = useCallback(async () => {
    await queryClient.invalidateQueries({ queryKey: datasourceDocumentsQueryKey() });
  }, [queryClient]);

  const runAction = useCallback(async (action, successText) => {
    setNotice('');
    setActionError('');
    try {
      await action();
      setNotice(successText);
      await invalidateDocuments();
    } catch (error) {
      setActionError(`${DATASOURCE_UI.errorPrefix}${errorMessage(error)}`);
    }
  }, [invalidateDocuments]);

  const handleImport = useCallback(async () => {
    setBusyAction('import');
    setNotice('');
    setActionError('');
    try {
      const selected = await selectFiles({ filters: DATASOURCE_IMPORT_FILTERS });
      const selectedPath = cleanScalar(selected[0]);
      if (!selectedPath) return;
      setSourcePath(selectedPath);
      await runAction(async () => {
        await importDatasourceLocalFile({ sourcePath: selectedPath });
        setSourcePath('');
      }, DATASOURCE_UI.importSuccess);
    } catch (error) {
      setActionError(`${DATASOURCE_UI.errorPrefix}${errorMessage(error)}`);
    } finally {
      setBusyAction('');
    }
  }, [runAction]);

  const handleUpdate = useCallback(async (form) => {
    if (!editingDoc) return;
    setBusyAction('update');
    await runAction(async () => {
      const updated = await updateDatasourceDocument({
        documentId: editingDoc.documentId,
        sourcePath: form.sourcePath,
        fileName: form.fileName,
        extension: form.extension,
        sizeBytes: form.sizeBytes,
      });
      setEditingDoc(null);
      const normalized = normalizeDatasourceDocument(updated, 0);
      if (detailID === normalized.documentId) {
        queryClient.setQueryData(['datasourceV2', 'document', detailID], (current) => (
          current ? { ...current, document: normalized } : current
        ));
      }
    }, DATASOURCE_UI.updateSuccess);
    setBusyAction('');
  }, [detailID, editingDoc, queryClient, runAction]);

  const handleDelete = useCallback(async () => {
    if (!deletingDoc) return;
    const documentID = deletingDoc.documentId;
    setBusyAction('delete');
    await runAction(async () => {
      await deleteDatasourceDocument({ documentId: documentID });
      setDeletingDoc(null);
      if (detailID === documentID) setDetailID(0);
      queryClient.removeQueries({ queryKey: ['datasourceV2', 'document', documentID] });
    }, DATASOURCE_UI.deleteSuccess);
    setBusyAction('');
  }, [deletingDoc, detailID, queryClient, runAction]);

  return (
    <div className="datasource-container">
      <div className="datasource-header">
        <div>
          <h1>{copy.datasourceTitle}</h1>
          <p className="datasource-subtitle">{copy.datasourceSubtitle}</p>
        </div>
        <button
          type="button"
          className="datasource-icon-button"
          title={DATASOURCE_UI.refresh}
          aria-label={DATASOURCE_UI.refresh}
          disabled={documentsIsFetching}
          onClick={() => { void refetchDocuments(); }}
        >
          <RefreshCw size={18} />
        </button>
      </div>

      <div className="datasource-import-row">
        <label className="datasource-path-field">
          <span>{DATASOURCE_UI.sourcePath}</span>
          <input
            data-testid="datasource-source-path"
            value={sourcePath}
            readOnly
            placeholder={DATASOURCE_UI.importPlaceholder}
          />
        </label>
        <button type="button" data-testid="datasource-import-button" disabled={busyAction === 'import'} onClick={() => { void handleImport(); }}>
          <Upload size={16} />
          <span>{busyAction === 'import' ? DATASOURCE_UI.loading : DATASOURCE_UI.import}</span>
        </button>
      </div>

      <div className="plugins-search-bar-wrap">
        <div className="plugins-search-input-container">
          <Search className="search-icon" size={18} />
          <input
            type="text"
            placeholder={copy.datasourceSearch}
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            aria-label={copy.datasourceSearch}
          />
        </div>
      </div>

      {notice ? <p className="datasource-notice" role="status">{notice}</p> : null}
      {actionError ? <p className="datasource-error" role="alert">{actionError}</p> : null}
      {documentsIsError ? <p className="datasource-error" role="alert">{`${DATASOURCE_UI.errorPrefix}${errorMessage(documentsError)}`}</p> : null}

      <div className="datasource-table-wrap">
        <table className="datasource-table">
          <thead>
            <tr>
              <th>{DATASOURCE_UI.fileName}</th>
              <th>{DATASOURCE_UI.path}</th>
              <th>{DATASOURCE_UI.size}</th>
              <th>{DATASOURCE_UI.chunks}</th>
              <th>{DATASOURCE_UI.status}</th>
              <th>{DATASOURCE_UI.actions}</th>
            </tr>
          </thead>
          <tbody>
            {documentsIsLoading ? (
              <tr><td colSpan={6}>{DATASOURCE_UI.loading}</td></tr>
            ) : filtered.length === 0 ? (
              <tr><td colSpan={6}>{DATASOURCE_UI.empty}</td></tr>
            ) : filtered.map((doc) => (
              <tr key={doc.documentId}>
                <td>
                  <div className="datasource-file-cell">
                    <FileText size={16} />
                    <span>{doc.fileName || doc.sourcePath || `#${doc.documentId}`}</span>
                  </div>
                </td>
                <td><span className="datasource-path-text">{doc.sourcePath || '-'}</span></td>
                <td>{formatDatasourceBytes(doc.sizeBytes)}</td>
                <td>{doc.chunkCount}</td>
                <td><span className={`datasource-status datasource-status-${datasourceStatusTone(doc.status)}`}>{doc.status || '-'}</span></td>
                <td>
                  <div className="datasource-actions">
                    <button type="button" data-testid={`datasource-view-${doc.documentId}`} title={DATASOURCE_UI.view} aria-label={`${DATASOURCE_UI.view} ${doc.fileName}`} onClick={() => setDetailID(doc.documentId)}><Eye size={16} /></button>
                    <button type="button" data-testid={`datasource-edit-${doc.documentId}`} title={DATASOURCE_UI.edit} aria-label={`${DATASOURCE_UI.edit} ${doc.fileName}`} onClick={() => setEditingDoc(doc)}><Pencil size={16} /></button>
                    <button type="button" data-testid={`datasource-delete-${doc.documentId}`} title={DATASOURCE_UI.delete} aria-label={`${DATASOURCE_UI.delete} ${doc.fileName}`} onClick={() => setDeletingDoc(doc)}><Trash2 size={16} /></button>
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {detailID > 0 ? (
        <DatasourceDetailModal
          detail={detailData}
          error={detailError}
          isError={detailIsError}
          isLoading={detailIsLoading}
          onClose={() => setDetailID(0)}
        />
      ) : null}
      {editingDoc ? (
        <DatasourceEditModal
          key={editingDoc.documentId}
          doc={editingDoc}
          saving={busyAction === 'update'}
          onClose={() => setEditingDoc(null)}
          onSave={handleUpdate}
        />
      ) : null}
      {deletingDoc ? (
        <DatasourceDeleteModal
          doc={deletingDoc}
          deleting={busyAction === 'delete'}
          onClose={() => setDeletingDoc(null)}
          onConfirm={handleDelete}
        />
      ) : null}
    </div>
  );
}

function DatasourceDetailModal({ detail, error, isError, isLoading, onClose }) {
  return (
    <FocusTrapDialog ariaLabel={DATASOURCE_UI.detailTitle} className="modal-box datasource-modal" closeDisabled={false} onClose={onClose}>
      <header>
        <h2>{DATASOURCE_UI.detailTitle}</h2>
        <button type="button" className="ghost" onClick={onClose}>{DATASOURCE_UI.close}</button>
      </header>
      {isLoading ? <p>{DATASOURCE_UI.loading}</p> : null}
      {isError ? <p className="datasource-error" role="alert">{`${DATASOURCE_UI.errorPrefix}${errorMessage(error)}`}</p> : null}
      {detail ? (
        <>
          <dl className="datasource-detail-grid">
            <div><dt>{DATASOURCE_UI.id}</dt><dd>{detail.document.documentId}</dd></div>
            <div><dt>{DATASOURCE_UI.fileName}</dt><dd>{detail.document.fileName || '-'}</dd></div>
            <div><dt>{DATASOURCE_UI.path}</dt><dd>{detail.document.sourcePath || '-'}</dd></div>
            <div><dt>{DATASOURCE_UI.size}</dt><dd>{formatDatasourceBytes(detail.document.sizeBytes)}</dd></div>
            <div><dt>{DATASOURCE_UI.totalChars}</dt><dd>{detail.document.totalChars}</dd></div>
            <div><dt>{DATASOURCE_UI.status}</dt><dd>{detail.document.status || '-'}</dd></div>
          </dl>
          <div className="datasource-chunks">
            <h3>{DATASOURCE_UI.content}</h3>
            {detail.chunks.length === 0 ? <p>{DATASOURCE_UI.noChunks}</p> : detail.chunks.map((chunk) => (
              <pre key={`${chunk.id}-${chunk.chunkIndex}`} data-testid="datasource-detail-chunk">{chunk.content}</pre>
            ))}
          </div>
        </>
      ) : null}
    </FocusTrapDialog>
  );
}

function DatasourceEditModal({ doc, saving, onClose, onSave }) {
  const [form, setForm] = useState(() => datasourceEditForm(doc));
  const update = (key) => (event) => setForm((current) => ({ ...current, [key]: event.target.value }));
  return (
    <FocusTrapDialog ariaLabel={DATASOURCE_UI.editTitle} className="modal-box datasource-modal" closeDisabled={saving} onClose={onClose}>
      <header>
        <h2>{DATASOURCE_UI.editTitle}</h2>
        <button type="button" className="ghost" onClick={onClose} disabled={saving}>{DATASOURCE_UI.close}</button>
      </header>
      <div className="datasource-form-grid">
        <label>{DATASOURCE_UI.sourcePath}<input data-testid="datasource-edit-source-path" value={form.sourcePath} onChange={update('sourcePath')} /></label>
        <label>{DATASOURCE_UI.fileName}<input data-testid="datasource-edit-file-name" value={form.fileName} onChange={update('fileName')} /></label>
        <label>{DATASOURCE_UI.extension}<input value={form.extension} onChange={update('extension')} /></label>
        <label>{DATASOURCE_UI.size}<input type="number" min="0" value={form.sizeBytes} onChange={update('sizeBytes')} /></label>
      </div>
      <footer>
        <button type="button" className="ghost" onClick={onClose} disabled={saving}>{DATASOURCE_UI.cancel}</button>
        <button type="button" data-testid="datasource-edit-save" onClick={() => { void onSave(form); }} disabled={saving}>{saving ? DATASOURCE_UI.loading : DATASOURCE_UI.save}</button>
      </footer>
    </FocusTrapDialog>
  );
}

function DatasourceDeleteModal({ doc, deleting, onClose, onConfirm }) {
  return (
    <FocusTrapDialog ariaLabel={DATASOURCE_UI.deleteTitle} className="modal-box datasource-modal" closeDisabled={deleting} onClose={onClose}>
      <header>
        <h2>{DATASOURCE_UI.deleteTitle}</h2>
        <button type="button" className="ghost" onClick={onClose} disabled={deleting}>{DATASOURCE_UI.close}</button>
      </header>
      <p>{DATASOURCE_UI.deletePrompt}</p>
      <p className="datasource-delete-target">{doc.fileName || doc.sourcePath || `#${doc.documentId}`}</p>
      <footer>
        <button type="button" className="ghost" onClick={onClose} disabled={deleting}>{DATASOURCE_UI.cancel}</button>
        <button type="button" className="text-danger" data-testid="datasource-delete-confirm" onClick={() => { void onConfirm(); }} disabled={deleting}>{deleting ? DATASOURCE_UI.loading : DATASOURCE_UI.confirmDelete}</button>
      </footer>
    </FocusTrapDialog>
  );
}

function mcpServersListQueryKey(projectPath) {
  return ['mcpServer', 'list', optionalSettingsCwd(projectPath) || 'pending'];
}

const MCP_TOOL_DEFINITIONS = [
  {
    id: 'sqlite',
    title: 'SQLite MCP',
    description: '使用 @bytebase/dbhub 暴露本地 Super-Dolphin SQLite 数据库。',
    Icon: Database,
    testId: 'sqlite-mcp-status',
    start: startSQLiteMCPServer,
    stop: stopSQLiteMCPServer,
  },
  {
    id: 'playwright',
    title: 'Playwright MCP',
    description: '使用 @playwright/mcp@latest 提供浏览器自动化 MCP 工具。',
    Icon: MousePointer2,
    testId: 'playwright-mcp-status',
    start: startPlaywrightMCPServer,
    stop: stopPlaywrightMCPServer,
  },
];

function mcpServerMap(response) {
  if (!response || typeof response !== 'object' || Array.isArray(response)) return {};
  const servers = response.mcpServers || response.mcp_servers || {};
  return servers && typeof servers === 'object' && !Array.isArray(servers) ? servers : {};
}

function mcpServerConfig(response, serverName) {
  const servers = mcpServerMap(response);
  const config = servers[serverName];
  return config && typeof config === 'object' && !Array.isArray(config) ? config : null;
}

function mcpServerStatus(projectReady, query, serverName) {
  if (!projectReady) return { label: '未选择项目', tone: 'missing' };
  if (query.isLoading || (query.isFetching && !query.data)) return { label: '读取中', tone: 'loading' };
  if (query.isError) return { label: '读取失败', tone: 'error' };
  const config = mcpServerConfig(query.data, serverName);
  if (!config) return { label: '未配置', tone: 'missing' };
  return config.enabled === false
    ? { label: '已关闭', tone: 'disabled' }
    : { label: '已开启', tone: 'enabled' };
}

function mergeMCPServerEnabled(response, result, serverName, enabled) {
  const current = response && typeof response === 'object' && !Array.isArray(response) ? response : {};
  const servers = mcpServerMap(current);
  const resultName = (result?.serverName || result?.server_name || serverName || '').toString().trim();
  if (!resultName) return current;
  const existingConfig = servers[resultName];
  const existing = existingConfig && typeof existingConfig === 'object' && !Array.isArray(existingConfig) ? existingConfig : {};
  const nextConfig = {
    ...existing,
    ...(result?.config && typeof result.config === 'object' && !Array.isArray(result.config) ? result.config : {}),
    enabled,
  };
  return {
    ...current,
    mcpServers: {
      ...servers,
      [resultName]: nextConfig,
    },
  };
}

function PluginsSquareView({ copy, projectPath }) {
  const projectReady = Boolean(optionalSettingsCwd(projectPath));
  const queryClient = useQueryClient();
  const [mcpActions, setMCPActions] = useState({});
  const [mcpNotices, setMCPNotices] = useState({});
  const [mcpErrors, setMCPErrors] = useState({});
  const {
    data: mcpServersData,
    error: mcpServersError,
    isError: mcpServersIsError,
    isFetching: mcpServersIsFetching,
    isLoading: mcpServersIsLoading,
  } = useQuery({
    queryKey: mcpServersListQueryKey(projectPath),
    queryFn: () => listMCPServers(),
    enabled: projectReady,
  });
  const mcpStatusQuery = useMemo(() => ({
    data: mcpServersData,
    isError: mcpServersIsError,
    isFetching: mcpServersIsFetching,
    isLoading: mcpServersIsLoading,
  }), [mcpServersData, mcpServersIsError, mcpServersIsFetching, mcpServersIsLoading]);

  // 本地状态只跟一次按钮操作绑定，真实开关状态仍以 mcpServer/list 的表数据为准。
  const runMCPAction = useCallback(async (tool, action) => {
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
      setMCPErrors((current) => ({ ...current, [tool.id]: `${tool.title} ${label}失败：${errorMessage(error)}` }));
    } finally {
      setMCPActions((current) => ({ ...current, [tool.id]: '' }));
    }
  }, [projectPath, queryClient]);

  return (
    <div className="plugins-square-container">
      <div className="plugins-square-header">
        <h1>{copy.pluginsTitle}</h1>
        <p className="plugins-square-subtitle">{copy.pluginsSubtitle}</p>
      </div>

      <div className="mcp-tool-panel">
        {MCP_TOOL_DEFINITIONS.map((tool) => {
          const status = mcpServerStatus(projectReady, mcpStatusQuery, tool.id);
          const action = mcpActions[tool.id] || '';
          const notice = mcpNotices[tool.id] || '';
          const error = mcpErrors[tool.id] || '';
          const isEnabled = status.tone === 'enabled';
          const nextAction = isEnabled ? 'stop' : 'start';
          const actionLabel = nextAction === 'start' ? '开启' : '关闭';
          const busyLabel = nextAction === 'start' ? '开启中...' : '关闭中...';
          const ActionIcon = nextAction === 'start' ? Power : PowerOff;
          const feedback = !projectReady
            ? '请选择项目后再管理 MCP 工具。'
            : mcpServersIsError
              ? `读取 ${tool.title} 状态失败：${errorMessage(mcpServersError)}`
              : error || notice;
          const feedbackRole = mcpServersIsError || error ? 'alert' : notice ? 'status' : undefined;
          const Icon = tool.Icon;
          return (
            <section className="mcp-tool-card" aria-label={`${tool.title} 控制`} key={tool.id}>
              <div className={`mcp-tool-icon ${tool.id}`} aria-hidden="true">
                <Icon size={20} />
              </div>
              <div className="mcp-tool-main">
                <div className="mcp-tool-title-line">
                  <h2 title={tool.description}>{tool.title}</h2>
                  <span className={`mcp-tool-status is-${status.tone}`} data-testid={tool.testId}>{status.label}</span>
                </div>
                {feedback ? <p className={`mcp-tool-notice${mcpServersIsError || error ? ' is-error' : ''}`} role={feedbackRole}>{feedback}</p> : null}
              </div>
              <div className="mcp-tool-actions">
                <button
                  type="button"
                  aria-label={`${actionLabel} ${tool.title}`}
                  className={nextAction === 'stop' ? 'is-stop' : ''}
                  onClick={() => { void runMCPAction(tool, nextAction); }}
                  disabled={!projectReady || Boolean(action)}
                >
                  <ActionIcon size={15} aria-hidden="true" />
                  <span>{action ? busyLabel : actionLabel}</span>
                </button>
              </div>
            </section>
          );
        })}
      </div>
    </div>
  );
}

function useSkillsPageModel({ projectPath, refreshKey, resolveLaunchPreferences }) {
  const projectCwd = optionalSettingsCwd(projectPath);
  const [query, setQuery] = useState('');
  const [scopeFilter, setScopeFilter] = useState('all');
  const [status, setStatus] = useState({ projectCwd, error: '', notice: '' });
  if (status.projectCwd !== projectCwd) {
    setStatus({ projectCwd, error: '', notice: '' });
  }
  const setError = useCallback((value) => {
    setStatus((current) => ({ ...current, error: typeof value === 'function' ? value(current.error) : value }));
  }, []);
  const setNotice = useCallback((value) => {
    setStatus((current) => ({ ...current, notice: typeof value === 'function' ? value(current.notice) : value }));
  }, []);
  const dashboard = useSkillsDashboard(projectCwd, refreshKey);
  const filters = useSkillsFilters(dashboard.items, query, scopeFilter);
  const editor = useSkillEditor({ projectPath, refreshSkillSurface: dashboard.refreshSkillSurface, resolveLaunchPreferences, setError, setNotice, skills: dashboard.items });
  const resolution = useSkillResolution({ projectPath, refreshSkillSurface: dashboard.refreshSkillSurface, resetKey: projectCwd, resolutionConflicts: dashboard.resolutionConflicts, setError, setNotice });
  const error = status.projectCwd === projectCwd ? status.error : '';
  const notice = status.projectCwd === projectCwd ? status.notice : '';
  return { dashboard, editor, error, filters, isProjectPending: !projectCwd, notice, query, resolution, scopeFilter, setQuery, setScopeFilter };
}

function useSkillsDashboard(projectCwd, refreshKey) {
  const queryClient = useQueryClient();
  const skillRefreshKey = Number(refreshKey || 0);
  const {
    data: skillsData,
    error: skillsError,
    isPending: skillsPending,
    refetch: refetchSkills,
  } = useQuery({
    queryKey: dashboardQueryKey(projectCwd, 'skills', `revision:${skillRefreshKey}`),
    queryFn: async () => {
      const data = await fetchSkillsDashboard(projectCwd);
      queryClient.setQueryData(dashboardQueryKey(projectCwd, 'skills'), data);
      return data;
    },
    enabled: Boolean(projectCwd),
    initialData: () => queryClient.getQueryData(dashboardQueryKey(projectCwd, 'skills')),
    initialDataUpdatedAt: 0,
    placeholderData: (previousData) => previousData,
  });
  const {
    data: resolutionsData,
    error: resolutionsError,
    isPending: resolutionsPending,
    refetch: refetchResolutions,
  } = useQuery({
    queryKey: dashboardQueryKey(projectCwd, 'skill-resolutions', `revision:${skillRefreshKey}`),
    queryFn: async () => {
      const data = await fetchSkillResolutionsDashboard(projectCwd);
      queryClient.setQueryData(dashboardQueryKey(projectCwd, 'skill-resolutions'), data);
      return data;
    },
    enabled: Boolean(projectCwd),
    initialData: () => queryClient.getQueryData(dashboardQueryKey(projectCwd, 'skill-resolutions')),
    initialDataUpdatedAt: 0,
    placeholderData: (previousData) => previousData,
  });
  const skillsQuery = { data: skillsData, error: skillsError, isPending: skillsPending };
  const resolutionsQuery = { data: resolutionsData, error: resolutionsError, isPending: resolutionsPending };
  const items = useMemo(() => (Array.isArray(skillsData) ? skillsData : []), [skillsData]);
  const resolutionConflicts = useMemo(() => (Array.isArray(resolutionsData) ? resolutionsData : []), [resolutionsData]);
  const refreshSkillSurface = useCallback(async () => {
    if (!projectCwd) return;
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: dashboardQueryKey(projectCwd, 'skills') }),
      queryClient.invalidateQueries({ queryKey: dashboardQueryKey(projectCwd, 'skill-resolutions') }),
    ]);
  }, [projectCwd, queryClient]);
  const retrySkillSurface = useCallback(async () => {
    if (!projectCwd) return;
    await Promise.all([refetchSkills(), refetchResolutions()]);
  }, [projectCwd, refetchResolutions, refetchSkills]);
  useSkillSurfaceRefresh(projectCwd, refreshSkillSurface);
  return skillsDashboardState({ items, projectCwd, resolutionConflicts, resolutionsQuery, retrySkillSurface, refreshSkillSurface, skillsQuery });
}

function skillsDashboardState({ items, projectCwd, resolutionConflicts, resolutionsQuery, retrySkillSurface, refreshSkillSurface, skillsQuery }) {
  const hasSnapshot = Array.isArray(skillsQuery.data);
  const hasResolutionSnapshot = Array.isArray(resolutionsQuery.data);
  const resolutionSyncErrorText = resolutionsQuery.error ? '读取技能冲突失败：' + errorMessage(resolutionsQuery.error) : '';
  const syncErrorText = skillsSyncErrorText(skillsQuery, resolutionsQuery);
  return {
    items,
    isInitialLoading: Boolean(projectCwd) && skillsQuery.isPending && !hasSnapshot && !syncErrorText,
    isResolutionPending: Boolean(projectCwd) && resolutionsQuery.isPending && !hasResolutionSnapshot && !resolutionSyncErrorText,
    refreshSkillSurface,
    resolutionConflicts,
    resolutionSyncErrorText,
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

function useSkillSurfaceRefresh(projectCwd, refreshSkillSurface) {
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
  const countText = skillCountText({ counts, filteredCount: filteredItems.length, query, scopeFilter });
  return { counts, countText, filteredItems, scopeOptions };
}

function skillCountText({ counts, filteredCount, query, scopeFilter }) {
  if (counts.all === 0) return '';
  if (scopeFilter === 'all' && !query.trim()) return `共 ${counts.all} 个技能`;
  if (filteredCount === 0) return `当前没有匹配技能，共 ${counts.all} 个`;
  return `显示 ${filteredCount} 个，共 ${counts.all} 个技能`;
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

function useSkillEditor({ projectPath, refreshSkillSurface, resolveLaunchPreferences, setError, setNotice, skills }) {
  const [state, setState] = useState(defaultSkillEditorState);
  const setPatch = useCallback((patch) => setState((current) => ({ ...current, ...patch })), []);
  const setForm = useCallback((updater) => setState((current) => ({ ...current, editorForm: typeof updater === 'function' ? updater(current.editorForm) : updater })), []);
  const actions = useMemo(() => skillEditorActions({ projectPath, refreshSkillSurface, resolveLaunchPreferences, setError, setForm, setNotice, setPatch, skills, state }), [projectPath, refreshSkillSurface, resolveLaunchPreferences, setError, setForm, setNotice, setPatch, skills, state]);
  return { ...state, ...actions, setForm };
}

function defaultSkillEditorState() {
  return { activeSkillPath: '', deleteTarget: null, deleting: false, editorForm: emptySkillForm(), editorOpen: false, importScopeOpen: false, importSummaryDrafts: [], importing: false, saving: false, skillFiles: [], summarySuggestion: '', summarySuggesting: false };
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
    clearImportSummaryDrafts: () => ctx.setPatch({ importSummaryDrafts: [] }),
    confirmDeleteSkill: () => confirmDeleteSkill(ctx),
    confirmImportScope: (scope) => confirmImportScope(ctx, scope),
    applyImportSummaryDraft: (draft) => applyImportSummaryDraft(ctx, draft),
    dismissImportSummaryDraft: (draft) => dismissImportSummaryDraft(ctx, draft),
    openImportSummaryDraft: (draft) => openImportSummaryDraft(ctx, draft),
    onDeleteSkill: (skill) => ctx.setPatch({ deleteTarget: skill }),
    openCreateEditor: () => openCreateSkillEditor(ctx),
    openEditSkill: (skill) => openEditSkill(ctx, skill),
    openImportScope: () => ctx.setPatch({ importScopeOpen: true }),
    openSkillFile: (file) => openSkillFile(ctx, file),
    openSkillCitation: (target, label) => openSkillCitation(ctx, target, label),
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
  const skillPath = skillFileForItem(skill);
  const skillDir = (skill?.dir || skillPreviewDir(skillPath)).toString().trim();
  if (!skillPath || !skillDir) { ctx.setError('skills/local/read: path is required'); return; }
  ctx.setError(''); ctx.setNotice(''); ctx.setPatch({ summarySuggestion: '' });
  try {
    const cwd = normalizeSettingsCwd(ctx.projectPath);
    const [rawSkill, rawFiles] = await Promise.all([readSkill({ cwd, path: skillPath }), listSkillFiles({ cwd, dir: skillDir })]);
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

async function openSkillCitation(ctx, target, label = '') {
  const citation = skillCitationFromLink(target, label);
  if (!citation) return false;
  ctx.setError('');
  if (citation.kind === 'conversation') {
    ctx.setNotice('暂不支持会话跳转：' + (citation.conversationId || citation.raw || '未命名会话'));
    return false;
  }
  const skill = findSkillForCitation(ctx.skills, citation);
  if (!skill) {
    ctx.setNotice('未找到引用的技能：' + (citation.skillName || citation.path || citation.skillId || citation.raw || '未命名技能'));
    return false;
  }
  await openEditSkill(ctx, skill);
  return true;
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
    const payload = skillSavePayload(normalizeSettingsCwd(ctx.projectPath), ctx.state);
    if (shouldCreateProjectSkill(ctx.state)) {
      await createSkill({ cwd: payload.cwd, name: payload.path, content: payload.content });
    } else {
      await writeSkill(payload);
    }
    ctx.setPatch({ editorOpen: false });
    await ctx.refreshSkillSurface();
    ctx.setNotice(skillSaveNotice(ctx.state, payload));
  } catch (err) {
    ctx.setError('保存失败：' + (err.message || String(err)));
  } finally {
    ctx.setPatch({ saving: false });
  }
}

function shouldCreateProjectSkill(state) {
  return !state.activeSkillPath && state.editorForm.scope === 'project';
}

function skillSaveNotice(state, payload) {
  if (state.activeSkillPath && !isMainSkillFile(state.activeSkillPath)) {
    return '文件已保存：' + (fileNameFromPath(payload.path) || payload.path);
  }
  return '已保存';
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
    const cwd = normalizeSettingsCwd(ctx.projectPath);
    const personalType = scope === 'personal' ? 'imported' : '';
    const result = await importSkillDirectories({ cwd, paths, scope, personal_type: personalType });
    const failures = Array.isArray(result?.failures) ? result.failures.map(normalizeImportFailure) : [];
    const importSummaryDrafts = await createImportSummaryDrafts(ctx, result?.imported, scope, personalType);
    ctx.setPatch({ importSummaryDrafts });
    await ctx.refreshSkillSurface();
    ctx.setNotice(importNotice(Array.isArray(result?.imported) ? result.imported.length : 0, importSummaryDrafts, failures, scope));
  } catch (err) {
    ctx.setError('导入目录失败：' + (err.message || String(err)));
  } finally {
    ctx.setPatch({ importing: false });
  }
}

async function createImportSummaryDrafts(ctx, importedSkills, scope, personalType) {
  if (!Array.isArray(importedSkills) || importedSkills.length === 0) return [];
  const cwd = normalizeSettingsCwd(ctx.projectPath);
  const draftResults = await Promise.all(
    importedSkills.map((item, index) => createImportSummaryDraft(ctx, cwd, item, scope, personalType, index)),
  );
  const drafts = [];
  for (const draft of draftResults) {
    if (draft) drafts.push(draft);
  }
  return drafts;
}

async function createImportSummaryDraft(ctx, cwd, item, scope, personalType, index) {
  const skillFile = importedSkillFilePath(item);
  if (!skillFile) return null;
  const fallbackName = (item?.name || '').toString().trim() || skillFileBaseName(skillFile);
  const baseDraft = {
    id: `${index}:${skillFile || fallbackName}`,
    name: fallbackName,
    skillFile,
    scope: normalizeImportSummaryDraftScope(scope),
    personalType,
    suggestion: '',
    status: 'ready',
    error: '',
  };
  try {
    const raw = await readSkill({ cwd, path: skillFile });
    const parsed = parseSkillMarkdown((raw?.skill?.content || '').toString(), fallbackName);
    const currentDescription = (parsed.description || '').toString().trim();
    if (currentDescription) return null;
    const suggestion = await suggestSkillSummary({
      cwd,
      name: parsed.name || fallbackName,
      description: currentDescription,
      content: parsed.body,
      scenario_words: parsed.triggerWords,
      scope: normalizeImportSummaryDraftScope(scope),
    });
    if (!suggestion) return null;
    return { ...baseDraft, name: parsed.name || fallbackName, suggestion };
  } catch (err) {
    if (isImportedSkillSameNameConflictError(err)) {
      return { ...baseDraft, status: 'conflict', error: importedSkillSameNameConflictMessage(baseDraft) };
    }
    return { ...baseDraft, status: 'error', error: '技能已正常导入。可以稍后重试，或手动补充简介。' };
  }
}

async function openImportSummaryDraft(ctx, draft) {
  if (!draft?.skillFile) return false;
  ctx.setError('');
  try {
    const cwd = normalizeSettingsCwd(ctx.projectPath);
    const raw = await readSkill({ cwd, path: draft.skillFile });
    ctx.setPatch({
      activeSkillPath: draft.skillFile,
      editorForm: {
        ...skillFormFromRaw(raw, { name: draft.name, title: draft.name, description: '', tags: [], scope: draft.scope, personalType: draft.personalType }),
        scope: draft.scope,
        personalType: draft.personalType,
      },
      editorOpen: true,
      skillFiles: [{ name: 'SKILL.md', path: draft.skillFile, isMain: true }],
      summarySuggestion: '',
    });
    return true;
  } catch (err) {
    ctx.setError('打开技能失败：' + (err.message || String(err)));
    return false;
  }
}

async function applyImportSummaryDraft(ctx, draft) {
  if (!draft || draft.status !== 'ready') return;
  const suggestion = (draft.suggestion || '').toString().trim();
  if (!suggestion) return;
  const opened = await openImportSummaryDraft(ctx, draft);
  if (!opened) return;
  ctx.setForm((form) => ({ ...form, description: suggestion }));
  ctx.setPatch({
    importSummaryDrafts: ctx.state.importSummaryDrafts.map((item) => (item.id === draft.id ? { ...item, status: 'applied' } : item)),
  });
  ctx.setNotice('已采用简介建议，保存技能后生效。');
}

function dismissImportSummaryDraft(ctx, draft) {
  ctx.setPatch({ importSummaryDrafts: ctx.state.importSummaryDrafts.filter((item) => item.id !== draft?.id) });
}

function useSkillResolution({ projectPath, refreshSkillSurface, resetKey, resolutionConflicts, setError, setNotice }) {
  const [resolutionState, setResolutionState] = useState({ resetKey, preview: null, namePrompt: null, nameInput: '' });
  const [actioning, setActioning] = useState('');
  const stateShouldReset = (
    resolutionState.resetKey !== resetKey
    || (resolutionConflicts.length === 0 && (resolutionState.preview || resolutionState.namePrompt || resolutionState.nameInput))
  );
  if (stateShouldReset) {
    setResolutionState({ resetKey, preview: null, namePrompt: null, nameInput: '' });
  }
  const preview = stateShouldReset ? null : resolutionState.preview;
  const namePrompt = stateShouldReset ? null : resolutionState.namePrompt;
  const nameInput = stateShouldReset ? '' : resolutionState.nameInput;
  const setPreview = useCallback((value) => {
    setResolutionState((current) => ({ ...current, preview: typeof value === 'function' ? value(current.preview) : value }));
  }, []);
  const setNamePrompt = useCallback((value) => {
    setResolutionState((current) => ({ ...current, namePrompt: typeof value === 'function' ? value(current.namePrompt) : value }));
  }, []);
  const setNameInput = useCallback((value) => {
    setResolutionState((current) => ({ ...current, nameInput: typeof value === 'function' ? value(current.nameInput) : value }));
  }, []);
  const reset = useCallback(() => setResolutionState((current) => ({ ...current, preview: null, namePrompt: null, nameInput: '' })), []);
  const runAction = useCallback((conflict, actionOrEntry, entry = null, newName = '') => runResolutionPipeline({ actionOrEntry, actioning, conflict, entry, newName, projectPath, refreshSkillSurface, setActioning, setError, setNameInput, setNamePrompt, setNotice, setPreview }), [actioning, projectPath, refreshSkillSurface, setError, setNameInput, setNamePrompt, setNotice, setPreview]);
  const confirmName = useCallback(() => confirmResolutionName({ nameInput, namePrompt, runAction, setError, setNameInput, setNamePrompt }), [nameInput, namePrompt, runAction, setError, setNameInput, setNamePrompt]);
  const confirmPreview = useCallback(() => confirmResolutionPreview({ preview, refreshSkillSurface, setActioning, setError, setNameInput, setNamePrompt, setNotice, setPreview }), [preview, refreshSkillSurface, setError, setNameInput, setNamePrompt, setNotice, setPreview]);
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

function SkillsPageView({ copy, model }) {
  return (
    <section className="console-page">
      <PageHeader icon={Sparkles} title={copy.title} subtitle={copy.subtitle} />
      <SkillsOverview copy={copy} model={model} />
      <div className="subhead">{copy.localLibrary}</div>
      <SkillsToolbar copy={copy} model={model} />
      <SkillFilter copy={copy} filters={model.filters} scopeFilter={model.scopeFilter} setScopeFilter={model.setScopeFilter} />
      <SkillsStatus copy={copy} model={model} />
      <SkillImportSummaryPanel editor={model.editor} />
      <SkillResolutionPanel model={model} />
      <SkillGrid copy={copy} model={model} />
      <SkillModals model={model} />
    </section>
  );
}

function SkillsOverview({ copy, model }) {
  const counts = model.filters.counts;
  const conflictValue = model.isProjectPending || model.dashboard.isResolutionPending || model.dashboard.resolutionSyncErrorText
    ? copy.pending
    : model.dashboard.resolutionConflicts.length;
  return (
    <section className="skills-overview" aria-label={copy.overviewAria}>
      <div className="skills-overview-copy">
        <span>{copy.currentConnection}</span>
        <h2>{copy.overviewTitle}</h2>
      </div>
      <dl>
        <div><dt>{copy.localSkills}</dt><dd>{counts.all}</dd></div>
        <div><dt>{copy.projectShared}</dt><dd>{counts.project}</dd></div>
        <div><dt>{copy.personalUse}</dt><dd>{counts.personal}</dd></div>
        <div><dt>{copy.pendingConflicts}</dt><dd>{conflictValue}</dd></div>
      </dl>
    </section>
  );
}

function SkillImportSummaryPanel({ editor }) {
  const drafts = Array.isArray(editor.importSummaryDrafts) ? editor.importSummaryDrafts : [];
  if (drafts.length === 0) return null;
  return (
    <section className="skills-import-summary-panel" data-testid="skills-import-summary-panel">
      <div className="skills-import-summary-head">
        <div><strong>{importSummaryPanelTitle(drafts)}</strong><span>{importSummaryPanelHint(drafts)}</span></div>
        <button type="button" className="ghost" data-testid="skills-import-summary-clear" onClick={editor.clearImportSummaryDrafts}>收起</button>
      </div>
      {drafts.map((draft, index) => <SkillImportSummaryItem draft={draft} editor={editor} index={index} key={draft.id || index} />)}
    </section>
  );
}

function SkillImportSummaryItem({ draft, editor, index }) {
  return (
    <article className={'skills-import-summary-item is-' + draft.status} data-testid={'skills-import-summary-item-' + index}>
      <div className="skills-import-summary-main"><strong>{draft.name || '未命名技能'}</strong><span>{scopeLabel(draft.scope)}</span></div>
      <p className="skills-import-summary-text">{draft.status === 'ready' || draft.status === 'applied' ? draft.suggestion : (draft.error || '技能已正常导入。可以稍后手动补充简介。')}</p>
      <div className="skills-import-summary-actions">
        {draft.status === 'ready' ? <button type="button" data-testid={'skills-import-summary-apply-' + index} onClick={() => { void editor.applyImportSummaryDraft(draft); }}>采用并编辑</button> : null}
        {draft.status === 'applied' ? <span className="skills-inline-tip">已采用，保存后生效</span> : null}
        {draft.status === 'error' ? <button type="button" data-testid={'skills-import-summary-edit-' + index} onClick={() => { void editor.openImportSummaryDraft(draft); }}>编辑简介</button> : null}
        <button type="button" className="ghost" data-testid={'skills-import-summary-dismiss-' + index} onClick={() => editor.dismissImportSummaryDraft(draft)}>跳过</button>
      </div>
    </article>
  );
}

function SkillsToolbar({ copy, model }) {
  return (
    <div className="skills-toolbar">
      <button type="button" onClick={model.editor.openImportScope} disabled={model.editor.importing}>{copy.importDirs}</button>
      <button type="button" className="ghost" onClick={model.editor.openCreateEditor}>{copy.newSkill}</button>
      <label><Search size={18} /><input value={model.query} onChange={(event) => model.setQuery(event.target.value)} placeholder={copy.searchSkillsPlaceholder} aria-label={copy.searchSkills} /></label>
    </div>
  );
}

function SkillFilter({ copy, filters, scopeFilter, setScopeFilter }) {
  const labels = { personal: copy.personalUse, project: copy.projectShared, all: copy.scopeAll };
  return (
    <div className="skill-filter">
      {filters.scopeOptions.map(([value]) => <button key={value} type="button" className={scopeFilter === value ? 'active' : ''} onClick={() => setScopeFilter(value)}>{labels[value]} {filters.counts[value]}</button>)}
    </div>
  );
}

function SkillsStatus({ copy, model }) {
  return (
    <>
      {model.isProjectPending ? <p className="console-message">{copy.connecting}</p> : null}
      {model.dashboard.isInitialLoading ? <p className="console-message">{copy.loading}</p> : null}
      {model.notice ? <p className="settings-status">{model.notice}</p> : null}
      {model.dashboard.showCachedSyncError ? <CachedSkillSyncError copy={copy} dashboard={model.dashboard} /> : null}
      {model.dashboard.showBlockingSyncError ? <RetryableSyncError className="danger-text skills-sync-alert" message={model.dashboard.syncErrorText} onRetry={model.dashboard.retrySkillSurface} /> : null}
      {model.error ? <p className="danger-text" role="alert">{model.error}</p> : null}
    </>
  );
}

function CachedSkillSyncError({ copy, dashboard }) {
  return (
    <div className="danger-text skills-sync-alert" role="alert">
      <span>同步失败，显示的是上次成功的数据：{dashboard.syncErrorText}</span>
      <button type="button" className="ghost" onClick={() => { void dashboard.retrySkillSurface(); }}>{copy.retrySync}</button>
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
  const manualSteps = resolutionManualSteps(conflict);
  return (
    <article className="skills-resolution-item">
      <header><h3>{conflict.name || conflict.skill_name || '未命名技能'} · {resolutionKindLabel(conflict.kind)}</h3><span>{scopeLabel(scopeForSkill(conflict))}</span></header>
      <p className="skills-resolution-guide">{resolutionConflictGuide(conflict)}</p>
      {resolutionProviderEntries(conflict).map((entry, sourceIndex) => <SkillResolutionActionRow conflict={conflict} conflictID={conflictID} providerEntry={entry} resolution={resolution} sourceIndex={sourceIndex} key={conflictID + ':' + sourceIndex + ':' + resolutionProviderEntryLabel(entry)} />)}
      {manualSteps.length > 0 ? <ul className="skills-resolution-manual-steps">{manualSteps.map((step) => <li key={step}>{step}</li>)}</ul> : null}
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
      <p className="skills-resolution-guide">{resolutionPreviewIntro(resolution.preview)}</p>
      {(resolution.preview.items || []).map((item, index) => <SkillResolutionPreviewItem action={resolution.preview.action} item={item} key={item.preview_id || index} />)}
    </article>
  );
}

function SkillResolutionPreviewItem({ action, item }) {
  const sourceHash = resolutionShortHash(item.source_hash || item.sourceHash);
  const targetHash = resolutionShortHash(item.target_hash || item.targetHash);
  const diff = (item.diff || '').toString();
  return (
    <div className="skills-resolution-preview-item">
      {previewItemPaths(item, action).map((pathItem) => <p key={pathItem.label + ':' + pathItem.value}><span>{pathItem.label}</span><code>{pathItem.value}</code></p>)}
      {sourceHash || targetHash || diff ? (
        <details className="skills-resolution-technical" open>
          <summary>技术信息</summary>
          {sourceHash ? <div className="skills-resolution-preview-path">外部版本号：{sourceHash}</div> : null}
          {targetHash ? <div className="skills-resolution-preview-path">管理版本号：{targetHash}</div> : null}
          {diff ? <pre className="skills-resolution-diff">{diff}</pre> : null}
        </details>
      ) : null}
    </div>
  );
}

function SkillGrid({ copy, model }) {
  const showReadyEmpty = !model.isProjectPending && !model.dashboard.isInitialLoading && !model.dashboard.showBlockingSyncError && model.filters.filteredItems.length === 0;
  return (
    <>
      {showReadyEmpty ? <SkillsEmptyState copy={copy} hasSkills={model.filters.counts.all > 0} /> : null}
      {model.filters.filteredItems.length > 0 ? <div className="skill-grid">{model.filters.filteredItems.map((skill) => <SkillCard copy={copy} key={skill.id} skill={skill} onEdit={model.editor.openEditSkill} onDelete={model.editor.onDeleteSkill} />)}</div> : null}
      {model.filters.countText ? <p className="skills-inline-tip">{model.filters.countText}</p> : null}
    </>
  );
}

function SkillsEmptyState({ copy, hasSkills }) {
  if (hasSkills) {
    return (
      <div className="empty-state">
        <h3>{copy.noMatchesTitle}</h3>
        <p>{copy.noMatchesText}</p>
      </div>
    );
  }
  return <p className="console-message">{copy.empty}</p>;
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
      key={editor.activeSkillPath || 'new'}
      form={editor.editorForm}
      setForm={editor.setForm}
      activeSkillPath={editor.activeSkillPath}
      files={editor.skillFiles}
      summarySuggestion={editor.summarySuggestion}
      summarySuggesting={editor.summarySuggesting}
      saving={editor.saving}
      onSuggestSummary={editor.suggestSummary}
      onApplySummary={editor.applySummary}
      onOpenCitation={editor.openSkillCitation}
      onOpenFile={editor.openSkillFile}
      onClose={editor.closeEditor}
      onSave={editor.saveEditor}
    />
  );
}

function SkillCard({ copy, skill, onEdit, onDelete }) {
  const tags = skill.tags.slice(0, 4);
  const extraTagCount = skill.tags.length - tags.length;
  const descriptionText = (skill.description || '').toString().trim();
  const summaryText = (skill.summary || '').toString().trim();
  const description = descriptionText || summaryText || copy.noDescription;
  const shouldShowSummary = Boolean(summaryText && summaryText !== description);

  return (
    <article className="skill-card">
      <header><h3>{skill.title}</h3><span>{scopeLabel(skill.scope)}</span></header>
      <p className="path">{skill.dir || copy.noPath}</p>
      <p>{description}</p>
      {shouldShowSummary ? <div className="quote">{summaryText}</div> : null}
      <small>{copy.keywords}</small>
      <div className="tags">
        {tags.length > 0 ? tags.map((tag) => <span key={tag}>{tag}</span>) : <span>{copy.noKeywords}</span>}
        {extraTagCount > 0 ? <span>+{extraTagCount}</span> : null}
      </div>
      <footer>
        <button type="button" onClick={() => { void onEdit(skill); }} disabled={!skill.dir}>{copy.editDetails}</button>
        <button type="button" className="text-danger" onClick={() => { void onDelete(skill); }} disabled={!skill.name}>{copy.delete}</button>
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
  onOpenCitation,
  onOpenFile,
  onClose,
  onSave,
}) {
  const isMain = !activeSkillPath || isMainSkillFile(activeSkillPath);
  const modalTitle = activeSkillPath ? '编辑技能' : '新建技能';
  const saveLabel = isMain ? '保存技能' : '保存文件';
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
  return (
    <FocusTrapDialog ariaLabel={modalTitle} className="modal-box skills-editor-modal" closeDisabled={saving} onClose={onClose}>
      <SkillEditorHeader modalTitle={modalTitle} />
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
      <SkillEditorBody activeSkillPath={activeSkillPath} bodyEditing={bodyEditing} files={files} form={form} isMain={isMain} onOpenCitation={onOpenCitation} onOpenFile={onOpenFile} setBodyEditing={setBodyEditing} update={update} />
      <footer>
        <button type="button" className="ghost" onClick={onClose} disabled={saving}>取消</button>
        <button type="button" onClick={() => { void onSave(); }} disabled={saving}>{saving ? '保存中...' : saveLabel}</button>
      </footer>
    </FocusTrapDialog>
  );
}

function SkillEditorHeader({ modalTitle }) {
  return (
    <header className="skills-editor-modal-head">
      <div><h2>{modalTitle}</h2><p>你可以修改简介和技能内容。</p></div>
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
        <fieldset className="skills-scope-segmented">
          <legend className="sr-only">使用范围</legend>
          <button type="button" className={form.scope === 'project' ? 'active' : ''} disabled={!isMain} onClick={() => setForm((current) => ({ ...current, scope: 'project' }))}>项目共享</button>
          <button type="button" className={form.scope === 'personal' ? 'active' : ''} disabled={!isMain} onClick={() => setForm((current) => ({ ...current, scope: 'personal' }))}>私人使用</button>
        </fieldset>
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

function SkillEditorBody({ activeSkillPath, bodyEditing, files, form, isMain, onOpenCitation, onOpenFile, setBodyEditing, update }) {
  const openPreviewPath = (path, label) => {
    if (skillCitationFromLink(path, label)) {
      void onOpenCitation(path, label);
      return;
    }
    const file = resolveSkillPreviewFile(path, files, activeSkillPath);
    if (file) void onOpenFile(file);
  };
  return (
    <div className="skills-body-field">
      <div className="skills-body-head">
        <span>{isMain ? '技能内容' : '关联文件内容'}</span>
        {bodyEditing ? <button type="button" className="ghost" onClick={() => setBodyEditing(false)}>预览正文</button> : <button type="button" onClick={() => setBodyEditing(true)}>编辑正文</button>}
      </div>
      {bodyEditing ? <textarea value={form.body} onChange={update('body')} aria-label={isMain ? '技能内容' : '关联文件内容'} /> : <div className="skills-body-preview" data-testid="skills-editor-body-preview"><SkillMarkdownPreview content={form.body} onOpenPath={openPreviewPath} /></div>}
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
