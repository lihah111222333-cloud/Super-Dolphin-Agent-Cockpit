// @ts-nocheck
import { describe, expect, it, vi } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { nextTick, reactive } from '../lib/vue.esm-browser.prod.js';

import { DagsPage } from './pages/DagsPage.js';

const FRONTEND_ROOT = resolve(import.meta.dirname, '.');

function readCSS(relativePath) {
  return readFileSync(resolve(FRONTEND_ROOT, relativePath), 'utf-8');
}

function cssBlock(css, selector) {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const match = css.match(new RegExp(`${escaped}\\s*\\{([^}]*)\\}`));
  return match ? match[1].replace(/\/\*[\s\S]*?\*\//g, '') : '';
}

describe('DagsPage console shell', () => {
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
