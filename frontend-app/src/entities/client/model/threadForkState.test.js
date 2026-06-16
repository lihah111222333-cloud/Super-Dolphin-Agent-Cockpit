import { describe, expect, it } from 'vitest';
import {
  buildForkThreadState,
  cachedForkSharedFiles,
  createLoadForkSharedFiles,
  forkSourceTitle,
  initialForkSharedFilePaths,
  mergeForkSharedFilesWithSelected,
  normalizeForkSharedFiles,
} from './threadForkState.js';

describe('threadForkState', () => {
  it('formats source titles from names, ids, and fallback text', () => {
    expect(forkSourceTitle({ name: ' Existing ' }, 'thread-1')).toBe('继承自会话：Existing');
    expect(forkSourceTitle({}, 'thread-1')).toBe('继承自会话：thread-1');
    expect(forkSourceTitle({}, '')).toBe('继承自前一个对话');
  });

  it('normalizes and deduplicates shared files', () => {
    expect(normalizeForkSharedFiles({
      files: [
        ' notes/a.md ',
        { path: 'notes/a.md' },
        { path: 'notes/b.md' },
        { path: '' },
      ],
    })).toEqual([{ path: 'notes/a.md' }, { path: 'notes/b.md' }]);
  });

  it('reads cached shared files by active project or cwd', () => {
    expect(cachedForkSharedFiles({
      activeProject: 'D:/repo///',
      cwd: 'D:/fallback',
      sharedFilesPageCacheByCwd: {
        'D:/repo': { files: ['notes/a.md'] },
      },
    })).toEqual([{ path: 'notes/a.md' }]);
  });

  it('keeps only available attachment seeds plus explicit seed path', () => {
    expect(initialForkSharedFilePaths({
      attachments: [
        { path: 'notes/a.md' },
        { path: 'notes/missing.md' },
      ],
    }, [{ path: 'notes/a.md' }], 'reports/final.md')).toEqual(['notes/a.md', 'reports/final.md']);
  });

  it('merges available and selected shared files without duplicates', () => {
    expect(mergeForkSharedFilesWithSelected([
      { path: 'notes/a.md' },
      { path: 'notes/b.md' },
    ], ['notes/b.md', 'reports/final.md'])).toEqual([
      { path: 'notes/a.md' },
      { path: 'notes/b.md' },
      { path: 'reports/final.md' },
    ]);
  });

  it('loads selected shared files and fails on invalid detail responses', async () => {
    const loadForkSharedFiles = createLoadForkSharedFiles({
      readSharedFile: async ({ path }) => (
        path === 'bad.md' ? null : { path: `${path}.resolved`, content: 42 }
      ),
    });
    await expect(loadForkSharedFiles([' notes/a.md ', ''])).resolves.toEqual([
      { path: 'notes/a.md.resolved', content: '42' },
    ]);
    await expect(loadForkSharedFiles(['bad.md'])).rejects.toThrow('shared file bad.md returned empty response');
  });

  it('builds local state for a newly forked thread', () => {
    const state = buildForkThreadState({
      provider: 'codex',
      activityThreadAtById: { old: 1 },
      threads: [
        { id: 'thread-fork', name: 'old duplicate' },
        { id: 'thread-old', name: 'old' },
      ],
      timelinesByThread: { old: [{ id: 'old' }] },
    }, 'thread-fork', {
      agentId: 'agent-1',
      providerThreadId: 'provider-1',
      sessionId: 'session-1',
    }, {
      modelProvider: 'codex',
    }, 'Forked thread', 'continue', {
      actionNotice: (message, tone) => ({ message, tone }),
      emptyForkDraft: () => ({ open: false }),
      nowISO: () => '2026-06-15T01:00:00Z',
      nowMillis: () => 123,
      threadActivityTimestamp: () => 456,
      threadMatchesIdentifier: (thread, id) => thread.id === id,
    });

    expect(state).toEqual({
      activePage: 'chat',
      activeThreadId: 'thread-fork',
      provider: 'codex',
      activityThreadAtById: { old: 1, 'thread-fork': 456 },
      forkDraft: { open: false },
      actionNotice: { message: '已创建继承对话', tone: 'success' },
      threads: [
        {
          id: 'thread-fork',
          agentId: 'agent-1',
          providerThreadId: 'provider-1',
          sessionId: 'session-1',
          name: 'Forked thread',
          provider: 'codex',
          status: '工作中',
        },
        { id: 'thread-old', name: 'old' },
      ],
      timelinesByThread: {
        old: [{ id: 'old' }],
        'thread-fork': [{
          id: 'fork-kickoff-123',
          role: 'user',
          text: 'continue',
          time: '2026-06-15T01:00:00Z',
          done: true,
          optimistic: true,
        }],
      },
    });
  });
});
