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
  showModal: false,
  modalPath: '',
  browsing: false,
});

function normalizeProjectList(input) {
  if (!Array.isArray(input)) return [];
  const projects = [];
  for (const item of input) {
    const normalized = normalizePath((item || '').toString());
    if (!normalized || normalized === '.') continue;
    if (projects.includes(normalized)) continue;
    projects.push(normalized);
  }
  return projects;
}

function applyProjectsState(payload) {
  const projects = normalizeProjectList(payload?.projects);
  let active = normalizePath((payload?.active || '.').toString()) || '.';
  if (active !== '.' && !projects.includes(active)) {
    active = '.';
  }
  state.projects = projects;
  state.active = active;
}

async function callProjectAPI(method, params = {}) {
  if (typeof globalThis.__AO_PROJECTS_CALL_API__ === 'function') {
    return globalThis.__AO_PROJECTS_CALL_API__(method, params);
  }
  return callAPI(method, params);
}

async function reloadProjects() {
  try {
    const res = await callProjectAPI('ui/projects/get', {});
    applyProjectsState(res || {});
    logDebug('project', 'state.reloaded', {
      count: state.projects.length,
      active: state.active,
    });
  } catch (error) {
    logWarn('project', 'state.reload.failed', { error });
  }
}

async function setActive(path) {
  const next = normalizePath(path) || '.';
  try {
    const res = await callProjectAPI('ui/projects/setActive', { path: next });
    applyProjectsState(res || {});
    logInfo('project', 'active.changed', { active: state.active });
  } catch (error) {
    logWarn('project', 'active.set.failed', { path: next, error });
  }
}

async function addProject(path) {
  const normalized = normalizePath(path);
  if (!normalized || normalized === '.') return false;
  try {
    const res = await callProjectAPI('ui/projects/add', { path: normalized });
    applyProjectsState(res || {});
    logInfo('project', 'added', { path: normalized, total: state.projects.length });
    return true;
  } catch (error) {
    logWarn('project', 'add.failed', { path: normalized, error });
    return false;
  }
}

async function removeProject(path) {
  const target = normalizePath(path);
  try {
    const res = await callProjectAPI('ui/projects/remove', { path: target });
    applyProjectsState(res || {});
    logInfo('project', 'removed', { path: target, total: state.projects.length });
  } catch (error) {
    logWarn('project', 'remove.failed', { path: target, error });
  }
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
  logInfo('project', 'browse.start', {});
  try {
    const value = await selectProjectDir();
    if (value) {
      state.modalPath = normalizePath(value);
    }
    logInfo('project', 'browse.done', {
      selected: Boolean(value),
      path: value || '',
      duration_ms: Date.now() - start,
    });
  } catch (error) {
    logWarn('project', 'browse.failed', {
      error,
      duration_ms: Date.now() - start,
    });
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

reloadProjects().catch((error) => {
  logWarn('project', 'state.bootstrap.failed', { error });
});


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
      let changed = true;
      while (changed) {
        changed = false;
        const countByLabel = {};
        items.forEach((item) => {
          countByLabel[item.label] = (countByLabel[item.label] || 0) + 1;
        });
        items.forEach((item) => {
          if (countByLabel[item.label] > 1 && item.label !== item.full) {
            const segs = item._segments;
            const currentDepth = item.label.split('/').filter(Boolean).length;
            const nextDepth = Math.min(currentDepth + 1, segs.length);
            const nextLabel = segs.slice(-nextDepth).join('/') || item.full;
            if (nextLabel !== item.label) {
              item.label = nextLabel;
              changed = true;
            }
          }
        });
      }
      items.forEach((item) => delete item._segments);
      return [{ value: '.', label: '当前目录 (.)', full: '.' }, ...items];
    }),

    setActive,
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
