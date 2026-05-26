// @ts-nocheck
import { beforeEach, describe, expect, it, vi } from 'vitest';

const apiMock = vi.hoisted(() => ({
  callAPI: vi.fn(() => Promise.resolve(null)),
}));

vi.mock('./services/api.js', () => ({
  callAPI: apiMock.callAPI,
}));

vi.mock('./services/log.js', () => ({
  logDebug: vi.fn(),
  logInfo: vi.fn(),
  logWarn: vi.fn(),
}));

import { MemoryCenterPage } from './pages/MemoryCenterPage.js';

function setupPage(overview = {}, overrides = {}) {
  const emit = vi.fn();
  const props = {
    model: {
      overview,
      private: { entries: [], ...(overrides.private || {}) },
      team: { entries: [], ...(overrides.team || {}) },
      ...(overrides.model || {}),
    },
  };
  const vm = MemoryCenterPage.setup(props, { emit });
  return { vm, emit, props };
}

beforeEach(() => {
  apiMock.callAPI.mockReset();
  apiMock.callAPI.mockResolvedValue(null);
});

describe('MemoryCenterPage auto-dream card', () => {
  it('runtime true with no intent → 已开启', () => {
    const { vm } = setupPage({ enabled: true, autoDreamEnabled: true });
    expect(vm.autoDreamEnabled.value).toBe(true);
    expect(vm.autoDreamStatusLabel.value).toBe('已开启');
    expect(vm.autoDreamPendingRestart.value).toBe(false);
  });

  it('runtime false with no intent → 已关闭', () => {
    const { vm } = setupPage({ enabled: true, autoDreamEnabled: false });
    expect(vm.autoDreamEnabled.value).toBe(false);
    expect(vm.autoDreamStatusLabel.value).toBe('已关闭');
    expect(vm.autoDreamPendingRestart.value).toBe(false);
  });

  it('intent=true overrides runtime=false and flags pending restart', () => {
    const { vm } = setupPage({ enabled: true, autoDreamEnabled: false, autoDreamIntent: true });
    expect(vm.autoDreamEnabled.value).toBe(true);
    expect(vm.autoDreamStatusLabel.value).toBe('已开启');
    expect(vm.autoDreamPendingRestart.value).toBe(true);
  });

  it('intent=false overrides runtime=true and flags pending restart', () => {
    const { vm } = setupPage({ enabled: true, autoDreamEnabled: true, autoDreamIntent: false });
    expect(vm.autoDreamEnabled.value).toBe(false);
    expect(vm.autoDreamStatusLabel.value).toBe('已关闭');
    expect(vm.autoDreamPendingRestart.value).toBe(true);
  });

  it('intent matching runtime → no pending restart', () => {
    const { vm } = setupPage({ enabled: true, autoDreamEnabled: true, autoDreamIntent: true });
    expect(vm.autoDreamPendingRestart.value).toBe(false);
  });

  it('toggleAutoDream calls RPC, sets warning notice, and emits refresh', async () => {
    const { vm, emit } = setupPage({ enabled: true, autoDreamEnabled: false });
    await vm.toggleAutoDream();
    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/memory/auto-dream/set-intent', { enabled: true });
    expect(vm.notice.level).toBe('warning');
    expect(vm.notice.message).toContain('开启');
    expect(vm.notice.message).toContain('重启 agent-terminal 后生效');
    expect(emit).toHaveBeenCalledWith('refresh');
    expect(vm.autoDreamToggling.value).toBe(false);
  });

  it('toggleAutoDream from enabled → disabled passes false', async () => {
    const { vm } = setupPage({ enabled: true, autoDreamEnabled: true, autoDreamIntent: true });
    await vm.toggleAutoDream();
    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/memory/auto-dream/set-intent', { enabled: false });
    expect(vm.notice.message).toContain('关闭');
  });

  it('toggleAutoDream surfaces RPC error as error notice', async () => {
    const { vm, emit } = setupPage({ enabled: true, autoDreamEnabled: false });
    apiMock.callAPI.mockRejectedValueOnce(new Error('boom'));
    await vm.toggleAutoDream();
    expect(vm.notice.level).toBe('error');
    expect(vm.notice.message).toContain('boom');
    expect(emit).not.toHaveBeenCalledWith('refresh');
    expect(vm.autoDreamToggling.value).toBe(false);
  });
});

