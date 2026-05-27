// @ts-nocheck
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { nextTick, reactive } from '../lib/vue.esm-browser.prod.js';

const detailMock = vi.hoisted(() => ({
  state: {
    loading: false,
    error: null,
    runsError: null,
    show: false,
    dag: null,
    nodes: [],
    runs: [],
    activeRun: null,
    run: null,
    selectedRunKey: '',
    finalOutput: null,
    starting: false,
    startError: null,
    terminating: false,
    terminateError: null,
    terminateWarning: null,
    savingNodeKey: '',
    saveError: null,
  },
  open: vi.fn(),
  start: vi.fn(),
  terminateActiveRun: vi.fn(),
  selectRun: vi.fn(),
  saveAgentNode: vi.fn(),
  handleStatusEvent: vi.fn(),
}));

vi.mock('./composables/useDagDetail.js', () => ({
  useDagDetail: () => detailMock,
}));
vi.mock('./services/api.js', () => ({
  callAPI: vi.fn(async () => null),
}));

import { DagsPage } from './pages/DagsPage.js';

beforeEach(() => {
  detailMock.open.mockReset();
  detailMock.handleStatusEvent.mockReset();
});

describe('DagsPage node status bridge events', () => {
  it('forwards bridge node status events to the open DAG detail state', async () => {
    const props = reactive({
      items: [{ dag_key: 'dag-a', title: 'Dag A' }],
      emptyText: '暂无 DAG',
      statusEvent: null,
    });

    DagsPage.setup(props, { emit: vi.fn() });
    const payload = { dag_key: 'dag-a', node_key: 'report', new_status: 'done' };
    props.statusEvent = { seq: 1, payload };
    await nextTick();

    expect(detailMock.handleStatusEvent).toHaveBeenCalledWith(payload);
  });
});
