// @ts-nocheck
import { describe, expect, it } from 'vitest';
import { createThreadViewHelpers } from './stores/thread-store-view.js';

describe('thread store view helpers', () => {
  it('orders chat threads by newest createdAt before name or first-seen order', () => {
    const state = {
      activeThreadId: 'thread-old',
      activeCmdThreadId: '',
      pinnedThreadAtById: {},
      agentMetaById: {},
      threads: [
        { id: 'thread-old', name: 'Z old', createdAt: '2026-05-22T10:00:00Z' },
        { id: 'thread-new', name: 'A new', createdAt: '2026-05-22T10:01:00Z' },
      ],
      agentRuntimeById: {},
    };

    const helpers = createThreadViewHelpers(state);

    expect(helpers.getThreadsByMode('chat').map((thread) => thread.id)).toEqual(['thread-new', 'thread-old']);
  });

  it('orders chat threads by updatedAt when createdAt is missing', () => {
    const state = {
      activeThreadId: 'thread-old',
      activeCmdThreadId: '',
      pinnedThreadAtById: {},
      agentMetaById: {},
      threads: [
        { id: 'thread-old', name: 'A old', updatedAt: '2026-05-22T10:00:00Z' },
        { id: 'thread-new', name: 'Z new', updatedAt: '2026-05-22T10:01:00Z' },
      ],
      agentRuntimeById: {},
    };

    const helpers = createThreadViewHelpers(state);

    expect(helpers.getThreadsByMode('chat').map((thread) => thread.id)).toEqual(['thread-new', 'thread-old']);
  });

  it('filters the active chat thread when it belongs to a different project cwd', () => {
    const state = {
      activeThreadId: 'thread-a',
      activeCmdThreadId: '',
      pinnedThreadAtById: {},
      agentMetaById: {},
      threads: [
        { id: 'thread-a', name: 'Repo A' },
        { id: 'thread-b', name: 'Repo B' },
      ],
      agentRuntimeById: {
        'thread-a': { cwd: '/repo-a' },
        'thread-b': { cwd: '/repo-b' },
      },
    };

    const helpers = createThreadViewHelpers(state);

    expect(helpers.getThreadsByMode('chat', '/repo-b').map((thread) => thread.id)).toEqual(['thread-b']);
  });
});
