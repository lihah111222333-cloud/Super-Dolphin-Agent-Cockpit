// @ts-nocheck
import { describe, it, expect, vi, beforeEach } from 'vitest';

// Mock callAPI and selectProjectDir before importing the store
vi.mock('../services/api.js', () => ({
    callAPI: vi.fn(async () => ({ projects: [], active: '.' })),
    selectProjectDir: vi.fn(async () => ''),
}));
vi.mock('../services/log.js', () => ({
    logDebug: vi.fn(),
    logInfo: vi.fn(),
    logWarn: vi.fn(),
}));

import { useProjectStore } from './projects.js';
import { callAPI, selectProjectDir } from '../services/api.js';

describe('projectOptions label disambiguation', () => {
    let store;
    beforeEach(() => {
        store = useProjectStore();
        vi.mocked(callAPI).mockReset().mockResolvedValue({ projects: [], active: '.' });
        vi.mocked(selectProjectDir).mockReset().mockResolvedValue('');
        // reset state
        store.state.projects = [];
        store.state.active = '.';
        store.state.showModal = false;
        store.state.modalPath = '';
        store.state.browsing = false;
        store.state.scopeCwd = '';
    });

    it('keeps short label (slice -2) when no collision', () => {
        store.setScopeCwd('/Users/mima0000/Desktop/wj/go-agent-v2');
        store.state.projects = ['/Users/mima0000/Desktop/wj/go-agent-v2'];
        const options = store.projectOptions.value;
        expect(options[0].label).toBe('当前目录 (.)');
        expect(options[0].full).toBe('/Users/mima0000/Desktop/wj/go-agent-v2');
        expect(options[1].label).toBe('wj/go-agent-v2');
        expect(options[1].value).toBe('/Users/mima0000/Desktop/wj/go-agent-v2');
    });

    it('exposes current directory placeholder when runtime cwd is missing', () => {
        store.state.projects = ['/Users/mima0000/Desktop/wj/go-agent-v2'];

        const options = store.projectOptions.value;

        expect(options[0].label).toBe('当前目录 (.)');
        expect(options[0].full).toBe('.');
        expect(options[1].label).toBe('wj/go-agent-v2');
        expect(options.some((option) => option.value === '.')).toBe(true);
    });

    it('disambiguates two paths with same last 2 segments', () => {
        store.setScopeCwd('/scope');
        store.state.projects = [
            '/Users/mima0000/Desktop/wj/go-agent-v2',
            '/Users/mima0000/.worktrees/tag-v1.03/wj/go-agent-v2',
        ];
        const options = store.projectOptions.value;
        const labels = options.slice(1).map((o) => o.label);
        // labels should be unique
        expect(new Set(labels).size).toBe(labels.length);
        // both should contain 'go-agent-v2'
        expect(labels.every((l) => l.includes('go-agent-v2'))).toBe(true);
        // they should have expanded beyond 2 segments
        expect(labels[0]).toBe('Desktop/wj/go-agent-v2');
        expect(labels[1]).toBe('tag-v1.03/wj/go-agent-v2');
    });

    it('disambiguates three colliding paths', () => {
        store.setScopeCwd('/scope');
        store.state.projects = [
            '/a/b/wj/go-agent-v2',
            '/c/d/wj/go-agent-v2',
            '/e/f/wj/go-agent-v2',
        ];
        const options = store.projectOptions.value;
        const labels = options.slice(1).map((o) => o.label);
        expect(new Set(labels).size).toBe(3);
    });

    it('does not modify labels when all are already unique', () => {
        store.setScopeCwd('/scope');
        store.state.projects = [
            '/Users/mima0000/Desktop/wj/go-agent-v2',
            '/Users/mima0000/Desktop/wj/wjboot-v2',
        ];
        const options = store.projectOptions.value;
        expect(options[1].label).toBe('wj/go-agent-v2');
        expect(options[2].label).toBe('wj/wjboot-v2');
    });

    it('values remain full paths regardless of disambiguation', () => {
        store.setScopeCwd('/scope');
        store.state.projects = [
            '/Users/mima0000/Desktop/wj/go-agent-v2',
            '/Users/mima0000/.worktrees/tag-v1.03/wj/go-agent-v2',
        ];
        const options = store.projectOptions.value;
        expect(options[1].value).toBe('/Users/mima0000/Desktop/wj/go-agent-v2');
        expect(options[2].value).toBe('/Users/mima0000/.worktrees/tag-v1.03/wj/go-agent-v2');
    });

    it('passes the current modal path to the native directory picker', async () => {
        store.state.active = '/workspace/active-project';
        store.state.modalPath = '/workspace/custom-seed';
        vi.mocked(selectProjectDir).mockResolvedValue('/workspace/selected-project');

        await store.browseDirectory();

        expect(selectProjectDir).toHaveBeenCalledWith('/workspace/custom-seed');
        expect(store.state.modalPath).toBe('/workspace/selected-project');
        expect(store.state.browsing).toBe(false);
    });

    it('rethrows native directory picker failures', async () => {
        store.state.modalPath = '/workspace/custom-seed';
        vi.mocked(selectProjectDir).mockRejectedValueOnce(new Error('picker failed'));

        await expect(store.browseDirectory()).rejects.toThrow('picker failed');

        expect(store.state.browsing).toBe(false);
    });

    it('does not leak _segments into the final option objects', () => {
        store.setScopeCwd('/scope');
        store.state.projects = ['/a/b/c/d'];
        const options = store.projectOptions.value;
        for (const opt of options) {
            expect(opt).not.toHaveProperty('_segments');
        }
    });

    it('scopes project state RPCs to the current window cwd', async () => {
        vi.mocked(callAPI).mockResolvedValue({ projects: ['/worktree'], active: '/worktree' });

        store.setScopeCwd('/worktree');
        await store.reloadProjects();
        await store.setActive('/worktree');
        await store.addProject('/another-worktree');
        await store.removeProject('/another-worktree');

        expect(callAPI).toHaveBeenCalledWith('ui/projects/get', { cwd: '/worktree' });
        expect(callAPI).toHaveBeenCalledWith('ui/projects/setActive', { path: '/worktree', cwd: '/worktree' });
        expect(callAPI).toHaveBeenCalledWith('ui/projects/add', { path: '/another-worktree', cwd: '/worktree' });
        expect(callAPI).toHaveBeenCalledWith('ui/projects/remove', { path: '/another-worktree', cwd: '/worktree' });
    });

    it('uses the first selected project as scope and active project when runtime cwd is missing', async () => {
        vi.mocked(callAPI).mockImplementation(async (method, params) => {
            if (method === 'ui/projects/add') {
                expect(params).toEqual({ path: '/Users/ai/Desktop/sd', cwd: '/Users/ai/Desktop/sd' });
                return { projects: ['/Users/ai/Desktop/sd'], active: '.' };
            }
            if (method === 'ui/projects/setActive') {
                expect(params).toEqual({ path: '/Users/ai/Desktop/sd', cwd: '/Users/ai/Desktop/sd' });
                return { projects: ['/Users/ai/Desktop/sd'], active: '/Users/ai/Desktop/sd' };
            }
            throw new Error(`unexpected method ${method}`);
        });

        const ok = await store.addProject('/Users/ai/Desktop/sd');

        expect(ok).toBe(true);
        expect(store.state.scopeCwd).toBe('/Users/ai/Desktop/sd');
        expect(store.state.active).toBe('/Users/ai/Desktop/sd');
    });

    it('uses the selected explicit project as scope when runtime cwd is missing', async () => {
        store.state.projects = ['/Users/ai/Desktop/sd'];
        vi.mocked(callAPI).mockImplementation(async (method, params) => {
            if (method === 'ui/projects/setActive') {
                expect(params).toEqual({ path: '/Users/ai/Desktop/sd', cwd: '/Users/ai/Desktop/sd' });
                return { projects: ['/Users/ai/Desktop/sd'], active: '/Users/ai/Desktop/sd' };
            }
            throw new Error(`unexpected method ${method}`);
        });

        await store.setActive('/Users/ai/Desktop/sd');

        expect(store.state.scopeCwd).toBe('/Users/ai/Desktop/sd');
        expect(store.state.active).toBe('/Users/ai/Desktop/sd');
    });

    it('fails fast when project RPC scope cwd is missing', async () => {
        await expect(store.reloadProjects()).rejects.toThrow('project scope cwd is required');
        expect(callAPI).not.toHaveBeenCalled();
    });

    it('fails fast on malformed project state responses', async () => {
        store.setScopeCwd('/worktree');
        vi.mocked(callAPI).mockResolvedValueOnce({});

        await expect(store.reloadProjects()).rejects.toThrow('project state projects must be an array');

        vi.mocked(callAPI).mockResolvedValueOnce({ projects: [], active: 123 });
        await expect(store.reloadProjects()).rejects.toThrow('project state active must be a string');

        vi.mocked(callAPI).mockResolvedValueOnce({ projects: ['/other'], active: '/missing' });
        await expect(store.reloadProjects()).rejects.toThrow('project state active path is not in projects');
    });
});
