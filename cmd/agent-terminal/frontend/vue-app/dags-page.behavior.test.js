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
    dag: null,
    nodes: [],
    runs: [],
    run: null,
    selectedRunKey: '',
    finalOutput: null,
    starting: false,
    startError: null,
  },
  open: vi.fn(),
  start: vi.fn(),
  selectRun: vi.fn(),
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
import { DagFinalOutputPanel } from './components/dag/DagFinalOutputPanel.js';
import { DagNodeList } from './components/dag/DagNodeList.js';

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
  detailMock.state.dag = null;
  detailMock.state.nodes = [];
  detailMock.state.runs = [];
  detailMock.state.run = null;
  detailMock.state.selectedRunKey = '';
  detailMock.state.finalOutput = null;
  detailMock.state.starting = false;
  detailMock.state.startError = null;
  detailMock.open.mockReset();
  detailMock.start.mockReset();
  detailMock.selectRun.mockReset();
  apiMock.callAPI.mockReset().mockImplementation(async () => null);
}

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
    expect(vm.dagDetail).toBe(detailMock);
    expect(vm.detailState).toBe(detailMock.state);
    expect(detailMock.open).toHaveBeenCalledWith(props.items[0]);

    vm.selectDag(vm.rows.value[1]);

    expect(detailMock.open).toHaveBeenLastCalledWith(props.items[1]);
    expect(DagsPage.template).toContain('<DagNodeList');
    expect(DagsPage.template).toContain('<DagRunHistoryPanel');
    expect(DagsPage.template).toContain('<DagFinalOutputPanel');
  });

  it('exposes start disabled reason, starts through the detail composable, and keeps running latest runs startable', () => {
    const props = reactive({
      items: [{ dag_key: 'dag-a', title: 'Dag A', latest_run: { status: 'running' } }],
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

    expect(vm.startDisabledReason.value).toBe('');
    vm.startSelectedDag();
    expect(detailMock.start).toHaveBeenCalledTimes(1);
    expect(DagsPage.template).toContain('data-testid="dag-start-button"');
    expect(DagsPage.template).toContain('startDisabledReason');
    expect(DagsPage.template).toContain('startErrorText');

    detailMock.state.starting = true;
    expect(DagsPage.setup(props, { emit: vi.fn() }).startDisabledReason.value).toContain('启动中');
    detailMock.state.starting = false;
    props.loading = true;
    expect(vm.startDisabledReason.value).toContain('加载中');
    props.loading = false;
    props.items = [];
    expect(vm.startDisabledReason.value).toContain('未选择 DAG');
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

    expect(vm.runsErrorText.value).toBe('runs unavailable');
    expect(DagsPage.template).toContain('data-testid="dag-runs-error"');
    expect(DagsPage.template).toContain('v-if="runsErrorText"');
    expect(DagsPage.template).toContain('v-else');
  });

  it('uses a two-pane console shell instead of the generic DataPage wrapper', () => {
    expect(DagsPage.name).toBe('DagsPage');
    expect(typeof DagsPage.setup).toBe('function');
    expect(DagsPage.components?.DataPage).toBeUndefined();
    expect(DagsPage.template).toContain('data-testid="dag-console"');
    expect(DagsPage.template).toContain('data-testid="dag-console-list"');
    expect(DagsPage.template).toContain('data-testid="dag-console-detail"');
    expect(DagsPage.template).toContain('{{ row.key }}');
    expect(DagsPage.template).toContain('{{ row.title }}');
    expect(DagsPage.template).toContain('{{ row.status }}');
    expect(DagsPage.template).toContain('{{ row.triggerLabel }}');
    expect(DagsPage.template).toContain('{{ row.latestRunLabel }}');
    expect(DagsPage.template).toContain('v-if="row.hasFinalOutput"');
    expect(DagsPage.template).not.toContain('<DataPage');
  });

  it('normalizes the DAG scanning fields used by the list', () => {
    const props = reactive({
      items: [
        {
          dag_key: 'daily-brief',
          title: 'Daily Brief',
          status: 'ready',
          trigger: { type: 'cron', schedule: '0 9 * * *' },
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
      status: 'ready',
      triggerLabel: 'cron 0 9 * * *',
      latestRunLabel: 'run-7 · done',
      hasFinalOutput: true,
    });
    expect(vm.rows.value[1]).toMatchObject({
      key: 'real-dashboard-shape',
      triggerLabel: 'scheduled 0 8 * * *',
      latestRunLabel: 'run-8 · succeeded',
      hasFinalOutput: true,
    });
    expect(vm.rows.value[2]).toMatchObject({
      key: 'code-review',
      title: 'code-review',
      status: 'idle',
      triggerLabel: 'manual',
      latestRunLabel: 'running',
      hasFinalOutput: false,
    });
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
    expect(DagsPage.props.loading).toMatchObject({ type: Boolean, default: false });
    expect(DagsPage.props.error).toMatchObject({ type: String, default: '' });
    expect(DagsPage.template).toContain('data-testid="dag-console-loading"');
    expect(DagsPage.template).toContain('data-testid="dag-console-error"');
    expect(DagsPage.template).toContain('v-if="loading"');
    expect(DagsPage.template).toContain('v-else-if="error"');
    expect(DagsPage.template).toContain('{{ error }}');
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
    const mediaBlock = css.match(/@media\s*\(max-width:\s*920px\)\s*\{([\s\S]*)\}\s*$/)?.[1] || '';

    expect(pageBlock).toMatch(/min-height\s*:\s*0/);
    expect(shellBlock).toMatch(/flex\s*:\s*1/);
    expect(shellBlock).toMatch(/overflow\s*:\s*hidden/);
    expect(listPaneBlock).toMatch(/overflow\s*:\s*auto/);
    expect(detailPaneBlock).toMatch(/overflow\s*:\s*auto/);
    expect(headingTitleBlock).toMatch(/min-width\s*:\s*0/);
    expect(headingTitleBlock).toMatch(/overflow-wrap\s*:\s*anywhere/);
    expect(mediaBlock).toMatch(/\.dag-console-facts\s*\{[^}]*grid-template-columns\s*:\s*1fr/);
  });
});

describe('DagFinalOutputPanel', () => {
  it('reads file final output through shared-file get and exposes content or errors', async () => {
    apiMock.callAPI.mockReset();
    apiMock.callAPI.mockResolvedValueOnce({ path: 'reports/final.md', content: 'final file content' });
    const props = reactive({
      finalOutput: { kind: 'file', path: 'reports/final.md' },
      runsError: null,
    });
    const vm = DagFinalOutputPanel.setup(props, { emit: vi.fn() });

    expect(vm.outputPath.value).toBe('reports/final.md');
    await vm.readFile();

    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/memory/shared-file/get', { path: 'reports/final.md' });
    expect(vm.fileContent.value).toBe('final file content');

    apiMock.callAPI.mockRejectedValueOnce(new Error('missing file'));
    await vm.readFile();

    expect(vm.fileErrorText.value).toBe('missing file');
    expect(DagFinalOutputPanel.template).toContain('data-testid="dag-final-output-open"');
    expect(DagFinalOutputPanel.template).toContain('data-testid="dag-final-output-read"');
    expect(DagFinalOutputPanel.template).toContain('ui/memory/shared-file/get');
  });

  it('renders compact previews for small text and json final outputs', () => {
    const textVm = DagFinalOutputPanel.setup({
      finalOutput: { kind: 'text', text: 'short answer' },
      runsError: null,
    }, { emit: vi.fn() });
    expect(textVm.previewText.value).toBe('short answer');

    const jsonVm = DagFinalOutputPanel.setup({
      finalOutput: { kind: 'json', result: { verdict: 'pass' } },
      runsError: null,
    }, { emit: vi.fn() });
    expect(jsonVm.previewText.value).toContain('"verdict": "pass"');
    expect(DagFinalOutputPanel.template).toContain('data-testid="dag-final-output-preview"');
    expect(DagFinalOutputPanel.template).toContain('v-else-if="finalOutput"');
  });

  it('ignores stale file reads after the selected final output path changes', async () => {
    let resolveOldRead;
    const oldRead = new Promise((resolve) => { resolveOldRead = resolve; });
    apiMock.callAPI.mockImplementation(async (method, params) => {
      if (method !== 'ui/memory/shared-file/get') throw new Error(`unexpected method ${method}`);
      if (params.path === 'reports/old.md') return oldRead;
      return { path: params.path, content: 'new content' };
    });
    const props = reactive({
      finalOutput: { kind: 'file', path: 'reports/old.md' },
      runsError: null,
    });
    const vm = DagFinalOutputPanel.setup(props, { emit: vi.fn() });

    const pendingOldRead = vm.readFile();
    props.finalOutput = { kind: 'file', path: 'reports/new.md' };
    await nextTick();
    await vm.readFile();

    expect(vm.fileContent.value).toBe('new content');
    resolveOldRead({ path: 'reports/old.md', content: 'old content' });
    await pendingOldRead;

    expect(vm.fileContent.value).toBe('new content');
  });
});

describe('DagNodeList', () => {
  it('shows provider/model/agent metadata from nested agent exec config', () => {
    const vm = DagNodeList.setup({
      nodes: [{
        node_key: 'writer',
        title: 'Writer',
        status: 'ready',
        node_type: 'agent',
        config: { exec: { provider: 'codex', model: 'gpt-5.3-codex', agent_key: 'daily_writer' } },
      }],
    }, { emit: vi.fn() });

    expect(vm.rows.value[0]).toMatchObject({
      key: 'writer',
      nodeType: 'agent',
      providerLabel: 'codex / gpt-5.3-codex / daily_writer',
    });
  });
});
