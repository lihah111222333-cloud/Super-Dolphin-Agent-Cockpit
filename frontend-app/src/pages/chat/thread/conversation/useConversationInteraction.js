import { useCallback, useLayoutEffect, useRef } from 'react';
import { approvalIdentityKey } from '../../../../shared/api/support/approvalRequestId.js';
import { workStatusForThread } from '../../adapters/threadStateAdapter.js';
import { useComposerInteractions } from '../../hooks/useComposerInteractions.js';
import { useScrollIntentManager } from '../../hooks/useScrollIntentManager.js';
import { trimmedText } from '../../markdown/markdownMessageModel.js';
import { usePendingReasoningHint } from '../pendingReasoningHint.js';
import {
  approvalSnapshotFromMessages,
  hasNewApprovalIdentity,
  pendingReasoningForConversation,
  timelineAutoScrollKey,
} from './conversationModel.js';

function useApprovalComposerFocus({ activeThreadId, composerInputRef, snapshot }) {
  const previousPendingRef = useRef(null);
  useLayoutEffect(() => {
    const previous = previousPendingRef.current;
    const currentPending = snapshot.pendingRequest;
    const currentIdentityKey = currentPending ? approvalIdentityKey(currentPending) : '';
    const node = composerInputRef.current;
    if (
      previous &&
      !currentPending &&
      previous.threadId === activeThreadId &&
      node &&
      previous.node === node &&
      !hasNewApprovalIdentity(previous.knownIdentityKeys, snapshot.knownIdentityKeys)
    ) {
      composerInputRef.current.focus();
    }
    if (!currentPending) {
      previousPendingRef.current = null;
      return;
    }
    if (
      !previous ||
      previous.threadId !== activeThreadId ||
      previous.identityKey !== currentIdentityKey
    ) {
      previousPendingRef.current = {
        threadId: activeThreadId,
        identityKey: currentIdentityKey,
        node,
        knownIdentityKeys: snapshot.knownIdentityKeys,
      };
    }
  }, [activeThreadId, composerInputRef, snapshot]);
}

function useConversationInteraction(input) {
  const {
    activeThread,
    activeThreadId,
    activeTurn,
    attachments,
    attachDroppedFiles,
    attachPaths,
    canUseProjectActions,
    composerInputRef,
    messages,
    removeAttachment,
    sendMessage,
    sending,
    statusEntry,
    timelineBlocked,
    timelineContentBlocked,
  } = input;
  const { clearCurrent, hintVisible: pendingReasoningHint, start } = usePendingReasoningHint({
    activeThreadId,
    messages,
  });
  useLayoutEffect(() => {
    if (activeTurn) clearCurrent();
  }, [activeTurn, clearCurrent]);
  const justSent = !activeTurn && pendingReasoningHint;
  const isBusy = workStatusForThread({
    sending,
    loading: timelineContentBlocked,
    activeThreadId,
    activeThread,
    statusEntry,
  }).busy;
  const introMode = !activeThreadId && !timelineBlocked && messages.length === 0;
  const lastUserMessage = [...messages]
    .reverse()
    .find((message) => trimmedText(message.role).toLowerCase() === 'user');
  const pendingReasoning = pendingReasoningForConversation({
    activeTurn,
    fallbackStartTime: lastUserMessage?.time,
    introMode,
    isBusy,
    messages,
    sending: sending || justSent,
    timelineBlocked,
  });
  const approvalSnapshot = approvalSnapshotFromMessages(messages);
  const approvalPending = Boolean(approvalSnapshot.pendingRequest);
  const effectiveCanUseProjectActions = canUseProjectActions && !approvalPending;
  useApprovalComposerFocus({ activeThreadId, composerInputRef, snapshot: approvalSnapshot });
  const composer = useComposerInteractions({
    attachments,
    attachPaths,
    attachDroppedFiles,
    removeAttachment,
    projectActionBlocked: !effectiveCanUseProjectActions,
    canUseProjectActions: effectiveCanUseProjectActions,
  });
  const autoScrollKey = timelineAutoScrollKey({
    activeThreadId,
    introMode,
    messages,
    pendingReasoning,
    timelineContentBlocked,
  });
  const scroll = useScrollIntentManager({
    activeThreadId,
    autoScrollKey,
    timelineContentBlocked,
  });
  const sendMessageAndScrollToBottom = useCallback(() => {
    const result = sendMessage();
    const generation = start();
    Promise.resolve(result).then((sent) => {
      if (sent === false) clearCurrent(generation);
    }).catch(() => {
      clearCurrent(generation);
    });
    scroll.markMessageSent();
    return result;
  }, [clearCurrent, scroll, sendMessage, start]);
  const scrollTimelineToBottomSmooth = useCallback(() => {
    scroll.scrollToBottom(true);
  }, [scroll]);

  return {
    approvalPending,
    composer,
    composerInputRef,
    effectiveCanUseProjectActions,
    introMode,
    isBusy,
    justSent,
    pendingReasoning,
    scroll,
    scrollTimelineToBottomSmooth,
    sendMessageAndScrollToBottom,
  };
}

export { useConversationInteraction };
