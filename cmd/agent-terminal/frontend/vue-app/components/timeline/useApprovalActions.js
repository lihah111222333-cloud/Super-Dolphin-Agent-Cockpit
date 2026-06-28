import { ref } from '../../../lib/vue.esm-browser.prod.js';
import { callAPI } from '../../services/api.js';
import { logInfo, logWarn } from '../../services/log.js';

function approvalRequestId(item) {
  const raw = Number(item?.requestId);
  if (!Number.isFinite(raw)) return 0;
  const requestId = Math.trunc(raw);
  return requestId > 0 ? requestId : 0;
}

function createApprovalActionDisabled(approvalBusyByRequestId, approvalResolvedByRequestId) {
  return function approvalActionDisabled(item) {
    const requestId = approvalRequestId(item);
    if (requestId <= 0) return true;
    if (approvalBusyByRequestId.value[requestId]) return true;
    if (approvalResolvedByRequestId.value[requestId]) return true;
    return false;
  };
}

function createApprovalHint(approvalBusyByRequestId, approvalResolvedByRequestId) {
  return function approvalHint(item) {
    const requestId = approvalRequestId(item);
    if (requestId <= 0) return '当前审批不可交互，请重试';
    if (approvalBusyByRequestId.value[requestId]) return '正在提交审批结果...';
    if (approvalResolvedByRequestId.value[requestId]) return '审批结果已提交';
    return '请选择同意或拒绝';
  };
}

function createRespondApproval(approvalBusyByRequestId, approvalResolvedByRequestId) {
  return async function respondApproval(item, approved) {
    const requestId = approvalRequestId(item);
    const decision = Boolean(approved);
    if (requestId <= 0) {
      logWarn('ui', 'timeline.approval.request_id_missing', {
        command: (item?.command || '').toString(),
      });
      return;
    }
    if (approvalBusyByRequestId.value[requestId] || approvalResolvedByRequestId.value[requestId]) {
      return;
    }

    approvalBusyByRequestId.value = {
      ...approvalBusyByRequestId.value,
      [requestId]: true,
    };
    try {
      const result = await callAPI('approval/respond', { requestId, approved: decision });
      if (Boolean(result?.ok)) {
        approvalResolvedByRequestId.value = {
          ...approvalResolvedByRequestId.value,
          [requestId]: true,
        };
        logInfo('ui', 'timeline.approval.responded', { requestId, approved: decision });
      } else {
        logWarn('ui', 'timeline.approval.respond_not_pending', { requestId, approved: decision });
      }
    } catch (error) {
      logWarn('ui', 'timeline.approval.respond_failed', {
        requestId,
        approved: decision,
        error: String(error || ''),
      });
    } finally {
      approvalBusyByRequestId.value = {
        ...approvalBusyByRequestId.value,
        [requestId]: false,
      };
    }
  };
}

export function useApprovalActions() {
  const approvalBusyByRequestId = ref({});
  const approvalResolvedByRequestId = ref({});
  const approvalActionDisabled = createApprovalActionDisabled(approvalBusyByRequestId, approvalResolvedByRequestId);
  const approvalHint = createApprovalHint(approvalBusyByRequestId, approvalResolvedByRequestId);
  const respondApproval = createRespondApproval(approvalBusyByRequestId, approvalResolvedByRequestId);

  return {
    approvalBusyByRequestId,
    approvalResolvedByRequestId,
    approvalRequestId,
    approvalActionDisabled,
    approvalHint,
    respondApproval,
  };
}
