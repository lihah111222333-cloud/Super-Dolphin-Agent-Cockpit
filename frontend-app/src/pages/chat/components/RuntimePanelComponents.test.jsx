import React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { RuntimeDiffView } from './RuntimeDiffView.jsx';
import { RuntimeToolbar } from './RuntimeToolbar.jsx';

const diffFile = {
  filename: 'src/App.jsx',
  additions: 2,
  deletions: 1,
  text: '+new line\n-old line',
};

function parseLineEntries() {
  return [
    { key: 'line-1', type: 'add', oldNo: '', newNo: 1, prefix: '+', content: 'new line' },
    { key: 'line-2', type: 'del', oldNo: 1, newNo: '', prefix: '-', content: 'old line' },
  ];
}

describe('RuntimeToolbar', () => {
  it('renders diff counters from the provided summary', () => {
    render(<RuntimeToolbar diffSummary={{ fileCount: 1, changedLines: 3, additions: 2, deletions: 1 }} />);

    expect(screen.getByRole('button', { name: '代码变更文件数' })).toHaveTextContent('1');
    expect(screen.getByRole('button', { name: '代码变更行数' })).toHaveTextContent('3');
    expect(screen.getByLabelText('代码新增行数')).toHaveTextContent('+2');
    expect(screen.getByLabelText('代码删除行数')).toHaveTextContent('-1');
  });
});

describe('RuntimeDiffView', () => {
  it('renders an empty state when there is no diff text', () => {
    render(
      <RuntimeDiffView
        diffText=""
        diffSummary={{ files: [] }}
        collapsedFiles={new Set()}
        actionNotice=""
        onLocateFile={vi.fn()}
        onOpenFile={vi.fn()}
        onToggleFile={vi.fn()}
        parseLineEntries={parseLineEntries}
      />,
    );

    expect(screen.getByText('暂无代码变更')).toBeInTheDocument();
  });

  it('renders diff files and routes file actions through props', () => {
    const onLocateFile = vi.fn();
    const onOpenFile = vi.fn();
    const onToggleFile = vi.fn();

    render(
      <RuntimeDiffView
        diffText="diff --git a/src/App.jsx b/src/App.jsx"
        diffSummary={{ files: [diffFile] }}
        collapsedFiles={new Set()}
        actionNotice="定位到 1 个路径"
        onLocateFile={onLocateFile}
        onOpenFile={onOpenFile}
        onToggleFile={onToggleFile}
        parseLineEntries={parseLineEntries}
      />,
    );

    expect(screen.getByTestId('diff-view')).toBeInTheDocument();
    expect(screen.getByText('定位到 1 个路径')).toBeInTheDocument();
    expect(screen.getByText('src/App.jsx')).toBeInTheDocument();
    expect(screen.getByText('new line')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '定位 src/App.jsx' }));
    fireEvent.click(screen.getByRole('button', { name: '打开 src/App.jsx' }));
    fireEvent.click(screen.getByRole('button', { name: '折叠 src/App.jsx' }));

    expect(onLocateFile).toHaveBeenCalledWith(diffFile);
    expect(onOpenFile).toHaveBeenCalledWith(diffFile);
    expect(onToggleFile).toHaveBeenCalledWith('src/App.jsx:0');
  });
});
