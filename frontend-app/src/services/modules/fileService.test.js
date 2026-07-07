import { afterEach, describe, expect, it, vi } from 'vitest';

afterEach(() => {
  vi.clearAllMocks();
  vi.doUnmock('../../shared/api/backendApi.js');
  vi.doUnmock('../../adapters/fileAdapter.js');
  vi.resetModules();
});

describe('fileService', () => {
  it('does not synthesize an empty fallback for readSharedFile', async () => {
    vi.resetModules();
    const readSharedFileBackend = vi.fn().mockResolvedValue({ file: { path: 'notes/a.md', content: 'hello' } });
    const adaptSharedFileDetail = vi.fn((response, fallbackFile) => ({ path: response.file.path, fallbackFile }));
    vi.doMock('../../shared/api/backendApi.js', () => ({
      deleteSharedFile: vi.fn(),
      listSharedFiles: vi.fn(),
      openSharedFile: vi.fn(),
      readSharedFile: readSharedFileBackend,
      saveTextFile: vi.fn(),
    }));
    vi.doMock('../../adapters/fileAdapter.js', () => ({
      adaptSharedFileDetail,
      adaptSharedFilesDashboard: vi.fn(),
    }));

    const { readSharedFile } = await import('./fileService.js');
    await readSharedFile({ path: 'notes/a.md' });

    expect(adaptSharedFileDetail).toHaveBeenCalledWith(expect.anything(), undefined);
  });

  it('rejects malformed shared file detail instead of adapting an empty fallback object', async () => {
    vi.resetModules();
    const readSharedFileBackend = vi.fn().mockResolvedValue(undefined);
    const adaptSharedFileDetail = vi.fn(() => {
      throw new Error('shared file detail response must be an object');
    });
    vi.doMock('../../shared/api/backendApi.js', () => ({
      deleteSharedFile: vi.fn(),
      listSharedFiles: vi.fn(),
      openSharedFile: vi.fn(),
      readSharedFile: readSharedFileBackend,
      saveTextFile: vi.fn(),
    }));
    vi.doMock('../../adapters/fileAdapter.js', () => ({
      adaptSharedFileDetail,
      adaptSharedFilesDashboard: vi.fn(),
    }));

    const { readSharedFile } = await import('./fileService.js');

    await expect(readSharedFile({ path: 'notes/a.md' })).rejects.toThrow('shared file detail response must be an object');
    expect(adaptSharedFileDetail).toHaveBeenCalledWith(undefined, undefined);
  });
});
