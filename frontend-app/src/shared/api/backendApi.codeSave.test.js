import { expect, it, vi } from 'vitest';
import { createBackendApi, RPC_METHODS } from './backendApi.js';

const validRequest = Object.freeze({
  filePath: 'src/App.jsx',
  content: 'export default App;',
  project: '/repo/app',
  previewMode: 'full',
  contentVersion: 'sha256:app-v1',
});

const validResponse = Object.freeze({
  ok: true,
  filePath: '/repo/app/src/App.jsx',
  relative: 'src/App.jsx',
  totalLines: 1,
  contentVersion: 'sha256:app-v2',
});

it('forwards the full-preview optimistic save contract', async () => {
  const callAPI = vi.fn().mockResolvedValue(validResponse);
  const api = createBackendApi({ callAPI });

  await expect(api.saveCodeFile(validRequest)).resolves.toEqual(validResponse);

  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_CODE_SAVE, validRequest);
});

it.each([
  ['missing previewMode', { previewMode: undefined }, 'previewMode is required'],
  ['blank previewMode', { previewMode: ' ' }, 'previewMode is required'],
  ['non-string previewMode', { previewMode: 7 }, 'previewMode must be a string'],
  ['snippet previewMode', { previewMode: 'snippet' }, 'previewMode must be full'],
  ['missing contentVersion', { contentVersion: undefined }, 'contentVersion is required'],
  ['blank contentVersion', { contentVersion: ' ' }, 'contentVersion is required'],
  ['non-string contentVersion', { contentVersion: 7 }, 'contentVersion must be a string'],
])('rejects %s before calling the backend', (_name, overrides, message) => {
  const callAPI = vi.fn();
  const api = createBackendApi({ callAPI });

  expect(() => api.saveCodeFile({ ...validRequest, ...overrides })).toThrow(message);
  expect(callAPI).not.toHaveBeenCalled();
});

it.each([
  ['missing', { contentVersion: undefined }],
  ['blank', { contentVersion: '' }],
  ['non-string', { contentVersion: 7 }],
])('rejects a %s save response contentVersion', async (_name, overrides) => {
  const api = createBackendApi({
    callAPI: vi.fn().mockResolvedValue({ ...validResponse, ...overrides }),
  });

  await expect(api.saveCodeFile(validRequest))
    .rejects.toThrow('ui/code/save response contentVersion must be a non-empty string');
});
