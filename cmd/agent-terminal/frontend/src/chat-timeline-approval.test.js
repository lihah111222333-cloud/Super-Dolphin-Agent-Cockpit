// @ts-nocheck
import { beforeEach, describe, expect, it, vi } from 'vitest';

const apiMock = vi.hoisted(() => ({
  callAPI: vi.fn(async () => ({ ok: true })),
}));

const logMock = vi.hoisted(() => ({
  logDebug: vi.fn(),
  logInfo: vi.fn(),
  logWarn: vi.fn(),
}));

vi.mock('./services/api.js', () => ({
  callAPI: apiMock.callAPI,
}));

vi.mock('./services/log.js', () => logMock);

vi.mock('./utils/assistant-markdown.js', () => ({
  renderAssistantMarkdown: vi.fn((text) => '<p>' + text + '</p>'),
  injectSentenceBreaks: vi.fn((text) => text),
}));

import { ChatTimeline } from './components/ChatTimeline.js';

function setupTimeline(overrides = {}, emit = vi.fn()) {
  return ChatTimeline.setup(
    {
      items: overrides.items ?? [],
      activeStatus: overrides.activeStatus ?? 'idle',
      activeStatusText: overrides.activeStatusText ?? '',
      activeStatusMeta: overrides.activeStatusMeta ?? '',
      pinnedPlanVisible: overrides.pinnedPlanVisible ?? false,
      pinnedPlanItemId: overrides.pinnedPlanItemId ?? null,
      resolveThreadDisplayName: overrides.resolveThreadDisplayName ?? null,
      presenceTarget: overrides.presenceTarget ?? null,
    },
    { emit },
  );
}

beforeEach(() => {
  apiMock.callAPI.mockReset().mockResolvedValue({ ok: true });
  logMock.logDebug.mockReset();
  logMock.logInfo.mockReset();
  logMock.logWarn.mockReset();
});

describe('ChatTimeline approval guards', () => {
  it('guards invalid request ids without calling the API and logs the missing-id branch', async () => {
    const vm = setupTimeline();
    const item = { kind: 'approval', requestId: 'oops', command: 'deploy' };

    await vm.respondApproval(item, true);

    expect(apiMock.callAPI).not.toHaveBeenCalled();
    expect(vm.approvalActionDisabled(item)).toBe(true);
    expect(vm.approvalHint(item)).toBe('当前审批不可交互，请重试');
    expect(vm.stateLabel(item)).toBe('不可交互');
    expect(logMock.logWarn).toHaveBeenCalledWith('ui', 'timeline.approval.request_id_missing', {
      command: 'deploy',
    });
  });

  it('blocks duplicate submissions while busy and stays resolved after success', async () => {
    let resolvePending;
    apiMock.callAPI.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolvePending = resolve;
        }),
    );
    const vm = setupTimeline();
    const item = { kind: 'approval', requestId: 11, command: 'deploy' };

    const submitPromise = vm.respondApproval(item, true);
    expect(vm.approvalActionDisabled(item)).toBe(true);
    expect(vm.approvalHint(item)).toBe('正在提交审批结果...');

    await vm.respondApproval(item, false);
    expect(apiMock.callAPI).toHaveBeenCalledTimes(1);

    resolvePending({ ok: true });
    await submitPromise;

    expect(apiMock.callAPI).toHaveBeenCalledWith('approval/respond', { requestId: 11, approved: true });
    expect(logMock.logInfo).toHaveBeenCalledWith('ui', 'timeline.approval.responded', { requestId: 11, approved: true });
    expect(vm.approvalActionDisabled(item)).toBe(true);
    expect(vm.approvalHint(item)).toBe('审批结果已提交');
    expect(vm.stateLabel(item)).toBe('已提交');

    await vm.respondApproval(item, false);
    expect(apiMock.callAPI).toHaveBeenCalledTimes(1);
  });

  it('returns to pending when the backend reports the approval is no longer pending', async () => {
    apiMock.callAPI.mockResolvedValueOnce({ ok: false });
    const vm = setupTimeline();
    const item = { kind: 'approval', requestId: 17, command: 'dangerous command' };

    await vm.respondApproval(item, false);

    expect(apiMock.callAPI).toHaveBeenCalledWith('approval/respond', { requestId: 17, approved: false });
    expect(logMock.logWarn).toHaveBeenCalledWith('ui', 'timeline.approval.respond_not_pending', { requestId: 17, approved: false });
    expect(vm.approvalActionDisabled(item)).toBe(false);
    expect(vm.approvalHint(item)).toBe('请选择同意或拒绝');
    expect(vm.stateLabel(item)).toBe('待确认');
  });

  it('returns to pending and logs the failure branch when the API throws', async () => {
    apiMock.callAPI.mockRejectedValueOnce(new Error('network down'));
    const vm = setupTimeline();
    const item = { kind: 'approval', requestId: 21, command: 'ship it' };

    await vm.respondApproval(item, true);

    expect(apiMock.callAPI).toHaveBeenCalledWith('approval/respond', { requestId: 21, approved: true });
    expect(logMock.logWarn).toHaveBeenCalledWith('ui', 'timeline.approval.respond_failed', {
      requestId: 21,
      approved: true,
      error: 'Error: network down',
    });
    expect(vm.approvalActionDisabled(item)).toBe(false);
    expect(vm.approvalHint(item)).toBe('请选择同意或拒绝');
    expect(vm.stateLabel(item)).toBe('待确认');
  });
});
