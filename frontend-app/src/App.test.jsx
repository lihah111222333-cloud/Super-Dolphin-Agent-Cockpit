import React from 'react';
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import App from './App.jsx';
import { getStoredTheme, syncThemeDOM } from './app/appShellModel.js';
import appSource from './App.jsx?raw';
import appRoutesSource from './AppRoutes.jsx?raw';
import { AppErrorBoundary } from './app/AppErrorBoundary.jsx';
import { rightPanelWidthSchema } from './app/shell/model/shellLayoutSchema.js';
import chatPageSource from './pages/chat/ChatPage.jsx?raw';
import chatWorkbenchLayoutSource from './pages/chat/hooks/useChatWorkbenchLayout.js?raw';
import { rightPanelDefaultWidth, rightPanelMaxWidth, threadRailTargetWidth } from './pages/chat/model/chatWorkbenchLayoutModel.js';
import { resetClientStoreForTests, useClientStore } from './entities/client/model/useClientStore.js';
import { frontendHealthSnapshot, resetFrontendHealthForTest } from './shared/diagnostics/frontendHealthStore.js';
import { normalizeMemorySnapshot as normalizeMemorySnapshotForFacade } from './adapters/memoryAdapter.js';
import mermaid from 'mermaid';

let createRootMock = null;
vi.mock('react-dom/client', async (importOriginal) => {
  const original = await importOriginal();
  return {
    ...original,
    createRoot: (...args) => {
      if (createRootMock) return createRootMock(...args);
      return original.createRoot(...args);
    },
  };
});

let syncThemeDOMMock = null;
let getStoredThemeMock = null;
vi.mock('./app/appShellModel.js', async (importOriginal) => {
  const original = await importOriginal();
  return {
    ...original,
    syncThemeDOM: (...args) => {
      if (syncThemeDOMMock) return syncThemeDOMMock(...args);
      return original.syncThemeDOM(...args);
    },
    getStoredTheme: (...args) => {
      if (getStoredThemeMock) return getStoredThemeMock(...args);
      return original.getStoredTheme(...args);
    },
  };
});

let bridgeCallback;
let appOverlayHost;

function dispatchPointer(target, type, clientX = 0, options = {}) {
  const defaultButtons = type === 'pointerup' ? 0 : 1;
  act(() => {
    target.dispatchEvent(new MouseEvent(type, {
      bubbles: true,
      clientX,
      buttons: options.buttons ?? defaultButtons,
    }));
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
        bridgeCallback = null;
      };
    }),
  };
});

vi.mock('./shared/api/backendApi.js', () => ({
  ...backend,
  registerBridgeLogStore: vi.fn(),
  sendFrontendLogBatch: vi.fn(),
}));

function promptPreferenceValue(key, activePromptKey = '') {
  return {
    'settings.provider.active': 'codex',
    'settings.provider.codex.model': 'gpt-5.5',
    'settings.provider.codex.effort': 'xhigh',
    'settings.provider.codex.codexHome': '~/.codex',
    'settings.provider.codex.codexInstanceKey': 'default',
    'settings.provider.codex.codexModelProvider': 'openai',
    'settings.provider.claude.model': 'sonnet',
    'settings.provider.claude.effort': 'high',
    'settings.activePromptKey': activePromptKey,
  }[key] ?? null;
}

function mockPromptPreferences(activePromptKey = '') {
  backend.getPreference.mockImplementation(({ key }) => Promise.resolve(promptPreferenceValue(key, activePromptKey)));
}

vi.mock('mermaid', () => ({
  default: {
    initialize: vi.fn(),
    render: vi.fn((_id, source) => Promise.resolve({
      svg: `<svg role="img" aria-label="mock mermaid"><text>${source}</text></svg>`,
    })),
  },
}));

function decodedSvgDataUrl(image) {
  const src = image.getAttribute('src') || '';
  const prefix = 'data:image/svg+xml;charset=utf-8,';
  expect(src.startsWith(prefix)).toBe(true);
  return decodeURIComponent(src.slice(prefix.length));
}

function waitForBackendThreadHeading() {
  return screen.findByRole('heading', { name: '后端线程' });
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

function mockShortcutPreferenceLoad(loadShortcutPreference) {
  backend.getPreference.mockImplementation(({ key }) => {
    if (key === 'settings.shortcuts.bindings') return loadShortcutPreference();
    return Promise.resolve(appPreferenceDefaults[key] ?? null);
  });
}

function openPluginsAndSkillsPage() {
  fireEvent.click(screen.getByLabelText('插件与技能'));
}

function getSidebarNavButton(name) {
  return within(screen.getByTestId('sidebar-nav')).getByRole('button', { name });
}

function getBackendThreadText() {
  return screen.getAllByText('后端线程')[0];
}

function getThreadCardByName(name) {
  const card = screen.getAllByText(name)
    .map((node) => node.closest('.thread-card'))
    .find(Boolean);
  if (!card) throw new Error(`Thread card not found: ${name}`);
  return card;
}

function clickThreadCardByName(name) {
  const button = getThreadCardByName(name).querySelector('.thread-main');
  if (!button) throw new Error(`Thread card button not found: ${name}`);
  fireEvent.click(button);
}

function queryThreadCardByName(name) {
  return screen.queryAllByText(name)
    .map((node) => node.closest('.thread-card'))
    .find(Boolean) ?? null;
}

async function findThreadCardByName(name) {
  await screen.findAllByText(name);
  return getThreadCardByName(name);
}

function defaultSkillFixtures() {
  return [
    {
      name: 'backend',
      display_name: '后端',
      dir: '/repo/app/.agents/skills/backend',
      description: '当你需要 Go 后端开发时使用。',
      summary: 'Go 后端开发指南',
      trigger_words: ['Go', 'backend', 'service'],
      force_words: ['sqlc'],
      scope: 'project',
    },
    {
      name: 'personal-review',
      dir: '/Users/test/.super-dolphin/skills/personal/user/personal-review',
      description: '当你需要私人代码审查偏好时使用。',
      trigger_words: ['review'],
      scope: 'personal',
      personal_type: 'user',
    },
  ];
}

function resetConnectedShellTestState() {
  vi.clearAllMocks();
  bridgeCallback = null;
  resetClientStoreForTests();
  window.localStorage.clear();
  window.history.replaceState({}, '', '/');
  Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1024 });
}

function installAppOverlayHost() {
  document.querySelectorAll('#overlay-root').forEach((node) => node.remove());
  appOverlayHost = document.createElement('div');
  appOverlayHost.id = 'overlay-root';
  document.body.append(appOverlayHost);
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
  const defaultSkills = defaultSkillFixtures();
  backend.getDashboardPage.mockImplementation(({ page }) => {
    if (page === 'memory') {
      return Promise.resolve({
        memory: [],
        finalOutputRefs: [],
        sharedFileRetention: { items: [], protectedCount: 0, cleanupCandidateCount: 0 },
      });
    }
    if (page === 'dags') {
      return Promise.resolve({ dags: [] });
    }
    if (page === 'skills') {
      return Promise.resolve({ skills: defaultSkills });
    }
    return Promise.resolve({});
  });
}

