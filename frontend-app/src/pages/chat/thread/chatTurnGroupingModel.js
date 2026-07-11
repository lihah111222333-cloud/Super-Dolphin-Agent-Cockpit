import { isApprovalMessage } from '../../../features/approval/model/approvalDecision.js';
import { trimmedText } from '../markdown/markdownMessageModel.js';
import { isReasoningMessage } from './chatReasoningModel.js';

function timelineMessageKey(message) {
  const callId = trimmedText(message?.callId);
  if (callId) return `tool-${callId}`;
  return trimmedText(message?.id);
}
function messageEntry(message) {
  return {
    type: 'message',
    key: timelineMessageKey(message),
    message,
  };
}

function isOrdinaryAssistant(message) {
  return (
    message?.role === 'assistant' &&
    !isReasoningMessage(message) &&
    !isApprovalMessage(message) &&
    Boolean(trimmedText(message?.text))
  );
}

function materializeTurn(userMessage, turnMessages, active) {
  let finalIndex = -1;
  for (let index = turnMessages.length - 1; index >= 0; index -= 1) {
    if (isOrdinaryAssistant(turnMessages[index])) {
      finalIndex = index;
      break;
    }
  }

  const output = [messageEntry(userMessage)];
  const processMessages = turnMessages.filter((message, index) => (
    index !== finalIndex && !isApprovalMessage(message)
  ));
  if (processMessages.length > 0) {
    output.push({
      type: 'process',
      key: `turn-process-${timelineMessageKey(userMessage)}`,
      messages: processMessages,
      active,
    });
  }

  for (const message of turnMessages) {
    if (isApprovalMessage(message)) output.push(messageEntry(message));
  }
  if (finalIndex >= 0) output.push(messageEntry(turnMessages[finalIndex]));
  return output;
}

export function materializeTurnTimelineEntries(messages = [], options = {}) {
  const output = [];
  let currentUser = null;
  let turnMessages = [];

  for (const message of messages) {
    if (message?.role === 'user') {
      if (currentUser) output.push(...materializeTurn(currentUser, turnMessages, false));
      currentUser = message;
      turnMessages = [];
      continue;
    }
    if (currentUser) {
      turnMessages.push(message);
    } else {
      output.push(messageEntry(message));
    }
  }

  if (currentUser) {
    output.push(...materializeTurn(currentUser, turnMessages, options.activeCurrentTurn === true));
  }
  return output;
}
