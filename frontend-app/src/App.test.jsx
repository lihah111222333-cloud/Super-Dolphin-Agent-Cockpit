import React from 'react';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import App from './App.jsx';
import { resetClientStoreForTests, useClientStore } from './entities/client/model/useClientStore.js';
import mermaid from 'mermaid';

let bridgeCallback;

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
    listPromptSections writePromptSection deletePromptSection
    deletePrompt draftPromptIntent commitPromptIntent discardPromptIntent dryRunPromptIntent getMemorySnapshot
    getMemoryEntry upsertMemoryEntry deleteMemoryEntry setMemoryAutoDreamIntent mergeMemoryEntries
    ignoreMemorySimilarity consolidateMemorySimilarities startConsolidateMemorySimilarities getMemoryConsolidationStatus
    listDags getDagDetail getDagRuns getDagRun startDag terminateDagRun deleteDag applyDagOps listWorkflowTemplates getWorkflowTemplate renderWorkflowTemplateDraft deleteSkill
    listCronJobs getCronJob createCronJob updateCronJob deleteCronJob runCronJobOnce setCronJobEnabled listCronJobRuns
    readSkill listSkillFiles createSkill writeSkill importSkillDirectories suggestSkillSummary selectProjectDir selectProjectDirs
    listMCPServers startSQLiteMCPServer stopSQLiteMCPServer startPlaywrightMCPServer stopPlaywrightMCPServer
    listSkillResolutions previewSkillResolution applySkillResolution readSharedFile deleteSharedFile getPreference
    startThread startTurn interruptTurn forceCompleteTurn compactThread recoverThread respondApproval resolveThreadIdentity archiveThread unarchiveThread
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

async function openSkillToolsPage() {
  fireEvent.click(screen.getByLabelText('插件与技能'));
  fireEvent.click(await screen.findByRole('button', { name: 'Skill工具' }));
}

function getBackendThreadText() {
  return screen.getAllByText('后端线程')[0];
}

