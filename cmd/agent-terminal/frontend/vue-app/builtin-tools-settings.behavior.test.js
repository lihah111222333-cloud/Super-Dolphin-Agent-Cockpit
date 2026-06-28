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
  it('loads the registry snapshot and surfaces labels/enable flags + provider + filterMode', async () => {
    apiMock.callAPI.mockResolvedValueOnce({
      tools: [
        { id: 'Read', label: '读文件', description: '读取文件', enabled: false, provider: 'claude', filterMode: 'hard' },
        { id: 'WebFetch', label: '抓取网页', description: '拉取网页', enabled: true, provider: 'claude', filterMode: 'hard' },
      ],
    });

    const { vm } = createBuiltinToolsSettings();
    await vm.loadBuiltinTools();

    expect(apiMock.callAPI).toHaveBeenCalledWith('config/builtinTools/read', { cwd: '/repo' });
    expect(vm.tools.value).toEqual([
      { id: 'Read', label: '读文件', description: '读取文件', enabled: false, provider: 'claude', filterMode: 'hard' },
      { id: 'WebFetch', label: '抓取网页', description: '拉取网页', enabled: true, provider: 'claude', filterMode: 'hard' },
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

  it('groups tools with user-facing labels and unfiltered tools', async () => {
    apiMock.callAPI.mockResolvedValueOnce({
      tools: [
        { id: 'Read', label: '读文件', description: '读取', enabled: false, provider: 'claude', filterMode: 'hard', enforcement: 'native-hard' },
        { id: 'multi_agent', label: '派生子 Agent', description: 'orchestration', enabled: false, provider: 'codex', filterMode: 'soft', enforcement: 'native-hard' },
        { id: 'apply_patch', label: '应用补丁', description: 'patch', enabled: false, provider: 'codex', filterMode: 'soft', enforcement: 'effect-hard' },
        { id: 'read_file', label: '读文件', description: 'read', enabled: false, provider: 'codex', filterMode: 'soft', enforcement: 'soft-audit' },
        { id: 'WebFetch', label: '抓取网页', description: '网页', enabled: true, provider: 'claude', filterMode: 'hard' },
      ],
    });
    const { vm } = createBuiltinToolsSettings();
    await vm.loadBuiltinTools();

    expect(vm.groups.value.map((g) => g.key)).toEqual(['native-hard', 'effect-hard', 'soft-audit', 'unfiltered']);
    const hardGroup = vm.groups.value.find((g) => g.key === 'native-hard');
    expect(hardGroup.label).toBe('启动前已关闭（2）');
    expect(vm.groupSummary(hardGroup)).toBe('已管控 2 项');
    expect(hardGroup.tools.map((tool) => tool.id)).toEqual(['Read', 'multi_agent']);
    const effectGroup = vm.groups.value.find((g) => g.key === 'effect-hard');
    expect(effectGroup.label).toBe('已限制为只读（1）');
    expect(effectGroup.tools.map((tool) => tool.id)).toEqual(['apply_patch']);
    const softGroup = vm.groups.value.find((g) => g.key === 'soft-audit');
    expect(softGroup.label).toBe('仅提醒使用项目工具（1）');
    expect(softGroup.tools.map((tool) => tool.id)).toEqual(['read_file']);
    const unfilteredGroup = vm.groups.value.find((g) => g.key === 'unfiltered');
    expect(unfilteredGroup.label).toBe('保持可用（1）');
    expect(vm.groupSummary(unfilteredGroup)).toBe('可用 1 项');
    expect(unfilteredGroup.tools).toHaveLength(1);
    expect(vm.filteredCount.value).toBe(4);
    expect(vm.totalToolCount.value).toBe(5);
  });

  it('formats tool rows without exposing internal ids or enforcement names', async () => {
    apiMock.callAPI.mockResolvedValueOnce({
      tools: [
        { id: 'multi_agent', label: '启动子任务', description: '让 Codex 自己创建和管理子任务；本项目已有任务编排。', enabled: false, provider: 'codex', filterMode: 'soft', enforcement: 'native-hard' },
        { id: 'apply_patch', label: '直接改文件', description: '绕过项目文件编辑链路直接修改文件。', enabled: false, provider: 'codex', filterMode: 'soft', enforcement: 'effect-hard' },
        { id: 'read_file', label: '直接读文件', description: '绕过项目文件工具直接读取文件。', enabled: false, provider: 'codex', filterMode: 'soft', enforcement: 'soft-audit' },
        { id: 'web_search', label: '网页搜索', description: '让模型自行搜索网页。', enabled: true, provider: 'codex', filterMode: 'soft' },
      ],
    });

    const { vm } = createBuiltinToolsSettings();
    await vm.loadBuiltinTools();

    const details = vm.tools.value.map((tool) => vm.toolMetaText(tool));
    expect(details[0]).toContain('启动前已关闭');
    expect(details[1]).toContain('已限制为只读');
    expect(details[2]).toContain('仅提醒使用项目工具');
    expect(details[3]).toContain('保持可用');
    expect(details.join('\n')).not.toMatch(/native-hard|effect-hard|soft-audit|multi_agent|apply_patch|read_file|web_search/);
  });

  it('formats replaced tools without exposing internal skill names', async () => {
    apiMock.callAPI.mockResolvedValueOnce({
      tools: [
        { id: 'Read', label: '直接读项目文件', description: '读取文件', enabled: false, provider: 'claude', filterMode: 'hard', enforcement: 'native-hard', replacedBy: 'skill_internal_file_reader' },
      ],
    });

    const { vm } = createBuiltinToolsSettings();
    await vm.loadBuiltinTools();

    const detail = vm.toolMetaText(vm.tools.value[0]);
    expect(detail).toContain('已由项目工具接管');
    expect(detail).not.toContain('skill_internal_file_reader');
  });

  it('keeps the rendered template on user-facing copy instead of internal policy tags', () => {
    expect(BuiltinToolsSettings.template).toContain('内置能力开关');
    expect(BuiltinToolsSettings.template).toContain('已管控');
    expect(BuiltinToolsSettings.template).toContain('toolMetaText(tool)');
    expect(BuiltinToolsSettings.template).not.toContain('tool.id }}');
    expect(BuiltinToolsSettings.template).not.toContain('tool.enforcement');
    expect(BuiltinToolsSettings.template).not.toContain('tool.filterMode');
  });

  it('toggles group expand state and defaults to collapsed', async () => {
    apiMock.callAPI.mockResolvedValueOnce({
      tools: [{ id: 'Read', label: '读文件', description: '', enabled: false, provider: 'claude' }],
    });
    const { vm } = createBuiltinToolsSettings();
    await vm.loadBuiltinTools();

    expect(vm.isGroupExpanded('native-hard')).toBe(false);
    vm.toggleGroupExpanded('native-hard');
    expect(vm.isGroupExpanded('native-hard')).toBe(true);
    vm.toggleGroupExpanded('native-hard');
    expect(vm.isGroupExpanded('native-hard')).toBe(false);
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
