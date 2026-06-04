import React from 'react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { WorkflowPage } from './WorkflowPage.jsx';

const backend = vi.hoisted(() => ({
  applyDagOps: vi.fn(),
  deleteDag: vi.fn(),
  getDashboardPage: vi.fn(),
  getDagDetail: vi.fn(),
  getDagRun: vi.fn(),
  getDagRuns: vi.fn(),
  readSharedFile: vi.fn(),
  startDag: vi.fn(),
  startTurn: vi.fn(),
  startThread: vi.fn(),
  terminateDagRun: vi.fn(),
}));

vi.mock('../../shared/api/backendApi.js', () => backend);

function renderWorkflowPage(store = {}) {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  });

  return render(
    <QueryClientProvider client={queryClient}>
      <WorkflowPage projectPath="/repo/app" store={store} />
    </QueryClientProvider>,
  );
}

function mockWorkflowDag() {
  const dag = {
    dag_key: 'daily-brief',
    title: 'Daily Brief',
    status: 'ready',
    trigger: 'manual',
    version: 7,
  };

  backend.getDashboardPage.mockResolvedValue({ dags: [dag] });
  backend.getDagDetail.mockResolvedValue({
    dag,
    nodes: [
      {
        node_key: 'draft',
        title: 'Draft',
        node_type: 'agent',
        assigned_to: 'codex',
        depends_on: [],
      },
    ],
  });
  backend.getDagRuns.mockResolvedValue({ runs: [] });
  backend.getDagRun.mockResolvedValue({ run: null, nodes: [] });
  backend.startDag.mockResolvedValue({ run_key: 'run-live' });
}

