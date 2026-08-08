import React from 'react';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { expect } from 'vitest';

let backend;
let App;
let state;
let waitForBackendThreadHeading;
function createSharedFileState() {
  let memoryFiles = [
    {
      path: 'reports/final.md',
      content: 'final summary',
      updated_by: 'dag-runner',
      updated_at: '2026-05-30T08:00:00Z',
    },
    {
      path: 'scratch/work.json',
      content: '{"step":1}',
      updated_by: 'agent',
      updated_at: '2026-05-30T07:00:00Z',
    },
  ];
  return {
    payload: () => ({
      files: memoryFiles,
      memory: memoryFiles,
      finalOutputRefs: [{ path: 'reports/final.md', runKey: 'run-1', dagKey: 'daily-brief', sourceNodeKey: 'report' }],
      sharedFileRetention: {
        items: [
          { path: 'reports/final.md', protected: true, cleanupCandidate: false, reason: 'final_output' },
          { path: 'scratch/work.json', protected: false, cleanupCandidate: true, reason: 'unreferenced' },
        ],
        protectedCount: 1,
        cleanupCandidateCount: 1,
      },
    }),
    add(file) {
      memoryFiles = [...memoryFiles, file];
    },
    remove(path) {
      memoryFiles = memoryFiles.filter((item) => item.path !== path);
    },
  };
}

function mockSharedFileWorkflow(sharedFiles) {
  backend.listSharedFiles.mockImplementation(() => Promise.resolve(sharedFiles.payload()));
  backend.readSharedFile.mockImplementation(({ path }) => Promise.resolve({
    path,
    content: path === 'reports/final.md' ? 'FINAL CONTENT' : '{"step":1,"detail":true}',
    updatedBy: path === 'reports/final.md' ? 'dag-runner' : 'agent',
    updatedAt: '2026-05-30T08:30:00Z',
  }));
  backend.deleteSharedFile.mockImplementation(({ path }) => {
    sharedFiles.remove(path);
    return Promise.resolve({ deleted: true });
  });
  backend.saveTextFile.mockResolvedValue('/exports/work.json');
}

async function openSharedFilesPage() {
  render(<App />);
  await waitForBackendThreadHeading();
  await waitFor(() => expect(backend.onBridgeEvent).toHaveBeenCalled());
  fireEvent.click(screen.getByLabelText('共享文件'));

  expect(await screen.findByText('final.md')).toBeInTheDocument();
  expect(screen.getByText('work.json')).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: /刷新/ })).not.toBeInTheDocument();
  expect(screen.getByRole('tab', { name: '全部 2' })).toBeInTheDocument();
  expect(screen.getByRole('tab', { name: '最终产物 1' })).toBeInTheDocument();
  expect(screen.getByRole('tab', { name: '工作文件 1' })).toBeInTheDocument();
  await waitFor(() => {
    expect(backend.listSharedFiles).toHaveBeenCalledWith();
  });
}

async function refreshSharedFilesFromBridge(sharedFiles) {
  sharedFiles.add({
    path: 'scratch/notes.md',
    content: 'fresh notes',
    updated_by: 'agent',
    updated_at: '2026-05-30T09:00:00Z',
  });
  await act(async () => {
    state.bridgeCallback?.({ type: 'ui/shared-files/changed', payload: { path: 'scratch/notes.md', action: 'write' } });
  });
  expect(await screen.findByText('notes.md')).toBeInTheDocument();
  expect(screen.getByRole('tab', { name: '全部 3' })).toBeInTheDocument();
  expect(screen.getByRole('tab', { name: '工作文件 2' })).toBeInTheDocument();
}

async function refreshSharedFilesFromFocus(sharedFiles) {
  sharedFiles.add({
    path: 'scratch/focus-refresh.md',
    content: 'focus refresh',
    updated_by: 'agent',
    updated_at: '2026-05-30T09:01:00Z',
  });
  await act(async () => {
    window.dispatchEvent(new Event('focus'));
  });
  expect(await screen.findByText('focus-refresh.md')).toBeInTheDocument();
  expect(screen.getByRole('tab', { name: '全部 4' })).toBeInTheDocument();
  expect(screen.getByRole('tab', { name: '工作文件 3' })).toBeInTheDocument();
}

