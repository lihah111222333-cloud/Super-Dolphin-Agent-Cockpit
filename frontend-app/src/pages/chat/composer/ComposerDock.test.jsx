import React from 'react';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { ComposerDock } from './ComposerDock.jsx';

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
    forkDraft: { open: false },
    hasActiveThreadActions: vi.fn(() => true),
    hasInterruptibleThreadAction: vi.fn(() => false),
    interruptActiveThread: vi.fn(),
    openForkDraft: vi.fn(),
    provider: 'codex',
    providerConfig: { provider: 'codex', model: 'gpt-5.5', effort: 'xhigh' },
    threadConfigByThread: {},
    threadConfigLoadingByThread: {},
    threadConfigSaving: false,
    ...overrides,
  };
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

    const { rerender } = render(<ApprovalComposerHarness approvalPending={false} />);
    const textarea = screen.getByRole('textbox', { name: '输入给 Agent 的内容' });
    fireEvent.change(textarea, { target: { value: 'draft kept through approval' } });
    expect(textarea).toHaveValue('draft kept through approval');

    rerender(<ApprovalComposerHarness approvalPending />);
    const pendingTextarea = screen.getByRole('textbox', { name: '输入给 Agent 的内容' });
    const pendingDock = screen.getByTestId('composer-dock');
    expect.soft(pendingTextarea).toBe(textarea);
    expect.soft(pendingTextarea).toHaveValue('draft kept through approval');
    expect.soft(inputRef.current).toBe(textarea);
    expect.soft(pendingDock).toHaveAttribute('inert', '');
    expect.soft(pendingDock).toHaveAttribute('aria-disabled', 'true');
    for (const name of ['发送消息', '添加文件', '选择模型', '选择项目']) {
      expect.soft(screen.getByRole('button', { name })).toBeDisabled();
    }

    rerender(<ApprovalComposerHarness approvalPending={false} />);
    const settledTextarea = screen.getByRole('textbox', { name: '输入给 Agent 的内容' });
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

    const { container } = render(<ComposerDock {...props} />);

    const addFileButton = screen.getByRole('button', { name: '添加文件' });
    expect(addFileButton).toHaveAccessibleName('添加文件');
    expect(addFileButton.textContent).toBe('');
    expect(addFileButton.querySelector('svg')).toBeInTheDocument();
    expect(addFileButton.querySelector('.composer-attach-label')).toBeNull();
    fireEvent.click(addFileButton);
    fireEvent.click(screen.getByRole('button', { name: '发送消息' }));
    fireEvent.paste(screen.getByRole('textbox', { name: '输入给 Agent 的内容' }));
    fireEvent.keyDown(screen.getByRole('textbox', { name: '输入给 Agent 的内容' }), { key: 'Enter' });

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

    render(
      <div className="sa-window" data-theme="light">
        <ComposerDock
          {...baseProps}
          composer={createComposer()}
          showProjectSelector
          store={store}
        />
      </div>,
    );

    fireEvent.click(screen.getByRole('button', { name: '选择项目' }));
    const menu = await screen.findByRole('menu');
    fireEvent.click(within(menu).getByRole('menuitem', { name: 'repo/side-project' }));

    expect(store.setActiveProjectPath).toHaveBeenCalledWith('/repo/side-project');
  });

  it('exposes an accessible submit anchor only in send mode', () => {
    const props = { ...baseProps, composer: createComposer(), store: createStore(), sendMessage: vi.fn() };

    render(<ComposerDock {...props} />);

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

    render(<ComposerDock {...props} />);

    const submitButton = screen.getByTestId('composer-submit');
    expect(submitButton).toBe(screen.getByRole('button', { name: '发送消息' }));
    expect(submitButton).toBeDisabled();
    expect(screen.queryByTestId('composer-interrupt')).not.toBeInTheDocument();

    fireEvent.click(submitButton);

    expect(props.sendMessage).not.toHaveBeenCalled();
  });

  it('uses the floating class for the new-chat intro composer', () => {
    render(<ComposerDock {...baseProps} floating composer={createComposer()} store={createStore()} />);

    expect(screen.getByTestId('composer-dock')).toHaveClass('composer--floating');
    expect(screen.getByTestId('composer-dock')).not.toHaveClass('composer--docked');
    expect(screen.getByTestId('composer-dock').querySelector('.composer-card')).toBeInTheDocument();
    expect(screen.getByText('燧元 AI 可能出错，请核对重要信息。')).toHaveClass('composer-disclaimer');
    expect(screen.queryByText('Suiyuan AI can make mistakes. Consider verifying critical information.')).not.toBeInTheDocument();
  });

  it('switches the primary action to interrupt when the active thread is interruptible', () => {
    const store = createStore({ hasInterruptibleThreadAction: vi.fn(() => true) });

    render(<ComposerDock {...baseProps} composer={createComposer()} store={store} />);

    const interruptButton = screen.getByTestId('composer-interrupt');
    expect(interruptButton).toBe(screen.getByRole('button', { name: '中断当前执行' }));
    expect(interruptButton).toHaveAccessibleName('中断当前执行');
    expect(interruptButton).toBeEnabled();
    expect(screen.queryByTestId('composer-submit')).not.toBeInTheDocument();

    fireEvent.click(interruptButton);

    expect(store.interruptActiveThread).toHaveBeenCalledTimes(1);
  });
});
