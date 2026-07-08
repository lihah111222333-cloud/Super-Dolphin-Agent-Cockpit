import { describe, expect, it } from 'vitest';
import {
  applyThreadRename,
  archiveThreadFailureState,
  archiveThreadOptimisticState,
  isArchivedStatus,
  threadArchiveStatus } from './helpers/threadListMutations.js';

describe('threadListMutations', () => {
  it('renames only the exact thread id used by the backend action', () => {
    expect(applyThreadRename([
      { id: 'thread-1', name: 'Old' },
      { id: 'thread-2', name: 'Other' },
    ], 'thread-1', 'Renamed')).toEqual([
      { id: 'thread-1', name: 'Renamed' },
      { id: 'thread-2', name: 'Other' },
    ]);
  });

  it('maps archived statuses when toggling archive state', () => {
    expect(isArchivedStatus('archived')).toBe(true);
    expect(isArchivedStatus('归档')).toBe(true);
    expect(isArchivedStatus('已归档')).toBe(true);
    expect(threadArchiveStatus({ status: 'archived' }, false)).toBe('created');
    expect(threadArchiveStatus({ status: 'idle' }, true)).toBe('archived');
  });

  it('builds optimistic archive state without touching unrelated threads', () => {
    const next = archiveThreadOptimisticState({
      activeThreadId: 'thread-1',
      threads: [
        { id: 'thread-1', name: 'One', status: 'idle', archived: false },
        { id: 'thread-2', name: 'Two', status: 'idle', archived: false },
      ],
      threadArchiveLoadingByThread: { 'thread-0': true },
      lastArchivedStatesByThread: { 'thread-0': { archived: true, timestamp: 10 } },
    }, {
      id: 'thread-1',
      archived: true,
      archivedAt: 1000,
      timestamp: 1001,
    });

    expect(next.activeThreadId).toBe('');
    expect(next.threads).toEqual([
      { id: 'thread-1', name: 'One', status: 'archived', archived: true, archivedAt: 1000 },
      { id: 'thread-2', name: 'Two', status: 'idle', archived: false },
    ]);
    expect(next.threadArchiveLoadingByThread).toEqual({ 'thread-0': true, 'thread-1': true });
    expect(next.lastArchivedStatesByThread).toEqual({
      'thread-0': { archived: true, timestamp: 10 },
      'thread-1': { archived: true, timestamp: 1001 },
    });
  });

  it('rolls back only the failed archive while preserving concurrent optimistic states', () => {
    const notice = { message: '归档会话失败：offline', tone: 'error' };
    const next = archiveThreadFailureState({
      activeThreadId: '',
      threads: [
        { id: 'agent_1', agentId: 'agent_1', name: 'One', status: 'archived', archived: true, archivedAt: 1000 },
        { id: 'thread-B', name: 'Two', status: 'archived', archived: true, archivedAt: 1002 },
      ],
      threadArchiveLoadingByThread: { agent_1: true, 'thread-B': true },
      lastArchivedStatesByThread: {
        agent_1: { archived: true, timestamp: 1001 },
        'thread-B': { archived: true, timestamp: 1003 },
      },
    }, {
      id: 'agent_1',
      originalThreads: [
        { id: 'db-1', agentId: 'agent_1', name: 'One', status: 'idle', archived: false },
      ],
      originalActiveThreadId: 'agent_1',
      actionNotice: notice,
    });

    expect(next.activeThreadId).toBe('agent_1');
    expect(next.threads).toEqual([
      { id: 'agent_1', agentId: 'agent_1', name: 'One', status: 'idle', archived: false, archivedAt: 0 },
      { id: 'thread-B', name: 'Two', status: 'archived', archived: true, archivedAt: 1002 },
    ]);
    expect(next.threadArchiveLoadingByThread).toEqual({ agent_1: false, 'thread-B': true });
    expect(next.lastArchivedStatesByThread).toEqual({
      'thread-B': { archived: true, timestamp: 1003 },
    });
    expect(next.actionNotice).toBe(notice);
  });
});
