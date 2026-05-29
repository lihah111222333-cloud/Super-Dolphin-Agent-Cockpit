// Project and Workspace CWD Zustand Store
import { create } from 'zustand';
import {
  addProject as addProjectRPC,
  getProjects,
  removeProject as removeProjectRPC,
  selectProjectDir,
  setActiveProject,
} from '../../../shared/api/backendApi';
import { useLogStore } from '../../log/model/useLogStore';

function normalizePath(path) {
  let value = (path || '').trim();
  if (!value) return '';
  if (value !== '/' && !/^[a-zA-Z]:[\\/]?$/.test(value)) {
    value = value.replace(/[\\/]+$/, '');
  }
  return value;
}

export const useProjectStore = create((set, get) => {
  const getCwdParams = () => {
    const cwd = normalizePath(get().scopeCwd || '');
    if (!cwd || cwd === '.') {
      useLogStore.getState().warn('project.missing_cwd_param', { scopeCwd: get().scopeCwd });
      throw new Error('project scope cwd is required');
    }
    return { cwd };
  };

  const applyProjectsState = (payload) => {
    if (!payload || typeof payload !== 'object') throw new Error('project state response must be an object');
    const projects = (payload.projects || []).map(normalizePath).filter(Boolean);
    const active = normalizePath(payload.active) || '.';

    set({ projects, active });
  };

  return {
    projects: [],
    active: '.',
    scopeCwd: '',
    showModal: false,
    modalPath: '',
    browsing: false,

    setScopeCwd: (cwd) => {
      const normalized = normalizePath(cwd || '');
      set({ scopeCwd: normalized });
      useLogStore.getState().debug('project.scope_cwd.set', { cwd: normalized });
    },

    requireActionCwd: (reason) => {
      const active = normalizePath(get().active);
      const scopeCwd = normalizePath(get().scopeCwd);
      const finalCwd = (!active || active === '.') ? scopeCwd : active;

      if (!finalCwd || finalCwd === '.') {
        useLogStore.getState().error('project.require_cwd.failed', { reason, active, scopeCwd });
        throw new Error(`feature.validate: CWD is required for ${reason}`);
      }
      return finalCwd;
    },

    reloadProjects: async () => {
      try {
        const res = await getProjects(getCwdParams());
        applyProjectsState(res);
        useLogStore.getState().debug('project.state.reloaded', {
          count: get().projects.length,
          active: get().active,
        });
      } catch (error) {
        useLogStore.getState().error('project.reload.failed', { error: error.message });
        throw error;
      }
    },

    setActive: async (path) => {
      const next = normalizePath(path) || '.';
      try {
        useLogStore.getState().info('project.active.set.start', { path: next });
        const res = await setActiveProject({ ...getCwdParams(), path: next });
        applyProjectsState(res);
        useLogStore.getState().info('project.active.changed', { active: get().active });
      } catch (error) {
        useLogStore.getState().error('project.setActive.failed', { error: error.message });
        throw error;
      }
    },

    addProject: async (path) => {
      const normalized = normalizePath(path);
      if (!normalized || normalized === '.') return false;
      try {
        const res = await addProjectRPC({ ...getCwdParams(), path: normalized });
        applyProjectsState(res);
        useLogStore.getState().info('project.added', { path: normalized, total: get().projects.length });
        return true;
      } catch (error) {
        useLogStore.getState().error('project.add.failed', { error: error.message });
        throw error;
      }
    },

    removeProject: async (path) => {
      const target = normalizePath(path);
      try {
        const res = await removeProjectRPC({ ...getCwdParams(), path: target });
        applyProjectsState(res);
        useLogStore.getState().info('project.removed', { path: target, total: get().projects.length });
      } catch (error) {
        useLogStore.getState().error('project.remove.failed', { error: error.message });
        throw error;
      }
    },

    openModal: (defaultPath = '') => {
      const seed = defaultPath || (get().active === '.' ? '' : get().active);
      set({
        modalPath: normalizePath(seed),
        showModal: true,
      });
    },

    closeModal: () => {
      set({ showModal: false, browsing: false });
    },

    browseDirectory: async () => {
      set({ browsing: true });
      const start = Date.now();
      const defaultPath = normalizePath(get().modalPath || (get().active === '.' ? '' : get().active));
      try {
        const value = await selectProjectDir(defaultPath);
        if (value) {
          set({ modalPath: normalizePath(value) });
        }
        useLogStore.getState().info('project.browse.done', {
          selected: Boolean(value),
          path: value || '',
          duration_ms: Date.now() - start,
        });
      } catch (error) {
        useLogStore.getState().warn('project.browse.failed', {
          error: error.message,
          duration_ms: Date.now() - start,
        });
      } finally {
        set({ browsing: false });
      }
    },

    confirmModal: async () => {
      const ok = await get().addProject(get().modalPath);
      if (ok) {
        get().closeModal();
      }
      return ok;
    },
  };
});
