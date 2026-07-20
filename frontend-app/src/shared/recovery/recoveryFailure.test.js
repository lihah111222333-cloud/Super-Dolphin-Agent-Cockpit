import { describe, expect, it } from 'vitest';

import {
  INVALID_RECOVERY_DATA_MESSAGE,
  normalizeRecoveryFailure,
  recoveryActionMessageFromRPCError,
} from './recoveryFailure.js';

describe('recovery failure RPC contract', () => {
  it.each([
    ['MCP_SCHEMA_CAPACITY_EXHAUSTED', true, 'wait_then_retry', '工具资源暂时不可用，请稍后重试。'],
    ['MCP_SCHEMA_REAP_FAILED', false, 'restart_application', '工具恢复失败，请重启应用后重试。'],
    ['MCP_SCHEMA_DIGEST_MISMATCH', false, 'preserve_state_export_diagnostics', '工具完整性异常，请保留当前状态并导出诊断信息。'],
    ['MCP_SCHEMA_PROTOCOL_VIOLATION', false, 'preserve_state_export_diagnostics', '工具完整性异常，请保留当前状态并导出诊断信息。'],
  ])('maps %s to a fixed action message', (code, retryable, action, message) => {
    const error = new Error('secret backend detail');
    error.data = { code, retryable, action, transaction_id: '' };

    expect(recoveryActionMessageFromRPCError(error)).toBe(message);
  });

  it('rejects extra, unknown, or inconsistent fields without exposing the backend message', () => {
    const malformed = new Error('secret backend detail');
    malformed.data = {
      code: 'MCP_SCHEMA_REAP_FAILED',
      retryable: false,
      action: 'restart_application',
      transaction_id: '',
      raw_error: 'postgres://secret',
    };
    expect(recoveryActionMessageFromRPCError(malformed)).toBe(INVALID_RECOVERY_DATA_MESSAGE);
    expect(() => normalizeRecoveryFailure({
      code: 'MCP_SCHEMA_UNKNOWN',
      retryable: false,
      action: 'restart_application',
      transaction_id: '',
    })).toThrow('code, retryability, and action are inconsistent');
  });

  it('leaves ordinary RPC errors on the existing error path', () => {
    expect(recoveryActionMessageFromRPCError(new Error('ordinary failure'))).toBe('');
  });
});
