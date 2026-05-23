// @ts-nocheck
import { describe, expect, it, vi } from 'vitest';
import { nextTick, reactive } from '../lib/vue.esm-browser.prod.js';

const apiMock = vi.hoisted(() => ({
  callAPI: vi.fn(async () => null),
}));

vi.mock('./services/api.js', () => ({
  callAPI: apiMock.callAPI,
}));

import { DagFinalOutputPanel } from './components/dag/DagFinalOutputPanel.js';
import { DagNodeEditForm } from './components/dag/DagNodeEditForm.js';
import { DagNodeList } from './components/dag/DagNodeList.js';
import { DagRunHistoryPanel } from './components/dag/DagRunHistoryPanel.js';
import { DagSharedFilesPanel } from './components/dag/DagSharedFilesPanel.js';
import { DagTopologyPanel } from './components/dag/DagTopologyPanel.js';

describe('DagTopologyPanel', () => {
  it('uses safe Mermaid ids while preserving special node labels', () => {
    const vm = DagTopologyPanel.setup({
      nodes: [
        {
          node_key: 'draft.node/1',
          title: 'Draft "Node"',
          depends_on: ['collect.raw/1', 'external input'],
        },
        {
          node_key: 'collect.raw/1',
          title: 'Collect Raw',
          depends_on: [],
        },
      ],
    });

    expect(vm.mermaidSource.value).toContain('n1["Draft \\"Node\\""]');
    expect(vm.mermaidSource.value).toContain('n2["Collect Raw"]');
    expect(vm.mermaidSource.value).toContain('n2 --> n1');
    expect(vm.mermaidSource.value).toContain('d1["外部依赖 1"]');
    expect(vm.mermaidSource.value).toContain('d1 --> n1');
    expect(vm.mermaidSource.value).not.toContain('collect.raw/1 --> draft.node/1');
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

    apiMock.callAPI.mockRejectedValueOnce(new Error('shared file reports/final.md missing dag_key'));
    await vm.readFile();

    expect(vm.fileErrorText.value).toBe('无法读取最终结果文件，请稍后重试。');
    expect(vm.fileErrorText.value).not.toContain('shared file');
    expect(vm.fileErrorText.value).not.toContain('dag_key');
    expect(DagFinalOutputPanel.template).toContain('data-testid="dag-final-output-open"');
    expect(DagFinalOutputPanel.template).toContain('data-testid="dag-final-output-read"');
    expect(DagFinalOutputPanel.template).toContain('ui/memory/shared-file/get');
  });

  it('renders compact previews without exposing raw final output internals', () => {
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

    const rawVm = DagFinalOutputPanel.setup({
      finalOutput: { kind: 'sharedfile', run_key: 'run-secret', dag_key: 'dag-secret' },
      runsError: null,
    }, { emit: vi.fn() });
    expect(rawVm.outputKind.value).toBe('文件');
    expect(rawVm.previewText.value).toBe('已生成最终结果，暂不支持预览。');
    expect(rawVm.previewText.value).not.toContain('run-secret');
    expect(rawVm.previewText.value).not.toContain('dag-secret');

    const sharedFileVm = DagFinalOutputPanel.setup({
      finalOutput: { kind: 'sharedfile', shared_file: { path: 'reports/final.md' } },
      runsError: null,
    }, { emit: vi.fn() });
    expect(sharedFileVm.outputPath.value).toBe('reports/final.md');

    expect(DagFinalOutputPanel.template).toContain('data-testid="dag-final-output-preview"');
    expect(DagFinalOutputPanel.template).toContain('v-else-if="finalOutput"');
    expect(DagFinalOutputPanel.template).toContain('当前运行尚未标记最终结果。');
    expect(DagFinalOutputPanel.template).not.toContain('最终产物');
  });

  it('sanitizes run-history errors shown in the final output panel', () => {
    const vm = DagFinalOutputPanel.setup({
      finalOutput: null,
      runsError: new Error('run_key run-1 failed for dag_key dag-a'),
    }, { emit: vi.fn() });

    expect(vm.runsErrorText.value).toBe('无法加载运行历史，请稍后重试。');
    expect(vm.runsErrorText.value).not.toContain('run_key');
    expect(vm.runsErrorText.value).not.toContain('dag_key');
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

describe('DagRunHistoryPanel', () => {
  it('keeps run keys internal while showing user-facing run labels and statuses', () => {
    const emit = vi.fn();
    const vm = DagRunHistoryPanel.setup({
      runs: [
        { run_key: 'run-new', status: 'succeeded', started_at: '2026-05-23T12:00:00Z' },
        { run_key: 'run-old', status: 'running' },
      ],
      selectedRunKey: 'run-old',
    }, { emit });

    expect(vm.rows.value[0]).toMatchObject({
      key: 'run-new',
      label: '第 1 次运行',
      status: '成功',
      startedAt: '2026-05-23T12:00:00Z',
    });
    expect(vm.rows.value[1]).toMatchObject({
      key: 'run-old',
      label: '第 2 次运行',
      status: '运行中',
    });
    expect(DagRunHistoryPanel.template).toContain('{{ row.label }}');
    expect(DagRunHistoryPanel.template).not.toContain('{{ row.key }}');

    vm.selectRun(vm.rows.value[1]);
    expect(emit).toHaveBeenCalledWith('select-run', 'run-old');
  });
});

describe('DagNodeList', () => {
  it('keeps technical step metadata internal while exposing user-facing step state and chat action', () => {
    const emit = vi.fn();
    const vm = DagNodeList.setup({
      nodes: [{
        node_key: 'writer',
        title: 'Writer',
        status: 'ready',
        node_type: 'agent',
        config: { exec: { provider: 'codex', model: 'gpt-5.3-codex', agent_key: 'daily_writer' } },
        spawning_thread_id: 'thread-writer',
      }],
    }, { emit });

    expect(vm.rows.value[0]).toMatchObject({
      key: 'writer',
      title: 'Writer',
      status: '可运行',
      chatLabel: '查看对话',
    });

    const fallbackVm = DagNodeList.setup({
      nodes: [{ node_key: 'internal_writer', status: 'ready', node_type: 'agent' }],
    }, { emit: vi.fn() });
    expect(fallbackVm.rows.value[0]).toMatchObject({
      key: 'internal_writer',
      title: '步骤 1',
    });
    expect(fallbackVm.rows.value[0].title).not.toContain('internal_writer');

    expect(DagNodeList.template).not.toContain('{{ row.nodeType }}');
    expect(DagNodeList.template).not.toContain('{{ row.providerLabel }}');
    expect(DagNodeList.template).not.toContain('{{ row.spawningThreadId }}');

    vm.openChat(vm.rows.value[0]);
    expect(emit).toHaveBeenCalledWith('open-chat', 'thread-writer');
  });
});

describe('DagNodeEditForm', () => {
  it('uses step labels when editable agent nodes do not have user-facing titles', async () => {
    const vm = DagNodeEditForm.setup({
      nodes: [{ node_key: 'internal_writer', node_type: 'agent', config: { exec: {} } }],
      savingNodeKey: '',
      saveError: null,
    }, { emit: vi.fn() });
    await nextTick();

    expect(vm.form.nodeKey).toBe('internal_writer');
    expect(vm.form.title).toBe('步骤 1');
    expect(DagNodeEditForm.template).toContain('displayNodeLabel(node, index)');
    expect(DagNodeEditForm.template).not.toContain('node.node_key }}');
    expect(DagNodeEditForm.template).not.toContain('node.nodeKey }}');
  });

  it('sanitizes save errors shown by the advanced node form', () => {
    const vm = DagNodeEditForm.setup({
      nodes: [{ node_key: 'writer', node_type: 'agent', config: { exec: {} } }],
      savingNodeKey: '',
      saveError: new Error('save failed for dag_key dag-a node_key writer'),
    }, { emit: vi.fn() });

    expect(vm.saveErrorText.value).toBe('保存步骤失败，请稍后重试。');
    expect(vm.saveErrorText.value).not.toContain('dag_key');
    expect(vm.saveErrorText.value).not.toContain('node_key');
  });
});

describe('DagTopologyPanel', () => {
  it('uses generated labels for untitled nodes and missing dependencies', () => {
    const vm = DagTopologyPanel.setup({
      nodes: [{
        node_key: 'internal_writer',
        depends_on: ['internal_collect'],
      }],
    });

    expect(vm.rows.value[0]).toMatchObject({
      key: 'internal_writer',
      title: '步骤 1',
      dependsOn: [{ key: 'internal_collect', label: '外部依赖 1' }],
    });
    expect(vm.mermaidSource.value).toContain('n1["步骤 1"]');
    expect(vm.mermaidSource.value).toContain('d1["外部依赖 1"]');
    expect(vm.mermaidSource.value).not.toContain('internal_writer');
    expect(vm.mermaidSource.value).not.toContain('internal_collect');
  });
});

describe('DagSharedFilesPanel', () => {
  it('labels work-file rows by step order and maps lock modes without exposing node keys', () => {
    const vm = DagSharedFilesPanel.setup({
      nodes: [{
        node_key: 'writer',
        config: {
          inputs: { from_sharedfiles: ['inputs/raw.md'] },
          outputs: { to_sharedfile: { path: 'reports/final.md', lock_mode: 'exclusive' } },
        },
      }],
    });

    expect(vm.rows.value).toMatchObject([
      { stepLabel: '第 1 步', path: 'inputs/raw.md', mode: '读取', lockMode: '-' },
      { stepLabel: '第 1 步', path: 'reports/final.md', mode: '写入', lockMode: '独占写入' },
    ]);
    expect(DagSharedFilesPanel.template).toContain('{{ row.stepLabel }}');
    expect(DagSharedFilesPanel.template).not.toContain('{{ row.nodeKey }}');
    expect(DagSharedFilesPanel.template).not.toContain('{{ row.lockMode }}');
  });
});
