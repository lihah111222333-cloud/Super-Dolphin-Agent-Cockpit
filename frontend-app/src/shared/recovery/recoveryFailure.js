const RECOVERY_FAILURE_FIELDS = Object.freeze([
  'code',
  'retryable',
  'action',
  'transaction_id',
]);

const RECOVERY_FAILURE_SPECS = Object.freeze({
  UPDATE_TRANSACTION_AMBIGUOUS: Object.freeze({ retryable: false, action: 'preserve_state_export_diagnostics' }),
  UPDATE_SIGNATURE_INVALID: Object.freeze({ retryable: false, action: 'preserve_state_export_diagnostics' }),
  UPDATE_INTEGRITY_INVALID: Object.freeze({ retryable: false, action: 'preserve_state_export_diagnostics' }),
  MCP_SCHEMA_CAPACITY_EXHAUSTED: Object.freeze({ retryable: true, action: 'wait_then_retry' }),
  MCP_SCHEMA_REAP_FAILED: Object.freeze({ retryable: false, action: 'restart_application' }),
  MCP_SCHEMA_DIGEST_MISMATCH: Object.freeze({ retryable: false, action: 'preserve_state_export_diagnostics' }),
  MCP_SCHEMA_PROTOCOL_VIOLATION: Object.freeze({ retryable: false, action: 'preserve_state_export_diagnostics' }),
});

const RECOVERY_ACTION_MESSAGES = Object.freeze({
  wait_then_retry: '工具资源暂时不可用，请稍后重试。',
  restart_application: '工具恢复失败，请重启应用后重试。',
  preserve_state_export_diagnostics: '工具完整性异常，请保留当前状态并导出诊断信息。',
});

const RECOVERY_CODE_MESSAGES = Object.freeze({
  UPDATE_TRANSACTION_AMBIGUOUS: '更新状态无法安全确认，请保持现场并导出诊断信息。',
  UPDATE_SIGNATURE_INVALID: '更新完整性校验失败，请保持现场并导出诊断信息。',
  UPDATE_INTEGRITY_INVALID: '更新完整性校验失败，请保持现场并导出诊断信息。',
});

const RECOVERY_FAILURE_ACTIONS = Object.freeze(Object.keys(RECOVERY_ACTION_MESSAGES));

const INVALID_RECOVERY_DATA_MESSAGE = '请求失败，恢复信息无效。';

function requireExactFailureFields(value) {
  const compareFields = (left, right) => left.localeCompare(right);
  const actual = Object.keys(value).sort(compareFields);
  const expected = [...RECOVERY_FAILURE_FIELDS].sort(compareFields);
  if (actual.length !== expected.length || actual.some((field, index) => field !== expected[index])) {
    throw new TypeError(`Recovery failure fields must exactly match ${expected.join(',')}`);
  }
}

function normalizeRecoveryFailure(failure) {
  if (!failure || typeof failure !== 'object' || Array.isArray(failure)) {
    throw new TypeError('Recovery failure must be an object');
  }
  requireExactFailureFields(failure);
  if (typeof failure.code !== 'string' || typeof failure.action !== 'string' ||
    typeof failure.retryable !== 'boolean' || typeof failure.transaction_id !== 'string') {
    throw new TypeError('Recovery failure fields have invalid types');
  }
  if (failure.code === '') {
    if (failure.retryable || failure.action !== '' || failure.transaction_id !== '') {
      throw new TypeError('Recovery empty failure fields are inconsistent');
    }
  }
  else {
	if (!RECOVERY_FAILURE_ACTIONS.includes(failure.action)) {
	  throw new TypeError('Recovery failure action is unsupported');
	}
	if (failure.retryable !== (failure.action === 'wait_then_retry')) {
	  throw new TypeError('Recovery failure retryability is inconsistent with its action');
	}
    const spec = RECOVERY_FAILURE_SPECS[failure.code];
    if (!spec || spec.retryable !== failure.retryable || spec.action !== failure.action) {
      throw new TypeError('Recovery failure code, retryability, and action are inconsistent');
    }
  }
  return Object.freeze({
    code: failure.code,
    retryable: failure.retryable,
    action: failure.action,
    transactionId: failure.transaction_id,
  });
}

function recoveryActionMessageFromRPCError(error) {
  if (!error || typeof error !== 'object' || !Object.hasOwn(error, 'data')) return '';
  try {
    const failure = normalizeRecoveryFailure(error.data);
    if (!failure.code) return INVALID_RECOVERY_DATA_MESSAGE;
    return RECOVERY_CODE_MESSAGES[failure.code] || RECOVERY_ACTION_MESSAGES[failure.action] || INVALID_RECOVERY_DATA_MESSAGE;
  }
  catch {
    return INVALID_RECOVERY_DATA_MESSAGE;
  }
}

export {
  INVALID_RECOVERY_DATA_MESSAGE,
  RECOVERY_ACTION_MESSAGES,
  RECOVERY_FAILURE_FIELDS,
  normalizeRecoveryFailure,
  recoveryActionMessageFromRPCError,
};
