import { installAppTestHooks, testEnv } from "./test-utils/appTestHarness.jsx";

installAppTestHooks();
const {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
  expect,
  it,
  vi,
  frontendHealthSnapshot,
  normalizeMemorySnapshotForFacade,
  App,
  backend,
  waitForBackendThreadHeading,
  createSimilaritySnapshots,
  openMemoryCenterWithSimilarity,
  runConsolidationUntilSimilaritiesClear,
  expectSimilarityWarningCleared,
} = testEnv;

it('renders memory create button inside search toolbar', async () => {
  render(<App />);
  await waitForBackendThreadHeading();
  fireEvent.click(screen.getByRole('button', { name: '记忆中心' }));
  const toolbar = await screen.findByTestId('memory-toolbar');
  expect(toolbar).toBeInTheDocument();
  expect(within(toolbar).getByRole('textbox', { name: '搜索记忆' })).toBeInTheDocument();
  expect(within(toolbar).getByRole('button', { name: /\+ 新建/ })).toBeInTheDocument();
});

it('loads memory center through ui/memory/get and groups entries by type', async () => {
  backend.getMemorySnapshot.mockResolvedValue(normalizeMemorySnapshotForFacade({
    overview: {
      enabled: true,
      autoDreamEnabled: false,
      autoDreamIntent: null,
      projectRoot: '/repo/app',
      health: {
        preferenceCount: 1,
        projectCount: 1,
        maxPerCategory: 15,
        similarGroups: [{
          nameA: '遵守 TDD', targetA: 'private', pathA: 'feedback/tdd.md',
          nameB: 'TDD 流程', targetB: 'team', pathB: 'feedback/team-tdd.md',
          score: 0.91,
        }],
      },
    },
    private: {
      entries: [{
        name: 'tdd-rule',
        title: '遵守 TDD',
        description: '先写红测并运行确认。',
        type: 'feedback',
        path: 'feedback/tdd.md',
        updatedAt: '2026-05-30T08:00:00Z',
        preview: '规则\n先写红测',
      }],
    },
    team: {
      entries: [{
        name: 'dag-policy',
        title: 'DAG 规范',
        description: '任务流程要使用 DAG 生命周期。',
        type: 'project',
        path: 'project/dag.md',
        updatedAt: '2026-05-29T08:00:00Z',
        preview: 'DAG 内容',
      }],
    },
  }));

  render(<App />);
  await waitForBackendThreadHeading();
  fireEvent.click(screen.getByLabelText('记忆中心'));

  expect(await screen.findByText('遵守 TDD')).toBeInTheDocument();
  const memoryCard = screen.getByText('遵守 TDD').closest('article');
  expect(within(memoryCard).getByText('偏好')).toBeInTheDocument();
  expect(within(memoryCard).queryByText('私有')).not.toBeInTheDocument();
  expect(within(memoryCard).queryByText('团队')).not.toBeInTheDocument();
  expect(within(memoryCard).queryByText('feedback/tdd.md')).not.toBeInTheDocument();
  expect(screen.getByRole('tab', { name: '偏好 1' })).toBeInTheDocument();
  expect(screen.getByRole('tab', { name: '项目 1' })).toBeInTheDocument();
  expect(screen.getByRole('tab', { name: '全部 2' })).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: /刷新/ })).not.toBeInTheDocument();
  expect(screen.getByText('1 组条目内容相似')).toBeInTheDocument();
  expect(backend.getMemorySnapshot).toHaveBeenCalledWith({ cwd: '/repo/app' });

  fireEvent.click(screen.getByRole('tab', { name: '项目 1' }));
  expect(screen.queryByText('遵守 TDD')).not.toBeInTheDocument();
  expect(screen.getByText('DAG 规范')).toBeInTheDocument();

  fireEvent.change(screen.getByLabelText('搜索记忆'), { target: { value: 'tdd' } });
  expect(screen.queryByText('DAG 规范')).not.toBeInTheDocument();
  expect(screen.getByText('没有匹配的条目')).toBeInTheDocument();
});

