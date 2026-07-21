import { installAppTestHooks, testEnv } from "./test-utils/appTestHarness.jsx";
import { chatPageSource } from "./test-utils/appSourceFixtures.js";

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
  useClientStore,
  App,
  backend,
  deferred,
  waitForBackendThreadHeading,
  mockShortcutPreferenceLoad,
  getSidebarNavButton,
} = testEnv;

it('fails fast when required browser storage is unavailable', () => {
    const originalStorage = window.localStorage;
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {});
    Object.defineProperty(window, 'localStorage', { configurable: true, value: {} });
    try {
      expect(() => render(<App />)).toThrow(/theme storage is unavailable/);
    } finally {
      Object.defineProperty(window, 'localStorage', { configurable: true, value: originalStorage });
      consoleError.mockRestore();
    }
  });

it('keeps settings reachable from the collapsible workbench control', async () => {
    render(<App />);

    const shell = await screen.findByTestId('frontend-app');
    const toggle = screen.getByRole('button', { name: '打开工作台' });
    expect(toggle).toHaveAttribute('aria-expanded', 'false');

    fireEvent.click(toggle);
    expect(shell).toHaveClass('sidebar-open');
    expect(toggle).toHaveAttribute('aria-expanded', 'true');
    expect(screen.getByTestId('app-sidebar')).toHaveClass('is-open');

    fireEvent.click(within(screen.getByTestId('app-sidebar')).getByRole('button', { name: '设置' }));
    expect(document.querySelector('.suiyuan-appbar-title h1')).toHaveTextContent('设置');
    expect(within(screen.getByTestId('app-sidebar')).getByRole('button', { name: '设置' })).toHaveClass('active');
    expect(shell).not.toHaveClass('sidebar-open');
  });

it('uses the custom brand icon only in the sidebar brand area', async () => {
    render(<App />);

    const sidebar = await screen.findByTestId('app-sidebar');
    expect(sidebar.querySelector('.suiyuan-brand-block img')?.getAttribute('src')).toContain('suiyuan-brand-icon.png');
    expect(sidebar.querySelector('.sidebar-tree-folder img')).toBeNull();
    expect(sidebar.querySelector('.suiyuan-nav-item svg')).toBeInTheDocument();
  });

it('keeps the workbench sidebar class stable while switching between chat and tools', async () => {
    render(<App />);

    const sidebar = await screen.findByTestId('app-sidebar');
    expect(sidebar).not.toHaveClass('app-sidebar--chat');

    fireEvent.click(getSidebarNavButton('插件与技能'));
    await waitFor(() => expect(getSidebarNavButton('插件与技能')).toHaveClass('active'));
    expect(sidebar).not.toHaveClass('app-sidebar--chat');

    fireEvent.click(screen.getByRole('button', { name: '新对话' }));
    await waitFor(() => expect(useClientStore.getState().activePage).toBe('chat'));
    expect(sidebar).not.toHaveClass('app-sidebar--chat');
  });

it('shows the project tree only while the chat page is active', async () => {
    render(<App />);

    const sidebar = await screen.findByTestId('app-sidebar');
    const nav = within(sidebar).getByRole('navigation', { name: 'Suiyuan navigation' });

    expect(within(sidebar).getByRole('region', { name: '项目' })).toBeInTheDocument();
    expect(within(sidebar).getByRole('button', { name: '添加项目目录' })).toBeInTheDocument();

    fireEvent.click(within(nav).getByRole('button', { name: '插件与技能' }));
    await waitFor(() => expect(useClientStore.getState().activePage).toBe('skills'));
    expect(within(sidebar).queryByRole('region', { name: '项目' })).not.toBeInTheDocument();
    expect(within(sidebar).queryByRole('button', { name: '添加项目目录' })).not.toBeInTheDocument();

    fireEvent.click(within(nav).getByRole('button', { name: '聊天页面' }));
    await waitFor(() => expect(useClientStore.getState().activePage).toBe('chat'));
    expect(within(sidebar).getByRole('region', { name: '项目' })).toBeInTheDocument();
    expect(within(sidebar).getByRole('button', { name: '添加项目目录' })).toBeInTheDocument();
  });

