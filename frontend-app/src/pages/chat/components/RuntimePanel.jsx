import React, { useMemo, useState } from 'react';
import {
  codeOpenDisplayPath,
  codePreviewStateFromOpenResult,
  countCodePreviewLines,
  emptyCodePreviewState,
} from '../adapters/codePreviewAdapter.js';
import {
  codeActionError,
  emptyPathChoiceState,
  normalizeCodeLocateOptions,
  runtimeCodeScopePayload,
} from '../adapters/runtimeCodeAdapter.js';
import { parseUnifiedDiffLineEntries } from '../adapters/runtimeDiffLineAdapter.js';
import { summarizeUnifiedDiff } from '../adapters/runtimeDiffSummaryAdapter.js';
import {
  ACTIVITY_PANEL_MIN_HEIGHT,
  runtimePanelHeightVars,
  useRuntimePanelLayout,
} from '../hooks/useRuntimePanelLayout.js';
import { CodePreviewDialog } from './CodePreviewDialog.jsx';
import { PathChoiceDialog } from './PathChoiceDialog.jsx';
import { RuntimeActivityPanel } from './RuntimeActivityPanel.jsx';
import { RuntimeDiffView } from './RuntimeDiffView.jsx';
import { RuntimeToolbar } from './RuntimeToolbar.jsx';

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
  const [collapsedDiffFiles, setCollapsedDiffFiles] = useState(() => new Set());
  const [diffActionNotice, setDiffActionNotice] = useState('');
  const [codePreview, setCodePreview] = useState(emptyCodePreviewState);
  const [pathChoice, setPathChoice] = useState(emptyPathChoiceState);
  const diffSummary = useMemo(() => summarizeUnifiedDiff(diffText), [diffText]);
  const runtimeLayout = useRuntimePanelLayout();

  const toggleDiffFile = (filename) => {
    setCollapsedDiffFiles((current) => {
      const next = new Set(current);
      if (next.has(filename)) next.delete(filename);
      else next.add(filename);
      return next;
    });
  };

  const locateDiffFile = async (file) => {
    setDiffActionNotice(`正在定位 ${file.filename}`);
    try {
      const result = await codeFileActions.locateCodeFile(runtimeCodeScopePayload(file.filename, projectPath, projects));
      const options = normalizeCodeLocateOptions(result);
      if (options.length > 1) {
        setPathChoice({ open: true, file, options, truncated: Boolean(result?.truncated) });
      }
      setDiffActionNotice(`定位到 ${options.length} 个路径`);
    } catch (error) {
      setDiffActionNotice(codeActionError(error, '定位失败'));
    }
  };

  const openCodePreviewForPath = async (filePath, fallbackRelative = '') => {
    const displayPath = (fallbackRelative || filePath || '').toString();
    setCodePreview({ ...emptyCodePreviewState(), open: true, loading: true, filePath, relative: displayPath });
    try {
      const result = await codeFileActions.openCodeFile(runtimeCodeScopePayload(filePath, projectPath, projects));
      setCodePreview(codePreviewStateFromOpenResult(result, filePath, displayPath));
    } catch (error) {
      setCodePreview((current) => ({ ...current, loading: false, error: codeActionError(error, '打开失败') }));
    }
  };

  const openChosenPath = async (filePath) => {
    const fallback = pathChoice.file?.filename || filePath;
    setPathChoice(emptyPathChoiceState());
    await openCodePreviewForPath(filePath, fallback);
  };

  const savePreviewChanges = async () => {
    if (!codePreview.filePath || codePreview.saving) return;
    setCodePreview((current) => ({ ...current, saving: true, error: '', status: '' }));
    try {
      const result = await codeFileActions.saveCodeFile({
        ...runtimeCodeScopePayload(codePreview.filePath, projectPath, projects),
        content: codePreview.draft,
      });
      const relative = codeOpenDisplayPath(result, codePreview.relative || codePreview.filePath);
      setCodePreview((current) => ({
        ...current,
        saving: false,
        filePath: (result?.filePath || current.filePath).toString(),
        relative,
        content: current.draft,
        editing: current.previewKind === 'markdown' ? false : current.editing,
        totalLines: Number.isFinite(Number(result?.totalLines)) ? Math.floor(Number(result.totalLines)) : countCodePreviewLines(current.draft),
        status: `已保存 ${relative}`,
      }));
    } catch (error) {
      setCodePreview((current) => ({ ...current, saving: false, error: codeActionError(error, '保存失败') }));
    }
  };

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
      {codePreview.open ? (
        <CodePreviewDialog
          preview={codePreview}
          renderMarkdownPreview={renderMarkdownPreview}
          onBeginEdit={() => setCodePreview((current) => ({ ...current, editing: true, error: '', status: '' }))}
          onCancelEdit={() => setCodePreview((current) => ({ ...current, editing: false, draft: current.content, error: '', status: '' }))}
          onChangeDraft={(draft) => setCodePreview((current) => ({ ...current, draft, error: '' }))}
          onClose={() => setCodePreview(emptyCodePreviewState())}
          onDirtyClose={() => setCodePreview((current) => ({ ...current, error: '请先保存或放弃预览更改' }))}
          onSave={savePreviewChanges}
        />
      ) : null}
      {pathChoice.open ? (
        <PathChoiceDialog
          choice={pathChoice}
          onClose={() => setPathChoice(emptyPathChoiceState())}
          onSelect={(filePath) => { void openChosenPath(filePath); }}
        />
      ) : null}
    </aside>
  );
}

export { RuntimePanel };
