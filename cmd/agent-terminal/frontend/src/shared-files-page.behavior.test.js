// @ts-nocheck
import { describe, expect, it, vi } from 'vitest';
import { nextTick, reactive } from '../lib/vue.esm-browser.prod.js';

const apiMock = vi.hoisted(() => ({
  callAPI: vi.fn(async () => null),
  saveTextFile: vi.fn(async () => ''),
}));

vi.mock('./services/api.js', () => ({
  callAPI: apiMock.callAPI,
  saveTextFile: apiMock.saveTextFile,
}));

import { SharedFilesPage } from './pages/SharedFilesPage.js';

describe('SharedFilesPage final output highlighting', () => {
  it('filters file artifacts with top category tabs', async () => {
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
    expect(vm.workFileCount.value).toBe(1);
    expect(vm.categoryTabs.value.map((tab) => [tab.key, tab.label, tab.count])).toEqual([
      ['all', '全部', 2],
      ['final', '最终产物', 1],
      ['work', '工作文件', 1],
    ]);
    expect(vm.visibleItems.value.map((item) => item.path)).toEqual([
      'scratch/intermediate.json',
      'reports/daily-brief.pptx',
    ]);

    vm.setFileCategory('final');
    await nextTick();

    expect(vm.finalOutputItems.value.map((item) => item.path)).toEqual(['reports/daily-brief.pptx']);
    expect(vm.workItems.value.map((item) => item.path)).toEqual(['scratch/intermediate.json']);
    expect(vm.visibleItems.value.map((item) => item.path)).toEqual(['reports/daily-brief.pptx']);

    props.finalOutputRefs = [];
    await nextTick();

    expect(vm.finalOutputItems.value).toEqual([]);
    expect(vm.workItems.value.map((item) => item.path)).toEqual(['scratch/intermediate.json', 'reports/daily-brief.pptx']);
    expect(vm.visibleItems.value).toEqual([]);
    expect(SharedFilesPage.template).toContain('shared-files-category-tabs');
    expect(SharedFilesPage.template).toContain('shared-files-category-tab-final');
    expect(SharedFilesPage.template).toContain('shared-files-category-tab-work');
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

    expect(vm.finalOutputItems.value).toEqual([]);
    expect(vm.workItems.value.map((item) => item.path)).toEqual(['scratch/intermediate.json']);
    expect(vm.visibleItems.value.map((item) => item.path)).toEqual(['scratch/intermediate.json']);
    vm.setFileCategory('final');
    await nextTick();
    expect(vm.visibleItems.value).toEqual([]);
    expect(vm.categoryEmptyTitle.value).toBe('无内容');
    expect(SharedFilesPage.template).toContain('shared-files-category-empty');
    expect(SharedFilesPage.template).toContain('shared-files-text-empty');
    expect(SharedFilesPage.template).not.toContain('data-testid="shared-files-category-empty" class="memory-empty"');
    expect(SharedFilesPage.template).not.toContain('shared-files-final-toggle');
    expect(SharedFilesPage.template).not.toContain('最终产物 {{ finalOutputCount }}');
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

  it('does not expose shared-file promotion to long-term memory', () => {
    expect(SharedFilesPage.template).not.toContain('shared-files-promote');
    expect(SharedFilesPage.template).not.toContain('提升为长期记忆');
    expect(SharedFilesPage.template).not.toContain('ui/memory/shared-file/promote');
  });

  it('uses product-facing file artifact copy instead of engineering shared-file copy', () => {
    expect(SharedFilesPage.template).toContain('<h2>文件产物</h2>');
    expect(SharedFilesPage.template).toContain('最终产物');
    expect(SharedFilesPage.template).toContain('文件预览');
    expect(SharedFilesPage.template).toContain('shared-files-viewer-modal');
    expect(SharedFilesPage.template).toContain('shared-files-viewer-meta');
    expect(SharedFilesPage.template).toContain('shared-files-content-preview');
    expect(SharedFilesPage.template).toContain('shared-files-viewer-export');
    expect(SharedFilesPage.template).toContain('shared-files-card-meta');
    expect(SharedFilesPage.template).toContain('fileRoleLabel(item)');
    expect(SharedFilesPage.template).not.toContain('共享文件 · Agent 协作中转站');
    expect(SharedFilesPage.template).not.toContain('搜索 path');
    expect(SharedFilesPage.template).not.toContain('shared_file_write');
    expect(SharedFilesPage.template).not.toContain('按 Path');
    expect(SharedFilesPage.template).not.toContain('shared-files-dirname');
    expect(SharedFilesPage.template).not.toContain("{{ item.updated_by || '-' }}");
    expect(SharedFilesPage.template).not.toContain('shared-files-callout');
    expect(SharedFilesPage.template).not.toContain('展开指引');
    expect(SharedFilesPage.template).not.toContain('打开记忆中心');
  });

  it('exports a file artifact through native save dialog using the basename and full content', async () => {
    apiMock.callAPI.mockImplementation(async (method, params) => {
      if (method === 'ui/preferences/get') return null;
      if (method === 'ui/memory/shared-file/get') {
        return { path: params.path, content: '# Final report\nready', updatedBy: 'agent', updatedAt: '2026-05-14T00:00:00Z' };
      }
      return null;
    });
    apiMock.saveTextFile.mockResolvedValue('/Users/me/Desktop/final-report.md');
    const props = reactive({
      cwd: '/repo',
      files: [{ path: 'reports/final-report.md', content: 'preview' }],
      finalOutputRefs: [{ path: 'reports/final-report.md', runKey: 'run-1' }],
    });

    const vm = SharedFilesPage.setup(props, { emit: vi.fn() });
    await vm.exportSharedFile(props.files[0]);

    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/memory/shared-file/get', { path: 'reports/final-report.md' });
    expect(apiMock.saveTextFile).toHaveBeenCalledWith({
      defaultPath: '/repo',
      defaultFilename: 'final-report.md',
      content: '# Final report\nready',
    });
    expect(vm.notice.level).toBe('info');
    expect(vm.notice.message).toContain('已保存到');
    expect(vm.notice.message).toContain('/Users/me/Desktop/final-report.md');
    expect(SharedFilesPage.template).toContain('shared-files-export-');
  });
});
