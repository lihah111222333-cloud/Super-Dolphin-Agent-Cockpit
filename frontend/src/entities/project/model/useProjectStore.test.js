import { vi, describe, it, expect, beforeEach } from 'vitest';
import { useProjectStore } from './useProjectStore';
import { useLogStore } from '../../log/model/useLogStore';

const mockBackend = vi.hoisted(() => ({
  addProject: vi.fn(),
  getProjects: vi.fn(),
  removeProject: vi.fn(),
  selectProjectDir: vi.fn(),
  setActiveProject: vi.fn(),
  registerBridgeLogStore: vi.fn(),
  sendFrontendLogBatch: vi.fn(),
}));

vi.mock('../../../shared/api/backendApi', () => ({
  addProject: (...args) => mockBackend.addProject(...args),
  getProjects: (...args) => mockBackend.getProjects(...args),
  removeProject: (...args) => mockBackend.removeProject(...args),
  selectProjectDir: (...args) => mockBackend.selectProjectDir(...args),
  setActiveProject: (...args) => mockBackend.setActiveProject(...args),
  registerBridgeLogStore: (...args) => mockBackend.registerBridgeLogStore(...args),
  sendFrontendLogBatch: (...args) => mockBackend.sendFrontendLogBatch(...args),
}));

describe('useProjectStore', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    useProjectStore.setState({
      projects: [],
      active: '.',
      scopeCwd: '',
      showModal: false,
      modalPath: '',
      browsing: false,
    });
    useLogStore.setState({
      entries: [],
      bridgeQueue: [],
    });
  });

  describe('fail-fast CWD requirements', () => {
    it('should throw an error in reloadProjects if scopeCwd is missing', async () => {
      const store = useProjectStore.getState();

      await expect(store.reloadProjects()).rejects.toThrow('project scope cwd is required');

      const logState = useLogStore.getState();
      expect(logState.entries.some(e => e.event === 'project.missing_cwd_param')).toBe(true);
    });

    it('should throw an error in addProject if scopeCwd is missing', async () => {
      const store = useProjectStore.getState();

      await expect(store.addProject('/some/path')).rejects.toThrow('project scope cwd is required');
    });

    it('should throw an error in requireActionCwd when both active and scopeCwd are missing or invalid', () => {
      const store = useProjectStore.getState();

      expect(() => store.requireActionCwd('build')).toThrow('feature.validate: CWD is required for build');

      const logState = useLogStore.getState();
      expect(logState.entries.some(e => e.event === 'project.require_cwd.failed')).toBe(true);
    });

    it('should return active project path if it is valid', () => {
      useProjectStore.setState({
        active: '/active/project',
        scopeCwd: '/scope/cwd',
      });

      const cwd = useProjectStore.getState().requireActionCwd('build');
      expect(cwd).toBe('/active/project');
    });

    it('should fall back to scopeCwd if active project path is invalid or "."', () => {
      useProjectStore.setState({
        active: '.',
        scopeCwd: '/scope/cwd',
      });

      const cwd = useProjectStore.getState().requireActionCwd('build');
      expect(cwd).toBe('/scope/cwd');
    });
  });

  describe('project state actions', () => {
    it('should reload projects list and update store state', async () => {
      useProjectStore.setState({ scopeCwd: '/scope/cwd' });
      mockBackend.getProjects.mockResolvedValue({
        projects: ['/scope/cwd/p1', '/scope/cwd/p2'],
        active: '/scope/cwd/p1',
      });

      await useProjectStore.getState().reloadProjects();

      const state = useProjectStore.getState();
      expect(state.projects).toEqual(['/scope/cwd/p1', '/scope/cwd/p2']);
      expect(state.active).toBe('/scope/cwd/p1');
      expect(mockBackend.getProjects).toHaveBeenCalledWith({ cwd: '/scope/cwd' });
    });

    it('should set active project path', async () => {
      useProjectStore.setState({ scopeCwd: '/scope/cwd' });
      mockBackend.setActiveProject.mockResolvedValue({
        projects: ['/scope/cwd/p1', '/scope/cwd/p2'],
        active: '/scope/cwd/p2',
      });

      await useProjectStore.getState().setActive('/scope/cwd/p2');

      const state = useProjectStore.getState();
      expect(state.active).toBe('/scope/cwd/p2');
      expect(mockBackend.setActiveProject).toHaveBeenCalledWith({
        cwd: '/scope/cwd',
        path: '/scope/cwd/p2',
      });
    });

    it('should add project path', async () => {
      useProjectStore.setState({ scopeCwd: '/scope/cwd' });
      mockBackend.addProject.mockResolvedValue({
        projects: ['/scope/cwd/p1'],
        active: '/scope/cwd/p1',
      });

      const result = await useProjectStore.getState().addProject('/scope/cwd/p1');
      expect(result).toBe(true);

      const state = useProjectStore.getState();
      expect(state.projects).toEqual(['/scope/cwd/p1']);
    });

    it('should remove project path', async () => {
      useProjectStore.setState({
        scopeCwd: '/scope/cwd',
        projects: ['/scope/cwd/p1', '/scope/cwd/p2'],
      });
      mockBackend.removeProject.mockResolvedValue({
        projects: ['/scope/cwd/p1'],
        active: '/scope/cwd/p1',
      });

      await useProjectStore.getState().removeProject('/scope/cwd/p2');

      const state = useProjectStore.getState();
      expect(state.projects).toEqual(['/scope/cwd/p1']);
    });
  });

  describe('directory selection and modals', () => {
    it('should manage modal open/close states', () => {
      const store = useProjectStore.getState();
      store.openModal('/default/path');

      let state = useProjectStore.getState();
      expect(state.showModal).toBe(true);
      expect(state.modalPath).toBe('/default/path');

      store.closeModal();
      state = useProjectStore.getState();
      expect(state.showModal).toBe(false);
    });

    it('should browse directory using native dialog', async () => {
      useProjectStore.setState({ modalPath: '/old/path' });
      mockBackend.selectProjectDir.mockResolvedValue('/new/path');

      await useProjectStore.getState().browseDirectory();

      const state = useProjectStore.getState();
      expect(state.modalPath).toBe('/new/path');
      expect(state.browsing).toBe(false);
      expect(mockBackend.selectProjectDir).toHaveBeenCalledWith('/old/path');
    });
  });
});
