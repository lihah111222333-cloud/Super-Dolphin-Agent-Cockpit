import { useEffect } from 'react';
import { runUIAction } from '../model/chatUiActions.js';
import {
  RIGHT_PANEL_CLOSE_THRESHOLD,
  SPLITTER_WIDTH,
  THREAD_RAIL_MIN_WIDTH,
  chatLayoutWidthBudget,
  ratioWidth,
  resizerNextWidth,
  rightPanelDefaultWidth,
  rightPanelMaxWidth,
  threadRailTargetWidth,
} from '../model/chatWorkbenchLayoutModel.js';

function useRuntimeDiffSync({ activeThreadId, open, store }) {
  useEffect(() => {
    if (!open || !activeThreadId) return;
    if (store.threadDiffReadyByThread?.[activeThreadId]) return;
    if (store.threadStateLoadingByThread?.[activeThreadId]) return;
    runUIAction('thread.sync', () => store.syncThreadState?.(activeThreadId, {
      includeArchived: true,
      includeDiff: true,
      loadMessages: false,
      preserveActiveThreadId: true,
    }));
  }, [activeThreadId, open, store]);
}

export {
  RIGHT_PANEL_CLOSE_THRESHOLD,
  SPLITTER_WIDTH,
  THREAD_RAIL_MIN_WIDTH,
  chatLayoutWidthBudget,
  ratioWidth,
  resizerNextWidth,
  rightPanelDefaultWidth,
  rightPanelMaxWidth,
  threadRailTargetWidth,
  useRuntimeDiffSync,
};
