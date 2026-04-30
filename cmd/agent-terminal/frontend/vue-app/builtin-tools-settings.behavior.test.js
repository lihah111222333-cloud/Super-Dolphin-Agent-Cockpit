// @ts-nocheck
import { beforeEach, describe, expect, it, vi } from 'vitest';

const apiMock = vi.hoisted(() => ({
  callAPI: vi.fn(),
}));

vi.mock('./services/api.js', () => ({
  callAPI: apiMock.callAPI,
}));

import { BuiltinToolsSettings } from './pages/settings/BuiltinToolsSettings.ts';

function createBuiltinToolsSettings(overrides = {}) {
  const props = {
    projectStore: overrides.projectStore ?? { state: { active: '/repo' } },
  };
  return { props, vm: BuiltinToolsSettings.setup(props) };
}

beforeEach(() => {
  apiMock.callAPI.mockReset();
});

describe('BuiltinToolsSettings behavior', () => {
  it('loads the registry snapshot and surfaces labels/enable flags + provider', async () => {
    apiMock.callAPI.mockResolvedValueOnce({
      tools: [
        { id: 'Read', label: '读文件', description: '读取文件', enabled: false, provider: 'claude' },
        { id: 'WebFetch', label: '抓取网页', description: '拉取网页', enabled: true, provider: 'claude' },
      ],
    });

    const { vm } = createBuiltinToolsSettings();
    await vm.loadBuiltinTools();

    expect(apiMock.callAPI).toHaveBeenCalledWith('config/builtinTools/read', { cwd: '/repo' });
    expect(vm.tools.value).toEqual([
      { id: 'Read', label: '读文件', description: '读取文件', enabled: false, provider: 'claude' },
      { id: 'WebFetch', label: '抓取网页', description: '拉取网页', enabled: true, provider: 'claude' },
    ]);
  });

  it('toggles a tool and applies the returned snapshot', async () => {
    const { vm } = createBuiltinToolsSettings();
    apiMock.callAPI.mockResolvedValueOnce({
      tools: [
        { id: 'Read', label: '读文件', description: '读取文件', enabled: false, provider: 'claude' },
      ],
    });
    await vm.loadBuiltinTools();
    apiMock.callAPI.mockResolvedValueOnce({
      tools: [
        { id: 'Read', label: '读文件', description: '读取文件', enabled: true, provider: 'claude' },
      ],
    });

    await vm.toggleBuiltinTool(vm.tools.value[0]);

    expect(apiMock.callAPI).toHaveBeenCalledWith('config/builtinTools/write', {
      cwd: '/repo',
      id: 'Read',
      enabled: true,
    });
    expect(vm.tools.value[0].enabled).toBe(true);
    expect(vm.notice.message).toContain('已启用');
  });

  it('rolls back the optimistic toggle when the write fails', async () => {
    const { vm } = createBuiltinToolsSettings();
    apiMock.callAPI.mockResolvedValueOnce({
      tools: [
        { id: 'Read', label: '读文件', description: '读取文件', enabled: false, provider: 'claude' },
      ],
    });
    await vm.loadBuiltinTools();
    apiMock.callAPI.mockRejectedValueOnce(new Error('boom'));

    await vm.toggleBuiltinTool(vm.tools.value[0]);

    expect(vm.tools.value[0].enabled).toBe(false);
    expect(vm.notice.level).toBe('error');
    expect(vm.notice.message).toContain('boom');
  });

  it('groups tools by provider and surfaces info-only providers from providerNotes', async () => {
    apiMock.callAPI.mockResolvedValueOnce({
      tools: [
        { id: 'Read', label: '读文件', description: '读取', enabled: false, provider: 'claude' },
        { id: 'WebFetch', label: '抓取网页', description: '网页', enabled: true, provider: 'claude' },
      ],
      providerNotes: [
        { provider: 'codex', label: 'Codex 内置工具', note: '协议未暴露' },
      ],
    });
    const { vm } = createBuiltinToolsSettings();
    await vm.loadBuiltinTools();

    expect(vm.groups.value).toHaveLength(2);
    const claudeGroup = vm.groups.value.find((g) => g.provider === 'claude');
    expect(claudeGroup.tools).toHaveLength(2);
    expect(claudeGroup.disabledCount).toBe(1);
    expect(claudeGroup.note).toBe('');
    const codexGroup = vm.groups.value.find((g) => g.provider === 'codex');
    expect(codexGroup.tools).toEqual([]);
    expect(codexGroup.label).toBe('Codex 内置工具');
    expect(codexGroup.note).toContain('协议未暴露');
    expect(vm.totalDisabledCount.value).toBe(1);
    expect(vm.totalToolCount.value).toBe(2);
  });

  it('toggles group expand state and defaults to collapsed', async () => {
    apiMock.callAPI.mockResolvedValueOnce({
      tools: [{ id: 'Read', label: '读文件', description: '', enabled: false, provider: 'claude' }],
    });
    const { vm } = createBuiltinToolsSettings();
    await vm.loadBuiltinTools();

    expect(vm.isGroupExpanded('claude')).toBe(false);
    vm.toggleGroupExpanded('claude');
    expect(vm.isGroupExpanded('claude')).toBe(true);
    vm.toggleGroupExpanded('claude');
    expect(vm.isGroupExpanded('claude')).toBe(false);
  });

  it('toggleBuiltinTool sends enabled=false when disabling a currently enabled tool (UI-flip semantics)', async () => {
    apiMock.callAPI.mockResolvedValueOnce({
      tools: [{ id: 'WebFetch', label: '抓取', description: '', enabled: true, provider: 'claude' }],
    });
    const { vm } = createBuiltinToolsSettings();
    await vm.loadBuiltinTools();
    apiMock.callAPI.mockResolvedValueOnce({
      tools: [{ id: 'WebFetch', label: '抓取', description: '', enabled: false, provider: 'claude' }],
    });

    // The user clicks the (now-checked-means-disabled) checkbox on a currently
    // enabled tool. We must send enabled=false to the backend.
    await vm.toggleBuiltinTool(vm.tools.value[0]);

    expect(apiMock.callAPI).toHaveBeenCalledWith('config/builtinTools/write', {
      cwd: '/repo',
      id: 'WebFetch',
      enabled: false,
    });
    expect(vm.tools.value[0].enabled).toBe(false);
    expect(vm.notice.message).toContain('已禁用');
  });
});
