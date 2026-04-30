// @ts-nocheck
// Phase 3.10b · 长任务进度协议（前端只读侧）
//
// agent 接到 watchdog "继续" 戳 / 任务完成后，按 handoff 模板里 Phase 3.10a
// 写入的协议段，自行写：
//   - _internal/progress/<taskId>.md  追加式，行数代表已推进的步骤
//   - _internal/done/<taskId>.md      存在即视为任务完成
//
// 本 composable 只做读侧原语，给 watchdog 决策用：
//   - readProgressLineCount(taskId) -> Promise<number>  非空行数（0 = 未写 / 不存在）
//   - readDoneMarker(taskId)        -> Promise<boolean> 文件存在且内容非空 = 完成
//
// 错误处理：
//   - not found / not configured → 静默返回默认值（agent 还没写过是正常状态）
//   - 其它错误 → logWarn 后返回默认值，不破降级（缺 progress 仍走旧累计上限）

import { callAPI as defaultCallAPI } from '../services/api.js';
import { logWarn } from '../services/log.js';

const PROGRESS_PREFIX = '_internal/progress/';
const DONE_PREFIX = '_internal/done/';

function isNotFoundError(err) {
  const msg = ((err && err.message) || String(err || '')).toLowerCase();
  return msg.includes('not found') || msg.includes('not configured');
}

function buildProgressPath(taskId) { return PROGRESS_PREFIX + taskId + '.md'; }
function buildDonePath(taskId)     { return DONE_PREFIX + taskId + '.md'; }

function countNonBlankLines(content) {
  if (!content) return 0;
  const text = String(content);
  let n = 0;
  for (const line of text.split('\n')) {
    if (line.trim().length > 0) n++;
  }
  return n;
}

export function useThreadProgressProtocol(opts = {}) {
  const callAPIFn = typeof opts.callAPIFn === 'function' ? opts.callAPIFn : defaultCallAPI;

  async function readProgressLineCount(taskId) {
    const tid = (taskId || '').toString().trim();
    if (!tid) return 0;
    const path = buildProgressPath(tid);
    try {
      const detail = await callAPIFn('ui/memory/shared-file/get', { path });
      const content = (detail && detail.content) || '';
      return countNonBlankLines(content);
    } catch (err) {
      if (isNotFoundError(err)) return 0;
      logWarn('ui', 'thread_progress_protocol.progress_read_failed', {
        task_id: tid, error: (err && err.message) || String(err),
      });
      return 0;
    }
  }

  async function readDoneMarker(taskId) {
    const tid = (taskId || '').toString().trim();
    if (!tid) return false;
    const path = buildDonePath(tid);
    try {
      const detail = await callAPIFn('ui/memory/shared-file/get', { path });
      const content = (detail && detail.content) || '';
      return content.toString().trim().length > 0;
    } catch (err) {
      if (isNotFoundError(err)) return false;
      logWarn('ui', 'thread_progress_protocol.done_read_failed', {
        task_id: tid, error: (err && err.message) || String(err),
      });
      return false;
    }
  }

  return { readProgressLineCount, readDoneMarker };
}

export const _USE_THREAD_PROGRESS_PROTOCOL_CONSTANTS = Object.freeze({
  PROGRESS_PREFIX, DONE_PREFIX,
});
