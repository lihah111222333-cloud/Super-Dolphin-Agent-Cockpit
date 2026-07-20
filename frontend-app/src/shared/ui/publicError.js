// @ts-check

import { frontendDiagnosticCorrelationForError } from '../diagnostics/frontendDiagnosticCorrelation.js';
import { validatePublicErrorV1 } from '../contracts/turnContractValidators.js';

/** @typedef {{ code: string, title: string, message: string }} PublicErrorCopy */
/** @typedef {{ code?: unknown, diagnosticId?: unknown, recoveryActions?: unknown }} RemoteTerminalError */

const ACTION_PUBLIC_ERRORS = Object.freeze({
  'prompt-history.previous': Object.freeze({
    code: 'PROMPT_HISTORY_UNAVAILABLE',
    title: '无法浏览提示历史',
    message: '提示历史暂时不可用，草稿与光标位置已保留。',
  }),
  'prompt-history.next': Object.freeze({
    code: 'PROMPT_HISTORY_UNAVAILABLE',
    title: '无法浏览提示历史',
    message: '提示历史暂时不可用，草稿与光标位置已保留。',
  }),
});

/** @type {Readonly<Record<string, Readonly<PublicErrorCopy>>>} */
const REMOTE_TERMINAL_PUBLIC_ERRORS = Object.freeze({
  PROVIDER_FAILED: Object.freeze({
    code: 'PROVIDER_FAILED',
    title: '提供方暂不可用',
    message: '提供方未能完成本轮请求，请稍后重试。',
  }),
  TURN_TERMINATED: Object.freeze({
    code: 'TURN_TERMINATED',
    title: '本轮已结束',
    message: '本轮执行已结束。',
  }),
  FAILED: Object.freeze({
    code: 'FAILED',
    title: '本轮未完成',
    message: '本轮执行未完成，请稍后重试。',
  }),
});

const REMOTE_TERMINAL_FALLBACK = Object.freeze({
  code: 'REMOTE_TERMINAL_FAILED',
  title: '远端执行未完成',
  message: '远端执行未完成，请稍后重试。',
});

/** @param {() => string} factory @returns {string} */
function requiredDiagnosticId(factory) {
  if (typeof factory !== 'function') throw new TypeError('diagnosticIdFactory is required');
  const diagnosticId = factory();
  if (typeof diagnosticId !== 'string' || !diagnosticId.trim()) {
    throw new TypeError('diagnosticIdFactory must return a non-empty string');
  }
  return diagnosticId.trim();
}

/** @returns {string} */
function defaultDiagnosticIdFactory() {
  if (typeof globalThis.crypto?.randomUUID !== 'function') {
    throw new Error('crypto.randomUUID is required for UI action diagnostics');
  }
  return globalThis.crypto.randomUUID();
}

/**
 * A Wails bridge correlation is registered only after its trace is logged for
 * this exact error object. All unregistered exception data stays unobservable.
 * @param {unknown} error
 * @param {(() => string) | undefined} fallbackFactory
 * @returns {(() => string) | undefined}
 */
export function diagnosticIdFactoryForError(error, fallbackFactory) {
  const traceId = frontendDiagnosticCorrelationForError(error);
  return traceId ? () => traceId : fallbackFactory;
}

/** @param {string} actionId @param {{ diagnosticIdFactory?: () => string, retryable?: boolean }} [options] */
export function publicErrorForAction(actionId, { diagnosticIdFactory = defaultDiagnosticIdFactory, retryable = false } = {}) {
  if (typeof actionId !== 'string' || !actionId.trim()) throw new TypeError('actionId is required');
  const copy = /** @type {Record<string, PublicErrorCopy>} */ (ACTION_PUBLIC_ERRORS)[actionId] || {
    code: 'UI_ACTION_FAILED',
    title: '操作未完成',
    message: '操作失败，当前页面状态已保留。',
  };
  return Object.freeze({
    ...copy,
    diagnosticId: requiredDiagnosticId(diagnosticIdFactory),
    retryable: Boolean(retryable),
    recoveryActions: retryable ? Object.freeze(['retry']) : Object.freeze([]),
  });
}

/** @param {unknown} diagnosticId @returns {string} */
function safeRemoteDiagnosticId(diagnosticId) {
  const value = typeof diagnosticId === 'string' ? diagnosticId : '';
  try {
    validatePublicErrorV1({
      code: 'REMOTE_TERMINAL_FAILED',
      title: 'Remote terminal error',
      message: 'Remote terminal error',
      diagnosticId: value,
      retryable: false,
      recoveryActions: [],
    });
    return value;
  } catch {
    return 'diag-remote-terminal-error';
  }
}

/** @param {unknown} recoveryActions */
function safeRemoteRecoveryActions(recoveryActions) {
  return Array.isArray(recoveryActions) && recoveryActions.includes('copy_diagnostics')
    ? Object.freeze(['copy_diagnostics'])
    : Object.freeze([]);
}

/** @param {unknown} remoteError */
export function publicErrorForRemoteTerminal(remoteError) {
  const remote = /** @type {RemoteTerminalError} */ (
    typeof remoteError === 'object' && remoteError !== null ? remoteError : {}
  );
  const code = typeof remote.code === 'string'
    ? remote.code
    : '';
  const copy = Object.hasOwn(REMOTE_TERMINAL_PUBLIC_ERRORS, code)
    ? REMOTE_TERMINAL_PUBLIC_ERRORS[code]
    : REMOTE_TERMINAL_FALLBACK;
  return Object.freeze({
    ...copy,
    diagnosticId: safeRemoteDiagnosticId(remote.diagnosticId),
    retryable: false,
    recoveryActions: safeRemoteRecoveryActions(remote.recoveryActions),
  });
}

/** @param {string} code @param {() => string} [diagnosticIdFactory] */
export function publicErrorForSink(code, diagnosticIdFactory = defaultDiagnosticIdFactory) {
  /** @type {Record<string, { title: string, message: string }>} */
  const copies = {
    DIAGNOSTIC_ID_FACTORY_FAILED: { title: '诊断标识异常', message: '操作失败已使用本次运行的诊断标识记录。' },
    HEALTH_SINK_FAILED: { title: 'Health 记录异常', message: '操作失败已保留在本次运行的 Health 中。' },
    ON_ERROR_CALLBACK_FAILED: { title: '错误回调异常', message: '错误回调失败已记录到 Health。' },
    VISIBLE_FAILURE_SINK_FAILED: { title: '错误提示异常', message: '操作失败已记录到 Health。' },
  };
  const copy = copies[code];
  if (!copy) throw new TypeError(`unsupported UI action sink error code: ${code}`);
  return Object.freeze({
    code,
    ...copy,
    diagnosticId: requiredDiagnosticId(diagnosticIdFactory),
    retryable: false,
    recoveryActions: Object.freeze([]),
  });
}

export { defaultDiagnosticIdFactory };
