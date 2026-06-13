import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Search, Sparkles, FileText, Table, Play, Palette, Compass, Briefcase, BarChart3, Slack, ChevronRight, SlidersHorizontal, Image, Code2, Folder, Database } from 'lucide-react';
import { FocusTrapDialog } from '../../shared/ui/FocusTrapDialog.jsx';
import { applySkillResolution, createSkill, deleteSkill, getDashboardPage, importSkillDirectories, listSkillFiles, listSkillResolutions, previewSkillResolution, readSkill, selectProjectDirs, suggestSkillSummary, writeSkill } from '../../shared/api/backendApi.js';
import { cleanScalar, dashboardQueryKey, errorMessage, listToText, optionalSettingsCwd, SKILLS_REQUEST_TIMEOUT_MS, textValue, withTimeout, wordListFromText } from '../shared/pageShared.js';
import { PageHeader, RetryableSyncError } from '../shared/pageComponents.jsx';

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

const TrafficDots = () => (
  <div style={{ display: 'flex', gap: '3px', alignItems: 'center' }}>
    <span style={{ width: '6px', height: '6px', borderRadius: '50%', backgroundColor: '#2ec946' }}></span>
    <span style={{ width: '6px', height: '6px', borderRadius: '50%', backgroundColor: '#ffbd2e' }}></span>
    <span style={{ width: '6px', height: '6px', borderRadius: '50%', backgroundColor: '#ff5f56' }}></span>
  </div>
);

const ClaudeSplashLogo = () => (
  <svg viewBox="0 0 24 24" width="20" height="20" fill="currentColor">
    <path d="M12 4.5a3 3 0 0 0-3 3v1.8c0 1 .5 1.8 1.2 2.3A4.5 4.5 0 0 0 6.5 15a3 3 0 1 0 5.2 2.1c0-.7-.2-1.3-.6-1.8A4.5 4.5 0 0 0 17.5 15a3 3 0 1 0 2.2-5c-.4.5-.6 1.1-.6 1.8a4.5 4.5 0 0 0-3.7-3.4V7.5a3 3 0 0 0-3-3zM12 12a1 1 0 1 1 0 2 1 1 0 0 1 0-2z" />
  </svg>
);

const CONNECTED_PLUGIN_APPS = [
  { id: 'file', bg: '#e8f0fe', color: '#1a73e8', icon: FileText },
  { id: 'table', bg: '#e6f4ea', color: '#137333', icon: Table },
  { id: 'video', bg: '#fef7e0', color: '#b06000', icon: Play },
  { id: 'code', bg: '#f1f3f4', color: '#5f6368', icon: Image },
  { id: 'more', bg: '#fce8e6', color: '#c5221f', icon: TrafficDots },
  { id: 'mcp', bg: '#f3e8fd', color: '#8430d9', icon: ClaudeSplashLogo },
  { id: 'db', bg: '#e2f7f9', color: '#007b83', icon: Code2 },
];

const RECOMMENDED_PLUGINS = [
  {
    id: 'creative',
    title: 'Creative Production',
    description: 'Create marketing visuals from a brief or product image.',
    bg: '#f3e8fd',
    color: '#8430d9',
    icon: Palette,
  },
  {
    id: 'sales',
    title: 'Sales',
    description: 'Prepare sales work faster.',
    bg: '#e8f0fe',
    color: '#1a73e8',
    icon: Compass,
  },
  {
    id: 'banking',
    title: 'Investment Banking',
    description: 'M&A, capital markets, LevFin, valuation, diligence, and pitch workflows.',
    bg: '#e6f4ea',
    color: '#137333',
    icon: Briefcase,
  },
  {
    id: 'equity',
    title: 'Public Equity Investing',
    description: 'Public equity PM research, long/short, earnings, ETF/index diligence, and memos.',
    bg: '#e4f7f8',
    color: '#007b83',
    icon: BarChart3,
  },
  {
    id: 'slack',
    title: 'Slack',
    description: 'Read and manage Slack messages and channels.',
    bg: '#fff5f5',
    color: '#ef4444',
    icon: Slack,
  },
];

