import React, { useMemo } from 'react';
import { parseUnifiedDiffLineEntries } from '../adapters/runtimeDiffLineAdapter.js';
import { summarizeUnifiedDiff } from '../adapters/runtimeDiffSummaryAdapter.js';
import {
  ACTIVITY_PANEL_MIN_HEIGHT,
  runtimePanelHeightVars,
  useRuntimePanelLayout,
} from '../hooks/useRuntimePanelLayout.js';
import { RuntimeActivityPanel } from './RuntimeActivityPanel.jsx';
import { RuntimeDiffView } from './RuntimeDiffView.jsx';
import { RuntimeToolbar } from './RuntimeToolbar.jsx';
import { useRuntimeCodePreview } from './useRuntimeCodePreview.jsx';
import './RuntimePanel.css';

function RuntimePanel({
  diffText,
  tokenUsage,
  activityStats,
  warnings,
  runtimeResults,
  projectPath,
  projects,
  codeFileActions,
  formatTime,
  renderMarkdownPreview,
}) {
  /*
   * RuntimePanel 不直接读 store，数据都由 ChatPage 传进来。
   * 本组件只管 diff 折叠、文件预览和右侧栏自己的 UI 状态。
   */
  const diffSummary = useMemo(() => summarizeUnifiedDiff(diffText), [diffText]);
  const runtimeLayout = useRuntimePanelLayout();
  const {
    collapsedDiffFiles,
    dialogs,
    diffActionNotice,
    locateDiffFile,
    openCodePreviewForPath,
    toggleDiffFile,
  } = useRuntimeCodePreview({
    codeFileActions,
    projectPath,
    projects,
    renderMarkdownPreview,
  });
  return (
    <aside
      className="runtime-panel"
      data-testid="runtime-panel"
      style={runtimePanelHeightVars(runtimeLayout.activityPanelHeight, runtimeLayout.viewportHeight)}
    >
      <RuntimeToolbar diffSummary={diffSummary} />
      <RuntimeDiffView
        diffText={diffText}
        diffSummary={diffSummary}
        collapsedFiles={collapsedDiffFiles}
        actionNotice={diffActionNotice}
        onLocateFile={locateDiffFile}
        onOpenFile={(file) => openCodePreviewForPath(file.filename, file.filename)}
        parseLineEntries={parseUnifiedDiffLineEntries}
        onToggleFile={toggleDiffFile}
      />
      <RuntimeActivityPanel
        activityStats={activityStats}
        tokenUsage={tokenUsage}
        warnings={warnings}
        runtimeResults={runtimeResults}
        activityPanelMax={runtimeLayout.activityPanelMax}
        activityPanelHeight={runtimeLayout.activityPanelHeight}
        activityPanelMinHeight={ACTIVITY_PANEL_MIN_HEIGHT}
        formatTime={formatTime}
        onResizeKeyDown={runtimeLayout.handleActivityPanelResizeKeyDown}
        onResizeStart={runtimeLayout.beginActivityPanelResize}
      />
      {dialogs}
    </aside>
  );
}

export { RuntimePanel };
