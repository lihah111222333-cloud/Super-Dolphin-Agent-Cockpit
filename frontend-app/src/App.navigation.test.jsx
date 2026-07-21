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
  App,
  backend,
  waitForBackendThreadHeading,
  getSidebarNavButton,
  findThreadCardByName,
  mockTraceDashboardQueryResult,
  openTraceDashboardForTraceId,
  expectTraceDashboardRpcCalls,
  expectTraceDashboardRows,
  expectTraceDashboardDetails,
  showAllTraceDashboardEvents,
  mockRecentSystemLogsResult,
  openRecentSystemLogs,
  expectRecentSystemLogsTable,
  expectRecentSystemLogsRpcCall,
  copyTraceFromRecentLogs,
  toggleInlineTraceFromRecentLogs,
} = testEnv;

it('opens observability tracing dashboard and queries by trace id', async () => {
    mockTraceDashboardQueryResult();

    const table = await openTraceDashboardForTraceId();

    await expectTraceDashboardRows(table);
    expectTraceDashboardDetails();
    expectTraceDashboardRpcCalls();
    await showAllTraceDashboardEvents();
  });

it('renders recent system logs and opens a trace from the table', async () => {
    mockRecentSystemLogsResult();

    const table = await openRecentSystemLogs();

    expectRecentSystemLogsTable(table);
    expectRecentSystemLogsRpcCall();
    await copyTraceFromRecentLogs(table);
    await toggleInlineTraceFromRecentLogs(table);
  });

it('keeps the observability page focused on filtered logs and trace drilldown', async () => {
    render(<App />);

    fireEvent.click(await screen.findByRole('button', { name: '链路追踪' }));

    expect(screen.queryByTestId('observability-backend-logs')).not.toBeInTheDocument();
    expect(screen.queryByTestId('observability-status')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '刷新慢点/错误' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '查询 Trace' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '查询 Thread Recent' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: '查询最新日志' })).toBeInTheDocument();
  });

it('bootstraps project, sidebar, and timeline from backend without the removed work status bar', async () => {
    const { container } = render(<App />);

    expect(await waitForBackendThreadHeading()).toBeInTheDocument();
    expect(within(screen.getByLabelText('Suiyuan app bar')).queryByRole('button', { name: '选择项目' })).not.toBeInTheDocument();
    expect(container.querySelector('.work-status')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));
    expect(await within(screen.getByTestId('runtime-panel')).findByRole('button', { name: '折叠 file' })).toBeInTheDocument();
    expect(screen.queryByText(/diff --git a\/file b\/file/)).not.toBeInTheDocument();
    expect(backend.getProjects).toHaveBeenCalledWith({ cwd: '/repo/app' });
    expect(backend.getThreadState).toHaveBeenCalledWith({
      cwd: '/repo/app',
      threadId: 'thread-1',
      includeDiff: true,
    });
  });

it('keeps project selection out of the Suiyuan shell toolbar', async () => {
    render(<App />);

    expect(await waitForBackendThreadHeading()).toBeInTheDocument();
    const topAppBar = within(screen.getByLabelText('Suiyuan app bar'));
    expect(topAppBar.queryByRole('button', { name: '选择项目' })).not.toBeInTheDocument();
    expect(topAppBar.queryByLabelText('当前工作目录')).not.toBeInTheDocument();
    const sidebarToggle = screen.getByRole('button', { name: '显示侧边栏' });
    expect(sidebarToggle).toHaveAttribute('title', '显示侧边栏');
    expect(sidebarToggle).not.toHaveTextContent('侧边栏');
  });

it('exposes an explicit collapse control inside the Suiyuan sidebar', () => {
    render(<App skipBootstrap />);

    const shell = screen.getByTestId('frontend-app');
    fireEvent.click(screen.getByRole('button', { name: '展开侧栏' }));
    expect(shell).toHaveClass('sidebar-open');

    const collapseButton = within(screen.getByTestId('app-sidebar')).getByRole('button', { name: '折叠侧栏' });
    expect(collapseButton).toHaveAttribute('title', '折叠侧栏');
    expect(collapseButton.textContent).toBe('');
    fireEvent.click(collapseButton);

    expect(shell).toHaveClass('sidebar-collapsed');
    expect(screen.getByRole('button', { name: '展开侧栏' })).toBeInTheDocument();
  });

it('renders the Stitch Suiyuan sidebar primary navigation order', () => {
    render(<App skipBootstrap />);

    const navButtons = Array.from(screen.getByTestId('sidebar-nav').querySelectorAll('.suiyuan-nav-item'));

    expect(navButtons.map((button) => button.textContent)).toEqual([
      '聊天页面',
      '插件与技能',
      '自动化',
      '提示词',
      '共享文件',
      '记忆中心',
      '链路追踪',
    ]);
    expect(navButtons.map((button) => button.querySelector('svg')?.classList.value)).toEqual([
      expect.stringContaining('lucide-message-square-text'),
      expect.stringContaining('lucide-puzzle'),
      expect.stringContaining('lucide-sliders-horizontal'),
      expect.stringContaining('lucide-circle-user-round'),
      expect.stringContaining('lucide-folder-open'),
      expect.stringContaining('lucide-brain'),
      expect.stringContaining('lucide-database'),
    ]);
    expect(screen.getByRole('button', { name: '新对话' }).querySelector('svg')).toHaveClass('lucide-plus');
  });