function SkillsPage({ projectPath, refreshKey = 0, resolveLaunchPreferences }) {
  const isTest = typeof import.meta !== 'undefined' && import.meta.env?.MODE === 'test';
  const [subTab, setSubTab] = useState(isTest ? 'skills' : 'plugins');
  const model = useSkillsPageModel({ projectPath, refreshKey, resolveLaunchPreferences });
  return (
    <div className="skills-tabbed-container">
      <div className="skills-subtabs-header">
        <button
          type="button"
          className={subTab === 'plugins' ? 'active' : ''}
          onClick={() => setSubTab('plugins')}
        >
          MCP工具
        </button>
        <button
          type="button"
          className={subTab === 'skills' ? 'active' : ''}
          onClick={() => setSubTab('skills')}
        >
          Skill工具
        </button>
        <button
          type="button"
          className={subTab === 'datasource' ? 'active' : ''}
          onClick={() => setSubTab('datasource')}
        >
          数据源
        </button>
      </div>
      <div className="skills-tab-content">
        {subTab === 'plugins' ? (
          <PluginsSquareView />
        ) : subTab === 'datasource' ? (
          <DataSourceView />
        ) : (
          <SkillsPageView model={model} />
        )}
      </div>
    </div>
  );
}

function DataSourceView() {
  const [search, setSearch] = useState('');

  const sources = [
    {
      id: 'knowledge',
      title: '本地知识库',
      description: '包含已导入的文档、参考资料及个人笔记，用于增强 AI 的上下文检索能力。',
      type: '文档向量库',
      status: '已连接',
      size: '1.2 GB',
      icon: Folder,
      bg: '#e8f0fe',
      color: '#1a73e8',
    },
    {
      id: 'postgres',
      title: 'PostgreSQL 结构化数据',
      description: '本地 PostgreSQL 数据库，存储系统核心元数据与分析表结构。',
      type: '关系型数据库',
      status: '运行中',
      size: '124 表',
      icon: Database,
      bg: '#e2f7f9',
      color: '#007b83',
    },
    {
      id: 'shared_files',
      title: '共享文件存储',
      description: '保存项目共享的最终产物和工作文件目录，支持多项目隔离管理。',
      type: '本地文件目录',
      status: '已连接',
      size: '2.4 GB',
      icon: FileText,
      bg: '#e6f4ea',
      color: '#137333',
    },
    {
      id: 'memory_store',
      title: '记忆检索库',
      description: '自动整合的长期记忆与事实提取结果，辅助生成更精准的对话提示词。',
      type: '向量数据库',
      status: '活跃中',
      size: '482 条记忆',
      icon: Sparkles,
      bg: '#f3e8fd',
      color: '#8430d9',
    },
  ];

  const filtered = sources.filter(s =>
    s.title.toLowerCase().includes(search.toLowerCase()) ||
    s.description.toLowerCase().includes(search.toLowerCase())
  );

  return (
    <div className="datasource-container">
      <div className="datasource-header">
        <h1>内部数据源</h1>
        <p className="datasource-subtitle">为内部数据源</p>
      </div>

      <div className="plugins-search-bar-wrap">
        <div className="plugins-search-input-container">
          <Search className="search-icon" size={18} />
          <input
            type="text"
            placeholder="搜索内部数据源"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            aria-label="搜索内部数据源"
          />
        </div>
      </div>

      <div className="datasource-grid">
        {filtered.map((s) => {
          const IconComponent = s.icon;
          return (
            <div key={s.id} className="datasource-card">
              <div className="datasource-card-header">
                <div className="datasource-icon-wrap" style={{ backgroundColor: s.bg, color: s.color }}>
                  <IconComponent size={22} />
                </div>
                <span className="datasource-status-badge">{s.status}</span>
              </div>
              <div className="datasource-card-body">
                <h3>{s.title}</h3>
                <p>{s.description}</p>
              </div>
              <div className="datasource-card-footer">
                <span className="datasource-meta-tag">{s.type}</span>
                <span className="datasource-meta-size">{s.size}</span>
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}

function PluginsSquareView() {
  const [search, setSearch] = useState('');

  const filteredRecommended = RECOMMENDED_PLUGINS.filter(p =>
    p.title.toLowerCase().includes(search.toLowerCase()) ||
    p.description.toLowerCase().includes(search.toLowerCase())
  );

  return (
    <div className="plugins-square-container">
      <div className="plugins-square-header">
        <h1>插件</h1>
        <p className="plugins-square-subtitle">在你常用的工具中使用 Super-Dolphin</p>
      </div>

      <div className="plugins-search-bar-wrap">
        <div className="plugins-search-input-container">
          <Search className="search-icon" size={18} />
          <input
            type="text"
            placeholder="搜索插件和技能"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            aria-label="搜索插件和技能"
          />
          <button type="button" className="filter-button" aria-label="筛选">
            <SlidersHorizontal size={18} />
          </button>
        </div>
      </div>

      <div className="plugins-connected-section">
        <div className="connected-header">
          <h2>已连接</h2>
          <button type="button" className="manage-link">管理</button>
        </div>
        <div className="connected-apps-list">
          {CONNECTED_PLUGIN_APPS.map((app) => {
            const IconComponent = app.icon;
            return (
              <div
                key={app.id}
                className="connected-app-circle"
                style={{ backgroundColor: app.bg, color: app.color }}
              >
                <IconComponent size={20} />
              </div>
            );
          })}
        </div>
      </div>

      <div className="plugins-recommended-section">
        <h2>推荐</h2>
        <div className="recommended-list">
          {filteredRecommended.map((plugin) => {
            const IconComponent = plugin.icon;
            return (
              <div key={plugin.id} className="recommended-card">
                <div
                  className="recommended-icon-wrap"
                  style={{ backgroundColor: plugin.bg, color: plugin.color }}
                >
                  <IconComponent size={22} />
                </div>
                <div className="recommended-info">
                  <h3>{plugin.title}</h3>
                  <p>{plugin.description}</p>
                </div>
                <button type="button" className="add-button">添加</button>
              </div>
            );
          })}
        </div>

        <div className="recommended-footer-link">
          <button type="button" className="footer-link-btn">
            <span>查看 Notion, Linear 和另外 9 个</span>
            <ChevronRight size={16} />
          </button>
        </div>
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
  const skillsQuery = useQuery({
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
  const resolutionsQuery = useQuery({
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

function SkillsPageView({ model }) {
  return (
    <section className="console-page">
      <PageHeader icon={Sparkles} title="插件与技能" subtitle="本地运行时" />
      <SkillsOverview model={model} />
      <div className="subhead">本地技能库</div>
      <SkillsToolbar model={model} />
      <SkillFilter filters={model.filters} scopeFilter={model.scopeFilter} setScopeFilter={model.setScopeFilter} />
      <SkillsStatus model={model} />
      <SkillImportSummaryPanel editor={model.editor} />
      <SkillResolutionPanel model={model} />
      <SkillGrid model={model} />
      <SkillModals model={model} />
    </section>
  );
}

function SkillsOverview({ model }) {
  const counts = model.filters.counts;
  const conflictValue = model.isProjectPending || model.dashboard.isResolutionPending || model.dashboard.resolutionSyncErrorText
    ? '待确认'
    : model.dashboard.resolutionConflicts.length;
  return (
    <section className="skills-overview" aria-label="插件与技能状态">
      <div className="skills-overview-copy">
        <span>当前连接</span>
        <h2>本地技能、个人技能和运行时冲突处理</h2>
      </div>
      <dl>
        <div><dt>本地技能</dt><dd>{counts.all}</dd></div>
        <div><dt>项目共享</dt><dd>{counts.project}</dd></div>
        <div><dt>私人使用</dt><dd>{counts.personal}</dd></div>
        <div><dt>待处理冲突</dt><dd>{conflictValue}</dd></div>
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

function SkillsToolbar({ model }) {
  return (
    <div className="skills-toolbar">
      <button type="button" onClick={model.editor.openImportScope} disabled={model.editor.importing}>批量导入技能目录</button>
      <button type="button" className="ghost" onClick={model.editor.openCreateEditor}>新建技能</button>
      <label><Search size={18} /><input value={model.query} onChange={(event) => model.setQuery(event.target.value)} placeholder="搜索技能名称、简介、关键词..." aria-label="搜索技能" /></label>
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

function SkillGrid({ model }) {
  const showReadyEmpty = !model.isProjectPending && !model.dashboard.isInitialLoading && !model.dashboard.showBlockingSyncError && model.filters.filteredItems.length === 0;
  return (
    <>
      {showReadyEmpty ? <SkillsEmptyState hasSkills={model.filters.counts.all > 0} /> : null}
      {model.filters.filteredItems.length > 0 ? <div className="skill-grid">{model.filters.filteredItems.map((skill) => <SkillCard key={skill.id} skill={skill} onEdit={model.editor.openEditSkill} onDelete={model.editor.onDeleteSkill} />)}</div> : null}
      {model.filters.countText ? <p className="skills-inline-tip">{model.filters.countText}</p> : null}
    </>
  );
}

function SkillsEmptyState({ hasSkills }) {
  if (hasSkills) {
    return (
      <div className="empty-state">
        <h3>没有匹配技能</h3>
        <p>尝试更换关键词或切换使用范围，支持按名称、简介、关键词搜索</p>
      </div>
    );
  }
  return <p className="console-message">暂无技能</p>;
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
