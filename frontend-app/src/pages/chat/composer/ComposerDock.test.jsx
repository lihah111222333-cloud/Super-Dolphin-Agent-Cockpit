import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import React, { useState } from 'react';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import {
  frontendHealthSnapshot,
  frontendHealthStateSnapshot,
  resetFrontendHealthForTest,
} from '../../../shared/diagnostics/frontendHealthStore.js';
import {
  resetVisibleActionFailureForTest,
  visibleActionFailureSnapshot,
} from '../../../shared/ui/actionFailureSink.js';
import { ComposerDock, shouldNavigatePromptHistory } from './ComposerDock.jsx';

function createComposer(overrides = {}) {
  return {
    activePreview: null,
    dropActive: false,
    handleCompositionEnd: vi.fn(),
    handleCompositionStart: vi.fn(),
    handleDragEnter: vi.fn(),
    handleDragLeave: vi.fn(),
    handleDragOver: vi.fn(),
    handleDrop: vi.fn(),
    handlePaste: vi.fn(),
    isComposing: vi.fn(() => false),
    previewAttachmentItem: vi.fn(),
    removeAttachmentItem: vi.fn(),
    setPreviewAttachment: vi.fn(),
    ...overrides,
  };
}

function createStore(overrides = {}) {
  return {
    addComposerCapability: vi.fn(),
    bootstrapStatus: 'ready',
    clearComposer: vi.fn(),
    composerCapabilities: [],
    forkDraft: { open: false },
    hasActiveThreadActions: vi.fn(() => true),
    hasInterruptibleThreadAction: vi.fn(() => false),
    interruptActiveThread: vi.fn(),
    openForkDraft: vi.fn(),
    newThread: vi.fn(),
    notifyAction: vi.fn(),
    provider: 'codex',
    providerConfig: { provider: 'codex', model: 'gpt-5.5', effort: 'xhigh' },
    threadConfigByThread: {},
    threadConfigLoadingByThread: {},
    threadConfigSaving: false,
    reconcileComposerCapabilities: vi.fn(),
    removeComposerCapability: vi.fn(),
    ...overrides,
  };
}

const reviewCapability = {
  kind: 'skill',
  key: 'skill:project::review:/repo/app/.agents/skills/review',
  name: 'review',
  label: 'Code Review',
  ref: {
    name: 'review',
    scope: 'project',
    personalType: '',
    path: '/repo/app/.agents/skills/review',
  },
};

const reviewItem = {
  id: reviewCapability.key,
  kind: 'skill',
  name: 'review',
  label: 'Code Review',
  description: 'Review code',
  keywords: ['review'],
  payload: { capability: reviewCapability },
  disabled: false,
  disabledReason: '',
};

function createSlashCommandService() {
  return {
    loadSkills: vi.fn().mockResolvedValue([reviewItem]),
    loadPrompts: vi.fn().mockResolvedValue([]),
    loadAutomations: vi.fn().mockResolvedValue([]),
    loadMCPTools: vi.fn().mockResolvedValue([]),
    loadPromptContent: vi.fn(),
  };
}

function createQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: Infinity },
      mutations: { retry: false },
    },
  });
}

function renderDock(props, childWrapper = null) {
  const dock = <ComposerDock {...props} />;
  return render(
    <QueryClientProvider client={createQueryClient()}>
      {childWrapper ? childWrapper(dock) : dock}
    </QueryClientProvider>,
  );
}

function StatefulComposerDock({ initialDraft, setDraftSpy, ...props }) {
  const [draft, setDraftState] = useState(initialDraft);
  const setDraft = (value) => {
    setDraftSpy(value);
    setDraftState(value);
  };
  return <ComposerDock {...props} draft={draft} setDraft={setDraft} />;
}

function renderStatefulDock(props, initialDraft = '') {
  const setDraftSpy = vi.fn();
  const result = render(
    <QueryClientProvider client={createQueryClient()}>
      <StatefulComposerDock {...props} initialDraft={initialDraft} setDraftSpy={setDraftSpy} />
    </QueryClientProvider>,
  );
  return { ...result, setDraftSpy };
}

