import React from 'react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { WorkflowPage } from './WorkflowPage.jsx';

const backend = vi.hoisted(() => ({
  applyDagOps: vi.fn(),
  deleteDag: vi.fn(),
  getDashboardPage: vi.fn(),
  getDagDetail: vi.fn(),
  getDagRun: vi.fn(),
  getDagRuns: vi.fn(),
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
      expect(backend.getDagRuns).toHaveBeenCalledWith({ dagKey: 'daily-brief', limit: 5 });
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
