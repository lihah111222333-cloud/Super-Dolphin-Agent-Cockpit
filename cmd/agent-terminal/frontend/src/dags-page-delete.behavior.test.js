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
  handleStatusEvent: vi.fn(),
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
  detailMock.handleStatusEvent.mockReset();
  vi.unstubAllGlobals();
}

const dagA = () => ({ dag_key: 'dag-a', title: 'Dag A', status: 'ready', trigger: 'manual' });
const dagProps = (extra = {}) => reactive({ items: [{ ...dagA(), ...extra }] });
const setDetailDag = (extra = {}) => Object.assign(detailMock.state, { dag: dagA(), ...extra });

beforeEach(() => {
  resetDetailMockState();
});

describe('DagsPage delete action', () => {
  it('opens an in-app confirmation before deleting and refreshes the DAG list after confirm', async () => {
    const props = dagProps();
    setDetailDag({ runs: [{ run_key: 'run-done', status: 'succeeded' }] });
    detailMock.deleteDAG.mockResolvedValueOnce({ ok: true });
    const emit = vi.fn();

    const vm = DagsPage.setup(props, { emit });
    expect(vm.deleteDisabledReason.value).toBe('');
    vm.deleteSelectedDag();

    expect(vm.deleteConfirmTarget.value).toEqual({ dagKey: 'dag-a', title: 'Dag A' });
    expect(detailMock.deleteDAG).not.toHaveBeenCalled();
    await vm.confirmDeleteDAG();

    expect(detailMock.deleteDAG).toHaveBeenCalledTimes(1);
    expect(emit).toHaveBeenCalledWith('refresh-dags');
    expect(vm.deleteSuccessText.value).toBe('已删除「Dag A」');
    expect(vm.deleteConfirmTarget.value).toBeNull();
    expect(DagsPage.template).toContain('data-testid="dag-delete-button"');
    expect(DagsPage.template).toContain('data-testid="dag-delete-overlay"');
    expect(DagsPage.template).toContain('data-testid="dag-delete-confirm"');
    expect(DagsPage.template).toContain('data-testid="dag-delete-success"');
    expect(DagsPage.template).toContain('deleteErrorText');
  });

  it('does not open confirmation when an active run is present and cancel keeps data intact', async () => {
    const props = dagProps({ latest_run: { run_key: 'run-active', status: 'running' } });
    setDetailDag({ activeRun: { run_key: 'run-active', status: 'running' } });
    const vm = DagsPage.setup(props, { emit: vi.fn() });

    expect(vm.deleteDisabledReason.value).toContain('正在进行');
    expect(DagsPage.template).toContain('data-testid="dag-delete-disabled-reason"');
    vm.deleteSelectedDag();
    expect(detailMock.deleteDAG).not.toHaveBeenCalled();
    expect(vm.deleteConfirmTarget.value).toBeNull();

    props.items = [{ dag_key: 'dag-a', title: 'Dag A', status: 'ready', trigger: 'manual' }];
    detailMock.state.dag = props.items[0];
    detailMock.state.activeRun = null;
    vm.deleteSelectedDag();
    expect(vm.deleteConfirmTarget.value).toEqual({ dagKey: 'dag-a', title: 'Dag A' });
    vm.cancelDeleteDAG();
    expect(vm.deleteConfirmTarget.value).toBeNull();
    expect(detailMock.deleteDAG).not.toHaveBeenCalled();
  });

  it('allows cleanup deletion when run history loading fails but list state is not active', () => {
    const props = dagProps({ latest_run: { run_key: 'run-done', status: 'succeeded' } });
    setDetailDag({ runsError: new Error('runs unavailable') });
    const vm = DagsPage.setup(props, { emit: vi.fn() });

    expect(vm.deleteDisabledReason.value).toBe('');
    vm.deleteSelectedDag();
    expect(vm.deleteConfirmTarget.value).toEqual({ dagKey: 'dag-a', title: 'Dag A' });
  });

  it('does not refresh the DAG list after delete reports an error', async () => {
    const props = dagProps();
    setDetailDag();
    detailMock.deleteDAG.mockImplementation(async () => {
      detailMock.state.deleteError = new Error('delete refused');
      return { ok: false };
    });
    const emit = vi.fn();

    const vm = DagsPage.setup(props, { emit });
    vm.deleteSelectedDag();
    await vm.confirmDeleteDAG();

    expect(detailMock.deleteDAG).toHaveBeenCalledTimes(1);
    expect(emit).not.toHaveBeenCalledWith('refresh-dags');
    expect(vm.deleteConfirmTarget.value).toEqual({ dagKey: 'dag-a', title: 'Dag A' });
  });
});