it('keeps project threads under their owning project node', async () => {
  backend.getProjects.mockResolvedValue({ projects: ['/repo/app', '/repo/other'], active: '/repo/app' });
  backend.getSidebarState.mockImplementation(({ cwd }) => Promise.resolve(
      cwd === '/repo/other'
        ? {
          activeThreadId: 'thread-other',
          threads: [{ id: 'thread-other', cwd: '/repo/other', name: 'Other project chat', provider: 'claude', status: 'idle' }],
        }
        : {
          activeThreadId: 'thread-1',
          threads: [{ id: 'thread-1', cwd: '/repo/app', name: '后端线程', provider: 'codex', status: '工作中' }],
        },
    ));

    render(<App />);

    const sidebar = await screen.findByTestId('app-sidebar');
    const projects = await within(sidebar).findByRole('region', { name: '项目' });
    const appThreads = within(projects).getByRole('list', { name: 'app 聊天记录' });
    const otherThreads = within(projects).getByRole('list', { name: 'other 聊天记录' });

    expect(await within(appThreads).findByText('后端线程')).toBeInTheDocument();
    expect(within(projects).queryByText('Other project chat')).not.toBeInTheDocument();

    fireEvent.click(within(projects).getByRole('button', { name: '选择项目 other' }));

    expect(await within(otherThreads).findByText('Other project chat')).toBeInTheDocument();
    expect(within(appThreads).queryByText('Other project chat')).not.toBeInTheDocument();
    expect(within(otherThreads).queryByText('后端线程')).not.toBeInTheDocument();
  });

it('starts a new empty draft from the screenshot sidebar new chat button', async () => {
    render(<App />);

    await waitForBackendThreadHeading();
    expect(screen.queryByText('我们应该在 燧元 中构建什么？')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '新对话' }));

    await screen.findByText('我们应该在 燧元 中构建什么？');
    expect(within(screen.getByTestId('app-sidebar')).getByRole('button', { name: '聊天页面' })).toHaveClass('active');
    expect(within(screen.getByTestId('app-sidebar')).getByRole('button', { name: '添加项目目录' })).toBeInTheDocument();
    expect(screen.getByTestId('composer-input')).toHaveValue('');
  });

it('dispatches the real new-chat, settings, sidebar, and palette commands from the app window', async () => {
    render(<App />);

    await waitForBackendThreadHeading();
    fireEvent.keyDown(window, { key: 'n', ctrlKey: true });
    await screen.findByText('我们应该在 燧元 中构建什么？');

    fireEvent.keyDown(window, { key: ',', ctrlKey: true });
    await waitFor(() => expect(useClientStore.getState().activePage).toBe('settings'));

    const appShell = screen.getByTestId('frontend-app');
    const sidebarWasOpen = appShell.classList.contains('sidebar-open');
    fireEvent.keyDown(window, { key: 'b', ctrlKey: true });
    await waitFor(() => expect(appShell.classList.contains('sidebar-open')).not.toBe(sidebarWasOpen));

    expect(appShell).toHaveAttribute('data-command-palette-open', 'false');
    fireEvent.keyDown(window, { key: 'k', ctrlKey: true });
    await waitFor(() => expect(appShell).toHaveAttribute('data-command-palette-open', 'true'));
  });

it('removes the ChatPage-global Escape listener after app command dispatch owns interruption', async () => {
    render(<App />);
    await waitForBackendThreadHeading();

    expect(chatPageSource).not.toContain('useChatInterruptShortcut');
  });

it('renders the real command palette state, executes a command, and closes the dialog', async () => {
    mockShortcutPreferenceLoad(() => Promise.resolve({}));
    render(<App />);
    await waitForBackendThreadHeading();
    await act(async () => Promise.resolve());

    fireEvent.keyDown(window, { key: 'k', ctrlKey: true });

    const palette = screen.getByRole('dialog', { name: '命令面板' });
    fireEvent.change(within(palette).getByRole('searchbox'), { target: { value: '打开设置' } });
    fireEvent.click(within(palette).getByRole('option', { name: /打开设置/ }));

    await waitFor(() => expect(useClientStore.getState().activePage).toBe('settings'));
    expect(screen.queryByRole('dialog', { name: '命令面板' })).not.toBeInTheDocument();
  });

