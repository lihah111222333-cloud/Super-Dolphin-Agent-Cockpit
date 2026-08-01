import React from 'react';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { RuntimeActivityPanel } from '../runtime/RuntimeActivityPanel.jsx';
import { RuntimeDiffView } from '../runtime/RuntimeDiffView.jsx';
import { RuntimeToolbar } from '../runtime/RuntimeToolbar.jsx';
import { RightPanelHeader } from './RightPanelHeader.jsx';

const diffFile = {
  filename: 'src/App.jsx',
  additions: 2,
  deletions: 1,
  text: '+new line\n-old line',
};

const PRIVATE_WARNING_DETAIL = {
  path: '/home/l4place/private-project/secret.txt',
  api_key: 'sk-live-secret',
  prompt: 'private prompt body',
};

const PRIVATE_WARNING_FIELDS = {
  method: 'thread/messages',
  req_id: 9,
  path: '/home/l4place/private-project/secret.txt',
  prompt: 'private prompt body',
  api_key: 'sk-live-secret',
  rawPreview: {
    content: 'private prompt body',
  },
};

function parseLineEntries() {
  return [
    { key: 'line-1', type: 'add', oldNo: '', newNo: 1, prefix: '+', content: 'new line' },
    { key: 'line-2', type: 'del', oldNo: 1, newNo: '', prefix: '-', content: 'old line' },
  ];
}

function largeDiffFile(lineCount = 240) {
  return {
    filename: 'src/Large.jsx',
    additions: lineCount,
    deletions: 0,
    text: Array.from({ length: lineCount }, (_, index) => `+line ${index}`).join('\n'),
  };
}

