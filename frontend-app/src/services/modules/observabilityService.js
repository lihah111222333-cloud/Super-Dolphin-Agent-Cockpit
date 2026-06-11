import {
  copyTextToClipboard as copyTextToClipboardBackend,
  getObservabilityTrace as getObservabilityTraceBackend,
  listObservabilityRecent as listObservabilityRecentBackend,
} from '../../shared/api/backendApi.js';
import { adaptObservabilityResult } from '../../adapters/observabilityAdapter.js';
import { runServiceRequest } from '../apiClient.js';

async function listObservabilityRecent(params = {}) {
  return runServiceRequest(async () => {
    const response = await listObservabilityRecentBackend(params);
    return adaptObservabilityResult(response);
  }, '查询最近链路日志失败');
}

async function getObservabilityTrace(params) {
  return runServiceRequest(async () => {
    const response = await getObservabilityTraceBackend(params);
    return adaptObservabilityResult(response);
  }, '查询 Trace 失败');
}

async function copyTextToClipboard(text) {
  return runServiceRequest(() => copyTextToClipboardBackend(text), '复制文本失败');
}

export { copyTextToClipboard, getObservabilityTrace, listObservabilityRecent };
