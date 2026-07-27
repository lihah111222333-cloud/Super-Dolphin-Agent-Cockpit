import { expect, it, vi } from 'vitest';
import { saveCodePreviewChanges } from './useCodePreviewController.jsx';

const service = vi.hoisted(() => ({
  saveCodeFile: vi.fn(),
}));

vi.mock('../services/chatCodeService.js', () => ({
  locateCodeFile: vi.fn(),
  openCodeFile: vi.fn(),
  openPath: vi.fn(),
  saveCodeFile: service.saveCodeFile,
}));

it('rotates the optimistic content version across consecutive saves', async () => {
  service.saveCodeFile
    .mockResolvedValueOnce({
      filePath: '/repo/app/src/a.js',
      relative: 'src/a.js',
      totalLines: 1,
      contentVersion: 'sha256:v2',
    })
    .mockResolvedValueOnce({
      filePath: '/repo/app/src/a.js',
      relative: 'src/a.js',
      totalLines: 1,
      contentVersion: 'sha256:v3',
    });
  let state = {
    filePath: '/repo/app/src/a.js',
    relative: 'src/a.js',
    scopeKey: 'project-scope',
    saving: false,
    editable: true,
    image: false,
    loading: false,
    draft: 'const version = 2;',
    content: 'const version = 1;',
    previewKind: 'code',
    previewMode: 'full',
    contentVersion: 'sha256:v1',
    editing: true,
  };
  const setCodePreview = (next) => {
    state = typeof next === 'function' ? next(state) : next;
  };
  const baseOptions = {
    isCurrentPreviewRequest: () => true,
    previewRequestSeqRef: { current: 1 },
    previewScopeKey: 'project-scope',
    projectPath: '/repo/app',
    projects: ['/repo/app'],
    setCodePreview,
  };

  await saveCodePreviewChanges({ ...baseOptions, codePreview: state });
  expect(state.contentVersion).toBe('sha256:v2');

  state = { ...state, draft: 'const version = 3;', editing: true };
  await saveCodePreviewChanges({ ...baseOptions, codePreview: state });

  expect(service.saveCodeFile).toHaveBeenNthCalledWith(1, expect.objectContaining({
    content: 'const version = 2;',
    contentVersion: 'sha256:v1',
  }));
  expect(service.saveCodeFile).toHaveBeenNthCalledWith(2, expect.objectContaining({
    content: 'const version = 3;',
    contentVersion: 'sha256:v2',
  }));
  expect(state.contentVersion).toBe('sha256:v3');
});