function parseLargeLineEntries(text) {
  return text.split('\n').map((line, index) => ({
    key: `large-line-${index}`,
    type: 'add',
    oldNo: '',
    newNo: index + 1,
    prefix: '+',
    content: line.slice(1),
  }));
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
  it('renders labeled diff metrics with units and keyboard-accessible tooltips', () => {
    render(<RuntimeToolbar diffSummary={{ fileCount: 1, changedLines: 3, additions: 2, deletions: 1 }} onShowAgents={vi.fn()} />);

    const fileStat = screen.getByTestId('runtime-toolbar-stat-files');
    const lineStat = screen.getByTestId('runtime-toolbar-stat-changedLines');
    const addStat = screen.getByTestId('runtime-toolbar-stat-additions');
    const delStat = screen.getByTestId('runtime-toolbar-stat-deletions');
    expect(fileStat).toHaveTextContent('文件');
    expect(fileStat).toHaveTextContent('1');
    expect(fileStat).toHaveTextContent('个');
    expect(lineStat).toHaveTextContent('变更');
    expect(lineStat).toHaveTextContent('3');
    expect(lineStat).toHaveTextContent('行');
    expect(addStat).toHaveTextContent('新增');
    expect(addStat).toHaveTextContent('+2');
    expect(delStat).toHaveTextContent('删除');
    expect(delStat).toHaveTextContent('-1');
    expect(fileStat).toHaveAttribute('aria-label', '代码变更文件数：1 个');
    expect(lineStat).toHaveAttribute('aria-label', '代码变更行数：3 行');
    expect(addStat).toHaveAttribute('aria-label', '代码新增行数：+2 行');
    expect(delStat).toHaveAttribute('aria-label', '代码删除行数：-1 行');

    fileStat.focus();
    expect(fileStat).toHaveFocus();
    fireEvent.click(fileStat);
    expect(screen.getByTestId('runtime-toolbar-tooltip')).toHaveTextContent('本次运行中产生代码变更的文件总数');
  });

  it('renders an Agents switch entry in the runtime toolbar', () => {
    const onShowAgents = vi.fn();
    render(
      <RightPanelHeader activeView="runtime" onCollapse={vi.fn()} onShowAgents={onShowAgents}>
        <RuntimeToolbar diffSummary={{ fileCount: 0, changedLines: 0, additions: 0, deletions: 0 }} />
      </RightPanelHeader>,
    );

    const switchButton = screen.getByTestId('runtime-show-agents');
    expect(switchButton).toHaveAttribute('aria-label', '切换到 Agent 看板');
    switchButton.focus();
    expect(switchButton).toHaveFocus();
    fireEvent.click(switchButton);
    expect(onShowAgents).toHaveBeenCalledTimes(1);
  });

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
    expect(screen.getByTestId('warning-log-popover')).not.toHaveTextContent('missing permission');
    expect(screen.getByTestId('warning-log-popover')).toHaveTextContent('[redacted]');
  });

  it('redacts warning popover details before display', () => {
    renderRuntimeActivityPanel({
      warnings: [{
        id: 'warn-1',
        timestamp: '2026-06-11T06:00:00.123456Z',
        message: 'RPC 失败',
        detail: JSON.stringify(PRIVATE_WARNING_DETAIL),
        fields: PRIVATE_WARNING_FIELDS,
      }],
    });

    fireEvent.click(screen.getByText('RPC 失败').closest('button'));
    const popover = screen.getByTestId('warning-log-popover');
    expect(popover).toHaveTextContent('thread/messages');
    expect(popover).toHaveTextContent('"req_id":9');
    expect(popover).not.toHaveTextContent('/home/l4place');
    expect(popover).not.toHaveTextContent('secret.txt');
    expect(popover).not.toHaveTextContent('private prompt body');
    expect(popover).not.toHaveTextContent('sk-live-secret');
  });

  it('redacts runtime result popover fields before display', () => {
    const resultPreview = {
      messages: [{
        id: 1,
        content: 'private prompt body',
        path: '/home/l4place/private-project/secret.txt',
        api_key: 'sk-live-secret',
        count: 2,
      }],
      total: 1,
    };
    renderRuntimeActivityPanel({
      runtimeResults: [{
        id: 'result-1',
        timestamp: '2026-06-11T06:00:00.123456Z',
        event: 'api.rpc.done',
        message: 'thread/messages 返回 · {"total":1}',
        fields: {
          method: 'thread/messages',
          req_id: 9,
          result_preview: JSON.stringify(resultPreview),
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

  it('keeps runtime popovers mutually exclusive and dismisses through Escape', async () => {
    renderRuntimeActivityPanel({
      activityStats: {
        toolCalls: {
          mcp__lsp__lsp_grep: 2,
        },
      },
      warnings: [{
        id: 'warning-1',
        method: 'turn/start',
        message: 'rpc.failed',
        status: 'error',
      }],
    });

    fireEvent.click(screen.getByRole('button', { name: 'LSP (8 tools) 调用次数' }));
    expect(await screen.findByTestId('runtime-stat-tooltip')).toBeInTheDocument();
    fireEvent.click(screen.getByText('rpc.failed').closest('button'));

    await waitFor(() => expect(screen.queryByTestId('runtime-stat-tooltip')).not.toBeInTheDocument());
    expect(await screen.findByTestId('warning-log-popover')).toBeInTheDocument();

    fireEvent.keyDown(screen.getByRole('dialog', { name: 'rpc.failed' }), { key: 'Escape' });
    await waitFor(() => expect(screen.queryByTestId('warning-log-popover')).not.toBeInTheDocument());
  });

  it('does not close an open runtime popover when dragging the activity resizer', async () => {
    renderRuntimeActivityPanel({
      warnings: [{
        id: 'warning-1',
        method: 'turn/start',
        message: 'rpc.failed',
        status: 'error',
      }],
    });

    fireEvent.click(screen.getByText('rpc.failed').closest('button'));
    fireEvent.pointerDown(screen.getByTestId('activity-panel-resizer'));

    expect(await screen.findByTestId('warning-log-popover')).toBeInTheDocument();
  });

  it('hides runtime log lines when the activity panel is collapsed', () => {
    renderRuntimeActivityPanel({
      activityPanelHeight: 64,
      warnings: [{ id: 'warn-1', message: '权限告警', detail: 'missing permission' }],
    });

    expect(screen.getByLabelText('工具使用面板')).toHaveClass('is-log-collapsed');
    expect(screen.queryByTestId('warning-log-panel')).not.toBeInTheDocument();
  });

  it('renders an actionable empty state when there is no runtime activity', () => {
    renderRuntimeActivityPanel();

    const panel = screen.getByTestId('warning-log-panel');
    expect(panel).toHaveTextContent('暂无运行活动');
    expect(panel).toHaveTextContent('任务开始后，事件与结果会显示在这里。');
    expect(panel).not.toHaveTextContent('--:--');
    expect(panel).not.toHaveTextContent('等待事件');
  });

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

    const empty = screen.getByTestId('diff-empty');
    expect(empty).toHaveClass('diff-empty--compact');
    expect(empty).toHaveTextContent('暂无代码变更');
    expect(empty).toHaveTextContent('继续委派修改代码的任务后');
    expect(empty).toHaveTextContent('最近活动');
  });

  it('shows time, type badge and summary for activity log lines', () => {
    renderRuntimeActivityPanel({
      warnings: [{
        id: 'warn-1',
        timestamp: '2026-06-11T06:00:00.123456Z',
        message: '权限告警',
        detail: 'missing permission',
      }],
      runtimeResults: [{
        id: 'result-1',
        timestamp: '2026-06-11T06:01:00.123456Z',
        event: 'api.rpc.done',
        message: 'thread/messages 返回 · {"total":1}',
        fields: { method: 'thread/messages' },
      }],
    });

    const panel = screen.getByTestId('warning-log-panel');
    expect(panel).toHaveAttribute('aria-label', '最近活动');
    expect(panel).toHaveTextContent('12:34');
    expect(panel).toHaveTextContent('警告');
    expect(panel).toHaveTextContent('结果');
    expect(panel).toHaveTextContent('权限告警');
    expect(panel).toHaveTextContent('thread/messages 返回');
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

  it('renders only visible rows for a large diff file', () => {
    const file = largeDiffFile();
    const { container } = render(
      <RuntimeDiffView
        diffText={file.text}
        diffSummary={{ files: [file] }}
        collapsedFiles={new Set()}
        actionNotice=""
        onLocateFile={vi.fn()}
        onOpenFile={vi.fn()}
        onToggleFile={vi.fn()}
        parseLineEntries={parseLargeLineEntries}
      />,
    );

    const renderedRows = container.querySelectorAll('.diff-line');
    expect(renderedRows.length).toBeGreaterThan(0);
    expect(renderedRows.length).toBeLessThan(80);
    expect(screen.queryByText('line 239')).not.toBeInTheDocument();
  });

  it('renders no diff rows for a collapsed file and keeps action labels accessible', () => {
    const parseCollapsedLines = vi.fn(parseLineEntries);
    const { container } = render(
      <RuntimeDiffView
        diffText="diff --git a/src/App.jsx b/src/App.jsx"
        diffSummary={{ files: [diffFile] }}
        collapsedFiles={new Set(['src/App.jsx:0'])}
        actionNotice=""
        onLocateFile={vi.fn()}
        onOpenFile={vi.fn()}
        onToggleFile={vi.fn()}
        parseLineEntries={parseCollapsedLines}
      />,
    );

    expect(container.querySelectorAll('.diff-line')).toHaveLength(0);
    expect(parseCollapsedLines).not.toHaveBeenCalled();
    expect(screen.getByRole('button', { name: '展开 src/App.jsx' })).toHaveAttribute('aria-expanded', 'false');
    expect(screen.getByRole('button', { name: '定位 src/App.jsx' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '打开 src/App.jsx' })).toBeInTheDocument();
  });