it('keeps only reachable Suiyuan footer actions outside the primary rail', () => {
    render(<App skipBootstrap />);

    expect(within(screen.getByTestId('app-sidebar')).getAllByRole('button').slice(-1).map((button) => button.getAttribute('aria-label'))).toEqual([
      '设置',
    ]);
    expect(within(screen.getByTestId('app-sidebar')).queryByRole('button', { name: 'Support' })).not.toBeInTheDocument();
  });

it('renders the mobile bottom navigation with core destinations and active state', async () => {
    render(<App skipBootstrap />);

    const mobileNav = screen.getByTestId('mobile-nav');
    expect(mobileNav).toHaveAttribute('aria-label', '主要导航');
    const items = within(mobileNav).getAllByRole('button');
    expect(items.map((button) => button.textContent)).toEqual(['聊天', '插件', '定制角色', '记忆', '设置']);
    expect(items.map((button) => button.querySelector('svg')?.classList.value)).toEqual([
      expect.stringContaining('lucide-message-square-text'),
      expect.stringContaining('lucide-puzzle'),
      expect.stringContaining('lucide-circle-user-round'),
      expect.stringContaining('lucide-brain'),
      expect.stringContaining('lucide-settings'),
    ]);
    expect(within(mobileNav).getByRole('button', { name: '聊天' })).toHaveAttribute('aria-current', 'page');

    fireEvent.click(within(mobileNav).getByRole('button', { name: '记忆' }));

    await waitFor(() => expect(window.location.pathname).toBe('/memory'));
    expect(within(mobileNav).getByRole('button', { name: '记忆' })).toHaveAttribute('aria-current', 'page');
    expect(within(mobileNav).getByRole('button', { name: '聊天' })).not.toHaveAttribute('aria-current');
  });

it('uses the current URL path as the active page on boot', async () => {
    window.history.pushState({}, '', '/dags');
    backend.getWindowBootstrap.mockResolvedValueOnce({ snapshot: { page: 'chat' } });

    render(<App />);

    const workflowButton = await screen.findByRole('button', { name: '自动化' });
    await waitFor(() => expect(workflowButton).toHaveClass('active'));
    expect(document.querySelector('.suiyuan-appbar-title h1')).toHaveTextContent('自动化');
    expect(window.location.pathname).toBe('/dags');
  });

it.each(['/tasks', '/commands'])('falls back to chat for the removed %s route', async (pathname) => {
    window.history.pushState({}, '', pathname);

    render(<App />);

    const chatButton = await screen.findByRole('button', { name: '聊天页面' });
    await waitFor(() => expect(chatButton).toHaveClass('active'));
    expect(screen.queryByRole('button', { name: '任务' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '命令' })).not.toBeInTheDocument();
    expect(document.querySelector('.suiyuan-appbar-title h1')).toHaveTextContent('聊天页面');
  });

it('lets user navigation override the explicit boot URL after initial route sync', async () => {
    window.history.pushState({}, '', '/dags');

    render(<App skipBootstrap />);

    const workflowButton = await screen.findByRole('button', { name: '自动化' });
    await waitFor(() => expect(workflowButton).toHaveClass('active'));

    fireEvent.click(getSidebarNavButton('插件与技能'));

    await waitFor(() => expect(getSidebarNavButton('插件与技能')).toHaveClass('active'));
    expect(document.querySelector('.suiyuan-appbar-title h1')).toHaveTextContent('插件与技能');
    expect(window.location.pathname).toBe('/skills');
  });

it('writes page navigation to browser history and restores it on popstate', async () => {
    render(<App skipBootstrap />);

    fireEvent.click(getSidebarNavButton('插件与技能'));
    await waitFor(() => expect(window.location.pathname).toBe('/skills'));

    fireEvent.click(within(screen.getByTestId('app-sidebar')).getByRole('button', { name: '设置' }));
    await waitFor(() => expect(window.location.pathname).toBe('/settings'));

    await act(async () => {
      window.history.pushState({ activePage: 'skills' }, '', '/skills');
      window.dispatchEvent(new PopStateEvent('popstate', { state: { activePage: 'skills' } }));
    });

    await waitFor(() => expect(getSidebarNavButton('插件与技能')).toHaveClass('active'));
    expect(document.querySelector('.suiyuan-appbar-title h1')).toHaveTextContent('插件与技能');
  });

it('hides idle status noise while keeping the provider badge in thread cards', async () => {
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: '静默会话', provider: 'codex', status: 'idle' }],
    });

    render(<App />);

    const card = await findThreadCardByName('静默会话');
    expect(within(card).queryByRole('button', { name: '重命名会话' })).not.toBeInTheDocument();
    expect(card).toHaveTextContent('codex');
    expect(card).not.toHaveTextContent('idle');
    expect(card.querySelector('em')).toBeNull();
    expect(card.querySelector('.thread-status-dot')).toHaveClass('thread-status-dot--idle');
  });