const baseProps = {
  attachments: [],
  canUseProjectActions: true,
  draft: 'hello',
  floating: false,
  modelThreadId: 'thread1',
  projectPath: '/repo/app',
  selectFiles: vi.fn(),
  sendMessage: vi.fn(),
  sending: false,
  setDraft: vi.fn(),
  showProviderToggle: true,
};

describe('ComposerDock', () => {
  it('navigates history only for an unhandled collapsed caret at the multiline boundary', () => {
    const textarea = document.createElement('textarea');
    textarea.value = 'first\nsecond\nthird';
    const keyEvent = (overrides = {}) => ({
      altKey: false,
      ctrlKey: false,
      defaultPrevented: false,
      isComposing: false,
      key: 'ArrowUp',
      keyCode: 38,
      metaKey: false,
      shiftKey: false,
      ...overrides,
    });

    textarea.setSelectionRange(2, 2);
    expect(shouldNavigatePromptHistory(keyEvent(), textarea, 'previous')).toBe(true);
    textarea.setSelectionRange(8, 8);
    expect(shouldNavigatePromptHistory(keyEvent(), textarea, 'previous')).toBe(false);
    textarea.setSelectionRange(8, 8);
    expect(shouldNavigatePromptHistory(keyEvent({ key: 'ArrowDown', keyCode: 40 }), textarea, 'next')).toBe(false);
    textarea.setSelectionRange(textarea.value.length, textarea.value.length);
    expect(shouldNavigatePromptHistory(keyEvent({ key: 'ArrowDown', keyCode: 40 }), textarea, 'next')).toBe(true);

    textarea.setSelectionRange(0, 3);
    expect(shouldNavigatePromptHistory(keyEvent(), textarea, 'previous')).toBe(false);
    textarea.setSelectionRange(0, 0);
    expect(shouldNavigatePromptHistory(keyEvent({ defaultPrevented: true }), textarea, 'previous')).toBe(false);
    expect(shouldNavigatePromptHistory(keyEvent({ isComposing: true }), textarea, 'previous')).toBe(false);
    expect(shouldNavigatePromptHistory(keyEvent({ keyCode: 229 }), textarea, 'previous')).toBe(false);
    expect(shouldNavigatePromptHistory(keyEvent({ metaKey: true }), textarea, 'previous')).toBe(false);
  });

  it('replaces draft from history without sending and restores the draft sentinel', async () => {
    const fetchPromptHistory = vi.fn().mockResolvedValue({
      entries: [{
        threadId: 'thread1',
        messageId: 'message-1',
        text: 'older prompt',
        createdAt: '2026-07-12T10:00:00Z',
      }],
      nextCursor: '',
      hasMore: false,
      nonce: 'nonce-1',
    });
    const sendMessage = vi.fn();
    function HistoryHarness() {
      const [draft, setDraft] = React.useState('unfinished');
      return (
        <ComposerDock
          {...baseProps}
          composer={createComposer()}
          draft={draft}
          fetchPromptHistory={fetchPromptHistory}
          sendMessage={sendMessage}
          setDraft={setDraft}
          store={createStore()}
        />
      );
    }
    render(
      <QueryClientProvider client={createQueryClient()}>
        <HistoryHarness />
      </QueryClientProvider>,
    );
    const textarea = screen.getByRole('combobox', { name: '输入给 Agent 的内容' });
    textarea.setSelectionRange(0, 0);
    fireEvent.keyDown(textarea, { key: 'ArrowUp' });
    await waitFor(() => expect(textarea).toHaveValue('older prompt'));
    expect(sendMessage).not.toHaveBeenCalled();

    textarea.setSelectionRange(textarea.value.length, textarea.value.length);
    fireEvent.keyDown(textarea, { key: 'ArrowDown' });
    await waitFor(() => expect(textarea).toHaveValue('unfinished'));
    expect(sendMessage).not.toHaveBeenCalled();
  });

  it('publishes rejected prompt history when Health storage contains malformed JSON without an unhandled rejection', async () => {
    window.localStorage.clear();
    window.localStorage.setItem('super-dolphin.frontend-health.v1', '{invalid');
    resetFrontendHealthForTest();
    resetVisibleActionFailureForTest();
    const rawSecret = 'provider token=secret /Users/private/path';
    const unhandled = vi.fn();
    window.addEventListener('unhandledrejection', unhandled);

    try {
      renderDock({
        ...baseProps,
        composer: createComposer(),
        fetchPromptHistory: vi.fn().mockRejectedValue(new Error(rawSecret)),
        store: createStore(),
      });
      const textarea = screen.getByRole('combobox', { name: '输入给 Agent 的内容' });
      textarea.setSelectionRange(0, 0);
      fireEvent.keyDown(textarea, { key: 'ArrowUp' });

      await waitFor(() => expect(visibleActionFailureSnapshot()).toEqual(expect.objectContaining({
        actionId: 'prompt-history.previous',
      })));
      expect(frontendHealthSnapshot()).toEqual(expect.arrayContaining([
        expect.objectContaining({ actionId: 'prompt-history.previous' }),
      ]));
      expect(frontendHealthStateSnapshot().persistence).toEqual(expect.objectContaining({
        status: 'failed', code: 'HEALTH_PERSISTENCE_FAILED',
      }));
      expect(JSON.stringify(frontendHealthStateSnapshot())).not.toContain(rawSecret);
      await Promise.resolve();
      expect(unhandled).not.toHaveBeenCalled();
    } finally {
      window.removeEventListener('unhandledrejection', unhandled);
    }
  });

  it.each(['create', 'delete', 'archive', 'rename'])(
    'passes the %s threads reference as the prompt-history lifecycle signal',
    async (actionName) => {
      const fetchPromptHistory = vi.fn()
        .mockResolvedValueOnce({
          entries: [{ threadId: 'thread1', messageId: 'before', text: 'before', createdAt: '2026-07-12T10:00:00Z' }],
          nextCursor: '', hasMore: false, nonce: 'nonce-before',
        })
        .mockResolvedValueOnce({
          entries: [{ threadId: 'thread1', messageId: actionName, text: actionName, createdAt: '2026-07-12T10:00:01Z' }],
          nextCursor: '', hasMore: false, nonce: `nonce-${actionName}`,
        });
      function Harness({ threads }) {
        const [draft, setDraft] = React.useState('draft');
        return <ComposerDock {...baseProps} composer={createComposer()} draft={draft} fetchPromptHistory={fetchPromptHistory}
          setDraft={setDraft} store={createStore({ threads })} />;
      }
      const queryClient = createQueryClient();
      const renderHarness = (threads) => (
        <QueryClientProvider client={queryClient}>
          <Harness threads={threads} />
        </QueryClientProvider>
      );
      const { rerender } = render(renderHarness([{ id: 'thread1' }]));
      const textarea = screen.getByRole('combobox', { name: '输入给 Agent 的内容' });
      textarea.setSelectionRange(0, 0);
      fireEvent.keyDown(textarea, { key: 'ArrowUp' });
      await waitFor(() => expect(textarea).toHaveValue('before'));
      rerender(renderHarness([{ id: 'thread1' }, { actionName }]));
      textarea.setSelectionRange(0, 0);
      fireEvent.keyDown(textarea, { key: 'ArrowUp' });
      await waitFor(() => expect(textarea).toHaveValue(actionName));
      expect(fetchPromptHistory).toHaveBeenCalledTimes(2);
    },
  );

  it('keeps one textarea mounted while approval pending makes every composer action inert', () => {
    const inputRef = React.createRef();
    const store = createStore({
      activeProject: '/repo/app',
      projects: ['/repo/app'],
      setActiveProjectPath: vi.fn(),
    });
    function ApprovalComposerHarness({ approvalPending }) {
      const [draft, setDraft] = React.useState('initial approval draft');
      return (
        <ComposerDock
          {...baseProps}
          approvalPending={approvalPending}
          canUseProjectActions
          composer={createComposer()}
          draft={draft}
          inputRef={inputRef}
          setDraft={setDraft}
          showProjectSelector
          store={store}
        />
      );
    }

    const queryClient = createQueryClient();
    const renderApprovalComposer = (approvalPending) => (
      <QueryClientProvider client={queryClient}>
        <ApprovalComposerHarness approvalPending={approvalPending} />
      </QueryClientProvider>
    );
    const { rerender } = render(renderApprovalComposer(false));
    const textarea = screen.getByRole('combobox', { name: '输入给 Agent 的内容' });
    fireEvent.change(textarea, { target: { value: 'draft kept through approval' } });
    expect(textarea).toHaveValue('draft kept through approval');

    rerender(renderApprovalComposer(true));
    const pendingTextarea = screen.getByRole('combobox', { name: '输入给 Agent 的内容' });
    const pendingDock = screen.getByTestId('composer-dock');
    expect.soft(pendingTextarea).toBe(textarea);
    expect.soft(pendingTextarea).toHaveValue('draft kept through approval');
    expect.soft(inputRef.current).toBe(textarea);
    expect.soft(pendingDock).toHaveAttribute('inert', '');
    expect.soft(pendingDock).toHaveAttribute('aria-disabled', 'true');
    for (const name of ['发送消息', '添加文件', '选择模型', '选择项目']) {
      expect.soft(screen.getByRole('button', { name })).toBeDisabled();
    }

    rerender(renderApprovalComposer(false));
    const settledTextarea = screen.getByRole('combobox', { name: '输入给 Agent 的内容' });
    const settledDock = screen.getByTestId('composer-dock');
    expect.soft(settledTextarea).toBe(textarea);
    expect.soft(settledTextarea).toHaveValue('draft kept through approval');
    expect.soft(inputRef.current).toBe(textarea);
    expect.soft(settledDock).not.toHaveAttribute('inert');
    expect.soft(settledDock).not.toHaveAttribute('aria-disabled', 'true');
  });

  it('routes primary, attach, paste, and enter actions through props without reserved controls', () => {
    const composer = createComposer();
    const store = createStore();
    const props = { ...baseProps, composer, store, selectFiles: vi.fn(), sendMessage: vi.fn(), setDraft: vi.fn() };

    const { container } = renderDock(props);

    const addFileButton = screen.getByRole('button', { name: '添加文件' });
    expect(addFileButton).toHaveAccessibleName('添加文件');
    expect(addFileButton.textContent).toBe('');
    expect(addFileButton.querySelector('svg')).toBeInTheDocument();
    expect(addFileButton.querySelector('.composer-attach-label')).toBeNull();
    fireEvent.click(addFileButton);
    fireEvent.click(screen.getByRole('button', { name: '发送消息' }));
    fireEvent.paste(screen.getByRole('combobox', { name: '输入给 Agent 的内容' }));
    fireEvent.keyDown(screen.getByRole('combobox', { name: '输入给 Agent 的内容' }), { key: 'Enter' });

    expect(screen.getByTestId('composer-dock')).toHaveClass('composer--docked');
    expect(screen.getByTestId('composer-dock')).not.toHaveClass('composer--floating');
    expect(addFileButton).toHaveClass('composer-icon-action', 'composer-attach');
    expect(container.querySelector('.composer-context')).toHaveTextContent('app');
    expect(screen.queryByText('添加附件')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '继承当前对话' })).not.toBeInTheDocument();
    expect(container.querySelector('.project-select')).toBeNull();
    expect(props.selectFiles).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole('button', { name: '自定义配置' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '语音输入' })).not.toBeInTheDocument();
    expect(store.openForkDraft).not.toHaveBeenCalled();
    expect(props.sendMessage).toHaveBeenCalledTimes(2);
    expect(composer.handlePaste).toHaveBeenCalledTimes(1);
  });

  it('switches the active project from the composer project selector', async () => {
    const store = createStore({
      activeProject: '/repo/app',
      projects: ['/repo/app', '/repo/side-project'],
      addProjectFromPicker: vi.fn(),
      removeProjectPath: vi.fn(),
      setActiveProjectPath: vi.fn(),
    });

    renderDock(
      {
        ...baseProps,
        composer: createComposer(),
        showProjectSelector: true,
        store,
      },
      (dock) => <div className="sa-window" data-theme="light">{dock}</div>,
    );

    fireEvent.click(screen.getByRole('button', { name: '选择项目' }));
    const menu = await screen.findByRole('menu');
    fireEvent.click(within(menu).getByRole('menuitem', { name: 'repo/side-project' }));

    expect(store.setActiveProjectPath).toHaveBeenCalledWith('/repo/side-project');
  });

  it('keeps composer tool controls usable when only the project cwd is missing', async () => {
    const selectFiles = vi.fn();
    const store = createStore({
      activeProject: '',
      projects: [],
      setActiveProjectPath: vi.fn(),
    });
    renderDock({
      ...baseProps,
      canUseProjectActions: false,
      composer: createComposer(),
      projectPath: '未选择项目',
      selectFiles,
      sendMessage: vi.fn(),
      showProjectSelector: true,
      store,
    });

    // 发送仍要求项目 cwd（业务契约保留）；四个工具控件只要求后端就绪。
    expect(screen.getByRole('button', { name: '发送消息' })).toBeDisabled();

    const addFile = screen.getByRole('button', { name: '添加文件' });
    const addImage = screen.getByRole('button', { name: 'Add image' });
    const projectButton = screen.getByRole('button', { name: '选择项目' });
    const modelButton = screen.getByRole('button', { name: '选择模型' });
    expect(addFile).toBeEnabled();
    expect(addImage).toBeEnabled();
    expect(projectButton).toBeEnabled();
    expect(modelButton).toBeEnabled();

    // 文件与图片按钮进入真实的文件选择流程。
    fireEvent.click(addFile);
    expect(selectFiles).toHaveBeenCalledTimes(1);
    fireEvent.click(addImage);
    expect(selectFiles).toHaveBeenCalledTimes(2);

    // 项目按钮在无项目时仍可打开选择器（恢复入口不能自我禁用）。
    fireEvent.click(projectButton);
    expect(await screen.findByRole('menu')).toBeInTheDocument();

    // 模型按钮打开模型菜单。
    fireEvent.click(modelButton);
    expect(await screen.findByRole('dialog')).toBeInTheDocument();
  });

  it('exposes an accessible submit anchor only in send mode', () => {
    const props = { ...baseProps, composer: createComposer(), store: createStore(), sendMessage: vi.fn() };

    renderDock(props);

    const submitButton = screen.getByTestId('composer-submit');
    expect(submitButton).toBe(screen.getByRole('button', { name: '发送消息' }));
    expect(submitButton).toHaveAccessibleName('发送消息');
    expect(submitButton).toBeEnabled();
    expect(screen.queryByTestId('composer-interrupt')).not.toBeInTheDocument();

    fireEvent.click(submitButton);

    expect(props.sendMessage).toHaveBeenCalledTimes(1);
  });

  it('keeps the submit anchor disabled when the send action is unavailable', () => {
    const props = { ...baseProps, draft: '', composer: createComposer(), store: createStore(), sendMessage: vi.fn() };

    renderDock(props);

    const submitButton = screen.getByTestId('composer-submit');
    expect(submitButton).toBe(screen.getByRole('button', { name: '发送消息' }));
    expect(submitButton).toBeDisabled();
    expect(screen.queryByTestId('composer-interrupt')).not.toBeInTheDocument();

    fireEvent.click(submitButton);

    expect(props.sendMessage).not.toHaveBeenCalled();
  });

  it('uses the floating class for the new-chat intro composer', () => {
    renderDock({ ...baseProps, floating: true, composer: createComposer(), store: createStore() });

    expect(screen.getByTestId('composer-dock')).toHaveClass('composer--floating');
    expect(screen.getByTestId('composer-dock')).not.toHaveClass('composer--docked');
    expect(screen.getByTestId('composer-dock').querySelector('.composer-card')).toBeInTheDocument();
    expect(screen.getByText('Super Dolphin Agent 可能出错，请核对重要信息。')).toHaveClass('composer-disclaimer');
    expect(screen.queryByText('Super Dolphin Agent can make mistakes. Consider verifying critical information.')).not.toBeInTheDocument();
  });

  it('switches the primary action to interrupt when the active thread is interruptible', () => {
    const store = createStore({ hasInterruptibleThreadAction: vi.fn(() => true) });

    renderDock({ ...baseProps, composer: createComposer(), store });

    const interruptButton = screen.getByTestId('composer-interrupt');
    expect(interruptButton).toBe(screen.getByRole('button', { name: '中断当前执行' }));
    expect(interruptButton).toHaveAccessibleName('中断当前执行');
    expect(interruptButton).toBeEnabled();
    expect(screen.queryByTestId('composer-submit')).not.toBeInTheDocument();

    fireEvent.click(interruptButton);

    expect(store.interruptActiveThread).toHaveBeenCalledTimes(1);
  });

  it('opens the slash palette, exposes combobox state, and selects before sending', async () => {
    const store = createStore();
    const sendMessage = vi.fn();
    renderStatefulDock({
      ...baseProps,
      composer: createComposer(),
      sendMessage,
      slashCommandService: createSlashCommandService(),
      store,
    });
    const textarea = screen.getByRole('combobox', { name: '输入给 Agent 的内容' });

    fireEvent.change(textarea, { target: { value: '/' } });
    expect(await screen.findByRole('listbox', { name: '命令与能力' })).toBeVisible();
    fireEvent.change(textarea, { target: { value: '/rev' } });
    await screen.findByRole('option', { name: /Code Review/u });

    fireEvent.keyDown(textarea, { key: 'ArrowDown' });
    expect(textarea).toHaveAttribute('aria-expanded', 'true');
    expect(textarea).toHaveAttribute('aria-controls');
    expect(textarea).toHaveAttribute('aria-activedescendant');

    fireEvent.keyDown(textarea, { key: 'Enter' });
    await waitFor(() => expect(store.addComposerCapability).toHaveBeenCalledWith(
      expect.objectContaining({ kind: 'skill', name: 'review' }),
    ));
    expect(sendMessage).not.toHaveBeenCalled();
  });

  it('preserves normal Enter, Shift+Enter, and IME key behavior while the palette is closed', () => {
    const composer = createComposer();
    const sendMessage = vi.fn();
    renderDock({ ...baseProps, composer, sendMessage, store: createStore() });
    const textarea = screen.getByRole('combobox', { name: '输入给 Agent 的内容' });

    expect(fireEvent.keyDown(textarea, { key: 'Enter', shiftKey: true })).toBe(true);
    expect(sendMessage).not.toHaveBeenCalled();

    composer.isComposing.mockReturnValue(true);
    expect(fireEvent.keyDown(textarea, { key: 'Enter' })).toBe(true);
    expect(sendMessage).not.toHaveBeenCalled();

    composer.isComposing.mockReturnValue(false);
    expect(fireEvent.keyDown(textarea, { key: 'Enter' })).toBe(false);
    expect(sendMessage).toHaveBeenCalledTimes(1);
  });

  it('dispatches /clear through the composer action that owns text, attachments, and capabilities', async () => {
    const sendMessage = vi.fn();
    const store = createStore({
      composerCapabilities: [{ ...reviewCapability, availability: 'ready' }],
    });
    renderStatefulDock({
      ...baseProps,
      attachments: [{ path: '/tmp/change.patch', name: 'change.patch' }],
      composer: createComposer(),
      sendMessage,
      slashCommandService: createSlashCommandService(),
      store,
    }, '/clear');
    const textarea = screen.getByRole('combobox', { name: '输入给 Agent 的内容' });

    await screen.findByRole('option', { name: /清空输入/u });
    fireEvent.keyDown(textarea, { key: 'Enter' });

    await waitFor(() => expect(store.clearComposer).toHaveBeenCalledTimes(1));
    expect(sendMessage).not.toHaveBeenCalled();
  });

  it('does not count a selected capability as sendable user input', () => {
    const store = createStore({
      composerCapabilities: [{ ...reviewCapability, availability: 'ready' }],
    });
    renderDock({
      ...baseProps,
      draft: '',
      composer: createComposer(),
      slashCommandService: createSlashCommandService(),
      store,
    });

    expect(screen.getByTestId('composer-submit')).toBeDisabled();
  });

  it.each(['stale', 'unverified'])('blocks sending while a capability is %s', (availability) => {
    const store = createStore({
      composerCapabilities: [{ ...reviewCapability, availability }],
    });
    renderDock({
      ...baseProps,
      composer: createComposer(),
      slashCommandService: createSlashCommandService(),
      store,
    });

    expect(screen.getByTestId('composer-submit')).toBeDisabled();
  });
});