async function previewFinalSharedFile() {
  const finalCard = screen.getByText('final.md').closest('article');
  expect(within(finalCard).getByText('最终产物')).toBeInTheDocument();
  expect(within(finalCard).getByRole('button', { name: '不可删除' })).toBeDisabled();
  fireEvent.click(within(finalCard).getByRole('button', { name: '打开' }));

  expect(await screen.findByRole('dialog', { name: '文件预览' })).toBeInTheDocument();
  expect(screen.getByText('FINAL CONTENT')).toBeInTheDocument();
  expect(backend.readSharedFile).toHaveBeenCalledWith({ path: 'reports/final.md' });
  fireEvent.click(screen.getByRole('button', { name: '关闭' }));
}

async function exportAndDeleteWorkSharedFile() {
  const workCard = screen.getByText('work.json').closest('article');
  fireEvent.click(within(workCard).getByRole('button', { name: '导出' }));
  await waitFor(() => {
    expect(backend.saveTextFile).toHaveBeenCalledWith({
      defaultPath: '/repo/app',
      defaultFilename: 'work.json',
      content: '{"step":1,"detail":true}',
    });
  });
  expect(await screen.findByText(/已保存到：\/exports\/work\.json/)).toBeInTheDocument();

  fireEvent.click(within(workCard).getByRole('button', { name: '删除' }));
  expect(await screen.findByRole('dialog', { name: '删除文件' })).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: '确认删除' }));
  await waitFor(() => {
    expect(backend.deleteSharedFile).toHaveBeenCalledWith({ path: 'scratch/work.json' });
  });
  expect(await screen.findByText(/已删除文件：scratch\/work\.json/)).toBeInTheDocument();
}

async function continueChatFromFinalSharedFile() {
  const remainingFinalCard = screen.getByText('final.md').closest('article');
  fireEvent.click(within(remainingFinalCard).getByRole('button', { name: '用此文件继续对话' }));
  const forkCard = await screen.findByTestId('fork-draft-card');
  expect(within(forkCard).getByText('继承自会话：后端线程')).toBeInTheDocument();
  expect(within(forkCard).getByRole('checkbox', { name: '选择共享文件 reports/final.md' })).toBeChecked();
}

function mockWorkflowDagLifecycle() {
  const dag = {
    dag_key: 'daily-brief',
    title: 'Daily Brief',
    description: '每日简报',
    status: 'ready',
    trigger: 'manual',
    version: 7,
  };
  const agentNode = {
    node_key: 'draft',
    title: '起草',
    node_type: 'agent',
    assigned_to: 'agent-a',
    depends_on: [],
    config: {
      provider: 'codex',
      model: 'gpt-5',
      prompt_key: 'main/writer',
      first_turn: '请起草简报',
    },
  };
  backend.getDashboardPage.mockImplementation(({ page }) => Promise.resolve(
    page === 'dags' ? { dags: [dag] } : { skills: [] },
  ));
  backend.getDagDetail.mockResolvedValue({ dag, nodes: [agentNode] });
  let hasActiveRun = false;
  backend.getDagRuns.mockImplementation(({ status }) => Promise.resolve({
    runs: status === 'running' && hasActiveRun ? [{ run_key: 'run-live', status: 'running' }] : [],
  }));
  backend.getDagRun.mockResolvedValue({ run: { run_key: 'run-live', status: 'running' }, nodes: [agentNode] });
  backend.startDag.mockImplementation(() => {
    hasActiveRun = true;
    return Promise.resolve({ runKey: 'run-live' });
  });
  backend.terminateDagRun.mockImplementation(() => {
    hasActiveRun = false;
    return Promise.resolve({ ok: true });
  });
  backend.getPreference.mockImplementation(({ key }) => Promise.resolve({
    'settings.provider.active': 'codex',
    'settings.provider.codex.model': 'gpt-5.5',
    'settings.provider.codex.effort': 'xhigh',
    'settings.provider.codex.codexHome': '/Users/test/.codex-alt',
    'settings.provider.codex.codexInstanceKey': 'desktop-main',
    'settings.provider.codex.codexModelProvider': 'openrouter',
    'settings.activePromptKey': 'main/reviewer',
  }[key] ?? null));
  backend.startThread.mockResolvedValue({ thread: { id: 'thread-design' }, provider: 'codex', modelProvider: 'codex' });
  backend.getThreadState.mockImplementation(({ threadId }) => Promise.resolve(
    threadId === 'thread-design'
      ? {
          timelinesByThread: {},
          activeThreadId: 'thread-design',
          threads: [{ id: 'thread-design', name: 'AI 设计流程', provider: 'codex', status: 'created', agentKey: 'dag_designer' }],
        }
      : {
          activeThreadId: 'thread-1',
          threads: [{ id: 'thread-1', name: '后端线程', provider: 'codex', status: '工作中' }],
          timelinesByThread: {
            'thread-1': [{ id: 'assistant-1', kind: 'assistant', text: '来自后端的消息', ts: '2026-05-30T00:00:00Z' }],
          },
          diffTextByThread: {
            'thread-1': 'diff --git a/file b/file',
          },
        },
  ));
}

