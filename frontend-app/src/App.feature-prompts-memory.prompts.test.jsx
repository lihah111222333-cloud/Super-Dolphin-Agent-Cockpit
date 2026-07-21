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
  App,
  backend,
  mockPromptPreferences,
  waitForBackendThreadHeading,
  canonicalPromptRPCItem,
  mockPromptWizardEntryPrompt,
} = testEnv;

it('loads prompt assets while wiring active launch prompt preference', async () => {
  backend.listPromptAssets.mockResolvedValue({
    prompts: [
      {
        id: 'main/reviewer',
        name: '代码审查专家',
        content: '先检查阻塞问题',
        description: '审查代码质量',
        when_to_use: 'Use for code review.',
        agentType: 'coder',
        createdAt: '2026-07-11T00:00:00Z',
        updatedAt: '2026-07-11T00:00:00Z',
        tags: ['intent:expert', 'review'],
        scope: 'project',
        enabled: true,
      },
      {
        id: 'main/knowledge/sqlc',
        name: 'SQLC 资料',
        content: '',
        description: 'SQLC migration 资料',
        agentType: 'main',
        when_to_use: '',
        createdAt: '2026-07-11T00:00:00Z',
        updatedAt: '2026-07-11T00:00:00Z',
        tags: ['intent:recall', 'scope.global', 'sqlc'],
        scope: 'global',
        enabled: true,
      },
      {
        id: 'intent/recall/ready',
        draft_key: 'intent/recall/ready',
        name: '价格表资料',
        content: '价格资料内容',
        description: '从 Excel 价格表整理出的资料',
        agentType: 'main',
        when_to_use: '',
        createdAt: '2026-07-11T00:00:00Z',
        updatedAt: '2026-07-11T00:00:00Z',
        tags: ['intent:recall', 'pricing'],
        scope: 'project',
        enabled: false,
        state: 'pending_confirm',
        draft_status: 'ready_to_save',
      },
    ],
  });
  mockPromptPreferences('main/reviewer');

  render(<App />);
  await waitForBackendThreadHeading();
  fireEvent.click(screen.getByLabelText('提示词'));

  expect(await screen.findByText('代码审查专家')).toBeInTheDocument();
  expect(screen.getByText('SQLC 资料')).toBeInTheDocument();
  expect(screen.getByText('价格表资料')).toBeInTheDocument();
  expect(screen.queryByRole('tablist', { name: '提示词分类' })).not.toBeInTheDocument();
  expect(screen.queryByRole('button', { name: /全部范围/ })).not.toBeInTheDocument();
  expect(screen.queryByRole('button', { name: /全部状态/ })).not.toBeInTheDocument();
  expect(screen.queryByRole('button', { name: '添加给 AI 的内容' })).not.toBeInTheDocument();
  expect(screen.getByText('强制使用')).toBeInTheDocument();
  expect(screen.getAllByText('全局可用').length).toBeGreaterThanOrEqual(1);
  expect(screen.queryByRole('button', { name: /刷新/ })).not.toBeInTheDocument();
  expect(backend.listPromptAssets).toHaveBeenCalledWith({ cwd: '/repo/app' });
  expect(backend.getPreference).toHaveBeenCalledWith({ cwd: '/repo/app', key: 'settings.activePromptKey' });

  const reviewerCard = screen.getByText('代码审查专家').closest('article');
  fireEvent.click(within(reviewerCard).getByRole('button', { name: '取消强制' }));
  await waitFor(() => {
    expect(backend.setPreference).toHaveBeenCalledWith({
      cwd: '/repo/app',
      key: 'settings.activePromptKey',
      value: '',
    });
  });
});

