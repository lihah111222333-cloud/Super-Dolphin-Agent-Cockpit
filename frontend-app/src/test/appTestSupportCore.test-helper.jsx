import { act, fireEvent, screen, within } from '@testing-library/react';
import { expect, vi } from 'vitest';

let backend;
let resetClientStoreForTests;
let state;
function dispatchPointer(target, type, clientX = 0, options = {}) {
  const defaultButtons = type === 'pointerup' ? 0 : 1;
  act(() => {
    const event = new MouseEvent(type, {
      bubbles: true,
      clientX,
      clientY: options.clientY ?? 0,
      buttons: options.buttons ?? defaultButtons,
    });
    Object.defineProperty(event, 'pointerId', {
      configurable: true,
      value: options.pointerId ?? 1,
    });
    target.dispatchEvent(event);
  });
}

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((promiseResolve, promiseReject) => {
    resolve = promiseResolve;
    reject = promiseReject;
  });
  return { promise, resolve, reject };
}

function formatParsedTimestampForTest(value) {
  const parsed = new Date(value);
  const year = String(parsed.getFullYear()).padStart(4, '0');
  const month = String(parsed.getMonth() + 1).padStart(2, '0');
  const day = String(parsed.getDate()).padStart(2, '0');
  const hour = String(parsed.getHours()).padStart(2, '0');
  const minute = String(parsed.getMinutes()).padStart(2, '0');
  const second = String(parsed.getSeconds()).padStart(2, '0');
  return `${year}-${month}-${day} ${hour}:${minute}:${second}`;
}

function decodedSvgDataUrl(image) {
  const src = image.getAttribute('src') || '';
  const prefix = 'data:image/svg+xml;charset=utf-8,';
  expect(src.startsWith(prefix)).toBe(true);
  return decodeURIComponent(src.slice(prefix.length));
}

function waitForBackendThreadHeading() {
  return screen.findByRole('heading', { name: '后端线程' });
}

function mockShortcutPreferenceLoad(loadShortcutPreference) {
  backend.getPreference.mockImplementation(({ key }) => {
    if (key === 'settings.shortcuts.bindings') return loadShortcutPreference();
    return Promise.resolve(appPreferenceDefaults[key] ?? null);
  });
}

function getSidebarNavButton(name) {
  return within(screen.getByTestId('sidebar-nav')).getByRole('button', { name });
}

function getBackendThreadText() {
  return screen.getAllByText('后端线程')[0];
}

function getThreadCardByName(name) {
  const card = screen.getAllByText(name)
    .map((node) => node.closest('.sidebar-thread-row'))
    .find(Boolean);
  if (!card) throw new Error(`Thread card not found: ${name}`);
  return card;
}

function clickThreadCardByName(name) {
  const button = getThreadCardByName(name).querySelector('.sidebar-thread-item');
  if (!button) throw new Error(`Thread card button not found: ${name}`);
  fireEvent.click(button);
}

function queryThreadCardByName(name) {
  return screen.queryAllByText(name)
    .map((node) => node.closest('.sidebar-thread-row'))
    .find(Boolean) ?? null;
}

async function findThreadCardByName(name) {
  await screen.findAllByText(name);
  return getThreadCardByName(name);
}

function resetConnectedShellTestState() {
  vi.clearAllMocks();
  state.bridgeCallback = null;
  resetClientStoreForTests();
  window.localStorage.clear();
  window.history.replaceState({}, '', '/');
  Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1024 });
}

function installAppOverlayHost() {
  document.querySelectorAll('#overlay-root').forEach((node) => node.remove());
  state.appOverlayHost = document.createElement('div');
  state.appOverlayHost.id = 'overlay-root';
  document.body.append(state.appOverlayHost);
}

function createShellLayoutStorage(initialValue = null) {
  let storedValue = initialValue;
  return {
    get: vi.fn(() => storedValue),
    set: vi.fn((_key, value) => { storedValue = value; }),
    remove: vi.fn(() => { storedValue = null; }),
    value: () => storedValue,
  };
}

