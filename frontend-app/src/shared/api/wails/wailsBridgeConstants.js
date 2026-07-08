// @ts-nocheck

const METHOD_IDS = Object.freeze({
  CALL_API: 2963398832,
  GET_BUILD_INFO: 2341363104,
  SAVE_CLIPBOARD_IMAGE: 3733550318,
  SELECT_FILES: 4126105303,
  SELECT_PROJECT_DIR: 3694631468,
});

const WAILS_RUNTIME_MODULE = '/wails/runtime.js';
const RPC_RESULT_PREVIEW_LIMIT = 1200;
const FRONTEND_TRACE_INGEST_METHOD = 'observability/frontend/ingest';
const FRONTEND_TRACE_BATCH_LIMIT = 50;
const FRONTEND_TRACE_QUEUE_LIMIT = 500;
const FRONTEND_TRACE_RPC_SLOW_MS = 1000;
const FRONTEND_TRACE_ALLOWED_PHASES = new Set([
  'frontend.rpc.start',
  'frontend.rpc.done',
  'frontend.rpc.failed',
  'runtime.rpc.pending',
  'runtime.rpc.send.done',
  'runtime.rpc.send.failed',
  'runtime.rpc.settled',
  'runtime.rpc.timeout',
  'runtime.rpc.failed',
  'frontend.warning',
  'frontend.patch.apply.slow',
  'frontend.render.slow',
]);
const FRONTEND_TRACE_ALLOWED_METADATA_KEYS = new Set([
  'req_id',
  'component',
  'react_phase',
  'pending_count',
  'attempt',
]);
const FRONTEND_TRACE_ALLOWED_STATUSES = new Set(['ok', 'slow', 'error']);
const FRONTEND_RUNTIME_TRACE_DEFAULT_PHASES = new Set([
  'runtime.rpc.pending',
  'runtime.rpc.send.done',
  'runtime.rpc.settled',
]);
const FRONTEND_RUNTIME_TRACE_SKIP_METHODS = new Set([FRONTEND_TRACE_INGEST_METHOD, 'ui/log']);
// 误判防护：FRONTEND_TRACE_FORBIDDEN_KEYS 阻断 prompt/content/tool result 进入前端 trace。
const FRONTEND_TRACE_FORBIDDEN_KEYS = new Set([
  'result_preview',
  'prompt',
  'user_prompt',
  'user_message',
  'message_text',
  'text',
  'content',
  'file_content',
  'file_contents',
  'tool_result',
  'tool_results',
  'stack',
  'raw_stack',
]);
const BRIDGE_ERROR_DATA_SAFE_KEYS = new Set(['message', 'code', 'name', 'type', 'status']);
const FRONTEND_TRACE_SECRET_ASSIGNMENT_RE =
  /\b(?:api[_\s-]?key|auth[_\s-]?token|access[_\s-]?token|refresh[_\s-]?token|id[_\s-]?token|authorization|credential(?:s)?|password|secret|token)\b\s*[:=]\s*["']?[^"',\s}]+/i;
const FRONTEND_TRACE_TOKEN_VALUE_RE = /\b(?:bearer|basic)\s+[a-z0-9._~+/=-]{8,}|\bsk-[a-z0-9][a-z0-9_-]{6,}\b/i;
const FRONTEND_TRACE_POSIX_PATH_RE =
  /(?:^|[\s("'`=])\/(?:home|users|var|tmp|etc|opt|private|workspace|mnt|volumes|root)\/[^\s"'`<>]*/i;
const FRONTEND_TRACE_WINDOWS_PATH_RE = /\b[a-z]:\\(?:[^\\/:*?"<>|\r\n]+\\?)+/i;
const FRONTEND_TRACE_UNC_PATH_RE = /\\\\[a-z0-9._-]+\\[^\s"'`<>|]+/i;
const FRONTEND_TRACE_SENSITIVE_TEXT_PATTERNS = [
  FRONTEND_TRACE_SECRET_ASSIGNMENT_RE,
  FRONTEND_TRACE_TOKEN_VALUE_RE,
  FRONTEND_TRACE_POSIX_PATH_RE,
  FRONTEND_TRACE_WINDOWS_PATH_RE,
  FRONTEND_TRACE_UNC_PATH_RE,
];

export {
  METHOD_IDS, WAILS_RUNTIME_MODULE, RPC_RESULT_PREVIEW_LIMIT,
  FRONTEND_TRACE_INGEST_METHOD, FRONTEND_TRACE_BATCH_LIMIT, FRONTEND_TRACE_QUEUE_LIMIT, FRONTEND_TRACE_RPC_SLOW_MS,
  FRONTEND_TRACE_ALLOWED_PHASES, FRONTEND_TRACE_ALLOWED_METADATA_KEYS, FRONTEND_TRACE_ALLOWED_STATUSES,
  FRONTEND_RUNTIME_TRACE_DEFAULT_PHASES, FRONTEND_RUNTIME_TRACE_SKIP_METHODS, FRONTEND_TRACE_FORBIDDEN_KEYS,
  BRIDGE_ERROR_DATA_SAFE_KEYS, FRONTEND_TRACE_SENSITIVE_TEXT_PATTERNS,
};
