import { describe, expect, it } from 'vitest';
import { publicErrorForRemoteTerminal } from './publicError.js';

describe('publicErrorForRemoteTerminal', () => {
  it('uses the local registry instead of remote display copy', () => {
    const secret = 'provider-token=secret-value /private/agent/config.go\nstack: remote failure';
    const error = publicErrorForRemoteTerminal({
      code: 'PROVIDER_FAILED',
      title: secret,
      message: secret,
      diagnosticId: 'diag-remote-1',
      retryable: true,
      recoveryActions: ['retry'],
    });

    expect(error).toEqual({
      code: 'PROVIDER_FAILED',
      title: '提供方暂不可用',
      message: '提供方未能完成本轮请求，请稍后重试。',
      diagnosticId: 'diag-remote-1',
      retryable: false,
      recoveryActions: [],
    });
    expect(JSON.stringify(error)).not.toMatch(/secret-value|\/private\/|stack:/);
  });

  it('uses the fixed local fallback for an unknown remote code and diagnostic ID', () => {
    const error = publicErrorForRemoteTerminal({
      code: 'toString',
      title: 'provider-token=secret-value',
      message: 'TypeError: /private/agent/config.go\nstack: remote failure',
      diagnosticId: 'provider-token=secret-value',
      recoveryActions: ['copy_diagnostics', 'retry'],
    });

    expect(error).toEqual({
      code: 'REMOTE_TERMINAL_FAILED',
      title: '远端执行未完成',
      message: '远端执行未完成，请稍后重试。',
      diagnosticId: 'diag-remote-terminal-error',
      retryable: false,
      recoveryActions: ['copy_diagnostics'],
    });
    expect(JSON.stringify(error)).not.toMatch(/secret-value|\/private\/|stack:/);
  });
});
