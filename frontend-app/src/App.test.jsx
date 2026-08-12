import React from 'react';
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import App from './App.jsx';
import appSource from './App.jsx?raw';
import appRoutesSource from './AppRoutes.jsx?raw';
import { AppErrorBoundary } from './app/AppErrorBoundary.jsx';
import chatPageSource from './pages/chat/ChatPage.jsx?raw';
import chatWorkbenchLayoutSource from './pages/chat/hooks/useChatWorkbenchLayout.js?raw';
import { resetClientStoreForTests, useClientStore } from './entities/client/model/useClientStore.js';
import './test-utils/preloadAppRouteModules.js';
import { createAppTestSupport } from './test/appTestSupport.test-helper.jsx';

let bridgeCallback;
let appOverlayHost;

window.matchMedia = vi.fn(() => ({
  matches: false,
  addEventListener: vi.fn(),
  removeEventListener: vi.fn(),
}));

const backend = vi.hoisted(() => {
  const mockNames = `
	    readConfig getWindowBootstrap openNewWindow getProjects setActiveProject addProject removeProject
	    callBackend checkAppUpdate installLatestAppUpdate
    getSidebarState getThreadState getThreadMessages getBuildInfo getVideoApiKey getDashboardPage getObservabilityStatus
    getObservabilityTrace getObservabilityThreadRecent listObservabilityRecent listObservabilitySlow
    listObservabilityErrors listSharedFiles listPromptAssets getDashboardPrompts getPrompt writePrompt
    readLspPromptHint writeLspPromptHint readBuiltinTools writeBuiltinTool listDashboardLogs
    getPersonalizationProfile savePersonalizationProfile listPromptSections writePromptSection deletePromptSection
    deletePrompt draftPromptIntent commitPromptIntent discardPromptIntent dryRunPromptIntent getMemorySnapshot
    getMemoryEntry upsertMemoryEntry deleteMemoryEntry setMemoryAutoDreamIntent mergeMemoryEntries
    ignoreMemorySimilarity consolidateMemorySimilarities startConsolidateMemorySimilarities getMemoryConsolidationStatus
    listDags getDagDetail getDagRuns getDagRun startDag terminateDagRun deleteDag applyDagOps listWorkflowTemplates getWorkflowTemplate renderWorkflowTemplateDraft deleteSkill
    listCronJobs getCronJob createCronJob updateCronJob deleteCronJob runCronJobOnce setCronJobEnabled listCronJobRuns
    readSkill listSkillFiles createSkill writeSkill importSkillDirectories suggestSkillSummary selectProjectDir selectProjectDirs
    createSkillTool listSkillTools getSkillTool updateSkillTool deleteSkillTool
    listMCPServers listToolbridgeTools startSQLiteMCPServer stopSQLiteMCPServer startPlaywrightMCPServer stopPlaywrightMCPServer
    listSkillResolutions previewSkillResolution applySkillResolution readSharedFile deleteSharedFile getPreference
    forkThread startThread startTurn interruptTurn forceCompleteTurn compactThread recoverThread respondApproval resolveThreadIdentity archiveThread unarchiveThread
    deleteThread getThreadConfig setThreadConfig renameThread setPreference setVideoApiKey selectFiles saveClipboardImage saveTextFile
    locateCodeFile openCodeFile openPath saveCodeFile beginTextClipboardWrite copyTextToClipboard emitFrontendTraceEvent
  `.trim().split(/\s+/);
  return {
    ...Object.fromEntries(mockNames.map((name) => [name, vi.fn()])),
    onFilesDropped: vi.fn(() => () => {}),
    onRuntimeReconnect: vi.fn(() => () => {}),
    onBridgeEvent: vi.fn((callback) => {
      bridgeCallback = callback;
      return () => {
        if (bridgeCallback === callback) bridgeCallback = null;
      };
    }),
  };
});

