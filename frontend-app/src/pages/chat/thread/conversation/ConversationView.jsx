import { ComposerDock } from '../../composer/ComposerDock.jsx';
import { APP_COPY } from '../../../../shared/i18n/appI18n.js';
import { runUIAction } from '../../model/chatUiActions.js';
import { CONVERSATION_DROP_TARGET_ID } from '../../hooks/useComposerInteractions.js';
import { ConversationTimeline } from './ConversationTimeline.jsx';

const CONTEXT_USAGE_FORK_THRESHOLD = 90;

function ConversationView({ conversation, interaction }) {
  const {
    activeThreadId,
    attachments,
    copy = APP_COPY.zh.chat,
    draft,
    loadOlderThreadMessages,
    messageActions,
    messagePagination,
    messages,
    modelThreadId,
    projectPath,
    selectFiles,
    sending,
    setDraft,
    store,
    timelineContentBlocked,
    tokenUsage,
  } = conversation;
  const composerView = {
    approvalPending: interaction.approvalPending,
    attachments,
    canUseProjectActions: interaction.effectiveCanUseProjectActions,
    composer: interaction.composer,
    copy,
    draft,
    floating: interaction.introMode,
    inputRef: interaction.composerInputRef,
    modelThreadId,
    projectPath,
    selectFiles,
    sendMessage: interaction.sendMessageAndScrollToBottom,
    sending,
    setDraft,
    showProviderToggle: !activeThreadId,
    store,
  };
  const composer = <ConversationComposer composerView={composerView} />;
  const timeline = {
    activeCurrentTurn: interaction.isBusy || sending || interaction.justSent,
    activeThreadId,
    composer,
    copy,
    introMode: interaction.introMode,
    loadOlderThreadMessages,
    messageActions,
    messagePagination,
    messages,
    onScrollToBottom: interaction.scrollTimelineToBottomSmooth,
    pendingReasoning: interaction.pendingReasoning,
    scroll: interaction.scroll,
    smoothStreaming: store?.smoothStreaming ?? false,
    timelineContentBlocked,
  };
  return (
    <section
      id={CONVERSATION_DROP_TARGET_ID}
      className={`conversation${interaction.introMode ? ' conversation--intro' : ''}${interaction.composer.dropActive ? ' drop-active' : ''}`}
      data-testid={CONVERSATION_DROP_TARGET_ID}
      data-file-drop-target=""
      onDragEnter={interaction.composer.handleDragEnter}
      onDragOver={interaction.composer.handleDragOver}
      onDragLeave={interaction.composer.handleDragLeave}
      onDrop={(event) => runUIAction('composer.drop', () => interaction.composer.handleDrop(event))}
    >
      <ContextUsageBanner activeThreadId={activeThreadId} store={store} tokenUsage={tokenUsage} />
      <ConversationTimeline timeline={timeline} />
      {!interaction.introMode ? composer : null}
    </section>
  );
}

function ContextUsageBanner({ activeThreadId, store, tokenUsage }) {
  if (!activeThreadId || !tokenUsage || tokenUsage.usedPercent < CONTEXT_USAGE_FORK_THRESHOLD) return null;
  const canFork = Boolean(store?.hasActiveThreadActions?.());
  return (
    <output className="context-usage-banner" data-testid="context-usage-banner">
      <span>上下文使用率 {Math.round(tokenUsage.usedPercent)}%</span>
      <button
        type="button"
        disabled={!canFork}
        onClick={() => {
          if (canFork) runUIAction('thread.fork.open', () => store.openForkDraft?.({ origin: 'context-usage' }));
        }}
      >
        新建继承会话
      </button>
    </output>
  );
}

function ConversationComposer({ composerView }) {
  const {
    copy = APP_COPY.zh.chat,
    floating,
    draft,
    setDraft,
    sendMessage,
    attachments,
    selectFiles,
    sending,
    store,
    projectPath,
    modelThreadId,
    showProviderToggle,
    composer,
    canUseProjectActions,
    inputRef,
    approvalPending,
  } = composerView;
  return (
    <ComposerDock
      floating={floating}
      copy={copy}
      draft={draft}
      setDraft={setDraft}
      sendMessage={sendMessage}
      attachments={attachments}
      selectFiles={selectFiles}
      sending={sending}
      store={store}
      projectPath={projectPath}
      modelThreadId={modelThreadId}
      showProviderToggle={showProviderToggle}
      showProjectSelector
      composer={composer}
      canUseProjectActions={canUseProjectActions}
      inputRef={inputRef}
      approvalPending={approvalPending}
    />
  );
}

export { ConversationView };
