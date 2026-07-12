import React from 'react';
import { existsSync, readFileSync } from 'node:fs';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ChatApprovalMessage } from './ChatApprovalMessage.jsx';
import { Conversation } from './Conversation.jsx';

function confirmChoice(choiceLabel) {
  fireEvent.click(screen.getByRole('button', { name: choiceLabel }));
  fireEvent.click(screen.getByRole('button', { name: '确认选择' }));
}

function deferred() {
  const pending = {};
  pending.promise = new Promise((resolve, reject) => {
    pending.resolve = resolve;
    pending.reject = reject;
  });
  return pending;
}

describe('ChatApprovalMessage', () => {
  it('separates selection from confirmation before mapping the approval choice to wire boolean', async () => {
    const onApproval = vi.fn().mockResolvedValue(true);
    const message = {
      kind: 'approval',
      requestId: 42,
      status: 'pending',
      title: 'Run command',
      text: 'Allow command execution?',
      time: '2026-06-15T08:00:00Z',
    };

    render(
      <ChatApprovalMessage
        message={message}
        actions={{ onApproval }}
        formatTime={() => '16:00'}
      />
    );

    const confirm = screen.getByRole('button', { name: '确认选择' });
    expect(confirm).toBeDisabled();
    fireEvent.click(screen.getByRole('button', { name: '同意' }));
    expect(onApproval).not.toHaveBeenCalled();
    fireEvent.click(confirm);

    await waitFor(() => {
      expect(onApproval).toHaveBeenCalledExactlyOnceWith(message, true);
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

    expect(screen.getByRole('button', { name: '同意' })).toBeDisabled();
    expect(screen.getByRole('button', { name: '拒绝' })).toBeDisabled();
    expect(screen.getByRole('button', { name: '确认选择' })).toBeDisabled();
  });

  it('renders a terminal approval without request identity as display-only', () => {
    render(
      <ChatApprovalMessage
        message={{ id: 'approval-fallback', kind: 'approval', status: 'rejected', command: 'Fallback resolved' }}
        actions={{ onApproval: vi.fn() }}
        formatTime={() => '--:--'}
      />
    );

    expect(screen.getByText('审批结果已提交')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '同意' })).toBeDisabled();
    expect(screen.getByRole('button', { name: '拒绝' })).toBeDisabled();
    expect(screen.getByRole('button', { name: '确认选择' })).toBeDisabled();
  });

  it('lets Conversation consume a display-only terminal approval', () => {
    render(
      <Conversation
        messages={[{ id: 'approval-fallback', kind: 'approval', status: 'approved', command: 'Fallback resolved' }]}
        sending={false}
        activeThreadId="thread-1"
        activeThread={{ id: 'thread-1', status: 'idle' }}
        statusEntry={null}
        activeTurn={null}
        timelineBlocked={false}
        messageActions={{ onApproval: vi.fn() }}
        store={{ smoothStreaming: false }}
        draft=""
        setDraft={vi.fn()}
        sendMessage={vi.fn()}
        attachments={[]}
        attachPaths={vi.fn()}
        attachDroppedFiles={vi.fn()}
        removeAttachment={vi.fn()}
        selectFiles={vi.fn()}
        projectPath="/tmp/project"
        modelThreadId="thread-1"
      />
    );

    expect(screen.getAllByText('Fallback resolved')).toHaveLength(2);
    expect(screen.getByText('审批结果已提交')).toBeInTheDocument();
  });

  it('fails closed before rendering malformed approval request ids', () => {
    vi.spyOn(console, 'error').mockImplementation(() => {});
    expect(() => render(
      <ChatApprovalMessage
        message={{ kind: 'approval', request_id: '7.5', status: 'pending', command: 'Malformed id' }}
        actions={{ onApproval: vi.fn() }}
        formatTime={() => '--:--'}
      />,
    )).toThrow();
  });

  it('submits only once while the confirmed approval is pending', async () => {
    const pending = deferred();
    const onApproval = vi.fn(() => pending.promise);
    render(
      <ChatApprovalMessage
        message={{ kind: 'approval', requestId: 9, status: 'pending', command: 'Deploy' }}
        actions={{ onApproval }}
        formatTime={() => '--:--'}
      />,
    );

    confirmChoice('同意');
    fireEvent.click(screen.getByRole('button', { name: '确认选择' }));
    expect(onApproval).toHaveBeenCalledTimes(1);

    pending.resolve(true);
    await act(async () => { await pending.promise; });
  });

  it('keeps a false approval result selected and retryable', async () => {
    const onApproval = vi.fn()
      .mockResolvedValueOnce(false)
      .mockResolvedValueOnce(true);
    render(
      <ChatApprovalMessage
        message={{ kind: 'approval', requestId: 10, status: 'pending', command: 'Deploy' }}
        actions={{ onApproval }}
        formatTime={() => '--:--'}
      />,
    );

    confirmChoice('拒绝');
    await waitFor(() => {
      expect(onApproval).toHaveBeenCalledTimes(1);
      expect(screen.getByRole('button', { name: '确认选择' })).toBeEnabled();
    });
    expect(screen.getByRole('button', { name: '拒绝' })).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByRole('button', { name: '确认选择' })).toBeEnabled();

    fireEvent.click(screen.getByRole('button', { name: '确认选择' }));
    await waitFor(() => expect(onApproval).toHaveBeenCalledTimes(2));
    expect(onApproval).toHaveBeenLastCalledWith(expect.objectContaining({ requestId: 10 }), false);
  });

  it('imports the single approval adapter and leaves the legacy model as deletion or re-export only', () => {
    const source = readFileSync('src/pages/chat/thread/ChatApprovalMessage.jsx', 'utf8');
    expect(source).toContain('features/approval/model/approvalDecision.js');
    expect(source).not.toContain('./chatApprovalModel.js');

    const legacyPath = 'src/pages/chat/thread/chatApprovalModel.js';
    if (!existsSync(legacyPath)) return;
    const legacySource = readFileSync(legacyPath, 'utf8');
    expect(legacySource).toMatch(/export\s*\{[\s\S]*\}\s*from\s*['"][^'"]*features\/approval\/model\/approvalDecision\.js['"]/);
    expect(legacySource).not.toMatch(/\bfunction\b|new Set\s*\(/);
  });

  it('delegates approval timeout ownership to the client store', () => {
    const source = readFileSync('src/pages/chat/thread/ChatApprovalMessage.jsx', 'utf8');
    expect(source).not.toMatch(/Promise\.race|setTimeout|APPROVAL_TIMEOUT/);
  });
});

