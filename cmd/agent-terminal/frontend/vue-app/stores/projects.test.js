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

describe('projectOptions label disambiguation', () => {
    let store;
    beforeEach(() => {
        store = useProjectStore();
        // reset state
        store.state.projects = [];
        store.state.active = '.';
    });

    it('keeps short label (slice -2) when no collision', () => {
        store.state.projects = ['/Users/mima0000/Desktop/wj/go-agent-v2'];
        const options = store.projectOptions.value;
        // first option is always '当前目录 (.)'
        expect(options[0].label).toBe('当前目录 (.)');
        expect(options[1].label).toBe('wj/go-agent-v2');
        expect(options[1].value).toBe('/Users/mima0000/Desktop/wj/go-agent-v2');
    });

    it('disambiguates two paths with same last 2 segments', () => {
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
        store.state.projects = [
            '/Users/mima0000/Desktop/wj/go-agent-v2',
            '/Users/mima0000/Desktop/wj/wjboot-v2',
        ];
        const options = store.projectOptions.value;
        expect(options[1].label).toBe('wj/go-agent-v2');
        expect(options[2].label).toBe('wj/wjboot-v2');
    });

    it('values remain full paths regardless of disambiguation', () => {
        store.state.projects = [
            '/Users/mima0000/Desktop/wj/go-agent-v2',
            '/Users/mima0000/.worktrees/tag-v1.03/wj/go-agent-v2',
        ];
        const options = store.projectOptions.value;
        expect(options[1].value).toBe('/Users/mima0000/Desktop/wj/go-agent-v2');
        expect(options[2].value).toBe('/Users/mima0000/.worktrees/tag-v1.03/wj/go-agent-v2');
    });

    it('does not leak _segments into the final option objects', () => {
        store.state.projects = ['/a/b/c/d'];
        const options = store.projectOptions.value;
        for (const opt of options) {
            expect(opt).not.toHaveProperty('_segments');
        }
    });
});
