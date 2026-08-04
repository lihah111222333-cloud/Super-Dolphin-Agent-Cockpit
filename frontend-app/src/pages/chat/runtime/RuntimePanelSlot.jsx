import React from 'react';
import { RuntimePanel } from './RuntimePanel.jsx';

function RuntimePanelSlot({
  beginResize,
  codeFileActions,
  formatTime,
  geometrySnapshot,
  handleKeyDown,
  layoutActions,
  onShowAgents,
  open,
  projectPath,
  projects,
  renderMarkdownPreview,
  threadData,
}) {
  /*
   * RuntimePanelSlot 只负责右侧栏外壳：splitter 和 RuntimePanel 透传。
   * 宽度计算与打开/关闭状态仍由 ChatPage 的布局 hook 管理。
   */
  if (!open) return null;
  return (
    <>
      <button
        type="button"
        className="splitter splitter--right"
        role="separator"
        aria-label="调整侧边栏宽度"
        aria-orientation="vertical"
        aria-valuemin={geometrySnapshot.aria.rightMin}
        aria-valuemax={geometrySnapshot.aria.rightMax}
        aria-valuenow={geometrySnapshot.aria.rightNow}
        title="调整侧边栏宽度"
        data-testid="right-panel-resizer"
        onKeyDown={handleKeyDown}
        onPointerDown={beginResize}
      >
        <span className="sr-only">调整侧边栏宽度，当前 {geometrySnapshot.aria.rightNow} 像素</span>
      </button>
      <RuntimePanel
        diffText={threadData.diffText}
        tokenUsage={threadData.tokenUsage}
        activityStats={threadData.activityStats}
        warnings={threadData.warnings}
        runtimeResults={threadData.runtimeResults}
        projectPath={projectPath}
        projects={projects}
        codeFileActions={codeFileActions}
        formatTime={formatTime}
        renderMarkdownPreview={renderMarkdownPreview}
        geometrySnapshot={geometrySnapshot}
        layoutActions={layoutActions}
        onCollapse={() => layoutActions.right.setOpen(false)}
        onShowAgents={onShowAgents}
      />
    </>
  );
}

export { RuntimePanelSlot };
