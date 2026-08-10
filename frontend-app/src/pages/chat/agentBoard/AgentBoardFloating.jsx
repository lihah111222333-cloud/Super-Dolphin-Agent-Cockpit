import React, { useRef, useState } from 'react';
import { Maximize2, Minimize2 } from 'lucide-react';
import { hasOnlyRootAgent } from './agentBoardModel.js';
import { AgentCountsSummary } from './AgentCountsSummary.jsx';

const DRAG_EDGE_INSET = 8;

function clamp(value, minimum, maximum) {
  return Math.min(maximum, Math.max(minimum, value));
}

function boundedDragOffset(drag, clientX, clientY) {
  const coordinates = [clientX, clientY, drag.x, drag.y];
  if (!coordinates.every(Number.isFinite)) {
    throw new Error('Agent board drag requires finite pointer coordinates');
  }
  const deltaX = clientX - drag.x;
  const deltaY = clientY - drag.y;
  return {
    x: drag.offset.x + clamp(
      deltaX,
      drag.containerRect.left + DRAG_EDGE_INSET - drag.cardRect.left,
      drag.containerRect.right - DRAG_EDGE_INSET - drag.cardRect.right,
    ),
    y: drag.offset.y + clamp(
      deltaY,
      drag.containerRect.top + DRAG_EDGE_INSET - drag.cardRect.top,
      drag.containerRect.bottom - DRAG_EDGE_INSET - drag.cardRect.bottom,
    ),
  };
}

function FloatingBody({ viewModel }) {
  const { agents } = viewModel;
  if (viewModel.loading) {
    return <p className="agent-board-floating__notice">正在加载 Agent 状态…</p>;
  }
  if (viewModel.error) {
    return <p className="agent-board-floating__error" role="alert">{viewModel.error}</p>;
  }
  if (agents.length === 0) {
    return <p className="agent-board-floating__notice">暂无 Agent</p>;
  }
  return (
    <>
      <AgentCountsSummary counts={viewModel.counts} />
      <ul className="agent-board-floating__agents" aria-label="当前 Subagent" data-testid="agent-floating-list">
        {agents.map((agent) => (
          <li key={agent.id} className="agent-board-floating__agent">
            <strong>{agent.name}</strong>
            <span>{agent.assignment.title}</span>
          </li>
        ))}
      </ul>
      {hasOnlyRootAgent(viewModel) ? <p className="agent-board-floating__notice">暂无子 Agent，等待委派</p> : null}
    </>
  );
}

/*
 * 常态悬浮 Agent 看板：聊天页面始终可见的紧凑卡片。
 * 数据完全来自 selector view model；点击展开按钮进入 docked 右侧栏。
 */
function AgentBoardFloating({ viewModel, collapsed = false, compact = false, onCollapsedChange = () => {}, onExpand }) {
  const [offset, setOffset] = useState({ x: 0, y: 0 });
  const [dragging, setDragging] = useState(false);
  const cardRef = useRef(null);
  const dragRef = useRef(null);
  const handlePointerDown = (event) => {
    if (event.target.closest('button')) return;
    const card = cardRef.current;
    const container = card?.parentElement;
    if (!card || !container) throw new Error('Agent board drag boundary is unavailable');
    dragRef.current = {
      pointerId: event.pointerId,
      x: event.clientX,
      y: event.clientY,
      offset,
      cardRect: card.getBoundingClientRect(),
      containerRect: container.getBoundingClientRect(),
    };
    setDragging(true);
    event.currentTarget.setPointerCapture?.(event.pointerId);
  };
  const handlePointerMove = (event) => {
    if (dragRef.current?.pointerId !== event.pointerId) return;
    setOffset(boundedDragOffset(dragRef.current, event.clientX, event.clientY));
  };
  const stopDragging = (event) => {
    if (dragRef.current?.pointerId !== event.pointerId) return;
    dragRef.current = null;
    setDragging(false);
  };
  return (
    <section
      ref={cardRef}
      className={`agent-board-floating${collapsed ? ' agent-board-floating--collapsed' : ''}${compact ? ' agent-board-floating--compact' : ''}${dragging ? ' is-dragging' : ''}`}
      role="region"
      aria-label="Agent 看板"
      aria-grabbed={dragging}
      data-testid="agent-board-floating"
      style={{ '--agent-board-drag-x': `${offset.x}px`, '--agent-board-drag-y': `${offset.y}px` }}
    >
      {collapsed ? (
        <button
          type="button"
          className="agent-board-floating__pill"
          aria-expanded="false"
          onClick={() => onCollapsedChange(false)}
        >
          Agents状态
        </button>
      ) : (
        <>
          <header
            className="agent-board-floating__header"
            onPointerDown={handlePointerDown}
            onPointerMove={handlePointerMove}
            onPointerUp={stopDragging}
            onPointerCancel={stopDragging}
          >
            <h2 className="agent-board-floating__title">Agents</h2>
            <span className="agent-board-floating__total" aria-label={`Agent 总数 ${viewModel.agents.length}`}>
              {viewModel.agents.length}
            </span>
            <button
              type="button"
              className="agent-board-floating__expand"
              aria-label="展开 Agent 看板"
              title="展开 Agent 看板"
              data-testid="agent-board-expand"
              onClick={() => onExpand()}
            >
              <Maximize2 size={14} aria-hidden="true" />
            </button>
          </header>
          <FloatingBody viewModel={viewModel} />
          <button
            type="button"
            className="agent-board-floating__collapse"
            aria-label="收起 Agents 状态"
            title="收起 Agents 状态"
            onClick={() => onCollapsedChange(true)}
          >
            <Minimize2 size={14} aria-hidden="true" />
          </button>
        </>
      )}
    </section>
  );
}

export { AgentBoardFloating };