describe('MemoryCenterPage editor validation', () => {
  it('does not save when description is blank', async () => {
    const { vm } = setupPage({ projectRoot: '/repo' });

    vm.memoryEditor.openCreate('private');
    Object.assign(vm.memoryEditor.form, {
      name: 'Release owner',
      description: '   ',
      content: 'Primary source is the runbook.',
    });

    await vm.memoryEditor.save();

    expect(apiMock.callAPI).not.toHaveBeenCalledWith('ui/memory/entry/upsert', expect.anything());
    expect(vm.notice.level).toBe('error');
    expect(vm.notice.message).toBe('请先填写描述');
  });

  it('trims description before save', async () => {
    const { vm } = setupPage({ projectRoot: '/repo' });

    vm.memoryEditor.openCreate('private');
    Object.assign(vm.memoryEditor.form, {
      name: 'Release owner',
      description: '  Who owns releases  ',
      content: 'Primary source is the runbook.',
    });

    await vm.memoryEditor.save();

    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/memory/entry/upsert', expect.objectContaining({
      cwd: '/repo',
      target: 'private',
      name: 'Release owner',
      description: 'Who owns releases',
      type: 'project',
      content: 'Primary source is the runbook.',
    }));
  });
});

describe('MemoryCenterPage health and destructive actions', () => {

  it('does not expose a shared files footer link from the memory center', () => {
    expect(MemoryCenterPage.template).not.toContain('memory-center-open-shared-files');
    expect(MemoryCenterPage.template).not.toContain('查看共享文件');
    expect(MemoryCenterPage.emits).toEqual(['refresh']);
  });

  it('normalizes health percentage when maxPerCategory is missing', () => {
    const { vm } = setupPage({ health: { preferenceCount: 4, projectCount: 2 } });
    expect(vm.healthPrefPercent.value).toBe(100);
    expect(vm.healthProjPercent.value).toBe(100);
  });

  it('exposes model loading state for the page template', () => {
    const { vm } = setupPage({}, { model: { loading: true } });
    expect(vm.isLoading.value).toBe(true);
  });

  it('opens merge confirmation instead of calling merge API immediately', () => {
    const group = {
      nameA: '私有规则', pathA: 'feedback/private.md', targetA: 'private',
      nameB: '团队规则', pathB: 'feedback/team.md', targetB: 'team', score: 0.91,
    };
    const { vm } = setupPage({ health: { maxPerCategory: 15, preferenceCount: 2, projectCount: 0, similarGroups: [group] } });

    vm.askMergeGroup(group, 0);

    expect(apiMock.callAPI).not.toHaveBeenCalledWith('ui/memory/entry/merge', expect.anything());
    expect(vm.mergeConfirm.target).toEqual(group);
    expect(vm.mergeConfirm.index).toBe(0);
  });

  it('confirms merge with full payload (same-scope)', async () => {
    const group = { nameA: 'A', pathA: 'a.md', targetA: 'private', nameB: 'B', pathB: 'b.md', targetB: 'private', score: 0.8 };
    const { vm, emit } = setupPage({ projectRoot: '/repo', health: { maxPerCategory: 15, preferenceCount: 2, projectCount: 0 } });
    vm.askMergeGroup(group, 2);

    await vm.confirmMergeGroup();

    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/memory/entry/merge', {
      cwd: '/repo',
      targetA: 'private', pathA: 'a.md',
      targetB: 'private', pathB: 'b.md',
    });
    expect(emit).toHaveBeenCalledWith('refresh');
    expect(vm.mergeConfirm.target).toBe(null);
  });

  it('confirms cross-scope merge without warning (cross-scope unlocked)', async () => {
    const group = { nameA: 'P', pathA: 'a.md', targetA: 'private', nameB: 'T', pathB: 'b.md', targetB: 'team', score: 0.8 };
    const { vm, emit } = setupPage({ projectRoot: '/repo', health: { maxPerCategory: 15, preferenceCount: 2, projectCount: 0 } });
    vm.askMergeGroup(group, 0);

    await vm.confirmMergeGroup();

    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/memory/entry/merge', {
      cwd: '/repo',
      targetA: 'private', pathA: 'a.md',
      targetB: 'team', pathB: 'b.md',
    });
    expect(vm.notice.level).toBe('info');
    expect(vm.notice.message).not.toContain('跨作用域');
    expect(emit).toHaveBeenCalledWith('refresh');
  });

  it('routes editor delete through inline confirmation instead of deleting immediately', () => {
    const { vm } = setupPage();
    Object.assign(vm.memoryEditor.form, { target: 'private', existingPath: 'project/a.md', name: 'A' });

    vm.askEditorDelete();

    expect(apiMock.callAPI).not.toHaveBeenCalledWith('ui/memory/entry/delete', expect.anything());
    expect(vm.inlineDelete.target).toEqual({ target: 'private', path: 'project/a.md', name: 'A' });
    expect(vm.memoryEditor.open).toBe(false);
  });


  it('mergeAllGroups calls consolidate-all LLM RPC and surfaces summary', async () => {
    const groups = [
      { nameA: 'A', pathA: 'a.md', targetA: 'private', nameB: 'B', pathB: 'b.md', targetB: 'private', score: 0.9 },
      { nameA: 'C', pathA: 'c.md', targetA: 'private', nameB: 'D', pathB: 'd.md', targetB: 'team', score: 0.7 },
    ];
    const { vm, emit } = setupPage({ projectRoot: '/repo', health: { similarGroups: groups } });
    apiMock.callAPI.mockResolvedValueOnce({ merged: 1, ignored: 1, failed: 0, skipped: 0 });

    await vm.mergeAllGroups();

    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/memory/similarity/consolidate-all', { cwd: '/repo' });
    expect(vm.notice.message).toContain('已整合 1 组');
    expect(vm.notice.message).toContain('1 组判定不应合');
    expect(emit).toHaveBeenCalledWith('refresh');
    expect(vm.mergingAll.value).toBe(false);
  });

  it('mergeAllGroups exposes first error message when partial failure happens', async () => {
    const groups = [
      { nameA: 'A', pathA: 'a.md', targetA: 'private', nameB: 'B', pathB: 'b.md', targetB: 'private', score: 0.9 },
      { nameA: 'C', pathA: 'c.md', targetA: 'private', nameB: 'D', pathB: 'd.md', targetB: 'team', score: 0.7 },
    ];
    const { vm } = setupPage({ projectRoot: '/repo', health: { similarGroups: groups } });
    apiMock.callAPI.mockResolvedValueOnce({
      merged: 0,
      ignored: 0,
      failed: 1,
      skipped: 0,
      errors: ['group 0 merge: invalid keep "X"'],
    });

    await vm.mergeAllGroups();

    expect(vm.notice.level).toBe('warning');
    expect(vm.notice.message).toContain('1 组失败');
    expect(vm.notice.message).toContain('invalid keep');
  });

  it('mergeAllGroups surfaces LLM-not-configured error', async () => {
    const groups = [
      { nameA: 'A', pathA: 'a.md', targetA: 'private', nameB: 'B', pathB: 'b.md', targetB: 'team', score: 0.8 },
      { nameA: 'C', pathA: 'c.md', targetA: 'private', nameB: 'D', pathB: 'd.md', targetB: 'private', score: 0.7 },
    ];
    const { vm, emit } = setupPage({ projectRoot: '/repo', health: { similarGroups: groups } });
    apiMock.callAPI.mockRejectedValueOnce(new Error('dream executor is not configured'));

    await vm.mergeAllGroups();

    expect(vm.notice.level).toBe('error');
    expect(vm.notice.message).toContain('智能整合失败');
    expect(vm.notice.message).toContain('dream executor');
    expect(emit).not.toHaveBeenCalledWith('refresh');
    expect(vm.mergingAll.value).toBe(false);
  });

  it('mergeAllGroups noop when no similar groups present', async () => {
    const { vm } = setupPage({ projectRoot: '/repo', health: { similarGroups: [] } });

    await vm.mergeAllGroups();

    expect(apiMock.callAPI).not.toHaveBeenCalledWith('ui/memory/similarity/consolidate-all', expect.anything());
  });

  it('ignoreGroup calls similarity ignore RPC then emits refresh', async () => {
    const group = { nameA: 'A', pathA: 'feedback/a.md', targetA: 'private', nameB: 'B', pathB: 'feedback/b.md', targetB: 'team', score: 0.8 };
    const { vm, emit } = setupPage({ projectRoot: '/repo', health: { similarGroups: [group] } });

    await vm.ignoreGroup(group);

    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/memory/similarity/ignore', {
      cwd: '/repo',
      targetA: 'private',
      pathA: 'feedback/a.md',
      targetB: 'team',
      pathB: 'feedback/b.md',
    });
    expect(vm.notice.level).toBe('info');
    expect(vm.notice.message).toContain('已忽略');
    expect(emit).toHaveBeenCalledWith('refresh');
    expect(vm.ignoringGroup.value).toBe(null);
  });

  it('ignoreGroup surfaces RPC error as error notice', async () => {
    const group = { nameA: 'A', pathA: 'a.md', targetA: 'private', nameB: 'B', pathB: 'b.md', targetB: 'private', score: 0.8 };
    const { vm, emit } = setupPage({ projectRoot: '/repo', health: { similarGroups: [group] } });
    apiMock.callAPI.mockRejectedValueOnce(new Error('boom'));

    await vm.ignoreGroup(group);

    expect(vm.notice.level).toBe('error');
    expect(vm.notice.message).toContain('boom');
    expect(emit).not.toHaveBeenCalledWith('refresh');
    expect(vm.ignoringGroup.value).toBe(null);
  });

  it('ignoreGroup is reentrancy-safe while another call is in flight', async () => {
    const group = { nameA: 'A', pathA: 'a.md', targetA: 'private', nameB: 'B', pathB: 'b.md', targetB: 'private', score: 0.8 };
    const { vm } = setupPage({ projectRoot: '/repo', health: { similarGroups: [group] } });
    let resolveCall;
    apiMock.callAPI.mockReturnValueOnce(new Promise((r) => { resolveCall = r; }));

    const first = vm.ignoreGroup(group);
    await vm.ignoreGroup(group); // should noop while first is pending

    expect(apiMock.callAPI).toHaveBeenCalledTimes(1);
    resolveCall(null);
    await first;
    expect(vm.ignoringGroup.value).toBe(null);
  });

  it('mergeAllGroups noop when health is null', async () => {
    const { vm } = setupPage({ projectRoot: '/repo' }); // 无 health overview

    await vm.mergeAllGroups();

    expect(apiMock.callAPI).not.toHaveBeenCalledWith('ui/memory/similarity/consolidate-all', expect.anything());
    expect(vm.mergingAll.value).toBe(false);
  });

  it('notice message truncates over-long error to 120 chars with ellipsis', async () => {
    const longErr = 'X'.repeat(200);
    const { vm } = setupPage({ projectRoot: '/repo' });
    vm.askEditorDelete?.(); // noop reset
    // 直接通过现成路径触发：mergeAllGroups 失败时 notice 透出 error.message
    // count>=2：单条相似不再触发 mergeAllGroups（UI 隐藏按钮），测试用真实可触发场景。
    const groups = [
      { nameA: 'A', pathA: 'a.md', targetA: 'private', nameB: 'B', pathB: 'b.md', targetB: 'team', score: 0.8 },
      { nameA: 'C', pathA: 'c.md', targetA: 'private', nameB: 'D', pathB: 'd.md', targetB: 'private', score: 0.7 },
    ];
    const fresh = setupPage({ projectRoot: '/repo', health: { similarGroups: groups } });
    apiMock.callAPI.mockRejectedValueOnce(new Error(longErr));

    await fresh.vm.mergeAllGroups();

    expect(fresh.vm.notice.message.length).toBeLessThanOrEqual(120);
    expect(fresh.vm.notice.message).toMatch(/…$/);
  });

  it('pairKey returns stable string from path and target', () => {
    const { vm } = setupPage();
    const k1 = vm.pairKey({ targetA: 'private', pathA: 'a.md', targetB: 'team', pathB: 'b.md' });
    const k2 = vm.pairKey({ targetA: 'private', pathA: 'a.md', targetB: 'team', pathB: 'b.md' });
    expect(k1).toBe(k2);
    expect(k1).toBe('private:a.md|team:b.md');
  });

  it('mergeAllGroups loading toast survives 5.2s auto-clear timer (persistent=true)', async () => {
    vi.useFakeTimers();
    try {
      const groups = [{ nameA: 'A', pathA: 'a.md', targetA: 'private', nameB: 'B', pathB: 'b.md', targetB: 'private', score: 0.9 }];
      const { vm } = setupPage({ projectRoot: '/repo', health: { similarGroups: groups } });
      let resolveCall;
      apiMock.callAPI.mockReturnValueOnce(new Promise((r) => { resolveCall = r; }));

      const inflight = vm.mergeAllGroups();
      // Inflight: loading toast set with persistent=true; default info timer = 5200ms.
      expect(vm.notice.message).toContain('智能整合中');
      vi.advanceTimersByTime(10000);
      expect(vm.notice.message).toContain('智能整合中'); // 仍然在，没被自清

      resolveCall({ merged: 1 });
      await inflight;
      expect(vm.notice.message).toContain('已整合 1 组');
    } finally {
      vi.useRealTimers();
    }
  });


});
