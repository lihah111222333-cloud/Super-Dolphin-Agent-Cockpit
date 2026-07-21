import { installAppTestHooks, testEnv } from "./test-utils/appTestHarness.jsx";
import {
  appRoutesSource,
  appSource,
  chatPageSource,
  chatWorkbenchLayoutSource,
} from "./test-utils/appSourceFixtures.js";

installAppTestHooks();
const {
  React,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
  expect,
  it,
  vi,
  AppErrorBoundary,
  App,
  support,
  waitForBackendThreadHeading,
  createShellLayoutStorage,
} = testEnv;

it('wires one required overlay host through the App React Aria provider and existing theme owner', () => {
  expect(appSource).toMatch(/import\s+\{[^}]*UNSAFE_PortalProvider[^}]*\}\s+from\s+['"]react-aria['"]/);
  expect(appSource).toMatch(/import\s+\{\s*requiredOverlayRoot\s*\}\s+from\s+['"]\.\/shared\/ui\/overlayPortalRoot\.js['"]/);
  expect(appSource).toMatch(/const\s+overlayRoot\s*=\s*requiredOverlayRoot\(\)/);
  expect(appSource).not.toMatch(/function\s+requiredOverlayRoot\s*\(/);
  expect(appSource).not.toMatch(/querySelectorAll\(['"]#overlay-root['"]\)/);
  expect(appSource).toMatch(/<UNSAFE_PortalProvider\b[\s\S]{0,200}getContainer=\{[^}]*overlayRoot[^}]*\}/);
  expect(appSource).toContain('useColorTheme');
  expect(appSource).not.toMatch(/overlay(?:Theme)?(?:Store|Storage|Persistence)/i);
  expect(appSource).not.toMatch(/overlayRoot\s*(?:\|\||\?\?)\s*document\.body/);
});

it('removes only its own theme projection and overwrites stale values on remount', async () => {
  let view = render(<App skipBootstrap />);
  await screen.findByTestId('frontend-app');
  expect(support.appOverlayHost).toHaveAttribute('data-theme', 'light');

  view.unmount();
  expect(support.appOverlayHost).not.toHaveAttribute('data-theme');

  support.appOverlayHost.setAttribute('data-theme', 'stale');
  view = render(<App skipBootstrap />);
  await screen.findByTestId('frontend-app');
  expect(support.appOverlayHost).toHaveAttribute('data-theme', 'light');

  support.appOverlayHost.setAttribute('data-theme', 'external');
  view.unmount();
  expect(support.appOverlayHost).toHaveAttribute('data-theme', 'external');

  support.appOverlayHost.setAttribute('data-theme', 'stale');
  view = render(<App skipBootstrap />);
  await screen.findByTestId('frontend-app');
  expect(support.appOverlayHost).toHaveAttribute('data-theme', 'light');
  view.unmount();
  expect(support.appOverlayHost).not.toHaveAttribute('data-theme');
});

it.each(['missing', 'duplicate'])('contains a %s overlay-root failure in the existing app boundary', async (mode) => {
  if (mode === 'missing') {
    support.appOverlayHost.remove();
  } else {
    const duplicate = document.createElement('div');
    duplicate.id = 'overlay-root';
    document.body.append(duplicate);
  }
  const reporter = vi.fn().mockResolvedValue(undefined);
  const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {});

  try {
    render(
      <AppErrorBoundary reporter={reporter} routeId="chat" reload={vi.fn()}>
        <App skipBootstrap />
      </AppErrorBoundary>,
    );

    expect(screen.getByRole('heading', { name: '界面发生错误' })).toBeInTheDocument();
    expect(screen.queryByTestId('frontend-app')).not.toBeInTheDocument();
    expect(screen.getByRole('alert')).not.toHaveTextContent('overlay-root');
    await waitFor(() => expect(reporter).toHaveBeenCalledTimes(1));
  } finally {
    consoleError.mockRestore();
  }
});

it('wires one explicit shell layout store from App through the chat route and layout hooks', () => {
  expect(appSource).toContain('createShellLayoutStore');
  expect(appSource).toContain('shellLayoutStorage');
  expect(appSource).toContain('shellLayoutStore');
  expect(appSource).toContain("from './shared/model/useShellLayoutStore.js'");
  expect(appRoutesSource).toMatch(/function ChatPageRoute\(props\)[\s\S]{0,240}const \{[^}]*shellLayoutStore[^}]*\} = props/);
  expect(appRoutesSource).toMatch(/<ChatPage[\s\S]{0,320}shellLayoutStore=\{shellLayoutStore\}/);
  expect(appRoutesSource).toMatch(/<ChatPageRoute[\s\S]{0,320}shellLayoutStore=\{shellLayoutStore\}/);
  expect(chatPageSource).toMatch(/function ChatPage\(props\)\s*\{\s*const \{[^\n]*shellLayoutStore/);
  expect(chatPageSource).toContain('useShellLayoutStore');
  expect(chatPageSource).toContain("from '../../shared/model/useShellLayoutStore.js'");
  expect(chatWorkbenchLayoutSource).not.toContain('store.rightPanelWidth');
  expect(chatWorkbenchLayoutSource).not.toContain('store.setRightPanelWidth');
});

it('persists the shell layout initial width exactly once under StrictMode', () => {
  const storage = createShellLayoutStorage();

  render(
    <React.StrictMode>
      <App skipBootstrap shellLayoutStorage={storage} />
    </React.StrictMode>,
  );

  expect(storage.set).toHaveBeenCalledExactlyOnceWith(
    'super-dolphin.shell.right-panel-width',
    '380',
  );
  expect(storage.remove).not.toHaveBeenCalled();
});

it('renders the persisted shell layout width through the real chat layout', async () => {
  Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1980 });
  const storage = createShellLayoutStorage('480.5');

  render(<App shellLayoutStorage={storage} />);
  fireEvent.click(await screen.findByRole('button', { name: '显示侧边栏' }));
  await waitForBackendThreadHeading();

  expect(screen.getByTestId('chat-layout')).toHaveStyle({
    gridTemplateColumns: 'minmax(0, 1fr) 6px 480.5px',
  });
  expect(storage.set).not.toHaveBeenCalled();
});

it.each([
  ['read', (storage) => storage.get.mockImplementation(() => { throw new Error('private shell layout read'); })],
  ['first write', (storage) => storage.set.mockImplementation(() => { throw new Error('private shell layout write'); })],
])('contains shell layout %s failures in the existing app boundary without fallback state', async (_phase, failStorage) => {
  const storage = createShellLayoutStorage();
  failStorage(storage);
  const reporter = vi.fn().mockResolvedValue(undefined);
  const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {});

  try {
    render(
      <AppErrorBoundary reporter={reporter} routeId="chat" reload={vi.fn()}>
        <App skipBootstrap shellLayoutStorage={storage} />
      </AppErrorBoundary>,
    );

    expect(screen.getByRole('heading', { name: '界面发生错误' })).toBeInTheDocument();
    expect(screen.queryByTestId('chat-layout')).not.toBeInTheDocument();
    expect(storage.remove).not.toHaveBeenCalled();
    await waitFor(() => expect(reporter).toHaveBeenCalledTimes(1));
    expect(JSON.stringify(reporter.mock.calls[0][0])).not.toContain('private shell layout');
  }
  finally {
    consoleError.mockRestore();
  }
});

