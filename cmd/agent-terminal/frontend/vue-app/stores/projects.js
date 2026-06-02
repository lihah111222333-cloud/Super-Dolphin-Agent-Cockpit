import { reactive, computed } from '../../lib/vue.esm-browser.prod.js';
import { callAPI, selectProjectDir } from '../services/api.js';
import { logDebug, logInfo, logWarn } from '../services/log.js';

function normalizePath(path) {
  let value = (path || '').trim();
  if (!value) return '';
  if (value !== '/' && !/^[a-zA-Z]:[\\/]?$/.test(value)) {
    value = value.replace(/[\\/]+$/, '');
  }
  return value;
}

const state = reactive({
  projects: [],
  active: '.',
  scopeCwd: '',
  showModal: false,
  modalPath: '',
  browsing: false,
});

function normalizeProjectList(input) {
  if (!Array.isArray(input)) throw new Error('project state projects must be an array');
  const projects = [];
  for (const item of input) {
    if (typeof item !== 'string') throw new Error('project state project path must be a string');
    const normalized = normalizePath(item);
    if (!normalized || normalized === '.') throw new Error('project state project path must be explicit');
    if (projects.includes(normalized)) throw new Error(`project state contains duplicate project path: ${normalized}`);
    projects.push(normalized);
  }
  return projects;
}

function applyProjectsState(payload) {
  if (!payload || typeof payload !== 'object') throw new Error('project state response must be an object');
  const projects = normalizeProjectList(payload.projects);
  if (typeof payload.active !== 'string') throw new Error('project state active must be a string');
  const active = normalizePath(payload.active) || '.';
  if (active !== '.' && !projects.includes(active)) {
    throw new Error(`project state active path is not in projects: ${active}`);
  }
  state.projects = projects;
  state.active = active;
}

function setScopeCwd(cwd) {
  state.scopeCwd = normalizePath((cwd || '').toString());
}

function scopedProjectParams(params = {}) {
  const cwd = normalizePath((state.scopeCwd || '').toString());
  if (!cwd || cwd === '.') throw new Error('project scope cwd is required');
  return { ...params, cwd };
}

function projectParamsForAdd(path) {
  const cwd = normalizePath((state.scopeCwd || '').toString()) || normalizePath(path);
  if (!cwd || cwd === '.') throw new Error('project scope cwd is required');
  return { path, cwd };
}

function projectParamsForSetActive(path) {
  const cwd = normalizePath((state.scopeCwd || '').toString()) || (path === '.' ? '' : normalizePath(path));
  if (!cwd || cwd === '.') throw new Error('project scope cwd is required');
  return { path, cwd };
}

async function callProjectAPI(method, params = {}) {
  if (typeof globalThis.__AO_PROJECTS_CALL_API__ === 'function') {
    return globalThis.__AO_PROJECTS_CALL_API__(method, params);
  }
  return callAPI(method, params);
}

async function reloadProjects() {
  const res = await callProjectAPI('ui/projects/get', scopedProjectParams());
  applyProjectsState(res);
  logDebug('project', 'state.reloaded', {
    count: state.projects.length,
    active: state.active,
  });
}

async function setActive(path) {
  const next = normalizePath(path) || '.';
  logWarn('project', 'active.set.start', { path: next, ts: Date.now() });
  const params = projectParamsForSetActive(next);
  const hadScope = Boolean(normalizePath((state.scopeCwd || '').toString()));
  const res = await callProjectAPI('ui/projects/setActive', params);
  applyProjectsState(res);
  if (!hadScope) setScopeCwd(params.cwd);
  logWarn('project', 'active.set.done', { active: state.active, ts: Date.now() });
  logInfo('project', 'active.changed', { active: state.active });
}

async function addProject(path) {
  const normalized = normalizePath(path);
  if (!normalized || normalized === '.') return false;
  const hadScope = Boolean(normalizePath((state.scopeCwd || '').toString()));
  const res = await callProjectAPI('ui/projects/add', projectParamsForAdd(normalized));
  applyProjectsState(res);
  if (!hadScope) {
    const addedPath = state.projects.includes(normalized) ? normalized : (state.projects[0] || normalized);
    setScopeCwd(addedPath);
    await setActive(addedPath);
  }
  logInfo('project', 'added', { path: normalized, total: state.projects.length });
  return true;
}

async function removeProject(path) {
  const target = normalizePath(path);
  const res = await callProjectAPI('ui/projects/remove', scopedProjectParams({ path: target }));
  applyProjectsState(res);
  logInfo('project', 'removed', { path: target, total: state.projects.length });
}

function openModal(defaultPath = '') {
  const seed = defaultPath || (state.active === '.' ? '' : state.active);
  state.modalPath = normalizePath(seed);
  state.showModal = true;
  logDebug('project', 'modal.opened', { seed: state.modalPath });
}

function closeModal() {
  state.showModal = false;
  state.browsing = false;
  logDebug('project', 'modal.closed', {});
}

async function browseDirectory() {
  // UI intent only: actual directory picker is provided by Wails bridge (Go).
  state.browsing = true;
  const start = Date.now();
  const defaultPath = normalizePath(state.modalPath || (state.active === '.' ? '' : state.active));
  logInfo('project', 'browse.start', { default_path: defaultPath });
  try {
    const value = await selectProjectDir(defaultPath);
    if (value) {
      state.modalPath = normalizePath(value);
    }
    logInfo('project', 'browse.done', {
      selected: Boolean(value),
      path: value || '',
      default_path: defaultPath,
      duration_ms: Date.now() - start,
    });
  } catch (error) {
    logWarn('project', 'browse.failed', {
      error,
      default_path: defaultPath,
      duration_ms: Date.now() - start,
    });
    throw error;
  } finally {
    state.browsing = false;
  }
}

function confirmModal() {
  return addProject(state.modalPath)
    .then((ok) => {
      if (ok) {
        closeModal();
      }
      logInfo('project', 'modal.confirm', {
        ok,
        path: normalizePath(state.modalPath),
      });
      return ok;
    });
}

function quickAdd() {
  openModal();
}

function disambiguateProjectLabels(items) {
  let changed = true;
  while (changed) {
    changed = false;
    const countByLabel = {};
    for (const item of items) countByLabel[item.label] = (countByLabel[item.label] || 0) + 1;
    for (const item of items) {
      if (countByLabel[item.label] <= 1 || item.label === item.full) continue;
      const segs = item._segments;
      const currentDepth = item.label.split('/').filter(Boolean).length;
      const nextDepth = Math.min(currentDepth + 1, segs.length);
      const nextLabel = segs.slice(-nextDepth).join('/') || item.full;
      if (nextLabel === item.label) continue;
      item.label = nextLabel;
      changed = true;
    }
  }
}

export function useProjectStore() {
  return {
    state,
    projectOptions: computed(() => {
      const items = state.projects.map((path) => {
        const segments = path.split('/').filter(Boolean);
        const short = segments.slice(-2).join('/') || path;
        return { value: path, label: short, full: path, _segments: segments };
      });
      // 消歧：label 碰撞时逐步增加段数，直到唯一或到达完整路径
      disambiguateProjectLabels(items);
      items.forEach((item) => delete item._segments);
      const scopeCwd = normalizePath(state.scopeCwd || '');
      return [{ value: '.', label: '当前目录 (.)', full: scopeCwd || '.' }, ...items];
    }),

    setActive,
    setScopeCwd,
    addProject,
    removeProject,
    reloadProjects,

    openModal,
    closeModal,
    confirmModal,
    browseDirectory,
    quickAdd,
  };
}