it('localizes the disabled interrupt reason in the English command palette', async () => {
    render(<App />);
    await waitForBackendThreadHeading();
    act(() => {
      useClientStore.setState({ activeThreadId: '', activeTurnByThread: {} });
    });
    fireEvent.click(screen.getByRole('button', { name: '切换到 English' }));
    fireEvent.keyDown(window, { key: 'k', ctrlKey: true });

    const palette = await screen.findByRole('dialog', { name: 'Command palette' });
    const interrupt = within(palette).getByRole('option', { name: /Interrupt current task/ });
    expect(interrupt).toHaveTextContent('No active task to interrupt');
    expect(interrupt).not.toHaveTextContent('当前没有可中断任务');
  });

it('does not install an executable default dispatcher while shortcut preferences are pending', async () => {
    const shortcutLoad = deferred();
    mockShortcutPreferenceLoad(() => shortcutLoad.promise);
    render(<App />);
    await waitForBackendThreadHeading();

    fireEvent.keyDown(window, { key: 'n', ctrlKey: true });

    expect(screen.getByRole('heading', { name: '后端线程' })).toBeInTheDocument();
    expect(screen.queryByText('我们应该在 燧元 中构建什么？')).not.toBeInTheDocument();
    await act(async () => {
      shortcutLoad.resolve({});
    });
  });

it.each([
    ['load rejection', new Error('preference backend unavailable')],
    ['unknown command', { 'unknown.command': { key: 'x', meta: false, ctrl: true, alt: false, shift: false } }],
    ['effective conflict', { 'settings.open': { key: 'n', meta: false, ctrl: true, alt: false, shift: false } }],
  ])('blocks all shortcuts and shows a visible configuration error for %s', async (_name, result) => {
    mockShortcutPreferenceLoad(() => (
      result instanceof Error ? Promise.reject(result) : Promise.resolve(result)
    ));
    render(<App />);
    await waitForBackendThreadHeading();

    const error = await screen.findByTestId('shortcut-config-error');
    expect(error).toHaveAttribute('role', 'alert');
    fireEvent.keyDown(window, { key: 'n', ctrlKey: true });

    expect(screen.getByRole('heading', { name: '后端线程' })).toBeInTheDocument();
    expect(screen.queryByText('我们应该在 燧元 中构建什么？')).not.toBeInTheDocument();
  });

it('uses the authoritative loaded shortcut override instead of the default binding', async () => {
    mockShortcutPreferenceLoad(() => Promise.resolve({
      'chat.new': { key: 'm', meta: false, ctrl: true, alt: false, shift: false },
    }));
    render(<App />);
    await waitForBackendThreadHeading();
    await act(async () => Promise.resolve());

    fireEvent.keyDown(window, { key: 'n', ctrlKey: true });
    expect(screen.getByRole('heading', { name: '后端线程' })).toBeInTheDocument();

    fireEvent.keyDown(window, { key: 'm', ctrlKey: true });
    await screen.findByText('我们应该在 燧元 中构建什么？');
  });

it('rebinds the runtime only after save completes its authoritative read-after-write', async () => {
    let shortcutPreference = {};
    mockShortcutPreferenceLoad(() => Promise.resolve(shortcutPreference));
    backend.setPreference.mockImplementation(({ key, value }) => {
      if (key === 'settings.shortcuts.bindings') shortcutPreference = value;
      return Promise.resolve({ ok: true });
    });
    render(<App />);
    await waitForBackendThreadHeading();
    await act(async () => Promise.resolve());

    fireEvent.keyDown(window, { key: ',', ctrlKey: true });
    const shortcutCard = await screen.findByTestId('shortcut-settings-card');
    fireEvent.keyDown(within(shortcutCard).getByRole('button', { name: /修改快捷键.*新建对话/ }), {
      key: 'm',
      ctrlKey: true,
    });
    fireEvent.click(within(shortcutCard).getByRole('button', { name: '保存快捷键' }));
    await waitFor(() => expect(backend.setPreference).toHaveBeenCalledWith({
      cwd: '/repo/app',
      key: 'settings.shortcuts.bindings',
      value: { 'chat.new': { key: 'm', meta: false, ctrl: true, alt: false, shift: false } },
    }));
    await waitFor(() => expect(backend.getPreference.mock.calls.filter(([params]) => (
      params.key === 'settings.shortcuts.bindings'
    ))).toHaveLength(2));

    fireEvent.keyDown(window, { key: 'n', ctrlKey: true });
    expect(screen.queryByText('我们应该在 燧元 中构建什么？')).not.toBeInTheDocument();
    fireEvent.keyDown(window, { key: 'm', ctrlKey: true });
    await screen.findByText('我们应该在 燧元 中构建什么？');
  });
