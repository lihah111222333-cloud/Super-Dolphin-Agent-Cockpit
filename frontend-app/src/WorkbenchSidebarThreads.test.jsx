import React from 'react';
import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { sidebarSnapshotThreads } from './WorkbenchSidebarModel.js';
import { SidebarThreadRow } from './WorkbenchSidebarThreads.jsx';

function threadActions() {
  return {
    beginDelete: vi.fn(),
    beginRename: vi.fn(),
    cancelDelete: vi.fn(),
    cancelRename: vi.fn(),
    confirmDelete: vi.fn(),
    deletingThreadId: '',
    editingName: '',
    editingThreadId: '',
    handleRenameBlur: vi.fn(),
    renamingThreadId: '',
    setEditingName: vi.fn(),
    submitRename: vi.fn(),
  };
}

describe('SidebarThreadRow', () => {
  it('renders a persisted numeric millisecond timestamp after Workbench mapping', () => {
    const [thread] = sidebarSnapshotThreads({
      threads: [{ id: 'thread-1', name: '历史会话', status: 'stopped', updated_at: 1784719357000 }],
    });

    render(
      <SidebarThreadRow
        active={false}
        label="历史会话"
        onSelect={vi.fn()}
        openLabel="打开历史会话"
        thread={thread}
        threadActions={threadActions()}
      />,
    );

    expect(screen.getByRole('button', { name: '打开历史会话' })).toBeInTheDocument();
    expect(document.querySelector('.sidebar-thread-time')).not.toBeNull();
  });
});
