import React, { useEffect, useRef } from 'react';
import { textValue } from '../../shared/pageShared.js';
import { AttachmentPreviewModal } from './AttachmentPreviewModal.jsx';
import { ComposerAttachments } from './ComposerAttachments.jsx';
import { ComposerMeta } from './ComposerMeta.jsx';
import { ComposerTextarea } from './ComposerTextarea.jsx';
import { ForkDraftCard } from './ForkDraftCard.jsx';
import { runUIAction } from './chatUiActions.js';

function useComposerDropTarget(ref, composer) {
  useEffect(() => {
    const target = ref.current;
    if (!target) return undefined;

    const handleDragEnter = (event) => composer.handleDragEnter(event);
    const handleDragOver = (event) => composer.handleDragOver(event);
    const handleDragLeave = (event) => composer.handleDragLeave(event);
    const handleDrop = (event) => runUIAction(() => composer.handleDrop(event));

    target.addEventListener('dragenter', handleDragEnter);
    target.addEventListener('dragover', handleDragOver);
    target.addEventListener('dragleave', handleDragLeave);
    target.addEventListener('drop', handleDrop);
    return () => {
      target.removeEventListener('dragenter', handleDragEnter);
      target.removeEventListener('dragover', handleDragOver);
      target.removeEventListener('dragleave', handleDragLeave);
      target.removeEventListener('drop', handleDrop);
    };
  }, [composer, ref]);
}

function useComposerSendKeyHandler({ canSend, composer, sendMessage }) {
  return (event) => {
    if (event.key !== 'Enter' || event.shiftKey || event.metaKey || event.ctrlKey || event.altKey) return;
    const keyCode = Number(event.keyCode || event.which || 0);
    const imeLikely = event.isComposing || composer.isComposing() || keyCode === 229 || event.key === 'Process' || event.key === 'Unidentified';
    if (imeLikely) return;
    event.preventDefault();
    if (!canSend) return;
    runUIAction(() => sendMessage());
  };
}

function ComposerDock({
  floating = false,
  draft,
  setDraft,
  sendMessage,
  attachments,
  selectFiles,
  sending,
  store,
  modelThreadId,
  showProviderToggle = true,
  composer,
  canUseProjectActions = true,
}) {
  const composerClass = `composer ${floating ? 'composer--floating' : 'composer--docked'}`;
  const hasComposerInput = Boolean(textValue(draft) || attachments.length > 0);
  const canInterrupt = canUseProjectActions && Boolean(store?.hasInterruptibleThreadAction?.(modelThreadId));
  const canSend = canUseProjectActions && !sending && !canInterrupt && hasComposerInput;
  const projectActionBlocked = !canUseProjectActions;
  const projectActionBlockedTitle = '请先连接后端并选择项目';
  const dockRef = useRef(null);
  const textareaRef = useRef(null);
  useComposerDropTarget(dockRef, composer);
  useComposerDropTarget(textareaRef, composer);

  const handleKeyDown = useComposerSendKeyHandler({ canSend, composer, sendMessage });
  const handleTextareaChange = (event) => setDraft(event.target.value);
  const handleTextareaPaste = (event) => { runUIAction(() => composer.handlePaste(event)); };

  return (
    <footer
      ref={dockRef}
      id="chat-input-bar"
      className={`${composerClass}${composer.dropActive ? ' drop-active' : ''}`}
      data-testid="composer-dock"
      data-file-drop-target=""
    >
      <div className="composer-card">
        {composer.dropActive ? <div className="composer-drop-hint" aria-live="polite">松开即可添加附件</div> : null}
        <ForkDraftCard store={store} />
        <ComposerAttachments attachments={attachments} onPreview={composer.previewAttachmentItem} onRemove={composer.removeAttachmentItem} />
        <ComposerTextarea
          ref={textareaRef}
          draft={draft}
          onChange={handleTextareaChange}
          onPaste={handleTextareaPaste}
          onCompositionStart={composer.handleCompositionStart}
          onCompositionEnd={composer.handleCompositionEnd}
          onKeyDown={handleKeyDown}
        />
        <ComposerMeta
          canInterrupt={canInterrupt}
          canSend={canSend}
          canUseProjectActions={canUseProjectActions}
          modelThreadId={modelThreadId}
          projectActionBlocked={projectActionBlocked}
          projectActionBlockedTitle={projectActionBlockedTitle}
          selectFiles={selectFiles}
          sendMessage={sendMessage}
          showProviderToggle={showProviderToggle}
          store={store}
        />
      </div>
      <ComposerPreviewModal composer={composer} />
    </footer>
  );
}

function ComposerPreviewModal({ composer }) {
  if (!composer.activePreview) return null;
  return (
    <AttachmentPreviewModal
      attachment={composer.activePreview}
      onClose={() => composer.setPreviewAttachment(null)}
      onRemove={() => composer.removeAttachmentItem(composer.activePreview)}
    />
  );
}

export { ComposerDock };