it('traps focus in the prompt editor and restores focus after Escape', async () => {
  backend.listPromptAssets.mockResolvedValue({
    prompts: [{
      ...canonicalPromptRPCItem(),
      id: 'main/reviewer',
      name: '代码审查专家',
      content: '先检查阻塞问题',
      description: '审查代码质量',
      when_to_use: 'Use for code review.',
      agentType: 'coder',
      tags: ['intent:expert', 'review'],
      scope: 'project',
      enabled: true,
    }],
  });
  mockPromptPreferences();

  render(<App />);
  await waitForBackendThreadHeading();
  fireEvent.click(screen.getByLabelText('提示词'));

  const card = (await screen.findByText('代码审查专家')).closest('article');
  const editButton = within(card).getByRole('button', { name: '编辑' });
  editButton.focus();
  fireEvent.click(editButton);

  const editor = await screen.findByRole('dialog', { name: '编辑提示词' });
  expect(within(editor).queryByLabelText('关闭编辑器')).not.toBeInTheDocument();
  const firstScopeButton = within(editor).getByRole('radio', { name: '这个项目' });
  await waitFor(() => {
    expect(document.activeElement).toBe(firstScopeButton);
  });

  fireEvent.keyDown(editor, { key: 'Escape', code: 'Escape' });
  await waitFor(() => {
    expect(screen.queryByRole('dialog', { name: '编辑提示词' })).not.toBeInTheDocument();
  });
  expect(document.activeElement).toBe(editButton);
});

it('traps focus in the prompt wizard and restores focus after Escape', async () => {
  mockPromptWizardEntryPrompt();
  mockPromptPreferences();

  render(<App />);
  await waitForBackendThreadHeading();
  fireEvent.click(screen.getByLabelText('提示词'));

  const continueButton = await screen.findByRole('button', { name: '继续确认' });
  continueButton.focus();
  fireEvent.click(continueButton);

  const wizard = await screen.findByRole('dialog', { name: '添加给 AI 的内容' });
  const firstKindTab = within(wizard).getByRole('tab', { name: '专家能力' });
  const saveButton = within(wizard).getByRole('button', { name: '确认保存' });
  await waitFor(() => {
    expect(document.activeElement).toBe(firstKindTab);
  });

  fireEvent.keyDown(wizard, { key: 'Tab', code: 'Tab', shiftKey: true });
  expect(document.activeElement).toBe(saveButton);
  fireEvent.keyDown(wizard, { key: 'Tab', code: 'Tab' });
  expect(document.activeElement).toBe(firstKindTab);
  fireEvent.keyDown(wizard, { key: 'Escape', code: 'Escape' });
  await waitFor(() => {
    expect(screen.queryByRole('dialog', { name: '添加给 AI 的内容' })).not.toBeInTheDocument();
  });
  expect(document.activeElement).toBe(continueButton);
});

it('auto-updates prompt assets without a manual refresh button', async () => {
  let prompts = [{
    ...canonicalPromptRPCItem(),
    id: 'main/reviewer',
    name: '代码审查专家',
    content: '先检查阻塞问题',
    description: '审查代码质量',
    tags: ['intent:expert', 'review'],
    scope: 'project',
    enabled: true,
  }];
  backend.listPromptAssets.mockImplementation(() => Promise.resolve({ prompts }));
  mockPromptPreferences();

  render(<App />);
  await waitForBackendThreadHeading();
  fireEvent.click(screen.getByLabelText('提示词'));

  expect(await screen.findByText('代码审查专家')).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: /刷新/ })).not.toBeInTheDocument();

  prompts = [{
    ...canonicalPromptRPCItem(),
    id: 'main/deploy',
    name: '部署助手',
    content: '先检查环境',
    description: '部署前检查',
    tags: ['intent:expert', 'deploy'],
    scope: 'project',
    enabled: true,
  }];
  await act(async () => {
    backend.__bridgeCallback?.({ type: 'prompts/changed', payload: { cwd: '/repo/app' } });
  });

  expect(await screen.findByText('部署助手')).toBeInTheDocument();
  expect(screen.queryByText('代码审查专家')).not.toBeInTheDocument();

  prompts = [{
    ...canonicalPromptRPCItem(),
    id: 'main/release-note',
    name: '发布说明',
    content: '整理发布变更',
    description: '发布前整理说明',
    tags: ['intent:expert', 'release'],
    scope: 'project',
    enabled: true,
  }];
  await act(async () => {
    window.dispatchEvent(new Event('focus'));
  });

  expect(await screen.findByText('发布说明')).toBeInTheDocument();
  expect(screen.queryByText('部署助手')).not.toBeInTheDocument();
});

