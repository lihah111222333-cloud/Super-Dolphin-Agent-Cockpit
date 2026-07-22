import { useCallback, useEffect, useState } from 'react';
import { RefreshCw, RotateCcw, ShieldCheck, Siren } from 'lucide-react';
import { recoveryPublicErrorForFailure, recoveryPublicReasonForDisplay } from './recoveryClient.js';

const EMPTY_STATE = Object.freeze({ status: 'loading', value: null, error: null });

function fieldValue(value) {
	return value === '' ? 'None' : value;
}

function RecoveryApp({ client, confirmRestore = window.confirm }) {
	const [view, setView] = useState(EMPTY_STATE);
	const [activeAction, setActiveAction] = useState('state');

	const runAction = useCallback(async (action) => {
		setActiveAction(action);
		setView((current) => ({ ...current, status: 'loading', error: null }));
		try {
			const value = await client[action]();
			setView({ status: 'ready', value, error: null });
		} catch (error) {
			setView((current) => ({
				...current,
				status: 'error',
				error: recoveryPublicErrorForFailure(error),
			}));
		}
	}, [client]);

	useEffect(() => {
		void runAction('state');
	}, [runAction]);

	const restore = useCallback(() => {
		if (!confirmRestore('Restore the previous release?')) return;
		void runAction('restore');
	}, [confirmRestore, runAction]);

	const projection = view.value?.projection;
	const actions = view.value?.actions;
	const recoveryReason = recoveryPublicReasonForDisplay(projection?.reason);
	const busy = view.status === 'loading';

	return (
		<main className="recovery-shell">
			<header className="recovery-header">
				<div className="recovery-brand">
					<span className="recovery-mark" aria-hidden="true"><Siren size={22} /></span>
					<div>
						<p className="recovery-kicker">Safe Mode</p>
						<h1>Super Dolphin Recovery</h1>
					</div>
				</div>
				<span className="recovery-status" data-status={view.status}>
					{busy ? 'Checking' : projection?.state || 'Recovery'}
				</span>
			</header>

			{view.error && (
				<div className="recovery-error" role="alert">
					<strong>{view.error.title}</strong>
					<p>{view.error.publicMessage}</p>
					<p>Diagnostic ID: {view.error.diagnosticId}</p>
				</div>
			)}

			{projection && (
				<>
					<section className="recovery-summary" aria-labelledby="recovery-reason-title">
						<p id="recovery-reason-title">Recovery reason</p>
						<strong>{recoveryReason ? recoveryReason.publicMessage : 'None'}</strong>
						{recoveryReason && <p>Diagnostic ID: {recoveryReason.diagnosticId}</p>}
					</section>

					<section className="recovery-details" aria-label="Recovery state">
						<dl>
							<div><dt>Transaction</dt><dd>{fieldValue(projection.transactionId)}</dd></div>
							<div><dt>Attempt</dt><dd>{fieldValue(projection.attemptId)}</dd></div>
							<div><dt>State</dt><dd>{fieldValue(projection.state)}</dd></div>
							<div><dt>Lease owner</dt><dd>{fieldValue(projection.leaseOwner)}</dd></div>
							<div><dt>Lease generation</dt><dd>{projection.leaseGeneration}</dd></div>
							<div><dt>Candidate SHA-256</dt><dd title={projection.candidateSHA256}>{fieldValue(projection.candidateSHA256)}</dd></div>
							<div><dt>Last action</dt><dd>{fieldValue(view.value.lastAction)}</dd></div>
						</dl>
					</section>
				</>
			)}

			<footer className="recovery-actions">
				<button type="button" onClick={() => void runAction('check')} disabled={busy || !actions?.check}>
					<ShieldCheck size={18} /> Check
				</button>
				<button type="button" onClick={() => void runAction('retry')} disabled={busy || !actions?.retry}>
					<RefreshCw size={18} className={busy && activeAction === 'retry' ? 'spin' : ''} /> Retry
				</button>
				<button type="button" className="restore" onClick={restore} disabled={busy || !actions?.restore}>
					<RotateCcw size={18} /> Restore
				</button>
			</footer>
		</main>
	);
}

export { RecoveryApp };
