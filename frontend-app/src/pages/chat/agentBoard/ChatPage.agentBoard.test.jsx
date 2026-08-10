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

function pointerEvent(type, { pointerId, clientX, clientY }) {
  const event = new Event(type, { bubbles: true });
  Object.defineProperties(event, {
    pointerId: { value: pointerId },
    clientX: { value: clientX },
    clientY: { value: clientY },
  });
  return event;
}

function mockDragBounds(card, { cardRect, containerRect }) {
  card.getBoundingClientRect = () => cardRect;
  card.parentElement.getBoundingClientRect = () => containerRect;
}

const originalInnerWidth = window.innerWidth;

afterEach(() => {
  window.innerWidth = originalInnerWidth;
});

it('默认在聊天页面展示常态悬浮看板，不打开右侧栏', () => {
  render(<TestChatPageWrapper store={boardStore()} projectPath="/repo/app" />);

  expect(screen.getByTestId('agent-board-floating')).toBeInTheDocument();
  expect(screen.getByTestId('agent-board-floating')).toHaveTextContent('Agents');
  expect(screen.getByTestId('agent-floating-list')).toHaveTextContent('Agent worker');
  expect(screen.getByTestId('agent-floating-list')).toHaveTextContent('任务 worker');
  expect(screen.queryByTestId('agent-board-panel')).toBeNull();
  expect(screen.queryByTestId('runtime-panel')).toBeNull();
});

it('会话闲置时收起为状态胶囊，恢复运行时自动展开', async () => {
  const { rerender } = render(<TestChatPageWrapper store={boardStore({ agents: [agent('root', { status: 'idle' })] })} projectPath="/repo/app" />);

  expect(screen.getByRole('button', { name: 'Agents状态' })).toBeInTheDocument();
  rerender(<TestChatPageWrapper store={boardStore({ agents: [agent('root')] })} projectPath="/repo/app" />);
  await waitFor(() => expect(screen.getByTestId('agent-board-floating')).toHaveTextContent('Agents'));
  expect(screen.queryByRole('button', { name: 'Agents状态' })).toBeNull();
});

it('可拖动悬浮看板并保留新的偏移位置', () => {
  render(<TestChatPageWrapper store={boardStore()} projectPath="/repo/app" />);

  const card = screen.getByTestId('agent-board-floating');
  const header = card.querySelector('.agent-board-floating__header');
  mockDragBounds(card, {
    cardRect: { left: 200, right: 760, top: 40, bottom: 140 },
    containerRect: { left: 0, right: 1200, top: 0, bottom: 800 },
  });
  fireEvent(header, pointerEvent('pointerdown', { pointerId: 1, clientX: 100, clientY: 80 }));
  fireEvent(header, pointerEvent('pointermove', { pointerId: 1, clientX: 140, clientY: 110 }));
  fireEvent(header, pointerEvent('pointerup', { pointerId: 1, clientX: 140, clientY: 110 }));

  expect(card.style.getPropertyValue('--agent-board-drag-x')).toBe('40px');
  expect(card.style.getPropertyValue('--agent-board-drag-y')).toBe('30px');
});

it('拖动悬浮看板时限制在聊天主列内部', () => {
  render(<TestChatPageWrapper store={boardStore()} projectPath="/repo/app" />);

  const card = screen.getByTestId('agent-board-floating');
  const header = card.querySelector('.agent-board-floating__header');
  mockDragBounds(card, {
    cardRect: { left: 100, right: 300, top: 20, bottom: 120 },
    containerRect: { left: 0, right: 500, top: 0, bottom: 400 },
  });
  fireEvent(header, pointerEvent('pointerdown', { pointerId: 2, clientX: 100, clientY: 80 }));
  fireEvent(header, pointerEvent('pointermove', { pointerId: 2, clientX: 1000, clientY: -1000 }));
  fireEvent(header, pointerEvent('pointerup', { pointerId: 2, clientX: 1000, clientY: -1000 }));

  expect(card.style.getPropertyValue('--agent-board-drag-x')).toBe('192px');
  expect(card.style.getPropertyValue('--agent-board-drag-y')).toBe('-12px');
});

