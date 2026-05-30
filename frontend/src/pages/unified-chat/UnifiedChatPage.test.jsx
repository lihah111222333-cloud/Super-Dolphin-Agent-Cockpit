// @vitest-environment jsdom
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/react';
import React from 'react';
import UnifiedChatPage from './UnifiedChatPage';
import { useThreadStore } from '../../entities/thread/model/useThreadStore';
import { useProjectStore } from '../../entities/project/model/useProjectStore';
import { useLogStore } from '../../entities/log/model/useLogStore';

afterEach(() => {
  cleanup();
});

// Mock backend API module
vi.mock('../../shared/api/backendApi', () => {
  return {
    startThread: vi.fn(),
    startTurn: vi.fn(),
    getSidebarState: vi.fn().mockResolvedValue({ threads: [] }),
    getThreadState: vi.fn().mockResolvedValue({ activeThreadId: '', threads: [] }),
    getThreadMessages: vi.fn().mockResolvedValue({ messages: [] }),
    onBridgeEvent: vi.fn(() => () => {}),
    selectFiles: vi.fn(),
    compactThread: vi.fn(),
    recoverThread: vi.fn(),
    renameThread: vi.fn(),
    setPreference: vi.fn(),
    registerBridgeLogStore: vi.fn(),
    sendFrontendLogBatch: vi.fn(),
  };
});

import {
  startThread as mockStartThread,
  startTurn as mockStartTurn,
  getSidebarState as mockGetSidebarState,
  getThreadState as mockGetThreadState,
  getThreadMessages as mockGetThreadMessages,
  selectFiles as mockSelectFiles,
  setPreference as mockSetPreference,
} from '../../shared/api/backendApi';

