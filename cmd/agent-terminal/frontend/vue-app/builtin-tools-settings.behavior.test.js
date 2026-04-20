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
  it('loads the registry snapshot and surfaces labels/enable flags', async () => {
    apiMock.callAPI.mockResolvedValueOnce({
      tools: [
        { id: 'Read', label: '读文件', description: '读取文件', enabled: false },
        { id: 'WebFetch', label: '抓取网页', description: '拉取网页', enabled: true },
      ],
    });

    const { vm } = createBuiltinToolsSettings();
    await vm.loadBuiltinTools();

    expect(apiMock.callAPI).toHaveBeenCalledWith('config/builtinTools/read', { cwd: '/repo' });
    expect(vm.tools.value).toEqual([
      { id: 'Read', label: '读文件', description: '读取文件', enabled: false },
      { id: 'WebFetch', label: '抓取网页', description: '拉取网页', enabled: true },
    ]);
  });

  it('toggles a tool and applies the returned snapshot', async () => {
    const { vm } = createBuiltinToolsSettings();
    apiMock.callAPI.mockResolvedValueOnce({
      tools: [
        { id: 'Read', label: '读文件', description: '读取文件', enabled: false },
      ],
    });
    await vm.loadBuiltinTools();
    apiMock.callAPI.mockResolvedValueOnce({
      tools: [
        { id: 'Read', label: '读文件', description: '读取文件', enabled: true },
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
        { id: 'Read', label: '读文件', description: '读取文件', enabled: false },
      ],
    });
    await vm.loadBuiltinTools();
    apiMock.callAPI.mockRejectedValueOnce(new Error('boom'));

    await vm.toggleBuiltinTool(vm.tools.value[0]);

    expect(vm.tools.value[0].enabled).toBe(false);
    expect(vm.notice.level).toBe('error');
    expect(vm.notice.message).toContain('boom');
  });
});
