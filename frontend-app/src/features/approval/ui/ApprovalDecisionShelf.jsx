import { useLayoutEffect, useRef, useState } from 'react';
import { approvalSubmissionFor } from '../model/approvalDecision.js';

const APPROVE_CHOICE = 'approve';
const REJECT_CHOICE = 'reject';

function ApprovalDecisionShelf({ request, onConfirm }) {
  const requestId = request?.requestId;
  const [choice, setChoice] = useState('');
  const [busyRequestId, setBusyRequestId] = useState(0);
  const [resolvedRequestId, setResolvedRequestId] = useState(0);
  const [errorText, setErrorText] = useState('');
  const currentRequestRef = useRef({ requestId, token: {} });

  useLayoutEffect(() => {
    currentRequestRef.current = { requestId, token: {} };
    setChoice('');
    setBusyRequestId(0);
    setResolvedRequestId(0);
    setErrorText('');
  }, [requestId]);

  const busy = busyRequestId === requestId;
  const resolved = resolvedRequestId === requestId;
  const terminal = request?.terminal === true;
  const unavailable = terminal || busy || resolved || typeof onConfirm !== 'function';

  const confirmSelection = async () => {
    if (!choice || unavailable) return;
    approvalSubmissionFor(request, choice);
    const requestToken = currentRequestRef.current.token;
    setErrorText('');
    setBusyRequestId(requestId);
    try {
      const confirmed = await onConfirm(choice);
      if (confirmed === true && currentRequestRef.current.token === requestToken) {
        setResolvedRequestId(requestId);
      }
    }
    catch (error) {
      if (currentRequestRef.current.token === requestToken) {
        setErrorText(error instanceof Error ? error.message : String(error));
      }
    }
    finally {
      if (currentRequestRef.current.token === requestToken) {
        setBusyRequestId((current) => (current === requestId ? 0 : current));
      }
    }
  };

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
