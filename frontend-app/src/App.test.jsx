import React from 'react';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import App from './App.jsx';
import { resetClientStoreForTests, useClientStore } from './entities/client/model/useClientStore.js';

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

const backend = vi.hoisted(() => ({
  readConfig: vi.fn(),
  getWindowBootstrap: vi.fn(),
  openNewWindow: vi.fn(),
  getProjects: vi.fn(),
  setActiveProject: vi.fn(),
  addProject: vi.fn(),
  removeProject: vi.fn(),
  getSidebarState: vi.fn(),
  getThreadState: vi.fn(),
  getThreadMessages: vi.fn(),
  getBuildInfo: vi.fn(),
  getDashboardPage: vi.fn(),
  getObservabilityStatus: vi.fn(),
  getObservabilityTrace: vi.fn(),
  getObservabilityThreadRecent: vi.fn(),
  listObservabilitySlow: vi.fn(),
  listObservabilityErrors: vi.fn(),
  listSharedFiles: vi.fn(),
  listPromptAssets: vi.fn(),
  getDashboardPrompts: vi.fn(),
  getPrompt: vi.fn(),
  writePrompt: vi.fn(),
  deletePrompt: vi.fn(),
  draftPromptIntent: vi.fn(),
  commitPromptIntent: vi.fn(),
  discardPromptIntent: vi.fn(),
  dryRunPromptIntent: vi.fn(),
  getMemorySnapshot: vi.fn(),
  getMemoryEntry: vi.fn(),
  upsertMemoryEntry: vi.fn(),
  deleteMemoryEntry: vi.fn(),
  setMemoryAutoDreamIntent: vi.fn(),
  mergeMemoryEntries: vi.fn(),
  ignoreMemorySimilarity: vi.fn(),
  consolidateMemorySimilarities: vi.fn(),
  startConsolidateMemorySimilarities: vi.fn(),
  getMemoryConsolidationStatus: vi.fn(),
  listDags: vi.fn(),
  getDagDetail: vi.fn(),
  getDagRuns: vi.fn(),
  getDagRun: vi.fn(),
  startDag: vi.fn(),
  terminateDagRun: vi.fn(),
  deleteDag: vi.fn(),
  applyDagOps: vi.fn(),
  deleteSkill: vi.fn(),
  readSkill: vi.fn(),
  listSkillFiles: vi.fn(),
  writeSkill: vi.fn(),
  importSkillDirectories: vi.fn(),
  suggestSkillSummary: vi.fn(),
  selectProjectDir: vi.fn(),
  selectProjectDirs: vi.fn(),
  listSkillResolutions: vi.fn(),
  previewSkillResolution: vi.fn(),
  applySkillResolution: vi.fn(),
  readSharedFile: vi.fn(),
  deleteSharedFile: vi.fn(),
  getPreference: vi.fn(),
  startThread: vi.fn(),
  startTurn: vi.fn(),
  interruptTurn: vi.fn(),
  compactThread: vi.fn(),
  recoverThread: vi.fn(),
  resolveThreadIdentity: vi.fn(),
  archiveThread: vi.fn(),
  unarchiveThread: vi.fn(),
  deleteThread: vi.fn(),
  getThreadConfig: vi.fn(),
  setThreadConfig: vi.fn(),
  renameThread: vi.fn(),
  setPreference: vi.fn(),
  selectFiles: vi.fn(),
  saveClipboardImage: vi.fn(),
  saveTextFile: vi.fn(),
  beginTextClipboardWrite: vi.fn(),
  copyTextToClipboard: vi.fn(),
  emitFrontendTraceEvent: vi.fn(),
  onFilesDropped: vi.fn(() => () => {}),
  onBridgeEvent: vi.fn((callback) => {
    bridgeCallback = callback;
    return () => {
      bridgeCallback = null;
    };
  }),
}));

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
    render: vi.fn(async (_id, source) => ({
      svg: `<svg role="img" aria-label="mock mermaid"><text>${source}</text></svg>`,
    })),
  },
}));