vi.mock('./shared/api/backendApi.js', () => ({
  ...backend,
  registerBridgeLogStore: vi.fn(),
  sendFrontendLogBatch: vi.fn(),
}));

vi.mock('mermaid', () => ({
  default: {
    initialize: vi.fn(),
    render: vi.fn((_id, source) => Promise.resolve({
      svg: `<svg role="img" aria-label="mock mermaid"><text>${source}</text></svg>`,
    })),
  },
}));

const appTestSupport = createAppTestSupport({
  App,
  backend,
  resetClientStoreForTests,
  state: {
    get bridgeCallback() { return bridgeCallback; },
    set bridgeCallback(value) { bridgeCallback = value; },
    get appOverlayHost() { return appOverlayHost; },
    set appOverlayHost(value) { appOverlayHost = value; },
  },
});
const {
  deferred,
  waitForBackendThreadHeading,
  mockShortcutPreferenceLoad,
  getSidebarNavButton,
  createShellLayoutStorage,
  installAppOverlayHost,
  resetConnectedShellTestState,
  mockBootstrapBackendDefaults,
  mockDashboardPageDefaults,
  mockObservabilityDefaults,
  mockPromptDefaults,
  mockMemoryDefaults,
  mockWorkflowDefaults,
  mockCronDefaults,
  mockSkillDefaults,
  mockSharedFileDefaults,
  mockSettingsAndThreadDefaults,
  resetFrontendHealthForTest,
  cleanupAppTest
} = appTestSupport;

beforeEach(installAppOverlayHost);
beforeEach(resetConnectedShellTestState);
beforeEach(mockBootstrapBackendDefaults);
beforeEach(mockDashboardPageDefaults);
beforeEach(mockObservabilityDefaults);
beforeEach(mockPromptDefaults);
beforeEach(mockMemoryDefaults);
beforeEach(mockWorkflowDefaults);
beforeEach(mockCronDefaults);
beforeEach(mockSkillDefaults);
beforeEach(mockSharedFileDefaults);
beforeEach(mockSettingsAndThreadDefaults);
beforeEach(resetFrontendHealthForTest);
afterEach(cleanupAppTest);

