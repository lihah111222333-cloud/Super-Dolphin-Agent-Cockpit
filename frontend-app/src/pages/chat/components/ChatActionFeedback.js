import { createElement } from 'react';

export function chatActionFeedbackTitle(feedback, copy) {
  if (feedback?.tone !== 'error') return copy.noticeTitle;
  if (feedback?.category === 'attachment') return copy.attachmentFailedTitle;
  if (feedback?.category === 'send') return copy.sendFailedTitle;
  return copy.actionFailedTitle;
}

export function ChatActionFeedback({ copy, feedback, onDismiss }) {
  if (!feedback?.message || feedback.bootstrapRecovery) return null;
  const tone = feedback.tone || 'info';
  return createElement(
    'output',
    { className: `chat-action-toast is-${tone}`, role: 'alert', 'data-testid': 'chat-action-feedback' },
    createElement('strong', null, chatActionFeedbackTitle(feedback, copy)),
    createElement('span', null, feedback.message),
    typeof onDismiss === 'function' ? createElement('button', {
      type: 'button',
      className: 'chat-action-toast-close',
      'aria-label': '关闭通知',
      title: '关闭通知',
      onClick: onDismiss,
    }, createElement('span', { 'aria-hidden': 'true' }, '×')) : null,
  );
}
