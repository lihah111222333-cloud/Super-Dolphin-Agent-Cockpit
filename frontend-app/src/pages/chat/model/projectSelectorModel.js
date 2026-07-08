/*
 * project selector model 只整理路径显示和按钮可用性。
 * 注册项目、保存 activeProject、刷新聊天列表都在 project slice。
 */

import { firstText, requiredMarkdownArray, trimmedText } from '../markdown/markdownMessageModel.js';

export function projectDisplayName(path) {
  const value = trimmedText(path);
  if (!value || value === '未选择项目') return '未选择项目';
  return firstText(value.split(/[\\/]/).filter(Boolean).pop(), value);
}

export function normalizeProjectPath(path) {
  const value = trimmedText(path);
  if (!value) return '';
  if (value !== '/' && !/^[a-zA-Z]:[\\/]?$/.test(value)) {
    return value.replace(/[\\/]+$/, '');
  }
  return value;
}

function hasUsableProjectCwd(store) {
  const activeProject = normalizeProjectPath(store?.activeProject);
  const cwd = activeProject && activeProject !== '.' && activeProject !== '未选择项目'
    ? activeProject
    : normalizeProjectPath(store?.cwd);
  return Boolean(cwd && cwd !== '.' && cwd !== '未选择项目');
}

export function runtimeProjectPath(activeProject, fallbackProject) {
  const normalized = normalizeProjectPath(activeProject);
  if (normalized && normalized !== '.' && normalized !== '未选择项目') return normalized;
  return normalizeProjectPath(fallbackProject);
}

export function canUseProjectActionsForStore(store) {
  return store?.bootstrapStatus === 'ready' && hasUsableProjectCwd(store);
}

function disambiguateProjectLabels(items) {
  let changed = true;
  while (changed) {
    changed = false;
    const countByLabel = items.reduce((acc, item) => {
      acc[item.label] = Number(acc[item.label] ?? 0) + 1;
      return acc;
    }, {});
    for (const item of items) {
      if (countByLabel[item.label] <= 1 || item.label === item.full) continue;
      const nextDepth = Math.min(item.depth + 1, item.segments.length);
      const nextLabel = firstText(item.segments.slice(-nextDepth).join('/'), item.full);
      if (nextLabel === item.label) continue;
      item.depth = nextDepth;
      item.label = nextLabel;
      changed = true;
    }
  }
}

export function projectOptionsFor(projects = [], activeProject = '', fallbackProject = '') {
  const values = [];
  const addValue = (value) => {
    const normalized = normalizeProjectPath(value);
    if (!normalized || values.includes(normalized)) return;
    values.push(normalized);
  };
  addValue(activeProject);
  addValue(fallbackProject);
  for (const project of requiredMarkdownArray(projects, 'projects')) addValue(project);

  const items = [];
  for (const value of values) {
    if (value === '.') continue;
    const segments = value.split(/[\\/]/).filter(Boolean);
    const depth = Math.min(2, segments.length);
    items.push({
      value,
      label: firstText(segments.slice(-depth).join('/'), value),
      full: value,
      segments,
      depth,
    });
  }
  disambiguateProjectLabels(items);
  return [
    { value: '.', label: '当前目录 (.)', full: '.' },
    ...items.map(({ value, label, full }) => ({ value, label, full })),
  ];
}
