import { describe, expect, it, vi } from 'vitest';
import { createObservabilityPageService } from './observabilityPageService.js';

function createApi() {
  return {
    copyTextToClipboard: vi.fn().mockResolvedValue(true),
    getObservabilityTrace: vi.fn().mockResolvedValue({ events: [] }),
    listObservabilityRecent: vi.fn().mockResolvedValue({ events: [] }),
  };
}

describe('observabilityPageService', () => {
  it('normalizes positive string limits before listing recent events', async () => {
    const api = createApi();
    const service = createObservabilityPageService(api);

    await service.listObservabilityRecent({ limit: '25', status: 'error', traceId: '' });

    expect(api.listObservabilityRecent).toHaveBeenCalledWith({ limit: 25, status: 'error', traceId: '' });
  });

  it('omits blank limits for recent event queries', async () => {
    const api = createApi();
    const service = createObservabilityPageService(api);

    await service.listObservabilityRecent({ limit: '   ', status: 'ok' });

    expect(api.listObservabilityRecent).toHaveBeenCalledWith({ status: 'ok' });
  });

  it('rejects invalid or non-positive limits', async () => {
    const service = createObservabilityPageService(createApi());

    await expect(service.listObservabilityRecent({ limit: 'abc' })).rejects.toThrow('limit must be a positive integer');
    await expect(service.listObservabilityRecent({ limit: 0 })).rejects.toThrow('limit must be a positive integer');
    await expect(service.getObservabilityTrace({ traceId: 'trace-1', limit: '-1' })).rejects.toThrow('limit must be a positive integer');
  });

  it('requires trace ids before loading a trace', async () => {
    const service = createObservabilityPageService(createApi());

    await expect(service.getObservabilityTrace({ traceId: '  ', limit: '5' })).rejects.toThrow('traceId is required');
  });

  it('normalizes trace ids and limits before loading trace details', async () => {
    const api = createApi();
    const service = createObservabilityPageService(api);

    await service.getObservabilityTrace({ traceId: ' trace-1 ', limit: '5' });

    expect(api.getObservabilityTrace).toHaveBeenCalledWith({ traceId: 'trace-1', limit: 5 });
  });

  it('requires clipboard text before copying', async () => {
    const service = createObservabilityPageService(createApi());

    await expect(service.copyTextToClipboard('')).rejects.toThrow('text is required');
  });
});
