import React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
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
  it('routes primary, attach, paste, and enter actions through props without reserved controls', () => {
    const composer = createComposer();
    const store = createStore();
    const props = { ...baseProps, composer, store, selectFiles: vi.fn(), sendMessage: vi.fn(), setDraft: vi.fn() };

    const { container } = render(<ComposerDock {...props} />);

    const addFileButton = screen.getByRole('button', { name: '添加文件' });
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
