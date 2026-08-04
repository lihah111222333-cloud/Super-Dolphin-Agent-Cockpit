import React from 'react';
import { GitBranch, PanelRight, Sparkles } from 'lucide-react';

export function WorkbenchStatusBar({
  accent,
  activePage,
  projectPath,
  rightPanelOpen,
  themeMode,
  uiScale,
}) {
  return (
    <footer className="workbench-status-bar" aria-label="工作台状态">
      <span title={projectPath}><GitBranch size={12} aria-hidden="true" /> {projectPath}</span>
      <span><Sparkles size={12} aria-hidden="true" /> {themeMode} · {uiScale}% · {accent}</span>
      <span><PanelRight size={12} aria-hidden="true" /> {rightPanelOpen ? 'Aux on' : 'Aux off'} · {activePage}</span>
    </footer>
  );
}
