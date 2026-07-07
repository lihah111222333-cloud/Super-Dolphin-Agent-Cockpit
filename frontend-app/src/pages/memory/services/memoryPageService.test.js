import { describe, expect, it, vi } from 'vitest';
import { createMemoryPageService } from './memoryPageService.js';

function createApi(overrides = {}) {
  return {
    deleteMemoryEntry: vi.fn().mockResolvedValue({ deleted: true }),
    fetchMemoryDashboard: vi.fn().mockResolvedValue({ entries: [] }),
    getMemoryConsolidationStatus: vi.fn().mockResolvedValue({ status: 'running' }),
    getMemoryEntry: vi.fn().mockResolvedValue({ content: 'memory' }),
    ignoreMemorySimilarity: vi.fn().mockResolvedValue({ ignored: true }),
    mergeMemoryEntries: vi.fn().mockResolvedValue({ merged: true }),
    setMemoryAutoDreamIntent: vi.fn().mockResolvedValue({ ok: true }),
    startConsolidateMemorySimilarities: vi.fn().mockResolvedValue({ status: 'running', jobId: 'job-1' }),
    upsertMemoryEntry: vi.fn().mockResolvedValue({ path: 'memory.md' }),
    ...overrides,
  };
}

function expectNoApiCalls(api) {
  for (const fn of Object.values(api)) {
    expect(fn).not.toHaveBeenCalled();
  }
}

