import React from 'react';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import App from './App.jsx';
import { resetClientStoreForTests } from './entities/client/model/useClientStore.js';

let bridgeCallback;

const backend = vi.hoisted(() => ({
  readConfig: vi.fn(),
  getWindowBootstrap: vi.fn(),
  getProjects: vi.fn(),
  setActiveProject: vi.fn(),
  addProject: vi.fn(),
  removeProject: vi.fn(),
  getSidebarState: vi.fn(),
  getThreadState: vi.fn(),
  getThreadMessages: vi.fn(),
  getBuildInfo: vi.fn(),
  getDashboardPage: vi.fn(),
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
  saveTextFile: vi.fn(),
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

describe('frontend-app connected client shell', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    bridgeCallback = null;
    resetClientStoreForTests();
    backend.readConfig.mockResolvedValue({ cwd: '/repo/app' });
    backend.getWindowBootstrap.mockResolvedValue({ snapshot: null });
    backend.getProjects.mockResolvedValue({ projects: ['/repo/app'], active: '/repo/app' });
    backend.setActiveProject.mockResolvedValue({ projects: ['/repo/app'], active: '/repo/app' });
    backend.addProject.mockResolvedValue({ projects: ['/repo/app'], active: '/repo/app' });
    backend.removeProject.mockResolvedValue({ projects: ['/repo/app'], active: '/repo/app' });
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: '后端线程', provider: 'codex', status: '工作中' }],
      tokenUsageByThread: {
        'thread-1': { usedTokens: 128, contextWindowTokens: 1024, usedPercent: 12.5 },
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
    backend.getDashboardPage.mockImplementation(({ page }) => Promise.resolve(
      page === 'memory'
        ? { memory: [], finalOutputRefs: [], sharedFileRetention: { items: [], protectedCount: 0, cleanupCandidateCount: 0 } }
        : { skills: defaultSkills },
    ));
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
    backend.saveTextFile.mockResolvedValue('/exports/file.md');
    backend.getBuildInfo.mockResolvedValue({
      version: 'v1.2.3',
      runtime: 'linux/amd64',
      buildTime: '2026-05-30T07:00:00Z',
      commit: 'abc123def456',
    });
    backend.getPreference.mockImplementation(({ key }) => Promise.resolve({
      'settings.provider.active': 'codex',
      'settings.provider.codex.codexHome': '~/.codex',
      'settings.provider.codex.codexInstanceKey': 'default',
      'settings.provider.codex.codexModelProvider': 'openai',
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

  it('bootstraps project, sidebar, timeline and token usage from backend', async () => {
    render(<App />);

    expect(await screen.findByText('后端线程')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '选择项目' })).toHaveTextContent('repo/app');
    expect(screen.getByLabelText('当前工作目录')).toHaveAttribute('title', '当前窗口 CWD：/repo/app');
    expect(screen.getByText(/128 \/ 1024 tokens/)).toBeInTheDocument();
    expect(screen.getByText(/diff --git a\/file b\/file/)).toBeInTheDocument();
    expect(backend.getProjects).toHaveBeenCalledWith({ cwd: '/repo/app' });
    expect(backend.getThreadState).toHaveBeenCalledWith({
      cwd: '/repo/app',
      threadId: 'thread-1',
      includeDiff: true,
    });
  });

  it('does not render a duplicate desktop titlebar inside the app shell', async () => {
    const { container } = render(<App />);

    expect(await screen.findByText('后端线程')).toBeInTheDocument();
    expect(container.querySelector('.titlebar')).toBeNull();
    expect(container.querySelector('.traffic-lights')).toBeNull();
    expect(screen.queryByText('Super Agent')).not.toBeInTheDocument();
  });

  it('keeps the user message visible and calls thread/start before turn/start for a new chat', async () => {
    backend.getSidebarState.mockResolvedValue({ activeThreadId: '', threads: [] });
    backend.getThreadState.mockResolvedValue({ timelinesByThread: {} });
    backend.startThread.mockResolvedValue({ thread: { id: 'thread-new' } });
    backend.startTurn.mockResolvedValue({ ok: true });

    render(<App />);

    await screen.findByText('我们应该在 app 中构建什么？');
    expect(screen.getByTestId('composer-project')).toHaveTextContent('app');
    expect(screen.getByTestId('composer-project')).toHaveAttribute('title', '/repo/app');
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
    expect(screen.getByText('暂无会话，点击顶部「新对话」开始草稿')).toBeInTheDocument();
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
    Object.assign(navigator, { clipboard: { writeText: vi.fn().mockResolvedValue(undefined) } });

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
      expect(navigator.clipboard.writeText).toHaveBeenCalledWith(expect.stringContaining('thread-1'));
      expect(backend.interruptTurn).toHaveBeenCalledWith({ cwd: '/repo/app', threadId: 'thread-1' });
      expect(backend.recoverThread).toHaveBeenCalledWith({ cwd: '/repo/app', threadId: 'thread-1' });
      expect(backend.archiveThread).toHaveBeenCalledWith({ threadId: 'thread-1' });
      expect(backend.setPreference).toHaveBeenCalledWith(expect.objectContaining({
        cwd: '/repo/app',
        key: 'archivedThreadAtById.thread-1',
      }));
    });
  });

  it('shows visible feedback for chat toolbar actions', async () => {
    backend.resolveThreadIdentity.mockResolvedValue({ id: 'thread-1', providerThreadId: 'provider-thread-1', agent_id: 'agent-1' });
    Object.assign(navigator, { clipboard: { writeText: vi.fn().mockResolvedValue(undefined) } });

    render(<App />);
    await screen.findByText('后端线程');

    fireEvent.click(screen.getByLabelText('复制当前线程'));

    await waitFor(() => {
      expect(screen.getByTestId('chat-action-feedback')).toHaveTextContent('线程信息已复制');
      expect(navigator.clipboard.writeText).toHaveBeenCalledWith(expect.stringContaining('thread-1'));
    });
  });

  it('shows visible feedback when copying thread info is blocked', async () => {
    backend.resolveThreadIdentity.mockResolvedValue({ id: 'thread-1', providerThreadId: 'provider-thread-1', agent_id: 'agent-1' });
    Object.assign(navigator, { clipboard: { writeText: vi.fn().mockRejectedValue(new Error('Write permission denied')) } });

    render(<App />);
    await screen.findByText('后端线程');

    fireEvent.click(screen.getByLabelText('复制当前线程'));

    await waitFor(() => {
      expect(screen.getByTestId('chat-action-feedback')).toHaveTextContent('复制失败：Write permission denied');
      expect(navigator.clipboard.writeText).toHaveBeenCalledWith(expect.stringContaining('thread-1'));
    });
  });

  it('aligns the top toolbar provider toggle and removes duplicate composer controls', async () => {
    render(<App />);
    await screen.findByText('后端线程');

    expect(screen.queryByLabelText('线程状态')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('压缩当前线程')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('选择附件')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('权限')).not.toBeInTheDocument();
    expect(screen.getByLabelText('添加文件')).toBeInTheDocument();
    expect(screen.getByLabelText('发送权限')).toBeInTheDocument();

    fireEvent.click(screen.getByLabelText('切换 Claude / Codex provider'));

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
      expect(screen.getByRole('button', { name: '选择项目' })).toHaveTextContent('repo/other');
    });

    fireEvent.click(screen.getByRole('button', { name: '选择项目' }));
    fireEvent.click(screen.getByRole('menuitem', { name: '添加项目' }));
    await waitFor(() => {
      expect(backend.selectProjectDir).toHaveBeenCalledWith('/repo/other');
      expect(backend.addProject).toHaveBeenCalledWith({ cwd: '/repo/app', path: '/repo/new' });
      expect(screen.getByRole('button', { name: '选择项目' })).toHaveTextContent('repo/other');
    });

    fireEvent.click(screen.getByRole('button', { name: '选择项目' }));
    fireEvent.click(screen.getByRole('button', { name: '移除此项目 repo/new' }));
    await waitFor(() => {
      expect(backend.removeProject).toHaveBeenCalledWith({ cwd: '/repo/app', path: '/repo/new' });
      expect(screen.getByTestId('chat-action-feedback')).toHaveTextContent('已移除项目：repo/new');
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

    act(() => {
      bridgeCallback({
        type: 'rpc.failed',
        payload: { method: 'turn/start', threadId: 'thread-1', traceId: 'trace-123' },
      });
    });

    expect(await screen.findByText('rpc.failed')).toBeInTheDocument();
    expect(screen.getByText(/turn\/start/)).toBeInTheDocument();
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
      expect(backend.getDashboardPage).toHaveBeenCalledWith({ cwd: '/repo/app', page: 'memory' });
    });
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
    backend.getPreference.mockImplementation(({ key }) => Promise.resolve(
      key === 'settings.activePromptKey' ? 'main/reviewer' : null,
    ));

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
    backend.getPreference.mockResolvedValue('');

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
    backend.getPreference.mockResolvedValue('');

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
    expect(await screen.findByRole('dialog', { name: '删除记忆' })).toBeInTheDocument();
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
    expect(await screen.findByRole('dialog', { name: '整合相似记忆' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '确认整合' }));
    await waitFor(() => {
      expect(backend.mergeMemoryEntries).toHaveBeenCalledWith({
        cwd: '/repo/app', targetA: 'private', pathA: 'feedback/a.md', targetB: 'team', pathB: 'feedback/b.md',
      });
    });

    fireEvent.click(screen.getByRole('button', { name: '一键整合全部' }));
    await waitFor(() => {
      expect(backend.consolidateMemorySimilarities).toHaveBeenCalledWith({ cwd: '/repo/app' });
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
    let finishConsolidation;
    backend.getMemorySnapshot.mockImplementation(() => Promise.resolve(hasSimilar ? snapshotWithSimilar : snapshotWithoutSimilar));
    backend.consolidateMemorySimilarities.mockImplementation(() => new Promise((resolve) => {
      finishConsolidation = resolve;
    }));

    render(<App />);
    await screen.findByText('后端线程');
    await waitFor(() => {
      expect(screen.getByLabelText('记忆中心').querySelector('i')).toHaveAttribute('title', '1 条待整合相似记忆');
    });

    fireEvent.click(screen.getByLabelText('记忆中心'));
    expect(await screen.findByText('1 组条目内容相似')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '一键整合全部' }));

    await waitFor(() => {
      expect(backend.consolidateMemorySimilarities).toHaveBeenCalledWith({ cwd: '/repo/app' });
    });
    expect(screen.getByRole('button', { name: '整合中...' })).toBeDisabled();

    hasSimilar = false;
    await act(async () => {
      finishConsolidation({ merged: 1, ignored: 0, failed: 0, skipped: 0 });
    });

    await waitFor(() => {
      expect(screen.queryByText('1 组条目内容相似')).not.toBeInTheDocument();
      expect(screen.queryByRole('button', { name: '一键整合全部' })).not.toBeInTheDocument();
      expect(screen.getByText('已整合 1 组')).toBeInTheDocument();
      expect(screen.getByLabelText('记忆中心').querySelector('i')).toBeNull();
    });
    expect(backend.getMemorySnapshot).toHaveBeenLastCalledWith({ cwd: '/repo/app' });
  });

  it('loads shared files from the memory dashboard and wires open, export, delete, and continue actions', async () => {
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
    backend.getDashboardPage.mockImplementation(({ page }) => Promise.resolve(
      page === 'memory' ? memoryPayload() : { skills: [] },
    ));
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
      expect(backend.getDashboardPage).toHaveBeenCalledWith({ cwd: '/repo/app', page: 'memory' });
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

  it('keeps cached shared files visible when navigating back and refreshes silently', async () => {
    let memoryFiles = [{
      path: 'reports/final.md',
      content: 'final summary',
      updated_by: 'dag-runner',
      updated_at: '2026-05-30T08:00:00Z',
    }];
    const memoryPayload = () => ({
      memory: memoryFiles,
      finalOutputRefs: [{ path: 'reports/final.md', runKey: 'run-1', dagKey: 'daily-brief', sourceNodeKey: 'report' }],
      sharedFileRetention: {
        items: [{ path: 'reports/final.md', protected: true, cleanupCandidate: false, reason: 'final_output' }],
        protectedCount: 1,
        cleanupCandidateCount: 0,
      },
    });
    backend.getDashboardPage.mockImplementation(({ page }) => Promise.resolve(
      page === 'memory' ? memoryPayload() : { skills: [] },
    ));

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

    expect(screen.queryByText('正在加载任务流程...')).not.toBeInTheDocument();
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
    expect(await screen.findByRole('dialog', { name: '删除任务流程' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '确认删除' }));
    await waitFor(() => {
      expect(backend.deleteDag).toHaveBeenCalledWith({ dagKey: 'daily-brief' });
    });

    fireEvent.click(screen.getByRole('button', { name: 'AI 设计流程' }));
    await waitFor(() => {
      expect(backend.startThread).toHaveBeenCalledWith(expect.objectContaining({
        cwd: '/repo/app',
        provider: 'codex',
        name: 'AI 设计流程',
        agentKey: 'dag_designer',
        promptKey: 'main/dag_designer_zh',
        deferSpawn: true,
      }));
      expect(backend.startThread.mock.calls.at(-1)[0].config.enabledTools).toContain('task_start_dag');
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
        vi.advanceTimersByTime(8000);
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

    expect(await screen.findByText(/当你需要部署服务时使用。/)).toBeInTheDocument();
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
