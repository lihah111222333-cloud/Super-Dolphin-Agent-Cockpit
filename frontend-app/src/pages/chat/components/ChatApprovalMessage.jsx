import { useState } from 'react';
import { CheckCircle2, X } from 'lucide-react';
import { MessageContent } from './MarkdownMessage.jsx';
import { approvalHintText, approvalRequestId, isApprovalTerminal } from './chatApprovalModel.js';

const APPROVE_LABEL = '\u540c\u610f';
const APPROVE_ARIA_PREFIX = '\u540c\u610f\u5ba1\u6279';
const REJECT_LABEL = '\u62d2\u7edd';
const REJECT_ARIA_PREFIX = '\u62d2\u7edd\u5ba1\u6279';

function ChatApprovalMessage({ message, actions, formatTime }) {
  const [busy, setBusy] = useState(false);
  const [resolved, setResolved] = useState(false);
  const requestId = approvalRequestId(message);
  const terminal = isApprovalTerminal(message);
  const disabled = requestId <= 0 || busy || resolved || terminal || typeof actions?.onApproval !== 'function';
  const title = (message.title || message.command || '审批请求').toString().trim();
  const hint = approvalHintText({ requestId, busy, resolved, terminal });

  const submitApproval = async (approved) => {
    if (disabled) return;
    setBusy(true);
    try {
      const ok = await actions.onApproval(message, approved);
      if (ok) setResolved(true);
    }
    catch (error) {
      actions.onError?.('approval.failed', error.message || String(error));
    }
    finally {
      setBusy(false);
    }
  };

  return (
    <article className="message assistant approval-message no-avatar" data-testid={`approval-request-${requestId || 'invalid'}`}>
      <div className="bubble approval-card">
        <header>
          <span>{title}</span>
          <time>{formatTime(message.time)}</time>
        </header>
        <MessageContent text={message.text || message.command || '审批请求'} actions={actions} />
        <div className="approval-footer">
          <span className="approval-hint">{hint}</span>
          <div className="approval-actions">
            <button
              type="button"
              className="approval-action approval-action--approve"
              aria-label={`${APPROVE_ARIA_PREFIX} ${requestId}`}
              disabled={disabled}
              onClick={() => { void submitApproval(true); }}
            >
              <CheckCircle2 size={14} />
              <span>{APPROVE_LABEL}</span>
            </button>
            <button
              type="button"
              className="approval-action approval-action--reject"
              aria-label={`${REJECT_ARIA_PREFIX} ${requestId}`}
              disabled={disabled}
              onClick={() => { void submitApproval(false); }}
            >
              <X size={14} />
              <span>{REJECT_LABEL}</span>
            </button>
          </div>
        </div>
      </div>
    </article>
  );
}

export { ChatApprovalMessage };
