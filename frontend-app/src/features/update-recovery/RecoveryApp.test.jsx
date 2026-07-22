import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { RecoveryApp } from './RecoveryApp.jsx';
import { recoveryPublicErrorForCode } from './recoveryClient.js';

const RECOVERY_DIAGNOSTIC_ID = '1111111111111111111111111111111111111111111111111111111111111111';
const RECOVERY_STARTUP_MESSAGE = 'Recovery mode started because the previous startup did not complete.';

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
			reason: recoveryPublicErrorForCode('RECOVERY_STARTUP_FAILED', RECOVERY_DIAGNOSTIC_ID),
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
		expect(screen.getByText(RECOVERY_STARTUP_MESSAGE)).toBeVisible();
		expect(screen.getByText('transaction-1')).toBeVisible();
		expect(screen.queryByText(/normal ready/i)).not.toBeInTheDocument();

		fireEvent.click(screen.getByRole('button', { name: 'Check' }));
		await waitFor(() => expect(client.check).toHaveBeenCalledTimes(1));
		expect(screen.getByText('check')).toBeVisible();
	});

	it('does not render a raw Recovery reason', async () => {
		const rawCause = 'secret=sk-recovery path=/private/recovery dsn=postgres://private';
		const client = {
			state: vi.fn().mockResolvedValue(recoveryState({
				projection: { ...recoveryState().projection, reason: rawCause },
			})),
			check: vi.fn(),
			retry: vi.fn(),
			restore: vi.fn(),
		};
		render(<RecoveryApp client={client} />);

		await screen.findByRole('heading', { name: 'Super Dolphin Recovery' });
		expect(screen.queryByText(rawCause)).not.toBeInTheDocument();
		expect(screen.getByText('Recovery mode started because the previous startup did not complete.')).toBeVisible();
	});

	it.each(['check', 'retry', 'restore'])('does not render a raw %s action failure', async (action) => {
		const rawCause = 'secret=sk-recovery path=/private/recovery dsn=postgres://private';
		const client = {
			state: vi.fn().mockResolvedValue(recoveryState()),
			check: vi.fn(),
			retry: vi.fn(),
			restore: vi.fn(),
		};
		client[action].mockRejectedValue(new Error(rawCause));
		render(<RecoveryApp client={client} confirmRestore={() => true} />);

		await screen.findByText(RECOVERY_STARTUP_MESSAGE);
		fireEvent.click(screen.getByRole('button', { name: action[0].toUpperCase() + action.slice(1) }));

		const alert = await screen.findByRole('alert');
		expect(alert).not.toHaveTextContent(rawCause);
		expect(alert).toHaveTextContent('Recovery action could not be completed');
	});

	it('requires explicit confirmation before restore', async () => {
		const client = {
			state: vi.fn().mockResolvedValue(recoveryState()),
			check: vi.fn(),
			retry: vi.fn(),
			restore: vi.fn(),
		};
		render(<RecoveryApp client={client} confirmRestore={() => false} />);
		await screen.findByText(RECOVERY_STARTUP_MESSAGE);

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
		await screen.findByText(RECOVERY_STARTUP_MESSAGE);

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
		await screen.findByText(RECOVERY_STARTUP_MESSAGE);
		fireEvent.click(screen.getByRole('button', { name: 'Restore' }));
		await waitFor(() => expect(client.restore).toHaveBeenCalledTimes(1));

		expect(screen.getByRole('button', { name: 'Check' })).toBeDisabled();
		expect(screen.getByRole('button', { name: 'Retry' })).toBeDisabled();
		expect(screen.getByRole('button', { name: 'Restore' })).toBeDisabled();
	});
});
