import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import App from './App.jsx';
import { resetClientStoreForTests } from './entities/client/model/useClientStore.js';
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
    listObservabilityErrors listSharedFiles
    readBuiltinTools writeBuiltinTool listDashboardLogs
    listDags getDagDetail getDagRuns getDagRun startDag terminateDagRun deleteDag applyDagOps listWorkflowTemplates getWorkflowTemplate renderWorkflowTemplateDraft
    listCronJobs getCronJob createCronJob updateCronJob deleteCronJob runCronJobOnce setCronJobEnabled listCronJobRuns
    selectProjectDir selectProjectDirs
    listMCPServers listToolbridgeTools startSQLiteMCPServer stopSQLiteMCPServer startPlaywrightMCPServer stopPlaywrightMCPServer
    readSharedFile getPreference
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
  installAppOverlayHost,
  resetConnectedShellTestState,
  mockBootstrapBackendDefaults,
  mockDashboardPageDefaults,
  mockObservabilityDefaults,
  mockWorkflowDefaults,
  mockCronDefaults,
  mockSharedFileDefaults,
  mockSettingsAndThreadDefaults,
  resetFrontendHealthForTest,
  cleanupAppTest,
} = appTestSupport;

beforeEach(installAppOverlayHost);
beforeEach(resetConnectedShellTestState);
beforeEach(mockBootstrapBackendDefaults);
beforeEach(mockDashboardPageDefaults);
beforeEach(mockObservabilityDefaults);
beforeEach(mockWorkflowDefaults);
beforeEach(mockCronDefaults);
beforeEach(mockSharedFileDefaults);
beforeEach(mockSettingsAndThreadDefaults);
beforeEach(resetFrontendHealthForTest);
afterEach(cleanupAppTest);

it('navigates to screenshot-style secondary pages', async () => {
  render(<App />);
  expect(screen.queryByLabelText('命令')).not.toBeInTheDocument();
  expect(screen.queryByLabelText('任务')).not.toBeInTheDocument();

  fireEvent.click(screen.getByLabelText('共享文件'));
  expect(await screen.findByText('文件产物')).toBeInTheDocument();
  await waitFor(() => {
    expect(backend.listSharedFiles).toHaveBeenCalledWith();
  });
});

it('keeps the automation route visible while project context resolves', async () => {
  const config = deferred();
  backend.readConfig.mockReturnValueOnce(config.promise);

  render(<App />);
  fireEvent.click(screen.getByLabelText('自动化'));

  await waitFor(() => expect(screen.getByLabelText('自动化')).toHaveClass('active'));
  expect(backend.getDashboardPage).not.toHaveBeenCalledWith({ cwd: '未选择项目', page: 'dags' });

  await act(async () => {
    config.resolve({ cwd: '/repo/app' });
    await Promise.resolve();
    await Promise.resolve();
  });

  expect(await screen.findByText('创建首个自动化')).toBeInTheDocument();
});

it('loads global shared files while project context resolves', async () => {
  const config = deferred();
  backend.readConfig.mockReturnValueOnce(config.promise);

  render(<App />);
  fireEvent.click(screen.getByLabelText('共享文件'));

  await waitFor(() => expect(screen.getByLabelText('共享文件')).toHaveClass('active'));
  expect(screen.queryByText('正在连接本地项目...')).not.toBeInTheDocument();
  await waitFor(() => {
    expect(backend.listSharedFiles).toHaveBeenCalledWith();
  });
  expect(backend.listSharedFiles).not.toHaveBeenCalledWith({ cwd: '未选择项目' });

  await act(async () => {
    config.resolve({ cwd: '/repo/app' });
    await Promise.resolve();
    await Promise.resolve();
  });
  expect(await screen.findByText('还没有文件产物')).toBeInTheDocument();
});