it('auto-updates memory center without a manual refresh button', async () => {
  let entries = [{
    name: 'tdd-rule',
    title: '遵守 TDD',
    description: '先写红测',
    type: 'feedback',
    path: 'feedback/tdd.md',
    updatedAt: '2026-05-30T08:00:00Z',
    preview: '规则\n先写红测',
  }];
  backend.getMemorySnapshot.mockImplementation(() => Promise.resolve(normalizeMemorySnapshotForFacade({
    overview: {
      enabled: true,
      autoDreamEnabled: true,
      autoDreamIntent: null,
      projectRoot: '/repo/app',
      health: { preferenceCount: entries.length, projectCount: 0, maxPerCategory: 15, similarGroups: [] },
    },
    private: { entries },
    team: { entries: [] },
  })));

  render(<App />);
  await waitForBackendThreadHeading();
  fireEvent.click(screen.getByLabelText('记忆中心'));

  expect(await screen.findByText('遵守 TDD')).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: /刷新/ })).not.toBeInTheDocument();

  entries = [
    ...entries,
    {
      name: 'reply-language',
      title: '默认中文',
      description: '回答时使用中文',
      type: 'feedback',
      path: 'feedback/reply-language.md',
      updatedAt: '2026-05-30T09:00:00Z',
      preview: '默认中文回复',
    },
  ];
  await act(async () => {
    backend.__bridgeCallback?.({ type: 'ui/memory/changed', payload: { action: 'upsert' } });
  });
  expect(await screen.findByText('默认中文')).toBeInTheDocument();
  expect(screen.getByRole('tab', { name: '偏好 2' })).toBeInTheDocument();

  entries = [
    ...entries,
    {
      name: 'review-style',
      title: '审查风格',
      description: '先列风险',
      type: 'feedback',
      path: 'feedback/review-style.md',
      updatedAt: '2026-05-30T09:01:00Z',
      preview: '先列风险',
    },
  ];
  await act(async () => {
    window.dispatchEvent(new Event('focus'));
  });
  expect(await screen.findByText('审查风格')).toBeInTheDocument();
  expect(screen.getByRole('tab', { name: '偏好 3' })).toBeInTheDocument();
});

it('does not poll memory center with a page interval', async () => {
  const intervalSpy = vi.spyOn(window, 'setInterval');
  try {
    backend.getMemorySnapshot.mockResolvedValue(normalizeMemorySnapshotForFacade({
      overview: {
        enabled: true,
        autoDreamEnabled: true,
        autoDreamIntent: null,
        projectRoot: '/repo/app',
        health: { preferenceCount: 1, projectCount: 0, maxPerCategory: 15, similarGroups: [] },
      },
      private: {
        entries: [{
          name: 'tdd-rule',
          title: '遵守 TDD',
          description: '先写红测',
          type: 'feedback',
          path: 'feedback/tdd.md',
          updatedAt: '2026-05-30T08:00:00Z',
          preview: '规则\n先写红测',
        }],
      },
      team: { entries: [] },
    }));

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('记忆中心'));

    expect(await screen.findByText('遵守 TDD')).toBeInTheDocument();
    expect(intervalSpy.mock.calls.filter((call) => call[1] === 4000)).toHaveLength(0);
  }
  finally {
    intervalSpy.mockRestore();
  }
});

it('keeps cached memory entries visible and exposes retry when a background sync fails', async () => {
  let entries = [{
    name: 'tdd-rule',
    title: '遵守 TDD',
    description: '先写红测',
    type: 'feedback',
    path: 'feedback/tdd.md',
    updatedAt: '2026-05-30T08:00:00Z',
    preview: '规则\n先写红测',
  }];
  backend.getMemorySnapshot.mockImplementation(() => Promise.resolve(normalizeMemorySnapshotForFacade({
    overview: {
      enabled: true,
      autoDreamEnabled: true,
      autoDreamIntent: null,
      projectRoot: '/repo/app',
      health: { preferenceCount: entries.length, projectCount: 0, maxPerCategory: 15, similarGroups: [] },
    },
    private: { entries },
    team: { entries: [] },
  })));

  render(<App />);
  await waitForBackendThreadHeading();
  fireEvent.click(screen.getByLabelText('记忆中心'));
  expect(await screen.findByText('遵守 TDD')).toBeInTheDocument();

  backend.getMemorySnapshot.mockRejectedValueOnce(new Error('memory backend offline'));
  await act(async () => {
    backend.__bridgeCallback?.({ type: 'ui/memory/changed', payload: { action: 'upsert' } });
    await Promise.resolve();
  });

  expect(screen.getByText('遵守 TDD')).toBeInTheDocument();
  const alert = await screen.findByRole('alert');
  expect(alert).toHaveTextContent('同步记忆失败，当前显示上次成功数据。');
  expect(alert).not.toHaveTextContent('memory backend offline');
  expect(frontendHealthSnapshot()).toEqual(expect.arrayContaining([
    expect.objectContaining({ actionId: 'memory.dashboard.load', diagnosticId: expect.any(String) }),
  ]));

  entries = [{
    name: 'review-style',
    title: '审查风格',
    description: '先列风险',
    type: 'feedback',
    path: 'feedback/review-style.md',
    updatedAt: '2026-05-30T09:01:00Z',
    preview: '先列风险',
  }];
  fireEvent.click(screen.getByRole('button', { name: '重试同步' }));

  expect(await screen.findByText('审查风格')).toBeInTheDocument();
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
});