it('renders the screenshot-style workbench sidebar and defaults to light theme', async () => {
    render(<App />);

    const shell = await screen.findByTestId('frontend-app');
    const sidebar = screen.getByTestId('app-sidebar');
    const appbar = document.querySelector('.suiyuan-top-appbar');
    expect(shell).toHaveAttribute('data-theme', 'light');
    expect(document.querySelector('.traffic-lights')).not.toBeInTheDocument();
    expect(document.querySelector('.titlebar')).not.toBeInTheDocument();
    expect(within(sidebar).getByText('燧元')).toBeInTheDocument();
    expect(within(sidebar).getByRole('button', { name: '新对话' })).toHaveTextContent('新对话');
    expect(within(sidebar).getByRole('button', { name: '设置' })).toHaveTextContent('设置');
    expect(within(sidebar).getByRole('button', { name: '聊天页面' })).toHaveTextContent('聊天页面');
    expect(within(sidebar).getByRole('button', { name: '插件与技能' })).toHaveTextContent('插件与技能');
    expect(within(appbar).getByRole('button', { name: '通知' })).toBeInTheDocument();
    expect(within(appbar).getByRole('button', { name: '历史记录' })).toBeInTheDocument();
    expect(screen.queryByText('Overview')).not.toBeInTheDocument();
    expect(screen.queryByText('Usage')).not.toBeInTheDocument();
    expect(screen.queryByText('Limits')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Upgrade Plan' })).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '切换到 English' }));
    expect(within(sidebar).getByRole('button', { name: 'New chat' })).toHaveTextContent('New chat');
    expect(within(sidebar).getByRole('button', { name: 'Chat' })).toHaveTextContent('Chat');
    expect(within(appbar).getByRole('button', { name: 'Notifications' })).toBeInTheDocument();
    expect(within(appbar).getByRole('button', { name: 'History' })).toBeInTheDocument();
    expect(document.querySelector('.suiyuan-appbar-title h1')).toHaveTextContent('Chat');
    fireEvent.click(screen.getByRole('button', { name: 'Switch to 中文' }));
    expect(within(sidebar).getByRole('button', { name: '新对话' })).toBeInTheDocument();
    expect(document.querySelector('.suiyuan-appbar-title h1')).toHaveTextContent('聊天页面');
    expect(within(sidebar).queryByRole('separator', { name: '调整工作台侧栏宽度' })).not.toBeInTheDocument();
  });
