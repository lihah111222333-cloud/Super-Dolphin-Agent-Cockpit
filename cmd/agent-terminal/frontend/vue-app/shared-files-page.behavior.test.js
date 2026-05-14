// @ts-nocheck
import { describe, expect, it, vi } from 'vitest';
import { nextTick, reactive } from '../lib/vue.esm-browser.prod.js';

const apiMock = vi.hoisted(() => ({
  callAPI: vi.fn(async () => null),
}));

vi.mock('./services/api.js', () => ({
  callAPI: apiMock.callAPI,
}));

import { SharedFilesPage } from './pages/SharedFilesPage.js';

describe('SharedFilesPage final output highlighting', () => {
  it('marks final output files and can filter to them', async () => {
    const props = reactive({
      cwd: '/repo',
      files: [
        { path: 'reports/daily-brief.pptx', content: 'deck', updated_at: '2026-05-14T00:00:00Z' },
        { path: 'scratch/intermediate.json', content: '{}', updated_at: '2026-05-14T00:01:00Z' },
      ],
      finalOutputRefs: [
        { path: 'reports/daily-brief.pptx', runKey: 'run-1', dagKey: 'dag-1', sourceNodeKey: 'report' },
      ],
    });

    const vm = SharedFilesPage.setup(props, { emit: vi.fn() });

    expect(vm.finalOutputCount.value).toBe(1);
    expect(vm.isFinalOutputFile(props.files[0])).toBe(true);
    expect(vm.isFinalOutputFile(props.files[1])).toBe(false);

    vm.toggleFinalOnly();
    await nextTick();

    expect(vm.filteredItems.value.map((item) => item.path)).toEqual(['reports/daily-brief.pptx']);

    props.finalOutputRefs = [];
    await nextTick();

    expect(vm.showFinalOnly.value).toBe(false);
    expect(vm.filteredItems.value.map((item) => item.path)).toEqual(['scratch/intermediate.json', 'reports/daily-brief.pptx']);
    expect(SharedFilesPage.template).toContain('shared-files-final-toggle');
    expect(SharedFilesPage.template).toContain('最终产物');
  });

  it('keeps detail reads path based and independent from final output metadata', async () => {
    apiMock.callAPI.mockImplementation(async (method, params) => {
      if (method === 'ui/preferences/get') return null;
      if (method === 'ui/memory/shared-file/get') {
        return { path: params.path, content: 'full content', updatedBy: 'agent', updatedAt: '2026-05-14T00:00:00Z' };
      }
      return null;
    });
    const props = reactive({
      cwd: '/repo',
      files: [{ path: 'reports/daily-brief.pptx', content: 'deck' }],
      finalOutputRefs: [{ path: 'reports/daily-brief.pptx', runKey: 'run-1' }],
    });

    const vm = SharedFilesPage.setup(props, { emit: vi.fn() });
    await vm.openViewer(props.files[0]);

    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/memory/shared-file/get', { path: 'reports/daily-brief.pptx' });
    expect(vm.selectedFile.content).toBe('full content');
  });

  it('counts only final output refs that are present in the current file list', async () => {
    const props = reactive({
      cwd: '/repo',
      files: [{ path: 'scratch/intermediate.json', content: '{}' }],
      finalOutputRefs: [{ path: 'reports/missing-final.pptx', runKey: 'run-1' }],
    });

    const vm = SharedFilesPage.setup(props, { emit: vi.fn() });

    expect(vm.finalOutputCount.value).toBe(0);
    expect(vm.isFinalOutputFile(props.files[0])).toBe(false);

    vm.toggleFinalOnly();
    await nextTick();

    expect(vm.showFinalOnly.value).toBe(false);
    expect(vm.filteredItems.value.map((item) => item.path)).toEqual(['scratch/intermediate.json']);
  });

  it('does not open delete confirmation for final output files', async () => {
    const props = reactive({
      cwd: '/repo',
      files: [{ path: 'reports/daily-brief.pptx', content: 'deck' }],
      finalOutputRefs: [{ path: 'reports/daily-brief.pptx', runKey: 'run-1' }],
      sharedFileRetention: {
        items: [{ path: 'reports/daily-brief.pptx', protected: true, reason: 'final_output' }],
      },
    });

    const vm = SharedFilesPage.setup(props, { emit: vi.fn() });
    vm.askDelete(props.files[0]);
    await nextTick();

    expect(vm.confirmDeletePath.value).toBe('');
    expect(vm.notice.level).toBe('error');
    expect(vm.notice.message).toContain('最终产物');
    expect(apiMock.callAPI).not.toHaveBeenCalledWith('ui/memory/shared-file/delete', { path: 'reports/daily-brief.pptx' });
  });
});