describe('memoryPageService', () => {
  it('forwards memory dashboard loads with options unchanged', async () => {
    const api = createApi();
    const service = createMemoryPageService(api);
    const controller = new AbortController();
    const options = { signal: controller.signal };

    await service.fetchMemoryDashboard('/repo', options);

    expect(api.fetchMemoryDashboard).toHaveBeenCalledWith('/repo', options);
  });

  it('keeps memory dashboard withSignal request shape stable', async () => {
    const api = createApi();
    const service = createMemoryPageService(api);
    const controller = new AbortController();

    await service.fetchMemoryDashboard.withSignal('/repo', controller.signal);

    expect(api.fetchMemoryDashboard).toHaveBeenCalledWith('/repo', { signal: controller.signal });
  });

  it('fails fast for blank dashboard cwd', () => {
    const api = createApi();
    const service = createMemoryPageService(api);

    expect(() => service.fetchMemoryDashboard(' ')).toThrow('cwd is required');
    expectNoApiCalls(api);
  });

  it('forwards memory entry detail requests with stable identity', async () => {
    const api = createApi();
    const service = createMemoryPageService(api);
    const payload = { cwd: '/repo', target: 'team', path: 'memory.md' };

    await service.getMemoryEntry(payload);

    expect(api.getMemoryEntry).toHaveBeenCalledWith(payload);
  });

  it('fails fast for malformed memory entry detail identity', () => {
    const api = createApi();
    const service = createMemoryPageService(api);

    expect(() => service.getMemoryEntry({ cwd: '/repo', target: '', path: 'memory.md' })).toThrow('target is required');
    expect(() => service.getMemoryEntry({ cwd: '/repo', target: 'team', path: '' })).toThrow('path is required');
    expectNoApiCalls(api);
  });

  it('forwards memory upserts with exact page payload shape', async () => {
    const api = createApi();
    const service = createMemoryPageService(api);
    const payload = {
      cwd: '/repo',
      target: 'private',
      existingPath: '',
      name: 'feedback-rule',
      description: 'write tests first',
      title: '',
      type: 'feedback',
      content: '规则',
    };

    await service.upsertMemoryEntry(payload);

    expect(api.upsertMemoryEntry).toHaveBeenCalledWith(payload);
  });

  it('keeps memory upsert DTO golden normalization stable', async () => {
    const api = createApi();
    const service = createMemoryPageService(api);

    await service.upsertMemoryEntry({
      cwd: ' /repo ',
      target: ' private ',
      existingPath: ' memory/existing.md ',
      name: ' feedback-rule ',
      description: ' write tests first ',
      title: '',
      type: ' feedback ',
      content: ' 规则 ',
    });

    expect(api.upsertMemoryEntry).toHaveBeenCalledWith({
      cwd: '/repo',
      target: 'private',
      existingPath: 'memory/existing.md',
      name: 'feedback-rule',
      description: 'write tests first',
      title: '',
      type: 'feedback',
      content: '规则',
    });
  });

  it('fails fast for malformed memory upsert identity and content', () => {
    const api = createApi();
    const service = createMemoryPageService(api);
    const base = {
      cwd: '/repo',
      target: 'private',
      existingPath: '',
      name: 'feedback-rule',
      description: 'write tests first',
      type: 'feedback',
      content: '规则',
    };

    expect(() => service.upsertMemoryEntry({ ...base, cwd: '' })).toThrow('cwd is required');
    expect(() => service.upsertMemoryEntry({ ...base, target: '' })).toThrow('target is required');
    const { existingPath, ...withoutPath } = base;
    expect(() => service.upsertMemoryEntry(withoutPath)).toThrow('path is required');
    expect(() => service.upsertMemoryEntry({ ...base, content: '' })).toThrow('content is required');
    expectNoApiCalls(api);
  });

  it('forwards memory deletion requests with stable identity', async () => {
    const api = createApi();
    const service = createMemoryPageService(api);
    const payload = { cwd: '/repo', target: 'private', path: 'memory.md' };

    await service.deleteMemoryEntry(payload);

    expect(api.deleteMemoryEntry).toHaveBeenCalledWith(payload);
  });

  it('fails fast for malformed deletion identity', () => {
    const api = createApi();
    const service = createMemoryPageService(api);

    expect(() => service.deleteMemoryEntry({ cwd: '', target: 'private', path: 'memory.md' })).toThrow('cwd is required');
    expect(() => service.deleteMemoryEntry({ cwd: '/repo', target: 'global', path: 'memory.md' })).toThrow('target must be private or team');
    expect(() => service.deleteMemoryEntry({ cwd: '/repo', target: 'private', path: '' })).toThrow('path is required');
    expectNoApiCalls(api);
  });

  it('forwards auto dream intent updates', async () => {
    const api = createApi();
    const service = createMemoryPageService(api);
    const payload = { cwd: '/repo', enabled: true };

    await service.setMemoryAutoDreamIntent(payload);

    expect(api.setMemoryAutoDreamIntent).toHaveBeenCalledWith(payload);
  });

  it('fails fast for malformed auto dream intent updates', () => {
    const api = createApi();
    const service = createMemoryPageService(api);

    expect(() => service.setMemoryAutoDreamIntent({ enabled: true })).toThrow('cwd is required');
    expect(() => service.setMemoryAutoDreamIntent({ cwd: '/repo' })).toThrow('enabled is required');
    expectNoApiCalls(api);
  });

  it('forwards merge and ignore requests with source and target identity', async () => {
    const api = createApi();
    const service = createMemoryPageService(api);
    const payload = { cwd: '/repo', targetA: 'private', pathA: 'a.md', targetB: 'team', pathB: 'b.md' };

    await service.mergeMemoryEntries(payload);
    await service.ignoreMemorySimilarity(payload);

    expect(api.mergeMemoryEntries).toHaveBeenCalledWith(payload);
    expect(api.ignoreMemorySimilarity).toHaveBeenCalledWith(payload);
  });

  it('keeps memory merge DTO golden identity validation stable', () => {
    const api = createApi();
    const service = createMemoryPageService(api);

    expect(() => service.mergeMemoryEntries({
      cwd: '/repo',
      targetA: 'private',
      pathA: ' memory/a.md ',
      targetB: 'private',
      pathB: 'memory/a.md',
    })).toThrow('source and target memory identity must be different');
    expect(api.mergeMemoryEntries).not.toHaveBeenCalled();
  });

  it('fails fast for missing or identical source and target similarity identity', () => {
    const api = createApi();
    const service = createMemoryPageService(api);

    expect(() => service.mergeMemoryEntries({ cwd: '/repo', targetA: 'private', pathA: 'a.md', targetB: 'team' })).toThrow('pathB is required');
    expect(() => service.ignoreMemorySimilarity({ cwd: '/repo', targetA: 'private', pathA: 'a.md', targetB: 'private', pathB: 'a.md' }))
      .toThrow('source and target memory identity must be different');
    expectNoApiCalls(api);
  });

  it('forwards consolidation start and status payloads', async () => {
    const api = createApi();
    const service = createMemoryPageService(api);
    const startPayload = { cwd: '/repo', provider: 'codex', model: 'gpt-5.5', codexModelProvider: 'openai' };
    const statusPayload = { cwd: '/repo', jobId: 'job-1' };

    await service.startConsolidateMemorySimilarities(startPayload);
    await service.getMemoryConsolidationStatus(statusPayload);

    expect(api.startConsolidateMemorySimilarities).toHaveBeenCalledWith(startPayload);
    expect(api.getMemoryConsolidationStatus).toHaveBeenCalledWith(statusPayload);
  });

  it('fails fast for malformed consolidation payloads', () => {
    const api = createApi();
    const service = createMemoryPageService(api);

    expect(() => service.startConsolidateMemorySimilarities({ cwd: '' })).toThrow('cwd is required');
    expect(() => service.getMemoryConsolidationStatus({ cwd: '/repo', jobId: '' })).toThrow('jobId is required');
    expectNoApiCalls(api);
  });
});