function mockObservabilityDefaults() {
  const emptyTraceResult = {
    source: 'memory',
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

function mockPromptDefaults() {
  backend.listPromptAssets.mockResolvedValue({ prompts: [] });
  backend.getDashboardPrompts.mockResolvedValue({ prompts: [] });
  backend.getPrompt.mockResolvedValue({ prompt: { content: '' } });
  backend.writePrompt.mockResolvedValue({ prompt: { id: 'saved-prompt' } });
  backend.deletePrompt.mockResolvedValue({ deleted: true });
  backend.getPersonalizationProfile.mockResolvedValue({ profile: {} });
  backend.savePersonalizationProfile.mockResolvedValue({ profile: {} });
  backend.listPromptSections.mockResolvedValue({ sections: [] });
  backend.writePromptSection.mockResolvedValue({ ok: true });
  backend.deletePromptSection.mockResolvedValue({ ok: true });
  backend.draftPromptIntent.mockResolvedValue({
    draft_key: 'intent/expert/default',
    kind: 'expert',
    scope: 'project',
    status: 'review',
    card: {
      kind: 'expert',
      title: '默认专家',
      summary: '整理后的能力',
      output: '执行说明',
      hit_examples: ['需要专家能力时'],
      miss_examples: ['普通聊天'],
    },
    issues: [],
  });
  backend.commitPromptIntent.mockResolvedValue({ prompt: { id: 'intent/expert/default' } });
  backend.discardPromptIntent.mockResolvedValue({ ok: true });
  backend.dryRunPromptIntent.mockResolvedValue({ would_use: true, reasons: ['matched'] });
}

function canonicalPromptRPCItem(overrides = {}) {
  return {
    id: 'main/canonical',
    name: '规范提示词',
    content: '',
    description: '',
    agentType: 'main',
    when_to_use: '',
    createdAt: '2026-07-11T00:00:00Z',
    updatedAt: '2026-07-11T00:00:00Z',
    enabled: true,
    scope: 'project',
    tags: ['intent:expert'],
    ...overrides,
  };
}

function mockPromptWizardEntryPrompt(overrides = {}) {
  const name = overrides.name || '待确认入口';
  const content = overrides.content || '待确认内容';
  const scope = overrides.scope || 'project';
  backend.listPromptAssets.mockResolvedValue({
    prompts: [canonicalPromptRPCItem({
      id: overrides.id || 'intent/expert/entry',
      draft_key: overrides.draftKey || 'intent/expert/entry',
      draft_status: overrides.status || 'ready_to_save',
      state: 'pending_confirm',
      name,
      content,
      description: overrides.description || '',
      tags: overrides.tags || ['intent:expert'],
      scope,
      enabled: true,
      card: overrides.card || {
        kind: 'expert',
        scope,
        title: name,
        output: content,
        hit_examples: [],
        miss_examples: [],
      },
      issues: overrides.issues || [],
    })],
  });
}

function mockMemoryDefaults() {
  // mock 必须与真实 facade 输出一致：backendApi.getMemorySnapshot 经 validateMemorySnapshotResponse
  // parse + transform 后返回扁平 { overview, entries }，而不是原始 wire 的 private/team 结构。
  backend.getMemorySnapshot.mockResolvedValue(normalizeMemorySnapshotForFacade({
    overview: {
      enabled: true,
      autoDreamEnabled: false,
      autoDreamIntent: null,
      projectRoot: '/repo/app',
      health: { preferenceCount: 0, projectCount: 0, maxPerCategory: 15, similarGroups: [] },
    },
    private: { entries: [] },
    team: { entries: [] },
  }));
  backend.getMemoryEntry.mockResolvedValue({
    target: 'private',
    path: 'feedback/tdd.md',
    name: 'tdd-rule',
    title: '遵守 TDD',
    description: '先写红测',
    type: 'feedback',
    content: '规则\n先写红测',
  });
  backend.upsertMemoryEntry.mockResolvedValue({ path: 'feedback/tdd.md' });
  backend.deleteMemoryEntry.mockResolvedValue({ deleted: true });
  backend.setMemoryAutoDreamIntent.mockResolvedValue({ ok: true, enabled: true });
  backend.mergeMemoryEntries.mockResolvedValue({ path: 'feedback/tdd.md' });
  backend.ignoreMemorySimilarity.mockResolvedValue({ ok: true });
  backend.consolidateMemorySimilarities.mockResolvedValue({ merged: 1, ignored: 0, failed: 0, skipped: 0 });
  backend.startConsolidateMemorySimilarities.mockResolvedValue({ jobId: 'memory-job-1', status: 'running' });
  backend.getMemoryConsolidationStatus.mockResolvedValue({
    jobId: 'memory-job-1',
    status: 'succeeded',
    result: { merged: 1, ignored: 0, failed: 0, skipped: 0 },
  });
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

function mockSkillDefaults() {
  backend.deleteSkill.mockResolvedValue({ ok: true });
  backend.listMCPServers.mockResolvedValue({ mcpServers: { sqlite: { enabled: false }, playwright: { enabled: false } } });
  backend.startSQLiteMCPServer.mockResolvedValue({ serverName: 'sqlite', enabled: true });
  backend.stopSQLiteMCPServer.mockResolvedValue({ serverName: 'sqlite', enabled: false });
  backend.startPlaywrightMCPServer.mockResolvedValue({ serverName: 'playwright', enabled: true });
  backend.stopPlaywrightMCPServer.mockResolvedValue({ serverName: 'playwright', enabled: false });
  backend.readSkill.mockImplementation(({ path }) => Promise.resolve({
    skill: {
      content: path.endsWith('/SKILL.md')
        ? [
          '---',
          'name: "backend"',
          'display_name: "后端"',
          'description: "当你需要 Go 后端开发时使用。"',
          'trigger_words: ["Go", "backend"]',
          '---',
          '',
          '## 后端规则',
        ].join('\n')
        : '关联文件内容',
    },
  }));
  backend.listSkillFiles.mockResolvedValue({
    files: [
      { name: 'SKILL.md', path: '/repo/app/.agents/skills/backend/SKILL.md', is_main: true },
      { name: 'guide.md', path: '/repo/app/.agents/skills/backend/references/guide.md', is_main: false },
    ],
  });
  backend.writeSkill.mockResolvedValue({ path: '/repo/app/.agents/skills/backend/SKILL.md' });
  backend.importSkillDirectories.mockResolvedValue({
    imported: [{ name: 'ImportedSkill', skill_file: '/imports/ImportedSkill/SKILL.md' }],
    failures: [],
  });
  backend.suggestSkillSummary.mockResolvedValue('当你需要部署服务时使用。');
  backend.selectProjectDir.mockResolvedValue('/repo/new');
  backend.selectProjectDirs.mockResolvedValue(['/imports/ImportedSkill']);
  backend.listSkillResolutions.mockResolvedValue({ items: [] });
  backend.listSkillTools.mockResolvedValue({ tools: [] });
  backend.createSkillTool.mockResolvedValue({ id: 1, name: 'Test Tool', command: 'echo', args: [], enabled: true });
  backend.getSkillTool.mockResolvedValue({ id: 1, name: 'Test Tool', command: 'echo', args: [], enabled: true });
  backend.updateSkillTool.mockResolvedValue({ id: 1, name: 'Test Tool', command: 'echo', args: [], enabled: true });
  backend.deleteSkillTool.mockResolvedValue({ id: 1, deleted: true });
  backend.previewSkillResolution.mockResolvedValue({
    items: [{
      provider: 'codex',
      preview_id: 'preview-1',
      preview_hash: 'hash-1',
      source_path: '/repo/app/.agents/skills/backend/SKILL.md',
      target_path: '/Users/test/.codex/skills/backend/SKILL.md',
    }],
  });
  backend.applySkillResolution.mockResolvedValue({ ok: true });
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
  backend.deleteSharedFile.mockResolvedValue({ deleted: true });
  backend.saveTextFile.mockResolvedValue('/exports/file.md');
}

function mockSettingsAndThreadDefaults() {
  backend.onFilesDropped.mockReturnValue(() => {});
  backend.beginTextClipboardWrite.mockReturnValue(null);
  backend.copyTextToClipboard.mockResolvedValue(true);
  backend.readLspPromptHint.mockResolvedValue({
    hint: 'effective prompt text',
    defaultHint: 'default prompt text',
    overrideHint: '',
    usingDefault: true,
  });
  backend.writeLspPromptHint.mockResolvedValue({
    hint: 'saved prompt text',
    defaultHint: 'default prompt text',
    overrideHint: 'saved prompt text',
    usingDefault: false,
  });
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
    override: {},
    effective: { model: 'gpt-5.4', effort: 'medium' },
  });
  backend.setThreadConfig.mockResolvedValue({
    threadId: 'thread-1',
    provider: 'codex',
    supportsThreadOverride: true,
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
  });
}

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
afterEach(() => {
  cleanup();
  document.querySelectorAll('#overlay-root').forEach((node) => node.remove());
  vi.useRealTimers();
});

it('wires one required overlay host through the App React Aria provider and existing theme owner', () => {
  expect(appSource).toMatch(/import\s+\{[^}]*UNSAFE_PortalProvider[^}]*\}\s+from\s+['"]react-aria['"]/);
  expect(appSource).toMatch(/import\s+\{\s*requiredOverlayRoot\s*\}\s+from\s+['"]\.\/shared\/ui\/OverlayPortal\.jsx['"]/);
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

it('wires one explicit shell layout store from App through the chat route and layout hooks', () => {
  expect(appSource).toContain('createShellLayoutStore');
  expect(appSource).toContain('shellLayoutStorage');
  expect(appSource).toContain('shellLayoutStore');
  expect(appRoutesSource).toMatch(/function ChatPageRoute\(props\)[\s\S]{0,240}const \{[^}]*shellLayoutStore[^}]*\} = props/);
  expect(appRoutesSource).toMatch(/<ChatPage[\s\S]{0,320}shellLayoutStore=\{shellLayoutStore\}/);
  expect(appRoutesSource).toMatch(/<ChatPageRoute[\s\S]{0,320}shellLayoutStore=\{shellLayoutStore\}/);
  expect(chatPageSource).toMatch(/function ChatPage\(props\)\s*\{\s*const \{[^\n]*shellLayoutStore/);
  expect(chatPageSource).toContain('useShellLayoutStore');
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
}, 15000);
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

function mockTraceDashboardQueryResult() {
  backend.listObservabilityRecent.mockResolvedValueOnce({
    source: 'mixed',
    total_duration_ms: 135,
    truncated: false,
    slowest_events: [],
    errors: [],
    events: [{
      ts: '2026-06-02T09:01:20.100Z',
      trace_id: 'trace-1',
      span_id: 'span-rpc',
      method: 'rpc.dispatch',
      status: 'slow',
      duration_ms: 120,
      thread_id: 'thread-1',
    }],
  });
  backend.getObservabilityTrace.mockResolvedValue({
    source: 'mixed',
    total_duration_ms: 135,
    truncated: false,
    slowest_events: [],
    errors: [],
    events: [
      { ts: '2026-06-02T09:01:19.000Z', trace_id: 'trace-1', span_id: 'span-begin', method: 'tool.call.begin', status: 'ok', thread_id: 'thread-1' },
      {
        ts: '2026-06-02T09:01:20.100Z',
        trace_id: 'trace-1',
        span_id: 'span-rpc',
        method: 'rpc.dispatch',
        status: 'slow',
        duration_ms: 120,
        thread_id: 'thread-1',
        parent_span_id: 'span-root',
        code: { file: 'internal/platform/rpc/server.go', function: '(*Server).Dispatch', line: 270 },
        stack: [{ file: 'internal/platform/rpc/server.go', function: '(*Server).Dispatch', line: 270 }],
        error: 'rpc dispatch exceeded slow threshold',
        metadata: { component: 'rpc', route: 'observability/trace/get' },
      },
      { ts: '2026-06-02T09:01:23.000Z', trace_id: 'trace-1', span_id: 'span-ui', method: 'ui/sidebar/get', status: 'ok', thread_id: 'thread-1' },
      { ts: '2026-06-02T09:01:24.000Z', trace_id: 'trace-1', span_id: 'span-noise', method: 'bus.event.lifecycle', kind: 'bus_event', status: 'dropped_summary', thread_id: 'thread-1' },
    ],
  });
}

async function openTraceDashboardForTraceId() {
  render(<App />);
  fireEvent.click(await screen.findByRole('button', { name: '链路追踪' }));
  fireEvent.change(await screen.findByLabelText('Trace ID'), { target: { value: 'trace-1' } });
  fireEvent.click(screen.getByRole('button', { name: '查询最新日志' }));
  const table = await screen.findByTestId('observability-recent-logs');
  fireEvent.click(within(table).getByRole('button', { name: '打开 Trace trace-1' }));
  return table;
}

function expectTraceDashboardRpcCalls() {
  expect(backend.listObservabilityRecent).toHaveBeenCalledTimes(1);
  expect(backend.listObservabilityRecent).toHaveBeenCalledWith({
    limit: 50,
    status: '',
    component: '',
    method: '',
    traceId: 'trace-1',
    threadId: '',
    agentId: '',
    keyword: '',
    includeTail: true,
  });
  expect(backend.getObservabilityTrace).toHaveBeenCalledWith({ traceId: 'trace-1', limit: 50 });
}

async function expectTraceDashboardRows(table) {
  const inlineTrace = await within(table).findByTestId('observability-inline-trace-trace-1');
  await waitFor(() => expect(inlineTrace).toHaveTextContent('source=mixed'));
  expect(screen.getAllByText(/internal\/platform\/rpc\/server.go:270/).length).toBeGreaterThan(0);
  let traceRows = [];
  await waitFor(() => {
    traceRows = within(inlineTrace).getAllByRole('listitem').filter((row) => row.classList.contains('observability-event-row'));
    expect(traceRows[0]).toHaveClass('observability-event-row');
  });
  expect(traceRows[0]).not.toHaveClass('settings-row');
  expect(traceRows[0]).toHaveTextContent('120ms');
  expect(traceRows[0]).toHaveTextContent('请求上下文');
  expect(traceRows[0]).toHaveTextContent('链路标识');
  expect(traceRows[0]).toHaveTextContent('失败原因');
  const zeroDurationRow = traceRows.find((row) => row.textContent.includes('ui/sidebar/get'));
  expect(zeroDurationRow).toBeTruthy();
  expect(zeroDurationRow).toHaveTextContent(formatParsedTimestampForTest('2026-06-02T09:01:23.000Z'));
  expect(zeroDurationRow).toHaveTextContent('耗时未记录');
  expect(zeroDurationRow).not.toHaveTextContent('0ms');
  expect(zeroDurationRow).not.toHaveTextContent('code=-');
  expect(traceRows[0]).toHaveTextContent('trace');
  expect(traceRows[0]).toHaveTextContent('trace-1');
  expect(traceRows[0]).toHaveTextContent('span');
  expect(traceRows[0]).toHaveTextContent('span-rpc');
  expect(traceRows[0]).toHaveTextContent('parent');
  expect(traceRows[0]).toHaveTextContent('span-root');
}

function expectTraceDashboardDetails() {
  expect(screen.getByText('rpc dispatch exceeded slow threshold')).toBeInTheDocument();
  expect(screen.getByText(/"component": "rpc"/)).toBeInTheDocument();
  expect(screen.getByText(/"route": "observability\/trace\/get"/)).toBeInTheDocument();
  expect(screen.getByText(/默认显示关键事件 2\/4/)).toBeInTheDocument();
  expect(screen.getByText(/已折叠 2 条成功过程事件/)).toBeInTheDocument();
  expect(screen.queryByText('tool.call.begin')).not.toBeInTheDocument();
  expect(screen.queryByText('bus.event.lifecycle')).not.toBeInTheDocument();
}

async function showAllTraceDashboardEvents() {
  fireEvent.click(screen.getByRole('button', { name: '显示全部事件' }));
  await waitFor(() => expect(screen.getAllByText('tool.call.begin').length).toBeGreaterThan(0));
  expect(screen.getAllByText('bus.event.lifecycle').length).toBeGreaterThan(0);
}

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

  describe('theme cold start and switching behavior', () => {
    beforeEach(() => {
      window.localStorage.clear();
      document.documentElement.removeAttribute('data-theme');
      document.body.removeAttribute('data-theme');
    });

    afterEach(() => {
      window.dispatchEvent(new Event('pagehide'));
      window.localStorage.clear();
      document.documentElement.removeAttribute('data-theme');
      document.body.removeAttribute('data-theme');
    });

    it('performs cold start with no stored value (defaults to light)', () => {
      const theme = getStoredTheme();
      expect(theme).toBe('light');
      syncThemeDOM(theme);
      expect(document.documentElement).toHaveAttribute('data-theme', 'light');
      expect(document.body).toHaveAttribute('data-theme', 'light');
    });

    it('performs cold start with pre-stored dark theme', () => {
      window.localStorage.setItem('super-dolphin-theme', 'dark');
      const theme = getStoredTheme();
      expect(theme).toBe('dark');
      syncThemeDOM(theme);
      expect(document.documentElement).toHaveAttribute('data-theme', 'dark');
      expect(document.body).toHaveAttribute('data-theme', 'dark');
    });

    it('executes main.jsx with no stored theme, calling syncThemeDOM before createRoot', async () => {
      const rootDiv = document.createElement('div');
      rootDiv.id = 'root';
      document.body.appendChild(rootDiv);

      const renderMock = vi.fn();
      createRootMock = vi.fn().mockReturnValue({ render: renderMock });
      syncThemeDOMMock = vi.fn();

      await import('./main.jsx?t=no-stored-theme');

      expect(syncThemeDOMMock).toHaveBeenCalledWith('light');
      expect(createRootMock).toHaveBeenCalled();

      const syncCallOrder = syncThemeDOMMock.mock.invocationCallOrder[0];
      const renderCallOrder = createRootMock.mock.invocationCallOrder[0];
      expect(syncCallOrder).toBeLessThan(renderCallOrder);

      createRootMock = null;
      syncThemeDOMMock = null;
      rootDiv.remove();
    });

    it('executes main.jsx with dark stored theme, calling syncThemeDOM before createRoot', async () => {
      window.localStorage.setItem('super-dolphin-theme', 'dark');

      const rootDiv = document.createElement('div');
      rootDiv.id = 'root';
      document.body.appendChild(rootDiv);

      const renderMock = vi.fn();
      createRootMock = vi.fn().mockReturnValue({ render: renderMock });
      syncThemeDOMMock = vi.fn();

      await import('./main.jsx?t=dark-stored-theme');

      expect(syncThemeDOMMock).toHaveBeenCalledWith('dark');
      expect(createRootMock).toHaveBeenCalled();

      const syncCallOrder = syncThemeDOMMock.mock.invocationCallOrder[0];
      const renderCallOrder = createRootMock.mock.invocationCallOrder[0];
      expect(syncCallOrder).toBeLessThan(renderCallOrder);

      createRootMock = null;
      syncThemeDOMMock = null;
      rootDiv.remove();
    });

    it('toggles the local color theme without calling backend preferences', async () => {
      render(<App />);

      const shell = await screen.findByTestId('frontend-app');
      const preferenceCallsBeforeToggle = backend.setPreference.mock.calls.length;

      // Check initial light theme synchronization
      expect(shell).toHaveAttribute('data-theme', 'light');
      expect(appOverlayHost).toHaveAttribute('data-theme', 'light');
      expect(document.documentElement).toHaveAttribute('data-theme', 'light');
      expect(document.body).toHaveAttribute('data-theme', 'light');

      fireEvent.click(screen.getByRole('button', { name: '切换到黑夜模式' }));
      expect(shell).toHaveAttribute('data-theme', 'dark');
      expect(appOverlayHost).toHaveAttribute('data-theme', 'dark');
      expect(document.documentElement).toHaveAttribute('data-theme', 'dark');
      expect(document.body).toHaveAttribute('data-theme', 'dark');
      expect(window.localStorage.getItem('super-dolphin-theme')).toBe('dark');
      expect(screen.getByRole('button', { name: '切换到白天模式' })).toBeInTheDocument();

      appOverlayHost.setAttribute('data-theme', 'tampered');
      fireEvent.click(screen.getByRole('button', { name: '切换到白天模式' }));
      expect(shell).toHaveAttribute('data-theme', 'light');
      expect(appOverlayHost).toHaveAttribute('data-theme', 'light');
      expect(document.documentElement).toHaveAttribute('data-theme', 'light');
      expect(document.body).toHaveAttribute('data-theme', 'light');
      expect(window.localStorage.getItem('super-dolphin-theme')).toBe('light');
      expect(screen.getByRole('button', { name: '切换到黑夜模式' })).toBeInTheDocument();
      expect(backend.setPreference.mock.calls.length).toBe(preferenceCallsBeforeToggle);
    });
  });

  it('opens observability tracing dashboard and queries by trace id', async () => {
    mockTraceDashboardQueryResult();

    const table = await openTraceDashboardForTraceId();

    await expectTraceDashboardRows(table);
    expectTraceDashboardDetails();
    expectTraceDashboardRpcCalls();
    await showAllTraceDashboardEvents();
  });

function mockRecentSystemLogsResult() {
  backend.listObservabilityRecent.mockResolvedValue({
    source: 'mixed',
    total_duration_ms: 38,
    truncated: false,
    slowest_events: [],
    errors: [],
    events: [
      {
        ts: '2026-06-02T09:01:22.459Z',
        trace_id: 'trace-frontend-1',
        span_id: 'span-ui',
        method: 'thread/start',
        phase: 'frontend.rpc.failed',
        kind: 'frontend',
        status: 'error',
        duration_ms: 33,
        thread_id: 'thread-1',
        client_route: '/chat',
        error: 'thread start failed',
      },
      {
        ts: '2026-06-02T09:01:20.100Z',
        trace_id: 'trace-frontend-1',
        span_id: 'span-rpc',
        method: 'rpc.dispatch',
        kind: 'rpc',
        status: 'ok',
        duration_ms: 5,
        thread_id: 'thread-1',
      },
      {
        ts: '2026-06-02T09:02:03.000Z',
        trace_id: 'trace-frontend-2',
        span_id: 'span-ui-2',
        method: 'thread/config/get',
        phase: 'frontend.rpc.done',
        kind: 'frontend',
        status: 'ok',
        duration_ms: 7,
        thread_id: 'thread-2',
      },
      {
        ts: '2026-06-02T09:03:04.000Z',
        trace_id: '',
        span_id: 'span-provider',
        method: 'provider.session.acquire',
        kind: 'provider',
        status: 'ok',
        duration_ms: 3268,
        thread_id: 'thread-provider',
      },
    ],
  });
  backend.getObservabilityTrace.mockResolvedValue({
    source: 'mixed',
    total_duration_ms: 33,
    truncated: false,
    slowest_events: [],
    errors: [],
    events: [{
      trace_id: 'trace-frontend-1',
      span_id: 'span-ui',
      method: 'thread/start',
      status: 'error',
      duration_ms: 33,
    }],
  });
}

async function openRecentSystemLogs() {
  render(<App />);
  fireEvent.click(await screen.findByRole('button', { name: '链路追踪' }));
  fireEvent.change(await screen.findByLabelText('状态'), { target: { value: 'error' } });
  fireEvent.change(screen.getByLabelText('关键词'), { target: { value: 'thread/start' } });
  fireEvent.click(screen.getByRole('button', { name: '查询最新日志' }));
  return screen.findByTestId('observability-recent-logs');
}

function expectRecentSystemLogsTable(table) {
  expect(table).toHaveTextContent('3 条匹配 event 分组 · 4 个匹配 event');
  expect(table).toHaveTextContent(formatParsedTimestampForTest('2026-06-02T09:01:22.459Z'));
  expect(table).toHaveTextContent(formatParsedTimestampForTest('2026-06-02T09:02:03.000Z'));
  expect(table).toHaveTextContent(formatParsedTimestampForTest('2026-06-02T09:03:04.000Z'));
  expect(table).not.toHaveTextContent('2026-06-02T09:01:22.459Z');
  expect(table).toHaveTextContent('thread/start');
  expect(table).toHaveTextContent('trace-frontend-1');
  expect(table).toHaveTextContent('thread start failed');
  expect(table).toHaveTextContent('provider.session.acquire');
  expect(table).toHaveTextContent('trace=-');
  expect(within(table).getAllByRole('button', { name: /复制 Trace ID/ })).toHaveLength(3);
  expect(within(table).getAllByRole('button', { name: /打开 Trace/ })).toHaveLength(3);
  expect(within(table).getByRole('button', { name: '复制 Trace ID -' })).toBeDisabled();
  expect(within(table).getByRole('button', { name: '打开 Trace -' })).toBeDisabled();
  expect(table).toHaveTextContent('2 个匹配 event');
}

function expectRecentSystemLogsRpcCall() {
  expect(backend.listObservabilityRecent).toHaveBeenCalledWith({
    limit: 50,
    status: 'error',
    component: '',
    method: '',
    traceId: '',
    threadId: '',
    agentId: '',
    keyword: 'thread/start',
    includeTail: true,
  });
}

async function copyTraceFromRecentLogs(table) {
  expect(screen.queryByText(/Trace 查询结果/)).not.toBeInTheDocument();
  expect(within(table).queryByTestId('observability-inline-trace-trace-frontend-1')).not.toBeInTheDocument();
  fireEvent.click(within(table).getByRole('button', { name: '复制 Trace ID trace-frontend-1' }));

  await waitFor(() => expect(backend.copyTextToClipboard).toHaveBeenCalledWith('trace-frontend-1'));
  expect(within(table).getByRole('button', { name: '复制 Trace ID trace-frontend-1' })).toHaveTextContent('已复制');
  expect(backend.getObservabilityTrace).not.toHaveBeenCalled();
}

async function toggleInlineTraceFromRecentLogs(table) {
  fireEvent.click(within(table).getByRole('button', { name: '打开 Trace trace-frontend-1' }));

  const inlineTrace = await within(table).findByTestId('observability-inline-trace-trace-frontend-1');
  await waitFor(() => {
    expect(inlineTrace).toHaveTextContent('Trace 结果');
    expect(inlineTrace).toHaveTextContent('source=mixed');
    expect(inlineTrace).toHaveTextContent('thread/start');
  });
  expect(within(table).getByRole('button', { name: '收起 Trace trace-frontend-1' })).toHaveAttribute('aria-expanded', 'true');
  expect(backend.getObservabilityTrace).toHaveBeenCalledWith({ traceId: 'trace-frontend-1', limit: 50 });
  expect(backend.listObservabilityRecent).toHaveBeenCalledTimes(1);
  expect(table).toHaveTextContent('trace-frontend-2');

  fireEvent.click(within(table).getByRole('button', { name: '收起 Trace trace-frontend-1' }));
  await waitFor(() => expect(within(table).queryByTestId('observability-inline-trace-trace-frontend-1')).not.toBeInTheDocument());
  expect(within(table).getByRole('button', { name: '打开 Trace trace-frontend-1' })).toHaveAttribute('aria-expanded', 'false');
  expect(backend.getObservabilityTrace).toHaveBeenCalledTimes(1);
}

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

  it('maps backend projected thread statuses in thread cards', async () => {
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: 'thread-thinking',
      threads: [
        { id: 'thread-thinking', name: '思考会话', provider: 'codex', status: 'thinking' },
        { id: 'thread-editing', name: '编辑会话', provider: 'codex', status: 'editing' },
        { id: 'thread-waiting', name: '确认会话', provider: 'codex', status: 'waiting' },
        { id: 'thread-syncing', name: '同步会话', provider: 'codex', status: 'syncing' },
        { id: 'thread-error', name: '异常会话', provider: 'codex', status: 'error' },
      ],
    });

    render(<App />);

    expect(await findThreadCardByName('思考会话')).toHaveTextContent('思考中');
    expect(getThreadCardByName('编辑会话')).toHaveTextContent('编辑中');
    expect(getThreadCardByName('确认会话')).toHaveTextContent('等待确认');
    expect(getThreadCardByName('同步会话')).toHaveTextContent('同步中');
    expect(getThreadCardByName('异常会话')).toHaveTextContent('异常');
    expect(getThreadCardByName('思考会话').querySelector('.thread-status-dot')).toHaveClass('thread-status-dot--thinking');
    expect(getThreadCardByName('确认会话').querySelector('.thread-status-dot')).toHaveClass('thread-status-dot--waiting');
    expect(getThreadCardByName('异常会话').querySelector('.thread-status-dot')).toHaveClass('thread-status-dot--error');
  });

  it('shows a bootstrap failure notice when the backend bridge is unavailable', async () => {
    backend.readConfig.mockRejectedValue(new Error('runtime shim: failed to connect ws://127.0.0.1:5175/wails/ws'));

    render(<App />);

    expect(await screen.findByText('连接后端失败：连接后端失败，请重试。')).toBeInTheDocument();
    expect(screen.queryByText(/127\.0\.0\.1/)).not.toBeInTheDocument();
    expect(frontendHealthSnapshot()).toEqual(expect.arrayContaining([
      expect.objectContaining({ actionId: 'app.bootstrap.background' }),
    ]));
    expect(JSON.stringify(frontendHealthSnapshot())).not.toContain('127.0.0.1');
  });

  it('does not expose provider switching when no project cwd is available', async () => {
    resetClientStoreForTests({
      bootstrapStatus: 'ready',
      cwd: '',
      activeProject: '',
      provider: 'codex',
    });

    render(<App skipBootstrap />);

    await screen.findByTestId('composer-input');
    expect(screen.queryByLabelText('切换 Claude / Codex provider')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '请先连接后端并选择项目' })).not.toBeInTheDocument();
    expect(backend.setPreference).not.toHaveBeenCalledWith(expect.objectContaining({ key: 'settings.provider.active' }));
  });

  it('disables composer send by button and Enter when no project cwd is available', async () => {
    resetClientStoreForTests({
      bootstrapStatus: 'ready',
      cwd: '',
      activeProject: '',
      activeThreadId: '',
      draft: 'Write something',
      attachments: [],
    });

    render(<App skipBootstrap />);

    // 发送仍要求项目 cwd（业务契约保留）；附件/模型控件在后端就绪后即可交互。
    const sendButton = await screen.findByRole('button', { name: '发送消息' });
    expect(sendButton).toBeDisabled();
    expect(screen.getByRole('button', { name: '添加文件' })).toBeEnabled();
    expect(screen.queryByRole('combobox', { name: '发送权限' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: '选择模型' })).toBeEnabled();

    fireEvent.click(sendButton);
    fireEvent.keyDown(screen.getByTestId('composer-input'), { key: 'Enter', code: 'Enter', charCode: 13 });

    expect(backend.startThread).not.toHaveBeenCalled();
    expect(backend.startTurn).not.toHaveBeenCalled();

    // 附件按钮在无项目时进入真实文件选择流程（不依赖项目 cwd）。
    fireEvent.click(screen.getByRole('button', { name: '添加文件' }));
    await waitFor(() => expect(backend.selectFiles).toHaveBeenCalled());
  });

  it('does not show composer interrupt controls for a running runtime agent without an active turn', async () => {
    resetClientStoreForTests({
      bootstrapStatus: 'ready',
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'agent_123',
      draft: '',
      attachments: [],
      threads: [{ id: 'agent_123', name: 'Runtime Agent', provider: 'codex', status: 'running' }],
      statuses: {
        agent_123: { status: 'running', interruptible: true },
      },
      threadTimelineReadyByThread: { agent_123: true },
      timelinesByThread: {
        agent_123: [{ id: 'assistant-1', role: 'assistant', text: '正在执行。' }],
      },
    });

    render(<App skipBootstrap />);
    await act(async () => Promise.resolve());

    expect(screen.queryByRole('button', { name: '中断当前执行' })).not.toBeInTheDocument();
    expect(screen.queryByLabelText('停止')).not.toBeInTheDocument();

    fireEvent.keyDown(window, { key: 'Escape', code: 'Escape' });

    await waitFor(() => expect(screen.getByTestId('chat-action-feedback')).toHaveTextContent('当前没有可中断任务'));
    expect(backend.interruptTurn).not.toHaveBeenCalled();
  });

  it('shows an enabled composer interrupt button for a running runtime agent with an active turn and without a draft', async () => {
    resetClientStoreForTests({
      bootstrapStatus: 'ready',
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'agent_123',
      draft: '',
      attachments: [],
      threads: [{ id: 'agent_123', name: 'Runtime Agent', provider: 'codex', status: 'running' }],
      activeTurnByThread: {
        agent_123: { id: 'turn-123', threadId: 'agent_123', status: 'running' },
      },
      statuses: {
        agent_123: { status: 'running', interruptible: true },
      },
      threadTimelineReadyByThread: { agent_123: true },
      timelinesByThread: {
        agent_123: [{ id: 'assistant-1', role: 'assistant', text: '正在执行。' }],
      },
    });

    render(<App skipBootstrap />);

    const interruptButton = screen.getByRole('button', { name: '中断当前执行' });
    expect(interruptButton).toBeEnabled();
    expect(screen.queryByRole('button', { name: '发送消息' })).not.toBeInTheDocument();

    fireEvent.click(interruptButton);

    await waitFor(() => expect(backend.interruptTurn).toHaveBeenCalledWith({
      cwd: '/repo/app',
      threadId: 'agent_123',
      expectedTurnId: 'turn-123',
      requestId: expect.any(String),
      source: 'ui_stop',
    }));
  });

  it('renders assistant markdown messages as formatted content', async () => {
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{
          id: 'assistant-md',
          kind: 'assistant',
          text: [
            '## 结果汇总',
            '',
            '| 工具 | 结果 |',
            '| --- | --- |',
            '| edit | 可用 |',
            '',
            '> 这是一条引用',
            '',
            '- [x] 已完成',
            '- [ ] 待处理',
            '',
            '访问 [官网](https://example.com)，这是 ~~旧内容~~。',
            '',
            '---',
            '',
            '![图例](https://example.com/chart.png)',
            '',
            '<script>alert(1)</script>',
          ].join('\n'),
          ts: '2026-05-30T00:00:00Z',
        }],
      },
    });

    const { container } = render(<App />);

    expect(await screen.findByRole('heading', { name: '结果汇总', level: 2 })).toBeInTheDocument();
    expect(screen.getByRole('table')).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: '工具' })).toBeInTheDocument();
    expect(screen.getByRole('cell', { name: '可用' })).toBeInTheDocument();
    expect(screen.getByText('这是一条引用').closest('blockquote')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: '官网' })).toHaveAttribute('href', 'https://example.com/');
    expect(screen.getByText('旧内容').tagName.toLowerCase()).toBe('del');
    expect(screen.getByRole('checkbox', { name: '已完成' })).toBeChecked();
    expect(screen.getByRole('checkbox', { name: '待处理' })).not.toBeChecked();
    expect(container.querySelector('hr')).toBeInTheDocument();
    expect(screen.getByRole('img', { name: '图例' })).toHaveAttribute('src', 'https://example.com/chart.png');
    expect(screen.getByText('<script>alert(1)</script>')).toBeInTheDocument();
    expect(screen.queryByText('## 结果汇总')).not.toBeInTheDocument();
  });

  it('copies completed AI output from the assistant message action', async () => {
    const text = [
      '这是 AI 输出。',
      '',
      '```js',
      'console.log("copy me");',
      '```',
    ].join('\n');
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{ id: 'assistant-copyable', kind: 'assistant', text, ts: '2026-05-30T00:00:00Z' }],
      },
    });

    render(<App />);

    await screen.findByText('这是 AI 输出。');
    fireEvent.click(screen.getByRole('button', { name: '复制 AI 输出' }));

    await waitFor(() => expect(backend.copyTextToClipboard).toHaveBeenCalledWith(text));
    expect(screen.getByRole('button', { name: '复制 AI 输出' })).toHaveTextContent('已复制');
  });

  it('renders mermaid code fences as diagrams instead of plain code blocks', async () => {
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{
          id: 'assistant-mermaid',
          kind: 'assistant',
          text: [
            '总体结构如下：',
            '```mermaid',
            'flowchart TD',
            '  User[用户] --> App[前端]',
            '  App --> API[后端]',
            '```',
          ].join('\n'),
          ts: '2026-05-30T00:00:00Z',
        }],
      },
    });

    const { container } = render(<App />);

    expect(await screen.findByLabelText('Mermaid 图表')).toBeInTheDocument();
    const image = await screen.findByRole('img', { name: 'Mermaid 图表' });
    expect(decodedSvgDataUrl(image)).toContain('flowchart TD');
    expect(container.querySelector('.mermaid-diagram')).toHaveTextContent('点击放大');
  });

  it('does not render Mermaid diagrams from unmaterialized older timeline history', async () => {
    const messages = Array.from({ length: 85 }, (_, index) => {
      if (index === 0) {
        return {
          id: 'older-mermaid',
          kind: 'assistant',
          text: [
            '旧 Mermaid 图表：',
            '```mermaid',
            'flowchart TD',
            '  Old[旧历史] --> Hidden[首屏隐藏]',
            '```',
          ].join('\n'),
          ts: '2026-05-30T00:00:00Z',
        };
      }
      return {
        id: `recent-${index}`,
        kind: index % 2 === 0 ? 'user' : 'assistant',
        text: `最近 timeline 消息 ${index}`,
        ts: '2026-05-30T00:00:00Z',
      };
    });
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': messages,
      },
    });

    render(<App />);

    expect(await screen.findByText('最近 timeline 消息 84')).toBeInTheDocument();
    expect(screen.queryByText('旧 Mermaid 图表：')).not.toBeInTheDocument();
    expect(mermaid.render).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole('button', { name: '显示更早的消息（5 条）' }));

    await waitFor(() => expect(mermaid.render).toHaveBeenCalledTimes(1));
    expect(screen.getByText('旧 Mermaid 图表：')).toBeInTheDocument();
  });

  it('sanitizes rendered mermaid SVG before rendering it as an image data URL', async () => {
    mermaid.render.mockResolvedValueOnce({
      svg: [
        '<svg role="img" aria-label="unsafe mermaid" onload="alert(1)">',
        '<script>alert(1)</script>',
        '<foreignObject><div>unsafe html</div></foreignObject>',
        '<a href="javascript:alert(1)"><text>unsafe link</text></a>',
        '<rect style="background: url( javascript:alert(1) )" />',
        '<text>safe mermaid</text>',
        '</svg>',
      ].join(''),
    });
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{
          id: 'assistant-mermaid-sanitized',
          kind: 'assistant',
          text: [
            '```mermaid',
            'flowchart TD',
            '  A-->B',
            '```',
          ].join('\n'),
          ts: '2026-05-30T00:00:00Z',
        }],
      },
    });

    const { container } = render(<App />);

    await screen.findByLabelText('Mermaid 图表');
    const image = await screen.findByRole('img', { name: 'Mermaid 图表' });
    const svg = decodedSvgDataUrl(image);
    expect(svg).toContain('safe mermaid');
    expect(svg).not.toContain('<script');
    expect(svg).not.toContain('foreignObject');
    expect(svg).not.toContain('onload');
    expect(svg).not.toContain('javascript:alert');
    expect(container.querySelector('script')).toBeNull();
    expect(container.querySelector('foreignObject')).toBeNull();
    expect(container.querySelector('[onload]')).toBeNull();
    expect(container.querySelector('[href^="javascript:"]')).toBeNull();
  });

  it('opens rendered mermaid diagrams in the enlarged preview with an external link', async () => {
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{
          id: 'assistant-mermaid-lightbox',
          kind: 'assistant',
          text: [
            '```mermaid',
            'flowchart TD',
            '  A[开始] --> B[完成]',
            '```',
          ].join('\n'),
          ts: '2026-05-30T00:00:00Z',
        }],
      },
    });

    render(<App />);

    fireEvent.click(await screen.findByRole('button', { name: '放大 Mermaid 图表' }));

    const dialog = screen.getByRole('dialog', { name: '图片预览：Mermaid 图表' });
    expect(dialog).toHaveClass('image-lightbox-dialog');
    expect(within(dialog).getByRole('img', { name: 'Mermaid 图表' })).toBeInTheDocument();
    expect(within(dialog).queryByRole('link', { name: '外部打开' })).not.toBeInTheDocument();
  });

  it('keeps assistant output from the thread snapshot when thread message history is stale', async () => {
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [
          { id: 'user-stale-history', kind: 'user', text: '我要图片。', ts: '2026-05-30T00:00:00Z' },
          { id: 'assistant-visible-output', kind: 'assistant', text: '这是 AI 输出。', ts: '2026-05-30T00:00:02Z' },
        ],
      },
    });
    backend.getThreadMessages.mockResolvedValue({
      messages: [{
        id: 1,
        role: 'user',
        content: '我要图片。',
        createdAt: '2026-05-30T00:00:00Z',
      }],
      total: 1,
    });

    render(<App />);

    expect(await screen.findByText('这是 AI 输出。')).toBeInTheDocument();
  });

  it('hides injected AGENTS instructions from restored chat history', async () => {
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {},
    });
    backend.getThreadMessages.mockResolvedValue({
      messages: [
        {
          id: 1,
          role: 'user',
          content: [
            '# AGENTS.md instructions for /home/ai01@f666.com/桌面/project/Super-Dolphin',
            '',
            '<INSTRUCTIONS>',
            '# Super Dolphin Agent Agent Context Policy',
            '',
            '## Scope',
            'This file defines how agents should load context.',
            '</INSTRUCTIONS>',
          ].join('\n'),
          createdAt: '2026-05-30T00:00:00Z',
        },
        {
          id: 2,
          role: 'user',
          content: '请修复前端渲染问题',
          createdAt: '2026-05-30T00:01:00Z',
        },
        {
          id: 3,
          role: 'assistant',
          content: '已完成修复。',
          createdAt: '2026-05-30T00:02:00Z',
        },
      ],
      total: 3,
    });

    render(<App />);

    expect(await screen.findByText('请修复前端渲染问题')).toBeInTheDocument();
    expect(screen.getByText('已完成修复。')).toBeInTheDocument();
    expect(screen.queryByText(/AGENTS\.md instructions/)).not.toBeInTheDocument();
    expect(screen.queryByText(/Super Dolphin Agent Agent Context Policy/)).not.toBeInTheDocument();
  });

  it('renders malformed inline markdown fences as readable code blocks', async () => {
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{
          id: 'assistant-inline-fence',
          kind: 'assistant',
          text: [
            '下面是当前仓库结构： ```textSuper-Dolphin/',
            '├── cmd/#可执行入口',
            '├── frontend-app/#当前前端',
            '└── README.md',
          ].join('\n'),
          ts: '2026-05-30T00:00:00Z',
        }],
      },
    });

    const { container } = render(<App />);

    await screen.findByText('下面是当前仓库结构：', {}, { timeout: 5000 });
    await waitFor(() => expect(container.querySelector('.message-markdown pre')).toBeInTheDocument());
    const codeBlock = container.querySelector('.message-markdown pre');
    expect(codeBlock).toHaveTextContent('Super-Dolphin/');
    expect(codeBlock).toHaveTextContent('frontend-app/#当前前端');
    expect(codeBlock).not.toHaveTextContent('```');
    expect(screen.queryByText(/```textSuper-Dolphin/)).not.toBeInTheDocument();
  });

  it('renders common markdown code fence variants without leaking fence metadata', async () => {
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{
          id: 'assistant-common-code-fences',
          kind: 'assistant',
          text: [
            '常见代码块：',
            '',
            '~~~bash',
            'npm run lint',
            '~~~',
            '',
            '```bash title="frontend test"',
            'npm test',
            '```',
            '',
            '```js {1,3}',
            'const value = 1;',
            'console.log(value);',
            '```',
            '',
            '缩进代码：',
            '    pnpm install',
            '    pnpm test',
          ].join('\n'),
          ts: '2026-05-30T00:00:00Z',
        }],
      },
    });

    const { container } = render(<App />);

    await screen.findByText('常见代码块：', {}, { timeout: 5000 });
    await waitFor(() => expect(container.querySelectorAll('.message-markdown pre code')).toHaveLength(4));
    const codeBlocks = Array.from(container.querySelectorAll('.message-markdown pre code'));
    expect(codeBlocks).toHaveLength(4);
    expect(codeBlocks[0]).toHaveTextContent('npm run lint');
    expect(codeBlocks[1]).toHaveTextContent('npm test');
    expect(codeBlocks[1]).not.toHaveTextContent('title="frontend test"');
    expect(codeBlocks[2]).toHaveTextContent('const value = 1;');
    expect(codeBlocks[2]).not.toHaveTextContent('{1,3}');
    expect(codeBlocks[3]).toHaveTextContent('pnpm install');
    expect(codeBlocks[3]).toHaveTextContent('pnpm test');
    expect(screen.queryByText(/~~~bash/)).not.toBeInTheDocument();
  });

  it('renders unfenced terminal transcripts as code blocks instead of markdown quotes', async () => {
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{
          id: 'assistant-terminal-transcript',
          kind: 'assistant',
          text: [
            '执行结果：',
            '$ npm test',
            '> super-dolphin-frontend-app@0.1.0 test',
            '> vitest run',
            'PASS src/App.test.jsx',
          ].join('\n'),
          ts: '2026-05-30T00:00:00Z',
        }],
      },
    });

    const { container } = render(<App />);

    await screen.findByText('执行结果：', {}, { timeout: 5000 });
    await waitFor(() => expect(container.querySelector('.message-markdown pre code')).toBeInTheDocument());
    const codeBlock = container.querySelector('.message-markdown pre code');
    expect(codeBlock).toHaveTextContent('$ npm test');
    expect(codeBlock).toHaveTextContent('> vitest run');
    expect(codeBlock).toHaveTextContent('PASS src/App.test.jsx');
    expect(container.querySelector('.message-markdown blockquote')).toBeNull();
  });

  it('renders generated local image paths from assistant replies as image previews', async () => {
    const imagePath = '/Users/ai/.codex/generated_images/019e8195-2f77-7aa1-96bd-63f784e87ac4/ig_088272cb55a587f8016a1d3d9660148191951c218f7b0b6c1.png';
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{
          id: 'assistant-image-path',
          kind: 'assistant',
          text: `已展示。图片文件路径：\`${imagePath}\``,
          ts: '2026-05-30T00:00:00Z',
        }],
      },
    });

    render(<App />);

    const image = await screen.findByRole('img', { name: 'ig_088272cb55a587f8016a1d3d9660148191951c218f7b0b6c1.png' });
    expect(image).toHaveAttribute('src', `/generated-image?path=${encodeURIComponent(imagePath)}`);
    expect(screen.getByRole('button', { name: '放大图片 ig_088272cb55a587f8016a1d3d9660148191951c218f7b0b6c1.png' })).toBeInTheDocument();
    expect(screen.queryByText(imagePath)).not.toBeInTheDocument();
  });

  it('opens assistant image previews in an enlarged lightbox with an external link', async () => {
    const imagePath = '/Users/ai/.codex/generated_images/019e8195-2f77-7aa1-96bd-63f784e87ac4/ig_lightbox.png';
    const routedSrc = `/generated-image?path=${encodeURIComponent(imagePath)}`;
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{
          id: 'assistant-image-lightbox',
          kind: 'assistant',
          text: `图片已生成：${imagePath}`,
          ts: '2026-05-30T00:00:00Z',
        }],
      },
    });

    render(<App />);

    await screen.findByRole('button', { name: '放大图片 ig_lightbox.png' });
    fireEvent.click(screen.getByRole('button', { name: '放大图片 ig_lightbox.png' }));

    const dialog = await screen.findByRole('dialog', { name: '图片预览：ig_lightbox.png' });
    expect(dialog).toHaveClass('image-lightbox-dialog');
    expect(within(dialog).getByRole('img', { name: 'ig_lightbox.png' })).toHaveAttribute('src', routedSrc);
    expect(within(dialog).queryByRole('link', { name: '外部打开' })).not.toBeInTheDocument();
  });

  it('shows a readable fallback when a generated image preview cannot load', async () => {
    const imagePath = '/Users/ai/.codex/generated_images/019e8195-2f77-7aa1-96bd-63f784e87ac4/ig_missing.png';
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{
          id: 'assistant-missing-image-path',
          kind: 'assistant',
          text: `图片文件路径：\`${imagePath}\``,
          ts: '2026-05-30T00:00:00Z',
        }],
      },
    });

    render(<App />);

    await screen.findByRole('img', { name: 'ig_missing.png' });
    const image = screen.getByRole('img', { name: 'ig_missing.png' });
    fireEvent.error(image);

    const note = await screen.findByRole('note');
    expect(note).toHaveTextContent('图片无法加载');
    expect(note).toHaveTextContent('ig_missing.png');
  });

  it('renders bare generated local image paths from assistant replies as image previews', async () => {
    const imagePath = '/Users/ai/.codex/generated_images/019e8195-2f77-7aa1-96bd-63f784e87ac4/ig_bare_path.png';
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{
          id: 'assistant-bare-image-path',
          kind: 'assistant',
          text: `图片已生成：${imagePath}`,
          ts: '2026-05-30T00:00:00Z',
        }],
      },
    });

    render(<App />);

    const image = await screen.findByRole('img', { name: 'ig_bare_path.png' });
    expect(image).toHaveAttribute('src', `/generated-image?path=${encodeURIComponent(imagePath)}`);
    expect(screen.queryByText(imagePath)).not.toBeInTheDocument();
  });

  it('renders local image paths in markdown image syntax through the generated image route', async () => {
    const imagePath = '/Users/ai/.codex/generated_images/019e8195-2f77-7aa1-96bd-63f784e87ac4/ig_markdown_path.png';
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{
          id: 'assistant-markdown-image-path',
          kind: 'assistant',
          text: `![生成图](${imagePath})`,
          ts: '2026-05-30T00:00:00Z',
        }],
      },
    });

    render(<App />);

    const image = await screen.findByRole('img', { name: '生成图' });
    expect(image).toHaveAttribute('src', `/generated-image?path=${encodeURIComponent(imagePath)}`);
  });


  it('renders common llm output forms with dedicated formatting', async () => {
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [
          {
            id: 'assistant-json',
            kind: 'assistant',
            text: '{"status":"ok","items":[{"name":"edit","count":2}]}',
            ts: '2026-05-30T00:00:00Z',
          },
          {
            id: 'assistant-diff',
            kind: 'assistant',
            text: [
              'diff --git a/src/a.js b/src/a.js',
              '--- a/src/a.js',
              '+++ b/src/a.js',
              '@@ -1 +1 @@',
              '-old',
              '+new',
            ].join('\n'),
            ts: '2026-05-30T00:00:01Z',
          },
          {
            id: 'assistant-log',
            kind: 'assistant',
            text: [
              '[ERROR] api.rpc.failed',
              'Error: boom',
              '    at run (app.js:10:2)',
            ].join('\n'),
            ts: '2026-05-30T00:00:02Z',
          },
          {
            id: 'assistant-config',
            kind: 'assistant',
            text: [
              'provider: codex',
              'model: gpt-5',
              'sandbox: workspace-write',
            ].join('\n'),
            ts: '2026-05-30T00:00:03Z',
          },
        ],
      },
    });

    render(<App />);

    expect(await screen.findByText(/"status": "ok"/)).toBeInTheDocument();
    const jsonBlock = document.querySelector('[data-output-kind="json"]');
    expect(jsonBlock).toBeInTheDocument();
    expect(jsonBlock).toHaveTextContent('"count": 2');

    const diffBlock = document.querySelector('[data-output-kind="diff"]');
    expect(diffBlock).toBeInTheDocument();
    expect(diffBlock.querySelector('.diff-line--deleted')).toHaveTextContent('-old');
    expect(diffBlock.querySelector('.diff-line--added')).toHaveTextContent('+new');
    expect(diffBlock.querySelector('.diff-line--hunk')).toHaveTextContent('@@ -1 +1 @@');

    const logBlock = document.querySelector('[data-output-kind="log"]');
    expect(logBlock).toBeInTheDocument();
    expect(logBlock).toHaveTextContent('[ERROR] api.rpc.failed');
    expect(logBlock).toHaveTextContent('at run (app.js:10:2)');

    const configBlock = document.querySelector('[data-output-kind="config"]');
    expect(configBlock).toBeInTheDocument();
    expect(configBlock).toHaveTextContent('sandbox: workspace-write');
  });

  it('[regression] renders streaming code blocks without showing opening code fences', async () => {
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [
          {
            id: 'assistant-streaming-log',
            kind: 'assistant',
            text: [
              '```log',
              '[INFO] starting server...',
              '[INFO] server listening on port 8080',
            ].join('\n'),
            ts: '2026-05-30T00:00:00Z',
          },
        ],
      },
    });

    render(<App />);

    expect(await screen.findByText(/\[INFO\] starting server\.\.\./)).toBeInTheDocument();
    const logBlock = document.querySelector('[data-output-kind="log"]');
    expect(logBlock).toBeInTheDocument();
    expect(logBlock).toHaveTextContent('[INFO] starting server...');
    expect(logBlock).not.toHaveTextContent('```log');
  });

  it('derives runtime code-change metrics from the backend diff for the selected thread', async () => {
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{ id: 'assistant-1', kind: 'assistant', text: '来自后端的消息', ts: '2026-05-30T00:00:00Z' }],
      },
      diffTextByThread: {
        'thread-1': [
          'diff --git a/src/a.js b/src/a.js',
          '--- a/src/a.js',
          '+++ b/src/a.js',
          '@@ -1,2 +1,3 @@',
          ' keep',
          '-old',
          '+new',
          '+extra',
          'diff --git a/src/b.js b/src/b.js',
          '--- a/src/b.js',
          '+++ b/src/b.js',
          '@@ -4,2 +4,2 @@',
          '-removed',
          '+added',
        ].join('\n'),
      },
    });

    render(<App />);
    await waitForBackendThreadHeading();

    act(() => {
      bridgeCallback({
        type: 'bridge.call/failed',
        payload: { method: 'turn/start', threadId: 'thread-1', error: 'backend failed' },
      });
    });
    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));

    const fileCountMetric = screen.getByLabelText('代码变更文件数');
    const changedLineMetric = screen.getByLabelText('代码变更行数');
    expect(fileCountMetric).toHaveTextContent('2');
    expect(fileCountMetric.querySelector('svg')).toHaveClass('lucide-file-text');
    expect(changedLineMetric).toHaveTextContent('5');
    expect(changedLineMetric.querySelector('svg')).toHaveClass('lucide-code-xml');
    expect(screen.getByLabelText('代码新增行数')).toHaveTextContent('+3');
    expect(screen.getByLabelText('代码删除行数')).toHaveTextContent('-2');
    expect(screen.getByLabelText('代码新增行数')).not.toHaveTextContent('+0');
    expect(screen.getByLabelText('代码删除行数')).not.toHaveTextContent('-1');
  });

  it('renders a grouped line-by-line diff instead of raw patch text', async () => {
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{ id: 'assistant-1', kind: 'assistant', text: '来自后端的消息', ts: '2026-05-30T00:00:00Z' }],
      },
      diffTextByThread: {
        'thread-1': [
          'diff --git a/src/a.js b/src/a.js',
          '--- a/src/a.js',
          '+++ b/src/a.js',
          '@@ -1 +1,2 @@',
          '-old',
          '+new',
          '+extra',
          'diff --git a/src/b.js b/src/b.js',
          '--- a/src/b.js',
          '+++ b/src/b.js',
          '@@ -4 +4 @@',
          '-removed',
          '+added',
          'diff --git a/docs/notes.md b/docs/notes.md',
          '--- a/docs/notes.md',
          '+++ b/docs/notes.md',
          '@@ -1,0 +1 @@',
          '+note',
        ].join('\n'),
      },
    });

    render(<App />);
    await waitForBackendThreadHeading();

    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));

    const diffView = screen.getByTestId('diff-view');
    const fileGroups = diffView.querySelectorAll('.diff-file-group');
    expect(fileGroups).toHaveLength(3);
    expect(diffView).not.toHaveTextContent('diff --git');

    const firstFile = fileGroups[0];
    expect(within(firstFile).getByRole('button', { name: '折叠 src/a.js' })).toHaveTextContent('+2');
    expect(within(firstFile).getByRole('button', { name: '折叠 src/a.js' })).toHaveTextContent('-1');
    expect(firstFile.querySelector('.diff-line.hunk')).toHaveTextContent('@@ -1 +1,2 @@');
    expect(firstFile.querySelector('.diff-line.del')).toHaveTextContent('old');
    expect(firstFile.querySelector('.diff-line.add')).toHaveTextContent('new');
    expect(firstFile.querySelector('.diff-line.add .diff-line-new')).toHaveTextContent('1');
    expect(firstFile.querySelector('.diff-line.del .diff-line-old')).toHaveTextContent('1');
    expect(firstFile).not.toHaveTextContent('diff --git');
    expect(firstFile).not.toHaveTextContent('--- a/src/a.js');
    expect(firstFile).not.toHaveTextContent('+++ b/src/a.js');

    expect(diffView).toHaveTextContent('src/b.js');
    expect(diffView).toHaveTextContent('docs/notes.md');
    expect(screen.queryByTestId('diff-raw')).not.toBeInTheDocument();
  });

  it('locates, previews and saves runtime diff files through code RPCs', async () => {
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{ id: 'assistant-1', kind: 'assistant', text: '来自后端的消息', ts: '2026-05-30T00:00:00Z' }],
      },
      diffTextByThread: {
        'thread-1': [
          'diff --git a/src/a.js b/src/a.js',
          '--- a/src/a.js',
          '+++ b/src/a.js',
          '@@ -1 +1,2 @@',
          '-old',
          '+new',
          '+extra',
        ].join('\n'),
      },
    });

    render(<App />);
    await waitForBackendThreadHeading();

    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));
    fireEvent.click(screen.getByRole('button', { name: '定位 src/a.js' }));

    await waitFor(() => {
      expect(backend.locateCodeFile).toHaveBeenCalledWith({
        filePath: 'src/a.js',
        project: '/repo/app',
        projects: ['/repo/app'],
      });
      expect(screen.getByTestId('runtime-panel')).toHaveTextContent('定位到 1 个路径');
    });

    fireEvent.click(screen.getByRole('button', { name: '打开 src/a.js' }));

    const previewDialog = await screen.findByRole('dialog', { name: '文件预览' });
    expect(backend.openCodeFile).toHaveBeenCalledWith({
      filePath: 'src/a.js',
      project: '/repo/app',
      projects: ['/repo/app'],
    });
    expect(within(previewDialog).getByText('src/a.js')).toBeInTheDocument();

    const previewEditor = within(previewDialog).getByLabelText('文件预览内容');
    expect(previewEditor).toHaveValue('old\nkeep');

    fireEvent.change(previewEditor, { target: { value: 'new\nkeep' } });
    fireEvent.click(within(previewDialog).getByRole('button', { name: '保存预览更改' }));

    await waitFor(() => {
      expect(backend.saveCodeFile).toHaveBeenCalledWith({
        filePath: '/repo/app/src/a.js',
        content: 'new\nkeep',
        previewMode: 'full',
        contentVersion: 'version-src-a',
        project: '/repo/app',
        projects: ['/repo/app'],
      });
      expect(within(previewDialog).getByText('已保存 src/a.js')).toBeInTheDocument();
    });
  });

  it('opens a path choice dialog when runtime diff locate returns multiple matches', async () => {
    backend.locateCodeFile.mockResolvedValueOnce({
      ok: true,
      paths: ['/repo/app/src/a.js', '/repo/app/packages/demo/src/a.js'],
      matches: [
        { path: '/repo/app/src/a.js', relative: 'src/a.js' },
        { path: '/repo/app/packages/demo/src/a.js', relative: 'packages/demo/src/a.js' },
      ],
      truncated: true,
    });
    backend.openCodeFile.mockResolvedValueOnce({
      ok: true,
      filePath: '/repo/app/packages/demo/src/a.js',
      relative: 'packages/demo/src/a.js',
      snippet: [{ line: 1, text: 'chosen file' }],
    });
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{ id: 'assistant-1', kind: 'assistant', text: '来自后端的消息', ts: '2026-05-30T00:00:00Z' }],
      },
      diffTextByThread: {
        'thread-1': [
          'diff --git a/src/a.js b/src/a.js',
          '--- a/src/a.js',
          '+++ b/src/a.js',
          '@@ -1 +1 @@',
          '-old',
          '+new',
        ].join('\n'),
      },
    });

    render(<App />);
    await waitForBackendThreadHeading();

    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));
    fireEvent.click(screen.getByRole('button', { name: '定位 src/a.js' }));

    const chooser = await screen.findByRole('dialog', { name: '选择文件路径' });
    expect(within(chooser).getByText('/repo/app/src/a.js')).toBeInTheDocument();
    expect(within(chooser).getByText('/repo/app/packages/demo/src/a.js')).toBeInTheDocument();
    expect(within(chooser).getByText('结果已截断，仅显示部分结果')).toBeInTheDocument();

    fireEvent.click(within(chooser).getByRole('button', { name: '/repo/app/packages/demo/src/a.js' }));

    const previewDialog = await screen.findByRole('dialog', { name: '文件预览' });
    expect(backend.openCodeFile).toHaveBeenCalledWith({
      filePath: '/repo/app/packages/demo/src/a.js',
      project: '/repo/app',
      projects: ['/repo/app'],
    });
    expect(within(previewDialog).getByText('packages/demo/src/a.js')).toBeInTheDocument();
    expect(within(previewDialog).getByText('chosen file')).toBeInTheDocument();
    expect(within(previewDialog).queryByLabelText('文件预览内容')).not.toBeInTheDocument();
    expect(within(previewDialog).queryByRole('button', { name: '保存预览更改' })).not.toBeInTheDocument();
  });

  it('renders markdown runtime diff previews and blocks closing dirty edits', async () => {
    backend.openCodeFile.mockResolvedValueOnce({
      ok: true,
      filePath: '/repo/app/docs/readme.md',
      relative: 'docs/readme.md',
      language: 'markdown',
      startLine: 1,
      endLine: 3,
      totalLines: 3,
      previewMode: 'full',
      contentVersion: 'version-docs-readme',
      snippet: '# Guide\n\n- first step',
    });
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{ id: 'assistant-1', kind: 'assistant', text: '来自后端的消息', ts: '2026-05-30T00:00:00Z' }],
      },
      diffTextByThread: {
        'thread-1': [
          'diff --git a/docs/readme.md b/docs/readme.md',
          '--- a/docs/readme.md',
          '+++ b/docs/readme.md',
          '@@ -1 +1 @@',
          '-old',
          '+new',
        ].join('\n'),
      },
    });

    render(<App />);
    await waitForBackendThreadHeading();

    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));
    fireEvent.click(screen.getByRole('button', { name: '打开 docs/readme.md' }));

    const previewDialog = await screen.findByRole('dialog', { name: '文件预览' });
    expect(within(previewDialog).getByRole('heading', { name: 'Guide' })).toBeInTheDocument();
    expect(within(previewDialog).getByText('first step')).toBeInTheDocument();
    expect(within(previewDialog).queryByLabelText('文件预览内容')).not.toBeInTheDocument();

    fireEvent.click(within(previewDialog).getByRole('button', { name: '编辑预览' }));
    const previewEditor = within(previewDialog).getByLabelText('文件预览内容');
    fireEvent.change(previewEditor, { target: { value: '# Guide\n\nchanged' } });
    fireEvent.click(within(previewDialog).getByRole('button', { name: '关闭文件预览' }));

    expect(screen.getByRole('dialog', { name: '文件预览' })).toBeInTheDocument();
    expect(within(previewDialog).getByRole('alert')).toHaveTextContent('请先保存或放弃预览更改');
  });

  it('renders image runtime diff previews without the text editor', async () => {
    backend.openCodeFile.mockResolvedValueOnce({
      ok: true,
      image: true,
      filePath: '/repo/app/assets/logo.png',
      relative: 'assets/logo.png',
      mediaType: 'image/png',
      previewURL: '/local-image?id=logo_full',
      thumbnailURL: '/local-image?id=logo_thumb',
      sizeBytes: 2048,
    });
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{ id: 'assistant-1', kind: 'assistant', text: '来自后端的消息', ts: '2026-05-30T00:00:00Z' }],
      },
      diffTextByThread: {
        'thread-1': [
          'diff --git a/assets/logo.png b/assets/logo.png',
          '--- a/assets/logo.png',
          '+++ b/assets/logo.png',
          '@@ -1 +1 @@',
          '-old',
          '+new',
        ].join('\n'),
      },
    });

    render(<App />);
    await waitForBackendThreadHeading();

    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));
    fireEvent.click(screen.getByRole('button', { name: '打开 assets/logo.png' }));

    const previewDialog = await screen.findByRole('dialog', { name: '文件预览' });
    const image = within(previewDialog).getByRole('img', { name: 'assets/logo.png' });
    expect(image).toHaveAttribute('src', '/local-image?id=logo_thumb');
    expect(within(previewDialog).getByText('image/png · 2.0 KB')).toBeInTheDocument();
    expect(within(previewDialog).queryByLabelText('文件预览内容')).not.toBeInTheDocument();
    expect(within(previewDialog).queryByRole('button', { name: '保存预览更改' })).not.toBeInTheDocument();
  });

  it('does not render the removed work status from the backend turn state machine', async () => {
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: '后端线程', provider: 'codex', status: 'preparing' }],
      tokenUsageByThread: {
        'thread-1': { usedTokens: 128, contextWindowTokens: 1024, usedPercent: 12.5 },
      },
    });

    const { container } = render(<App />);

    await waitForBackendThreadHeading();
    expect(container.querySelector('.work-status')).toBeNull();

    act(() => {
      bridgeCallback({
        type: 'ui/thread/patch',
        payload: {
          threadId: 'thread-1',
          sequence: '1',
          status: 'force_completing',
        },
      });
    });

    expect(container.querySelector('.work-status')).toBeNull();
  });

  it('keeps backend projected thread states out of the removed work status bar', async () => {
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: '后端线程', provider: 'codex', status: 'idle' }],
    });

    const { container } = render(<App />);

    await waitForBackendThreadHeading();
    expect(container.querySelector('.work-status')).toBeNull();

    for (const [index, status] of [
      'starting',
      'thinking',
      'editing',
      'waiting',
      'syncing',
      'responding',
      'error',
      'archived',
    ].entries()) {
      act(() => {
        bridgeCallback({
          type: 'ui/thread/patch',
          payload: {
            threadId: 'thread-1',
            sequence: `${index + 1}`,
            status,
          },
        });
      });
      expect(container.querySelector('.work-status')).toBeNull();
    }
  });

  it('does not render removed work status details or token chip', async () => {
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: '后端线程', provider: 'codex', status: 'idle' }],
      tokenUsageByThread: {
        'thread-1': { usedTokens: 21017, contextWindowTokens: 258400, usedPercent: 8.1 },
      },
    });
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: { 'thread-1': [] },
    });

    const { container } = render(<App />);
    await waitForBackendThreadHeading();

    act(() => {
      useClientStore.setState((state) => ({
        statuses: {
          ...state.statuses,
          'thread-1': {
            status: 'idle',
            statusDetails: '��持被跳过，但写入成功|临时文件清理|输出 `scratch_removed`',
          },
        },
      }));
    });

    expect(container.querySelector('.work-status')).toBeNull();
    expect(container).not.toHaveTextContent('持被跳过，但写入成功');
    expect(container).not.toHaveTextContent('21017 / 258400 tokens');
  });

  it('does not expose internal thread identifiers when the work status bar is hidden', async () => {
    const internalId = 'agent_1780284988948557000';
    backend.getSidebarState.mockResolvedValueOnce({
      activeThreadId: internalId,
      threads: [{ id: internalId, name: internalId, provider: 'codex', status: 'idle' }],
      statuses: { [internalId]: 'idle' },
    });
    backend.getThreadState.mockResolvedValueOnce({
      activeThreadId: internalId,
      timelinesByThread: { [internalId]: [] },
    });

    const { container } = render(<App />);

    await screen.findByRole('button', { name: '新对话' });
    expect(container.querySelector('.work-status')).toBeNull();
    expect(container).not.toHaveTextContent(internalId);
    expect(screen.getAllByRole('button', { name: '新对话' }).length).toBeGreaterThan(0);
  });

  it('shows a lightweight history placeholder when the active thread has no trusted cache', async () => {
    const { container } = render(<App />);
    await waitForBackendThreadHeading();

    act(() => {
      useClientStore.setState((state) => ({
        statuses: { ...state.statuses, 'thread-1': 'idle' },
        threads: state.threads.map((thread) => (
          thread.id === 'thread-1' ? { ...thread, status: 'idle' } : thread
        )),
        timelinesByThread: {
          ...state.timelinesByThread,
          'thread-1': [],
        },
        threadTimelineReadyByThread: {
          ...state.threadTimelineReadyByThread,
          'thread-1': false,
        },
        threadStateLoadingByThread: {
          ...state.threadStateLoadingByThread,
          'thread-1': true,
        },
      }));
    });

    await waitFor(() => {
      expect(screen.getByTestId('timeline-loading-placeholder')).toHaveTextContent('正在同步会话历史');
      expect(container.querySelector('.work-status')).toBeNull();
    });
  });

  it('keeps the existing timeline visible while the active thread state is refreshing', () => {
    resetClientStoreForTests({
      bootstrapStatus: 'ready',
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: '后端线程', provider: 'codex', status: 'idle' }],
      statuses: { 'thread-1': 'idle' },
      timelinesByThread: {
        'thread-1': [{ id: 'assistant-cached', kind: 'assistant', text: '刷新前已有的回答', ts: '2026-05-30T00:00:00Z' }],
      },
      threadTimelineReadyByThread: { 'thread-1': true },
      threadStateLoadingByThread: { 'thread-1': true },
    });

    const { container } = render(<App skipBootstrap />);

    expect(screen.getByText('刷新前已有的回答')).toBeInTheDocument();
    expect(screen.getByTestId('chat-timeline')).toHaveTextContent('刷新前已有的回答');
    expect(screen.queryByTestId('timeline-loading-placeholder')).not.toBeInTheDocument();
    expect(container.querySelector('.work-status')).toBeNull();
  });

  it('shows AI thinking records with elapsed time in the chat timeline', async () => {
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{
          id: 'thinking-1',
          kind: 'thinking',
          text: '已探索 4 个文件并运行 2 条命令。',
          done: true,
          ts: '2026-05-30T00:00:00Z',
          completedAt: '2026-05-30T00:06:05Z',
        }, {
          id: 'assistant-after-thinking',
          kind: 'assistant',
          text: '这是整理后的回答。',
          ts: '2026-05-30T00:06:06Z',
        }],
      },
    });

    render(<App />);

    expect(await screen.findByLabelText('AI 思考记录')).toHaveTextContent('已处理 AI 思考 6m 5s');
    expect(screen.getByLabelText('AI 思考记录')).toHaveTextContent('已探索 4 个文件并运行 2 条命令。');
    expect(screen.getByText('这是整理后的回答。')).toBeInTheDocument();
  });

  it('does not invent elapsed time for completed thinking records without an end timestamp', async () => {
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{
          id: 'thinking-without-end',
          kind: 'thinking',
          text: '完成态缺少结束时间。',
          done: true,
          ts: '2026-05-30T00:00:00Z',
        }],
      },
    });

    render(<App />);

    const traces = await screen.findAllByLabelText('AI 思考记录');
    const trace = traces.find((node) => node.textContent.includes('完成态缺少结束时间。'));
    expect(trace).toBeTruthy();
    expect(trace).toHaveTextContent('已处理');
    expect(trace).not.toHaveTextContent(/已处理 \d+[sm]/);
  });

  it('does not show noisy zero-second elapsed time for completed thinking records', async () => {
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{
          id: 'thinking-zero-duration',
          kind: 'thinking',
          text: '完成态小于一秒。',
          done: true,
          ts: '2026-05-30T00:00:00Z',
          completedAt: '2026-05-30T00:00:00Z',
        }],
      },
    });

    render(<App />);

    const traces = await screen.findAllByLabelText('AI 思考记录');
    const trace = traces.find((node) => node.textContent.includes('完成态小于一秒。'));
    expect(trace).toBeTruthy();
    expect(trace).toHaveTextContent('已处理');
    expect(trace).not.toHaveTextContent('已处理 0s');
  });

  it('uses numeric unix timestamps for thinking elapsed time instead of dropping them', async () => {
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{
          id: 'thinking-numeric-time',
          kind: 'thinking',
          text: '使用后端数值时间。',
          done: true,
          ts: 1000,
          completedAt: 1003,
        }],
      },
    });

    render(<App />);

    const traces = await screen.findAllByLabelText('AI 思考记录');
    const trace = traces.find((node) => node.textContent.includes('使用后端数值时间。'));
    expect(trace).toBeTruthy();
    expect(trace).toHaveTextContent('已处理 AI 思考 3s');
  });

  it('uses backend-provided thinking duration when timestamps are incomplete', async () => {
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{
          id: 'thinking-duration-ms',
          kind: 'thinking',
          text: '使用后端耗时。',
          done: true,
          ts: '2026-05-30T00:00:00Z',
          elapsedMs: 2300,
        }],
      },
    });

    render(<App />);

    const traces = await screen.findAllByLabelText('AI 思考记录');
    const trace = traces.find((node) => node.textContent.includes('使用后端耗时。'));
    expect(trace).toBeTruthy();
    expect(trace).toHaveTextContent('已处理 AI 思考 2s');
  });

  it('shows tool execution details inside the AI processing frame', async () => {
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{
          id: 'tool-file-read',
          kind: 'tool',
          title: 'file.open',
          status: 'completed',
          text: '读取 frontend-app/src/App.jsx，定位 ReasoningTrace。',
          done: true,
          ts: '2026-05-30T00:00:00Z',
          completedAt: '2026-05-30T00:00:03Z',
        }, {
          id: 'assistant-after-tool',
          kind: 'assistant',
          text: '工具结果已整理。',
          ts: '2026-05-30T00:00:04Z',
        }],
      },
    });

    render(<App />);

    const trace = await screen.findByLabelText('AI 思考记录');
    expect(trace).toHaveClass('reasoning-message');
    expect(trace).not.toHaveClass('message');
    expect(trace).not.toHaveClass('assistant');
    expect(trace).toHaveTextContent('已处理 file.open 3s');
    const step = within(trace).getByLabelText('工具步骤');
    expect(step).toHaveTextContent('读取 frontend-app/src/App.jsx');
    expect(screen.getByText('工具结果已整理。')).toBeInTheDocument();
  });

  it('shows active agent timeline tool cards when timeline state is keyed by agent id', async () => {
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', agentId: 'agent-1', name: 'Thread 1', provider: 'codex', status: 'running' }],
    });

    render(<App />);

    await findThreadCardByName('Thread 1');

    act(() => {
      useClientStore.setState((state) => ({
        activeThreadId: 'thread-1',
        threads: [{ id: 'thread-1', agentId: 'agent-1', name: 'Thread 1', provider: 'codex', status: 'running' }],
        timelinesByThread: {
          ...state.timelinesByThread,
          'agent-1': [{
            id: 'tool-agent-keyed',
            kind: 'tool',
            title: 'file',
            status: 'completed',
            text: 'agent keyed tool result',
            done: true,
            ts: '2026-05-30T00:00:00Z',
          }],
        },
        threadTimelineReadyByThread: {
          ...state.threadTimelineReadyByThread,
          'agent-1': true,
        },
        threadStateLoadingByThread: {},
      }));
    });

    const trace = await screen.findByLabelText('AI 思考记录');
    expect(trace).toHaveTextContent('agent keyed tool result');
  });

  it('hides ghost command timeline cards from the conversation body', async () => {
    render(<App />);

    await waitForBackendThreadHeading();

    act(() => {
      useClientStore.setState((state) => ({
        timelinesByThread: {
          ...state.timelinesByThread,
          'thread-1': [{
            id: 'ghost-command',
            kind: 'command',
            title: '执行命令',
            status: 'completed',
            done: true,
          }, {
            id: 'assistant-after-ghost',
            role: 'assistant',
            kind: 'assistant',
            text: '正常回复',
            time: '2026-05-30T00:00:00Z',
          }],
        },
        threadTimelineReadyByThread: {
          ...state.threadTimelineReadyByThread,
          'thread-1': true,
        },
      }));
    });

    expect(await screen.findByText('正常回复')).toBeInTheDocument();
    expect(screen.queryByText('执行命令')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('AI 思考记录')).not.toBeInTheDocument();
  });

  it('coalesces running and completed lifecycle events for the same tool call', async () => {
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{
          id: 'tool-file-running',
          kind: 'tool',
          title: 'file',
          status: 'running',
          call_id: 'call-file-1',
          done: false,
          ts: '2026-05-30T00:00:00Z',
        }, {
          id: 'tool-file-completed',
          kind: 'tool',
          title: 'file',
          status: 'completed',
          call_id: 'call-file-1',
          text: '{\n  "success": true\n}',
          done: true,
          ts: '2026-05-30T00:00:00Z',
          completedAt: '2026-05-30T00:00:01Z',
        }],
      },
    });

    render(<App />);

    const traces = await screen.findAllByLabelText('AI 思考记录');
    const fileTraces = traces.filter((node) => node.textContent.includes('success'));
    expect(fileTraces).toHaveLength(1);
    expect(fileTraces[0]).toHaveTextContent('已处理 file 1s');
    expect(fileTraces[0]).toHaveTextContent('"success": true');
    expect(within(fileTraces[0]).getByLabelText('工具步骤')).toHaveTextContent('"success": true');
    expect(fileTraces[0]).not.toHaveTextContent('正在调用工具并等待返回结果。');
  });

  it('does not append a pending thinking placeholder after completed processing activity', async () => {
    resetClientStoreForTests({
      bootstrapStatus: 'ready',
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: '后端线程', provider: 'codex', status: 'running' }],
      activeTurnByThread: {
        'thread-1': { id: 'turn-running', threadId: 'thread-1', status: 'running', startedAt: '2026-05-30T00:00:00Z' },
      },
      timelinesByThread: {
        'thread-1': [{
          id: 'user-waiting',
          role: 'user',
          kind: 'user',
          text: '请生成架构图',
          time: '2026-05-30T00:00:00Z',
        }, {
          id: 'tool-file-completed',
          role: 'assistant',
          kind: 'tool',
          title: 'file',
          status: 'completed',
          text: '读取文件完成。',
          done: true,
          time: '2026-05-30T00:00:01Z',
          completedAt: '2026-05-30T00:00:02Z',
        }],
      },
      threadTimelineReadyByThread: { 'thread-1': true },
      threadStateLoadingByThread: {},
    });

    render(<App skipBootstrap />);

    await act(async () => {
      await Promise.resolve();
    });
    const traces = screen.getAllByLabelText('AI 思考记录');
    expect(traces).toHaveLength(1);
    expect(traces[0]).toHaveTextContent('读取文件完成。');
    expect(traces[0]).not.toHaveTextContent('正在处理请求');
  });

  it('renders AI execution plans as checklist details in the processing frame', async () => {
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{
          id: 'plan-1',
          kind: 'plan',
          title: '执行计划',
          status: 'running',
          done: false,
          text: [
            '并行审查前端和后端代码',
            '✅ 1. 读取当前前端代码',
            '🔄 2. 修复项目选择器重复展示',
            '⏳ 3. 隐藏注入提示词',
          ].join('\n'),
          ts: '2026-05-30T00:00:00Z',
        }],
      },
    });

    render(<App />);

    const plan = await screen.findByLabelText('AI 执行计划');
    expect(plan).toHaveTextContent('执行计划');
    expect(plan).toHaveTextContent('已完成 1/3 项任务');
    expect(within(plan).getByText('读取当前前端代码')).toBeInTheDocument();
    expect(within(plan).getByText('修复项目选择器重复展示')).toBeInTheDocument();
    expect(within(plan).getByText('隐藏注入提示词')).toBeInTheDocument();
    const list = within(plan).getByRole('list');
    expect(list.tagName).toBe('OL');
    expect(list).toHaveClass('execution-plan-list');
    const items = within(list).getAllByRole('listitem');
    expect(items).toHaveLength(3);
    expect(items[0]).toHaveAttribute('data-plan-status', 'done');
    expect(items[1]).toHaveAttribute('data-plan-status', 'pending');
  });

  it('shows an active thinking placeholder while a turn is running before output arrives', async () => {
    backend.getSidebarState.mockResolvedValueOnce({
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: '后端线程', provider: 'codex', status: 'running' }],
      active_turn: { id: 'turn-running', thread_id: 'thread-1', status: 'running', started_at: '2026-05-30T00:00:00Z' },
    });
    backend.getThreadState.mockResolvedValueOnce({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{ id: 'user-waiting', kind: 'user', text: '请生成架构图', ts: '2026-05-30T00:00:00Z' }],
      },
    });

    render(<App />);

    expect(await screen.findByLabelText('AI 思考记录')).toHaveTextContent(/正在思考 \d+[sm]/);
    expect(screen.getByLabelText('AI 思考记录')).toHaveTextContent('AI 正在分析上下文、选择工具并整理回答。');
  });

  it('shows a non-timed preparation status before the first real turn starts', async () => {
    backend.getSidebarState.mockResolvedValue({ activeThreadId: '', threads: [] });
    backend.getThreadState.mockResolvedValue({ timelinesByThread: {} });
    backend.startThread.mockResolvedValue({ thread: { id: 'thread-new' } });
    const startTurnDeferred = deferred();
    backend.startTurn.mockReturnValue(startTurnDeferred.promise);

    render(<App />);

    await screen.findByText('我们应该在 燧元 中构建什么？');
    fireEvent.change(screen.getByTestId('composer-input'), {
      target: { value: '请真正调用后端聊天' },
    });
    fireEvent.click(screen.getByLabelText('发送消息'));

    await waitFor(() => expect(backend.startTurn).toHaveBeenCalled());
    const preparingTrace = screen.getByLabelText('AI 思考记录');
    expect(preparingTrace).toHaveTextContent('正在准备响应');
    expect(preparingTrace).not.toHaveTextContent('正在思考');
    expect(preparingTrace).not.toHaveTextContent('0s');

    act(() => {
      bridgeCallback({
        type: 'ui/thread/patch',
        payload: {
          threadId: 'thread-new',
          sequence: '1',
          activeTurn: {
            id: 'turn-live',
            threadId: 'thread-new',
            status: 'running',
            startedAt: '2026-05-30T00:00:00Z',
          },
        },
      });
    });

    await waitFor(() => expect(screen.getByLabelText('AI 思考记录')).toHaveTextContent(/正在思考 \d+[sm]/));

    await act(async () => {
      startTurnDeferred.resolve({ ok: true });
      await Promise.resolve();
    });
  });

  it('updates active thinking elapsed time in place every second', async () => {
    await import('./pages/chat/ChatPage.jsx').then(() => vi.useFakeTimers());
    try {
      vi.setSystemTime(new Date('2026-05-30T00:00:00Z'));
      resetClientStoreForTests({
        bootstrapStatus: 'ready',
        cwd: '/repo/app',
        activeProject: '/repo/app',
        activeThreadId: 'thread-1',
        timelinesByThread: {
          'thread-1': [{
            id: 'thinking-live',
            role: 'assistant',
            kind: 'thinking',
            title: 'grep',
            text: '正在搜索。',
            time: '2026-05-30T00:00:00Z',
            done: false,
          }],
        },
      });

      render(<App skipBootstrap />);

      await act(async () => {
        await Promise.resolve();
        await Promise.resolve();
      });

      const trace = screen.getByLabelText('AI 思考记录');
      expect(trace).toHaveTextContent('正在思考 0s');

      await act(async () => {
        await vi.advanceTimersByTimeAsync(2100);
      });

      expect(trace).toHaveTextContent('正在思考 2s');
    }
    finally {
      vi.useRealTimers();
    }
  });

  it('renders runtime tool details with long names in a shrink-safe structure', async () => {
    const longToolName = 'mcp__very_long_server_name_that_would_overflow__deeply_nested_tool_name_with_many_segments';
    backend.getSidebarState.mockResolvedValueOnce({
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: '后端线程', provider: 'codex', status: 'running' }],
      activityStatsByThread: {
        'thread-1': {
          toolCalls: { [longToolName]: 3 },
        },
      },
    });

    const { container } = render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));

    const toolStat = screen.getByRole('button', { name: '工具调用总数' });
    expect(toolStat).not.toHaveAttribute('title');
    fireEvent.mouseEnter(toolStat);
    expect(screen.queryByTestId('runtime-stat-tooltip')).not.toBeInTheDocument();
    fireEvent.click(toolStat);

    const tooltip = await screen.findByTestId('runtime-stat-tooltip');
    expect(tooltip).toHaveTextContent('deeply_nested_tool_name_with_many_segments');
    expect(tooltip.querySelector('.runtime-stat-tooltip-row')).toBeInTheDocument();
    expect(tooltip.querySelector('.runtime-stat-tooltip-name')).not.toHaveAttribute('title');
    expect(container.querySelector('.runtime-panel')).toHaveClass('runtime-panel');
  });

  it('sets the chat composer textarea to three visible rows', async () => {
    render(<App />);
    await waitForBackendThreadHeading();

    const composer = screen.getByRole('combobox', { name: '输入给 Agent 的内容' });
    expect(composer).toHaveAttribute('rows', '3');
    expect(composer).toHaveAttribute('placeholder', '随心输入');
  });

  it('does not render a desktop titlebar inside the workbench shell', async () => {
    const { container } = render(<App />);

    expect(await waitForBackendThreadHeading()).toBeInTheDocument();
    expect(container.querySelector('.traffic-lights')).toBeNull();
    expect(container.querySelectorAll('.titlebar')).toHaveLength(0);
    expect(within(screen.getByTestId('app-sidebar')).getByText('燧元')).toBeInTheDocument();
    expect(screen.getByTestId('suiyuan-brand-light-logo')).toBeInTheDocument();
    expect(screen.getByTestId('suiyuan-brand-dark-logo')).toBeInTheDocument();
    expect(within(screen.getByTestId('app-sidebar')).getByRole('button', { name: '新对话' }).querySelector('.lucide-plus')).toBeInTheDocument();
    expect(within(screen.getByTestId('app-sidebar')).getByRole('button', { name: '聊天页面' }).querySelector('.lucide-message-square-text')).toBeInTheDocument();
    expect(within(screen.getByTestId('app-sidebar')).getByRole('button', { name: '自动化' }).querySelector('.lucide-sliders-horizontal')).toBeInTheDocument();
    expect(within(screen.getByTestId('app-sidebar')).getByRole('button', { name: '链路追踪' }).querySelector('.lucide-database')).toBeInTheDocument();
  });

  it('keeps the user message visible and calls thread/start before turn/start for a new chat', async () => {
    backend.getSidebarState.mockResolvedValue({ activeThreadId: '', threads: [] });
    backend.getThreadState.mockResolvedValue({ timelinesByThread: {} });
    backend.startThread.mockResolvedValue({ thread: { id: 'thread-new' } });
    backend.startTurn.mockResolvedValue({ ok: true });

    render(<App />);

    await screen.findByText('我们应该在 燧元 中构建什么？');
    expect(screen.queryByTestId('composer-project')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('发送权限')).not.toBeInTheDocument();
    fireEvent.change(screen.getByTestId('composer-input'), {
      target: { value: '请真正调用后端聊天' },
    });
    fireEvent.click(screen.getByLabelText('发送消息'));

    await waitFor(() => {
      expect(backend.startThread).toHaveBeenCalledBefore(backend.startTurn);
      expect(backend.startTurn).toHaveBeenCalledWith({
        cwd: '/repo/app',
        threadId: 'thread-new',
        input: [{ type: 'text', text: '请真正调用后端聊天' }],
        manualSkillSelection: false,
      });
    });
    const startPayload = backend.startThread.mock.calls[0][0];
    expect(startPayload).not.toHaveProperty('prompt');
    expect(startPayload).not.toHaveProperty('optimisticUserMessage');
    expect(startPayload).not.toHaveProperty('skipInitialRuntimeSync');
    expect(startPayload.config).toEqual({
      codexHome: '~/.codex',
      codexInstanceKey: 'default',
      codexModelProvider: 'openai',
    });

    expect(screen.getAllByText('请真正调用后端聊天').length).toBeGreaterThanOrEqual(1);
  });

  it('renders the inherited timeline used by fork drafts', async () => {
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [
          { id: 'user-1', kind: 'user', text: '原始需求：补齐工作台能力' },
          { id: 'assistant-1', kind: 'assistant', text: '阶段结论：先迁移 fork draft 链路' },
        ],
      },
    });

    render(<App />);

    expect(await screen.findByText('阶段结论：先迁移 fork draft 链路')).toBeInTheDocument();
  });

  it('opens a fork draft card from the chat composer and submits an inherited thread', async () => {
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [
          { id: 'user-1', kind: 'user', text: '原始需求：补齐工作台能力' },
          { id: 'assistant-1', kind: 'assistant', text: '阶段结论：先迁移 fork draft 链路' },
        ],
      },
    });
    backend.listSharedFiles.mockResolvedValue({
      files: [{ path: 'reports/final.md' }],
      finalOutputRefs: [],
      sharedFileRetention: { items: [], protectedCount: 0, cleanupCandidateCount: 0 },
    });
    backend.forkThread.mockResolvedValue({
      thread: { id: 'thread-fork', forkedFrom: 'thread-1' },
      kickoffState: 'created_only',
    });
    backend.startTurn.mockResolvedValue({ ok: true });

    render(<App />);

    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByRole('button', { name: '聊天操作' }));
    fireEvent.click(await screen.findByRole('menuitem', { name: '继承当前对话' }));

    const card = await screen.findByTestId('fork-draft-card');
    expect(card).toHaveTextContent('继承自会话：后端线程');
    fireEvent.click(within(card).getByLabelText('选择共享文件 reports/final.md'));
    fireEvent.click(within(card).getByRole('button', { name: '创建继承对话' }));

    await waitFor(() => {
      expect(backend.forkThread).toHaveBeenCalledWith({ threadId: 'thread-1' });
    });
    expect(backend.startThread).not.toHaveBeenCalled();
    expect(backend.startTurn).toHaveBeenCalledWith({
      cwd: '/repo/app',
      threadId: 'thread-fork',
      input: [
        { type: 'text', text: '请基于已继承的完整对话历史，简要总结当前进展并提出下一步建议。' },
        {
          type: 'filecontent',
          path: 'reports/final.md',
          name: 'reports/final.md',
          content: 'content for reports/final.md',
        },
      ],
      manualSkillSelection: false,
    });
  });

  it('opens a fork draft from the context usage warning banner', async () => {
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: '后端线程', provider: 'codex', status: '工作中' }],
      active_turn: { id: 'turn-1', thread_id: 'thread-1', status: 'running' },
      tokenUsageByThread: {
        'thread-1': { usedTokens: 920, contextWindowTokens: 1000, usedPercent: 92 },
      },
    });
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [
          { id: 'user-1', kind: 'user', text: '上下文快满了' },
          { id: 'assistant-1', kind: 'assistant', text: '建议新建继承会话' },
        ],
      },
    });
    backend.listSharedFiles.mockResolvedValue({
      files: [{ path: 'reports/final.md' }],
      finalOutputRefs: [],
      sharedFileRetention: { items: [], protectedCount: 0, cleanupCandidateCount: 0 },
    });

    render(<App />);

    await screen.findByText('建议新建继承会话');
    const banner = await screen.findByTestId('context-usage-banner');
    expect(banner.tagName).toBe('OUTPUT');
    expect(banner).toHaveTextContent('上下文使用率');
    expect(banner).toHaveTextContent('92%');
    fireEvent.click(within(banner).getByRole('button', { name: '新建继承会话' }));

    const card = await screen.findByTestId('fork-draft-card');
    expect(card).toHaveTextContent('继承自会话：后端线程');
  });

  it('sends the composer draft when plain Enter is pressed inside the textarea', async () => {
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: '后端线程', provider: 'codex', status: 'idle' }],
    });
    backend.startTurn.mockResolvedValue({ ok: true });
    render(<App />);

    await waitForBackendThreadHeading();
    const input = screen.getByTestId('composer-input');
    fireEvent.change(input, {
      target: { value: '普通 Enter 发送' },
    });

    expect(fireEvent.keyDown(input, { key: 'Enter', code: 'Enter', isComposing: false })).toBe(false);

    expect(backend.startThread).not.toHaveBeenCalled();
    await waitFor(() => {
      expect(backend.startTurn).toHaveBeenCalledWith({
        cwd: '/repo/app',
        threadId: 'thread-1',
        input: [{ type: 'text', text: '普通 Enter 发送' }],
        manualSkillSelection: false,
      });
    });
  });

  it('does not send the composer draft when Enter confirms IME composition', async () => {
    render(<App />);

    await waitForBackendThreadHeading();
    const input = screen.getByTestId('composer-input');
    fireEvent.change(input, {
      target: { value: '拼音候选' },
    });

    expect(fireEvent.keyDown(input, {
      key: 'Process',
      code: 'Enter',
      keyCode: 229,
      which: 229,
      isComposing: true,
    })).toBe(true);

    expect(backend.startTurn).not.toHaveBeenCalled();
    expect(input).toHaveValue('拼音候选');
  });

  it('floats the composer in the intro state and docks it after the first message', async () => {
    backend.getSidebarState.mockResolvedValue({ activeThreadId: '', threads: [] });
    backend.getThreadState.mockResolvedValue({ timelinesByThread: {} });
    backend.startThread.mockResolvedValue({ thread: { id: 'thread-new' } });
    backend.startTurn.mockResolvedValue({ ok: true });

    const { container } = render(<App />);

    await screen.findByText('我们应该在 燧元 中构建什么？');
    expect(screen.getByTestId('composer-dock')).toHaveClass('composer', 'composer--floating');
    expect(screen.getByTestId('chat-timeline')).toContainElement(screen.getByTestId('composer-dock'));
    expect(container.querySelector('.work-status')).toBeNull();

    fireEvent.change(screen.getByTestId('composer-input'), {
      target: { value: '让输入框下沉到底部' },
    });
    fireEvent.click(screen.getByLabelText('发送消息'));

    await waitFor(() => {
      expect(screen.getByTestId('composer-dock')).toHaveClass('composer', 'composer--docked');
    });
    expect(screen.getByTestId('composer-dock')).not.toHaveClass('composer--floating');
    expect(screen.getByTestId('chat-timeline')).not.toContainElement(screen.getByTestId('composer-dock'));
    expect(container.querySelector('.work-status')).toBeNull();
  });

  it('starts with only the chat rail and conversation, then toggles the right sidebar from the toolbar', async () => {
    const { container } = render(<App />);
    const restoredRightPanelWidth = Math.min(
      rightPanelWidthSchema.initialValue,
      rightPanelMaxWidth(window.innerWidth, threadRailTargetWidth(window.innerWidth)),
    );

    await waitForBackendThreadHeading();
    const layout = screen.getByTestId('chat-layout');

    expect(screen.queryByTestId('runtime-panel')).not.toBeInTheDocument();
    expect(screen.queryByTestId('right-panel-resizer')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: '显示侧边栏' })).toBeInTheDocument();
    expect(layout).toHaveStyle({ gridTemplateColumns: 'minmax(0, 1fr)' });

    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));

    expect(screen.getByTestId('runtime-panel')).toBeInTheDocument();
    expect(screen.getByTestId('right-panel-resizer')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '隐藏侧边栏' })).toBeInTheDocument();
    expect(within(container.querySelector('.runtime-panel')).getByRole('button', { name: '折叠 file' })).toBeInTheDocument();
    expect(container.querySelector('.runtime-panel')).not.toHaveTextContent('diff --git a/file b/file');
    expect(screen.getByRole('list', { name: '工具调用统计' })).toBeInTheDocument();
    expect(screen.queryByTestId('warning-log-panel')).not.toBeInTheDocument();
    expect(layout).toHaveStyle({
      gridTemplateColumns: `minmax(0, 1fr) 6px ${restoredRightPanelWidth}px`,
    });
  });

  it('supports keyboard resizing for chat and activity resizer controls', async () => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1400 });
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: 640 });
    const storage = createShellLayoutStorage(String(rightPanelWidthSchema.initialValue));

    render(<App shellLayoutStorage={storage} />);

    await waitForBackendThreadHeading();
    const layout = screen.getByTestId('chat-layout');
    const leftResizer = screen.getByRole('separator', { name: '调整会话栏宽度' });
    expect(leftResizer.tagName).toBe('BUTTON');

    expect(leftResizer).toHaveAttribute('aria-valuenow', '264');

    fireEvent.keyDown(leftResizer, { key: 'ArrowLeft' });

    expect(leftResizer).toHaveAttribute('aria-valuenow', '248');
    expect(layout).toHaveStyle({ gridTemplateColumns: 'minmax(0, 1fr)' });

    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));
    let rightResizer = screen.getByRole('separator', { name: '调整侧边栏宽度' });
    expect(rightResizer.tagName).toBe('BUTTON');

    const rightPanelMaximum = rightPanelMaxWidth(window.innerWidth, 248);
    const restoredWidth = Math.min(rightPanelWidthSchema.initialValue, rightPanelMaximum);
    expect(rightResizer).toHaveAttribute('aria-valuenow', String(restoredWidth));
    expect(storage.value()).toBe(String(rightPanelWidthSchema.initialValue));

    fireEvent.keyDown(rightResizer, { key: 'ArrowLeft' });

    const arrowWidth = Number(rightResizer.getAttribute('aria-valuenow'));
    expect(arrowWidth).toBeGreaterThan(restoredWidth);
    expect(storage.value()).toBe(String(arrowWidth));
    expect(layout).toHaveStyle({ gridTemplateColumns: `minmax(0, 1fr) 6px ${arrowWidth}px` });

    fireEvent.keyDown(rightResizer, { key: 'Home' });

    expect(storage.value()).toBe('0');
    expect(screen.queryByTestId('runtime-panel')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));
    rightResizer = screen.getByRole('separator', { name: '调整侧边栏宽度' });
    const defaultWidth = Math.min(rightPanelDefaultWidth(window.innerWidth), rightPanelMaximum);
    expect(rightResizer).toHaveAttribute('aria-valuenow', String(defaultWidth));
    expect(storage.value()).toBe(String(defaultWidth));

    fireEvent.keyDown(rightResizer, { key: 'End' });

    expect(rightResizer).toHaveAttribute('aria-valuenow', String(rightPanelMaximum));
    expect(storage.value()).toBe(String(rightPanelMaximum));

    fireEvent.click(screen.getByRole('button', { name: '隐藏侧边栏' }));
    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));
    rightResizer = screen.getByRole('separator', { name: '调整侧边栏宽度' });
    expect(rightResizer).toHaveAttribute('aria-valuenow', String(rightPanelMaximum));
    expect(storage.value()).toBe(String(rightPanelMaximum));
    expect(storage.set).toHaveBeenCalledTimes(4);

    const activityResizer = screen.getByRole('separator', { name: '调整工具使用面板高度' });
    expect(activityResizer.tagName).toBe('BUTTON');

    expect(activityResizer).toHaveAttribute('aria-valuenow', '64');

    fireEvent.keyDown(activityResizer, { key: 'ArrowUp' });

    expect(activityResizer).toHaveAttribute('aria-valuenow', '80');
    expect(screen.getByTestId('runtime-panel')).toHaveStyle({ '--activity-panel-height': '80px' });
  });

  it('opens the right sidebar at one fifth on wide screens', async () => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1980 });

    render(<App />);
    await waitForBackendThreadHeading();

    const layout = screen.getByTestId('chat-layout');

    expect(layout).toHaveStyle({ gridTemplateColumns: 'minmax(0, 1fr)' });

    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));

    expect(layout).toHaveStyle({ gridTemplateColumns: 'minmax(0, 1fr) 6px 380px' });
  });

  it('clamps a persisted panel width on viewport shrink and keeps the committed clamp when it grows', async () => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1400 });
    const persistedWidth = 480.5;
    const storage = createShellLayoutStorage(String(persistedWidth));

    render(<App shellLayoutStorage={storage} />);
    await waitForBackendThreadHeading();

    const layout = screen.getByTestId('chat-layout');

    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));
    expect(layout).toHaveStyle({ gridTemplateColumns: `minmax(0, 1fr) 6px ${persistedWidth}px` });
    expect(storage.set).not.toHaveBeenCalled();

    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1024 });
    act(() => {
      window.dispatchEvent(new Event('resize'));
    });

    const clampedWidth = rightPanelMaxWidth(
      window.innerWidth,
      threadRailTargetWidth(window.innerWidth),
    );
    await waitFor(() => {
      expect(layout).toHaveStyle({ gridTemplateColumns: `minmax(0, 1fr) 6px ${clampedWidth}px` });
      expect(storage.value()).toBe(String(clampedWidth));
    });
    expect(storage.set).toHaveBeenCalledExactlyOnceWith(
      rightPanelWidthSchema.key,
      String(clampedWidth),
    );

    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1980 });
    act(() => {
      window.dispatchEvent(new Event('resize'));
    });

    await waitFor(() => {
      expect(layout).toHaveStyle({ gridTemplateColumns: `minmax(0, 1fr) 6px ${clampedWidth}px` });
    });
    expect(storage.value()).toBe(String(clampedWidth));
    expect(storage.set).toHaveBeenCalledTimes(1);
  });

  it('lets the right sidebar grow toward two fifths while preserving two fifths for conversation', async () => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1980 });

    render(<App />);
    await waitForBackendThreadHeading();

    const layout = screen.getByTestId('chat-layout');

    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));
    const rightResizer = screen.getByTestId('right-panel-resizer');

    dispatchPointer(rightResizer, 'pointerdown', 1100);
    dispatchPointer(window, 'pointermove', 500);
    dispatchPointer(window, 'pointerup', 500);

    await waitFor(() => {
      expect(layout).toHaveStyle({ gridTemplateColumns: 'minmax(0, 1fr) 6px 751px' });
    });
  });

  it('keeps right sidebar drag updates local until the pointer is released', async () => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1980 });
    const storage = createShellLayoutStorage('380');

    render(<App shellLayoutStorage={storage} />);
    await waitForBackendThreadHeading();

    const layout = screen.getByTestId('chat-layout');

    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));
    const rightResizer = screen.getByTestId('right-panel-resizer');

    expect(storage.value()).toBe('380');

    dispatchPointer(rightResizer, 'pointerdown', 1100);
    dispatchPointer(window, 'pointermove', 700);

    expect(layout).toHaveStyle({ gridTemplateColumns: 'minmax(0, 1fr) 6px 751px' });
    expect(storage.value()).toBe('380');

    dispatchPointer(window, 'pointerup', 700);

    expect(storage.value()).toBe('751');
  });

  it('stops right sidebar resizing when the pointer is no longer pressed', async () => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1980 });
    const storage = createShellLayoutStorage('380');

    render(<App shellLayoutStorage={storage} />);
    await waitForBackendThreadHeading();

    const layout = screen.getByTestId('chat-layout');

    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));
    const rightResizer = screen.getByTestId('right-panel-resizer');

    dispatchPointer(rightResizer, 'pointerdown', 1100);
    dispatchPointer(window, 'pointermove', 1000);
    expect(layout).toHaveStyle({ gridTemplateColumns: 'minmax(0, 1fr) 6px 480px' });

    dispatchPointer(window, 'pointermove', 700, { buttons: 0 });
    expect(layout).toHaveStyle({ gridTemplateColumns: 'minmax(0, 1fr) 6px 480px' });
    expect(storage.value()).toBe('480');

    dispatchPointer(window, 'pointermove', 500, { buttons: 0 });
    expect(layout).toHaveStyle({ gridTemplateColumns: 'minmax(0, 1fr) 6px 480px' });
  });

  it('keeps the right sidebar draggable past the previous early close width', async () => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1980 });
    const storage = createShellLayoutStorage('380');

    render(<App shellLayoutStorage={storage} />);
    await waitForBackendThreadHeading();

    const layout = screen.getByTestId('chat-layout');

    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));
    const rightResizer = screen.getByTestId('right-panel-resizer');

    dispatchPointer(rightResizer, 'pointerdown', 1100);
    dispatchPointer(window, 'pointermove', 1330);

    expect(screen.getByTestId('runtime-panel')).toBeInTheDocument();
    expect(screen.getByTestId('right-panel-resizer')).toBeInTheDocument();
    expect(layout).toHaveStyle({ gridTemplateColumns: 'minmax(0, 1fr) 6px 150px' });

    dispatchPointer(window, 'pointerup', 1330);

    expect(storage.value()).toBe('150');
  });

  it('closes the right sidebar when dragged flush to the right edge', async () => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1980 });
    const storage = createShellLayoutStorage('380');

    render(<App shellLayoutStorage={storage} />);
    await waitForBackendThreadHeading();

    const layout = screen.getByTestId('chat-layout');

    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));
    const rightResizer = screen.getByTestId('right-panel-resizer');

    dispatchPointer(rightResizer, 'pointerdown', 1100);
    dispatchPointer(window, 'pointermove', 1480);

    expect(screen.queryByTestId('runtime-panel')).not.toBeInTheDocument();
    expect(screen.queryByTestId('right-panel-resizer')).not.toBeInTheDocument();
    expect(layout).toHaveStyle({ gridTemplateColumns: 'minmax(0, 1fr)' });
    expect(storage.value()).toBe('0');
  });

  it('isolates right sidebar diff, warnings, and tool stats to the selected agent', async () => {
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: 'thread-a',
      threads: [
        { id: 'thread-a', agentId: 'agent-a', name: 'Agent A', provider: 'codex', status: 'running' },
        { id: 'thread-b', agentId: 'agent-b', name: 'Agent B', provider: 'codex', status: 'running' },
      ],
      activityStatsByThread: {
        'agent-a': { lspCalls: 1, commands: 0, fileEdits: 1, toolCalls: { edit: 1 } },
        'agent-b': { lspCalls: 7, commands: 0, fileEdits: 0, toolCalls: { shell: 7 } },
      },
      diffTextByThread: {
        'agent-a': 'diff --git a/a b/a',
        'agent-b': 'diff --git a/b b/b',
      },
    });
    backend.getThreadState.mockImplementation(({ threadId }) => Promise.resolve({
      activeThreadId: threadId,
      timelinesByThread: { [threadId]: [{ id: `assistant-${threadId}`, kind: 'assistant', text: `${threadId} ready` }] },
    }));

    render(<App />);
    await findThreadCardByName('Agent A');

    act(() => {
      bridgeCallback({
        type: 'thread.send/failed',
        payload: { method: 'turn/start', agentId: 'agent-a', error: 'a failed' },
      });
      bridgeCallback({
        type: 'bridge.call/failed',
        payload: { method: 'turn/start', agentId: 'agent-b', error: 'b failed' },
      });
      bridgeCallback({
        type: 'api.rpc.failed',
        payload: { method: 'thread/config/get', error: 'global failed' },
      });
    });

    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));
    expect(screen.queryByTestId('warning-log-panel')).not.toBeInTheDocument();
    fireEvent.keyDown(screen.getByTestId('activity-panel-resizer'), { key: 'ArrowUp' });

    expect(within(screen.getByTestId('runtime-panel')).getByRole('button', { name: '折叠 a' })).toBeInTheDocument();
    expect(screen.getByTestId('runtime-panel')).not.toHaveTextContent('diff --git a/a b/a');
    expect(screen.getByTestId('runtime-panel')).not.toHaveTextContent('diff --git a/b b/b');
    expect(screen.getByLabelText('LSP (8 tools) 调用次数')).toHaveTextContent('1');
    expect(screen.getByTestId('warning-log-panel')).toHaveTextContent('thread.send/failed');
    expect(screen.getByTestId('warning-log-panel')).toHaveTextContent('api.rpc.failed');
    expect(screen.getByTestId('warning-log-panel')).not.toHaveTextContent('bridge.call/failed');

    clickThreadCardByName('Agent B');

    await waitFor(() => {
      expect(within(screen.getByTestId('runtime-panel')).getByRole('button', { name: '折叠 b' })).toBeInTheDocument();
      expect(screen.getByTestId('runtime-panel')).not.toHaveTextContent('diff --git a/a b/a');
      expect(screen.getByTestId('runtime-panel')).not.toHaveTextContent('diff --git a/b b/b');
      expect(screen.getByLabelText('LSP (8 tools) 调用次数')).toHaveTextContent('7');
      expect(screen.getByTestId('warning-log-panel')).toHaveTextContent('bridge.call/failed');
      expect(screen.getByTestId('warning-log-panel')).toHaveTextContent('api.rpc.failed');
      expect(screen.getByTestId('warning-log-panel')).not.toHaveTextContent('thread.send/failed');
    });
  });

  it('switches identity immediately but shields stale target-thread content while refreshing', async () => {
    let resolveThreadBState;
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: 'thread-a',
      threads: [
        { id: 'thread-a', name: 'Agent A', provider: 'codex', status: 'idle' },
        { id: 'thread-b', name: 'Agent B', provider: 'codex', status: 'idle' },
      ],
    });
    backend.getThreadState.mockImplementation(({ threadId }) => {
      if (threadId === 'thread-b') {
        return new Promise((resolve) => {
          resolveThreadBState = resolve;
        });
      }
      return Promise.resolve({
        activeThreadId: threadId,
        timelinesByThread: {
          [threadId]: [{ id: 'assistant-a', kind: 'assistant', text: 'Agent A ready' }],
        },
      });
    });

    render(<App />);
    await screen.findByText('Agent A ready');

    act(() => {
      useClientStore.setState((state) => ({
        timelinesByThread: {
          ...state.timelinesByThread,
          'thread-b': [{ id: 'stale-b', role: 'assistant', text: 'stale cached Agent B content' }],
        },
      }));
    });

    clickThreadCardByName('Agent B');

    await waitFor(() => expect(backend.getThreadState).toHaveBeenCalledWith({
      cwd: '/repo/app',
      threadId: 'thread-b',
      includeDiff: false,
    }));
    expect(useClientStore.getState().activeThreadId).toBe('thread-b');
    expect(useClientStore.getState().pendingActiveThreadId).toBe('');
    expect(useClientStore.getState().threadStateLoadingByThread['thread-b']).toBe(true);
    expect(getThreadCardByName('Agent B')).toHaveClass('active');
    expect(screen.queryByText('Agent A ready')).not.toBeInTheDocument();
    expect(screen.queryByText('stale cached Agent B content')).not.toBeInTheDocument();
    expect(screen.queryByText(/让我们从/)).not.toBeInTheDocument();
    expect(screen.getByTestId('timeline-loading-placeholder')).toHaveTextContent('正在同步会话历史');

    act(() => {
      resolveThreadBState({
        activeThreadId: 'thread-b',
        threads: [
          { id: 'thread-a', name: 'Agent A', provider: 'codex', status: 'idle' },
          { id: 'thread-b', name: 'Agent B', provider: 'codex', status: 'idle' },
        ],
        timelinesByThread: {
          'thread-b': [{ id: 'fresh-b', kind: 'assistant', text: 'fresh Agent B content' }],
        },
      });
    });

    await screen.findByText('fresh Agent B content');
    expect(useClientStore.getState().activeThreadId).toBe('thread-b');
    expect(useClientStore.getState().pendingActiveThreadId).toBe('');
    expect(screen.queryByText('Agent A ready')).not.toBeInTheDocument();
    expect(screen.queryByText('stale cached Agent B content')).not.toBeInTheDocument();
  });

  it('shows trusted cached target-thread history immediately while refreshing', async () => {
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: 'thread-a',
      threads: [
        { id: 'thread-a', name: 'Agent A', provider: 'codex', status: 'idle' },
        { id: 'thread-b', name: 'Agent B', provider: 'codex', status: 'idle' },
      ],
    });
    backend.getThreadState.mockImplementation(({ threadId }) => {
      if (threadId === 'thread-b') return new Promise(() => {});
      return Promise.resolve({
        activeThreadId: threadId,
        timelinesByThread: {
          [threadId]: [{ id: 'assistant-a', kind: 'assistant', text: 'Agent A ready' }],
        },
      });
    });
    backend.getThreadMessages.mockImplementation(({ threadId }) => {
      if (threadId === 'thread-b') return new Promise(() => {});
      return Promise.resolve({ messages: [] });
    });

    render(<App />);
    await screen.findByText('Agent A ready');

    act(() => {
      useClientStore.setState((state) => ({
        timelinesByThread: {
          ...state.timelinesByThread,
          'thread-b': [{ id: 'cached-b', role: 'assistant', text: 'cached Agent B content' }],
        },
        threadTimelineReadyByThread: {
          ...state.threadTimelineReadyByThread,
          'thread-b': true,
        },
      }));
    });

    clickThreadCardByName('Agent B');

    await waitFor(() => expect(backend.getThreadState).toHaveBeenCalledWith({
      cwd: '/repo/app',
      threadId: 'thread-b',
      includeDiff: false,
    }));
    expect(screen.getByText('cached Agent B content')).toBeInTheDocument();
    expect(screen.queryByText('Agent A ready')).not.toBeInTheDocument();
    expect(screen.queryByTestId('timeline-loading-placeholder')).not.toBeInTheDocument();
  });

  it('resizes the chat rail and right sidebar without crossing their minimum widths', async () => {
    render(<App />);
    await waitForBackendThreadHeading();

    const layout = screen.getByTestId('chat-layout');
    const leftResizer = screen.getByTestId('thread-rail-resizer');

    dispatchPointer(leftResizer, 'pointerdown', 280);
    dispatchPointer(window, 'pointermove', 40);
    dispatchPointer(window, 'pointerup', 40);

    expect(layout).toHaveStyle({ gridTemplateColumns: 'minmax(0, 1fr)' });

    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));
    const rightResizer = screen.getByTestId('right-panel-resizer');

    dispatchPointer(rightResizer, 'pointerdown', 1100);
    dispatchPointer(window, 'pointermove', 1500);
    dispatchPointer(window, 'pointerup', 1500);

    await waitFor(() => {
      expect(screen.queryByTestId('runtime-panel')).not.toBeInTheDocument();
      expect(layout).toHaveStyle({ gridTemplateColumns: 'minmax(0, 1fr)' });
    });
  });

  it('uses backend activity stats for the resizable tool usage panel', async () => {
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: 640 });

    render(<App />);
    await waitForBackendThreadHeading();

    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));

    expect(screen.getByTestId('runtime-panel')).toHaveStyle({
      '--activity-panel-height': '64px',
      '--activity-panel-min-height': '64px',
      '--activity-panel-max-height': '286px',
      '--diff-panel-min-height': '286px',
      '--diff-panel-max-height': '509px',
    });
    expect(screen.queryByTestId('warning-log-panel')).not.toBeInTheDocument();
    expect(screen.getByLabelText('LSP (8 tools) 调用次数')).toHaveTextContent('3');
    expect(screen.getByLabelText('LSP (8 tools) 调用次数')).not.toHaveAttribute('title');
    expect(screen.getByLabelText('工具调用总数')).toHaveTextContent('6');
    expect(screen.queryByText('edit:')).not.toBeInTheDocument();

    fireEvent.mouseEnter(screen.getByLabelText('LSP (8 tools) 调用次数'));
    expect(screen.queryByTestId('runtime-stat-tooltip')).not.toBeInTheDocument();
    fireEvent.click(screen.getByLabelText('LSP (8 tools) 调用次数'));
    expect(screen.getByTestId('runtime-stat-tooltip')).toHaveTextContent('LSP (8 tools)');
    expect(screen.getByTestId('runtime-stat-tooltip')).toHaveTextContent('edit');
    expect(screen.getByTestId('runtime-stat-tooltip')).toHaveTextContent('3');
    fireEvent.keyDown(screen.getByRole('dialog', { name: 'LSP (8 tools) 调用明细' }), { key: 'Escape' });
    await waitFor(() => {
      expect(screen.queryByTestId('runtime-stat-tooltip')).not.toBeInTheDocument();
    });

    fireEvent.mouseDown(screen.getByTestId('activity-panel-resizer'), { clientY: 500 });
    fireEvent.mouseMove(window, { clientY: 0 });
    fireEvent.mouseUp(window);

    await waitFor(() => {
      expect(screen.getByTestId('runtime-panel')).toHaveStyle({ '--activity-panel-height': '286px' });
    });
    expect(screen.getByTestId('warning-log-panel')).toBeInTheDocument();
  });

  it('shows tool return entries alongside warning lines in the runtime panel', async () => {
    render(<App />);
    await waitForBackendThreadHeading();

    act(() => {
      bridgeCallback({
        type: 'ui/thread/patch',
        payload: {
          threadId: 'thread-1',
          sequence: '9007199254740993124',
          timelineItems: [{
            id: 'tool-grep',
            kind: 'tool',
            tool: 'mcp__lsp__grep',
            status: 'completed',
            preview: '{"total":3}',
            output: 'src/App.jsx: runtime log result',
            ts: '2026-05-30T08:00:00Z',
          }],
        },
      });
      bridgeCallback({
        type: 'api.rpc.failed',
        payload: { method: 'thread/config/get', threadId: 'thread-1', error: 'backend unavailable' },
      });
    });

    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));
    fireEvent.keyDown(screen.getByTestId('activity-panel-resizer'), { key: 'ArrowUp' });

    const logPanel = screen.getByTestId('warning-log-panel');
    expect(logPanel).toHaveTextContent('api.rpc.failed');
    expect(logPanel).toHaveTextContent('grep');
    expect(logPanel).toHaveTextContent('返回');
    expect(logPanel).not.toHaveTextContent('{"total":3}');

    const resultLine = within(logPanel).getByRole('button', { name: /grep/ });
    fireEvent.mouseEnter(resultLine);
    expect(screen.queryByTestId('warning-log-popover')).not.toBeInTheDocument();
    fireEvent.click(resultLine);

    const popover = screen.getByTestId('warning-log-popover');
    expect(popover).toHaveTextContent('[redacted]');
    expect(popover).not.toHaveTextContent('src/App.jsx: runtime log result');
    expect(popover).not.toHaveTextContent('"preview": "{\\"total\\":3}"');
  });

  it('clamps right-edge runtime click details into the viewport', async () => {
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: 640 });

    render(<App />);
    await waitForBackendThreadHeading();

    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));

    const toolStat = screen.getByLabelText('工具调用总数');
    toolStat.getBoundingClientRect = () => ({
      x: 980,
      y: 580,
      left: 980,
      right: 1008,
      top: 580,
      bottom: 596,
      width: 28,
      height: 16,
      toJSON() {
        return this;
      },
    });

    fireEvent.click(toolStat);

    const tooltip = screen.getByTestId('runtime-stat-tooltip');
    expect(tooltip).toHaveTextContent('工具');
    expect(tooltip.style.getPropertyValue('--runtime-stat-tooltip-left')).toBe('652px');
    expect(tooltip.style.getPropertyValue('--runtime-stat-tooltip-bottom')).toBe('70px');
  });

  it('lets bottom-right runtime click details use the available vertical space', async () => {
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: 640 });
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: '后端线程', provider: 'codex', status: '工作中' }],
      active_turn: { id: 'turn-1', thread_id: 'thread-1', status: 'running' },
      tokenUsageByThread: {
        'thread-1': { usedTokens: 128, contextWindowTokens: 1024, usedPercent: 12.5 },
      },
      activityStatsByThread: {
        'thread-1': {
          toolCalls: Object.fromEntries(
            Array.from({ length: 18 }, (_, index) => [`very_long_tool_name_${index + 1}`, index + 1]),
          ),
        },
      },
    });

    render(<App />);
    await waitForBackendThreadHeading();

    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));

    const toolStat = screen.getByLabelText('工具调用总数');
    toolStat.getBoundingClientRect = () => ({
      x: 980,
      y: 580,
      left: 980,
      right: 1008,
      top: 580,
      bottom: 596,
      width: 28,
      height: 16,
      toJSON() {
        return this;
      },
    });

    fireEvent.click(toolStat);

    const tooltip = screen.getByTestId('runtime-stat-tooltip');
    expect(tooltip).toHaveTextContent('very_long_tool_name_18');
    expect(tooltip.style.getPropertyValue('--runtime-stat-tooltip-left')).toBe('652px');
    expect(tooltip.style.getPropertyValue('--runtime-stat-tooltip-bottom')).toBe('70px');
    expect(tooltip.style.getPropertyValue('--runtime-stat-tooltip-max-height')).toBe('558px');
  });

  it('disables thread-scoped chat buttons before a backend thread exists', async () => {
    backend.getSidebarState.mockResolvedValue({ activeThreadId: '', threads: [] });
    backend.getThreadState.mockResolvedValue({ timelinesByThread: {} });

    render(<App />);

    await screen.findByText('我们应该在 燧元 中构建什么？');
    expect(screen.queryByLabelText('复制当前线程')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('停止')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('线程状态')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('压缩当前线程')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('选择附件')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('权限')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('请先选择会话')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '自定义配置' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '语音输入' })).not.toBeInTheDocument();
    expect(screen.getByLabelText('添加文件')).toBeInTheDocument();
    expect(screen.queryByLabelText('发送权限')).not.toBeInTheDocument();
    expect(screen.getByLabelText('会话列表')).toBeInTheDocument();
    expect(screen.getByLabelText('0 个 Agent')).toBeInTheDocument();
    expect(screen.getByLabelText('打开归档列表')).toBeEnabled();
    expect(screen.getByText('暂无会话，点击「新建对话」开始草稿')).toBeInTheDocument();
  });

  it('disables thread-scoped chat buttons when the active backend thread is archived', async () => {
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: 'essay_agent_15',
      threads: [{ id: 'essay_agent_15', name: '作文Agent-15', provider: 'codex', status: 'archived' }],
    });
    backend.getThreadState.mockResolvedValue({ timelinesByThread: {} });

    render(<App />);

    await screen.findByText('我们应该在 燧元 中构建什么？');
    expect(screen.queryByLabelText('复制当前线程')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('线程状态')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('停止')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('强制完成')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('请先选择会话')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '自定义配置' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '语音输入' })).not.toBeInTheDocument();
    expect(screen.queryByText('作文Agent-15')).not.toBeInTheDocument();
    expect(backend.getThreadState).not.toHaveBeenCalledWith(expect.objectContaining({ threadId: 'essay_agent_15' }));
  });

  it('connects ComposerMeta attachments as plain arrays and conversation operation buttons', async () => {
    backend.selectFiles.mockResolvedValue(['/tmp/a.txt']);
    backend.resolveThreadIdentity.mockResolvedValue({ id: 'thread-1', providerThreadId: 'provider-thread-1', agent_id: 'agent-1' });

    render(<App />);
    await waitForBackendThreadHeading();

    fireEvent.click(screen.getByLabelText('添加文件'));
    expect(await screen.findByText('a.txt')).toBeInTheDocument();

    fireEvent.click(screen.getByLabelText('复制当前线程'));
    fireEvent.click(screen.getByLabelText('停止'));
    fireEvent.click(screen.getByLabelText('强制完成'));
    fireEvent.click(screen.getByLabelText('进程恢复'));
    expect(screen.queryByLabelText('归档会话')).not.toBeInTheDocument();

    await waitFor(() => {
      expect(backend.selectFiles).toHaveBeenCalledWith();
      expect(JSON.parse(backend.copyTextToClipboard.mock.calls[0][0])).toEqual(expect.objectContaining({
        agentId: 'agent-1',
        providerThreadId: 'provider-thread-1',
        provider: 'codex',
      }));
      expect(backend.interruptTurn).toHaveBeenCalledWith(expect.objectContaining({
        cwd: '/repo/app',
        threadId: 'thread-1',
        expectedTurnId: expect.any(String),
        requestId: expect.any(String),
        source: 'ui_stop',
      }));
      expect(backend.forceCompleteTurn).toHaveBeenCalledWith({ cwd: '/repo/app', threadId: 'thread-1' });
      expect(backend.recoverThread).toHaveBeenCalledWith({ cwd: '/repo/app', threadId: 'thread-1' });
      expect(backend.archiveThread).not.toHaveBeenCalled();
    });
  });

  it('submits timeline approval decisions from the React chat timeline', async () => {
    backend.respondApproval.mockResolvedValue(null);
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{
          id: 'approval-1',
          role: 'assistant',
          kind: 'approval',
          title: 'shell',
          text: '需要执行 deploy 命令',
          sessionScope: 'session-scope-a',
          callId: 'call-11',
          requestId: 11,
          status: 'pending',
          ts: '2026-05-30T00:00:00Z',
        }],
      },
    });

    render(<App />);

    expect(await screen.findByTestId('approval-request-11')).toHaveTextContent('需要执行 deploy 命令');
    fireEvent.click(screen.getByRole('button', { name: '同意' }));
    expect(backend.respondApproval).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: '确认选择' }));

    await waitFor(() => {
      expect(backend.respondApproval).toHaveBeenCalledWith({
        sessionScope: 'session-scope-a',
        callId: 'call-11',
        requestId: 11,
        approved: true,
      });
    });
    expect(screen.getByRole('button', { name: '同意' })).toBeDisabled();
    expect(screen.getByRole('button', { name: '确认选择' })).toBeDisabled();
  });

  it('interrupts the selected conversation when Escape is pressed', async () => {
    const interruptActiveThread = vi.spyOn(useClientStore.getState(), 'interruptActiveThread');
    render(<App />);
    await waitForBackendThreadHeading();

    fireEvent.keyDown(window, { key: 'Escape', code: 'Escape' });

    await waitFor(() => {
      expect(interruptActiveThread).toHaveBeenCalledTimes(1);
      expect(backend.interruptTurn).toHaveBeenCalledWith(expect.objectContaining({
        cwd: '/repo/app',
        threadId: 'thread-1',
        expectedTurnId: expect.any(String),
        requestId: expect.any(String),
        source: 'ui_stop',
      }));
    });
  });

  it('leaves Escape to an open local surface without interrupting or preventing it', async () => {
    render(<App />);
    await waitForBackendThreadHeading();
    const localSurface = document.createElement('div');
    localSurface.setAttribute('role', 'dialog');
    document.body.append(localSurface);

    const event = new KeyboardEvent('keydown', { key: 'Escape', code: 'Escape', bubbles: true, cancelable: true });
    act(() => window.dispatchEvent(event));

    expect(event.defaultPrevented).toBe(false);
    expect(backend.interruptTurn).not.toHaveBeenCalled();
    localSurface.remove();
  });

  it('does not interrupt the selected conversation when Escape is handled by the composer', async () => {
    render(<App />);
    await waitForBackendThreadHeading();

    const input = screen.getByTestId('composer-input');
    input.focus();
    fireEvent.keyDown(input, { key: 'Escape', code: 'Escape' });

    expect(backend.interruptTurn).not.toHaveBeenCalled();
  });

  it('does not send an invalid interrupt when a running conversation has no active turn id', async () => {
    backend.getSidebarState.mockResolvedValueOnce({
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: '后端线程', provider: 'codex', status: '工作中' }],
    });

    render(<App />);
    await waitForBackendThreadHeading();

    fireEvent.keyDown(window, { key: 'Escape', code: 'Escape' });

    await waitFor(() => expect(screen.getByTestId('chat-action-feedback')).toHaveTextContent('当前没有可中断任务'));
    expect(backend.interruptTurn).not.toHaveBeenCalled();
  });

  it('previews attachments on click and removes them only with the remove control', async () => {
    backend.selectFiles.mockResolvedValue(['/tmp/a.txt']);

    render(<App />);
    await waitForBackendThreadHeading();

    fireEvent.click(screen.getByLabelText('添加文件'));
    const attachment = await screen.findByRole('button', { name: /预览附件 a\.txt/ });
    fireEvent.click(attachment);

    const dialog = screen.getByRole('dialog', { name: '附件预览' });
    expect(dialog).toBeInTheDocument();
    expect(dialog).toHaveTextContent('a.txt');
    expect(dialog).not.toHaveTextContent('/tmp/a.txt');
    expect(screen.getByRole('button', { name: /预览附件 a\.txt/ })).toBeInTheDocument();

    fireEvent.click(screen.getByLabelText('关闭附件预览'));
    fireEvent.click(screen.getByLabelText('移除附件 a.txt'));

    expect(screen.queryByRole('button', { name: /预览附件 a\.txt/ })).not.toBeInTheDocument();
  });

  it('traps focus in the attachment preview and restores focus after Escape', async () => {
    backend.selectFiles.mockResolvedValue(['/tmp/a.txt']);

    render(<App />);
    await waitForBackendThreadHeading();

    fireEvent.click(screen.getByLabelText('添加文件'));
    const attachment = await screen.findByRole('button', { name: /预览附件 a\.txt/ });
    attachment.focus();
    fireEvent.click(attachment);

    const dialog = screen.getByRole('dialog', { name: '附件预览' });
    const closeIcon = within(dialog).getByLabelText('关闭附件预览');
    const closeText = within(dialog).getByRole('button', { name: '关闭' });
    await waitFor(() => {
      expect(document.activeElement).toBe(closeIcon);
    });

    fireEvent.keyDown(dialog, { key: 'Tab', code: 'Tab', shiftKey: true });
    expect(document.activeElement).toBe(closeText);
    fireEvent.keyDown(dialog, { key: 'Tab', code: 'Tab' });
    expect(document.activeElement).toBe(closeIcon);
    fireEvent.keyDown(dialog, { key: 'Escape', code: 'Escape' });

    await waitFor(() => {
      expect(screen.queryByRole('dialog', { name: '附件预览' })).not.toBeInTheDocument();
    });
    expect(document.activeElement).toBe(attachment);
    expect(backend.interruptTurn).not.toHaveBeenCalled();
  });

  it('adds pasted images and dropped files to the composer attachments', async () => {
    backend.saveClipboardImage.mockResolvedValue('/tmp/pasted.png');

    render(<App />);
    await waitForBackendThreadHeading();

    const input = screen.getByTestId('composer-input');
    const image = new File(['png'], 'shot.png', { type: 'image/png' });
    fireEvent.paste(input, {
      clipboardData: {
        files: [image],
        items: [],
        getData: () => '',
      },
    });

    expect(await screen.findByRole('button', { name: /预览附件 shot\.png/ })).toBeInTheDocument();

    const dropped = new File(['txt'], 'notes.txt', { type: 'text/plain' });
    Object.defineProperty(dropped, 'path', { value: '/tmp/notes.txt' });
    fireEvent.drop(screen.getByTestId('composer-dock'), {
      dataTransfer: {
        files: [dropped],
        items: [],
        types: ['Files'],
      },
    });

    expect(await screen.findByRole('button', { name: /预览附件 notes\.txt/ })).toBeInTheDocument();

    fireEvent.paste(input, {
      clipboardData: {
        files: [],
        items: [],
        types: ['x-special/gnome-copied-files', 'text/uri-list', 'text/plain'],
        getData: (type) => {
          if (type === 'x-special/gnome-copied-files') return 'copy\nfile:///tmp/desktop-copy.txt';
          if (type === 'text/uri-list') return 'file:///tmp/desktop-copy.txt';
          if (type === 'text/plain') return '/tmp/desktop-copy.txt';
          return '';
        },
      },
    });

    expect(await screen.findByRole('button', { name: /预览附件 desktop-copy\.txt/ })).toBeInTheDocument();
    expect(backend.saveClipboardImage).toHaveBeenCalledWith(expect.any(String));
  });

  it('accepts native Wails file drops on the text editor target', async () => {
    let nativeDropHandler = null;
    backend.onFilesDropped.mockImplementation((handler) => {
      nativeDropHandler = handler;
      return () => {};
    });

    render(<App />);
    await waitForBackendThreadHeading();

    const composer = screen.getByTestId('composer-dock');
    const input = screen.getByTestId('composer-input');
    const conversation = screen.getByTestId('conversation-drop-zone');
    expect(composer).toHaveAttribute('data-file-drop-target');
    expect(input).toHaveAttribute('id', 'composer-input');
    expect(input).toHaveAttribute('data-file-drop-target');
    expect(conversation).toHaveAttribute('id', 'conversation-drop-zone');
    expect(conversation).toHaveAttribute('data-file-drop-target');

    act(() => {
      nativeDropHandler({
        files: ['/tmp/native-editor-drop.txt'],
        details: {
          id: 'composer-input',
          classList: [],
          attributes: { 'data-file-drop-target': '' },
        },
      });
    });

    expect(await screen.findByRole('button', { name: /预览附件 native-editor-drop\.txt/ })).toBeInTheDocument();

    act(() => {
      nativeDropHandler({
        name: 'files-dropped',
        data: {
          files: ['/tmp/native-wails-event-drop.txt'],
          details: {
            id: 'composer-input',
            classList: [],
            attributes: { 'data-file-drop-target': '' },
          },
        },
      });
    });

    expect(await screen.findByRole('button', { name: /预览附件 native-wails-event-drop\.txt/ })).toBeInTheDocument();

    act(() => {
      nativeDropHandler({
        payload: {
          files: ['/tmp/native-payload-drop.txt'],
          details: {
            id: 'composer-input',
            classList: [],
            attributes: { 'data-file-drop-target': '' },
          },
        },
      });
    });

    expect(await screen.findByRole('button', { name: /预览附件 native-payload-drop\.txt/ })).toBeInTheDocument();

    act(() => {
      nativeDropHandler({
        files: ['/tmp/native-composer-bar-drop.txt'],
        details: {
          id: 'chat-input-bar',
          classList: ['composer'],
          attributes: { 'data-file-drop-target': '' },
        },
      });
    });

    expect(await screen.findByRole('button', { name: /预览附件 native-composer-bar-drop\.txt/ })).toBeInTheDocument();

    act(() => {
      nativeDropHandler({
        files: ['/tmp/native-conversation-drop.txt'],
        details: {
          id: 'conversation-drop-zone',
          classList: ['conversation'],
          attributes: { 'data-file-drop-target': '' },
        },
      });
    });

    expect(await screen.findByRole('button', { name: /预览附件 native-conversation-drop\.txt/ })).toBeInTheDocument();

    act(() => {
      nativeDropHandler({
        files: ['/tmp/native-unknown-target-drop.txt'],
        details: {
          id: 'timeline-inner-node',
          classList: ['timeline-inner-node'],
          attributes: { 'data-testid': 'timeline-inner-node' },
        },
      });
    });

    await waitFor(() => {
      expect(screen.queryByRole('button', { name: /预览附件 native-unknown-target-drop\.txt/ })).not.toBeInTheDocument();
    });
  });

  it('shows visible feedback for chat toolbar actions', async () => {
    backend.resolveThreadIdentity.mockResolvedValue({
      id: 'thread-1',
      providerThreadId: 'provider-thread-1',
      sessionId: 'session-uuid-1',
      agent_id: 'agent-1',
      provider: 'codex',
      port: 4512,
      cwd: '/repo/app',
      logPath: '/repo/app/.multi-agent/log/app/agent.log',
    });

    render(<App />);
    await waitForBackendThreadHeading();

    fireEvent.click(screen.getByLabelText('复制当前线程'));

    await waitFor(() => {
      expect(screen.getByTestId('chat-action-feedback')).toHaveTextContent('线程信息已复制');
      const payload = JSON.parse(backend.copyTextToClipboard.mock.calls[0][0]);
      expect(payload).toEqual(expect.objectContaining({
        agentId: 'agent-1',
        providerThreadId: 'provider-thread-1',
        uuid: 'session-uuid-1',
        name: '后端线程',
        status: '工作中',
        provider: 'codex',
        model: 'gpt-5.4',
        effort: 'medium',
        port: 4512,
        cwd: '/repo/app',
        'log-path': '/repo/app/.multi-agent/log/app/agent.log',
      }));
      expect(payload.copiedAt).toContain('UTC+8');
    });
  });

  it('shows visible feedback when copying thread info is blocked', async () => {
    backend.resolveThreadIdentity.mockResolvedValue({ id: 'thread-1', providerThreadId: 'provider-thread-1', agent_id: 'agent-1' });
    backend.copyTextToClipboard.mockRejectedValue(new Error('clipboard copy failed: native ui/copyText returned ok=false: clipboard not available in headless mode'));

    render(<App />);
    await waitForBackendThreadHeading();

    fireEvent.click(screen.getByLabelText('复制当前线程'));

    await waitFor(() => {
      expect(screen.getByTestId('chat-action-feedback')).toHaveTextContent('复制失败，请重试。');
      expect(screen.getByTestId('chat-action-feedback')).not.toHaveTextContent('headless mode');
      expect(JSON.parse(backend.copyTextToClipboard.mock.calls[0][0])).toEqual(expect.objectContaining({
        agentId: 'agent-1',
        providerThreadId: 'provider-thread-1',
      }));
    });
  });

  it('hides the provider toggle after an opened chat already has an assistant reply', async () => {
    render(<App />);
    await waitForBackendThreadHeading();

    expect(screen.queryByLabelText('线程状态')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('压缩当前线程')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('选择附件')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('权限')).not.toBeInTheDocument();
    expect(screen.getByLabelText('添加文件')).toBeInTheDocument();
    expect(screen.queryByLabelText('发送权限')).not.toBeInTheDocument();

    expect(screen.queryByLabelText('切换 Claude / Codex provider')).not.toBeInTheDocument();
    expect(screen.queryByText('Codex')).not.toBeInTheDocument();
  });

  it('keeps Codex model selection available before a backend chat exists', async () => {
    backend.getSidebarState.mockResolvedValue({ activeThreadId: '', threads: [] });
    backend.getThreadState.mockResolvedValue({ timelinesByThread: {} });

    render(<App />);
    await screen.findByText('我们应该在 燧元 中构建什么？');

    expect(screen.queryByLabelText('切换 Claude / Codex provider')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: '选择模型' })).toBeEnabled();
    expect(backend.setPreference).not.toHaveBeenCalledWith(expect.objectContaining({ key: 'settings.provider.active' }));
  });

  it('uses the opened thread provider model selector without showing the global provider toggle', async () => {
    backend.getPreference.mockImplementation(({ key }) => Promise.resolve({
      'settings.provider.active': 'codex',
      'settings.provider.codex.model': 'gpt-5.5',
      'settings.provider.codex.effort': 'xhigh',
    }[key] ?? null));
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: 'thread-failed',
      threads: [{ id: 'thread-failed', name: 'Broken Codex', provider: 'codex', status: 'failed' }],
    });
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-failed',
      timelinesByThread: { 'thread-failed': [] },
    });
    backend.getThreadConfig.mockResolvedValue({
      threadId: 'thread-failed',
      provider: 'codex',
      supportsThreadOverride: true,
      override: {},
      effective: { model: 'gpt-5.4', effort: 'medium' },
    });

    render(<App />);

    await waitFor(() => {
      expect(screen.getByRole('button', { name: '选择模型' })).toHaveTextContent('5.4 中');
    });
    expect(screen.queryByLabelText('切换 Claude / Codex provider')).not.toBeInTheDocument();
  });

  it('renders hydrated provider metadata for provider-less thread cards', async () => {
    resetClientStoreForTests({
      bootstrapStatus: 'ready',
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-unknown',
      threads: [{ id: 'thread-unknown', name: 'Provider missing', status: 'error', provider: 'codex' }],
      timelinesByThread: { 'thread-unknown': [] },
      threadTimelineReadyByThread: { 'thread-unknown': true },
    });

    render(<App skipBootstrap />);

    const card = await findThreadCardByName('Provider missing');
    expect(card).toHaveTextContent('codex');
    expect(screen.queryByText('unknown')).not.toBeInTheDocument();
  });

  it('keeps project switching controls out of the Suiyuan top app bar while loading the active thread', async () => {
    render(<App />);

    expect(await waitForBackendThreadHeading()).toBeInTheDocument();
    const topAppBar = within(screen.getByLabelText('Suiyuan app bar'));
    expect(topAppBar.queryByRole('button', { name: '选择项目' })).not.toBeInTheDocument();
    expect(topAppBar.queryByText('Overview')).not.toBeInTheDocument();
    expect(topAppBar.queryByText('Usage')).not.toBeInTheDocument();
    expect(topAppBar.queryByText('Limits')).not.toBeInTheDocument();
    expect(topAppBar.queryByRole('button', { name: 'Upgrade Plan' })).not.toBeInTheDocument();
    expect(backend.getThreadState).toHaveBeenCalledWith({
      cwd: '/repo/app',
      threadId: 'thread-1',
      includeDiff: true,
    });
    expect(backend.setActiveProject).not.toHaveBeenCalled();
  });

  it('turns the composer model chip into a thread model selector', async () => {
    backend.getPreference.mockImplementation(({ key }) => Promise.resolve({
      'settings.provider.active': 'codex',
      'settings.provider.codex.model': 'gpt-5.4',
      'settings.provider.codex.effort': 'medium',
      'settings.provider.codex.codexHome': '~/.codex',
      'settings.provider.codex.codexInstanceKey': 'default',
      'settings.provider.codex.codexModelProvider': 'openai',
    }[key] ?? null));

    render(<App />);
    await waitForBackendThreadHeading();

    await waitFor(() => {
      expect(screen.getByRole('button', { name: '选择模型' })).toHaveTextContent('5.4 中');
    });

    const modelButton = screen.getByRole('button', { name: '选择模型' });
    fireEvent.click(modelButton);
    expect(screen.getByRole('dialog', { name: '模型配置' })).toBeInTheDocument();
    expect(screen.getByRole('option', { name: '默认（当前：GPT-5.4）' })).toBeInTheDocument();
    expect(screen.getByRole('option', { name: '默认（当前：中）' })).toBeInTheDocument();
    expect(screen.queryByText('渠道')).not.toBeInTheDocument();
    expect(screen.queryByRole('group', { name: '模型渠道' })).not.toBeInTheDocument();

    fireEvent.change(screen.getByLabelText('模型'), { target: { value: 'gpt-5.5' } });

    await waitFor(() => {
      expect(backend.setThreadConfig).toHaveBeenCalledWith({
        threadId: 'thread-1',
        model: 'gpt-5.5',
        effort: '',
      });
      expect(modelButton).toHaveTextContent('5.5 中');
      expect(screen.getByTestId('chat-action-feedback')).toHaveTextContent('线程配置已保存');
    });
  });

  it('shows delete and running indicators on each visible thread card without active archive', async () => {
    render(<App />);
    await waitForBackendThreadHeading();

    expect(screen.getAllByLabelText('会话运行中').length).toBeGreaterThan(0);
    expect(screen.getByRole('button', { name: '删除会话' })).toBeInTheDocument();
    expect(screen.queryByLabelText('归档会话')).not.toBeInTheDocument();
    expect(getBackendThreadText()).toBeInTheDocument();
  });

  it('shows the pin action tooltip when hovering the thread pin icon', async () => {
    render(<App />);
    await waitForBackendThreadHeading();

    const pinButton = screen.getByLabelText('置顶对话');
    expect(pinButton).not.toHaveAttribute('title');
    fireEvent.mouseEnter(pinButton);

    expect(screen.getByTestId('thread-pin-tooltip')).toHaveTextContent('置顶对话');

    fireEvent.mouseLeave(pinButton);

    expect(screen.queryByTestId('thread-pin-tooltip')).not.toBeInTheDocument();
  });

  it('renames a thread inline through the legacy backend name RPC', async () => {
    render(<App />);
    await waitForBackendThreadHeading();

    fireEvent.doubleClick(within(getThreadCardByName('后端线程')).getByRole('button', { name: /后端线程/ }));
    const input = screen.getByLabelText('会话别名');
    fireEvent.change(input, { target: { value: '重命名会话' } });
    fireEvent.click(screen.getByRole('button', { name: '保存别名' }));

    await waitFor(() => {
      expect(backend.renameThread).toHaveBeenCalledWith({ threadId: 'thread-1', name: '重命名会话' });
      expect(getThreadCardByName('重命名会话')).toBeInTheDocument();
    });
  });

  it('persists thread pins through the backend threadPins chat preference', async () => {
    render(<App />);
    await waitForBackendThreadHeading();

    fireEvent.click(screen.getByLabelText('置顶对话'));

    await waitFor(() => {
      expect(backend.setPreference).toHaveBeenCalledWith({
        cwd: '/repo/app',
        key: 'threadPins.chat',
        value: { 'thread-1': expect.any(Number) },
      });
      expect(screen.getByLabelText('取消置顶对话')).toBeInTheDocument();
      expect(screen.getByTestId('chat-action-feedback')).toHaveTextContent('会话已置顶');
    });
  });

  it('moves a sent ordinary chat below pinned chats but above other ordinary chats', async () => {
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: 'thread-old',
      threads: [
        { id: 'thread-pin', name: 'Pinned chat', provider: 'codex', status: 'idle' },
        { id: 'thread-new', name: 'Newer chat', provider: 'codex', status: 'idle' },
        { id: 'thread-old', name: 'Older chat', provider: 'codex', status: 'idle' },
      ],
      'threadPins.chat': { 'thread-pin': 1735689600000 },
    });
    backend.getThreadState.mockResolvedValue({ activeThreadId: 'thread-old', timelinesByThread: {} });
    backend.startTurn.mockResolvedValue({ ok: true });
    const { container } = render(<App />);
    await findThreadCardByName('Older chat');

    fireEvent.change(screen.getByTestId('composer-input'), { target: { value: 'bring old chat forward' } });
    fireEvent.click(screen.getByLabelText('发送消息'));

    await waitFor(() => expect(backend.startTurn).toHaveBeenCalledWith(expect.objectContaining({ threadId: 'thread-old' })));
    expect([...container.querySelectorAll('.thread-card .thread-name')].map((node) => node.textContent)).toEqual([
      'Pinned chat',
      'Older chat',
      'Newer chat',
    ]);
  });

  it('only floats an ordinary chat on reply completion, not unrelated runtime patches', async () => {
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: 'thread-old',
      threads: [
        { id: 'thread-pin', name: 'Pinned chat', provider: 'codex', status: 'idle' },
        { id: 'thread-old', name: 'Older chat', provider: 'codex', status: 'idle' },
        { id: 'thread-new', name: 'Newer chat', provider: 'codex', status: 'idle' },
      ],
      'threadPins.chat': { 'thread-pin': 1735689600000 },
    });
    backend.getThreadState.mockResolvedValue({ activeThreadId: 'thread-old', timelinesByThread: {} });
    const { container } = render(<App />);
    await waitFor(() => expect(getThreadCardByName('Newer chat')).toBeInTheDocument());

    act(() => {
      bridgeCallback?.({
        type: 'ui/thread/patch',
        payload: {
          threadId: 'thread-new',
          source: 'tool/diffUpdated',
          status: 'running',
          thread: { id: 'thread-new', name: 'Newer chat', status: 'running' },
        },
      });
    });
    expect([...container.querySelectorAll('.thread-card .thread-name')].map((node) => node.textContent)).toEqual([
      'Pinned chat',
      'Older chat',
      'Newer chat',
    ]);

    act(() => {
      bridgeCallback?.({
        type: 'ui/thread/patch',
        payload: {
          threadId: 'thread-new',
          source: 'turn/completed',
          status: 'idle',
          thread: { id: 'thread-new', name: 'Newer chat', status: 'idle' },
        },
      });
    });
    expect([...container.querySelectorAll('.thread-card .thread-name')].map((node) => node.textContent)).toEqual([
      'Pinned chat',
      'Newer chat',
      'Older chat',
    ]);
  });

  it('matches the legacy thread rail archive-list toggle', async () => {
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: 'thread-1',
      threads: [
        { id: 'thread-1', name: '活跃线程', provider: 'codex', status: '工作中' },
        { id: 'thread-archived', name: '归档线程', provider: 'codex', status: 'archived' },
      ],
    });
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {},
    });

    render(<App />);
    await findThreadCardByName('活跃线程');

    expect(screen.getByLabelText('会话列表')).toBeInTheDocument();
    expect(screen.getByLabelText('1 个 Agent')).toBeInTheDocument();
    expect(screen.queryByText('归档线程')).not.toBeInTheDocument();

    fireEvent.click(screen.getByLabelText('打开归档列表'));

    expect(await screen.findByText('归档线程')).toBeInTheDocument();
    expect(screen.getByLabelText('归档列表')).toBeInTheDocument();
    expect(screen.getByLabelText('返回会话列表')).toBeInTheDocument();
    expect(queryThreadCardByName('活跃线程')).not.toBeInTheDocument();

    fireEvent.click(screen.getByLabelText('恢复会话'));

    await waitFor(() => {
      expect(backend.unarchiveThread).toHaveBeenCalledWith({ threadId: 'thread-archived' });
      expect(backend.setPreference).toHaveBeenCalledWith(expect.objectContaining({
        cwd: '/repo/app',
        key: 'archivedThreadAtById.thread-archived',
        value: null,
      }));
      expect(screen.getByText('暂无归档会话')).toBeInTheDocument();
    });
  });

  it('opens archived thread content from the archive list without showing the new-chat draft', async () => {
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: 'thread-1',
      threads: [
        { id: 'thread-1', name: '活跃线程', provider: 'codex', status: '工作中' },
        { id: 'thread-archived', name: '归档线程', provider: 'codex', status: 'archived' },
      ],
    });
    backend.getThreadState.mockImplementation(({ threadId }) => Promise.resolve({
      activeThreadId: threadId,
      threads: [
        { id: 'thread-1', name: '活跃线程', provider: 'codex', status: '工作中' },
        { id: 'thread-archived', name: '归档线程', provider: 'codex', status: 'idle' },
      ],
      timelinesByThread: {
        [threadId]: [{
          id: `${threadId}-assistant`,
          kind: 'assistant',
          text: threadId === 'thread-archived' ? '归档线程历史内容' : '活跃线程内容',
        }],
      },
    }));

    render(<App />);
    await screen.findByText('活跃线程内容');

    fireEvent.click(screen.getByLabelText('打开归档列表'));
    fireEvent.click(await screen.findByRole('button', { name: /归档线程/ }));

    await waitFor(() => expect(useClientStore.getState().activeThreadId).toBe('thread-archived'));
    expect(await screen.findByText('归档线程历史内容')).toBeInTheDocument();
    expect(screen.queryByText(/让我们从/)).not.toBeInTheDocument();
    expect(screen.queryByLabelText('复制当前线程')).not.toBeInTheDocument();
  });

  it('keeps an empty archived thread selection out of the new-chat intro state', async () => {
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: 'thread-1',
      threads: [
        { id: 'thread-1', name: '活跃线程', provider: 'codex', status: '工作中' },
        { id: 'thread-archived', name: '空归档线程', provider: 'codex', status: 'archived' },
      ],
    });
    backend.getThreadState.mockImplementation(({ threadId }) => Promise.resolve({
      activeThreadId: threadId,
      threads: [
        { id: 'thread-1', name: '活跃线程', provider: 'codex', status: '工作中' },
        { id: 'thread-archived', name: '空归档线程', provider: 'codex', status: 'idle' },
      ],
      timelinesByThread: threadId === 'thread-1'
        ? { 'thread-1': [{ id: 'active-msg', kind: 'assistant', text: '活跃线程内容' }] }
        : { 'thread-archived': [] },
    }));

    render(<App />);
    await screen.findByText('活跃线程内容');

    fireEvent.click(screen.getByLabelText('打开归档列表'));
    fireEvent.click(await screen.findByRole('button', { name: /空归档线程/ }));

    await waitFor(() => expect(useClientStore.getState().activeThreadId).toBe('thread-archived'));
    expect(screen.queryByText(/让我们从/)).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: /空归档线程/ }).closest('.thread-card')).toHaveClass('active');
    expect(screen.queryByLabelText('复制当前线程')).not.toBeInTheDocument();
  });

  it('loads archived thread messages from the legacy messages RPC', async () => {
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: 'thread-1',
      threads: [
        { id: 'thread-1', name: '活跃线程', provider: 'codex', status: '工作中' },
        { id: 'thread-archived', name: '消息归档线程', provider: 'codex', status: 'archived' },
      ],
    });
    backend.getThreadState.mockImplementation(({ threadId }) => Promise.resolve({
      activeThreadId: threadId,
      threads: [
        { id: 'thread-1', name: '活跃线程', provider: 'codex', status: '工作中' },
        { id: 'thread-archived', name: '消息归档线程', provider: 'codex', status: 'idle' },
      ],
      timelinesByThread: threadId === 'thread-1'
        ? { 'thread-1': [{ id: 'active-msg', kind: 'assistant', text: '活跃线程内容' }] }
        : { 'thread-archived': [] },
    }));
    backend.getThreadMessages.mockImplementation(({ threadId }) => Promise.resolve({
      messages: threadId === 'thread-archived'
        ? [{ id: 'archived-message', role: 'assistant', content: '来自 thread/messages 的归档内容', createdAt: '2026-05-30T00:00:00Z' }]
        : [],
    }));

    render(<App />);
    await screen.findByText('活跃线程内容');

    fireEvent.click(screen.getByLabelText('打开归档列表'));
    fireEvent.click(await screen.findByRole('button', { name: /消息归档线程/ }));

    expect(await screen.findByText('来自 thread/messages 的归档内容')).toBeInTheDocument();
    expect(backend.getThreadMessages).toHaveBeenCalledWith({ threadId: 'thread-archived', limit: 300 });
    expect(screen.queryByText(/让我们从/)).not.toBeInTheDocument();
  });

  it('cleans stale archived threads through the legacy delete RPC', async () => {
    const staleArchiveAt = Date.now() - (8 * 24 * 60 * 60 * 1000);
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: 'thread-1',
      threads: [
        { id: 'thread-1', name: '活跃线程', provider: 'codex', status: '工作中' },
        { id: 'thread-stale', name: '旧归档线程', provider: 'codex', status: 'archived', archivedAt: staleArchiveAt },
        { id: 'thread-fresh', name: '近期归档线程', provider: 'codex', status: 'archived', archivedAt: Date.now() },
      ],
    });
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {},
    });

    render(<App />);
    await findThreadCardByName('活跃线程');

    fireEvent.click(screen.getByLabelText('打开归档列表'));
    expect(await screen.findByText('旧归档线程')).toBeInTheDocument();
    expect(screen.getByText('近期归档线程')).toBeInTheDocument();
    expect(screen.getByText('超7天')).toBeInTheDocument();

    fireEvent.click(screen.getByLabelText('清理无用对话'));
    fireEvent.click(screen.getByText('确认'));

    await waitFor(() => {
      expect(backend.deleteThread).toHaveBeenCalledWith({ threadId: 'thread-stale' });
      expect(backend.deleteThread).not.toHaveBeenCalledWith({ threadId: 'thread-fresh' });
      expect(backend.setPreference).toHaveBeenCalledWith({
        cwd: '/repo/app',
        key: 'archivedThreadAtById.thread-stale',
        value: null,
      });
      expect(screen.queryByText('旧归档线程')).not.toBeInTheDocument();
      expect(screen.getByText('近期归档线程')).toBeInTheDocument();
      expect(screen.getByTestId('chat-action-feedback')).toHaveTextContent('已删除 1 个无用会话');
    });
  });

  it('renders warning log entries from bridge events', async () => {
    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));
    fireEvent.keyDown(screen.getByTestId('activity-panel-resizer'), { key: 'ArrowUp' });

    act(() => {
      bridgeCallback({
        type: 'rpc.failed',
        payload: { method: 'turn/start', threadId: 'thread-1', traceId: 'trace-123' },
      });
    });

    const warningLine = await screen.findByRole('button', { name: /rpc.failed/ });
    expect(screen.queryByText(/turn\/start/)).not.toBeInTheDocument();

    fireEvent.mouseEnter(warningLine);
    expect(screen.queryByTestId('warning-log-popover')).not.toBeInTheDocument();
    fireEvent.click(warningLine);

    expect(screen.getByTestId('warning-log-popover')).toHaveTextContent('rpc.failed');
    expect(screen.getByTestId('warning-log-popover')).toHaveTextContent('turn/start');

    fireEvent.keyDown(screen.getByRole('dialog', { name: 'rpc.failed' }), { key: 'Escape' });

    await waitFor(() => {
      expect(screen.queryByTestId('warning-log-popover')).not.toBeInTheDocument();
    });
  });

  it('navigates to screenshot-style secondary pages', async () => {
    render(<App />);
    await screen.findByLabelText('插件与技能');

    expect(screen.queryByLabelText('命令')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('任务')).not.toBeInTheDocument();

    openPluginsAndSkillsPage();
    expect(await screen.findByRole('heading', { name: 'MCP工具' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Skill工具' })).not.toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Skill工具' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '新增工具' })).not.toBeInTheDocument();
    expect(screen.queryByText('本地技能库')).not.toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: '后端' })).not.toBeInTheDocument();
    expect(backend.listSkillTools).not.toHaveBeenCalled();
    expect(backend.getDashboardPage).not.toHaveBeenCalledWith({ cwd: '/repo/app', page: 'skills' });
    expect(backend.listSkillResolutions).not.toHaveBeenCalled();

    fireEvent.click(screen.getByLabelText('共享文件'));
    expect(await screen.findByText('文件产物')).toBeInTheDocument();
    await waitFor(() => {
      expect(backend.listSharedFiles).toHaveBeenCalledWith();
    });
  });

  it.each([
    ['提示词', '个性化', '暂无内容', () => expect(backend.listPromptAssets).not.toHaveBeenCalled()],
    ['自动化', '自动化', '创建首个自动化', () => expect(backend.getDashboardPage).not.toHaveBeenCalledWith({ cwd: '未选择项目', page: 'dags' })],
    ['记忆中心', '记忆中心', '暂无记忆', () => expect(backend.getMemorySnapshot).not.toHaveBeenCalledWith({ cwd: '未选择项目' })],
  ])('keeps the %s route visible while project context resolves', async (navLabel, heading, settledText, assertNoInvalidLoad) => {
    const config = deferred();
    backend.readConfig.mockReturnValueOnce(config.promise);

    render(<App />);
    fireEvent.click(screen.getByLabelText(navLabel));

    await waitFor(() => expect(document.querySelector('.suiyuan-appbar-title h1')).toHaveTextContent(heading));
    assertNoInvalidLoad();

    await act(async () => {
      config.resolve({ cwd: '/repo/app' });
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(await screen.findByText(settledText)).toBeInTheDocument();
  });

  it('loads global shared files while project context resolves', async () => {
    const config = deferred();
    backend.readConfig.mockReturnValueOnce(config.promise);

    render(<App />);
    fireEvent.click(screen.getByLabelText('共享文件'));

    await waitFor(() => expect(document.querySelector('.suiyuan-appbar-title h1')).toHaveTextContent('文件产物'));
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

  it('does not mark the memory center nav when no similar memories need merging', async () => {
    render(<App />);
    await waitForBackendThreadHeading();

    expect(screen.getByLabelText('记忆中心').querySelector('i')).toBeNull();
  });

  it('marks the memory center nav only for similar memories that need merging', async () => {
    backend.getMemorySnapshot.mockResolvedValue(normalizeMemorySnapshotForFacade({
      overview: {
        enabled: true,
        autoDreamEnabled: false,
        autoDreamIntent: null,
        projectRoot: '/repo/app',
        health: {
          preferenceCount: 0,
          projectCount: 0,
          maxPerCategory: 15,
          similarGroups: [{
            nameA: 'A', targetA: 'private', pathA: 'feedback/a.md',
            nameB: 'B', targetB: 'team', pathB: 'feedback/b.md',
            score: 0.88,
          }, {
            nameA: 'C', targetA: 'private', pathA: 'feedback/c.md',
            nameB: 'D', targetB: 'team', pathB: 'feedback/d.md',
            score: 0.82,
          }],
        },
      },
      private: { entries: [] },
      team: { entries: [] },
    }));

    render(<App />);
    await waitForBackendThreadHeading();

    await waitFor(() => {
      expect(screen.getByLabelText('记忆中心').querySelector('i')).toHaveAttribute('title', '2 条待整合相似记忆');
    });
  });


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
      bridgeCallback?.({ type: 'prompts/changed', payload: { cwd: '/repo/app' } });
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
      bridgeCallback?.({ type: 'prompts/changed', payload: { cwd: '/repo/app' } });
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

function mockPromptAssetWorkflow() {
  backend.getPreference.mockImplementation(({ key }) => Promise.resolve({
    'settings.provider.active': 'codex',
    'settings.provider.codex.model': 'gpt-5.5',
    'settings.provider.codex.effort': 'xhigh',
    'settings.provider.codex.codexHome': '~/.codex',
    'settings.provider.codex.codexInstanceKey': 'default',
    'settings.provider.codex.codexModelProvider': 'openrouter',
  }[key] ?? null));
  let prompts = [canonicalPromptRPCItem({
    id: 'main/reviewer',
    name: '代码审查专家',
    content: '先检查阻塞问题',
    description: '审查代码质量',
    when_to_use: 'Use for code review.',
    agentType: 'coder',
    tags: ['intent:expert', 'review'],
    scope: 'project',
    enabled: true,
  }), canonicalPromptRPCItem({
    id: 'intent/recall/ready',
    draft_key: 'intent/recall/ready',
    name: '价格表资料',
    content: '价格资料内容',
    description: '待确认的资料',
    tags: ['intent:recall', 'pricing'],
    scope: 'project',
    enabled: false,
    state: 'pending_confirm',
    draft_status: 'ready_to_save',
    card: { kind: 'recall', title: '价格表资料', summary: '待确认的资料', output: '价格资料内容' },
  })];
  backend.listPromptAssets.mockImplementation(() => Promise.resolve({ prompts }));
  backend.writePrompt.mockImplementation(({ id, name, content }) => {
    prompts = prompts.map((item) => (item.id === id ? { ...item, name, content } : item));
    return Promise.resolve({ prompt: { id } });
  });
  backend.deletePrompt.mockImplementation(({ id }) => {
    prompts = prompts.filter((item) => item.id !== id);
    return Promise.resolve({ deleted: true });
  });
  backend.draftPromptIntent.mockResolvedValue({
    draft_key: 'intent/expert/review',
    kind: 'expert',
    scope: 'project',
    status: 'review',
    card: {
      kind: 'expert',
      title: '代码风险审查',
      summary: '识别阻塞风险',
      output: '先列阻塞问题，再给修改建议',
      hit_examples: ['审查这段代码'],
      miss_examples: ['解释一个概念'],
    },
    issues: [],
  });
  backend.commitPromptIntent.mockResolvedValue({ prompt: { id: 'main/code-risk-review' } });
}

async function openPromptAssetsPage() {
  render(<App />);
  await waitForBackendThreadHeading();
  fireEvent.click(screen.getByLabelText('提示词'));
  expect(await screen.findByText('代码审查专家')).toBeInTheDocument();
}

async function openPromptWizardFromPendingCard(cardName = '价格表资料') {
  const pendingCard = (await screen.findByText(cardName)).closest('article');
  const continueButton = within(pendingCard).getByRole('button', { name: '继续确认' });
  fireEvent.click(continueButton);
  const wizard = await screen.findByRole('dialog', { name: '添加给 AI 的内容' });
  return { continueButton, pendingCard, wizard };
}

async function editAndDeleteReviewerPrompt() {
  const card = screen.getByText('代码审查专家').closest('article');
  backend.getPrompt.mockResolvedValueOnce({ prompt: { content: '完整审查提示词' } });
  fireEvent.click(within(card).getByRole('button', { name: '复制' }));
  await waitFor(() => {
    expect(backend.getPrompt).toHaveBeenCalledWith({ cwd: '/repo/app', id: 'main/reviewer' });
    expect(backend.copyTextToClipboard).toHaveBeenCalledWith('完整审查提示词');
  });
  expect(await screen.findByText('已复制提示词内容')).toBeInTheDocument();
  fireEvent.click(within(card).getByRole('button', { name: '编辑' }));
  const editor = await screen.findByRole('dialog', { name: '编辑提示词' });
  expect(editor).toBeInTheDocument();
  expect(within(editor).getByText('可用范围：这个项目')).toBeInTheDocument();
  expect(within(editor).getByLabelText('保存后 AI 会看到什么')).toHaveValue('先检查阻塞问题');
  expect(within(editor).queryByLabelText('Agent Key')).not.toBeInTheDocument();
  expect(within(editor).queryByLabelText('场景标签')).not.toBeInTheDocument();
  expect(within(editor).queryByLabelText('排序权重')).not.toBeInTheDocument();
  fireEvent.change(screen.getByLabelText('名称'), { target: { value: '代码风险审查' } });
  fireEvent.change(screen.getByLabelText('AI 使用时怎么做'), { target: { value: '先列阻塞问题，再给修改建议' } });
  fireEvent.click(screen.getByRole('button', { name: '保存' }));
  await waitFor(() => {
    expect(backend.writePrompt).toHaveBeenCalledWith(expect.objectContaining({
      cwd: '/repo/app',
      id: 'main/reviewer',
      name: '代码风险审查',
      agentType: 'coder',
      content: '先列阻塞问题，再给修改建议',
      scope: 'project',
      enabled: true,
    }));
  });

  await screen.findByText('代码风险审查');
}

async function handlePendingPromptDraft() {
  const { pendingCard, wizard: pendingDialog } = await openPromptWizardFromPendingCard('价格表资料');
  expect(screen.getAllByText('价格表资料').length).toBeGreaterThanOrEqual(1);
  fireEvent.click(within(pendingDialog).getAllByRole('button', { name: '关闭' }).at(-1));

  fireEvent.click(within(pendingCard).getByRole('button', { name: '丢弃' }));
  await waitFor(() => {
    expect(backend.discardPromptIntent).toHaveBeenCalledWith({ cwd: '/repo/app', draftKey: 'intent/recall/ready' });
  });
}

async function createGeneratedPromptIntent() {
  const { wizard } = await openPromptWizardFromPendingCard('价格表资料');
  fireEvent.click(within(wizard).getByRole('tab', { name: '专家能力' }));
  fireEvent.change(screen.getByLabelText('写下希望 AI 记住或使用的内容'), {
    target: { value: '当用户要求代码审查时，先检查阻塞问题。' },
  });
  expect(screen.queryByRole('button', { name: '整理草稿' })).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: '帮我生成' }));
  expect(await screen.findByText('代码风险审查')).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: '确认保存' }));
  await waitFor(() => {
    expect(backend.draftPromptIntent).toHaveBeenCalledWith({
      cwd: '/repo/app',
      kind: 'expert',
      rawInput: '当用户要求代码审查时，先检查阻塞问题。',
      sourceType: 'user_input',
      scope: 'project',
      provider: 'codex',
      model: 'gpt-5.5',
      codexModelProvider: 'openrouter',
    });
    expect(backend.commitPromptIntent).toHaveBeenCalledWith({ cwd: '/repo/app', draftKey: 'intent/expert/review', scope: 'project' });
  });
}

  it('wires prompt edit, delete, pending draft, and intent wizard actions without card copy action', async () => {
    mockPromptAssetWorkflow();

    await openPromptAssetsPage();
    await editAndDeleteReviewerPrompt();
    await handlePendingPromptDraft();
    await createGeneratedPromptIntent();
  });

  it('uses the first generated prompt draft option when the backend infers multiple choices', async () => {
    backend.draftPromptIntent.mockResolvedValueOnce({
      requested_kind: 'expert',
      inferred_kind: 'recall',
      drafts: [{
        draft_key: 'intent/recall/generated',
        kind: 'recall',
        scope: 'project',
        status: 'review',
        card: {
          kind: 'recall',
          title: '酒后提醒',
          summary: '阻止酒后继续操作',
          recall_body: '在用户喝酒时提醒停止继续操作。',
          hit_examples: ['我喝酒了还想继续工作'],
          miss_examples: ['普通工作安排'],
        },
        issues: [],
      }],
    });
    backend.commitPromptIntent.mockResolvedValueOnce({ prompt: { id: 'recall/alcohol-guard' } });
    mockPromptWizardEntryPrompt();

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('提示词'));
    const { wizard } = await openPromptWizardFromPendingCard('待确认入口');
    fireEvent.click(within(wizard).getByRole('tab', { name: '专家能力' }));
    fireEvent.change(await screen.findByLabelText('写下希望 AI 记住或使用的内容'), {
      target: { value: '在我喝酒的时候阻止我' },
    });
    fireEvent.click(screen.getByRole('button', { name: '帮我生成' }));

    expect(await screen.findByText('酒后提醒')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '确认保存' }));
    await waitFor(() => {
      expect(backend.commitPromptIntent).toHaveBeenCalledWith({ cwd: '/repo/app', draftKey: 'intent/recall/generated', scope: 'project' });
    });
  });

  it('does not submit prompt drafts that still need revision', async () => {
    backend.draftPromptIntent.mockResolvedValueOnce({
      draft_key: 'intent/expert/alcohol-support',
      kind: 'expert',
      scope: 'project',
      status: 'draft',
      card: {
        kind: 'expert',
        title: '想喝酒时给予支持性鼓励',
        summary: '在用户想喝酒时给予支持。',
        output: '温和提醒用户先停下来。',
        hit_examples: ['我想喝酒'],
        miss_examples: ['帮我写代码'],
      },
      issues: [{ code: 'missing_when_not_to_use', severity: 'block', message: '需要补充不用它的场景' }],
    });
    mockPromptWizardEntryPrompt();

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('提示词'));
    await openPromptWizardFromPendingCard('待确认入口');
    fireEvent.change(await screen.findByLabelText('写下希望 AI 记住或使用的内容'), {
      target: { value: '在我想喝酒的时候鼓励我' },
    });
    fireEvent.click(screen.getByRole('button', { name: '帮我生成' }));

    expect(await screen.findByText('想喝酒时给予支持性鼓励')).toBeInTheDocument();
    expect(screen.getByText('这条内容还需要完善后才能保存，请调整描述后重新生成。')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '确认保存' })).toBeDisabled();
    expect(backend.commitPromptIntent).not.toHaveBeenCalled();
  });

  it('shows user-facing prompt save guidance when the backend rejects an unready draft', async () => {
    backend.draftPromptIntent.mockResolvedValueOnce({
      draft_key: 'intent/expert/alcohol-support',
      kind: 'expert',
      scope: 'project',
      status: 'ready_to_save',
      card: {
        kind: 'expert',
        title: '想喝酒时给予支持性鼓励',
        summary: '在用户想喝酒时给予支持。',
        output: '温和提醒用户先停下来。',
        hit_examples: ['我想喝酒'],
        miss_examples: ['帮我写代码'],
      },
      issues: [],
    });
    backend.commitPromptIntent.mockRejectedValueOnce(new Error('with_tx prompt_template: [-31007] prompt intent draft is not ready to save'));
    mockPromptWizardEntryPrompt();

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('提示词'));
    await openPromptWizardFromPendingCard('待确认入口');
    fireEvent.change(await screen.findByLabelText('写下希望 AI 记住或使用的内容'), {
      target: { value: '在我想喝酒的时候鼓励我' },
    });
    fireEvent.click(screen.getByRole('button', { name: '帮我生成' }));
    expect(await screen.findByText('想喝酒时给予支持性鼓励')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '确认保存' }));

    await waitFor(() => {
      expect(screen.getByText('这条内容还需要完善后才能保存，请调整描述后重新生成。')).toBeInTheDocument();
    });
    expect(screen.getByText('这条内容还需要完善后才能保存，请调整描述后重新生成。')).not.toHaveClass('error');
    expect(screen.queryByText(/with_tx|31007|not ready to save/i)).not.toBeInTheDocument();
  });

  it('shows generated prompt draft details like the legacy confirmation card', async () => {
    backend.draftPromptIntent.mockResolvedValueOnce({
      draft_key: 'intent/expert/alcohol-support',
      kind: 'expert',
      scope: 'project',
      status: 'draft',
      card: {
        kind: 'expert',
        title: '想喝酒时暂停提醒',
        summary: '在用户表达想喝酒时给予支持。',
        when_to_use: '当用户表达想喝酒、想买酒或可能冲动饮酒时使用。',
        when_not_to_use: '不要用于普通饮食建议或医疗诊断。',
        workflow: ['先接住情绪', '提醒用户暂停饮酒', '建议做一个安全替代行动'],
        save_boundary: '只给出建议，不声称已经保存到记忆。',
        output: '输出一段温和、坚定的提醒，并给出一个可马上执行的替代行动。',
        hit_examples: ['我现在想喝酒'],
        miss_examples: ['推荐一杯咖啡'],
      },
      issues: [{ code: 'missing_when_not_to_use', severity: 'block', message: 'internal field copy' }],
    });
    mockPromptWizardEntryPrompt();

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('提示词'));
    await openPromptWizardFromPendingCard('待确认入口');
    fireEvent.change(await screen.findByLabelText('写下希望 AI 记住或使用的内容'), {
      target: { value: '在我想喝酒的时候阻止我' },
    });
    fireEvent.click(screen.getByRole('button', { name: '帮我生成' }));

    expect(await screen.findByText('想喝酒时暂停提醒')).toBeInTheDocument();
    expect(screen.getByText('当用户表达想喝酒、想买酒或可能冲动饮酒时使用。')).toBeInTheDocument();
    expect(screen.getByText('不要用于普通饮食建议或医疗诊断。')).toBeInTheDocument();
    expect(screen.getByText('先接住情绪')).toBeInTheDocument();
    expect(screen.getByText('只给出建议，不声称已经保存到记忆。')).toBeInTheDocument();
    expect(screen.getByText('我现在想喝酒')).toBeInTheDocument();
    expect(screen.getByText('推荐一杯咖啡')).toBeInTheDocument();
    expect(screen.getByText('需要说明哪些问题不适合使用它。')).toBeInTheDocument();
    expect(screen.queryByText('internal field copy')).not.toBeInTheDocument();
  });

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
      bridgeCallback?.({ type: 'ui/memory/changed', payload: { action: 'upsert' } });
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
      bridgeCallback?.({ type: 'ui/memory/changed', payload: { action: 'upsert' } });
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

