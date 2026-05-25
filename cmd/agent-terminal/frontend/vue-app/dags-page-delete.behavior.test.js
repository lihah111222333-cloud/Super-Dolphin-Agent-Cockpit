// @ts-nocheck
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { reactive } from '../lib/vue.esm-browser.prod.js';

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
}));

vi.mock('./composables/useDagDetail.js', () => ({
  useDagDetail: () => detailMock,
}));

import { DagsPage } from './pages/DagsPage.js';

function resetDetailMockState() {
  Object.assign(detailMock.state, {
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
    deleting: false,
    deleteError: null,
    savingNodeKey: '',
    saveError: null,
  });
  detailMock.open.mockReset();
  detailMock.deleteDAG.mockReset();
  vi.unstubAllGlobals();
}

const dagA = () => ({ dag_key: 'dag-a', title: 'Dag A', status: 'ready', trigger: 'manual' });
const dagProps = (extra = {}) => reactive({ items: [{ ...dagA(), ...extra }] });
const setDetailDag = (extra = {}) => Object.assign(detailMock.state, { dag: dagA(), ...extra });

beforeEach(() => {
  resetDetailMockState();
});

describe('DagsPage delete action', () => {
  it('confirms deletion, calls the detail composable, and refreshes the DAG list', async () => {
    const props = dagProps();
    setDetailDag({ runs: [{ run_key: 'run-done', status: 'succeeded' }] });
    detailMock.deleteDAG.mockResolvedValueOnce({ ok: true });
    vi.stubGlobal('confirm', vi.fn(() => true));
    const emit = vi.fn();

    const vm = DagsPage.setup(props, { emit });
    expect(vm.deleteDisabledReason.value).toBe('');
    await vm.deleteSelectedDag();

    expect(globalThis.confirm).toHaveBeenCalledWith(expect.stringContaining('删除'));
    expect(detailMock.deleteDAG).toHaveBeenCalledTimes(1);
    expect(emit).toHaveBeenCalledWith('refresh-dags');
    expect(DagsPage.template).toContain('data-testid="dag-delete-button"');
    expect(DagsPage.template).toContain('deleteErrorText');
  });

  it('does not delete without confirmation or when an active run is present', async () => {
    const props = dagProps({ latest_run: { run_key: 'run-active', status: 'running' } });
    setDetailDag({ activeRun: { run_key: 'run-active', status: 'running' } });
    vi.stubGlobal('confirm', vi.fn(() => true));
    const vm = DagsPage.setup(props, { emit: vi.fn() });

    expect(vm.deleteDisabledReason.value).toContain('正在进行');
    await vm.deleteSelectedDag();
    expect(detailMock.deleteDAG).not.toHaveBeenCalled();
    expect(globalThis.confirm).not.toHaveBeenCalled();

    props.items = [{ dag_key: 'dag-a', title: 'Dag A', status: 'ready', trigger: 'manual' }];
    detailMock.state.dag = props.items[0];
    detailMock.state.activeRun = null;
    vi.stubGlobal('confirm', vi.fn(() => false));
    await vm.deleteSelectedDag();
    expect(detailMock.deleteDAG).not.toHaveBeenCalled();
  });

  it('does not refresh the DAG list after delete reports an error', async () => {
    const props = dagProps();
    setDetailDag();
    detailMock.deleteDAG.mockImplementation(async () => {
      detailMock.state.deleteError = new Error('delete refused');
      return { ok: false };
    });
    vi.stubGlobal('confirm', vi.fn(() => true));
    const emit = vi.fn();

    const vm = DagsPage.setup(props, { emit });
    await vm.deleteSelectedDag();

    expect(detailMock.deleteDAG).toHaveBeenCalledTimes(1);
    expect(emit).not.toHaveBeenCalledWith('refresh-dags');
  });
});
