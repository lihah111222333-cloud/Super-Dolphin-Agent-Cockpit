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

function requiredDiagnosticId(factory) {
  if (typeof factory !== 'function') throw new TypeError('diagnosticIdFactory is required');
  const diagnosticId = factory();
  if (typeof diagnosticId !== 'string' || !diagnosticId.trim()) {
    throw new TypeError('diagnosticIdFactory must return a non-empty string');
  }
  return diagnosticId.trim();
}

function defaultDiagnosticIdFactory() {
  if (typeof globalThis.crypto?.randomUUID !== 'function') {
    throw new Error('crypto.randomUUID is required for UI action diagnostics');
  }
  return globalThis.crypto.randomUUID();
}

export function publicErrorForAction(actionId, { diagnosticIdFactory = defaultDiagnosticIdFactory, retryable = false } = {}) {
  if (typeof actionId !== 'string' || !actionId.trim()) throw new TypeError('actionId is required');
  const copy = ACTION_PUBLIC_ERRORS[actionId] || {
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

export function publicErrorForSink(code, diagnosticIdFactory = defaultDiagnosticIdFactory) {
  const copies = {
    DIAGNOSTIC_ID_FACTORY_FAILED: { title: '诊断标识异常', message: '操作失败已使用本次运行的诊断标识记录。' },
    EMERGENCY_HEALTH_SINK_FAILED: { title: '应急 Health 异常', message: '操作失败已保留在本次运行的最终 Health 中。' },
    HEALTH_SINK_FAILED: { title: 'Health 记录异常', message: '操作失败已保留在本次运行的 Health 中。' },
    ON_ERROR_CALLBACK_FAILED: { title: '错误回调异常', message: '错误回调失败已记录到 Health。' },
    UI_ACTION_REPORTING_FAILED: { title: '错误报告异常', message: '操作失败已保留在本次运行的最终 Health 中。' },
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
