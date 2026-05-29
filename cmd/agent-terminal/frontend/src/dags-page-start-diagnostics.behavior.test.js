// @ts-nocheck
import { describe, expect, it, vi } from 'vitest';
import { reactive } from '../lib/vue.esm-browser.prod.js';

const detailMock = vi.hoisted(() => ({
  state: {
    loading: false,
    error: null,
    runsError: null,
    dag: null,
    nodes: [],
    runs: [],
    activeRun: null,
    run: null,
    selectedRunKey: '',
    finalOutput: null,
    starting: false,
    startError: null,
    startWarning: null,
    startExecutionState: '',
    terminating: false,
    terminateError: null,
    terminateWarning: null,
    deleting: false,
    deleteError: null,
    savingNodeKey: '',
    saveError: null,
  },
  open: vi.fn(),
  start: vi.fn(),
  terminateActiveRun: vi.fn(),
  deleteDAG: vi.fn(),
  selectRun: vi.fn(),
  saveAgentNode: vi.fn(),
  handleStatusEvent: vi.fn(),
}));

vi.mock('./composables/useDagDetail.js', () => ({
  useDagDetail: () => detailMock,
}));

import { DagsPage } from './pages/DagsPage.js';

describe('DagsPage start diagnostics', () => {
  it('shows a user-facing waiting-for-dispatch warning after start', () => {
    const props = reactive({
      items: [{ dag_key: 'dag-a', title: 'Dag A', status: 'ready', trigger: 'manual' }],
    });
    Object.assign(detailMock.state, {
      dag: props.items[0],
      startWarning: new Error('wait'),
      startExecutionState: 'waiting_for_assignee',
    });

    const vm = DagsPage.setup(props, { emit: vi.fn() });

    expect(vm.startWarningText.value).toContain('等待指派');
    expect(DagsPage.template).toContain('data-testid="dag-start-warning"');
  });

  it('refreshes the DAG list after a successful start so category counts use the new run snapshot', async () => {
    const props = reactive({
      items: [{ dag_key: 'dag-a', title: 'Dag A', status: 'ready', trigger: 'manual' }],
    });
    Object.assign(detailMock.state, {
      dag: props.items[0],
      startError: null,
    });
    detailMock.start.mockResolvedValueOnce(undefined);
    const emit = vi.fn();

    const vm = DagsPage.setup(props, { emit });
    await vm.startSelectedDag();

    expect(detailMock.start).toHaveBeenCalledTimes(1);
    expect(emit).toHaveBeenCalledWith('refresh-dags');
  });
});
