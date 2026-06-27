import React from 'react';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { MemoryPage } from './MemoryPage.jsx';
import { normalizeMemorySnapshot } from '../../adapters/memoryAdapter.js';
import { fetchMemoryDashboard, upsertMemoryEntry } from '../../services/modules/memoryService.js';

const backend = vi.hoisted(() => ({
  deleteMemoryEntry: vi.fn(),
  fetchMemoryDashboard: vi.fn(),
  getMemoryConsolidationStatus: vi.fn(),
  getMemoryEntry: vi.fn(),
  ignoreMemorySimilarity: vi.fn(),
  mergeMemoryEntries: vi.fn(),
  setMemoryAutoDreamIntent: vi.fn(),
  startConsolidateMemorySimilarities: vi.fn(),
  upsertMemoryEntry: vi.fn(),
}));

vi.mock('../../services/modules/memoryService.js', () => backend);

function memorySnapshot({ privateEntries = [], similarGroups = [], teamEntries = [] } = {}) {
  return {
    overview: {
      autoDreamEnabled: false,
      autoDreamIntent: null,
      health: {
        preferenceCount: privateEntries.length,
        projectCount: teamEntries.length,
        maxPerCategory: 15,
        similarGroups,
      },
    },
    private: { entries: privateEntries },
    team: { entries: teamEntries },
  };
}

function renderMemoryPage(projectPath = '/repo/app') {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryPage projectPath={projectPath} />
    </QueryClientProvider>,
  );
}

function resetMemoryBackendMocks() {
  vi.clearAllMocks();
  fetchMemoryDashboard.mockResolvedValue(normalizeMemorySnapshot(memorySnapshot()));
  upsertMemoryEntry.mockResolvedValue({ path: 'feedback/default.md' });
}

beforeEach(resetMemoryBackendMocks);

afterEach(() => {
  vi.useRealTimers();
});

async function flushPromises(count = 6) {
  for (let index = 0; index < count; index += 1) {
    await Promise.resolve();
  }
}

describe('MemoryPage module export', () => {
  it('exports the memory page component', () => {
    expect(MemoryPage).toBeTypeOf('function');
  });
});

describe('MemoryPage dashboard loading', () => {
  it('loads memory dashboard entries through memory service', async () => {
    fetchMemoryDashboard.mockResolvedValue(normalizeMemorySnapshot(memorySnapshot({
      privateEntries: [{
        name: 'reply-language',
        title: '默认中文',
        description: '回复时使用中文',
        type: 'feedback',
        path: 'feedback/reply-language.md',
        updatedAt: '2026-05-30T08:00:00Z',
        preview: '规则\n默认中文回复',
      }],
      teamEntries: [{
        name: 'dag-policy',
        title: 'DAG 规范',
        description: '任务流程使用 DAG 生命周期。',
        type: 'project',
        path: 'project/dag.md',
        updatedAt: '2026-05-29T08:00:00Z',
        preview: 'DAG 内容',
      }],
    })));

    renderMemoryPage();

    expect(await screen.findByText('默认中文')).toBeInTheDocument();
    expect(fetchMemoryDashboard).toHaveBeenCalledWith('/repo/app');
    expect(screen.getByRole('tab', { name: '偏好 1' })).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByRole('tab', { name: '项目 1' })).toHaveAttribute('aria-selected', 'false');

    fireEvent.click(screen.getByRole('tab', { name: '项目 1' }));

    expect(screen.queryByText('默认中文')).not.toBeInTheDocument();
    expect(screen.getByText('DAG 规范')).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: '项目 1' })).toHaveAttribute('aria-selected', 'true');
  });
});

describe('MemoryPage editor', () => {
  it('creates a preference memory entry with the upsert payload expected by backendApi', async () => {
    renderMemoryPage();

    expect(await screen.findByText('暂无记忆')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '+ 新建 ▾' }));
    fireEvent.click(screen.getByRole('button', { name: '新建偏好' }));

    const editor = await screen.findByRole('dialog', { name: '新建记忆' });
    expect(within(editor).getByLabelText('分类')).toHaveValue('feedback');
    fireEvent.change(within(editor).getByLabelText('描述'), { target: { value: '回复时使用中文' } });
    fireEvent.change(within(editor).getByLabelText('内容'), { target: { value: '规则\n默认中文回复' } });
    fireEvent.click(within(editor).getByRole('button', { name: '保存' }));

    await waitFor(() => {
      expect(upsertMemoryEntry).toHaveBeenCalledWith({
        cwd: '/repo/app',
        target: 'private',
        existingPath: '',
        name: expect.stringMatching(/^feedback-[a-z0-9]+$/),
        description: '回复时使用中文',
        title: '',
        type: 'feedback',
        content: '规则\n默认中文回复',
      });
    });
  });
});

describe('MemoryPage consolidation polling', () => {
  it('aborts the background consolidation poller when the page unmounts', async () => {
    fetchMemoryDashboard.mockResolvedValue(normalizeMemorySnapshot(memorySnapshot({
      similarGroups: [{
        targetA: 'team',
        pathA: 'project/a.md',
        nameA: 'A',
        targetB: 'team',
        pathB: 'project/b.md',
        nameB: 'B',
        score: 0.91,
      }],
    })));
    backend.startConsolidateMemorySimilarities.mockResolvedValue({ status: 'running', jobId: 'job-1' });
    backend.getMemoryConsolidationStatus.mockResolvedValue({ status: 'running' });

    const { unmount } = renderMemoryPage();
    const mergeAll = await screen.findByRole('button', { name: '一键整合全部' });

    vi.useFakeTimers();
    fireEvent.click(mergeAll);
    await act(async () => {
      await flushPromises();
    });
    await act(async () => {
      await flushPromises();
    });

    expect(backend.startConsolidateMemorySimilarities).toHaveBeenCalledTimes(1);
    expect(backend.getMemoryConsolidationStatus).toHaveBeenCalledTimes(1);

    unmount();
    await act(async () => {
      for (let attempt = 0; attempt < 5; attempt += 1) {
        vi.advanceTimersByTime(2000);
        await flushPromises();
      }
    });

    expect(backend.getMemoryConsolidationStatus).toHaveBeenCalledTimes(1);
  });
});
