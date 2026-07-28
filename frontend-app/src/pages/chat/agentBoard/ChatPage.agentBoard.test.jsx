import React from 'react';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it } from 'vitest';
import { TestChatPageWrapper, createFakeStore } from '../__tests__/chatPageTestSupport.js';

const at = '2026-07-28T08:00:00.000Z';

function agent(id, { status = 'turn_running', outcome = null, parentAgentId = '' } = {}) {
  return {
    id,
    threadId: `thread-${id}`,
    ...(parentAgentId ? { parentAgentId } : {}),
    name: `Agent ${id}`,
    assignment: { title: `任务 ${id}`, description: `描述 ${id}`, assignedAt: at },
    progress: { status, currentStep: null, completedSteps: null, totalSteps: null, updatedAt: at },
    outcome,
  };
}

function boardStore(overrides = {}) {
  return createFakeStore({
    agents: [agent('root'), agent('worker', { parentAgentId: 'root' })],
    mainAgentId: 'root',
    ...overrides,
  });
}

const originalInnerWidth = window.innerWidth;

afterEach(() => {
  window.innerWidth = originalInnerWidth;
});

it('默认在聊天页面展示常态悬浮看板，不打开右侧栏', () => {
  render(<TestChatPageWrapper store={boardStore()} projectPath="/repo/app" />);

  expect(screen.getByTestId('agent-board-floating')).toBeInTheDocument();
  expect(screen.getByTestId('agent-board-floating')).toHaveTextContent('Agents');
  expect(screen.queryByTestId('agent-board-panel')).toBeNull();
  expect(screen.queryByTestId('runtime-panel')).toBeNull();
});

it('点击展开按钮后切换为 docked 右侧栏并保留宽度调整能力', () => {
  render(<TestChatPageWrapper store={boardStore()} projectPath="/repo/app" />);

  fireEvent.click(screen.getByTestId('agent-board-expand'));

  expect(screen.getByTestId('agent-board-panel')).toBeInTheDocument();
  expect(screen.getByTestId('right-panel-resizer')).toBeInTheDocument();
  expect(screen.queryByTestId('agent-board-floating')).toBeNull();
  expect(screen.getByTestId('agent-hierarchy')).toBeInTheDocument();
});

it('收起右侧栏后恢复悬浮看板', () => {
  render(<TestChatPageWrapper store={boardStore()} projectPath="/repo/app" />);

  fireEvent.click(screen.getByTestId('agent-board-expand'));
  expect(screen.getByTestId('agent-board-panel')).toBeInTheDocument();

  fireEvent.click(screen.getByTestId('agent-board-collapse'));
  expect(screen.queryByTestId('agent-board-panel')).toBeNull();
  expect(screen.getByTestId('agent-board-floating')).toBeInTheDocument();
});

it('与 RuntimePanel 共享右侧栏：展开看板替换内容，可切回运行时视图', () => {
  render(<TestChatPageWrapper store={boardStore()} projectPath="/repo/app" rightPanelOpen={true} />);

  expect(screen.getByTestId('runtime-panel')).toBeInTheDocument();
  expect(screen.getByTestId('agent-board-floating')).toBeInTheDocument();

  fireEvent.click(screen.getByTestId('agent-board-expand'));
  expect(screen.queryByTestId('runtime-panel')).toBeNull();
  expect(screen.getByTestId('agent-board-panel')).toBeInTheDocument();

  fireEvent.click(screen.getByTestId('agent-board-show-runtime'));
  expect(screen.getByTestId('runtime-panel')).toBeInTheDocument();
  expect(screen.queryByTestId('agent-board-panel')).toBeNull();
  expect(screen.getByTestId('agent-board-floating')).toBeInTheDocument();
});

it('展开右侧看板后可选中对应 Agent 并展示层级', () => {
  render(<TestChatPageWrapper store={boardStore()} projectPath="/repo/app" />);

  fireEvent.click(screen.getByTestId('agent-board-expand'));
  fireEvent.click(screen.getByTestId('agent-node-worker'));

  expect(screen.getByTestId('agent-node-worker')).toHaveAttribute('aria-pressed', 'true');
  expect(screen.getByTestId('agent-node-root')).toHaveAttribute('aria-pressed', 'false');
  expect(screen.getByTestId('agent-detail')).toHaveTextContent('Agent worker');
  expect(screen.getByTestId('agent-detail')).toHaveTextContent('任务 worker');
});

it('选中 Agent 消失后回退到根 Agent', async () => {
  const { rerender } = render(<TestChatPageWrapper store={boardStore()} projectPath="/repo/app" />);

  fireEvent.click(screen.getByTestId('agent-board-expand'));
  fireEvent.click(screen.getByTestId('agent-node-worker'));
  expect(screen.getByTestId('agent-detail')).toHaveTextContent('Agent worker');

  rerender(<TestChatPageWrapper store={boardStore({ agents: [agent('root')] })} projectPath="/repo/app" />);

  await waitFor(() => expect(screen.getByTestId('agent-detail')).toHaveTextContent('Agent root'));
  expect(screen.getByTestId('agent-node-root')).toHaveAttribute('aria-pressed', 'true');
});

it('悬浮看板展示后端错误，不静默吞掉', () => {
  render(<TestChatPageWrapper store={boardStore({ error: '快照同步失败' })} projectPath="/repo/app" />);

  expect(screen.getByTestId('agent-board-floating')).toBeInTheDocument();
  expect(screen.getByRole('alert')).toHaveTextContent('快照同步失败');
});

it('没有 Agent 时悬浮看板展示空状态', () => {
  render(<TestChatPageWrapper store={boardStore({ agents: [], mainAgentId: '' })} projectPath="/repo/app" />);

  expect(screen.getByTestId('agent-board-floating')).toHaveTextContent('暂无 Agent');
});

it('窄窗口下收敛为紧凑卡片且不遮挡聊天输入区', () => {
  window.innerWidth = 500;
  render(<TestChatPageWrapper store={boardStore()} projectPath="/repo/app" />);

  const card = screen.getByTestId('agent-board-floating');
  expect(card).toHaveClass('agent-board-floating--compact');
  expect(screen.getByTestId('agent-board-expand')).toBeInTheDocument();
  const chatLayout = screen.getByTestId('chat-layout');
  expect(card.compareDocumentPosition(chatLayout) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  expect(screen.getByTestId('composer-dock')).toBeInTheDocument();
  expect(screen.getByTestId('composer-input')).toBeInTheDocument();
});

it('悬浮看板与展开按钮支持键盘聚焦与 aria 标注', () => {
  render(<TestChatPageWrapper store={boardStore()} projectPath="/repo/app" />);

  const card = screen.getByTestId('agent-board-floating');
  expect(card).toHaveAttribute('role', 'region');
  expect(card).toHaveAttribute('aria-label', 'Agent 看板');

  const expand = screen.getByTestId('agent-board-expand');
  expect(expand).toHaveAttribute('aria-label', '展开 Agent 看板');
  expand.focus();
  expect(expand).toHaveFocus();
});
