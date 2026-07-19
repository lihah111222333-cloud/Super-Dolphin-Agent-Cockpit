import { expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { ChatActionFeedback } from './ChatActionFeedback.js';

const copy = Object.freeze({
  actionFailedTitle: '操作失败',
  attachmentFailedTitle: '附件选择失败',
  noticeTitle: '操作通知',
  sendFailedTitle: '发送失败',
});

it('renders the production chat action feedback DOM semantics', () => {
  render(<ChatActionFeedback copy={copy} feedback={{ message: '已发送中断请求', tone: 'success' }} />);

  const feedback = screen.getByTestId('chat-action-feedback');
  expect(feedback).toHaveClass('chat-action-toast', 'is-success');
  expect(feedback).toHaveAttribute('role', 'alert');
  expect(feedback.querySelector('strong')).toHaveTextContent('操作通知');
  expect(feedback.querySelector('span')).toHaveTextContent('已发送中断请求');
});
