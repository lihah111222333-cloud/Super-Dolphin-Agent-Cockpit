import React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { selectAgentBoardViewModel } from '../../../entities/client/model/helpers/agentBoard/selector.js';
import { AgentBoardPanel } from './AgentBoardPanel.jsx';

const at = '2026-07-28T08:00:00.000Z';
const formatTime = (value) => `T:${value}`;

function agent(id, { status = 'turn_running', outcome = null, parentAgentId = '', progress } = {}) {
  return {
    id,
    threadId: `thread-${id}`,
    ...(parentAgentId ? { parentAgentId } : {}),
    name: `Agent ${id}`,
    assignment: { title: `任务 ${id}`, description: `描述 ${id}`, assignedAt: at },
    progress: { status, currentStep: null, completedSteps: null, totalSteps: null, updatedAt: at, ...progress },
    outcome,
  };
}

function dockedViewModel(agents, options = {}) {
  return selectAgentBoardViewModel(
    { agents, mainAgentId: options.mainAgentId ?? 'root' },
    {
      mode: 'docked',
      selectedAgentId: options.selectedAgentId ?? 'root',
      loading: options.loading ?? false,
      error: options.error ?? null,
    },
  );
}

function renderPanel(viewModel, overrides = {}) {
  const props = {
    viewModel,
    formatTime,
    onSelectAgent: vi.fn(),
    onCollapse: vi.fn(),
    onShowRuntime: vi.fn(),
    ...overrides,
  };
  return { props, ...render(<AgentBoardPanel {...props} />) };
}

