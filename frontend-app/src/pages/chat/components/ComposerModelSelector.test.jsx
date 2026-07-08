import React from 'react';
import { act, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { renderWithQueryClient as renderModelSelector } from '../../../__tests__/reactQueryRender.jsx';
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

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((promiseResolve, promiseReject) => {
    resolve = promiseResolve;
    reject = promiseReject;
  });
  return { promise, reject, resolve };
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe('ComposerModelSelector', () => {
  it('renders the active model label and saves thread overrides', async () => {
    const store = createStore();

    renderModelSelector(<ComposerModelSelector store={store} activeThreadId="thread1" />);

    const trigger = screen.getByRole('button', { name: '选择模型' });
    fireEvent.click(trigger);
    fireEvent.change(screen.getByLabelText('推理强度'), { target: { value: 'high' } });

    expect(trigger).toHaveTextContent('5.5 超高');
    await waitFor(() => {
      expect(store.saveComposerModelConfig).toHaveBeenCalledWith({ threadId: 'thread1', model: '', effort: 'high' });
    });
  });

  it('closes with Escape and restores focus to the selector button', async () => {
    const store = createStore();
    renderModelSelector(<ComposerModelSelector store={store} activeThreadId="thread1" />);

    const trigger = screen.getByRole('button', { name: '选择模型' });
    trigger.focus();
    fireEvent.click(trigger);
    const dialog = await screen.findByRole('dialog', { name: '模型配置' });

    fireEvent.keyDown(dialog, { key: 'Escape' });

    await waitFor(() => {
      expect(screen.queryByRole('dialog', { name: '模型配置' })).not.toBeInTheDocument();
      expect(trigger).toHaveFocus();
    });
  });

  it('closes when pressing outside the selector popover', async () => {
    const store = createStore();
    renderModelSelector(
      <>
        <ComposerModelSelector store={store} activeThreadId="thread1" />
        <button type="button">外部区域</button>
      </>,
    );

    const outsideButton = screen.getByRole('button', { name: '外部区域' });
    fireEvent.click(screen.getByRole('button', { name: '选择模型' }));
    expect(await screen.findByRole('dialog', { name: '模型配置' })).toBeInTheDocument();

    expect(outsideButton).toBeInTheDocument();
    fireEvent.pointerDown(outsideButton, { button: 0 });
    fireEvent.click(outsideButton, { button: 0 });
    fireEvent.mouseDown(outsideButton, { button: 0 });
    fireEvent.mouseUp(outsideButton, { button: 0 });

    await waitFor(() => {
      expect(screen.queryByRole('dialog', { name: '模型配置' })).not.toBeInTheDocument();
    });
  });

  it('ignores async loaded thread config after unmount', async () => {
    const pendingConfig = deferred();
    const store = createStore({
      loadThreadConfig: vi.fn(() => pendingConfig.promise),
    });
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {});
    const { unmount } = renderModelSelector(<ComposerModelSelector store={store} activeThreadId="thread1" />);

    fireEvent.click(screen.getByRole('button', { name: '选择模型' }));
    expect(await screen.findByRole('dialog', { name: '模型配置' })).toBeInTheDocument();
    unmount();

    await act(async () => {
      pendingConfig.resolve({
        effective: { model: 'gpt-5.4', effort: 'high' },
        override: { model: 'gpt-5.4', effort: 'high' },
      });
      await pendingConfig.promise;
    });

    expect(consoleError).not.toHaveBeenCalled();
  });

  it('preserves inherited model when only effort is changed', async () => {
    const store = createStore();

    renderModelSelector(<ComposerModelSelector store={store} activeThreadId="thread1" />);

    fireEvent.click(screen.getByRole('button', { name: '选择模型' }));
    fireEvent.change(screen.getByLabelText('推理强度'), { target: { value: 'medium' } });

    await waitFor(() => expect(store.saveComposerModelConfig).toHaveBeenCalledWith({ threadId: 'thread1', model: '', effort: 'medium' }));
  });

  it('disables the selector when project actions are blocked', () => {
    const store = createStore();

    renderModelSelector(<ComposerModelSelector store={store} activeThreadId="thread1" disabled />);

    const button = screen.getByRole('button', { name: '选择模型' });
    expect(button).toBeDisabled();
  });
});
