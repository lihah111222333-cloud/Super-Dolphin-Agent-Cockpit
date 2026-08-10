import { describe, expect, it } from 'vitest';
import { normalizeBoardView, normalizeSnapshotAgent } from '../../../shared/api/contracts/agentBoardContract.js';
import { bridgePatchData, bridgePatchState } from './bridgePatchState.js';
import { selectAgentBoardViewModel } from './helpers/agentBoard/selector.js';
import { buildSnapshotState } from './helpers/a1/clientStoreSnapshotModel.js';
import { baseState } from './helpers/a1/clientStoreUtils.js';

const at = '2026-07-28T08:00:00.000Z';

function board(id, status = 'turn_running', outcome = null, parentAgentId = '', assignedAt = at) {
  return {
    id,
    threadId: `thread-${id}`,
    ...(parentAgentId ? { parentAgentId } : {}),
    name: `Agent ${id}`,
    assignment: { title: `Task ${id}`, description: `Prompt ${id}`, assignedAt },
    progress: { status, currentStep: null, completedSteps: null, totalSteps: null, updatedAt: at },
    outcome,
  };
}

function snapshotAgent(value) {
  const { threadId, ...rest } = value;
  return { ...rest, thread_id: threadId, provider: 'codex' };
}

const selectorOptions = { mode: 'docked', selectedAgentId: '', loading: false, error: null };
const normalizeThread = (raw) => ({ id: raw.threadId, name: raw.name || 'Agent thread', status: raw.status || '' });

describe('agent board data model', () => {
  it('hydrates snapshot agents and produces the same view model as realtime patch', () => {
    const rawBoard = board('root');
    const snapshotState = buildSnapshotState(baseState, {
      threads: [],
      agents: [snapshotAgent(rawBoard)],
      mainAgentId: 'root',
    });
    const patch = bridgePatchData('ui/thread/patch', {
      agent: rawBoard,
      thread: { name: 'Agent root' },
    }, rawBoard.threadId, { normalizeThread });
    const patchedState = bridgePatchState({ ...baseState, mainAgentId: 'root' }, patch);

    expect(selectAgentBoardViewModel(snapshotState, selectorOptions))
      .toEqual(selectAgentBoardViewModel({ ...baseState, ...patchedState, mainAgentId: 'root' }, selectorOptions));
  });

  it('orders roots and children stably from parentAgentId and assignedAt', () => {
    const agents = [
      normalizeBoardView(board('child-b', 'idle', null, 'root', '2026-07-28T08:02:00.000Z')),
      normalizeBoardView(board('other', 'idle', null, '', '2026-07-28T08:03:00.000Z')),
      normalizeBoardView(board('root', 'turn_running', null, '', '2026-07-28T08:00:00.000Z')),
      normalizeBoardView(board('child-a', 'idle', null, 'root', '2026-07-28T08:01:00.000Z')),
    ];
    const view = selectAgentBoardViewModel({ agents, mainAgentId: 'root' }, selectorOptions);
    expect(view.rootAgentId).toBe('root');
    expect(view.agents.map((agent) => agent.id)).toEqual(['root', 'child-a', 'child-b', 'other']);
  });

  it('scopes the board to the active conversation root and its subagents', () => {
    const agents = [
      normalizeBoardView(board('root-a')),
      normalizeBoardView(board('child-a', 'idle', null, 'root-a')),
      normalizeBoardView(board('root-b')),
      normalizeBoardView(board('child-b', 'idle', null, 'root-b')),
    ];
    const view = selectAgentBoardViewModel({ agents, mainAgentId: 'root-b' }, {
      ...selectorOptions,
      activeThreadId: 'provider-a',
      threads: [{ id: 'thread-root-a', agentId: 'root-a', providerThreadId: 'provider-a' }],
    });
    expect(view.rootAgentId).toBe('root-a');
    expect(view.agents.map((agent) => agent.id)).toEqual(['root-a', 'child-a']);
  });

  it('counts running, waiting, success, failure, and stopped outcomes structurally', () => {
    const success = { kind: 'success', summary: 'done', recoverable: null, completedAt: at };
    const failure = { kind: 'failure', reason: 'boom', code: 'provider', recoverable: true, completedAt: at };
    const stopped = { kind: 'stopped', reason: 'cancelled', recoverable: null, completedAt: at };
    const agents = [
      board('running', 'turn_running'),
      board('waiting', 'awaiting_user_input'),
      board('success', 'idle', success),
      board('failure', 'failed', failure),
      board('stopped', 'stopped', stopped),
      board('terminal-without-outcome', 'stopped', null),
    ].map((agent) => normalizeBoardView(agent));
    const view = selectAgentBoardViewModel({ agents, mainAgentId: '' }, { ...selectorOptions, mode: 'floating' });
    expect(view.counts).toEqual({ running: 1, waiting: 1, completed: 1, failed: 3 });
    expect(Object.fromEntries(view.agents.map((agent) => [agent.id, agent.statusView]))).toEqual({
      running: { category: 'running', text: '运行中' },
      waiting: { category: 'waiting', text: '等待中' },
      success: { category: 'completed', text: '已完成' },
      failure: { category: 'failed', text: '失败' },
      stopped: { category: 'failed', text: '已停止' },
      'terminal-without-outcome': { category: 'failed', text: '已停止' },
    });
    expect(view.agents.find((agent) => agent.id === 'terminal-without-outcome').outcome).toBeNull();
    expect(view.agents[0].progress).toEqual(expect.objectContaining({ currentStep: null, completedSteps: null, totalSteps: null }));
  });

  it('upserts, updates, and explicitly removes the agent for an accepted thread patch', () => {
    const initial = normalizeBoardView(board('worker', 'turn_running'));
    const updated = board('worker', 'awaiting_user_input');
    const updatePatch = bridgePatchData('ui/thread/patch', { agent: updated }, updated.threadId, { normalizeThread });
    const updatedState = bridgePatchState({ ...baseState, agents: [initial] }, updatePatch);
    expect(updatedState.agents).toEqual([normalizeBoardView(updated)]);

    const removePatch = bridgePatchData('ui/thread/patch', { agent: null }, updated.threadId, { normalizeThread });
    expect(bridgePatchState({ ...baseState, agents: updatedState.agents }, removePatch).agents).toEqual([]);
  });

  it('fails fast on missing fields, invalid enums, and invalid terminal combinations', () => {
    const missingProgressField = board('missing');
    delete missingProgressField.progress.updatedAt;
    expect(() => normalizeBoardView(missingProgressField)).toThrow('progress.updatedAt is required');

    expect(() => normalizeBoardView(board('enum', 'running'))).toThrow('progress.status is invalid');
    expect(() => normalizeBoardView(board('success', 'idle', {
      kind: 'success', recoverable: null, completedAt: at,
    }))).toThrow('outcome.summary is required for success');
    expect(() => normalizeBoardView(board('failure', 'failed', {
      kind: 'failure', recoverable: false, completedAt: at,
    }))).toThrow('outcome.reason is required for failure');
    expect(() => normalizeSnapshotAgent({ ...snapshotAgent(board('snapshot')), assignment: undefined }))
      .toThrow('assignment must be an object');
  });
});
