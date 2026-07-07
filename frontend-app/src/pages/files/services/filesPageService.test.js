import { describe, expect, it, vi } from 'vitest';
import { createFilesPageService } from './filesPageService.js';

function createApi(overrides = {}) {
  return {
    deleteSharedFile: vi.fn().mockResolvedValue({ deleted: true }),
    listSharedFilesDashboard: vi.fn().mockResolvedValue({
      files: [],
      finalOutputRefs: [],
      retention: { items: [], protectedCount: 0, cleanupCandidateCount: 0 },
    }),
    openSharedFile: vi.fn().mockResolvedValue({ opened: true }),
    readSharedFile: vi.fn().mockResolvedValue({ path: 'notes/a.md', content: 'hello' }),
    saveTextFile: vi.fn().mockResolvedValue('/tmp/notes.md'),
    ...overrides,
  };
}

describe('filesPageService', () => {
  it('loads the shared files dashboard through the file service module', async () => {
    const api = createApi();
    const service = createFilesPageService(api);

    await expect(service.listSharedFilesDashboard()).resolves.toMatchObject({ files: [] });

    expect(api.listSharedFilesDashboard).toHaveBeenCalledWith();
  });

  it('rejects malformed dashboard responses', async () => {
    const api = createApi({ listSharedFilesDashboard: vi.fn().mockResolvedValue(null) });
    const service = createFilesPageService(api);

    await expect(service.listSharedFilesDashboard()).rejects.toThrow('shared files dashboard response must be an object');
  });

  it('reads shared file detail through the existing file service module', async () => {
    const api = createApi({ readSharedFile: vi.fn().mockResolvedValue({ path: 'notes/a.md', content: 'hello' }) });
    const service = createFilesPageService(api);

    await expect(service.readSharedFile('notes/a.md')).resolves.toEqual({ path: 'notes/a.md', content: 'hello' });

    expect(api.readSharedFile).toHaveBeenCalledWith({ path: 'notes/a.md' }, undefined);
  });

  it('keeps readSharedFile request shape stable', async () => {
    const calls = [];
    const api = createApi({
      readSharedFile: vi.fn((payload, fallbackFile) => {
        calls.push([payload, fallbackFile]);
        return Promise.resolve({ path: payload.path, content: '' });
      }),
    });
    const service = createFilesPageService(api);

    await service.readSharedFile('src/App.jsx', { size: 10 });

    expect(calls).toEqual([[{ path: 'src/App.jsx' }, { size: 10 }]]);
  });

  it('fails fast for malformed read requests and responses', async () => {
    const api = createApi();
    const service = createFilesPageService(api);

    await expect(service.readSharedFile('')).rejects.toThrow('file path is required');
    await expect(service.readSharedFile(12)).rejects.toThrow('file path is required');
    expect(api.readSharedFile).not.toHaveBeenCalled();

    api.readSharedFile.mockResolvedValueOnce({});
    await expect(service.readSharedFile('notes/a.md')).rejects.toThrow('file path is required');
  });

  it('opens and deletes shared files with path DTOs', async () => {
    const api = createApi();
    const service = createFilesPageService(api);

    await service.openSharedFile('notes/a.md');
    await service.deleteSharedFile('notes/a.md');

    expect(api.openSharedFile).toHaveBeenCalledWith({ path: 'notes/a.md' });
    expect(api.deleteSharedFile).toHaveBeenCalledWith({ path: 'notes/a.md' });
  });

  it('fails fast before open or delete when the path is blank or non-string', () => {
    const api = createApi();
    const service = createFilesPageService(api);

    expect(() => service.openSharedFile(' ')).toThrow('file path is required');
    expect(() => service.deleteSharedFile(null)).toThrow('file path is required');

    expect(api.openSharedFile).not.toHaveBeenCalled();
    expect(api.deleteSharedFile).not.toHaveBeenCalled();
  });

  it('saves text files with the native save DTO', async () => {
    const api = createApi();
    const service = createFilesPageService(api);

    await service.saveTextFile({ defaultPath: '/tmp', defaultFilename: ' notes.md ', content: 'hello' });

    expect(api.saveTextFile).toHaveBeenCalledWith({
      defaultPath: '/tmp',
      defaultFilename: 'notes.md',
      content: 'hello',
    });
  });

  it('fails fast for malformed save text file DTOs', () => {
    const api = createApi();
    const service = createFilesPageService(api);

    expect(() => service.saveTextFile(null)).toThrow('file save params are required');
    expect(() => service.saveTextFile({ defaultFilename: '', content: 'hello' })).toThrow('default filename is required');
    expect(() => service.saveTextFile({ defaultFilename: 7, content: 'hello' })).toThrow('default filename is required');
    expect(() => service.saveTextFile({ defaultPath: 7, defaultFilename: 'notes.md', content: 'hello' })).toThrow('default path must be a string');
    expect(() => service.saveTextFile({ defaultFilename: 'notes.md' })).toThrow('file content is required');
    expect(() => service.saveTextFile({ defaultFilename: 'notes.md', content: 7 })).toThrow('file content is required');

    expect(api.saveTextFile).not.toHaveBeenCalled();
  });
});
