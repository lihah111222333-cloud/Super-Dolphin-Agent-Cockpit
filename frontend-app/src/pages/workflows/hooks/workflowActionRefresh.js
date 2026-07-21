import { runBackgroundAction } from '../../../shared/ui/runUIAction.js';
import { firstText, textValue } from '../../shared/pageShared.js';

function workflowRefreshNotice(message, refreshError) {
  if (!refreshError) return message;
  runBackgroundAction('workflow.refresh-after-action', () => Promise.reject(refreshError));
  return `${message}，但刷新状态失败，请手动刷新。`;
}

async function refreshWorkflowListResult(list, fallbackItems) {
  try {
    return { error: null, items: await list.refreshDags() };
  } catch (err) {
    return { error: err, items: fallbackItems };
  }
}

async function refreshWorkflowDetailResult(refresh, targetDagKey, runKey = '') {
  if (!refresh || !textValue(targetDagKey)) return null;
  try {
    await refresh.refreshDetail(targetDagKey, runKey);
    return null;
  } catch (err) {
    return err;
  }
}

function firstWorkflowRefreshError(...errors) {
  return errors.find(Boolean) || null;
}

function isIdempotencyKeyExhaustedError(error) {
  return firstText(error?.message, error?.data?.message, error?.data)
    .toLowerCase()
    .includes('idempotency key exhausted:');
}

async function refreshWorkflowAfterAction({ fallbackItems, list, refresh, runKey = '', targetDagKey }) {
  const listResult = list
    ? await refreshWorkflowListResult(list, fallbackItems)
    : { error: null, items: fallbackItems };
  const detailError = refresh
    ? await refreshWorkflowDetailResult(refresh, targetDagKey, runKey)
    : null;
  return {
    error: firstWorkflowRefreshError(listResult.error, detailError),
    items: listResult.items,
  };
}

export {
  isIdempotencyKeyExhaustedError,
  refreshWorkflowAfterAction,
  refreshWorkflowDetailResult,
  workflowRefreshNotice,
};
