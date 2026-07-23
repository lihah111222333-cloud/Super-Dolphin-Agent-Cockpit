import { expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { ChatActionFeedback } from './ChatActionFeedback.js';

const copy = Object.freeze({
  actionFailedTitle: '操作失败',
  attachmentFailedTitle: '附件选择失败',
  noticeTitle: '操作通知',
  sendFailedTitle: '发送失败',
});

it('renders and invokes the dismiss control when dismiss capability is provided', () => {
  const onDismiss = vi.fn();
  render(<ChatActionFeedback copy={copy} feedback={{ message: '消息已发送，等待回复', tone: 'info' }} onDismiss={onDismiss} />);

  const feedback = screen.getByTestId('chat-action-feedback');
  expect(feedback).toHaveClass('chat-action-toast', 'is-info');
  expect(feedback).toHaveAttribute('role', 'alert');
  expect(feedback.querySelector('strong')).toHaveTextContent('操作通知');
  expect(feedback).toHaveTextContent('消息已发送，等待回复');
  fireEvent.click(screen.getByRole('button', { name: '关闭通知' }));
  expect(onDismiss).toHaveBeenCalledTimes(1);
});

it('does not render a dismiss control when dismiss capability is absent', () => {
  render(<ChatActionFeedback copy={copy} feedback={{ message: '消息已发送，等待回复', tone: 'info' }} />);

  expect(screen.getByTestId('chat-action-feedback')).toHaveTextContent('消息已发送，等待回复');
  expect(screen.queryByRole('button', { name: '关闭通知' })).not.toBeInTheDocument();
});
