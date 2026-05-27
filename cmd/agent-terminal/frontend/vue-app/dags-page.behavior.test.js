// @ts-nocheck
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
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
  saveAgentNode: vi.fn(), handleStatusEvent: vi.fn(),
}));
const apiMock = vi.hoisted(() => ({
  callAPI: vi.fn(async () => null),
}));

vi.mock('./composables/useDagDetail.js', () => ({
  useDagDetail: () => detailMock,
}));
vi.mock('./services/api.js', () => ({
  callAPI: apiMock.callAPI,
}));

import { DagsPage } from './pages/DagsPage.js';
import { DagNodeEditForm } from './components/dag/DagNodeEditForm.js';

const FRONTEND_ROOT = resolve(import.meta.dirname, '.');

function readCSS(relativePath) {
  return readFileSync(resolve(FRONTEND_ROOT, relativePath), 'utf-8');
}

function cssBlock(css, selector) {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const match = css.match(new RegExp(`${escaped}\\s*\\{([^}]*)\\}`));
  return match ? match[1].replace(/\/\*[\s\S]*?\*\//g, '') : '';
}

function resetDetailMockState() {
  detailMock.state.loading = false;
  detailMock.state.error = null;
  detailMock.state.runsError = null;
  detailMock.state.show = false;
  detailMock.state.dag = null;
  detailMock.state.nodes = [];
  detailMock.state.runs = [];
  detailMock.state.activeRun = null;
  detailMock.state.run = null;
  detailMock.state.selectedRunKey = '';
  detailMock.state.finalOutput = null;
  detailMock.state.starting = false;
  detailMock.state.startError = null;
  detailMock.state.terminating = false;
  detailMock.state.terminateError = null;
  detailMock.state.terminateWarning = null;
  detailMock.state.savingNodeKey = '';
  detailMock.state.saveError = null;
  detailMock.open.mockReset();
  detailMock.start.mockReset();
  detailMock.terminateActiveRun.mockReset();
  detailMock.selectRun.mockReset();
  detailMock.saveAgentNode.mockReset(); detailMock.handleStatusEvent.mockReset();
  apiMock.callAPI.mockReset().mockImplementation(async () => null);
}

const dagA = () => ({ dag_key: 'dag-a', title: 'Dag A', status: 'ready', trigger: 'manual' });
const dagProps = (extra = {}) => reactive({ items: [{ ...dagA(), ...extra }] });
const setDetailDag = (extra = {}) => Object.assign(detailMock.state, { dag: dagA(), ...extra });

beforeEach(() => {
  resetDetailMockState();
});

describe('DagsPage console shell', () => {
  it('wires DAG detail components and opens detail when a row is selected', () => {
    const props = reactive({
      items: [
        { dag_key: 'dag-a', title: 'Dag A' },
        { dag_key: 'dag-b', title: 'Dag B' },
      ],
      emptyText: '暂无 DAG',
    });

    detailMock.open.mockReset();
    const vm = DagsPage.setup(props, { emit: vi.fn() });

    expect(DagsPage.components?.DagNodeList).toBeTruthy();
    expect(DagsPage.components?.DagRunHistoryPanel).toBeTruthy();
    expect(DagsPage.components?.DagFinalOutputPanel).toBeTruthy();
    expect(DagsPage.components?.DagNodeEditForm).toBeTruthy();
    expect(DagsPage.components?.DagTopologyPanel).toBeTruthy();
    expect(DagsPage.components?.DagSharedFilesPanel).toBeTruthy();
    expect(vm.dagDetail).toBe(detailMock);
    expect(vm.detailState).toBe(detailMock.state);
    expect(detailMock.open).toHaveBeenCalledWith(props.items[0]);

    vm.selectDag(vm.rows.value[1]);

    expect(detailMock.open).toHaveBeenLastCalledWith(props.items[1]);
    expect(DagsPage.template).toContain('<DagNodeList');
    expect(DagsPage.template).toContain('<DagRunHistoryPanel');
    expect(DagsPage.template).toContain('<DagFinalOutputPanel');
    expect(DagsPage.template).toContain('<DagNodeEditForm');
    expect(DagsPage.template).toContain('<DagTopologyPanel');
    expect(DagsPage.template).toContain('<DagSharedFilesPanel');
  });

  it('emits AI design requests with the selected DAG context', () => {
    const emit = vi.fn();
    const props = reactive({
      items: [{ dag_key: 'dag-a', title: 'Dag A' }],
      emptyText: '暂无 DAG',
    });

    const vm = DagsPage.setup(props, { emit });
    vm.startDesignFlow();

    expect(emit).toHaveBeenCalledWith('design-flow', {
      dagKey: 'dag-a',
      title: 'Dag A',
    });
    expect(DagsPage.template).toContain('data-testid="dag-design-flow-button"');
    expect(DagsPage.template).toContain('@click="startDesignFlow"');
  });

  it('uses user-facing task flow wording instead of DAG internals on the default page shell', () => {
    expect(DagsPage.props.emptyText.default).toBe('暂无任务流程');
    expect(DagsPage.template).toContain('任务流程');
    expect(DagsPage.template).toContain('AI 设计流程');
    expect(DagsPage.template).toContain('正在加载任务流程');
    expect(DagsPage.template).toContain('加载任务流程失败');
    expect(DagsPage.template).toContain('运行');
    for (const label of ['任务状态', '运行计划']) expect(DagsPage.template).toContain(label);
    expect(DagsPage.template).toContain('最近运行');
    expect(DagsPage.template).toContain('最终结果');
    expect(DagsPage.template).not.toContain('DAG Console');
    expect(DagsPage.template).not.toContain('>Start<');
  });

  it('maps raw DAG and run statuses to user-facing labels without exposing run ids in the list', () => {
    const props = reactive({
      items: [
        {
          dag_key: 'daily-brief',
          title: 'Daily Brief',
          status: 'ready',
          trigger: { type: 'manual' }, cron_expr: '0 8 * * *',
          latest_run: { run_key: 'run-7', status: 'succeeded' },
          metadata: { final_output: { type: 'file', path: 'reports/daily.pptx' } },
        },
        {
          dag_key: 'daily-failing',
          title: 'Daily Failing',
          status: 'failed',
          trigger: { type: 'scheduled' },
          latest_run: { run_key: 'run-8', status: 'running' },
        },
      ],
    });

    const vm = DagsPage.setup(props, { emit: vi.fn() });

    expect(vm.rows.value[0]).toMatchObject({
      key: 'daily-brief',
      status: '可运行',
      triggerLabel: '手动',
      latestRunLabel: '成功',
      hasFinalOutput: true,
    });
    expect(vm.rows.value[1]).toMatchObject({
      key: 'daily-failing',
      status: '失败',
      triggerLabel: '定时',
      latestRunLabel: '运行中',
      hasFinalOutput: false,
    });
    expect(vm.rows.value[0].latestRunLabel).not.toContain('run-7');
    expect(vm.rows.value[1].latestRunLabel).not.toContain('run-8');
  });

  it('wires agent node form saves through the detail composable', async () => {
    const props = reactive({ items: [{ dag_key: 'dag-a', title: 'Dag A' }] });
    detailMock.state.nodes = [{
      node_key: 'draft',
      title: 'Draft',
      node_type: 'agent',
      config: { exec: { provider: 'claude', model: 'sonnet', prompt_key: 'main/writer' } },
    }];
    detailMock.saveAgentNode.mockResolvedValueOnce(undefined);

    const vm = DagsPage.setup(props, { emit: vi.fn() });
    await vm.saveAgentNode({
      nodeKey: 'draft',
      title: 'Draft v2',
      dependsOn: [],
      config: { exec: { provider: 'claude', model: 'opus', prompt_key: 'main/writer' }, first_turn: 'write the report' },
    });

    expect(detailMock.saveAgentNode).toHaveBeenCalledWith({
      nodeKey: 'draft',
      title: 'Draft v2',
      dependsOn: [],
      config: { exec: { provider: 'claude', model: 'opus', prompt_key: 'main/writer' }, first_turn: 'write the report' },
    });
    expect(DagsPage.template).toContain('@save-agent-node="saveAgentNode"');
    expect(DagsPage.template).toContain(':saving-node-key="detailState.savingNodeKey"');
    expect(DagsPage.template).toContain(':save-error="detailState.saveError"');
  });

  it('preserves hidden exec agent_key when the agent node form submits full config edits', async () => {
    const emit = vi.fn();
    const props = reactive({
      nodes: [{
        node_key: 'draft',
        title: 'Draft',
        node_type: 'agent',
        depends_on: [],
        config: {
          exec: {
            provider: 'claude',
            model: 'sonnet',
            prompt_key: 'main/writer',
            agent_key: 'daily_writer',
          },
          first_turn: 'old prompt',
          inputs: { preserved_input: true },
          outputs: { preserved_output: true },
        },
      }],
      savingNodeKey: '',
      saveError: null,
    });

    const vm = DagNodeEditForm.setup(props, { emit });
    await nextTick();
    vm.form.model = 'opus';
    vm.form.firstTurn = 'new prompt';
    vm.submit();

    expect(emit).toHaveBeenCalledWith('save-agent-node', expect.objectContaining({
      nodeKey: 'draft',
      config: expect.objectContaining({
        exec: expect.objectContaining({
          agent_key: 'daily_writer',
          model: 'opus',
          prompt_key: 'main/writer',
        }),
        first_turn: 'new prompt',
        inputs: expect.objectContaining({ preserved_input: true }),
        outputs: expect.objectContaining({ preserved_output: true }),
      }),
    }));
  });

  it('does not pin a default provider when the existing agent config omits provider', async () => {
    const emit = vi.fn();
    const props = reactive({
      nodes: [{
        node_key: 'draft',
        title: 'Draft',
        node_type: 'agent',
        depends_on: [],
        config: {
          exec: {
            model: 'sonnet',
            prompt_key: 'main/writer',
            agent_key: 'daily_writer',
          },
          first_turn: 'old prompt',
        },
      }],
      savingNodeKey: '',
      saveError: null,
    });

    const vm = DagNodeEditForm.setup(props, { emit });
    await nextTick();
    expect(vm.form.provider).toBe('');
    vm.form.firstTurn = 'new prompt';
    vm.submit();

    const payload = emit.mock.calls[0]?.[1];
    expect(payload.config.exec).not.toHaveProperty('provider');
    expect(payload.config.exec).toMatchObject({
      model: 'sonnet',
      prompt_key: 'main/writer',
      agent_key: 'daily_writer',
    });
  });

  it('passes edited agent form payload through the page save and start path', async () => {
    const formEmit = vi.fn();
    const props = reactive({ items: [{ dag_key: 'dag-a', title: 'Dag A', status: 'ready', trigger: 'manual' }] });
    detailMock.state.dag = { dag_key: 'dag-a', title: 'Dag A', status: 'ready', trigger: 'manual', version: 3 };
    detailMock.state.nodes = [{
      node_key: 'draft',
      title: 'Draft',
      node_type: 'agent',
      depends_on: ['collect'],
      config: {
        exec: { prompt_key: 'main/writer', agent_key: 'daily_writer' },
        first_turn: 'old prompt',
      },
    }];
    detailMock.saveAgentNode.mockResolvedValueOnce(undefined);

    const pageVm = DagsPage.setup(props, { emit: vi.fn() });
    const formVm = DagNodeEditForm.setup({
      nodes: detailMock.state.nodes,
      savingNodeKey: '',
      saveError: null,
    }, { emit: formEmit });
    await nextTick();
    formVm.form.title = 'Draft v2';
    formVm.form.provider = 'codex';
    formVm.form.model = 'gpt-5.5';
    formVm.form.firstTurn = 'write the final report';
    formVm.form.toSharedfilePath = 'reports/final.md';
    formVm.submit();

    const payload = formEmit.mock.calls[0]?.[1];
    await pageVm.saveAgentNode(payload);
    pageVm.startSelectedDag();

    expect(detailMock.saveAgentNode).toHaveBeenCalledWith(expect.objectContaining({
      nodeKey: 'draft',
      title: 'Draft v2',
      dependsOn: ['collect'],
      config: expect.objectContaining({
        exec: expect.objectContaining({
          provider: 'codex',
          model: 'gpt-5.5',
          prompt_key: 'main/writer',
          agent_key: 'daily_writer',
        }),
        first_turn: 'write the final report',
        outputs: expect.objectContaining({
          to_sharedfile: { path: 'reports/final.md', lock_mode: 'exclusive' },
        }),
      }),
    }));
    expect(detailMock.start).toHaveBeenCalledTimes(1);
  });

  it('exposes start disabled reason and blocks running DAG starts before calling the detail composable', () => {
    const props = reactive({
      items: [{ dag_key: 'dag-a', title: 'Dag A', status: 'ready', trigger: 'manual', latest_run: { status: 'running' } }],
      loading: false,
      error: '',
    });
    detailMock.start.mockReset();
    detailMock.state.loading = false;
    detailMock.state.starting = false;
    detailMock.state.startError = new Error('start failed');
    detailMock.state.dag = props.items[0];
    detailMock.state.runsError = null;

    const vm = DagsPage.setup(props, { emit: vi.fn() });

    expect(vm.startDisabledReason.value).toContain('已有运行正在进行');
    vm.startSelectedDag();
    expect(detailMock.start).toHaveBeenCalledTimes(0);
    expect(DagsPage.template).toContain('data-testid="dag-start-button"');
    expect(DagsPage.template).toContain('startDisabledReason');
    expect(DagsPage.template).toContain('startErrorText');

    props.items = [{ dag_key: 'dag-a', title: 'Dag A', status: 'ready', trigger: 'manual' }];
    detailMock.state.dag = props.items[0];
    expect(vm.startDisabledReason.value).toBe('');
    vm.startSelectedDag();
    expect(detailMock.start).toHaveBeenCalledTimes(1);

    props.items = [{ dag_key: 'dag-a', title: 'Dag A', status: 'ready', trigger: 'cron' }];
    detailMock.state.dag = props.items[0];
    expect(vm.startDisabledReason.value).toBe('');

    props.items = [{ dag_key: 'dag-a', title: 'Dag A', status: 'ready', trigger: 'schedule' }];
    detailMock.state.dag = props.items[0];
    expect(vm.startDisabledReason.value).toBe('');

    detailMock.state.runsError = new Error('runs unavailable');
    const runsErrorVm = DagsPage.setup(props, { emit: vi.fn() });
    expect(runsErrorVm.startDisabledReason.value).toContain('运行历史不可用');
    runsErrorVm.startSelectedDag();
    expect(detailMock.start).toHaveBeenCalledTimes(1);
    detailMock.state.runsError = null;

    detailMock.state.starting = true;
    expect(DagsPage.setup(props, { emit: vi.fn() }).startDisabledReason.value).toContain('启动中');
    detailMock.state.starting = false;
    props.loading = true;
    expect(vm.startDisabledReason.value).toContain('加载中');
    props.loading = false;
    props.items = [];
    expect(vm.startDisabledReason.value).toContain('未选择任务流程');
  });

  it('shows a stop action for active runs and calls terminate on the detail composable', async () => {
    const props = dagProps();
    setDetailDag({ activeRun: { run_key: 'run-active', status: 'running' } });
    const emit = vi.fn();

    const vm = DagsPage.setup(props, { emit });

    expect(vm.stopDisabledReason.value).toBe('');
    await vm.stopSelectedDag();
    expect(detailMock.terminateActiveRun).toHaveBeenCalledTimes(1);
    expect(emit).toHaveBeenCalledWith('refresh-dags');
    expect(DagsPage.template).toContain('data-testid="dag-stop-button"');
    expect(DagsPage.template).toContain('terminateErrorText');

    detailMock.state.terminating = true;
    expect(DagsPage.setup(props, { emit: vi.fn() }).stopDisabledReason.value).toContain('停止中');
  });

  it('does not refresh the DAG list after terminate reports an error', async () => {
    const props = dagProps();
    setDetailDag({ activeRun: { run_key: 'run-active', status: 'running' } });
    detailMock.terminateActiveRun.mockImplementation(async () => {
      detailMock.state.terminateError = new Error('terminate refused');
    });
    const emit = vi.fn();

    const vm = DagsPage.setup(props, { emit });
    await vm.stopSelectedDag();

    expect(detailMock.terminateActiveRun).toHaveBeenCalledTimes(1);
    expect(emit).not.toHaveBeenCalledWith('refresh-dags');
  });

  it('waits for terminate to settle before refreshing the DAG list', async () => {
    const props = dagProps();
    setDetailDag({ activeRun: { run_key: 'run-active', status: 'running' } });
    let resolveTerminate;
    detailMock.terminateActiveRun.mockImplementation(() => new Promise((resolve) => { resolveTerminate = resolve; }));
    const emit = vi.fn();

    const vm = DagsPage.setup(props, { emit });
    const stopPromise = vm.stopSelectedDag();
    await Promise.resolve();
    expect(emit).not.toHaveBeenCalledWith('refresh-dags');

    resolveTerminate();
    await stopPromise;
    expect(emit).toHaveBeenCalledWith('refresh-dags');
  });

  it('uses loaded run detail instead of stale list latest_run when deciding whether stop is available', () => {
    const props = dagProps({ latest_run: { run_key: 'stale-run', status: 'running' } });
    setDetailDag({
      runs: [{ run_key: 'stale-run', status: 'cancelled' }],
      run: { run_key: 'stale-run', status: 'cancelled' },
      activeRun: null,
    });

    const vm = DagsPage.setup(props, { emit: vi.fn() });

    expect(vm.rows.value[0].latestRunLabel).toBe('已取消');
    expect(vm.selectedRow.value.latestRunLabel).toBe('已取消');
    expect(vm.stopActionVisible.value).toBe(false);
    expect(vm.stopDisabledReason.value).toContain('暂无运行中任务');
    vm.stopSelectedDag();
    expect(detailMock.terminateActiveRun).not.toHaveBeenCalled();
    expect(vm.startDisabledReason.value).toBe('');
  });

  it('uses refreshed terminal run detail instead of stale detail history for the same run', () => {
    const props = dagProps({ latest_run: { run_key: 'stale-run', status: 'running' } });
    setDetailDag({
      runs: [{ run_key: 'stale-run', status: 'running' }],
      run: { run_key: 'stale-run', status: 'cancelled' },
      activeRun: null,
    });

    const vm = DagsPage.setup(props, { emit: vi.fn() });

    expect(vm.rows.value[0].latestRunLabel).toBe('已取消');
    expect(vm.stopActionVisible.value).toBe(false);
    expect(vm.startDisabledReason.value).toBe('');
  });

  it('does not let empty detail history hide an active list latest_run', () => {
    const props = dagProps({ latest_run: { run_key: 'stale-run', status: 'running' } });
    setDetailDag({ runs: [], run: null, activeRun: null, show: true, loading: false, runsError: null });

    const vm = DagsPage.setup(props, { emit: vi.fn() });

    expect(vm.rows.value[0].latestRunLabel).toBe('运行中');
    expect(vm.stopActionVisible.value).toBe(true);
    expect(vm.startDisabledReason.value).toContain('已有运行正在进行');
    expect(vm.editDisabledReason.value).toContain('已有运行正在进行');
    vm.stopSelectedDag();
    expect(detailMock.terminateActiveRun).toHaveBeenCalledWith({ run_key: 'stale-run', status: 'running' });
  });

  it('does not expose stop for non-running run statuses even when they have a run key', () => {
    const props = dagProps();
    setDetailDag({ run: { run_key: 'run-queued', status: 'queued' } });

    const vm = DagsPage.setup(props, { emit: vi.fn() });

    expect(vm.stopActionVisible.value).toBe(false);
    expect(vm.stopDisabledReason.value).toContain('暂无运行中任务');
    vm.stopSelectedDag();
    expect(detailMock.terminateActiveRun).not.toHaveBeenCalled();
  });

  it('keeps list latest_run visible when detail loading failed', () => {
    const props = dagProps({ latest_run: { run_key: 'stale-run', status: 'running' } });
    setDetailDag({ runs: [], run: null, activeRun: null, show: true, loading: false, runsError: null, error: new Error('detail unavailable') });

    const vm = DagsPage.setup(props, { emit: vi.fn() });

    expect(vm.rows.value[0].latestRunLabel).toBe('运行中');
    expect(vm.stopActionVisible.value).toBe(true);
    expect(vm.stopDisabledReason.value).toContain('任务流程详情不可用');
  });

  it('does not expose stop when the only active signal has no run key to terminate', () => {
    const props = dagProps();
    setDetailDag({ activeRun: { status: 'running' } });

    const vm = DagsPage.setup(props, { emit: vi.fn() });

    expect(vm.stopActionVisible.value).toBe(false);
    expect(vm.stopDisabledReason.value).toContain('暂无运行中任务');
    vm.stopSelectedDag();
    expect(detailMock.terminateActiveRun).not.toHaveBeenCalled();
  });

  it('disables node editing while a DAG has an active run', () => {
    const props = reactive({
      items: [{ dag_key: 'dag-a', title: 'Dag A', status: 'ready', trigger: 'manual', latest_run: { status: 'running' } }],
    });
    detailMock.state.dag = props.items[0];

    const vm = DagsPage.setup(props, { emit: vi.fn() });

    expect(vm.editDisabledReason.value).toContain('已有运行正在进行');
    expect(DagsPage.template).toContain(':disabled-reason="editDisabledReason"');

    const disabledEmit = vi.fn();
    const formVm = DagNodeEditForm.setup({
      nodes: [{
        node_key: 'draft',
        title: 'Draft',
        node_type: 'agent',
        depends_on: [],
        config: { exec: { provider: 'claude' } },
      }],
      savingNodeKey: '',
      saveError: null,
      disabledReason: '已有运行正在进行，不能编辑步骤',
    }, { emit: disabledEmit });

    expect(formVm.editingDisabled.value).toBe(true);
    formVm.submit();
    expect(disabledEmit).not.toHaveBeenCalled();
    expect(DagNodeEditForm.template).toContain('data-testid="dag-node-edit-disabled-reason"');
    expect(DagNodeEditForm.template).toContain(':disabled="editingDisabled"');

    props.items = [{ dag_key: 'dag-a', title: 'Dag A', status: 'ready', trigger: 'manual' }];
    detailMock.state.dag = props.items[0];
    detailMock.state.runsError = new Error('runs unavailable');
    const runsErrorVm = DagsPage.setup(props, { emit: vi.fn() });
    expect(runsErrorVm.editDisabledReason.value).toContain('运行历史不可用');
    detailMock.saveAgentNode.mockReset();
    runsErrorVm.saveAgentNode({ nodeKey: 'draft' });
    expect(detailMock.saveAgentNode).not.toHaveBeenCalled();
    detailMock.state.runsError = null;
  });

  it('blocks start and node editing when DAG detail failed to load', () => {
    const props = reactive({
      items: [{ dag_key: 'dag-a', title: 'Dag A', status: 'ready', trigger: 'manual' }],
    });
    detailMock.state.dag = props.items[0];
    detailMock.state.error = new Error('detail unavailable');

    const vm = DagsPage.setup(props, { emit: vi.fn() });

    expect(vm.startDisabledReason.value).toContain('任务流程详情不可用');
    expect(vm.editDisabledReason.value).toContain('任务流程详情不可用');
    vm.startSelectedDag();
    expect(detailMock.start).not.toHaveBeenCalled();
    detailMock.saveAgentNode.mockReset();
    vm.saveAgentNode({ nodeKey: 'draft' });
    expect(detailMock.saveAgentNode).not.toHaveBeenCalled();
  });

  it('blocks start and node editing when a hidden active run is loaded outside recent history', () => {
    const props = reactive({
      items: [{ dag_key: 'dag-a', title: 'Dag A', status: 'ready', trigger: 'manual' }],
    });
    detailMock.state.dag = props.items[0];
    detailMock.state.activeRun = { run_key: 'run-hidden', status: 'running' };

    const vm = DagsPage.setup(props, { emit: vi.fn() });

    expect(vm.startDisabledReason.value).toContain('已有运行正在进行');
    expect(vm.editDisabledReason.value).toContain('已有运行正在进行');
    vm.startSelectedDag();
    expect(detailMock.start).not.toHaveBeenCalled();
  });

  it('selects runs from the history panel and passes selected-run final output to the final output panel', () => {
    const props = reactive({ items: [{ dag_key: 'dag-a', title: 'Dag A' }] });
    detailMock.selectRun.mockReset();
    detailMock.state.runs = [
      { run_key: 'run-new', status: 'done' },
      { run_key: 'run-old', status: 'done' },
    ];
    detailMock.state.selectedRunKey = 'run-old';
    detailMock.state.finalOutput = { kind: 'text', text: 'old result' };

    const vm = DagsPage.setup(props, { emit: vi.fn() });
    vm.selectRun('run-new');

    expect(detailMock.selectRun).toHaveBeenCalledWith('run-new');
    expect(vm.selectedFinalOutput.value).toEqual({ kind: 'text', text: 'old result' });
    expect(DagsPage.template).toContain('@select-run="selectRun"');
    expect(DagsPage.template).toContain(':final-output="selectedFinalOutput"');
  });

  it('shows runs errors separately from final-output empty state', () => {
    const props = reactive({ items: [{ dag_key: 'dag-a', title: 'Dag A' }] });
    detailMock.state.runsError = new Error('runs unavailable');
    detailMock.state.finalOutput = null;

    const vm = DagsPage.setup(props, { emit: vi.fn() });

    expect(vm.runsErrorText.value).toBe('无法加载运行历史，请稍后重试。');
    expect(DagsPage.template).toContain('data-testid="dag-runs-error"');
    expect(DagsPage.template).toContain('v-if="runsErrorText"');
    expect(DagsPage.template).toContain('v-else');
  });

  it('sanitizes user-visible DAG operation errors instead of exposing internal ids', () => {
    const props = reactive({
      items: [{ dag_key: 'dag-a', title: 'Dag A', status: 'ready', trigger: 'manual' }],
    });
    detailMock.state.dag = props.items[0];
    detailMock.state.error = new Error('detail failed for dag_key=dag-a');
    detailMock.state.startError = new Error('start failed for run_key=run-1');
    detailMock.state.runsError = new Error('shared file reports/final.md missing dag_key');
    detailMock.state.terminateError = new Error('terminate failed for dag_key=dag-a run_key=run-1');

    const vm = DagsPage.setup(props, { emit: vi.fn() });

    expect(vm.detailErrorText.value).toBe('任务流程详情不可用，请稍后重试。');
    expect(vm.startErrorText.value).toBe('运行任务流程失败，请稍后重试。');
    expect(vm.runsErrorText.value).toBe('无法加载运行历史，请稍后重试。');
    expect(vm.terminateErrorText.value).toBe('停止任务流程失败，请稍后重试。');
    expect(vm.detailErrorText.value).not.toContain('dag_key');
    expect(vm.startErrorText.value).not.toContain('run_key');
    expect(vm.runsErrorText.value).not.toContain('shared file');
    expect(vm.terminateErrorText.value).not.toContain('dag_key');
    expect(vm.terminateErrorText.value).not.toContain('run_key');
    expect(DagsPage.template).toContain('{{ detailErrorText }}');
    expect(DagsPage.template).not.toContain('detailState.error.message');
  });

  it('uses a two-pane console shell instead of the generic DataPage wrapper', () => {
    expect(DagsPage.name).toBe('DagsPage');
    expect(typeof DagsPage.setup).toBe('function');
    expect(DagsPage.components?.DataPage).toBeUndefined();
    expect(DagsPage.template).toContain('data-testid="dag-console"');
    expect(DagsPage.template).toContain('data-testid="dag-console-list"');
    expect(DagsPage.template).toContain('data-testid="dag-console-detail"');
    expect(DagsPage.template).not.toContain('{{ row.key }}');
    expect(DagsPage.template).toContain('{{ row.title }}');
    expect(DagsPage.template).toContain('{{ row.status }}');
    expect(DagsPage.template).toContain('{{ row.triggerLabel }}');
    expect(DagsPage.template).toContain('{{ row.latestRunLabel }}');
    expect(DagsPage.template).toContain('v-if="row.hasFinalOutput"');
    expect(DagsPage.template).toContain('<details class="dag-advanced-section"');
    expect(DagsPage.template).toContain('<summary>高级设置</summary>');
    expect(DagsPage.template.indexOf('<details class="dag-advanced-section"')).toBeLessThan(DagsPage.template.indexOf('<DagNodeEditForm'));
    expect(DagsPage.template.indexOf('<details class="dag-advanced-section"')).toBeLessThan(DagsPage.template.indexOf('<DagTopologyPanel'));
    expect(DagsPage.template.indexOf('<details class="dag-advanced-section"')).toBeLessThan(DagsPage.template.indexOf('<DagSharedFilesPanel'));
    expect(DagsPage.template).not.toContain('<DataPage');
  });

  it('prioritizes final output before overview, run history, steps, and advanced settings', () => {
    const finalOutputIndex = DagsPage.template.indexOf('<DagFinalOutputPanel');
    const factsIndex = DagsPage.template.indexOf('<dl class="dag-console-facts"');
    const runHistoryIndex = DagsPage.template.indexOf('<DagRunHistoryPanel');
    const nodeListIndex = DagsPage.template.indexOf('<DagNodeList');
    const advancedIndex = DagsPage.template.indexOf('<details class="dag-advanced-section"');

    expect(finalOutputIndex).toBeGreaterThan(-1);
    expect(factsIndex).toBeGreaterThan(-1);
    expect(runHistoryIndex).toBeGreaterThan(-1);
    expect(nodeListIndex).toBeGreaterThan(-1);
    expect(advancedIndex).toBeGreaterThan(-1);
    expect(finalOutputIndex).toBeLessThan(factsIndex);
    expect(factsIndex).toBeLessThan(runHistoryIndex);
    expect(runHistoryIndex).toBeLessThan(nodeListIndex);
    expect(nodeListIndex).toBeLessThan(advancedIndex);
    expect(DagsPage.template).not.toContain('<details class="dag-advanced-section" open');
  });

  it('normalizes the DAG scanning fields used by the list', () => {
    const props = reactive({
      items: [
        {
          dag_key: 'daily-brief',
          title: 'Daily Brief',
          status: 'ready', trigger: { type: 'cron', schedule: '0 9 * * *' },
          next_run_at: '2026-05-27T01:00:00Z',
          latest_run: { run_key: 'run-7', status: 'done' },
          metadata: { final_output: { type: 'file', path: 'reports/daily.pptx' } },
        },
        {
          dag_key: 'real-dashboard-shape',
          title: 'Real Dashboard Shape',
          status: 'running',
          trigger: 'scheduled',
          cron_expr: '0 8 * * *',
          latest_run: {
            run_key: 'run-8',
            status: 'succeeded',
            metadata: { final_output: { kind: 'text', text: 'ready' } },
          },
        },
        {
          dagKey: 'code-review',
          status: 'idle',
          triggerType: 'manual',
          latestRunStatus: 'running',
        },
      ],
      emptyText: '暂无 DAG',
    });

    const vm = DagsPage.setup(props, { emit: vi.fn() });

    expect(vm.rows.value).toHaveLength(3);
    expect(vm.rows.value[0]).toMatchObject({
      key: 'daily-brief',
      title: 'Daily Brief',
      status: '已启用',
      triggerLabel: '每天 09:00',
      latestRunLabel: '成功',
      hasFinalOutput: true,
    });
    expect(vm.rows.value[1]).toMatchObject({
      key: 'real-dashboard-shape',
      status: '运行中',
      triggerLabel: '每天 08:00',
      latestRunLabel: '成功',
      hasFinalOutput: true,
    });
    expect(vm.rows.value[2]).toMatchObject({
      key: 'code-review',
      title: '任务流程 3',
      status: '空闲',
      triggerLabel: '手动',
      latestRunLabel: '运行中',
      hasFinalOutput: false,
    });
    expect(vm.rows.value[2].title).not.toContain('code-review');
  });

  it('selects rows locally without opening the legacy detail modal', () => {
    const props = reactive({
      items: [
        { dag_key: 'dag-a', title: 'Dag A' },
        { dag_key: 'dag-b', title: 'Dag B' },
      ],
      emptyText: '暂无 DAG',
    });
    const emit = vi.fn();

    const vm = DagsPage.setup(props, { emit });
    expect(vm.selectedRow.value.key).toBe('dag-a');

    vm.selectDag(vm.rows.value[1]);

    expect(vm.selectedRow.value.key).toBe('dag-b');
    expect(emit).not.toHaveBeenCalled();
  });

  it('keeps loading and error states distinct from an empty DAG list', () => {
    const props = reactive({
      items: [],
      loading: false,
      error: 'dashboard failed for dag_key=dag-a run_key=run-1',
    });
    const vm = DagsPage.setup(props, { emit: vi.fn() });

    expect(vm.pageErrorText.value).toBe('加载任务流程失败，请稍后重试。');
    expect(vm.pageErrorText.value).not.toContain('dag_key');
    expect(vm.pageErrorText.value).not.toContain('run_key');
    expect(DagsPage.props.loading).toMatchObject({ type: Boolean, default: false });
    expect(DagsPage.props.error).toMatchObject({ type: String, default: '' });
    expect(DagsPage.template).toContain('data-testid="dag-console-loading"');
    expect(DagsPage.template).toContain('data-testid="dag-console-error"');
    expect(DagsPage.template).toContain('v-if="loading"');
    expect(DagsPage.template).toContain('v-else-if="pageErrorText"');
    expect(DagsPage.template).toContain('{{ pageErrorText }}');
    expect(DagsPage.template).not.toContain('{{ error }}');
  });

  it('tolerates Vue runtime setup calls without an emit context', () => {
    const props = reactive({
      items: [{ dag_key: 'dag-a', title: 'Dag A' }],
      emptyText: '暂无 DAG',
    });

    const vm = DagsPage.setup(props, null);
    expect(vm.selectedRow.value.key).toBe('dag-a');
    expect(() => vm.selectDag(vm.rows.value[0])).not.toThrow();
  });

  it('keeps selection stable across refreshes and resets when the selected DAG disappears', async () => {
    const props = reactive({
      items: [
        { dag_key: 'dag-a', title: 'Dag A' },
        { dag_key: 'dag-b', title: 'Dag B' },
      ],
      emptyText: '暂无 DAG',
    });

    const vm = DagsPage.setup(props, { emit: vi.fn() });
    vm.selectDag(vm.rows.value[1]);
    expect(vm.selectedRow.value.key).toBe('dag-b');

    props.items = [
      { dag_key: 'dag-a', title: 'Dag A refreshed' },
      { dag_key: 'dag-b', title: 'Dag B refreshed' },
    ];
    await nextTick();
    expect(vm.selectedRow.value.key).toBe('dag-b');

    props.items = [{ dag_key: 'dag-a', title: 'Dag A refreshed' }];
    await nextTick();
    expect(vm.selectedRow.value.key).toBe('dag-a');

    props.items = [];
    await nextTick();
    expect(vm.selectedRow.value).toBeNull();
  });

  it('does not reload detail or reset run selection when refresh keeps the same selected dag key', async () => {
    const props = reactive({
      items: [
        { dag_key: 'dag-a', title: 'Dag A' },
        { dag_key: 'dag-b', title: 'Dag B' },
      ],
      emptyText: '暂无 DAG',
    });

    const vm = DagsPage.setup(props, { emit: vi.fn() });
    vm.selectDag(vm.rows.value[1]);
    expect(detailMock.open).toHaveBeenCalledTimes(2);

    props.items = [
      { dag_key: 'dag-a', title: 'Dag A refreshed' },
      { dag_key: 'dag-b', title: 'Dag B refreshed' },
    ];
    await nextTick();

    expect(vm.selectedRow.value.key).toBe('dag-b');
    expect(detailMock.open).toHaveBeenCalledTimes(2);
  });

  it('keeps the detail pane empty when there are no DAGs', () => {
    const props = reactive({ items: [], emptyText: '暂无 DAG' });
    const vm = DagsPage.setup(props, { emit: vi.fn() });

    expect(vm.rows.value).toEqual([]);
    expect(vm.selectedRow.value).toBeNull();
    expect(DagsPage.template).toContain('{{ emptyText }}');
  });

  it('keeps the console scrollable inside the fixed page shell', () => {
    const css = readCSS('styles/dag-console.css');
    const pageBlock = cssBlock(css, '.dag-console-page');
    const shellBlock = cssBlock(css, '.dag-console-shell');
    const listPaneBlock = cssBlock(css, '.dag-console-list-pane');
    const detailPaneBlock = cssBlock(css, '.dag-console-detail-pane');
    const headingTitleBlock = cssBlock(css, '.dag-console-detail-heading h3');
    const stopButtonBlock = cssBlock(css, '.dag-stop-button');
    const mediaBlock = css.match(/@media\s*\(max-width:\s*920px\)\s*\{([\s\S]*)\}\s*$/)?.[1] || '';

    expect(pageBlock).toMatch(/min-height\s*:\s*0/);
    expect(shellBlock).toMatch(/flex\s*:\s*1/);
    expect(shellBlock).toMatch(/overflow\s*:\s*hidden/);
    expect(listPaneBlock).toMatch(/overflow\s*:\s*auto/);
    expect(detailPaneBlock).toMatch(/overflow\s*:\s*auto/);
    expect(headingTitleBlock).toMatch(/min-width\s*:\s*0/);
    expect(headingTitleBlock).toMatch(/overflow-wrap\s*:\s*anywhere/);
    expect(stopButtonBlock).toContain('var(--error)');
    expect(css).toMatch(/\.dag-console-error-inline\s*\{[^}]*color\s*:\s*var\(--error\)/);
    expect(mediaBlock).toMatch(/\.dag-console-facts\s*\{[^}]*grid-template-columns\s*:\s*1fr/);
  });
});