function createSimilaritySnapshots() {
  const group = {
    nameA: 'A', targetA: 'private', pathA: 'feedback/a.md',
    nameB: 'B', targetB: 'team', pathB: 'feedback/b.md',
    score: 0.88,
  };
  // 与真实 facade 输出一致：parse + transform 后的扁平 { overview, entries } 形态。
  const snapshotWithSimilar = normalizeMemorySnapshotForFacade({
    overview: {
      enabled: true,
      autoDreamEnabled: true,
      projectRoot: '/repo/app',
      health: {
        preferenceCount: 2,
        projectCount: 0,
        maxPerCategory: 15,
        similarGroups: [group],
      },
    },
    private: { entries: [] },
    team: { entries: [] },
  });
  const snapshotWithoutSimilar = {
    ...snapshotWithSimilar,
    overview: {
      ...snapshotWithSimilar.overview,
      health: {
        ...snapshotWithSimilar.overview.health,
        similarGroups: [],
      },
    },
  };
  return { snapshotWithSimilar, snapshotWithoutSimilar };
}

async function openMemoryCenterWithSimilarity() {
  render(<App />);
  await waitForBackendThreadHeading();
  await waitFor(() => {
    expect(screen.getByLabelText('记忆中心').querySelector('i')).toHaveAttribute('title', '1 条待整合相似记忆');
  });

  fireEvent.click(screen.getByLabelText('记忆中心'));
  expect(await screen.findByText('1 组条目内容相似')).toBeInTheDocument();
}

