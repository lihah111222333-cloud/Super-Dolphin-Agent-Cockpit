import React, { useEffect, useImperativeHandle, useMemo, useRef } from 'react';
import { composerCapabilitiesReady } from '../../../entities/client/model/capabilities/composerCapabilities.js';
import { ComposerCapabilityChips } from '../../../features/slash-commands/components/ComposerCapabilityChips.jsx';
import { SlashCommandPalette } from '../../../features/slash-commands/components/SlashCommandPalette.jsx';
import { useSlashCommandPalette } from '../../../features/slash-commands/hooks/useSlashCommandPalette.js';
import { APP_COPY } from '../../../shared/i18n/appI18n.js';
import { textValue } from '../../shared/pageShared.js';
import { AttachmentPreviewModal } from './AttachmentPreviewModal.jsx';
import { ComposerAttachments } from './ComposerAttachments.jsx';
import { ComposerMeta } from './ComposerMeta.jsx';
import { ComposerTextarea } from './ComposerTextarea.jsx';
import { ForkDraftCard } from './ForkDraftCard.jsx';
import { runUIAction } from '../model/chatUiActions.js';
import './ComposerDock.css';

function useComposerDropTarget(ref, composer) {
  /*
   * drop 事件同时挂在 dock 和 textarea 上。
   * 这里只桥接 DOM 事件，附件解析和写入交给 ChatPage/store。
   */
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

function useComposerTextareaRef(inputRef) {
  const textareaRef = useRef(null);
  useImperativeHandle(inputRef, () => textareaRef.current);
  return textareaRef;
}

function ComposerDock({
  floating = false,
  copy = APP_COPY.zh.chat,
  draft,
  setDraft,
  sendMessage,
  attachments,
  selectFiles,
  sending,
  store,
  projectPath,
  modelThreadId,
  showProviderToggle = true,
  showProjectSelector = false,
  composer,
  canUseProjectActions = true,
  inputRef,
  approvalPending = false,
  slashCommandService,
}) {
  const composerClass = `composer ${floating ? 'composer--floating' : 'composer--docked'}`;
  const effectiveCanUseProjectActions = canUseProjectActions && !approvalPending;
  const hasComposerInput = Boolean(textValue(draft) || attachments.length > 0);
  const capabilitiesReady = composerCapabilitiesReady(store.composerCapabilities);
  const canInterrupt = effectiveCanUseProjectActions && Boolean(store?.hasInterruptibleThreadAction?.(modelThreadId));
  const canSend = effectiveCanUseProjectActions
    && capabilitiesReady
    && !sending
    && !canInterrupt
    && hasComposerInput;
  const projectActionBlocked = !effectiveCanUseProjectActions;
  const projectActionBlockedTitle = copy.projectActionBlocked;
  const dockRef = useRef(null);
  const textareaRef = useComposerTextareaRef(inputRef);
  const slashCopy = useMemo(() => ({
    ...copy.slashCommands,
    ariaLabel: copy.slashCommands.label,
    loadError: copy.slashCommands.catalogLoadFailed,
    selecting: copy.slashCommands.loading,
  }), [copy.slashCommands]);
  const palette = useSlashCommandPalette({
    copy: slashCopy,
    cwd: projectPath,
    draft,
    service: slashCommandService,
    setDraft,
    store,
    textareaRef,
  });
  useComposerDropTarget(dockRef, composer);
  useComposerDropTarget(textareaRef, composer);

  const handleSendKeyDown = useComposerSendKeyHandler({ canSend, composer, sendMessage });
  const handleKeyDown = (event) => {
    if (palette.handleKeyDown(event, { isComposing: composer.isComposing() })) return;
    handleSendKeyDown(event);
  };
  const handleTextareaChange = (event) => setDraft(event.target.value);
  const handleTextareaPaste = (event) => { runUIAction(() => composer.handlePaste(event)); };

  return (
    <footer
      ref={dockRef}
      id="chat-input-bar"
      className={`${composerClass}${composer.dropActive ? ' drop-active' : ''}`}
      data-testid="composer-dock"
      data-file-drop-target=""
      inert={approvalPending}
      aria-disabled={approvalPending ? 'true' : undefined}
    >
      <div className="composer-card">
        {composer.dropActive ? <div className="composer-drop-hint" aria-live="polite">{copy.dropHint}</div> : null}
        <SlashCommandPalette {...palette} copy={slashCopy} cwd={projectPath} />
        <ForkDraftCard store={store} />
        <ComposerAttachments attachments={attachments} onPreview={composer.previewAttachmentItem} onRemove={composer.removeAttachmentItem} />
        <ComposerCapabilityChips
          copy={copy.slashCommands}
          items={store.composerCapabilities}
          onRemove={store.removeComposerCapability}
        />
        <ComposerTextarea
          ref={textareaRef}
          ariaActiveDescendant={palette.activeOptionId === '' ? undefined : palette.activeOptionId}
          ariaControls={palette.open ? palette.listboxId : undefined}
          ariaExpanded={palette.open}
          copy={copy}
          draft={draft}
          onChange={handleTextareaChange}
          onPaste={handleTextareaPaste}
          onCompositionStart={composer.handleCompositionStart}
          onCompositionEnd={composer.handleCompositionEnd}
          onKeyDown={handleKeyDown}
        />
        <ComposerMeta
          copy={copy}
          canInterrupt={canInterrupt}
          canSend={canSend}
          canUseProjectActions={effectiveCanUseProjectActions}
          modelThreadId={modelThreadId}
          projectPath={projectPath}
          projectActionBlocked={projectActionBlocked}
          projectActionBlockedTitle={projectActionBlockedTitle}
          selectFiles={selectFiles}
          sendMessage={sendMessage}
          showProviderToggle={showProviderToggle}
          showProjectSelector={showProjectSelector}
          store={store}
        />
      </div>
      {floating ? <p className="composer-disclaimer">{copy.composerDisclaimer}</p> : null}
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