function mockBootstrapBackendDefaults() {
  backend.readConfig.mockResolvedValue({ cwd: '/repo/app' });
  backend.getWindowBootstrap.mockResolvedValue({ snapshot: null });
  backend.openNewWindow.mockResolvedValue({ ok: true });
  backend.getProjects.mockResolvedValue({ projects: ['/repo/app'], active: '/repo/app' });
  backend.setActiveProject.mockResolvedValue({ projects: ['/repo/app'], active: '/repo/app' });
  backend.addProject.mockResolvedValue({ projects: ['/repo/app'], active: '/repo/app' });
  backend.removeProject.mockResolvedValue({ projects: ['/repo/app'], active: '/repo/app' });
  backend.getSidebarState.mockResolvedValue({
    activeThreadId: 'thread-1',
    threads: [{ id: 'thread-1', name: '后端线程', provider: 'codex', status: '工作中' }],
    active_turn: { id: 'turn-1', thread_id: 'thread-1', status: 'running' },
    tokenUsageByThread: {
      'thread-1': { usedTokens: 128, contextWindowTokens: 1024, usedPercent: 12.5 },
    },
    activityStatsByThread: {
      'thread-1': {
        lspCalls: 3,
        commands: 4,
        fileEdits: 2,
        toolCalls: { edit: 3, json_render: 1, shell: 2 },
      },
    },
  });
  backend.getThreadState.mockResolvedValue({
    activeThreadId: 'thread-1',
    timelinesByThread: {
      'thread-1': [{ id: 'assistant-1', kind: 'assistant', text: '来自后端的消息', ts: '2026-05-30T00:00:00Z' }],
    },
    diffTextByThread: {
      'thread-1': 'diff --git a/file b/file',
    },
  });
  backend.getThreadMessages.mockResolvedValue({ messages: [] });
  backend.callBackend.mockResolvedValue({});
  backend.checkAppUpdate.mockResolvedValue({ enabled: true, available: false });
  backend.installLatestAppUpdate.mockResolvedValue({ started: true, helper: '/tmp/updater' });
  backend.getVideoApiKey.mockResolvedValue({ configured: false, masked: '' });
  backend.setVideoApiKey.mockResolvedValue({ ok: true });
}

function mockDashboardPageDefaults() {
  backend.getDashboardPage.mockImplementation(({ page }) => {
    if (page === 'dags') {
      return Promise.resolve({ dags: [] });
    }
    return Promise.resolve({});
  });
}

function mockObservabilityDefaults() {
  const emptyTraceResult = {
    source: 'buffer',
    events: [],
    slowest_events: [],
    errors: [],
    total_duration_ms: 0,
    truncated: false,
  };
  backend.getObservabilityStatus.mockResolvedValue({
    enabled: true,
    schema_version: 1,
    index_trace_keys: 1,
    sink_events_written: 2,
    sink_write_errors: 0,
  });
  backend.getObservabilityTrace.mockResolvedValue(emptyTraceResult);
  backend.getObservabilityThreadRecent.mockResolvedValue(emptyTraceResult);
  backend.listObservabilityRecent.mockResolvedValue(emptyTraceResult);
  backend.listObservabilitySlow.mockResolvedValue(emptyTraceResult);
  backend.listObservabilityErrors.mockResolvedValue(emptyTraceResult);
}

function mockWorkflowDefaults() {
  backend.listDags.mockResolvedValue({ dags: [] });
  backend.getDagDetail.mockResolvedValue({ dag: null, nodes: [] });
  backend.getDagRuns.mockResolvedValue({ runs: [] });
  backend.getDagRun.mockResolvedValue({ run: null, nodes: [] });
  backend.startDag.mockResolvedValue({ runKey: 'run-started' });
  backend.terminateDagRun.mockResolvedValue({ ok: true });
  backend.deleteDag.mockResolvedValue({ ok: true });
  backend.applyDagOps.mockResolvedValue({ newVersion: 2 });
  backend.listWorkflowTemplates.mockResolvedValue({ templates: [] });
  backend.getWorkflowTemplate.mockResolvedValue({ template: null });
  backend.renderWorkflowTemplateDraft.mockResolvedValue({ draft: null });
}

function mockCronDefaults() {
  backend.listCronJobs.mockResolvedValue({ jobs: [] });
  backend.getCronJob.mockResolvedValue({ id: 'cron-1' });
  backend.createCronJob.mockResolvedValue({ id: 'cron-created' });
  backend.updateCronJob.mockResolvedValue({ id: 'cron-1' });
  backend.deleteCronJob.mockResolvedValue({ ok: true });
  backend.runCronJobOnce.mockResolvedValue({ id: 'cron-1' });
  backend.setCronJobEnabled.mockResolvedValue({ id: 'cron-1', enabled: true });
  backend.listCronJobRuns.mockResolvedValue({ runs: [] });
}

function mockSharedFileDefaults() {
  backend.listSharedFiles.mockResolvedValue({
    files: [],
    finalOutputRefs: [],
    sharedFileRetention: { items: [], protectedCount: 0, cleanupCandidateCount: 0 },
  });
  backend.readSharedFile.mockImplementation(({ path }) => Promise.resolve({
    path,
    content: `content for ${path}`,
    updatedBy: 'agent',
    updatedAt: '2026-05-30T07:00:00Z',
  }));
  backend.saveTextFile.mockResolvedValue('/exports/file.md');
}