it('does not poll prompt assets with a page interval', async () => {
  const intervalSpy = vi.spyOn(window, 'setInterval');
  try {
    backend.listPromptAssets.mockResolvedValue({
      prompts: [{
        ...canonicalPromptRPCItem(),
        id: 'main/code-review',
        name: '代码审查助手',
        description: '检查改动风险',
        content: '先列风险',
        tags: ['intent:expert'],
        scope: 'project',
        enabled: true,
      }],
    });
    mockPromptPreferences();

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('提示词'));

    expect(await screen.findByText('代码审查助手')).toBeInTheDocument();
    expect(intervalSpy.mock.calls.filter((call) => call[1] === 4000)).toHaveLength(0);
  }
  finally {
    intervalSpy.mockRestore();
  }
});

it('keeps cached prompt assets visible and exposes retry when a background sync fails', async () => {
  let prompts = [{
    ...canonicalPromptRPCItem(),
    id: 'main/reviewer',
    name: '代码审查专家',
    content: '先检查阻塞问题',
    description: '审查代码质量',
    tags: ['intent:expert', 'review'],
    scope: 'project',
    enabled: true,
  }];
  backend.listPromptAssets.mockImplementation(() => Promise.resolve({ prompts }));
  mockPromptPreferences();

  render(<App />);
  await waitForBackendThreadHeading();
  fireEvent.click(screen.getByLabelText('提示词'));
  expect(await screen.findByText('代码审查专家')).toBeInTheDocument();

  backend.listPromptAssets.mockRejectedValueOnce(new Error('prompt backend offline'));
  await act(async () => {
    backend.__bridgeCallback?.({ type: 'prompts/changed', payload: { cwd: '/repo/app' } });
    await Promise.resolve();
  });

  expect(screen.getByText('代码审查专家')).toBeInTheDocument();
  const alert = await screen.findByRole('alert');
  expect(alert).toHaveTextContent('同步失败，显示的是上次成功的数据。');
  expect(alert).not.toHaveTextContent('prompt backend offline');
  expect(frontendHealthSnapshot()).toEqual(expect.arrayContaining([
    expect.objectContaining({ actionId: 'prompt.assets.load', diagnosticId: expect.any(String) }),
  ]));

  prompts = [{
    ...canonicalPromptRPCItem(),
    id: 'main/deploy',
    name: '部署助手',
    content: '先检查环境',
    description: '部署前检查',
    tags: ['intent:expert', 'deploy'],
    scope: 'project',
    enabled: true,
  }];
  fireEvent.click(screen.getByRole('button', { name: '重试同步' }));

  expect(await screen.findByText('部署助手')).toBeInTheDocument();
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
});

it('keeps prompt assets visible and exposes retry when active prompt preference sync fails', async () => {
  backend.listPromptAssets.mockResolvedValue({
    prompts: [{
      ...canonicalPromptRPCItem(),
      id: 'main/reviewer',
      name: '代码审查专家',
      content: '先检查阻塞问题',
      description: '审查代码质量',
      tags: ['intent:expert', 'review'],
      scope: 'project',
      enabled: true,
    }],
  });
  let activePreferenceFails = true;
  backend.getPreference.mockImplementation(({ key }) => {
    if (key === 'settings.activePromptKey') {
      return (
        activePreferenceFails
          ? Promise.reject(new Error('active prompt preference offline'))
          : Promise.resolve('')
      );
    }
    return Promise.resolve({
      'settings.provider.active': 'codex',
      'settings.provider.codex.model': 'gpt-5.5',
      'settings.provider.codex.effort': 'xhigh',
      'settings.provider.codex.codexHome': '~/.codex',
      'settings.provider.codex.codexInstanceKey': 'default',
      'settings.provider.codex.codexModelProvider': 'openai',
      'settings.provider.claude.model': 'sonnet',
      'settings.provider.claude.effort': 'high',
    }[key] ?? null);
  });

  render(<App />);
  await waitForBackendThreadHeading();
  fireEvent.click(screen.getByLabelText('提示词'));

  expect(await screen.findByText('代码审查专家')).toBeInTheDocument();
  const alert = await screen.findByRole('alert');
  expect(alert).toHaveTextContent('同步失败，显示的是上次成功的数据。');
  expect(alert).not.toHaveTextContent('active prompt preference offline');
  expect(frontendHealthSnapshot()).toEqual(expect.arrayContaining([
    expect.objectContaining({ actionId: 'prompt.active.load', diagnosticId: expect.any(String) }),
  ]));

  activePreferenceFails = false;
  fireEvent.click(within(alert).getByRole('button', { name: '重试同步' }));

  await waitFor(() => {
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });
  expect(screen.getByText('代码审查专家')).toBeInTheDocument();
});

