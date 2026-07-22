import { useLayoutEffect, useRef, useState } from 'react';
import { approvalIdentityKey } from '../../../shared/api/approvalRequestId.js';
import { approvalSubmissionFor } from '../model/approvalDecision.js';
import { runUIAction } from '../../../shared/ui/runUIAction.js';

const APPROVE_CHOICE = 'approve';
const REJECT_CHOICE = 'reject';

function ApprovalDecisionShelf({ request, onConfirm }) {
  const requestKey = !request || request.displayOnly === true ? '' : approvalIdentityKey(request);
  const [choice, setChoice] = useState('');
  const [busyRequestKey, setBusyRequestKey] = useState('');
  const [resolvedRequestKey, setResolvedRequestKey] = useState('');
  const [errorText, setErrorText] = useState('');
  const currentRequestRef = useRef({ requestKey, token: {} });

  useLayoutEffect(() => {
    currentRequestRef.current = { requestKey, token: {} };
    setChoice('');
    setBusyRequestKey('');
    setResolvedRequestKey('');
    setErrorText('');
  }, [requestKey]);

  const busy = Boolean(requestKey) && busyRequestKey === requestKey;
  const resolved = Boolean(requestKey) && resolvedRequestKey === requestKey;
  const terminal = request?.terminal === true;
  const unavailable = terminal || busy || resolved || typeof onConfirm !== 'function';

  const confirmSelection = () => runUIAction('approval.respond', async () => {
    if (!choice || unavailable) return;
    approvalSubmissionFor(request, choice);
    const requestToken = currentRequestRef.current.token;
    setErrorText('');
    setBusyRequestKey(requestKey);
    try {
      const confirmed = await onConfirm(choice);
      if (confirmed === true && currentRequestRef.current.token === requestToken) setResolvedRequestKey(requestKey);
    }
    finally {
      if (currentRequestRef.current.token === requestToken) {
        setBusyRequestKey((current) => (current === requestKey ? '' : current));
      }
    }
  }, {
    onError: (publicError) => {
      if (currentRequestRef.current.requestKey === requestKey) setErrorText(publicError.message);
    },
  });

  return (
    <div
      className="approval-actions"
      data-testid="approval-decision-shelf"
      aria-busy={busy ? 'true' : 'false'}
    >
      <button
        type="button"
        className="approval-action approval-action--approve"
        aria-pressed={choice === APPROVE_CHOICE ? 'true' : 'false'}
        disabled={unavailable}
        onClick={() => setChoice(APPROVE_CHOICE)}
      >
        同意
      </button>
      <button
        type="button"
        className="approval-action approval-action--reject"
        aria-pressed={choice === REJECT_CHOICE ? 'true' : 'false'}
        disabled={unavailable}
        onClick={() => setChoice(REJECT_CHOICE)}
      >
        拒绝
      </button>
      <button
        type="button"
        className="approval-action approval-action--confirm"
        disabled={unavailable || !choice}
        onClick={() => { void confirmSelection(); }}
      >
        确认选择
      </button>
      {errorText ? <p role="alert">{errorText}</p> : null}
    </div>
  );
}

export { ApprovalDecisionShelf };
