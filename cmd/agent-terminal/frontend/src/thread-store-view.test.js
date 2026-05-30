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

  it('orders chat threads by newest updatedAt before a newer createdAt', () => {
    const state = {
      activeThreadId: 'thread-old-replied',
      activeCmdThreadId: '',
      pinnedThreadAtById: {},
      agentMetaById: {},
      threads: [
        {
          id: 'thread-old-replied',
          name: 'Old with latest reply',
          createdAt: '2026-05-22T10:00:00Z',
          updatedAt: '2026-05-22T10:05:00Z',
        },
        {
          id: 'thread-newer-created',
          name: 'Newer created',
          createdAt: '2026-05-22T10:04:00Z',
          updatedAt: '2026-05-22T10:04:00Z',
        },
      ],
      agentRuntimeById: {},
    };

    const helpers = createThreadViewHelpers(state);

    expect(helpers.getThreadsByMode('chat').map((thread) => thread.id)).toEqual(['thread-old-replied', 'thread-newer-created']);
  });

  it('orders a running thread first when sending advances updatedAt', () => {
    const state = {
      activeThreadId: 'thread-outgoing',
      activeCmdThreadId: '',
      pinnedThreadAtById: {},
      agentMetaById: {
        'thread-replied': { lastActiveAt: '2026-05-22T10:04:00Z' },
      },
      threads: [
        {
          id: 'thread-outgoing',
          name: 'Outgoing prompt still working',
          state: 'running',
          createdAt: '2026-05-22T10:00:00Z',
          updatedAt: '2026-05-22T10:05:00Z',
        },
        {
          id: 'thread-replied',
          name: 'Latest waiting reply',
          state: 'idle',
          createdAt: '2026-05-22T10:04:00Z',
          updatedAt: '2026-05-22T10:04:00Z',
        },
      ],
      agentRuntimeById: {},
    };

    const helpers = createThreadViewHelpers(state);

    expect(helpers.getThreadsByMode('chat').map((thread) => thread.id)).toEqual(['thread-outgoing', 'thread-replied']);
  });

  it('does not let running agent activity beat an older idle completion activity', () => {
    const state = {
      activeThreadId: 'thread-outgoing',
      activeCmdThreadId: '',
      pinnedThreadAtById: {},
      agentMetaById: {
        'thread-outgoing': { lastActiveAt: '2026-05-22T10:05:00Z' },
        'thread-replied': { lastActiveAt: '2026-05-22T10:04:00Z' },
      },
      threads: [
        {
          id: 'thread-outgoing',
          name: 'Outgoing prompt still working',
          state: 'running',
          createdAt: '2026-05-22T10:00:00Z',
          updatedAt: '2026-05-22T10:00:00Z',
        },
        {
          id: 'thread-replied',
          name: 'Latest waiting reply',
          state: 'idle',
          createdAt: '2026-05-22T10:04:00Z',
          updatedAt: '2026-05-22T10:04:00Z',
        },
      ],
      agentRuntimeById: {},
    };

    const helpers = createThreadViewHelpers(state);

    expect(helpers.getThreadsByMode('chat').map((thread) => thread.id)).toEqual(['thread-replied', 'thread-outgoing']);
  });

  it('orders an idle thread first when completion advances agent activity', () => {
    const state = {
      activeThreadId: 'thread-completed',
      activeCmdThreadId: '',
      pinnedThreadAtById: {},
      agentMetaById: {
        'thread-completed': { lastActiveAt: '2026-05-22T10:06:00Z' },
        'thread-older': { lastActiveAt: '2026-05-22T10:05:00Z' },
      },
      threads: [
        {
          id: 'thread-older',
          name: 'Previous completion',
          state: 'idle',
          createdAt: '2026-05-22T10:00:00Z',
          updatedAt: '2026-05-22T10:00:00Z',
        },
        {
          id: 'thread-completed',
          name: 'Latest completion',
          state: 'idle',
          createdAt: '2026-05-22T09:00:00Z',
          updatedAt: '2026-05-22T09:00:00Z',
        },
      ],
      agentRuntimeById: {},
    };

    const helpers = createThreadViewHelpers(state);

    expect(helpers.getThreadsByMode('chat').map((thread) => thread.id)).toEqual(['thread-completed', 'thread-older']);
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
