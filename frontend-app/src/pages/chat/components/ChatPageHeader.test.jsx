import React from 'react';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { ChatPageHeader } from './ChatPageHeader.jsx';

function createStore(overrides = {}) {
  return {
    activeThreadId: '',
    hasActiveThreadActions: () => true,
    hasInterruptibleThreadAction: () => true,
    openNewWindow: vi.fn(),
    copyActiveThreadInfo: vi.fn(),
    openForkDraft: vi.fn(),
    interruptActiveThread: vi.fn(),
    forceCompleteActiveThread: vi.fn(),
    recoverActiveThread: vi.fn(),
    ...overrides,
  };
}

describe('ChatPageHeader', () => {
  it('opens the actions menu and runs menu actions', async () => {
    const store = createStore();

    render(
      <ChatPageHeader
        store={store}
        projectPath="D:/project/Super-Dolphin"
        rightPanelOpen={false}
        setRightPanelOpen={vi.fn()}
      />
    );

    fireEvent.click(screen.getByRole('button', { name: '\u804a\u5929\u64cd\u4f5c' }));

    const menu = await screen.findByTestId('chat-actions-menu');
    fireEvent.click(within(menu).getByRole('menuitem', { name: '\u65b0\u7a97\u53e3\uff08\u72ec\u7acb\u8fdb\u7a0b\uff09' }));

    expect(store.openNewWindow).toHaveBeenCalledTimes(1);
  });

  it('opens actions menu with keyboard and restores focus on Escape', async () => {
    const store = createStore();

    render(
      <ChatPageHeader
        store={store}
        projectPath="D:/project/Super-Dolphin"
        rightPanelOpen={false}
        setRightPanelOpen={vi.fn()}
      />
    );

    const trigger = screen.getByRole('button', { name: '\u804a\u5929\u64cd\u4f5c' });
    trigger.focus();
    fireEvent.keyDown(trigger, { key: 'ArrowDown' });

    expect(await screen.findByRole('menu', { name: '\u804a\u5929\u64cd\u4f5c' })).toBeInTheDocument();

    fireEvent.keyDown(document.activeElement || trigger, { key: 'Escape' });
    await waitFor(() => expect(screen.queryByRole('menu', { name: '\u804a\u5929\u64cd\u4f5c' })).not.toBeInTheDocument());
    expect(trigger).toHaveFocus();
  });

  it('leaves feedback rendering to the page-level toast', () => {
    const store = createStore({ actionNotice: { message: 'Saved', tone: 'success' } });

    render(
      <ChatPageHeader
        store={store}
        projectPath="D:/project/Super-Dolphin"
        rightPanelOpen={false}
        setRightPanelOpen={vi.fn()}
      />
    );

    expect(screen.queryByTestId('chat-action-feedback')).not.toBeInTheDocument();
  });
});
