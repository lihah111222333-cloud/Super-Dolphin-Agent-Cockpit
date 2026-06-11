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
  selectFiles: vi.fn(),
  sendMessage: vi.fn(),
  sending: false,
  setDraft: vi.fn(),
  showProviderToggle: true,
};

describe('ComposerDock', () => {
  it('routes primary, attach, fork, paste, and enter actions through props', () => {
    const composer = createComposer();
    const store = createStore();
    const props = { ...baseProps, composer, store, selectFiles: vi.fn(), sendMessage: vi.fn(), setDraft: vi.fn() };

    render(<ComposerDock {...props} />);

    fireEvent.click(screen.getByRole('button', { name: '添加文件' }));
    fireEvent.click(screen.getByRole('button', { name: '继承当前对话' }));
    fireEvent.click(screen.getByRole('button', { name: '发送消息' }));
    fireEvent.paste(screen.getByRole('textbox', { name: '输入给 Agent 的内容' }));
    fireEvent.keyDown(screen.getByRole('textbox', { name: '输入给 Agent 的内容' }), { key: 'Enter' });

    expect(props.selectFiles).toHaveBeenCalledTimes(1);
    expect(store.openForkDraft).toHaveBeenCalledTimes(1);
    expect(props.sendMessage).toHaveBeenCalledTimes(2);
    expect(composer.handlePaste).toHaveBeenCalledTimes(1);
  });

  it('switches the primary action to interrupt when the active thread is interruptible', () => {
    const store = createStore({ hasInterruptibleThreadAction: vi.fn(() => true) });

    render(<ComposerDock {...baseProps} composer={createComposer()} store={store} />);

    fireEvent.click(screen.getByRole('button', { name: '中断当前执行' }));

    expect(store.interruptActiveThread).toHaveBeenCalledTimes(1);
  });
});
