import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { AlertTriangle, Archive, ArrowLeft, Bot, Boxes, Brain, ChevronDown, CircleStop, Code2, Copy, Download, Eye, File, FileText, Folder, FolderOpen, GitBranch, Image, Link2, MemoryStick, MessageCircle, MoreHorizontal, Pencil, Plus, RefreshCw, Search, Send, Settings, Sparkles, Trash2, Workflow, X } from 'lucide-react';
import { useClientStore } from './entities/client/model/useClientStore.js';
import { PromptPageView } from './features/prompts/PromptPageView.jsx';
import {
  applyDagOps,
  applySkillResolution,
  consolidateMemorySimilarities,
  deleteDag,
  deleteMemoryEntry,
  deleteSharedFile,
  deleteSkill,
  getBuildInfo,
  getDashboardPage,
  getMemoryEntry,
  getMemorySnapshot,
  getDagDetail,
  getDagRun,
  getDagRuns,
  getPreference,
  importSkillDirectories,
  listSkillFiles,
  listSkillResolutions,
  ignoreMemorySimilarity,
  mergeMemoryEntries,
  previewSkillResolution,
  readSharedFile,
  readSkill,
  saveTextFile,
  selectProjectDirs,
  setMemoryAutoDreamIntent,
  setPreference,
  startDag,
  startThread,
  suggestSkillSummary,
  terminateDagRun,
  upsertMemoryEntry,
  writeSkill,
} from './shared/api/backendApi.js';

const navItems = [
  { id: 'chat', label: 'Chat', icon: MessageCircle },
  { id: 'prompts', label: '提示词', icon: FileText },
  { id: 'workflows', label: '自动化', icon: Workflow },
  { id: 'skills', label: '技能', icon: Sparkles },
  { id: 'memory', label: '记忆中心', icon: Brain },
  { id: 'files', label: '共享文件', icon: FolderOpen },
  { id: 'settings', label: '设置', icon: MoreHorizontal },
];

const DAG_RECENT_RUN_LIMIT = 5;
const DAG_DESIGNER_ENABLED_TOOLS = Object.freeze([
  'list_models',
  'prompt_list',
  'command_list',
  'shared_file_list',
  'task_create_dag',
  'task_get_dag',
  'task_dag_apply_ops',
  'task_start_dag',
]);
const DAG_CATEGORIES = Object.freeze([
  { key: 'running', label: '进行中' },
  { key: 'scheduled', label: '定时任务' },
  { key: 'history', label: '历史记录' },
]);
const STARTABLE_DAG_STATUSES = new Set(['draft', 'ready']);
const STARTABLE_DAG_TRIGGERS = new Set(['manual', 'scheduled', 'schedule', 'cron', '']);
const RUNNING_RUN_STATUSES = new Set(['running', 'pending', 'dispatching', 'waiting_for_assignee']);
const WORKFLOW_AUTO_REFRESH_MS = 4000;
const SHARED_FILE_CATEGORIES = Object.freeze([
  { key: 'all', label: '全部' },
  { key: 'final', label: '最终产物' },
  { key: 'work', label: '工作文件' },
]);
const SHARED_FILE_SORTS = Object.freeze([
  { key: 'updated-desc', label: '最新更新' },
  { key: 'updated-asc', label: '最早更新' },
  { key: 'path-asc', label: '按文件名' },
]);
const SHARED_FILES_AUTO_REFRESH_MS = 4000;
const MEMORY_AUTO_REFRESH_MS = 4000;
const SKILLS_AUTO_REFRESH_MS = 4000;
const SKILLS_REQUEST_TIMEOUT_MS = 8000;
const MEMORY_CATEGORIES = Object.freeze([
  { key: 'preference', label: '偏好' },
  { key: 'project', label: '项目' },
  { key: 'all', label: '全部' },
]);
const MEMORY_TYPE_INFO = Object.freeze({
  user: { category: 'preference', label: '偏好' },
  feedback: { category: 'preference', label: '偏好' },
  project: { category: 'project', label: '项目' },
  reference: { category: 'project', label: '项目' },
});
const MEMORY_EDITOR_TYPES = Object.freeze([
  { key: 'feedback', label: '偏好' },
  { key: 'project', label: '项目' },
]);


function projectDisplayName(path) {
  const value = (path || '').toString().trim();
  if (!value || value === '未选择项目') return '未选择项目';
  return value.split(/[\\/]/).filter(Boolean).pop() || value;
}

function withTimeout(promise, timeoutMs, message) {
  let timeoutID;
  const timeout = new Promise((_, reject) => {
    timeoutID = globalThis.setTimeout(() => reject(new Error(message)), timeoutMs);
  });
  return Promise.race([promise, timeout]).finally(() => {
    if (timeoutID) globalThis.clearTimeout(timeoutID);
  });
}

function normalizeProjectPath(path) {
  const value = (path || '').toString().trim();
  if (!value) return '';
  if (value !== '/' && !/^[a-zA-Z]:[\\/]?$/.test(value)) {
    return value.replace(/[\\/]+$/, '');
  }
  return value;
}

function disambiguateProjectLabels(items) {
  let changed = true;
  while (changed) {
    changed = false;
    const countByLabel = items.reduce((acc, item) => {
      acc[item.label] = (acc[item.label] || 0) + 1;
      return acc;
    }, {});
    for (const item of items) {
      if (countByLabel[item.label] <= 1 || item.label === item.full) continue;
      const nextDepth = Math.min(item.depth + 1, item.segments.length);
      const nextLabel = item.segments.slice(-nextDepth).join('/') || item.full;
      if (nextLabel === item.label) continue;
      item.depth = nextDepth;
      item.label = nextLabel;
      changed = true;
    }
  }
}

function projectOptionsFor(projects = [], activeProject = '', fallbackProject = '') {
  const values = [];
  const addValue = (value) => {
    const normalized = normalizeProjectPath(value);
    if (!normalized || values.includes(normalized)) return;
    values.push(normalized);
  };
  addValue(activeProject);
  addValue(fallbackProject);
  for (const project of projects || []) addValue(project);

  const items = values
    .filter((value) => value !== '.')
    .map((value) => {
      const segments = value.split(/[\\/]/).filter(Boolean);
      const depth = Math.min(2, segments.length);
      return {
        value,
        label: segments.slice(-depth).join('/') || value,
        full: value,
        segments,
        depth,
      };
    });
  disambiguateProjectLabels(items);
  return [
    { value: '.', label: '当前目录 (.)', full: '.' },
    ...items.map(({ value, label, full }) => ({ value, label, full })),
  ];
}

const MODEL_OPTIONS_BY_PROVIDER = Object.freeze({
  codex: Object.freeze([
    { value: 'gpt-5.5', label: 'GPT-5.5', short: '5.5' },
    { value: 'gpt-5.4', label: 'GPT-5.4', short: '5.4' },
    { value: 'gpt-5.3-codex', label: 'GPT-5.3 Codex', short: '5.3 Codex' },
    { value: 'gpt-5.2', label: 'GPT-5.2', short: '5.2' },
  ]),
  claude: Object.freeze([
    { value: 'opus', label: 'Opus 4.7' },
    { value: 'opus[1m]', label: 'Opus 4.7 [1M]' },
    { value: 'claude-opus-4-6', label: 'Opus 4.6' },
    { value: 'claude-opus-4-6[1m]', label: 'Opus 4.6 [1M]' },
    { value: 'sonnet', label: 'Sonnet 4.7' },
    { value: 'sonnet[1m]', label: 'Sonnet 4.7 [1M]' },
    { value: 'claude-sonnet-4-6', label: 'Sonnet 4.6' },
    { value: 'claude-sonnet-4-6[1m]', label: 'Sonnet 4.6 [1M]' },
    { value: 'haiku', label: 'Haiku 4.5' },
  ]),
});

const EFFORT_OPTIONS_BY_PROVIDER = Object.freeze({
  codex: Object.freeze([
    { value: 'xhigh', label: '极高' },
    { value: 'high', label: '高' },
    { value: 'medium', label: '中' },
    { value: 'low', label: '低' },
    { value: 'minimal', label: '极低' },
    { value: 'none', label: '关闭' },
  ]),
  claude: Object.freeze([
    { value: 'max', label: 'max' },
    { value: 'high', label: 'high' },
    { value: 'medium', label: 'medium' },
    { value: 'low', label: 'low' },
  ]),
});

const MODEL_DEFAULTS_BY_PROVIDER = Object.freeze({
  codex: Object.freeze({ model: 'gpt-5.5', effort: 'xhigh' }),
  claude: Object.freeze({ model: 'sonnet', effort: 'high' }),
});
const CLAUDE_LONG_TO_SHORT = Object.freeze({
  'claude-opus-4-7': 'opus',
  'claude-opus-4-7[1m]': 'opus[1m]',
  'claude-haiku-4-5': 'haiku',
});

function normalizeProviderKey(value) {
  return (value || '').toString().trim().toLowerCase() === 'claude' ? 'claude' : 'codex';
}

function normalizeConfigText(value) {
  return (value || '').toString().trim();
}

function canonicalizeModelValue(provider, value) {
  const normalized = normalizeConfigText(value);
  if (normalizeProviderKey(provider) === 'claude') return CLAUDE_LONG_TO_SHORT[normalized] || normalized;
  return normalized;
}

function isClaudeOpusFamilyModel(model) {
  const normalized = normalizeConfigText(model).toLowerCase();
  return normalized === 'best' || normalized.includes('opus');
}

function modelOptionFor(provider, value) {
  const normalized = canonicalizeModelValue(provider, value);
  const options = MODEL_OPTIONS_BY_PROVIDER[normalizeProviderKey(provider)] || MODEL_OPTIONS_BY_PROVIDER.codex;
  return options.find((item) => canonicalizeModelValue(provider, item.value) === normalized)
    || (normalized ? { value: normalized, label: normalized, short: normalized } : null);
}

function effortOptionFor(provider, value) {
  const normalized = normalizeConfigText(value);
  const options = EFFORT_OPTIONS_BY_PROVIDER[normalizeProviderKey(provider)] || EFFORT_OPTIONS_BY_PROVIDER.codex;
  return options.find((item) => item.value === normalized) || (normalized ? { value: normalized, label: normalized } : null);
}

function appendCurrentModelOption(provider, value) {
  const options = MODEL_OPTIONS_BY_PROVIDER[normalizeProviderKey(provider)] || MODEL_OPTIONS_BY_PROVIDER.codex;
  const current = modelOptionFor(provider, value);
  if (!current || options.some((item) => canonicalizeModelValue(provider, item.value) === current.value)) return options;
  return [...options, current];
}

function appendCurrentEffortOption(provider, value, model = '') {
  const providerKey = normalizeProviderKey(provider);
  const baseOptions = EFFORT_OPTIONS_BY_PROVIDER[providerKey] || EFFORT_OPTIONS_BY_PROVIDER.codex;
  const options = providerKey === 'claude' && !isClaudeOpusFamilyModel(model)
    ? baseOptions.filter((item) => item.value !== 'max')
    : baseOptions;
  const current = effortOptionFor(provider, value);
  if (!current || options.some((item) => item.value === current.value)) return options;
  return [...options, current];
}

function composerModelLabel(provider, model, effort) {
  const providerKey = normalizeProviderKey(provider);
  const modelValue = normalizeConfigText(model) || MODEL_DEFAULTS_BY_PROVIDER[providerKey].model;
  const effortValue = normalizeConfigText(effort) || MODEL_DEFAULTS_BY_PROVIDER[providerKey].effort;
  const modelLabel = modelOptionFor(providerKey, modelValue)?.label || modelValue;
  const effortLabel = effortOptionFor(providerKey, effortValue)?.label || effortValue;
  return `${modelLabel} · ${effortLabel}`.trim();
}

const STALE_ARCHIVE_MS = 7 * 24 * 60 * 60 * 1000;

function archivedStaleReason(thread) {
  if (!thread?.archived) return '';
  const archivedAt = Number(thread.archivedAt || 0);
  if (Number.isFinite(archivedAt) && archivedAt > STALE_ARCHIVE_MS && Date.now() - archivedAt > STALE_ARCHIVE_MS) {
    return 'expired';
  }
  if ((thread.name || '').toString().trim() === (thread.id || '').toString().trim()) {
    return 'empty';
  }
  return '';
}

const SETTINGS_KEYS = Object.freeze({
  stallThreshold: 'stallThresholdSec',
  contextThresholds: 'contextUsageAlerts.thresholds',
  activeProvider: 'settings.provider.active',
});

const SETTINGS_DEFAULTS = Object.freeze({
  stallThresholdSec: 30,
  contextThresholds: [70, 85, 95],
  activeProvider: 'codex',
  codexHome: '~/.codex',
  codexInstanceKey: 'default',
  codexModelProvider: 'openai',
  providerModel: 'gpt-5',
  providerEffort: 'high',
  sandboxPolicy: 'workspaceWrite',
  writableRoots: '',
  networkAccess: false,
});

function providerSettingKey(provider, key) {
  return `settings.provider.${provider}.${key}`;
}

function normalizeSettingsCwd(value) {
  const cwd = (value || '').toString().trim();
  if (!cwd || cwd === '.' || cwd === '未选择项目') {
    throw new Error('settings: cwd is required');
  }
  return cwd;
}

function isSettingsCwdReady(value) {
  const cwd = (value || '').toString().trim();
  return Boolean(cwd && cwd !== '.' && cwd !== '未选择项目');
}

function stringSetting(value, fallback) {
  if (typeof value === 'string' && value.trim()) return value.trim();
  return fallback;
}

function numberSetting(value, fallback) {
  const number = Number(value);
  return Number.isFinite(number) ? number : fallback;
}

function normalizeProviderName(value) {
  const provider = stringSetting(value, SETTINGS_DEFAULTS.activeProvider).toLowerCase();
  return provider === 'claude' ? 'claude' : 'codex';
}

function normalizeContextThresholds(value) {
  if (!Array.isArray(value) || value.length < 3) return SETTINGS_DEFAULTS.contextThresholds;
  return [
    numberSetting(value[0], SETTINGS_DEFAULTS.contextThresholds[0]),
    numberSetting(value[1], SETTINGS_DEFAULTS.contextThresholds[1]),
    numberSetting(value[2], SETTINGS_DEFAULTS.contextThresholds[2]),
  ];
}

function sandboxPolicyFromPreference(value) {
  if (typeof value === 'string') return value;
  if (value && typeof value === 'object') {
    return value.type || value.mode || SETTINGS_DEFAULTS.sandboxPolicy;
  }
  return SETTINGS_DEFAULTS.sandboxPolicy;
}

function writableRootsFromPreference(value) {
  if (!value || typeof value !== 'object' || !Array.isArray(value.writableRoots)) return '';
  return value.writableRoots.join('\n');
}

