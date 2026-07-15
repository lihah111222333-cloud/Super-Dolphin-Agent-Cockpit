import { describe, expect, it } from 'vitest';
import {
  approvalRequestFromMessage,
  approvalSubmissionFor,
  isApprovalMessage,
} from './approvalDecision.js';
import approvalDecisionSource from './approvalDecision.js?raw';

describe('approvalDecision', () => {
  it('reuses the shared strict approval identity helper without creating another parser', () => {
    expect(approvalDecisionSource).toContain("from '../../../shared/api/approvalRequestId.js'");
    expect(approvalDecisionSource).toContain('requireApprovalIdentity(');
    expect(approvalDecisionSource).toContain('approvalIdentityFromFields(');
    expect(approvalDecisionSource).not.toMatch(/parseInt|parseFloat|Number\s*\(/);
  });

  it('adapts only valid approval wire requests and exact wire statuses', () => {
    expect(isApprovalMessage({ kind: 'approval' })).toBe(true);
    expect(isApprovalMessage({ kind: 'tool' })).toBe(false);
    expect(isApprovalMessage(null)).toBe(false);
    expect(isApprovalMessage([])).toBe(false);

    expect(approvalRequestFromMessage({
      kind: 'approval',
      sessionScope: 'session-a',
      callId: 'call-a',
      requestId: 11,
      status: 'pending',
    })).toEqual(expect.objectContaining({ sessionScope: 'session-a', callId: 'call-a', requestId: 11, status: 'pending', terminal: false }));
    expect(approvalRequestFromMessage({
      kind: 'approval',
      session_scope: 'session-b',
      call_id: 'call-b',
      request_id: 12,
      status: 'approved',
    })).toEqual(expect.objectContaining({ sessionScope: 'session-b', callId: 'call-b', requestId: 12, status: 'approved', terminal: true }));
    expect(approvalRequestFromMessage({
      kind: 'approval',
      sessionScope: 'session-c',
      callId: 'call-c',
      requestId: 13,
      status: 'rejected',
    })).toEqual(expect.objectContaining({ sessionScope: 'session-c', callId: 'call-c', requestId: 13, status: 'rejected', terminal: true }));

    for (const message of [
      { kind: 'approval', status: 'pending' },
      { kind: 'approval', requestId: 0, status: 'pending' },
      { kind: 'approval', requestId: '11', status: 'pending' },
      { kind: 'approval', requestId: '11', status: 'approved' },
      { kind: 'approval', requestId: 11, status: 'completed' },
    ]) {
      expect(() => approvalRequestFromMessage(message)).toThrow();
    }
  });

  it('models terminal approvals without identity as display-only', () => {
    expect(approvalRequestFromMessage({
      kind: 'approval',
      status: 'approved',
    })).toEqual({
      sessionScope: null,
      callId: null,
      requestId: null,
      status: 'approved',
      terminal: true,
      displayOnly: true,
    });
    expect(approvalRequestFromMessage({
      kind: 'approval',
      sessionScope: 'session-scope-a',
      callId: 'call-a',
      requestId: 31,
      status: 'rejected',
    })).toEqual({
      sessionScope: 'session-scope-a',
      callId: 'call-a',
      requestId: 31,
      status: 'rejected',
      terminal: true,
      displayOnly: false,
    });
  });

  it('accepts only approval choices and blocks terminal resubmission', () => {
    const pending = approvalRequestFromMessage({
      kind: 'approval',
      sessionScope: 'session-scope-a',
      callId: 'call-a',
      requestId: 21,
      status: 'pending',
    });
    const identity = { sessionScope: 'session-scope-a', callId: 'call-a', requestId: 21 };
    expect(approvalSubmissionFor(pending, 'approve')).toEqual({ ...identity, approved: true });
    expect(approvalSubmissionFor(pending, 'reject')).toEqual({ ...identity, approved: false });
    expect(() => approvalSubmissionFor(pending, 'allow')).toThrow();
    expect(() => approvalSubmissionFor(pending, true)).toThrow();

    const terminal = approvalRequestFromMessage({ kind: 'approval', ...identity, status: 'approved' });
    expect(() => approvalSubmissionFor(terminal, 'approve')).toThrow();
  });

  it('requires the complete composite identity for actionable approvals', () => {
    const pending = approvalRequestFromMessage({
      kind: 'approval',
      sessionScope: 'session-scope-a',
      callId: 'call-a',
      requestId: 21,
      status: 'pending',
    });
    expect(pending).toEqual(expect.objectContaining({
      sessionScope: 'session-scope-a',
      callId: 'call-a',
      requestId: 21,
      terminal: false,
      displayOnly: false,
    }));
    expect(approvalSubmissionFor(pending, 'approve')).toEqual({
      sessionScope: 'session-scope-a',
      callId: 'call-a',
      requestId: 21,
      approved: true,
    });

    for (const message of [
      { kind: 'approval', callId: 'call-a', requestId: 21, status: 'pending' },
      { kind: 'approval', sessionScope: 'session-scope-a', requestId: 21, status: 'pending' },
      { kind: 'approval', sessionScope: 'session-scope-a', callId: 'call-a', status: 'pending' },
      { kind: 'approval', sessionScope: 'session-a', session_scope: 'session-b', callId: 'call-a', requestId: 21, status: 'pending' },
      { kind: 'approval', sessionScope: 'session-a', callId: 'call-a', call_id: 'call-b', requestId: 21, status: 'pending' },
      { kind: 'approval', sessionScope: 'session-a', callId: 'call-a', requestId: 21, request_id: 22, status: 'pending' },
    ]) {
      expect(() => approvalRequestFromMessage(message)).toThrow();
    }
  });

  it('keeps the new domain approval-specific while allowing the existing wire kind field', () => {
    expect(approvalDecisionSource).toContain("kind === 'approval'");
    expect(approvalDecisionSource).not.toMatch(/DecisionKind|Capability|Ask|Plan/);
  });
});
