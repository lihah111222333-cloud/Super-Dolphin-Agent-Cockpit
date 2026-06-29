import React from 'react';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ChatApprovalMessage } from './ChatApprovalMessage.jsx';
import { approvalHintText, approvalRequestId, isApprovalMessage, isApprovalTerminal } from './chatApprovalModel.js';

describe('ChatApprovalMessage', () => {
  it('submits approval decisions and marks the request resolved', async () => {
    const onApproval = vi.fn().mockResolvedValue(true);

    render(
      <ChatApprovalMessage
        message={{
          kind: 'approval',
          requestId: 42,
          title: 'Run command',
          text: 'Allow command execution?',
          time: '2026-06-15T08:00:00Z',
        }}
        actions={{ onApproval }}
        formatTime={() => '16:00'}
      />
    );

    expect(screen.getByTestId('approval-request-42')).toHaveTextContent('等待审批');

    fireEvent.click(screen.getByRole('button', { name: '同意审批 42' }));

    await waitFor(() => {
      expect(onApproval).toHaveBeenCalledWith(expect.objectContaining({ requestId: 42 }), true);
      expect(screen.getByTestId('approval-request-42')).toHaveTextContent('审批结果已提交');
    });
  });

  it('disables terminal approval requests', () => {
    render(
      <ChatApprovalMessage
        message={{ kind: 'approval', request_id: 7, status: 'approved', command: 'Already done' }}
        actions={{ onApproval: vi.fn() }}
        formatTime={() => '--:--'}
      />
    );

    expect(screen.getByRole('button', { name: '同意审批 7' })).toBeDisabled();
    expect(screen.getByRole('button', { name: '拒绝审批 7' })).toBeDisabled();
    expect(screen.getByTestId('approval-request-7')).toHaveTextContent('审批结果已提交');
  });
});

describe('chatApprovalModel', () => {
  it('normalizes approval identity and hint state', () => {
    expect(isApprovalMessage({ kind: 'approval' })).toBe(true);
    expect(isApprovalMessage({ kind: 'tool' })).toBe(false);
    expect(approvalRequestId({ request_id: '9.9' })).toBe(9);
    expect(isApprovalTerminal({ status: 'success' })).toBe(true);
    expect(approvalHintText({ requestId: 0, busy: false, resolved: false, terminal: false })).toBe('审批请求缺少编号');
    expect(approvalHintText({ requestId: 1, busy: true, resolved: false, terminal: false })).toBe('正在提交审批结果');
  });
});

describe('ChatApprovalMessage bug-locking', () => {
  afterEach(() => { vi.useRealTimers(); });

  const baseMessage = { kind: 'approval', requestId: 5, title: 'Test', text: 'Allow?', time: '2026-06-27T00:00:00Z' };

  it('calls onError when onApproval rejects', async () => {
    const onApproval = vi.fn().mockRejectedValue(new Error('network error'));
    const onError = vi.fn();
    render(
      <ChatApprovalMessage message={baseMessage} actions={{ onApproval, onError }} formatTime={() => '--'} />
    );
    fireEvent.click(screen.getByRole('button', { name: '同意审批 5' }));
    await waitFor(() => expect(onError).toHaveBeenCalledWith('approval.failed', 'network error'));
    expect(screen.getByRole('button', { name: '同意审批 5' })).not.toBeDisabled();
  });

  it('shows timeout errors and lets the user retry without auto double-submit', async () => {
    vi.useFakeTimers();
    const onApproval = vi.fn(() => new Promise(() => {})); // never resolves
    const onError = vi.fn();
    render(
      <ChatApprovalMessage message={baseMessage} actions={{ onApproval, onError }} formatTime={() => '--'} />
    );
    fireEvent.click(screen.getByRole('button', { name: '同意审批 5' }));
    await act(async () => { await vi.advanceTimersByTimeAsync(15_000); });
    // microtask flush so catch/finally in submitApproval completes
    await act(async () => { await Promise.resolve(); });
    expect(onError).toHaveBeenCalledWith('approval.failed', '审批提交超时');
    expect(screen.getByTestId('approval-request-5')).toHaveTextContent('审批提交超时');
    expect(screen.getByRole('button', { name: '同意审批 5' })).not.toBeDisabled();

    fireEvent.click(screen.getByRole('button', { name: '同意审批 5' }));
    expect(onApproval).toHaveBeenCalledTimes(2);
  });
});
