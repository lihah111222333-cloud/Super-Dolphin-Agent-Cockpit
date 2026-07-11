import { describe, expect, it } from 'vitest';
import {
  approvalRequestFromMessage,
  approvalSubmissionFor,
  isApprovalMessage,
} from './approvalDecision.js';
import approvalDecisionSource from './approvalDecision.js?raw';

describe('approvalDecision', () => {
  it('reuses the shared strict request id helper without creating another parser', () => {
    expect(approvalDecisionSource).toContain("from '../../../shared/api/approvalRequestId.js'");
    expect(approvalDecisionSource).toContain('positiveApprovalRequestIdFromFields(');
    expect(approvalDecisionSource).not.toMatch(/parseInt|parseFloat|Number\s*\(/);
  });

  it('adapts only valid approval wire requests and exact wire statuses', () => {
    expect(isApprovalMessage({ kind: 'approval' })).toBe(true);
    expect(isApprovalMessage({ kind: 'tool' })).toBe(false);
    expect(isApprovalMessage(null)).toBe(false);
    expect(isApprovalMessage([])).toBe(false);

    expect(approvalRequestFromMessage({
      kind: 'approval',
      requestId: 11,
      status: 'pending',
    })).toEqual(expect.objectContaining({ requestId: 11, status: 'pending', terminal: false }));
    expect(approvalRequestFromMessage({
      kind: 'approval',
      request_id: 12,
      status: 'approved',
    })).toEqual(expect.objectContaining({ requestId: 12, status: 'approved', terminal: true }));
    expect(approvalRequestFromMessage({
      kind: 'approval',
      requestId: 13,
      status: 'rejected',
    })).toEqual(expect.objectContaining({ requestId: 13, status: 'rejected', terminal: true }));

    for (const message of [
      { kind: 'approval', requestId: 0, status: 'pending' },
      { kind: 'approval', requestId: '11', status: 'pending' },
      { kind: 'approval', requestId: 11, status: 'completed' },
    ]) {
      expect(() => approvalRequestFromMessage(message)).toThrow();
    }
  });

  it('accepts only approval choices and blocks terminal resubmission', () => {
    const pending = approvalRequestFromMessage({ kind: 'approval', requestId: 21, status: 'pending' });
    expect(approvalSubmissionFor(pending, 'approve')).toEqual({ requestId: 21, approved: true });
    expect(approvalSubmissionFor(pending, 'reject')).toEqual({ requestId: 21, approved: false });
    expect(() => approvalSubmissionFor(pending, 'allow')).toThrow();
    expect(() => approvalSubmissionFor(pending, true)).toThrow();

    const terminal = approvalRequestFromMessage({ kind: 'approval', requestId: 21, status: 'approved' });
    expect(() => approvalSubmissionFor(terminal, 'approve')).toThrow();
  });

  it('keeps the new domain approval-specific while allowing the existing wire kind field', () => {
    expect(approvalDecisionSource).toContain("kind === 'approval'");
    expect(approvalDecisionSource).not.toMatch(/DecisionKind|Capability|Ask|Plan/);
  });
});
