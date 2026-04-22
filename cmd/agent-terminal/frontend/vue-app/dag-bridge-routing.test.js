// @ts-nocheck
import { describe, expect, it, vi } from 'vitest';
import { ref } from '../lib/vue.esm-browser.prod.js';

vi.mock('./services/api.js', () => ({
  callAPI: vi.fn(),
  getBuildInfo: vi.fn(),
  onAgentEvent: vi.fn(),
  onBridgeEvent: vi.fn(),
  onAppWillQuit: vi.fn(),
}));

import { routeDagBridgeEvent, openChatFromDagNode } from './app.js';

describe('routeDagBridgeEvent', () => {
  it('ignores unrelated methods', () => {
    const dagDetail = { handleStatusEvent: vi.fn() };
    const refreshDashboardByPage = vi.fn();
    routeDagBridgeEvent('skills/changed', '', {}, {
      page: ref('dags'),
      refreshDashboardByPage,
      dagDetail,
    });
    expect(dagDetail.handleStatusEvent).not.toHaveBeenCalled();
    expect(refreshDashboardByPage).not.toHaveBeenCalled();
  });

  it('delegates to dagDetail.handleStatusEvent on matching method', () => {
    const dagDetail = { handleStatusEvent: vi.fn() };
    routeDagBridgeEvent('task/node/statuschanged', '', { dag_key: 'dag-1' }, {
      page: ref('chat'),
      refreshDashboardByPage: vi.fn(),
      dagDetail,
    });
    expect(dagDetail.handleStatusEvent).toHaveBeenCalledWith({ dag_key: 'dag-1' });
  });

  it('refreshes dags list when viewing the dags page', () => {
    const refreshDashboardByPage = vi.fn().mockResolvedValue(undefined);
    routeDagBridgeEvent('task/node/statuschanged', '', { dag_key: 'dag-1' }, {
      page: ref('dags'),
      refreshDashboardByPage,
      dagDetail: { handleStatusEvent: vi.fn() },
    });
    expect(refreshDashboardByPage).toHaveBeenCalledWith('dags');
  });

  it('also matches when only eventType carries the method', () => {
    const dagDetail = { handleStatusEvent: vi.fn() };
    routeDagBridgeEvent('', 'task/node/statusChanged', { dag_key: 'x' }, {
      page: ref('chat'),
      refreshDashboardByPage: vi.fn(),
      dagDetail,
    });
    expect(dagDetail.handleStatusEvent).toHaveBeenCalledWith({ dag_key: 'x' });
  });
});

describe('openChatFromDagNode', () => {
  it('switches the page ref to chat', () => {
    const page = ref('dags');
    const out = openChatFromDagNode({ turnId: 'turn-1', assignedTo: 'agent-1' }, { page });
    expect(page.value).toBe('chat');
    expect(out).toEqual({ turnId: 'turn-1', assignedTo: 'agent-1' });
  });

  it('trims ids and tolerates missing deps', () => {
    const out = openChatFromDagNode({ turnId: '  t  ', assignedTo: '  a  ' }, {});
    expect(out).toEqual({ turnId: 't', assignedTo: 'a' });
  });
});