describe('UnifiedChatPage Integration Tests', () => {
  beforeEach(() => {
    vi.clearAllMocks();

    // Mock scrollIntoView which is not implemented in JSDOM
    window.HTMLElement.prototype.scrollIntoView = vi.fn();

    // Reset Zustand stores to clean initial state
    useThreadStore.setState({
      threads: [],
      statuses: {},
      timelinesByThread: {},
      tokenUsageByThread: {},
      diffTextByThread: {},
      activeThreadId: '',
      activeCmdThreadId: '',
    });

    useProjectStore.setState({
      projects: [],
      active: '/mock/cwd',
      scopeCwd: '',
      showModal: false,
      modalPath: '',
      browsing: false,
    });

    useLogStore.setState({
      entries: [],
    });

    // Default API mock resolutions
    mockGetSidebarState.mockResolvedValue({
      threads: [],
    });
    mockGetThreadState.mockResolvedValue({
      activeThreadId: '',
      threads: [],
    });
    mockGetThreadMessages.mockResolvedValue({
      messages: [],
    });
  });

  it('verifies that sending a message on a blank thread triggers startThread (thread/start) and then sendMessage (turn/start)', async () => {
    // Mock successful startThread RPC response
    mockStartThread.mockResolvedValue({
      id: 'thread-new-id',
      threadId: 'thread-new-id',
    });
    mockStartTurn.mockResolvedValue({ ok: true });

    render(<UnifiedChatPage />);

    // Type message in composer input
    const input = screen.getByTestId('composer-input');
    fireEvent.change(input, { target: { value: 'Hello Super Dolphin' } });

    // Click send
    const sendButton = screen.getByTestId('composer-send-button');
    fireEvent.click(sendButton);

    // Wait for the sequence to complete
    await waitFor(() => {
      expect(mockStartThread).toHaveBeenCalledWith({
        cwd: '/mock/cwd',
        deferSpawn: true,
        name: 'Hello Super Dolphin',
        provider: 'codex',
      });
      expect(mockStartTurn).toHaveBeenCalledWith({
        cwd: '/mock/cwd',
        threadId: 'thread-new-id',
        input: [{ type: 'text', text: 'Hello Super Dolphin' }],
        manualSkillSelection: false,
      });
    });

    // Verify input is cleared
    expect(input.value).toBe('');
  });

  it('verifies subsequent sends skip thread/start and only call turn/start', async () => {
    // Setup existing thread in store
    const threadId = 'thread-existing-id';
    useThreadStore.setState({
      activeThreadId: threadId,
      threads: [{ id: threadId, name: 'Existing Conversation', agentKey: 'codex', createdAt: new Date().toISOString() }],
    });

    mockStartTurn.mockResolvedValue({ ok: true });

    render(<UnifiedChatPage />);

    // Type and send message
    const input = screen.getByTestId('composer-input');
    fireEvent.change(input, { target: { value: 'Subsequent Message' } });

    const sendButton = screen.getByTestId('composer-send-button');
    fireEvent.click(sendButton);

    await waitFor(() => {
      expect(mockStartTurn).toHaveBeenCalledWith({
        cwd: '/mock/cwd',
        threadId: threadId,
        input: [{ type: 'text', text: 'Subsequent Message' }],
        manualSkillSelection: false,
      });
    });

    // Verify startThread was never called
    expect(mockStartThread).not.toHaveBeenCalled();
    expect(input.value).toBe('');
  });

  it('starts a new backend thread instead of sending to a stale disconnected active thread', async () => {
    const staleThread = {
      id: 'dead-thread',
      name: 'Disconnected thread',
      provider: 'codex',
      lastMessage: 'health-failure: codexapp: transport not running',
      createdAt: new Date().toISOString(),
    };
    useThreadStore.setState({
      activeThreadId: 'dead-thread',
      statuses: { 'dead-thread': 'idle' },
      threads: [staleThread],
    });
    mockGetSidebarState.mockResolvedValue({
      activeThreadId: 'dead-thread',
      statuses: { 'dead-thread': 'idle' },
      threads: [staleThread],
    });
    mockStartThread.mockResolvedValue({
      id: 'thread-new-id',
      threadId: 'thread-new-id',
    });
    mockStartTurn.mockResolvedValue({ ok: true });

    render(<UnifiedChatPage />);

    const input = screen.getByTestId('composer-input');
    fireEvent.change(input, { target: { value: 'Please answer after stale recovery' } });
    fireEvent.click(screen.getByTestId('composer-send-button'));

    await waitFor(() => {
      expect(mockStartThread).toHaveBeenCalledWith({
        cwd: '/mock/cwd',
        deferSpawn: true,
        name: 'Please answer after ',
        provider: 'codex',
      });
      expect(mockStartTurn).toHaveBeenCalledWith({
        cwd: '/mock/cwd',
        threadId: 'thread-new-id',
        input: [{ type: 'text', text: 'Please answer after stale recovery' }],
        manualSkillSelection: false,
      });
    });
    expect(mockStartTurn).not.toHaveBeenCalledWith(expect.objectContaining({ threadId: 'dead-thread' }));
  });

  it('verifies drafts and attachments are preserved when startThread fails', async () => {
    // Mock startThread failure
    mockStartThread.mockRejectedValue(new Error('Start Thread RPC Failed'));

    render(<UnifiedChatPage />);

    // Type message
    const input = screen.getByTestId('composer-input');
    fireEvent.change(input, { target: { value: 'Will fail thread creation' } });

    // Click send
    const sendButton = screen.getByTestId('composer-send-button');
    fireEvent.click(sendButton);

    await waitFor(() => {
      expect(mockStartThread).toHaveBeenCalled();
    });

    // Input draft should be preserved (not cleared)
    expect(input.value).toBe('Will fail thread creation');
    expect(mockStartTurn).not.toHaveBeenCalled();
  });

  it('verifies drafts and attachments are preserved when sendMessage (turn/start) fails', async () => {
    const threadId = 'thread-existing-id';
    useThreadStore.setState({
      activeThreadId: threadId,
      threads: [{ id: threadId, name: 'Existing Conversation', agentKey: 'codex', createdAt: new Date().toISOString() }],
    });

    // Mock startTurn failure
    mockStartTurn.mockRejectedValue(new Error('Start Turn RPC Failed'));
    mockSelectFiles.mockResolvedValue(['/mock/path/file.txt']);

    render(<UnifiedChatPage />);

    // Add attachment
    const attachButton = screen.getByTestId('composer-attach-button');
    fireEvent.click(attachButton);

    await screen.findByText('file.txt');

    // Type message
    const input = screen.getByTestId('composer-input');
    fireEvent.change(input, { target: { value: 'Will fail message turn' } });

    // Click send
    const sendButton = screen.getByTestId('composer-send-button');
    fireEvent.click(sendButton);

    await waitFor(() => {
      expect(mockStartTurn).toHaveBeenCalledWith(expect.objectContaining({
        cwd: '/mock/cwd',
        threadId,
        input: [
          { type: 'text', text: 'Will fail message turn' },
          { type: 'mention', name: 'file.txt', path: '/mock/path/file.txt' },
        ],
      }));
    });

    // Input draft and attachments should be preserved/restored
    expect(input.value).toBe('Will fail message turn');
    expect(screen.getByText('file.txt')).toBeTruthy();
  });

  it('verifies that missing CWD fails-fast and preserves drafts', async () => {
    // Set CWD to empty/dot to trigger CWD error
    useProjectStore.setState({
      active: '.',
      scopeCwd: '',
    });

    render(<UnifiedChatPage />);

    const input = screen.getByTestId('composer-input');
    fireEvent.change(input, { target: { value: 'No CWD message' } });

    const sendButton = screen.getByTestId('composer-send-button');
    fireEvent.click(sendButton);

    // Should fail-fast: mock APIs never called
    expect(mockStartThread).not.toHaveBeenCalled();
    expect(mockStartTurn).not.toHaveBeenCalled();

    // Draft preserved
    expect(input.value).toBe('No CWD message');

    // Error logged in LogStore
    const logEntries = useLogStore.getState().entries;
    const errorLog = logEntries.find(e => e.event === 'chat.send.failed');
    expect(errorLog).toBeTruthy();
    expect(errorLog.fields.error).toContain('CWD is required');
  });

  it('passes explicit cwd to compact and recover toolbar actions', async () => {
    const threadId = 'thread-existing-id';
    useThreadStore.setState({
      activeThreadId: threadId,
      statuses: { [threadId]: 'idle' },
      threads: [{ id: threadId, name: 'Existing Conversation', provider: 'codex', createdAt: new Date().toISOString() }],
    });

    const { compactThread, recoverThread } = await import('../../shared/api/backendApi');
    compactThread.mockResolvedValue({ ok: true });
    recoverThread.mockResolvedValue({ ok: true });

    render(<UnifiedChatPage />);

    fireEvent.click(screen.getByTestId('composer-compact-button'));
    fireEvent.click(screen.getByTitle('恢复线程'));

    await waitFor(() => {
      expect(compactThread).toHaveBeenCalledWith({ cwd: '/mock/cwd', threadId });
      expect(recoverThread).toHaveBeenCalledWith({ cwd: '/mock/cwd', threadId });
    });
  });

  it('pins and archives the clicked thread card even when it is not the active thread', async () => {
    useThreadStore.setState({
      activeThreadId: '',
      threads: [{
        id: 'card-thread',
        name: 'Card action thread',
        provider: 'codex',
        createdAt: new Date().toISOString(),
      }],
    });
    mockSetPreference.mockResolvedValue({ ok: true });

    render(<UnifiedChatPage />);

    fireEvent.click(screen.getByTestId('thread-pin-card-thread'));
    fireEvent.click(screen.getByTestId('thread-archive-card-thread'));

    await waitFor(() => {
      expect(mockSetPreference).toHaveBeenCalledWith(expect.objectContaining({
        cwd: '/mock/cwd',
        key: 'pinnedThreadAtById.card-thread',
      }));
      expect(mockSetPreference).toHaveBeenCalledWith(expect.objectContaining({
        cwd: '/mock/cwd',
        key: 'archivedThreadAtById.card-thread',
      }));
    });
  });

  it('allows the workbench and runtime log panel to be resized horizontally', () => {
    render(<UnifiedChatPage />);

    const panel = screen.getByTestId('right-inspector-panel');
    const resizer = screen.getByTestId('right-panel-resizer');

    expect(panel.style.width).toBe('384px');
    fireEvent.mouseDown(resizer, { clientX: 1000 });
    fireEvent.mouseMove(window, { clientX: 880 });
    fireEvent.mouseUp(window);

    expect(panel.style.width).toBe('504px');
  });

  it('renders backend sidebar threads without a name field instead of crashing', () => {
    useThreadStore.setState({
      activeThreadId: 'thread-without-name',
      statuses: { 'thread-without-name': 'idle' },
      threads: [{
        id: 'thread-without-name',
        title: '后端标题',
        provider: 'codex',
        createdAt: new Date().toISOString(),
      }],
    });

    render(<UnifiedChatPage />);

    expect(screen.getByText('后端标题')).toBeTruthy();
  });

  it('does not render internal turn timeline records as empty assistant bubbles', () => {
    useThreadStore.setState({
      activeThreadId: 'thread-internal',
      statuses: { 'thread-internal': 'idle' },
      threads: [{ id: 'thread-internal', name: 'Internal timeline', provider: 'codex', createdAt: new Date().toISOString() }],
      timelinesByThread: {
        'thread-internal': [
          { id: 'turn-start', kind: 'turn_start', status: 'running' },
          { id: 'turn-end', kind: 'turn_end', status: 'completed' },
        ],
      },
    });

    render(<UnifiedChatPage />);

    expect(screen.queryByText('Invalid Date')).toBeNull();
    expect(screen.queryByText('AI')).toBeNull();
  });
});
