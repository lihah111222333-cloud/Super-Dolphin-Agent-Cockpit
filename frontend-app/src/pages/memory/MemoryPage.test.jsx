import React from 'react';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { MemoryPage } from './MemoryPage.jsx';
import { normalizeMemorySnapshot } from '../../adapters/memoryAdapter.js';
import { fetchMemoryDashboard, upsertMemoryEntry } from './services/memoryPageService.js';

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

vi.mock('./services/memoryPageService.js', () => backend);

function memorySnapshot({ privateEntries = [], similarGroups = [], similarityDegraded, teamEntries = [] } = {}) {
  return {
    overview: {
      autoDreamEnabled: false,
      autoDreamIntent: null,
      health: {
        preferenceCount: privateEntries.length,
        projectCount: teamEntries.length,
        maxPerCategory: 15,
        similarGroups,
        ...(similarityDegraded === undefined ? {} : { similarityDegraded }),
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
  const view = render(
    <QueryClientProvider client={queryClient}>
      <MemoryPage projectPath={projectPath} />
    </QueryClientProvider>,
  );
  return {
    ...view,
    rerenderProject(nextProjectPath) {
      view.rerender(
        <QueryClientProvider client={queryClient}>
          <MemoryPage projectPath={nextProjectPath} />
        </QueryClientProvider>,
      );
    },
  };
}

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((promiseResolve, promiseReject) => {
    resolve = promiseResolve;
    reject = promiseReject;
  });
  return { promise, reject, resolve };
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
  it('opens the create menu with accessible menu items', async () => {
    renderMemoryPage();

    expect(await screen.findByText('暂无记忆')).toBeInTheDocument();
    const trigger = screen.getByRole('button', { name: '+ 新建 ▾' });
    fireEvent.click(trigger);
    const menu = await screen.findByRole('menu');
    const preferenceItem = within(menu).getByRole('menuitem', { name: '新建偏好' });
    expect(within(menu).getByRole('menuitem', { name: '新建项目' })).toBeInTheDocument();

    expect(preferenceItem).toBeInTheDocument();
  });

	it('creates a preference memory entry with the upsert payload expected by backendApi', async () => {
		renderMemoryPage();

    expect(await screen.findByText('暂无记忆')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '+ 新建 ▾' }));
    fireEvent.click(screen.getByRole('menuitem', { name: '新建偏好' }));

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

	it('blocks editing when detail response omits content', async () => {
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
		})));
		backend.getMemoryEntry.mockResolvedValue({
			target: 'private',
			path: 'feedback/reply-language.md',
			name: 'reply-language',
			description: '回复时使用中文',
			type: 'feedback',
		});

		renderMemoryPage();

		const title = await screen.findByText('默认中文');
		const card = title.closest('article');
		fireEvent.click(within(card).getByRole('button', { name: '编辑' }));

		await waitFor(() => {
			expect(screen.getByText(/记忆详情缺少内容/)).toBeInTheDocument();
		});
		expect(screen.queryByRole('dialog', { name: '编辑记忆' })).not.toBeInTheDocument();
		expect(upsertMemoryEntry).not.toHaveBeenCalled();
	});

  it('ignores stale edit detail responses after switching projects', async () => {
    fetchMemoryDashboard.mockResolvedValue(normalizeMemorySnapshot(memorySnapshot({
      privateEntries: [{
        name: 'reply-language',
        title: '默认中文',
        description: '回复时使用中文',
        target: 'private',
        type: 'feedback',
        path: 'feedback/reply-language.md',
        updatedAt: '2026-05-30T08:00:00Z',
        preview: '规则\n默认中文回复',
      }],
    })));
    const detailRequest = deferred();
    backend.getMemoryEntry.mockReturnValueOnce(detailRequest.promise);

    const { rerenderProject } = renderMemoryPage('/repo/one');

    const title = await screen.findByText('默认中文');
    const card = title.closest('article');
    fireEvent.click(within(card).getByRole('button', { name: '编辑' }));

    expect(backend.getMemoryEntry).toHaveBeenCalledWith({ cwd: '/repo/one', target: 'private', path: 'feedback/reply-language.md' });
    rerenderProject('/repo/two');

    await act(async () => {
      detailRequest.resolve({
        target: 'private',
        path: 'feedback/reply-language.md',
        name: 'reply-language',
        description: '回复时使用中文',
        type: 'feedback',
        content: '规则\n默认中文回复',
      });
      await flushPromises();
    });

    expect(screen.queryByRole('dialog', { name: '编辑记忆' })).not.toBeInTheDocument();
    expect(upsertMemoryEntry).not.toHaveBeenCalled();
  });

  it('ignores stale edit detail responses for older entries in the same project', async () => {
    fetchMemoryDashboard.mockResolvedValue(normalizeMemorySnapshot(memorySnapshot({
      privateEntries: [
        {
          name: 'first-rule',
          title: '第一条',
          description: '第一条描述',
          target: 'private',
          type: 'feedback',
          path: 'feedback/first.md',
          updatedAt: '2026-05-30T08:00:00Z',
          preview: '第一条预览',
        },
        {
          name: 'second-rule',
          title: '第二条',
          description: '第二条描述',
          target: 'private',
          type: 'feedback',
          path: 'feedback/second.md',
          updatedAt: '2026-05-31T08:00:00Z',
          preview: '第二条预览',
        },
      ],
    })));
    const firstDetailRequest = deferred();
    backend.getMemoryEntry
      .mockReturnValueOnce(firstDetailRequest.promise)
      .mockResolvedValueOnce({
        target: 'private',
        path: 'feedback/second.md',
        name: 'second-rule',
        description: '第二条描述',
        title: '第二条',
        type: 'feedback',
        content: '第二条内容',
      });

    renderMemoryPage('/repo/app');

    const firstCard = (await screen.findByText('第一条')).closest('article');
    const secondCard = screen.getByText('第二条').closest('article');
    fireEvent.click(within(firstCard).getByRole('button', { name: '编辑' }));
    fireEvent.click(within(secondCard).getByRole('button', { name: '编辑' }));

    const editor = await screen.findByRole('dialog', { name: '编辑记忆' });
    expect(within(editor).getByLabelText('内容')).toHaveValue('第二条内容');

    await act(async () => {
      firstDetailRequest.resolve({
        target: 'private',
        path: 'feedback/first.md',
        name: 'first-rule',
        description: '第一条描述',
        title: '第一条',
        type: 'feedback',
        content: '第一条内容',
      });
      await flushPromises();
    });

    expect(within(editor).getByLabelText('内容')).toHaveValue('第二条内容');
    expect(upsertMemoryEntry).not.toHaveBeenCalled();
  });

  it('closes stale delete confirmation after switching projects', async () => {
    fetchMemoryDashboard.mockResolvedValue(normalizeMemorySnapshot(memorySnapshot({
      privateEntries: [{
        name: 'reply-language',
        title: '默认中文',
        description: '回复时使用中文',
        target: 'private',
        type: 'feedback',
        path: 'feedback/reply-language.md',
        updatedAt: '2026-05-30T08:00:00Z',
        preview: '规则\n默认中文回复',
      }],
    })));
    backend.deleteMemoryEntry.mockResolvedValue({ deleted: true });

    const { rerenderProject } = renderMemoryPage('/repo/one');

    const title = await screen.findByText('默认中文');
    const card = title.closest('article');
    fireEvent.click(within(card).getByRole('button', { name: '删除' }));
    expect(screen.getByRole('dialog', { name: '删除记忆' })).toBeInTheDocument();

    rerenderProject('/repo/two');

    await waitFor(() => {
      expect(screen.queryByRole('dialog', { name: '删除记忆' })).not.toBeInTheDocument();
    });
    expect(backend.deleteMemoryEntry).not.toHaveBeenCalled();
  });

	it('passes cwd when toggling auto-dream intent', async () => {
		backend.setMemoryAutoDreamIntent.mockResolvedValue({ ok: true, enabled: true });
		renderMemoryPage('/repo/app');

		fireEvent.click(await screen.findByRole('button', { name: '开启' }));

		await waitFor(() => {
			expect(backend.setMemoryAutoDreamIntent).toHaveBeenCalledWith({ cwd: '/repo/app', enabled: true });
		});
	});
});