async function runConsolidationUntilSimilaritiesClear(clearSimilarities) {
  vi.useFakeTimers();
  try {
    fireEvent.click(screen.getByRole('button', { name: '一键整合全部' }));
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(backend.startConsolidateMemorySimilarities).toHaveBeenCalledWith(expect.objectContaining({
      cwd: '/repo/app',
      provider: 'codex',
      codexModelProvider: 'openai',
    }));
    expect(backend.getMemoryConsolidationStatus).toHaveBeenCalledWith({ cwd: '/repo/app', jobId: 'memory-job-live' });
    expect(screen.getByRole('button', { name: '后台整合中' })).toBeDisabled();

    clearSimilarities();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(2000);
      await Promise.resolve();
    });
  }
  finally {
    vi.useRealTimers();
  }
}

function expectSimilarityWarningCleared() {
  expect(screen.queryByText('1 组条目内容相似')).not.toBeInTheDocument();
  expect(screen.queryByRole('button', { name: '一键整合全部' })).not.toBeInTheDocument();
  expect(screen.getByText('已整合 1 组')).toBeInTheDocument();
  expect(screen.getByLabelText('记忆中心').querySelector('i')).toBeNull();
}

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

function createSharedFileState() {
  let memoryFiles = [
    {
      path: 'reports/final.md',
      content: 'final summary',
      updated_by: 'dag-runner',
      updated_at: '2026-05-30T08:00:00Z',
    },
    {
      path: 'scratch/work.json',
      content: '{"step":1}',
      updated_by: 'agent',
      updated_at: '2026-05-30T07:00:00Z',
    },
  ];
  return {
    payload: () => ({
      files: memoryFiles,
      memory: memoryFiles,
      finalOutputRefs: [{ path: 'reports/final.md', runKey: 'run-1', dagKey: 'daily-brief', sourceNodeKey: 'report' }],
      sharedFileRetention: {
        items: [
          { path: 'reports/final.md', protected: true, cleanupCandidate: false, reason: 'final_output' },
          { path: 'scratch/work.json', protected: false, cleanupCandidate: true, reason: 'unreferenced' },
        ],
        protectedCount: 1,
        cleanupCandidateCount: 1,
      },
    }),
    add(file) {
      memoryFiles = [...memoryFiles, file];
    },
    remove(path) {
      memoryFiles = memoryFiles.filter((item) => item.path !== path);
    },
  };
}

