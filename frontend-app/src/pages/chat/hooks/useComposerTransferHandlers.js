import { useCallback, useEffect } from 'react';
import {
  collectTransferFiles,
  extractClipboardFiles,
  extractFilePathsFromTransferData,
  fileDropSubscriptionCleanup,
  hasFilesTransfer,
  nativeDropFiles,
} from '../model/composerInteractionsModel.js';
import { onFilesDropped } from '../services/chatCodeService.js';
import { runUIAction } from '../../../shared/ui/runUIAction.js';

function extractTransferFilePaths(event) {
  return extractFilePathsFromTransferData(event?.dataTransfer);
}

function extractClipboardFilePaths(event) {
  return extractFilePathsFromTransferData(event?.clipboardData);
}

function useComposerTransferHandlers({
  attachDroppedFiles,
  attachPaths,
  canUseProjectActions,
  dropDepthRef,
  projectActionBlocked,
  setDropActive,
}) {
  const resetDropState = useCallback(() => {
    dropDepthRef.current = 0;
    setDropActive(false);
  }, [dropDepthRef, setDropActive]);

  useEffect(() => {
    if (typeof attachPaths !== 'function') return undefined;
    const subscription = onFilesDropped((event) => {
      const files = nativeDropFiles(event, { acceptEmptyDetails: dropDepthRef.current > 0 });
      if (files.length === 0) return;
      if (!canUseProjectActions) return;
      runUIAction('composer.file.native-drop', () => attachPaths(files));
      resetDropState();
    });
    return fileDropSubscriptionCleanup(subscription);
  }, [attachPaths, canUseProjectActions, dropDepthRef, resetDropState]);

  const handlePaste = (event) => {
    const paths = extractClipboardFilePaths(event);
    if (paths.length > 0) {
      event.preventDefault();
      if (projectActionBlocked) return;
      if (typeof attachPaths === 'function') return runUIAction('composer.file.paste-paths', () => attachPaths(paths));
      return undefined;
    }
    const files = extractClipboardFiles(event);
    if (files.length === 0) return;
    event.preventDefault();
    if (projectActionBlocked) return;
    return runUIAction('composer.file.paste', () => attachDroppedFiles(files));
  };
  const handleDragEnter = (event) => {
    if (!hasFilesTransfer(event)) return;
    event.preventDefault();
    event.stopPropagation();
    if (projectActionBlocked) return;
    dropDepthRef.current += 1;
    setDropActive(true);
  };
  const handleDragOver = (event) => {
    if (!hasFilesTransfer(event)) return;
    event.preventDefault();
    event.stopPropagation();
    if (projectActionBlocked) return;
    if (event.dataTransfer) event.dataTransfer.dropEffect = 'copy';
    setDropActive(true);
  };
  const handleDragLeave = (event) => {
    if (!hasFilesTransfer(event)) return;
    event.preventDefault();
    event.stopPropagation();
    dropDepthRef.current = Math.max(dropDepthRef.current - 1, 0);
    if (dropDepthRef.current === 0) setDropActive(false);
  };
  const handleDrop = (event) => {
    if (!hasFilesTransfer(event)) return;
    event.preventDefault();
    event.stopPropagation();
    resetDropState();
    if (projectActionBlocked) return;
    return runUIAction('composer.file.drop', async () => {
      const files = collectTransferFiles(event);
      const paths = extractTransferFilePaths(event);
      if (files.length > 0) {
        const attachedCount = await attachDroppedFiles(files);
        if (attachedCount > 0 && paths.length === 0) return;
      }
      if (paths.length > 0 && typeof attachPaths === 'function') attachPaths(paths);
    });
  };
  return { handleDragEnter, handleDragLeave, handleDragOver, handleDrop, handlePaste };
}

export { useComposerTransferHandlers };