describe('frontend-app connected client shell', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    bridgeCallback = null;
    resetClientStoreForTests();
    window.localStorage.clear();
    window.history.replaceState({}, '', '/');
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1024 });
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
    const defaultSkills = [
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
    backend.getObservabilityStatus.mockResolvedValue({ enabled: true, schema_version: 1, index_trace_keys: 1, sink_events_written: 2, sink_write_errors: 0 });
    backend.getObservabilityTrace.mockResolvedValue({ source: 'memory', events: [], slowest_events: [], errors: [], total_duration_ms: 0, truncated: false });
    backend.getObservabilityThreadRecent.mockResolvedValue({ source: 'memory', events: [], slowest_events: [], errors: [], total_duration_ms: 0, truncated: false });
    backend.listObservabilitySlow.mockResolvedValue({ source: 'memory', events: [], slowest_events: [], errors: [], total_duration_ms: 0, truncated: false });
    backend.listObservabilityErrors.mockResolvedValue({ source: 'memory', events: [], slowest_events: [], errors: [], total_duration_ms: 0, truncated: false });
    backend.listSharedFiles.mockResolvedValue({
      files: [],
      finalOutputRefs: [],
      sharedFileRetention: { items: [], protectedCount: 0, cleanupCandidateCount: 0 },
    });
    backend.listPromptAssets.mockResolvedValue({ prompts: [] });
    backend.getDashboardPrompts.mockResolvedValue({ prompts: [] });
    backend.getPrompt.mockResolvedValue({ prompt: { content: '' } });
    backend.writePrompt.mockResolvedValue({ prompt: { id: 'saved-prompt' } });
    backend.deletePrompt.mockResolvedValue({ deleted: true });
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
    backend.listDags.mockResolvedValue({ dags: [] });
    backend.getDagDetail.mockResolvedValue({ dag: null, nodes: [] });
    backend.getDagRuns.mockResolvedValue({ runs: [] });
    backend.getDagRun.mockResolvedValue({ run: null, nodes: [] });
    backend.startDag.mockResolvedValue({ runKey: 'run-started' });
    backend.terminateDagRun.mockResolvedValue({ ok: true });
    backend.deleteDag.mockResolvedValue({ ok: true });
    backend.applyDagOps.mockResolvedValue({ newVersion: 2 });
    backend.deleteSkill.mockResolvedValue({ ok: true });
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
    backend.readSharedFile.mockImplementation(({ path }) => Promise.resolve({
      path,
      content: `content for ${path}`,
      updatedBy: 'agent',
      updatedAt: '2026-05-30T07:00:00Z',
    }));
    backend.deleteSharedFile.mockResolvedValue({ deleted: true });
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
    backend.onFilesDropped.mockReturnValue(() => {});
    backend.saveTextFile.mockResolvedValue('/exports/file.md');
    backend.beginTextClipboardWrite.mockReturnValue(null);
    backend.copyTextToClipboard.mockResolvedValue(true);
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
  });

  it('renders the product titlebar without macOS controls and defaults to dark theme', async () => {
    render(<App />);

    const shell = await screen.findByTestId('frontend-app');
    expect(shell).toHaveAttribute('data-theme', 'dark');
    expect(document.querySelector('.traffic-lights')).not.toBeInTheDocument();
    expect(screen.getByText('Super Agent')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '切换到白天模式' })).toBeInTheDocument();
  });

  it('toggles the local color theme without calling backend preferences', async () => {
    render(<App />);

    const shell = await screen.findByTestId('frontend-app');
    const preferenceCallsBeforeToggle = backend.setPreference.mock.calls.length;

    fireEvent.click(screen.getByRole('button', { name: '切换到白天模式' }));
    expect(shell).toHaveAttribute('data-theme', 'light');
    expect(window.localStorage.getItem('super-dolphin-theme')).toBe('light');
    expect(screen.getByRole('button', { name: '切换到黑夜模式' })).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '切换到黑夜模式' }));
    expect(shell).toHaveAttribute('data-theme', 'dark');
    expect(window.localStorage.getItem('super-dolphin-theme')).toBe('dark');
    expect(screen.getByRole('button', { name: '切换到白天模式' })).toBeInTheDocument();
    expect(backend.setPreference.mock.calls.length).toBe(preferenceCallsBeforeToggle);
  });

  it('opens observability tracing dashboard and queries by trace id', async () => {
    backend.getObservabilityTrace.mockResolvedValue({
      source: 'mixed',
      total_duration_ms: 135,
      truncated: false,
      slowest_events: [],
      errors: [],
      events: [{
        trace_id: 'trace-1',
        span_id: 'span-rpc',
        method: 'rpc.dispatch',
        status: 'slow',
        duration_ms: 120,
        thread_id: 'thread-1',
        code: { file: 'internal/platform/rpc/server.go', function: '(*Server).Dispatch', line: 270 },
        stack: [{ file: 'internal/platform/rpc/server.go', function: '(*Server).Dispatch', line: 270 }],
      }],
    });
    render(<App />);

    fireEvent.click(await screen.findByRole('button', { name: '链路追踪' }));
    fireEvent.change(screen.getByLabelText('Trace ID'), { target: { value: 'trace-1' } });
    fireEvent.click(screen.getByRole('button', { name: '查询 Trace' }));

    expect(await screen.findByText(/source=mixed/)).toBeInTheDocument();
    expect(screen.getAllByText(/internal\/platform\/rpc\/server.go:270/).length).toBeGreaterThan(0);
    expect(backend.getObservabilityTrace).toHaveBeenCalledWith({ traceId: 'trace-1', limit: 50 });
  });

  it('bootstraps project, sidebar, timeline and token usage from backend', async () => {
    render(<App />);

    expect(await screen.findByText('后端线程')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '选择项目' })).toHaveTextContent(/^app$/);
    expect(screen.getByLabelText('当前工作目录')).toHaveAttribute('title', '当前窗口 CWD：/repo/app');
    expect(screen.getByText(/128 \/ 1024 tokens/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));
    expect(within(screen.getByTestId('runtime-panel')).getByRole('button', { name: 'file' })).toBeInTheDocument();
    expect(screen.queryByText(/diff --git a\/file b\/file/)).not.toBeInTheDocument();
    expect(backend.getProjects).toHaveBeenCalledWith({ cwd: '/repo/app' });
    expect(backend.getThreadState).toHaveBeenCalledWith({
      cwd: '/repo/app',
      threadId: 'thread-1',
      includeDiff: true,
    });
  });

  it('uses the current URL path as the active page on boot', async () => {
    window.history.pushState({}, '', '/dags');
    backend.getWindowBootstrap.mockResolvedValueOnce({ snapshot: { page: 'chat' } });

    render(<App />);

    const workflowButton = await screen.findByRole('button', { name: '自动化' });
    await waitFor(() => expect(workflowButton).toHaveClass('active'));
    expect(screen.getByText('当前页面：自动化')).toBeInTheDocument();
    expect(window.location.pathname).toBe('/dags');
  });

  it('lets user navigation override the explicit boot URL after initial route sync', async () => {
    window.history.pushState({}, '', '/dags');

    render(<App skipBootstrap />);

    const workflowButton = await screen.findByRole('button', { name: '自动化' });
    await waitFor(() => expect(workflowButton).toHaveClass('active'));

    fireEvent.click(screen.getByRole('button', { name: '技能' }));

    await waitFor(() => expect(screen.getByRole('button', { name: '技能' })).toHaveClass('active'));
    expect(screen.getByText('当前页面：技能')).toBeInTheDocument();
    expect(window.location.pathname).toBe('/skills');
  });

  it('writes page navigation to browser history and restores it on popstate', async () => {
    render(<App skipBootstrap />);

    fireEvent.click(screen.getByRole('button', { name: '技能' }));
    await waitFor(() => expect(window.location.pathname).toBe('/skills'));

    fireEvent.click(screen.getByRole('button', { name: '设置' }));
    await waitFor(() => expect(window.location.pathname).toBe('/settings'));

    await act(async () => {
      window.history.pushState({ activePage: 'skills' }, '', '/skills');
      window.dispatchEvent(new PopStateEvent('popstate', { state: { activePage: 'skills' } }));
    });

    await waitFor(() => expect(screen.getByRole('button', { name: '技能' })).toHaveClass('active'));
    expect(screen.getByText('当前页面：技能')).toBeInTheDocument();
  });

  it('hides idle status noise while keeping the provider badge in thread cards', async () => {
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: '静默会话', provider: 'codex', status: 'idle' }],
    });

    render(<App />);

    const card = (await screen.findByText('静默会话')).closest('.thread-card');
    expect(card).toHaveTextContent('codex');
    expect(card).not.toHaveTextContent('idle');
    expect(card.querySelector('em')).toBeNull();
  });

  it('shows a bootstrap failure notice when the backend bridge is unavailable', async () => {
    backend.readConfig.mockRejectedValue(new Error('runtime shim: failed to connect ws://127.0.0.1:5175/wails/ws'));

    render(<App />);

    expect(await screen.findByText('连接后端失败：runtime shim: failed to connect ws://127.0.0.1:5175/wails/ws')).toBeInTheDocument();
  });

  it('disables provider switching when no project cwd is available', () => {
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
    expect(screen.getByRole('combobox', { name: '发送权限' })).toBeDisabled();
    expect(screen.getByRole('button', { name: '选择模型' })).toBeDisabled();

    fireEvent.click(sendButton);
    fireEvent.click(screen.getByRole('button', { name: '添加文件' }));
    fireEvent.keyDown(screen.getByTestId('composer-input'), { key: 'Enter', code: 'Enter', charCode: 13 });

    expect(backend.startThread).not.toHaveBeenCalled();
    expect(backend.startTurn).not.toHaveBeenCalled();
    expect(backend.selectFiles).not.toHaveBeenCalled();
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
    expect(screen.getAllByRole('checkbox')).toHaveLength(2);
    expect(screen.getAllByRole('checkbox')[0]).toBeChecked();
    expect(screen.getAllByRole('checkbox')[1]).not.toBeChecked();
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
    await screen.findByLabelText('mock mermaid');
    expect(container.querySelector('.mermaid-diagram')).toHaveTextContent('flowchart TD');
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
    expect(within(dialog).getByLabelText('mock mermaid')).toBeInTheDocument();
    expect(within(dialog).getByRole('link', { name: '外部打开' })).toHaveAttribute(
      'href',
      expect.stringContaining('data:image/svg+xml;charset=utf-8,'),
    );
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
    expect(within(dialog).getByRole('img', { name: 'ig_lightbox.png' })).toHaveAttribute('src', routedSrc);
    expect(within(dialog).getByRole('link', { name: '外部打开' })).toHaveAttribute('href', routedSrc);
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
    await screen.findByText('后端线程');

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
    await screen.findByText('后端线程');

    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));

    const diffView = screen.getByTestId('diff-view');
    const fileGroups = diffView.querySelectorAll('.diff-file-group');
    expect(fileGroups).toHaveLength(3);
    expect(diffView).not.toHaveTextContent('diff --git');

    const firstFile = fileGroups[0];
    expect(within(firstFile).getByRole('button', { name: /src\/a\.js/ })).toHaveTextContent('+2');
    expect(within(firstFile).getByRole('button', { name: /src\/a\.js/ })).toHaveTextContent('-1');
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

  it('renders the work status from the backend turn state machine', async () => {
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: '后端线程', provider: 'codex', status: 'preparing' }],
      tokenUsageByThread: {
        'thread-1': { usedTokens: 128, contextWindowTokens: 1024, usedPercent: 12.5 },
      },
    });

    render(<App />);

    expect(await screen.findByText('准备中')).toBeInTheDocument();

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

    expect(await screen.findByText('强制完成中')).toBeInTheDocument();
  });

  it('sanitizes corrupted work status text and keeps the token chip complete', async () => {
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
    await screen.findByText('后端线程');

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

    await waitFor(() => {
      const status = container.querySelector('.work-status');
      expect(status).not.toHaveTextContent('�');
      expect(status).toHaveTextContent('持被跳过，但写入成功 临时文件清理 输出 `scratch_removed`');
      expect(status.querySelector('code')).toHaveTextContent('21017 / 258400 tokens');
      expect(status.querySelector('code')).toHaveAttribute('title', '21017 / 258400 tokens');
    });
  });

  it('does not expose internal thread identifiers in visible chat status', async () => {
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

    await screen.findByText('当前会话已连接');
    expect(container).not.toHaveTextContent(internalId);
    expect(screen.getByText('新对话')).toBeInTheDocument();
  });

  it('shows a lightweight history placeholder when the active thread has no trusted cache', async () => {
    const { container } = render(<App />);
    await screen.findByText('后端线程');

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
      const status = container.querySelector('.work-status');
      expect(status).toHaveTextContent('加载中');
      expect(status).toHaveClass('is-busy');
      expect(status.querySelector('.spinner')).toHaveAttribute('aria-hidden', 'true');
    });
  });

  it('keeps the existing timeline visible while the active thread state is refreshing', async () => {
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
    const status = container.querySelector('.work-status');
    expect(status).not.toHaveTextContent('加载中');
    expect(status).not.toHaveClass('is-busy');
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

    expect(await screen.findByLabelText('AI 思考记录')).toHaveTextContent('已处理 6m 5s');
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
    expect(trace).toHaveTextContent('已处理 3s');
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
    expect(trace).toHaveTextContent('已处理 2s');
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
    expect(trace).toHaveTextContent('已处理 3s');
    expect(trace).toHaveTextContent('file.open');
    const step = within(trace).getByLabelText('工具步骤');
    expect(step).toHaveTextContent('工具');
    expect(step).toHaveTextContent('完成');
    expect(step).toHaveTextContent('读取 frontend-app/src/App.jsx');
    expect(screen.getByText('工具结果已整理。')).toBeInTheDocument();
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

      const trace = screen.getByLabelText('AI 思考记录');
      expect(trace).toHaveTextContent('正在思考 0s');

      await act(async () => {
        await vi.advanceTimersByTimeAsync(2100);
      });

      expect(trace).toHaveTextContent('正在思考 2s');
    } finally {
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
    await screen.findByText('后端线程');
    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));

    const toolStat = screen.getByLabelText('工具调用总数');
    fireEvent.mouseEnter(toolStat);

    const tooltip = await screen.findByTestId('runtime-stat-tooltip');
    expect(tooltip).toHaveTextContent('deeply_nested_tool_name_with_many_segments');
    expect(tooltip.querySelector('.runtime-stat-tooltip-row')).toBeInTheDocument();
    expect(tooltip.querySelector('.runtime-stat-tooltip-name')).toHaveAttribute('title', 'deeply_nested_tool_name_with_many_segments');
    expect(container.querySelector('.runtime-panel')).toHaveClass('runtime-panel');
  });

  it('sets the chat composer textarea to three visible rows', async () => {
    render(<App />);
    await screen.findByText('后端线程');

    const composer = screen.getByTestId('composer-input');
    expect(composer).toHaveAttribute('rows', '3');
  });

  it('does not render a duplicate desktop titlebar inside the app shell', async () => {
    const { container } = render(<App />);

    expect(await screen.findByText('后端线程')).toBeInTheDocument();
    expect(container.querySelector('.traffic-lights')).toBeNull();
    expect(container.querySelectorAll('.titlebar').length).toBe(1);
  });

  it('keeps the user message visible and calls thread/start before turn/start for a new chat', async () => {
    backend.getSidebarState.mockResolvedValue({ activeThreadId: '', threads: [] });
    backend.getThreadState.mockResolvedValue({ timelinesByThread: {} });
    backend.startThread.mockResolvedValue({ thread: { id: 'thread-new' } });
    backend.startTurn.mockResolvedValue({ ok: true });

    render(<App />);

    await screen.findByText('我们应该在 app 中构建什么？');
    expect(screen.queryByTestId('composer-project')).not.toBeInTheDocument();
    expect(screen.getByLabelText('发送权限')).toBeInTheDocument();
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

    expect(screen.getAllByText('请真正调用后端聊天').length).toBeGreaterThanOrEqual(1);
  });

  it('sends the composer draft when plain Enter is pressed inside the textarea', async () => {
    backend.startTurn.mockResolvedValue({ ok: true });
    render(<App />);

    await screen.findByText('后端线程');
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

    await screen.findByText('后端线程');
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

    await screen.findByText('我们应该在 app 中构建什么？');
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
    expect(container.querySelector('.work-status')).toBeInTheDocument();
  });

  it('starts with only the chat rail and conversation, then toggles the right sidebar from the toolbar', async () => {
    const { container } = render(<App />);

    await screen.findByText('后端线程');
    const layout = screen.getByTestId('chat-layout');

    expect(screen.queryByTestId('runtime-panel')).not.toBeInTheDocument();
    expect(screen.queryByTestId('right-panel-resizer')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: '显示侧边栏' })).toBeInTheDocument();
    expect(layout).toHaveStyle({ gridTemplateColumns: '240px 6px minmax(0, 1fr)' });

    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));

    expect(screen.getByTestId('runtime-panel')).toBeInTheDocument();
    expect(screen.getByTestId('right-panel-resizer')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '隐藏侧边栏' })).toBeInTheDocument();
    expect(within(container.querySelector('.runtime-panel')).getByRole('button', { name: 'file' })).toBeInTheDocument();
    expect(container.querySelector('.runtime-panel')).not.toHaveTextContent('diff --git a/file b/file');
    expect(layout).toHaveStyle({ gridTemplateColumns: '240px 6px minmax(0, 1fr) 6px 189px' });
  });

  it('supports keyboard resizing for chat and activity separators', async () => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1400 });
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: 640 });

    render(<App />);

    await screen.findByText('后端线程');
    const layout = screen.getByTestId('chat-layout');
    const leftResizer = screen.getByRole('separator', { name: '调整会话栏宽度' });

    expect(leftResizer).toHaveAttribute('aria-valuenow', '264');

    fireEvent.keyDown(leftResizer, { key: 'ArrowLeft' });

    expect(leftResizer).toHaveAttribute('aria-valuenow', '248');
    expect(layout).toHaveStyle({ gridTemplateColumns: '248px 6px minmax(0, 1fr)' });

    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));
    const rightResizer = screen.getByRole('separator', { name: '调整侧边栏宽度' });

    expect(rightResizer).toHaveAttribute('aria-valuenow', '264');

    fireEvent.keyDown(rightResizer, { key: 'ArrowLeft' });

    expect(rightResizer).toHaveAttribute('aria-valuenow', '280');
    expect(layout).toHaveStyle({ gridTemplateColumns: '248px 6px minmax(0, 1fr) 6px 280px' });

    const activityResizer = screen.getByRole('separator', { name: '调整工具使用面板高度' });

    expect(activityResizer).toHaveAttribute('aria-valuenow', '160');

    fireEvent.keyDown(activityResizer, { key: 'ArrowUp' });

    expect(activityResizer).toHaveAttribute('aria-valuenow', '176');
    expect(screen.getByTestId('runtime-panel')).toHaveStyle({ '--activity-panel-height': '176px' });
  });

  it('opens the right sidebar at one fifth on wide screens', async () => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1980 });

    render(<App />);
    await screen.findByText('后端线程');

    const layout = screen.getByTestId('chat-layout');

    expect(layout).toHaveStyle({ gridTemplateColumns: '380px 6px minmax(0, 1fr)' });

    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));

    expect(layout).toHaveStyle({ gridTemplateColumns: '380px 6px minmax(0, 1fr) 6px 380px' });
  });

  it('keeps default chat columns proportional when the window is resized', async () => {
    render(<App />);
    await screen.findByText('后端线程');

    const layout = screen.getByTestId('chat-layout');

    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));
    expect(layout).toHaveStyle({ gridTemplateColumns: '240px 6px minmax(0, 1fr) 6px 189px' });

    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1980 });
    act(() => {
      window.dispatchEvent(new Event('resize'));
    });

    await waitFor(() => {
      expect(layout).toHaveStyle({ gridTemplateColumns: '380px 6px minmax(0, 1fr) 6px 380px' });
    });
  });

  it('lets the right sidebar grow toward two fifths while preserving two fifths for conversation', async () => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1980 });

    render(<App />);
    await screen.findByText('后端线程');

    const layout = screen.getByTestId('chat-layout');

    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));
    const rightResizer = screen.getByTestId('right-panel-resizer');

    dispatchPointer(rightResizer, 'pointerdown', 1100);
    dispatchPointer(window, 'pointermove', 500);
    dispatchPointer(window, 'pointerup', 500);

    await waitFor(() => {
      expect(layout).toHaveStyle({ gridTemplateColumns: '380px 6px minmax(0, 1fr) 6px 751px' });
    });
  });

  it('keeps right sidebar drag updates local until the pointer is released', async () => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1980 });

    render(<App />);
    await screen.findByText('后端线程');

    const layout = screen.getByTestId('chat-layout');

    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));
    const rightResizer = screen.getByTestId('right-panel-resizer');

    expect(useClientStore.getState().rightPanelWidth).toBe(380);

    dispatchPointer(rightResizer, 'pointerdown', 1100);
    dispatchPointer(window, 'pointermove', 700);

    expect(layout).toHaveStyle({ gridTemplateColumns: '380px 6px minmax(0, 1fr) 6px 751px' });
    expect(useClientStore.getState().rightPanelWidth).toBe(380);

    dispatchPointer(window, 'pointerup', 700);

    await waitFor(() => {
      expect(useClientStore.getState().rightPanelWidth).toBe(751);
    });
  });

  it('stops right sidebar resizing when the pointer is no longer pressed', async () => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1980 });

    render(<App />);
    await screen.findByText('后端线程');

    const layout = screen.getByTestId('chat-layout');

    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));
    const rightResizer = screen.getByTestId('right-panel-resizer');

    dispatchPointer(rightResizer, 'pointerdown', 1100);
    dispatchPointer(window, 'pointermove', 1000);
    expect(layout).toHaveStyle({ gridTemplateColumns: '380px 6px minmax(0, 1fr) 6px 480px' });

    dispatchPointer(window, 'pointermove', 700, { buttons: 0 });
    expect(layout).toHaveStyle({ gridTemplateColumns: '380px 6px minmax(0, 1fr) 6px 480px' });
    expect(useClientStore.getState().rightPanelWidth).toBe(480);

    dispatchPointer(window, 'pointermove', 500, { buttons: 0 });
    expect(layout).toHaveStyle({ gridTemplateColumns: '380px 6px minmax(0, 1fr) 6px 480px' });
  });

  it('keeps the right sidebar draggable past the previous early close width', async () => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1980 });

    render(<App />);
    await screen.findByText('后端线程');

    const layout = screen.getByTestId('chat-layout');

    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));
    const rightResizer = screen.getByTestId('right-panel-resizer');

    dispatchPointer(rightResizer, 'pointerdown', 1100);
    dispatchPointer(window, 'pointermove', 1330);

    expect(screen.getByTestId('runtime-panel')).toBeInTheDocument();
    expect(screen.getByTestId('right-panel-resizer')).toBeInTheDocument();
    expect(layout).toHaveStyle({ gridTemplateColumns: '380px 6px minmax(0, 1fr) 6px 150px' });

    dispatchPointer(window, 'pointerup', 1330);

    await waitFor(() => {
      expect(useClientStore.getState().rightPanelWidth).toBe(150);
    });
  });

  it('closes the right sidebar when dragged flush to the right edge', async () => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1980 });

    render(<App />);
    await screen.findByText('后端线程');

    const layout = screen.getByTestId('chat-layout');

    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));
    const rightResizer = screen.getByTestId('right-panel-resizer');

    dispatchPointer(rightResizer, 'pointerdown', 1100);
    dispatchPointer(window, 'pointermove', 1480);

    await waitFor(() => {
      expect(screen.queryByTestId('runtime-panel')).not.toBeInTheDocument();
      expect(screen.queryByTestId('right-panel-resizer')).not.toBeInTheDocument();
      expect(layout).toHaveStyle({ gridTemplateColumns: '380px 6px minmax(0, 1fr)' });
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
    await screen.findByText('Agent A');

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

    expect(within(screen.getByTestId('runtime-panel')).getByRole('button', { name: 'a' })).toBeInTheDocument();
    expect(screen.getByTestId('runtime-panel')).not.toHaveTextContent('diff --git a/a b/a');
    expect(screen.getByTestId('runtime-panel')).not.toHaveTextContent('diff --git a/b b/b');
    expect(screen.getByLabelText('LSP (8 tools) 调用次数')).toHaveTextContent('1');
    expect(screen.getByTestId('warning-log-panel')).toHaveTextContent('thread.send/failed');
    expect(screen.getByTestId('warning-log-panel')).toHaveTextContent('api.rpc.failed');
    expect(screen.getByTestId('warning-log-panel')).not.toHaveTextContent('bridge.call/failed');

    fireEvent.click(screen.getByRole('button', { name: /Agent B/ }));

    await waitFor(() => {
      expect(within(screen.getByTestId('runtime-panel')).getByRole('button', { name: 'b' })).toBeInTheDocument();
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

    fireEvent.click(screen.getByRole('button', { name: /Agent B/ }));

    await waitFor(() => expect(backend.getThreadState).toHaveBeenCalledWith({
      cwd: '/repo/app',
      threadId: 'thread-b',
      includeDiff: false,
    }));
    expect(useClientStore.getState().activeThreadId).toBe('thread-b');
    expect(useClientStore.getState().pendingActiveThreadId).toBe('');
    expect(useClientStore.getState().threadStateLoadingByThread['thread-b']).toBe(true);
    expect(screen.getByRole('button', { name: /Agent B/ }).closest('.thread-card')).toHaveClass('active');
    expect(screen.queryByText('Agent A ready')).not.toBeInTheDocument();
    expect(screen.queryByText('stale cached Agent B content')).not.toBeInTheDocument();
    expect(screen.queryByText(/我们应该在/)).not.toBeInTheDocument();
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

    fireEvent.click(screen.getByRole('button', { name: /Agent B/ }));

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
    await screen.findByText('后端线程');

    const layout = screen.getByTestId('chat-layout');
    const leftResizer = screen.getByTestId('thread-rail-resizer');

    dispatchPointer(leftResizer, 'pointerdown', 280);
    dispatchPointer(window, 'pointermove', 40);
    dispatchPointer(window, 'pointerup', 40);

    expect(layout).toHaveStyle({ gridTemplateColumns: '240px 6px minmax(0, 1fr)' });

    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));
    const rightResizer = screen.getByTestId('right-panel-resizer');

    dispatchPointer(rightResizer, 'pointerdown', 1100);
    dispatchPointer(window, 'pointermove', 1300);
    dispatchPointer(window, 'pointerup', 1300);

    await waitFor(() => {
      expect(screen.queryByTestId('runtime-panel')).not.toBeInTheDocument();
      expect(layout).toHaveStyle({ gridTemplateColumns: '240px 6px minmax(0, 1fr)' });
    });
  });

  it('uses backend activity stats for the resizable tool usage panel', async () => {
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: 640 });

    render(<App />);
    await screen.findByText('后端线程');

    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));

    expect(screen.getByTestId('runtime-panel')).toHaveStyle({
      '--activity-panel-height': '160px',
      '--activity-panel-min-height': '96px',
      '--activity-panel-max-height': '286px',
      '--diff-panel-min-height': '286px',
      '--diff-panel-max-height': '477px',
    });
    expect(screen.getByLabelText('LSP (8 tools) 调用次数')).toHaveTextContent('3');
    expect(screen.getByLabelText('LSP (8 tools) 调用次数')).toHaveAttribute('title', 'LSP (8 tools): 3');
    expect(screen.getByLabelText('工具调用总数')).toHaveTextContent('6');
    expect(screen.queryByText('edit:')).not.toBeInTheDocument();

    fireEvent.mouseEnter(screen.getByLabelText('LSP (8 tools) 调用次数'));
    expect(screen.getByTestId('runtime-stat-tooltip')).toHaveTextContent('LSP (8 tools)');
    expect(screen.getByTestId('runtime-stat-tooltip')).toHaveTextContent('edit');
    expect(screen.getByTestId('runtime-stat-tooltip')).toHaveTextContent('3');
    fireEvent.mouseLeave(screen.getByLabelText('LSP (8 tools) 调用次数'));
    expect(screen.queryByTestId('runtime-stat-tooltip')).not.toBeInTheDocument();

    fireEvent.mouseDown(screen.getByTestId('activity-panel-resizer'), { clientY: 500 });
    fireEvent.mouseMove(window, { clientY: 0 });
    fireEvent.mouseUp(window);

    await waitFor(() => {
      expect(screen.getByTestId('runtime-panel')).toHaveStyle({ '--activity-panel-height': '286px' });
    });
  });

  it('shows tool return entries alongside warning lines in the runtime panel', async () => {
    render(<App />);
    await screen.findByText('后端线程');

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

    const logPanel = screen.getByTestId('warning-log-panel');
    expect(logPanel).toHaveTextContent('api.rpc.failed');
    expect(logPanel).toHaveTextContent('grep');
    expect(logPanel).toHaveTextContent('返回');
    expect(logPanel).not.toHaveTextContent('{"total":3}');

    const resultLine = within(logPanel).getByText(/grep/).closest('p');
    fireEvent.mouseEnter(resultLine);

    expect(screen.getByTestId('warning-log-popover')).toHaveTextContent('src/App.jsx: runtime log result');
    expect(screen.getByTestId('warning-log-popover')).toHaveTextContent('"preview": "{\\"total\\":3}"');
  });

  it('clamps right-edge runtime hover details into the viewport', async () => {
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: 640 });

    render(<App />);
    await screen.findByText('后端线程');

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

    fireEvent.mouseEnter(toolStat);

    const tooltip = screen.getByTestId('runtime-stat-tooltip');
    expect(tooltip).toHaveTextContent('工具');
    expect(tooltip.style.getPropertyValue('--runtime-stat-tooltip-left')).toBe('652px');
    expect(tooltip.style.getPropertyValue('--runtime-stat-tooltip-bottom')).toBe('70px');
  });

  it('lets bottom-right runtime hover details use the available vertical space', async () => {
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
    await screen.findByText('后端线程');

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

    fireEvent.mouseEnter(toolStat);

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

    await screen.findByText('我们应该在 app 中构建什么？');
    expect(screen.queryByLabelText('复制当前线程')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('停止')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('线程状态')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('压缩当前线程')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('选择附件')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('权限')).not.toBeInTheDocument();
    expect(screen.getByLabelText('请先选择会话')).toBeDisabled();
    expect(screen.getByLabelText('添加文件')).toBeInTheDocument();
    expect(screen.getByLabelText('发送权限')).toBeInTheDocument();
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

    await screen.findByText('我们应该在 app 中构建什么？');
    expect(screen.queryByLabelText('复制当前线程')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('线程状态')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('停止')).not.toBeInTheDocument();
    expect(screen.getByLabelText('请先选择会话')).toBeDisabled();
    expect(screen.queryByText('作文Agent-15')).not.toBeInTheDocument();
    expect(backend.getThreadState).not.toHaveBeenCalledWith(expect.objectContaining({ threadId: 'essay_agent_15' }));
  });

  it('connects attachments and conversation operation buttons', async () => {
    backend.selectFiles.mockResolvedValue(['/tmp/a.txt']);
    backend.resolveThreadIdentity.mockResolvedValue({ id: 'thread-1', providerThreadId: 'provider-thread-1', agent_id: 'agent-1' });

    render(<App />);
    await screen.findByText('后端线程');

    fireEvent.click(screen.getByLabelText('添加文件'));
    expect(await screen.findByText('a.txt')).toBeInTheDocument();

    fireEvent.click(screen.getByLabelText('复制当前线程'));
    fireEvent.click(screen.getByLabelText('停止'));
    fireEvent.click(screen.getByLabelText('进程恢复'));
    fireEvent.click(screen.getByLabelText('归档会话'));

    await waitFor(() => {
      expect(backend.selectFiles).toHaveBeenCalled();
      expect(JSON.parse(backend.copyTextToClipboard.mock.calls[0][0])).toEqual(expect.objectContaining({
        agentId: 'agent-1',
        providerThreadId: 'provider-thread-1',
        provider: 'codex',
      }));
      expect(backend.interruptTurn).toHaveBeenCalledWith({ cwd: '/repo/app', threadId: 'thread-1', turnId: 'turn-1', source: 'ui_stop' });
      expect(backend.recoverThread).toHaveBeenCalledWith({ cwd: '/repo/app', threadId: 'thread-1' });
      expect(backend.archiveThread).toHaveBeenCalledWith({ threadId: 'thread-1' });
      expect(backend.setPreference).toHaveBeenCalledWith(expect.objectContaining({
        cwd: '/repo/app',
        key: 'archivedThreadAtById.thread-1',
      }));
    });
  });

  it('interrupts the selected conversation when Escape is pressed', async () => {
    render(<App />);
    await screen.findByText('后端线程');

    fireEvent.keyDown(window, { key: 'Escape', code: 'Escape' });

    await waitFor(() => {
      expect(backend.interruptTurn).toHaveBeenCalledWith({ cwd: '/repo/app', threadId: 'thread-1', turnId: 'turn-1', source: 'ui_stop' });
    });
  });

  it('does not interrupt the selected conversation when Escape is handled by the composer', async () => {
    render(<App />);
    await screen.findByText('后端线程');

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
    await screen.findByText('后端线程');

    fireEvent.keyDown(window, { key: 'Escape', code: 'Escape' });

    await waitFor(() => expect(screen.getByTestId('chat-action-feedback')).toHaveTextContent('当前没有可中断任务'));
    expect(backend.interruptTurn).not.toHaveBeenCalled();
  });

  it('previews attachments on click and removes them only with the remove control', async () => {
    backend.selectFiles.mockResolvedValue(['/tmp/a.txt']);

    render(<App />);
    await screen.findByText('后端线程');

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
    await screen.findByText('后端线程');

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
    await screen.findByText('后端线程');

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
    expect(backend.saveClipboardImage).toHaveBeenCalledWith(expect.any(String));
  });

  it('accepts native Wails file drops on the text editor target', async () => {
    let nativeDropHandler = null;
    backend.onFilesDropped.mockImplementation((handler) => {
      nativeDropHandler = handler;
      return () => {};
    });

    render(<App />);
    await screen.findByText('后端线程');

    const composer = screen.getByTestId('composer-dock');
    const input = screen.getByTestId('composer-input');
    expect(composer).toHaveAttribute('data-file-drop-target');
    expect(input).toHaveAttribute('id', 'composer-input');
    expect(input).toHaveAttribute('data-file-drop-target');

    act(() => {
      nativeDropHandler({
        files: ['/tmp/native-editor-drop.txt'],
        details: { id: 'composer-input' },
      });
    });

    expect(await screen.findByRole('button', { name: /预览附件 native-editor-drop\.txt/ })).toBeInTheDocument();
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
    await screen.findByText('后端线程');

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
    await screen.findByText('后端线程');

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
    await screen.findByText('后端线程');

    expect(screen.queryByLabelText('线程状态')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('压缩当前线程')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('选择附件')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('权限')).not.toBeInTheDocument();
    expect(screen.getByLabelText('添加文件')).toBeInTheDocument();
    expect(screen.getByLabelText('发送权限')).toBeInTheDocument();

    expect(screen.queryByLabelText('切换 Claude / Codex provider')).not.toBeInTheDocument();
    expect(screen.queryByText('Codex')).not.toBeInTheDocument();
  });

  it('keeps provider switching available before a backend chat exists', async () => {
    backend.getSidebarState.mockResolvedValue({ activeThreadId: '', threads: [] });
    backend.getThreadState.mockResolvedValue({ timelinesByThread: {} });

    render(<App />);
    await screen.findByText('我们应该在 app 中构建什么？');

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
      'settings.provider.active': 'claude',
      'settings.provider.claude.model': 'sonnet',
      'settings.provider.claude.effort': 'high',
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
      expect(screen.getByRole('button', { name: '选择模型' })).toHaveTextContent('GPT-5.4 · 中');
    });
    expect(screen.queryByLabelText('切换 Claude / Codex provider')).not.toBeInTheDocument();
  });

  it('uses sidebar runtime metadata for provider-less thread cards', async () => {
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
    backend.setActiveProject.mockResolvedValue({ projects: ['/repo/app', '/repo/other'], active: '/repo/other' });
    backend.addProject.mockResolvedValue({ projects: ['/repo/app', '/repo/other', '/repo/new'], active: '/repo/other' });
    backend.removeProject.mockResolvedValue({ projects: ['/repo/app', '/repo/other'], active: '/repo/other' });

    render(<App />);
    await screen.findByText('后端线程');

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
      expect(screen.getByRole('button', { name: '选择项目' })).toHaveTextContent(/^other$/);
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
    await screen.findByText('后端线程');

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
    await screen.findByText('后端线程');

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
    await screen.findByText('后端线程');

    fireEvent.click(screen.getByRole('button', { name: '选择项目' }));
    fireEvent.click(screen.getByRole('menuitem', { name: 'repo/other' }));

    await waitFor(() => {
      expect(backend.getSidebarState).toHaveBeenCalledWith({ cwd: '/repo/other' });
      expect(screen.getByText('Other project chat')).toBeInTheDocument();
      expect(screen.queryByText('后端线程')).not.toBeInTheDocument();
    });
    expect(useClientStore.getState().activeThreadId).toBe('');
    expect(screen.getByRole('button', { name: /Other project chat/ }).closest('.thread-card')).not.toHaveClass('active');
    expect(backend.getThreadState).not.toHaveBeenCalledWith({
      cwd: '/repo/other',
      threadId: 'thread-other',
      includeDiff: true,
    });

    fireEvent.click(screen.getByRole('button', { name: /Other project chat/ }));

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
    await screen.findByText('后端线程');

    fireEvent.click(screen.getByRole('button', { name: '选择项目' }));
    fireEvent.click(screen.getByRole('menuitem', { name: 'repo/other' }));

    await waitFor(() => {
      expect(screen.getByRole('button', { name: '选择项目' })).toHaveTextContent(/^other$/);
      expect(screen.getByText('正在加载会话列表…')).toBeInTheDocument();
      expect(screen.queryByText('后端线程')).not.toBeInTheDocument();
    });

    await act(async () => {
      sidebarRefresh.resolve({
        activeThreadId: 'thread-other',
        threads: [{ id: 'thread-other', name: 'Other project chat', provider: 'claude', status: 'idle' }],
      });
      await Promise.resolve();
    });

    await screen.findByText('Other project chat');
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
    await screen.findByText('后端线程');

    fireEvent.click(screen.getByRole('button', { name: '选择项目' }));
    fireEvent.click(screen.getByRole('menuitem', { name: 'repo/other' }));

    await waitFor(() => {
      expect(screen.getByText('Other project chat')).toBeInTheDocument();
      expect(screen.queryByText('后端线程')).not.toBeInTheDocument();
    });
    expect(useClientStore.getState().activeThreadId).toBe('');
    expect(screen.getByRole('button', { name: /Other project chat/ }).closest('.thread-card')).not.toHaveClass('active');
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
    await screen.findByText('后端线程');

    await waitFor(() => {
      expect(screen.getByRole('button', { name: '选择模型' })).toHaveTextContent('GPT-5.4 · 中');
    });

    fireEvent.click(screen.getByRole('button', { name: '选择模型' }));
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
      expect(screen.getByRole('button', { name: '选择模型' })).toHaveTextContent('GPT-5.5 · 中');
      expect(screen.getByTestId('chat-action-feedback')).toHaveTextContent('线程配置已保存');
    });
  });

  it('shows an archive option on each visible thread card', async () => {
    render(<App />);
    await screen.findByText('后端线程');

    fireEvent.click(screen.getByLabelText('归档会话'));

    await waitFor(() => {
      expect(backend.setPreference).toHaveBeenCalledWith(expect.objectContaining({
        cwd: '/repo/app',
        key: 'archivedThreadAtById.thread-1',
        value: expect.any(Number),
      }));
      expect(screen.queryByText('后端线程')).not.toBeInTheDocument();
      expect(screen.getByTestId('chat-action-feedback')).toHaveTextContent('线程已归档');
    });
  });

  it('keeps the thread visible and reports a readable error when archive RPC fails', async () => {
    backend.archiveThread.mockRejectedValueOnce(new Error('orchestration: service not configured'));
    render(<App />);
    await screen.findByText('后端线程');

    fireEvent.click(screen.getByLabelText('归档会话'));

    expect(await screen.findByText('归档会话失败：orchestration: service not configured')).toBeInTheDocument();
    expect(screen.getByText('后端线程')).toBeInTheDocument();
    expect(backend.setPreference).not.toHaveBeenCalledWith(expect.objectContaining({
      key: 'archivedThreadAtById.thread-1',
    }));
  });

  it('shows the pin action tooltip when hovering the thread pin icon', async () => {
    render(<App />);
    await screen.findByText('后端线程');

    fireEvent.mouseEnter(screen.getByLabelText('置顶对话'));

    expect(screen.getByTestId('thread-pin-tooltip')).toHaveTextContent('置顶对话');

    fireEvent.mouseLeave(screen.getByLabelText('置顶对话'));

    expect(screen.queryByTestId('thread-pin-tooltip')).not.toBeInTheDocument();
  });

  it('renames a thread inline through the legacy backend name RPC', async () => {
    render(<App />);
    await screen.findByText('后端线程');

    fireEvent.click(screen.getByText('后端线程'));
    const input = screen.getByLabelText('会话别名');
    fireEvent.change(input, { target: { value: '重命名会话' } });
    fireEvent.click(screen.getByRole('button', { name: '保存别名' }));

    await waitFor(() => {
      expect(backend.renameThread).toHaveBeenCalledWith({ threadId: 'thread-1', name: '重命名会话' });
      expect(screen.getByText('重命名会话')).toBeInTheDocument();
    });
  });

  it('persists thread pins through the backend threadPins chat preference', async () => {
    render(<App />);
    await screen.findByText('后端线程');

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
    await screen.findByText('Older chat');

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
    await screen.findByText('Newer chat');

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
    await screen.findByText('活跃线程');

    expect(screen.getByLabelText('会话列表')).toBeInTheDocument();
    expect(screen.getByLabelText('1 个 Agent')).toBeInTheDocument();
    expect(screen.queryByText('归档线程')).not.toBeInTheDocument();

    fireEvent.click(screen.getByLabelText('打开归档列表'));

    expect(await screen.findByText('归档线程')).toBeInTheDocument();
    expect(screen.getByLabelText('归档列表')).toBeInTheDocument();
    expect(screen.getByLabelText('返回会话列表')).toBeInTheDocument();
    expect(screen.queryByText('活跃线程')).not.toBeInTheDocument();

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
    expect(screen.queryByText(/我们应该在/)).not.toBeInTheDocument();
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
    expect(screen.queryByText(/我们应该在/)).not.toBeInTheDocument();
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
    expect(screen.queryByText(/我们应该在/)).not.toBeInTheDocument();
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
    await screen.findByText('活跃线程');

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
    await screen.findByText('后端线程');
    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));

    act(() => {
      bridgeCallback({
        type: 'rpc.failed',
        payload: { method: 'turn/start', threadId: 'thread-1', traceId: 'trace-123' },
      });
    });

    const warningLine = await screen.findByText('rpc.failed');
    expect(warningLine).toBeInTheDocument();
    expect(screen.queryByText(/turn\/start/)).not.toBeInTheDocument();

    fireEvent.mouseEnter(warningLine);

    expect(screen.getByTestId('warning-log-popover')).toHaveTextContent('rpc.failed');
    expect(screen.getByTestId('warning-log-popover')).toHaveTextContent('turn/start');

    fireEvent.mouseLeave(warningLine);

    expect(screen.queryByTestId('warning-log-popover')).not.toBeInTheDocument();
  });

  it('navigates to screenshot-style secondary pages without command or task routes', async () => {
    render(<App />);
    await screen.findByText('后端线程');

    expect(screen.queryByLabelText('命令')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('任务')).not.toBeInTheDocument();

    fireEvent.click(screen.getByLabelText('技能'));
    expect(screen.getByText('技能管理')).toBeInTheDocument();
    expect(await screen.findByText('后端')).toBeInTheDocument();
    expect(screen.getByText('/repo/app/.agent/skills/backend')).toBeInTheDocument();
    expect(screen.getByText('私人使用 1')).toBeInTheDocument();
    expect(screen.getByText('项目共享 1')).toBeInTheDocument();
    expect(screen.getByText('全部 2')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /刷新/ })).not.toBeInTheDocument();
    expect(backend.getDashboardPage).toHaveBeenCalledWith({ cwd: '/repo/app', page: 'skills' });

    fireEvent.click(screen.getByLabelText('共享文件'));
    expect(screen.getByText('文件产物')).toBeInTheDocument();
    await waitFor(() => {
      expect(backend.listSharedFiles).toHaveBeenCalledWith();
    });
  });

  it.each([
    ['提示词', 'AI 能力与资料', '暂无内容', () => expect(backend.listPromptAssets).not.toHaveBeenCalled()],
    ['自动化', '自动化', '无任务', () => expect(backend.getDashboardPage).not.toHaveBeenCalledWith({ cwd: '未选择项目', page: 'dags' })],
    ['记忆中心', '记忆中心', '暂无记忆', () => expect(backend.getMemorySnapshot).not.toHaveBeenCalledWith({ cwd: '未选择项目' })],
  ])('keeps the %s route visible while project context resolves', async (navLabel, heading, settledText, assertNoInvalidLoad) => {
    const config = deferred();
    backend.readConfig.mockReturnValueOnce(config.promise);

    render(<App />);
    fireEvent.click(screen.getByLabelText(navLabel));

    expect(screen.getByRole('heading', { name: heading })).toBeInTheDocument();
    expect(screen.getByText('正在连接本地项目...')).toBeInTheDocument();
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

    expect(screen.getByRole('heading', { name: '文件产物' })).toBeInTheDocument();
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
    await screen.findByText('后端线程');

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
    await screen.findByText('后端线程');

    await waitFor(() => {
      expect(screen.getByLabelText('记忆中心').querySelector('i')).toHaveAttribute('title', '2 条待整合相似记忆');
    });
  });


  it('loads and filters prompt assets while wiring active launch prompt preference', async () => {
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
    await screen.findByText('后端线程');
    fireEvent.click(screen.getByLabelText('提示词'));

    expect(await screen.findByText('代码审查专家')).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: '全部 3' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: '专家能力 1' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: '参考资料 1' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: '待确认 1' })).toBeInTheDocument();
    expect(screen.getByText('强制中')).toBeInTheDocument();
    expect(screen.getAllByText('全局可用').length).toBeGreaterThanOrEqual(1);
    expect(screen.queryByRole('button', { name: /刷新/ })).not.toBeInTheDocument();
    expect(backend.listPromptAssets).toHaveBeenCalledWith({ cwd: '/repo/app' });
    expect(backend.getPreference).toHaveBeenCalledWith({ cwd: '/repo/app', key: 'settings.activePromptKey' });

    fireEvent.click(screen.getByRole('tab', { name: '参考资料 1' }));
    expect(screen.queryByText('代码审查专家')).not.toBeInTheDocument();
    expect(screen.getByText('SQLC 资料')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('tab', { name: '专家能力 1' }));
    fireEvent.click(screen.getByRole('button', { name: '取消强制' }));
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
    await screen.findByText('后端线程');
    fireEvent.click(screen.getByLabelText('提示词'));

    const card = (await screen.findByText('代码审查专家')).closest('article');
    const editButton = within(card).getByRole('button', { name: '编辑' });
    editButton.focus();
    fireEvent.click(editButton);

    const editor = await screen.findByRole('dialog', { name: '编辑提示词' });
    const closeButton = within(editor).getByLabelText('关闭编辑器');
    await waitFor(() => {
      expect(document.activeElement).toBe(closeButton);
    });

    fireEvent.keyDown(editor, { key: 'Escape', code: 'Escape' });
    await waitFor(() => {
      expect(screen.queryByRole('dialog', { name: '编辑提示词' })).not.toBeInTheDocument();
    });
    expect(document.activeElement).toBe(editButton);
  });

  it('traps focus in the prompt wizard and restores focus after Escape', async () => {
    backend.listPromptAssets.mockResolvedValue({ prompts: [] });
    mockPromptPreferences();

    render(<App />);
    await screen.findByText('后端线程');
    fireEvent.click(screen.getByLabelText('提示词'));

    const addButton = await screen.findByRole('button', { name: '+ 添加给 AI 的内容' });
    addButton.focus();
    fireEvent.click(addButton);

    const wizard = await screen.findByRole('dialog', { name: '添加给 AI 的内容' });
    const closeButtons = within(wizard).getAllByRole('button', { name: '关闭' });
    await waitFor(() => {
      expect(document.activeElement).toBe(closeButtons[0]);
    });

    fireEvent.keyDown(wizard, { key: 'Tab', code: 'Tab', shiftKey: true });
    expect(document.activeElement).toBe(closeButtons.at(-1));
    fireEvent.keyDown(wizard, { key: 'Tab', code: 'Tab' });
    expect(document.activeElement).toBe(closeButtons[0]);
    fireEvent.keyDown(wizard, { key: 'Escape', code: 'Escape' });
    await waitFor(() => {
      expect(screen.queryByRole('dialog', { name: '添加给 AI 的内容' })).not.toBeInTheDocument();
    });
    expect(document.activeElement).toBe(addButton);
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
    await screen.findByText('后端线程');
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
      await screen.findByText('后端线程');
      fireEvent.click(screen.getByLabelText('提示词'));

      expect(await screen.findByText('代码审查助手')).toBeInTheDocument();
      expect(intervalSpy.mock.calls.filter((call) => call[1] === 4000)).toHaveLength(0);
    } finally {
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
    await screen.findByText('后端线程');
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
        return activePreferenceFails
          ? Promise.reject(new Error('active prompt preference offline'))
          : Promise.resolve('');
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
    await screen.findByText('后端线程');
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
    backend.getDashboardPrompts.mockRejectedValueOnce(new Error('readonly fallback offline'));
    mockPromptPreferences();

    render(<App />);
    await screen.findByText('后端线程');
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

  it('does not fall back to the legacy prompt dashboard when prompt assets are unavailable', async () => {
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
    await screen.findByText('后端线程');
    fireEvent.click(screen.getByLabelText('提示词'));

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('加载提示词失败');
    expect(alert).toHaveTextContent('method not found');
    expect(backend.getDashboardPrompts).not.toHaveBeenCalled();
    expect(screen.queryByText('旧提示词')).not.toBeInTheDocument();
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
    await screen.findByText('后端线程');
    fireEvent.click(screen.getByLabelText('提示词'));
    expect(await screen.findByText('代码审查专家')).toBeInTheDocument();

    fireEvent.click(screen.getByLabelText('Chat'));
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

  it('wires prompt edit, delete, pending draft, and intent wizard actions without card copy action', async () => {
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
    render(<App />);
    await screen.findByText('后端线程');
    fireEvent.click(screen.getByLabelText('提示词'));
    expect(await screen.findByText('代码审查专家')).toBeInTheDocument();

    const card = screen.getByText('代码审查专家').closest('article');
    expect(within(card).queryByRole('button', { name: '复制' })).not.toBeInTheDocument();

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

    const updatedCard = await screen.findByText('代码风险审查');
    fireEvent.click(within(updatedCard.closest('article')).getByRole('button', { name: '删除' }));
    await waitFor(() => {
      expect(backend.deletePrompt).toHaveBeenCalledWith({ cwd: '/repo/app', id: 'main/reviewer', scope: 'project' });
    });

    const pendingCard = screen.getByText('价格表资料').closest('article');
    fireEvent.click(within(pendingCard).getByRole('button', { name: '继续确认' }));
    const pendingDialog = await screen.findByRole('dialog', { name: '添加给 AI 的内容' });
    expect(pendingDialog).toBeInTheDocument();
    expect(screen.getAllByText('价格表资料').length).toBeGreaterThanOrEqual(1);
    fireEvent.click(within(pendingDialog).getAllByRole('button', { name: '关闭' }).at(-1));

    fireEvent.click(within(pendingCard).getByRole('button', { name: '丢弃' }));
    await waitFor(() => {
      expect(backend.discardPromptIntent).toHaveBeenCalledWith({ cwd: '/repo/app', draftKey: 'intent/recall/ready' });
    });

    fireEvent.click(screen.getByRole('button', { name: '+ 添加给 AI 的内容' }));
    expect(await screen.findByRole('dialog', { name: '添加给 AI 的内容' })).toBeInTheDocument();
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

    render(<App />);
    await screen.findByText('后端线程');
    fireEvent.click(screen.getByLabelText('提示词'));
    fireEvent.click(await screen.findByRole('button', { name: '+ 添加给 AI 的内容' }));
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

    render(<App />);
    await screen.findByText('后端线程');
    fireEvent.click(screen.getByLabelText('提示词'));
    fireEvent.click(await screen.findByRole('button', { name: '+ 添加给 AI 的内容' }));
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

    render(<App />);
    await screen.findByText('后端线程');
    fireEvent.click(screen.getByLabelText('提示词'));
    fireEvent.click(await screen.findByRole('button', { name: '+ 添加给 AI 的内容' }));
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

    render(<App />);
    await screen.findByText('后端线程');
    fireEvent.click(screen.getByLabelText('提示词'));
    fireEvent.click(await screen.findByRole('button', { name: '+ 添加给 AI 的内容' }));
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
    await screen.findByText('后端线程');
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
    await screen.findByText('后端线程');
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
      await screen.findByText('后端线程');
      fireEvent.click(screen.getByLabelText('记忆中心'));

      expect(await screen.findByText('遵守 TDD')).toBeInTheDocument();
      expect(intervalSpy.mock.calls.filter((call) => call[1] === 4000)).toHaveLength(0);
    } finally {
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
    await screen.findByText('后端线程');
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
    await screen.findByText('后端线程');
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
    await screen.findByText('后端线程');
    fireEvent.click(screen.getByLabelText('记忆中心'));
    expect(await screen.findByText('遵守 TDD')).toBeInTheDocument();

    fireEvent.click(screen.getByLabelText('Chat'));
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
    await screen.findByText('后端线程');
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
    await screen.findByText('后端线程');
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

    render(<App />);
    await screen.findByText('后端线程');
    await waitFor(() => {
      expect(screen.getByLabelText('记忆中心').querySelector('i')).toHaveAttribute('title', '1 条待整合相似记忆');
    });

    fireEvent.click(screen.getByLabelText('记忆中心'));
    expect(await screen.findByText('1 组条目内容相似')).toBeInTheDocument();

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

      hasSimilar = false;
      await act(async () => {
        await vi.advanceTimersByTimeAsync(2000);
        await Promise.resolve();
      });
    } finally {
      vi.useRealTimers();
    }

    await waitFor(() => {
      expect(screen.queryByText('1 组条目内容相似')).not.toBeInTheDocument();
      expect(screen.queryByRole('button', { name: '一键整合全部' })).not.toBeInTheDocument();
      expect(screen.getByText('已整合 1 组')).toBeInTheDocument();
      expect(screen.getByLabelText('记忆中心').querySelector('i')).toBeNull();
    });
    expect(backend.getMemorySnapshot).toHaveBeenLastCalledWith({ cwd: '/repo/app' });
  });

  it('loads shared files from the shared-files RPC and wires open, export, delete, and continue actions', async () => {
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
    const memoryPayload = () => ({
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
    });
    backend.listSharedFiles.mockImplementation(() => Promise.resolve(memoryPayload()));
    backend.readSharedFile.mockImplementation(({ path }) => Promise.resolve({
      path,
      content: path === 'reports/final.md' ? 'FINAL CONTENT' : '{"step":1,"detail":true}',
      updatedBy: path === 'reports/final.md' ? 'dag-runner' : 'agent',
      updatedAt: '2026-05-30T08:30:00Z',
    }));
    backend.deleteSharedFile.mockImplementation(({ path }) => {
      memoryFiles = memoryFiles.filter((item) => item.path !== path);
      return Promise.resolve({ deleted: true });
    });
    backend.saveTextFile.mockResolvedValue('/exports/work.json');

    render(<App />);
    await screen.findByText('后端线程');
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

    memoryFiles = [
      ...memoryFiles,
      {
        path: 'scratch/notes.md',
        content: 'fresh notes',
        updated_by: 'agent',
        updated_at: '2026-05-30T09:00:00Z',
      },
    ];
    await act(async () => {
      bridgeCallback?.({ type: 'ui/shared-files/changed', payload: { path: 'scratch/notes.md', action: 'write' } });
    });
    expect(await screen.findByText('notes.md')).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: '全部 3' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: '工作文件 2' })).toBeInTheDocument();

    memoryFiles = [
      ...memoryFiles,
      {
        path: 'scratch/focus-refresh.md',
        content: 'focus refresh',
        updated_by: 'agent',
        updated_at: '2026-05-30T09:01:00Z',
      },
    ];
    await act(async () => {
      window.dispatchEvent(new Event('focus'));
    });
    expect(await screen.findByText('focus-refresh.md')).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: '全部 4' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: '工作文件 3' })).toBeInTheDocument();

    const finalCard = screen.getByText('final.md').closest('article');
    expect(within(finalCard).getByText('最终产物')).toBeInTheDocument();
    expect(within(finalCard).getByRole('button', { name: '不可删除' })).toBeDisabled();
    fireEvent.click(within(finalCard).getByRole('button', { name: '打开' }));

    expect(await screen.findByRole('dialog', { name: '文件预览' })).toBeInTheDocument();
    expect(screen.getByText('FINAL CONTENT')).toBeInTheDocument();
    expect(backend.readSharedFile).toHaveBeenCalledWith({ path: 'reports/final.md' });
    fireEvent.click(screen.getByRole('button', { name: '关闭' }));

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

    const remainingFinalCard = screen.getByText('final.md').closest('article');
    fireEvent.click(within(remainingFinalCard).getByRole('button', { name: '用此文件继续对话' }));
    expect(screen.getByTestId('composer-input').value).toContain('reports/final.md');
    expect(screen.getByText('final.md')).toBeInTheDocument();
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
    await screen.findByText('后端线程');
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
    await screen.findByText('后端线程');
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
    await screen.findByText('后端线程');
    fireEvent.click(screen.getByLabelText('共享文件'));
    expect(await screen.findByText('final.md')).toBeInTheDocument();

    fireEvent.click(screen.getByLabelText('Chat'));
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
      await screen.findByText('后端线程');
      fireEvent.click(screen.getByLabelText('共享文件'));

      expect(await screen.findByText('final.md')).toBeInTheDocument();
      expect(intervalSpy.mock.calls.filter((call) => call[1] === 4000)).toHaveLength(0);
    } finally {
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
    await screen.findByText('后端线程');
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
    await screen.findByText('后端线程');
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
    await screen.findByText('后端线程');
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
      expect(backend.getDagRuns).toHaveBeenCalledWith({ dagKey: 'daily-brief', limit: 5 });
      expect(backend.getDagRuns).toHaveBeenCalledWith({ dagKey: 'daily-brief', status: 'running', limit: 1 });
      expect(backend.getDagRun).toHaveBeenCalledWith({ runKey: 'run-1' });
    });
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
    await screen.findByText('后端线程');
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
      bridgeCallback?.({ type: 'task/node/statusChanged', payload: { dag_key: 'flow-b', node_key: 'step', new_status: 'running' } });
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
      await screen.findByText('后端线程');
      fireEvent.click(screen.getByLabelText('自动化'));

      expect((await screen.findAllByText('流程 A')).length).toBeGreaterThanOrEqual(1);
      expect(intervalSpy.mock.calls.filter((call) => call[1] === 4000)).toHaveLength(0);
    } finally {
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
    await screen.findByText('后端线程');
    fireEvent.click(screen.getByLabelText('自动化'));
    expect((await screen.findAllByText('流程 A')).length).toBeGreaterThanOrEqual(1);

    backend.getDashboardPage.mockRejectedValueOnce(new Error('workflow backend offline'));
    await act(async () => {
      bridgeCallback?.({ type: 'task/node/statusChanged', payload: { dag_key: 'flow-a', node_key: 'step', new_status: 'running' } });
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
    await screen.findByText('后端线程');
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
    await screen.findByText('后端线程');
    fireEvent.click(screen.getByLabelText('自动化'));
    expect((await screen.findAllByText('流程 A')).length).toBeGreaterThanOrEqual(1);

    fireEvent.click(screen.getByLabelText('Chat'));
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
    await screen.findByText('后端线程');
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
    await screen.findByText('后端线程');
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

  it('runs, stops, deletes, schedules, edits and designs DAGs through the old RPC surface', async () => {
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
    backend.getPreference.mockImplementation(({ key }) => Promise.resolve({
      'settings.provider.active': 'codex',
      'settings.provider.codex.model': 'gpt-5.5',
      'settings.provider.codex.effort': 'xhigh',
      'settings.provider.codex.codexHome': '/Users/test/.codex-alt',
      'settings.provider.codex.codexInstanceKey': 'desktop-main',
      'settings.provider.codex.codexModelProvider': 'openrouter',
      'settings.activePromptKey': 'main/reviewer',
    }[key] ?? null));
    backend.startThread.mockResolvedValue({ thread: { id: 'thread-design' } });
    backend.getThreadState.mockResolvedValueOnce({ timelinesByThread: {}, activeThreadId: 'thread-design' });

    render(<App />);
    await screen.findByText('后端线程');
    fireEvent.click(screen.getByLabelText('自动化'));
    expect((await screen.findAllByText('Daily Brief')).length).toBeGreaterThanOrEqual(2);

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
        ops: [{ op: 'update_dag', patch: { trigger: 'scheduled', cron_expr: '0 9 * * 1-5' } }],
      });
    });
    expect(await screen.findByText('已保存定时任务')).toBeInTheDocument();

    fireEvent.click(screen.getByText('高级设置'));
    fireEvent.input(screen.getByLabelText('名称'), { target: { value: '起草 v2' } });
    expect(screen.getByLabelText('名称')).toHaveValue('起草 v2');
    fireEvent.change(screen.getByLabelText('依赖步骤'), { target: { value: 'outline' } });
    expect(screen.queryByLabelText('Provider')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('Prompt Key')).not.toBeInTheDocument();
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
            depends_on: ['outline'],
            config: expect.objectContaining({ provider: 'codex', model: 'gpt-5', prompt_key: 'main/writer' }),
          }),
        })],
      });
    });

    fireEvent.click(screen.getByRole('button', { name: '删除' }));
    expect(await screen.findByRole('dialog', { name: '删除自动化' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '确认删除' }));
    await waitFor(() => {
      expect(backend.deleteDag).toHaveBeenCalledWith({ dagKey: 'daily-brief' });
    });

    fireEvent.click(screen.getByRole('button', { name: 'AI 设计流程' }));
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
      expect(designPayload.provider).toBeUndefined();
      expect(designPayload.config).toEqual(expect.objectContaining({
        codexHome: '/Users/test/.codex-alt',
        codexInstanceKey: 'desktop-main',
        codexModelProvider: 'openrouter',
        providerNativeSkills: false,
      }));
      expect(designPayload.config.enabledTools).toContain('task_start_dag');
    });
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
    await screen.findByText('后端线程');
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
    await screen.findByText('后端线程');
    fireEvent.click(screen.getByLabelText('技能'));
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
    await screen.findByText('后端线程');
    fireEvent.click(screen.getByLabelText('技能'));
    const personalCard = (await screen.findByRole('heading', { name: 'personal-review' })).closest('article');

    expect(within(personalCard).getAllByText('当你需要私人代码审查偏好时使用。')).toHaveLength(1);
  });

  it('keeps the skills route visible while project context resolves', async () => {
    const config = deferred();
    backend.readConfig.mockReturnValueOnce(config.promise);

    render(<App />);
    fireEvent.click(screen.getByLabelText('技能'));

    expect(screen.getByRole('heading', { name: '技能管理' })).toBeInTheDocument();
    expect(screen.getByText('正在连接本地项目...')).toBeInTheDocument();
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
    await screen.findByText('后端线程');
    fireEvent.click(screen.getByLabelText('技能'));
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
    await screen.findByText('后端线程');
    fireEvent.click(screen.getByLabelText('技能'));

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
    await screen.findByText('后端线程');
    fireEvent.click(screen.getByLabelText('技能'));
    expect(await screen.findByText('后端')).toBeInTheDocument();

    fireEvent.click(screen.getByLabelText('Chat'));
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
    fireEvent.click(screen.getByLabelText('技能'));

    expect(screen.queryByText('加载技能中...')).not.toBeInTheDocument();
    expect(screen.getByText('后端')).toBeInTheDocument();
    expect(await screen.findByText('安全工程师')).toBeInTheDocument();
    expect(screen.queryByText('后端')).not.toBeInTheDocument();
  });

  it('releases the skills loading state when the dashboard request hangs', async () => {
    render(<App />);
    await screen.findByText('后端线程');

    backend.getDashboardPage.mockImplementation(({ page }) => (
      page === 'skills'
        ? new Promise(() => {})
        : Promise.resolve({
          memory: [],
          finalOutputRefs: [],
          sharedFileRetention: { items: [], protectedCount: 0, cleanupCandidateCount: 0 },
        })
    ));

    vi.useFakeTimers();
    try {
      fireEvent.click(screen.getByLabelText('技能'));
      expect(screen.getByText('加载技能中...')).toBeInTheDocument();

      await act(async () => {
        await vi.advanceTimersByTimeAsync(8000);
        await Promise.resolve();
        await Promise.resolve();
      });

      expect(screen.queryByText('加载技能中...')).not.toBeInTheDocument();
      expect(screen.getByRole('alert')).toHaveTextContent('技能列表加载超时');
    } finally {
      vi.useRealTimers();
    }

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
    await screen.findByText('后端线程');
    fireEvent.click(screen.getByLabelText('技能'));

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
    await screen.findByText('后端线程');
    fireEvent.click(screen.getByLabelText('技能'));
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

  it('creates a skill, suggests a summary, and saves through skills/local/write', async () => {
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
    await screen.findByText('后端线程');
    fireEvent.click(screen.getByLabelText('技能'));
    await screen.findByText('后端');

    fireEvent.click(screen.getByRole('button', { name: '新建技能' }));
    expect(await screen.findByRole('dialog', { name: '新建技能' })).toBeInTheDocument();
    expect(screen.queryByLabelText('显示名称')).not.toBeInTheDocument();
    fireEvent.change(screen.getByLabelText('技能名称'), { target: { value: '部署技能' } });
    fireEvent.change(screen.getByLabelText('关键词'), { target: { value: 'deploy, ship' } });
    fireEvent.change(screen.getByLabelText('技能内容'), { target: { value: '## 部署规则\n执行部署前检查环境。' } });
    fireEvent.click(screen.getByRole('button', { name: '帮我生成' }));

    const summarySuggestion = await screen.findByText(/当你需要部署服务时使用。/);
    expect(summarySuggestion).toBeInTheDocument();
    expect(screen.getByLabelText('技能简介').compareDocumentPosition(summarySuggestion) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(summarySuggestion.compareDocumentPosition(screen.getByText('使用范围')) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
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
      expect(backend.writeSkill).toHaveBeenCalledWith(expect.objectContaining({
        cwd: '/repo/app',
        path: '部署技能',
        scope: 'project',
        personal_type: '',
      }));
    });
    const savePayload = backend.writeSkill.mock.calls.at(-1)[0];
    expect(savePayload.content).toContain('name: "部署技能"');
    expect(savePayload.content).toContain('display_name: "部署技能"');
    expect(savePayload.content).toContain('description: "当你需要部署服务时使用。"');
    expect(savePayload.content).toContain('trigger_words: ["deploy", "ship"]');
  });

  it('opens an existing skill, loads related files, and saves edits', async () => {
    render(<App />);
    await screen.findByText('后端线程');
    fireEvent.click(screen.getByLabelText('技能'));
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

  it('imports skill directories with selected scope through skills/local/importDir', async () => {
    render(<App />);
    await screen.findByText('后端线程');
    fireEvent.click(screen.getByLabelText('技能'));
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

    render(<App />);
    await screen.findByText('后端线程');
    fireEvent.click(screen.getByLabelText('技能'));

    expect(await screen.findByText(/发现 1 个技能冲突/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '用本项目内容覆盖外部版本' }));
    expect(await screen.findByText('/Users/test/.codex/skills/backend/SKILL.md')).toBeInTheDocument();
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
    await screen.findByText('后端线程');
    fireEvent.click(screen.getByLabelText('技能'));

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
    await screen.findByText('后端线程');
    fireEvent.click(screen.getByLabelText('技能'));

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

    fireEvent.change(screen.getByLabelText('Codex Home'), { target: { value: '/tmp/codex-home' } });
    fireEvent.change(screen.getByLabelText('Instance Key'), { target: { value: 'desktop-main' } });
    fireEvent.change(screen.getByLabelText('Model Provider'), { target: { value: 'openrouter' } });
    fireEvent.change(screen.getByLabelText('Sandbox Policy'), { target: { value: 'readOnly' } });
    fireEvent.click(screen.getByRole('button', { name: '保存 Provider 设置' }));

    await waitFor(() => {
      expect(backend.setPreference).toHaveBeenCalledWith({ cwd: '/repo/app', key: 'settings.provider.codex.codexHome', value: '/tmp/codex-home' });
      expect(backend.setPreference).toHaveBeenCalledWith({ cwd: '/repo/app', key: 'settings.provider.codex.codexInstanceKey', value: 'desktop-main' });
      expect(backend.setPreference).toHaveBeenCalledWith({ cwd: '/repo/app', key: 'settings.provider.codex.codexModelProvider', value: 'openrouter' });
      expect(backend.setPreference).toHaveBeenCalledWith({
        cwd: '/repo/app',
        key: 'settings.provider.codex.sandbox',
        value: { type: 'readOnly' },
      });
    });

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
});
