import { approvalRequestFromMessage, isApprovalMessage } from '../../../../features/approval/model/approvalDecision.js';
import { approvalIdentityKey } from '../../../../shared/api/support/approvalRequestId.js';
import { firstText, firstTrimmedText, textValue, trimmedText } from '../../markdown/markdownMessageModel.js';
import { isReasoningMessage, syntheticReasoningMessage } from '../chatReasoningModel.js';

function timelineItemTextValue(item = {}) {
  return firstTrimmedText(item.text, item.content, item.message, item.output, item.result, item.error);
}

function hasAssistantReplyAfterLastUser(messages = []) {
  let lastUserIndex = -1;
  for (let index = 0; index < messages.length; index += 1) {
    if (trimmedText(messages[index]?.role).toLowerCase() === 'user') lastUserIndex = index;
  }
  return messages.some((message, index) => (
    index > lastUserIndex &&
    trimmedText(message?.role).toLowerCase() === 'assistant' &&
    !isReasoningMessage(message) && Boolean(trimmedText(message?.text))
  ));
}

function hasReasoningMessageAfterLastUser(messages = []) {
  let lastUserIndex = -1;
  for (let index = 0; index < messages.length; index += 1) {
    if (trimmedText(messages[index]?.role).toLowerCase() === 'user') lastUserIndex = index;
  }
  return messages.some((message, index) => index > lastUserIndex && isReasoningMessage(message));
}

function timelineMessageAutoScrollKey(message) {
  if (!message) return '';
  const done = Object.prototype.hasOwnProperty.call(message, 'done') ? String(message.done) : '';
  return [textValue(message.id), firstText(message.role, message.kind), textValue(message.status), done, timelineItemTextValue(message)]
    .map((value) => value.toString()).join('\u0001');
}

function shouldAutoScrollForTimelineMessage(message) {
  if (!message) return false;
  const role = trimmedText(message.role).toLowerCase();
  return role === 'assistant' || isReasoningMessage(message) || isApprovalMessage(message);
}

function timelineAutoScrollKey({ activeThreadId, introMode, messages, pendingReasoning, timelineContentBlocked }) {
  if (introMode || timelineContentBlocked) return '';
  const lastMessage = messages[messages.length - 1] ?? null;
  if (!shouldAutoScrollForTimelineMessage(lastMessage) && !shouldAutoScrollForTimelineMessage(pendingReasoning)) return '';
  return [
    textValue(activeThreadId),
    shouldAutoScrollForTimelineMessage(lastMessage) ? timelineMessageAutoScrollKey(lastMessage) : '',
    timelineMessageAutoScrollKey(pendingReasoning),
  ].join('\u0002');
}

function pendingReasoningForConversation(state) {
  const { activeTurn, fallbackStartTime, introMode, isBusy, messages, sending, timelineBlocked } = state;
  if (introMode || timelineBlocked || hasReasoningMessageAfterLastUser(messages) || hasAssistantReplyAfterLastUser(messages)) return null;
  return syntheticReasoningMessage({ activeTurn, sending, isBusy, fallbackStartTime });
}

function approvalSnapshotFromMessages(messages = []) {
  const knownIdentityKeys = new Set();
  let pendingRequest = null;
  for (const message of messages) {
    if (!isApprovalMessage(message)) continue;
    const request = approvalRequestFromMessage(message);
    if (!request.displayOnly) knownIdentityKeys.add(approvalIdentityKey(request));
    if (request.status === 'pending') pendingRequest = request;
  }
  return { knownIdentityKeys, pendingRequest };
}

function hasNewApprovalIdentity(previousKeys, currentKeys) {
  for (const identityKey of currentKeys) {
    if (!previousKeys.has(identityKey)) return true;
  }
  return false;
}

export {
  approvalSnapshotFromMessages,
  hasNewApprovalIdentity,
  pendingReasoningForConversation,
  timelineAutoScrollKey,
};
