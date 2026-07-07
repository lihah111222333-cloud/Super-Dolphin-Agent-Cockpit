import { describe, expect, it, vi } from 'vitest';
import { createFilesPageService } from '../files/services/filesPageService.js';
import { createMemoryPageService } from '../memory/services/memoryPageService.js';
import { createObservabilityPageService } from '../observability/services/observabilityPageService.js';
import { createPromptPageService } from '../prompts/services/promptPageService.js';

function createBackendCapture(methodName, result = { ok: true }) {
  const calls = [];
  const api = {
    [methodName]: vi.fn((payload) => {
      calls.push(payload);
      return Promise.resolve(result);
    }),
  };
  return { api, calls };
}

async function captureOutboundDto(factory, methodName, payload) {
  const { api, calls } = createBackendCapture(methodName);
  const service = factory(api);
  await service[methodName](payload);
  expect(api[methodName]).toHaveBeenCalledTimes(1);
  return calls[0];
}

function captureSyncErrorMessage(callback) {
  try {
    callback();
  } catch (error) {
    return error.message;
  }
  throw new Error('expected sync DTO validation error');
}

async function captureAsyncErrorMessage(callback) {
  try {
    await callback();
  } catch (error) {
    return error.message;
  }
  throw new Error('expected async DTO validation error');
}

function expectSyncFailureBeforeBackendCall(factory, methodName, payload) {
  const { api } = createBackendCapture(methodName);
  const service = factory(api);
  const message = captureSyncErrorMessage(() => service[methodName](payload));
  expect(api[methodName]).not.toHaveBeenCalled();
  return message;
}

async function expectAsyncFailureBeforeBackendCall(factory, methodName, payload) {
  const { api } = createBackendCapture(methodName);
  const service = factory(api);
  const message = await captureAsyncErrorMessage(() => service[methodName](payload));
  expect(api[methodName]).not.toHaveBeenCalled();
  return message;
}

describe('feature service DTO golden harness', () => {
  it('locks files saveTextFile DTO normalization and fail-fast shape', async () => {
    await expect(captureOutboundDto(createFilesPageService, 'saveTextFile', {
      defaultPath: ' /tmp/export ',
      defaultFilename: ' notes.md ',
      content: 'hello',
    })).resolves.toMatchInlineSnapshot(`
      {
        "content": "hello",
        "defaultFilename": "notes.md",
        "defaultPath": " /tmp/export ",
      }
    `);

    expect(expectSyncFailureBeforeBackendCall(createFilesPageService, 'saveTextFile', {
      defaultFilename: 'notes.md',
    })).toMatchInlineSnapshot('"file content is required"');
  });

  it('locks memory upsert DTO normalization', async () => {
    await expect(captureOutboundDto(createMemoryPageService, 'upsertMemoryEntry', {
      cwd: ' /repo/app ',
      target: ' private ',
      existingPath: ' docs/memory.md ',
      name: ' feedback-rule ',
      description: ' write tests first ',
      type: ' feedback ',
      content: ' remember the boundary ',
    })).resolves.toMatchInlineSnapshot(`
      {
        "content": "remember the boundary",
        "cwd": "/repo/app",
        "description": "write tests first",
        "existingPath": "docs/memory.md",
        "name": "feedback-rule",
        "target": "private",
        "type": "feedback",
      }
    `);
  });

  it('rejects identical memory merge identities before backend calls', () => {
    expect(expectSyncFailureBeforeBackendCall(createMemoryPageService, 'mergeMemoryEntries', {
      cwd: ' /repo/app ',
      targetA: ' private ',
      pathA: ' docs/memory.md ',
      targetB: ' private ',
      pathB: ' docs/memory.md ',
    })).toMatchInlineSnapshot('"source and target memory identity must be different"');
  });

  it('locks observability recent DTO limit normalization and rejection shape', async () => {
    await expect(captureOutboundDto(createObservabilityPageService, 'listObservabilityRecent', {
      limit: ' 25 ',
      status: 'error',
      traceId: 'trace-1',
    })).resolves.toMatchInlineSnapshot(`
      {
        "limit": 25,
        "status": "error",
        "traceId": "trace-1",
      }
    `);

    await expect(expectAsyncFailureBeforeBackendCall(createObservabilityPageService, 'listObservabilityRecent', {
      limit: 'all',
    })).resolves.toMatchInlineSnapshot('"limit must be a positive integer"');
  });

  it('requires prompt intent fields before drafting', () => {
    expect(expectSyncFailureBeforeBackendCall(createPromptPageService, 'draftPromptIntent', {
      cwd: '/repo/app',
      kind: 'expert',
      rawInput: ' ',
    })).toMatchInlineSnapshot('"rawInput is required"');

    expect(expectSyncFailureBeforeBackendCall(createPromptPageService, 'draftPromptIntent', {
      cwd: '/repo/app',
      kind: ' ',
      rawInput: 'review',
    })).toMatchInlineSnapshot('"kind is required"');

    expect(expectSyncFailureBeforeBackendCall(createPromptPageService, 'draftPromptIntent', {
      cwd: ' ',
      kind: 'expert',
      rawInput: 'review',
    })).toMatchInlineSnapshot('"cwd is required"');
  });

  it('allows prompt writes with id or key and rejects missing identity', async () => {
    await expect(captureOutboundDto(createPromptPageService, 'writePrompt', {
      cwd: '/repo/app',
      key: 'main/reviewer',
      name: 'Reviewer',
      content: 'Review carefully.',
    })).resolves.toMatchInlineSnapshot(`
      {
        "content": "Review carefully.",
        "cwd": "/repo/app",
        "key": "main/reviewer",
        "name": "Reviewer",
      }
    `);

    expect(expectSyncFailureBeforeBackendCall(createPromptPageService, 'writePrompt', {
      cwd: '/repo/app',
      id: ' ',
      key: ' ',
      name: 'missing identity',
    })).toMatchInlineSnapshot('"id is required"');
  });
});