it('shows a retryable blocking error instead of an empty memory state on initial load failure', async () => {
  let failMemory = true;
  backend.getMemorySnapshot.mockImplementation(() => {
    if (failMemory) return Promise.reject(new Error('memory backend offline'));
    return Promise.resolve(normalizeMemorySnapshotForFacade({
      overview: {
        enabled: true,
        autoDreamEnabled: true,
        autoDreamIntent: null,
        projectRoot: '/repo/app',
        health: { preferenceCount: 1, projectCount: 0, maxPerCategory: 15, similarGroups: [] },
      },
      private: {
        entries: [{
          name: 'review-style',
          title: '审查风格',
          description: '先列风险',
          type: 'feedback',
          path: 'feedback/review-style.md',
          updatedAt: '2026-05-30T09:01:00Z',
          preview: '先列风险',
        }],
      },
      team: { entries: [] },
    }));
  });

  render(<App />);
  await waitForBackendThreadHeading();
  fireEvent.click(screen.getByLabelText('记忆中心'));

  const alert = await screen.findByRole('alert');
  expect(alert).toHaveTextContent('读取记忆失败，请重试。');
  expect(alert).not.toHaveTextContent('memory backend offline');
  expect(frontendHealthSnapshot()).toEqual(expect.arrayContaining([
    expect.objectContaining({ actionId: 'memory.dashboard.load', diagnosticId: expect.any(String) }),
  ]));
  expect(screen.queryByText('暂无记忆')).not.toBeInTheDocument();

  failMemory = false;
  fireEvent.click(within(alert).getByRole('button', { name: '重试同步' }));

  expect(await screen.findByText('审查风格')).toBeInTheDocument();
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
});

it('keeps cached memory entries visible when navigating back and refreshes silently', async () => {
  let entries = [{
    name: 'tdd-rule',
    title: '遵守 TDD',
    description: '先写红测',
    type: 'feedback',
    path: 'feedback/tdd.md',
    updatedAt: '2026-05-30T08:00:00Z',
    preview: '规则\n先写红测',
  }];
  backend.getMemorySnapshot.mockImplementation(() => Promise.resolve(normalizeMemorySnapshotForFacade({
    overview: {
      enabled: true,
      autoDreamEnabled: true,
      autoDreamIntent: null,
      projectRoot: '/repo/app',
      health: { preferenceCount: entries.length, projectCount: 0, maxPerCategory: 15, similarGroups: [] },
    },
    private: { entries },
    team: { entries: [] },
  })));

  render(<App />);
  await waitForBackendThreadHeading();
  fireEvent.click(screen.getByLabelText('记忆中心'));
  expect(await screen.findByText('遵守 TDD')).toBeInTheDocument();

  fireEvent.click(screen.getByLabelText('新对话'));
  entries = [{
    name: 'reply-language',
    title: '默认中文',
    description: '回答时使用中文',
    type: 'feedback',
    path: 'feedback/reply-language.md',
    updatedAt: '2026-05-30T09:00:00Z',
    preview: '默认中文回复',
  }];
  fireEvent.click(screen.getByLabelText('记忆中心'));

  expect(screen.queryByText('正在加载记忆中心...')).not.toBeInTheDocument();
  expect(screen.getByText('遵守 TDD')).toBeInTheDocument();
  expect(await screen.findByText('默认中文')).toBeInTheDocument();
  expect(screen.queryByText('遵守 TDD')).not.toBeInTheDocument();
});

