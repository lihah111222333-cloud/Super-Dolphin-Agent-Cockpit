// @ts-nocheck
import { describe, expect, it } from 'vitest';
import { createThreadViewHelpers } from './stores/thread-store-view.js';

describe('thread store view helpers', () => {
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