function mockSharedFileWorkflow(sharedFiles) {
  backend.listSharedFiles.mockImplementation(() => Promise.resolve(sharedFiles.payload()));
  backend.readSharedFile.mockImplementation(({ path }) => Promise.resolve({
    path,
    content: path === 'reports/final.md' ? 'FINAL CONTENT' : '{"step":1,"detail":true}',
    updatedBy: path === 'reports/final.md' ? 'dag-runner' : 'agent',
    updatedAt: '2026-05-30T08:30:00Z',
  }));
  backend.deleteSharedFile.mockImplementation(({ path }) => {
    sharedFiles.remove(path);
    return Promise.resolve({ deleted: true });
  });
  backend.saveTextFile.mockResolvedValue('/exports/work.json');
}

async function openSharedFilesPage() {
  render(<App />);
  await waitForBackendThreadHeading();
  fireEvent.click(screen.getByLabelText('共享文件'));

  expect(await screen.findByText('final.md')).toBeInTheDocument();
  expect(screen.getByText('work.json')).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: /刷新/ })).not.toBeInTheDocument();
  expect(screen.getByRole('tab', { name: '全部 2' })).toBeInTheDocument();
  expect(screen.getByRole('tab', { name: '最终产物 1' })).toBeInTheDocument();
  expect(screen.getByRole('tab', { name: '工作文件 1' })).toBeInTheDocument();
  await waitFor(() => {
    expect(backend.listSharedFiles).toHaveBeenCalledWith();
  });
}

