// @ts-nocheck
import { describe, expect, it, vi } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
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

import { DagsPage } from './pages/DagsPage.js';

const FRONTEND_ROOT = resolve(import.meta.dirname, '.');

function readCSS(relativePath) {
  return readFileSync(resolve(FRONTEND_ROOT, relativePath), 'utf-8');
}

describe('DagsPage category filters', () => {
  it('groups task flows into running scheduled and history tabs without an all tab', () => {
    const props = reactive({
      items: [
        { dag_key: 'scheduled-active', title: 'Scheduled Active', status: 'ready', trigger: 'scheduled', latest_run: { run_key: 'run-1', status: 'running' } },
        { dag_key: 'scheduled-done', title: 'Scheduled Done', status: 'ready', trigger: { type: 'cron', schedule: '0 8 * * *' }, latest_run: { run_key: 'run-2', status: 'succeeded' } },
        { dag_key: 'manual-active', title: 'Manual Active', status: 'ready', trigger: 'manual', latestRunStatus: 'running' },
        { dag_key: 'status-active', title: 'Status Active', status: 'running', trigger: 'manual', latest_run: { run_key: 'run-3', status: 'succeeded' } },
        { dag_key: 'manual-done', title: 'Manual Done', status: 'ready', trigger: 'manual', latest_run: { run_key: 'run-4', status: 'done' } },
        { dag_key: 'manual-draft', title: 'Manual Draft', status: 'draft', trigger: 'manual' },
      ],
      emptyText: '暂无 DAG',
    });

    const vm = DagsPage.setup(props, { emit: vi.fn() });

    expect(vm.categoryTabs.value.map((tab) => tab.label)).toEqual(['进行中', '定时任务', '历史记录']);
    expect(vm.categoryTabs.value.map((tab) => tab.count)).toEqual([3, 2, 2]);
    expect(vm.activeCategory.value).toBe('running');
    expect(vm.visibleRows.value.map((row) => row.key)).toEqual(['scheduled-active', 'manual-active', 'status-active']);

    vm.setCategory('scheduled');
    expect(vm.visibleRows.value.map((row) => row.key)).toEqual(['scheduled-active', 'scheduled-done']);

    vm.setCategory('history');
    expect(vm.visibleRows.value.map((row) => row.key)).toEqual(['manual-done', 'manual-draft']);
    expect(vm.selectedRow.value.key).toBe('manual-done');
    expect(vm.visibleRows.value.find((row) => row.key === 'manual-draft')?.latestRunLabel).toBe('未启动');

    expect(DagsPage.template).toContain('data-testid="dag-category-tabs"');
    expect(DagsPage.template).toContain('role="tablist"');
    expect(DagsPage.template).toContain('role="tab"');
    expect(DagsPage.template).toContain(':aria-selected="activeCategory === tab.key ? \'true\' : \'false\'"');
    expect(DagsPage.template).toContain('v-for="tab in categoryTabs"');
    expect(DagsPage.template).toContain('visibleRows');
    expect(DagsPage.template).not.toContain('全部');

    const css = readCSS('styles/dag-console.css');
    expect(css).toContain('grid-template-columns: repeat(auto-fit, minmax(88px, 1fr));');
    expect(css).toContain('flex-wrap: wrap;');
    expect(css).toContain('overflow-wrap: anywhere;');
  });

  it('allows selecting empty categories and shows the category empty state', () => {
    const props = reactive({
      items: [
        { dag_key: 'manual-done', title: 'Manual Done', status: 'ready', trigger: 'manual', latest_run: { run_key: 'run-4', status: 'done' } },
      ],
      emptyText: '暂无 DAG',
    });

    const vm = DagsPage.setup(props, { emit: vi.fn() });

    expect(vm.categoryTabs.value.map((tab) => `${tab.key}:${tab.count}`)).toEqual(['running:0', 'scheduled:0', 'history:1']);
    expect(vm.activeCategory.value).toBe('history');

    vm.setCategory('running');
    expect(vm.activeCategory.value).toBe('running');
    expect(vm.visibleRows.value).toEqual([]);
    expect(vm.selectedRow.value).toBeNull();

    vm.setCategory('scheduled');
    expect(vm.activeCategory.value).toBe('scheduled');
    expect(vm.visibleRows.value).toEqual([]);

    expect(DagsPage.template).not.toContain(':disabled="tab.count === 0"');
    expect(DagsPage.template).toContain('data-testid="dag-category-empty"');
  });
});