describe('WorkflowPage module', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('exports the workflow page component', () => {
    expect(WorkflowPage).toBeTypeOf('function');
  });

  it('loads DAG dashboard data and fetches selected DAG detail', async () => {
    mockWorkflowDag();

    renderWorkflowPage();

    expect((await screen.findAllByText('Daily Brief')).length).toBeGreaterThanOrEqual(2);
    expect(screen.getByRole('tab', { name: '历史记录 1' })).toHaveAttribute('aria-selected', 'true');

    await waitFor(() => {
      expect(backend.getDashboardPage).toHaveBeenCalledWith({ cwd: '/repo/app', page: 'dags' });
      expect(backend.getDagDetail).toHaveBeenCalledWith({ dagKey: 'daily-brief' });
      expect(backend.getDagRuns).toHaveBeenCalledWith({ dagKey: 'daily-brief', limit: 30 });
      expect(backend.getDagRuns).toHaveBeenCalledWith({ dagKey: 'daily-brief', status: 'running', limit: 1 });
    });
  });

  it('starts the selected DAG with the manual trigger payload', async () => {
    mockWorkflowDag();

    renderWorkflowPage();

    fireEvent.click(await screen.findByRole('button', { name: '运行' }));

    await waitFor(() => {
      expect(backend.startDag).toHaveBeenCalledWith(expect.objectContaining({
        dagKey: 'daily-brief',
        triggerSource: 'manual',
      }));
    });

    const payload = backend.startDag.mock.calls[0][0];
    expect(payload.idempotencyKey).toMatch(/^ui-/);
  });

  it('restores the run action and shows an error when startDag times out', async () => {
    mockWorkflowDag();
    backend.startDag.mockReturnValue(new Promise(() => {}));

    renderWorkflowPage();

    const runButton = await screen.findByRole('button', { name: '运行' });
    vi.useFakeTimers();
    fireEvent.click(runButton);

    expect(screen.getByRole('button', { name: '启动中...' })).toBeDisabled();

    await act(async () => {
      await vi.advanceTimersByTimeAsync(8000);
    });

    expect(screen.getByRole('button', { name: '运行' })).not.toBeDisabled();
    expect(screen.getByRole('alert')).toHaveTextContent(
      '启动自动化失败：自动化操作超时，请检查任务数据或后端状态。',
    );
  });

  it('formats the active run row time as a readable date and time', async () => {
    mockWorkflowDag();
    backend.getDagRuns.mockImplementation((params = {}) => Promise.resolve({
      runs: params.status === 'running'
        ? []
        : [{
            run_key: 'run-readable-time',
            status: 'succeeded',
            started_at: '2026-05-30T08:09:10Z',
          }],
    }));
    backend.getDagRun.mockResolvedValue({
      run: { run_key: 'run-readable-time', status: 'succeeded' },
      nodes: [],
    });

    renderWorkflowPage();

    const runRow = await screen.findByRole('button', { name: /第 1 次运行/ });
    await waitFor(() => expect(runRow).toHaveClass('active'));
    const time = runRow.querySelector('time');
    expect(time).toHaveTextContent('2026-05-30 08:09:10');
    expect(time).toHaveAttribute('dateTime', '2026-05-30T08:09:10Z');
    expect(time).toHaveAttribute('title', '2026-05-30T08:09:10Z');
  });

  it('numbers run rows by chronological run order even when the backend returns newest first', async () => {
    mockWorkflowDag();
    backend.getDagRuns.mockImplementation((params = {}) => Promise.resolve({
      runs: params.status === 'running'
        ? []
        : [
            { run_key: 'run-later', status: 'running', started_at: '2026-06-02T20:12:43.303129+08:00' },
            { run_key: 'run-earlier', status: 'cancelled', started_at: '2026-06-02T17:22:56.112233+08:00' },
          ],
    }));
    backend.getDagRun.mockResolvedValue({ run: { run_key: 'run-later', status: 'running' }, nodes: [] });

    renderWorkflowPage();

    const rows = await screen.findAllByRole('button', { name: /第 \d+ 次运行/ });
    expect(rows[0]).toHaveTextContent('第 1 次运行');
    expect(rows[0]).toHaveTextContent('2026-06-02 17:22:56');
    expect(rows[1]).toHaveTextContent('第 2 次运行');
    expect(rows[1]).toHaveTextContent('2026-06-02 20:12:43');
  });

  it('keeps the newest ten runs visible and collapses earlier runs behind a themed expand button', async () => {
    mockWorkflowDag();
    const runs = Array.from({ length: 12 }, (_, index) => ({
      run_key: `run-${String(index + 1).padStart(2, '0')}`,
      status: index === 11 ? 'running' : 'succeeded',
      started_at: `2026-06-${String(index + 1).padStart(2, '0')}T08:00:00Z`,
    })).reverse();
    backend.getDagRuns.mockImplementation((params = {}) => Promise.resolve({
      runs: params.status === 'running' ? [] : runs,
    }));
    backend.getDagRun.mockResolvedValue({ run: { run_key: 'run-12', status: 'running' }, nodes: [] });

    renderWorkflowPage();

    expect(await screen.findByRole('button', { name: /第 3 次运行/ })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /第 12 次运行/ })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /第 1 次运行/ })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /第 2 次运行/ })).not.toBeInTheDocument();
    const expand = screen.getByRole('button', { name: '展开较早 2 次运行' });
    expect(expand).toHaveClass('dag-run-list-toggle');
    expect(expand).toHaveAttribute('aria-expanded', 'false');

    fireEvent.click(expand);

    expect(await screen.findByRole('button', { name: /第 1 次运行/ })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /第 2 次运行/ })).toBeInTheDocument();
    const collapse = screen.getByRole('button', { name: '收起较早运行记录' });
    expect(collapse).toHaveAttribute('aria-expanded', 'true');
  });

  it('starts the Douyin 05:00 video automation template with a seeded designer turn', async () => {
    mockWorkflowDag();
    backend.startThread.mockResolvedValue({ thread_id: 'thread-douyin' });
    backend.startTurn.mockResolvedValue({ turn_id: 'turn-douyin' });
    const store = {
      resolveLaunchPreferences: vi.fn().mockResolvedValue({
        modelProvider: 'codex',
        model: 'gpt-5.5',
        effort: 'high',
      }),
      setActiveThread: vi.fn(),
      setActivePage: vi.fn(),
    };

    renderWorkflowPage(store);

    fireEvent.click(await screen.findByRole('button', { name: '抖音 5 点模板' }));

    await waitFor(() => {
      expect(backend.startThread).toHaveBeenCalledWith(expect.objectContaining({
        cwd: '/repo/app',
        name: '抖音 5 点视频自动化',
        agentKey: 'dag_designer',
        promptKey: 'main/dag_designer_zh',
        deferSpawn: true,
      }));
      expect(backend.startTurn).toHaveBeenCalledWith(expect.objectContaining({
        cwd: '/repo/app',
        threadId: 'thread-douyin',
      }));
    });
    const turnInput = backend.startTurn.mock.calls[0][0].input;
    expect(turnInput).toContain('daily_workplace_mentor_douyin_video');
    expect(turnInput).toContain('每天 05:00 Asia/Shanghai');
    expect(turnInput).toContain('cron_expr = `0 21 * * *`');
    expect(turnInput).toContain('生成 1 条');
    expect(turnInput).toContain('不自动发布');
    expect(store.setActiveThread).toHaveBeenCalledWith('thread-douyin');
    expect(store.setActivePage).toHaveBeenCalledWith('chat');
  });
});
