import React from 'react';
import { fireEvent, render, screen, within } from '@testing-library/react';
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
  it('opens the actions menu and runs menu actions', () => {
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

    const menu = screen.getByTestId('chat-actions-menu');
    fireEvent.click(within(menu).getByRole('button', { name: '\u65b0\u7a97\u53e3\uff08\u72ec\u7acb\u8fdb\u7a0b\uff09' }));

    expect(store.openNewWindow).toHaveBeenCalledTimes(1);
  });

  it('shows feedback from the header model', () => {
    const store = createStore({ actionNotice: { message: 'Saved', tone: 'success' } });

    render(
      <ChatPageHeader
        store={store}
        projectPath="D:/project/Super-Dolphin"
        rightPanelOpen={false}
        setRightPanelOpen={vi.fn()}
      />
    );

    expect(screen.getByTestId('chat-action-feedback')).toHaveTextContent('Saved');
  });
});