function sandboxPreferenceValue(policy, writableRootsText, networkAccess) {
  if (policy === 'readOnly') return { type: 'readOnly' };
  if (policy === 'dangerFullAccess') return { type: 'dangerFullAccess' };
  const writableRoots = writableRootsText
    .split(/\r?\n/)
    .map((item) => item.trim())
    .filter(Boolean);
  return {
    type: 'workspaceWrite',
    writableRoots,
    networkAccess: Boolean(networkAccess),
  };
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

function normalizeSkill(raw, index) {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) {
    throw new Error(`skills dashboard response item ${index} must be an object`);
  }
  const name = (raw.name || raw.key || '').toString().trim();
  const displayName = (raw.display_name || raw.displayName || raw.title || '').toString().trim();
  const triggerWords = Array.isArray(raw.trigger_words) ? raw.trigger_words : raw.triggerWords || [];
  const forceWords = Array.isArray(raw.force_words) ? raw.force_words : raw.forceWords || [];
  const scope = scopeForSkill(raw);
  const dir = (raw.dir || raw.path || '').toString().trim();
  const description = (raw.description || raw.summary || '').toString().trim();
  const summary = (raw.summary || raw.description || '').toString().trim();
  const title = displayName || name;
  return {
    id: [scope, raw.personal_type || raw.personalType || '', name, dir, index].join(':'),
    name,
    title: title || '未命名技能',
    dir,
    description,
    summary,
    scope,
    personalType: (raw.personal_type || raw.personalType || '').toString().trim(),
    tags: normalizeWordList(triggerWords, forceWords),
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

function cleanScalar(value) {
  return (value || '').toString().trim().replace(/^['"]|['"]$/g, '').trim();
}

function wordListFromText(value) {
  const text = Array.isArray(value) ? value.join(',') : (value || '').toString();
  return text
    .replace(/[，、；;\n]/g, ',')
    .split(',')
    .map(cleanScalar)
    .filter(Boolean)
    .filter((word, index, list) => list.findIndex((item) => item.toLowerCase() === word.toLowerCase()) === index);
}

function textValue(value) {
  return value === null || value === undefined ? '' : value.toString().trim();
}

function normalizeSummarySuggestion(value) {
  if (value && typeof value === 'object' && !Array.isArray(value)) {
    return textValue(value.description);
  }
  return textValue(value);
}

function firstText(...values) {
  for (const value of values) {
    const text = textValue(value);
    if (text) return text;
  }
  return '';
}

function numberOrNull(value) {
  if (value === null || value === undefined || value === '') return null;
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : null;
}

function objectValue(value) {
  return value && typeof value === 'object' && !Array.isArray(value) ? value : {};
}

function cleanObject(payload) {
  return Object.fromEntries(
    Object.entries(payload).filter(([, value]) => value !== undefined && value !== ''),
  );
}

function splitSharedFilePath(path) {
  const value = textValue(path);
  if (!value) return { dir: '', base: '未命名文件' };
  const index = value.lastIndexOf('/');
  if (index < 0) return { dir: '', base: value };
  return {
    dir: value.slice(0, index + 1),
    base: value.slice(index + 1) || value,
  };
}

function sharedFileExportName(path) {
  const base = splitSharedFilePath(path).base;
  return base && base !== '未命名文件' ? base : 'shared-file.txt';
}

function sharedFileTimestamp(value) {
  const text = textValue(value);
  if (!text) return '-';
  const date = new Date(text);
  if (!Number.isFinite(date.getTime())) return '-';
  return date.toLocaleString('zh-CN', { hour12: false });
}

function formatBytes(size) {
  const value = Number(size);
  if (!Number.isFinite(value) || value <= 0) return '0 B';
  if (value < 1024) return `${value} B`;
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KB`;
  return `${(value / 1024 / 1024).toFixed(1)} MB`;
}

function sharedFileContent(file) {
  return (file?.content || '').toString();
}

function sharedFileSummary(file) {
  const text = sharedFileContent(file).trim();
  if (!text) return '点击“打开”加载全文。';
  const lines = text.split('\n').map((line) => line.trim()).filter(Boolean).slice(0, 2).join(' ');
  return lines.length > 180 ? `${lines.slice(0, 180)}...` : lines;
}

function sharedFilePreview(file) {
  const text = sharedFileContent(file).trim();
  if (!text) return '文件为空';
  const preview = text.split('\n').slice(0, 8).join('\n');
  return preview.length > 600 ? `${preview.slice(0, 600)}...` : preview;
}

function normalizeSharedFile(raw, index) {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) {
    throw new Error(`shared file item ${index} must be an object`);
  }
  const path = textValue(raw.path);
  if (!path) throw new Error(`shared file item ${index} path is required`);
  return {
    id: `${path}:${index}`,
    path,
    content: (raw.content || '').toString(),
    updatedBy: firstText(raw.updated_by, raw.updatedBy),
    updatedAt: firstText(raw.updated_at, raw.updatedAt),
    createdAt: firstText(raw.created_at, raw.createdAt),
  };
}

function normalizeFinalOutputRefs(value) {
  if (!Array.isArray(value)) throw new Error('shared files dashboard finalOutputRefs must be an array');
  return value.map((item, index) => {
    if (typeof item === 'string') {
      const path = textValue(item);
      if (!path) throw new Error(`final output ref ${index} path is required`);
      return { path, runKey: '', dagKey: '', sourceNodeKey: '' };
    }
    if (!item || typeof item !== 'object' || Array.isArray(item)) {
      throw new Error(`final output ref ${index} must be an object`);
    }
    const path = firstText(item.path, item.sharedfile?.path, item.sharedFile?.path, item.shared_file?.path);
    if (!path) throw new Error(`final output ref ${index} path is required`);
    return {
      path,
      runKey: firstText(item.runKey, item.run_key),
      dagKey: firstText(item.dagKey, item.dag_key),
      sourceNodeKey: firstText(item.sourceNodeKey, item.source_node_key),
    };
  });
}

function normalizeSharedFileRetention(value) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    throw new Error('shared files dashboard sharedFileRetention must be an object');
  }
  if (!Array.isArray(value.items)) {
    throw new Error('shared files dashboard sharedFileRetention.items must be an array');
  }
  return {
    items: value.items.map((item, index) => {
      if (!item || typeof item !== 'object' || Array.isArray(item)) {
        throw new Error(`shared file retention item ${index} must be an object`);
      }
      const path = textValue(item.path);
      if (!path) throw new Error(`shared file retention item ${index} path is required`);
      return {
        path,
        protected: Boolean(item.protected),
        cleanupCandidate: Boolean(item.cleanupCandidate),
        reason: textValue(item.reason),
        finalOutput: item.finalOutput || item.final_output || null,
      };
    }),
    protectedCount: Number(value.protectedCount) || 0,
    cleanupCandidateCount: Number(value.cleanupCandidateCount) || 0,
  };
}

function normalizeSharedFilesResponse(response) {
  if (!response || typeof response !== 'object' || Array.isArray(response)) {
    throw new Error('shared files dashboard response must be an object');
  }
  if (!Array.isArray(response.memory)) {
    throw new Error('shared files dashboard response memory must be an array');
  }
  return {
    files: response.memory.map((item, index) => normalizeSharedFile(item, index)),
    finalOutputRefs: normalizeFinalOutputRefs(response.finalOutputRefs),
    retention: normalizeSharedFileRetention(response.sharedFileRetention),
  };
}

function sharedFileMatches(file, query) {
  const needle = textValue(query).toLowerCase();
  if (!needle) return true;
  return [
    file.path,
    file.updatedBy,
    file.content,
  ].some((value) => value.toLowerCase().includes(needle));
}

function sortSharedFiles(files, sortMode) {
  const list = [...files];
  if (sortMode === 'path-asc') {
    return list.sort((left, right) => left.path.localeCompare(right.path));
  }
  const updatedTime = (file) => {
    const parsed = new Date(file.updatedAt || 0).getTime();
    return Number.isFinite(parsed) ? parsed : 0;
  };
  return list.sort((left, right) => (
    sortMode === 'updated-asc'
      ? updatedTime(left) - updatedTime(right)
      : updatedTime(right) - updatedTime(left)
  ));
}

function sharedFileCategoryOf(file, finalOutputByPath) {
  return finalOutputByPath.has(file.path) ? 'final' : 'work';
}

function memoryTemplateForType(type) {
  switch (textValue(type)) {
    case 'feedback':
      return '规则\n原因：\n如何应用：';
    case 'project':
      return '事实\n原因：\n如何应用：';
    case 'reference':
      return '指向：\n为什么重要：';
    default:
      return '用户偏好：';
  }
}

function memoryTargetForType(type) {
  return textValue(type) === 'project' ? 'team' : 'private';
}

function memorySlugText(value) {
  return textValue(value)
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 48);
}

function memoryNameHash(value) {
  let hash = 5381;
  const text = textValue(value);
  for (let index = 0; index < text.length; index += 1) {
    hash = ((hash << 5) + hash + text.charCodeAt(index)) >>> 0;
  }
  return hash.toString(36);
}

function memoryAutoName(form) {
  const existing = textValue(form?.name);
  if (existing) return existing;
  const type = textValue(form?.type) || 'project';
  const base = firstText(form?.title, form?.description, form?.content, type);
  const slug = memorySlugText(base);
  if (slug) return slug;
  return `${type}-${memoryNameHash(base)}`;
}

function defaultMemoryForm(type = 'project', target = memoryTargetForType(type)) {
  return {
    target,
    existingPath: '',
    name: '',
    description: '',
    title: '',
    type,
    content: memoryTemplateForType(type),
  };
}

function normalizeMemorySnapshot(response) {
  if (!response || typeof response !== 'object' || Array.isArray(response)) {
    throw new Error('memory snapshot response must be an object');
  }
  return {
    overview: objectValue(response.overview),
    entries: [
      ...normalizeMemorySection(response.private, 'private'),
      ...normalizeMemorySection(response.team, 'team'),
    ],
  };
}

function normalizeMemorySection(section, target) {
  const value = objectValue(section);
  if (!Array.isArray(value.entries)) {
    throw new Error(`memory ${target} entries must be an array`);
  }
  return value.entries.map((item, index) => normalizeMemoryEntry(item, index, target));
}

function normalizeMemoryEntry(raw, index, target) {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) {
    throw new Error(`memory ${target} entry ${index} must be an object`);
  }
  const path = textValue(raw.path);
  if (!path) throw new Error(`memory ${target} entry ${index} path is required`);
  const type = textValue(raw.type).toLowerCase();
  const typeInfo = MEMORY_TYPE_INFO[type];
  if (!typeInfo) throw new Error(`memory ${target} entry ${index} type is unsupported: ${type || '(empty)'}`);
  const name = firstText(raw.name, raw.title, path);
  if (!name) throw new Error(`memory ${target} entry ${index} name is required`);
  return {
    id: `${target}:${path}:${index}`,
    target,
    path,
    type,
    category: typeInfo.category,
    tag: typeInfo.label,
    name,
    title: firstText(raw.title, raw.name),
    description: firstText(raw.description, raw.summary),
    preview: firstText(raw.preview, raw.content, raw.text),
    updatedAt: firstText(raw.updatedAt, raw.updated_at, raw.createdAt, raw.created_at),
    source: textValue(raw.source),
    raw,
  };
}

function normalizeAutoDreamIntent(value) {
  if (value === true) return true;
  if (value === false) return false;
  return null;
}

function normalizeSimilarityGroups(value) {
  if (value === undefined || value === null) return [];
  if (!Array.isArray(value)) throw new Error('memory health similarGroups must be an array');
  return value.map((item, index) => {
    if (!item || typeof item !== 'object' || Array.isArray(item)) {
      throw new Error(`memory similar group ${index} must be an object`);
    }
    const group = {
      targetA: textValue(item.targetA || item.target_a),
      pathA: textValue(item.pathA || item.path_a),
      nameA: firstText(item.nameA, item.name_a),
      targetB: textValue(item.targetB || item.target_b),
      pathB: textValue(item.pathB || item.path_b),
      nameB: firstText(item.nameB, item.name_b),
      score: numberOrNull(item.score) ?? 0,
    };
    for (const key of ['targetA', 'pathA', 'targetB', 'pathB']) {
      if (!group[key]) throw new Error(`memory similar group ${index} ${key} is required`);
    }
    return group;
  });
}

function memoryHealth(overview, counts) {
  const health = overview?.health;
  if (!health || typeof health !== 'object' || Array.isArray(health)) return null;
  return {
    preferenceCount: numberOrNull(health.preferenceCount) ?? counts.preference,
    projectCount: numberOrNull(health.projectCount) ?? counts.project,
    maxPerCategory: numberOrNull(health.maxPerCategory) ?? 15,
    similarGroups: normalizeSimilarityGroups(health.similarGroups),
  };
}

function memorySimilarGroupCount(response) {
  const snapshot = normalizeMemorySnapshot(response);
  const counts = {
    preference: snapshot.entries.filter((entry) => entry.category === 'preference').length,
    project: snapshot.entries.filter((entry) => entry.category === 'project').length,
    all: snapshot.entries.length,
  };
  const health = memoryHealth(snapshot.overview, counts);
  return health?.similarGroups?.length || 0;
}

function memoryHealthPercent(count, max) {
  const safeMax = Number(max) || 1;
  const safeCount = Number(count) || 0;
  return Math.min(100, Math.max(0, Math.round((safeCount / safeMax) * 100)));
}

function memoryHealthClass(percent) {
  if (percent >= 100) return 'danger';
  if (percent >= 80) return 'warning';
  return '';
}

function memoryMatches(entry, query) {
  const needle = textValue(query).toLowerCase();
  if (!needle) return true;
  return [entry.title, entry.name, entry.description, entry.path, entry.type, entry.preview]
    .some((value) => textValue(value).toLowerCase().includes(needle));
}

function sortMemoryEntries(entries) {
  return [...entries].sort((left, right) => {
    const leftTime = new Date(left.updatedAt || 0).getTime();
    const rightTime = new Date(right.updatedAt || 0).getTime();
    const safeLeft = Number.isFinite(leftTime) ? leftTime : 0;
    const safeRight = Number.isFinite(rightTime) ? rightTime : 0;
    return safeRight - safeLeft || left.title.localeCompare(right.title);
  });
}

function memoryPairKey(group) {
  return `${group.targetA}:${group.pathA}|${group.targetB}:${group.pathB}`;
}

function formatMemoryScore(score) {
  return `${Math.round((Number(score) || 0) * 100)}%`;
}

function memoryEntryTitle(entry) {
  return firstText(entry.title, entry.description, entry.name, entry.path);
}

function memoryNoticeText(value) {
  const text = textValue(value);
  return text.length > 120 ? `${text.slice(0, 119)}…` : text;
}

function errorMessage(error) {
  return memoryNoticeText(error?.message || String(error || ''));
}

function dagKeyOf(raw) {
  return firstText(raw?.dag_key, raw?.dagKey, raw?.key, raw?.id);
}

function runKeyOf(raw) {
  return firstText(raw?.run_key, raw?.runKey, raw?.key, raw?.id);
}

function nodeKeyOf(raw) {
  return firstText(raw?.node_key, raw?.nodeKey, raw?.key, raw?.id);
}

function dagVersionOf(item) {
  return numberOrNull(item?.version ?? item?.dag_version ?? item?.dagVersion ?? item?.raw?.version);
}

function normalizeDagRun(raw = {}, index = 0) {
  const runKey = runKeyOf(raw);
  return {
    id: runKey || `run:${index}`,
    runKey,
    status: firstText(raw.status, raw.state),
    triggerSource: firstText(raw.trigger_source, raw.triggerSource),
    startedAt: firstText(raw.started_at, raw.startedAt, raw.created_at, raw.createdAt),
    finishedAt: firstText(raw.finished_at, raw.finishedAt),
    metadata: objectValue(raw.metadata),
    raw,
  };
}

function normalizeDagNode(raw = {}, index = 0) {
  const nodeKey = nodeKeyOf(raw);
  const config = objectValue(raw.config);
  const dependsOn = Array.isArray(raw.depends_on)
    ? raw.depends_on
    : (Array.isArray(raw.dependsOn) ? raw.dependsOn : wordListFromText(raw.depends_on || raw.dependsOn || ''));
  return {
    id: nodeKey || `node:${index}`,
    nodeKey,
    title: firstText(raw.title, raw.name, nodeKey, `节点 ${index + 1}`),
    nodeType: firstText(raw.node_type, raw.nodeType, raw.type),
    assignedTo: firstText(raw.assigned_to, raw.assignedTo),
    dependsOn,
    status: firstText(raw.status, raw.state),
    threadId: firstText(raw.spawning_thread_id, raw.spawningThreadId, raw.threadId, raw.thread_id),
    config,
    raw,
  };
}

function normalizeDashboardDag(raw = {}, index = 0) {
  const dagKey = dagKeyOf(raw);
  const latestRun = raw.latest_run || raw.latestRun || null;
  const cronExpr = cronExprFromDagItem(raw);
  return {
    id: dagKey || `dag:${index}`,
    dagKey,
    title: firstText(raw.title, raw.name, dagKey, `任务流程 ${index + 1}`),
    description: firstText(raw.description, raw.summary),
    status: firstText(raw.status, raw.state),
    trigger: dagTriggerValue(raw),
    cronExpr,
    nextRunAt: firstText(raw.next_run_at, raw.nextRunAt),
    startedAt: firstText(raw.started_at, raw.startedAt, raw.created_at, raw.createdAt),
    finishedAt: firstText(raw.finished_at, raw.finishedAt),
    version: dagVersionOf(raw),
    latestRun: latestRun ? normalizeDagRun(latestRun) : null,
    scheduleEnabled: scheduleEnabledFromDagItem(raw),
    raw,
  };
}

function normalizeDagsResponse(response) {
  if (!response || typeof response !== 'object' || Array.isArray(response)) {
    throw new Error('dags dashboard response must be an object');
  }
  if (!Array.isArray(response.dags)) {
    throw new Error('dags dashboard response dags must be an array');
  }
  return response.dags.map((item, index) => normalizeDashboardDag(item, index));
}

function dagStatusLabel(value) {
  const status = textValue(value).toLowerCase();
  const labels = {
    draft: '草稿',
    ready: '可运行',
    running: '运行中',
    succeeded: '成功',
    done: '成功',
    success: '成功',
    failed: '失败',
    cancelled: '已取消',
    canceled: '已取消',
    pending: '待开始',
    queued: '排队中',
    starting: '启动中',
    awaiting_verify: '待确认',
    skipped: '已跳过',
    idle: '空闲',
  };
  return labels[status] || textValue(value) || '-';
}

function dagTriggerValue(raw = {}) {
  const trigger = raw.trigger || raw.trigger_config || raw.triggerConfig;
  if (trigger && typeof trigger === 'object' && !Array.isArray(trigger)) {
    return firstText(trigger.type, trigger.kind, raw.trigger_type, raw.triggerType);
  }
  return firstText(trigger, raw.trigger_type, raw.triggerType);
}

function cronExprFromDagItem(item = {}) {
  const trigger = item.trigger || item.trigger_config || item.triggerConfig;
  if (trigger && typeof trigger === 'object' && !Array.isArray(trigger)) {
    return firstText(trigger.schedule, trigger.cron, trigger.expression, item.schedule, item.cron, item.cron_expr, item.cronExpr);
  }
  return firstText(item.schedule, item.cron, item.cron_expr, item.cronExpr);
}

function scheduleEnabledFromDagItem(item = {}) {
  if (typeof item.schedule_enabled === 'boolean') return item.schedule_enabled;
  if (typeof item.scheduleEnabled === 'boolean') return item.scheduleEnabled;
  const trigger = item.trigger || item.trigger_config || item.triggerConfig;
  if (trigger && typeof trigger === 'object' && !Array.isArray(trigger)) {
    return Boolean(firstText(trigger.next_run_at, trigger.nextRunAt, item.next_run_at, item.nextRunAt));
  }
  return Boolean(firstText(item.next_run_at, item.nextRunAt));
}

function isScheduledTrigger(value) {
  return ['scheduled', 'schedule', 'cron'].includes(textValue(value).toLowerCase());
}

function triggerLabel(value) {
  const trigger = textValue(value).toLowerCase();
  const labels = {
    manual: '手动',
    scheduled: '定时',
    schedule: '定时',
    cron: '定时',
  };
  return labels[trigger] || textValue(value) || '-';
}

const DEFAULT_DAG_SCHEDULE = Object.freeze({ preset: 'daily', time: '08:00', weekday: '1', monthDay: '1' });
const DAG_WEEKDAY_OPTIONS = Object.freeze([
  { value: '1', label: '周一' },
  { value: '2', label: '周二' },
  { value: '3', label: '周三' },
  { value: '4', label: '周四' },
  { value: '5', label: '周五' },
  { value: '6', label: '周六' },
  { value: '7', label: '周日' },
]);
const DAG_WEEKDAY_LABELS = Object.freeze(Object.fromEntries(DAG_WEEKDAY_OPTIONS.map((item) => [item.value, item.label])));

function twoDigits(value) {
  return value.toString().padStart(2, '0');
}

function parseScheduleTime(value) {
  const text = textValue(value);
  const match = /^(\d{1,2}):(\d{2})$/.exec(text);
  if (!match) return null;
  const hour = Number(match[1]);
  const minute = Number(match[2]);
  if (!Number.isInteger(hour) || !Number.isInteger(minute) || hour < 0 || hour > 23 || minute < 0 || minute > 59) {
    return null;
  }
  return { hour, minute, label: `${twoDigits(hour)}:${twoDigits(minute)}` };
}

function scheduleStateFromCron(cronExpr) {
  const text = textValue(cronExpr);
  if (!text) return { ...DEFAULT_DAG_SCHEDULE, warning: '' };
  const parts = text.split(/\s+/);
  if (parts.length !== 5) return { ...DEFAULT_DAG_SCHEDULE, warning: '已有计划格式无法识别，请重新选择运行频率和时间。' };
  const [minuteText, hourText, dayOfMonth, month, dayOfWeek] = parts;
  const hour = Number(hourText);
  const minute = Number(minuteText);
  if (!Number.isInteger(hour) || !Number.isInteger(minute) || hour < 0 || hour > 23 || minute < 0 || minute > 59) {
    return { ...DEFAULT_DAG_SCHEDULE, warning: '已有计划格式无法识别，请重新选择运行频率和时间。' };
  }
  const time = `${twoDigits(hour)}:${twoDigits(minute)}`;
  if (dayOfMonth === '*' && month === '*' && dayOfWeek === '1-5') return { ...DEFAULT_DAG_SCHEDULE, preset: 'weekdays', time };
  if (dayOfMonth === '*' && month === '*' && Object.prototype.hasOwnProperty.call(DAG_WEEKDAY_LABELS, dayOfWeek)) {
    return { ...DEFAULT_DAG_SCHEDULE, preset: 'weekly', weekday: dayOfWeek, time };
  }
  const monthDay = Number(dayOfMonth);
  if (Number.isInteger(monthDay) && monthDay >= 1 && monthDay <= 31 && month === '*' && dayOfWeek === '*') {
    return { ...DEFAULT_DAG_SCHEDULE, preset: 'monthly', monthDay: monthDay.toString(), time };
  }
  if (dayOfMonth === '*' && month === '*' && dayOfWeek === '*') return { ...DEFAULT_DAG_SCHEDULE, preset: 'daily', time };
  return { ...DEFAULT_DAG_SCHEDULE, warning: '已有计划超出简化设置范围，请重新选择运行频率和时间。' };
}

function scheduleLabelFromState(schedule) {
  const parsed = parseScheduleTime(schedule?.time);
  if (!parsed) return '';
  if (schedule?.preset === 'daily') return `每天 ${parsed.label}`;
  if (schedule?.preset === 'weekdays') return `工作日 ${parsed.label}`;
  if (schedule?.preset === 'weekly') return `${DAG_WEEKDAY_LABELS[schedule.weekday] ? `每${DAG_WEEKDAY_LABELS[schedule.weekday]}` : '每周'} ${parsed.label}`;
  if (schedule?.preset === 'monthly') return `每月 ${schedule.monthDay || DEFAULT_DAG_SCHEDULE.monthDay} 日 ${parsed.label}`;
  return '';
}

function scheduleLabelFromCron(cronExpr) {
  if (!textValue(cronExpr)) return '';
  const state = scheduleStateFromCron(cronExpr);
  if (state.warning) return '';
  return scheduleLabelFromState(state);
}

function scheduleLabelFromDag(item) {
  return scheduleLabelFromCron(cronExprFromDagItem(item));
}

function cronExprFromSchedule(preset, time, weekday, monthDay) {
  const parsed = parseScheduleTime(time);
  if (!parsed) return { cronExpr: '', error: '请选择运行时间' };
  const minute = parsed.minute.toString();
  const hour = parsed.hour.toString();
  if (preset === 'daily') return { cronExpr: `${minute} ${hour} * * *`, error: '' };
  if (preset === 'weekdays') return { cronExpr: `${minute} ${hour} * * 1-5`, error: '' };
  if (preset === 'weekly') {
    if (!Object.prototype.hasOwnProperty.call(DAG_WEEKDAY_LABELS, weekday)) return { cronExpr: '', error: '请选择星期几' };
    return { cronExpr: `${minute} ${hour} * * ${weekday}`, error: '' };
  }
  if (preset === 'monthly') {
    const day = Number(monthDay);
    if (!Number.isInteger(day) || day < 1 || day > 31) return { cronExpr: '', error: '请选择每月几号' };
    return { cronExpr: `${minute} ${hour} ${day} * *`, error: '' };
  }
  return { cronExpr: '', error: '请选择运行频率' };
}

function schedulePlanLabel(item) {
  const readable = scheduleLabelFromDag(item);
  if (readable) return readable;
  return triggerLabel(item?.trigger);
}

function latestDagRunLabel(item) {
  const status = firstText(item?.latestRun?.status, item?.latest_run_status, item?.latestRunStatus);
  if (status) return dagStatusLabel(status);
  if (item?.latestRun?.runKey) return '有运行记录';
  if (isScheduledTrigger(item?.trigger)) return '未运行';
  return textValue(item?.status).toLowerCase() === 'draft' || textValue(item?.status).toLowerCase() === 'ready' ? '未启动' : '-';
}

function displayDagStatusLabel(item) {
  const trigger = textValue(item?.trigger).toLowerCase();
  const status = textValue(item?.status).toLowerCase();
  if (isScheduledTrigger(trigger) && cronExprFromDagItem(item)) {
    if (status === 'running') return '运行中';
    return scheduleEnabledFromDagItem(item) ? '已启用' : '已暂停';
  }
  return dagStatusLabel(item?.status);
}

function isRunningStatus(value) {
  return RUNNING_RUN_STATUSES.has(textValue(value).toLowerCase());
}

function dagHasActiveRun(item) {
  return isRunningStatus(item?.latestRun?.status) || isRunningStatus(item?.status);
}

function isScheduledDag(item) {
  const trigger = textValue(item?.trigger).toLowerCase();
  return ['scheduled', 'schedule', 'cron'].includes(trigger) || Boolean(item?.cronExpr || item?.nextRunAt);
}

function dagCategoryOf(item) {
  if (dagHasActiveRun(item)) return 'running';
  if (isScheduledDag(item)) return 'scheduled';
  return 'history';
}

function categoryCounts(items) {
  return DAG_CATEGORIES.reduce((acc, category) => {
    acc[category.key] = items.filter((item) => dagCategoryOf(item) === category.key).length;
    return acc;
  }, {});
}

function firstAvailableCategory(items) {
  const counts = categoryCounts(items);
  const found = DAG_CATEGORIES.find((category) => counts[category.key] > 0);
  return found?.key || DAG_CATEGORIES[0].key;
}

function finalOutputText(raw) {
  const source = raw?.run || raw || {};
  const metadata = objectValue(source.metadata);
  const value = source.final_output || source.finalOutput || metadata.final_output || metadata.finalOutput;
  if (typeof value === 'string') return value.trim();
  if (value && typeof value === 'object') {
    return firstText(value.text, value.content, value.message, value.output, value.summary) || JSON.stringify(value);
  }
  return '';
}

function validThreadIdText(value) {
  const text = textValue(value);
  if (!text || /^launch[_-]/i.test(text)) return '';
  return text;
}

function firstValidThreadId(...values) {
  for (const value of values) {
    const text = validThreadIdText(value);
    if (text) return text;
  }
  return '';
}

function threadIdFromStartResponse(value) {
  return firstValidThreadId(
    value?.threadId,
    value?.thread_id,
    value?.thread?.threadId,
    value?.thread?.thread_id,
    value?.id,
    value?.thread?.id,
    value?.agentId,
    value?.agent_id,
    value?.thread?.agentId,
    value?.thread?.agent_id,
  );
}

function dagNodeFormFromNode(node) {
  const config = objectValue(node?.config);
  return {
    nodeKey: textValue(node?.nodeKey),
    title: textValue(node?.title),
    provider: firstText(config.provider, config.agentKey, config.agent_key),
    model: textValue(config.model),
    promptKey: firstText(config.prompt_key, config.promptKey),
    dependsOn: listToText(node?.dependsOn || []),
    firstTurn: firstText(config.first_turn, config.firstTurn, config.prompt),
    outputFile: firstText(config.output_file, config.outputFile),
  };
}

function dagNodePatchFromForm(form, node) {
  const config = {
    ...objectValue(node?.config),
    provider: textValue(form.provider),
    model: textValue(form.model),
    prompt_key: textValue(form.promptKey),
    first_turn: textValue(form.firstTurn),
    output_file: textValue(form.outputFile),
  };
  return {
    title: textValue(form.title),
    depends_on: wordListFromText(form.dependsOn),
    config: cleanObject(config),
  };
}

function listToText(words) {
  return Array.isArray(words) ? words.join(', ') : '';
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
  return response.files
    .map((file) => ({
      name: (file?.name || '').toString().trim(),
      path: (file?.path || '').toString().trim(),
      isMain: Boolean(file?.is_main || file?.isMain),
    }))
    .filter((file) => file.name && file.path);
}

function isMainSkillFile(path) {
  return /(^|[\\/])SKILL\.md$/i.test((path || '').toString().trim());
}

function normalizeResolutionResponse(response) {
  if (Array.isArray(response)) return response;
  if (Array.isArray(response?.items)) return response.items;
  if (Array.isArray(response?.conflicts)) return response.conflicts;
  return [];
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
  return action === 'save_as_new_skill'
    || action === 'save_as_new_personal_skill'
    || action === 'rename_personal';
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
  return (conflict?.kind || '').toString().trim().toLowerCase() === 'canonical_deleted_with_drift'
    && (conflict?.scope || '').toString().trim().toLowerCase() === 'personal';
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
    return actions
      .filter((action) => allowed.has(action))
      .map((action) => ({
        action,
        label: ({
          use_project_shared_skill: '使用项目共享版本，删除旧私人版本',
          use_external_provider_skill: '继续私人使用，替换项目共享版本',
        }[action] || resolutionActionLabel(action)),
        help: resolutionActionHelp(action),
      }));
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
    return action === 'use_project_shared_skill'
      || action === 'use_external_provider_skill'
      || action === 'save_as_new_personal_skill';
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

function parsePositiveInteger(label, value) {
  const parsed = Number.parseInt(value, 10);
  if (!Number.isInteger(parsed)) throw new Error(`${label} 必须是整数`);
  return parsed;
}

function validateRuntimeThresholds(form) {
  const stallThresholdSec = parsePositiveInteger('统一超时阈值', form.stallThresholdSec);
  if (stallThresholdSec < 30) throw new Error('统一超时阈值必须大于或等于 30 秒');

  const warn = parsePositiveInteger('Warn 阈值', form.contextWarn);
  const danger = parsePositiveInteger('Danger 阈值', form.contextDanger);
  const critical = parsePositiveInteger('Critical 阈值', form.contextCritical);
  if (!(warn > 0 && warn < danger && danger < critical && critical <= 100)) {
    throw new Error('上下文阈值必须满足 0 < warn < danger < critical <= 100');
  }
  return { stallThresholdSec, contextThresholds: [warn, danger, critical] };
}

function App({ skipBootstrap = false }) {
  const store = useClientStore();
  const bootstrap = store.bootstrap;
  const addWarning = store.addWarning;
  const [memorySimilarCount, setMemorySimilarCount] = useState(0);
  const memoryRevision = Number(store.memoryRevision || 0);

  useEffect(() => {
    if (skipBootstrap) return undefined;
    let cancelled = false;
    bootstrap().catch((error) => {
      if (!cancelled) {
        console.error('[frontend-app] bootstrap failed', error);
      }
    });
    return () => {
      cancelled = true;
    };
  }, [bootstrap, skipBootstrap]);

  const projectPath = store.activeProject && store.activeProject !== '.' ? store.activeProject : store.cwd || '未选择项目';

  useEffect(() => {
    const cwd = textValue(projectPath);
    if (!cwd || cwd === '.' || cwd === '未选择项目') {
      setMemorySimilarCount(0);
      return undefined;
    }
    let cancelled = false;
    getMemorySnapshot({ cwd }).then((response) => {
      if (!cancelled) {
        setMemorySimilarCount(memorySimilarGroupCount(response));
      }
    }).catch((error) => {
      if (!cancelled) {
        addWarning('warn', 'memory.badge.refresh.failed', { error: errorMessage(error) });
      }
    });
    return () => {
      cancelled = true;
    };
  }, [addWarning, memoryRevision, projectPath]);

  const updateMemorySimilarCount = useCallback((count) => {
    setMemorySimilarCount(Math.max(0, Number(count) || 0));
  }, []);

  const activeLabel = useMemo(() => (
    navItems.find((item) => item.id === store.activePage)?.label || 'Chat'
  ), [store.activePage]);
  const projectPathReady = isSettingsCwdReady(projectPath);

  return (
    <div className="sa-window" data-testid="frontend-app">
      <div className="sa-body">
        <NavRail activePage={store.activePage} setActivePage={store.setActivePage} memorySimilarCount={memorySimilarCount} />
        <main className="sa-main">
          {store.activePage === 'chat' ? <ChatPage store={store} projectPath={projectPath} /> : null}
          {store.activePage === 'prompts' ? (
            projectPathReady ? <PromptPage projectPath={projectPath} refreshKey={store.promptRevision} /> : <ProjectLoadingPage />
          ) : null}
          {store.activePage === 'workflows' ? (
            projectPathReady ? <WorkflowPage projectPath={projectPath} store={store} refreshKey={store.workflowRevision} /> : <ProjectLoadingPage />
          ) : null}
          {store.activePage === 'skills' ? (
            projectPathReady ? <SkillsPage projectPath={projectPath} refreshKey={store.skillRevision} /> : <ProjectLoadingPage />
          ) : null}
          {store.activePage === 'memory' ? (
            projectPathReady ? <MemoryPage projectPath={projectPath} onSimilarCountChange={updateMemorySimilarCount} refreshKey={memoryRevision} /> : <ProjectLoadingPage />
          ) : null}
          {store.activePage === 'files' ? (
            projectPathReady ? <FilesPage projectPath={projectPath} store={store} /> : <ProjectLoadingPage />
          ) : null}
          {store.activePage === 'settings' ? (
            projectPathReady ? <SettingsPage projectPath={projectPath} /> : <ProjectLoadingPage />
          ) : null}
          <span className="sr-only">当前页面：{activeLabel}</span>
        </main>
      </div>
    </div>
  );
}

function ProjectLoadingPage() {
  return (
    <section className="console-page">
      <div className="empty-state">
        <h2>正在加载项目...</h2>
      </div>
    </section>
  );
}

function NavRail({ activePage, setActivePage, memorySimilarCount = 0 }) {
  const memoryBadgeCount = Math.max(0, Number(memorySimilarCount) || 0);
  return (
    <aside className="nav-rail" data-testid="sidebar-nav">
      <nav>
        {navItems.map((item) => {
          const Icon = item.icon;
          const badgeCount = item.id === 'memory' ? memoryBadgeCount : 0;
          return (
            <button
              key={item.id}
              type="button"
              className={activePage === item.id ? 'active' : ''}
              onClick={() => setActivePage(item.id)}
              aria-label={item.label}
            >
              <Icon size={22} aria-hidden="true" />
              <span>{item.label}</span>
              {badgeCount > 0 ? <i aria-hidden="true" title={`${badgeCount} 条待整合相似记忆`} /> : null}
            </button>
          );
        })}
      </nav>
    </aside>
  );
}

function ChatPage({ store, projectPath }) {
  const activeThreadId = store.activeThreadId;
  const messages = store.timelinesByThread[activeThreadId] || [];
  const tokenUsage = store.tokenUsageByThread[activeThreadId] || null;
  const diffText = store.diffTextByThread[activeThreadId] || '';

  const beginResize = (event) => {
    event.preventDefault();
    const startX = event.clientX;
    const startWidth = store.rightPanelWidth;
    const move = (moveEvent) => {
      const next = Math.max(360, Math.min(780, startWidth - (moveEvent.clientX - startX)));
      store.setRightPanelWidth(next);
    };
    const stop = () => {
      window.removeEventListener('pointermove', move);
      window.removeEventListener('pointerup', stop);
    };
    window.addEventListener('pointermove', move);
    window.addEventListener('pointerup', stop);
  };

  return (
    <section className="chat-page" data-testid="chat-page">
      <TopCommandBar store={store} projectPath={projectPath} />
      <div
        className="chat-layout"
        style={{ gridTemplateColumns: `320px minmax(0, 1fr) 8px ${store.rightPanelWidth}px` }}
      >
        <ThreadRail store={store} />
        <Conversation
          messages={messages}
          draft={store.draft}
          setDraft={store.setDraft}
          sendMessage={store.sendDraft}
          attachments={store.attachments}
          selectFiles={store.selectFilesForComposer}
          removeAttachment={store.removeAttachment}
          sending={store.sending}
          store={store}
          projectPath={projectPath}
          permission={store.permission}
          setPermission={store.setPermission}
          tokenUsage={tokenUsage}
          activeThreadId={activeThreadId}
        />
        <button
          type="button"
          className="splitter"
          aria-label="调整工作台宽度"
          onPointerDown={beginResize}
        />
        <RuntimePanel
          diffText={diffText}
          tokenUsage={tokenUsage}
          warnings={store.warningEntries}
          activity={store.activityEntries}
        />
      </div>
    </section>
  );
}

function ProjectSelector({ store, projectPath }) {
  const [open, setOpen] = useState(false);
  const wrapRef = useRef(null);
  const activeProject = store.activeProject || projectPath;
  const options = useMemo(
    () => projectOptionsFor(store.projects, activeProject, projectPath),
    [store.projects, activeProject, projectPath],
  );
  const selectedValue = normalizeProjectPath(activeProject) || '.';
  const selected = options.find((item) => item.value === selectedValue)
    || options.find((item) => item.value === '.')
    || { value: '.', label: '当前目录 (.)', full: '.' };

  useEffect(() => {
    if (!open) return undefined;
    const onPointerDown = (event) => {
      if (wrapRef.current && !wrapRef.current.contains(event.target)) {
        setOpen(false);
      }
    };
    document.addEventListener('pointerdown', onPointerDown, true);
    return () => document.removeEventListener('pointerdown', onPointerDown, true);
  }, [open]);

  const selectProject = async (value) => {
    setOpen(false);
    await store.setActiveProjectPath?.(value);
  };

  const addProject = async () => {
    setOpen(false);
    await store.addProjectFromPicker?.();
  };

  const removeProject = async (event, value) => {
    event.stopPropagation();
    await store.removeProjectPath?.(value);
  };

  return (
    <div className="project-select-wrap" ref={wrapRef}>
      <button
        type="button"
        className="project-select"
        aria-label="选择项目"
        aria-haspopup="menu"
        aria-expanded={open}
        title={selected.full === '.' ? projectPath : selected.full}
        onClick={() => setOpen((value) => !value)}
      >
        <Folder size={15} />
        <span>{selected.label}</span>
        <ChevronDown size={14} />
      </button>
      {open ? (
        <div className="project-dropdown" role="menu" aria-label="项目列表">
          {options.map((item) => (
            <div key={item.value} className={`project-dropdown-row ${item.value === selected.value ? 'selected' : ''}`} role="none" title={item.full}>
              <button
                type="button"
                className="project-dropdown-item"
                role="menuitem"
                onClick={() => void selectProject(item.value)}
              >
                <span className="project-option-check" aria-hidden="true">{item.value === selected.value ? '✓' : ''}</span>
                <span className="project-dropdown-label">{item.label}</span>
              </button>
              {item.value !== '.' ? (
                <button
                  type="button"
                  className="project-dropdown-remove"
                  aria-label={`移除此项目 ${item.label}`}
                  title="移除此项目"
                  onClick={(event) => void removeProject(event, item.value)}
                >
                  <X size={12} />
                </button>
              ) : null}
            </div>
          ))}
          <div className="project-dropdown-divider" />
          <button
            type="button"
            className="project-dropdown-item project-dropdown-add"
            role="menuitem"
            onClick={() => void addProject()}
          >
            <Plus size={13} />
            <span>添加项目</span>
          </button>
        </div>
      ) : null}
    </div>
  );
}

function TopCommandBar({ store, projectPath }) {
  const canUseThreadActions = Boolean(store.hasActiveThreadActions?.());
  const providerLabel = store.provider === 'claude' ? 'Claude' : 'Codex';
  return (
    <div className="top-command" data-testid="chat-toolbar">
      <ProjectSelector store={store} projectPath={projectPath} />
      {canUseThreadActions ? (
        <button type="button" className="icon-btn" aria-label="复制当前线程" title="复制当前线程" onClick={() => void store.copyActiveThreadInfo()}><Copy size={15} /></button>
      ) : null}
      {canUseThreadActions ? (
        <button type="button" className="icon-btn" aria-label="停止" title="中断当前执行" onClick={() => void store.interruptActiveThread()}><CircleStop size={15} /></button>
      ) : null}
      <button
        type="button"
        className={`provider ${store.provider === 'claude' ? 'active' : ''}`}
        aria-label="切换 Claude / Codex provider"
        title="切换 Claude / Codex provider"
        onClick={() => void store.toggleProviderMode()}
      >
        <span />
        {providerLabel}
      </button>
      <button type="button" className="icon-btn launch-agent" aria-label="新对话" title="新对话：发送第一条消息时才会创建会话" onClick={store.newThread}><Pencil size={15} /></button>
      <button
        type="button"
        className="icon-btn"
        aria-label={canUseThreadActions ? '进程恢复' : '请先选择会话'}
        title={canUseThreadActions ? '手动杀进程并恢复连接' : '请先选择会话'}
        disabled={!canUseThreadActions}
        onClick={() => void store.recoverActiveThread()}
      >
        <RefreshCw size={15} />
      </button>
      {store.actionNotice?.message ? (
        <span
          className={`action-feedback ${store.actionNotice.tone || 'info'}`}
          data-testid="chat-action-feedback"
          role="status"
        >
          {store.actionNotice.message}
        </span>
      ) : null}
      <span className="project-pill" aria-label="当前工作目录" title={`当前窗口 CWD：${projectPath}`}><Folder size={14} /> {projectDisplayName(projectPath)}</span>
    </div>
  );
}

function ThreadRail({ store }) {
  const [showArchivedThreads, setShowArchivedThreads] = useState(false);
  const [confirmCleanMode, setConfirmCleanMode] = useState(false);
  const activeThreads = store.threads.filter((thread) => !thread.archived);
  const archivedThreads = store.threads.filter((thread) => thread.archived);
  const threads = showArchivedThreads ? archivedThreads : activeThreads;
  const visibleThreads = threads.map((thread) => ({ ...thread, staleReason: archivedStaleReason(thread) }));
  const staleThreadIds = showArchivedThreads
    ? visibleThreads.filter((thread) => thread.staleReason).map((thread) => thread.id)
    : [];
  const railLabel = showArchivedThreads ? '归档列表' : '会话列表';
  const toggleArchiveLabel = showArchivedThreads ? '返回会话列表' : '打开归档列表';
  const toggleArchiveList = () => {
    setShowArchivedThreads((value) => {
      const next = !value;
      if (!next) setConfirmCleanMode(false);
      return next;
    });
  };
  return (
    <aside className="thread-rail" data-testid="thread-rail">
      <div className="thread-tools">
        <span className="round thread-kind" role="img" aria-label={railLabel} title={railLabel}>
          <Bot size={17} />
        </span>
        <span className="count thread-count" role="img" aria-label={`${visibleThreads.length} 个 Agent`} title={`${visibleThreads.length} 个 Agent`}>
          <Bot size={14} />
          <strong>{visibleThreads.length}</strong>
        </span>
        <button type="button" className="round add" aria-label="新建对话" onClick={store.newThread}><Plus size={18} /></button>
        {showArchivedThreads && staleThreadIds.length > 0 && !confirmCleanMode ? (
          <button
            type="button"
            className="round thread-clean"
            aria-label="清理无用对话"
            title="清理无用对话"
            onClick={() => setConfirmCleanMode(true)}
          >
            <Trash2 size={15} />
          </button>
        ) : null}
        {showArchivedThreads && confirmCleanMode ? (
          <>
            <button
              type="button"
              className="thread-clean-confirm"
              onClick={() => {
                setConfirmCleanMode(false);
                void store.deleteStaleThreads(staleThreadIds);
              }}
            >
              确认
            </button>
            <button type="button" className="thread-clean-cancel" onClick={() => setConfirmCleanMode(false)}>取消</button>
          </>
        ) : null}
        <button
          type="button"
          className={`round thread-archive-toggle ${showArchivedThreads ? 'active' : ''}`}
          aria-label={toggleArchiveLabel}
          title={toggleArchiveLabel}
          onClick={toggleArchiveList}
        >
          {showArchivedThreads ? <ArrowLeft size={15} /> : <Archive size={15} />}
        </button>
      </div>
      <div className="thread-list">
        {visibleThreads.length === 0 ? (
          <p className="thread-empty">
            {showArchivedThreads ? '暂无归档会话' : '暂无会话，点击顶部「新对话」开始草稿'}
          </p>
        ) : null}
        {visibleThreads.map((thread) => {
          const active = store.activeThreadId === thread.id;
          const running = ['running', '工作中', 'pending', 'recovering'].includes((thread.status || '').toLowerCase()) || thread.status === '工作中';
          const archiveLabel = thread.archived ? '恢复会话' : '归档会话';
          return (
            <div
              key={thread.id}
              className={`thread-card ${active ? 'active' : ''}`}
            >
              <button
                type="button"
                className="thread-main"
                onClick={() => void store.setActiveThread(thread.id)}
              >
                <span className="thread-pin"><GitBranch size={15} /></span>
                <span className="thread-name">{thread.name}</span>
                <b>{thread.provider || 'Codex'}</b>
                <em className={running ? 'running' : ''}>
                  {running ? '工作中' : thread.status || '等待指示'}
                  {thread.staleReason ? (
                    <span className="thread-stale-badge" data-stale-reason={thread.staleReason}>
                      {thread.staleReason === 'expired' ? '超7天' : '空对话'}
                    </span>
                  ) : null}
                </em>
              </button>
              <button
                type="button"
                className={`thread-archive ${thread.archived ? 'active' : ''}`}
                aria-label={archiveLabel}
                title={archiveLabel}
                onClick={() => void store.archiveThread(thread.id, !thread.archived)}
              >
                <Archive size={15} />
              </button>
            </div>
          );
        })}
      </div>
    </aside>
  );
}

function ModelSelector({ store, activeThreadId }) {
  const [open, setOpen] = useState(false);
  const wrapRef = useRef(null);
  const activeThreadConfig = activeThreadId ? store.threadConfigByThread?.[activeThreadId] : null;
  const providerKey = normalizeProviderKey(activeThreadConfig?.provider || store.providerConfig?.provider || store.provider);
  const providerDefaults = MODEL_DEFAULTS_BY_PROVIDER[providerKey] || MODEL_DEFAULTS_BY_PROVIDER.codex;
  const canOverrideThread = Boolean(activeThreadId && activeThreadConfig?.supportsThreadOverride);
  const activeModel = canOverrideThread
    ? normalizeConfigText(activeThreadConfig?.override?.model || activeThreadConfig?.effective?.model || providerDefaults.model)
    : normalizeConfigText(store.providerConfig?.model || providerDefaults.model);
  const activeEffort = canOverrideThread
    ? normalizeConfigText(activeThreadConfig?.override?.effort || activeThreadConfig?.effective?.effort || providerDefaults.effort)
    : normalizeConfigText(store.providerConfig?.effort || providerDefaults.effort);
  const draftModel = canOverrideThread ? normalizeConfigText(activeThreadConfig?.override?.model) : activeModel;
  const draftEffort = canOverrideThread ? normalizeConfigText(activeThreadConfig?.override?.effort) : activeEffort;
  const [draft, setDraft] = useState({ model: draftModel, effort: draftEffort });

  useEffect(() => {
    if (!open) {
      setDraft({ model: draftModel, effort: draftEffort });
    }
  }, [draftModel, draftEffort, open]);

  useEffect(() => {
    if (!open) return undefined;
    const onPointerDown = (event) => {
      if (wrapRef.current && !wrapRef.current.contains(event.target)) setOpen(false);
    };
    document.addEventListener('pointerdown', onPointerDown, true);
    return () => document.removeEventListener('pointerdown', onPointerDown, true);
  }, [open]);

  const openSelector = async () => {
    const nextOpen = !open;
    setDraft({ model: draftModel, effort: draftEffort });
    setOpen(nextOpen);
    if (!nextOpen || !activeThreadId) return;
    const loaded = await store.loadThreadConfig?.(activeThreadId);
    if (!loaded) return;
    const loadedCanOverride = Boolean(loaded.supportsThreadOverride);
    setDraft({
      model: loadedCanOverride ? normalizeConfigText(loaded.override?.model) : activeModel,
      effort: loadedCanOverride ? normalizeConfigText(loaded.override?.effort) : activeEffort,
    });
  };

  const saveModelConfig = async (patch) => {
    let next = { ...draft, ...patch };
    if (
      providerKey === 'claude'
      && normalizeConfigText(next.effort).toLowerCase() === 'max'
      && !isClaudeOpusFamilyModel(next.model || activeModel)
    ) {
      next = { ...next, effort: 'high' };
    }
    setDraft(next);
    await store.saveComposerModelConfig?.({ model: next.model, effort: next.effort });
  };

  const restoreInheritance = async () => {
    await store.restoreComposerModelInheritance?.();
    setOpen(false);
  };

  const selectedModel = canonicalizeModelValue(providerKey, draft.model || activeModel);
  const selectedEffort = draft.effort || activeEffort;
  const selectModelValue = canOverrideThread
    ? canonicalizeModelValue(providerKey, draft.model)
    : canonicalizeModelValue(providerKey, draft.model || activeModel);
  const selectEffortValue = canOverrideThread ? draft.effort : draft.effort || activeEffort;
  const modelOptions = appendCurrentModelOption(providerKey, selectedModel);
  const effortOptions = appendCurrentEffortOption(providerKey, selectedEffort, selectedModel);
  const inherited = canOverrideThread && !activeThreadConfig?.override?.model && !activeThreadConfig?.override?.effort;
  const label = composerModelLabel(providerKey, activeModel, activeEffort);
  const inheritModelLabel = activeModel ? `默认（当前：${modelOptionFor(providerKey, activeModel)?.label || activeModel}）` : '默认';
  const inheritEffortLabel = activeEffort ? `默认（当前：${effortOptionFor(providerKey, activeEffort)?.label || activeEffort}）` : '默认';
  const selectorBusy = Boolean(store.threadConfigSaving || (activeThreadId && store.threadConfigLoadingByThread?.[activeThreadId]));

  return (
    <div className="composer-model-wrap" ref={wrapRef}>
      <button
        type="button"
        className="composer-model"
        aria-label="选择模型"
        aria-expanded={open}
        aria-haspopup="dialog"
        aria-busy={selectorBusy}
        title={canOverrideThread ? '线程执行配置' : '全局模型配置'}
        onClick={() => void openSelector()}
      >
        {label}
        <ChevronDown size={12} />
      </button>
      {open ? (
        <div className="model-dropdown" role="dialog" aria-label="模型配置">
          <label>
            <span>模型</span>
            <select aria-label="模型" value={selectModelValue} disabled={selectorBusy} onChange={(event) => void saveModelConfig({ model: event.target.value })}>
              {canOverrideThread ? <option value="">{inheritModelLabel}</option> : null}
              {modelOptions.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}
            </select>
          </label>
          <label>
            <span>强度</span>
            <select aria-label="推理强度" value={selectEffortValue} disabled={selectorBusy} onChange={(event) => void saveModelConfig({ effort: event.target.value })}>
              {canOverrideThread ? <option value="">{inheritEffortLabel}</option> : null}
              {effortOptions.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}
            </select>
          </label>
          {canOverrideThread && !inherited ? (
            <button type="button" className="model-inherit" disabled={selectorBusy} onClick={() => void restoreInheritance()}>
              继承全局
            </button>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

function Conversation({
  messages,
  draft,
  setDraft,
  sendMessage,
  attachments,
  selectFiles,
  removeAttachment,
  sending,
  store,
  projectPath,
  permission,
  setPermission,
  tokenUsage,
  activeThreadId,
}) {
  return (
    <section className="conversation">
      <div className="timeline" data-testid="chat-timeline">
        {messages.length === 0 ? (
          <div className="empty-chat">
            <h2>我们应该在 {projectDisplayName(projectPath)} 中构建什么？</h2>
            <p>{projectPath}</p>
          </div>
        ) : null}
        {messages.map((message) => (
          <article key={message.id} className={`message ${message.role}`}>
            <div className="avatar">{message.role === 'user' ? 'U' : 'AI'}</div>
            <div className="bubble">
              <header><span>{message.role === 'user' ? '你' : 'AI'}</span><time>{formatTime(message.time)}</time></header>
              <pre>{message.text}</pre>
            </div>
          </article>
        ))}
      </div>
      <WorkStatus sending={sending} activeThreadId={activeThreadId} tokenUsage={tokenUsage} />
      <footer className="composer composer--docked" data-testid="composer-dock">
        <div className="composer-card">
          {attachments.length > 0 ? (
            <div className="attachments">
              {attachments.map((item) => (
                <button key={item.path} type="button" onClick={() => removeAttachment(item.path)}>
                  {item.name || item.path}
                </button>
              ))}
            </div>
          ) : null}
          <textarea
            data-testid="composer-input"
            value={draft}
            onChange={(event) => setDraft(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === 'Enter' && !event.shiftKey) {
                event.preventDefault();
                void sendMessage();
              }
            }}
            placeholder="输入给 Agent 的内容，Enter 发送，Shift+Enter 换行"
          />
          <div className="composer-meta">
            <button type="button" className="composer-attach" aria-label="添加文件" onClick={() => void selectFiles()}>
              <Plus size={18} />
            </button>
            <label className="permission-chip">
              <span className="sr-only">发送权限</span>
              <select aria-label="发送权限" value={permission} onChange={(event) => setPermission(event.target.value)}>
                <option>完全访问权限</option>
                <option>工作区写入</option>
                <option>只读模式</option>
              </select>
            </label>
            <span className="composer-project" data-testid="composer-project" title={projectPath}>
              <Folder size={14} />
              {projectDisplayName(projectPath)}
            </span>
            <ModelSelector store={store} activeThreadId={activeThreadId} />
            <button type="button" className="send" aria-label="发送消息" disabled={sending} onClick={() => void sendMessage()}>
              <Send size={18} />
            </button>
          </div>
        </div>
      </footer>
    </section>
  );
}

function WorkStatus({ sending, activeThreadId, tokenUsage }) {
  return (
    <div className="work-status">
      <span className="spinner" /> {sending ? '发送中' : activeThreadId ? '已连接' : '待启动'}
      <em>{activeThreadId ? `线程 ${activeThreadId}` : '发送首条消息后创建线程'}</em>
      <code>{tokenUsage ? `${tokenUsage.usedTokens} / ${tokenUsage.contextWindowTokens} tokens` : 'token usage 等待后端同步'}</code>
    </div>
  );
}

function RuntimePanel({ diffText, tokenUsage, warnings, activity }) {
  return (
    <aside className="runtime-panel">
      <div className="runtime-toolbar">
        <button type="button"><Image size={14} /> {diffText ? 1 : 0}</button>
        <button type="button"><FileText size={14} /> {activity.length}</button>
        <span className="score good">+{warnings.filter((item) => item.level === 'warn').length}</span>
        <span className="score bad">-{warnings.filter((item) => item.level === 'error').length}</span>
      </div>
      <div className="diff-empty">{diffText ? <pre>{diffText}</pre> : '暂无代码变更'}</div>
      <div className="runtime-icons">
        {[Code2, Boxes, FileText, Link2, GitBranch, AlertTriangle].map((Icon, index) => <Icon key={index} size={16} />)}
        <span>{tokenUsage ? `${tokenUsage.usedPercent.toFixed(1)}% context` : 'context --'}</span>
      </div>
      <div className="log-lines" data-testid="warning-log-panel">
        {warnings.length === 0 ? <p><time>--:--</time> warning log 等待事件</p> : null}
        {warnings.map((entry) => (
          <p key={entry.id}>
            <time>{formatTime(entry.timestamp)}</time> <b>{entry.event}</b> · {JSON.stringify(entry.fields)}
          </p>
        ))}
      </div>
    </aside>
  );
}

function formatTime(value) {
  const date = new Date(value);
  if (!Number.isFinite(date.getTime())) return '--:--';
  return date.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' });
}

function PageHeader({ icon: Icon, title, subtitle, actions }) {
  return (
    <header className="page-header">
      <h1><Icon size={25} /> {title}</h1>
      {subtitle ? <p>{subtitle}</p> : null}
      {actions ? <div className="page-actions">{actions}</div> : null}
    </header>
  );
}

function PromptPage({ projectPath, refreshKey = 0 }) {
  return <PromptPageView projectPath={projectPath} refreshKey={refreshKey} />;
}


function WorkflowPage({ projectPath, store, refreshKey = 0 }) {
  const workflowCacheKey = normalizeSettingsCwd(projectPath);
  const cachedWorkflowPage = useClientStore((state) => state.workflowPageCacheByCwd?.[workflowCacheKey]);
  const setWorkflowPageCache = useClientStore((state) => state.setWorkflowPageCache);
  const cachedSelectedDagKey = textValue(cachedWorkflowPage?.selectedDagKey);
  const cachedSelectedDetail = cachedSelectedDagKey ? cachedWorkflowPage?.detailsByDagKey?.[cachedSelectedDagKey] : null;
  const [items, setItems] = useState(() => (Array.isArray(cachedWorkflowPage?.items) ? cachedWorkflowPage.items : []));
  const [activeCategory, setActiveCategory] = useState(DAG_CATEGORIES[0].key);
  const [categoryManuallySelected, setCategoryManuallySelected] = useState(false);
  const [selectedDagKey, setSelectedDagKey] = useState(() => cachedSelectedDagKey);
  const [detailDag, setDetailDag] = useState(() => cachedSelectedDetail?.detailDag || null);
  const [nodes, setNodes] = useState(() => (Array.isArray(cachedSelectedDetail?.nodes) ? cachedSelectedDetail.nodes : []));
  const [runs, setRuns] = useState(() => (Array.isArray(cachedSelectedDetail?.runs) ? cachedSelectedDetail.runs : []));
  const [activeRun, setActiveRun] = useState(() => cachedSelectedDetail?.activeRun || null);
  const [selectedRunKey, setSelectedRunKey] = useState(() => textValue(cachedSelectedDetail?.selectedRunKey));
  const [selectedRun, setSelectedRun] = useState(() => cachedSelectedDetail?.selectedRun || null);
  const [loading, setLoading] = useState(false);
  const [detailLoading, setDetailLoading] = useState(false);
  const [actioning, setActioning] = useState('');
  const [savingNodeKey, setSavingNodeKey] = useState('');
  const [error, setError] = useState('');
  const [notice, setNotice] = useState(null);
  const [deleteTarget, setDeleteTarget] = useState(null);
  const [scheduleOpen, setScheduleOpen] = useState(false);
  const [scheduleCron, setScheduleCron] = useState('0 8 * * *');
  const hasLoadedDagsRef = useRef(Boolean(cachedWorkflowPage?.hasLoadedDags));
  const selectedDagKeyRef = useRef(selectedDagKey);
  const selectedRunKeyRef = useRef(selectedRunKey);
  const autoRefreshInFlightRef = useRef(false);
  const workflowRefreshKey = Number(refreshKey || 0);

  const refreshDags = useCallback(async (options = {}) => {
    const isCancelled = typeof options.isCancelled === 'function' ? options.isCancelled : () => false;
    const silent = options.silent === true;
    const cached = useClientStore.getState().workflowPageCacheByCwd?.[workflowCacheKey];
    const hasUsableSnapshot = hasLoadedDagsRef.current || Boolean(cached?.hasLoadedDags);
    const showBlockingLoading = !silent && !hasUsableSnapshot;
    if (showBlockingLoading) {
      setLoading(true);
      setError('');
    } else if (!silent) {
      setError('');
    }
    try {
      const cwd = normalizeSettingsCwd(projectPath);
      const response = await getDashboardPage({ cwd, page: 'dags' });
      const nextItems = normalizeDagsResponse(response);
      if (!isCancelled()) {
        hasLoadedDagsRef.current = true;
        setItems(nextItems);
        setWorkflowPageCache(cwd, { items: nextItems, hasLoadedDags: true });
      }
      return nextItems;
    } catch (err) {
      if (!isCancelled()) {
        if (showBlockingLoading) setItems([]);
        if (!silent || !hasUsableSnapshot) setError(`加载任务流程失败：${err.message || String(err)}`);
      }
      return [];
    } finally {
      if (!isCancelled() && showBlockingLoading) setLoading(false);
    }
  }, [projectPath, setWorkflowPageCache, workflowCacheKey]);

  useEffect(() => {
    let cancelled = false;
    const cached = useClientStore.getState().workflowPageCacheByCwd?.[workflowCacheKey];
    refreshDags({ isCancelled: () => cancelled, silent: Boolean(cached?.hasLoadedDags) });
    return () => {
      cancelled = true;
    };
  }, [refreshDags, workflowCacheKey]);

  const counts = useMemo(() => categoryCounts(items), [items]);
  const preferredCategory = useMemo(() => firstAvailableCategory(items), [items]);
  const visibleItems = useMemo(
    () => items.filter((item) => dagCategoryOf(item) === activeCategory),
    [activeCategory, items],
  );
  const selectedDag = useMemo(
    () => items.find((item) => item.dagKey === selectedDagKey) || visibleItems[0] || null,
    [items, selectedDagKey, visibleItems],
  );

  useEffect(() => {
    selectedDagKeyRef.current = selectedDagKey;
    if (selectedDagKey) setWorkflowPageCache(workflowCacheKey, { selectedDagKey });
    setNotice(null);
  }, [selectedDagKey, setWorkflowPageCache, workflowCacheKey]);

  useEffect(() => {
    selectedRunKeyRef.current = selectedRunKey;
  }, [selectedRunKey]);

  useEffect(() => {
    const cached = useClientStore.getState().workflowPageCacheByCwd?.[workflowCacheKey];
    const cachedKey = textValue(cached?.selectedDagKey);
    const cachedDetail = cachedKey ? cached?.detailsByDagKey?.[cachedKey] : null;
    hasLoadedDagsRef.current = Boolean(cached?.hasLoadedDags);
    setItems(Array.isArray(cached?.items) ? cached.items : []);
    setSelectedDagKey(cachedKey);
    setDetailDag(cachedDetail?.detailDag || null);
    setNodes(Array.isArray(cachedDetail?.nodes) ? cachedDetail.nodes : []);
    setRuns(Array.isArray(cachedDetail?.runs) ? cachedDetail.runs : []);
    setActiveRun(cachedDetail?.activeRun || null);
    setSelectedRunKey(textValue(cachedDetail?.selectedRunKey));
    setSelectedRun(cachedDetail?.selectedRun || null);
    setLoading(false);
    setDetailLoading(false);
    setError('');
    setNotice(null);
  }, [workflowCacheKey]);

  useEffect(() => {
    if (!cachedWorkflowPage) return;
    if (Array.isArray(cachedWorkflowPage.items)) setItems(cachedWorkflowPage.items);
    if (cachedWorkflowPage.hasLoadedDags) hasLoadedDagsRef.current = true;
  }, [cachedWorkflowPage]);

  const clearNotice = useCallback(() => {
    setNotice(null);
  }, []);

  const showTaskNotice = useCallback((message, taskKey = selectedDagKey) => {
    const key = textValue(taskKey);
    if (!message || !key || selectedDagKeyRef.current !== key) return;
    setNotice({ dagKey: key, message });
  }, [selectedDagKey]);

  useEffect(() => {
    if (!categoryManuallySelected && items.length > 0 && visibleItems.length === 0 && activeCategory !== preferredCategory) {
      setActiveCategory(preferredCategory);
    }
  }, [activeCategory, categoryManuallySelected, items.length, preferredCategory, visibleItems.length]);

  useEffect(() => {
    if (visibleItems.length === 0) {
      setSelectedDagKey('');
      return;
    }
    if (!visibleItems.some((item) => item.dagKey === selectedDagKey)) {
      setSelectedDagKey(visibleItems[0].dagKey);
    }
  }, [selectedDagKey, visibleItems]);

  const loadRunDetail = useCallback(async (runKey, options = {}) => {
    const key = textValue(runKey);
    if (!key) {
      setSelectedRun(null);
      setSelectedRunKey('');
      return null;
    }
    const isCancelled = typeof options.isCancelled === 'function' ? options.isCancelled : () => false;
    const response = await getDagRun({ runKey: key });
    if (isCancelled()) return null;
    const run = response?.run ? normalizeDagRun(response.run) : null;
    const runNodes = Array.isArray(response?.nodes) ? response.nodes.map((node, index) => normalizeDagNode(node, index)) : [];
    setSelectedRunKey(key);
    setSelectedRun({ run, nodes: runNodes });
    return { run, nodes: runNodes };
  }, []);

  const refreshDetail = useCallback(async (dagKey, preferredRunKey = '', options = {}) => {
    const key = textValue(dagKey);
    if (!key) return;
    const isCancelled = typeof options.isCancelled === 'function' ? options.isCancelled : () => false;
    const silent = options.silent === true;
    const cachedDetail = useClientStore.getState().workflowPageCacheByCwd?.[workflowCacheKey]?.detailsByDagKey?.[key];
    const showDetailLoading = !silent && !cachedDetail;
    if (showDetailLoading) {
      setDetailLoading(true);
      setError('');
    } else if (!silent) {
      setError('');
    }
    try {
      const [detailResponse, runsResponse, activeResponse] = await Promise.all([
        getDagDetail({ dagKey: key }),
        getDagRuns({ dagKey: key, limit: DAG_RECENT_RUN_LIMIT }),
        getDagRuns({ dagKey: key, status: 'running', limit: 1 }),
      ]);
      if (isCancelled()) return;

      const listDag = items.find((item) => item.dagKey === key);
      const dag = normalizeDashboardDag({ ...objectValue(listDag?.raw), ...objectValue(detailResponse?.dag) });
      const normalizedNodes = Array.isArray(detailResponse?.nodes)
        ? detailResponse.nodes.map((node, index) => normalizeDagNode(node, index))
        : [];
      const nextRuns = Array.isArray(runsResponse?.runs)
        ? runsResponse.runs.map((run, index) => normalizeDagRun(run, index))
        : [];
      const runningRun = Array.isArray(activeResponse?.runs) && activeResponse.runs.length > 0
        ? normalizeDagRun(activeResponse.runs[0])
        : null;
      const nextRunKey = textValue(preferredRunKey)
        || runningRun?.runKey
        || nextRuns[0]?.runKey
        || '';
      let loadedRun = null;

      setDetailDag(dag);
      setNodes(normalizedNodes);
      setRuns(nextRuns);
      setActiveRun(runningRun);
      if (nextRunKey) loadedRun = await loadRunDetail(nextRunKey, { isCancelled });
      else {
        setSelectedRunKey('');
        setSelectedRun(null);
      }
      if (isCancelled()) return;
      const currentDetails = useClientStore.getState().workflowPageCacheByCwd?.[workflowCacheKey]?.detailsByDagKey || {};
      setWorkflowPageCache(workflowCacheKey, {
        selectedDagKey: key,
        detailsByDagKey: {
          ...currentDetails,
          [key]: {
            detailDag: dag,
            nodes: normalizedNodes,
            runs: nextRuns,
            activeRun: runningRun,
            selectedRunKey: nextRunKey,
            selectedRun: loadedRun,
          },
        },
      });
    } catch (err) {
      if (!isCancelled()) {
        if (showDetailLoading) {
          setDetailDag(null);
          setNodes([]);
          setRuns([]);
          setActiveRun(null);
          setSelectedRun(null);
        }
        setError(`加载任务流程详情失败：${err.message || String(err)}`);
      }
    } finally {
      if (!isCancelled() && showDetailLoading) setDetailLoading(false);
    }
  }, [items, loadRunDetail, setWorkflowPageCache, workflowCacheKey]);

  const refreshWorkflowSurface = useCallback(async (options = {}) => {
    if (autoRefreshInFlightRef.current) return;
    autoRefreshInFlightRef.current = true;
    try {
      await refreshDags(options);
      const activeKey = selectedDagKeyRef.current;
      if (activeKey) {
        await refreshDetail(activeKey, selectedRunKeyRef.current, options);
      }
    } finally {
      autoRefreshInFlightRef.current = false;
    }
  }, [refreshDags, refreshDetail]);

  useEffect(() => {
    if (workflowRefreshKey <= 0) return undefined;
    let cancelled = false;
    void refreshWorkflowSurface({ isCancelled: () => cancelled, silent: true });
    return () => {
      cancelled = true;
    };
  }, [refreshWorkflowSurface, workflowRefreshKey]);

  useEffect(() => {
    const runAutoRefresh = () => {
      if (typeof document !== 'undefined' && document.visibilityState === 'hidden') return;
      void refreshWorkflowSurface({ silent: true });
    };
    const handleVisibilityChange = () => {
      if (typeof document === 'undefined' || document.visibilityState === 'visible') {
        runAutoRefresh();
      }
    };
    const intervalID = window.setInterval(runAutoRefresh, WORKFLOW_AUTO_REFRESH_MS);
    window.addEventListener('focus', runAutoRefresh);
    document.addEventListener('visibilitychange', handleVisibilityChange);
    return () => {
      window.clearInterval(intervalID);
      window.removeEventListener('focus', runAutoRefresh);
      document.removeEventListener('visibilitychange', handleVisibilityChange);
    };
  }, [refreshWorkflowSurface]);

  useEffect(() => {
    let cancelled = false;
    if (selectedDagKey) {
      const cachedDetail = useClientStore.getState().workflowPageCacheByCwd?.[workflowCacheKey]?.detailsByDagKey?.[selectedDagKey];
      if (cachedDetail) {
        setDetailDag(cachedDetail.detailDag || null);
        setNodes(Array.isArray(cachedDetail.nodes) ? cachedDetail.nodes : []);
        setRuns(Array.isArray(cachedDetail.runs) ? cachedDetail.runs : []);
        setActiveRun(cachedDetail.activeRun || null);
        setSelectedRunKey(textValue(cachedDetail.selectedRunKey));
        setSelectedRun(cachedDetail.selectedRun || null);
      }
      refreshDetail(selectedDagKey, textValue(cachedDetail?.selectedRunKey), {
        isCancelled: () => cancelled,
        silent: Boolean(cachedDetail) || hasLoadedDagsRef.current,
      });
    } else {
      setDetailDag(null);
      setNodes([]);
      setRuns([]);
      setActiveRun(null);
      setSelectedRun(null);
      setSelectedRunKey('');
    }
    return () => {
      cancelled = true;
    };
  }, [refreshDetail, selectedDagKey, workflowCacheKey]);

  const activeDetailDag = detailDag || selectedDag;
  const dagKey = activeDetailDag?.dagKey || selectedDag?.dagKey || '';
  const baseVersion = dagVersionOf(activeDetailDag);
  const activeRunKey = activeRun?.runKey || (isRunningStatus(selectedRun?.run?.status) ? selectedRun?.run?.runKey : '');
  const finalText = finalOutputText(selectedRun?.run) || finalOutputText(activeRun) || finalOutputText(selectedDag?.latestRun);
  const recentRunPanelLabel = dagStatusLabel(activeRun?.status || runs[0]?.status || activeDetailDag?.latestRun?.status);
  const scheduleActionLabel = isScheduledTrigger(activeDetailDag?.trigger) || activeDetailDag?.cronExpr ? '修改计划' : '创建定时任务';
  const scheduleToggleVisible = isScheduledTrigger(activeDetailDag?.trigger) && Boolean(activeDetailDag?.cronExpr);
  const startDisabledReason = useMemo(() => {
    if (!dagKey) return '未选择任务流程';
    if (loading || detailLoading) return '任务流程详情加载中';
    if (activeRunKey) return '已有运行正在进行';
    if (!STARTABLE_DAG_STATUSES.has(textValue(activeDetailDag?.status).toLowerCase())) return '当前流程状态不可运行';
    if (!STARTABLE_DAG_TRIGGERS.has(textValue(activeDetailDag?.trigger).toLowerCase())) return '当前触发方式不可运行';
    return '';
  }, [activeDetailDag, activeRunKey, dagKey, detailLoading, loading]);

  const runSelectedDag = useCallback(async () => {
    if (startDisabledReason) return;
    const targetDagKey = dagKey;
    setActioning('start');
    setError('');
    clearNotice();
    try {
      const result = await startDag({
        dagKey: targetDagKey,
        triggerSource: 'manual',
        idempotencyKey: `ui-${Date.now()}-${Math.random().toString(16).slice(2)}`,
      });
      const runKey = runKeyOf(result);
      await refreshDags();
      await refreshDetail(targetDagKey, runKey);
      const warning = textValue(result?.warning);
      showTaskNotice(warning ? `已启动，后端提示：${warning}` : '已启动任务流程', targetDagKey);
    } catch (err) {
      setError(`启动任务流程失败：${err.message || String(err)}`);
    } finally {
      setActioning('');
    }
  }, [clearNotice, dagKey, refreshDags, refreshDetail, showTaskNotice, startDisabledReason]);

  const stopSelectedDag = useCallback(async () => {
    if (!dagKey || !activeRunKey) return;
    const targetDagKey = dagKey;
    setActioning('stop');
    setError('');
    clearNotice();
    try {
      await terminateDagRun({ dagKey: targetDagKey, runKey: activeRunKey, reason: 'user_requested' });
      await refreshDags();
      await refreshDetail(targetDagKey);
      showTaskNotice('已停止运行', targetDagKey);
    } catch (err) {
      setError(`停止运行失败：${err.message || String(err)}`);
    } finally {
      setActioning('');
    }
  }, [activeRunKey, clearNotice, dagKey, refreshDags, refreshDetail, showTaskNotice]);

  const confirmDeleteDAG = useCallback(async () => {
    const target = deleteTarget;
    const targetKey = target?.dagKey || dagKey;
    if (!targetKey) return;
    setActioning('delete');
    setError('');
    clearNotice();
    try {
      await deleteDag({ dagKey: targetKey });
      setDeleteTarget(null);
      const nextItems = await refreshDags();
      setSelectedDagKey(nextItems.find((item) => dagCategoryOf(item) === activeCategory)?.dagKey || nextItems[0]?.dagKey || '');
      showTaskNotice(`已删除 ${target?.title || targetKey}`, targetKey);
    } catch (err) {
      setError(`删除任务流程失败：${err.message || String(err)}`);
    } finally {
      setActioning('');
    }
  }, [activeCategory, clearNotice, dagKey, deleteTarget, refreshDags, showTaskNotice]);

  const openScheduleModal = useCallback(() => {
    setScheduleCron(activeDetailDag?.cronExpr || '0 8 * * *');
    setScheduleOpen(true);
  }, [activeDetailDag]);

  const saveSchedule = useCallback(async (nextCronExpr = '') => {
    const cronExpr = textValue(nextCronExpr) || textValue(scheduleCron);
    if (!dagKey || !cronExpr) return;
    if (baseVersion === null) {
      setError('任务流程详情不完整，无法保存定时任务');
      return;
    }
    const targetDagKey = dagKey;
    setActioning('schedule');
    setError('');
    clearNotice();
    try {
      await applyDagOps({
        dagKey: targetDagKey,
        baseVersion,
        ops: [{ op: 'update_dag', patch: { trigger: 'scheduled', cron_expr: cronExpr } }],
      });
      setScheduleOpen(false);
      await refreshDags();
      await refreshDetail(targetDagKey);
      showTaskNotice('已保存定时任务', targetDagKey);
    } catch (err) {
      setError(`保存定时任务失败：${err.message || String(err)}`);
    } finally {
      setActioning('');
    }
  }, [baseVersion, clearNotice, dagKey, refreshDags, refreshDetail, scheduleCron, showTaskNotice]);

  const toggleScheduleEnabled = useCallback(async () => {
    if (!dagKey) return;
    if (baseVersion === null) {
      setError('任务流程详情不完整，无法切换自动运行');
      return;
    }
    const targetDagKey = dagKey;
    const enabled = !activeDetailDag?.scheduleEnabled;
    setActioning('schedule-toggle');
    setError('');
    clearNotice();
    try {
      await applyDagOps({
        dagKey: targetDagKey,
        baseVersion,
        ops: [{ op: 'update_dag', patch: { schedule_enabled: enabled } }],
      });
      await refreshDags();
      await refreshDetail(targetDagKey);
      showTaskNotice(enabled ? '已启用自动运行' : '已暂停自动运行', targetDagKey);
    } catch (err) {
      setError(`切换自动运行失败：${err.message || String(err)}`);
    } finally {
      setActioning('');
    }
  }, [activeDetailDag, baseVersion, clearNotice, dagKey, refreshDags, refreshDetail, showTaskNotice]);

  const saveAgentNode = useCallback(async (form, node) => {
    if (!dagKey || !node?.nodeKey) return;
    if (baseVersion === null) {
      setError('任务流程详情不完整，无法保存步骤');
      return;
    }
    const targetDagKey = dagKey;
    setSavingNodeKey(node.nodeKey);
    setError('');
    clearNotice();
    try {
      await applyDagOps({
        dagKey: targetDagKey,
        baseVersion,
        ops: [{
          op: 'update_node',
          node_key: node.nodeKey,
          patch: dagNodePatchFromForm(form, node),
        }],
      });
      await refreshDetail(targetDagKey);
      showTaskNotice(`已保存步骤 ${node.title}`, targetDagKey);
    } catch (err) {
      setError(`保存步骤失败：${err.message || String(err)}`);
    } finally {
      setSavingNodeKey('');
    }
  }, [baseVersion, clearNotice, dagKey, refreshDetail, showTaskNotice]);

  const startDesignFlow = useCallback(async () => {
    setActioning('design');
    setError('');
    clearNotice();
    try {
      const cwd = normalizeSettingsCwd(projectPath);
      const response = await startThread({
        cwd,
        provider: store?.provider,
        name: 'AI 设计流程',
        agentKey: 'dag_designer',
        promptKey: 'main/dag_designer_zh',
        deferSpawn: true,
        config: {
          enabledTools: [...DAG_DESIGNER_ENABLED_TOOLS],
          providerNativeSkills: false,
        },
      });
      const threadId = threadIdFromStartResponse(response);
      if (threadId && typeof store?.setActiveThread === 'function') {
        await store.setActiveThread(threadId);
      }
      if (typeof store?.setActivePage === 'function') store.setActivePage('chat');
    } catch (err) {
      setError(`启动 AI 设计流程失败：${err.message || String(err)}`);
    } finally {
      setActioning('');
    }
  }, [clearNotice, projectPath, store]);

  const agentNodes = nodes.filter((node) => textValue(node.nodeType).toLowerCase() === 'agent');

  return (
    <section className="workflow-page">
      <PageHeader
        icon={Workflow}
        title="任务流程"
        subtitle={`当前项目：${projectPath}`}
        actions={(
          <button type="button" onClick={() => { void startDesignFlow(); }} disabled={actioning === 'design'}>
            {actioning === 'design' ? '启动中...' : 'AI 设计流程'}
          </button>
        )}
      />
      <div className="workflow-grid">
        <aside className="workflow-list">
          <div className="tabs" role="tablist" aria-label="任务流程分类">
            {DAG_CATEGORIES.map((category) => (
              <button
                key={category.key}
                type="button"
                role="tab"
                aria-selected={activeCategory === category.key ? 'true' : 'false'}
                className={activeCategory === category.key ? 'active' : ''}
                onClick={() => {
                  setCategoryManuallySelected(true);
                  setActiveCategory(category.key);
                }}
              >
                {category.label} {counts[category.key] || 0}
              </button>
            ))}
          </div>
          {loading ? <p className="console-message">正在加载任务流程...</p> : null}
          {!loading && visibleItems.length === 0 ? <p className="console-message">无任务</p> : null}
          {visibleItems.map((item) => (
            <button
              type="button"
              key={item.id}
              className={item.dagKey === selectedDagKey ? 'active' : ''}
              onClick={() => setSelectedDagKey(item.dagKey)}
            >
              <strong>{item.title}</strong>
              <span>{latestDagRunLabel(item) === '-' ? '暂无运行' : `最近运行：${latestDagRunLabel(item)}`}</span>
              <em>{displayDagStatusLabel(item)} · {schedulePlanLabel(item)} · {latestDagRunLabel(item)}</em>
            </button>
          ))}
        </aside>
        <section className="workflow-detail">
          {!activeDetailDag ? (
            <EmptyState icon={Workflow} title="暂无任务流程" text="左侧选择任务流程后查看详情。" />
          ) : (
            <>
              <div className="detail-top">
                <h2>{activeDetailDag.title}</h2>
                <button type="button" className="danger" onClick={() => setDeleteTarget(activeDetailDag)} disabled={actioning === 'delete'}>删除</button>
                <button type="button" onClick={openScheduleModal} disabled={baseVersion === null || actioning === 'schedule'}>
                  {scheduleActionLabel}
                </button>
                {scheduleToggleVisible ? (
                  <button type="button" onClick={() => { void toggleScheduleEnabled(); }} disabled={baseVersion === null || actioning === 'schedule-toggle'}>
                    {activeDetailDag.scheduleEnabled ? '暂停自动运行' : '启用自动运行'}
                  </button>
                ) : null}
                {activeRunKey ? (
                  <button type="button" className="danger" onClick={() => { void stopSelectedDag(); }} disabled={actioning === 'stop'}>
                    {actioning === 'stop' ? '停止中...' : '停止运行'}
                  </button>
                ) : null}
                <button
                  type="button"
                  onClick={() => { void runSelectedDag(); }}
                  disabled={Boolean(startDisabledReason) || actioning === 'start'}
                  title={startDisabledReason}
                >
                  {actioning === 'start' ? '启动中...' : '运行'}
                </button>
              </div>
              {detailLoading ? <p className="console-message">正在加载详情...</p> : null}
              {notice?.message && notice.dagKey === selectedDagKey ? <p className="settings-status">{notice.message}</p> : null}
              {error ? <p className="danger-text" role="alert">{error}</p> : null}
              {startDisabledReason ? <p className="console-message">{startDisabledReason}</p> : null}
              <Panel title="最终结果">{finalText || '当前运行尚未标记最终结果。'}</Panel>
              <div className="stat-grid">
                <Panel title="任务状态">{displayDagStatusLabel(activeDetailDag)}</Panel>
                <Panel title="运行计划">{schedulePlanLabel(activeDetailDag)}</Panel>
                <Panel title="最近运行">{recentRunPanelLabel === '-' ? latestDagRunLabel(activeDetailDag) : recentRunPanelLabel}</Panel>
                <Panel title="最终结果">{finalText ? '已生成' : '-'}</Panel>
              </div>
              <Panel title="运行历史">
                <div className="dag-run-list">
                  {runs.length === 0 ? <p>暂无运行记录</p> : null}
                  {runs.map((run, index) => (
                    <button
                      key={run.id}
                      type="button"
                      className={`run-row ${run.runKey === selectedRunKey ? 'active' : ''}`}
                      onClick={() => { void loadRunDetail(run.runKey); }}
                    >
                      <span>{`第 ${index + 1} 次运行`}</span>
                      <em>{dagStatusLabel(run.status)}</em>
                      <time>{run.startedAt || '时间未记录'}</time>
                    </button>
                  ))}
                </div>
              </Panel>
              <Panel title="执行步骤">
                <div className="dag-node-list">
                  {nodes.length === 0 ? <p>暂无步骤</p> : null}
                  {nodes.map((node) => (
                    <article key={node.id} className="dag-node-row">
                      <strong>{node.title}</strong>
                      <em>{dagStatusLabel(node.status)}</em>
                      {node.threadId ? <button type="button" onClick={() => { void store?.setActiveThread?.(node.threadId); store?.setActivePage?.('chat'); }}>查看对话</button> : null}
                    </article>
                  ))}
                </div>
              </Panel>
              <details className="workflow-advanced">
                <summary>高级设置</summary>
                {agentNodes.length > 0 ? (
                  <DagNodeEditor
                    nodes={agentNodes}
                    savingNodeKey={savingNodeKey}
                    onSave={saveAgentNode}
                  />
                ) : <p className="console-message">暂无可配置步骤</p>}
              </details>
            </>
          )}
        </section>
      </div>
      {scheduleOpen ? (
        <DagScheduleModal
          cron={scheduleCron}
          actionLabel={scheduleActionLabel}
          saving={actioning === 'schedule'}
          onClose={() => setScheduleOpen(false)}
          onSave={saveSchedule}
        />
      ) : null}
      {deleteTarget ? (
        <ConfirmDagDeleteModal
          dag={deleteTarget}
          deleting={actioning === 'delete'}
          onClose={() => setDeleteTarget(null)}
          onConfirm={confirmDeleteDAG}
        />
      ) : null}
    </section>
  );
}

function DagNodeEditor({ nodes, savingNodeKey, onSave }) {
  const [activeNodeKey, setActiveNodeKey] = useState(nodes[0]?.nodeKey || '');
  const activeNode = useMemo(
    () => nodes.find((node) => node.nodeKey === activeNodeKey) || nodes[0] || null,
    [activeNodeKey, nodes],
  );
  const [form, setForm] = useState(() => dagNodeFormFromNode(activeNode));
  const [formNodeKey, setFormNodeKey] = useState(activeNode?.nodeKey || '');

  useEffect(() => {
    if (!activeNode || nodes.some((node) => node.nodeKey === activeNodeKey)) return;
    setActiveNodeKey(nodes[0]?.nodeKey || '');
  }, [activeNode, activeNodeKey, nodes]);

  useEffect(() => {
    const nextNodeKey = activeNode?.nodeKey || '';
    if (nextNodeKey === formNodeKey) return;
    setFormNodeKey(nextNodeKey);
    setForm(dagNodeFormFromNode(activeNode));
  }, [activeNode, formNodeKey]);

  if (!activeNode) return null;
  const update = (key) => (event) => setForm((current) => ({ ...current, [key]: event.target.value }));
  const modelOptions = form.provider ? appendCurrentModelOption(form.provider, form.model) : [];

  return (
    <Panel title="步骤设置">
      <div className="dag-node-editor">
        <label>
          步骤
          <select value={activeNode.nodeKey} onChange={(event) => setActiveNodeKey(event.target.value)}>
            {nodes.map((node) => <option key={node.nodeKey} value={node.nodeKey}>{node.title}</option>)}
          </select>
        </label>
        <label>名称<input value={form.title} onChange={update('title')} aria-label="名称" /></label>
        <label>
          执行引擎
          <select value={form.provider} onChange={update('provider')} aria-label="执行引擎">
            <option value="">默认</option>
            <option value="claude">claude</option>
            <option value="codex">codex</option>
          </select>
        </label>
        <label>
          模型
          <select value={form.model} onChange={update('model')} aria-label="模型">
            <option value="">默认</option>
            {modelOptions.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
          </select>
        </label>
        <label>提示词<input value={form.promptKey} onChange={update('promptKey')} aria-label="提示词" /></label>
        <label>依赖步骤<input value={form.dependsOn} onChange={update('dependsOn')} aria-label="依赖步骤" /></label>
        <label className="wide">指令<textarea value={form.firstTurn} onChange={update('firstTurn')} aria-label="指令" /></label>
        <label>输出文件<input value={form.outputFile} onChange={update('outputFile')} aria-label="输出文件" /></label>
        <div className="dag-node-editor-actions">
          <button type="button" onClick={() => { void onSave(form, activeNode); }} disabled={savingNodeKey === activeNode.nodeKey}>
            {savingNodeKey === activeNode.nodeKey ? '保存中...' : '保存步骤'}
          </button>
        </div>
      </div>
    </Panel>
  );
}

function DagScheduleModal({ cron, actionLabel, saving, onClose, onSave }) {
  const initialSchedule = useMemo(() => scheduleStateFromCron(cron), [cron]);
  const [preset, setPreset] = useState(initialSchedule.preset);
  const [time, setTime] = useState(initialSchedule.time);
  const [weekday, setWeekday] = useState(initialSchedule.weekday);
  const [monthDay, setMonthDay] = useState(initialSchedule.monthDay);
  const [inputError, setInputError] = useState(initialSchedule.warning || '');
  const monthDays = useMemo(() => Array.from({ length: 31 }, (_item, index) => (index + 1).toString()), []);
  const previewText = scheduleLabelFromState({ preset, time, weekday, monthDay });

  useEffect(() => {
    setPreset(initialSchedule.preset);
    setTime(initialSchedule.time);
    setWeekday(initialSchedule.weekday);
    setMonthDay(initialSchedule.monthDay);
    setInputError(initialSchedule.warning || '');
  }, [initialSchedule]);

  const choose = (setter) => (event) => {
    setter(event.target.value);
    setInputError('');
  };

  const confirm = () => {
    const { cronExpr, error } = cronExprFromSchedule(preset, time, weekday, monthDay);
    if (error) {
      setInputError(error);
      return;
    }
    void onSave(cronExpr);
  };

  return (
    <div className="modal-overlay">
      <section className="modal-box" role="dialog" aria-modal="true" aria-label={actionLabel}>
        <header><h2>{actionLabel}</h2><button type="button" className="ghost" onClick={onClose} disabled={saving}>关闭</button></header>
        <div className="dag-node-editor">
          <label>
            运行频率
            <select value={preset} onChange={choose(setPreset)} disabled={saving} aria-label="运行频率">
              <option value="daily">每天</option>
              <option value="weekdays">工作日</option>
              <option value="weekly">每周</option>
              <option value="monthly">每月</option>
            </select>
          </label>
          {preset === 'weekly' ? (
            <label>
              星期几
              <select value={weekday} onChange={choose(setWeekday)} disabled={saving} aria-label="星期几">
                {DAG_WEEKDAY_OPTIONS.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}
              </select>
            </label>
          ) : null}
          {preset === 'monthly' ? (
            <label>
              每月几号
              <select value={monthDay} onChange={choose(setMonthDay)} disabled={saving} aria-label="每月几号">
                {monthDays.map((day) => <option key={day} value={day}>{day} 日</option>)}
              </select>
            </label>
          ) : null}
          <label>
            运行时间
            <input value={time} type="time" onChange={choose(setTime)} disabled={saving} aria-label="运行时间" />
          </label>
        </div>
        {previewText ? <p className="settings-status">{previewText} 自动运行</p> : null}
        {inputError ? <p className="danger-text" role="alert">{inputError}</p> : null}
        <footer>
          <button type="button" className="ghost" onClick={onClose} disabled={saving}>取消</button>
          <button type="button" onClick={confirm} disabled={saving}>{saving ? '保存中...' : actionLabel}</button>
        </footer>
      </section>
    </div>
  );
}

function ConfirmDagDeleteModal({ dag, deleting, onClose, onConfirm }) {
  return (
    <div className="modal-overlay">
      <section className="modal-box" role="dialog" aria-modal="true" aria-label="删除任务流程">
        <header><h2>删除任务流程</h2><button type="button" className="ghost" onClick={onClose} disabled={deleting}>关闭</button></header>
        <p>确定删除任务流程 “{dag.title}” 吗？该操作会删除流程定义和运行关联信息，无法恢复。</p>
        <p className="path">{dag.dagKey}</p>
        <footer>
          <button type="button" className="ghost" onClick={onClose} disabled={deleting}>取消</button>
          <button type="button" className="text-danger" onClick={() => { void onConfirm(); }} disabled={deleting}>{deleting ? '删除中...' : '确认删除'}</button>
        </footer>
      </section>
    </div>
  );
}

function SkillsPage({ projectPath, refreshKey = 0 }) {
  const skillCacheKey = normalizeSettingsCwd(projectPath);
  const cachedSkillPage = useClientStore((state) => state.skillPageCacheByCwd?.[skillCacheKey]);
  const setSkillPageCache = useClientStore((state) => state.setSkillPageCache);
  const [items, setItems] = useState(() => (
    Array.isArray(cachedSkillPage?.items) ? cachedSkillPage.items : []
  ));
  const [query, setQuery] = useState('');
  const [scopeFilter, setScopeFilter] = useState('all');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [editorOpen, setEditorOpen] = useState(false);
  const [editorForm, setEditorForm] = useState(emptySkillForm);
  const [activeSkillPath, setActiveSkillPath] = useState('');
  const [skillFiles, setSkillFiles] = useState([]);
  const [summarySuggestion, setSummarySuggestion] = useState('');
  const [summarySuggesting, setSummarySuggesting] = useState(false);
  const [saving, setSaving] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState(null);
  const [deleting, setDeleting] = useState(false);
  const [importScopeOpen, setImportScopeOpen] = useState(false);
  const [importing, setImporting] = useState(false);
  const [resolutionConflicts, setResolutionConflicts] = useState(() => (
    Array.isArray(cachedSkillPage?.resolutionConflicts)
      ? cachedSkillPage.resolutionConflicts
      : []
  ));
  const [resolutionPreview, setResolutionPreview] = useState(null);
  const [resolutionNamePrompt, setResolutionNamePrompt] = useState(null);
  const [resolutionNameInput, setResolutionNameInput] = useState('');
  const [resolutionActioning, setResolutionActioning] = useState('');
  const hasLoadedSkillsRef = useRef(Boolean(cachedSkillPage?.hasLoadedSkills));
  const refreshInFlightRef = useRef(false);
  const skillRefreshKey = Number(refreshKey || 0);

  const refreshSkills = useCallback(async (options = {}) => {
    const isCancelled = typeof options.isCancelled === 'function' ? options.isCancelled : () => false;
    const silent = options.silent === true;
    const cwd = normalizeSettingsCwd(projectPath);
    const cached = useClientStore.getState().skillPageCacheByCwd?.[cwd];
    const hasUsableSnapshot = hasLoadedSkillsRef.current || Boolean(cached?.hasLoadedSkills);
    const showBlockingLoading = !silent && !hasUsableSnapshot;
    if (showBlockingLoading) {
      setLoading(true);
      setError('');
      setNotice('');
    } else if (!silent) {
      setError('');
    }
    try {
      const response = await withTimeout(
        getDashboardPage({ cwd, page: 'skills' }),
        SKILLS_REQUEST_TIMEOUT_MS,
        '技能列表加载超时，请检查技能目录或后端状态。',
      );
      if (isCancelled()) return;
      const nextItems = normalizeSkillsResponse(response);
      hasLoadedSkillsRef.current = true;
      setItems(nextItems);
      setSkillPageCache(cwd, { items: nextItems, hasLoadedSkills: true });
      setError('');
    } catch (err) {
      if (isCancelled()) return;
      if (showBlockingLoading) setItems([]);
      if (!silent || !hasUsableSnapshot) setError(err.message || String(err));
    } finally {
      if (!isCancelled() && showBlockingLoading) setLoading(false);
    }
  }, [projectPath, setSkillPageCache]);

  const refreshResolutions = useCallback(async (options = {}) => {
    const isCancelled = typeof options.isCancelled === 'function' ? options.isCancelled : () => false;
    try {
      const cwd = normalizeSettingsCwd(projectPath);
      const response = await withTimeout(
        listSkillResolutions({ cwd }),
        SKILLS_REQUEST_TIMEOUT_MS,
        '技能冲突检查超时，请检查技能目录或后端状态。',
      );
      if (!isCancelled()) {
        const conflicts = normalizeResolutionResponse(response);
        setResolutionConflicts(conflicts);
        setSkillPageCache(cwd, { resolutionConflicts: conflicts });
        if (conflicts.length === 0) {
          setResolutionPreview(null);
          setResolutionNamePrompt(null);
          setResolutionNameInput('');
        }
      }
    } catch (err) {
      if (!isCancelled() && options.silent !== true) setError(`读取技能冲突失败：${err.message || String(err)}`);
    }
  }, [projectPath, setSkillPageCache]);

  const refreshSkillSurface = useCallback(async (options = {}) => {
    if (refreshInFlightRef.current) return;
    refreshInFlightRef.current = true;
    try {
      await refreshSkills(options);
      await refreshResolutions(options);
    } finally {
      refreshInFlightRef.current = false;
    }
  }, [refreshResolutions, refreshSkills]);

  const openCreateEditor = useCallback(() => {
    setEditorForm(emptySkillForm());
    setActiveSkillPath('');
    setSkillFiles([]);
    setSummarySuggestion('');
    setError('');
    setNotice('');
    setEditorOpen(true);
  }, []);

  const openEditSkill = useCallback(async (skill) => {
    if (!skill?.dir) {
      setError('skills/local/read: path is required');
      return;
    }
    setError('');
    setNotice('');
    setSummarySuggestion('');
    setLoading(true);
    const path = `${skill.dir.replace(/[\\/]+$/g, '')}/SKILL.md`;
    try {
      const cwd = normalizeSettingsCwd(projectPath);
      const [rawSkill, rawFiles] = await Promise.all([
        readSkill({ cwd, path }),
        listSkillFiles({ cwd, dir: skill.dir }),
      ]);
      const content = (rawSkill?.skill?.content || '').toString();
      const parsed = parseSkillMarkdown(content, skill.name);
      setEditorForm({
        name: parsed.name || skill.name,
        displayName: parsed.displayName || skill.title || '',
        description: parsed.description || skill.description || '',
        keywords: listToText(parsed.triggerWords.length > 0 ? parsed.triggerWords : skill.tags),
        body: parsed.body,
        scope: skill.scope,
        personalType: skill.personalType,
      });
      setActiveSkillPath(path);
      setSkillFiles(normalizeSkillFileList(rawFiles));
      setEditorOpen(true);
    } catch (err) {
      setError(`读取技能失败：${err.message || String(err)}`);
    } finally {
      setLoading(false);
    }
  }, [projectPath]);

  const openSkillFile = useCallback(async (file) => {
    const path = (file?.path || '').toString().trim();
    if (!path) return;
    setError('');
    try {
      const cwd = normalizeSettingsCwd(projectPath);
      const raw = await readSkill({ cwd, path });
      const content = (raw?.skill?.content || '').toString();
      if (isMainSkillFile(path)) {
        const parsed = parseSkillMarkdown(content, editorForm.name);
        setEditorForm((form) => ({
          ...form,
          name: parsed.name || form.name,
          displayName: parsed.displayName || parsed.name || form.displayName,
          description: parsed.description,
          keywords: listToText(parsed.triggerWords),
          body: parsed.body,
        }));
      } else {
        setEditorForm((form) => ({ ...form, body: content }));
      }
      setActiveSkillPath(path);
    } catch (err) {
      setError(`读取子文件失败：${err.message || String(err)}`);
    }
  }, [editorForm.name, projectPath]);

  const suggestSummary = useCallback(async () => {
    setSummarySuggesting(true);
    setError('');
    setSummarySuggestion('');
    try {
      const cwd = normalizeSettingsCwd(projectPath);
      const description = await suggestSkillSummary({
        cwd,
        name: editorForm.displayName || editorForm.name,
        description: editorForm.description,
        content: editorForm.body,
        scenario_words: wordListFromText(editorForm.keywords),
        scope: editorForm.scope,
      });
      setSummarySuggestion(normalizeSummarySuggestion(description));
    } catch (err) {
      setError(`生成简介失败：${err.message || String(err)}`);
    } finally {
      setSummarySuggesting(false);
    }
  }, [editorForm, projectPath]);

  const saveEditor = useCallback(async () => {
    setSaving(true);
    setError('');
    setNotice('');
    try {
      const cwd = normalizeSettingsCwd(projectPath);
      const isMain = !activeSkillPath || isMainSkillFile(activeSkillPath);
      const displayName = editorForm.displayName.trim();
      const name = editorForm.name.trim() || skillNameFromDisplayName(displayName);
      if (isMain && !displayName) throw new Error('请先填写技能名称');
      if (isMain && !name) throw new Error('技能名称必须包含中文、英文或数字');
      const normalizedForm = isMain ? { ...editorForm, name, displayName } : editorForm;
      const payload = {
        cwd,
        path: isMain ? (activeSkillPath || name) : activeSkillPath,
        content: isMain ? buildSkillMarkdown(normalizedForm) : editorForm.body,
        scope: editorForm.scope,
        personal_type: editorForm.scope === 'personal' ? (editorForm.personalType || 'user') : '',
      };
      await writeSkill(payload);
      setEditorOpen(false);
      await refreshSkillSurface();
      setNotice('已保存');
    } catch (err) {
      setError(`保存失败：${err.message || String(err)}`);
    } finally {
      setSaving(false);
    }
  }, [activeSkillPath, editorForm, projectPath, refreshSkillSurface]);

  const onDeleteSkill = useCallback((skill) => {
    setDeleteTarget(skill);
  }, []);

  const confirmDeleteSkill = useCallback(async () => {
    const skill = deleteTarget;
    const skillName = (skill?.name || '').toString().trim();
    if (!skillName) {
      setError('skills/local/delete: name is required');
      return;
    }

    setDeleting(true);
    setError('');
    setNotice('');
    try {
      const cwd = normalizeSettingsCwd(projectPath);
      await deleteSkill({
        cwd,
        name: skillName,
        scope: skill.scope,
        personal_type: skill.personalType,
      });
      setDeleteTarget(null);
      await refreshSkillSurface();
      setNotice(`已删除 ${skill.title}`);
    } catch (err) {
      setError(err.message || String(err));
    } finally {
      setDeleting(false);
    }
  }, [deleteTarget, projectPath, refreshSkillSurface]);

  const confirmImportScope = useCallback(async (scope) => {
    setImporting(true);
    setError('');
    setNotice('');
    try {
      const cwd = normalizeSettingsCwd(projectPath);
      const paths = await selectProjectDirs();
      setImportScopeOpen(false);
      if (!Array.isArray(paths) || paths.length === 0) {
        setNotice('未选择目录');
        return;
      }
      await importSkillDirectories({
        cwd,
        paths,
        scope,
        personal_type: scope === 'personal' ? 'imported' : '',
      });
      await refreshSkillSurface();
      setNotice('导入完成');
    } catch (err) {
      setError(`导入目录失败：${err.message || String(err)}`);
    } finally {
      setImporting(false);
    }
  }, [projectPath, refreshSkillSurface]);

  const runResolutionAction = useCallback(async (conflict, actionOrEntry, entry = null, newName = '') => {
    const conflictID = (conflict?.conflict_id || conflict?.conflictId || '').toString().trim();
    const actionEntry = typeof actionOrEntry === 'string' ? { action: actionOrEntry } : actionOrEntry || {};
    const action = (actionEntry.action || '').toString().trim();
    if (!conflictID || !action) return false;
    if (resolutionActionUnsupported(action)) {
      setNotice(`暂不支持该技能冲突操作：${resolutionActionLabel(action)}`);
      return false;
    }
    const providerEntry = resolutionActionEntryTarget(actionEntry, entry || resolutionProviderEntries(conflict)[0] || {});
    const applyKey = resolutionApplyKey(conflict, action, providerEntry);
    const trimmedNewName = (newName || '').toString().trim();
    if (requiresResolutionNewName(action) && !trimmedNewName) {
      setResolutionPreview(null);
      setResolutionNamePrompt({
        conflict,
        action,
        entry: providerEntry,
        applyKey,
        autoApply: resolutionActionAutoAppliesForConflict(action, conflict),
      });
      setResolutionNameInput(defaultResolutionNewName(conflict, action));
      setNotice('请输入新技能名称后继续。');
      return false;
    }
    const payload = {
      cwd: normalizeSettingsCwd(projectPath),
      conflict_id: conflictID,
      action,
      name: conflict?.name || conflict?.skill_name || '',
      scope: conflict?.scope || '',
      personal_type: conflict?.personal_type || conflict?.personalType || '',
      provider: providerEntry?.provider || conflict?.provider || '',
      source_provider: providerEntry?.provider || conflict?.source_provider || conflict?.provider || '',
      source_path_id: providerEntry?.source_path_id || providerEntry?.sourcePathId || conflict?.source_path_id || '',
      ...resolutionSameNamePayloadFields(conflict, action, actionEntry),
    };
    if (trimmedNewName) payload.new_name = trimmedNewName;
    setResolutionActioning(applyKey);
    setError('');
    try {
      const preview = await previewSkillResolution(payload);
      const items = Array.isArray(preview?.items) ? preview.items : [];
      if (resolutionActionAutoAppliesForConflict(action, conflict)) {
        const proof = items[0];
        if (!proof?.preview_id || !proof?.preview_hash) throw new Error('缺少处理预览凭据');
        await applySkillResolution({
          ...payload,
          provider: proof.provider || payload.provider,
          source_provider: proof.source_provider || payload.source_provider,
          source_path_id: proof.source_path_id || payload.source_path_id,
          preview_id: proof.preview_id,
          preview_hash: proof.preview_hash,
        });
        setResolutionPreview(null);
        setResolutionNamePrompt(null);
        setResolutionNameInput('');
        await refreshSkillSurface();
        setNotice('已处理技能冲突');
        return true;
      }
      setResolutionPreview({
        ...preview,
        action,
        payload,
        items,
        requiresApply: resolutionRequiresApply(action),
      });
      setNotice(isResolutionViewAction(action) ? '已生成处理预览' : '已生成处理预览，请确认应用。');
      return true;
    } catch (err) {
      setError(`处理技能冲突失败：${err.message || String(err)}`);
      return false;
    } finally {
      setResolutionActioning('');
    }
  }, [projectPath, refreshSkillSurface]);

  const confirmResolutionNewName = useCallback(async () => {
    const prompt = resolutionNamePrompt;
    if (!prompt) return;
    const newName = resolutionNameInput.trim();
    if (!newName) {
      setError('请输入新技能名称。');
      return;
    }
    const ok = await runResolutionAction(prompt.conflict, prompt.action, prompt.entry, newName);
    if (ok) {
      setResolutionNamePrompt(null);
      setResolutionNameInput('');
    }
  }, [resolutionNameInput, resolutionNamePrompt, runResolutionAction]);

  const confirmResolutionPreview = useCallback(async () => {
    const preview = resolutionPreview;
    const proof = Array.isArray(preview?.items) ? preview.items[0] : null;
    if (!preview?.requiresApply || !proof?.preview_id || !proof?.preview_hash) return;
    setResolutionActioning('confirm');
    try {
      await applySkillResolution({
        ...preview.payload,
        provider: proof.provider || preview.payload.provider,
        source_provider: proof.source_provider || preview.payload.source_provider,
        source_path_id: proof.source_path_id || preview.payload.source_path_id,
        preview_id: proof.preview_id,
        preview_hash: proof.preview_hash,
      });
      setResolutionPreview(null);
      setResolutionNamePrompt(null);
      setResolutionNameInput('');
      await refreshSkillSurface();
      setNotice('已处理技能冲突');
    } catch (err) {
      setError(`应用技能冲突处理失败：${err.message || String(err)}`);
    } finally {
      setResolutionActioning('');
    }
  }, [refreshSkillSurface, resolutionPreview]);

  useEffect(() => {
    const cached = useClientStore.getState().skillPageCacheByCwd?.[skillCacheKey];
    hasLoadedSkillsRef.current = Boolean(cached?.hasLoadedSkills);
    setItems(Array.isArray(cached?.items) ? cached.items : []);
    setResolutionConflicts(Array.isArray(cached?.resolutionConflicts) ? cached.resolutionConflicts : []);
    setLoading(false);
    setError('');
    setNotice('');
  }, [skillCacheKey]);

  useEffect(() => {
    if (!cachedSkillPage) return;
    if (Array.isArray(cachedSkillPage.items)) setItems(cachedSkillPage.items);
    if (Array.isArray(cachedSkillPage.resolutionConflicts)) setResolutionConflicts(cachedSkillPage.resolutionConflicts);
    if (cachedSkillPage.hasLoadedSkills) hasLoadedSkillsRef.current = true;
  }, [cachedSkillPage]);

  useEffect(() => {
    let cancelled = false;
    const cached = useClientStore.getState().skillPageCacheByCwd?.[skillCacheKey];
    refreshSkillSurface({ isCancelled: () => cancelled, silent: Boolean(cached?.hasLoadedSkills) });
    return () => {
      cancelled = true;
    };
  }, [refreshSkillSurface, skillCacheKey]);

  useEffect(() => {
    if (skillRefreshKey <= 0) return undefined;
    let cancelled = false;
    refreshSkillSurface({ isCancelled: () => cancelled, silent: true });
    return () => {
      cancelled = true;
    };
  }, [refreshSkillSurface, skillRefreshKey]);

  useEffect(() => {
    const runAutoRefresh = () => {
      if (typeof document !== 'undefined' && document.visibilityState === 'hidden') return;
      void refreshSkillSurface({ silent: true });
    };
    const handleVisibilityChange = () => {
      if (typeof document === 'undefined' || document.visibilityState === 'visible') {
        runAutoRefresh();
      }
    };
    const intervalID = window.setInterval(runAutoRefresh, SKILLS_AUTO_REFRESH_MS);
    window.addEventListener('focus', runAutoRefresh);
    document.addEventListener('visibilitychange', handleVisibilityChange);
    return () => {
      window.clearInterval(intervalID);
      window.removeEventListener('focus', runAutoRefresh);
      document.removeEventListener('visibilitychange', handleVisibilityChange);
    };
  }, [refreshSkillSurface]);

  const counts = useMemo(() => items.reduce((acc, item) => {
    acc.all += 1;
    if (item.scope === 'personal') acc.personal += 1;
    else acc.project += 1;
    return acc;
  }, { all: 0, personal: 0, project: 0 }), [items]);

  const filteredItems = useMemo(() => {
    const keyword = query.trim().toLowerCase();
    return items.filter((item) => {
      if (scopeFilter !== 'all' && item.scope !== scopeFilter) return false;
      if (!keyword) return true;
      return [
        item.name,
        item.title,
        item.description,
        item.summary,
        item.dir,
        ...item.tags,
      ].join(' ').toLowerCase().includes(keyword);
    });
  }, [items, query, scopeFilter]);

  const scopeOptions = [
    ['personal', `私人使用 ${counts.personal}`],
    ['project', `项目共享 ${counts.project}`],
    ['all', `全部 ${counts.all}`],
  ];

  return (
    <section className="console-page">
      <PageHeader
        icon={Sparkles}
        title="技能管理"
      />
      <div className="subhead">技能列表</div>
      <div className="skills-toolbar">
        <button type="button" onClick={() => setImportScopeOpen(true)} disabled={importing}>批量导入技能目录</button>
        <button type="button" className="ghost" onClick={openCreateEditor}>新建技能</button>
        <label><Search size={18} /><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="搜索技能名称、简介、关键词..." /></label>
      </div>
      <div className="skill-filter">
        {scopeOptions.map(([value, label]) => (
          <button
            key={value}
            type="button"
            className={scopeFilter === value ? 'active' : ''}
            onClick={() => setScopeFilter(value)}
          >
            {label}
          </button>
        ))}
      </div>
      {loading ? <p className="console-message">加载技能中...</p> : null}
      {notice ? <p className="settings-status">{notice}</p> : null}
      {error ? <p className="danger-text" role="alert">{error}</p> : null}
      {resolutionConflicts.length > 0 ? (
        <section className="skills-resolution-panel">
          <strong>发现 {resolutionConflicts.length} 个技能冲突，需要处理后再使用。</strong>
          {resolutionConflicts.map((conflict, index) => {
            const conflictID = (conflict.conflict_id || conflict.conflictId || index).toString();
            const providerEntries = resolutionProviderEntries(conflict);
            const actionEntries = resolutionActionEntries(conflict);
            const promptConflictID = (resolutionNamePrompt?.conflict?.conflict_id || resolutionNamePrompt?.conflict?.conflictId || '').toString();
            const promptApplies = resolutionNamePrompt && promptConflictID === (conflict.conflict_id || conflict.conflictId || '').toString();
            return (
              <article key={conflictID} className="skills-resolution-item">
                <header>
                  <h3>{conflict.name || conflict.skill_name || '未命名技能'} · {resolutionKindLabel(conflict.kind)}</h3>
                  <span>{scopeLabel(scopeForSkill(conflict))}</span>
                </header>
                {providerEntries.map((providerEntry, sourceIndex) => (
                  <div className="skills-resolution-actions" key={`${conflictID}:${sourceIndex}:${resolutionProviderEntryLabel(providerEntry)}`}>
                    {providerEntries.length > 1 ? <span className="skills-resolution-source">{resolutionProviderEntryLabel(providerEntry)}</span> : null}
                    {actionEntries.map((actionEntry, actionIndex) => {
                      const action = (actionEntry.action || actionEntry).toString();
                      const targetEntry = resolutionActionEntryTarget(actionEntry, providerEntry);
                      const applyKey = resolutionApplyKey(conflict, action, targetEntry);
                      const label = resolutionActionEntryLabel(actionEntry);
                      const help = resolutionActionEntryHelp(actionEntry);
                      return (
                        <button
                          key={`${applyKey}:${actionIndex}`}
                          type="button"
                          title={help}
                          onClick={() => { void runResolutionAction(conflict, actionEntry, providerEntry); }}
                          disabled={resolutionActioning === applyKey}
                        >
                          {resolutionActioning === applyKey ? '处理中...' : label}
                        </button>
                      );
                    })}
                  </div>
                ))}
                {promptApplies ? (
                  <div className="skills-resolution-name-field">
                    <label>
                      新技能名称
                      <input
                        value={resolutionNameInput}
                        onChange={(event) => setResolutionNameInput(event.target.value)}
                        aria-label="新技能名称"
                      />
                    </label>
                    <button
                      type="button"
                      onClick={() => { void confirmResolutionNewName(); }}
                      disabled={resolutionActioning === resolutionNamePrompt.applyKey}
                    >
                      {resolutionActioning === resolutionNamePrompt.applyKey
                        ? (resolutionNamePrompt.autoApply ? '处理中...' : '生成中...')
                        : (resolutionNamePrompt.autoApply ? '确认处理' : '生成预览')}
                    </button>
                    <button
                      type="button"
                      className="ghost"
                      onClick={() => {
                        setResolutionNamePrompt(null);
                        setResolutionNameInput('');
                      }}
                    >
                      取消
                    </button>
                  </div>
                ) : null}
              </article>
            );
          })}
          {resolutionPreview ? (
            <article className="skills-resolution-preview">
              <header>
                <h3>{resolutionActionLabel(resolutionPreview.action)}</h3>
                {resolutionPreview.requiresApply ? (
                  <button type="button" onClick={() => { void confirmResolutionPreview(); }} disabled={resolutionActioning === 'confirm'}>
                    {resolutionActioning === 'confirm' ? '应用中...' : '确认应用'}
                  </button>
                ) : null}
                <button type="button" className="ghost" onClick={() => setResolutionPreview(null)}>取消</button>
              </header>
              {(resolutionPreview.items || []).map((item, index) => (
                <div key={item.preview_id || index} className="skills-resolution-preview-item">
                  {previewItemPaths(item).map((pathItem) => (
                    <p key={`${pathItem.label}:${pathItem.value}`}><span>{pathItem.label}</span><code>{pathItem.value}</code></p>
                  ))}
                </div>
              ))}
            </article>
          ) : null}
        </section>
      ) : null}
      {!loading && !error && filteredItems.length === 0 ? <p className="console-message">暂无技能</p> : null}
      {!error && filteredItems.length > 0 ? (
        <div className="skill-grid">
          {filteredItems.map((skill) => <SkillCard key={skill.id} skill={skill} onEdit={openEditSkill} onDelete={onDeleteSkill} />)}
        </div>
      ) : null}
      {editorOpen ? (
        <SkillEditorModal
          form={editorForm}
          setForm={setEditorForm}
          activeSkillPath={activeSkillPath}
          files={skillFiles}
          summarySuggestion={summarySuggestion}
          summarySuggesting={summarySuggesting}
          saving={saving}
          onSuggestSummary={suggestSummary}
          onApplySummary={() => {
            setEditorForm((form) => ({ ...form, description: summarySuggestion }));
            setSummarySuggestion('');
          }}
          onOpenFile={openSkillFile}
          onClose={() => setEditorOpen(false)}
          onSave={saveEditor}
        />
      ) : null}
      {deleteTarget ? (
        <ConfirmSkillDeleteModal
          skill={deleteTarget}
          deleting={deleting}
          onClose={() => setDeleteTarget(null)}
          onConfirm={confirmDeleteSkill}
        />
      ) : null}
      {importScopeOpen ? (
        <ImportScopeModal
          importing={importing}
          onClose={() => setImportScopeOpen(false)}
          onConfirm={confirmImportScope}
        />
      ) : null}
    </section>
  );
}

function SkillCard({ skill, onEdit, onDelete }) {
  const tags = skill.tags.slice(0, 4);
  const extraTagCount = skill.tags.length - tags.length;
  const description = skill.description || '暂无描述';
  const summary = skill.summary || description;

  return (
    <article className="skill-card">
      <header><h3>{skill.title}</h3><span>{scopeLabel(skill.scope)}</span></header>
      <p className="path">{skill.dir || '未提供路径'}</p>
      <p>{description}</p>
      <div className="quote">{summary}</div>
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
    <div className="modal-overlay">
      <section className="modal-box skills-editor-modal" role="dialog" aria-modal="true" aria-label={modalTitle}>
        <header className="skills-editor-modal-head">
          <div>
            <h2>{modalTitle}</h2>
            <p>你可以修改简介和技能内容。</p>
          </div>
          <button type="button" className="ghost" onClick={onClose}>关闭</button>
        </header>
        <div className="form-grid">
          <label className="wide">技能名称<input value={form.displayName} onChange={updateDisplayName} disabled={!isMain} /></label>
          <div className="skills-field wide">
            <div className="skills-editor-label-row">
              <label htmlFor="skills-description-input">技能简介</label>
              <button type="button" className="ghost" onClick={() => { void onSuggestSummary(); }} disabled={!isMain || summarySuggesting || (!form.name.trim() && !form.body.trim())}>
                {summarySuggesting ? '生成中' : '帮我生成'}
              </button>
            </div>
            <input id="skills-description-input" value={form.description} onChange={update('description')} disabled={!isMain} aria-label="技能简介" />
            <div className="skills-inline-tip">建议写成“当你需要……时使用”。</div>
          </div>
          <div className="skills-field">
            <span>使用范围</span>
            <div className="skills-scope-segmented" role="group" aria-label="使用范围">
              <button type="button" className={form.scope === 'project' ? 'active' : ''} disabled={!isMain} onClick={() => setForm((current) => ({ ...current, scope: 'project' }))}>项目共享</button>
              <button type="button" className={form.scope === 'personal' ? 'active' : ''} disabled={!isMain} onClick={() => setForm((current) => ({ ...current, scope: 'personal' }))}>私人使用</button>
            </div>
          </div>
          <label>关键词<input value={form.keywords} onChange={update('keywords')} disabled={!isMain} aria-label="关键词" /></label>
        </div>
        {summarySuggestion ? (
          <div className="skills-editor-actions">
          {summarySuggestion ? <span>建议：{summarySuggestion}</span> : null}
          {summarySuggestion ? <button type="button" onClick={onApplySummary}>采用</button> : null}
          </div>
        ) : null}
        {files.some((file) => !file.isMain) ? (
          <div className="skills-subfiles-wrap">
            <span>附加内容</span>
            <div className="skills-subfiles">
            {files.map((file) => (
              <button
                key={file.path}
                type="button"
                className={file.path === activeSkillPath ? 'active' : ''}
                onClick={() => { void onOpenFile(file); }}
              >
                {file.name}{file.isMain ? ' · 主要文件' : ''}
              </button>
            ))}
            </div>
            <div className="skills-inline-tip">这里是这个技能附带的示例、模板或脚本。</div>
          </div>
        ) : null}
        <div className="skills-body-field">
          <div className="skills-body-head">
            <span>{isMain ? '技能内容' : '关联文件内容'}</span>
            {bodyEditing ? (
              <button type="button" className="ghost" onClick={() => setBodyEditing(false)}>预览正文</button>
            ) : (
              <button type="button" onClick={() => setBodyEditing(true)}>编辑正文</button>
            )}
          </div>
          {bodyEditing ? (
            <textarea value={form.body} onChange={update('body')} aria-label={isMain ? '技能内容' : '关联文件内容'} />
          ) : (
            <div className="skills-body-preview" data-testid="skills-editor-body-preview">
              <SkillMarkdownPreview content={form.body} />
            </div>
          )}
          <div className="skills-inline-tip">点击“编辑正文”展开编辑；切回“预览正文”查看效果。</div>
        </div>
        <footer>
          <button type="button" className="ghost" onClick={onClose}>取消</button>
          <button type="button" onClick={() => { void onSave(); }} disabled={saving}>{saving ? '保存中...' : '保存技能'}</button>
        </footer>
      </section>
    </div>
  );
}

function ConfirmSkillDeleteModal({ skill, deleting, onClose, onConfirm }) {
  return (
    <div className="modal-overlay">
      <section className="modal-box" role="dialog" aria-modal="true" aria-label="删除技能">
        <header><h2>删除技能</h2><button type="button" className="ghost" onClick={onClose} disabled={deleting}>关闭</button></header>
        <p>确定删除技能 “{skill.name}” 吗？该操作会删除技能目录及其资源文件，无法恢复。</p>
        <p className="path">{skill.dir || '-'}</p>
        <footer>
          <button type="button" className="ghost" onClick={onClose} disabled={deleting}>取消</button>
          <button type="button" className="text-danger" onClick={() => { void onConfirm(); }} disabled={deleting}>{deleting ? '删除中...' : '确认删除'}</button>
        </footer>
      </section>
    </div>
  );
}

function ImportScopeModal({ importing, onClose, onConfirm }) {
  return (
    <div className="modal-overlay">
      <section className="modal-box" role="dialog" aria-modal="true" aria-label="导入技能">
        <header><h2>导入技能</h2><button type="button" className="ghost" onClick={onClose} disabled={importing}>关闭</button></header>
        <p>这些技能导入后给谁使用？</p>
        <footer>
          <button type="button" className="ghost" onClick={onClose} disabled={importing}>取消</button>
          <button type="button" onClick={() => { void onConfirm('personal'); }} disabled={importing}>私人使用</button>
          <button type="button" onClick={() => { void onConfirm('project'); }} disabled={importing}>项目共享</button>
        </footer>
      </section>
    </div>
  );
}

function FilesPage({ projectPath, store }) {
  const sharedFilesCacheKey = normalizeSettingsCwd(projectPath);
  const cachedSharedFilesPage = useClientStore((state) => state.sharedFilesPageCacheByCwd?.[sharedFilesCacheKey]);
  const setSharedFilesPageCache = useClientStore((state) => state.setSharedFilesPageCache);
  const [files, setFiles] = useState(() => (Array.isArray(cachedSharedFilesPage?.files) ? cachedSharedFilesPage.files : []));
  const [finalOutputRefs, setFinalOutputRefs] = useState(() => (
    Array.isArray(cachedSharedFilesPage?.finalOutputRefs) ? cachedSharedFilesPage.finalOutputRefs : []
  ));
  const [retention, setRetention] = useState(() => (
    cachedSharedFilesPage?.retention || { items: [], protectedCount: 0, cleanupCandidateCount: 0 }
  ));
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState(null);
  const [searchText, setSearchText] = useState('');
  const [sortMode, setSortMode] = useState('updated-desc');
  const [category, setCategory] = useState('all');
  const [selectedFile, setSelectedFile] = useState(null);
  const [deleteTarget, setDeleteTarget] = useState(null);
  const [busyPath, setBusyPath] = useState('');
  const [exportingPath, setExportingPath] = useState('');
  const [deletingPath, setDeletingPath] = useState('');
  const [copied, setCopied] = useState(false);
  const hasLoadedFilesRef = useRef(Boolean(cachedSharedFilesPage?.hasLoadedFiles));
  const refreshInFlightRef = useRef(false);
  const sharedFilesRevision = Number(store?.sharedFilesRevision || 0);

  const finalOutputByPath = useMemo(() => (
    new Map(finalOutputRefs.filter((ref) => files.some((file) => file.path === ref.path)).map((ref) => [ref.path, ref]))
  ), [files, finalOutputRefs]);
  const retentionByPath = useMemo(() => (
    new Map(retention.items.map((item) => [item.path, item]))
  ), [retention.items]);
  const finalCount = files.filter((file) => finalOutputByPath.has(file.path)).length;
  const workCount = Math.max(0, files.length - finalCount);
  const activeSortLabel = SHARED_FILE_SORTS.find((item) => item.key === sortMode)?.label || '最新更新';
  const categoryCountsByKey = useMemo(() => ({
    all: files.length,
    final: finalCount,
    work: workCount,
  }), [files.length, finalCount, workCount]);

  const refreshFiles = useCallback(async ({ silent = false } = {}) => {
    if (refreshInFlightRef.current) return;
    refreshInFlightRef.current = true;
    const cached = useClientStore.getState().sharedFilesPageCacheByCwd?.[sharedFilesCacheKey];
    const hasUsableSnapshot = hasLoadedFilesRef.current || Boolean(cached?.hasLoadedFiles);
    const showBlockingLoading = !silent && !hasUsableSnapshot;
    if (showBlockingLoading) {
      setLoading(true);
      setError('');
    } else if (!silent) {
      setError('');
    }
    try {
      const cwd = normalizeSettingsCwd(projectPath);
      const response = await getDashboardPage({ cwd, page: 'memory' });
      const normalized = normalizeSharedFilesResponse(response);
      hasLoadedFilesRef.current = true;
      setFiles(normalized.files);
      setFinalOutputRefs(normalized.finalOutputRefs);
      setRetention(normalized.retention);
      setSharedFilesPageCache(cwd, {
        files: normalized.files,
        finalOutputRefs: normalized.finalOutputRefs,
        retention: normalized.retention,
        hasLoadedFiles: true,
      });
      setError('');
    } catch (err) {
      if (showBlockingLoading) {
        setFiles([]);
        setFinalOutputRefs([]);
        setRetention({ items: [], protectedCount: 0, cleanupCandidateCount: 0 });
      }
      if (!silent || !hasUsableSnapshot) setError(`加载共享文件失败：${err.message || String(err)}`);
    } finally {
      refreshInFlightRef.current = false;
      if (showBlockingLoading) setLoading(false);
    }
  }, [projectPath, setSharedFilesPageCache, sharedFilesCacheKey]);

  useEffect(() => {
    const cached = useClientStore.getState().sharedFilesPageCacheByCwd?.[sharedFilesCacheKey];
    hasLoadedFilesRef.current = Boolean(cached?.hasLoadedFiles);
    setFiles(Array.isArray(cached?.files) ? cached.files : []);
    setFinalOutputRefs(Array.isArray(cached?.finalOutputRefs) ? cached.finalOutputRefs : []);
    setRetention(cached?.retention || { items: [], protectedCount: 0, cleanupCandidateCount: 0 });
    setLoading(false);
    setError('');
    setNotice(null);
  }, [sharedFilesCacheKey]);

  useEffect(() => {
    if (!cachedSharedFilesPage) return;
    if (Array.isArray(cachedSharedFilesPage.files)) setFiles(cachedSharedFilesPage.files);
    if (Array.isArray(cachedSharedFilesPage.finalOutputRefs)) setFinalOutputRefs(cachedSharedFilesPage.finalOutputRefs);
    if (cachedSharedFilesPage.retention) setRetention(cachedSharedFilesPage.retention);
    if (cachedSharedFilesPage.hasLoadedFiles) hasLoadedFilesRef.current = true;
  }, [cachedSharedFilesPage]);

  useEffect(() => {
    const cached = useClientStore.getState().sharedFilesPageCacheByCwd?.[sharedFilesCacheKey];
    void refreshFiles({ silent: Boolean(cached?.hasLoadedFiles) });
  }, [refreshFiles, sharedFilesCacheKey]);

  useEffect(() => {
    if (sharedFilesRevision <= 0) return;
    void refreshFiles({ silent: true });
  }, [refreshFiles, sharedFilesRevision]);

  useEffect(() => {
    const runAutoRefresh = () => {
      if (typeof document !== 'undefined' && document.visibilityState === 'hidden') return;
      void refreshFiles({ silent: true });
    };
    const handleVisibilityChange = () => {
      if (typeof document === 'undefined' || document.visibilityState === 'visible') {
        runAutoRefresh();
      }
    };
    const intervalID = window.setInterval(runAutoRefresh, SHARED_FILES_AUTO_REFRESH_MS);
    window.addEventListener('focus', runAutoRefresh);
    document.addEventListener('visibilitychange', handleVisibilityChange);
    return () => {
      window.clearInterval(intervalID);
      window.removeEventListener('focus', runAutoRefresh);
      document.removeEventListener('visibilitychange', handleVisibilityChange);
    };
  }, [refreshFiles]);

  const visibleFiles = useMemo(() => {
    const filtered = sortSharedFiles(files.filter((file) => sharedFileMatches(file, searchText)), sortMode);
    if (category === 'final') return filtered.filter((file) => sharedFileCategoryOf(file, finalOutputByPath) === 'final');
    if (category === 'work') return filtered.filter((file) => sharedFileCategoryOf(file, finalOutputByPath) === 'work');
    return filtered;
  }, [category, files, finalOutputByPath, searchText, sortMode]);

  const protectionFor = useCallback((file) => {
    const retentionItem = retentionByPath.get(file.path);
    if (retentionItem?.protected) return retentionItem;
    const ref = finalOutputByPath.get(file.path);
    if (ref) return { path: file.path, protected: true, reason: 'final_output', finalOutput: ref };
    return null;
  }, [finalOutputByPath, retentionByPath]);

  const loadFileDetail = useCallback(async (file) => {
    const path = textValue(file?.path);
    if (!path) throw new Error('shared file path is required');
    setBusyPath(path);
    try {
      const detail = await readSharedFile({ path });
      return normalizeSharedFile({
        path: detail?.path || path,
        content: detail?.content || file?.content || '',
        updatedBy: detail?.updatedBy || file?.updatedBy || '',
        updatedAt: detail?.updatedAt || file?.updatedAt || '',
      }, 0);
    } finally {
      setBusyPath('');
    }
  }, []);

  const openFile = useCallback(async (file) => {
    setNotice(null);
    setCopied(false);
    try {
      setSelectedFile(await loadFileDetail(file));
    } catch (err) {
      setNotice({ level: 'error', message: `读取文件失败：${err.message || String(err)}` });
    }
  }, [loadFileDetail]);

  const exportFile = useCallback(async (file) => {
    const path = textValue(file?.path);
    if (!path || exportingPath) return;
    setNotice(null);
    setExportingPath(path);
    try {
      const detail = await loadFileDetail(file);
      const savedPath = await saveTextFile({
        defaultPath: normalizeSettingsCwd(projectPath),
        defaultFilename: sharedFileExportName(detail.path),
        content: detail.content,
      });
      setNotice({ level: 'info', message: savedPath ? `已保存到：${savedPath}` : '已取消保存。' });
    } catch (err) {
      setNotice({ level: 'error', message: `导出失败：${err.message || String(err)}` });
    } finally {
      setExportingPath('');
    }
  }, [exportingPath, loadFileDetail, projectPath]);

  const askDelete = useCallback((file) => {
    const protection = protectionFor(file);
    if (protection) {
      setNotice({ level: 'error', message: `最终产物不能直接删除：${file.path}` });
      return;
    }
    setDeleteTarget(file);
  }, [protectionFor]);

  const confirmDelete = useCallback(async () => {
    const target = deleteTarget;
    if (!target?.path || deletingPath) return;
    setNotice(null);
    setDeletingPath(target.path);
    try {
      await deleteSharedFile({ path: target.path });
      if (selectedFile?.path === target.path) setSelectedFile(null);
      setDeleteTarget(null);
      setNotice({ level: 'info', message: `已删除文件：${target.path}` });
      await refreshFiles();
    } catch (err) {
      setNotice({ level: 'error', message: `删除失败：${err.message || String(err)}` });
    } finally {
      setDeletingPath('');
    }
  }, [deleteTarget, deletingPath, refreshFiles, selectedFile]);

  const continueWithFile = useCallback((file) => {
    if (typeof store?.continueWithSharedFile === 'function') {
      store.continueWithSharedFile(file.path);
    }
  }, [store]);

  const copySelectedContent = useCallback(async () => {
    const text = selectedFile?.content || '';
    if (!text) return;
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
    } catch (err) {
      setNotice({ level: 'error', message: `复制失败：${err.message || String(err)}` });
    }
  }, [selectedFile]);

  return (
    <section className="console-page shared-files-page">
      <PageHeader
        icon={FolderOpen}
        title="文件产物"
        subtitle={`${activeSortLabel} · 全部${files.length} 最终产物${finalCount} 工作文件${workCount}`}
        actions={(
          <>
            <label className="shared-files-search">
              <Search size={15} />
              <input
                aria-label="搜索共享文件"
                placeholder="搜索文件名 / 内容"
                value={searchText}
                onChange={(event) => setSearchText(event.target.value)}
              />
            </label>
            <select aria-label="共享文件排序" value={sortMode} onChange={(event) => setSortMode(event.target.value)}>
              {SHARED_FILE_SORTS.map((item) => <option key={item.key} value={item.key}>{item.label}</option>)}
            </select>
          </>
        )}
      />
      <div className="file-intro">
        <FolderOpen size={29} />
        <h2>共享文件 · Agent 协作中转站</h2>
        <p>Agent 在运行过程中产生的所有数据产物都保存在这里。</p>
      </div>
      <div className="shared-files-tabs" role="tablist" aria-label="文件产物分类">
        {SHARED_FILE_CATEGORIES.map((item) => {
          const count = categoryCountsByKey[item.key] || 0;
          return (
            <button
              key={item.key}
              type="button"
              role="tab"
              aria-selected={category === item.key}
              className={category === item.key ? 'active' : ''}
              onClick={() => setCategory(item.key)}
            >
              {item.label} {count}
            </button>
          );
        })}
      </div>
      {notice ? <p className={notice.level === 'error' ? 'danger-text' : 'settings-status'}>{notice.message}</p> : null}
      {error ? <p className="danger-text">{error}</p> : null}
      {!error && loading && files.length === 0 ? <p className="console-message">正在加载共享文件...</p> : null}
      {!error && !loading && files.length === 0 ? (
        <div className="empty-state">
          <span><File size={24} /></span>
          <h2>还没有文件产物</h2>
          <p>Agent 生成报告、草稿或数据文件后，会显示在这里。</p>
        </div>
      ) : null}
      {!error && files.length > 0 && visibleFiles.length === 0 ? (
        <div className="empty-state">
          <span><Search size={24} /></span>
          <h2>没有匹配的文件</h2>
          <p>清空搜索或切换分类后再试。</p>
        </div>
      ) : null}
      {!error && visibleFiles.length > 0 ? (
        <div className="file-list" data-testid="shared-files-list">
          {visibleFiles.map((file) => (
            <SharedFileRow
              key={file.path}
              file={file}
              finalOutputRef={finalOutputByPath.get(file.path)}
              protectedFile={Boolean(protectionFor(file))}
              busy={busyPath === file.path}
              exporting={exportingPath === file.path}
              deleting={deletingPath === file.path}
              onOpen={openFile}
              onExport={exportFile}
              onDelete={askDelete}
              onContinue={continueWithFile}
            />
          ))}
        </div>
      ) : null}
      {selectedFile ? (
        <SharedFileViewer
          file={selectedFile}
          copied={copied}
          exporting={exportingPath === selectedFile.path}
          onClose={() => setSelectedFile(null)}
          onCopy={copySelectedContent}
          onExport={() => { void exportFile(selectedFile); }}
        />
      ) : null}
      {deleteTarget ? (
        <ConfirmSharedFileDeleteModal
          file={deleteTarget}
          deleting={deletingPath === deleteTarget.path}
          onClose={() => setDeleteTarget(null)}
          onConfirm={confirmDelete}
        />
      ) : null}
    </section>
  );
}

function SharedFileRow({
  file,
  finalOutputRef,
  protectedFile,
  busy,
  exporting,
  deleting,
  onOpen,
  onExport,
  onDelete,
  onContinue,
}) {
  const path = splitSharedFilePath(file.path);
  const role = finalOutputRef ? '最终产物' : '工作文件';
  return (
    <article className={`file-row${finalOutputRef ? ' is-final-output' : ''}`}>
      <header>
        <h3>{path.base}</h3>
        <span>{role}</span>
      </header>
      <p>{role} {sharedFileTimestamp(file.updatedAt)} {formatBytes(sharedFileContent(file).length)}</p>
      <code>{file.path}</code>
      {finalOutputRef ? (
        <small>Run {finalOutputRef.runKey || '-'} · DAG {finalOutputRef.dagKey || '-'} · Node {finalOutputRef.sourceNodeKey || '-'}</small>
      ) : null}
      <pre>{sharedFileSummary(file)}</pre>
      <footer>
        <button type="button" onClick={() => { void onOpen(file); }} disabled={busy}>
          <Eye size={14} /> {busy ? '加载中...' : '打开'}
        </button>
        <button type="button" onClick={() => { void onExport(file); }} disabled={busy || exporting}>
          <Download size={14} /> {exporting ? '导出中...' : '导出'}
        </button>
        <button
          type="button"
          className={protectedFile ? 'ghost' : 'text-danger'}
          onClick={() => onDelete(file)}
          disabled={protectedFile || deleting}
          title={protectedFile ? '最终产物由任务结果引用，不能直接删除。' : ''}
        >
          <Trash2 size={14} /> {protectedFile ? '不可删除' : deleting ? '删除中...' : '删除'}
        </button>
        <button type="button" className="ghost" onClick={() => onContinue(file)}>
          <MessageCircle size={14} /> 用此文件继续对话
        </button>
      </footer>
    </article>
  );
}

function SharedFileViewer({ file, copied, exporting, onClose, onCopy, onExport }) {
  return (
    <div className="modal-overlay">
      <section className="modal-box shared-file-viewer-modal" role="dialog" aria-modal="true" aria-label="文件预览">
        <header>
          <div>
            <h2>文件预览</h2>
            <p className="path">{file.path}</p>
          </div>
          <div className="modal-actions">
            <button type="button" onClick={onExport} disabled={exporting}><Download size={14} /> {exporting ? '导出中...' : '导出'}</button>
            <button type="button" onClick={() => { void onCopy(); }} disabled={!file.content}><Copy size={14} /> {copied ? '已复制' : '复制内容'}</button>
            <button type="button" className="ghost" onClick={onClose}><X size={14} /> 关闭</button>
          </div>
        </header>
        <dl className="shared-file-viewer-meta">
          <dt>来源</dt><dd>{file.updatedBy || '-'}</dd>
          <dt>更新时间</dt><dd>{sharedFileTimestamp(file.updatedAt)}</dd>
        </dl>
        <pre className="shared-file-content-preview">{sharedFilePreview(file)}</pre>
      </section>
    </div>
  );
}

function ConfirmSharedFileDeleteModal({ file, deleting, onClose, onConfirm }) {
  return (
    <div className="modal-overlay">
      <section className="modal-box" role="dialog" aria-modal="true" aria-label="删除文件">
        <header>
          <h2>删除文件</h2>
          <button type="button" className="ghost" onClick={onClose} disabled={deleting}>关闭</button>
        </header>
        <p>文件删除后无法恢复。删除前请确认这份内容不再需要。</p>
        <p className="path">{file.path}</p>
        <footer>
          <button type="button" className="ghost" onClick={onClose} disabled={deleting}>取消</button>
          <button type="button" className="text-danger" onClick={() => { void onConfirm(); }} disabled={deleting}>
            {deleting ? '删除中...' : '确认删除'}
          </button>
        </footer>
      </section>
    </div>
  );
}

function MemoryPage({ projectPath, onSimilarCountChange, refreshKey = 0 }) {
  const memoryCacheKey = normalizeSettingsCwd(projectPath);
  const cachedMemoryPage = useClientStore((state) => state.memoryPageCacheByCwd?.[memoryCacheKey]);
  const setMemoryPageCache = useClientStore((state) => state.setMemoryPageCache);
  const [snapshot, setSnapshot] = useState(() => (
    cachedMemoryPage?.snapshot || { overview: {}, entries: [] }
  ));
  const [loading, setLoading] = useState(() => !cachedMemoryPage?.hasLoadedMemory);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState({ level: 'info', message: '' });
  const [searchText, setSearchText] = useState('');
  const [activeCategory, setActiveCategory] = useState('preference');
  const [createMenuOpen, setCreateMenuOpen] = useState(false);
  const [editor, setEditor] = useState({ open: false, mode: 'create', form: defaultMemoryForm('project') });
  const [deleteTarget, setDeleteTarget] = useState(null);
  const [mergeTarget, setMergeTarget] = useState(null);
  const [similarExpanded, setSimilarExpanded] = useState(false);
  const [busyKey, setBusyKey] = useState('');
  const [saving, setSaving] = useState(false);
  const [deletingKey, setDeletingKey] = useState('');
  const [autoToggling, setAutoToggling] = useState(false);
  const [mergingAll, setMergingAll] = useState(false);
  const [ignoringKey, setIgnoringKey] = useState('');
  const [mergingKey, setMergingKey] = useState('');
  const hasLoadedMemoryRef = useRef(Boolean(cachedMemoryPage?.hasLoadedMemory));
  const refreshInFlightRef = useRef(false);
  const memoryRefreshKey = Number(refreshKey || 0);

  const refreshMemory = useCallback(async ({ silent = false } = {}) => {
    if (refreshInFlightRef.current) return;
    refreshInFlightRef.current = true;
    const cached = useClientStore.getState().memoryPageCacheByCwd?.[memoryCacheKey];
    const hasUsableSnapshot = hasLoadedMemoryRef.current || Boolean(cached?.hasLoadedMemory);
    const showBlockingLoading = !silent && !hasUsableSnapshot;
    if (showBlockingLoading) {
      setLoading(true);
      setError('');
    } else if (!silent) {
      setError('');
    }
    try {
      const cwd = normalizeSettingsCwd(projectPath);
      const response = await getMemorySnapshot({ cwd });
      const nextSnapshot = normalizeMemorySnapshot(response);
      hasLoadedMemoryRef.current = true;
      setSnapshot(nextSnapshot);
      setMemoryPageCache(cwd, { snapshot: nextSnapshot, hasLoadedMemory: true });
      setError('');
    } catch (err) {
      if (!silent || !hasUsableSnapshot) setError(errorMessage(err));
    } finally {
      refreshInFlightRef.current = false;
      if (showBlockingLoading) setLoading(false);
    }
  }, [memoryCacheKey, projectPath, setMemoryPageCache]);

  useEffect(() => {
    const cached = useClientStore.getState().memoryPageCacheByCwd?.[memoryCacheKey];
    hasLoadedMemoryRef.current = Boolean(cached?.hasLoadedMemory);
    setSnapshot(cached?.snapshot || { overview: {}, entries: [] });
    setLoading(!cached?.hasLoadedMemory);
    setError('');
    setNotice({ level: 'info', message: '' });
  }, [memoryCacheKey]);

  useEffect(() => {
    if (!cachedMemoryPage) return;
    if (cachedMemoryPage.snapshot) setSnapshot(cachedMemoryPage.snapshot);
    if (cachedMemoryPage.hasLoadedMemory) {
      hasLoadedMemoryRef.current = true;
      setLoading(false);
    }
  }, [cachedMemoryPage]);

  useEffect(() => {
    const cached = useClientStore.getState().memoryPageCacheByCwd?.[memoryCacheKey];
    refreshMemory({ silent: Boolean(cached?.hasLoadedMemory) }).catch(() => { });
  }, [memoryCacheKey, refreshMemory]);

  useEffect(() => {
    if (memoryRefreshKey <= 0) return;
    void refreshMemory({ silent: true });
  }, [memoryRefreshKey, refreshMemory]);

  useEffect(() => {
    const runAutoRefresh = () => {
      if (typeof document !== 'undefined' && document.visibilityState === 'hidden') return;
      void refreshMemory({ silent: true });
    };
    const handleVisibilityChange = () => {
      if (typeof document === 'undefined' || document.visibilityState === 'visible') {
        runAutoRefresh();
      }
    };
    const intervalID = window.setInterval(runAutoRefresh, MEMORY_AUTO_REFRESH_MS);
    window.addEventListener('focus', runAutoRefresh);
    document.addEventListener('visibilitychange', handleVisibilityChange);
    return () => {
      window.clearInterval(intervalID);
      window.removeEventListener('focus', runAutoRefresh);
      document.removeEventListener('visibilitychange', handleVisibilityChange);
    };
  }, [refreshMemory]);

  const showNotice = useCallback((level, message) => {
    setNotice({ level: level || 'info', message: memoryNoticeText(message) });
  }, []);

  const preferenceEntries = useMemo(
    () => snapshot.entries.filter((entry) => entry.category === 'preference'),
    [snapshot.entries],
  );
  const projectEntries = useMemo(
    () => snapshot.entries.filter((entry) => entry.category === 'project'),
    [snapshot.entries],
  );
  const entriesByCategory = useMemo(() => ({
    preference: preferenceEntries,
    project: projectEntries,
    all: snapshot.entries,
  }), [preferenceEntries, projectEntries, snapshot.entries]);
  const categoryCounts = useMemo(() => ({
    preference: preferenceEntries.length,
    project: projectEntries.length,
    all: snapshot.entries.length,
  }), [preferenceEntries.length, projectEntries.length, snapshot.entries.length]);
  const visibleEntries = useMemo(() => (
    sortMemoryEntries((entriesByCategory[activeCategory] || []).filter((entry) => memoryMatches(entry, searchText)))
  ), [activeCategory, entriesByCategory, searchText]);
  const health = useMemo(() => memoryHealth(snapshot.overview, categoryCounts), [snapshot.overview, categoryCounts]);
  const autoDreamRuntime = snapshot.overview?.autoDreamEnabled === true;
  const autoDreamIntent = normalizeAutoDreamIntent(snapshot.overview?.autoDreamIntent);
  const autoDreamEnabled = autoDreamIntent === null ? autoDreamRuntime : autoDreamIntent;
  const autoDreamPendingRestart = autoDreamIntent !== null && autoDreamIntent !== autoDreamRuntime;
  const prefPercent = health ? memoryHealthPercent(health.preferenceCount, health.maxPerCategory) : 0;
  const projPercent = health ? memoryHealthPercent(health.projectCount, health.maxPerCategory) : 0;
  const similarGroups = health?.similarGroups || [];

  useEffect(() => {
    if (typeof onSimilarCountChange === 'function') {
      onSimilarCountChange(similarGroups.length);
    }
  }, [onSimilarCountChange, similarGroups.length]);

  const updateEditorForm = useCallback((patch) => {
    setEditor((current) => ({ ...current, form: { ...current.form, ...patch } }));
  }, []);

  const openCreate = useCallback((type) => {
    const form = defaultMemoryForm(type, memoryTargetForType(type));
    setEditor({ open: true, mode: 'create', form });
    setCreateMenuOpen(false);
  }, []);

  const openEdit = useCallback(async (entry) => {
    const key = `${entry.target}:${entry.path}`;
    setBusyKey(key);
    try {
      const cwd = normalizeSettingsCwd(projectPath);
      const detail = await getMemoryEntry({ cwd, target: entry.target, path: entry.path });
      setEditor({
        open: true,
        mode: 'edit',
        form: {
          target: firstText(detail?.target, entry.target),
          existingPath: firstText(detail?.path, entry.path),
          name: firstText(detail?.name, entry.name),
          description: firstText(detail?.description, entry.description),
          title: firstText(detail?.title, entry.title),
          type: firstText(detail?.type, entry.type),
          content: firstText(detail?.content, entry.preview, memoryTemplateForType(entry.type)),
        },
      });
    } catch (err) {
      showNotice('error', `加载失败：${errorMessage(err)}`);
    } finally {
      setBusyKey('');
    }
  }, [projectPath, showNotice]);

  const closeEditor = useCallback(() => {
    if (saving) return;
    setEditor((current) => ({ ...current, open: false }));
  }, [saving]);

  const saveEditor = useCallback(async () => {
    if (saving) return;
    const form = editor.form;
    const description = textValue(form.description);
    const content = textValue(form.content);
    if (!description) { showNotice('error', '请先填写描述'); return; }
    if (!content) { showNotice('error', '内容不能为空'); return; }
    setSaving(true);
    try {
      const cwd = normalizeSettingsCwd(projectPath);
      const type = textValue(form.type) || 'project';
      await upsertMemoryEntry({
        cwd,
        target: form.existingPath ? form.target : memoryTargetForType(type),
        existingPath: form.existingPath,
        name: memoryAutoName({ ...form, type }),
        description,
        title: textValue(form.title),
        type,
        content,
      });
      setEditor((current) => ({ ...current, open: false }));
      showNotice('info', '已保存');
      await refreshMemory();
    } catch (err) {
      showNotice('error', `保存失败：${errorMessage(err)}`);
    } finally {
      setSaving(false);
    }
  }, [editor.form, projectPath, refreshMemory, saving, showNotice]);

  const confirmDelete = useCallback(async () => {
    if (!deleteTarget || deletingKey) return;
    const key = `${deleteTarget.target}:${deleteTarget.path}`;
    setDeletingKey(key);
    try {
      const cwd = normalizeSettingsCwd(projectPath);
      await deleteMemoryEntry({ cwd, target: deleteTarget.target, path: deleteTarget.path });
      showNotice('info', `已删除：${memoryEntryTitle(deleteTarget)}`);
      setDeleteTarget(null);
      await refreshMemory();
    } catch (err) {
      showNotice('error', `删除失败：${errorMessage(err)}`);
    } finally {
      setDeletingKey('');
    }
  }, [deleteTarget, deletingKey, projectPath, refreshMemory, showNotice]);

  const toggleAutoDream = useCallback(async () => {
    if (autoToggling) return;
    const next = !autoDreamEnabled;
    setAutoToggling(true);
    try {
      await setMemoryAutoDreamIntent({ enabled: next });
      showNotice('warning', `自动沉淀已切换为${next ? '开启' : '关闭'}，重启 agent-terminal 后生效`);
      await refreshMemory();
    } catch (err) {
      showNotice('error', `切换自动沉淀失败：${errorMessage(err)}`);
    } finally {
      setAutoToggling(false);
    }
  }, [autoDreamEnabled, autoToggling, refreshMemory, showNotice]);

  const confirmMerge = useCallback(async () => {
    if (!mergeTarget || mergingKey) return;
    const key = memoryPairKey(mergeTarget);
    setMergingKey(key);
    try {
      const cwd = normalizeSettingsCwd(projectPath);
      await mergeMemoryEntries({
        cwd,
        targetA: mergeTarget.targetA,
        pathA: mergeTarget.pathA,
        targetB: mergeTarget.targetB,
        pathB: mergeTarget.pathB,
      });
      showNotice('info', `已整合「${mergeTarget.nameA || mergeTarget.pathA}」与「${mergeTarget.nameB || mergeTarget.pathB}」`);
      setMergeTarget(null);
      await refreshMemory();
    } catch (err) {
      showNotice('error', `整合失败：${errorMessage(err)}`);
    } finally {
      setMergingKey('');
    }
  }, [mergeTarget, mergingKey, projectPath, refreshMemory, showNotice]);

  const mergeAllGroups = useCallback(async () => {
    if (!similarGroups.length || mergingAll) return;
    setMergingAll(true);
    showNotice('info', '智能整合中（通常 10-20 秒），请勿离开');
    try {
      const cwd = normalizeSettingsCwd(projectPath);
      const result = await consolidateMemorySimilarities({ cwd });
      const merged = Number(result?.merged) || 0;
      const ignored = Number(result?.ignored) || 0;
      const failed = Number(result?.failed) || 0;
      const skipped = Number(result?.skipped) || 0;
      const parts = [`已整合 ${merged} 组`];
      if (ignored) parts.push(`${ignored} 组判定不应合`);
      if (failed) parts.push(`${failed} 组失败`);
      if (skipped) parts.push(`${skipped} 组跳过`);
      const firstError = Array.isArray(result?.errors) ? result.errors[0] : '';
      showNotice(failed || skipped ? 'warning' : 'info', `${parts.join('，')}${firstError ? `，原因：${firstError}` : ''}`);
      await refreshMemory();
    } catch (err) {
      showNotice('error', `智能整合失败：${errorMessage(err)}`);
    } finally {
      setMergingAll(false);
    }
  }, [mergingAll, projectPath, refreshMemory, showNotice, similarGroups.length]);

  const ignoreGroup = useCallback(async (group) => {
    const key = memoryPairKey(group);
    if (ignoringKey) return;
    setIgnoringKey(key);
    try {
      const cwd = normalizeSettingsCwd(projectPath);
      await ignoreMemorySimilarity({
        cwd,
        targetA: group.targetA,
        pathA: group.pathA,
        targetB: group.targetB,
        pathB: group.pathB,
      });
      showNotice('info', `已忽略「${group.nameA || group.pathA}」与「${group.nameB || group.pathB}」`);
      await refreshMemory();
    } catch (err) {
      showNotice('error', `忽略失败：${errorMessage(err)}`);
    } finally {
      setIgnoringKey('');
    }
  }, [ignoringKey, projectPath, refreshMemory, showNotice]);

  return (
    <section className="memory-page">
      <PageHeader
        icon={MemoryStick}
        title="记忆中心"
        actions={(
          <>
            <label>
              <Search size={17} />
              <input
                aria-label="搜索记忆"
                placeholder="搜索 name / description / path"
                value={searchText}
                onChange={(event) => setSearchText(event.target.value)}
              />
            </label>
            <div className="memory-create">
              <button type="button" className="light" aria-label="+ 新建 ▾" onClick={() => setCreateMenuOpen((open) => !open)}>
                <Plus size={15} /> 新建 ▾
              </button>
              {createMenuOpen ? (
                <div className="memory-create-menu">
                  <button type="button" onClick={() => openCreate('feedback')}>新建偏好</button>
                  <button type="button" onClick={() => openCreate('project')}>新建项目</button>
                </div>
              ) : null}
            </div>
          </>
        )}
      />

      <div className="memory-stats">
        <Panel title="总览">
          <strong className="big">{categoryCounts.all}</strong>
          <p><span className="orange-dot" />{categoryCounts.preference} 偏好 <span />{categoryCounts.project} 项目</p>
        </Panel>
        {health ? (
          <Panel title="健康度">
            <p>偏好 <meter value={health.preferenceCount} max={health.maxPerCategory} /> {health.preferenceCount} / {health.maxPerCategory}</p>
            <div className={`memory-health-track ${memoryHealthClass(prefPercent)}`}><span style={{ width: `${prefPercent}%` }} /></div>
            <p>项目 <meter value={health.projectCount} max={health.maxPerCategory} /> {health.projectCount} / {health.maxPerCategory}</p>
            <div className={`memory-health-track ${memoryHealthClass(projPercent)}`}><span style={{ width: `${projPercent}%` }} /></div>
            <p><span className="green-dot" /> 综合良好</p>
          </Panel>
        ) : null}
        <Panel title="自动沉淀">
          <p><span className={autoDreamEnabled ? 'green-dot' : 'orange-dot'} /> {autoDreamEnabled ? '已开启' : '已关闭'}</p>
          <small>对话结束后自动整理重要内容</small>
          <button type="button" onClick={() => { void toggleAutoDream(); }} disabled={autoToggling}>
            {autoDreamEnabled ? '关闭' : '开启'}
          </button>
          {autoDreamPendingRestart ? <small className="memory-pending">已保存切换，重启 agent-terminal 后生效</small> : null}
        </Panel>
      </div>

      {similarGroups.length ? (
        <div className="similar-alert">
          <AlertTriangle size={20} />
          <span>{similarGroups.length} 组条目内容相似</span>
          <button type="button" onClick={() => { void mergeAllGroups(); }} disabled={mergingAll || Boolean(mergingKey) || Boolean(ignoringKey)}>
            {mergingAll ? '整合中...' : '一键整合全部'}
          </button>
          <button type="button" onClick={() => setSimilarExpanded((expanded) => !expanded)}>{similarExpanded ? '收起' : '展开'}</button>
        </div>
      ) : null}
      {similarExpanded && similarGroups.length ? (
        <div className="memory-similar-list">
          {similarGroups.map((group) => {
            const key = memoryPairKey(group);
            return (
              <div className="memory-similar-item" key={key}>
                <span>「{group.nameA || group.pathA}」与「{group.nameB || group.pathB}」</span>
                <strong>{formatMemoryScore(group.score)}</strong>
                <button type="button" onClick={() => setMergeTarget(group)} disabled={Boolean(mergingKey) || mergingAll || Boolean(ignoringKey)}>整合</button>
                <button type="button" className="ghost" onClick={() => { void ignoreGroup(group); }} disabled={Boolean(ignoringKey) || mergingAll || Boolean(mergingKey)}>
                  {ignoringKey === key ? '...' : '忽略'}
                </button>
              </div>
            );
          })}
        </div>
      ) : null}

      {notice.message ? <div className={`memory-notice is-${notice.level}`}>{notice.message}</div> : null}
      {loading ? <div className="memory-notice is-info">正在加载记忆中心...</div> : null}
      {error ? <div className="memory-notice is-error">{error}</div> : null}

      <div className="memory-tabs" role="tablist" aria-label="记忆分类">
        {MEMORY_CATEGORIES.map((item) => (
          <button
            key={item.key}
            type="button"
            role="tab"
            className={activeCategory === item.key ? 'active' : ''}
            onClick={() => setActiveCategory(item.key)}
          >
            {item.label} {categoryCounts[item.key] || 0}
          </button>
        ))}
      </div>

      {!error && !loading && visibleEntries.length === 0 ? (
        <div className="empty-state memory-empty">
          <span><MemoryStick size={24} /></span>
          <h2>{searchText ? '没有匹配的条目' : '暂无记忆'}</h2>
          <p>{searchText ? '清空搜索或切换分类后再试。' : '点击右上角“新建”按钮开始添加记忆。'}</p>
        </div>
      ) : null}

      {!error && visibleEntries.length ? (
        <div className="memory-cards">
          {visibleEntries.map((entry) => (
            <MemoryCard
              key={entry.id}
              entry={entry}
              busy={busyKey === `${entry.target}:${entry.path}`}
              deleting={deletingKey === `${entry.target}:${entry.path}`}
              onEdit={openEdit}
              onDelete={setDeleteTarget}
            />
          ))}
        </div>
      ) : null}

      {editor.open ? (
        <MemoryEditorModal
          editor={editor}
          saving={saving}
          onClose={closeEditor}
          onChange={updateEditorForm}
          onSave={saveEditor}
          onDelete={() => {
            setDeleteTarget({
              target: editor.form.target,
              path: editor.form.existingPath,
              name: editor.form.name,
              title: editor.form.title,
            });
            setEditor((current) => ({ ...current, open: false }));
          }}
        />
      ) : null}
      {deleteTarget ? (
        <MemoryDeleteModal
          entry={deleteTarget}
          deleting={deletingKey === `${deleteTarget.target}:${deleteTarget.path}`}
          onClose={() => setDeleteTarget(null)}
          onConfirm={confirmDelete}
        />
      ) : null}
      {mergeTarget ? (
        <MemoryMergeModal
          group={mergeTarget}
          merging={mergingKey === memoryPairKey(mergeTarget)}
          onClose={() => setMergeTarget(null)}
          onConfirm={confirmMerge}
        />
      ) : null}
    </section>
  );
}

function MemoryCard({ entry, busy, deleting, onEdit, onDelete }) {
  return (
    <article className={`memory-card ${entry.category === 'project' ? 'type-project' : 'type-preference'}`}>
      <header>
        <h3>{memoryEntryTitle(entry)}</h3>
        <span>{entry.tag}</span>
        {entry.source === 'dream' ? <em>梦境</em> : null}
      </header>
      {entry.description ? <p>{entry.description}</p> : null}
      <code>{entry.preview || '暂无预览'}</code>
      <small className="path">{entry.path}</small>
      <footer>
        <time>{sharedFileTimestamp(entry.updatedAt)}</time>
        <button type="button" onClick={() => { void onEdit(entry); }} disabled={busy}>{busy ? '加载中...' : '编辑'}</button>
        <button type="button" className="danger" onClick={() => onDelete(entry)} disabled={deleting}>{deleting ? '删除中...' : '删除'}</button>
      </footer>
    </article>
  );
}

function MemoryEditorModal({ editor, saving, onClose, onChange, onSave, onDelete }) {
  const form = editor.form;
  const identityLocked = editor.mode === 'edit' && Boolean(form.existingPath);
  return (
    <div className="modal-overlay">
      <section className="modal-box memory-editor-modal" role="dialog" aria-modal="true" aria-label={editor.mode === 'edit' ? '编辑记忆' : '新建记忆'}>
        <header>
          <div>
            <h2>{editor.mode === 'edit' ? '编辑记忆' : '新建记忆'}</h2>
            <p>{form.type === 'project' ? '项目记忆' : '偏好记忆'}</p>
          </div>
        </header>
        <div className="memory-form-grid">
          <label>分类
            <select
              value={form.type}
              onChange={(event) => onChange({
                type: event.target.value,
                target: memoryTargetForType(event.target.value),
                content: memoryTemplateForType(event.target.value),
              })}
              disabled={identityLocked}
            >
              {MEMORY_EDITOR_TYPES.map((type) => <option key={type.key} value={type.key}>{type.label}</option>)}
            </select>
          </label>
          <label>描述
            <input value={form.description} onChange={(event) => onChange({ description: event.target.value })} placeholder="一句话描述为什么值得长期保留" />
          </label>
          <label>卡片标题
            <input value={form.title} onChange={(event) => onChange({ title: event.target.value })} placeholder="卡片上显示的短标题" />
          </label>
        </div>
        <label className="memory-content-label">内容
          <textarea rows={12} value={form.content} onChange={(event) => onChange({ content: event.target.value })} />
        </label>
        <div className="memory-editor-actions">
          <button type="button" className="ghost" onClick={onClose} disabled={saving}>取消</button>
          {form.existingPath ? <button type="button" className="danger" onClick={onDelete} disabled={saving}>删除</button> : null}
          <button type="button" onClick={() => onChange({ content: memoryTemplateForType(form.type) })} disabled={saving}>套用当前类型模板</button>
          <button type="button" className="light" onClick={() => { void onSave(); }} disabled={saving || !textValue(form.description) || !textValue(form.content)}>
            {saving ? '保存中...' : '保存'}
          </button>
        </div>
      </section>
    </div>
  );
}

function MemoryDeleteModal({ entry, deleting, onClose, onConfirm }) {
  return (
    <div className="modal-overlay">
      <section className="modal-box" role="dialog" aria-modal="true" aria-label="删除记忆">
        <header>
          <h2>删除记忆</h2>
          <button type="button" className="ghost" onClick={onClose} disabled={deleting}>关闭</button>
        </header>
        <p>删除后无法恢复。如果后续可能重用，建议先编辑备份内容。</p>
        <p className="path">{memoryEntryTitle(entry)} · {entry.target}</p>
        <footer>
          <button type="button" className="ghost" onClick={onClose} disabled={deleting}>取消</button>
          <button type="button" className="danger" onClick={() => { void onConfirm(); }} disabled={deleting}>{deleting ? '删除中...' : '确认删除'}</button>
        </footer>
      </section>
    </div>
  );
}

function MemoryMergeModal({ group, merging, onClose, onConfirm }) {
  return (
    <div className="modal-overlay">
      <section className="modal-box" role="dialog" aria-modal="true" aria-label="整合相似记忆">
        <header>
          <div>
            <h2>整合相似记忆</h2>
            <p>相似度 {formatMemoryScore(group.score)}</p>
          </div>
          <button type="button" className="ghost" onClick={onClose} disabled={merging}>关闭</button>
        </header>
        <p>合并到：{group.nameA || group.pathA} · {group.targetA}</p>
        <p>移除：{group.nameB || group.pathB} · {group.targetB}</p>
        <footer>
          <button type="button" className="ghost" onClick={onClose} disabled={merging}>取消</button>
          <button type="button" className="light" onClick={() => { void onConfirm(); }} disabled={merging}>{merging ? '整合中...' : '确认整合'}</button>
        </footer>
      </section>
    </div>
  );
}

function SettingsPage({ projectPath }) {
  const [buildInfo, setBuildInfo] = useState(null);
  const [form, setForm] = useState({
    stallThresholdSec: String(SETTINGS_DEFAULTS.stallThresholdSec),
    contextWarn: String(SETTINGS_DEFAULTS.contextThresholds[0]),
    contextDanger: String(SETTINGS_DEFAULTS.contextThresholds[1]),
    contextCritical: String(SETTINGS_DEFAULTS.contextThresholds[2]),
    activeProvider: SETTINGS_DEFAULTS.activeProvider,
    codexHome: SETTINGS_DEFAULTS.codexHome,
    codexInstanceKey: SETTINGS_DEFAULTS.codexInstanceKey,
    codexModelProvider: SETTINGS_DEFAULTS.codexModelProvider,
    providerModel: SETTINGS_DEFAULTS.providerModel,
    providerEffort: SETTINGS_DEFAULTS.providerEffort,
    sandboxPolicy: SETTINGS_DEFAULTS.sandboxPolicy,
    writableRoots: SETTINGS_DEFAULTS.writableRoots,
    networkAccess: SETTINGS_DEFAULTS.networkAccess,
  });
  const [status, setStatus] = useState('');
  const [error, setError] = useState('');

  const settingsCwd = projectPath;

  const refreshBuildInfo = useCallback(async () => {
    setError('');
    try {
      const info = await getBuildInfo();
      if (!info || typeof info !== 'object') throw new Error('build info response must be an object');
      setBuildInfo(info);
      setStatus('构建信息已刷新');
    } catch (err) {
      setError(err.message || String(err));
    }
  }, []);

  const loadPreferences = useCallback(async () => {
    setError('');
    const cwd = normalizeSettingsCwd(settingsCwd);
    const [stallValue, contextValue, activeProviderValue] = await Promise.all([
      getPreference({ cwd, key: SETTINGS_KEYS.stallThreshold }),
      getPreference({ cwd, key: SETTINGS_KEYS.contextThresholds }),
      getPreference({ cwd, key: SETTINGS_KEYS.activeProvider }),
    ]);
    const activeProvider = normalizeProviderName(activeProviderValue);
    const providerPrefix = `settings.provider.${activeProvider}`;
    const [
      codexHome,
      codexInstanceKey,
      codexModelProvider,
      providerModel,
      providerEffort,
      sandbox,
    ] = await Promise.all([
      getPreference({ cwd, key: providerSettingKey('codex', 'codexHome') }),
      getPreference({ cwd, key: providerSettingKey('codex', 'codexInstanceKey') }),
      getPreference({ cwd, key: providerSettingKey('codex', 'codexModelProvider') }),
      getPreference({ cwd, key: `${providerPrefix}.model` }),
      getPreference({ cwd, key: `${providerPrefix}.effort` }),
      getPreference({ cwd, key: `${providerPrefix}.sandbox` }),
    ]);
    const contextThresholds = normalizeContextThresholds(contextValue);
    setForm({
      stallThresholdSec: String(numberSetting(stallValue, SETTINGS_DEFAULTS.stallThresholdSec)),
      contextWarn: String(contextThresholds[0]),
      contextDanger: String(contextThresholds[1]),
      contextCritical: String(contextThresholds[2]),
      activeProvider,
      codexHome: stringSetting(codexHome, SETTINGS_DEFAULTS.codexHome),
      codexInstanceKey: stringSetting(codexInstanceKey, SETTINGS_DEFAULTS.codexInstanceKey),
      codexModelProvider: stringSetting(codexModelProvider, SETTINGS_DEFAULTS.codexModelProvider),
      providerModel: stringSetting(providerModel, SETTINGS_DEFAULTS.providerModel),
      providerEffort: stringSetting(providerEffort, SETTINGS_DEFAULTS.providerEffort),
      sandboxPolicy: sandboxPolicyFromPreference(sandbox),
      writableRoots: writableRootsFromPreference(sandbox),
      networkAccess: Boolean(sandbox && typeof sandbox === 'object' && sandbox.networkAccess),
    });
  }, [settingsCwd]);

  useEffect(() => {
    refreshBuildInfo().catch(() => { });
  }, [refreshBuildInfo]);

  useEffect(() => {
    loadPreferences().catch((err) => {
      setError(err.message || String(err));
    });
  }, [loadPreferences]);

  const updateForm = (key) => (event) => {
    const value = event.target.type === 'checkbox' ? event.target.checked : event.target.value;
    setForm((current) => ({ ...current, [key]: value }));
  };

  const saveRuntimeSettings = async () => {
    setError('');
    setStatus('');
    try {
      const cwd = normalizeSettingsCwd(settingsCwd);
      const { stallThresholdSec, contextThresholds } = validateRuntimeThresholds(form);
      await setPreference({ cwd, key: SETTINGS_KEYS.stallThreshold, value: stallThresholdSec });
      await setPreference({ cwd, key: SETTINGS_KEYS.contextThresholds, value: contextThresholds });
      setStatus('运行阈值已保存');
    } catch (err) {
      setError(err.message || String(err));
    }
  };

  const saveProviderSettings = async () => {
    setError('');
    setStatus('');
    try {
      const cwd = normalizeSettingsCwd(settingsCwd);
      const provider = normalizeProviderName(form.activeProvider);
      await setPreference({ cwd, key: SETTINGS_KEYS.activeProvider, value: provider });
      await setPreference({ cwd, key: providerSettingKey(provider, 'model'), value: form.providerModel.trim() });
      await setPreference({ cwd, key: providerSettingKey(provider, 'effort'), value: form.providerEffort.trim() });
      await setPreference({
        cwd,
        key: providerSettingKey(provider, 'sandbox'),
        value: sandboxPreferenceValue(form.sandboxPolicy, form.writableRoots, form.networkAccess),
      });
      await setPreference({ cwd, key: providerSettingKey('codex', 'codexHome'), value: form.codexHome.trim() });
      await setPreference({ cwd, key: providerSettingKey('codex', 'codexInstanceKey'), value: form.codexInstanceKey.trim() });
      await setPreference({ cwd, key: providerSettingKey('codex', 'codexModelProvider'), value: form.codexModelProvider.trim() });
      setStatus('Provider 设置已保存');
    } catch (err) {
      setError(err.message || String(err));
    }
  };

  return (
    <section className="settings-page">
      <PageHeader icon={Settings} title="设置" actions={<button type="button" onClick={() => void refreshBuildInfo()}>刷新构建信息</button>} />
      <Panel title="ABOUT">
        <dl>
          <dt>版本</dt><dd>Agent Orchestrator {buildInfo?.version || 'unknown'}</dd>
          <dt>运行时</dt><dd>{buildInfo?.runtime || 'unknown'}</dd>
          <dt>构建时间</dt><dd>{buildInfo?.buildTime || 'unknown'}</dd>
          <dt>Commit</dt><dd>{buildInfo?.commit || 'unknown'}</dd>
          <dt>当前项目</dt><dd>{settingsCwd}</dd>
        </dl>
      </Panel>
      <Panel title="TURN TRACKER">
        <div className="form-line">
          <label>统一超时阈值<input aria-label="统一超时阈值" type="number" min="30" value={form.stallThresholdSec} onChange={updateForm('stallThresholdSec')} /> 秒</label>
          <button type="button" onClick={() => void saveRuntimeSettings()}>保存超时阈值</button>
        </div>
      </Panel>
      <Panel title="CONTEXT USAGE ALERT">
        <div className="form-line">
          <label>Warn 阈值<input type="number" min="1" max="100" value={form.contextWarn} onChange={updateForm('contextWarn')} /></label>
          <label>Danger 阈值<input type="number" min="1" max="100" value={form.contextDanger} onChange={updateForm('contextDanger')} /></label>
          <label>Critical 阈值<input type="number" min="1" max="100" value={form.contextCritical} onChange={updateForm('contextCritical')} /></label>
          <button type="button" onClick={() => void saveRuntimeSettings()}>保存运行阈值</button>
        </div>
      </Panel>
      <Panel title="PROVIDER">
        <div className="form-grid">
          <label>Active Provider<select value={form.activeProvider} onChange={updateForm('activeProvider')}><option value="codex">Codex</option><option value="claude">Claude</option></select></label>
          <label>Provider Model<input value={form.providerModel} onChange={updateForm('providerModel')} /></label>
          <label>Provider Effort<input value={form.providerEffort} onChange={updateForm('providerEffort')} /></label>
          <label>Codex Home<input value={form.codexHome} onChange={updateForm('codexHome')} /></label>
          <label>Instance Key<input value={form.codexInstanceKey} onChange={updateForm('codexInstanceKey')} /></label>
          <label>Model Provider<input value={form.codexModelProvider} onChange={updateForm('codexModelProvider')} /></label>
          <label>Sandbox Policy<select value={form.sandboxPolicy} onChange={updateForm('sandboxPolicy')}><option value="workspaceWrite">workspaceWrite</option><option value="readOnly">readOnly</option><option value="dangerFullAccess">dangerFullAccess</option></select></label>
          <label className="checkbox-line"><input type="checkbox" checked={form.networkAccess} onChange={updateForm('networkAccess')} /> Network Access</label>
          <label className="wide">Writable Roots<textarea value={form.writableRoots} onChange={updateForm('writableRoots')} placeholder="每行一个绝对路径" /></label>
        </div>
        <div className="settings-actions"><button type="button" onClick={() => void saveProviderSettings()}>保存 Provider 设置</button></div>
      </Panel>
      {status ? <p className="settings-status">{status}</p> : null}
      {error ? <p className="danger-text" role="alert">{error}</p> : null}
    </section>
  );
}

function Panel({ title, children }) {
  return (
    <section className="panel">
      <h3>{title}</h3>
      <div>{children}</div>
    </section>
  );
}

function EmptyState({ icon: Icon, title, text }) {
  return (
    <div className="empty-state">
      <span><Icon size={34} /></span>
      <h2>{title}</h2>
      <p>{text}</p>
    </div>
  );
}

export default App;