async function refreshSharedFilesFromBridge(sharedFiles) {
  sharedFiles.add({
    path: 'scratch/notes.md',
    content: 'fresh notes',
    updated_by: 'agent',
    updated_at: '2026-05-30T09:00:00Z',
  });
  await act(async () => {
    bridgeCallback?.({ type: 'ui/shared-files/changed', payload: { path: 'scratch/notes.md', action: 'write' } });
  });
  expect(await screen.findByText('notes.md')).toBeInTheDocument();
  expect(screen.getByRole('tab', { name: '全部 3' })).toBeInTheDocument();
  expect(screen.getByRole('tab', { name: '工作文件 2' })).toBeInTheDocument();
}

async function refreshSharedFilesFromFocus(sharedFiles) {
  sharedFiles.add({
    path: 'scratch/focus-refresh.md',
    content: 'focus refresh',
    updated_by: 'agent',
    updated_at: '2026-05-30T09:01:00Z',
  });
  await act(async () => {
    window.dispatchEvent(new Event('focus'));
  });
  expect(await screen.findByText('focus-refresh.md')).toBeInTheDocument();
  expect(screen.getByRole('tab', { name: '全部 4' })).toBeInTheDocument();
  expect(screen.getByRole('tab', { name: '工作文件 3' })).toBeInTheDocument();
}