it('点击展开按钮后切换为 docked 右侧栏并保留宽度调整能力', () => {
  render(<TestChatPageWrapper store={boardStore()} projectPath="/repo/app" />);

  fireEvent.click(screen.getByTestId('agent-board-expand'));

  expect(screen.getByTestId('agent-board-panel')).toBeInTheDocument();
  expect(screen.getByTestId('right-panel-resizer')).toBeInTheDocument();
  expect(screen.queryByTestId('agent-board-floating')).toBeNull();
  expect(screen.getByTestId('agent-hierarchy')).toBeInTheDocument();
});

it('收起右侧栏时先播放退出动画，再恢复悬浮看板', async () => {
  render(<TestChatPageWrapper store={boardStore()} projectPath="/repo/app" />);

  fireEvent.click(screen.getByTestId('agent-board-expand'));
  expect(screen.getByTestId('agent-board-panel')).toBeInTheDocument();

  fireEvent.click(screen.getByTestId('agent-board-collapse'));
  expect(screen.getByTestId('agent-board-panel')).toHaveClass('is-closing');
  expect(screen.getByTestId('agent-board-panel')).toHaveAttribute('aria-hidden', 'true');
  expect(screen.getByTestId('agent-board-floating')).toBeInTheDocument();
  await waitFor(() => expect(screen.queryByTestId('agent-board-panel')).toBeNull());
});

it('Runtime 右侧栏展开时悬浮看板同样隐藏', () => {
  render(<TestChatPageWrapper store={boardStore()} projectPath="/repo/app" rightPanelOpen={true} />);

  expect(screen.getByTestId('runtime-panel')).toBeInTheDocument();
  expect(screen.queryByTestId('agent-board-floating')).toBeNull();
});

it('Agent/Runtime 切换过程中悬浮看板不出现，收起动画后恢复', async () => {
  render(<TestChatPageWrapper store={boardStore()} projectPath="/repo/app" rightPanelOpen={true} />);

  expect(screen.getByTestId('runtime-panel')).toBeInTheDocument();
  expect(screen.queryByTestId('agent-board-floating')).toBeNull();

  fireEvent.click(screen.getByTestId('runtime-show-agents'));
  expect(screen.getByTestId('agent-board-panel')).toBeInTheDocument();
  expect(screen.queryByTestId('runtime-panel')).toBeNull();
  expect(screen.queryByTestId('agent-board-floating')).toBeNull();

  fireEvent.click(screen.getByTestId('agent-board-show-runtime'));
  expect(screen.getByTestId('runtime-panel')).toBeInTheDocument();
  expect(screen.queryByTestId('agent-board-panel')).toBeNull();
  expect(screen.queryByTestId('agent-board-floating')).toBeNull();

  fireEvent.click(screen.getByTestId('runtime-show-agents'));
  fireEvent.click(screen.getByTestId('agent-board-collapse'));
  expect(screen.getByTestId('agent-board-panel')).toHaveClass('is-closing');
  expect(screen.getByTestId('agent-board-floating')).toBeInTheDocument();
  await waitFor(() => expect(screen.queryByTestId('agent-board-panel')).toBeNull());
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

it('没有 Agent 时悬浮看板保持闲置胶囊', () => {
  render(<TestChatPageWrapper store={boardStore({ agents: [], mainAgentId: '' })} projectPath="/repo/app" />);

  expect(screen.getByRole('button', { name: 'Agents状态' })).toBeInTheDocument();
});

it('窄窗口下收敛为紧凑卡片且不遮挡聊天输入区', () => {
  window.innerWidth = 500;
  render(<TestChatPageWrapper store={boardStore()} projectPath="/repo/app" />);

  const card = screen.getByTestId('agent-board-floating');
  expect(card).toHaveClass('agent-board-floating--compact');
  expect(screen.getByTestId('agent-board-expand')).toBeInTheDocument();
  const mainColumn = screen.getByTestId('chat-main-column');
  expect(mainColumn.contains(card)).toBe(true);
  expect(mainColumn.contains(screen.getByTestId('composer-dock'))).toBe(true);
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
