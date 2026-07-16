import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { RecoveryApp } from './RecoveryApp.jsx';

function recoveryState(overrides = {}) {
  return {
    mode: 'recovery',
    lastAction: 'state',
    actions: { check: true, retry: true, restore: true },
    projection: {
      transactionId: 'transaction-1',
      attemptId: 'attempt-1',
      state: 'probation',
      leasePresent: true,
      leaseOwner: 'guard-1',
      leaseGeneration: 2,
      candidateSHA256: 'abcdef0123456789',
      reason: 'normal preflight failed',
    },
    ...overrides,
  };
}

describe('RecoveryApp', () => {
  it('shows typed Recovery state and exposes only Recovery actions', async () => {
    const client = {
      state: vi.fn().mockResolvedValue(recoveryState()),
      check: vi.fn().mockResolvedValue(recoveryState({ lastAction: 'check' })),
      retry: vi.fn().mockResolvedValue(recoveryState({ lastAction: 'retry' })),
      restore: vi.fn().mockResolvedValue(recoveryState({ lastAction: 'restore' })),
    };

    render(<RecoveryApp client={client} confirmRestore={() => true} />);

    expect(await screen.findByRole('heading', { name: 'Super Dolphin Recovery' })).toBeVisible();
    expect(screen.getByText('normal preflight failed')).toBeVisible();
    expect(screen.getByText('transaction-1')).toBeVisible();
    expect(screen.queryByText(/normal ready/i)).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Check' }));
    await waitFor(() => expect(client.check).toHaveBeenCalledTimes(1));
    expect(screen.getByText('check')).toBeVisible();
  });

  it('requires explicit confirmation before restore', async () => {
    const client = {
      state: vi.fn().mockResolvedValue(recoveryState()),
      check: vi.fn(),
      retry: vi.fn(),
      restore: vi.fn(),
    };
    render(<RecoveryApp client={client} confirmRestore={() => false} />);
    await screen.findByText('normal preflight failed');

    fireEvent.click(screen.getByRole('button', { name: 'Restore' }));

    expect(client.restore).not.toHaveBeenCalled();
  });

  it('disables transaction actions for normal-preflight Recovery', async () => {
    const client = {
      state: vi.fn().mockResolvedValue(recoveryState({
        actions: { check: false, retry: false, restore: false },
        projection: {
          ...recoveryState().projection,
          transactionId: '',
          attemptId: '',
          state: '',
          leasePresent: false,
          leaseOwner: '',
          leaseGeneration: 0,
          candidateSHA256: '',
        },
      })),
      check: vi.fn(),
      retry: vi.fn(),
      restore: vi.fn(),
    };
    render(<RecoveryApp client={client} />);
    await screen.findByText('normal preflight failed');

    expect(screen.getByRole('button', { name: 'Check' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Retry' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Restore' })).toBeDisabled();
  });

  it('disables every action after restore returns rolled_back state', async () => {
    const client = {
      state: vi.fn().mockResolvedValue(recoveryState()),
      check: vi.fn(),
      retry: vi.fn(),
      restore: vi.fn().mockResolvedValue(recoveryState({
        lastAction: 'restore',
        actions: { check: false, retry: false, restore: false },
        projection: {
          ...recoveryState().projection,
          state: 'rolled_back',
          leasePresent: false,
          leaseOwner: '',
          leaseGeneration: 0,
        },
      })),
    };
    render(<RecoveryApp client={client} confirmRestore={() => true} />);
    await screen.findByText('normal preflight failed');
    fireEvent.click(screen.getByRole('button', { name: 'Restore' }));
    await waitFor(() => expect(client.restore).toHaveBeenCalledTimes(1));

    expect(screen.getByRole('button', { name: 'Check' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Retry' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Restore' })).toBeDisabled();
  });
});
