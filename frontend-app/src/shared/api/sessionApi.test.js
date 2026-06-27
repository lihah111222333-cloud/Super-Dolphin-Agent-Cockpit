import fs from 'node:fs';
import path from 'node:path';
import { cwd } from 'node:process';
import { beforeEach, expect, test, vi } from 'vitest';
import { sessionApi } from './sessionApi.js';
import {
  callBackend,
  getThreadMessages,
  interruptTurn,
  startThread,
  startTurn,
} from './backendApi.js';

vi.mock('./backendApi.js', () => ({
  callBackend: vi.fn(() => {
    throw new Error('sessionApi must not call raw callBackend');
  }),
  getThreadMessages: vi.fn(),
  interruptTurn: vi.fn(),
  startThread: vi.fn(),
  startTurn: vi.fn(),
}));

beforeEach(() => {
  vi.clearAllMocks();
});

test('sessionApi exposes stable session method names', () => {
  expect(Object.keys(sessionApi).sort()).toEqual([
    'getThreadMessages',
    'interrupt',
    'interruptTurn',
    'messages',
    'start',
    'startTurn',
  ]);
});

test('sessionApi does not import raw bridge API', () => {
  const source = fs.readFileSync(path.join(cwd(), 'src/shared/api/sessionApi.js'), 'utf8');
  expect(source).not.toContain('wailsBridge');
  expect(source).not.toContain('callAPI');
  expect(source).not.toContain('callBackend');
});

test('sessionApi delegates only to guarded backendApi exports', async () => {
  startThread.mockResolvedValue({ id: 'thread-1' });
  startTurn.mockResolvedValue({ ok: true });
  interruptTurn.mockResolvedValue({ interrupted: true });
  getThreadMessages.mockResolvedValue({ messages: [] });

  await expect(sessionApi.start({ cwd: '/repo', name: 'draft' })).resolves.toEqual({ id: 'thread-1' });
  await expect(sessionApi.startTurn({ threadId: 'thread-1', text: 'hello' })).resolves.toEqual({ ok: true });
  await expect(sessionApi.interrupt('thread-1', '/repo', 'ui_stop')).resolves.toEqual({ interrupted: true });
  await expect(sessionApi.interruptTurn({ threadId: 'thread-2', cwd: '/repo', source: 'ui_stop' })).resolves.toEqual({ interrupted: true });
  await expect(sessionApi.messages('thread-1', 25, 'cursor-1')).resolves.toEqual({ messages: [] });
  await expect(sessionApi.getThreadMessages({ threadId: 'thread-2', limit: 10 })).resolves.toEqual({ messages: [] });

  expect(startThread).toHaveBeenCalledWith({ cwd: '/repo', name: 'draft' });
  expect(startTurn).toHaveBeenCalledWith({ threadId: 'thread-1', text: 'hello' });
  expect(interruptTurn).toHaveBeenCalledWith({ threadId: 'thread-1', cwd: '/repo', source: 'ui_stop' });
  expect(interruptTurn).toHaveBeenCalledWith({ threadId: 'thread-2', cwd: '/repo', source: 'ui_stop' });
  expect(getThreadMessages).toHaveBeenCalledWith({ threadId: 'thread-1', limit: 25, before: 'cursor-1' });
  expect(getThreadMessages).toHaveBeenCalledWith({ threadId: 'thread-2', limit: 10 });
  expect(callBackend).not.toHaveBeenCalled();
});

test('workflow service routes thread session methods through sessionApi', () => {
  const source = fs.readFileSync(path.join(cwd(), 'src/pages/workflows/services/workflowPageService.js'), 'utf8');
  const backendImportBlock = source.match(/import\s*{([\s\S]*?)}\s*from '..\/..\/..\/shared\/api\/backendApi\.js';/)?.[1] || '';

  expect(source).toContain("import { sessionApi } from '../../../shared/api/sessionApi.js';");
  expect(backendImportBlock).not.toMatch(/\b(startThread|startTurn)\b/);
  expect(source).toMatch(/export function startThread\(payload\) \{\s*return sessionApi\.start\(payload\);\s*\}/);
  expect(source).toMatch(/export function startTurn\(payload\) \{\s*return sessionApi\.startTurn\(payload\);\s*\}/);
});