it('wires memory center mutation actions to backend RPCs', async () => {
  backend.getMemorySnapshot.mockResolvedValue(normalizeMemorySnapshotForFacade({
    overview: {
      enabled: true,
      autoDreamEnabled: false,
      autoDreamIntent: null,
      projectRoot: '/repo/app',
      health: { preferenceCount: 1, projectCount: 0, maxPerCategory: 15, similarGroups: [] },
    },
    private: {
      entries: [{
        name: 'tdd-rule',
        title: '遵守 TDD',
        description: '先写红测',
        type: 'feedback',
        path: 'feedback/tdd.md',
        updatedAt: '2026-05-30T08:00:00Z',
        preview: '规则\n先写红测',
      }],
    },
    team: { entries: [] },
  }));

  render(<App />);
  await waitForBackendThreadHeading();
  fireEvent.click(screen.getByLabelText('记忆中心'));
  expect(await screen.findByText('遵守 TDD')).toBeInTheDocument();

  fireEvent.click(screen.getByRole('button', { name: '开启' }));
  await waitFor(() => {
    expect(backend.setMemoryAutoDreamIntent).toHaveBeenCalledWith({ cwd: '/repo/app', enabled: true });
  });

  fireEvent.click(screen.getByRole('button', { name: '+ 新建 ▾' }));
  fireEvent.click(screen.getByRole('menuitem', { name: '新建偏好' }));
  const createEditor = await screen.findByRole('dialog', { name: '新建记忆' });
  expect(within(createEditor).getByLabelText('分类')).toHaveValue('feedback');
  expect(within(createEditor).queryByLabelText('目标')).not.toBeInTheDocument();
  expect(within(createEditor).queryByLabelText('标识名')).not.toBeInTheDocument();
  fireEvent.change(screen.getByLabelText('描述'), { target: { value: '回复时使用中文' } });
  fireEvent.change(screen.getByLabelText('内容'), { target: { value: '规则\n默认中文回复' } });
  fireEvent.click(screen.getByRole('button', { name: '保存' }));
  await waitFor(() => {
    expect(backend.upsertMemoryEntry).toHaveBeenCalledWith(expect.objectContaining({
      cwd: '/repo/app',
      target: 'private',
      name: expect.stringMatching(/^feedback-/),
      description: '回复时使用中文',
      type: 'feedback',
      content: '规则\n默认中文回复',
    }));
  });

  const card = screen.getByText('遵守 TDD').closest('article');
  fireEvent.click(within(card).getByRole('button', { name: '编辑' }));
  await waitFor(() => {
    expect(backend.getMemoryEntry).toHaveBeenCalledWith({ cwd: '/repo/app', target: 'private', path: 'feedback/tdd.md' });
  });
  const editor = await screen.findByRole('dialog', { name: '编辑记忆' });
  expect(within(editor).queryByRole('button', { name: '关闭' })).not.toBeInTheDocument();
  expect(within(editor).getByLabelText('分类')).toHaveValue('feedback');
  expect(within(editor).queryByLabelText('目标')).not.toBeInTheDocument();
  expect(within(editor).queryByLabelText('标识名')).not.toBeInTheDocument();
  expect(await screen.findByDisplayValue('先写红测')).toBeInTheDocument();
  fireEvent.click(within(editor).getByRole('button', { name: '取消' }));

  fireEvent.click(within(card).getByRole('button', { name: '删除' }));
  const deleteDialog = await screen.findByRole('dialog', { name: '删除记忆' });
  expect(deleteDialog).toBeInTheDocument();
  expect(within(deleteDialog).queryByText('private')).not.toBeInTheDocument();
  expect(within(deleteDialog).queryByText('feedback/tdd.md')).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: '确认删除' }));
  await waitFor(() => {
    expect(backend.deleteMemoryEntry).toHaveBeenCalledWith({ cwd: '/repo/app', target: 'private', path: 'feedback/tdd.md' });
  });
});

