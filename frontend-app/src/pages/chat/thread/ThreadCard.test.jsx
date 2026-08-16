import React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { ThreadCard } from './ThreadCard.jsx';

function renderThreadCard() {
  const store = {
    archiveThread: vi.fn(),
    threadArchiveLoadingByThread: {},
    toggleThreadPin: vi.fn(),
  };
  render(
    <ThreadCard
      active
      deleting={false}
      editing={false}
      editingName=""
      hoveredArchiveThreadId=""
      hoveredPinThreadId=""
      onBeginDelete={vi.fn()}
      onBeginRename={vi.fn()}
      onCancelDelete={vi.fn()}
      onCancelRename={vi.fn()}
      onConfirmDelete={vi.fn()}
      onRenameBlur={vi.fn()}
      onSetEditingName={vi.fn()}
      onSetHoveredArchiveThreadId={vi.fn()}
      onSetHoveredPinThreadId={vi.fn()}
      onSubmitRename={vi.fn()}
      renaming={false}
      store={store}
      thread={{ id: 'thread-1', name: 'Active thread', provider: 'codex', status: 'idle' }}
    />,
  );
  return store;
}

describe('ThreadCard', () => {
  it('routes pin actions through the store', () => {
    const store = renderThreadCard();

    fireEvent.click(screen.getByRole('button', { name: '置顶对话' }));

    expect(store.toggleThreadPin).toHaveBeenCalledWith('thread-1');
  });
});
