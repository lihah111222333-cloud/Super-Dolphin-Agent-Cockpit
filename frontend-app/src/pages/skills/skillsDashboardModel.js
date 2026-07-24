import { validateSkillInfo } from '../../features/slash-commands/adapters/skillInfoWireContract.js';
import { skillsPageService } from './services/skillsPageService.js';
import { SKILLS_REQUEST_TIMEOUT_MS, withTimeout } from '../shared/pageShared.js';

/*
 * skills 领域共享 model：文本基础工具与 skills dashboard 数据链的单一事实源。
 * SkillsPage 与 skillToolRegistrationModel 都从这里消费，禁止重复实现 dashboard normalize。
 */

function textFromValue(value) { if (value === null || value === undefined) return ''; return value.toString(); }
function trimmedText(value) { return textFromValue(value).trim(); }
function lowerTrimmedText(value) { return trimmedText(value).toLowerCase(); }
function requiredText(value, field, source) { const text = trimmedText(value); if (!text) throw new Error(`${source} is missing ${field}`); return text; }
function optionalArray(value) { return Array.isArray(value) ? value : []; }
function optionalObject(value) { return value && typeof value === 'object' && !Array.isArray(value) ? value : null; }
function firstTextField(raw, fields, source, required = false) { for (const field of fields) { const text = trimmedText(raw?.[field]); if (text) return text; } if (required) throw new Error(`${source} is missing ${fields[0]}`); return ''; }
function firstArrayField(raw, fields, source, required = false) { for (const field of fields) { const value = raw?.[field]; if (Array.isArray(value)) return value; } if (required) throw new Error(`${source} is missing ${fields[0]}`); return []; }

const SKILLS_DASHBOARD_TIMEOUT_MS = Math.max(1, SKILLS_REQUEST_TIMEOUT_MS - 250);

const { getDashboardPage } = skillsPageService;

function scopeForSkill(raw) {
  const scope = lowerTrimmedText(raw?.scope);
  if (scope === 'project' || scope === 'personal') return scope;
  const trust = lowerTrimmedText(raw?.trust);
  if (trust === 'user' || trust === 'signed' || trust === 'system' || trust === 'personal') {
    return 'personal';
  }
  return 'project';
}

function isInternalSkillReferenceWord(word) { const text = trimmedText(word); return text.startsWith('@') || /^\[skill:[^\]]+\]$/i.test(text); }

function normalizeWordList(...groups) {
  const seen = new Set();
  const words = [];
  groups.flat().forEach((word) => {
    const text = trimmedText(word);
    const key = text.toLowerCase();
    if (!text || seen.has(key) || isInternalSkillReferenceWord(text)) return;
    seen.add(key);
    words.push(text);
  });
  return words;
}

function normalizeSkill(raw, index) {
  const source = `skills dashboard response item ${index}`;
  const skill = validateSkillInfo(raw, source);
  const name = skill.name;
  const displayName = skill.display_name;
  const scope = scopeForSkill(skill);
  const dir = skill.dir;
  // skill_file 在 dashboard wire（contract.SkillInfo）中不存在，定位 SKILL.md 时由
  // skillFileForItem 用 dir + '/SKILL.md' 回退。
  const skillFile = '';
  const description = skill.description;
  const summary = skill.summary;
  const title = displayName ? displayName : name;
  const personalType = skill.personal_type;
  return {
    id: [scope, personalType, name, dir, index].join(':'),
    name,
    title: title ? title : '未命名技能',
    dir,
    skillFile,
    description,
    summary,
    scope,
    personalType,
    tags: normalizeWordList(skill.trigger_words, skill.force_words),
  };
}

function parseSkillsDashboardResponse(response) {
  if (!response || typeof response !== 'object' || Array.isArray(response)) {
    throw new TypeError('skills dashboard response must be an object');
  }
  if (!Array.isArray(response.skills)) {
    throw new TypeError('skills dashboard response skills must be an array');
  }
  return response;
}

function normalizeSkillsResponse(response) {
  const parsed = parseSkillsDashboardResponse(response);
  return parsed.skills.map((item, index) => normalizeSkill(item, index));
}

async function fetchSkillsDashboard(cwd) {
  const response = await withTimeout(
    getDashboardPage({ cwd, page: 'skills' }),
    SKILLS_DASHBOARD_TIMEOUT_MS,
    '技能列表加载超时，请检查技能目录或后端状态。',
  );
  return normalizeSkillsResponse(response);
}

export {
  fetchSkillsDashboard,
  firstArrayField,
  firstTextField,
  lowerTrimmedText,
  normalizeSkillsResponse,
  optionalArray,
  optionalObject,
  requiredText,
  scopeForSkill,
  SKILLS_DASHBOARD_TIMEOUT_MS,
  textFromValue,
  trimmedText,
};
