import React from 'react';
import { AgentBoardPanel } from './AgentBoardPanel.jsx';

/*
 * AgentBoardPanelSlot 与 RuntimePanelSlot 结构一致：
 * 复用同一套右侧栏 splitter 与拖拽调整宽度机制，仅内容换成 Agent 看板。
 */
function AgentBoardPanelSlot({ resize, panel }) {
  if (!resize.open) return null;
  return (
    <>
      <button
        type="button"
        className="splitter splitter--right"
        role="separator"
        aria-label="调整侧边栏宽度"
        aria-orientation="vertical"
        aria-valuemin={resize.closeThreshold}
        aria-valuemax={resize.maxWidth}
        aria-valuenow={resize.width}
        title="调整侧边栏宽度"
        data-testid="right-panel-resizer"
        onKeyDown={resize.handleKeyDown}
        onPointerDown={resize.beginResize}
      >
        <span className="sr-only">调整侧边栏宽度，当前 {resize.width} 像素</span>
      </button>
      <AgentBoardPanel
        viewModel={panel.viewModel}
        formatTime={panel.formatTime}
        onSelectAgent={panel.onSelectAgent}
        onCollapse={panel.onCollapse}
        onShowRuntime={panel.onShowRuntime}
      />
    </>
  );
}

export { AgentBoardPanelSlot };
