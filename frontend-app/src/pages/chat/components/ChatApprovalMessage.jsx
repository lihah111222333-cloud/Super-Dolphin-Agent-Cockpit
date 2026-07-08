import { useState } from 'react';
import { useMutation } from '@tanstack/react-query';
import { CheckCircle2, X } from 'lucide-react';
import { MessageContent } from './MarkdownMessage.jsx';
import { approvalHintText, approvalRequestId, isApprovalTerminal } from './chatApprovalModel.js';
import { withTimeout } from '../../shared/pageShared.js';

const APPROVE_LABEL = '\u540c\u610f';
const APPROVE_ARIA_PREFIX = '\u540c\u610f\u5ba1\u6279';
const REJECT_LABEL = '\u62d2\u7edd';
const REJECT_ARIA_PREFIX = '\u62d2\u7edd\u5ba1\u6279';
const APPROVAL_SUBMIT_TIMEOUT_MS = 15_000;
const APPROVAL_SUBMIT_TIMEOUT_MESSAGE = '审批提交超时';

function approvalSubmitErrorText(error) {
  return error?.message || String(error);
}

function ChatApprovalMessage({ message, actions, formatTime }) {
  const [errorText, setErrorText] = useState('');
  const [resolved, setResolved] = useState(false);
  const requestId = approvalRequestId(message);
  const approvalMutation = useMutation({
    mutationFn: ({ approved }) => withTimeout(
      actions.onApproval(message, approved),
      APPROVAL_SUBMIT_TIMEOUT_MS,
      APPROVAL_SUBMIT_TIMEOUT_MESSAGE
    ),
    onMutate: () => {
      setErrorText('');
    },
    onSuccess: (ok) => {
      if (ok) setResolved(true);
    },
    onError: (error) => {
      const messageText = approvalSubmitErrorText(error);
      setErrorText(messageText);
      actions.onError?.('approval.failed', messageText);
    },
    retry: false,
  });
  const busy = approvalMutation.isPending && approvalMutation.variables?.requestId === requestId;
  const terminal = isApprovalTerminal(message);
  const disabled = requestId <= 0 || busy || resolved || terminal || typeof actions?.onApproval !== 'function';
  const title = (message.title || message.command || '审批请求').toString().trim();
  const hint = approvalHintText({ requestId, busy, resolved, terminal });

  const submitApproval = (approved) => {
    if (disabled) return;
    approvalMutation.mutate({ approved, requestId });
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
          <span className="approval-hint">{errorText || hint}</span>
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
