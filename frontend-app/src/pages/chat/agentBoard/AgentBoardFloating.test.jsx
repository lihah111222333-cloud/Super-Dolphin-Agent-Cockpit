import React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { selectAgentBoardViewModel } from '../../../entities/client/model/helpers/agentBoard/selector.js';
import { AgentBoardFloating } from './AgentBoardFloating.jsx';

const at = '2026-07-28T08:00:00.000Z';

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

function floatingViewModel(agents, options = {}) {
  return selectAgentBoardViewModel(
    { agents, mainAgentId: options.mainAgentId ?? 'root' },
    {
      mode: 'floating',
      selectedAgentId: options.selectedAgentId ?? '',
      loading: options.loading ?? false,
      error: options.error ?? null,
    },
  );
}

function renderFloating(viewModel, overrides = {}) {
  const props = {
    viewModel,
    ...overrides,
  };
  return { props, ...render(<AgentBoardFloating {...props} />) };
}

describe('AgentBoardFloating', () => {
  it('renders title, total count and status summary', () => {
    const viewModel = floatingViewModel([
      agent('root'),
      agent('child-a', { parentAgentId: 'root' }),
      agent('child-b', { parentAgentId: 'root', status: 'awaiting_user_input' }),
    ]);

    renderFloating(viewModel);

    expect(screen.getByTestId('agent-board-floating')).toBeInTheDocument();
    expect(screen.getByText('Agents')).toBeInTheDocument();
    expect(screen.getByLabelText('Agent 总数 3')).toHaveTextContent('3');
    expect(screen.getByTestId('agent-count-running')).toHaveTextContent('运行中 2');
    expect(screen.getByTestId('agent-count-waiting')).toHaveTextContent('等待 1');
    expect(screen.getByTestId('agent-count-completed')).toHaveTextContent('完成 0');
    expect(screen.getByTestId('agent-count-failed')).toHaveTextContent('失败 0');
    expect(screen.getByTestId('agent-board-floating').lastElementChild).toBe(screen.getByRole('button', { name: '收起 Agents 状态' }));
  });

  it('只展示 selector 汇总，不重排或复制 Agent 业务状态', () => {
    const failure = { kind: 'failure', reason: '子任务执行失败', code: 'provider', recoverable: true, completedAt: at };
    const viewModel = floatingViewModel([
      agent('root'),
      agent('ok', { parentAgentId: 'root', status: 'idle', outcome: { kind: 'success', summary: '完成', recoverable: null, completedAt: at } }),
      agent('bad', { parentAgentId: 'root', status: 'failed', outcome: failure }),
    ]);

    renderFloating(viewModel);

    expect(screen.queryByTestId('agent-entry-root')).toBeNull();
    expect(screen.queryByTestId('agent-entry-ok')).toBeNull();
    expect(screen.queryByTestId('agent-entry-bad')).toBeNull();
    expect(screen.getByLabelText('Agent 总数 3')).toHaveTextContent('3');
    expect(screen.getByTestId('agent-count-failed')).toHaveClass('agent-counts__item--alert');
  });

  it('shows loading, error and empty states without fabricating data', () => {
    const { props, rerender } = renderFloating(floatingViewModel([agent('root')], { loading: true }));
    expect(screen.getByText('正在加载 Agent 状态…')).toBeInTheDocument();
    expect(screen.queryByTestId('agent-counts')).toBeNull();

    rerender(<AgentBoardFloating {...props} viewModel={floatingViewModel([], { error: '后端连接断开' })} />);
    expect(screen.getByRole('alert')).toHaveTextContent('后端连接断开');

    rerender(<AgentBoardFloating {...props} viewModel={floatingViewModel([])} />);
    expect(screen.getByText('暂无 Agent')).toBeInTheDocument();
  });

  it('shows a dedicated hint when only the root agent exists', () => {
    renderFloating(floatingViewModel([agent('root')]));

    expect(screen.getByText('暂无子 Agent，等待委派')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /在右侧栏查看 Agent/ })).toBeNull();
  });

  it('converges to a compact card on narrow viewports while staying visible', () => {
    const viewModel = floatingViewModel([agent('root'), agent('worker', { parentAgentId: 'root' })]);

    renderFloating(viewModel, { compact: true });

    const card = screen.getByTestId('agent-board-floating');
    expect(card).toHaveClass('agent-board-floating--compact');
    expect(screen.getByTestId('agent-counts')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /在右侧栏查看 Agent/ })).toBeNull();
  });

  it('renders an idle pill and restores the full floating board on demand', () => {
    const onCollapsedChange = vi.fn();
    renderFloating(floatingViewModel([agent('root', { status: 'idle' })]), { collapsed: true, onCollapsedChange });

    expect(screen.getByRole('button', { name: 'Agents状态' })).toHaveAttribute('aria-expanded', 'false');
    expect(screen.queryByTestId('agent-counts')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Agents状态' }));
    expect(onCollapsedChange).toHaveBeenCalledWith(false);
  });
});