async function openWorkflowDashboard() {
  render(<App />);
  fireEvent.click(await screen.findByLabelText('自动化'));
  expect((await screen.findAllByText('Daily Brief')).length).toBeGreaterThanOrEqual(2);
}

async function runAndStopWorkflowDag() {
  fireEvent.click(await screen.findByRole('button', { name: '运行' }));
  await waitFor(() => {
    expect(backend.startDag).toHaveBeenCalledWith(expect.objectContaining({
      dagKey: 'daily-brief',
      triggerSource: 'manual',
    }));
  });

  fireEvent.click(await screen.findByRole('button', { name: '停止运行' }));
  await waitFor(() => {
    expect(backend.terminateDagRun).toHaveBeenCalledWith({
      dagKey: 'daily-brief',
      runKey: 'run-live',
      reason: 'user_requested',
    });
  });
  await waitFor(() => expect(screen.queryByRole('button', { name: '停止运行' })).not.toBeInTheDocument());
}

async function createWorkflowSchedule() {
  fireEvent.click(screen.getByRole('button', { name: '创建定时任务' }));
  const scheduleDialog = await screen.findByRole('dialog', { name: '创建定时任务' });
  expect(scheduleDialog).toBeInTheDocument();
  expect(within(scheduleDialog).queryByLabelText('Cron 表达式')).not.toBeInTheDocument();
  fireEvent.change(within(scheduleDialog).getByLabelText('运行频率'), { target: { value: 'weekdays' } });
  fireEvent.change(within(scheduleDialog).getByLabelText('运行时间'), { target: { value: '09:00' } });
  expect(within(scheduleDialog).getByText('工作日 09:00 自动运行')).toBeInTheDocument();
  fireEvent.click(within(scheduleDialog).getByRole('button', { name: '创建定时任务' }));
  await waitFor(() => {
    expect(backend.applyDagOps).toHaveBeenCalledWith({
      dagKey: 'daily-brief',
      baseVersion: 7,
      ops: [{ op: 'update_dag', patch: { trigger: 'scheduled', cron_expr: 'CRON_TZ=Asia/Shanghai 0 9 * * 1-5' } }],
    });
  });
  expect(await screen.findByText('已保存定时任务')).toBeInTheDocument();
}

async function editWorkflowStep() {
  fireEvent.click(screen.getByText('高级设置'));
  fireEvent.input(screen.getByLabelText('名称'), { target: { value: '起草 v2' } });
  expect(screen.getByLabelText('名称')).toHaveValue('起草 v2');
  expect(screen.getByLabelText('执行者')).toHaveValue('agent-a');
  fireEvent.input(screen.getByLabelText('执行者'), { target: { value: 'agent-b' } });
  fireEvent.change(screen.getByLabelText('依赖步骤'), { target: { value: 'outline' } });
  expect(screen.queryByLabelText('Provider')).not.toBeInTheDocument();
  expect(screen.getByLabelText('执行引擎')).toHaveValue('codex');
  expect(screen.getByLabelText('Prompt Key')).toHaveValue('main/writer');
  fireEvent.click(screen.getByRole('button', { name: '保存步骤' }));
  await waitFor(() => {
    expect(backend.applyDagOps).toHaveBeenCalledWith({
      dagKey: 'daily-brief',
      baseVersion: 7,
      ops: [expect.objectContaining({
        op: 'update_node',
        node_key: 'draft',
        patch: expect.objectContaining({
          title: '起草 v2',
          assigned_to: 'agent-b',
          depends_on: ['outline'],
          config: expect.objectContaining({
            exec: expect.objectContaining({ provider: 'codex', model: 'gpt-5', prompt_key: 'main/writer' }),
          }),
        }),
      })],
    });
  });
}

