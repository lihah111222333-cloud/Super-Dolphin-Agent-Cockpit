import React, { useEffect, useImperativeHandle, useMemo, useRef } from 'react';
import { composerCapabilitiesReady } from '../../../entities/client/model/capabilities/composerCapabilities.js';
import { usePromptHistory } from '../../../features/prompt-history/hooks/usePromptHistory.js';
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
import { runUIAction, threadScopedActionOptions } from '../model/chatUiActions.js';
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
    const handleDrop = (event) => { void composer.handleDrop(event); };

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

function useComposerSendKeyHandler({ canSend, composer, sendMessage, threadId }) {
  return (event) => {
    if (event.key !== 'Enter' || event.shiftKey || event.metaKey || event.ctrlKey || event.altKey) return;
    const keyCode = Number(event.keyCode || event.which || 0);
    const imeLikely = event.isComposing || composer.isComposing() || keyCode === 229 || event.key === 'Process' || event.key === 'Unidentified';
    if (imeLikely) return;
    event.preventDefault();
    if (!canSend) return;
    runUIAction('composer.send', () => sendMessage(), threadScopedActionOptions(threadId, {
      supersedesActionIds: ['thread.interrupt'],
    }));
  };
}

function shouldNavigatePromptHistory(event, textarea, direction) {
  const expectedKey = direction === 'previous' ? 'ArrowUp' : direction === 'next' ? 'ArrowDown' : '';
  if (!expectedKey || event.key !== expectedKey || event.defaultPrevented) return false;
  if (event.shiftKey || event.metaKey || event.ctrlKey || event.altKey) return false;
  const keyCode = Number(event.keyCode || event.which || 0);
  if (event.isComposing || event.nativeEvent?.isComposing || keyCode === 229) return false;
  if (!textarea || typeof textarea.value !== 'string') return false;
  const { selectionStart, selectionEnd, value } = textarea;
  if (!Number.isInteger(selectionStart) || selectionStart !== selectionEnd) return false;
  if (direction === 'previous') return value.lastIndexOf('\n', selectionStart - 1) === -1;
  return value.indexOf('\n', selectionStart) === -1;
}

function runPromptHistoryAction(direction, promptHistory) {
  if (direction === 'previous') {
    return runUIAction('prompt-history.previous', () => promptHistory.previous(), { retryable: true });
  }
  return runUIAction('prompt-history.next', () => promptHistory.next(), { retryable: true });
}

function useComposerKeyHandler({ canSend, composer, promptHistory, sendMessage, threadId }) {
  const handleSendKey = useComposerSendKeyHandler({ canSend, composer, sendMessage, threadId });
  return (event) => {
    const direction = event.key === 'ArrowUp' ? 'previous' : event.key === 'ArrowDown' ? 'next' : '';
    if (direction && !composer.isComposing() && shouldNavigatePromptHistory(event, event.currentTarget, direction)) {
      event.preventDefault();
      runPromptHistoryAction(direction, promptHistory);
      return;
    }
    handleSendKey(event);
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
  fetchPromptHistory,
  scrollBottomControl,
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
  const projectActionBlockedTitle = copy.projectActionBlocked;
  // composer 工具控件（项目选择器、附件、图片、模型）只要求后端就绪：
  // 1. 项目选择器是“未选择项目”的恢复入口，不能因缺项目自我禁用（死锁）；
  // 2. 附件/图片走原生文件对话框，不依赖项目 cwd；
  // 3. 模型菜单可以打开查看，保存失败时由 store action 给出可见通知。
  // 发送/打断仍走 effectiveCanUseProjectActions（必须有可用 cwd），业务契约不变。
  const controlsReady = store?.bootstrapStatus === 'ready';
  const composerControlsBlocked = !controlsReady || approvalPending;
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
  const promptHistory = usePromptHistory({
    activeThreadId: modelThreadId,
    cwd: projectPath,
    draft,
    fetchPage: fetchPromptHistory,
    sendMessage,
    setDraft,
    threadLifecycleSignal: store?.threads,
  });

  const handlePromptHistoryKeyDown = useComposerKeyHandler({
    canSend,
    composer,
    promptHistory,
    sendMessage: promptHistory.send,
    threadId: modelThreadId,
  });
  const handleKeyDown = (event) => {
    if (palette.handleKeyDown(event, { isComposing: composer.isComposing() })) return;
    handlePromptHistoryKeyDown(event);
  };
  const handleTextareaChange = (event) => setDraft(event.target.value);
  const handleTextareaPaste = (event) => { void composer.handlePaste(event); };

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
      {floating ? null : scrollBottomControl}
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
          projectActionBlockedTitle={projectActionBlockedTitle}
          composerControlsBlocked={composerControlsBlocked}
          selectFiles={selectFiles}
          sendMessage={promptHistory.send}
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

// eslint-disable-next-line react-refresh/only-export-components
export { ComposerDock, shouldNavigatePromptHistory };
