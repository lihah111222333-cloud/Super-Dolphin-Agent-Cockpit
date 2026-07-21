import {
  approvalIdentityFromFields,
  requireApprovalIdentity,
} from '../../../shared/api/support/approvalRequestId.js';

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
  const status = request.status;
  if (!APPROVAL_STATUSES.has(status)) throw new TypeError('审批请求状态无效');
  const terminal = status !== 'pending';
  const partialIdentity = approvalIdentityFromFields(request, '审批请求');
  const identity = terminal
    ? partialIdentity
    : { ...requireApprovalIdentity(request, '审批请求'), complete: true };
  return {
    sessionScope: identity.sessionScope || null,
    callId: identity.callId || null,
    requestId: identity.requestId > 0 ? identity.requestId : null,
    status,
    terminal,
    displayOnly: terminal && identity.complete !== true,
  };
}

function approvalRequestFromMessage(message) {
  if (!isApprovalMessage(message)) throw new TypeError('消息不是审批请求');
  return validatedApprovalRequest(message);
}

function approvalSubmissionFor(request, choice) {
  const normalized = validatedApprovalRequest(request);
  if (normalized.terminal) throw new TypeError('审批请求已经结束');
  const identity = {
    sessionScope: normalized.sessionScope,
    callId: normalized.callId,
    requestId: normalized.requestId,
  };
  if (choice === 'approve') return { ...identity, approved: true };
  if (choice === 'reject') return { ...identity, approved: false };
  throw new TypeError('审批选择无效');
}

export {
  approvalRequestFromMessage,
  approvalSubmissionFor,
  isApprovalMessage,
};
