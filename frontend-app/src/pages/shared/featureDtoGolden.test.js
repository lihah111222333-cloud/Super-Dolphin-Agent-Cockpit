import { describe, expect, it, vi } from 'vitest';
import { createFilesPageService } from '../files/services/filesPageService.js';
import { createObservabilityPageService } from '../observability/services/observabilityPageService.js';

function capturedApi(methods) {
  const calls = [];
  const api = {};
  for (const method of methods) {
    api[method] = vi.fn((payload) => {
      calls.push({ method, payload });
      return Promise.resolve({ ok: true });
    });
  }
  return { api, calls };
}

async function expectDtoGolden({ factory, methods, method, input, expectedPayload }) {
  const { api, calls } = capturedApi(methods);
  const service = factory(api);

  expect(service[method]).toEqual(expect.any(Function));
  await service[method](input);

  expect(calls).toEqual([{ method, payload: expectedPayload }]);
}

async function expectSyncDtoError({ factory, methods, method, input, message }) {
  const { api, calls } = capturedApi(methods);
  const service = factory(api);
  let thrown = null;
  let returned;
  let asyncError = null;

  try {
    returned = service[method](input);
  } catch (error) {
    thrown = error;
  }
  if (returned && typeof returned.catch === 'function') {
    asyncError = await returned.then(
      () => null,
      (error) => error,
    );
  }

  expect(thrown?.message).toBe(message);
  expect(returned).toBeUndefined();
  expect(asyncError?.message).toBeUndefined();
  expect(calls).toEqual([]);
}

describe('feature service DTO golden harness', () => {
  it('captures files saveTextFile normalized DTOs', async () => {
    await expectDtoGolden({
      factory: createFilesPageService,
      methods: ['saveTextFile'],
      method: 'saveTextFile',
      input: { defaultPath: '/tmp/notes', defaultFilename: ' notes.md ', content: 'hello' },
      expectedPayload: { defaultPath: '/tmp/notes', defaultFilename: 'notes.md', content: 'hello' },
    });

    await expectSyncDtoError({
      factory: createFilesPageService,
      methods: ['saveTextFile'],
      method: 'saveTextFile',
      input: { defaultFilename: 'notes.md' },
      message: 'file content is required',
    });
  });

  it('captures observability listObservabilityRecent normalized DTOs', async () => {
    await expectDtoGolden({
      factory: createObservabilityPageService,
      methods: ['listObservabilityRecent'],
      method: 'listObservabilityRecent',
      input: { limit: '25', status: 'error', traceId: '' },
      expectedPayload: { limit: 25, status: 'error', traceId: '' },
    });

    await expectSyncDtoError({
      factory: createObservabilityPageService,
      methods: ['listObservabilityRecent'],
      method: 'listObservabilityRecent',
      input: { limit: 'unsupported' },
      message: 'limit must be a positive integer',
    });
  });

});