it('wires one required overlay host through the App React Aria provider and existing theme owner', () => {
  expect(appSource).toMatch(/import\s+\{[^}]*UNSAFE_PortalProvider[^}]*\}\s+from\s+['"]react-aria['"]/);
  expect(appSource).toMatch(/import\s+\{\s*requiredOverlayRoot\s*\}\s+from\s+['"]\.\/shared\/ui\/OverlayPortal\.jsx['"]/);
  expect(appSource).toMatch(/const\s+overlayRoot\s*=\s*requiredOverlayRoot\(\)/);
  expect(appSource).not.toMatch(/function\s+requiredOverlayRoot\s*\(/);
  expect(appSource).not.toMatch(/querySelectorAll\(['"]#overlay-root['"]\)/);
  expect(appSource).toMatch(/<UNSAFE_PortalProvider\b[\s\S]{0,200}getContainer=\{[^}]*overlayRoot[^}]*\}/);
  expect(appSource).toContain('AppearanceProvider');
  expect(appSource).not.toMatch(/overlay(?:Theme)?(?:Store|Storage|Persistence)/i);
  expect(appSource).not.toMatch(/overlayRoot\s*(?:\|\||\?\?)\s*document\.body/);
});

it('removes only its own theme projection and overwrites stale values on remount', async () => {
  let view = render(<App skipBootstrap />);
  await screen.findByTestId('frontend-app');
  expect(appOverlayHost).toHaveAttribute('data-theme', 'light');

  view.unmount();
  expect(appOverlayHost).not.toHaveAttribute('data-theme');

  appOverlayHost.setAttribute('data-theme', 'stale');
  view = render(<App skipBootstrap />);
  await screen.findByTestId('frontend-app');
  expect(appOverlayHost).toHaveAttribute('data-theme', 'light');

  appOverlayHost.setAttribute('data-theme', 'external');
  view.unmount();
  expect(appOverlayHost).toHaveAttribute('data-theme', 'external');

  appOverlayHost.setAttribute('data-theme', 'stale');
  view = render(<App skipBootstrap />);
  await screen.findByTestId('frontend-app');
  expect(appOverlayHost).toHaveAttribute('data-theme', 'light');
  view.unmount();
  expect(appOverlayHost).not.toHaveAttribute('data-theme');
});

it.each(['missing', 'duplicate'])('contains a %s overlay-root failure in the existing app boundary', async (mode) => {
  if (mode === 'missing') {
    appOverlayHost.remove();
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

it('keeps the shell store above ChatPage and routes one geometry snapshot with layout actions', () => {
  expect(appSource).toContain('createShellLayoutStore');
  expect(appSource).toContain('shellLayoutStorage');
  expect(appSource).toContain('shellLayoutStore');
  expect(appRoutesSource).toMatch(/<ChatPage[\s\S]{0,320}geometrySnapshot=\{geometrySnapshot\}/);
  expect(appRoutesSource).toMatch(/<ChatPage[\s\S]{0,320}layoutActions=\{layoutActions\}/);
  expect(appRoutesSource).not.toContain('shellLayoutStore');
  expect(chatPageSource).toContain('ChatPage requires one geometry snapshot and layout actions');
  expect(chatPageSource).not.toContain('useShellLayoutStore');
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
  const chatLayout = await screen.findByTestId('chat-layout', {}, { timeout: 20_000 });
  fireEvent.click(await screen.findByRole('button', { name: '显示侧边栏' }, { timeout: 20_000 }));

  expect(chatLayout).toHaveStyle({
    gridTemplateColumns: 'minmax(0, 1fr) 6px 480.5px',
  });
  expect(storage.set).not.toHaveBeenCalled();
}, 30000);

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
    const appbar = screen.getByLabelText('Super Dolphin Agent app bar');
    expect(shell).toHaveAttribute('data-theme', 'light');
    expect(document.querySelector('.traffic-lights')).not.toBeInTheDocument();
    expect(document.querySelector('.titlebar')).not.toBeInTheDocument();
    expect(within(sidebar).getByText('Super Dolphin Agent')).toBeInTheDocument();
    expect(within(sidebar).getByRole('button', { name: '新对话' })).toHaveTextContent('新对话');
    expect(within(sidebar).getByRole('button', { name: '设置' })).toHaveTextContent('设置');
    expect(within(sidebar).getByRole('button', { name: '聊天页面' })).toHaveTextContent('聊天页面');
    expect(within(sidebar).getByRole('button', { name: '插件与技能' })).toHaveTextContent('插件与技能');
    expect(within(appbar).getByRole('button', { name: '通知' })).toBeInTheDocument();
    expect(within(appbar).getByRole('button', { name: '历史记录' })).toBeInTheDocument();
    expect(document.querySelector('.super-dolphin-agent-appbar-title')).not.toBeInTheDocument();
    expect(screen.queryByText('Overview')).not.toBeInTheDocument();
    expect(screen.queryByText('Usage')).not.toBeInTheDocument();
    expect(screen.queryByText('Limits')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Upgrade Plan' })).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '切换到 English' }));
    expect(within(sidebar).getByRole('button', { name: 'New chat' })).toHaveTextContent('New chat');
    expect(within(sidebar).getByRole('button', { name: 'Chat' })).toHaveTextContent('Chat');
    expect(within(appbar).getByRole('button', { name: 'Notifications' })).toBeInTheDocument();
    expect(within(appbar).getByRole('button', { name: 'History' })).toBeInTheDocument();
    expect(within(sidebar).getByRole('button', { name: 'Chat' })).toHaveClass('active');
    fireEvent.click(screen.getByRole('button', { name: 'Switch to 中文' }));
    expect(within(sidebar).getByRole('button', { name: '新对话' })).toBeInTheDocument();
    expect(within(sidebar).getByRole('button', { name: '聊天页面' })).toHaveClass('active');
    expect(within(sidebar).getByRole('separator', { name: '调整工作台侧栏宽度' })).toBeInTheDocument();
  });

  it('fails fast when required browser storage is unavailable', () => {
    const originalStorage = window.localStorage;
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {});
    Object.defineProperty(window, 'localStorage', { configurable: true, value: {} });
    try {
      expect(() => render(<App />)).toThrow(/appearance storage is unavailable/);
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
    expect(within(screen.getByTestId('app-sidebar')).getByRole('button', { name: '设置' })).toHaveClass('active');
    expect(shell).not.toHaveClass('sidebar-open');
  });

  it('uses the custom brand icon only in the sidebar brand area', async () => {
    render(<App />);

    const sidebar = await screen.findByTestId('app-sidebar');
    expect(within(sidebar).getByTestId('super-dolphin-agent-brand-dark-logo')).toHaveAttribute(
      'src',
      expect.stringContaining('super-dolphin-agent-brand-icon.png'),
    );
    expect(within(sidebar).getByTestId('super-dolphin-agent-brand-light-logo')).toBeInTheDocument();
    expect(sidebar.querySelector('.sidebar-tree-folder img')).toBeNull();
    expect(sidebar.querySelector('.super-dolphin-agent-nav-item svg')).toBeInTheDocument();
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
    const nav = within(sidebar).getByRole('navigation', { name: 'Super Dolphin Agent navigation' });

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
    expect(screen.queryByText('我们应该在 Super Dolphin Agent 中构建什么？')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '新对话' }));

    await screen.findByText('我们应该在 Super Dolphin Agent 中构建什么？');
    expect(within(screen.getByTestId('app-sidebar')).getByRole('button', { name: '聊天页面' })).toHaveClass('active');
    expect(within(screen.getByTestId('app-sidebar')).getByRole('button', { name: '添加项目目录' })).toBeInTheDocument();
    expect(screen.getByTestId('composer-input')).toHaveValue('');
  });

  it('dispatches the real new-chat, settings, sidebar, and palette commands from the app window', async () => {
    render(<App />);

    await waitForBackendThreadHeading();
    fireEvent.keyDown(window, { key: 'n', ctrlKey: true });
    await screen.findByText('我们应该在 Super Dolphin Agent 中构建什么？');

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
    expect(screen.queryByText('我们应该在 Super Dolphin Agent 中构建什么？')).not.toBeInTheDocument();
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
    expect(screen.queryByText('我们应该在 Super Dolphin Agent 中构建什么？')).not.toBeInTheDocument();
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
    await screen.findByText('我们应该在 Super Dolphin Agent 中构建什么？');
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
    expect(screen.queryByText('我们应该在 Super Dolphin Agent 中构建什么？')).not.toBeInTheDocument();
    fireEvent.keyDown(window, { key: 'm', ctrlKey: true });
    await screen.findByText('我们应该在 Super Dolphin Agent 中构建什么？');
  });

  it('shows an app update banner after the background check finds a new version', async () => {
    vi.useFakeTimers();
    backend.checkAppUpdate.mockResolvedValueOnce({ enabled: true, available: true, version: '0.1.1' });

    render(<App />);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(2100);
    });

    const banner = screen.getByTestId('app-update-banner');
    expect(banner).toHaveTextContent('发现新版本 0.1.1');
    expect(banner).toHaveTextContent('建议更新到最新版');
    expect(backend.checkAppUpdate).toHaveBeenCalledTimes(1);
  });

  it('shows a fixed recovery banner when the background update signature check fails', async () => {
    vi.useFakeTimers();
    const secret = 'codesign output /Applications/Super Dolphin.app';
    const failure = new Error(secret);
    failure.data = {
      code: 'UPDATE_SIGNATURE_INVALID',
      retryable: false,
      action: 'preserve_state_export_diagnostics',
      transaction_id: '',
    };
    backend.checkAppUpdate.mockRejectedValueOnce(failure);

    render(<App />);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(2100);
    });

    const banner = screen.getByTestId('app-update-banner');
    expect(banner).toHaveTextContent('更新完整性校验失败，请保持现场并导出诊断信息。');
    expect(banner).not.toHaveTextContent(secret);
    expect(screen.queryByRole('button', { name: '立即更新' })).not.toBeInTheDocument();
  });

  it('starts installing the latest update from the main update banner', async () => {
    vi.useFakeTimers();
    backend.checkAppUpdate.mockResolvedValueOnce({ enabled: true, available: true, version: '0.1.1' });
    backend.installLatestAppUpdate.mockResolvedValueOnce({ started: true, helper: 'updater' });

    render(<App />);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(2100);
    });

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: '立即更新' }));
      await Promise.resolve();
    });
    expect(backend.installLatestAppUpdate).toHaveBeenCalledTimes(1);
    expect(screen.getByText('安装程序已启动，请按提示完成更新。')).toBeInTheDocument();
  });

  it('redacts typed integrity details when update installation fails', async () => {
    vi.useFakeTimers();
    backend.checkAppUpdate.mockResolvedValueOnce({ enabled: true, available: true, version: '0.1.1' });
    const secret = 'codesign output /Applications/Super Dolphin.app';
    const failure = new Error(secret);
    failure.data = {
      code: 'UPDATE_SIGNATURE_INVALID',
      retryable: false,
      action: 'preserve_state_export_diagnostics',
      transaction_id: '',
    };
    backend.installLatestAppUpdate.mockRejectedValueOnce(failure);

    render(<App />);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(2100);
    });
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: '立即更新' }));
      await Promise.resolve();
    });

    const banner = screen.getByTestId('app-update-banner');
    expect(banner).toHaveTextContent('更新完整性校验失败，请保持现场并导出诊断信息。');
    expect(banner).not.toHaveTextContent(secret);
  });

  describe('global appearance switching behavior', () => {
    beforeEach(() => {
      window.localStorage.clear();
      document.documentElement.removeAttribute('data-theme');
      document.body.removeAttribute('data-theme');
    });

    afterEach(() => {
      window.localStorage.clear();
    });

    it('initializes system/100/violet and projects every global attribute', async () => {
      render(<App />);
      const shell = await screen.findByTestId('frontend-app');
      expect(shell).toHaveAttribute('data-theme-mode', 'system');
      expect(shell).toHaveAttribute('data-ui-scale', '100');
      expect(shell).toHaveAttribute('data-accent', 'violet');
      expect(document.documentElement).toHaveAttribute('data-theme', 'light');
      expect(appOverlayHost).toHaveAttribute('data-accent', 'violet');
      expect(window.localStorage.getItem('super-dolphin.appearance')).toContain('"version":1');
    });

    it('toggles the single global owner without backend preferences', async () => {
      render(<App />);
      const preferenceCallsBeforeToggle = backend.setPreference.mock.calls.length;
      fireEvent.click(await screen.findByRole('button', { name: '切换到黑夜模式' }));
      expect(document.documentElement).toHaveAttribute('data-theme', 'dark');
      expect(appOverlayHost).toHaveAttribute('data-theme', 'dark');
      expect(window.localStorage.getItem('super-dolphin.appearance')).toContain('"themeMode":"dark"');
      expect(backend.setPreference.mock.calls.length).toBe(preferenceCallsBeforeToggle);
    });

    it('migrates the valid legacy dark value into the versioned owner once', async () => {
      window.localStorage.setItem('super-dolphin-theme', 'dark');
      render(<App />);
      const shell = await screen.findByTestId('frontend-app');
      const stored = window.localStorage.getItem('super-dolphin.appearance');
      expect(shell).toHaveAttribute('data-theme', 'dark');
      expect(shell).toHaveAttribute('data-theme-mode', 'dark');
      expect(document.documentElement).toHaveAttribute('data-theme', 'dark');
      expect(document.body).toHaveAttribute('data-theme', 'dark');
      expect(appOverlayHost).toHaveAttribute('data-theme', 'dark');
      expect(stored).toContain('"version":1');
      expect(stored).toContain('"themeMode":"dark"');
      expect(stored).toContain('"uiScale":100');
      expect(stored).toContain('"accent":"violet"');
      expect(window.localStorage.getItem('super-dolphin-theme')).toBeNull();
    });

    it('loads persisted scale and accent into every root projection target', async () => {
      window.localStorage.setItem(
        'super-dolphin.appearance',
        JSON.stringify({
          version: 1,
          settings: {
            themeMode: 'light',
            uiScale: 125,
            accent: 'mint',
          },
        }),
      );
      render(<App />);
      const shell = await screen.findByTestId('frontend-app');
      expect(shell).toHaveAttribute('data-theme', 'light');
      expect(shell).toHaveAttribute('data-theme-mode', 'light');
      expect(shell).toHaveAttribute('data-ui-scale', '125');
      expect(shell).toHaveAttribute('data-accent', 'mint');
      expect(shell.style.getPropertyValue('--ui-scale')).toBe('1.25');
      expect(document.documentElement).toHaveAttribute('data-ui-scale', '125');
      expect(document.documentElement).toHaveAttribute('data-accent', 'mint');
      expect(document.body).toHaveAttribute('data-ui-scale', '125');
      expect(document.body).toHaveAttribute('data-accent', 'mint');
      expect(appOverlayHost).toHaveAttribute('data-ui-scale', '125');
      expect(appOverlayHost).toHaveAttribute('data-accent', 'mint');
    });

    it('resolves system dark mode without replacing the stored system preference', async () => {
      window.matchMedia.mockReturnValue({
        matches: true,
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
      });
      render(<App />);
      const shell = await screen.findByTestId('frontend-app');
      const stored = window.localStorage.getItem('super-dolphin.appearance');
      expect(window.matchMedia).toHaveBeenCalledWith('(prefers-color-scheme: dark)');
      expect(shell).toHaveAttribute('data-theme', 'dark');
      expect(shell).toHaveAttribute('data-theme-mode', 'system');
      expect(document.documentElement).toHaveAttribute('data-theme', 'dark');
      expect(document.documentElement).toHaveAttribute('data-theme-mode', 'system');
      expect(document.body).toHaveAttribute('data-theme', 'dark');
      expect(document.body).toHaveAttribute('data-theme-mode', 'system');
      expect(appOverlayHost).toHaveAttribute('data-theme', 'dark');
      expect(appOverlayHost).toHaveAttribute('data-theme-mode', 'system');
      expect(stored).toContain('"themeMode":"system"');
    });

    it('keeps the status surface aligned with the single appearance owner', async () => {
      window.localStorage.setItem(
        'super-dolphin.appearance',
        JSON.stringify({
          version: 1,
          settings: { themeMode: 'dark', uiScale: 150, accent: 'rose' },
        }),
      );
      render(<App />);
      const shell = await screen.findByTestId('frontend-app');
      const status = await screen.findByLabelText('工作台状态');
      expect(shell).toHaveAttribute('data-theme', 'dark');
      expect(shell).toHaveAttribute('data-ui-scale', '150');
      expect(shell).toHaveAttribute('data-accent', 'rose');
      expect(status).toHaveTextContent('dark');
      expect(status).toHaveTextContent('150%');
      expect(status).toHaveTextContent('rose');
      expect(status).toHaveTextContent('chat');
      expect(status).toHaveTextContent('/repo/app');
    });
  });