it('shows a retryable blocking error instead of an empty prompt state on initial load failure', async () => {
  backend.listPromptAssets.mockRejectedValueOnce(new Error('prompt backend offline'));
  mockPromptPreferences();

  render(<App />);
  await waitForBackendThreadHeading();
  fireEvent.click(screen.getByLabelText('提示词'));

  const alert = await screen.findByRole('alert');
  expect(alert).toHaveTextContent('加载提示词失败，请重试。');
  expect(alert).not.toHaveTextContent('prompt backend offline');
  expect(frontendHealthSnapshot()).toEqual(expect.arrayContaining([
    expect.objectContaining({ actionId: 'prompt.assets.load', diagnosticId: expect.any(String) }),
  ]));
  expect(screen.queryByText('暂无内容')).not.toBeInTheDocument();

  backend.listPromptAssets.mockResolvedValueOnce({
    prompts: [{
      ...canonicalPromptRPCItem(),
      id: 'main/reviewer',
      name: '代码审查专家',
      content: '先检查阻塞问题',
      description: '审查代码质量',
      tags: ['intent:expert', 'review'],
      scope: 'project',
      enabled: true,
    }],
  });

  fireEvent.click(within(alert).getByRole('button', { name: '重试同步' }));

  expect(await screen.findByText('代码审查专家')).toBeInTheDocument();
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
});

it('falls back to the legacy prompt dashboard in readonly mode when prompt assets are unavailable', async () => {
  const missingMethodError = new Error('method not found');
  missingMethodError.code = -32601;
  backend.listPromptAssets.mockRejectedValueOnce(missingMethodError);
  backend.getDashboardPrompts.mockResolvedValueOnce({
    prompts: [{
      id: 17,
      prompt_key: 'legacy/prompt',
      title: '旧提示词',
      agent_key: 'main',
      tool_name: '',
      prompt_text: 'legacy readonly data',
      when_to_use: '',
      variables: {},
      tags: ['intent:expert', 'scope.cwd:/repo/app'],
      enabled: true,
      manually_edited: false,
      priority: 0,
      created_by: '',
      updated_by: '',
      created_at: '2026-07-11T00:00:00Z',
      updated_at: '2026-07-11T00:00:00Z',
      description: '',
    }],
  });
  mockPromptPreferences();

  render(<App />);
  await waitForBackendThreadHeading();
  fireEvent.click(screen.getByLabelText('提示词'));

  expect(await screen.findByText('旧提示词')).toBeInTheDocument();
  expect(screen.getByText(/只读模式/)).toBeInTheDocument();
  expect(backend.getDashboardPrompts).toHaveBeenCalledWith({ cwd: '/repo/app' });
  expect(screen.getByRole('button', { name: '查看' })).toBeInTheDocument();
});

it('keeps cached prompt assets visible when navigating back and refreshes silently', async () => {
  let prompts = [{
    ...canonicalPromptRPCItem(),
    id: 'main/reviewer',
    name: '代码审查专家',
    content: '先检查阻塞问题',
    description: '审查代码质量',
    tags: ['intent:expert', 'review'],
    scope: 'project',
    enabled: true,
  }];
  backend.listPromptAssets.mockImplementation(() => Promise.resolve({ prompts }));
  mockPromptPreferences();

  render(<App />);
  await waitForBackendThreadHeading();
  fireEvent.click(screen.getByLabelText('提示词'));
  expect(await screen.findByText('代码审查专家')).toBeInTheDocument();

  fireEvent.click(screen.getByLabelText('新对话'));
  prompts = [{
    ...canonicalPromptRPCItem(),
    id: 'main/deploy',
    name: '部署助手',
    content: '先检查环境',
    description: '部署前检查',
    tags: ['intent:expert', 'deploy'],
    scope: 'project',
    enabled: true,
  }];
  fireEvent.click(screen.getByLabelText('提示词'));

  expect(screen.queryByText('加载中...')).not.toBeInTheDocument();
  expect(screen.getByText('代码审查专家')).toBeInTheDocument();
  expect(await screen.findByText('部署助手')).toBeInTheDocument();
  expect(screen.queryByText('代码审查专家')).not.toBeInTheDocument();
});