describe('AgentBoardPanel', () => {
  it('marks the active tab position for the sliding indicator', () => {
    renderPanel(dockedViewModel([]));

    expect(screen.getByRole('group', { name: '右侧栏视图切换' })).toHaveClass('is-agents');
  });
  it('shows structural counts for running, waiting, completed and failed', () => {
    const viewModel = dockedViewModel([
      agent('root'),
      agent('waiting', { parentAgentId: 'root', status: 'turn_queued' }),
      agent('done', { parentAgentId: 'root', status: 'idle', outcome: { kind: 'success', summary: 's', recoverable: null, completedAt: at } }),
      agent('bad', { parentAgentId: 'root', status: 'failed', outcome: { kind: 'failure', reason: 'r', code: 'c', recoverable: false, completedAt: at } }),
    ]);

    renderPanel(viewModel);

    expect(screen.getByTestId('agent-count-running')).toHaveTextContent('运行中 1');
    expect(screen.getByTestId('agent-count-waiting')).toHaveTextContent('等待 1');
    expect(screen.getByTestId('agent-count-completed')).toHaveTextContent('完成 1');
    expect(screen.getByTestId('agent-count-failed')).toHaveTextContent('失败 1');
  });

  it('renders parent-child hierarchy with indentation and selected state', () => {
    const viewModel = dockedViewModel([
      agent('root'),
      agent('child', { parentAgentId: 'root' }),
      agent('grandchild', { parentAgentId: 'child' }),
    ], { selectedAgentId: 'child' });

    const { props } = renderPanel(viewModel);

    const root = screen.getByTestId('agent-node-root');
    const child = screen.getByTestId('agent-node-child');
    const grandchild = screen.getByTestId('agent-node-grandchild');
    expect(root.style.getPropertyValue('--agent-depth')).toBe('0');
    expect(child.style.getPropertyValue('--agent-depth')).toBe('1');
    expect(grandchild.style.getPropertyValue('--agent-depth')).toBe('2');
    expect(root).toHaveTextContent('根');
    expect(child).toHaveAttribute('aria-pressed', 'true');
    expect(root).toHaveAttribute('aria-pressed', 'false');

    fireEvent.click(grandchild);
    expect(props.onSelectAgent).toHaveBeenCalledWith('grandchild');
  });

  it('shows assignment facts and the real status of the selected agent', () => {
    const viewModel = dockedViewModel([agent('root'), agent('worker', { parentAgentId: 'root' })], { selectedAgentId: 'worker' });

    renderPanel(viewModel);

    const detail = screen.getByTestId('agent-detail');
    expect(detail).toHaveTextContent('Agent worker');
    expect(detail).toHaveTextContent('任务 worker');
    expect(detail).toHaveTextContent('描述 worker');
    expect(detail).toHaveTextContent(`T:${at}`);
    expect(detail).toHaveTextContent('运行中（turn_running）');
  });

  it('shows success summary exactly as provided', () => {
    const outcome = { kind: 'success', summary: '已生成 3 个文件并通过测试', recoverable: null, completedAt: at };
    const viewModel = dockedViewModel([agent('root', { status: 'idle', outcome })]);

    renderPanel(viewModel);

    expect(screen.getByTestId('agent-outcome-summary')).toHaveTextContent('已生成 3 个文件并通过测试');
  });

  it('shows failure reason exactly with code and recoverable flag', () => {
    const outcome = { kind: 'failure', reason: 'provider 返回 429', code: 'rate_limit', recoverable: true, completedAt: at };
    const viewModel = dockedViewModel([agent('root', { status: 'failed', outcome })]);

    renderPanel(viewModel);

    expect(screen.getByTestId('agent-outcome-reason')).toHaveTextContent('provider 返回 429');
    expect(screen.getByTestId('agent-outcome')).toHaveTextContent('错误码：rate_limit');
    expect(screen.getByTestId('agent-outcome')).toHaveTextContent('可恢复：是');
  });

  it('shows stopped reason exactly', () => {
    const outcome = { kind: 'stopped', reason: '用户手动停止', recoverable: null, completedAt: at };
    const viewModel = dockedViewModel([agent('root', { status: 'stopped', outcome })]);

    renderPanel(viewModel);

    expect(screen.getByTestId('agent-outcome-reason')).toHaveTextContent('用户手动停止');
  });

  it('never fabricates an outcome when outcome is null', () => {
    const viewModel = dockedViewModel([agent('root', { status: 'stopped' })]);

    renderPanel(viewModel);

    expect(screen.getByTestId('agent-outcome')).toHaveTextContent('尚无最终结果');
    expect(screen.queryByTestId('agent-outcome-summary')).toBeNull();
    expect(screen.queryByTestId('agent-outcome-reason')).toBeNull();
  });

  it('shows 暂无结构化步骤 without any percentage when step fields are null', () => {
    const viewModel = dockedViewModel([agent('root')]);

    renderPanel(viewModel);

    const progress = screen.getByTestId('agent-progress');
    expect(progress).toHaveTextContent('暂无结构化步骤');
    expect(progress.textContent).not.toMatch(/%|百分比/);
    expect(screen.queryByTestId('agent-progress-steps')).toBeNull();
  });

  it('shows structured step counts as text without computing percentages', () => {
    const viewModel = dockedViewModel([
      agent('root', { progress: { currentStep: '运行测试', completedSteps: 2, totalSteps: 5 } }),
    ]);

    renderPanel(viewModel);

    const progress = screen.getByTestId('agent-progress');
    expect(progress).toHaveTextContent('当前步骤：运行测试');
    expect(progress).toHaveTextContent('已完成 2 / 5 步');
    expect(progress.textContent).not.toMatch(/%/);
  });

  it('shows loading, error and empty states', () => {
    const { props, rerender } = renderPanel(dockedViewModel([agent('root')], { loading: true }));
    expect(screen.getByTestId('agent-board-loading')).toHaveTextContent('正在加载 Agent 状态…');

    rerender(<AgentBoardPanel {...props} viewModel={dockedViewModel([], { error: '快照同步失败' })} />);
    expect(screen.getByTestId('agent-board-error')).toHaveTextContent('快照同步失败');

    rerender(<AgentBoardPanel {...props} viewModel={dockedViewModel([])} />);
    expect(screen.getByTestId('agent-board-empty')).toHaveTextContent('暂无 Agent');
  });

  it('shows a hint when only the root agent exists', () => {
    renderPanel(dockedViewModel([agent('root')]));

    expect(screen.getByText('暂无子 Agent，等待委派')).toBeInTheDocument();
  });

  it('exposes runtime switch and collapse entries with aria attributes', () => {
    const { props } = renderPanel(dockedViewModel([agent('root')]));

    const runtimeTab = screen.getByTestId('agent-board-show-runtime');
    expect(runtimeTab).toHaveAttribute('aria-label', '切换到运行时视图');
    expect(runtimeTab).toHaveAttribute('aria-pressed', 'false');
    runtimeTab.focus();
    expect(runtimeTab).toHaveFocus();
    fireEvent.click(runtimeTab);
    expect(props.onShowRuntime).toHaveBeenCalled();

    const collapse = screen.getByTestId('agent-board-collapse');
    expect(collapse).toHaveAttribute('aria-label', '收起 Agent 看板');
    fireEvent.click(collapse);
    expect(props.onCollapse).toHaveBeenCalled();

    expect(screen.getByTestId('agent-board-tab-agents')).toHaveAttribute('aria-pressed', 'true');
  });
});
