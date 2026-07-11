import React from 'react';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { ApprovalDecisionShelf } from './ApprovalDecisionShelf.jsx';
import approvalShelfSource from './ApprovalDecisionShelf.jsx?raw';

const pendingRequest = {
  requestId: 17,
  status: 'pending',
  terminal: false,
};

function deferred() {
  const pending = {};
  pending.promise = new Promise((resolve, reject) => {
    pending.resolve = resolve;
    pending.reject = reject;
  });
  return pending;
}

describe('ApprovalDecisionShelf', () => {
  it('keeps selection separate from explicit confirmation', async () => {
    const onConfirm = vi.fn().mockResolvedValue(true);
    render(<ApprovalDecisionShelf request={pendingRequest} onConfirm={onConfirm} />);

    const confirm = screen.getByRole('button', { name: '确认选择' });
    expect(confirm).toBeDisabled();

    fireEvent.click(screen.getByRole('button', { name: '同意' }));
    expect(onConfirm).not.toHaveBeenCalled();
    expect(screen.getByRole('button', { name: '同意' })).toHaveAttribute('aria-pressed', 'true');
    expect(confirm).toBeEnabled();

    fireEvent.click(confirm);
    await waitFor(() => expect(onConfirm).toHaveBeenCalledExactlyOnceWith('approve'));
  });

  it('blocks duplicate confirmation while submission is pending', async () => {
    const submission = deferred();
    const onConfirm = vi.fn(() => submission.promise);
    render(<ApprovalDecisionShelf request={pendingRequest} onConfirm={onConfirm} />);

    fireEvent.click(screen.getByRole('button', { name: '拒绝' }));
    const confirm = screen.getByRole('button', { name: '确认选择' });
    fireEvent.click(confirm);
    fireEvent.click(confirm);

    expect(onConfirm).toHaveBeenCalledExactlyOnceWith('reject');
    expect(confirm).toBeDisabled();
    expect(screen.getByTestId('approval-decision-shelf')).toHaveAttribute('aria-busy', 'true');

    submission.resolve(true);
    await waitFor(() => expect(screen.getByTestId('approval-decision-shelf')).toHaveAttribute('aria-busy', 'false'));
  });

  it('keeps a successful request locally resolved until a new request id resets the shelf', async () => {
    const onConfirm = vi.fn().mockResolvedValue(true);
    const { rerender } = render(<ApprovalDecisionShelf request={pendingRequest} onConfirm={onConfirm} />);

    fireEvent.click(screen.getByRole('button', { name: '同意' }));
    fireEvent.click(screen.getByRole('button', { name: '确认选择' }));

    await waitFor(() => expect(onConfirm).toHaveBeenCalledExactlyOnceWith('approve'));
    await waitFor(() => expect(screen.getByRole('button', { name: '确认选择' })).toBeDisabled());
    expect(screen.getByRole('button', { name: '同意' })).toBeDisabled();
    expect(screen.getByRole('button', { name: '拒绝' })).toBeDisabled();

    fireEvent.click(screen.getByRole('button', { name: '确认选择' }));
    expect(onConfirm).toHaveBeenCalledTimes(1);

    rerender(
      <ApprovalDecisionShelf
        request={{ ...pendingRequest, requestId: 18 }}
        onConfirm={onConfirm}
      />,
    );

    expect(screen.getByRole('button', { name: '同意' })).toHaveAttribute('aria-pressed', 'false');
    expect(screen.getByRole('button', { name: '拒绝' })).toHaveAttribute('aria-pressed', 'false');
    expect(screen.getByRole('button', { name: '确认选择' })).toBeDisabled();

    fireEvent.click(screen.getByRole('button', { name: '拒绝' }));
    fireEvent.click(screen.getByRole('button', { name: '确认选择' }));
    await waitFor(() => expect(onConfirm).toHaveBeenCalledTimes(2));
    expect(onConfirm).toHaveBeenLastCalledWith('reject');
  });

  it.each(['approved', 'rejected'])('never submits an initially terminal %s request', (status) => {
    const onConfirm = vi.fn();
    render(
      <ApprovalDecisionShelf
        request={{ requestId: 17, status, terminal: true }}
        onConfirm={onConfirm}
      />,
    );

    const approve = screen.getByRole('button', { name: '同意' });
    const reject = screen.getByRole('button', { name: '拒绝' });
    const confirm = screen.getByRole('button', { name: '确认选择' });
    expect(approve).toBeDisabled();
    expect(reject).toBeDisabled();
    expect(confirm).toBeDisabled();

    fireEvent.click(approve);
    fireEvent.click(reject);
    fireEvent.click(confirm);
    expect(onConfirm).not.toHaveBeenCalled();
  });

  it.each([
    ['resolves', (submission) => submission.resolve(true)],
    ['rejects', (submission) => submission.reject(new Error('stale request failed'))],
  ])('ignores an old request that %s after a new request is rendered', async (_result, settleOldRequest) => {
    const oldSubmission = deferred();
    const onConfirm = vi.fn()
      .mockImplementationOnce(() => oldSubmission.promise)
      .mockResolvedValueOnce(true);
    const { rerender } = render(<ApprovalDecisionShelf request={pendingRequest} onConfirm={onConfirm} />);

    fireEvent.click(screen.getByRole('button', { name: '同意' }));
    fireEvent.click(screen.getByRole('button', { name: '确认选择' }));
    expect(onConfirm).toHaveBeenCalledExactlyOnceWith('approve');

    rerender(
      <ApprovalDecisionShelf
        request={{ ...pendingRequest, requestId: 18 }}
        onConfirm={onConfirm}
      />,
    );
    fireEvent.click(screen.getByRole('button', { name: '拒绝' }));

    await act(async () => {
      settleOldRequest(oldSubmission);
      await Promise.resolve();
    });

    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(screen.getByTestId('approval-decision-shelf')).toHaveAttribute('aria-busy', 'false');
    expect(screen.getByRole('button', { name: '拒绝' })).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByRole('button', { name: '确认选择' })).toBeEnabled();

    fireEvent.click(screen.getByRole('button', { name: '确认选择' }));
    await waitFor(() => expect(onConfirm).toHaveBeenCalledTimes(2));
    expect(onConfirm).toHaveBeenLastCalledWith('reject');
  });

  it('retains the selected choice after failure and allows an explicit retry', async () => {
    const onConfirm = vi.fn()
      .mockRejectedValueOnce(new Error('approval unavailable'))
      .mockResolvedValueOnce(true);
    render(<ApprovalDecisionShelf request={pendingRequest} onConfirm={onConfirm} />);

    const reject = screen.getByRole('button', { name: '拒绝' });
    fireEvent.click(reject);
    fireEvent.click(screen.getByRole('button', { name: '确认选择' }));

    expect(await screen.findByRole('alert')).toHaveTextContent('approval unavailable');
    expect(reject).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByRole('button', { name: '确认选择' })).toBeEnabled();

    fireEvent.click(screen.getByRole('button', { name: '确认选择' }));
    await waitFor(() => expect(onConfirm).toHaveBeenCalledTimes(2));
    expect(onConfirm).toHaveBeenLastCalledWith('reject');
  });

  it('treats a false confirmation as unsettled and keeps the choice retryable', async () => {
    const onConfirm = vi.fn()
      .mockResolvedValueOnce(false)
      .mockResolvedValueOnce(true);
    render(<ApprovalDecisionShelf request={pendingRequest} onConfirm={onConfirm} />);

    const approve = screen.getByRole('button', { name: '同意' });
    fireEvent.click(approve);
    fireEvent.click(screen.getByRole('button', { name: '确认选择' }));

    await waitFor(() => expect(onConfirm).toHaveBeenCalledTimes(1));
    expect(approve).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByTestId('approval-decision-shelf')).toHaveAttribute('aria-busy', 'false');
    expect(screen.getByRole('button', { name: '确认选择' })).toBeEnabled();

    fireEvent.click(screen.getByRole('button', { name: '确认选择' }));
    await waitFor(() => expect(onConfirm).toHaveBeenCalledTimes(2));
    expect(onConfirm).toHaveBeenLastCalledWith('approve');
  });

  it('stays dependency-injected and approval-specific', () => {
    expect(approvalShelfSource).not.toMatch(/useClientStore|entities\/client|backendApi|shared\/api\/backend/);
    expect(approvalShelfSource).not.toMatch(/DecisionKind|Capability|Ask|Plan/);
  });
});