async function previewFinalSharedFile() {
  const finalCard = screen.getByText('final.md').closest('article');
  expect(within(finalCard).getByText('最终产物')).toBeInTheDocument();
  expect(within(finalCard).getByRole('button', { name: '不可删除' })).toBeDisabled();
  fireEvent.click(within(finalCard).getByRole('button', { name: '打开' }));

  expect(await screen.findByRole('dialog', { name: '文件预览' })).toBeInTheDocument();
  expect(screen.getByText('FINAL CONTENT')).toBeInTheDocument();
  expect(backend.readSharedFile).toHaveBeenCalledWith({ path: 'reports/final.md' });
  fireEvent.click(screen.getByRole('button', { name: '关闭' }));
}

async function exportAndDeleteWorkSharedFile() {
  const workCard = screen.getByText('work.json').closest('article');
  fireEvent.click(within(workCard).getByRole('button', { name: '导出' }));
  await waitFor(() => {
    expect(backend.saveTextFile).toHaveBeenCalledWith({
      defaultPath: '/repo/app',
      defaultFilename: 'work.json',
      content: '{"step":1,"detail":true}',
    });
  });
  expect(await screen.findByText(/已保存到：\/exports\/work\.json/)).toBeInTheDocument();

  fireEvent.click(within(workCard).getByRole('button', { name: '删除' }));
  expect(await screen.findByRole('dialog', { name: '删除文件' })).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: '确认删除' }));
  await waitFor(() => {
    expect(backend.deleteSharedFile).toHaveBeenCalledWith({ path: 'scratch/work.json' });
  });
  expect(await screen.findByText(/已删除文件：scratch\/work\.json/)).toBeInTheDocument();
}