it('wires memory similarity actions to backend RPCs', async () => {
  backend.getMemorySnapshot.mockResolvedValue(normalizeMemorySnapshotForFacade({
    overview: {
      enabled: true,
      autoDreamEnabled: true,
      projectRoot: '/repo/app',
      health: {
        preferenceCount: 2,
        projectCount: 0,
        maxPerCategory: 15,
        similarGroups: [{
          nameA: 'A', targetA: 'private', pathA: 'feedback/a.md',
          nameB: 'B', targetB: 'team', pathB: 'feedback/b.md',
          score: 0.88,
        }],
      },
    },
    private: { entries: [] },
    team: { entries: [] },
  }));

  render(<App />);
  await waitForBackendThreadHeading();
  fireEvent.click(screen.getByLabelText('记忆中心'));
  expect(await screen.findByText('1 组条目内容相似')).toBeInTheDocument();

  fireEvent.click(screen.getByRole('button', { name: '展开' }));
  fireEvent.click(screen.getByRole('button', { name: '整合' }));
  const mergeDialog = await screen.findByRole('dialog', { name: '整合相似记忆' });
  expect(mergeDialog).toBeInTheDocument();
  expect(within(mergeDialog).queryByText('private')).not.toBeInTheDocument();
  expect(within(mergeDialog).queryByText('team')).not.toBeInTheDocument();
  expect(within(mergeDialog).queryByText('feedback/a.md')).not.toBeInTheDocument();
  expect(within(mergeDialog).queryByText('feedback/b.md')).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: '确认整合' }));
  await waitFor(() => {
    expect(backend.mergeMemoryEntries).toHaveBeenCalledWith({
      cwd: '/repo/app', targetA: 'private', pathA: 'feedback/a.md', targetB: 'team', pathB: 'feedback/b.md',
    });
  });
  await waitFor(() => {
    expect(screen.queryByRole('dialog', { name: '整合相似记忆' })).not.toBeInTheDocument();
  });

  await waitFor(() => {
    expect(screen.getByRole('button', { name: '一键整合全部' })).not.toBeDisabled();
  });
  backend.getPreference.mockImplementation(({ key }) => Promise.resolve({
    'settings.provider.active': 'codex',
    'settings.provider.codex.model': 'gpt-5.4',
    'settings.provider.codex.effort': 'xhigh',
    'settings.provider.codex.codexHome': '~/.codex',
    'settings.provider.codex.codexInstanceKey': 'default',
    'settings.provider.codex.codexModelProvider': 'openai',
  }[key] ?? null));
  fireEvent.click(screen.getByRole('button', { name: '一键整合全部' }));
  await waitFor(() => {
    expect(backend.startConsolidateMemorySimilarities).toHaveBeenCalledWith(expect.objectContaining({
      cwd: '/repo/app',
      provider: 'codex',
      model: 'gpt-5.4',
      codexModelProvider: 'openai',
    }));
  });
  await waitFor(() => {
    expect(backend.getMemoryConsolidationStatus).toHaveBeenCalledWith({ cwd: '/repo/app', jobId: 'memory-job-1' });
  });
  await waitFor(() => {
    expect(screen.getByRole('button', { name: '忽略' })).not.toBeDisabled();
  });

  fireEvent.click(screen.getByRole('button', { name: '忽略' }));
  await waitFor(() => {
    expect(backend.ignoreMemorySimilarity).toHaveBeenCalledWith({
      cwd: '/repo/app', targetA: 'private', pathA: 'feedback/a.md', targetB: 'team', pathB: 'feedback/b.md',
    });
  });
});

it('simulates one-click memory consolidation and clears similarity warnings after refresh', async () => {
  const { snapshotWithSimilar, snapshotWithoutSimilar } = createSimilaritySnapshots();
  let hasSimilar = true;
  backend.getMemorySnapshot.mockImplementation(() => Promise.resolve(hasSimilar ? snapshotWithSimilar : snapshotWithoutSimilar));
  backend.startConsolidateMemorySimilarities.mockResolvedValue({ jobId: 'memory-job-live', status: 'running' });
  backend.getMemoryConsolidationStatus
    .mockResolvedValueOnce({ jobId: 'memory-job-live', status: 'running' })
    .mockResolvedValueOnce({
      jobId: 'memory-job-live',
      status: 'succeeded',
      result: { merged: 1, ignored: 0, failed: 0, skipped: 0 },
    });

  await openMemoryCenterWithSimilarity();
  await runConsolidationUntilSimilaritiesClear(() => {
    hasSimilar = false;
  });

  await waitFor(() => {
    expectSimilarityWarningCleared();
  });
  expect(backend.getMemorySnapshot).toHaveBeenLastCalledWith({ cwd: '/repo/app' });
});
