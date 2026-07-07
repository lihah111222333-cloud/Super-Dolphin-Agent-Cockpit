import { describe, expect, it, vi } from 'vitest';
import { createFilesPageService } from '../files/services/filesPageService.js';
import { createMemoryPageService } from '../memory/services/memoryPageService.js';
import { createObservabilityPageService } from '../observability/services/observabilityPageService.js';
import { createPromptPageService } from '../prompts/services/promptPageService.js';

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

  it('captures memory upsertMemoryEntry normalized DTOs', async () => {
    await expectDtoGolden({
      factory: createMemoryPageService,
      methods: ['upsertMemoryEntry'],
      method: 'upsertMemoryEntry',
      input: {
        cwd: ' /repo ',
        target: ' private ',
        existingPath: ' memory/old.md ',
        name: ' feedback-rule ',
        description: ' write tests first ',
        type: ' feedback ',
        content: ' keep DTOs owned by services ',
        title: 'untouched title',
      },
      expectedPayload: {
        cwd: '/repo',
        target: 'private',
        existingPath: 'memory/old.md',
        name: 'feedback-rule',
        description: 'write tests first',
        type: 'feedback',
        content: 'keep DTOs owned by services',
        title: 'untouched title',
      },
    });

    await expectSyncDtoError({
      factory: createMemoryPageService,
      methods: ['upsertMemoryEntry'],
      method: 'upsertMemoryEntry',
      input: {
        cwd: ' /repo ',
        target: ' private ',
        existingPath: '',
        name: ' feedback-rule ',
        description: ' write tests first ',
        type: ' feedback ',
        content: ' ',
      },
      message: 'content is required',
    });
  });

  it('captures memory mergeMemoryEntries identity validation', async () => {
    await expectDtoGolden({
      factory: createMemoryPageService,
      methods: ['mergeMemoryEntries'],
      method: 'mergeMemoryEntries',
      input: { cwd: ' /repo ', targetA: ' private ', pathA: ' a.md ', targetB: ' team ', pathB: ' b.md ' },
      expectedPayload: { cwd: '/repo', targetA: 'private', pathA: 'a.md', targetB: 'team', pathB: 'b.md' },
    });

    await expectSyncDtoError({
      factory: createMemoryPageService,
      methods: ['mergeMemoryEntries'],
      method: 'mergeMemoryEntries',
      input: { cwd: '/repo', targetA: 'private', pathA: ' a.md ', targetB: 'private', pathB: 'a.md' },
      message: 'source and target memory identity must be different',
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

  it('captures prompt draftPromptIntent required DTO fields', async () => {
    await expectDtoGolden({
      factory: createPromptPageService,
      methods: ['draftPromptIntent'],
      method: 'draftPromptIntent',
      input: { cwd: '/repo/app', kind: 'expert', rawInput: 'review', sourceType: 'user_input', scope: 'project' },
      expectedPayload: { cwd: '/repo/app', kind: 'expert', rawInput: 'review', sourceType: 'user_input', scope: 'project' },
    });

    await expectSyncDtoError({
      factory: createPromptPageService,
      methods: ['draftPromptIntent'],
      method: 'draftPromptIntent',
      input: { cwd: '/repo/app', kind: 'expert', rawInput: ' ' },
      message: 'rawInput is required',
    });
  });

  it('captures prompt writePrompt id or key ownership', async () => {
    await expectDtoGolden({
      factory: createPromptPageService,
      methods: ['writePrompt'],
      method: 'writePrompt',
      input: { cwd: '/repo/app', key: 'project/reviewer', name: 'Reviewer', content: 'Check risks first' },
      expectedPayload: { cwd: '/repo/app', key: 'project/reviewer', name: 'Reviewer', content: 'Check risks first' },
    });

    await expectSyncDtoError({
      factory: createPromptPageService,
      methods: ['writePrompt'],
      method: 'writePrompt',
      input: { cwd: '/repo/app', name: 'Missing identity' },
      message: 'id or key is required',
    });
  });
});
