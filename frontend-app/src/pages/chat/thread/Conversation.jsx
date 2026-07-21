import { useRef } from 'react';
import { APP_COPY } from '../../../shared/i18n/appI18n.js';
import { optionalArrayValue } from '../../shared/pageShared.js';
import { ConversationView } from './conversation/ConversationView.jsx';
import { useConversationInteraction } from './conversation/useConversationInteraction.js';

/* The only public Conversation API remains the ChatPage prop contract. */
function Conversation(props) {
  const composerInputRef = useRef(null);
  const defaults = {
    attachments: optionalArrayValue(props.attachments, 'conversation attachments'),
    canUseProjectActions: props.canUseProjectActions ?? true,
    copy: props.copy ?? APP_COPY.zh.chat,
    timelineContentBlocked: props.timelineContentBlocked ?? false,
  };
  const interaction = useConversationInteraction({
    activeThread: props.activeThread,
    activeThreadId: props.activeThreadId,
    activeTurn: props.activeTurn,
    attachments: defaults.attachments,
    attachDroppedFiles: props.attachDroppedFiles,
    attachPaths: props.attachPaths,
    canUseProjectActions: defaults.canUseProjectActions,
    composerInputRef,
    messages: props.messages,
    removeAttachment: props.removeAttachment,
    sendMessage: props.sendMessage,
    sending: props.sending,
    statusEntry: props.statusEntry,
    timelineBlocked: props.timelineBlocked,
    timelineContentBlocked: defaults.timelineContentBlocked,
  });
  const conversation = {
    activeThreadId: props.activeThreadId,
    activeTurn: props.activeTurn,
    attachments: defaults.attachments,
    copy: defaults.copy,
    draft: props.draft,
    loadOlderThreadMessages: props.loadOlderThreadMessages,
    messageActions: props.messageActions,
    messagePagination: props.messagePagination,
    messages: props.messages,
    modelThreadId: props.modelThreadId,
    projectPath: props.projectPath,
    selectFiles: props.selectFiles,
    sending: props.sending,
    setDraft: props.setDraft,
    store: props.store,
    timelineContentBlocked: defaults.timelineContentBlocked,
    tokenUsage: props.tokenUsage,
  };
  return <ConversationView conversation={conversation} interaction={interaction} />;
}

export { Conversation };
