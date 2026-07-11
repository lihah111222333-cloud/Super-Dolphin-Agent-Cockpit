import fs from 'node:fs';
import path from 'node:path';
import { cwd } from 'node:process';
import { beforeEach, expect, test, vi } from 'vitest';
import { sessionApi } from './sessionApi.js';
import {
  callBackend,
  forkThread,
  getThreadMessages,
  interruptTurn,
  startThread,
  startTurn,
} from './backendApi.js';

vi.mock('./backendApi.js', () => ({
  callBackend: vi.fn(() => {
    throw new Error('sessionApi must not call raw callBackend');
  }),
  forkThread: vi.fn(),
  getThreadMessages: vi.fn(),
  interruptTurn: vi.fn(),
  startThread: vi.fn(),
  startTurn: vi.fn(),
}));

beforeEach(() => {
  vi.clearAllMocks();
});

test('sessionApi exposes stable session method names', () => {
  expect(Object.keys(sessionApi).sort()).toEqual(['fork', 'interrupt', 'messages', 'start', 'startTurn']);
});

test('sessionApi does not import raw bridge API', () => {
  const source = fs.readFileSync(path.join(cwd(), 'src/shared/api/sessionApi.js'), 'utf8');
  expect(source).not.toContain('wailsBridge');
  expect(source).not.toContain('callAPI');
  expect(source).not.toContain('callBackend');
});

test('sessionApi delegates only to guarded backendApi exports', async () => {
  forkThread.mockResolvedValue({
    thread: { id: 'thread-fork', forkedFrom: 'thread-1' },
    kickoffState: 'created_only',
  });
  startThread.mockResolvedValue({ id: 'thread-1' });
  startTurn.mockResolvedValue({ ok: true });
  interruptTurn.mockResolvedValue({ interrupted: true });
  getThreadMessages.mockResolvedValue({ messages: [] });

  await expect(sessionApi.fork({ threadId: 'thread-1' })).resolves.toEqual({
    thread: { id: 'thread-fork', forkedFrom: 'thread-1' },
    kickoffState: 'created_only',
  });
  await expect(sessionApi.start({ cwd: '/repo', name: 'draft' })).resolves.toEqual({ id: 'thread-1' });
  await expect(sessionApi.startTurn({ threadId: 'thread-1', text: 'hello' })).resolves.toEqual({ ok: true });
  await expect(sessionApi.interrupt('thread-1', '/repo', 'ui_stop')).resolves.toEqual({ interrupted: true });
  await expect(sessionApi.messages('thread-1', 25, 'cursor-1')).resolves.toEqual({ messages: [] });

  expect(forkThread).toHaveBeenCalledWith({ threadId: 'thread-1' });
  expect(startThread).toHaveBeenCalledWith({ cwd: '/repo', name: 'draft' });
  expect(startTurn).toHaveBeenCalledWith({ threadId: 'thread-1', text: 'hello' });
  expect(interruptTurn).toHaveBeenCalledWith({ threadId: 'thread-1', cwd: '/repo', source: 'ui_stop' });
  expect(getThreadMessages).toHaveBeenCalledWith({ threadId: 'thread-1', limit: 25, before: 'cursor-1' });
  expect(callBackend).not.toHaveBeenCalled();
});