async function continueChatFromFinalSharedFile() {
  const remainingFinalCard = screen.getByText('final.md').closest('article');
  fireEvent.click(within(remainingFinalCard).getByRole('button', { name: '用此文件继续对话' }));
  const forkCard = await screen.findByTestId('fork-draft-card');
  expect(within(forkCard).getByText('继承自会话：后端线程')).toBeInTheDocument();
  expect(within(forkCard).getByRole('checkbox', { name: '选择共享文件 reports/final.md' })).toBeChecked();
}

  it('loads shared files from the shared-files RPC and wires open, export, delete, and continue actions', async () => {
    const sharedFiles = createSharedFileState();
    mockSharedFileWorkflow(sharedFiles);

    await openSharedFilesPage();
    await refreshSharedFilesFromBridge(sharedFiles);
    await refreshSharedFilesFromFocus(sharedFiles);
    await previewFinalSharedFile();
    await exportAndDeleteWorkSharedFile();
    await continueChatFromFinalSharedFile();
  });

  it('formats markdown-fenced JSON shared files for the row summary and preview modal', async () => {
    const content = [
      '```json',
      JSON.stringify({
        videos: [{
          title: '月薪5000我是怎么在上海活下去的',
          hook: '很多人问我，5000块在上海怎么活？',
          script: '开场：我来上海三年了，最低的时候月薪5000。',
        }],
        thumbnail_idea: '本人手写账单特写',
      }),
      '```',
    ].join('\n');
    backend.listSharedFiles.mockResolvedValue({
      files: [{
        path: 'reports/douyin_viral_scripts.md',
        content,
        updated_by: 'node-router',
        updated_at: '2026-06-03T12:59:59Z',
      }],
      finalOutputRefs: [{
        path: 'reports/douyin_viral_scripts.md',
        runKey: 'run-ui-1',
        dagKey: 'douyin-viral-script-daily-5pm',
        sourceNodeKey: 'generate_douyin_scripts',
      }],
      sharedFileRetention: {
        items: [{ path: 'reports/douyin_viral_scripts.md', protected: true, cleanupCandidate: false, reason: 'final_output' }],
        protectedCount: 1,
        cleanupCandidateCount: 0,
      },
    });
    backend.readSharedFile.mockResolvedValue({
      path: 'reports/douyin_viral_scripts.md',
      content,
      updatedBy: 'node-router',
      updatedAt: '2026-06-03T12:59:59Z',
    });

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('共享文件'));

    const finalCard = (await screen.findByText('douyin_viral_scripts.md')).closest('article');
    expect(within(finalCard).getByText(/JSON 对象 · videos: 1 项/)).toBeInTheDocument();
    expect(within(finalCard).queryByText(/```json/)).not.toBeInTheDocument();

    fireEvent.click(within(finalCard).getByRole('button', { name: '打开' }));
    const dialog = await screen.findByRole('dialog', { name: '文件预览' });
    expect(within(dialog).getByText('JSON（Markdown 代码块）')).toBeInTheDocument();

    const preview = appOverlayHost.querySelector('.shared-file-content-preview');
    expect(preview?.textContent).toContain('"videos": [');
    expect(preview?.textContent).toContain('"title": "月薪5000我是怎么在上海活下去的"');
    expect(preview?.textContent).not.toContain('```json');
  });

  it('renders invalid markdown-fenced JSON-like shared files without showing parse errors', async () => {
    const content = [
      '```json',
      '{"videos":[{"title":"月薪5000我是怎么在上海活下去的","hook":"很多人问我，5000块在上海怎么活？","thumbnail_idea":"本人手写账单特写，标注"月薪5000存款5万"红色大字","cta":"评论区报一下"}]}',
      '```',
    ].join('\n');
    backend.listSharedFiles.mockResolvedValue({
      files: [{
        path: 'reports/douyin_viral_scripts.md',
        content,
        updated_by: 'node-router',
        updated_at: '2026-06-03T12:59:59Z',
      }],
      finalOutputRefs: [{
        path: 'reports/douyin_viral_scripts.md',
        runKey: 'run-ui-1',
        dagKey: 'douyin-viral-script-daily-5pm',
        sourceNodeKey: 'generate_douyin_scripts',
      }],
      sharedFileRetention: {
        items: [{ path: 'reports/douyin_viral_scripts.md', protected: true, cleanupCandidate: false, reason: 'final_output' }],
        protectedCount: 1,
        cleanupCandidateCount: 0,
      },
    });
    backend.readSharedFile.mockResolvedValue({
      path: 'reports/douyin_viral_scripts.md',
      content,
      updatedBy: 'node-router',
      updatedAt: '2026-06-03T12:59:59Z',
    });

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('共享文件'));

    const finalCard = (await screen.findByText('douyin_viral_scripts.md')).closest('article');
    expect(within(finalCard).getByText(/类 JSON · videos: 1 项/)).toBeInTheDocument();
    expect(within(finalCard).queryByText(/JSON 格式化失败|JSON Parse error|Unrecognized token/)).not.toBeInTheDocument();

    fireEvent.click(within(finalCard).getByRole('button', { name: '打开' }));
    const dialog = await screen.findByRole('dialog', { name: '文件预览' });
    expect(within(dialog).getByText('类 JSON（Markdown 代码块）')).toBeInTheDocument();

    const preview = appOverlayHost.querySelector('.shared-file-content-preview');
    expect(preview?.textContent).toContain('\n    "hook":');
    expect(preview?.textContent).toContain('标注"月薪5000存款5万"红色大字');
    expect(preview?.textContent).not.toMatch(/JSON 格式化失败|JSON Parse error|Unrecognized token|```json/);
  });

  it('keeps the shared-file delete dialog open while deletion is pending', async () => {
    const deletePending = deferred();
    backend.listSharedFiles.mockResolvedValue({
      files: [{
        path: 'scratch/work.json',
        content: '{"step":1}',
        updated_by: 'agent',
        updated_at: '2026-05-30T07:00:00Z',
      }],
      memory: [{
        path: 'scratch/work.json',
        content: '{"step":1}',
        updated_by: 'agent',
        updated_at: '2026-05-30T07:00:00Z',
      }],
      finalOutputRefs: [],
      sharedFileRetention: {
        items: [{ path: 'scratch/work.json', protected: false, cleanupCandidate: true, reason: 'unreferenced' }],
        protectedCount: 0,
        cleanupCandidateCount: 1,
      },
    });
    backend.deleteSharedFile.mockReturnValue(deletePending.promise);

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('共享文件'));

    const workCard = (await screen.findByText('work.json')).closest('article');
    fireEvent.click(within(workCard).getByRole('button', { name: '删除' }));
    let dialog = await screen.findByRole('dialog', { name: '删除文件' });
    fireEvent.click(within(dialog).getByRole('button', { name: '确认删除' }));
    await waitFor(() => {
      expect(within(screen.getByRole('dialog', { name: '删除文件' })).getByRole('button', { name: '删除中...' })).toBeDisabled();
    });

    dialog = screen.getByRole('dialog', { name: '删除文件' });
    fireEvent.keyDown(dialog, { key: 'Escape', code: 'Escape' });
    expect(screen.getByRole('dialog', { name: '删除文件' })).toBeInTheDocument();

    await act(async () => {
      deletePending.resolve({ deleted: true });
    });
    await waitFor(() => {
      expect(screen.queryByRole('dialog', { name: '删除文件' })).not.toBeInTheDocument();
    });
  });

  it('accepts the legacy shared-files response without final-output metadata', async () => {
    backend.listSharedFiles.mockResolvedValue({
      memory: [{
        path: 'scratch/legacy.md',
        content: 'legacy shared file',
        updated_by: 'agent',
        updated_at: '2026-05-30T09:00:00Z',
      }],
    });

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('共享文件'));

    expect(await screen.findByText('legacy.md')).toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(screen.getByRole('tab', { name: '全部 1' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: '工作文件 1' })).toBeInTheDocument();
  });

  it('keeps cached shared files visible when navigating back and refreshes silently', async () => {
    let memoryFiles = [{
      path: 'reports/final.md',
      content: 'final summary',
      updated_by: 'dag-runner',
      updated_at: '2026-05-30T08:00:00Z',
    }];
    const memoryPayload = () => ({
      files: memoryFiles,
      memory: memoryFiles,
      finalOutputRefs: [{ path: 'reports/final.md', runKey: 'run-1', dagKey: 'daily-brief', sourceNodeKey: 'report' }],
      sharedFileRetention: {
        items: [{ path: 'reports/final.md', protected: true, cleanupCandidate: false, reason: 'final_output' }],
        protectedCount: 1,
        cleanupCandidateCount: 0,
      },
    });
    backend.listSharedFiles.mockImplementation(() => Promise.resolve(memoryPayload()));

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('共享文件'));
    expect(await screen.findByText('final.md')).toBeInTheDocument();

    fireEvent.click(screen.getByLabelText('新对话'));
    memoryFiles = [{
      path: 'scratch/notes.md',
      content: 'fresh notes',
      updated_by: 'agent',
      updated_at: '2026-05-30T09:00:00Z',
    }];
    fireEvent.click(screen.getByLabelText('共享文件'));

    expect(screen.queryByText('正在加载共享文件...')).not.toBeInTheDocument();
    expect(screen.getByText('final.md')).toBeInTheDocument();
    expect(await screen.findByText('notes.md')).toBeInTheDocument();
    expect(screen.queryByText('final.md')).not.toBeInTheDocument();
  });

  it('does not poll shared files with a page interval', async () => {
    const intervalSpy = vi.spyOn(window, 'setInterval');
    try {
      backend.listSharedFiles.mockResolvedValue({
        files: [{
          path: 'reports/final.md',
          content: 'final summary',
          updated_by: 'dag-runner',
          updated_at: '2026-05-30T08:00:00Z',
        }],
        finalOutputRefs: [{ path: 'reports/final.md', runKey: 'run-1', dagKey: 'daily-brief', sourceNodeKey: 'report' }],
        sharedFileRetention: {
          items: [{ path: 'reports/final.md', protected: true, cleanupCandidate: false, reason: 'final_output' }],
          protectedCount: 1,
          cleanupCandidateCount: 0,
        },
      });

      render(<App />);
      await waitForBackendThreadHeading();
      fireEvent.click(screen.getByLabelText('共享文件'));

      expect(await screen.findByText('final.md')).toBeInTheDocument();
      expect(intervalSpy.mock.calls.filter((call) => call[1] === 4000)).toHaveLength(0);
    }
    finally {
      intervalSpy.mockRestore();
    }
  });

  it('keeps cached shared files visible and exposes retry when a background sync fails', async () => {
    let memoryFiles = [{
      path: 'reports/final.md',
      content: 'final summary',
      updated_by: 'dag-runner',
      updated_at: '2026-05-30T08:00:00Z',
    }];
    const memoryPayload = () => ({
      files: memoryFiles,
      memory: memoryFiles,
      finalOutputRefs: [{ path: 'reports/final.md', runKey: 'run-1', dagKey: 'daily-brief', sourceNodeKey: 'report' }],
      sharedFileRetention: {
        items: [{ path: 'reports/final.md', protected: true, cleanupCandidate: false, reason: 'final_output' }],
        protectedCount: 1,
        cleanupCandidateCount: 0,
      },
    });
    backend.listSharedFiles.mockImplementation(() => Promise.resolve(memoryPayload()));

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('共享文件'));
    expect(await screen.findByText('final.md')).toBeInTheDocument();

    backend.listSharedFiles.mockRejectedValueOnce(new Error('shared files backend offline'));
    await act(async () => {
      bridgeCallback?.({ type: 'ui/shared-files/changed', payload: { path: 'reports/final.md', action: 'write' } });
      await Promise.resolve();
    });

    expect(screen.getByText('final.md')).toBeInTheDocument();
    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('同步共享文件失败，当前显示上次成功数据。');
    expect(alert).not.toHaveTextContent('shared files backend offline');
    expect(frontendHealthSnapshot()).toEqual(expect.arrayContaining([
      expect.objectContaining({ actionId: 'file.dashboard.load', diagnosticId: expect.any(String) }),
    ]));

    memoryFiles = [{
      path: 'scratch/notes.md',
      content: 'fresh notes',
      updated_by: 'agent',
      updated_at: '2026-05-30T09:00:00Z',
    }];
    fireEvent.click(screen.getByRole('button', { name: '重试同步' }));

    expect(await screen.findByText('notes.md')).toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('shows a retryable blocking error instead of an empty shared-files state on initial load failure', async () => {
    backend.listSharedFiles.mockRejectedValueOnce(new Error('shared files backend offline'));

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('共享文件'));

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('加载共享文件失败，请重试。');
    expect(alert).not.toHaveTextContent('shared files backend offline');
    expect(frontendHealthSnapshot()).toEqual(expect.arrayContaining([
      expect.objectContaining({ actionId: 'file.dashboard.load', diagnosticId: expect.any(String) }),
    ]));
    expect(screen.queryByText('还没有文件产物')).not.toBeInTheDocument();

    backend.listSharedFiles.mockResolvedValueOnce({
      files: [{
        path: 'scratch/notes.md',
        content: 'fresh notes',
        updated_by: 'agent',
        updated_at: '2026-05-30T09:00:00Z',
      }],
      finalOutputRefs: [],
      sharedFileRetention: { items: [], protectedCount: 0, cleanupCandidateCount: 0 },
    });
    fireEvent.click(within(alert).getByRole('button', { name: '重试同步' }));

    expect(await screen.findByText('notes.md')).toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('loads DAG list, detail, runs and selected run through legacy dashboard RPCs', async () => {
    const runningDag = {
      dag_key: 'daily-brief',
      title: 'Daily Brief',
      description: '每日简报',
      status: 'ready',
      trigger: 'manual',
      version: 7,
      latest_run: { run_key: 'run-1', status: 'running', metadata: { final_output: '正在汇总' } },
    };
    backend.getDashboardPage.mockImplementation(({ page }) => Promise.resolve(
      page === 'dags'
        ? {
          dags: [
            runningDag,
            { dag_key: 'weekly-report', title: 'Weekly Report', status: 'ready', trigger: 'scheduled', cron_expr: '0 8 * * 1', next_run_at: '2026-06-01T00:00:00Z' },
            { dag_key: 'done-flow', title: 'Done Flow', status: 'done', trigger: 'manual', latest_run: { run_key: 'run-done', status: 'done' } },
          ],
        }
        : { skills: [] },
    ));
    backend.getDagDetail.mockResolvedValue({
      dag: runningDag,
      nodes: [
        { node_key: 'draft', title: '起草', node_type: 'agent', status: 'running', depends_on: [], config: { provider: 'codex', model: 'gpt-5' } },
      ],
    });
    backend.getDagRuns.mockImplementation(({ status }) => Promise.resolve({
      runs: status === 'running'
        ? [{ run_key: 'run-1', status: 'running', metadata: { final_output: '正在汇总' } }]
        : [
          { run_key: 'run-1', status: 'running', metadata: { final_output: '正在汇总' } },
          { run_key: 'run-0', status: 'done' },
        ],
    }));
    backend.getDagRun.mockResolvedValue({
      run: { run_key: 'run-1', status: 'running', metadata: { final_output: { text: '最终简报完成' } } },
      nodes: [{ node_key: 'draft', status: 'running' }],
    });

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('自动化'));

    expect((await screen.findAllByText('Daily Brief')).length).toBeGreaterThanOrEqual(2);
    expect(screen.getByRole('tab', { name: '进行中 1' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: '定时任务 1' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: '历史记录 1' })).toBeInTheDocument();
    expect(await screen.findByText('最终简报完成')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /刷新/ })).not.toBeInTheDocument();

    await waitFor(() => {
      expect(backend.getDashboardPage).toHaveBeenCalledWith({ cwd: '/repo/app', page: 'dags' });
      expect(backend.getDagDetail).toHaveBeenCalledWith({ dagKey: 'daily-brief' });
      expect(backend.getDagRuns).toHaveBeenCalledWith({ dagKey: 'daily-brief', limit: 30 });
      expect(backend.getDagRuns).toHaveBeenCalledWith({ dagKey: 'daily-brief', status: 'running', limit: 1 });
      expect(backend.getDagRun).toHaveBeenCalledWith({ runKey: 'run-1' });
    });
  });

  it('renders workflow topology, shared files, and readable final output file panels', async () => {
    const dag = {
      dag_key: 'report-flow',
      title: '报告流水线',
      status: 'succeeded',
      trigger: 'manual',
      version: 3,
      latest_run: { run_key: 'run-report', status: 'succeeded' },
    };
    const nodes = [{
      node_key: 'collect',
      title: '收集资料',
      status: 'succeeded',
      config: { outputs: { to_sharedfile: { path: 'brief/raw.md', lock_mode: 'exclusive' } } },
    }, {
      node_key: 'write',
      title: '撰写报告',
      status: 'succeeded',
      depends_on: ['collect', 'external-input'],
      config: {
        inputs: { from_sharedfiles: ['brief/raw.md'] },
        outputs: { to_sharedfile: { path: 'reports/final.md', lock_mode: 'append' } },
      },
    }];
    backend.getDashboardPage.mockImplementation(({ page }) => (
      page === 'dags' ? Promise.resolve({ dags: [dag] }) : Promise.resolve({ skills: [] })
    ));
    backend.getDagDetail.mockResolvedValue({ dag, nodes });
    backend.getDagRuns.mockImplementation(({ status }) => Promise.resolve({
      runs: status === 'running' ? [] : [{ run_key: 'run-report', status: 'succeeded', metadata: { final_output: { kind: 'sharedfile', path: 'reports/final.md' } } }],
    }));
    backend.getDagRun.mockResolvedValue({
      run: { run_key: 'run-report', status: 'succeeded', metadata: { final_output: { kind: 'sharedfile', path: 'reports/final.md' } } },
      nodes,
    });
    backend.readSharedFile.mockResolvedValue({ path: 'reports/final.md', content: '最终报告正文' });

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('自动化'));

    expect((await screen.findAllByText('报告流水线')).length).toBeGreaterThanOrEqual(2);
    expect(await screen.findByText('流程图')).toBeInTheDocument();
    expect((await screen.findAllByText(/收集资料/)).length).toBeGreaterThanOrEqual(1);
    expect(await screen.findByText(/收集资料 --> 撰写报告/)).toBeInTheDocument();
    expect(await screen.findByText(/外部依赖 1 --> 撰写报告/)).toBeInTheDocument();

    expect(screen.getByText('工作文件')).toBeInTheDocument();
    expect(screen.getAllByText('brief/raw.md').length).toBeGreaterThanOrEqual(1);
    expect(screen.getAllByText('reports/final.md').length).toBeGreaterThanOrEqual(2);
    expect(screen.getByText('读取')).toBeInTheDocument();
    expect(screen.getByText('写入 · 追加写入')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '读取最终结果' }));
    await waitFor(() => {
      expect(backend.readSharedFile).toHaveBeenCalledWith({ path: 'reports/final.md' });
    });
    expect(await screen.findByText('最终报告正文')).toBeInTheDocument();
  });

  it('auto-updates workflow page without a manual refresh button', async () => {
    let dags = [{
      dag_key: 'flow-a',
      title: '流程 A',
      status: 'running',
      trigger: 'manual',
      version: 1,
      latest_run: { run_key: 'run-a', status: 'running' },
    }];
    backend.getDashboardPage.mockImplementation(({ page }) => Promise.resolve(
      page === 'dags' ? { dags } : { skills: [] },
    ));
    backend.getDagDetail.mockImplementation(({ dagKey }) => {
      const dag = dags.find((item) => item.dag_key === dagKey) || dags[0];
      const suffix = (dag?.title || '').split(' ').pop() || '';
      return Promise.resolve({
        dag,
        nodes: [{ node_key: 'step', title: `步骤 ${suffix}`, node_type: 'agent', status: 'running', depends_on: [], config: {} }],
      });
    });
    backend.getDagRuns.mockImplementation(({ dagKey, status }) => {
      const dag = dags.find((item) => item.dag_key === dagKey) || dags[0];
      if (status === 'running') return Promise.resolve({ runs: dag?.latest_run ? [dag.latest_run] : [] });
      return Promise.resolve({ runs: dag?.latest_run ? [dag.latest_run] : [] });
    });
    backend.getDagRun.mockImplementation(({ runKey }) => Promise.resolve({
      run: { run_key: runKey, status: 'running' },
      nodes: [],
    }));

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('自动化'));

    expect((await screen.findAllByText('流程 A')).length).toBeGreaterThanOrEqual(1);
    expect(screen.queryByRole('button', { name: /刷新/ })).not.toBeInTheDocument();

    dags = [{
      dag_key: 'flow-b',
      title: '流程 B',
      status: 'running',
      trigger: 'manual',
      version: 2,
      latest_run: { run_key: 'run-b', status: 'running' },
    }];
    await act(async () => {
      bridgeCallback?.({ type: 'task/node/statusChanged', payload: { dag_key: 'flow-b', run_key: 'run-b', node_key: 'step', new_status: 'running' } });
    });

    expect((await screen.findAllByText('流程 B')).length).toBeGreaterThanOrEqual(1);
    expect(screen.queryByText('流程 A')).not.toBeInTheDocument();

    dags = [{
      dag_key: 'flow-c',
      title: '流程 C',
      status: 'running',
      trigger: 'manual',
      version: 3,
      latest_run: { run_key: 'run-c', status: 'running' },
    }];
    await act(async () => {
      window.dispatchEvent(new Event('focus'));
    });

    expect((await screen.findAllByText('流程 C')).length).toBeGreaterThanOrEqual(1);
    expect(screen.queryByText('流程 B')).not.toBeInTheDocument();
  });

  it('does not poll workflow data with a page interval', async () => {
    const intervalSpy = vi.spyOn(window, 'setInterval');
    try {
      const runningDag = {
        dag_key: 'flow-a',
        title: '流程 A',
        status: 'running',
        trigger: 'manual',
        version: 1,
        latest_run: { run_key: 'run-a', status: 'running' },
      };
      backend.getDashboardPage.mockImplementation(({ page }) => Promise.resolve(
        page === 'dags' ? { dags: [runningDag] } : { skills: [] },
      ));
      backend.getDagDetail.mockResolvedValue({
        dag: runningDag,
        nodes: [{ node_key: 'step', title: '步骤 A', node_type: 'agent', status: 'running', depends_on: [], config: {} }],
      });
      backend.getDagRuns.mockImplementation(({ status }) => Promise.resolve({
        runs: status === 'running' ? [{ run_key: 'run-a', status: 'running' }] : [{ run_key: 'run-a', status: 'running' }],
      }));
      backend.getDagRun.mockResolvedValue({
        run: { run_key: 'run-a', status: 'running' },
        nodes: [],
      });

      render(<App />);
      await waitForBackendThreadHeading();
      fireEvent.click(screen.getByLabelText('自动化'));

      expect((await screen.findAllByText('流程 A')).length).toBeGreaterThanOrEqual(1);
      expect(intervalSpy.mock.calls.filter((call) => call[1] === 4000)).toHaveLength(0);
    }
    finally {
      intervalSpy.mockRestore();
    }
  });

  it('keeps cached workflow data visible and exposes retry when a background sync fails', async () => {
    let dags = [{
      dag_key: 'flow-a',
      title: '流程 A',
      status: 'running',
      trigger: 'manual',
      version: 1,
      latest_run: { run_key: 'run-a', status: 'running' },
    }];
    backend.getDashboardPage.mockImplementation(({ page }) => Promise.resolve(
      page === 'dags' ? { dags } : { skills: [] },
    ));
    backend.getDagDetail.mockImplementation(({ dagKey }) => {
      const dag = dags.find((item) => item.dag_key === dagKey) || dags[0];
      const suffix = (dag?.title || '').split(' ').pop() || '';
      return Promise.resolve({
        dag,
        nodes: [{ node_key: 'step', title: `步骤 ${suffix}`, node_type: 'agent', status: 'running', depends_on: [], config: {} }],
      });
    });
    backend.getDagRuns.mockImplementation(({ dagKey, status }) => {
      const dag = dags.find((item) => item.dag_key === dagKey) || dags[0];
      if (status === 'running') return Promise.resolve({ runs: dag?.latest_run ? [dag.latest_run] : [] });
      return Promise.resolve({ runs: dag?.latest_run ? [dag.latest_run] : [] });
    });
    backend.getDagRun.mockImplementation(({ runKey }) => Promise.resolve({
      run: { run_key: runKey, status: 'running' },
      nodes: [],
    }));

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('自动化'));
    expect((await screen.findAllByText('流程 A')).length).toBeGreaterThanOrEqual(1);

    backend.getDashboardPage.mockRejectedValueOnce(new Error('workflow backend offline'));
    await act(async () => {
      bridgeCallback?.({ type: 'task/node/statusChanged', payload: { dag_key: 'flow-a', run_key: 'run-a', node_key: 'step', new_status: 'running' } });
      await Promise.resolve();
    });

    expect(screen.getAllByText('流程 A').length).toBeGreaterThanOrEqual(1);
    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('同步自动化失败，当前显示上次成功数据。');
    expect(alert).not.toHaveTextContent('workflow backend offline');
    expect(frontendHealthSnapshot()).toEqual(expect.arrayContaining([
      expect.objectContaining({ actionId: 'workflow.dashboard.load', diagnosticId: expect.any(String) }),
    ]));

    dags = [{
      dag_key: 'flow-b',
      title: '流程 B',
      status: 'running',
      trigger: 'manual',
      version: 2,
      latest_run: { run_key: 'run-b', status: 'running' },
    }];
    fireEvent.click(screen.getByRole('button', { name: '重试同步' }));

    expect((await screen.findAllByText('流程 B')).length).toBeGreaterThanOrEqual(1);
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('clears a stale workflow sync alert after focus refresh succeeds', async () => {
    let dags = [{
      dag_key: 'flow-a',
      title: '流程 A',
      status: 'running',
      trigger: 'manual',
      version: 1,
      latest_run: { run_key: 'run-a', status: 'running' },
    }];
    backend.getDashboardPage.mockImplementation(({ page }) => Promise.resolve(
      page === 'dags' ? { dags } : { skills: [] },
    ));
    backend.getDagDetail.mockImplementation(({ dagKey }) => {
      const dag = dags.find((item) => item.dag_key === dagKey) || dags[0];
      const suffix = (dag?.title || '').split(' ').pop() || '';
      return Promise.resolve({
        dag,
        nodes: [{ node_key: 'step', title: `步骤 ${suffix}`, node_type: 'agent', status: 'running', depends_on: [], config: {} }],
      });
    });
    backend.getDagRuns.mockImplementation(({ dagKey, status }) => {
      const dag = dags.find((item) => item.dag_key === dagKey) || dags[0];
      if (status === 'running') return Promise.resolve({ runs: dag?.latest_run ? [dag.latest_run] : [] });
      return Promise.resolve({ runs: dag?.latest_run ? [dag.latest_run] : [] });
    });
    backend.getDagRun.mockImplementation(({ runKey }) => Promise.resolve({
      run: { run_key: runKey, status: 'running' },
      nodes: [],
    }));

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('自动化'));
    expect((await screen.findAllByText('流程 A')).length).toBeGreaterThanOrEqual(1);

    backend.getDashboardPage.mockRejectedValueOnce(new Error('workflow backend offline'));
    await act(async () => {
      bridgeCallback?.({ type: 'task/node/statusChanged', payload: { dag_key: 'flow-a', run_key: 'run-a', node_key: 'step', new_status: 'running' } });
      await Promise.resolve();
    });
    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('同步自动化失败，当前显示上次成功数据。');
    expect(alert).not.toHaveTextContent('workflow backend offline');

    dags = [{
      dag_key: 'flow-b',
      title: '流程 B',
      status: 'running',
      trigger: 'manual',
      version: 2,
      latest_run: { run_key: 'run-b', status: 'running' },
    }];
    await act(async () => {
      window.dispatchEvent(new Event('focus'));
    });

    expect((await screen.findAllByText('流程 B')).length).toBeGreaterThanOrEqual(1);
    await waitFor(() => {
      expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    });
  });

  it('coalesces selected workflow detail and run refreshes when events and retry overlap', async () => {
    const runningDag = {
      dag_key: 'flow-a',
      title: '流程 A',
      status: 'running',
      trigger: 'manual',
      version: 1,
      latest_run: { run_key: 'run-a', status: 'running' },
    };
    const node = { node_key: 'step', title: '步骤 A', node_type: 'agent', status: 'running', depends_on: [], config: {} };
    backend.getDashboardPage.mockImplementation(({ page }) => Promise.resolve(
      page === 'dags' ? { dags: [runningDag] } : { skills: [] },
    ));
    backend.getDagDetail.mockResolvedValue({ dag: runningDag, nodes: [node] });
    backend.getDagRuns.mockImplementation(({ status }) => Promise.resolve({
      runs: status === 'running' ? [runningDag.latest_run] : [runningDag.latest_run],
    }));
    backend.getDagRun.mockResolvedValue({ run: runningDag.latest_run, nodes: [node] });

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('自动化'));
    expect((await screen.findAllByText('流程 A')).length).toBeGreaterThanOrEqual(1);
    expect((await screen.findAllByText('步骤 A')).length).toBeGreaterThanOrEqual(1);

    vi.clearAllMocks();
    const detailRefresh = deferred();
    const recentRunsRefresh = deferred();
    const activeRunsRefresh = deferred();
    const runRefresh = deferred();
    backend.getDashboardPage
      .mockImplementationOnce(({ page }) => (
        page === 'dags' ? Promise.reject(new Error('workflow backend offline')) : Promise.resolve({ skills: [] })
      ))
      .mockImplementation(({ page }) => Promise.resolve(
        page === 'dags' ? { dags: [runningDag] } : { skills: [] },
      ));
    backend.getDagDetail.mockImplementation(() => detailRefresh.promise);
    backend.getDagRuns.mockImplementation(({ status }) => (
      status === 'running' ? activeRunsRefresh.promise : recentRunsRefresh.promise
    ));
    backend.getDagRun.mockImplementation(() => runRefresh.promise);

    await act(async () => {
      bridgeCallback?.({ type: 'task/node/statusChanged', payload: { dag_key: 'flow-a', run_key: 'run-a', node_key: 'step', new_status: 'running' } });
      await Promise.resolve();
    });
    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('同步自动化失败，当前显示上次成功数据。');
    expect(alert).not.toHaveTextContent('workflow backend offline');
    await waitFor(() => expect(backend.getDagDetail).toHaveBeenCalledTimes(1));

    fireEvent.click(within(alert).getByRole('button', { name: '重试同步' }));
    await act(async () => {
      bridgeCallback?.({ type: 'task/node/statusChanged', payload: { dag_key: 'flow-a', run_key: 'run-a', node_key: 'step', new_status: 'running' } });
      await Promise.resolve();
      await Promise.resolve();
    });
    await waitFor(() => expect(backend.getDashboardPage.mock.calls.length).toBeGreaterThanOrEqual(2));

    expect(backend.getDagDetail).toHaveBeenCalledTimes(1);
    expect(backend.getDagRuns).toHaveBeenCalledTimes(2);
    expect(backend.getDagRun).toHaveBeenCalledTimes(1);

    await act(async () => {
      detailRefresh.reject(new Error('detail backend offline'));
      recentRunsRefresh.resolve({ runs: [runningDag.latest_run] });
      activeRunsRefresh.resolve({ runs: [runningDag.latest_run] });
      runRefresh.resolve({ run: runningDag.latest_run, nodes: [node] });
    });

    const detailAlert = await screen.findByRole('alert');
    expect(detailAlert).toHaveTextContent('同步自动化失败，当前显示上次成功数据。');
    expect(detailAlert).not.toHaveTextContent('detail backend offline');
  });

  it('shows a retryable blocking error instead of an empty workflow state on initial load failure', async () => {
    const flow = {
      dag_key: 'flow-a',
      title: '流程 A',
      status: 'running',
      trigger: 'manual',
      version: 1,
      latest_run: { run_key: 'run-a', status: 'running' },
    };
    backend.getDashboardPage.mockImplementation(({ page }) => (
      page === 'dags'
        ? Promise.reject(new Error('workflow backend offline'))
        : Promise.resolve({ skills: [] })
    ));

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('自动化'));

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('加载自动化失败，请重试。');
    expect(alert).not.toHaveTextContent('workflow backend offline');
    expect(frontendHealthSnapshot()).toEqual(expect.arrayContaining([
      expect.objectContaining({ actionId: 'workflow.dashboard.load', diagnosticId: expect.any(String) }),
    ]));
    expect(screen.queryByText('无任务')).not.toBeInTheDocument();

    backend.getDashboardPage.mockImplementation(({ page }) => (
      page === 'dags' ? Promise.resolve({ dags: [flow] }) : Promise.resolve({ skills: [] })
    ));
    backend.getDagDetail.mockResolvedValue({
      dag: flow,
      nodes: [{ node_key: 'step', title: '步骤 A', node_type: 'agent', status: 'running', depends_on: [], config: {} }],
    });
    backend.getDagRuns.mockResolvedValue({ runs: [{ run_key: 'run-a', status: 'running' }] });
    backend.getDagRun.mockResolvedValue({ run: { run_key: 'run-a', status: 'running' }, nodes: [] });
    fireEvent.click(within(alert).getByRole('button', { name: '重试同步' }));

    expect((await screen.findAllByText('流程 A')).length).toBeGreaterThanOrEqual(1);
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('keeps cached workflow data visible when navigating back and refreshes silently', async () => {
    let dags = [{
      dag_key: 'flow-a',
      title: '流程 A',
      status: 'running',
      trigger: 'manual',
      version: 1,
      latest_run: { run_key: 'run-a', status: 'running' },
    }];
    backend.getDashboardPage.mockImplementation(({ page }) => Promise.resolve(
      page === 'dags' ? { dags } : { skills: [] },
    ));
    backend.getDagDetail.mockImplementation(({ dagKey }) => {
      const dag = dags.find((item) => item.dag_key === dagKey) || dags[0];
      const suffix = (dag?.title || '').split(' ').pop() || '';
      return Promise.resolve({
        dag,
        nodes: [{ node_key: 'step', title: `步骤 ${suffix}`, node_type: 'agent', status: 'running', depends_on: [], config: {} }],
      });
    });
    backend.getDagRuns.mockImplementation(({ dagKey }) => {
      const dag = dags.find((item) => item.dag_key === dagKey) || dags[0];
      return Promise.resolve({ runs: dag?.latest_run ? [dag.latest_run] : [] });
    });
    backend.getDagRun.mockImplementation(({ runKey }) => Promise.resolve({
      run: { run_key: runKey, status: 'running' },
      nodes: [],
    }));

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('自动化'));
    expect((await screen.findAllByText('流程 A')).length).toBeGreaterThanOrEqual(1);

    fireEvent.click(screen.getByLabelText('新对话'));
    dags = [{
      dag_key: 'flow-b',
      title: '流程 B',
      status: 'running',
      trigger: 'manual',
      version: 2,
      latest_run: { run_key: 'run-b', status: 'running' },
    }];
    fireEvent.click(screen.getByLabelText('自动化'));

    expect(screen.queryByText('正在加载自动化...')).not.toBeInTheDocument();
    expect(screen.queryByText('正在加载详情...')).not.toBeInTheDocument();
    expect(screen.getAllByText('流程 A').length).toBeGreaterThanOrEqual(1);
    expect((await screen.findAllByText('流程 B')).length).toBeGreaterThanOrEqual(1);
    expect(screen.queryByText('流程 A')).not.toBeInTheDocument();
  });

  it('allows selecting an empty DAG category and shows an empty state', async () => {
    const scheduledDag = {
      dag_key: 'weekly-report',
      title: 'Weekly Report',
      description: '每周报告',
      status: 'ready',
      trigger: 'scheduled',
      cron_expr: '0 8 * * 1',
      version: 3,
    };
    backend.getDashboardPage.mockImplementation(({ page }) => Promise.resolve(
      page === 'dags' ? { dags: [scheduledDag] } : { skills: [] },
    ));
    backend.getDagDetail.mockResolvedValue({ dag: scheduledDag, nodes: [] });
    backend.getDagRuns.mockResolvedValue({ runs: [] });

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('自动化'));

    await waitFor(() => {
      expect(screen.getByRole('tab', { name: '定时任务 1' })).toHaveAttribute('aria-selected', 'true');
    });
    fireEvent.click(screen.getByRole('tab', { name: '进行中 0' }));

    await waitFor(() => {
      expect(screen.getByRole('tab', { name: '进行中 0' })).toHaveAttribute('aria-selected', 'true');
    });
    expect(screen.getByText('无任务')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Weekly Report/ })).not.toBeInTheDocument();
  });

  it('presents workflow schedules without raw cron or DAG internals', async () => {
    const scheduledDag = {
      dag_key: 'daily_remote_main_pr_review',
      title: '每日远程 main PR 审核',
      status: 'ready',
      trigger: 'scheduled',
      cron_expr: '0 1 * * *',
      next_run_at: '2026-06-01T01:00:00Z',
      version: 7,
    };
    backend.getDashboardPage.mockImplementation(({ page }) => Promise.resolve(
      page === 'dags' ? { dags: [scheduledDag] } : { skills: [] },
    ));
    backend.getDagDetail.mockResolvedValue({ dag: scheduledDag, nodes: [] });
    backend.getDagRuns.mockResolvedValue({ runs: [] });

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('自动化'));

    await waitFor(() => {
      expect(screen.getByRole('tab', { name: '定时任务 1' })).toHaveAttribute('aria-selected', 'true');
    });
    expect(screen.getAllByText('每天 01:00').length).toBeGreaterThanOrEqual(1);
    expect(screen.getByText('已启用')).toBeInTheDocument();
    expect(screen.queryByText('0 1 * * *')).not.toBeInTheDocument();
    expect(screen.queryByText('daily_remote_main_pr_review')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '修改计划' }));
    const dialog = await screen.findByRole('dialog', { name: '修改计划' });
    expect(within(dialog).queryByLabelText('Cron 表达式')).not.toBeInTheDocument();
    expect(within(dialog).getByLabelText('运行频率')).toHaveValue('daily');
    expect(within(dialog).getByLabelText('运行时间')).toHaveValue('01:00');
  });

function mockWorkflowDagLifecycle() {
  const dag = {
    dag_key: 'daily-brief',
    title: 'Daily Brief',
    description: '每日简报',
    status: 'ready',
    trigger: 'manual',
    version: 7,
  };
  const agentNode = {
    node_key: 'draft',
    title: '起草',
    node_type: 'agent',
    assigned_to: 'agent-a',
    depends_on: [],
    config: {
      provider: 'codex',
      model: 'gpt-5',
      prompt_key: 'main/writer',
      first_turn: '请起草简报',
    },
  };
  backend.getDashboardPage.mockImplementation(({ page }) => Promise.resolve(
    page === 'dags' ? { dags: [dag] } : { skills: [] },
  ));
  backend.getDagDetail.mockResolvedValue({ dag, nodes: [agentNode] });
  let hasActiveRun = false;
  backend.getDagRuns.mockImplementation(({ status }) => Promise.resolve({
    runs: status === 'running' && hasActiveRun ? [{ run_key: 'run-live', status: 'running' }] : [],
  }));
  backend.getDagRun.mockResolvedValue({ run: { run_key: 'run-live', status: 'running' }, nodes: [agentNode] });
  backend.startDag.mockImplementation(() => {
    hasActiveRun = true;
    return Promise.resolve({ runKey: 'run-live' });
  });
  backend.terminateDagRun.mockImplementation(() => {
    hasActiveRun = false;
    return Promise.resolve({ ok: true });
  });
  backend.getPreference.mockImplementation(({ key }) => Promise.resolve({
    'settings.provider.active': 'codex',
    'settings.provider.codex.model': 'gpt-5.5',
    'settings.provider.codex.effort': 'xhigh',
    'settings.provider.codex.codexHome': '/Users/test/.codex-alt',
    'settings.provider.codex.codexInstanceKey': 'desktop-main',
    'settings.provider.codex.codexModelProvider': 'openrouter',
    'settings.activePromptKey': 'main/reviewer',
  }[key] ?? null));
  backend.startThread.mockResolvedValue({ thread: { id: 'thread-design' }, provider: 'codex', modelProvider: 'codex' });
  backend.getThreadState.mockImplementation(({ threadId }) => Promise.resolve(
    threadId === 'thread-design'
      ? {
          timelinesByThread: {},
          activeThreadId: 'thread-design',
          threads: [{ id: 'thread-design', name: 'AI 设计流程', provider: 'codex', status: 'created', agentKey: 'dag_designer' }],
        }
      : {
          activeThreadId: 'thread-1',
          threads: [{ id: 'thread-1', name: '后端线程', provider: 'codex', status: '工作中' }],
          timelinesByThread: {
            'thread-1': [{ id: 'assistant-1', kind: 'assistant', text: '来自后端的消息', ts: '2026-05-30T00:00:00Z' }],
          },
          diffTextByThread: {
            'thread-1': 'diff --git a/file b/file',
          },
        },
  ));
}

async function openWorkflowDashboard() {
  render(<App />);
  fireEvent.click(await screen.findByLabelText('自动化'));
  expect((await screen.findAllByText('Daily Brief')).length).toBeGreaterThanOrEqual(2);
}

async function runAndStopWorkflowDag() {
  fireEvent.click(await screen.findByRole('button', { name: '运行' }));
  await waitFor(() => {
    expect(backend.startDag).toHaveBeenCalledWith(expect.objectContaining({
      dagKey: 'daily-brief',
      triggerSource: 'manual',
    }));
  });

  fireEvent.click(await screen.findByRole('button', { name: '停止运行' }));
  await waitFor(() => {
    expect(backend.terminateDagRun).toHaveBeenCalledWith({
      dagKey: 'daily-brief',
      runKey: 'run-live',
      reason: 'user_requested',
    });
  });
  await waitFor(() => expect(screen.queryByRole('button', { name: '停止运行' })).not.toBeInTheDocument());
}

async function createWorkflowSchedule() {
  fireEvent.click(screen.getByRole('button', { name: '创建定时任务' }));
  const scheduleDialog = await screen.findByRole('dialog', { name: '创建定时任务' });
  expect(scheduleDialog).toBeInTheDocument();
  expect(within(scheduleDialog).queryByLabelText('Cron 表达式')).not.toBeInTheDocument();
  fireEvent.change(within(scheduleDialog).getByLabelText('运行频率'), { target: { value: 'weekdays' } });
  fireEvent.change(within(scheduleDialog).getByLabelText('运行时间'), { target: { value: '09:00' } });
  expect(within(scheduleDialog).getByText('工作日 09:00 自动运行')).toBeInTheDocument();
  fireEvent.click(within(scheduleDialog).getByRole('button', { name: '创建定时任务' }));
  await waitFor(() => {
    expect(backend.applyDagOps).toHaveBeenCalledWith({
      dagKey: 'daily-brief',
      baseVersion: 7,
      ops: [{ op: 'update_dag', patch: { trigger: 'scheduled', cron_expr: 'CRON_TZ=Asia/Shanghai 0 9 * * 1-5' } }],
    });
  });
  expect(await screen.findByText('已保存定时任务')).toBeInTheDocument();
}

async function editWorkflowStep() {
  fireEvent.click(screen.getByText('高级设置'));
  fireEvent.input(screen.getByLabelText('名称'), { target: { value: '起草 v2' } });
  expect(screen.getByLabelText('名称')).toHaveValue('起草 v2');
  expect(screen.getByLabelText('执行者')).toHaveValue('agent-a');
  fireEvent.input(screen.getByLabelText('执行者'), { target: { value: 'agent-b' } });
  fireEvent.change(screen.getByLabelText('依赖步骤'), { target: { value: 'outline' } });
  expect(screen.queryByLabelText('Provider')).not.toBeInTheDocument();
  expect(screen.getByLabelText('执行引擎')).toHaveValue('codex');
  expect(screen.getByLabelText('Prompt Key')).toHaveValue('main/writer');
  fireEvent.click(screen.getByRole('button', { name: '保存步骤' }));
  await waitFor(() => {
    expect(backend.applyDagOps).toHaveBeenCalledWith({
      dagKey: 'daily-brief',
      baseVersion: 7,
      ops: [expect.objectContaining({
        op: 'update_node',
        node_key: 'draft',
        patch: expect.objectContaining({
          title: '起草 v2',
          assigned_to: 'agent-b',
          depends_on: ['outline'],
          config: expect.objectContaining({
            exec: expect.objectContaining({ provider: 'codex', model: 'gpt-5', prompt_key: 'main/writer' }),
          }),
        }),
      })],
    });
  });
}

async function deleteWorkflowDag() {
  fireEvent.click(screen.getByRole('button', { name: '删除' }));
  expect(await screen.findByRole('dialog', { name: '删除自动化' })).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: '确认删除' }));
  await waitFor(() => {
    expect(backend.deleteDag).toHaveBeenCalledWith({ dagKey: 'daily-brief' });
  });
}

async function designWorkflowWithAi() {
  fireEvent.click(screen.getByRole('button', { name: '自由设计' }));
  await waitFor(() => {
    expect(backend.startThread).toHaveBeenCalledWith(expect.objectContaining({
      cwd: '/repo/app',
      modelProvider: 'codex',
      model: 'gpt-5.5',
      effort: 'xhigh',
      name: 'AI 设计流程',
      agentKey: 'dag_designer',
      promptKey: 'main/dag_designer_zh',
      deferSpawn: true,
    }));
    const designPayload = backend.startThread.mock.calls.at(-1)[0];
    expect(designPayload.provider).toBe('codex');
    expect(designPayload.config).toEqual(expect.objectContaining({
      codexHome: '/Users/test/.codex-alt',
      codexInstanceKey: 'desktop-main',
      codexModelProvider: 'openrouter',
      providerNativeSkills: false,
    }));
    expect(designPayload.config.enabledTools).toContain('task_start_dag');
    expect(designPayload.config.enabledTools).toContain('task_get_run');
    expect(designPayload.config.enabledTools).toContain('task_list_runs');
    expect(designPayload.config.enabledTools).toContain('task_dispatch_node');
    expect(designPayload.config.enabledTools).toContain('workflow_template_list');
    expect(designPayload.config.enabledTools).toContain('workflow_template_get');
    expect(designPayload.config.enabledTools).toContain('workflow_template_render_dag');
    expect(designPayload.config.enabledTools).not.toContain('task_update_node');
  });
  expect(await screen.findByRole('status')).toHaveTextContent('AI 设计流程已创建');
  fireEvent.click(screen.getByRole('button', { name: '查看设计对话' }));
  expect((await screen.findAllByText('AI 设计流程')).length).toBeGreaterThanOrEqual(1);
  expect(screen.queryByText('unknown')).not.toBeInTheDocument();
}

  it('runs, stops, deletes, schedules, edits and designs DAGs through the old RPC surface', async () => {
    mockWorkflowDagLifecycle();

    await openWorkflowDashboard();
    await runAndStopWorkflowDag();
    await createWorkflowSchedule();

    await editWorkflowStep();
    await deleteWorkflowDag();
    await designWorkflowWithAi();
  });

  it('uses the active-run query, not stale selected run detail, to unlock DAG controls after stop', async () => {
    const dag = {
      dag_key: 'daily-brief',
      title: 'Daily Brief',
      status: 'ready',
      trigger: 'manual',
      version: 7,
    };
    const agentNode = {
      node_key: 'draft',
      title: '起草',
      node_type: 'agent',
      assigned_to: 'agent-a',
      depends_on: [],
      config: {},
    };
    let hasActiveRun = true;
    backend.getDashboardPage.mockImplementation(({ page }) => Promise.resolve(
      page === 'dags' ? { dags: [dag] } : { skills: [] },
    ));
    backend.getDagDetail.mockResolvedValue({ dag, nodes: [agentNode] });
    backend.getDagRuns.mockImplementation(({ status }) => {
      if (status === 'running') {
        return Promise.resolve({ runs: hasActiveRun ? [{ run_key: 'run-live', status: 'running' }] : [] });
      }
      return Promise.resolve({ runs: [{ run_key: 'run-live', status: hasActiveRun ? 'running' : 'cancelled' }] });
    });
    backend.getDagRun.mockResolvedValue({ run: { run_key: 'run-live', status: 'running' }, nodes: [agentNode] });
    backend.terminateDagRun.mockImplementation(() => {
      hasActiveRun = false;
      return Promise.resolve({ ok: true });
    });

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('自动化'));
    expect(await screen.findByRole('button', { name: '停止运行' })).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '停止运行' }));
    await waitFor(() => {
      expect(backend.terminateDagRun).toHaveBeenCalledWith({
        dagKey: 'daily-brief',
        runKey: 'run-live',
        reason: 'user_requested',
      });
    });

    await waitFor(() => {
      expect(screen.queryByRole('button', { name: '停止运行' })).not.toBeInTheDocument();
      expect(screen.getByRole('button', { name: '运行' })).not.toBeDisabled();
    });
  });

  it('blocks scheduling when root DAG steps have no assignee', async () => {
    const dag = {
      dag_key: 'daily-brief',
      title: 'Daily Brief',
      status: 'ready',
      trigger: 'manual',
      version: 7,
    };
    const unassignedRoot = {
      node_key: 'draft',
      title: '起草',
      node_type: 'agent',
      assigned_to: '',
      depends_on: [],
      config: { provider: 'codex', model: 'gpt-5', first_turn: '请起草简报' },
    };
    backend.getDashboardPage.mockImplementation(({ page }) => Promise.resolve(
      page === 'dags' ? { dags: [dag] } : { skills: [] },
    ));
    backend.getDagDetail.mockResolvedValue({ dag, nodes: [unassignedRoot] });
    backend.getDagRuns.mockResolvedValue({ runs: [] });

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('自动化'));
    expect((await screen.findAllByText('Daily Brief')).length).toBeGreaterThanOrEqual(1);

    expect(await screen.findByText('首个步骤「起草」缺少执行者，请先在高级设置中填写执行者。')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '运行' })).toBeDisabled();

    fireEvent.click(screen.getByRole('button', { name: '创建定时任务' }));
    const scheduleDialog = await screen.findByRole('dialog', { name: '创建定时任务' });
    fireEvent.click(within(scheduleDialog).getByRole('button', { name: '创建定时任务' }));

    expect(await screen.findByRole('alert')).toHaveTextContent('保存定时任务失败：首个步骤「起草」缺少执行者');
    expect(backend.applyDagOps).not.toHaveBeenCalled();
  });

  it('keeps workflow action notices scoped to the selected task', async () => {
    const firstDag = {
      dag_key: 'flow-a',
      title: '流程 A',
      status: 'ready',
      trigger: 'manual',
      version: 7,
    };
    const secondDag = {
      dag_key: 'flow-b',
      title: '流程 B',
      status: 'ready',
      trigger: 'manual',
      version: 8,
    };
    backend.getDashboardPage.mockImplementation(({ page }) => Promise.resolve(
      page === 'dags' ? { dags: [firstDag, secondDag] } : { skills: [] },
    ));
    backend.getDagDetail.mockImplementation(({ dagKey }) => Promise.resolve({
      dag: dagKey === 'flow-a' ? firstDag : secondDag,
      nodes: [{
        node_key: 'draft',
        title: dagKey === 'flow-a' ? '步骤 A' : '步骤 B',
        node_type: 'agent',
        status: 'pending',
        depends_on: [],
        config: {},
      }],
    }));
    backend.getDagRuns.mockResolvedValue({ runs: [] });
    backend.applyDagOps.mockResolvedValue({ newVersion: 8 });

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('自动化'));
    expect((await screen.findAllByText('流程 A')).length).toBeGreaterThanOrEqual(2);
    expect((await screen.findAllByText('步骤 A')).length).toBeGreaterThanOrEqual(1);

    fireEvent.click(screen.getByText('高级设置'));
    fireEvent.click(screen.getByRole('button', { name: '保存步骤' }));
    await waitFor(() => {
      expect(screen.getByText('已保存步骤 步骤 A')).toBeInTheDocument();
    });

    fireEvent.click(screen.getByRole('button', { name: /流程 B/ }));
    await waitFor(() => {
      expect(screen.getByRole('heading', { name: '流程 B' })).toBeInTheDocument();
    });
    expect((await screen.findAllByText('步骤 B')).length).toBeGreaterThanOrEqual(1);
    await waitFor(() => {
      expect(screen.queryByText('已保存步骤 步骤 A')).not.toBeInTheDocument();
    });

    fireEvent.click(screen.getByRole('button', { name: /流程 A/ }));
    await waitFor(() => {
      expect(screen.getByRole('heading', { name: '流程 A' })).toBeInTheDocument();
    });
    expect((await screen.findAllByText('步骤 A')).length).toBeGreaterThanOrEqual(1);
    await waitFor(() => {
      expect(screen.queryByText('已保存步骤 步骤 A')).not.toBeInTheDocument();
    });
  });

  it('does not expose database Skill tools from the Skills navigation', async () => {
    backend.listSkillTools.mockResolvedValueOnce({
      tools: [{
        id: 7,
        name: 'Format Go',
        description: 'Run formatter',
        command: 'gofmt',
        args: ['-w', './internal/module/skill'],
        enabled: true,
      }],
    });
    render(<App />);
    await screen.findByLabelText('插件与技能');
    openPluginsAndSkillsPage();

    expect(await screen.findByRole('heading', { name: 'MCP工具' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Skill工具' })).not.toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Skill工具' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '新增工具' })).not.toBeInTheDocument();
    expect(screen.queryByText('Format Go')).not.toBeInTheDocument();
    expect(screen.queryByText('本地技能库')).not.toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: '后端' })).not.toBeInTheDocument();
    expect(backend.listSkillTools).not.toHaveBeenCalled();
    expect(backend.getDashboardPage).not.toHaveBeenCalledWith({ cwd: '/repo/app', page: 'skills' });
    expect(backend.listSkillResolutions).not.toHaveBeenCalled();
  });

  it('keeps composer dock pinned inside the viewport', () => {
    resetClientStoreForTests({
      bootstrapStatus: 'ready',
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': Array.from({ length: 70 }, (_, index) => ({
          id: `m-${index}`,
          role: index % 2 ? 'user' : 'assistant',
          text: `message ${index}`,
          time: '2026-05-30T00:00:00Z',
        })),
      },
    });

    render(<App skipBootstrap />);

    expect(screen.getByTestId('composer-dock')).toHaveClass('composer', 'composer--docked');
    expect(screen.getByTestId('chat-timeline')).toHaveClass('timeline');
  });

  it('connects settings page build info and provider preferences to backend', async () => {
    resetClientStoreForTests({
      bootstrapStatus: 'ready',
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activePage: 'settings',
    });
    const preferenceValues = {
      stallThresholdSec: 60,
      'contextUsageAlerts.thresholds': [65, 80, 95],
      'settings.provider.active': 'codex',
      'settings.provider.codex.codexHome': '/home/test/.codex',
      'settings.provider.codex.codexInstanceKey': 'main',
      'settings.provider.codex.codexModelProvider': 'openai',
      'settings.provider.codex.model': 'gpt-5.5',
      'settings.provider.codex.effort': 'xhigh',
      'settings.provider.codex.sandbox': { type: 'workspaceWrite', writableRoots: ['/repo/app'], networkAccess: false },
    };
    backend.getPreference.mockImplementation(({ key }) => Promise.resolve(preferenceValues[key] ?? null));

    render(<App skipBootstrap />);

    expect(await screen.findByText('Agent Orchestrator v1.2.3')).toBeInTheDocument();
    expect(screen.getByText('linux/amd64')).toBeInTheDocument();
    expect(screen.getByText('2026-05-30T07:00:00Z')).toBeInTheDocument();
    expect(screen.getByText('abc123def456')).toBeInTheDocument();
    expect(backend.getPreference).toHaveBeenCalledWith({ cwd: '/repo/app', key: 'stallThresholdSec' });
    expect(backend.getPreference).toHaveBeenCalledWith({ cwd: '/repo/app', key: 'settings.provider.active' });

    fireEvent.change(screen.getByLabelText('统一超时阈值'), { target: { value: '120' } });
    fireEvent.change(screen.getByLabelText('Warn 阈值'), { target: { value: '70' } });
    fireEvent.change(screen.getByLabelText('Danger 阈值'), { target: { value: '85' } });
    fireEvent.change(screen.getByLabelText('Critical 阈值'), { target: { value: '96' } });
    fireEvent.click(screen.getByRole('button', { name: '保存运行阈值' }));

    await waitFor(() => {
      expect(backend.setPreference).toHaveBeenCalledWith({ cwd: '/repo/app', key: 'stallThresholdSec', value: 120 });
      expect(backend.setPreference).toHaveBeenCalledWith({ cwd: '/repo/app', key: 'contextUsageAlerts.thresholds', value: [70, 85, 96] });
    });

    expect(screen.queryByLabelText('Model Provider')).not.toBeInTheDocument();
    fireEvent.change(screen.getByLabelText('Codex Home'), { target: { value: '/tmp/codex-home' } });
    fireEvent.change(screen.getByLabelText('Instance Key'), { target: { value: 'desktop-main' } });
    fireEvent.change(screen.getByLabelText('Sandbox Policy'), { target: { value: 'readOnly' } });
    fireEvent.click(screen.getByRole('button', { name: '保存 Provider 设置' }));

    await waitFor(() => {
      expect(backend.setPreference).toHaveBeenCalledWith({ cwd: '/repo/app', key: 'settings.provider.codex.codexHome', value: '/tmp/codex-home' });
      expect(backend.setPreference).toHaveBeenCalledWith({ cwd: '/repo/app', key: 'settings.provider.codex.codexInstanceKey', value: 'desktop-main' });
      expect(backend.setPreference).toHaveBeenCalledWith({
        cwd: '/repo/app',
        key: 'settings.provider.codex.sandbox',
        value: { type: 'readOnly' },
      });
    });
    expect(backend.setPreference).not.toHaveBeenCalledWith(expect.objectContaining({
      key: 'settings.provider.codex.codexModelProvider',
    }));

    backend.getBuildInfo.mockResolvedValueOnce({
      version: 'v1.2.4',
      runtime: 'linux/amd64',
      buildTime: '2026-05-30T08:00:00Z',
      commit: 'feedface9876',
    });
    fireEvent.click(screen.getByRole('button', { name: '刷新构建信息' }));
    expect(await screen.findByText('Agent Orchestrator v1.2.4')).toBeInTheDocument();
    expect(screen.getByText('feedface9876')).toBeInTheDocument();
  });
