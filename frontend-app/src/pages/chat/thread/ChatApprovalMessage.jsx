import { approvalRequestFromMessage, approvalSubmissionFor } from '../../../features/approval/model/approvalDecision.js';
import { ApprovalDecisionShelf } from '../../../features/approval/ui/ApprovalDecisionShelf.jsx';
import { MessageContent } from '../markdown/MarkdownMessage.jsx';

function ChatApprovalMessage({ message, actions, formatTime }) {
  const request = approvalRequestFromMessage(message);
  const title = (message.title || message.command || '审批请求').toString().trim();
  const confirmApproval = typeof actions?.onApproval === 'function'
    ? async (choice) => {
        const submission = approvalSubmissionFor(request, choice);
        try {
          return await actions.onApproval(submission, submission.approved);
        }
        catch (error) {
          const messageText = error instanceof Error ? error.message : String(error);
          actions.onError?.('approval.failed', messageText);
          throw error instanceof Error ? error : new Error(messageText);
        }
      }
    : undefined;

  return (
    <article className="message assistant approval-message no-avatar" data-testid={`approval-request-${request.requestId}`}>
      <div className="bubble approval-card">
        <header>
          <span>{title}</span>
          <time>{formatTime(message.time)}</time>
        </header>
        <MessageContent text={message.text || message.command || '审批请求'} actions={actions} />
        <div className="approval-footer">
          <span className="approval-hint">{request.terminal ? '审批结果已提交' : '等待审批'}</span>
          <ApprovalDecisionShelf request={request} onConfirm={confirmApproval} />
        </div>
      </div>
    </article>
  );
}

export { ChatApprovalMessage };
