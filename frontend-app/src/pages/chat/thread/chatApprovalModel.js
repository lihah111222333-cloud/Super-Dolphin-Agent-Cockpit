import { positiveApprovalRequestIdFromFields } from '../../../shared/api/approvalRequestId.js';
import { trimmedText } from '../markdown/markdownMessageModel.js';

const APPROVAL_TERMINAL_STATUSES = new Set(['approved', 'rejected', 'denied', 'resolved', 'completed', 'complete', 'done', 'success', 'succeeded']);

function isApprovalMessage(message) {
  return trimmedText(message?.kind).toLowerCase() === 'approval';
}

function approvalRequestId(message) {
  return positiveApprovalRequestIdFromFields(message);
}

function isApprovalTerminal(message) {
  const status = trimmedText(message?.status).toLowerCase();
  return Boolean(status && APPROVAL_TERMINAL_STATUSES.has(status));
}

function approvalHintText({ requestId, busy, resolved, terminal }) {
  if (requestId <= 0) return '审批请求缺少编号';
  if (busy) return '正在提交审批结果';
  if (resolved || terminal) return '审批结果已提交';
  return '等待审批';
}

export { approvalHintText, approvalRequestId, isApprovalMessage, isApprovalTerminal };