function mockSettingsAndThreadDefaults() {
  backend.onFilesDropped.mockReturnValue(() => {});
  backend.beginTextClipboardWrite.mockReturnValue(null);
  backend.copyTextToClipboard.mockResolvedValue(true);
  backend.resolveThreadIdentity.mockImplementation(({ threadId }) => Promise.resolve({
    id: threadId, provider: 'codex', cwd: '/repo/app',
  }));
  backend.readBuiltinTools.mockResolvedValue({ tools: [] });
  backend.writeBuiltinTool.mockResolvedValue({ tools: [] });
  backend.listToolbridgeTools.mockResolvedValue({ tools: [] });
  backend.listDashboardLogs.mockResolvedValue({ logs: [] });
  backend.getBuildInfo.mockResolvedValue({
    version: 'v1.2.3',
    runtime: 'linux/amd64',
    buildTime: '2026-05-30T07:00:00Z',
    commit: 'abc123def456',
  });
  backend.getPreference.mockImplementation(({ key }) => Promise.resolve({
    'settings.provider.active': 'codex',
    'settings.provider.codex.model': 'gpt-5.5',
    'settings.provider.codex.effort': 'xhigh',
    'settings.provider.codex.codexHome': '~/.codex',
    'settings.provider.codex.codexInstanceKey': 'default',
    'settings.provider.codex.codexModelProvider': 'openai',
    'settings.provider.claude.model': 'sonnet',
    'settings.provider.claude.effort': 'high',
  }[key] ?? null));
  backend.archiveThread.mockResolvedValue({ ok: true });
  backend.unarchiveThread.mockResolvedValue({ ok: true });
  backend.deleteThread.mockResolvedValue({ ok: true });
  backend.getThreadConfig.mockResolvedValue({
    threadId: 'thread-1',
    provider: 'codex',
    supportsThreadOverride: true,
    availableModels: ['gpt-5.5', 'gpt-5.4'],
    override: {},
    effective: { model: 'gpt-5.4', effort: 'medium' },
  });
  backend.setThreadConfig.mockResolvedValue({
    threadId: 'thread-1',
    provider: 'codex',
    supportsThreadOverride: true,
    availableModels: ['gpt-5.5', 'gpt-5.4'],
    override: { model: 'gpt-5.5', effort: '' },
    effective: { model: 'gpt-5.5', effort: 'medium' },
  });
  backend.setPreference.mockResolvedValue({ ok: true });
  backend.locateCodeFile.mockResolvedValue({
    ok: true,
    paths: ['/repo/app/src/a.js'],
    matches: [{ path: '/repo/app/src/a.js', relative: 'src/a.js', totalLines: 2 }],
  });
  backend.openCodeFile.mockResolvedValue({
    ok: true,
    filePath: '/repo/app/src/a.js',
    relative: 'src/a.js',
    startLine: 1,
    endLine: 2,
    totalLines: 2,
    previewMode: 'full',
    contentVersion: 'version-src-a',
    snippet: [
      { line: 1, text: 'old' },
      { line: 2, text: 'keep' },
    ],
  });
  backend.saveCodeFile.mockResolvedValue({
    ok: true,
    filePath: '/repo/app/src/a.js',
    relative: 'src/a.js',
    totalLines: 2,
    contentVersion: 'version-src-a-saved',
  });
}

const appPreferenceDefaults = Object.freeze({
  'settings.provider.active': 'codex',
  'settings.provider.codex.model': 'gpt-5.5',
  'settings.provider.codex.effort': 'xhigh',
  'settings.provider.codex.codexHome': '~/.codex',
  'settings.provider.codex.codexInstanceKey': 'default',
  'settings.provider.codex.codexModelProvider': 'openai',
  'settings.provider.claude.model': 'sonnet',
  'settings.provider.claude.effort': 'high',
});

export function createAppTestCore(nextContext) {
  backend = nextContext.backend;
  resetClientStoreForTests = nextContext.resetClientStoreForTests;
  state = nextContext.state;
  return {
    dispatchPointer: (...args) => dispatchPointer(...args),
    deferred: (...args) => deferred(...args),
    formatParsedTimestampForTest: (...args) => formatParsedTimestampForTest(...args),
    decodedSvgDataUrl: (...args) => decodedSvgDataUrl(...args),
    waitForBackendThreadHeading: (...args) => waitForBackendThreadHeading(...args),
    mockShortcutPreferenceLoad: (...args) => mockShortcutPreferenceLoad(...args),
    getSidebarNavButton: (...args) => getSidebarNavButton(...args),
    getBackendThreadText: (...args) => getBackendThreadText(...args),
    getThreadCardByName: (...args) => getThreadCardByName(...args),
    clickThreadCardByName: (...args) => clickThreadCardByName(...args),
    queryThreadCardByName: (...args) => queryThreadCardByName(...args),
    findThreadCardByName: (...args) => findThreadCardByName(...args),
    resetConnectedShellTestState: (...args) => resetConnectedShellTestState(...args),
    installAppOverlayHost: (...args) => installAppOverlayHost(...args),
    createShellLayoutStorage: (...args) => createShellLayoutStorage(...args),
    mockBootstrapBackendDefaults: (...args) => mockBootstrapBackendDefaults(...args),
    mockDashboardPageDefaults: (...args) => mockDashboardPageDefaults(...args),
    mockObservabilityDefaults: (...args) => mockObservabilityDefaults(...args),
    mockWorkflowDefaults: (...args) => mockWorkflowDefaults(...args),
    mockCronDefaults: (...args) => mockCronDefaults(...args),
    mockSharedFileDefaults: (...args) => mockSharedFileDefaults(...args),
    mockSettingsAndThreadDefaults: (...args) => mockSettingsAndThreadDefaults(...args),
    appPreferenceDefaults: (...args) => appPreferenceDefaults(...args),
  };
}
