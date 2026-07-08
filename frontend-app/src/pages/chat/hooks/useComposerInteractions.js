import { useRef, useState } from 'react';
import { composerAttachmentKey } from '../composer/composerAttachmentKey.js';
import {
  CONVERSATION_DROP_TARGET_ID,
  clipboardPathsFromText,
  extractFilePathsFromTransferData,
  fileDropSubscriptionCleanup,
  nativeDropFiles,
  nativeDropTargetAcceptsFiles,
} from '../model/composerInteractionsModel.js';
import { useComposerTransferHandlers } from './useComposerTransferHandlers.js';

export function useComposerInteractions({
  attachments,
  attachPaths,
  attachDroppedFiles,
  removeAttachment,
  projectActionBlocked,
  canUseProjectActions,
}) {
  /*
   * composer 交互层只管理浏览器本地状态：预览、拖拽深度、IME 输入。
   * 附件保存、去重和发送 input 都在 store 里完成。
   */
  const [previewAttachment, setPreviewAttachment] = useState(null);
  const [dropActive, setDropActive] = useState(false);
  const dropDepthRef = useRef(0);
  const isComposingRef = useRef(false);
  const activePreview = previewAttachment && attachments.some((item) => composerAttachmentKey(item) === composerAttachmentKey(previewAttachment))
    ? previewAttachment
    : null;

  const previewAttachmentItem = (item) => {
    setPreviewAttachment(item);
  };
  const removeAttachmentItem = (item) => {
    removeAttachment(composerAttachmentKey(item));
    if (activePreview && composerAttachmentKey(activePreview) === composerAttachmentKey(item)) {
      setPreviewAttachment(null);
    }
  };
  const handlers = useComposerTransferHandlers({
    attachDroppedFiles,
    attachPaths,
    canUseProjectActions,
    dropDepthRef,
    projectActionBlocked,
    setDropActive,
  });

  return {
    activePreview,
    dropActive,
    handleCompositionEnd: () => { isComposingRef.current = false; },
    handleCompositionStart: () => { isComposingRef.current = true; },
    isComposing: () => isComposingRef.current,
    previewAttachmentItem,
    removeAttachmentItem,
    setPreviewAttachment,
    ...handlers,
  };
}

export {
  CONVERSATION_DROP_TARGET_ID,
  clipboardPathsFromText,
  extractFilePathsFromTransferData,
  fileDropSubscriptionCleanup,
  nativeDropFiles,
  nativeDropTargetAcceptsFiles,
};
