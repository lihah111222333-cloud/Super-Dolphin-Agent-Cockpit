import React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { ComposerModelSelector } from './ComposerModelSelector.jsx';

function createStore(overrides = {}) {
  return {
    provider: 'codex',
    providerConfig: { provider: 'codex', model: 'gpt-5.5', effort: 'xhigh' },
    threadConfigByThread: {
      thread1: {
        provider: 'codex',
        supportsThreadOverride: true,
        effective: { model: 'gpt-5.5', effort: 'xhigh' },
        override: {},
      },
    },
    threadConfigLoadingByThread: {},
    threadConfigSaving: false,
    loadThreadConfig: vi.fn(),
    restoreComposerModelInheritance: vi.fn(),
    saveComposerModelConfig: vi.fn(),
    ...overrides,
  };
}

describe('ComposerModelSelector', () => {
  it('renders the active model label and saves thread overrides', () => {
    const store = createStore();

    render(<ComposerModelSelector store={store} activeThreadId="thread1" />);

    fireEvent.click(screen.getByRole('button', { name: '选择模型' }));
    fireEvent.change(screen.getByLabelText('推理强度'), { target: { value: 'high' } });

    expect(screen.getByRole('button', { name: '选择模型' })).toHaveTextContent('5.5 超高');
    expect(store.saveComposerModelConfig).toHaveBeenCalledWith({ threadId: 'thread1', model: '', effort: 'high' });
  });

  it('disables the selector when project actions are blocked', () => {
    const store = createStore();

    render(<ComposerModelSelector store={store} activeThreadId="thread1" disabled />);

    const button = screen.getByRole('button', { name: '选择模型' });
    expect(button).toBeDisabled();
    expect(button).toHaveAttribute('title', '请先连接后端并选择项目');
  });
});