function queryBackendThreadText() {
  return screen.queryAllByText('后端线程')[0] ?? null;
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
      dir: '/repo/app/.agent/skills/backend',
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

function mockPromptWizardEntryPrompt(overrides = {}) {
  const name = overrides.name || '待确认入口';
  const content = overrides.content || '待确认内容';
  const scope = overrides.scope || 'project';
  backend.listPromptAssets.mockResolvedValue({
    prompts: [{
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
    }],
  });
}

function mockMemoryDefaults() {
  backend.getMemorySnapshot.mockResolvedValue({
    overview: {
      enabled: true,
      autoDreamEnabled: false,
      autoDreamIntent: null,
      projectRoot: '/repo/app',
      health: { preferenceCount: 0, projectCount: 0, maxPerCategory: 15, similarGroups: [] },
    },
    private: { entries: [] },
    team: { entries: [] },
  });
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
      { name: 'SKILL.md', path: '/repo/app/.agent/skills/backend/SKILL.md', is_main: true },
      { name: 'guide.md', path: '/repo/app/.agent/skills/backend/references/guide.md', is_main: false },
    ],
  });
  backend.writeSkill.mockResolvedValue({ path: '/repo/app/.agent/skills/backend/SKILL.md' });
  backend.importSkillDirectories.mockResolvedValue({
    imported: [{ name: 'ImportedSkill', skill_file: '/imports/ImportedSkill/SKILL.md' }],
    failures: [],
  });
  backend.suggestSkillSummary.mockResolvedValue('当你需要部署服务时使用。');
  backend.selectProjectDir.mockResolvedValue('/repo/new');
  backend.selectProjectDirs.mockResolvedValue(['/imports/ImportedSkill']);
  backend.listSkillResolutions.mockResolvedValue({ items: [] });
  backend.previewSkillResolution.mockResolvedValue({
    items: [{
      provider: 'codex',
      preview_id: 'preview-1',
      preview_hash: 'hash-1',
      source_path: '/repo/app/.agent/skills/backend/SKILL.md',
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
afterEach(() => {
  vi.useRealTimers();
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
  expect(inlineTrace).toHaveTextContent('source=mixed');
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
    expect(shell).toHaveAttribute('data-theme', 'light');
    expect(document.querySelector('.traffic-lights')).not.toBeInTheDocument();
    expect(document.querySelector('.titlebar')).not.toBeInTheDocument();
    expect(within(sidebar).getByText('燧元')).toBeInTheDocument();
    expect(within(sidebar).getByRole('button', { name: '新对话' })).toBeInTheDocument();
    expect(within(sidebar).getByRole('button', { name: '设置' })).toBeInTheDocument();
    fireEvent.click(within(sidebar).getByRole('button', { name: '切换到 English' }));
    expect(within(sidebar).getByRole('button', { name: 'New chat' })).toBeInTheDocument();
    expect(screen.getByText('Current page: Chat')).toBeInTheDocument();
    fireEvent.click(within(sidebar).getByRole('button', { name: 'Switch to 中文' }));
    expect(within(sidebar).getByRole('button', { name: '新对话' })).toBeInTheDocument();
    expect(screen.getByText('当前页面: 聊天页面')).toBeInTheDocument();
    const sidebarResizer = within(sidebar).getByRole('separator', { name: '调整工作台侧栏宽度' });
    expect(sidebarResizer).toHaveAttribute('aria-valuenow', '340');

    fireEvent.keyDown(sidebarResizer, { key: 'ArrowLeft' });

    expect(sidebarResizer).toHaveAttribute('aria-valuenow', '324');
    expect(sidebar.parentElement).toHaveStyle({ '--workbench-sidebar-width': '324px' });

    dispatchPointer(sidebarResizer, 'pointerdown', 324);
    dispatchPointer(window, 'pointermove', 374);
    dispatchPointer(window, 'pointerup', 374);

    expect(sidebarResizer).toHaveAttribute('aria-valuenow', '374');
    expect(sidebar.parentElement).toHaveStyle({ '--workbench-sidebar-width': '374px' });
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
    expect(screen.getByTestId('app-sidebar')).toHaveStyle({ marginLeft: '0px' });

    fireEvent.click(screen.getByRole('button', { name: '设置' }));
    await screen.findByTestId('settings-page');
    expect(shell).not.toHaveClass('sidebar-open');
  });

  it('uses the custom brand icon only in the sidebar brand area', async () => {
    render(<App />);

    const sidebar = await screen.findByTestId('app-sidebar');
    expect(sidebar.querySelector('.sidebar-brand img')?.getAttribute('src')).toContain('suiyuan-brand-icon.png');
    expect(sidebar.querySelector('.sidebar-tree-folder img')).toBeNull();
    expect(sidebar.querySelector('.sidebar-tree-folder svg')).toBeInTheDocument();
  });

  it('wires the sidebar project directory to project and thread actions', async () => {
    backend.getSidebarState.mockImplementation(({ cwd }) => Promise.resolve(cwd === '/repo/other' ? {
      activeThreadId: 'thread-other',
      threads: [{ id: 'thread-other', name: 'Other project chat', provider: 'codex', status: 'idle', projectPath: '/repo/other' }],
    } : {
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: '后端线程', provider: 'codex', status: '工作中', cwd: '/repo/app' }],
    }));
    backend.getThreadState.mockImplementation(({ threadId }) => Promise.resolve({
      activeThreadId: threadId,
      threads: [
        { id: 'thread-1', name: '后端线程', provider: 'codex', status: '工作中', cwd: '/repo/app' },
        { id: 'thread-other', name: 'Other project chat', provider: 'codex', status: 'idle', projectPath: '/repo/other' },
      ],
      timelinesByThread: {
        [threadId]: [{ id: `message-${threadId}`, kind: 'assistant', text: `${threadId} message`, ts: '2026-05-30T00:00:00Z' }],
      },
    }));
    backend.getProjects.mockResolvedValue({ projects: ['/repo/app', '/repo/other'], active: '/repo/app' });
    backend.setActiveProject.mockImplementation(({ path }) => Promise.resolve({
      projects: path === '/repo/new' ? ['/repo/app', '/repo/other', '/repo/new'] : ['/repo/app', '/repo/other'],
      active: path,
    }));
    backend.addProject.mockResolvedValue({ projects: ['/repo/app', '/repo/other', '/repo/new'], active: '/repo/other' });
    backend.selectProjectDir.mockResolvedValue('/repo/new');

    render(<App />);

    const sidebar = await screen.findByTestId('app-sidebar');
    const appChats = await within(sidebar).findByRole('list', { name: 'app 聊天记录' });
    const otherChats = await within(sidebar).findByRole('list', { name: 'other 聊天记录' });
    expect(within(appChats).getByTitle('后端线程')).toBeInTheDocument();
    expect(within(appChats).queryByText('指定文件')).not.toBeInTheDocument();
    expect(within(appChats).queryByText('共享文件')).not.toBeInTheDocument();
    expect(within(otherChats).queryByTitle('Other project chat')).not.toBeInTheDocument();

    fireEvent.click(await within(sidebar).findByRole('button', { name: '选择项目 other' }));
    await waitFor(() => expect(within(otherChats).getByTitle('Other project chat')).toBeInTheDocument());
    expect(backend.setActiveProject).not.toHaveBeenCalledWith({ cwd: '/repo/app', path: '/repo/other' });

    fireEvent.doubleClick(within(otherChats).getByTitle('Other project chat'));
    fireEvent.change(within(otherChats).getByLabelText('会话名称'), { target: { value: 'Renamed sidebar chat' } });
    fireEvent.click(within(otherChats).getByLabelText('保存会话名称'));
    await waitFor(() => expect(backend.renameThread).toHaveBeenCalledWith({ threadId: 'thread-other', name: 'Renamed sidebar chat' }));
    await waitFor(() => expect(within(otherChats).getByTitle('Renamed sidebar chat')).toBeInTheDocument());

    fireEvent.click(within(otherChats).getByTitle('Renamed sidebar chat'));
    await waitFor(() => expect(useClientStore.getState().activeThreadId).toBe('thread-other'));

    fireEvent.click(within(otherChats).getByTitle('删除'));
    fireEvent.click(within(otherChats).getByRole('button', { name: '删除' }));
    await waitFor(() => expect(backend.deleteThread).toHaveBeenCalledWith({ threadId: 'thread-other' }));

    await waitFor(() => expect(useClientStore.getState().activeThreadId).toBe(''));

    fireEvent.click(within(sidebar).getByRole('button', { name: '添加项目目录' }));
    await waitFor(() => expect(backend.selectProjectDir).toHaveBeenCalledWith('/repo/other'));
    expect(backend.addProject).toHaveBeenCalledWith({ cwd: '/repo/app', path: '/repo/new' });
    expect(backend.setActiveProject).toHaveBeenCalledWith({ cwd: '/repo/app', path: '/repo/new' });
    await waitFor(() => expect(useClientStore.getState().activePage).toBe('chat'));
  });

  it('keeps sidebar project order stable and toggles project chats from folder clicks without switching projects', async () => {
    backend.getProjects.mockResolvedValue({ projects: ['/repo/app', '/repo/other'], active: '/repo/app' });
    backend.setActiveProject.mockResolvedValue({ projects: ['/repo/app', '/repo/other'], active: '/repo/other' });
    backend.getSidebarState.mockImplementation(({ cwd }) => Promise.resolve(cwd === '/repo/other' ? {
      activeThreadId: '',
      threads: [{ id: 'thread-other', name: 'Other project chat', provider: 'codex', status: 'idle', cwd: '/repo/other' }],
    } : {
      activeThreadId: '',
      threads: [{ id: 'thread-app', name: 'App project chat', provider: 'codex', status: 'idle', cwd: '/repo/app' }],
    }));

    render(<App />);

    const sidebar = await screen.findByTestId('app-sidebar');
    const projectNames = () => Array.from(sidebar.querySelectorAll('.sidebar-tree-folder span'))
      .map((node) => node.textContent);
    const projectLists = () => Array.from(sidebar.querySelectorAll('.sidebar-project-thread-list'));
    const projectButton = (name) => Array.from(sidebar.querySelectorAll('.sidebar-tree-folder'))
      .find((button) => button.textContent === name);

    await waitFor(() => expect(projectNames()).toEqual(['app', 'other']));
    expect(within(projectLists()[0]).getByTitle('App project chat')).toBeInTheDocument();
    expect(within(projectLists()[1]).queryByTitle('Other project chat')).not.toBeInTheDocument();

    fireEvent.click(projectButton('other'));

    await waitFor(() => {
      expect(backend.getSidebarState).toHaveBeenCalledWith({ cwd: '/repo/other' });
      expect(within(projectLists()[1]).getByTitle('Other project chat')).toBeInTheDocument();
    });
    expect(projectNames()).toEqual(['app', 'other']);
    expect(backend.setActiveProject).not.toHaveBeenCalled();

    fireEvent.click(projectButton('other'));

    await waitFor(() => expect(within(projectLists()[1]).queryByTitle('Other project chat')).not.toBeInTheDocument());
    expect(projectNames()).toEqual(['app', 'other']);
  });

  it('keeps a sidebar project chat selected when the project switch sidebar refresh returns late', async () => {
    const expandOtherSidebar = deferred();
    const lateSwitchSidebar = deferred();
    let otherSidebarCalls = 0;
    backend.getProjects.mockResolvedValue({ projects: ['/repo/app', '/repo/other'], active: '/repo/app' });
    backend.setActiveProject.mockResolvedValue({ projects: ['/repo/app', '/repo/other'], active: '/repo/other' });
    backend.getSidebarState.mockImplementation(({ cwd }) => {
      if (cwd === '/repo/other') {
        otherSidebarCalls += 1;
        return otherSidebarCalls === 1 ? expandOtherSidebar.promise : lateSwitchSidebar.promise;
      }
      return Promise.resolve({
        activeThreadId: 'thread-app',
        threads: [{ id: 'thread-app', name: 'App project chat', provider: 'codex', status: 'idle', cwd: '/repo/app' }],
      });
    });
    backend.getThreadState.mockImplementation(({ threadId }) => Promise.resolve({
      activeThreadId: threadId,
      threads: [
        { id: 'thread-app', name: 'App project chat', provider: 'codex', status: 'idle', cwd: '/repo/app' },
        { id: 'thread-other', name: 'Other project chat', provider: 'codex', status: 'idle', cwd: '/repo/other' },
      ],
      timelinesByThread: {
        [threadId]: [{ id: `message-${threadId}`, kind: 'assistant', text: `${threadId} message`, ts: '2026-05-30T00:00:00Z' }],
      },
    }));
    backend.getThreadMessages.mockResolvedValue({ messages: [] });

    render(<App />);

    const sidebar = await screen.findByTestId('app-sidebar');
    const projectLists = () => Array.from(sidebar.querySelectorAll('.sidebar-project-thread-list'));
    const projectButton = (name) => Array.from(sidebar.querySelectorAll('.sidebar-tree-folder'))
      .find((button) => button.textContent === name);

    fireEvent.click(projectButton('other'));
    expandOtherSidebar.resolve({
      activeThreadId: '',
      threads: [{ id: 'thread-other', name: 'Other project chat', provider: 'codex', status: 'idle', cwd: '/repo/other' }],
    });
    await waitFor(() => expect(within(projectLists()[1]).getByTitle('Other project chat')).toBeInTheDocument());

    fireEvent.click(within(projectLists()[1]).getByTitle('Other project chat'));
    await waitFor(() => expect(otherSidebarCalls).toBe(2));
    await waitFor(() => expect(useClientStore.getState().activeThreadId).toBe('thread-other'));
    expect(useClientStore.getState().chatSurfaceLoadingCwd).toBe('/repo/other');

    lateSwitchSidebar.resolve({
      activeThreadId: '',
      threads: [{ id: 'thread-other', name: 'Other project chat', provider: 'codex', status: 'idle', cwd: '/repo/other' }],
    });
    await waitFor(() => expect(useClientStore.getState().chatSurfaceLoadingCwd).toBe(''));

    expect(useClientStore.getState().activeThreadId).toBe('thread-other');
  });

  it('does not show the new-chat intro while opening a project tree chat across projects', async () => {
    const expandOtherSidebar = deferred();
    const projectChange = deferred();
    const lateSwitchSidebar = deferred();
    let otherSidebarCalls = 0;
    backend.getProjects.mockResolvedValue({ projects: ['/repo/app', '/repo/other'], active: '/repo/app' });
    backend.setActiveProject.mockReturnValue(projectChange.promise);
    backend.getSidebarState.mockImplementation(({ cwd }) => {
      if (cwd === '/repo/other') {
        otherSidebarCalls += 1;
        return otherSidebarCalls === 1 ? expandOtherSidebar.promise : lateSwitchSidebar.promise;
      }
      return Promise.resolve({
        activeThreadId: 'thread-app',
        threads: [{ id: 'thread-app', name: 'App project chat', provider: 'codex', status: 'idle', cwd: '/repo/app' }],
      });
    });
    backend.getThreadState.mockImplementation(({ threadId }) => Promise.resolve({
      activeThreadId: threadId,
      threads: [
        { id: 'thread-app', name: 'App project chat', provider: 'codex', status: 'idle', cwd: '/repo/app' },
        { id: 'thread-other', name: 'Other project chat', provider: 'codex', status: 'idle', cwd: '/repo/other' },
      ],
      timelinesByThread: {
        [threadId]: [{ id: `message-${threadId}`, kind: 'assistant', text: `${threadId} message`, ts: '2026-05-30T00:00:00Z' }],
      },
    }));
    backend.getThreadMessages.mockResolvedValue({ messages: [] });

    render(<App />);

    const sidebar = await screen.findByTestId('app-sidebar');
    const projectLists = () => Array.from(sidebar.querySelectorAll('.sidebar-project-thread-list'));
    const projectButton = (name) => Array.from(sidebar.querySelectorAll('.sidebar-tree-folder'))
      .find((button) => button.textContent === name);

    fireEvent.click(projectButton('other'));
    expandOtherSidebar.resolve({
      activeThreadId: '',
      threads: [{ id: 'thread-other', name: 'Other project chat', provider: 'codex', status: 'idle', cwd: '/repo/other' }],
    });
    await waitFor(() => expect(within(projectLists()[1]).getByTitle('Other project chat')).toBeInTheDocument());

    fireEvent.click(within(projectLists()[1]).getByTitle('Other project chat'));

    await waitFor(() => expect(useClientStore.getState().chatSurfaceLoadingCwd).toBe('/repo/other'));
    expect(useClientStore.getState().activeThreadId).toBe('thread-other');
    expect(useClientStore.getState().threadStateLoadingByThread['thread-other']).toBe(true);

    projectChange.resolve({ projects: ['/repo/app', '/repo/other'], active: '/repo/other' });
    lateSwitchSidebar.resolve({
      activeThreadId: '',
      threads: [{ id: 'thread-other', name: 'Other project chat', provider: 'codex', status: 'idle', cwd: '/repo/other' }],
    });
    await waitFor(() => expect(useClientStore.getState().activeThreadId).toBe('thread-other'));
  });

  it('starts a new chat for a sidebar project only from the project action button', async () => {
    backend.getProjects.mockResolvedValue({ projects: ['/repo/app', '/repo/empty'], active: '/repo/app' });
    backend.getSidebarState.mockImplementation(({ cwd }) => Promise.resolve(cwd === '/repo/empty' ? {
      activeThreadId: '',
      threads: [],
    } : {
      activeThreadId: 'thread-app',
      threads: [{ id: 'thread-app', name: 'App project chat', provider: 'codex', status: 'idle', cwd: '/repo/app' }],
    }));
    backend.setActiveProject.mockImplementation(({ path }) => Promise.resolve({
      projects: ['/repo/app', '/repo/empty'],
      active: path,
    }));

    render(<App />);

    const sidebar = await screen.findByTestId('app-sidebar');
    const emptyChats = await within(sidebar).findByRole('list', { name: /empty/ });
    const emptyProjectButton = within(sidebar).getByRole('button', { name: '选择项目 empty' });

    fireEvent.click(emptyProjectButton);

    await waitFor(() => expect(within(emptyChats).getByText('暂无聊天记录')).toBeInTheDocument());
    expect(backend.setActiveProject).not.toHaveBeenCalled();
    expect(useClientStore.getState()).toEqual(expect.objectContaining({
      activeProject: '/repo/app',
      activeThreadId: 'thread-app',
    }));

    fireEvent.click(within(sidebar).getByTitle(/empty/));

    await waitFor(() => expect(backend.setActiveProject).toHaveBeenCalledWith({ cwd: '/repo/app', path: '/repo/empty' }));
    await waitFor(() => expect(useClientStore.getState()).toEqual(expect.objectContaining({
      activeProject: '/repo/empty',
      activeThreadId: '',
    })));
  });

  it('keeps cached chats visible when multiple sidebar projects are expanded', async () => {
    backend.getProjects.mockResolvedValue({ projects: ['/repo/app', '/repo/other'], active: '/repo/app' });
    backend.setActiveProject.mockImplementation(({ path }) => Promise.resolve({ projects: ['/repo/app', '/repo/other'], active: path }));
    backend.getSidebarState.mockImplementation(({ cwd }) => Promise.resolve(cwd === '/repo/other' ? {
      activeThreadId: '',
      threads: [{ id: 'thread-other', name: 'Other project chat', provider: 'codex', status: 'idle' }],
      agentRuntimeById: { 'thread-other': { cwd: '/repo/other' } },
    } : {
      activeThreadId: '',
      threads: [{ id: 'thread-app', name: 'App project chat', provider: 'codex', status: 'idle' }],
      agentRuntimeById: { 'thread-app': { cwd: '/repo/app' } },
    }));

    render(<App />);

    const sidebar = await screen.findByTestId('app-sidebar');
    const projectLists = () => Array.from(sidebar.querySelectorAll('.sidebar-project-thread-list'));
    const projectButton = (name) => Array.from(sidebar.querySelectorAll('.sidebar-tree-folder'))
      .find((button) => button.textContent === name);

    fireEvent.click(projectButton('other'));
    await waitFor(() => expect(within(projectLists()[1]).getByTitle('Other project chat')).toBeInTheDocument());

    await waitFor(() => {
      expect(within(projectLists()[0]).getByTitle('App project chat')).toBeInTheDocument();
      expect(within(projectLists()[1]).getByTitle('Other project chat')).toBeInTheDocument();
    });
    expect(within(projectLists()[1]).queryByText('暂无聊天记录')).not.toBeInTheDocument();
  });

  it('keeps cached project chats available after expanding an empty project', async () => {
    backend.getProjects.mockResolvedValue({ projects: ['/repo/app', '/repo/empty'], active: '/repo/app' });
    backend.getSidebarState.mockImplementation(({ cwd }) => Promise.resolve(cwd === '/repo/empty' ? {
      activeThreadId: '',
      threads: [],
    } : {
      activeThreadId: 'thread-app',
      threads: [{ id: 'thread-app', name: 'App project chat', provider: 'codex', status: 'idle', cwd: '/repo/app' }],
    }));
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-app',
      timelinesByThread: {
        'thread-app': [{ id: 'message-thread-app', kind: 'assistant', text: 'app message', ts: '2026-05-30T00:00:00Z' }],
      },
    });
    backend.setActiveProject.mockImplementation(({ path }) => Promise.resolve({
      projects: ['/repo/app', '/repo/empty'],
      active: path,
    }));

    render(<App />);

    const sidebar = await screen.findByTestId('app-sidebar');
    const appChats = await within(sidebar).findByRole('list', { name: /app/ });
    const emptyChats = await within(sidebar).findByRole('list', { name: /empty/ });
    expect(within(appChats).getByTitle('App project chat')).toBeInTheDocument();

    fireEvent.click(within(sidebar).getByRole('button', { name: '选择项目 empty' }));

    await waitFor(() => expect(within(emptyChats).getByText('暂无聊天记录')).toBeInTheDocument());
    expect(backend.setActiveProject).not.toHaveBeenCalled();
    expect(within(appChats).getByTitle('App project chat')).toBeInTheDocument();
  });

  it('refreshes a project instead of reusing the empty cache written during bootstrap', async () => {
    const projects = deferred();
    let superSidebarCalls = 0;
    backend.readConfig.mockResolvedValue({ cwd: '/repo/super' });
    backend.getProjects.mockReturnValue(projects.promise);
    backend.getSidebarState.mockImplementation(({ cwd }) => {
      if (cwd === '/repo/super') {
        superSidebarCalls += 1;
        return Promise.resolve(superSidebarCalls === 1 ? {
          activeThreadId: 'thread-ai',
          threads: [{ id: 'thread-ai', name: 'AI Chat', provider: 'codex', status: 'idle', cwd: '/repo/ai' }],
        } : {
          activeThreadId: '',
          threads: [{ id: 'thread-super', name: 'Super Chat', provider: 'codex', status: 'idle', cwd: '/repo/super' }],
        });
      }
      return Promise.resolve({ activeThreadId: '', threads: [] });
    });

    render(<App />);

    const sidebar = await screen.findByTestId('app-sidebar');
    const projectLists = () => Array.from(sidebar.querySelectorAll('.sidebar-project-thread-list'));
    const projectButton = (name) => Array.from(sidebar.querySelectorAll('.sidebar-tree-folder'))
      .find((button) => button.textContent === name);

    await waitFor(() => expect(projectButton('super')).toBeTruthy());
    await act(async () => {
      await Promise.resolve();
    });

    projects.resolve({ projects: ['/repo/super', '/repo/ai'], active: '/repo/ai' });

    await waitFor(() => expect(useClientStore.getState().bootstrapStatus).toBe('ready'));
    expect(superSidebarCalls).toBe(1);

    fireEvent.click(projectButton('super'));

    await waitFor(() => expect(superSidebarCalls).toBe(2));
    await waitFor(() => expect(within(projectLists()[0]).getByTitle('Super Chat')).toBeInTheDocument());
  });

  it('keeps the active project chat list when opening a thread returns a thread-scoped snapshot', async () => {
    const threads = [
      { id: 'thread-a', name: 'Thread A', provider: 'codex', status: 'idle', cwd: '/repo/app' },
      { id: 'thread-b', name: 'Thread B', provider: 'codex', status: 'idle', cwd: '/repo/app' },
    ];
    backend.getProjects.mockResolvedValue({ projects: ['/repo/app'], active: '/repo/app' });
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: 'thread-a',
      threads,
    });
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-b',
      threads: [threads[1]],
      timelinesByThread: {
        'thread-b': [{ id: 'message-thread-b', kind: 'assistant', text: 'thread b message', ts: '2026-05-30T00:00:00Z' }],
      },
    });
    backend.getThreadMessages.mockResolvedValue({ messages: [] });

    render(<App />);

    const sidebar = await screen.findByTestId('app-sidebar');
    const appChats = await within(sidebar).findByRole('list', { name: /app/ });
    expect(within(appChats).getByTitle('Thread A')).toBeInTheDocument();
    expect(within(appChats).getByTitle('Thread B')).toBeInTheDocument();

    fireEvent.click(within(appChats).getByTitle('Thread B'));

    await waitFor(() => expect(backend.getThreadState).toHaveBeenCalledWith({
      cwd: '/repo/app',
      threadId: 'thread-b',
      includeDiff: false,
    }));
    expect(within(appChats).getByTitle('Thread A')).toBeInTheDocument();
    expect(within(appChats).getByTitle('Thread B')).toBeInTheDocument();
  });

  it('hides raw archived threads when expanding a cached sidebar project', async () => {
    let otherSidebarCalls = 0;
    backend.getProjects.mockResolvedValue({ projects: ['/repo/app', '/repo/other'], active: '/repo/app' });
    backend.setActiveProject.mockImplementation(() => new Promise(() => {}));
    backend.getSidebarState.mockImplementation(({ cwd }) => {
      if (cwd === '/repo/other') {
        otherSidebarCalls += 1;
        if (otherSidebarCalls > 1) return new Promise(() => {});
        return Promise.resolve({
          activeThreadId: '',
          threads: [
            { id: 'thread-other-live', name: 'Other live chat', provider: 'codex', status: 'idle' },
            { id: 'thread-other-archived', name: 'Other archived chat', provider: 'codex', status: 'archived' },
          ],
          agentRuntimeById: {
            'thread-other-live': { cwd: '/repo/other' },
            'thread-other-archived': { cwd: '/repo/other' },
          },
        });
      }
      return Promise.resolve({
        activeThreadId: '',
        threads: [{ id: 'thread-app', name: 'App project chat', provider: 'codex', status: 'idle' }],
      });
    });

    render(<App />);

    const sidebar = await screen.findByTestId('app-sidebar');
    const projectLists = () => Array.from(sidebar.querySelectorAll('.sidebar-project-thread-list'));
    const projectButton = (name) => Array.from(sidebar.querySelectorAll('.sidebar-tree-folder'))
      .find((button) => button.textContent === name);

    fireEvent.click(projectButton('other'));

    await waitFor(() => expect(within(projectLists()[1]).getByTitle('Other live chat')).toBeInTheDocument());
    expect(within(projectLists()[1]).queryByTitle('Other archived chat')).not.toBeInTheDocument();
  });

  it('uses runtime cwd for cached sidebar project threads before showing them', async () => {
    let otherSidebarCalls = 0;
    backend.getProjects.mockResolvedValue({ projects: ['/repo/app', '/repo/other'], active: '/repo/app' });
    backend.setActiveProject.mockImplementation(() => new Promise(() => {}));
    backend.getSidebarState.mockImplementation(({ cwd }) => {
      if (cwd === '/repo/other') {
        otherSidebarCalls += 1;
        if (otherSidebarCalls > 1) return new Promise(() => {});
        return Promise.resolve({
          activeThreadId: '',
          threads: [
            { id: 'thread-other-runtime', name: 'Runtime other chat', provider: 'codex', status: 'idle' },
            { id: 'thread-app-runtime', name: 'Runtime app chat', provider: 'codex', status: 'idle' },
          ],
          agentRuntimeById: {
            'thread-other-runtime': { cwd: '/repo/other' },
            'thread-app-runtime': { cwd: '/repo/app' },
          },
        });
      }
      return Promise.resolve({
        activeThreadId: '',
        threads: [{ id: 'thread-app', name: 'App project chat', provider: 'codex', status: 'idle' }],
      });
    });

    render(<App />);

    const sidebar = await screen.findByTestId('app-sidebar');
    const projectLists = () => Array.from(sidebar.querySelectorAll('.sidebar-project-thread-list'));
    const projectButton = (name) => Array.from(sidebar.querySelectorAll('.sidebar-tree-folder'))
      .find((button) => button.textContent === name);

    fireEvent.click(projectButton('other'));

    await waitFor(() => expect(within(projectLists()[1]).getByTitle('Runtime other chat')).toBeInTheDocument());
    expect(within(projectLists()[1]).queryByTitle('Runtime app chat')).not.toBeInTheDocument();
  });

  it('keeps cached sidebar project threads that only have a recoverable session uuid', async () => {
    let otherSidebarCalls = 0;
    backend.getProjects.mockResolvedValue({ projects: ['/repo/app', '/repo/other'], active: '/repo/app' });
    backend.setActiveProject.mockImplementation(() => new Promise(() => {}));
    backend.getSidebarState.mockImplementation(({ cwd }) => {
      if (cwd === '/repo/other') {
        otherSidebarCalls += 1;
        if (otherSidebarCalls > 1) return new Promise(() => {});
        return Promise.resolve({
          activeThreadId: '',
          threads: [
            {
              id: 'agent-half-bound',
              name: 'Recoverable half-bound chat',
              provider: 'codex',
              status: 'idle',
              provider_thread_id: '',
              rollout_path: '',
              session_uuid: 'session-half-bound',
            },
          ],
          agentRuntimeById: {
            'session-half-bound': { cwd: '/repo/other' },
          },
        });
      }
      return Promise.resolve({
        activeThreadId: '',
        threads: [{ id: 'thread-app', name: 'App project chat', provider: 'codex', status: 'idle' }],
      });
    });

    render(<App />);

    const sidebar = await screen.findByTestId('app-sidebar');
    const projectLists = () => Array.from(sidebar.querySelectorAll('.sidebar-project-thread-list'));
    const projectButton = (name) => Array.from(sidebar.querySelectorAll('.sidebar-tree-folder'))
      .find((button) => button.textContent === name);

    fireEvent.click(projectButton('other'));

    await waitFor(() => expect(within(projectLists()[1]).getByTitle('Recoverable half-bound chat')).toBeInTheDocument());
  });

  it('does not show cached sidebar project threads when cwd is unknown', async () => {
    let otherSidebarCalls = 0;
    backend.getProjects.mockResolvedValue({ projects: ['/repo/app', '/repo/other'], active: '/repo/app' });
    backend.setActiveProject.mockImplementation(() => new Promise(() => {}));
    backend.getSidebarState.mockImplementation(({ cwd }) => {
      if (cwd === '/repo/other') {
        otherSidebarCalls += 1;
        if (otherSidebarCalls > 1) return new Promise(() => {});
        return Promise.resolve({
          activeThreadId: '',
          threads: [{ id: 'thread-unknown-cwd', name: 'Unknown cwd chat', provider: 'codex', status: 'idle' }],
        });
      }
      return Promise.resolve({
        activeThreadId: '',
        threads: [{ id: 'thread-app', name: 'App project chat', provider: 'codex', status: 'idle' }],
      });
    });

    render(<App />);

    const sidebar = await screen.findByTestId('app-sidebar');
    const projectLists = () => Array.from(sidebar.querySelectorAll('.sidebar-project-thread-list'));
    const projectButton = (name) => Array.from(sidebar.querySelectorAll('.sidebar-tree-folder'))
      .find((button) => button.textContent === name);

    fireEvent.click(projectButton('other'));

    await waitFor(() => expect(within(projectLists()[1]).getByText('暂无聊天记录')).toBeInTheDocument());
    expect(within(projectLists()[1]).queryByTitle('Unknown cwd chat')).not.toBeInTheDocument();
  });

  it('moves automation threads from project chats into the sidebar task list', async () => {
    const threads = [
      { id: 'thread-project', name: '项目普通对话', provider: 'codex', status: 'idle', cwd: '/repo/app' },
      { id: 'thread-design', name: '[AI 流程设计师] AI 设计流程', provider: 'codex', status: 'created', cwd: '/repo/app', agentKey: 'dag_designer' },
      { id: 'thread-legacy-design', name: 'AI 设计流程', provider: 'codex', status: 'idle', cwd: '/repo/app' },
    ];
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: 'thread-project',
      threads,
    });
    backend.getThreadState.mockImplementation(({ threadId }) => Promise.resolve({
      activeThreadId: threadId,
      threads,
      timelinesByThread: {
        [threadId]: [{ id: `message-${threadId}`, kind: 'assistant', text: `${threadId} message`, ts: '2026-05-30T00:00:00Z' }],
      },
    }));

    render(<App />);

    const sidebar = await screen.findByTestId('app-sidebar');
    const appChats = await within(sidebar).findByRole('list', { name: 'app 聊天记录' });
    expect(within(appChats).getByTitle('项目普通对话')).toBeInTheDocument();
    expect(within(appChats).getByRole('button', { name: '打开项目聊天：项目普通对话' })).toBeInTheDocument();
    expect(within(appChats).queryByTitle('[AI 流程设计师] AI 设计流程')).not.toBeInTheDocument();
    expect(within(appChats).queryByTitle('AI 设计流程')).not.toBeInTheDocument();

    const tasks = within(sidebar).getByRole('list', { name: '任务对话' });
    const taskThread = within(tasks).getByTitle('[AI 流程设计师] AI 设计流程');
    expect(taskThread).toBeInTheDocument();
    expect(within(tasks).getByRole('button', { name: '打开任务对话：[AI 流程设计师] AI 设计流程' })).toBeInTheDocument();
    expect(within(tasks).getByTitle('AI 设计流程')).toBeInTheDocument();

    fireEvent.click(taskThread);
    await waitFor(() => expect(useClientStore.getState().activeThreadId).toBe('thread-design'));
  });

  it('starts a new empty draft from the screenshot sidebar new chat button', async () => {
    render(<App />);

    await waitForBackendThreadHeading();
    expect(screen.queryByText('我们应该在 燧元 中构建什么？')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '新对话' }));

    await screen.findByText('我们应该在 燧元 中构建什么？');
    expect(within(screen.getByTestId('app-sidebar')).getByRole('button', { name: '添加项目目录' })).toBeVisible();
    expect(screen.getByTestId('composer-input')).toHaveValue('');
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

  it('toggles the local color theme without calling backend preferences', async () => {
    render(<App />);

    const shell = await screen.findByTestId('frontend-app');
    const preferenceCallsBeforeToggle = backend.setPreference.mock.calls.length;

    fireEvent.click(screen.getByRole('button', { name: '切换到黑夜模式' }));
    expect(shell).toHaveAttribute('data-theme', 'dark');
    expect(window.localStorage.getItem('super-dolphin-theme')).toBe('dark');
    expect(screen.getByRole('button', { name: '切换到白天模式' })).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '切换到白天模式' }));
    expect(shell).toHaveAttribute('data-theme', 'light');
    expect(window.localStorage.getItem('super-dolphin-theme')).toBe('light');
    expect(screen.getByRole('button', { name: '切换到黑夜模式' })).toBeInTheDocument();
    expect(backend.setPreference.mock.calls.length).toBe(preferenceCallsBeforeToggle);
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
  expect(inlineTrace).toHaveTextContent('Trace 结果');
  expect(inlineTrace).toHaveTextContent('source=mixed');
  expect(inlineTrace).toHaveTextContent('thread/start');
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
    const projectSelector = screen.getByRole('button', { name: '选择项目' });
    expect(projectSelector).toHaveTextContent(/^app$/);
    expect(projectSelector).toHaveAttribute('title', '/repo/app');
    expect(container.querySelector('.work-status')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));
    expect(within(screen.getByTestId('runtime-panel')).getByRole('button', { name: '折叠 file' })).toBeInTheDocument();
    expect(screen.queryByText(/diff --git a\/file b\/file/)).not.toBeInTheDocument();
    expect(backend.getProjects).toHaveBeenCalledWith({ cwd: '/repo/app' });
    expect(backend.getThreadState).toHaveBeenCalledWith({
      cwd: '/repo/app',
      threadId: 'thread-1',
      includeDiff: true,
    });
  });

  it('shows the project selector only once in the shell toolbar', async () => {
    render(<App />);

    expect(await waitForBackendThreadHeading()).toBeInTheDocument();
    expect(screen.getAllByRole('button', { name: '选择项目' })).toHaveLength(1);
    expect(screen.queryByLabelText('当前工作目录')).not.toBeInTheDocument();
    const sidebarToggle = screen.getByRole('button', { name: '显示侧边栏' });
    expect(sidebarToggle).toHaveAttribute('title', '显示侧边栏');
    expect(sidebarToggle).not.toHaveTextContent('侧边栏');
  });

  it('renders the prototype sidebar primary navigation order', () => {
    render(<App skipBootstrap />);

    const navButtons = within(screen.getByTestId('sidebar-nav')).getAllByRole('button');

    expect(navButtons.map((button) => button.textContent)).toEqual([
      '插件',
      '自动化',
      '定制角色',
      '共享文件',
    ]);
    expect(navButtons.map((button) => button.querySelector('svg')?.classList.value)).toEqual([
      expect.stringContaining('lucide-puzzle'),
      expect.stringContaining('lucide-refresh-cw'),
      expect.stringContaining('lucide-circle-user-round'),
      expect.stringContaining('lucide-folder-open'),
    ]);
    expect(screen.getByRole('button', { name: '新对话' }).querySelector('svg')).toHaveClass('lucide-square-plus');
  });

  it('keeps non-prototype utility navigation outside the primary rail', () => {
    render(<App skipBootstrap />);

    expect(within(screen.getByTestId('sidebar-secondary-nav')).getAllByRole('button').map((button) => button.getAttribute('aria-label'))).toEqual([
      '记忆中心',
      '链路追踪',
    ]);
  });

  it('uses the current URL path as the active page on boot', async () => {
    window.history.pushState({}, '', '/dags');
    backend.getWindowBootstrap.mockResolvedValueOnce({ snapshot: { page: 'chat' } });

    render(<App />);

    const workflowButton = await screen.findByRole('button', { name: '自动化' });
    await waitFor(() => expect(workflowButton).toHaveClass('active'));
    expect(screen.getByText('当前页面: 自动化')).toBeInTheDocument();
    expect(window.location.pathname).toBe('/dags');
  });

  it.each(['/tasks', '/commands'])('falls back to chat for the removed %s route', async (pathname) => {
    window.history.pushState({}, '', pathname);

    render(<App />);

    const chatButton = await screen.findByRole('button', { name: '新对话' });
    await waitFor(() => expect(chatButton).toHaveClass('active'));
    expect(screen.queryByRole('button', { name: '任务' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '命令' })).not.toBeInTheDocument();
    expect(screen.getByText('当前页面: 聊天页面')).toBeInTheDocument();
  });

  it('lets user navigation override the explicit boot URL after initial route sync', async () => {
    window.history.pushState({}, '', '/dags');

    render(<App skipBootstrap />);

    const workflowButton = await screen.findByRole('button', { name: '自动化' });
    await waitFor(() => expect(workflowButton).toHaveClass('active'));

    fireEvent.click(screen.getByRole('button', { name: '插件与技能' }));

    await waitFor(() => expect(screen.getByRole('button', { name: '插件与技能' })).toHaveClass('active'));
    expect(screen.getByText('当前页面: 插件与技能')).toBeInTheDocument();
    expect(window.location.pathname).toBe('/skills');
  });

  it('writes page navigation to browser history and restores it on popstate', async () => {
    render(<App skipBootstrap />);

    fireEvent.click(screen.getByRole('button', { name: '插件与技能' }));
    await waitFor(() => expect(window.location.pathname).toBe('/skills'));

    fireEvent.click(screen.getByRole('button', { name: '设置' }));
    await waitFor(() => expect(window.location.pathname).toBe('/settings'));

    await act(async () => {
      window.history.pushState({ activePage: 'skills' }, '', '/skills');
      window.dispatchEvent(new PopStateEvent('popstate', { state: { activePage: 'skills' } }));
    });

    await waitFor(() => expect(screen.getByRole('button', { name: '插件与技能' })).toHaveClass('active'));
    expect(screen.getByText('当前页面: 插件与技能')).toBeInTheDocument();
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

    expect(await screen.findByText('连接后端失败：runtime shim: failed to connect ws://127.0.0.1:5175/wails/ws')).toBeInTheDocument();
  });

  it.skip('disables provider switching when no project cwd is available', () => {
    resetClientStoreForTests({
      bootstrapStatus: 'ready',
      cwd: '',
      activeProject: '',
      provider: 'codex',
    });

    render(<App skipBootstrap />);

    const providerToggle = screen.getByRole('button', { name: '请先连接后端并选择项目' });
    expect(providerToggle).toBeDisabled();

    fireEvent.click(providerToggle);

    expect(backend.setPreference).not.toHaveBeenCalledWith(expect.objectContaining({
      key: 'settings.provider.active',
    }));
  });

  it('disables composer send by button and Enter when no project cwd is available', () => {
    resetClientStoreForTests({
      bootstrapStatus: 'ready',
      cwd: '',
      activeProject: '',
      activeThreadId: '',
      draft: 'Write something',
      attachments: [],
    });

    render(<App skipBootstrap />);

    const sendButton = screen.getByRole('button', { name: '发送消息' });
    expect(sendButton).toBeDisabled();
    expect(screen.getByRole('button', { name: '添加文件' })).toBeDisabled();
    expect(screen.queryByRole('combobox', { name: '发送权限' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: '选择模型' })).toBeDisabled();

    fireEvent.click(sendButton);
    fireEvent.click(screen.getByRole('button', { name: '添加文件' }));
    fireEvent.keyDown(screen.getByTestId('composer-input'), { key: 'Enter', code: 'Enter', charCode: 13 });

    expect(backend.startThread).not.toHaveBeenCalled();
    expect(backend.startTurn).not.toHaveBeenCalled();
    expect(backend.selectFiles).not.toHaveBeenCalled();
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
    expect(dialog.tagName).toBe('DIALOG');
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
            '# Super Agent v3 Agent Context Policy',
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
    expect(screen.queryByText(/Super Agent v3 Agent Context Policy/)).not.toBeInTheDocument();
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

    expect(await screen.findByText('下面是当前仓库结构：')).toBeInTheDocument();
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

    expect(await screen.findByText('常见代码块：')).toBeInTheDocument();
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

    expect(await screen.findByText('执行结果：')).toBeInTheDocument();
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

    fireEvent.click(await screen.findByRole('button', { name: '放大图片 ig_lightbox.png' }));

    const dialog = screen.getByRole('dialog', { name: '图片预览：ig_lightbox.png' });
    expect(dialog.tagName).toBe('DIALOG');
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

    const image = await screen.findByRole('img', { name: 'ig_missing.png' });
    fireEvent.error(image);

    expect(screen.getByRole('note')).toHaveTextContent('图片无法加载');
    expect(screen.getByRole('note')).toHaveTextContent('ig_missing.png');
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
    expect(within(previewDialog).getByLabelText('文件预览内容')).toHaveValue('chosen file');
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
      previewURL: 'file:///repo/app/assets/logo.png',
      thumbnailURL: 'file:///repo/app/assets/logo-thumb.png',
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
    expect(image).toHaveAttribute('src', 'file:///repo/app/assets/logo-thumb.png');
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

    await screen.findByText('新对话');
    expect(container.querySelector('.work-status')).toBeNull();
    expect(container).not.toHaveTextContent(internalId);
    expect(screen.getAllByText('新对话').length).toBeGreaterThan(0);
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

  it('updates active thinking elapsed time in place every second', async () => {
    vi.useFakeTimers();
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

    const composer = screen.getByRole('textbox', { name: '输入给 Agent 的内容' });
    expect(composer).toHaveAttribute('rows', '3');
    expect(composer).toHaveAttribute('placeholder', '随心输入');
  });

  it('does not render a desktop titlebar inside the workbench shell', async () => {
    const { container } = render(<App />);

    expect(await waitForBackendThreadHeading()).toBeInTheDocument();
    expect(container.querySelector('.traffic-lights')).toBeNull();
    expect(container.querySelectorAll('.titlebar')).toHaveLength(0);
    expect(within(screen.getByTestId('app-sidebar')).getByText('燧元')).toBeInTheDocument();
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
    backend.startThread.mockResolvedValue({ thread: { id: 'thread-fork' } });
    backend.startTurn.mockResolvedValue({ ok: true });

    render(<App />);

    await screen.findByText('阶段结论：先迁移 fork draft 链路');
    fireEvent.click(screen.getByRole('button', { name: '聊天操作' }));
    fireEvent.click(await screen.findByRole('button', { name: '继承当前对话' }));

    const card = await screen.findByTestId('fork-draft-card');
    expect(card).toHaveTextContent('继承自会话：后端线程');
    fireEvent.click(within(card).getByLabelText('选择共享文件 reports/final.md'));
    fireEvent.click(within(card).getByRole('button', { name: '创建继承对话' }));

    await waitFor(() => {
      expect(backend.startThread).toHaveBeenCalledWith(expect.objectContaining({
        name: '继承自会话：后端线程',
        baseInstructions: expect.stringContaining('共享文件：reports/final.md'),
      }));
    });
    expect(backend.startTurn).toHaveBeenCalledWith({
      cwd: '/repo/app',
      threadId: 'thread-fork',
      input: [{ type: 'text', text: '请基于上文摘要，简要总结上次进展并提出下一步建议。' }],
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
    expect(layout).toHaveStyle({ gridTemplateColumns: 'minmax(0, 1fr) 6px 189px' });
  });

  it('supports keyboard resizing for chat and activity resizer controls', async () => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1400 });
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: 640 });

    render(<App />);

    await waitForBackendThreadHeading();
    const layout = screen.getByTestId('chat-layout');
    const leftResizer = screen.getByRole('separator', { name: '调整会话栏宽度' });
    expect(leftResizer.tagName).toBe('BUTTON');

    expect(leftResizer).toHaveAttribute('aria-valuenow', '264');

    fireEvent.keyDown(leftResizer, { key: 'ArrowLeft' });

    expect(leftResizer).toHaveAttribute('aria-valuenow', '248');
    expect(layout).toHaveStyle({ gridTemplateColumns: 'minmax(0, 1fr)' });

    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));
    const rightResizer = screen.getByRole('separator', { name: '调整侧边栏宽度' });
    expect(rightResizer.tagName).toBe('BUTTON');

    expect(rightResizer).toHaveAttribute('aria-valuenow', '264');

    fireEvent.keyDown(rightResizer, { key: 'ArrowLeft' });

    expect(rightResizer).toHaveAttribute('aria-valuenow', '280');
    expect(layout).toHaveStyle({ gridTemplateColumns: 'minmax(0, 1fr) 6px 280px' });

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

  it('keeps default chat columns proportional when the window is resized', async () => {
    render(<App />);
    await waitForBackendThreadHeading();

    const layout = screen.getByTestId('chat-layout');

    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));
    expect(layout).toHaveStyle({ gridTemplateColumns: 'minmax(0, 1fr) 6px 189px' });

    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1980 });
    act(() => {
      window.dispatchEvent(new Event('resize'));
    });

    await waitFor(() => {
      expect(layout).toHaveStyle({ gridTemplateColumns: 'minmax(0, 1fr) 6px 380px' });
    });
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

    render(<App />);
    await waitForBackendThreadHeading();

    const layout = screen.getByTestId('chat-layout');

    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));
    const rightResizer = screen.getByTestId('right-panel-resizer');

    expect(useClientStore.getState().rightPanelWidth).toBe(380);

    dispatchPointer(rightResizer, 'pointerdown', 1100);
    dispatchPointer(window, 'pointermove', 700);

    expect(layout).toHaveStyle({ gridTemplateColumns: 'minmax(0, 1fr) 6px 751px' });
    expect(useClientStore.getState().rightPanelWidth).toBe(380);

    dispatchPointer(window, 'pointerup', 700);

    await waitFor(() => {
      expect(useClientStore.getState().rightPanelWidth).toBe(751);
    });
  });

  it('stops right sidebar resizing when the pointer is no longer pressed', async () => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1980 });

    render(<App />);
    await waitForBackendThreadHeading();

    const layout = screen.getByTestId('chat-layout');

    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));
    const rightResizer = screen.getByTestId('right-panel-resizer');

    dispatchPointer(rightResizer, 'pointerdown', 1100);
    dispatchPointer(window, 'pointermove', 1000);
    expect(layout).toHaveStyle({ gridTemplateColumns: 'minmax(0, 1fr) 6px 480px' });

    dispatchPointer(window, 'pointermove', 700, { buttons: 0 });
    expect(layout).toHaveStyle({ gridTemplateColumns: 'minmax(0, 1fr) 6px 480px' });
    expect(useClientStore.getState().rightPanelWidth).toBe(480);

    dispatchPointer(window, 'pointermove', 500, { buttons: 0 });
    expect(layout).toHaveStyle({ gridTemplateColumns: 'minmax(0, 1fr) 6px 480px' });
  });

  it('keeps the right sidebar draggable past the previous early close width', async () => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1980 });

    render(<App />);
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

    await waitFor(() => {
      expect(useClientStore.getState().rightPanelWidth).toBe(150);
    });
  });

  it('closes the right sidebar when dragged flush to the right edge', async () => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1980 });

    render(<App />);
    await waitForBackendThreadHeading();

    const layout = screen.getByTestId('chat-layout');

    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));
    const rightResizer = screen.getByTestId('right-panel-resizer');

    dispatchPointer(rightResizer, 'pointerdown', 1100);
    dispatchPointer(window, 'pointermove', 1480);

    await waitFor(() => {
      expect(screen.queryByTestId('runtime-panel')).not.toBeInTheDocument();
      expect(screen.queryByTestId('right-panel-resizer')).not.toBeInTheDocument();
      expect(layout).toHaveStyle({ gridTemplateColumns: 'minmax(0, 1fr)' });
      expect(useClientStore.getState().rightPanelWidth).toBe(0);
    });
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
    dispatchPointer(window, 'pointermove', 1300);
    dispatchPointer(window, 'pointerup', 1300);

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
    fireEvent.click(screen.getByRole('region', { name: '工具使用面板' }));
    expect(screen.queryByTestId('runtime-stat-tooltip')).not.toBeInTheDocument();

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

    expect(screen.getByTestId('warning-log-popover')).toHaveTextContent('src/App.jsx: runtime log result');
    expect(screen.getByTestId('warning-log-popover')).toHaveTextContent('"preview": "{\\"total\\":3}"');
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

  it('connects attachments and conversation operation buttons', async () => {
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
      expect(backend.selectFiles).toHaveBeenCalled();
      expect(JSON.parse(backend.copyTextToClipboard.mock.calls[0][0])).toEqual(expect.objectContaining({
        agentId: 'agent-1',
        providerThreadId: 'provider-thread-1',
        provider: 'codex',
      }));
      expect(backend.interruptTurn).toHaveBeenCalledWith({ cwd: '/repo/app', threadId: 'thread-1', source: 'ui_stop' });
      expect(backend.forceCompleteTurn).toHaveBeenCalledWith({ cwd: '/repo/app', threadId: 'thread-1' });
      expect(backend.recoverThread).toHaveBeenCalledWith({ cwd: '/repo/app', threadId: 'thread-1' });
      expect(backend.archiveThread).not.toHaveBeenCalled();
    });
  });

  it('submits timeline approval decisions from the React chat timeline', async () => {
    backend.respondApproval.mockResolvedValue({ ok: true });
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{
          id: 'approval-1',
          role: 'assistant',
          kind: 'approval',
          title: 'shell',
          text: '需要执行 deploy 命令',
          requestId: 11,
          status: 'pending',
          ts: '2026-05-30T00:00:00Z',
        }],
      },
    });

    render(<App />);

    expect(await screen.findByTestId('approval-request-11')).toHaveTextContent('需要执行 deploy 命令');
    fireEvent.click(screen.getByRole('button', { name: '同意审批 11' }));

    await waitFor(() => {
      expect(backend.respondApproval).toHaveBeenCalledWith({ requestId: 11, approved: true });
    });
    expect(screen.getByRole('button', { name: '同意审批 11' })).toBeDisabled();
  });

  it('interrupts the selected conversation when Escape is pressed', async () => {
    render(<App />);
    await waitForBackendThreadHeading();

    fireEvent.keyDown(window, { key: 'Escape', code: 'Escape' });

    await waitFor(() => {
      expect(backend.interruptTurn).toHaveBeenCalledWith({ cwd: '/repo/app', threadId: 'thread-1', source: 'ui_stop' });
    });
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
    expect(dialog).toHaveTextContent('/tmp/a.txt');
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
      expect(screen.getByTestId('chat-action-feedback')).toHaveTextContent('复制失败：clipboard copy failed: native ui/copyText returned ok=false: clipboard not available in headless mode');
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

  it.skip('keeps provider switching available before a backend chat exists', async () => {
    backend.getSidebarState.mockResolvedValue({ activeThreadId: '', threads: [] });
    backend.getThreadState.mockResolvedValue({ timelinesByThread: {} });

    render(<App />);
    await screen.findByText('我们应该在 燧元 中构建什么？');

    const providerToggle = screen.getByLabelText('切换 Claude / Codex provider');
    expect(providerToggle).not.toBeDisabled();

    fireEvent.click(providerToggle);

    await waitFor(() => {
      expect(backend.setPreference).toHaveBeenCalledWith({
        cwd: '/repo/app',
        key: 'settings.provider.active',
        value: 'claude',
      });
      expect(screen.getByLabelText('切换 Claude / Codex provider')).toHaveTextContent('Claude');
      expect(screen.getByTestId('chat-action-feedback')).toHaveTextContent('已切换为 Claude');
    });
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

  it.skip('uses sidebar runtime metadata for provider-less thread cards', async () => {
    backend.getPreference.mockImplementation(({ key }) => Promise.resolve({
      'settings.provider.active': 'claude',
      'settings.provider.claude.model': 'sonnet',
      'settings.provider.claude.effort': 'high',
    }[key] ?? null));
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: 'thread-unknown',
      threads: [{ id: 'thread-unknown', name: 'Provider missing', status: 'error' }],
      agentRuntimeById: {
        'thread-unknown': { provider: 'claude' },
      },
    });
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-unknown',
      timelinesByThread: { 'thread-unknown': [] },
    });
    backend.getThreadConfig.mockResolvedValue({
      threadId: 'thread-unknown',
      provider: 'claude',
      supportsThreadOverride: true,
      override: {},
      effective: { model: 'sonnet', effort: 'high' },
    });

    render(<App />);

    await screen.findByText('Provider missing');
    expect(screen.getByText('claude')).toBeInTheDocument();
    expect(screen.queryByText('unknown')).not.toBeInTheDocument();
    expect(screen.queryByText('Codex')).not.toBeInTheDocument();
    expect(screen.queryByText('codex')).not.toBeInTheDocument();
  });

  it('aligns the project selector dropdown with old project actions', async () => {
    backend.getProjects.mockResolvedValue({ projects: ['/repo/app', '/repo/other'], active: '/repo/app' });
    backend.setActiveProject.mockImplementation(({ path }) => Promise.resolve({
      projects: path === '/repo/new' ? ['/repo/app', '/repo/other', '/repo/new'] : ['/repo/app', '/repo/other'],
      active: path,
    }));
    backend.addProject.mockResolvedValue({ projects: ['/repo/app', '/repo/other', '/repo/new'], active: '/repo/other' });
    backend.removeProject.mockResolvedValue({ projects: ['/repo/app', '/repo/other'], active: '/repo/other' });

    render(<App />);
    await waitForBackendThreadHeading();

    fireEvent.click(screen.getByRole('button', { name: '选择项目' }));
    expect(screen.getByRole('menu', { name: '项目列表' })).toHaveTextContent('repo/app');
    expect(screen.getByRole('menu', { name: '项目列表' })).toHaveTextContent('repo/other');

    fireEvent.click(screen.getByRole('menuitem', { name: 'repo/other' }));
    await waitFor(() => {
      expect(backend.setActiveProject).toHaveBeenCalledWith({ cwd: '/repo/app', path: '/repo/other' });
      expect(screen.getByRole('button', { name: '选择项目' })).toHaveTextContent(/^other$/);
    });

    fireEvent.click(screen.getByRole('button', { name: '选择项目' }));
    fireEvent.click(screen.getByRole('menuitem', { name: '添加项目' }));
    await waitFor(() => {
      expect(backend.selectProjectDir).toHaveBeenCalledWith('/repo/other');
      expect(backend.addProject).toHaveBeenCalledWith({ cwd: '/repo/app', path: '/repo/new' });
      expect(backend.setActiveProject).toHaveBeenCalledWith({ cwd: '/repo/app', path: '/repo/new' });
      expect(screen.getByRole('button', { name: '选择项目' })).toHaveTextContent(/^new$/);
    });

    fireEvent.click(screen.getByRole('button', { name: '选择项目' }));
    fireEvent.click(screen.getByRole('button', { name: '移除此项目 repo/new' }));
    await waitFor(() => {
      expect(backend.removeProject).toHaveBeenCalledWith({ cwd: '/repo/app', path: '/repo/new' });
      expect(screen.getByTestId('chat-action-feedback')).toHaveTextContent('已移除项目：repo/new');
    });
  });

  it('keeps the independent new-window action in the top command bar', async () => {
    backend.selectProjectDir.mockResolvedValue('/repo/window');

    render(<App />);
    await waitForBackendThreadHeading();

    fireEvent.click(screen.getByRole('button', { name: '新窗口（独立进程）' }));

    await waitFor(() => {
      expect(backend.selectProjectDir).toHaveBeenCalledWith('/repo/app');
      expect(backend.openNewWindow).toHaveBeenCalledWith({ cwd: '/repo/window' });
      expect(screen.getByTestId('chat-action-feedback')).toHaveTextContent('已打开新窗口：repo/window');
    });
  });

  it('switches from current directory to the visible absolute cwd project option', async () => {
    backend.getProjects.mockResolvedValue({ projects: [], active: '.' });
    backend.addProject.mockResolvedValue({ projects: ['/repo/app'], active: '.' });
    backend.setActiveProject.mockResolvedValue({ projects: ['/repo/app'], active: '/repo/app' });

    render(<App />);
    await waitForBackendThreadHeading();

    fireEvent.click(screen.getByRole('button', { name: '选择项目' }));
    expect(screen.getByRole('menu', { name: '项目列表' })).toHaveTextContent('当前目录 (.)');
    expect(screen.getByRole('menu', { name: '项目列表' })).toHaveTextContent('repo/app');

    fireEvent.click(screen.getByRole('menuitem', { name: 'repo/app' }));

    await waitFor(() => {
      expect(backend.addProject).toHaveBeenCalledWith({ cwd: '/repo/app', path: '/repo/app' });
      expect(backend.setActiveProject).toHaveBeenCalledWith({ cwd: '/repo/app', path: '/repo/app' });
      expect(screen.getByRole('button', { name: '选择项目' })).toHaveTextContent(/^app$/);
    });
  });

  it('refreshes the chat list when switching to another project', async () => {
    backend.getProjects.mockResolvedValue({ projects: ['/repo/app', '/repo/other'], active: '/repo/app' });
    backend.setActiveProject.mockResolvedValue({ projects: ['/repo/app', '/repo/other'], active: '/repo/other' });
    backend.getSidebarState.mockImplementation(({ cwd }) => Promise.resolve(
      cwd === '/repo/other'
        ? {
          activeThreadId: 'thread-other',
          threads: [{ id: 'thread-other', name: 'Other project chat', provider: 'claude', status: 'idle' }],
        }
        : {
          activeThreadId: 'thread-1',
          threads: [{ id: 'thread-1', name: '后端线程', provider: 'codex', status: '工作中' }],
        },
    ));
    backend.getThreadState.mockImplementation(({ threadId, includeDiff }) => Promise.resolve({
      activeThreadId: threadId,
      timelinesByThread: { [threadId]: [] },
      ...(includeDiff ? { diffTextByThread: { [threadId]: '' } } : {}),
    }));

    render(<App />);
    await waitForBackendThreadHeading();

    fireEvent.click(screen.getByRole('button', { name: '选择项目' }));
    fireEvent.click(screen.getByRole('menuitem', { name: 'repo/other' }));

    await waitFor(() => {
      expect(backend.getSidebarState).toHaveBeenCalledWith({ cwd: '/repo/other' });
      expect(getThreadCardByName('Other project chat')).toBeInTheDocument();
      expect(queryBackendThreadText()).not.toBeInTheDocument();
    });
    expect(useClientStore.getState().activeThreadId).toBe('');
    expect(getThreadCardByName('Other project chat')).not.toHaveClass('active');
    expect(backend.getThreadState).not.toHaveBeenCalledWith({
      cwd: '/repo/other',
      threadId: 'thread-other',
      includeDiff: true,
    });

    clickThreadCardByName('Other project chat');

    await waitFor(() => {
      expect(backend.getThreadState).toHaveBeenCalledWith({
        cwd: '/repo/other',
        threadId: 'thread-other',
        includeDiff: false,
      });
    });
    expect(useClientStore.getState().activeThreadId).toBe('thread-other');
    expect(backend.getThreadState).not.toHaveBeenCalledWith({
      cwd: '/repo/other',
      threadId: 'thread-other',
      includeDiff: true,
    });

    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));

    await waitFor(() => {
      expect(backend.getThreadState).toHaveBeenCalledWith({
        cwd: '/repo/other',
        threadId: 'thread-other',
        includeDiff: true,
      });
    });
  });

  it('shows a loading chat list immediately while a project switch refreshes slowly', async () => {
    const projectChange = deferred();
    const sidebarRefresh = deferred();
    backend.getProjects.mockResolvedValue({ projects: ['/repo/app', '/repo/other'], active: '/repo/app' });
    backend.setActiveProject.mockReturnValue(projectChange.promise);
    backend.getSidebarState.mockImplementation(({ cwd }) => (
      cwd === '/repo/other'
        ? sidebarRefresh.promise
        : Promise.resolve({
          activeThreadId: 'thread-1',
          threads: [{ id: 'thread-1', name: '后端线程', provider: 'codex', status: '工作中' }],
        })
    ));

    render(<App />);
    await waitForBackendThreadHeading();

    fireEvent.click(screen.getByRole('button', { name: '选择项目' }));
    fireEvent.click(screen.getByRole('menuitem', { name: 'repo/other' }));

    await waitFor(() => {
      expect(screen.getByRole('button', { name: '选择项目' })).toHaveTextContent(/^other$/);
      expect(screen.getByText('正在加载会话列表…')).toBeInTheDocument();
      expect(queryBackendThreadText()).not.toBeInTheDocument();
    });

    await act(async () => {
      sidebarRefresh.resolve({
        activeThreadId: 'thread-other',
        threads: [{ id: 'thread-other', name: 'Other project chat', provider: 'claude', status: 'idle' }],
      });
      await Promise.resolve();
    });

    await waitFor(() => expect(getThreadCardByName('Other project chat')).toBeInTheDocument());
    expect(useClientStore.getState().activeThreadId).toBe('');

    await act(async () => {
      projectChange.resolve({ projects: ['/repo/app', '/repo/other'], active: '/repo/other' });
      await Promise.resolve();
    });
  });

  it('refreshes the chat list when the new project has no active sidebar thread', async () => {
    backend.getProjects.mockResolvedValue({ projects: ['/repo/app', '/repo/other'], active: '/repo/app' });
    backend.setActiveProject.mockResolvedValue({ projects: ['/repo/app', '/repo/other'], active: '/repo/other' });
    backend.getSidebarState.mockImplementation(({ cwd }) => Promise.resolve(
      cwd === '/repo/other'
        ? {
          activeThreadId: '',
          threads: [{ id: 'thread-other', name: 'Other project chat', provider: 'claude', status: 'idle' }],
        }
        : {
          activeThreadId: 'thread-1',
          threads: [{ id: 'thread-1', name: '后端线程', provider: 'codex', status: '工作中' }],
        },
    ));
    backend.getThreadState.mockImplementation(({ threadId }) => Promise.resolve({
      activeThreadId: threadId,
      timelinesByThread: { [threadId]: [] },
      diffTextByThread: { [threadId]: '' },
    }));

    render(<App />);
    await waitForBackendThreadHeading();

    fireEvent.click(screen.getByRole('button', { name: '选择项目' }));
    fireEvent.click(screen.getByRole('menuitem', { name: 'repo/other' }));

    await waitFor(() => {
      expect(getThreadCardByName('Other project chat')).toBeInTheDocument();
      expect(queryBackendThreadText()).not.toBeInTheDocument();
    });
    expect(useClientStore.getState().activeThreadId).toBe('');
    expect(getThreadCardByName('Other project chat')).not.toHaveClass('active');
    expect(backend.getThreadState).not.toHaveBeenCalledWith({
      cwd: '/repo/other',
      threadId: 'thread-other',
      includeDiff: true,
    });
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

    fireEvent.click(screen.getByRole('button', { name: '选择模型' }));
    expect(screen.getByRole('dialog', { name: '模型配置' }).tagName).toBe('DIALOG');
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
      expect(screen.getByRole('button', { name: '选择模型' })).toHaveTextContent('5.5 中');
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

    fireEvent.click(screen.getByTestId('warning-log-panel'));

    expect(screen.queryByTestId('warning-log-popover')).not.toBeInTheDocument();
  });

  it('navigates to screenshot-style secondary pages', async () => {
    render(<App />);
    await screen.findByLabelText('插件与技能');

    expect(screen.queryByLabelText('命令')).not.toBeInTheDocument();
    expect(screen.getByLabelText('任务')).toHaveTextContent('暂无任务');

    await openSkillToolsPage();
    expect(await screen.findByText('插件与技能')).toBeInTheDocument();
    expect(await screen.findByText('后端')).toBeInTheDocument();
    expect(screen.getByText('/repo/app/.agent/skills/backend')).toBeInTheDocument();
    expect(screen.getByText('私人使用 1')).toBeInTheDocument();
    expect(screen.getByText('项目共享 1')).toBeInTheDocument();
    expect(screen.getByText('全部 2')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /刷新/ })).not.toBeInTheDocument();
    expect(backend.getDashboardPage).toHaveBeenCalledWith({ cwd: '/repo/app', page: 'skills' });

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

    expect(await screen.findByRole('heading', { name: heading })).toBeInTheDocument();
    expect(await screen.findByText('正在连接本地项目...')).toBeInTheDocument();
    assertNoInvalidLoad();

    await act(async () => {
      config.resolve({ cwd: '/repo/app' });
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(await screen.findByText(settledText)).toBeInTheDocument();
    expect(screen.queryByText('正在连接本地项目...')).not.toBeInTheDocument();
  });

  it('loads global shared files while project context resolves', async () => {
    const config = deferred();
    backend.readConfig.mockReturnValueOnce(config.promise);

    render(<App />);
    fireEvent.click(screen.getByLabelText('共享文件'));

    expect(await screen.findByRole('heading', { name: '文件产物' })).toBeInTheDocument();
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
    backend.getMemorySnapshot.mockResolvedValue({
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
    });

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
          tags: '["intent:expert","review"]',
          scope: 'project',
          enabled: true,
        },
        {
          id: 'main/knowledge/sqlc',
          name: 'SQLC 资料',
          content: '',
          description: 'SQLC migration 资料',
          tags: '["intent:recall","scope.global","sqlc"]',
          scope: 'global',
          enabled: true,
        },
        {
          id: 'intent/recall/ready',
          draft_key: 'intent/recall/ready',
          name: '价格表资料',
          description: '从 Excel 价格表整理出的资料',
          tags: '["intent:recall","pricing"]',
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
    const firstScopeButton = within(editor).getByRole('button', { name: '这个项目' });
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
          id: 'main/code-review',
          name: '代码审查助手',
          description: '检查改动风险',
          content: '先列风险',
          tags: ['intent:expert'],
          scope: 'project',
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
    expect(await screen.findByRole('alert')).toHaveTextContent('同步失败，显示的是上次成功的数据：prompt backend offline');

    prompts = [{
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
    expect(alert).toHaveTextContent('同步失败，显示的是上次成功的数据：active prompt preference offline');

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
    expect(alert).toHaveTextContent('加载提示词失败');
    expect(alert).toHaveTextContent('prompt backend offline');
    expect(screen.queryByText('暂无内容')).not.toBeInTheDocument();

    backend.listPromptAssets.mockResolvedValueOnce({
      prompts: [{
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
        id: 'legacy/prompt',
        name: '旧提示词',
        content: 'legacy readonly data',
        tags: ['intent:expert'],
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
  let prompts = [{
    id: 'main/reviewer',
    name: '代码审查专家',
    content: '先检查阻塞问题',
    description: '审查代码质量',
    when_to_use: 'Use for code review.',
    agentType: 'coder',
    tags: ['intent:expert', 'review'],
    scope: 'project',
    enabled: true,
  }, {
    id: 'intent/recall/ready',
    draft_key: 'intent/recall/ready',
    name: '价格表资料',
    description: '待确认的资料',
    tags: ['intent:recall', 'pricing'],
    state: 'pending_confirm',
    draft_status: 'ready_to_save',
    card: { kind: 'recall', title: '价格表资料', summary: '待确认的资料', output: '价格资料内容' },
  }];
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

  it('loads memory center through ui/memory/get and groups entries by type', async () => {
    backend.getMemorySnapshot.mockResolvedValue({
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
    });

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
    backend.getMemorySnapshot.mockImplementation(() => Promise.resolve({
      overview: {
        enabled: true,
        autoDreamEnabled: true,
        autoDreamIntent: null,
        projectRoot: '/repo/app',
        health: { preferenceCount: entries.length, projectCount: 0, maxPerCategory: 15, similarGroups: [] },
      },
      private: { entries },
      team: { entries: [] },
    }));

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
      backend.getMemorySnapshot.mockResolvedValue({
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
      });

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
    backend.getMemorySnapshot.mockImplementation(() => Promise.resolve({
      overview: {
        enabled: true,
        autoDreamEnabled: true,
        autoDreamIntent: null,
        projectRoot: '/repo/app',
        health: { preferenceCount: entries.length, projectCount: 0, maxPerCategory: 15, similarGroups: [] },
      },
      private: { entries },
      team: { entries: [] },
    }));

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
    expect(await screen.findByRole('alert')).toHaveTextContent('同步失败，显示的是上次成功的数据：memory backend offline');

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
      return Promise.resolve({
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
      });
    });

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('记忆中心'));

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('memory backend offline');
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
    backend.getMemorySnapshot.mockImplementation(() => Promise.resolve({
      overview: {
        enabled: true,
        autoDreamEnabled: true,
        autoDreamIntent: null,
        projectRoot: '/repo/app',
        health: { preferenceCount: entries.length, projectCount: 0, maxPerCategory: 15, similarGroups: [] },
      },
      private: { entries },
      team: { entries: [] },
    }));

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
    backend.getMemorySnapshot.mockResolvedValue({
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
    });

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('记忆中心'));
    expect(await screen.findByText('遵守 TDD')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '开启' }));
    await waitFor(() => {
      expect(backend.setMemoryAutoDreamIntent).toHaveBeenCalledWith({ enabled: true });
    });

    fireEvent.click(screen.getByRole('button', { name: '+ 新建 ▾' }));
    fireEvent.click(screen.getByRole('button', { name: '新建偏好' }));
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
    backend.getMemorySnapshot.mockResolvedValue({
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
    });

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
  const snapshotWithSimilar = {
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
  };
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

    const { container } = render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('共享文件'));

    const finalCard = (await screen.findByText('douyin_viral_scripts.md')).closest('article');
    expect(within(finalCard).getByText(/JSON 对象 · videos: 1 项/)).toBeInTheDocument();
    expect(within(finalCard).queryByText(/```json/)).not.toBeInTheDocument();

    fireEvent.click(within(finalCard).getByRole('button', { name: '打开' }));
    const dialog = await screen.findByRole('dialog', { name: '文件预览' });
    expect(within(dialog).getByText('JSON（Markdown 代码块）')).toBeInTheDocument();

    const preview = container.querySelector('.shared-file-content-preview');
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

    const { container } = render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('共享文件'));

    const finalCard = (await screen.findByText('douyin_viral_scripts.md')).closest('article');
    expect(within(finalCard).getByText(/类 JSON · videos: 1 项/)).toBeInTheDocument();
    expect(within(finalCard).queryByText(/JSON 格式化失败|JSON Parse error|Unrecognized token/)).not.toBeInTheDocument();

    fireEvent.click(within(finalCard).getByRole('button', { name: '打开' }));
    const dialog = await screen.findByRole('dialog', { name: '文件预览' });
    expect(within(dialog).getByText('类 JSON（Markdown 代码块）')).toBeInTheDocument();

    const preview = container.querySelector('.shared-file-content-preview');
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
    expect(await screen.findByRole('alert')).toHaveTextContent('同步失败，显示的是上次成功的数据：shared files backend offline');

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
    expect(alert).toHaveTextContent('加载共享文件失败：shared files backend offline');
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
    expect(await screen.findByRole('alert')).toHaveTextContent('同步失败，显示的是上次成功的数据：workflow backend offline');

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
    expect(await screen.findByRole('alert')).toHaveTextContent('同步失败，显示的是上次成功的数据：workflow backend offline');

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
    expect(alert).toHaveTextContent('workflow backend offline');
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

    expect(await screen.findByRole('alert')).toHaveTextContent('detail backend offline');
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
    expect(alert).toHaveTextContent('加载自动化失败：workflow backend offline');
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
  fireEvent.click(screen.getByRole('button', { name: '通过聊天创建' }));
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
  expect((await screen.findAllByText('AI 设计流程')).length).toBeGreaterThanOrEqual(1);
  const designThreadCard = screen.getAllByText('AI 设计流程')
    .map((node) => node.closest('.thread-card'))
    .find(Boolean);
  expect(designThreadCard).toHaveTextContent('codex');
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

  it('refreshes skills page from backend when skills changed event arrives', async () => {
    render(<App />);
    await waitForBackendThreadHeading();
    await openSkillToolsPage();
    expect(await screen.findByText('后端')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /刷新/ })).not.toBeInTheDocument();

    backend.getDashboardPage.mockResolvedValueOnce({
      skills: [{
        name: 'security',
        display_name: '安全工程师',
        dir: '/repo/app/.agent/skills/security',
        description: '安全审计',
        trigger_words: ['security'],
        scope: 'project',
      }],
    });

    act(() => {
      bridgeCallback({ type: 'skills/changed', payload: { cwd: '/repo/app' } });
    });

    expect(await screen.findByText('安全工程师')).toBeInTheDocument();
    expect(screen.queryByText('后端')).not.toBeInTheDocument();
    expect(backend.getDashboardPage).toHaveBeenCalledTimes(2);

    backend.getDashboardPage.mockResolvedValueOnce({
      skills: [{
        name: 'review-style',
        display_name: '审查风格',
        dir: '/repo/app/.agent/skills/review-style',
        description: '先列风险',
        trigger_words: ['review'],
        scope: 'project',
      }],
    });

    await act(async () => {
      window.dispatchEvent(new Event('focus'));
    });

    expect(await screen.findByText('审查风格')).toBeInTheDocument();
    expect(screen.queryByText('安全工程师')).not.toBeInTheDocument();
    expect(backend.getDashboardPage).toHaveBeenCalledTimes(3);
  });

  it('does not repeat a skill description when summary is empty', async () => {
    render(<App />);
    await waitForBackendThreadHeading();
    await openSkillToolsPage();
    const personalCard = (await screen.findByRole('heading', { name: 'personal-review' })).closest('article');

    expect(within(personalCard).getAllByText('当你需要私人代码审查偏好时使用。')).toHaveLength(1);
  });

  it('shows skills filter counts and an empty search state', async () => {
    render(<App />);
    await waitForBackendThreadHeading();
    await openSkillToolsPage();

    expect(await screen.findByText('共 2 个技能')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '私人使用 1' }));
    expect(screen.getByText('显示 1 个，共 2 个技能')).toBeInTheDocument();

    fireEvent.change(screen.getByPlaceholderText('搜索技能名称、简介、关键词...'), {
      target: { value: 'does-not-exist' },
    });

    expect(screen.getByText('没有匹配技能')).toBeInTheDocument();
    expect(screen.getByText('尝试更换关键词或切换使用范围，支持按名称、简介、关键词搜索')).toBeInTheDocument();
    expect(screen.getByText('当前没有匹配技能，共 2 个')).toBeInTheDocument();
    expect(screen.queryByText('暂无技能')).not.toBeInTheDocument();
  });

  it('keeps the skills route visible while project context resolves', async () => {
    const config = deferred();
    backend.readConfig.mockReturnValueOnce(config.promise);

    render(<App />);
    await openSkillToolsPage();

    expect(await screen.findByRole('heading', { name: '插件与技能' })).toBeInTheDocument();
    expect(await screen.findByText('正在连接本地项目...')).toBeInTheDocument();
    expect(backend.getDashboardPage).not.toHaveBeenCalledWith({ cwd: '未选择项目', page: 'skills' });

    await act(async () => {
      config.resolve({ cwd: '/repo/app' });
      await Promise.resolve();
    });

    expect(await screen.findByText('后端')).toBeInTheDocument();
    expect(backend.getDashboardPage).toHaveBeenCalledWith({ cwd: '/repo/app', page: 'skills' });
  });

  it('keeps skills visible and exposes retry when a background sync fails', async () => {
    render(<App />);
    await waitForBackendThreadHeading();
    await openSkillToolsPage();
    expect(await screen.findByText('后端')).toBeInTheDocument();

    backend.getDashboardPage.mockRejectedValueOnce(new Error('backend offline'));
    await act(async () => {
      bridgeCallback({ type: 'skills/changed', payload: { cwd: '/repo/app' } });
      await Promise.resolve();
    });

    expect(screen.getByText('后端')).toBeInTheDocument();
    expect(await screen.findByRole('alert')).toHaveTextContent('同步失败，显示的是上次成功的数据：backend offline');

    backend.getDashboardPage.mockResolvedValueOnce({
      skills: [{
        name: 'security',
        display_name: '安全工程师',
        dir: '/repo/app/.agent/skills/security',
        description: '安全审计',
        trigger_words: ['security'],
        scope: 'project',
      }],
    });
    fireEvent.click(screen.getByRole('button', { name: '重试同步' }));

    expect(await screen.findByText('安全工程师')).toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('keeps skills visible and exposes retry when the resolution payload is invalid', async () => {
    backend.listSkillResolutions.mockResolvedValueOnce({});

    render(<App />);
    await waitForBackendThreadHeading();
    await openSkillToolsPage();

    expect(await screen.findByText('后端')).toBeInTheDocument();
    expect(await screen.findByRole('alert')).toHaveTextContent('读取技能冲突失败：skill resolutions response items must be an array');

    backend.listSkillResolutions.mockResolvedValueOnce({ items: [] });
    fireEvent.click(screen.getByRole('button', { name: '重试同步' }));

    await waitFor(() => {
      expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    });
    expect(screen.getByText('后端')).toBeInTheDocument();
  });

  it('keeps cached skills visible when navigating back and refreshes silently', async () => {
    render(<App />);
    await waitForBackendThreadHeading();
    await openSkillToolsPage();
    expect(await screen.findByText('后端')).toBeInTheDocument();

    fireEvent.click(screen.getByLabelText('新对话'));
    backend.getDashboardPage.mockResolvedValueOnce({
      skills: [{
        name: 'security',
        display_name: '安全工程师',
        dir: '/repo/app/.agent/skills/security',
        description: '安全审计',
        trigger_words: ['security'],
        scope: 'project',
      }],
    });
    await openSkillToolsPage();

    expect(screen.queryByText('加载技能中...')).not.toBeInTheDocument();
    expect(await screen.findByText('安全工程师')).toBeInTheDocument();
    expect(screen.queryByText('后端')).not.toBeInTheDocument();
  });

  it('releases the skills loading state when the dashboard request hangs', async () => {
    render(<App />);
    await waitForBackendThreadHeading();

    let rejectSkillsDashboard;
    backend.getDashboardPage.mockImplementation(({ page }) => (
      page === 'skills'
        ? new Promise((_, reject) => {
          rejectSkillsDashboard = reject;
        })
        : Promise.resolve({
          memory: [],
          finalOutputRefs: [],
          sharedFileRetention: { items: [], protectedCount: 0, cleanupCandidateCount: 0 },
        })
    ));

    fireEvent.click(screen.getByLabelText('插件与技能'));
    const skillToolsTab = await screen.findByRole('button', { name: 'Skill工具' });

    fireEvent.click(skillToolsTab);
    expect(screen.getByText('加载技能中...')).toBeInTheDocument();

    await act(async () => {
      rejectSkillsDashboard(new Error('技能列表加载超时，请检查技能目录或后端状态。'));
      await Promise.resolve();
    });

    expect(await screen.findByRole('alert')).toHaveTextContent('技能列表加载超时');
    expect(screen.queryByText('加载技能中...')).not.toBeInTheDocument();

    backend.getDashboardPage.mockImplementation(({ page }) => Promise.resolve(
      page === 'skills'
        ? {
          skills: [{
            name: 'security',
            display_name: '安全工程师',
            dir: '/repo/app/.agent/skills/security',
            description: '安全审计',
            trigger_words: ['security'],
            scope: 'project',
          }],
        }
        : {
          memory: [],
          finalOutputRefs: [],
          sharedFileRetention: { items: [], protectedCount: 0, cleanupCandidateCount: 0 },
        },
    ));

    await act(async () => {
      window.dispatchEvent(new Event('focus'));
      await Promise.resolve();
    });

    expect(await screen.findByText('安全工程师')).toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('shows a retryable blocking error instead of an empty skills state on initial load failure', async () => {
    backend.getDashboardPage.mockImplementation(({ page }) => (
      page === 'skills'
        ? Promise.reject(new Error('skills backend offline'))
        : Promise.resolve({
          memory: [],
          finalOutputRefs: [],
          sharedFileRetention: { items: [], protectedCount: 0, cleanupCandidateCount: 0 },
        })
    ));

    render(<App />);
    await waitForBackendThreadHeading();
    await openSkillToolsPage();

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('skills backend offline');
    expect(screen.queryByText('暂无技能')).not.toBeInTheDocument();

    backend.getDashboardPage.mockImplementation(({ page }) => Promise.resolve(
      page === 'skills'
        ? {
          skills: [{
            name: 'security',
            display_name: '安全工程师',
            dir: '/repo/app/.agent/skills/security',
            description: '安全审计',
            trigger_words: ['security'],
            scope: 'project',
          }],
        }
        : {
          memory: [],
          finalOutputRefs: [],
          sharedFileRetention: { items: [], protectedCount: 0, cleanupCandidateCount: 0 },
        },
    ));
    fireEvent.click(within(alert).getByRole('button', { name: '重试同步' }));

    expect(await screen.findByText('安全工程师')).toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('deletes a skill through the legacy scoped API and refreshes the list', async () => {
    render(<App />);
    await waitForBackendThreadHeading();
    await openSkillToolsPage();
    expect(await screen.findByText('后端')).toBeInTheDocument();

    backend.getDashboardPage.mockResolvedValueOnce({ skills: [] });
    const backendCard = screen.getByText('后端').closest('article');
    fireEvent.click(within(backendCard).getByRole('button', { name: '删除' }));
    expect(await screen.findByRole('dialog', { name: '删除技能' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '确认删除' }));

    await waitFor(() => {
      expect(backend.deleteSkill).toHaveBeenCalledWith({
        cwd: '/repo/app',
        name: 'backend',
        scope: 'project',
        personal_type: '',
      });
      expect(backend.getDashboardPage).toHaveBeenCalledTimes(2);
    });
    expect(await screen.findByText('暂无技能')).toBeInTheDocument();
  });

  it('creates a skill, suggests a summary, and saves through skills/create', async () => {
    backend.suggestSkillSummary.mockResolvedValueOnce({ description: '当你需要部署服务时使用。' });
    backend.getPreference.mockImplementation(({ key }) => Promise.resolve({
      'settings.provider.active': 'codex',
      'settings.provider.codex.model': 'gpt-5.5',
      'settings.provider.codex.effort': 'xhigh',
      'settings.provider.codex.codexHome': '~/.codex',
      'settings.provider.codex.codexInstanceKey': 'default',
      'settings.provider.codex.codexModelProvider': 'openrouter',
    }[key] ?? null));

    render(<App />);
    await waitForBackendThreadHeading();
    await openSkillToolsPage();
    await screen.findByText('后端');

    fireEvent.click(screen.getByRole('button', { name: '新建技能' }));
    expect(await screen.findByRole('dialog', { name: '新建技能' })).toBeInTheDocument();
    expect(screen.queryByLabelText('显示名称')).not.toBeInTheDocument();
    fireEvent.change(screen.getByLabelText('技能名称'), { target: { value: '部署技能' } });
    fireEvent.change(screen.getByLabelText('关键词'), { target: { value: 'deploy, ship' } });
    fireEvent.change(screen.getByLabelText('技能内容'), { target: { value: '## 部署规则\n执行部署前检查环境。' } });
    fireEvent.click(screen.getByRole('button', { name: '帮我生成' }));

    const summarySuggestion = await screen.findByText(/当你需要部署服务时使用。/);
    const scopeLabel = screen.getAllByText('使用范围').find((element) => element.tagName.toLowerCase() === 'span');
    expect(summarySuggestion).toBeInTheDocument();
    expect(screen.getByLabelText('技能简介').compareDocumentPosition(summarySuggestion) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(summarySuggestion.compareDocumentPosition(scopeLabel) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: '采用' }));
    expect(screen.getByLabelText('技能简介')).toHaveValue('当你需要部署服务时使用。');
    fireEvent.click(screen.getByRole('button', { name: '保存技能' }));

    await waitFor(() => {
      expect(backend.suggestSkillSummary).toHaveBeenCalledWith({
        cwd: '/repo/app',
        name: '部署技能',
        description: '',
        content: '## 部署规则\n执行部署前检查环境。',
        scenario_words: ['deploy', 'ship'],
        scope: 'project',
        provider: 'codex',
        model: 'gpt-5.5',
        codexModelProvider: 'openrouter',
      });
      expect(backend.createSkill).toHaveBeenCalledWith(expect.objectContaining({
        cwd: '/repo/app',
        name: '部署技能',
      }));
    });
    expect(backend.writeSkill).not.toHaveBeenCalledWith(expect.objectContaining({
      path: '部署技能',
      scope: 'project',
    }));
    const savePayload = backend.createSkill.mock.calls.at(-1)[0];
    expect(savePayload.content).toContain('name: "部署技能"');
    expect(savePayload.content).toContain('display_name: "部署技能"');
    expect(savePayload.content).toContain('description: "当你需要部署服务时使用。"');
    expect(savePayload.content).toContain('trigger_words: ["deploy", "ship"]');
  });

  it('opens an existing skill, loads related files, and saves edits', async () => {
    render(<App />);
    await waitForBackendThreadHeading();
    await openSkillToolsPage();
    await screen.findByText('后端');

    const backendCard = screen.getByText('后端').closest('article');
    fireEvent.click(within(backendCard).getByRole('button', { name: '编辑详情' }));

    expect(await screen.findByRole('dialog', { name: '编辑技能' })).toBeInTheDocument();
    expect(screen.queryByLabelText('显示名称')).not.toBeInTheDocument();
    expect(screen.getByLabelText('技能名称')).toHaveValue('后端');
    expect(backend.readSkill).toHaveBeenCalledWith({
      cwd: '/repo/app',
      path: '/repo/app/.agent/skills/backend/SKILL.md',
    });
    expect(backend.listSkillFiles).toHaveBeenCalledWith({
      cwd: '/repo/app',
      dir: '/repo/app/.agent/skills/backend',
    });
    expect(screen.getByLabelText('技能简介')).toHaveValue('当你需要 Go 后端开发时使用。');
    expect(screen.getByText('guide.md')).toBeInTheDocument();
    expect(screen.getByTestId('skills-editor-body-preview')).toBeInTheDocument();
    expect(screen.queryByLabelText('技能内容')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: '编辑正文' })).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText('技能名称'), { target: { value: 'Go 后端' } });
    fireEvent.change(screen.getByLabelText('技能简介'), { target: { value: '当你需要维护 Go 服务时使用。' } });
    fireEvent.click(screen.getByRole('button', { name: '编辑正文' }));
    expect(screen.getByLabelText('技能内容')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '预览正文' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '保存技能' }));

    await waitFor(() => {
      expect(backend.writeSkill).toHaveBeenCalledWith(expect.objectContaining({
        cwd: '/repo/app',
        path: '/repo/app/.agent/skills/backend/SKILL.md',
        scope: 'project',
        personal_type: '',
      }));
    });
    const savedContent = backend.writeSkill.mock.calls.at(-1)[0].content;
    expect(savedContent).toContain('name: "backend"');
    expect(savedContent).toContain('display_name: "Go 后端"');
    expect(savedContent).toContain('description: "当你需要维护 Go 服务时使用。"');
  });

  it('opens a linked skill subfile from the markdown preview', async () => {
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
            '',
            '参考 [guide](references/guide.md)。',
          ].join('\n')
          : '## Guide Body',
      },
    }));

    render(<App />);
    await waitForBackendThreadHeading();
    await openSkillToolsPage();
    await screen.findByText('后端');

    const backendCard = screen.getByText('后端').closest('article');
    fireEvent.click(within(backendCard).getByRole('button', { name: '编辑详情' }));

    expect(await screen.findByRole('dialog', { name: '编辑技能' })).toBeInTheDocument();
    fireEvent.click(await screen.findByRole('button', { name: 'guide' }));

    await waitFor(() => {
      expect(backend.readSkill).toHaveBeenCalledWith({
        cwd: '/repo/app',
        path: '/repo/app/.agent/skills/backend/references/guide.md',
      });
    });
    expect(await screen.findByText('Guide Body')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '编辑正文' }));
    fireEvent.change(screen.getByLabelText('关联文件内容'), { target: { value: '## Updated Guide' } });
    fireEvent.click(screen.getByRole('button', { name: '保存文件' }));

    await waitFor(() => {
      expect(backend.writeSkill).toHaveBeenCalledWith({
        cwd: '/repo/app',
        path: '/repo/app/.agent/skills/backend/references/guide.md',
        content: '## Updated Guide',
        scope: 'project',
        personal_type: '',
      });
      expect(screen.getByText('文件已保存：guide.md')).toBeInTheDocument();
    });
  });

  it('imports skill directories with selected scope through skills/local/importDir', async () => {
    render(<App />);
    await waitForBackendThreadHeading();
    await openSkillToolsPage();
    await screen.findByText('后端');

    fireEvent.click(screen.getByRole('button', { name: '批量导入技能目录' }));
    expect(await screen.findByRole('dialog', { name: '导入技能' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '私人使用' }));

    await waitFor(() => {
      expect(backend.selectProjectDirs).toHaveBeenCalledTimes(1);
      expect(backend.importSkillDirectories).toHaveBeenCalledWith({
        cwd: '/repo/app',
        paths: ['/imports/ImportedSkill'],
        scope: 'personal',
        personal_type: 'imported',
      });
      expect(backend.getDashboardPage).toHaveBeenCalledTimes(2);
    });
  });

  it('surfaces duplicate import failures and same-name conflict drafts', async () => {
    backend.importSkillDirectories.mockResolvedValueOnce({
      imported: [{ name: 'ProjectConflict', skill_file: '/imports/ProjectConflict/SKILL.md' }],
      failures: [{ source: '/imports/DupSkill', error: 'skill already exists: DupSkill' }],
    });
    backend.readSkill.mockRejectedValueOnce(new Error('skill same-name conflict: ProjectConflict'));

    render(<App />);
    await waitForBackendThreadHeading();
    await openSkillToolsPage();
    await screen.findByText('后端');

    fireEvent.click(screen.getByRole('button', { name: '批量导入技能目录' }));
    fireEvent.click(await screen.findByRole('button', { name: '项目共享' }));

    expect(await screen.findByText(/项目共享里已存在：DupSkill，未重复导入。/)).toBeInTheDocument();
    expect(screen.getByTestId('skills-import-summary-panel')).toHaveTextContent('导入后需要处理');
    expect(screen.getByTestId('skills-import-summary-item-0')).toHaveTextContent('已导入，但与现有技能同名，暂未启用。请在冲突提示中选择使用哪个版本。');
  });

  it('shows import summary drafts and opens the imported skill with the suggested summary', async () => {
    backend.readSkill.mockImplementation(({ path }) => Promise.resolve({
      skill: {
        content: path.includes('/imports/ImportedSkill')
          ? [
            '---',
            'name: "ImportedSkill"',
            'display_name: "Imported Skill"',
            '---',
            '',
            '## 导入规则',
          ].join('\n')
          : [
            '---',
            'name: "backend"',
            'display_name: "后端"',
            'description: "当你需要 Go 后端开发时使用。"',
            'trigger_words: ["Go", "backend"]',
            '---',
            '',
            '## 后端规则',
          ].join('\n'),
      },
    }));
    backend.suggestSkillSummary.mockResolvedValueOnce('当你需要维护导入技能时使用。');

    render(<App />);
    await waitForBackendThreadHeading();
    await openSkillToolsPage();
    await screen.findByText('后端');

    fireEvent.click(screen.getByRole('button', { name: '批量导入技能目录' }));
    fireEvent.click(await screen.findByRole('button', { name: '私人使用' }));

    expect(await screen.findByTestId('skills-import-summary-panel')).toBeInTheDocument();
    expect(screen.getByText('ImportedSkill')).toBeInTheDocument();
    expect(screen.getByText('当你需要维护导入技能时使用。')).toBeInTheDocument();
    expect(backend.readSkill).toHaveBeenCalledWith({
      cwd: '/repo/app',
      path: '/imports/ImportedSkill/SKILL.md',
    });
    expect(backend.suggestSkillSummary).toHaveBeenCalledWith(expect.objectContaining({
      cwd: '/repo/app',
      name: 'ImportedSkill',
      scope: 'personal',
    }));

    fireEvent.click(screen.getByTestId('skills-import-summary-apply-0'));
    expect(await screen.findByRole('dialog', { name: '编辑技能' })).toBeInTheDocument();
    expect(screen.getByLabelText('技能简介')).toHaveValue('当你需要维护导入技能时使用。');
    expect(screen.getByTestId('skills-import-summary-item-0')).toHaveTextContent('已采用，保存后生效');
    fireEvent.click(screen.getByTestId('skills-import-summary-dismiss-0'));
    expect(screen.queryByTestId('skills-import-summary-panel')).not.toBeInTheDocument();
  });

  it('shows skill resolution conflicts and applies a previewed action', async () => {
    backend.listSkillResolutions.mockResolvedValueOnce({
      items: [{
        conflict_id: 'conflict-1',
        name: 'backend',
        kind: 'mirror_drift',
        scope: 'project',
        provider: 'codex',
        available_actions: ['view_diff', 'canonical_overwrite_mirror'],
      }],
    }).mockResolvedValue({ items: [] });
    backend.previewSkillResolution.mockResolvedValueOnce({
      items: [{
        provider: 'codex',
        preview_id: 'preview-1',
        preview_hash: 'hash-1',
        source_path: '/repo/app/.agent/skills/backend/SKILL.md',
        target_path: '/Users/test/.codex/skills/backend/SKILL.md',
        source_hash: '8b022cc49401abd24425d711fe24aed33d49ddb7dff41bbd2a6bc69e4909af22c',
        target_hash: '854b60866d3b76b7c95ccbc4ec856459624dc622d34971865412b47b56fa840d',
        diff: '--- source\n+++ target\n@@ -1 +1 @@\n-old\n+new',
      }],
    });

    render(<App />);
    await waitForBackendThreadHeading();
    await openSkillToolsPage();

    expect(await screen.findByText(/发现 1 个技能冲突/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '用本项目内容覆盖外部版本' }));
    expect(await screen.findByText('/Users/test/.codex/skills/backend/SKILL.md')).toBeInTheDocument();
    expect(screen.getByText('外部版本号：8b022cc4')).toBeInTheDocument();
    expect(screen.getByText('管理版本号：854b6086')).toBeInTheDocument();
    expect(screen.getByText(/--- source/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '确认应用' }));

    await waitFor(() => {
      expect(backend.previewSkillResolution).toHaveBeenCalledWith(expect.objectContaining({
        cwd: '/repo/app',
        conflict_id: 'conflict-1',
        action: 'canonical_overwrite_mirror',
      }));
      expect(backend.applySkillResolution).toHaveBeenCalledWith(expect.objectContaining({
        cwd: '/repo/app',
        conflict_id: 'conflict-1',
        action: 'canonical_overwrite_mirror',
        preview_id: 'preview-1',
        preview_hash: 'hash-1',
      }));
    });
  });

  it('shows legacy resolution guide and preview intro copy', async () => {
    backend.listSkillResolutions.mockResolvedValueOnce({
      items: [{
        conflict_id: 'personal-project',
        name: 'backend',
        kind: 'external_personal_project_same_name',
        scope: 'personal',
        provider: 'codex',
        available_actions: ['view_diff'],
      }],
    });

    render(<App />);
    await waitForBackendThreadHeading();
    await openSkillToolsPage();

    expect(await screen.findByText('检测到同名技能同时存在于私人使用和项目共享。请选择使用项目共享版本、继续私人使用，或另存为新私人技能。')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '查看两个版本' }));

    expect(await screen.findByText('下面只说明两个版本分别在哪里，不会修改文件。')).toBeInTheDocument();
    expect(screen.getByText('外部版本')).toBeInTheDocument();
    expect(screen.getByText('本项目版本')).toBeInTheDocument();
  });

  it('shows manual resolution steps when same-name conflicts cannot be auto resolved', async () => {
    backend.listSkillResolutions.mockResolvedValueOnce({
      items: [{
        conflict_id: 'same-manual',
        name: 'backend',
        kind: 'same_name',
        scope: 'project',
        available_actions: [],
      }],
    });

    render(<App />);
    await waitForBackendThreadHeading();
    await openSkillToolsPage();

    expect(await screen.findByText('要保留项目共享：编辑或删除同名私人技能。')).toBeInTheDocument();
    expect(screen.getByText('要保留私人使用：编辑项目共享技能改名，或删除项目共享技能。')).toBeInTheDocument();
    expect(screen.getByText('两边都要保留：把其中一个改成更明确的名字。')).toBeInTheDocument();
  });

  it('prompts for a new resolution skill name and sends provider source fields', async () => {
    backend.listSkillResolutions.mockResolvedValueOnce({
      items: [{
        conflict_id: 'conflict-new',
        name: 'backend',
        kind: 'unmanaged_provider_skill',
        scope: 'project',
        provider_entries: [{
          provider: 'codex',
          source_path_id: 'codex://backend',
          source_path: '/Users/test/.codex/skills/backend/SKILL.md',
        }],
        available_actions: ['save_as_new_skill'],
      }],
    });

    render(<App />);
    await waitForBackendThreadHeading();
    await openSkillToolsPage();

    expect(await screen.findByText(/发现 1 个技能冲突/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '另存为新技能' }));
    expect(await screen.findByLabelText('新技能名称')).toHaveValue('backend-copy');
    fireEvent.change(screen.getByLabelText('新技能名称'), { target: { value: 'backend-v2' } });
    fireEvent.click(screen.getByRole('button', { name: '生成预览' }));

    await waitFor(() => {
      expect(backend.previewSkillResolution).toHaveBeenCalledWith(expect.objectContaining({
        cwd: '/repo/app',
        conflict_id: 'conflict-new',
        action: 'save_as_new_skill',
        provider: 'codex',
        source_provider: 'codex',
        source_path_id: 'codex://backend',
        new_name: 'backend-v2',
      }));
    });
    expect(await screen.findByText('/Users/test/.codex/skills/backend/SKILL.md')).toBeInTheDocument();
  });

  it('auto-applies same-name keep-selected resolution with the selected source id', async () => {
    backend.listSkillResolutions.mockResolvedValueOnce({
      items: [{
        conflict_id: 'same-1',
        name: 'backend',
        kind: 'same_name',
        scope: 'project',
        available_actions: ['keep_selected'],
        sources: [
          {
            scope: 'project',
            canonical_id: 'project/backend',
            path: '/repo/app/.agent/skills/backend/SKILL.md',
          },
          {
            scope: 'personal',
            personal_type: 'user',
            canonical_id: 'personal/user/backend',
            path: '/Users/test/.super-dolphin/skills/personal/user/backend/SKILL.md',
          },
        ],
      }],
    }).mockResolvedValue({ items: [] });

    render(<App />);
    await waitForBackendThreadHeading();
    await openSkillToolsPage();

    expect(await screen.findByText(/发现 1 个技能冲突/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '用项目共享版本，删除其他版本' }));

    await waitFor(() => {
      expect(backend.previewSkillResolution).toHaveBeenCalledWith(expect.objectContaining({
        cwd: '/repo/app',
        conflict_id: 'same-1',
        action: 'keep_selected',
        keep_source_id: 'project/backend',
      }));
      expect(backend.applySkillResolution).toHaveBeenCalledWith(expect.objectContaining({
        cwd: '/repo/app',
        conflict_id: 'same-1',
        action: 'keep_selected',
        keep_source_id: 'project/backend',
        preview_id: 'preview-1',
        preview_hash: 'hash-1',
      }));
    });
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
