import React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { RuntimeActivityPanel } from './RuntimeActivityPanel.jsx';
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

function renderRuntimeActivityPanel(overrides = {}) {
  return render(
    <RuntimeActivityPanel
      activityStats={{}}
      tokenUsage={null}
      warnings={[]}
      runtimeResults={[]}
      activityPanelMax={240}
      activityPanelHeight={128}
      activityPanelMinHeight={64}
      formatTime={(value) => (value ? '12:34' : '--:--')}
      onResizeKeyDown={vi.fn()}
      onResizeStart={vi.fn()}
      {...overrides}
    />,
  );
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

describe('RuntimeActivityPanel', () => {
  it('renders runtime stats and context usage from props', () => {
    renderRuntimeActivityPanel({
      activityStats: {
        toolCalls: {
          mcp__lsp__lsp_grep: 2,
          json_render: 1,
          mcp__playwright__browser_click: 3,
          go_run: 4,
        },
        commands: 5,
        fileEdits: 6,
      },
      tokenUsage: { usedPercent: 42.5 },
    });

    expect(screen.getByRole('button', { name: 'LSP (8 tools) 调用次数' })).toHaveTextContent('2');
    expect(screen.getByRole('button', { name: 'JSON-Render 调用次数' })).toHaveTextContent('1');
    expect(screen.getByRole('button', { name: 'Playwright 调用次数' })).toHaveTextContent('3');
    expect(screen.getByRole('button', { name: 'go-run 调用次数' })).toHaveTextContent('4');
    expect(screen.getByRole('button', { name: '命令 调用次数' })).toHaveTextContent('5');
    expect(screen.getByRole('button', { name: '文件 调用次数' })).toHaveTextContent('6');
    expect(screen.getByRole('button', { name: '工具调用总数' })).toHaveTextContent('10');
    expect(screen.getByLabelText('上下文使用率 42.5%')).toHaveTextContent('42.5% context');
  });

  it('opens stat detail and warning popovers without backend calls', () => {
    renderRuntimeActivityPanel({
      activityStats: {
        toolCalls: {
          mcp__lsp__lsp_grep: 2,
        },
      },
      warnings: [{
        id: 'warn-1',
        timestamp: '2026-06-11T06:00:00.123456Z',
        message: '权限告警',
        detail: 'missing permission',
        occurrenceCount: 2,
      }],
    });

    fireEvent.click(screen.getByRole('button', { name: 'LSP (8 tools) 调用次数' }));
    expect(screen.getByTestId('runtime-stat-tooltip')).toHaveTextContent('grep');
    expect(screen.getByTestId('runtime-stat-tooltip')).toHaveTextContent('2');

    fireEvent.click(screen.getByText('权限告警').closest('button'));
    expect(screen.getByText('×2')).toBeInTheDocument();
    expect(screen.getByTestId('warning-log-popover')).toHaveTextContent('missing permission');
  });

  it('redacts runtime result popover fields before display', () => {
    renderRuntimeActivityPanel({
      runtimeResults: [{
        id: 'result-1',
        timestamp: '2026-06-11T06:00:00.123456Z',
        event: 'api.rpc.done',
        message: 'thread/messages 返回 · {"total":1}',
        fields: {
          method: 'thread/messages',
          req_id: 9,
          result_preview: JSON.stringify({
            messages: [{
              id: 1,
              content: 'private prompt body',
              path: '/home/l4place/private-project/secret.txt',
              api_key: 'sk-live-secret',
              count: 2,
            }],
            total: 1,
          }),
        },
      }],
    });

    fireEvent.click(screen.getByText('thread/messages 返回').closest('button'));
    const popover = screen.getByTestId('warning-log-popover');
    expect(popover).toHaveTextContent('thread/messages');
    expect(popover).toHaveTextContent('"req_id":9');
    expect(popover).not.toHaveTextContent('private prompt body');
    expect(popover).not.toHaveTextContent('/home/l4place');
    expect(popover).not.toHaveTextContent('sk-live-secret');
    expect(popover).not.toHaveTextContent('secret.txt');
  });

  it('hides runtime log lines when the activity panel is collapsed', () => {
    renderRuntimeActivityPanel({
      activityPanelHeight: 64,
      warnings: [{ id: 'warn-1', message: '权限告警', detail: 'missing permission' }],
    });

    expect(screen.getByLabelText('工具使用面板')).toHaveClass('is-log-collapsed');
    expect(screen.queryByTestId('warning-log-panel')).not.toBeInTheDocument();
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
