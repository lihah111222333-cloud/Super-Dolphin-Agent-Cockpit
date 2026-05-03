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
    expect(vm.mergeConfirm.crossScope).toBe(true);
  });

  it('does not merge cross-scope groups from confirmation', async () => {
    const group = { nameA: 'A', pathA: 'a.md', targetA: 'private', nameB: 'B', pathB: 'b.md', targetB: 'team', score: 0.8 };
    const { vm } = setupPage({ health: { maxPerCategory: 15, preferenceCount: 2, projectCount: 0 } });
    vm.askMergeGroup(group, 1);

    await vm.confirmMergeGroup();

    expect(apiMock.callAPI).not.toHaveBeenCalledWith('ui/memory/entry/merge', expect.anything());
    expect(vm.notice.level).toBe('warning');
    expect(vm.notice.message).toContain('跨作用域');
  });

  it('confirms same-scope merge with full payload', async () => {
    const group = { nameA: 'A', pathA: 'a.md', targetA: 'private', nameB: 'B', pathB: 'b.md', targetB: 'private', score: 0.8 };
    const { vm, emit } = setupPage({ projectRoot: '/repo', health: { maxPerCategory: 15, preferenceCount: 2, projectCount: 0 } });
    vm.askMergeGroup(group, 2);

    await vm.confirmMergeGroup();

    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/memory/entry/merge', {
      cwd: '/repo',
      targetA: 'private',
      pathA: 'a.md',
      targetB: 'private',
      pathB: 'b.md',
    });
    expect(emit).toHaveBeenCalledWith('refresh');
    expect(vm.mergeConfirm.target).toBe(null);
  });

  it('routes editor delete through inline confirmation instead of deleting immediately', () => {
    const { vm } = setupPage();
    Object.assign(vm.memoryEditor.form, { target: 'private', existingPath: 'project/a.md', name: 'A' });

    vm.askEditorDelete();

    expect(apiMock.callAPI).not.toHaveBeenCalledWith('ui/memory/entry/delete', expect.anything());
    expect(vm.inlineDelete.target).toEqual({ target: 'private', path: 'project/a.md', name: 'A' });
    expect(vm.memoryEditor.open).toBe(false);
  });

});
