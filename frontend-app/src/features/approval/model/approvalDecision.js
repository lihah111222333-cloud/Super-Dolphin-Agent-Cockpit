import { positiveApprovalRequestIdFromFields } from '../../../shared/api/approvalRequestId.js';

const APPROVAL_STATUSES = new Set(['pending', 'approved', 'rejected']);

function isApprovalMessage(message) {
  return Boolean(
    message &&
    !Array.isArray(message) &&
    typeof message === 'object' &&
    message.kind === 'approval'
  );
}

function validatedApprovalRequest(request) {
  if (!request || Array.isArray(request) || typeof request !== 'object') {
    throw new TypeError('审批请求必须是对象');
  }
  const requestId = positiveApprovalRequestIdFromFields(request);
  if (requestId <= 0) throw new TypeError('审批请求缺少有效编号');
  const status = request.status;
  if (!APPROVAL_STATUSES.has(status)) throw new TypeError('审批请求状态无效');
  return {
    requestId,
    status,
    terminal: status !== 'pending',
  };
}

function approvalRequestFromMessage(message) {
  if (!isApprovalMessage(message)) throw new TypeError('消息不是审批请求');
  return validatedApprovalRequest(message);
}

function approvalSubmissionFor(request, choice) {
  const normalized = validatedApprovalRequest(request);
  if (normalized.terminal) throw new TypeError('审批请求已经结束');
  if (choice === 'approve') return { requestId: normalized.requestId, approved: true };
  if (choice === 'reject') return { requestId: normalized.requestId, approved: false };
  throw new TypeError('审批选择无效');
}

export {
  approvalRequestFromMessage,
  approvalSubmissionFor,
  isApprovalMessage,
};
