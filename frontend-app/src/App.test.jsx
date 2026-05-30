import React from 'react';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import App from './App.jsx';
import { resetClientStoreForTests } from './entities/client/model/useClientStore.js';

let bridgeCallback;

const backend = vi.hoisted(() => ({
  readConfig: vi.fn(),
  getWindowBootstrap: vi.fn(),
  getProjects: vi.fn(),
  getSidebarState: vi.fn(),
  getThreadState: vi.fn(),
  getThreadMessages: vi.fn(),
  getBuildInfo: vi.fn(),
  getPreference: vi.fn(),
  startThread: vi.fn(),
  startTurn: vi.fn(),
  interruptTurn: vi.fn(),
  compactThread: vi.fn(),
  recoverThread: vi.fn(),
  renameThread: vi.fn(),
  setPreference: vi.fn(),
  selectFiles: vi.fn(),
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
    backend.getWindowBootstrap.mockResolvedValue({ ok: true });
    backend.getProjects.mockResolvedValue({ projects: ['/repo/app'], active: '/repo/app' });
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
    backend.getBuildInfo.mockResolvedValue({
      version: 'v1.2.3',
      runtime: 'linux/amd64',
      buildTime: '2026-05-30T07:00:00Z',
      commit: 'abc123def456',
    });
    backend.getPreference.mockResolvedValue(null);
    backend.setPreference.mockResolvedValue({ ok: true });
  });

  it('bootstraps project, sidebar, timeline and token usage from backend', async () => {
    render(<App />);

    expect(await screen.findByText('后端线程')).toBeInTheDocument();
    expect(screen.getByText('/repo/app')).toBeInTheDocument();
    expect(screen.getByText(/128 \/ 1024 tokens/)).toBeInTheDocument();
    expect(screen.getByText(/diff --git a\/file b\/file/)).toBeInTheDocument();
    expect(backend.getProjects).toHaveBeenCalledWith({ cwd: '/repo/app' });
    expect(backend.getThreadState).toHaveBeenCalledWith({
      cwd: '/repo/app',
      threadId: 'thread-1',
      includeDiff: true,
    });
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

    expect(screen.getAllByText('请真正调用后端聊天').length).toBeGreaterThanOrEqual(1);
  });

  it('connects attachments and conversation operation buttons', async () => {
    backend.selectFiles.mockResolvedValue(['/tmp/a.txt']);

    render(<App />);
    await screen.findByText('后端线程');

    fireEvent.click(screen.getByLabelText('添加文件'));
    expect(await screen.findByText('a.txt')).toBeInTheDocument();

    fireEvent.click(screen.getByLabelText('中断当前对话'));
    fireEvent.click(screen.getByLabelText('压缩当前线程'));
    fireEvent.click(screen.getByLabelText('恢复当前线程'));
    fireEvent.click(screen.getByLabelText('归档当前线程'));

    await waitFor(() => {
      expect(backend.selectFiles).toHaveBeenCalled();
      expect(backend.interruptTurn).toHaveBeenCalledWith({ cwd: '/repo/app', threadId: 'thread-1' });
      expect(backend.compactThread).toHaveBeenCalledWith({ cwd: '/repo/app', threadId: 'thread-1' });
      expect(backend.recoverThread).toHaveBeenCalledWith({ cwd: '/repo/app', threadId: 'thread-1' });
      expect(backend.setPreference).toHaveBeenCalledWith(expect.objectContaining({
        cwd: '/repo/app',
        key: 'archivedThreadAtById.thread-1',
      }));
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

    fireEvent.click(screen.getByLabelText('共享文件'));
    expect(screen.getByText('文件产物')).toBeInTheDocument();
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