describe('MemoryPage consolidation polling', () => {
	it('shows degraded similarity status and blocks every similarity mutation', async () => {
		fetchMemoryDashboard.mockResolvedValue(normalizeMemorySnapshot(memorySnapshot({
			similarityDegraded: true,
			similarGroups: [{
				targetA: 'team', pathA: 'project/a.md', nameA: 'A',
				targetB: 'team', pathB: 'project/b.md', nameB: 'B', score: 0.91,
			}],
		})));

		renderMemoryPage();

		const warning = await screen.findByRole('status');
		expect(warning).toHaveTextContent('相似记忆状态暂不可用');
		expect(within(warning).getByRole('button', { name: '一键整合全部' })).toBeDisabled();
		fireEvent.click(within(warning).getByRole('button', { name: '展开' }));
		expect(screen.getByRole('button', { name: '整合' })).toBeDisabled();
		expect(screen.getByRole('button', { name: '忽略' })).toBeDisabled();
		expect(backend.startConsolidateMemorySimilarities).not.toHaveBeenCalled();
		expect(backend.mergeMemoryEntries).not.toHaveBeenCalled();
		expect(backend.ignoreMemorySimilarity).not.toHaveBeenCalled();
	});

	it('keeps similarity actions enabled when the degraded field is absent', async () => {
		fetchMemoryDashboard.mockResolvedValue(normalizeMemorySnapshot(memorySnapshot({
			similarGroups: [{
				targetA: 'team', pathA: 'project/a.md', nameA: 'A',
				targetB: 'team', pathB: 'project/b.md', nameB: 'B', score: 0.91,
			}],
		})));

		renderMemoryPage();

		expect(await screen.findByRole('button', { name: '一键整合全部' })).toBeEnabled();
		expect(screen.queryByRole('status')).not.toBeInTheDocument();
	});

	it('keeps similarity actions enabled when degraded is explicitly false', async () => {
		fetchMemoryDashboard.mockResolvedValue(normalizeMemorySnapshot(memorySnapshot({
			similarityDegraded: false,
			similarGroups: [{
				targetA: 'team', pathA: 'project/a.md', nameA: 'A',
				targetB: 'team', pathB: 'project/b.md', nameB: 'B', score: 0.91,
			}],
		})));

		renderMemoryPage();

		expect(await screen.findByRole('button', { name: '一键整合全部' })).toBeEnabled();
		expect(screen.queryByRole('status')).not.toBeInTheDocument();
	});

	it('fails fast when degraded has a non-boolean value', async () => {
		fetchMemoryDashboard.mockResolvedValue(normalizeMemorySnapshot(memorySnapshot({
			similarityDegraded: 'true',
			similarGroups: [{
				targetA: 'team', pathA: 'project/a.md', nameA: 'A',
				targetB: 'team', pathB: 'project/b.md', nameB: 'B', score: 0.91,
			}],
		})));

		renderMemoryPage();

		expect(await screen.findByText(/memory health similarityDegraded must be a boolean/)).toBeInTheDocument();
		expect(screen.queryByRole('button', { name: '一键整合全部' })).not.toBeInTheDocument();
		expect(backend.startConsolidateMemorySimilarities).not.toHaveBeenCalled();
	});

  function mockDashboardWithSimilarGroup() {
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
  }

  async function startMergeAllPolling(mergeAll) {
    fireEvent.click(mergeAll);
    await act(async () => {
      await flushPromises();
    });
  }

  it('aborts the background consolidation poller when the page unmounts', async () => {
    mockDashboardWithSimilarGroup();
    backend.startConsolidateMemorySimilarities.mockResolvedValue({ status: 'running', jobId: 'job-1' });
    backend.getMemoryConsolidationStatus.mockResolvedValue({ status: 'running' });

    const { unmount } = renderMemoryPage();
    const mergeAll = await screen.findByRole('button', { name: '一键整合全部' });

    vi.useFakeTimers();
    await startMergeAllPolling(mergeAll);
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

  it('reports an explicit error when the consolidation job exceeds max polling attempts', async () => {
    mockDashboardWithSimilarGroup();
    backend.startConsolidateMemorySimilarities.mockResolvedValue({ status: 'running', jobId: 'job-1' });
    backend.getMemoryConsolidationStatus.mockResolvedValue({ status: 'running' });

    renderMemoryPage();
    const mergeAll = await screen.findByRole('button', { name: '一键整合全部' });

    vi.useFakeTimers();
    await startMergeAllPolling(mergeAll);
    await act(async () => {
      for (let attempt = 0; attempt < 181; attempt += 1) {
        vi.advanceTimersByTime(2000);
        await flushPromises(2);
      }
    });

    expect(screen.getByText('智能整合仍在进行，请稍后查看结果')).toBeInTheDocument();
  }, 10_000);

  it('treats a succeeded consolidation status without result as an error', async () => {
    mockDashboardWithSimilarGroup();
    backend.startConsolidateMemorySimilarities.mockResolvedValue({ status: 'running', jobId: 'job-1' });
    backend.getMemoryConsolidationStatus.mockResolvedValue({ status: 'succeeded' });

    renderMemoryPage();
    const mergeAll = await screen.findByRole('button', { name: '一键整合全部' });

    await startMergeAllPolling(mergeAll);

    expect(await screen.findByText('智能整合失败：智能整合完成但没有返回结果')).toBeInTheDocument();
  });

  it('invalidates the memory dashboard when consolidation succeeds with a result', async () => {
    mockDashboardWithSimilarGroup();
    backend.startConsolidateMemorySimilarities.mockResolvedValue({ status: 'running', jobId: 'job-1' });
    backend.getMemoryConsolidationStatus.mockResolvedValue({ status: 'succeeded', result: { merged: 1, ignored: 0, failed: 0, skipped: 0 } });

    renderMemoryPage();
    const mergeAll = await screen.findByRole('button', { name: '一键整合全部' });

    await startMergeAllPolling(mergeAll);

    await waitFor(() => {
      expect(fetchMemoryDashboard).toHaveBeenCalledTimes(2);
    });
    expect(await screen.findByText('已整合 1 组')).toBeInTheDocument();
  });
});