describe('ChatApprovalMessage bug-locking', () => {
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  const baseMessage = { kind: 'approval', requestId: 5, status: 'pending', title: 'Test', text: 'Allow?', time: '2026-06-27T00:00:00Z' };

  it('calls onError when onApproval rejects', async () => {
    const onApproval = vi.fn().mockRejectedValue(new Error('network error'));
    const onError = vi.fn();
    render(
      <ChatApprovalMessage message={baseMessage} actions={{ onApproval, onError }} formatTime={() => '--'} />
    );
    confirmChoice('同意');
    await waitFor(() => expect(onError).toHaveBeenCalledWith('approval.failed', 'network error'));
    expect(screen.getByRole('button', { name: '同意' })).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByRole('button', { name: '确认选择' })).toBeEnabled();
  });

  it('shows store-owned timeout errors and lets the user retry explicitly', async () => {
    const timeoutError = Object.assign(new Error('审批提交超时'), { code: 'APPROVAL_SUBMIT_TIMEOUT' });
    const onApproval = vi.fn()
      .mockRejectedValueOnce(timeoutError)
      .mockResolvedValueOnce(true);
    const onError = vi.fn();
    render(
      <ChatApprovalMessage message={baseMessage} actions={{ onApproval, onError }} formatTime={() => '--'} />
    );
    confirmChoice('同意');
    await waitFor(() => expect(onError).toHaveBeenCalledWith('approval.failed', '审批提交超时'));
    expect(screen.getByRole('alert')).toHaveTextContent('审批提交超时');
    expect(screen.getByRole('button', { name: '同意' })).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByRole('button', { name: '确认选择' })).toBeEnabled();

    fireEvent.click(screen.getByRole('button', { name: '确认选择' }));
    await waitFor(() => expect(onApproval).toHaveBeenCalledTimes(2));
  });
});
