// @ts-nocheck
import { describe, expect, it, vi, beforeEach } from 'vitest';

const apiMock = vi.hoisted(() => ({ callAPI: vi.fn() }));

vi.mock('./services/api.js', () => ({
  callAPI: apiMock.callAPI,
  getBuildInfo: vi.fn(),
  onAgentEvent: vi.fn(),
  onBridgeEvent: vi.fn(),
  onAppWillQuit: vi.fn(),
}));

import { useDagDetail } from './composables/useDagDetail.js';

describe('useDagDetail', () => {
  beforeEach(() => {
    apiMock.callAPI.mockReset();
  });

  it('does not open when dag_key is missing', async () => {
    const { state, open } = useDagDetail();
    await open({});
    expect(state.show).toBe(false);
    expect(apiMock.callAPI).not.toHaveBeenCalled();
  });

  it('loads detail via dashboard/dagDetail and stores dag + nodes', async () => {
    apiMock.callAPI.mockResolvedValueOnce({
      dag: { dag_key: 'dag-1', title: 'Dag One' },
      nodes: [{ node_key: 'node-1' }],
    });
    const { state, open } = useDagDetail();
    await open({ dag_key: 'dag-1' });
    expect(apiMock.callAPI).toHaveBeenCalledWith('dashboard/dagDetail', { dagKey: 'dag-1' });
    expect(state.show).toBe(true);
    expect(state.loading).toBe(false);
    expect(state.error).toBe('');
    expect(state.dag).toEqual({ dag_key: 'dag-1', title: 'Dag One' });
    expect(state.nodes).toEqual([{ node_key: 'node-1' }]);
  });

  it('captures errors from callAPI and stops loading', async () => {
    apiMock.callAPI.mockRejectedValueOnce(new Error('boom'));
    const { state, open } = useDagDetail();
    await open({ dag_key: 'dag-1' });
    expect(state.show).toBe(true);
    expect(state.loading).toBe(false);
    expect(state.error).toBe('boom');
    expect(state.dag).toBeNull();
    expect(state.nodes).toEqual([]);
  });

  it('close sets show to false', () => {
    const { state, close } = useDagDetail();
    state.show = true;
    close();
    expect(state.show).toBe(false);
  });

  it('updateNodeStatus calls task/node/update and refreshes detail', async () => {
    apiMock.callAPI
      .mockResolvedValueOnce({ dag: { dag_key: 'dag-1' }, nodes: [{ node_key: 'n1', status: 'pending' }] })
      .mockResolvedValueOnce({})
      .mockResolvedValueOnce({ dag: { dag_key: 'dag-1' }, nodes: [{ node_key: 'n1', status: 'done' }] });
    const { state, open, updateNodeStatus } = useDagDetail();
    await open({ dag_key: 'dag-1' });
    const ok = await updateNodeStatus('n1', 'done');
    expect(ok).toBe(true);
    expect(apiMock.callAPI.mock.calls[1]).toEqual([
      'task/node/update',
      { dag_key: 'dag-1', node_key: 'n1', status: 'done' },
    ]);
    expect(state.nodes[0].status).toBe('done');
    expect(state.saveError).toBe('');
    expect(state.savingNodeKey).toBe('');
  });

  it('updateNodeStatus captures errors from callAPI', async () => {
    apiMock.callAPI
      .mockResolvedValueOnce({ dag: { dag_key: 'dag-1' }, nodes: [{ node_key: 'n1', status: 'pending' }] })
      .mockRejectedValueOnce(new Error('nope'));
    const { state, open, updateNodeStatus } = useDagDetail();
    await open({ dag_key: 'dag-1' });
    const ok = await updateNodeStatus('n1', 'done');
    expect(ok).toBe(false);
    expect(state.saveError).toBe('nope');
    expect(state.savingNodeKey).toBe('');
  });

  it('handleStatusEvent refreshes when payload matches open dag', async () => {
    apiMock.callAPI
      .mockResolvedValueOnce({ dag: { dag_key: 'dag-1' }, nodes: [{ node_key: 'n1', status: 'pending' }] })
      .mockResolvedValueOnce({ dag: { dag_key: 'dag-1' }, nodes: [{ node_key: 'n1', status: 'running' }] });
    const { state, open, handleStatusEvent } = useDagDetail();
    await open({ dag_key: 'dag-1' });
    const handled = handleStatusEvent({ dag_key: 'dag-1' });
    expect(handled).toBe(true);
    // wait microtasks for refresh to settle
    await Promise.resolve();
    await Promise.resolve();
    expect(state.nodes[0].status).toBe('running');
  });

  it('handleStatusEvent ignores mismatched dag', async () => {
    apiMock.callAPI.mockResolvedValueOnce({ dag: { dag_key: 'dag-1' }, nodes: [] });
    const { open, handleStatusEvent } = useDagDetail();
    await open({ dag_key: 'dag-1' });
    const handled = handleStatusEvent({ dag_key: 'other' });
    expect(handled).toBe(false);
    expect(apiMock.callAPI).toHaveBeenCalledTimes(1);
  });
});