async function deleteWorkflowDag() {
  fireEvent.click(screen.getByRole('button', { name: '删除' }));
  expect(await screen.findByRole('dialog', { name: '删除自动化' })).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: '确认删除' }));
  await waitFor(() => {
    expect(backend.deleteDag).toHaveBeenCalledWith({ dagKey: 'daily-brief' });
  });
}

async function designWorkflowWithAi() {
  fireEvent.click(screen.getByRole('button', { name: '自由设计' }));
  await waitFor(() => {
    expect(backend.startThread).toHaveBeenCalledWith(expect.objectContaining({
      cwd: '/repo/app',
      modelProvider: 'codex',
      model: 'gpt-5.5',
      effort: 'xhigh',
      name: 'AI 设计流程',
      agentKey: 'dag_designer',
      promptKey: 'main/dag_designer_zh',
      deferSpawn: true,
    }));
    const designPayload = backend.startThread.mock.calls.at(-1)[0];
    expect(designPayload.provider).toBe('codex');
    expect(designPayload.config).toEqual(expect.objectContaining({
      codexHome: '/Users/test/.codex-alt',
      codexInstanceKey: 'desktop-main',
      codexModelProvider: 'openrouter',
      providerNativeSkills: false,
    }));
    expect(designPayload.config.enabledTools).toContain('task_start_dag');
    expect(designPayload.config.enabledTools).toContain('task_get_run');
    expect(designPayload.config.enabledTools).toContain('task_list_runs');
    expect(designPayload.config.enabledTools).toContain('task_dispatch_node');
    expect(designPayload.config.enabledTools).toContain('workflow_template_list');
    expect(designPayload.config.enabledTools).toContain('workflow_template_get');
    expect(designPayload.config.enabledTools).toContain('workflow_template_render_dag');
    expect(designPayload.config.enabledTools).not.toContain('task_update_node');
  });
  expect(await screen.findByRole('status')).toHaveTextContent('AI 设计流程已创建');
  fireEvent.click(screen.getByRole('button', { name: '查看设计对话' }));
  expect((await screen.findAllByText('AI 设计流程')).length).toBeGreaterThanOrEqual(1);
  expect(screen.queryByText('unknown')).not.toBeInTheDocument();
}

export function createAppTestRoutes(nextContext) {
  backend = nextContext.backend;
  App = nextContext.App;
  state = nextContext.state;
  waitForBackendThreadHeading = nextContext.waitForBackendThreadHeading;
  return {
    createSharedFileState: (...args) => createSharedFileState(...args),
    mockSharedFileWorkflow: (...args) => mockSharedFileWorkflow(...args),
    openSharedFilesPage: (...args) => openSharedFilesPage(...args),
    refreshSharedFilesFromBridge: (...args) => refreshSharedFilesFromBridge(...args),
    refreshSharedFilesFromFocus: (...args) => refreshSharedFilesFromFocus(...args),
    previewFinalSharedFile: (...args) => previewFinalSharedFile(...args),
    exportAndDeleteWorkSharedFile: (...args) => exportAndDeleteWorkSharedFile(...args),
    continueChatFromFinalSharedFile: (...args) => continueChatFromFinalSharedFile(...args),
    mockWorkflowDagLifecycle: (...args) => mockWorkflowDagLifecycle(...args),
    openWorkflowDashboard: (...args) => openWorkflowDashboard(...args),
    runAndStopWorkflowDag: (...args) => runAndStopWorkflowDag(...args),
    createWorkflowSchedule: (...args) => createWorkflowSchedule(...args),
    editWorkflowStep: (...args) => editWorkflowStep(...args),
    deleteWorkflowDag: (...args) => deleteWorkflowDag(...args),
    designWorkflowWithAi: (...args) => designWorkflowWithAi(...args),
  };
}
